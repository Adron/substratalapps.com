package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
)

// opts describe one operation's place in the pipeline.
type opts struct {
	op       string   // operationId from docs/openapi.yaml
	public   bool     // no bearer credential (auth endpoints, health)
	userOnly bool     // a User token is required; API Keys get 400 user_token_required
	idem     idemMode // Idempotency-Key handling
}

type idemMode int

const (
	idemNone idemMode = iota
	idemOptional
	idemRequired
)

// Call is one in-flight request: its principal, its transaction, and the
// response it will write once that transaction commits.
type Call struct {
	s   *Server
	ctx context.Context
	r   *http.Request
	op  opts
	q   db.Querier
	p   *Principal
	now time.Time

	raw []byte // request body, read once

	status  int
	body    any
	etag    int
	headers http.Header

	commitOnError bool
	afterCommit   []func(context.Context)
	ip            string
	requestID     string
	// sysTestMode is the mode a system (job) call runs in; there's no
	// principal to read it from.
	sysTestMode bool
}

// serve runs fn as one operation. Every handler is a thin call to serve.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, o opts, fn func(c *Call) error) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	c := &Call{
		s: s, ctx: ctx, r: r, op: o, now: s.Now().UTC(),
		headers: http.Header{}, ip: clientIP(r), requestID: RequestID(r.Context()),
	}
	raw, err := httpx.ReadBody(r)
	if err != nil {
		s.writeErr(w, c, err)
		return
	}
	c.raw = raw
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if o.public {
		if err := c.rateLimitPublic(); err != nil {
			s.writeErr(w, c, err)
			return
		}
	}

	var fnErr error
	txErr := s.DB.Tx(ctx, db.Settings{AllModes: !o.public}, func(q db.Querier) error {
		c.q = q
		if !o.public {
			// The transaction opens in "any" mode only long enough to
			// resolve the credential by its hash; it's pinned to the
			// principal's own mode before any other statement runs.
			p, err := s.authenticate(c)
			if err != nil {
				fnErr = err
				return err
			}
			c.p = p
			if err := c.setMode(p.TestMode); err != nil {
				return err
			}
			if o.userOnly && !p.IsUser() {
				fnErr = httpx.ErrUserTokenRequired()
				return fnErr
			}
			if err := c.rateLimitPrincipal(); err != nil {
				fnErr = err
				return err
			}
			if err := c.destructiveAlways(); err != nil {
				fnErr = err
				return err
			}
		}
		replayed, err := c.idempotencyBegin()
		if err != nil {
			fnErr = err
			return err
		}
		if replayed {
			return nil
		}
		if err := fn(c); err != nil {
			fnErr = err
			if c.commitOnError {
				return nil
			}
			return err
		}
		return c.idempotencyFinish()
	})
	if fnErr != nil {
		if txErr != nil && !errors.Is(txErr, fnErr) && !isAPIError(fnErr) {
			s.Log.Error("transaction failed", "op", o.op, "request_id", c.requestID, "err", txErr)
		}
		// A handler that committed despite its error (lockout counters, a
		// re-sent invitation) still gets its post-commit effects.
		if c.commitOnError && txErr == nil {
			for _, f := range c.afterCommit {
				f(context.WithoutCancel(ctx))
			}
		}
		s.writeErr(w, c, fnErr)
		return
	}
	if txErr != nil {
		if ce, ok := uniqueOrCheck(txErr); ok {
			s.writeErr(w, c, ce)
			return
		}
		s.Log.Error("transaction failed", "op", o.op, "request_id", c.requestID, "err", txErr)
		s.writeErr(w, c, httpx.ErrInternal())
		return
	}
	for _, f := range c.afterCommit {
		f(context.WithoutCancel(ctx))
	}
	c.write(w)
}

func isAPIError(err error) bool {
	var e *httpx.Error
	return errors.As(err, &e)
}

// uniqueOrCheck turns a constraint failure that escaped a handler into a
// 409/422 rather than a 500. Handlers check the cases the spec names
// explicitly; this is the backstop for races between the check and insert.
func uniqueOrCheck(err error) (*httpx.Error, bool) {
	if name, ok := db.UniqueViolation(err); ok {
		return httpx.Conflict("conflict", "The request conflicts with an existing record.").With("constraint", name), true
	}
	return nil, false
}

func (s *Server) writeErr(w http.ResponseWriter, c *Call, err error) {
	var e *httpx.Error
	if !errors.As(err, &e) {
		if ce, ok := uniqueOrCheck(err); ok {
			e = ce
		} else {
			s.Log.Error("request failed", "op", c.op.op, "request_id", c.requestID, "err", err)
			e = httpx.ErrInternal()
		}
	}
	if e.Status == 401 || e.Status == 403 || e.Status == 429 {
		s.securityLog(c, e)
	}
	for k, v := range c.headers {
		w.Header()[k] = v
	}
	httpx.WriteError(w, e)
}

// securityLog is NFR → Security logging: structured, never a secret.
func (s *Server) securityLog(c *Call, e *httpx.Error) {
	actor := ""
	if c.p != nil {
		actor = c.p.ID
	}
	s.Log.Warn("security", "event", "denied", "op", c.op.op, "code", e.Code, "status", e.Status,
		"request_id", c.requestID, "actor", actor, "ip", c.ip, "user_agent", c.r.UserAgent())
}

func (c *Call) write(w http.ResponseWriter) {
	for k, v := range c.headers {
		w.Header()[k] = v
	}
	if c.etag > 0 {
		w.Header().Set("ETag", httpx.ETag(c.etag))
	}
	switch {
	case c.status == http.StatusNoContent:
		w.WriteHeader(http.StatusNoContent)
	case c.status == http.StatusAccepted && c.body == nil:
		w.WriteHeader(http.StatusAccepted)
	default:
		if c.status == 0 {
			c.status = http.StatusOK
		}
		httpx.JSON(w, c.status, c.body)
	}
}

// ── Response helpers ───────────────────────────────────────────────────

// OK queues a 200 with body.
func (c *Call) OK(body any) error { c.status, c.body = http.StatusOK, body; return nil }

// Created queues a 201 with body.
func (c *Call) Created(body any) error { c.status, c.body = http.StatusCreated, body; return nil }

// Accepted queues a 202 (body may be nil).
func (c *Call) Accepted(body any) error { c.status, c.body = http.StatusAccepted, body; return nil }

// NoContent queues a 204.
func (c *Call) NoContent() error { c.status = http.StatusNoContent; return nil }

// Versioned sets the response ETag from a row version.
func (c *Call) Versioned(version int) *Call { c.etag = version; return c }

// After runs f once the transaction has committed (emails, queue nudges).
func (c *Call) After(f func(ctx context.Context)) { c.afterCommit = append(c.afterCommit, f) }

// CommitAnyway makes the transaction commit even though fn returns an
// error: failed-login counters and token invalidation must persist.
func (c *Call) CommitAnyway() { c.commitOnError = true }

// Decode decodes the JSON body into dst.
func (c *Call) Decode(dst any) error { return httpx.DecodeBytes(c.raw, dst) }

// Patch decodes the body preserving field presence.
func (c *Call) Patch() (httpx.Patch, error) {
	if len(bytes.TrimSpace(c.raw)) == 0 {
		return httpx.Patch{}, nil
	}
	var p httpx.Patch
	if err := json.Unmarshal(c.raw, &p); err != nil {
		return nil, httpx.Invalid("The request body isn't a JSON object.")
	}
	return p, nil
}

// setMode pins the transaction's test/live mode (RLS). With no principal
// (public auth endpoints, jobs) it's also the mode new rows are written in.
func (c *Call) setMode(testMode bool) error {
	if c.p == nil {
		c.sysTestMode = testMode
	}
	_, err := c.q.Query(c.ctx, `select to_jsonb(set_config('app.current_test_mode', :tm, true))`,
		db.Args{"tm": db.Settings{TestMode: testMode}.ModeSetting()})
	return err
}

// testMode is the mode the request operates in.
func (c *Call) testMode() bool {
	if c.p != nil {
		return c.p.TestMode
	}
	return c.sysTestMode
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" || host == "127.0.0.1" || host == "::1" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			host = strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ── Idempotency (Conventions → Idempotency) ────────────────────────────

func (c *Call) callerID() string {
	if c.p != nil {
		return c.p.ID
	}
	return "ip:" + c.ip
}

// idempotencyBegin claims (caller, key) in this transaction. A concurrent
// request with the same key blocks on the row lock until this one commits,
// then sees the completed row and replays it.
func (c *Call) idempotencyBegin() (replayed bool, err error) {
	key := c.r.Header.Get("Idempotency-Key")
	if c.op.idem == idemNone || key == "" {
		if c.op.idem == idemRequired {
			return false, httpx.ErrIdempotencyRequired()
		}
		return false, nil
	}
	if len(key) > 255 {
		var f httpx.Fields
		f.Max("Idempotency-Key", "too_long", 255)
		return false, f.Err()
	}
	hash := sha256Hex(append([]byte(c.op.op+"\n"+c.r.URL.Path+"\n"), c.raw...))
	n, err := c.q.Exec(c.ctx, `insert into idempotency_keys (caller_id, idempotency_key, state, request_body_hash, expires_at)
		values (:caller, :key, 'in_flight', :hash, :exp::timestamptz)
		on conflict (caller_id, idempotency_key) do update
		  set state = 'in_flight', request_body_hash = excluded.request_body_hash,
		      response_status = null, response_body = null, expires_at = excluded.expires_at
		  where idempotency_keys.expires_at < now()`,
		db.Args{"caller": c.callerID(), "key": key, "hash": hash, "exp": db.Time(c.now.Add(24 * time.Hour))})
	if err != nil {
		return false, err
	}
	if n == 1 {
		return false, nil
	}
	var prev struct {
		State  string          `json:"state"`
		Hash   string          `json:"request_body_hash"`
		Status int             `json:"response_status"`
		Body   json.RawMessage `json:"response_body"`
	}
	if err := db.One(c.ctx, c.q, &prev, `select to_jsonb(k) from idempotency_keys k
		where caller_id = :caller and idempotency_key = :key`, db.Args{"caller": c.callerID(), "key": key}); err != nil {
		return false, err
	}
	switch {
	case prev.Hash != hash:
		return false, httpx.Conflict("idempotency_key_reused", "This Idempotency-Key was already used with a different request body.")
	case prev.State != "completed":
		return false, httpx.Conflict("idempotency_key_in_flight", "The first request with this Idempotency-Key is still being processed. Retry after a second.").RetryAfter(1)
	}
	c.headers.Set("Idempotent-Replayed", "true")
	c.status = prev.Status
	if len(prev.Body) > 0 && string(prev.Body) != "null" {
		c.body = prev.Body
	}
	return true, nil
}

func (c *Call) idempotencyFinish() error {
	key := c.r.Header.Get("Idempotency-Key")
	if c.op.idem == idemNone || key == "" {
		return nil
	}
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	var body any
	if c.body != nil {
		b, err := json.Marshal(c.body)
		if err != nil {
			return err
		}
		body = string(b)
	}
	_, err := c.q.Exec(c.ctx, `update idempotency_keys set state = 'completed', response_status = :st, response_body = :body::jsonb
		where caller_id = :caller and idempotency_key = :key`,
		db.Args{"st": int64(status), "body": body, "caller": c.callerID(), "key": key})
	return err
}

// errf is fmt.Errorf, for internal (500) errors with context.
func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }
