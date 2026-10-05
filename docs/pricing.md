---
layout: default
title: Pricing
nav_order: 12.5
---

# Pricing
{: .no_toc }

Three subscription tiers for what Substratal Apps itself charges — the platform's own customer, in every tier below, is the **Application owner** (the developer), never their app's end users.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .decision }
Illustrative, proposed numbers — grounded in the real infrastructure costs already established in [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) and [Tenancy](../domain-model/tenancy/), not a published price list. The tiers, their structure, and how they're charged are settled; the dollar amounts are still awaiting confirmation ([DECISIONS.md #31](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#31-published-prices-and-billing-options)).

## Who pays whom, again

Keep this straight before reading tiers: [Orders & Audit → Billing system of record](../domain-model/orders-and-audit/#billing-system-of-record) establishes that Substratal Apps is not a payment processor for a developer's own end users — a developer bills *their* customers however they choose, entirely outside this API. This page is the other, previously-unaddressed direction: **what Substratal charges the developer** for the platform itself — user accounts, settings, organizations, tenancy, and storage, per [Home](../#what-substratal-apps-actually-is). Two separate billing relationships, two separate pages; [Billing system of record](../domain-model/orders-and-audit/#billing-system-of-record) covers the first, this page covers the second.

## What "seat" means here

Seats sync to Stripe once a day with no mid-period proration (see [Enforcement](#enforcement) and [Stripe catalog](#stripe-catalog)). Real-time sync with prorations was considered and not chosen: it's more accurate, but noisier for customers and means more Stripe API calls.

A **seat** is a distinct, non-test-mode User whose *resolved* access is `active` to at least one Application this Tenant owns, by a personal Entitlement or through an [Organization](../domain-model/users-and-organizations/)'s org-wide grant that includes them (see [Access Control](../access-control/#the-algorithm)). A User with access to three of the Tenant's apps is **one** seat. An org-wide grant counts every member it includes. Suspended and deleted Users don't count. It's computed directly from existing Entitlement and membership rows, with no new tracking primitive. Deliberately **provisioned, not activity-based** (not "monthly active users") — a seat count a customer can predict and budget against, consistent with [Entitlement](../domain-model/entitlements/) already being a persistent on/off state rather than a usage log.

## The three tiers

| | **Starter** | **Team** | **Enterprise** |
|---|---|---|---|
| For | A single developer (the "single-user consumer" case — this is you, building your first Application; [Team](#the-three-tiers) is where "3+ Applications" belongs) | A company with one or more Organizations under a single Tenant | A corporate account at real scale, with infrastructure requirements of its own |
| Price | **$0/month** | **$49/month** + $6/seat beyond 25 included | **From $999/month** + volume seat pricing + [tenancy tier](#enterprise-tenancy-tier-options) |
| Applications | 1 | 5 | Unlimited |
| Tenant owned by an Organization | No (the owner must be an individual) — see [Enforcement](#enforcement) | Yes | Yes |
| End-user Organizations & org-wide grants in your apps | Yes | Yes | Yes |
| Seats included | 1,000 (hard cap) | 25, then $6/seat with no cap | Negotiated (typically 500+), overage at the contract rate |
| [Tenant](../domain-model/tenancy/) tier available | `shared` only | `shared` only | **Choice of all three** — see below |
| AppRoles per Application | Up to 3 | Unlimited | Unlimited |
| [Webhook](../api-reference/webhooks/) subscriptions (per Tenant, across its apps) | 1 | 10 | Unlimited |
| Audit log hot-storage window | 30 days | 1 year | Negotiated, [Compliance](../compliance/)-driven |
| Support | Community / best-effort email | Business-hours email | Dedicated channel, custom SLA |

{: .note }
"Audit log hot-storage window" is how long an Audit Event stays in fast, directly-queryable storage — it is **not** a retention/deletion period. Every Audit Event is retained indefinitely regardless of plan; see [Non-Functional Requirements → Data retention](../non-functional-requirements/#data-retention) for the cold-storage mechanism that keeps that true without keeping every plan's hot storage the same size.

## Enforcement

Every limit in the table above is checked server-side, at write time, on the Tenant that would own the new resource — independent of whether the calling credential otherwise has permission to make the call, the same "two independent gates" pattern [API Keys → Agent keys](../api-reference/api-keys/#agent-keys--restrict_destructive) already uses for `restrict_destructive`. A platform admin's `applications.manage` lets them call `POST /v1/applications`; it does not let them push a Starter Tenant past its own cap of 1.

| Limit | Checked on | Rejected with |
|---|---|---|
| Applications | `POST /v1/applications` — counts existing Applications owned by the same Tenant | `409`, `code: "plan_limit_reached"`, `details: {"resource": "applications", "limit": 1, "current": 1}` |
| AppRoles | `POST`/`PATCH /v1/applications` (length of `available_app_roles`) and `POST /v1/roles` with an app `scope` | `409`, `code: "plan_limit_reached"`, `details: {"resource": "app_roles", "limit": 3, "current": 3}` |
| Webhooks | `POST /v1/webhooks` — counts every app-scoped subscription across the Tenant's Applications | `409`, `code: "plan_limit_reached"`, `details: {"resource": "webhooks", ...}` |
| Seats (Starter only) | Any write that would add a *new* seat: a personal or org grant, re-enabling, adding an org member under an `all_members`/`denylist` grant, or narrowing to an `allowlist` that includes new people | `409`, `code: "plan_limit_reached"`, `details: {"resource": "seats", "limit": 1000, "current": 1000}`. Adding an org member past the cap still succeeds as a *membership*, but they don't receive access through that org's grants to the capped Tenant's apps until a seat frees up or the plan is upgraded. Their `granted_via.member_decision` reads `seat_limit`. |
| Restricted Tenant | Any write above that adds usage, on a Tenant whose subscription lapsed (see [below](#subscription-lapse--downgrades)) | `402`, `code: "subscription_required"` |
| Organizations | Not a count — see below | — |

"Organizations: Not available" on Starter isn't a count limit (a Tenant has exactly one owner either way) — it's that a Starter Tenant's owner must be a User, never an Organization: `check (plan != 'starter' or owner_type = 'user')` on `tenants`, the same database-level pattern already used for [`isolated`/`dedicated_region` being Enterprise-only](#enterprise-tenancy-tier-options) — see [Database Schema → tenants](../domain-model/database-schema/#tenants). An Application's owner being an Organization auto-provisions that Organization a Tenant on `plan: team`, never `starter`, specifically so this constraint is never actually reachable as a runtime error — see [Tenancy → What it represents](../domain-model/tenancy/#what-it-represents).

## Subscription lapse & downgrades

A lapsed paid Tenant becomes `restricted` rather than suspended, so no end user is cut off because of their developer's billing problem. An Organization-owned Tenant never reverts to `starter`, which the database rejects.

| Situation | What happens |
|---|---|
| Payment fails (`past_due`) | Nothing changes for anyone. Stripe retries (Smart Retries, about 3 weeks) and emails the customer. |
| Subscription ends (`canceled`, `unpaid`, `incomplete_expired`, `paused`), and the Tenant is user-owned and its usage fits Starter | `plan` drops to `starter`. Everything keeps working within Starter's limits. |
| Subscription ends, and usage exceeds Starter, or the Tenant is Organization-owned (which Starter can't be) | `plan` stays as it was, and the Tenant becomes **`restricted: true`**. Every read, every existing grant, and every end user's access keep working, so **no end user is ever cut off by their developer's billing problem**. Anything that *adds usage* (a new Application, AppRole, or webhook subscription, or a grant that adds a seat) returns `402 subscription_required`. Resubscribing through Checkout clears it immediately. |
| Voluntary downgrade, Team → Starter | Cancel at period end in the Stripe Customer Portal. At period end, the rules above apply. `GET /v1/tenants/{id}/usage` → `fits_plans` tells the owner in advance whether they'll land on Starter or in restricted mode. |
| Enterprise → anything | Handled by sales. An Enterprise Tenant on `isolated`/`dedicated_region` can't move to a non-Enterprise plan until ops has moved it back to `shared`, because the database enforces `plan = 'enterprise' or tier = 'shared'`. |

## Stripe catalog

Starter is a Stripe Customer with no Subscription, which avoids $0 subscriptions that must be cancelled and replaced on upgrade. Team upgrades go through Stripe Checkout and self-service management through the Customer Portal; Enterprise is sales-led. Billing is monthly, USD only, and tax-exclusive with Stripe Tax.

The Products and Prices configured in Stripe. Lookup keys are what the code references, never Stripe price ids, so test and live mode can share code.

| Product | Price lookup key | Amount | Shape |
|---|---|---|---|
| Substratal Team | `team_base_monthly_usd` | $49.00 | Flat, monthly, `quantity: 1`. |
| Substratal Team | `team_seats_monthly_usd` | $0 for seats 1–25, then $6.00 per seat | `billing_scheme: tiered`, `tiers_mode: graduated`, licensed (not metered), `quantity` = seat count. |
| Substratal Enterprise | `enterprise_<tenant>_base` / `_seats` | Contract, at least $999/month base | Created per customer by sales, usually from a Stripe Quote. |
| Isolated tenancy add-on | `addon_isolated_monthly_usd` | $750.00 | Enterprise only. Added to the subscription when a tier change to `isolated` completes. |
| Dedicated-region tenancy add-on | `addon_dedicated_region_monthly_usd` | $1,500.00 | Enterprise only. Replaces the isolated add-on. |

- **Starter has no Stripe Subscription.** The Tenant has a Customer, and `plan: starter` is the absence of a paid subscription. That avoids $0 subscriptions that would need cancelling and replacing on upgrade.
- **Monthly, USD, tax-exclusive** (Stripe Tax enabled) at launch. Annual plans, a Team trial, and whether Starter requires a card on file are open questions ([DECISIONS.md #31](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#31-published-prices-and-billing-options)). They don't block the build.
- **Seat quantity** is written to the `team_seats_monthly_usd` item by a daily job (00:15 UTC) with `proration_behavior: none`. Each invoice charges for the seat count on the day before it's issued, with no mid-period proration.
- Every Stripe object carries `metadata.tenant_id`. Every Product carries `metadata.substratal_plan` (`team`/`enterprise`) or `metadata.substratal_addon`, which is how the webhook handler maps a subscription back to `plan`.

The API for all of this is [API Reference → Billing](../api-reference/billing/). The webhook handling is in [root `DEPLOYMENT.md` → Stripe Billing](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md#stripe-billing).

## Enterprise tenancy tier options

This is the pricing dimension that maps directly onto [Tenant.tier](../domain-model/tenancy/#fields) — an Enterprise customer picks how their data is physically isolated, and pays accordingly. Nothing new is invented here; these are the exact three tiers [Tenancy → Tiers](../domain-model/tenancy/#tiers) already defines, priced:

| Tier | What it is | Price | Why this number |
|---|---|---|---|
| `shared` | Included in the Enterprise base price | $0 extra | Same infrastructure as every other tenant, logically isolated by Row-Level Security — see [Tenancy → Tiers](../domain-model/tenancy/#tiers). Most Enterprise customers start here; it's the volume/SLA terms that justify the Enterprise price, not the infrastructure. |
| `isolated` | A dedicated Aurora Serverless v2 cluster, same region | **+$750/month** | The raw infrastructure add-on is ~$45–55/month (see [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) — the rest of this price is the value of real blast-radius isolation and the operational overhead of running and monitoring a dedicated cluster, not a cost pass-through. |
| `dedicated_region` | A dedicated cluster in the customer's chosen AWS region | **+$1,500/month** | Roughly double `isolated`'s surcharge, matching [Tenancy → Tiers](../domain-model/tenancy/#tiers): "the `isolated` floor again, in a second region" — plus the compliance value of a genuine data-residency guarantee, the kind [Compliance → GDPR](../compliance/#gdpr-and-ccpa--build-for-it-now) data residency conversations actually ask for. |

A tier change still goes through the exact process in [Tenancy → How a tier change happens today](../domain-model/tenancy/#how-a-tier-change-happens-today) — support-run, scheduled maintenance window, `tenants.manage`-gated. Pricing doesn't change that; choosing `isolated` on this page is the commercial side of the same support conversation, not a self-service toggle.

## Why these three, and not something finer-grained

Three tiers is deliberately the whole menu:

- **Starter is free, not just cheap.** [Growth trajectory](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)'s first horizon — dozens of users, a handful of Applications — sits entirely inside Starter's limits, and the shared-tier infrastructure cost of serving that is near-zero against the floor already being paid regardless (see [Deployment Architecture → Illustrative Tier 0 floor cost](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)). Charging for it would tax exactly the adoption this product needs most right now.
- **Team's per-seat price is value-based, not cost-plus.** The marginal infrastructure cost of one more seat on shared `Tier 0` is effectively zero until a [Scale-out](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) trigger fires — $6/seat is priced against what Team replaces (weeks of building user/org/settings/tenancy infrastructure, per [Home](../#what-substratal-apps-actually-is)), not against AWS's bill for that seat.
- **Enterprise is the only tier where infrastructure choice is a pricing lever**, because it's the only tier where a customer's own requirement (compliance, data residency) drives a real, named infrastructure cost — see [Enterprise tenancy tier options](#enterprise-tenancy-tier-options) above. Below Enterprise, that choice isn't offered at all, which is itself a deliberate simplification: a Starter or Team customer who needs `isolated`/`dedicated_region` has outgrown those tiers by definition.

## How the subscription is charged

The platform subscription is processed through **Stripe Billing**. This is strictly Substratal's own direct customer relationship: the Application owner paying for Starter/Team/Enterprise. It never covers a developer's own end-user billing, which stays [entirely outside this API](../domain-model/orders-and-audit/#billing-system-of-record).

Stripe was chosen because adopting it changes nothing else in this spec. PCI scope stays minimal by design: card data never touches this API, and Stripe is PCI-DSS Level 1 (see [Compliance](../compliance/#what-this-doesnt-cover)). A subscription-billing product used only for Substratal's own customers is also a far smaller integration than the marketplace payment-processor role this API explicitly doesn't take on. Stripe is one of the platform's two sub-processors, alongside AWS (see [Compliance → GDPR (and CCPA)](../compliance/#gdpr-and-ccpa--build-for-it-now)).

The implementation (object mapping, the `tenants` schema additions, webhook event handling) is specified in [root `DEPLOYMENT.md` → Stripe Billing](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md#stripe-billing), not here. It's a build/ops concern, not part of the API that Applications and their developers call.

## Keeping these numbers honest

The MCP servers researched alongside this pricing work are the ones that keep it grounded in reality rather than going stale:

- **AWS Pricing MCP Server** — verify the per-component cost assumptions in [Deployment Architecture's floor cost table](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) against live AWS pricing before this page's infrastructure-derived numbers (the `isolated`/`dedicated_region` surcharges above) are treated as current.
- **AWS Billing and Cost Management MCP Server** — once Tier 0 is live, pull actual spend and compare it against the floor-cost assumptions this page's margins are built on; its Cost Anomaly Detection integration is also the fastest way to notice an Enterprise `isolated` Tenant's real cost has drifted from the ~$45–55/month this page assumes.
- **AWS Labs Postgres MCP Server** — per-cluster health/usage data for `isolated` and `dedicated_region` Tenants specifically, informing whether a given Enterprise account's actual resource consumption still matches the tier (and price) it's on.
- **Stripe's official MCP server** — now that these prices actually flow through [Stripe Billing](#how-the-subscription-is-charged), it's the direct source for whether the Products/Prices configured in Stripe still match what this page states — the two are meant to be kept in sync by hand, the same relationship [Conventions → Machine-readable](../api-reference/conventions/#machine-readable) already describes between this site's prose and `openapi.yaml`.

None of these are wired into the product — they're the tooling recommendation for whoever maintains this page, so the numbers above get revisited against real data rather than left as a one-time guess.
