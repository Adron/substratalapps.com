// Package ids generates the prefixed resource ids and credential strings
// from Conventions → IDs: a type prefix plus a ULID (26 Crockford base32
// characters). Application and Role ids are derived, not generated, and
// live with those resources.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Resource id prefixes.
const (
	User          = "usr_"
	Organization  = "org_"
	Tenant        = "tnt_"
	TierChange    = "tcr_"
	Entitlement   = "ent_"
	AuditEvent    = "evt_"
	Webhook       = "whk_"
	APIKey        = "key_"
	UserIdentity  = "uid_"
	SSOConnection = "ssc_"
	Session       = "ses_"
	WebhookEvent  = "wev_"
	Delivery      = "dlv_"
)

// Credential prefixes. Never logged, never echoed after issuance.
const (
	RefreshToken      = "rtk_"
	MFAChallenge      = "mfa_"
	AuthorizationCode = "ac_"
	EmailVerify       = "emv_"
	PasswordReset     = "pwr_"
	Invitation        = "inv_"
	WebhookSecret     = "whsec_"
	APIKeyLive        = "satk_live_"
	APIKeyTest        = "satk_test_"
	AccessTokenJTI    = "atk_"
	AppTokenJTI       = "apt_"
)

var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// New returns prefix + a new ULID.
func New(prefix string) string {
	mu.Lock()
	defer mu.Unlock()
	return prefix + ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// HasPrefix reports whether id is prefix + a well-formed ULID.
func HasPrefix(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	_, err := ulid.ParseStrict(id[len(prefix):])
	return err == nil
}

// Secret returns prefix + 26 base32 characters of fresh randomness (130
// bits), formatted like a ULID but entirely random, for bearer credentials.
func Secret(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	var u ulid.ULID
	copy(u[:], b[:])
	return prefix + u.String()
}

// Hash is the SHA-256 hex of a bearer credential: what's stored and looked
// up, never the credential itself (Conventions → IDs, credential strings).
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
