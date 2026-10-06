// Command stripe-webhook is the Lambda at /internal/stripe/webhook: the one
// route whose caller isn't a Bearer holder at all, verified instead by
// Stripe's own signature (DEPLOYMENT.md → Stripe Billing).
package main

import (
	"context"
	"log"

	"github.com/Adron/substratalapps.com/internal/billing"
	"github.com/Adron/substratalapps.com/internal/platform/lambdahttp"
	"github.com/Adron/substratalapps.com/internal/platform/wire"
)

func main() {
	ctx := context.Background()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if base.Config.StripeSecretKey == "" || base.Config.StripeWebhookSecret == "" {
		log.Fatal("stripe-webhook: stripe_secret_key and stripe_webhook_secret are required")
	}
	stripe := billing.New(base.Config.StripeSecretKey, base.Config.StripePrices, base.Log)
	lambdahttp.Start(&billing.Webhook{Secret: base.Config.StripeWebhookSecret, Stripe: stripe,
		Core: base.Server(nil), Log: base.Log})
}
