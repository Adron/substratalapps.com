// Package testenv runs the real API against a real, freshly migrated
// Postgres for integration and contract tests (NFR → Testing strategy:
// "not a mocked data layer"). Every response a test sees is validated
// against docs/openapi.yaml, so the spec can't silently drift from the
// implementation.
//
// Each test package gets its own database, created from TEST_DATABASE_URL
// (default: the docker-compose Postgres) and dropped afterwards, so
// packages run in parallel without sharing state.
package testenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	validatorerrors "github.com/pb33f/libopenapi-validator/errors"

	"github.com/CompositeCode/substratalapps.com/internal/auth"
	"github.com/CompositeCode/substratalapps.com/internal/core"
	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/db/migrate"
	"github.com/CompositeCode/substratalapps.com/internal/db/pg"
	"github.com/CompositeCode/substratalapps.com/internal/email"
	"github.com/CompositeCode/substratalapps.com/internal/httpx"
	"github.com/CompositeCode/substratalapps.com/internal/webhooks"
	"github.com/CompositeCode/substratalapps.com/migrations"
)

// AdminPassword is the bootstrap superadmin's password in tests.
const AdminPassword = "test-superadmin-passphrase-1"

// Env is one running API.
type Env struct {
	T         testing.TB
	Server    *httptest.Server
	Core      *core.Server
	DB        db.DB
	Mail      *email.Capture
	Deliverer *webhooks.Deliverer
	Signer    *auth.LocalSigner
	Admin     string // superadmin access token
	AdminID   string
	Validate  bool

	validator validator.Validator
}

var (
	pkgOnce sync.Once
	pkgDB   string
	pkgErr  error
)

func baseURL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	return "postgres://substratal:substratal@localhost:55432/postgres?sslmode=disable"
}

func withDB(u, name string) string {
	p, _ := url.Parse(u)
	p.Path = "/" + name
	return p.String()
}

// database creates and migrates this package's database, once.
func database(t testing.TB) string {
	pkgOnce.Do(func() {
		ctx := context.Background()
		admin, err := pgx.Connect(ctx, baseURL())
		if err != nil {
			pkgErr = fmt.Errorf("testenv: connect to %s (is `make up` running?): %w", baseURL(), err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		name := "substratal_test_" + hex.EncodeToString(b)
		if _, err := admin.Exec(ctx, "create database "+name); err != nil {
			pkgErr = err
			return
		}
		pkgDB = withDB(baseURL(), name)
		d, err := pg.Open(ctx, pkgDB)
		if err != nil {
			pkgErr = err
			return
		}
		defer d.Close()
		all, err := migrate.Load(migrations.FS)
		if err != nil {
			pkgErr = err
			return
		}
		if _, err := migrate.Up(ctx, d, all); err != nil {
			pkgErr = err
		}
	})
	if pkgErr != nil {
		t.Fatalf("%v", pkgErr)
	}
	return pkgDB
}

// Drop removes this package's database; call it from TestMain.
func Drop() {
	if pkgDB == "" {
		return
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, baseURL())
	if err != nil {
		return
	}
	defer func() { _ = admin.Close(ctx) }()
	p, _ := url.Parse(pkgDB)
	_, _ = admin.Exec(ctx, "drop database if exists "+strings.TrimPrefix(p.Path, "/")+" with (force)")
}

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
	specMu  sync.Mutex
	specDoc libopenapi.Document
)

func signer() *auth.LocalSigner {
	keyOnce.Do(func() { testKey, _ = rsa.GenerateKey(rand.Reader, 2048) })
	return auth.NewLocalSignerFromKey(testKey)
}

func specPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "docs", "openapi.yaml")
}

func specValidator(t testing.TB) validator.Validator {
	specMu.Lock()
	defer specMu.Unlock()
	if specDoc == nil {
		b, err := os.ReadFile(specPath())
		if err != nil {
			t.Fatalf("read spec: %v", err)
		}
		doc, err := libopenapi.NewDocument(b)
		if err != nil {
			t.Fatalf("parse spec: %v", err)
		}
		specDoc = doc
	}
	v, errs := validator.NewValidator(specDoc)
	if len(errs) > 0 {
		t.Fatalf("build validator: %v", errs)
	}
	return v
}

// Options tune New.
type Options struct {
	RateLimits bool // run with the production rate limits on
}

// New starts an API for one test, with a fresh superadmin.
func New(t testing.TB) *Env { return NewWith(t, Options{}) }

// NewWith is New with options.
func NewWith(t testing.TB, opts Options) *Env {
	t.Helper()
	ctx := context.Background()
	d, err := pg.Open(ctx, database(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	enc, _ := auth.NewLocalEncrypter("")
	mail := &email.Capture{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	core.AllowPrivateWebhookTargets = true
	deliverer := webhooks.New(d, enc, mail, log)
	deliverer.AllowPrivate = true
	s := core.New(core.Deps{DB: d, Signer: signer(), Encrypter: enc, Email: mail,
		Cursors: httpx.Cursors{Key: []byte("test-cursor-key")}, Log: log,
		Issuer: "https://api.substratalapps.com", PublicBaseURL: "http://localhost", AuthorizeURL: "https://auth.substratalapps.com/authorize",
		DashboardURL: "https://dashboard.test", DisableRateLimits: !opts.RateLimits})
	srv := httptest.NewServer(s.Handler())
	e := &Env{T: t, Server: srv, Core: s, DB: d, Mail: mail, Deliverer: deliverer, Signer: signer(), Validate: true,
		validator: specValidator(t)}
	t.Cleanup(func() { srv.Close(); d.Close() })
	adminEmail := "admin+" + Unique() + "@example.com"
	id, err := s.BootstrapSuperadmin(ctx, adminEmail, AdminPassword)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	e.AdminID = id
	e.Admin = e.Login(adminEmail, AdminPassword).Token()
	return e
}

// Unique returns a short random suffix for emails, slugs, and names.
func Unique() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Resp is one HTTP response.
type Resp struct {
	T      testing.TB
	Status int
	Body   []byte
	Header http.Header
}

// JSON decodes the body as a generic map.
func (r *Resp) JSON() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		r.T.Fatalf("body isn't a JSON object (%d): %s", r.Status, r.Body)
	}
	return m
}

// Decode decodes the body into dst.
func (r *Resp) Decode(dst any) {
	if err := json.Unmarshal(r.Body, dst); err != nil {
		r.T.Fatalf("decode (%d): %v: %s", r.Status, err, r.Body)
	}
}

// Str returns a top-level string field.
func (r *Resp) Str(key string) string {
	v, _ := r.JSON()[key].(string)
	return v
}

// Token returns access_token from an AuthSession.
func (r *Resp) Token() string { return r.Str("access_token") }

// Code returns error.code, or "".
func (r *Resp) Code() string {
	m := map[string]any{}
	_ = json.Unmarshal(r.Body, &m)
	if e, ok := m["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

// Expect fails the test unless the status matches.
func (r *Resp) Expect(status int) *Resp {
	r.T.Helper()
	if r.Status != status {
		r.T.Fatalf("status = %d, want %d: %s", r.Status, status, r.Body)
	}
	return r
}

// ExpectErr fails unless the status and error code match.
func (r *Resp) ExpectErr(status int, code string) *Resp {
	r.T.Helper()
	if r.Status != status || r.Code() != code {
		r.T.Fatalf("got %d %q, want %d %q: %s", r.Status, r.Code(), status, code, r.Body)
	}
	return r
}

// Req is a request builder.
type Req struct {
	Method, Path, Token string
	Body                any
	Header              map[string]string
}

// Do sends a request and validates the response against the spec.
func (e *Env) Do(rq Req) *Resp {
	e.T.Helper()
	var body io.Reader
	var raw []byte
	if rq.Body != nil {
		switch b := rq.Body.(type) {
		case string:
			raw = []byte(b)
		default:
			raw, _ = json.Marshal(b)
		}
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(rq.Method, e.Server.URL+rq.Path, body)
	if rq.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if rq.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rq.Token)
	}
	for k, v := range rq.Header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatalf("%s %s: %v", rq.Method, rq.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	r := &Resp{T: e.T, Status: resp.StatusCode, Body: b, Header: resp.Header}
	if e.Validate && strings.HasPrefix(rq.Path, "/v1/") {
		e.contract(rq, raw, resp, b)
	}
	return r
}

// contract validates the response body against docs/openapi.yaml.
func (e *Env) contract(rq Req, raw []byte, resp *http.Response, body []byte) {
	e.T.Helper()
	specReq, _ := http.NewRequest(rq.Method, "https://api.substratalapps.com"+rq.Path, bytes.NewReader(raw))
	specReq.Header.Set("Content-Type", "application/json")
	if rq.Token != "" {
		specReq.Header.Set("Authorization", "Bearer "+rq.Token)
	}
	for k, v := range rq.Header {
		specReq.Header.Set(k, v)
	}
	copyResp := &http.Response{StatusCode: resp.StatusCode, Header: resp.Header.Clone(),
		Body: io.NopCloser(bytes.NewReader(body)), Request: specReq}
	if len(body) == 0 {
		copyResp.Header.Del("Content-Type")
	}
	ok, errs := e.validator.ValidateHttpResponse(specReq, copyResp)
	if ok {
		return
	}
	// Cross-cutting errors (Conventions → Errors: unauthenticated,
	// rate_limited, version_conflict, …) apply to every operation and are
	// defined once there, so an operation needn't list each status. Accept
	// an undeclared 4xx/5xx when its body is the standard error envelope.
	if resp.StatusCode >= 400 && undeclaredStatus(errs) {
		var env struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &env) == nil && env.Error != nil && env.Error.Code != "" && env.Error.Message != "" {
			return
		}
	}
	var msgs []string
	for _, ve := range errs {
		m := ve.Message
		if ve.Reason != "" {
			m += ": " + ve.Reason
		}
		for _, se := range ve.SchemaValidationErrors {
			m += " [" + se.FieldPath + ": " + se.Reason + "]"
		}
		msgs = append(msgs, m)
	}
	e.T.Errorf("contract: %s %s → %d doesn't match docs/openapi.yaml:\n  %s\n  body: %s",
		rq.Method, rq.Path, resp.StatusCode, strings.Join(msgs, "\n  "), truncate(string(body), 600))
}

func undeclaredStatus(errs []*validatorErrors) bool {
	for _, e := range errs {
		if !strings.Contains(e.Message, "does not exist") && !strings.Contains(e.Reason, "has not been defined") {
			return false
		}
	}
	return len(errs) > 0
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Shorthands.
func (e *Env) Get(path, token string) *Resp {
	return e.Do(Req{Method: "GET", Path: path, Token: token})
}
func (e *Env) Post(path, token string, body any) *Resp {
	return e.Do(Req{Method: "POST", Path: path, Token: token, Body: body})
}
func (e *Env) Patch(path, token string, body any) *Resp {
	return e.Do(Req{Method: "PATCH", Path: path, Token: token, Body: body})
}
func (e *Env) Delete(path, token string) *Resp {
	return e.Do(Req{Method: "DELETE", Path: path, Token: token})
}

// PostIdem sends a POST with an Idempotency-Key.
func (e *Env) PostIdem(path, token, key string, body any) *Resp {
	return e.Do(Req{Method: "POST", Path: path, Token: token, Body: body, Header: map[string]string{"Idempotency-Key": key}})
}

// Login signs in and returns the AuthSession response.
func (e *Env) Login(emailAddr, password string) *Resp {
	return e.Post("/v1/auth/login", "", map[string]any{"email": emailAddr, "password": password}).Expect(200)
}

// User is a signed-up test user.
type User struct {
	ID, Email, Password, Token, Refresh string
}

// Signup creates an active user through the public API and signs them in.
func (e *Env) Signup() User {
	e.T.Helper()
	u := User{Email: "user+" + Unique() + "@example.com", Password: "a long enough test passphrase"}
	r := e.Post("/v1/auth/signup", "", map[string]any{"email": u.Email, "password": u.Password}).Expect(201)
	u.ID, u.Token, u.Refresh = r.Str("user_id"), r.Token(), r.Str("refresh_token")
	return u
}

// App creates an Application owned by ownerID (or the system account).
func (e *Env) App(ownerID string, extra map[string]any) string {
	e.T.Helper()
	slug := "app-" + Unique()
	body := map[string]any{"slug": slug, "name": "Test " + slug, "launch_url": "https://" + slug + ".example.com/launch",
		"owner_user_id": ownerID, "available_app_roles": []string{"admin", "member"}, "default_app_role": "member",
		"permissions": []map[string]any{{"key": "app." + slug + ".view"}, {"key": "app." + slug + ".export"}}}
	for k, v := range extra {
		body[k] = v
	}
	r := e.Post("/v1/applications", e.Admin, body).Expect(201)
	return r.Str("id")
}

// Grant gives userID a personal admin_grant Entitlement to appID.
func (e *Env) Grant(token, userID, appID string) string {
	e.T.Helper()
	return e.PostIdem("/v1/users/"+userID+"/entitlements", token, Unique(),
		map[string]any{"application_id": appID, "source": "admin_grant"}).Expect(201).Str("id")
}

// Deliver runs the webhook pipeline to completion.
func (e *Env) Deliver() {
	e.T.Helper()
	if err := e.Deliverer.Run(context.Background()); err != nil {
		e.T.Fatalf("deliver: %v", err)
	}
}

// Events lists outbox events of a type, newest first.
func (e *Env) Events(eventType string) []map[string]any {
	e.T.Helper()
	var out []map[string]any
	err := e.DB.Tx(context.Background(), db.Settings{AllModes: true}, func(q db.Querier) error {
		rows, err := db.All[map[string]any](context.Background(), q, `select jsonb_build_object('id', id, 'type', type,
			'application_id', application_id, 'payload', payload, 'test_mode', test_mode) from webhook_events
			where type = :t order by created_at desc, id desc`, db.Args{"t": eventType})
		out = rows
		return err
	})
	if err != nil {
		e.T.Fatalf("events: %v", err)
	}
	return out
}

// Token returns the most recent emailed token of a prefix sent to addr.
func (e *Env) Token(addr, kind string) string {
	e.T.Helper()
	m, ok := e.Mail.Last(addr, kind)
	if !ok {
		e.T.Fatalf("no %s email to %s", kind, addr)
	}
	i := strings.Index(m.Text, "token=")
	if i < 0 {
		e.T.Fatalf("no token in email: %s", m.Text)
	}
	tok := m.Text[i+6:]
	if j := strings.IndexAny(tok, "\n "); j >= 0 {
		tok = tok[:j]
	}
	v, _ := url.QueryUnescape(tok)
	return v
}

// Wait polls cond for up to d.
func Wait(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

type validatorErrors = validatorerrors.ValidationError
