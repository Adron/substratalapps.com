// Package config reads every setting from the environment (README → Local
// development documents each one in .env.example). Nothing outside
// internal/platform branches on the environment; it only reads Config.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration.
type Config struct {
	Env string // local | test | production

	// Database. Backend "pg" uses DatabaseURL; "dataapi" uses the ARNs.
	DatabaseBackend string
	DatabaseURL     string
	DBClusterARN    string
	DBSecretARN     string
	DBName          string

	// Public identity of the API.
	Issuer          string // JWT iss, e.g. https://api.substratalapps.com
	PublicBaseURL   string // where /v1 lives, e.g. https://api.substratalapps.com
	AuthorizeURL    string // hosted sign-in page (OAuth authorization endpoint)
	DashboardURL    string // links in emails (verify, reset, invitation)
	ListenAddr      string // devserver only
	CORSOrigins     []string
	SigningKeyID    string // KMS key id/ARN for RS256 JWT signing; empty = local key
	LocalSigningKey string // path to a PEM RSA key for local signing (generated if missing)
	DataKeyID       string // KMS key id/ARN for encrypting TOTP/webhook secrets; empty = local key
	LocalDataKey    string // base64 32-byte AES key for local encryption
	CursorSecret    string // HMAC key for pagination cursors

	// Email: "smtp" (Mailpit locally), "ses", or "log".
	EmailBackend string
	SMTPAddr     string
	EmailFrom    string

	// Webhooks: SQS queue the API nudges after committing an outbox event.
	// Empty = the devserver's in-process worker polls instead.
	WebhookQueueURL string

	// Stripe.
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePrices        StripePrices

	// AppSecretARN, when set, names a Secrets Manager secret holding a JSON
	// object whose keys override the secret fields above (cursor_secret,
	// stripe_secret_key, stripe_webhook_secret, local_data_key). Lambdas
	// read secrets this way so they never sit in function configuration.
	AppSecretARN string

	// Server-side hard limits.
	RequestTimeout time.Duration
}

// StripePrices are the Stripe Price ids the catalog sync script creates.
type StripePrices struct {
	TeamBase       string
	TeamSeat       string
	IsolatedAddOn  string
	DedicatedAddOn string
}

// Load reads Config from the environment, applying local defaults.
func Load() (Config, error) {
	env := get("APP_ENV", "local")
	c := Config{
		Env:                 env,
		DatabaseBackend:     get("DATABASE_BACKEND", "pg"),
		DatabaseURL:         get("DATABASE_URL", "postgres://substratal:substratal@localhost:55432/substratal?sslmode=disable"),
		DBClusterARN:        os.Getenv("DB_CLUSTER_ARN"),
		DBSecretARN:         os.Getenv("DB_SECRET_ARN"),
		DBName:              get("DB_NAME", "substratal"),
		Issuer:              get("ISSUER", "https://api.substratalapps.com"),
		PublicBaseURL:       get("PUBLIC_BASE_URL", "http://localhost:8080"),
		AuthorizeURL:        get("AUTHORIZE_URL", "https://auth.substratalapps.com/authorize"),
		DashboardURL:        get("DASHBOARD_URL", "http://localhost:3000"),
		ListenAddr:          get("LISTEN_ADDR", ":8080"),
		CORSOrigins:         list(os.Getenv("CORS_ALLOWED_ORIGINS")),
		SigningKeyID:        os.Getenv("KMS_SIGNING_KEY_ID"),
		LocalSigningKey:     get("LOCAL_SIGNING_KEY_FILE", ".dev/signing-key.pem"),
		DataKeyID:           os.Getenv("KMS_DATA_KEY_ID"),
		LocalDataKey:        os.Getenv("LOCAL_DATA_KEY"),
		CursorSecret:        os.Getenv("CURSOR_SECRET"),
		EmailBackend:        get("EMAIL_BACKEND", "smtp"),
		SMTPAddr:            get("SMTP_ADDR", "localhost:51025"),
		EmailFrom:           get("EMAIL_FROM", "Substratal <no-reply@mail.substratalapps.com>"),
		WebhookQueueURL:     os.Getenv("WEBHOOK_QUEUE_URL"),
		StripeSecretKey:     os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePrices: StripePrices{
			TeamBase:       os.Getenv("STRIPE_PRICE_TEAM_BASE"),
			TeamSeat:       os.Getenv("STRIPE_PRICE_TEAM_SEAT"),
			IsolatedAddOn:  os.Getenv("STRIPE_PRICE_ISOLATED"),
			DedicatedAddOn: os.Getenv("STRIPE_PRICE_DEDICATED_REGION"),
		},
		AppSecretARN:   os.Getenv("APP_SECRET_ARN"),
		RequestTimeout: duration("REQUEST_TIMEOUT", 25*time.Second),
	}
	if c.Env == "production" {
		if c.DatabaseBackend == "pg" && os.Getenv("DATABASE_BACKEND") == "" {
			c.DatabaseBackend = "dataapi"
		}
		if c.EmailBackend == "smtp" && os.Getenv("EMAIL_BACKEND") == "" {
			c.EmailBackend = "ses"
		}
	}
	return c, nil
}

// SecretFetcher reads a secret's string value (Secrets Manager in AWS).
type SecretFetcher func(ctx context.Context, arn string) (string, error)

// ApplySecret overlays the JSON secret named by AppSecretARN, then checks
// that production has every secret it needs.
func (c *Config) ApplySecret(ctx context.Context, fetch SecretFetcher) error {
	if c.AppSecretARN != "" {
		raw, err := fetch(ctx, c.AppSecretARN)
		if err != nil {
			return fmt.Errorf("config: read app secret: %w", err)
		}
		var s map[string]string
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return fmt.Errorf("config: app secret isn't a JSON object: %w", err)
		}
		set := func(dst *string, key string) {
			if v, ok := s[key]; ok && v != "" {
				*dst = v
			}
		}
		set(&c.CursorSecret, "cursor_secret")
		set(&c.StripeSecretKey, "stripe_secret_key")
		set(&c.StripeWebhookSecret, "stripe_webhook_secret")
		set(&c.LocalDataKey, "local_data_key")
	}
	if c.CursorSecret == "" {
		if c.Env == "production" {
			return fmt.Errorf("config: cursor_secret is required in production")
		}
		c.CursorSecret = "local-development-cursor-secret-not-for-production"
	}
	if c.Env == "production" {
		if c.SigningKeyID == "" || c.DataKeyID == "" {
			return fmt.Errorf("config: KMS_SIGNING_KEY_ID and KMS_DATA_KEY_ID are required in production")
		}
	}
	return nil
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func list(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func duration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}
