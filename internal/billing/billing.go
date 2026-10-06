// Package billing is the Stripe side of Substratal's own platform
// subscription (DEPLOYMENT.md → Stripe Billing): Customers, Team Checkout,
// the Customer Portal, the inbound webhook, subscription sync, and the
// daily seat sync. It never touches a developer's own end-user billing.
package billing

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/stripe/stripe-go/v87"

	"github.com/Adron/substratalapps.com/internal/core"
	"github.com/Adron/substratalapps.com/internal/platform/config"
)

// Price lookup keys from Pricing → Stripe catalog. Code resolves prices
// by lookup key, never by hard-coded price id, so test and live share code.
const (
	LookupTeamBase  = "team_base_monthly_usd"
	LookupTeamSeats = "team_seats_monthly_usd"
)

// Client implements core.Billing over stripe-go.
type Client struct {
	sc     *stripe.Client
	prices config.StripePrices
	log    *slog.Logger
}

var _ core.Billing = (*Client)(nil)

// New returns a Stripe-backed Client.
func New(secretKey string, prices config.StripePrices, log *slog.Logger) *Client {
	return &Client{sc: stripe.NewClient(secretKey), prices: prices, log: log}
}

// EnsureCustomer creates the Tenant's Customer. The idempotency key makes
// the post-commit call and the retry job safe to repeat.
func (c *Client) EnsureCustomer(ctx context.Context, tenantID, email string) (string, error) {
	p := &stripe.CustomerCreateParams{Metadata: map[string]string{"tenant_id": tenantID}}
	if email != "" {
		p.Email = stripe.String(email)
	}
	p.SetIdempotencyKey("customer:" + tenantID)
	cust, err := c.sc.V1Customers.Create(ctx, p)
	if err != nil {
		return "", err
	}
	return cust.ID, nil
}

// priceIDs resolves the Team prices: configured ids win, otherwise lookup keys.
func (c *Client) priceIDs(ctx context.Context) (base, seats string, err error) {
	if c.prices.TeamBase != "" && c.prices.TeamSeat != "" {
		return c.prices.TeamBase, c.prices.TeamSeat, nil
	}
	lp := &stripe.PriceListParams{LookupKeys: []*string{stripe.String(LookupTeamBase), stripe.String(LookupTeamSeats)}}
	for p, err := range c.sc.V1Prices.List(ctx, lp).All(ctx) {
		if err != nil {
			return "", "", err
		}
		switch p.LookupKey {
		case LookupTeamBase:
			base = p.ID
		case LookupTeamSeats:
			seats = p.ID
		}
	}
	if base == "" || seats == "" {
		return "", "", fmt.Errorf("billing: Team prices not found; run the catalog sync script")
	}
	return base, seats, nil
}

// CheckoutSession starts a Team subscription in Stripe Checkout.
func (c *Client) CheckoutSession(ctx context.Context, in core.CheckoutInput) (string, time.Time, error) {
	base, seats, err := c.priceIDs(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	qty := int64(in.Seats)
	if qty < 1 {
		qty = 1
	}
	p := &stripe.CheckoutSessionCreateParams{
		Mode:                     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:                 stripe.String(in.CustomerID),
		ClientReferenceID:        stripe.String(in.TenantID),
		SuccessURL:               stripe.String(in.SuccessURL),
		CancelURL:                stripe.String(in.CancelURL),
		BillingAddressCollection: stripe.String("required"),
		AutomaticTax:             &stripe.CheckoutSessionCreateAutomaticTaxParams{Enabled: stripe.Bool(true)},
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{Price: stripe.String(base), Quantity: stripe.Int64(1)},
			{Price: stripe.String(seats), Quantity: stripe.Int64(qty)},
		},
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{
			Metadata: map[string]string{"tenant_id": in.TenantID},
		},
	}
	sess, err := c.sc.V1CheckoutSessions.Create(ctx, p)
	if err != nil {
		return "", time.Time{}, err
	}
	return sess.URL, time.Unix(sess.ExpiresAt, 0), nil
}

// PortalSession opens the Customer Portal (configured in Stripe without
// plan switching).
func (c *Client) PortalSession(ctx context.Context, customerID, returnURL string) (string, time.Time, error) {
	sess, err := c.sc.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer: stripe.String(customerID), ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	// Portal sessions are short-lived; Stripe doesn't return an expiry, so
	// report the conservative five minutes the dashboard should assume.
	return sess.URL, time.Unix(sess.Created, 0).Add(5 * time.Minute), nil
}

// Subscription fetches a subscription's current state, the only source
// the sync trusts (out-of-order webhooks are harmless that way).
func (c *Client) Subscription(ctx context.Context, id string) (core.StripeSubscription, error) {
	p := &stripe.SubscriptionRetrieveParams{}
	p.AddExpand("items.data.price.product")
	s, err := c.sc.V1Subscriptions.Retrieve(ctx, id, p)
	if err != nil {
		return core.StripeSubscription{}, err
	}
	out := core.StripeSubscription{ID: s.ID, Status: string(s.Status), CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		TenantID: s.Metadata["tenant_id"]}
	if s.Items != nil {
		for _, it := range s.Items.Data {
			if it.CurrentPeriodStart > 0 {
				ps, pe := time.Unix(it.CurrentPeriodStart, 0).UTC(), time.Unix(it.CurrentPeriodEnd, 0).UTC()
				out.PeriodStart, out.PeriodEnd = &ps, &pe
			}
			if it.Price == nil {
				continue
			}
			if it.Price.LookupKey == LookupTeamSeats || it.Price.ID == c.prices.TeamSeat {
				out.SeatItemID = it.ID
			}
			if it.Price.Product != nil {
				if plan := it.Price.Product.Metadata["substratal_plan"]; plan != "" {
					out.Plan = plan
				}
			}
		}
	}
	return out, nil
}

// SetSeats writes the seat quantity, once per Tenant per day, with no
// proration (Pricing → Stripe catalog).
func (c *Client) SetSeats(ctx context.Context, tenantID, itemID string, seats int, day string) error {
	p := &stripe.SubscriptionItemUpdateParams{Quantity: stripe.Int64(int64(seats)),
		ProrationBehavior: stripe.String("none")}
	p.SetIdempotencyKey("seat-sync:" + tenantID + ":" + day + ":" + strconv.Itoa(seats))
	_, err := c.sc.V1SubscriptionItems.Update(ctx, itemID, p)
	return err
}

// Event re-fetches an event from Stripe, for reprocessing one whose first
// attempt failed.
func (c *Client) Event(ctx context.Context, id string) (stripe.Event, error) {
	ev, err := c.sc.V1Events.Retrieve(ctx, id, nil)
	if err != nil {
		return stripe.Event{}, err
	}
	return *ev, nil
}
