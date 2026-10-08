package wire

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/CompositeCode/substratalapps.com/internal/auth"
	"github.com/CompositeCode/substratalapps.com/internal/billing"
	"github.com/CompositeCode/substratalapps.com/internal/core"
	"github.com/CompositeCode/substratalapps.com/internal/db"
	"github.com/CompositeCode/substratalapps.com/internal/email"
	"github.com/CompositeCode/substratalapps.com/internal/httpx"
	"github.com/CompositeCode/substratalapps.com/internal/platform/config"
	"github.com/CompositeCode/substratalapps.com/internal/webhooks"
)

// Logger returns the process's structured JSON logger.
func Logger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// LoadConfig reads Config and overlays the app secret from Secrets Manager.
func LoadConfig(ctx context.Context) (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}
	err = cfg.ApplySecret(ctx, func(ctx context.Context, arn string) (string, error) {
		awsCfg, err := AWS(ctx)
		if err != nil {
			return "", err
		}
		out, err := secretsmanager.NewFromConfig(awsCfg).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &arn})
		if err != nil {
			return "", err
		}
		if out.SecretString == nil {
			return "", fmt.Errorf("secret %s has no string value", arn)
		}
		return *out.SecretString, nil
	})
	return cfg, err
}

// Signer builds the JWT signer: KMS when configured, else a local key.
func Signer(ctx context.Context, cfg config.Config) (auth.Signer, error) {
	if cfg.SigningKeyID == "" {
		return auth.NewLocalSigner(cfg.LocalSigningKey)
	}
	awsCfg, err := AWS(ctx)
	if err != nil {
		return nil, err
	}
	var published []string
	for _, k := range strings.Split(os.Getenv("KMS_PUBLISHED_KEY_IDS"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			published = append(published, k)
		}
	}
	return auth.NewKMSSigner(ctx, kms.NewFromConfig(awsCfg), cfg.SigningKeyID, published)
}

// Encrypter builds the secret encrypter: KMS when configured, else local.
func Encrypter(ctx context.Context, cfg config.Config) (auth.Encrypter, error) {
	if cfg.DataKeyID == "" {
		return auth.NewLocalEncrypter(cfg.LocalDataKey)
	}
	awsCfg, err := AWS(ctx)
	if err != nil {
		return nil, err
	}
	return auth.NewKMSEncrypter(kms.NewFromConfig(awsCfg), cfg.DataKeyID), nil
}

// Email builds the sender for EMAIL_BACKEND.
func Email(ctx context.Context, cfg config.Config) (email.Sender, error) {
	switch cfg.EmailBackend {
	case "smtp":
		return email.SMTP{Addr: cfg.SMTPAddr, From: cfg.EmailFrom}, nil
	case "ses":
		awsCfg, err := AWS(ctx)
		if err != nil {
			return nil, err
		}
		return email.SES{Client: sesv2.NewFromConfig(awsCfg), From: cfg.EmailFrom}, nil
	case "log":
		return email.Log{}, nil
	}
	return nil, fmt.Errorf("wire: unknown EMAIL_BACKEND %q", cfg.EmailBackend)
}

// Billing builds the Stripe client, or nil when no key is configured
// (checkout/portal then answer 503 billing_unavailable).
func Billing(cfg config.Config, log *slog.Logger) core.Billing {
	if cfg.StripeSecretKey == "" {
		return nil
	}
	return billing.New(cfg.StripeSecretKey, cfg.StripePrices, log)
}

// Base is everything every entry point shares.
type Base struct {
	Config    config.Config
	Log       *slog.Logger
	DB        db.DB
	Signer    auth.Signer
	Encrypter auth.Encrypter
	Email     email.Sender
}

// NewBase builds the shared dependencies.
func NewBase(ctx context.Context) (*Base, error) {
	log := Logger()
	cfg, err := LoadConfig(ctx)
	if err != nil {
		return nil, err
	}
	d, err := Database(ctx, cfg)
	if err != nil {
		return nil, err
	}
	signer, err := Signer(ctx, cfg)
	if err != nil {
		return nil, err
	}
	enc, err := Encrypter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	mail, err := Email(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Base{Config: cfg, Log: log, DB: d, Signer: signer, Encrypter: enc, Email: mail}, nil
}

// Deliverer is the webhook delivery engine.
func (b *Base) Deliverer() *webhooks.Deliverer {
	d := webhooks.New(b.DB, b.Encrypter, b.Email, b.Log)
	d.AllowPrivate = b.Config.Env != "production" && os.Getenv("WEBHOOKS_ALLOW_PRIVATE") == "true"
	return d
}

// Notifier is SQS when WEBHOOK_QUEUE_URL is set (AWS), or nil.
func (b *Base) Notifier(ctx context.Context) (core.Notifier, error) {
	if b.Config.WebhookQueueURL == "" {
		return nil, nil
	}
	awsCfg, err := AWS(ctx)
	if err != nil {
		return nil, err
	}
	return webhooks.SQSNotifier{Client: sqs.NewFromConfig(awsCfg), QueueURL: b.Config.WebhookQueueURL, Log: b.Log}, nil
}

// Server builds the API with the given notifier.
func (b *Base) Server(n core.Notifier) *core.Server {
	key := []byte(b.Config.CursorSecret)
	if raw, err := base64.StdEncoding.DecodeString(b.Config.CursorSecret); err == nil && len(raw) >= 16 {
		key = raw
	}
	core.AllowPrivateWebhookTargets = b.Config.Env != "production" && os.Getenv("WEBHOOKS_ALLOW_PRIVATE") == "true"
	return core.New(core.Deps{
		DB: b.DB, Signer: b.Signer, Encrypter: b.Encrypter, Email: b.Email, Notifier: n,
		Billing: Billing(b.Config, b.Log), Cursors: httpx.Cursors{Key: key}, Log: b.Log,
		Issuer: b.Config.Issuer, PublicBaseURL: b.Config.PublicBaseURL, AuthorizeURL: b.Config.AuthorizeURL,
		DashboardURL: b.Config.DashboardURL, CORSOrigins: b.Config.CORSOrigins,
	})
}
