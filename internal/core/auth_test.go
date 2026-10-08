package core_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"crypto/rsa"

	"github.com/CompositeCode/substratalapps.com/internal/auth"
	"github.com/CompositeCode/substratalapps.com/internal/testenv"
)

func TestSignupLoginRefreshLogout(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()

	me := e.Get("/v1/users/me", u.Token).Expect(200)
	if me.Str("id") != u.ID || me.JSON()["email_verified"] != false {
		t.Fatalf("me = %s", me.Body)
	}
	if _, ok := e.Mail.Last(u.Email, "email_verify"); !ok {
		t.Fatal("signup should send a verification email")
	}

	// Wrong password: one code for every failure, no enumeration.
	e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": "not the right password"}).
		ExpectErr(401, "invalid_credentials")
	e.Post("/v1/auth/login", "", map[string]any{"email": "nobody+" + testenv.Unique() + "@example.com", "password": "whatever passphrase"}).
		ExpectErr(401, "invalid_credentials")

	// Refresh rotates; presenting the old token again revokes the session.
	r1 := e.Post("/v1/auth/token/refresh", "", map[string]any{"refresh_token": u.Refresh}).Expect(200)
	e.Get("/v1/users/me", r1.Token()).Expect(200)
	e.Post("/v1/auth/token/refresh", "", map[string]any{"refresh_token": u.Refresh}).ExpectErr(401, "refresh_token_reused")
	e.Get("/v1/users/me", r1.Token()).ExpectErr(401, "session_revoked")
	e.Post("/v1/auth/token/refresh", "", map[string]any{"refresh_token": r1.Str("refresh_token")}).ExpectErr(401, "session_revoked")

	// Logout is immediate.
	s := e.Login(u.Email, u.Password)
	e.Post("/v1/auth/logout", s.Token(), map[string]any{}).Expect(204)
	e.Get("/v1/users/me", s.Token()).ExpectErr(401, "session_revoked")
}

func TestLockout(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	for i := 0; i < 5; i++ {
		e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": "wrong passphrase " + testenv.Unique()}).
			ExpectErr(401, "invalid_credentials")
	}
	r := e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": u.Password}).ExpectErr(429, "too_many_attempts")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("lockout should set Retry-After")
	}
}

func TestPasswordPolicy(t *testing.T) {
	e := testenv.New(t)
	for pw, reason := range map[string]string{"short": "too_short", "passwordpassword": "breached"} {
		r := e.Post("/v1/auth/signup", "", map[string]any{"email": "p+" + testenv.Unique() + "@example.com", "password": pw}).
			ExpectErr(422, "weak_password")
		if r.JSON()["error"].(map[string]any)["details"].(map[string]any)["reason"] != reason {
			t.Fatalf("%s: %s", pw, r.Body)
		}
	}
	addr := "same+" + testenv.Unique() + "@example.com"
	e.Post("/v1/auth/signup", "", map[string]any{"email": addr, "password": addr}).ExpectErr(422, "weak_password")
	u := e.Signup()
	e.Post("/v1/auth/signup", "", map[string]any{"email": u.Email, "password": "another long passphrase"}).ExpectErr(409, "email_taken")
}

func TestMFA(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	enroll := e.Post("/v1/users/me/mfa/totp", u.Token, map[string]any{}).Expect(200)
	secret := enroll.Str("secret")
	if !strings.HasPrefix(enroll.Str("otpauth_uri"), "otpauth://totp/") {
		t.Fatalf("uri = %s", enroll.Str("otpauth_uri"))
	}
	e.Post("/v1/users/me/mfa/totp/confirm", u.Token, map[string]any{"code": "000000"}).ExpectErr(401, "invalid_mfa_code")
	conf := e.Post("/v1/users/me/mfa/totp/confirm", u.Token, map[string]any{"code": auth.TOTPCode(secret, time.Now())}).Expect(200)
	codes := toStrings(conf.JSON()["recovery_codes"])
	if len(codes) != 10 {
		t.Fatalf("recovery codes = %d", len(codes))
	}
	if e.Get("/v1/users/me", u.Token).JSON()["mfa_enabled"] != true {
		t.Fatal("mfa_enabled should be true")
	}

	// Login now returns a challenge, not a session.
	ch := e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": u.Password}).Expect(200)
	if ch.JSON()["mfa_required"] != true || ch.Token() != "" {
		t.Fatalf("challenge = %s", ch.Body)
	}
	// The code used at confirm can't be reused in its window.
	e.Post("/v1/auth/mfa/verify", "", map[string]any{"mfa_token": ch.Str("mfa_token"),
		"code": auth.TOTPCode(secret, time.Now())}).ExpectErr(401, "invalid_mfa_code")
	sess := e.Post("/v1/auth/mfa/verify", "", map[string]any{"mfa_token": ch.Str("mfa_token"), "recovery_code": codes[0]}).Expect(200)
	e.Get("/v1/users/me", sess.Token()).Expect(200)
	// The challenge is single-use.
	e.Post("/v1/auth/mfa/verify", "", map[string]any{"mfa_token": ch.Str("mfa_token"), "recovery_code": codes[1]}).
		ExpectErr(401, "mfa_token_invalid")
	// A recovery code is single-use too.
	ch2 := e.Post("/v1/auth/login", "", map[string]any{"email": u.Email, "password": u.Password}).Expect(200)
	e.Post("/v1/auth/mfa/verify", "", map[string]any{"mfa_token": ch2.Str("mfa_token"), "recovery_code": codes[0]}).
		ExpectErr(401, "invalid_mfa_code")

	// Disable with a recovery code.
	e.Post("/v1/users/me/mfa/totp/disable", sess.Token(), map[string]any{"recovery_code": codes[2]}).Expect(204)
	e.Login(u.Email, u.Password) // a plain session again
}

func TestPasswordResetAndEmailVerification(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	e.Post("/v1/auth/email/verify", "", map[string]any{"token": e.Token(u.Email, "email_verify")}).Expect(204)
	if e.Get("/v1/users/me", u.Token).JSON()["email_verified"] != true {
		t.Fatal("email should be verified")
	}
	e.Post("/v1/auth/password/forgot", "", map[string]any{"email": u.Email}).Expect(202)
	e.Post("/v1/auth/password/forgot", "", map[string]any{"email": "ghost+" + testenv.Unique() + "@example.com"}).Expect(202)
	tok := e.Token(u.Email, "password_reset")
	e.Post("/v1/auth/password/reset", "", map[string]any{"token": tok, "new_password": "a brand new passphrase"}).Expect(204)
	e.Post("/v1/auth/password/reset", "", map[string]any{"token": tok, "new_password": "a brand new passphrase"}).
		ExpectErr(400, "token_invalid")
	e.Get("/v1/users/me", u.Token).ExpectErr(401, "session_revoked") // reset revokes every session
	e.Login(u.Email, "a brand new passphrase")
}

func TestInvitationAccept(t *testing.T) {
	e := testenv.New(t)
	addr := "invitee+" + testenv.Unique() + "@example.com"
	uid := e.Post("/v1/users", e.Admin, map[string]any{"email": addr}).Expect(201).Str("id")
	// Signing up to an invited email is refused and re-sends the invitation.
	e.Post("/v1/auth/signup", "", map[string]any{"email": addr, "password": "a long enough passphrase"}).
		ExpectErr(409, "email_taken")
	sess := e.Post("/v1/auth/invitations/accept", "", map[string]any{"token": e.Token(addr, "invitation"),
		"password": "a long enough passphrase", "display_name": "Invited Person"}).Expect(200)
	if sess.Str("user_id") != uid {
		t.Fatalf("accepted as %s, want %s", sess.Str("user_id"), uid)
	}
	me := e.Get("/v1/users/me", sess.Token()).Expect(200)
	if me.Str("status") != "active" || me.JSON()["email_verified"] != true {
		t.Fatalf("me = %s", me.Body)
	}
	if e.Get("/v1/users/me/profile", sess.Token()).Str("display_name") != "Invited Person" {
		t.Fatal("display_name not applied")
	}
}

func jwksKey(t *testing.T, e *testenv.Env, kid string) *rsa.PublicKey {
	var set struct {
		Keys []auth.JWK `json:"keys"`
	}
	e.Get("/.well-known/jwks.json", "").Expect(200).Decode(&set)
	for _, k := range set.Keys {
		if k.Kid == kid {
			n, _ := base64.RawURLEncoding.DecodeString(k.N)
			eb, _ := base64.RawURLEncoding.DecodeString(k.E)
			return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(eb).Int64())}
		}
	}
	t.Fatalf("kid %s not in JWKS", kid)
	return nil
}

func decodeJWT(t *testing.T, e *testenv.Env, tok string) map[string]any {
	parts := strings.Split(tok, ".")
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var h struct{ Kid string }
	_ = json.Unmarshal(hb, &h)
	key := jwksKey(t, e, h.Kid)
	var claims map[string]any
	if err := auth.VerifyJWT(tok, map[string]*rsa.PublicKey{h.Kid: key}, &claims); err != nil {
		t.Fatalf("app token doesn't verify against the published JWKS: %v", err)
	}
	return claims
}

func TestAppTokenAndJWKS(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	app := e.App("usr_01JAG0SYSTEM00000000000000", nil)
	e.Post("/v1/auth/app-tokens", u.Token, map[string]any{"application_id": app}).ExpectErr(403, "entitlement_required")
	e.Grant(e.Admin, u.ID, app)
	r := e.Post("/v1/auth/app-tokens", u.Token, map[string]any{"application_id": app}).Expect(200)
	claims := decodeJWT(t, e, r.Str("app_token"))
	if claims["aud"] != app || claims["sub"] != u.ID || claims["entitlement_status"] != "active" || claims["org_id"] != nil {
		t.Fatalf("claims = %v", claims)
	}
	// default_app_role (member) is held implicitly, with no assignment row.
	if _, ok := claims["effective_permissions"].([]any); !ok {
		t.Fatalf("effective_permissions = %v", claims["effective_permissions"])
	}
	// API Keys can't mint app tokens.
	key := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "k", "scope": "platform", "permissions": []string{"users.list"}}).
		Expect(201).Str("secret")
	e.Post("/v1/auth/app-tokens", key, map[string]any{"application_id": app}).ExpectErr(400, "user_token_required")
	oc := e.Get("/.well-known/openid-configuration", "").Expect(200).JSON()
	if oc["token_endpoint"] == nil || oc["authorization_endpoint"] == nil {
		t.Fatalf("openid-configuration = %v", oc)
	}
}

func TestOAuthPKCE(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	redirect := "com.example.app:/oauth/callback"
	app := e.App("usr_01JAG0SYSTEM00000000000000", map[string]any{"redirect_uris": []string{redirect}})
	e.Grant(e.Admin, u.ID, app)
	verifier := strings.Repeat("v", 50) + testenv.Unique()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	e.Post("/v1/auth/oauth/authorization-codes", u.Token, map[string]any{"application_id": app,
		"redirect_uri": "https://evil.example.com/cb", "code_challenge": challenge, "code_challenge_method": "S256"}).
		ExpectErr(422, "redirect_uri_not_registered")
	code := e.Post("/v1/auth/oauth/authorization-codes", u.Token, map[string]any{"application_id": app,
		"redirect_uri": redirect, "code_challenge": challenge, "code_challenge_method": "S256", "state": "xyz"}).Expect(201)
	if !strings.Contains(code.Str("redirect_to"), "state=xyz") {
		t.Fatalf("redirect_to = %s", code.Str("redirect_to"))
	}
	// Form-encoded, as OAuth libraries send it.
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {app}, "code": {code.Str("code")},
		"code_verifier": {verifier}, "redirect_uri": {redirect}}
	tok := e.Do(testenv.Req{Method: "POST", Path: "/v1/auth/oauth/token", Body: form.Encode(),
		Header: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}).Expect(200)
	if decodeJWT(t, e, tok.Str("app_token"))["aud"] != app {
		t.Fatal("wrong audience")
	}
	// The code is single-use.
	e.Post("/v1/auth/oauth/token", "", map[string]any{"grant_type": "authorization_code", "client_id": app,
		"code": code.Str("code"), "code_verifier": verifier, "redirect_uri": redirect}).Expect(400)
	// Refresh re-checks access every time.
	ref := e.Post("/v1/auth/oauth/token", "", map[string]any{"grant_type": "refresh_token", "client_id": app,
		"refresh_token": tok.Str("refresh_token")}).Expect(200)
	ent := e.Get("/v1/users/"+u.ID+"/entitlements", e.Admin).JSON()["data"].([]any)[0].(map[string]any)["id"].(string)
	e.Patch("/v1/entitlements/"+ent, e.Admin, map[string]any{"status": "disabled", "disabled_reason": "test"}).Expect(200)
	denied := e.Post("/v1/auth/oauth/token", "", map[string]any{"grant_type": "refresh_token", "client_id": app,
		"refresh_token": ref.Str("refresh_token")}).Expect(400)
	if denied.Str("error") != "access_denied" {
		t.Fatalf("refresh after revoke = %s", denied.Body)
	}
}

func TestSessions(t *testing.T) {
	e := testenv.New(t)
	u := e.Signup()
	other := e.Login(u.Email, u.Password)
	list := e.Get("/v1/users/me/sessions", u.Token).Expect(200).JSON()["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions = %d", len(list))
	}
	e.Delete("/v1/users/me/sessions/"+other.Str("session_id"), u.Token).Expect(204)
	e.Get("/v1/users/me", other.Token()).ExpectErr(401, "session_revoked")
	e.Get("/v1/users/me", u.Token).Expect(200)
}

func TestTestModeIsolation(t *testing.T) {
	e := testenv.New(t)
	app := e.App("usr_01JAG0SYSTEM00000000000000", nil)
	testKey := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "qa", "scope": app, "mode": "test",
		"permissions": []string{"entitlements.manage", "users.list"}}).Expect(201).Str("secret")
	if !strings.HasPrefix(testKey, "satk_test_") {
		t.Fatalf("secret = %s", testKey)
	}
	liveKey := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "live", "scope": "platform",
		"permissions": []string{"users.list", "users.manage"}}).Expect(201).Str("secret")
	addr := "qa+" + testenv.Unique() + "@example.com"
	// A test key needs users.manage to create users; use a platform test key.
	platformTest := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "qa-platform", "scope": "platform", "mode": "test",
		"permissions": []string{"users.manage", "users.list", "entitlements.manage"}}).Expect(201).Str("secret")
	tu := e.Post("/v1/users", platformTest, map[string]any{"email": addr, "status": "active", "send_invitation": false}).Expect(201)
	if tu.JSON()["test_mode"] != true {
		t.Fatalf("user = %s", tu.Body)
	}
	// A live user with the same email can coexist.
	e.Post("/v1/users", liveKey, map[string]any{"email": addr, "status": "active", "send_invitation": false}).Expect(201)
	// Live credentials never see the test user, and vice versa.
	live := e.Get("/v1/users?email="+url.QueryEscape(addr), liveKey).Expect(200).JSON()["data"].([]any)
	if len(live) != 1 || live[0].(map[string]any)["test_mode"] != false {
		t.Fatalf("live view = %v", live)
	}
	e.Get("/v1/users/"+tu.Str("id"), liveKey).ExpectErr(404, "user_not_found")
	ent := e.PostIdem("/v1/users/"+tu.Str("id")+"/entitlements", testKey, testenv.Unique(),
		map[string]any{"application_id": app, "source": "trial", "ends_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339)}).Expect(201)
	if ent.JSON()["test_mode"] != true {
		t.Fatalf("entitlement = %s", ent.Body)
	}
	// Test sign-in.
	e.Post("/v1/auth/password/forgot", "", map[string]any{"email": addr, "test_mode": true}).Expect(202)
	e.Post("/v1/auth/password/reset", "", map[string]any{"token": e.Token(addr, "password_reset"), "new_password": "test mode passphrase"}).Expect(204)
	s := e.Post("/v1/auth/login", "", map[string]any{"email": addr, "password": "test mode passphrase", "test_mode": true}).Expect(200)
	if s.Str("user_id") != tu.Str("id") {
		t.Fatal("test-mode login matched the wrong account")
	}
	claims := decodeJWT(t, e, e.Post("/v1/auth/app-tokens", s.Token(), map[string]any{"application_id": app}).Expect(200).Str("app_token"))
	if claims["test_mode"] != true {
		t.Fatalf("app token test_mode = %v", claims["test_mode"])
	}
}
