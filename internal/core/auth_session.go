package core

import (
	"net/http"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/auth"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

// Token lifetimes (Auth → Token types at a glance).
const (
	accessTTL       = 15 * time.Minute
	appTokenTTL     = 5 * time.Minute
	sessionIdleTTL  = 30 * 24 * time.Hour
	sessionAbsTTL   = 90 * 24 * time.Hour
	mfaChallengeTTL = 5 * time.Minute
	lockoutWindow   = 15 * time.Minute
	lockoutAfter    = 5
)

type authSession struct {
	TokenType             string `json:"token_type"`
	AccessToken           string `json:"access_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	SessionID             string `json:"session_id"`
	UserID                string `json:"user_id"`
	EmailVerified         bool   `json:"email_verified"`
}

// setAnyMode lets a public endpoint look a token up across modes; it must
// pin the owner's mode (setMode) before doing anything else.
func (c *Call) setAnyMode() error {
	_, err := c.q.Query(c.ctx, `select to_jsonb(set_config('app.current_test_mode', 'any', true))`, nil)
	return err
}

// signAccess mints a platform access token for a session.
func (c *Call) signAccess(userID, sessionID string, amr []string, emailVerified, testMode bool) (string, error) {
	return auth.SignJWT(c.ctx, c.s.Signer, accessClaims{
		Iss: c.s.Issuer, Aud: platformAudience, Sub: userID, Sid: sessionID, AMR: amr,
		EmailVerified: emailVerified, TestMode: testMode,
		Iat: c.now.Unix(), Exp: c.now.Add(accessTTL).Unix(), Jti: ids.New(ids.AccessTokenJTI),
	})
}

// issueSession creates a session with a refresh token and an access token,
// and records the login on the User and the identity used.
func (c *Call) issueSession(u userRow, amr []string, identityID string) (authSession, error) {
	sid := ids.New(ids.Session)
	var ipArg any
	if c.ip != "" {
		ipArg = c.ip
	}
	ua := c.r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	if _, err := c.q.Exec(c.ctx, `insert into sessions (id, user_id, amr, ip_address, user_agent, test_mode,
			idle_expires_at, absolute_expires_at)
		values (:id, :u, :amr::text[], (case when :ip::text ~ '^[0-9a-fA-F:.]+$' then :ip::inet end), :ua, :tm,
			:idle::timestamptz, :abs::timestamptz)`,
		db.Args{"id": sid, "u": u.ID, "amr": db.TextArray(amr), "ip": ipArg, "ua": ua, "tm": u.TestMode,
			"idle": db.Time(c.now.Add(sessionIdleTTL)), "abs": db.Time(c.now.Add(sessionAbsTTL))}); err != nil {
		return authSession{}, err
	}
	rtk := ids.Secret(ids.RefreshToken)
	if _, err := c.q.Exec(c.ctx, `insert into refresh_tokens (token_hash, session_id) values (:h, :s)`,
		db.Args{"h": ids.Hash(rtk), "s": sid}); err != nil {
		return authSession{}, err
	}
	if _, err := c.q.Exec(c.ctx, `update users set last_login_at = now() where id = :u`, db.Args{"u": u.ID}); err != nil {
		return authSession{}, err
	}
	if identityID != "" {
		if _, err := c.q.Exec(c.ctx, `update user_identities set last_used_at = now(), failed_login_count = 0,
			failed_login_window_start = null, locked_until = null where id = :i`, db.Args{"i": identityID}); err != nil {
			return authSession{}, err
		}
	}
	at, err := c.signAccess(u.ID, sid, amr, u.EmailVerified, u.TestMode)
	if err != nil {
		return authSession{}, err
	}
	return authSession{TokenType: "Bearer", AccessToken: at, ExpiresIn: int(accessTTL.Seconds()),
		RefreshToken: rtk, RefreshTokenExpiresIn: int(sessionIdleTTL.Seconds()),
		SessionID: sid, UserID: u.ID, EmailVerified: u.EmailVerified}, nil
}

// revokeSessions revokes a User's sessions (all, or all but `except`).
func (c *Call) revokeSessions(userID, reason, except string) error {
	_, err := c.q.Exec(c.ctx, `update sessions set revoked_at = now(), revoked_reason = :r
		where user_id = :u and revoked_at is null and id <> :except`,
		db.Args{"u": userID, "r": reason, "except": except})
	return err
}

func weakPassword(err error) error {
	reason, _ := auth.WeakReason(err)
	return httpx.E(422, "weak_password", "The password doesn't meet the password policy.").With("reason", reason)
}

// ── POST /v1/auth/signup ───────────────────────────────────────────────

func (s *Server) AuthSignup(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.signup", public: true, idem: idemOptional}, func(c *Call) error {
		var in struct {
			Email         string  `json:"email"`
			Password      string  `json:"password"`
			DisplayName   *string `json:"display_name"`
			ApplicationID *string `json:"application_id"`
			TestMode      bool    `json:"test_mode"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.setMode(in.TestMode); err != nil {
			return err
		}
		var f httpx.Fields
		addr, ok := validEmail(in.Email)
		if in.Email == "" {
			f.Add("email", "required")
		} else if !ok {
			f.Add("email", "invalid_format")
		}
		if in.Password == "" {
			f.Add("password", "required")
		}
		if in.DisplayName != nil && (len([]rune(*in.DisplayName)) < 1 || len([]rune(*in.DisplayName)) > 100) {
			f.Max("display_name", "too_long", 100)
		}
		if in.ApplicationID != nil {
			if _, err := c.loadApp(*in.ApplicationID); err == db.ErrNotFound {
				f.Add("application_id", "unknown_value")
			} else if err != nil {
				return err
			}
		}
		if err := f.Err(); err != nil {
			return err
		}
		if err := auth.CheckPolicy(in.Password, addr); err != nil {
			return weakPassword(err)
		}
		if existing, taken, err := c.emailTaken(addr); err != nil {
			return err
		} else if taken {
			if existing.Status == "invited" {
				// Re-send the invitation; counts against the resend limit.
				if c.emailQuota(addr, "resend") {
					if err := c.sendInvitation(existing.ID, existing.Email, existing.SignupApplicationID); err != nil {
						return err
					}
				}
				c.CommitAnyway()
				return httpx.Conflict("email_taken", "An invitation is pending for this email; check your inbox.").
					With("reason", "invitation_pending")
			}
			return httpx.Conflict("email_taken", "A user with this email already exists.")
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return err
		}
		u, err := c.createUser(newUser{Email: addr, Status: "active", DisplayName: deref(in.DisplayName),
			SignupApp: in.ApplicationID, PasswordHash: hash})
		if err != nil {
			return err
		}
		if err := c.sendVerification(u.ID, u.Email, in.ApplicationID, nil); err != nil {
			return err
		}
		var identityID string
		_ = db.One(c.ctx, c.q, &identityID, `select to_jsonb(id) from user_identities where user_id = :u and method = 'password'`, db.Args{"u": u.ID})
		sess, err := c.issueSession(u, []string{"pwd"}, identityID)
		if err != nil {
			return err
		}
		return c.Created(sess)
	})
}

// emailQuota enforces "3 per email per hour" for resend/forgot/invitation
// re-sends. It reports whether another email may be sent; over the limit
// the endpoint still answers normally, so it never reveals anything.
func (c *Call) emailQuota(addr, kind string) bool {
	err := c.hit("email:"+strings.ToLower(addr)+":"+kind+":"+boolStr(c.testMode()), perHour(3), false)
	return err == nil
}

func boolStr(b bool) string {
	if b {
		return "test"
	}
	return "live"
}

// ── POST /v1/auth/login ────────────────────────────────────────────────

type identityRow struct {
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	Method           string     `json:"method"`
	PasswordHash     *string    `json:"password_hash"`
	MFAEnabled       bool       `json:"mfa_enabled"`
	MFASecret        *string    `json:"mfa_secret"`
	MFAPendingSecret *string    `json:"mfa_pending_secret"`
	MFAPendingExp    *time.Time `json:"mfa_pending_expires_at"`
	MFALastStep      *int64     `json:"mfa_last_step"`
	FailedCount      int        `json:"failed_login_count"`
	FailedWindow     *time.Time `json:"failed_login_window_start"`
	LockedUntil      *time.Time `json:"locked_until"`
	LastUsedAt       *time.Time `json:"last_used_at"`
}

func (c *Call) passwordIdentity(userID string) (identityRow, error) {
	var i identityRow
	err := db.One(c.ctx, c.q, &i, `select to_jsonb(i) from user_identities i
		where user_id = :u and method = 'password' order by created_at limit 1`, db.Args{"u": userID})
	return i, err
}

func invalidCredentials() error {
	return httpx.E(401, "invalid_credentials", "The email or password is incorrect.")
}

func (s *Server) AuthLogin(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.login", public: true}, func(c *Call) error {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			TestMode bool   `json:"test_mode"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.Email == "" || in.Password == "" {
			return httpx.Invalid("email and password are required.")
		}
		if err := c.setMode(in.TestMode); err != nil {
			return err
		}
		u, err := c.findUser("u.email = :e and u.deleted_at is null", db.Args{"e": strings.TrimSpace(in.Email)})
		if err == db.ErrNotFound {
			auth.BurnPasswordCheck(in.Password)
			return invalidCredentials()
		}
		if err != nil {
			return err
		}
		ident, err := c.passwordIdentity(u.ID)
		if err == db.ErrNotFound || (err == nil && ident.PasswordHash == nil) {
			auth.BurnPasswordCheck(in.Password)
			return invalidCredentials()
		}
		if err != nil {
			return err
		}
		if ident.LockedUntil != nil && ident.LockedUntil.After(c.now) {
			return httpx.E(429, "too_many_attempts", "Too many failed attempts; try again later.").
				RetryAfter(int(ident.LockedUntil.Sub(c.now).Seconds()) + 1)
		}
		if !auth.VerifyPassword(in.Password, *ident.PasswordHash) {
			c.CommitAnyway()
			return c.recordLoginFailure(ident)
		}
		if u.Status == "suspended" {
			return httpx.E(403, "account_suspended", "This account is suspended.")
		}
		if u.Status != "active" {
			return invalidCredentials()
		}
		if ident.MFAEnabled {
			tok, err := c.issueToken("mfa_challenge", ids.MFAChallenge, u.ID, mfaChallengeTTL,
				map[string]any{"identity_id": ident.ID})
			if err != nil {
				return err
			}
			if _, err := c.q.Exec(c.ctx, `update user_identities set failed_login_count = 0,
				failed_login_window_start = null where id = :i`, db.Args{"i": ident.ID}); err != nil {
				return err
			}
			s.Log.Info("security", "event", "mfa_challenge", "user", u.ID, "request_id", c.requestID, "ip", c.ip)
			return c.OK(map[string]any{"mfa_required": true, "mfa_token": tok,
				"methods": []string{"totp", "recovery_code"}, "expires_in": int(mfaChallengeTTL.Seconds())})
		}
		sess, err := c.issueSession(u, []string{"pwd"}, ident.ID)
		if err != nil {
			return err
		}
		s.Log.Info("security", "event", "login", "user", u.ID, "request_id", c.requestID, "ip", c.ip)
		return c.OK(sess)
	})
}

// recordLoginFailure counts a failure in a rolling 15-minute window and
// locks the identity for 15 minutes at 5 failures.
func (c *Call) recordLoginFailure(ident identityRow) error {
	count, window := ident.FailedCount+1, c.now
	if ident.FailedWindow != nil && c.now.Sub(*ident.FailedWindow) < lockoutWindow {
		window = *ident.FailedWindow
	} else {
		count = 1
	}
	var locked any
	if count >= lockoutAfter {
		locked = db.Time(c.now.Add(lockoutWindow))
	}
	if _, err := c.q.Exec(c.ctx, `update user_identities set failed_login_count = :n,
		failed_login_window_start = :w::timestamptz, locked_until = :l::timestamptz where id = :i`,
		db.Args{"n": count, "w": db.Time(window), "l": locked, "i": ident.ID}); err != nil {
		return err
	}
	if locked != nil {
		c.s.Log.Warn("security", "event", "lockout", "user", ident.UserID, "request_id", c.requestID, "ip", c.ip)
	}
	return invalidCredentials()
}

// ── POST /v1/auth/mfa/verify ───────────────────────────────────────────

type authTokenRow struct {
	TokenHash  string         `json:"token_hash"`
	Kind       string         `json:"kind"`
	UserID     string         `json:"user_id"`
	Payload    map[string]any `json:"payload"`
	Attempts   int            `json:"attempts"`
	ExpiresAt  time.Time      `json:"expires_at"`
	ConsumedAt *time.Time     `json:"consumed_at"`
}

// lookupToken finds an unexpired, unconsumed token of kind in any mode,
// then pins the transaction to its User's mode and returns that User.
func (c *Call) lookupToken(tok, kind string) (authTokenRow, userRow, bool, error) {
	if err := c.setAnyMode(); err != nil {
		return authTokenRow{}, userRow{}, false, err
	}
	var t authTokenRow
	err := db.One(c.ctx, c.q, &t, `select to_jsonb(t) from auth_tokens t where token_hash = :h and kind = :k`,
		db.Args{"h": ids.Hash(tok), "k": kind})
	if err == db.ErrNotFound {
		return t, userRow{}, false, c.setMode(false)
	}
	if err != nil {
		return t, userRow{}, false, err
	}
	u, err := c.findUser("u.id = :id", db.Args{"id": t.UserID})
	if err != nil {
		return t, u, false, err
	}
	if err := c.setMode(u.TestMode); err != nil {
		return t, u, false, err
	}
	valid := t.ConsumedAt == nil && t.ExpiresAt.After(c.now)
	return t, u, valid, nil
}

func (c *Call) consumeToken(hash string) error {
	_, err := c.q.Exec(c.ctx, `update auth_tokens set consumed_at = now() where token_hash = :h`, db.Args{"h": hash})
	return err
}

// verifySecondFactor checks a TOTP code (refusing reuse within its window)
// or consumes a recovery code. It returns the amr entry used.
func (c *Call) verifySecondFactor(ident identityRow, code, recovery string) (string, bool, error) {
	if code != "" && ident.MFASecret != nil {
		secret, err := c.s.Encrypter.Decrypt(c.ctx, *ident.MFASecret)
		if err != nil {
			return "", false, err
		}
		last := int64(0)
		if ident.MFALastStep != nil {
			last = *ident.MFALastStep
		}
		step, ok := auth.VerifyTOTP(secret, code, c.now, last)
		if !ok {
			return "", false, nil
		}
		_, err = c.q.Exec(c.ctx, `update user_identities set mfa_last_step = :s where id = :i`,
			db.Args{"s": step, "i": ident.ID})
		return "otp", true, err
	}
	if recovery != "" {
		type rc struct {
			Hash string `json:"code_hash"`
		}
		codes, err := db.All[rc](c.ctx, c.q, `select to_jsonb(r) from mfa_recovery_codes r
			where user_identity_id = :i and used_at is null`, db.Args{"i": ident.ID})
		if err != nil {
			return "", false, err
		}
		norm := auth.NormalizeRecoveryCode(recovery)
		for _, code := range codes {
			if auth.VerifyPassword(norm, code.Hash) {
				_, err := c.q.Exec(c.ctx, `update mfa_recovery_codes set used_at = now()
					where user_identity_id = :i and code_hash = :h`, db.Args{"i": ident.ID, "h": code.Hash})
				return "recovery_code", true, err
			}
		}
	}
	return "", false, nil
}

func (s *Server) AuthVerifyMfa(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.verifyMfa", public: true}, func(c *Call) error {
		var in struct {
			MFAToken     string `json:"mfa_token"`
			Code         string `json:"code"`
			RecoveryCode string `json:"recovery_code"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.MFAToken == "" || (in.Code == "") == (in.RecoveryCode == "") {
			return httpx.Invalid("mfa_token and exactly one of code or recovery_code are required.")
		}
		tokenInvalid := httpx.E(401, "mfa_token_invalid", "The MFA challenge expired, was used, or is exhausted. Start over at login.")
		t, u, valid, err := c.lookupToken(in.MFAToken, "mfa_challenge")
		if err != nil {
			return err
		}
		if !valid || t.Attempts >= 5 {
			return tokenInvalid
		}
		identityID, _ := t.Payload["identity_id"].(string)
		var ident identityRow
		if err := db.One(c.ctx, c.q, &ident, `select to_jsonb(i) from user_identities i where id = :i`,
			db.Args{"i": identityID}); err != nil {
			return tokenInvalid
		}
		method, ok, err := c.verifySecondFactor(ident, in.Code, in.RecoveryCode)
		if err != nil {
			return err
		}
		if !ok {
			c.CommitAnyway()
			if _, err := c.q.Exec(c.ctx, `update auth_tokens set attempts = attempts + 1 where token_hash = :h`,
				db.Args{"h": t.TokenHash}); err != nil {
				return err
			}
			return httpx.E(401, "invalid_mfa_code", "The code is wrong or was already used.")
		}
		if u.Status != "active" {
			if u.Status == "suspended" {
				return httpx.E(403, "account_suspended", "This account is suspended.")
			}
			return tokenInvalid
		}
		if err := c.consumeToken(t.TokenHash); err != nil {
			return err
		}
		sess, err := c.issueSession(u, []string{"pwd", method}, ident.ID)
		if err != nil {
			return err
		}
		return c.OK(sess)
	})
}

// ── POST /v1/auth/token/refresh ────────────────────────────────────────

type refreshRow struct {
	TokenHash     string     `json:"token_hash"`
	SessionID     string     `json:"session_id"`
	ApplicationID *string    `json:"application_id"`
	RotatedAt     *time.Time `json:"rotated_at"`
	UserID        string     `json:"user_id"`
	AMR           []string   `json:"amr"`
	RevokedAt     *time.Time `json:"revoked_at"`
	IdleExpires   time.Time  `json:"idle_expires_at"`
	AbsExpires    time.Time  `json:"absolute_expires_at"`
}

// lookupRefresh resolves a refresh token in any mode and pins the owner's.
func (c *Call) lookupRefresh(tok string) (refreshRow, userRow, error) {
	if err := c.setAnyMode(); err != nil {
		return refreshRow{}, userRow{}, err
	}
	var rt refreshRow
	err := db.One(c.ctx, c.q, &rt, `select to_jsonb(r) || jsonb_build_object('user_id', s.user_id, 'amr', s.amr,
			'revoked_at', s.revoked_at, 'idle_expires_at', s.idle_expires_at, 'absolute_expires_at', s.absolute_expires_at)
		from refresh_tokens r join sessions s on s.id = r.session_id where r.token_hash = :h`,
		db.Args{"h": ids.Hash(tok)})
	if err != nil {
		_ = c.setMode(false)
		return rt, userRow{}, err
	}
	u, err := c.findUser("u.id = :id", db.Args{"id": rt.UserID})
	if err != nil {
		return rt, u, err
	}
	return rt, u, c.setMode(u.TestMode)
}

// rotateRefresh enforces single use: a token presented after rotation is
// theft, and revokes the whole session (Auth → token/refresh).
func (c *Call) rotateRefresh(rt refreshRow) (string, error) {
	if rt.RotatedAt != nil {
		c.CommitAnyway()
		if _, err := c.q.Exec(c.ctx, `update sessions set revoked_at = now(), revoked_reason = 'refresh_token_reused'
			where id = :s and revoked_at is null`, db.Args{"s": rt.SessionID}); err != nil {
			return "", err
		}
		c.s.Log.Warn("security", "event", "refresh_token_reused", "user", rt.UserID, "session", rt.SessionID,
			"request_id", c.requestID, "ip", c.ip)
		return "", httpx.E(401, "refresh_token_reused", "This refresh token was already used; the session has been revoked.")
	}
	if rt.RevokedAt != nil || !rt.IdleExpires.After(c.now) || !rt.AbsExpires.After(c.now) {
		return "", httpx.ErrSessionRevoked()
	}
	if _, err := c.q.Exec(c.ctx, `update refresh_tokens set rotated_at = now() where token_hash = :h`,
		db.Args{"h": rt.TokenHash}); err != nil {
		return "", err
	}
	next := ids.Secret(ids.RefreshToken)
	if _, err := c.q.Exec(c.ctx, `insert into refresh_tokens (token_hash, session_id, application_id) values (:h, :s, :a)`,
		db.Args{"h": ids.Hash(next), "s": rt.SessionID, "a": rt.ApplicationID}); err != nil {
		return "", err
	}
	if _, err := c.q.Exec(c.ctx, `update sessions set last_seen_at = now(),
		idle_expires_at = least(absolute_expires_at, :idle::timestamptz) where id = :s`,
		db.Args{"s": rt.SessionID, "idle": db.Time(c.now.Add(sessionIdleTTL))}); err != nil {
		return "", err
	}
	return next, nil
}

func (s *Server) AuthRefreshToken(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.refreshToken", public: true}, func(c *Call) error {
		var in struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if !strings.HasPrefix(in.RefreshToken, ids.RefreshToken) {
			return httpx.ErrUnauthenticated()
		}
		rt, u, err := c.lookupRefresh(in.RefreshToken)
		if err == db.ErrNotFound || (err == nil && rt.ApplicationID != nil) {
			return httpx.ErrUnauthenticated()
		}
		if err != nil {
			return err
		}
		if err := c.hit("refresh:"+rt.SessionID, perMinute(30), true); err != nil {
			return err
		}
		if u.Status == "suspended" || u.Status == "deleted" {
			return httpx.ErrSessionRevoked()
		}
		next, err := c.rotateRefresh(rt)
		if err != nil {
			return err
		}
		at, err := c.signAccess(u.ID, rt.SessionID, rt.AMR, u.EmailVerified, u.TestMode)
		if err != nil {
			return err
		}
		return c.OK(authSession{TokenType: "Bearer", AccessToken: at, ExpiresIn: int(accessTTL.Seconds()),
			RefreshToken: next, RefreshTokenExpiresIn: int(sessionIdleTTL.Seconds()),
			SessionID: rt.SessionID, UserID: u.ID, EmailVerified: u.EmailVerified})
	})
}

// ── POST /v1/auth/logout ───────────────────────────────────────────────

func (s *Server) AuthLogout(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.logout", userOnly: true}, func(c *Call) error {
		var in struct {
			AllSessions bool `json:"all_sessions"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.destructiveIf(in.AllSessions); err != nil {
			return err
		}
		var err error
		if in.AllSessions {
			err = c.revokeSessions(c.p.UserID, "logout", "")
		} else {
			_, err = c.q.Exec(c.ctx, `update sessions set revoked_at = now(), revoked_reason = 'logout'
				where id = :s and revoked_at is null`, db.Args{"s": c.p.SessionID})
		}
		if err != nil {
			return err
		}
		return c.NoContent()
	})
}

// AuthSsoCallback is reserved until the SSO broker integration ships.
func (s *Server) AuthSsoCallback(w http.ResponseWriter, r *http.Request, provider string) {
	s.serve(w, r, opts{op: "auth.ssoCallback", public: true}, func(c *Call) error {
		return httpx.E(501, "not_implemented", "SSO sign-in isn't available yet.")
	})
}
