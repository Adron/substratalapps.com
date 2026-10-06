package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP is defined over HMAC-SHA-1; authenticator apps expect it.
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (Auth → mfa/verify): RFC 6238, SHA-1, 6 digits, 30 s
// step, ±1 step of drift.
const (
	totpStep   = 30
	totpDigits = 6
	totpDrift  = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh 160-bit base32 secret.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// OTPAuthURI is what the client renders as a QR code.
func OTPAuthURI(secret, email string) string {
	label := url.PathEscape("Substratal:" + email)
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=Substratal&digits=%d&period=%d", label, secret, totpDigits, totpStep)
}

// totpAt is the code for one time step.
func totpAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", code), nil
}

// TOTPCode returns the current code (tests and local tooling).
func TOTPCode(secret string, now time.Time) string {
	c, _ := totpAt(secret, now.Unix()/totpStep)
	return c
}

// VerifyTOTP checks code against the window around now. It returns the
// matched step so the caller can store it and refuse reuse: a step at or
// before lastStep is rejected even when its code is correct.
func VerifyTOTP(secret, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpStep
	for d := int64(-totpDrift); d <= totpDrift; d++ {
		s := cur + d
		if s <= lastStep {
			continue
		}
		want, err := totpAt(secret, s)
		if err != nil {
			return 0, false
		}
		if hmac.Equal([]byte(want), []byte(code)) {
			return s, true
		}
	}
	return 0, false
}

// recoveryAlphabet avoids look-alike characters.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns n codes shaped like "7hq2-kx9m-a4vd".
func NewRecoveryCodes(n int) []string {
	out := make([]string, n)
	for i := range out {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		var sb strings.Builder
		for j, c := range b {
			if j > 0 && j%4 == 0 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(c)%len(recoveryAlphabet)])
		}
		out[i] = sb.String()
	}
	return out
}

// NormalizeRecoveryCode lowercases and trims a typed recovery code.
func NormalizeRecoveryCode(c string) string {
	return strings.ToLower(strings.TrimSpace(c))
}
