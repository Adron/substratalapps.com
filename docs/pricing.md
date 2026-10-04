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
Illustrative, proposed numbers — grounded in the real infrastructure costs already established in [Deployment Architecture](../deployment-architecture/) and [Tenancy](../domain-model/tenancy/), not a published price list. See [Decisions → Platform subscription billing processor](../decisions/#15-platform-subscription-billing-processor) for what's still unconfirmed.

## Who pays whom, again

Keep this straight before reading tiers: [Decision #4](../decisions/#4-billing-system-of-record) already resolved that Substratal Apps is not a payment processor for a developer's own end users — a developer bills *their* customers however they choose, entirely outside this API. This page is the other, previously-unaddressed direction: **what Substratal charges the developer** for the platform itself — user accounts, settings, organizations, tenancy, and storage, per [Home](../#what-substratal-apps-actually-is). Two separate billing relationships, two separate pages; [Decision #4](../decisions/#4-billing-system-of-record) covers the first, this page covers the second.

## What "seat" means here

A **seat** is a distinct User holding an active [Entitlement](../domain-model/entitlements/) — personal or [Organization](../domain-model/users-and-organizations/)-granted — to any Application this Tenant owns. Computed directly from existing Entitlement rows; no new tracking primitive. Deliberately **provisioned, not activity-based** (not "monthly active users") — a seat count a customer can predict and budget against, consistent with [Entitlement](../domain-model/entitlements/) already being a persistent on/off state rather than a usage log.

## The three tiers

| | **Starter** | **Team** | **Enterprise** |
|---|---|---|---|
| For | A single developer (the "single-user consumer" case — this is you, building your own 3+ Applications) | A company with one or more Organizations under a single Tenant | A corporate account at real scale, with infrastructure requirements of its own |
| Price | **$0/month** | **$49/month** + $6/seat beyond 25 included | **From $999/month** + volume seat pricing + [tenancy tier](#enterprise-tenancy-tier-options) |
| Applications | 1 | 5 | Unlimited |
| Organizations | 0 | Unlimited | Unlimited |
| Seats included | 1,000 | 25 | Negotiated (typically 500+) |
| [Tenant](../domain-model/tenancy/) tier available | `shared` only | `shared` only | **Choice of all three** — see below |
| Custom AppRoles | Up to 3 | Unlimited | Unlimited |
| [Webhooks](../api-reference/webhooks/) | 1 subscription | 10 subscriptions | Unlimited |
| Audit log retention | 30 days | 1 year | Negotiated, [Compliance](../compliance/)-driven |
| Support | Community / best-effort email | Business-hours email | Dedicated channel, custom SLA |

## Enterprise tenancy tier options

This is the pricing dimension that maps directly onto [Tenant.tier](../domain-model/tenancy/#fields) — an Enterprise customer picks how their data is physically isolated, and pays accordingly. Nothing new is invented here; these are the exact three tiers [Decision #12](../decisions/#12-tenancy-tiers--dedicated-infrastructure) already resolved, priced:

| Tier | What it is | Price | Why this number |
|---|---|---|---|
| `shared` | Included in the Enterprise base price | $0 extra | Same infrastructure as every other tenant, logically isolated by Row-Level Security — see [Tenancy → Tiers](../domain-model/tenancy/#tiers). Most Enterprise customers start here; it's the volume/SLA terms that justify the Enterprise price, not the infrastructure. |
| `isolated` | A dedicated Aurora Serverless v2 cluster, same region | **+$750/month** | The raw infrastructure add-on is ~$45–55/month (see [Deployment Architecture](../deployment-architecture/#illustrative-tier-0-floor-cost)) — the rest of this price is the value of real blast-radius isolation and the operational overhead of running and monitoring a dedicated cluster, not a cost pass-through. |
| `dedicated_region` | A dedicated cluster in the customer's chosen AWS region | **+$1,500/month** | Roughly double `isolated`'s surcharge, matching [Tenancy → Tiers](../domain-model/tenancy/#tiers): "the `isolated` floor again, in a second region" — plus the compliance value of a genuine data-residency guarantee, the kind [Compliance → GDPR](../compliance/#gdpr-and-ccpa--build-for-it-now) data residency conversations actually ask for. |

A tier change still goes through the exact process in [Tenancy → How a tier change happens today](../domain-model/tenancy/#how-a-tier-change-happens-today) — support-run, scheduled maintenance window, `tenants.manage`-gated. Pricing doesn't change that; choosing `isolated` on this page is the commercial side of the same support conversation, not a self-service toggle.

## Why these three, and not something finer-grained

Three tiers is deliberately the whole menu:

- **Starter is free, not just cheap.** [Growth trajectory](../deployment-architecture/#growth-trajectory)'s first horizon — dozens of users, a handful of Applications — sits entirely inside Starter's limits, and the shared-tier infrastructure cost of serving that is near-zero against the floor already being paid regardless (see [Deployment Architecture → Illustrative Tier 0 floor cost](../deployment-architecture/#illustrative-tier-0-floor-cost)). Charging for it would tax exactly the adoption this product needs most right now.
- **Team's per-seat price is value-based, not cost-plus.** The marginal infrastructure cost of one more seat on shared `Tier 0` is effectively zero until a [Scale-out](../deployment-architecture/#scale-out) trigger fires — $6/seat is priced against what Team replaces (weeks of building user/org/settings/tenancy infrastructure, per [Home](../#what-substratal-apps-actually-is)), not against AWS's bill for that seat.
- **Enterprise is the only tier where infrastructure choice is a pricing lever**, because it's the only tier where a customer's own requirement (compliance, data residency) drives a real, named infrastructure cost — see [Enterprise tenancy tier options](#enterprise-tenancy-tier-options) above. Below Enterprise, that choice isn't offered at all, which is itself a deliberate simplification: a Starter or Team customer who needs `isolated`/`dedicated_region` has outgrown those tiers by definition.

## Keeping these numbers honest

The MCP servers researched alongside this pricing work are the ones that keep it grounded in reality rather than going stale:

- **AWS Pricing MCP Server** — verify the per-component cost assumptions in [Deployment Architecture's floor cost table](../deployment-architecture/#illustrative-tier-0-floor-cost) against live AWS pricing before this page's infrastructure-derived numbers (the `isolated`/`dedicated_region` surcharges above) are treated as current.
- **AWS Billing and Cost Management MCP Server** — once Tier 0 is live, pull actual spend and compare it against the floor-cost assumptions this page's margins are built on; its Cost Anomaly Detection integration is also the fastest way to notice an Enterprise `isolated` Tenant's real cost has drifted from the ~$45–55/month this page assumes.
- **AWS Labs Postgres MCP Server** — per-cluster health/usage data for `isolated` and `dedicated_region` Tenants specifically, informing whether a given Enterprise account's actual resource consumption still matches the tier (and price) it's on.

None of these are wired into the product — they're the tooling recommendation for whoever maintains this page, so the numbers above get revisited against real data rather than left as a one-time guess.
