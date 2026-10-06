package core

import (
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/auth"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

// Principal is the authenticated caller: a User (platform access token) or
// an API Key.
type Principal struct {
	Type      string // "user" | "api_key"
	ID        string // usr_… or key_…
	UserID    string // set for users
	SessionID string // set for users
	AMR       []string
	TestMode  bool

	// Perms is the caller's platform permission set: the union of a User's
	// platform Roles, or an API Key's own permissions.
	Perms map[string]bool

	KeyScope            string // api keys: "platform" or an application_id
	RestrictDestructive bool
	EmailVerified       bool
}

// IsUser reports whether the caller is a User token.
func (p *Principal) IsUser() bool { return p.Type == "user" }

// AppKey returns the Application an app-scoped API Key is confined to.
func (p *Principal) AppKey() (string, bool) {
	if p.Type == "api_key" && p.KeyScope != "platform" {
		return p.KeyScope, true
	}
	return "", false
}

// PlatformKey reports whether the caller is a platform-scoped API Key.
func (p *Principal) PlatformKey() bool { return p.Type == "api_key" && p.KeyScope == "platform" }

// accessClaims are the platform access token's claims (Auth → AuthSession).
type accessClaims struct {
	Iss           string   `json:"iss"`
	Aud           string   `json:"aud"`
	Sub           string   `json:"sub"`
	Sid           string   `json:"sid"`
	AMR           []string `json:"amr"`
	EmailVerified bool     `json:"email_verified"`
	TestMode      bool     `json:"test_mode"`
	Iat           int64    `json:"iat"`
	Exp           int64    `json:"exp"`
	Jti           string   `json:"jti"`
}

const platformAudience = "substratal-platform"

func bearer(c *Call) (string, error) {
	h := c.r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(tok) == "" {
		return "", httpx.ErrUnauthenticated()
	}
	return strings.TrimSpace(tok), nil
}

// authenticate resolves the bearer credential. The transaction is in
// "any" mode while it runs; serve pins the principal's mode afterwards.
func (s *Server) authenticate(c *Call) (*Principal, error) {
	tok, err := bearer(c)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(tok, ids.APIKeyLive) || strings.HasPrefix(tok, ids.APIKeyTest) {
		return s.authenticateKey(c, tok)
	}
	return s.authenticateUser(c, tok)
}

func (s *Server) authenticateKey(c *Call, secret string) (*Principal, error) {
	var k struct {
		ID                  string     `json:"id"`
		Scope               string     `json:"scope"`
		Permissions         []string   `json:"permissions"`
		TestMode            bool       `json:"test_mode"`
		RestrictDestructive bool       `json:"restrict_destructive"`
		ExpiresAt           *time.Time `json:"expires_at"`
		RevokedAt           *time.Time `json:"revoked_at"`
		LastUsedAt          *time.Time `json:"last_used_at"`
	}
	err := db.One(c.ctx, c.q, &k, `select to_jsonb(k) from api_keys k where secret_hash = :h`,
		db.Args{"h": ids.Hash(secret)})
	if err == db.ErrNotFound {
		return nil, httpx.ErrUnauthenticated()
	}
	if err != nil {
		return nil, err
	}
	if k.RevokedAt != nil || (k.ExpiresAt != nil && !k.ExpiresAt.After(c.now)) {
		return nil, httpx.ErrUnauthenticated()
	}
	// The secret's mode prefix must agree with the row (defense in depth).
	if strings.HasPrefix(secret, ids.APIKeyTest) != k.TestMode {
		return nil, httpx.ErrUnauthenticated()
	}
	if k.LastUsedAt == nil || c.now.Sub(*k.LastUsedAt) > time.Minute {
		if _, err := c.q.Exec(c.ctx, `update api_keys set last_used_at = now() where id = :id`, db.Args{"id": k.ID}); err != nil {
			return nil, err
		}
	}
	p := &Principal{Type: "api_key", ID: k.ID, TestMode: k.TestMode, KeyScope: k.Scope,
		RestrictDestructive: k.RestrictDestructive, Perms: map[string]bool{}}
	for _, perm := range k.Permissions {
		p.Perms[perm] = true
	}
	return p, nil
}

func (s *Server) authenticateUser(c *Call, tok string) (*Principal, error) {
	var cl accessClaims
	if err := auth.VerifyJWT(tok, s.keys, &cl); err != nil {
		return nil, httpx.ErrUnauthenticated()
	}
	if cl.Iss != s.Issuer || cl.Aud != platformAudience || cl.Exp <= c.now.Unix() ||
		!strings.HasPrefix(cl.Jti, ids.AccessTokenJTI) || cl.Sub == "" || cl.Sid == "" {
		return nil, httpx.ErrUnauthenticated()
	}
	// One round trip: the session (logout is immediate because every
	// request checks it), the User's standing, and the platform permission
	// set from their platform Roles (resolved fresh, never carried in the
	// token, so a Role change applies on the very next call).
	var row struct {
		RevokedAt     *time.Time `json:"revoked_at"`
		IdleExpires   time.Time  `json:"idle_expires_at"`
		AbsExpires    time.Time  `json:"absolute_expires_at"`
		UserStatus    string     `json:"user_status"`
		TestMode      bool       `json:"test_mode"`
		EmailVerified bool       `json:"email_verified"`
		Perms         []string   `json:"perms"`
	}
	err := db.One(c.ctx, c.q, &row, `select jsonb_build_object(
			'revoked_at', s.revoked_at, 'idle_expires_at', s.idle_expires_at,
			'absolute_expires_at', s.absolute_expires_at,
			'user_status', u.status, 'test_mode', u.test_mode, 'email_verified', u.email_verified,
			'perms', coalesce((select array_agg(distinct perm) from user_role_assignments a
			                     join roles r on r.id = a.role_id, unnest(r.permissions) perm
			                    where a.user_id = u.id and r.scope = 'platform'), '{}'))
		from sessions s join users u on u.id = s.user_id
		where s.id = :sid and s.user_id = :sub`, db.Args{"sid": cl.Sid, "sub": cl.Sub})
	if err == db.ErrNotFound {
		return nil, httpx.ErrUnauthenticated()
	}
	if err != nil {
		return nil, err
	}
	if row.TestMode != cl.TestMode {
		return nil, httpx.ErrUnauthenticated()
	}
	if row.RevokedAt != nil || !row.AbsExpires.After(c.now) || row.UserStatus == "suspended" || row.UserStatus == "deleted" {
		return nil, httpx.ErrSessionRevoked()
	}
	p := &Principal{Type: "user", ID: cl.Sub, UserID: cl.Sub, SessionID: cl.Sid, AMR: cl.AMR,
		TestMode: row.TestMode, EmailVerified: row.EmailVerified, Perms: map[string]bool{}}
	for _, perm := range row.Perms {
		p.Perms[perm] = true
	}
	return p, nil
}

// ── Permission checks ──────────────────────────────────────────────────

// can is has_platform_permission (Access Control → The algorithm): a
// User's platform Roles, or a platform-scoped API Key's own permissions.
// App-scoped keys never hold platform permissions unconfined.
func (c *Call) can(perm string) bool {
	if c.p == nil {
		return false
	}
	if c.p.IsUser() || c.p.PlatformKey() {
		return c.p.Perms[perm]
	}
	return false
}

// require returns 403 forbidden unless can(perm).
func (c *Call) require(perm string) error {
	if c.can(perm) {
		return nil
	}
	return httpx.Forbidden(perm)
}

// appKeyCan reports whether an app-scoped key confined to appID carries
// perm (App-confined permissions).
func (c *Call) appKeyCan(perm, appID string) bool {
	app, ok := c.p.AppKey()
	return ok && app == appID && c.p.Perms[perm]
}

// selfID resolves a {id} path parameter, where "me" means the caller.
func (c *Call) selfID(id string) (string, error) {
	if id == "me" {
		if c.p == nil || !c.p.IsUser() {
			return "", httpx.ErrUserTokenRequired()
		}
		return c.p.UserID, nil
	}
	return id, nil
}

// isSelf reports whether userID is the calling User.
func (c *Call) isSelf(userID string) bool {
	return c.p != nil && c.p.IsUser() && c.p.UserID == userID
}

// actor is the audit-event actor for this call.
func (c *Call) actor() (typ, id string) {
	if c.p == nil {
		return "system", "system"
	}
	return c.p.Type, c.p.ID
}
