package billing

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/CompositeCode/substratalapps.com/internal/core"
)

// Webhook is POST /internal/stripe/webhook (DEPLOYMENT.md → Webhook
// handling). It isn't Bearer-authenticated: it verifies Stripe-Signature
// against the raw body. Every event id is recorded first (insert-or-skip),
// so a duplicate returns 200 immediately; processing re-fetches the
// Subscription from Stripe, so ordering never matters.
type Webhook struct {
	Secret string
	Stripe *Client
	Core   *core.Server
	Log    *slog.Logger
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ev, err := webhook.ConstructEventWithOptions(body, r.Header.Get("Stripe-Signature"), h.Secret,
		webhook.ConstructEventOptions{Tolerance: 5 * time.Minute, IgnoreAPIVersionMismatch: true})
	if err != nil {
		h.Log.Warn("stripe webhook signature rejected", "err", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	fresh, err := h.Core.RecordStripeEvent(r.Context(), ev.ID, string(ev.Type))
	if err != nil {
		h.Log.Error("record stripe event", "event", ev.ID, "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if fresh {
		if err := h.Process(r.Context(), ev); err != nil {
			// Left unprocessed (processed_at null): the retry job re-fetches
			// it, and an alarm fires on events stuck unprocessed.
			h.Log.Error("process stripe event", "event", ev.ID, "type", ev.Type, "err", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// Process applies one Stripe event.
func (h *Webhook) Process(ctx context.Context, ev stripe.Event) error {
	var subID, tenantID string
	switch ev.Type {
	case "checkout.session.completed":
		var s stripe.CheckoutSession
		if err := json.Unmarshal(ev.Data.Raw, &s); err != nil {
			return err
		}
		tenantID = s.ClientReferenceID
		if s.Subscription != nil {
			subID = s.Subscription.ID
		}
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		var s stripe.Subscription
		if err := json.Unmarshal(ev.Data.Raw, &s); err != nil {
			return err
		}
		subID, tenantID = s.ID, s.Metadata["tenant_id"]
	case "invoice.paid", "invoice.payment_failed":
		var inv struct {
			Parent *struct {
				SubscriptionDetails *struct {
					Subscription string `json:"subscription"`
				} `json:"subscription_details"`
			} `json:"parent"`
			Subscription string `json:"subscription"`
		}
		if err := json.Unmarshal(ev.Data.Raw, &inv); err != nil {
			return err
		}
		subID = inv.Subscription
		if subID == "" && inv.Parent != nil && inv.Parent.SubscriptionDetails != nil {
			subID = inv.Parent.SubscriptionDetails.Subscription
		}
	default:
		return h.Core.MarkStripeEventProcessed(ctx, ev.ID, "")
	}
	if subID == "" {
		return h.Core.MarkStripeEventProcessed(ctx, ev.ID, tenantID)
	}
	sub, err := h.Stripe.Subscription(ctx, subID)
	if err != nil {
		return err
	}
	if sub.TenantID == "" {
		sub.TenantID = tenantID
	}
	if err := h.Core.ApplyStripeSubscription(ctx, sub); err != nil {
		return err
	}
	return h.Core.MarkStripeEventProcessed(ctx, ev.ID, sub.TenantID)
}
