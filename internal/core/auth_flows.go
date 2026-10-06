package core

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Adron/substratalapps.com/internal/access"
	"github.com/Adron/substratalapps.com/internal/api/gen"
	"github.com/Adron/substratalapps.com/internal/auth"
	"github.com/Adron/substratalapps.com/internal/db"
	"github.com/Adron/substratalapps.com/internal/httpx"
	"github.com/Adron/substratalapps.com/internal/ids"
)

// ── App tokens (Auth → Getting an app token) ───────────────────────────

type appTokenClaims struct {
	Iss                  string   `json:"iss"`
	Aud                  string   `json:"aud"`
	Sub                  string   `json:"sub"`
	Sid                  string   `json:"sid"`
	OrgID                *string  `json:"org_id"`
	Email                string   `json:"email"`
	EmailVerified        bool     `json:"email_verified"`
	EntitlementStatus    string   `json:"entitlement_status"`
	EffectivePermissions []string `json:"effective_permissions"`
	TestMode             bool     `json:"test_mode"`
	Iat                  int64    `json:"iat"`
	Exp                  int64    `json:"exp"`
	Jti                  string   `json:"jti"`
}

// appVisibleTo applies Applications → Who can see an Application.
func (c *Call) appVisible(a appRow) (bool, error) {
	if c.can("applications.manage") {
		return true, nil
	}
	if app, ok := c.p.AppKey(); ok && app == a.ID {
		return true, nil
	}
	if owner, err := c.isAppOwner(a); err != nil || owner {
		return owner, err
	}
	if a.ReviewStatus != "approved" {
		return false, nil
	}
	switch a.Visibility {
	case "public":
		return true, nil
	case "invite_only":
		if c.p.IsUser() {
			return c.userHasAccessPath(c.p.UserID, a.ID)
		}
	}
	return false, nil
}

// mintAppToken checks access right now and signs the per-Application JWT.
// The same checks back app-tokens, authorization-codes, and oauth/token.
func (c *Call) mintAppToken(u userRow, sessionID string, a appRow, orgID *string) (string, error) {
	if u.Status == "suspended" {
		return "", httpx.E(403, "account_suspended", "This account is suspended.")
	}
	if a.ReviewStatus != "approved" {
		return "", httpx.Conflict("application_not_available", "This Application isn't available.").
			With("review_status", a.ReviewStatus)
	}
	t, err := c.loadTenant(a.TenantID)
	if err != nil {
		return "", err
	}
	if t.Status == "suspended" {
		return "", httpx.E(403, "tenant_suspended", "This Application's Tenant is suspended.").With("tenant_id", t.ID)
	}
	res, err := c.resolveOne(u.ID, a.ID)
	if err != nil {
		return "", err
	}
	entitlementRequired := func(status string) error {
		return httpx.E(403, "entitlement_required", "This user has no active entitlement to "+a.ID+".").
			With("application_id", a.ID).With("entitlement_status", status)
	}
	if !res.Allowed {
		return "", entitlementRequired(res.Res.Status)
	}
	var attributed *string
	if orgID != nil {
		via, ok := access.ActiveViaOrg(u.ID, *orgID, res.Paths, c.now)
		if !ok {
			return "", entitlementRequired(res.Res.Status)
		}
		attributed = via.OrganizationID
	} else if res.Res.ActiveVia != nil && res.Res.ActiveVia.IsOrg() {
		attributed = res.Res.ActiveVia.OrganizationID
	}
	roles, err := c.appRolesFor(u.ID, a.ID)
	if err != nil {
		return "", err
	}
	_, perms := access.EffectivePermissions(true, roles)
	return auth.SignJWT(c.ctx, c.s.Signer, appTokenClaims{
		Iss: c.s.Issuer, Aud: a.ID, Sub: u.ID, Sid: sessionID, OrgID: attributed, Email: u.Email,
		EmailVerified: u.EmailVerified, EntitlementStatus: "active", EffectivePermissions: perms,
		TestMode: u.TestMode, Iat: c.now.Unix(), Exp: c.now.Add(appTokenTTL).Unix(), Jti: ids.New(ids.AppTokenJTI),
	})
}

func (s *Server) AuthCreateAppToken(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.createAppToken", userOnly: true}, func(c *Call) error {
		var in struct {
			ApplicationID  string  `json:"application_id"`
			OrganizationID *string `json:"organization_id"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if in.ApplicationID == "" {
			return httpx.Invalid("application_id is required.")
		}
		a, err := c.loadApp(in.ApplicationID)
		if err == db.ErrNotFound {
			return httpx.NotFoundResource("application")
		}
		if err != nil {
			return err
		}
		if ok, err := c.appVisibleForToken(a); err != nil {
			return err
		} else if !ok {
			return httpx.NotFoundResource("application")
		}
		u, err := c.findUser("u.id = :id", db.Args{"id": c.p.UserID})
		if err != nil {
			return err
		}
		tok, err := c.mintAppToken(u, c.p.SessionID, a, in.OrganizationID)
		if err != nil {
			return err
		}
		return c.OK(map[string]any{"token_type": "Bearer", "app_token": tok,
			"expires_in": int(appTokenTTL.Seconds()), "application_id": a.ID})
	})
}

// appVisibleForToken: a User may request a token for any Application they
// can see, or have any access path to (an internal app granted by the
// platform is invisible in the catalog but still launchable by its users).
func (c *Call) appVisibleForToken(a appRow) (bool, error) {
	if ok, err := c.appVisible(a); err != nil || ok {
		return ok, err
	}
	return c.userHasAccessPath(c.p.UserID, a.ID)
}

// ── Hosted flow: authorization codes + PKCE ────────────────────────────

func (s *Server) AuthCreateAuthorizationCode(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.createAuthorizationCode", userOnly: true}, func(c *Call) error {
		var in struct {
			ApplicationID       string `json:"application_id"`
			RedirectURI         string `json:"redirect_uri"`
			CodeChallenge       string `json:"code_challenge"`
			CodeChallengeMethod string `json:"code_challenge_method"`
			State               string `json:"state"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		var f httpx.Fields
		if in.ApplicationID == "" {
			f.Add("application_id", "required")
		}
		if in.RedirectURI == "" {
			f.Add("redirect_uri", "required")
		}
		if len(in.CodeChallenge) < 43 || len(in.CodeChallenge) > 128 {
			f.Add("code_challenge", "invalid_format")
		}
		if in.CodeChallengeMethod != "S256" {
			f.Enum("code_challenge_method", "S256")
		}
		if err := f.Err(); err != nil {
			return err
		}
		a, err := c.loadApp(in.ApplicationID)
		if err == db.ErrNotFound {
			return httpx.NotFoundResource("application")
		}
		if err != nil {
			return err
		}
		if ok, err := c.appVisibleForToken(a); err != nil {
			return err
		} else if !ok {
			return httpx.NotFoundResource("application")
		}
		registered := false
		for _, u := range a.RedirectURIs {
			if u == in.RedirectURI {
				registered = true
			}
		}
		if !registered {
			return httpx.E(422, "redirect_uri_not_registered", "redirect_uri must exactly match one of the Application's redirect_uris.")
		}
		u, err := c.findUser("u.id = :id", db.Args{"id": c.p.UserID})
		if err != nil {
			return err
		}
		// Access is checked here too, so a User without access is never
		// redirected into the app holding a code.
		if _, err := c.mintAppToken(u, c.p.SessionID, a, nil); err != nil {
			return err
		}
		code := ids.Secret(ids.AuthorizationCode)
		if _, err := c.q.Exec(c.ctx, `insert into auth_tokens (token_hash, kind, user_id, payload, expires_at)
			values (:h, 'authorization_code', :u, :p::jsonb, :exp::timestamptz)`,
			db.Args{"h": ids.Hash(code), "u": u.ID, "exp": db.Time(c.now.Add(60 * time.Second)),
				"p": db.JSON(map[string]any{"application_id": a.ID, "redirect_uri": in.RedirectURI,
					"code_challenge": in.CodeChallenge, "session_id": c.p.SessionID})}); err != nil {
			return err
		}
		q := url.Values{"code": {code}}
		if in.State != "" {
			q.Set("state", in.State)
		}
		sep := "?"
		if strings.Contains(in.RedirectURI, "?") {
			sep = "&"
		}
		return c.Created(map[string]any{"code": code, "expires_in": 60, "redirect_to": in.RedirectURI + sep + q.Encode()})
	})
}

// oauthErr responds with the OAuth 2.0 error shape this one endpoint uses.
func (c *Call) oauthErr(status int, code, desc string) error {
	c.status = status
	c.body = map[string]string{"error": code, "error_description": desc}
	return nil
}

func (s *Server) AuthExchangeOAuthToken(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.exchangeOAuthToken", public: true}, func(c *Call) error {
		var in struct {
			GrantType    string `json:"grant_type"`
			ClientID     string `json:"client_id"`
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
			RedirectURI  string `json:"redirect_uri"`
			RefreshToken string `json:"refresh_token"`
		}
		if strings.HasPrefix(c.r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			v, err := url.ParseQuery(string(c.raw))
			if err != nil {
				return c.oauthErr(400, "invalid_request", "The form body couldn't be parsed.")
			}
			in.GrantType, in.ClientID, in.Code = v.Get("grant_type"), v.Get("client_id"), v.Get("code")
			in.CodeVerifier, in.RedirectURI, in.RefreshToken = v.Get("code_verifier"), v.Get("redirect_uri"), v.Get("refresh_token")
		} else if err := json.Unmarshal(nonEmpty(c.raw), &in); err != nil {
			return c.oauthErr(400, "invalid_request", "The body must be JSON or form-encoded.")
		}
		if in.GrantType != "authorization_code" && in.GrantType != "refresh_token" {
			return c.oauthErr(400, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token.")
		}
		a, err := c.loadApp(in.ClientID)
		if err == db.ErrNotFound || in.ClientID == "" {
			return c.oauthErr(401, "invalid_client", "Unknown client_id.")
		}
		if err != nil {
			return err
		}
		var u userRow
		var sessionID string
		if in.GrantType == "authorization_code" {
			t, owner, valid, err := c.lookupToken(in.Code, "authorization_code")
			if err != nil {
				return err
			}
			if !valid {
				return c.oauthErr(400, "invalid_grant", "The authorization code is invalid, expired, or already used.")
			}
			// Single use, even when the exchange below fails.
			if err := c.consumeToken(t.TokenHash); err != nil {
				return err
			}
			challenge, _ := t.Payload["code_challenge"].(string)
			sum := sha256.Sum256([]byte(in.CodeVerifier))
			computed := base64.RawURLEncoding.EncodeToString(sum[:])
			if t.Payload["application_id"] != a.ID || t.Payload["redirect_uri"] != in.RedirectURI ||
				subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) != 1 {
				return c.oauthErr(400, "invalid_grant", "The code, redirect_uri, or code_verifier doesn't match.")
			}
			sessionID, _ = t.Payload["session_id"].(string)
			var sessRevoked *time.Time
			if err := db.One(c.ctx, c.q, &sessRevoked, `select to_jsonb(revoked_at) from sessions where id = :s`,
				db.Args{"s": sessionID}); err != nil || sessRevoked != nil {
				return c.oauthErr(400, "invalid_grant", "The session that issued this code has ended.")
			}
			u = owner
		} else {
			rt, owner, err := c.lookupRefresh(in.RefreshToken)
			if err == db.ErrNotFound || (err == nil && (rt.ApplicationID == nil || *rt.ApplicationID != a.ID)) {
				return c.oauthErr(400, "invalid_grant", "The refresh token is invalid.")
			}
			if err != nil {
				return err
			}
			if rt.RotatedAt != nil || rt.RevokedAt != nil || !rt.IdleExpires.After(c.now) {
				if _, err := c.rotateRefresh(rt); err != nil {
					c.CommitAnyway()
				}
				return c.oauthErr(400, "invalid_grant", "The refresh token was already used or its session ended.")
			}
			sessionID, u = rt.SessionID, owner
			// Re-check access every time before rotating.
			tok, err := c.mintAppToken(u, sessionID, a, nil)
			if err != nil {
				return c.oauthAccessDenied(err)
			}
			next, err := c.rotateRefresh(rt)
			if err != nil {
				return err
			}
			return c.OK(oauthTokenResponse(tok, next, a.ID))
		}
		tok, err := c.mintAppToken(u, sessionID, a, nil)
		if err != nil {
			return c.oauthAccessDenied(err)
		}
		rtk := ids.Secret(ids.RefreshToken)
		if _, err := c.q.Exec(c.ctx, `insert into refresh_tokens (token_hash, session_id, application_id) values (:h, :s, :a)`,
			db.Args{"h": ids.Hash(rtk), "s": sessionID, "a": a.ID}); err != nil {
			return err
		}
		return c.OK(oauthTokenResponse(tok, rtk, a.ID))
	})
}

func oauthTokenResponse(tok, refresh, appID string) map[string]any {
	return map[string]any{"token_type": "Bearer", "app_token": tok, "expires_in": int(appTokenTTL.Seconds()),
		"refresh_token": refresh, "refresh_token_expires_in": int(sessionIdleTTL.Seconds()), "application_id": appID}
}

// oauthAccessDenied maps an access failure to OAuth's access_denied, with
// the API's own error code carried in error_description.
func (c *Call) oauthAccessDenied(err error) error {
	if e, ok := err.(*httpx.Error); ok {
		c.CommitAnyway()
		desc := e.Code
		if st, ok := e.Details["entitlement_status"]; ok {
			desc = fmt.Sprintf("%s (entitlement_status: %v)", e.Code, st)
		}
		_ = c.oauthErr(400, "access_denied", desc)
		return nil
	}
	return err
}

func nonEmpty(b []byte) []byte {
	if len(strings.TrimSpace(string(b))) == 0 {
		return []byte("{}")
	}
	return b
}

// ── Email verification ─────────────────────────────────────────────────

func tokenInvalid() error {
	return httpx.E(400, "token_invalid", "This token is expired, already used, or unknown.")
}

func (s *Server) AuthVerifyEmail(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.verifyEmail", public: true}, func(c *Call) error {
		var in struct {
			Token string `json:"token"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		t, u, valid, err := c.lookupToken(in.Token, "email_verify")
		if err != nil {
			return err
		}
		if !valid {
			return tokenInvalid()
		}
		if err := c.consumeToken(t.TokenHash); err != nil {
			return err
		}
		if newEmail, ok := t.Payload["new_email"].(string); ok && newEmail != "" {
			if u.PendingEmail == nil || !strings.EqualFold(*u.PendingEmail, newEmail) {
				return tokenInvalid()
			}
			if _, taken, err := c.emailTaken(newEmail); err != nil {
				return err
			} else if taken {
				return httpx.Conflict("email_taken", "Another account now uses this email.")
			}
			if _, err := c.q.Exec(c.ctx, `update users set email = pending_email, pending_email = null,
				email_verified = true where id = :u`, db.Args{"u": u.ID}); err != nil {
				return err
			}
			after, _ := c.findUser("u.id = :id", db.Args{"id": u.ID})
			if err := c.audit(Audit{Action: "user.email_changed", TargetType: "user", TargetID: u.ID,
				TargetUserID: &u.ID, Before: map[string]any{"email": u.Email}, After: map[string]any{"email": after.Email},
				ActorType: "user", ActorID: u.ID}); err != nil {
				return err
			}
			return c.NoContent()
		}
		if _, err := c.q.Exec(c.ctx, `update users set email_verified = true where id = :u`, db.Args{"u": u.ID}); err != nil {
			return err
		}
		return c.NoContent()
	})
}

func (s *Server) AuthResendEmailVerification(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.resendEmailVerification", public: true}, func(c *Call) error {
		var in struct {
			Email    string `json:"email"`
			TestMode bool   `json:"test_mode"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.setMode(in.TestMode); err != nil {
			return err
		}
		// Always 202: whether the email exists or is verified is never revealed.
		u, err := c.findUser("u.email = :e and u.deleted_at is null", db.Args{"e": strings.TrimSpace(in.Email)})
		if err == nil && u.Status == "active" && c.emailQuota(u.Email, "resend") {
			if !u.EmailVerified {
				if err := c.sendVerification(u.ID, u.Email, u.SignupApplicationID, nil); err != nil {
					return err
				}
			} else if u.PendingEmail != nil {
				if err := c.sendVerification(u.ID, *u.PendingEmail, nil, map[string]any{"new_email": *u.PendingEmail}); err != nil {
					return err
				}
			}
		} else if err != nil && err != db.ErrNotFound {
			return err
		}
		return c.Accepted(nil)
	})
}

// ── Password lifecycle ─────────────────────────────────────────────────

func (s *Server) AuthForgotPassword(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.forgotPassword", public: true}, func(c *Call) error {
		var in struct {
			Email    string `json:"email"`
			TestMode bool   `json:"test_mode"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if err := c.setMode(in.TestMode); err != nil {
			return err
		}
		u, err := c.findUser("u.email = :e and u.deleted_at is null", db.Args{"e": strings.TrimSpace(in.Email)})
		if err == db.ErrNotFound || (err == nil && u.Status != "active") {
			return c.Accepted(nil)
		}
		if err != nil {
			return err
		}
		if !c.emailQuota(u.Email, "forgot") {
			return c.Accepted(nil)
		}
		var methods []string
		methods, err = db.All[string](c.ctx, c.q, `select to_jsonb(method) from user_identities where user_id = :u`, db.Args{"u": u.ID})
		if err != nil {
			return err
		}
		hasPassword, hasSSO := false, false
		for _, m := range methods {
			hasPassword = hasPassword || m == "password"
			hasSSO = hasSSO || m == "sso"
		}
		if hasSSO && !hasPassword {
			c.notice(u.Email, "sso_signin", "Signing in to Substratal",
				"Your account signs in through your organization's identity provider, so there's no password to reset.")
			return c.Accepted(nil)
		}
		// An active User created without a password (POST /v1/users with
		// status active) sets their first one this way too.
		tok, err := c.issueToken("password_reset", ids.PasswordReset, u.ID, time.Hour, nil)
		if err != nil {
			return err
		}
		c.notice(u.Email, "password_reset", "Reset your Substratal password",
			fmt.Sprintf("Set a new password:\n\n%s\n\nThis link expires in 1 hour. If you didn't ask for this, ignore this email.",
				c.link("/reset-password", tok)))
		return c.Accepted(nil)
	})
}

// setPassword writes a new password, creating the password identity if
// the User has none yet.
func (c *Call) setPassword(userID, hash string) (string, error) {
	ident, err := c.passwordIdentity(userID)
	if err == db.ErrNotFound {
		id := ids.New(ids.UserIdentity)
		_, err := c.q.Exec(c.ctx, `insert into user_identities (id, user_id, method, password_hash)
			values (:id, :u, 'password', :h)`, db.Args{"id": id, "u": userID, "h": hash})
		return id, err
	}
	if err != nil {
		return "", err
	}
	_, err = c.q.Exec(c.ctx, `update user_identities set password_hash = :h, failed_login_count = 0,
		failed_login_window_start = null, locked_until = null where id = :i`, db.Args{"h": hash, "i": ident.ID})
	return ident.ID, err
}

func (s *Server) AuthResetPassword(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.resetPassword", public: true}, func(c *Call) error {
		var in struct {
			Token       string `json:"token"`
			NewPassword string `json:"new_password"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		t, u, valid, err := c.lookupToken(in.Token, "password_reset")
		if err != nil {
			return err
		}
		if !valid || u.Status != "active" {
			return tokenInvalid()
		}
		if err := auth.CheckPolicy(in.NewPassword, u.Email); err != nil {
			return weakPassword(err)
		}
		hash, err := auth.HashPassword(in.NewPassword)
		if err != nil {
			return err
		}
		if err := c.consumeToken(t.TokenHash); err != nil {
			return err
		}
		if _, err := c.setPassword(u.ID, hash); err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update users set email_verified = true where id = :u`, db.Args{"u": u.ID}); err != nil {
			return err
		}
		if err := c.revokeSessions(u.ID, "password_reset", ""); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "user.password_reset", TargetType: "user", TargetID: u.ID,
			TargetUserID: &u.ID, ActorType: "user", ActorID: u.ID}); err != nil {
			return err
		}
		c.notice(u.Email, "password_changed", "Your Substratal password was changed",
			"Your password was just reset and every session was signed out. If this wasn't you, contact support.")
		return c.NoContent()
	})
}

func (s *Server) AuthChangePassword(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "auth.changePassword", userOnly: true}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) {
			return httpx.Forbidden("")
		}
		var in struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		u, err := c.findUser("u.id = :id", db.Args{"id": uid})
		if err != nil {
			return err
		}
		ident, err := c.passwordIdentity(uid)
		if err == db.ErrNotFound || (err == nil && (ident.PasswordHash == nil || !auth.VerifyPassword(in.CurrentPassword, *ident.PasswordHash))) {
			return invalidCredentials()
		}
		if err != nil {
			return err
		}
		if err := auth.CheckPolicy(in.NewPassword, u.Email); err != nil {
			return weakPassword(err)
		}
		hash, err := auth.HashPassword(in.NewPassword)
		if err != nil {
			return err
		}
		if _, err := c.setPassword(uid, hash); err != nil {
			return err
		}
		if err := c.revokeSessions(uid, "password_changed", c.p.SessionID); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "user.password_changed", TargetType: "user", TargetID: uid, TargetUserID: &uid}); err != nil {
			return err
		}
		c.notice(u.Email, "password_changed", "Your Substratal password was changed",
			"Your password was just changed and your other sessions were signed out. If this wasn't you, reset it now.")
		return c.NoContent()
	})
}

// ── Invitations ────────────────────────────────────────────────────────

func (s *Server) AuthAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, opts{op: "auth.acceptInvitation", public: true}, func(c *Call) error {
		var in struct {
			Token       string  `json:"token"`
			Password    string  `json:"password"`
			DisplayName *string `json:"display_name"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		t, u, valid, err := c.lookupToken(in.Token, "invitation")
		if err != nil {
			return err
		}
		if !valid || u.Status != "invited" {
			return tokenInvalid()
		}
		if err := auth.CheckPolicy(in.Password, u.Email); err != nil {
			return weakPassword(err)
		}
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return err
		}
		if err := c.consumeToken(t.TokenHash); err != nil {
			return err
		}
		identityID, err := c.setPassword(u.ID, hash)
		if err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update users set status = 'active', email_verified = true where id = :u`, db.Args{"u": u.ID}); err != nil {
			return err
		}
		if in.DisplayName != nil && *in.DisplayName != "" {
			if _, err := c.q.Exec(c.ctx, `update profiles set display_name = :d where user_id = :u`,
				db.Args{"d": truncate(*in.DisplayName, 100), "u": u.ID}); err != nil {
				return err
			}
		}
		// Accepting the invitation accepts every pending membership too.
		orgs, err := db.All[string](c.ctx, c.q, `select to_jsonb(organization_id) from organization_memberships
			where user_id = :u and status = 'pending'`, db.Args{"u": u.ID})
		if err != nil {
			return err
		}
		pairs, err := c.userAppPairs(u.ID)
		if err != nil {
			return err
		}
		watch, err := c.watchAccess(pairs)
		if err != nil {
			return err
		}
		for _, org := range orgs {
			if err := c.activateMembership(u.ID, org, u.ID); err != nil {
				return err
			}
		}
		if err := c.flip(watch, "org_member_added", "org_member_removed"); err != nil {
			return err
		}
		u.Status, u.EmailVerified = "active", true
		sess, err := c.issueSession(u, []string{"pwd"}, identityID)
		if err != nil {
			return err
		}
		return c.OK(sess)
	})
}

// ── MFA (TOTP) ─────────────────────────────────────────────────────────

func (c *Call) selfOnly(id gen.UserIdOrMe) (string, error) {
	uid, err := c.selfID(id)
	if err != nil {
		return "", err
	}
	if !c.isSelf(uid) {
		return "", httpx.Forbidden("")
	}
	return uid, nil
}

func (c *Call) mfaIdentity(uid string) (identityRow, error) {
	ident, err := c.passwordIdentity(uid)
	if err == db.ErrNotFound {
		return ident, httpx.Conflict("mfa_not_applicable", "MFA applies only to password sign-in; this User has none.")
	}
	return ident, err
}

func (s *Server) AuthEnrollMfaTotp(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "auth.enrollMfaTotp", userOnly: true}, func(c *Call) error {
		uid, err := c.selfOnly(id)
		if err != nil {
			return err
		}
		ident, err := c.mfaIdentity(uid)
		if err != nil {
			return err
		}
		if ident.MFAEnabled {
			return httpx.Conflict("mfa_already_enabled", "TOTP is already enabled.")
		}
		u, err := c.findUser("u.id = :id", db.Args{"id": uid})
		if err != nil {
			return err
		}
		secret := auth.NewTOTPSecret()
		enc, err := c.s.Encrypter.Encrypt(c.ctx, secret)
		if err != nil {
			return err
		}
		if _, err := c.q.Exec(c.ctx, `update user_identities set mfa_pending_secret = :s,
			mfa_pending_expires_at = :exp::timestamptz where id = :i`,
			db.Args{"s": enc, "exp": db.Time(c.now.Add(10 * time.Minute)), "i": ident.ID}); err != nil {
			return err
		}
		return c.OK(map[string]any{"secret": secret, "otpauth_uri": auth.OTPAuthURI(secret, u.Email), "expires_in": 600})
	})
}

func (s *Server) AuthConfirmMfaTotp(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "auth.confirmMfaTotp", userOnly: true}, func(c *Call) error {
		uid, err := c.selfOnly(id)
		if err != nil {
			return err
		}
		var in struct {
			Code string `json:"code"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		ident, err := c.mfaIdentity(uid)
		if err != nil {
			return err
		}
		if ident.MFAEnabled {
			return httpx.Conflict("mfa_already_enabled", "TOTP is already enabled.")
		}
		if ident.MFAPendingSecret == nil || ident.MFAPendingExp == nil || !ident.MFAPendingExp.After(c.now) {
			return httpx.Conflict("mfa_enrollment_expired", "Start enrollment again; the pending secret expired.")
		}
		secret, err := c.s.Encrypter.Decrypt(c.ctx, *ident.MFAPendingSecret)
		if err != nil {
			return err
		}
		step, ok := auth.VerifyTOTP(secret, in.Code, c.now, 0)
		if !ok {
			return httpx.E(401, "invalid_mfa_code", "The code is wrong or was already used.")
		}
		if _, err := c.q.Exec(c.ctx, `update user_identities set mfa_enabled = true, mfa_secret = mfa_pending_secret,
			mfa_pending_secret = null, mfa_pending_expires_at = null, mfa_last_step = :s where id = :i`,
			db.Args{"s": step, "i": ident.ID}); err != nil {
			return err
		}
		codes := auth.NewRecoveryCodes(10)
		for _, code := range codes {
			h, err := auth.HashPassword(code)
			if err != nil {
				return err
			}
			if _, err := c.q.Exec(c.ctx, `insert into mfa_recovery_codes (user_identity_id, code_hash) values (:i, :h)`,
				db.Args{"i": ident.ID, "h": h}); err != nil {
				return err
			}
		}
		if err := c.audit(Audit{Action: "user.mfa_enabled", TargetType: "user", TargetID: uid, TargetUserID: &uid}); err != nil {
			return err
		}
		if u, err := c.findUser("u.id = :id", db.Args{"id": uid}); err == nil {
			c.notice(u.Email, "mfa_enabled", "Two-step verification is on", "TOTP two-step verification was just enabled on your Substratal account.")
		}
		return c.OK(map[string]any{"mfa_enabled": true, "recovery_codes": codes})
	})
}

func (s *Server) AuthDisableMfaTotp(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "auth.disableMfaTotp", userOnly: true}, func(c *Call) error {
		uid, err := c.selfOnly(id)
		if err != nil {
			return err
		}
		var in struct {
			Code         string `json:"code"`
			RecoveryCode string `json:"recovery_code"`
		}
		if err := c.Decode(&in); err != nil {
			return err
		}
		if (in.Code == "") == (in.RecoveryCode == "") {
			return httpx.Invalid("Send exactly one of code or recovery_code.")
		}
		ident, err := c.mfaIdentity(uid)
		if err != nil {
			return err
		}
		if !ident.MFAEnabled {
			return httpx.Conflict("mfa_not_enabled", "TOTP isn't enabled.")
		}
		bucket := limit{5, 15 * time.Minute}
		if n, err := c.peek("mfa-disable:"+uid, bucket); err != nil {
			return err
		} else if n >= 5 {
			return httpx.E(429, "too_many_attempts", "Too many wrong codes; try again later.").RetryAfter(900)
		}
		_, ok, err := c.verifySecondFactor(ident, in.Code, in.RecoveryCode)
		if err != nil {
			return err
		}
		if !ok {
			_ = c.hit("mfa-disable:"+uid, bucket, false)
			c.CommitAnyway()
			return httpx.E(401, "invalid_mfa_code", "The code is wrong or was already used.")
		}
		if err := c.clearMFA(ident.ID); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "user.mfa_disabled", TargetType: "user", TargetID: uid, TargetUserID: &uid}); err != nil {
			return err
		}
		if u, err := c.findUser("u.id = :id", db.Args{"id": uid}); err == nil {
			c.notice(u.Email, "mfa_disabled", "Two-step verification is off", "TOTP two-step verification was just turned off on your Substratal account.")
		}
		return c.NoContent()
	})
}

func (c *Call) clearMFA(identityID string) error {
	if _, err := c.q.Exec(c.ctx, `update user_identities set mfa_enabled = false, mfa_secret = null,
		mfa_pending_secret = null, mfa_pending_expires_at = null, mfa_last_step = null where id = :i`,
		db.Args{"i": identityID}); err != nil {
		return err
	}
	_, err := c.q.Exec(c.ctx, `delete from mfa_recovery_codes where user_identity_id = :i`, db.Args{"i": identityID})
	return err
}

func (s *Server) AuthResetMfaTotp(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe) {
	s.serve(w, r, opts{op: "auth.resetMfaTotp"}, func(c *Call) error {
		if err := c.require("users.manage"); err != nil {
			return err
		}
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		ident, err := c.mfaIdentity(uid)
		if err != nil {
			return err
		}
		if !ident.MFAEnabled {
			return httpx.Conflict("mfa_not_enabled", "TOTP isn't enabled.")
		}
		if err := c.clearMFA(ident.ID); err != nil {
			return err
		}
		if err := c.revokeSessions(uid, "admin", ""); err != nil {
			return err
		}
		if err := c.audit(Audit{Action: "user.mfa_reset", TargetType: "user", TargetID: uid, TargetUserID: &uid}); err != nil {
			return err
		}
		return c.NoContent()
	})
}

// ── Sessions ───────────────────────────────────────────────────────────

func (s *Server) AuthListSessions(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, params gen.AuthListSessionsParams) {
	s.serve(w, r, opts{op: "auth.listSessions"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		if !c.isSelf(uid) && !c.can("users.manage") {
			return httpx.Forbidden("users.manage")
		}
		if _, err := c.visibleUser(uid); err != nil {
			return err
		}
		lim := httpx.Limit(params.Limit)
		filters := httpx.Filters(c.r)
		pos, err := c.s.Cursors.Decode(deref(params.Cursor), filters)
		if err != nil {
			return err
		}
		type sess struct {
			ID         string    `json:"id"`
			CreatedAt  time.Time `json:"created_at"`
			LastSeenAt time.Time `json:"last_seen_at"`
			ExpiresAt  time.Time `json:"expires_at"`
			IPAddress  *string   `json:"ip_address"`
			UserAgent  *string   `json:"user_agent"`
			AMR        []string  `json:"amr"`
			Current    bool      `json:"current"`
		}
		args := db.Args{"u": uid, "lim": int64(lim + 1), "cur": deref(&c.p.SessionID)}
		where := ""
		if pos != nil {
			where = ` and (s.created_at, s.id) < (:ps::timestamptz, :pi)`
			args["ps"], args["pi"] = pos.Sort, pos.ID
		}
		rows, err := db.All[sess](c.ctx, c.q, `select jsonb_build_object('id', s.id, 'created_at', s.created_at,
				'last_seen_at', s.last_seen_at, 'expires_at', least(s.idle_expires_at, s.absolute_expires_at),
				'ip_address', host(s.ip_address), 'user_agent', s.user_agent, 'amr', s.amr, 'current', s.id = :cur)
			from sessions s where s.user_id = :u and s.revoked_at is null
			  and least(s.idle_expires_at, s.absolute_expires_at) > now()`+where+`
			order by s.created_at desc, s.id desc limit :lim`, args)
		if err != nil {
			return err
		}
		return c.OK(httpx.Paginate(c.s.Cursors, rows, lim, filters, func(x sess) (string, string) {
			return httpx.TimeKey(x.CreatedAt), x.ID
		}))
	})
}

func (s *Server) AuthRevokeSession(w http.ResponseWriter, r *http.Request, id gen.UserIdOrMe, sessionID string) {
	s.serve(w, r, opts{op: "auth.revokeSession"}, func(c *Call) error {
		uid, err := c.selfID(id)
		if err != nil {
			return err
		}
		reason := "logout"
		if !c.isSelf(uid) {
			if !c.can("users.manage") {
				return httpx.Forbidden("users.manage")
			}
			reason = "admin"
		}
		n, err := c.q.Exec(c.ctx, `update sessions set revoked_at = now(), revoked_reason = :r
			where id = :s and user_id = :u and revoked_at is null`, db.Args{"r": reason, "s": sessionID, "u": uid})
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.NotFoundResource("session")
		}
		return c.NoContent()
	})
}
