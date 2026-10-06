// Command stripe-catalog creates the Stripe Products and Prices in
// Pricing → Stripe catalog, once per Stripe mode. It's idempotent: a Price
// whose lookup_key already exists is left alone, and Products use fixed
// ids, so it's safe to re-run. Point it at test mode first.
//
//	STRIPE_SECRET_KEY=sk_test_... go run ./cmd/stripe-catalog
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/stripe/stripe-go/v87"
)

type price struct {
	lookup  string
	product string
	recur   bool
	amount  int64
	tiers   []*stripe.PriceCreateTierParams
}

func main() {
	key := os.Getenv("STRIPE_SECRET_KEY")
	if key == "" {
		log.Fatal("stripe-catalog: STRIPE_SECRET_KEY is required")
	}
	if strings.HasPrefix(key, "sk_live_") && os.Getenv("CONFIRM_LIVE") != "yes" {
		log.Fatal("stripe-catalog: refusing live mode without CONFIRM_LIVE=yes")
	}
	sc := stripe.NewClient(key)
	ctx := context.Background()
	products := map[string]map[string]string{
		"substratal_team":            {"name": "Substratal Team", "substratal_plan": "team"},
		"substratal_addon_isolated":  {"name": "Isolated tenancy add-on", "substratal_addon": "isolated_tenancy"},
		"substratal_addon_dedicated": {"name": "Dedicated-region tenancy add-on", "substratal_addon": "dedicated_region_tenancy"},
		"substratal_enterprise":      {"name": "Substratal Enterprise", "substratal_plan": "enterprise"},
	}
	for id, meta := range products {
		name := meta["name"]
		delete(meta, "name")
		p := &stripe.ProductCreateParams{ID: stripe.String(id), Name: stripe.String(name), Metadata: meta}
		if _, err := sc.V1Products.Create(ctx, p); err != nil && !strings.Contains(err.Error(), "already exists") {
			log.Fatalf("product %s: %v", id, err)
		}
		fmt.Println("product", id)
	}
	prices := []price{
		{lookup: "team_base_monthly_usd", product: "substratal_team", recur: true, amount: 4900},
		{lookup: "team_seats_monthly_usd", product: "substratal_team", recur: true, tiers: []*stripe.PriceCreateTierParams{
			{UpTo: stripe.Int64(25), UnitAmount: stripe.Int64(0)},
			{UpToInf: stripe.Bool(true), UnitAmount: stripe.Int64(600)},
		}},
		{lookup: "addon_isolated_monthly_usd", product: "substratal_addon_isolated", recur: true, amount: 75000},
		{lookup: "addon_dedicated_region_monthly_usd", product: "substratal_addon_dedicated", recur: true, amount: 150000},
	}
	existing := map[string]bool{}
	lp := &stripe.PriceListParams{}
	for _, pr := range prices {
		lp.LookupKeys = append(lp.LookupKeys, stripe.String(pr.lookup))
	}
	for p, err := range sc.V1Prices.List(ctx, lp).All(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		existing[p.LookupKey] = true
	}
	for _, pr := range prices {
		if existing[pr.lookup] {
			fmt.Println("price", pr.lookup, "(exists)")
			continue
		}
		p := &stripe.PriceCreateParams{
			Currency: stripe.String("usd"), LookupKey: stripe.String(pr.lookup), Product: stripe.String(pr.product),
			Recurring: &stripe.PriceCreateRecurringParams{Interval: stripe.String("month"), UsageType: stripe.String("licensed")},
		}
		if pr.tiers != nil {
			p.BillingScheme, p.TiersMode, p.Tiers = stripe.String("tiered"), stripe.String("graduated"), pr.tiers
		} else {
			p.UnitAmount = stripe.Int64(pr.amount)
		}
		if _, err := sc.V1Prices.Create(ctx, p); err != nil {
			log.Fatalf("price %s: %v", pr.lookup, err)
		}
		fmt.Println("price", pr.lookup, "(created)")
	}
}
