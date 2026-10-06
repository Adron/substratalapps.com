// Package auth holds the credential primitives behind API Reference → Auth:
// password hashing and policy, TOTP, recovery codes, and JWT signing.
package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters from NFR → Authentication: m=19456 KiB, t=2, p=1.
const (
	argonMemory  = 19456
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-format Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC Argon2id hash in constant time.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when an account doesn't exist, so a login
// for an unknown email costs the same as a wrong password (no enumeration
// by timing).
var (
	dummyOnce sync.Once
	dummyHash string
)

// BurnPasswordCheck spends one Argon2id verification on nothing.
func BurnPasswordCheck(password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("timing-equalizer-password") })
	VerifyPassword(password, dummyHash)
}

//go:embed breached.txt
var breachedList string

var (
	breachedOnce sync.Once
	breached     map[string]struct{}
)

func isBreached(password string) bool {
	breachedOnce.Do(func() {
		breached = map[string]struct{}{}
		sc := bufio.NewScanner(strings.NewReader(breachedList))
		for sc.Scan() {
			line := sc.Text()
			if line == "" || strings.HasPrefix(line, "# ") {
				continue
			}
			breached[line] = struct{}{}
		}
	})
	_, ok := breached[strings.ToLower(password)]
	return ok
}

// ErrWeakPassword carries the policy reason: too_short, too_long, breached,
// or matches_email.
type ErrWeakPassword struct{ Reason string }

func (e *ErrWeakPassword) Error() string { return "weak password: " + e.Reason }

// CheckPolicy applies the password policy (Auth → signup): 12–128
// characters, not in the breached list, not equal to the email. No
// composition rules (NIST SP 800-63B).
func CheckPolicy(password, email string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < 12:
		return &ErrWeakPassword{"too_short"}
	case n > 128:
		return &ErrWeakPassword{"too_long"}
	case strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(email)):
		return &ErrWeakPassword{"matches_email"}
	case isBreached(password):
		return &ErrWeakPassword{"breached"}
	}
	return nil
}

// WeakReason extracts the policy reason from err, if it is one.
func WeakReason(err error) (string, bool) {
	var w *ErrWeakPassword
	if errors.As(err, &w) {
		return w.Reason, true
	}
	return "", false
}
