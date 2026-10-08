// Package core implements every operation in docs/openapi.yaml. Server
// satisfies the generated gen.ServerInterface; each handler runs inside
// serve(), which gives it the request pipeline every endpoint shares:
// authentication, rate limiting, the destructive-operation gate,
// idempotency, one database transaction, and response buffering so the
// response is only written after that transaction commits.
package core

import (
	"context"
	"crypto/rsa"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/CompositeCode/substratalapps.com/internal/api/gen"
	"github.com/CompositeCode/substratalapps.com/internal/auth"
	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/email"
	"github.com/CompositeCode/substratalapps.com/internal/httpx"
	"github.com/CompositeCode/substratalapps.com/internal/ids"
)

// Notifier is told that a webhook outbox event committed, so delivery can
// start immediately instead of waiting for the next scheduled sweep (SQS
// in AWS, an in-process channel locally).
type Notifier interface {
	EventCommitted(ctx context.Context, eventID string)
}

// Billing is the Stripe-facing surface the billing endpoints need.
type Billing interface {
	EnsureCustomer(ctx context.Context, tenantID, email string) (customerID string, err error)
	CheckoutSession(ctx context.Context, in CheckoutInput) (url string, expires time.Time, err error)
	PortalSession(ctx context.Context, customerID, returnURL string) (url string, expires time.Time, err error)
}

// CheckoutInput is a Team upgrade request.
type CheckoutInput struct {
	TenantID   string
	CustomerID string
	Seats      int
	SuccessURL string
	CancelURL  string
}

// Deps are everything Server needs from the outside world.
type Deps struct {
	DB        db.DB
	Signer    auth.Signer
	Encrypter auth.Encrypter
	Email     email.Sender
	Notifier  Notifier
	Billing   Billing // nil disables checkout/portal (503 billing_unavailable)
	Cursors   httpx.Cursors
	Log       *slog.Logger

	Issuer        string // JWT iss
	PublicBaseURL string
	AuthorizeURL  string
	DashboardURL  string
	CORSOrigins   []string
	Now           func() time.Time
	// DisableRateLimits turns the NFR rate limits off. Integration tests
	// only, where every request comes from 127.0.0.1.
	DisableRateLimits bool
}

// Server implements gen.ServerInterface.
type Server struct {
	Deps
	keys map[string]*rsa.PublicKey
}

var _ gen.ServerInterface = (*Server)(nil)

// New builds a Server.
func New(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Notifier == nil {
		d.Notifier = noopNotifier{}
	}
	return &Server{Deps: d, keys: d.Signer.PublicKeys()}
}

type noopNotifier struct{}

func (noopNotifier) EventCommitted(context.Context, string) {}

// Handler returns the full HTTP surface: /v1 (generated routes), the
// well-known endpoints at the host root, and a JSON 404 for everything
// else. /mcp and /internal/stripe/webhook are separate Lambdas and mounted
// by the devserver alongside this handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	gen.HandlerWithOptions(s, gen.StdHTTPServerOptions{
		BaseURL:          "/v1",
		BaseRouter:       mux,
		ErrorHandlerFunc: s.paramError,
	})
	mux.HandleFunc("GET /.well-known/jwks.json", s.jwks)
	mux.HandleFunc("GET /.well-known/openid-configuration", s.openIDConfiguration)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, httpx.ErrNotFound())
	})
	return s.middleware(mux)
}

// middleware wraps every request: request id, panic recovery, CORS,
// security headers.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" || len(rid) > 128 {
			rid = ids.New("req_")
		}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, rid))
		w.Header().Set("X-Request-Id", rid)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if s.cors(w, r) {
			return
		}
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "request_id", rid, "panic", v, "stack", string(debug.Stack()))
				httpx.WriteError(w, httpx.ErrInternal())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors allows the configured first-party origins on browser-facing GETs
// (NFR → CORS). It returns true when it fully handled a preflight.
func (s *Server) cors(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	allowed := false
	for _, o := range s.CORSOrigins {
		if o == origin {
			allowed = true
		}
	}
	if !allowed {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers", "ETag, X-Request-Id, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, PATCH, POST, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Idempotency-Key, X-Request-Id")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

type requestIDKey struct{}

// RequestID returns the request's X-Request-Id.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// paramError maps generated parameter-binding failures onto the spec's
// error codes.
func (s *Server) paramError(w http.ResponseWriter, r *http.Request, err error) {
	var rh *gen.RequiredHeaderError
	if errors.As(err, &rh) && strings.EqualFold(rh.ParamName, "Idempotency-Key") {
		httpx.WriteError(w, httpx.ErrIdempotencyRequired())
		return
	}
	var ip *gen.InvalidParamFormatError
	if errors.As(err, &ip) {
		var f httpx.Fields
		f.Add(ip.ParamName, "invalid_format")
		httpx.WriteError(w, f.Err())
		return
	}
	httpx.WriteError(w, httpx.Invalid("%s", err.Error()))
}

// HealthCheck is GET /v1/health: no auth, no database, always 200.
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httpx.JSON(w, http.StatusOK, auth.JWKS(s.keys))
}

func (s *Server) openIDConfiguration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	httpx.JSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.Issuer,
		"jwks_uri":                              s.Issuer + "/.well-known/jwks.json",
		"authorization_endpoint":                s.AuthorizeURL,
		"token_endpoint":                        s.Issuer + "/v1/auth/oauth/token",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}
