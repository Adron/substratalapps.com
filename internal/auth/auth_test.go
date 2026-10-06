package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", h) || VerifyPassword("wrong", h) {
		t.Fatal("verify")
	}
	for pw, reason := range map[string]string{"short": "too_short", "jordan@example.com": "matches_email", "passwordpassword": "breached"} {
		if r, _ := WeakReason(CheckPolicy(pw, "jordan@example.com")); r != reason {
			t.Errorf("%s: %s, want %s", pw, r, reason)
		}
	}
	if CheckPolicy("a perfectly fine passphrase", "x@example.com") != nil {
		t.Fatal("a long unique passphrase passes")
	}
}

func TestTOTP(t *testing.T) {
	s := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code := TOTPCode(s, now)
	step, ok := VerifyTOTP(s, code, now, 0)
	if !ok {
		t.Fatal("current code")
	}
	if _, ok := VerifyTOTP(s, code, now, step); ok {
		t.Fatal("a code can't be reused in its window")
	}
	if _, ok := VerifyTOTP(s, TOTPCode(s, now.Add(-30*time.Second)), now, 0); !ok {
		t.Fatal("±1 step of drift")
	}
	if _, ok := VerifyTOTP(s, TOTPCode(s, now.Add(-90*time.Second)), now, 0); ok {
		t.Fatal("3 steps old is out of window")
	}
	// RFC 6238 test vector (SHA-1, T=59): 94287082 → 6 digits 287082.
	if got := TOTPCode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0)); got != "287082" {
		t.Fatalf("RFC vector = %s", got)
	}
}

func TestJWT(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	s := NewLocalSignerFromKey(key)
	tok, err := SignJWT(context.Background(), s, map[string]any{"sub": "usr_1"})
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := VerifyJWT(tok, s.PublicKeys(), &claims); err != nil || claims["sub"] != "usr_1" {
		t.Fatalf("verify = %v %v", claims, err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if VerifyJWT(tok, NewLocalSignerFromKey(other).PublicKeys(), &claims) == nil {
		t.Fatal("a token must not verify under another key")
	}
	if VerifyJWT(tok[:len(tok)-4]+"AAAA", s.PublicKeys(), &claims) == nil {
		t.Fatal("a tampered signature must not verify")
	}
}

func TestLocalEncrypter(t *testing.T) {
	e, _ := NewLocalEncrypter("")
	ct, _ := e.Encrypt(context.Background(), "whsec_x")
	pt, err := e.Decrypt(context.Background(), ct)
	if err != nil || pt != "whsec_x" {
		t.Fatalf("round trip = %q %v", pt, err)
	}
}
