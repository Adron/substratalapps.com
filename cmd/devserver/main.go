// Command devserver runs the whole API on one port for local development
// (README → Local development): every /v1 route, /.well-known, /mcp, and
// /internal/stripe/webhook, with the webhook worker and the 5-minute sweep
// running in-process instead of SQS and EventBridge.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CompositeCode/substratalapps.com/internal/billing"
	"github.com/CompositeCode/substratalapps.com/internal/jobs"
	"github.com/CompositeCode/substratalapps.com/internal/mcp"
	"github.com/CompositeCode/substratalapps.com/internal/platform/wire"
	"github.com/CompositeCode/substratalapps.com/internal/webhooks"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	notifier := webhooks.NewLocal(ctx, base.Deliverer(), 2*time.Second)
	api := base.Server(notifier)
	apiHandler := api.Handler()

	mux := http.NewServeMux()
	mux.Handle("/", apiHandler)
	mux.Handle("/mcp", &mcp.Server{API: mcp.InProcess(apiHandler), BaseURL: "http://devserver.internal",
		Key: []byte(base.Config.CursorSecret)})
	if base.Config.StripeSecretKey != "" && base.Config.StripeWebhookSecret != "" {
		stripe := billing.New(base.Config.StripeSecretKey, base.Config.StripePrices, base.Log)
		mux.Handle("/internal/stripe/webhook", &billing.Webhook{Secret: base.Config.StripeWebhookSecret,
			Stripe: stripe, Core: api, Log: base.Log})
	}

	// The 5-minute entitlement sweep, in-process.
	runner := jobs.New(base)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = runner.Run(ctx, "entitlement-sweep")
			}
		}
	}()

	srv := &http.Server{Addr: base.Config.ListenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	base.Log.Info("devserver listening", "addr", base.Config.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
