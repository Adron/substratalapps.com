---
layout: default
title: Tenancy
parent: Domain Model
nav_order: 9
---

# Tenancy
{: .no_toc }

Where one customer's data physically lives, and how isolated it is from everyone else's. Distinct from — and often confused with — [Organization](../users-and-organizations/#organization), which is about grouping people, not placing infrastructure.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

This entity formalizes "tenancy," named in the product pitch alongside user/settings/organization/storage (see [Home](../../#what-substratal-apps-actually-is)). It's a separate concept from [Organization](../users-and-organizations/#organization); see [Tenant vs. Organization](#tenant-vs-organization).

## What it represents

A **Tenant** is the infrastructure-placement and data-isolation boundary for one of Substratal's own paying customers — concretely, whoever owns an [Application](../applications/)'s catalog entry (`owner_user_id` or `owner_organization_id`). It answers one question only: *where does this customer's data live, and how separated is it from every other customer's?* It has nothing to do with who can log in or what they can do — that's [Entitlement](../entitlements/) and [Role](../roles-and-permissions/).

Every Application owner gets a Tenant automatically, the moment they register their first Application — defaulting to the cheapest tier, `shared`, and to `plan: starter` when the owner is a User or `plan: team` when the owner is an [Organization](../users-and-organizations/#organization) (never `starter` for an Organization-owned Tenant — see [Pricing → Enforcement](../../pricing/#enforcement) for why). This is deliberate: "even a customer who signs up for the service as-is" already has their own protected, logically-isolated slice of data, with no action required and no cost — see [Tiers](#tiers) below. Upgrading to a more isolated tier is an explicit, paid, support-arranged step, not a different starting state.

{: .note }
**Most Users never touch this entity at all.** A Tenant belongs to whoever *owns an Application's catalog entry* — a comparatively small set of developers — not to every person with an account. The overwhelming common case on this platform, per [Pricing](../../pricing/)'s own framing, is someone who signs up (often directly through one of the Applications' own signup flow) purely to *use* an app via an [Entitlement](../entitlements/) — that User never creates, owns, or references a Tenant, because `tenant_id` lives on the Application and the records scoped to it, never on `User` itself.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `tnt_` prefix. |
| `owner_type` | enum | `user` \| `organization`. |
| `owner_user_id` | string, nullable | Set iff `owner_type: user`. |
| `owner_organization_id` | string, nullable | Set iff `owner_type: organization`. |
| `tier` | enum | `shared` (default) \| `isolated` \| `dedicated_region`. See [Tiers](#tiers). **Infrastructure placement** — where the data lives. |
| `plan` | enum | `starter` (default) \| `team` \| `enterprise`. See [Pricing](../../pricing/). **Commercial subscription tier** — what's being paid for. Independent of `tier`, except that `isolated`/`dedicated_region` are only offered on `enterprise` — see [Pricing → Enterprise tenancy tier options](../../pricing/#enterprise-tenancy-tier-options). |
| `region` | string | The AWS region the data lives in, always set: `us-east-1` for `shared` and `isolated` (same region as [Tier 0](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md), with `isolated` just a dedicated cluster within it), and the chosen region for `dedicated_region`. Supported regions: `us-east-1`, `us-west-2`, `ca-central-1`, `eu-west-1`, `eu-central-1`, `ap-southeast-2`. |
| `subscription_status` | enum | `none` (Starter, no Stripe subscription) \| `active` \| `trialing` \| `past_due` \| `canceled` \| `unpaid` \| `incomplete` \| `incomplete_expired` \| `paused`. Mirrors Stripe and is read-only. See [API Reference → Billing](../../api-reference/billing/). |
| `restricted` | boolean | `true` after a lapsed subscription when usage exceeds what the Tenant can drop to. Blocks new usage, never existing access. See [Pricing → Subscription lapse](../../pricing/#subscription-lapse--downgrades). |
| `status` | enum | `active` \| `migrating` \| `suspended`. `migrating` is the transitional state during a tier-change maintenance window — see [Deployment Architecture → Tenancy tiers](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md). |
| `application_count` | integer, read-only | Computed: how many Applications this Tenant owns. |
| `created_at`, `updated_at` | timestamp | |

The billing-period fields (`current_period_start`, `current_period_end`, `cancel_at_period_end`, seat counts) aren't part of the Tenant resource; they're on [`GET /v1/tenants/{id}/subscription`](../../api-reference/billing/#get-v1tenantsidsubscription). The database row holds them along with the Stripe ids (`stripe_customer_id`, `stripe_subscription_id`), which are internal and never returned by the API. See [Database Schema → tenants](../database-schema/#tenants).

`suspended` is a platform action, taken only for abuse or legal reasons, never for billing. It blocks every write to the Tenant's data *and* every app-token issuance for the Tenant's Applications, both with `403 tenant_suspended`. Reads keep working. It's set directly by `tenants.manage` through ops tooling, and isn't exposed as an API transition today.

**Constraint:** exactly one of `owner_user_id` / `owner_organization_id` is set — the same mutual-exclusivity pattern already used by [Entitlement](../entitlements/#fields) (`user_id`/`organization_id`) and [Application](../applications/#fields) (`owner_user_id`/`owner_organization_id`).

### Example

```json
{
  "id": "tnt_01JAGC3D4E5F6G7H8J9K011M2N",
  "owner_type": "organization",
  "owner_user_id": null,
  "owner_organization_id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P",
  "tier": "isolated",
  "plan": "enterprise",
  "region": "us-east-1",
  "subscription_status": "active",
  "restricted": false,
  "status": "active",
  "application_count": 3,
  "created_at": "2026-04-02T10:00:00Z",
  "updated_at": "2026-09-14T03:12:00Z"
}
```

## Tiers

| Tier | What changes physically | Cost | Who it's for |
|---|---|---|---|
| `shared` | Rows live in the shared Aurora Serverless v2 cluster from [Tier 0](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md), logically isolated by a Postgres Row-Level Security policy keyed on `tenant_id`. | $0 incremental — the default. | Every customer, from day one, with no action taken. |
| `isolated` | A dedicated Aurora Serverless v2 cluster (own Secrets Manager secret), same AWS account and region as Tier 0 — stronger blast-radius and noisy-neighbor separation, same Lambda codebase routed by a tenant→cluster lookup. | A full extra Aurora floor (~$45–55/month, see [Deployment Architecture](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md)) — a paid add-on, priced to at least cover it. | A customer with a real compliance or isolation requirement, not just a preference. |
| `dedicated_region` | Like `isolated`, but the dedicated cluster sits in the customer's chosen AWS region instead of the default one. | The `isolated` floor again, in a second region — the most expensive tier. | A customer with a genuine data-residency requirement (e.g. "our EU users' data must stay in the EU"). |

This is a strict ladder — `dedicated_region` implies everything `isolated` provides, plus region choice; there's no "dedicated region, still logically shared" combination.

## How a tier change happens today

There is no self-service API for this. `tenants.manage` (support/superadmin only) is required even to *request* a tier change, and no public API lets a customer trigger their own migration. The flow:

1. A customer asks (support is the intake, not a self-serve button) for a tier upgrade.
2. Support calls `POST /v1/tenants/{id}/tier-change-requests` (see [API Reference → Tenancy](../../api-reference/tenancy/)), then moves the request through `pending → scheduled → in_progress → completed` (or `cancelled`) with `PATCH` as the work happens.
3. Support schedules a brief maintenance window, flips `status` to `migrating`, and runs the snapshot/restore cutover into the new infrastructure (new cluster, and for `dedicated_region`, a new region) described in [Deployment Architecture → Tenancy tiers](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md).
4. `tier`, `region`, and `status` are updated back to `active`; an [Audit Event](../orders-and-audit/#audit-event) (`tenant.tier_changed`) records the change.

A brief, scheduled downtime window during step 3 is an accepted tradeoff. Tier changes are support-run and infrequent at current scale, so a snapshot/restore cutover is enough. Zero-downtime migration (logical replication) is a legitimate future upgrade once tier changes are self-serve and frequent, not a day-one requirement.

Automating this into a customer-triggered flow is deliberately deferred, and it's gated by revenue, not by a roadmap phase number. It means accepting real migration risk (a failed cutover, a narrower support safety net) that a human currently absorbs step by step. That trade only makes sense once the volume of tier-change requests justifies the engineering investment. See [Roadmap](../../roadmap/).

## What this does — and doesn't — isolate

**Does:** an Application's catalog entry and everything scoped to it — [Entitlement](../entitlements/), [AppProfile](../profiles/#appprofile), [AppSettings](../settings/#appsettings), its app-scoped [webhook subscriptions](../../api-reference/webhooks/), and the slice of the [Audit Event](../orders-and-audit/#audit-event) log with that `tenant_id` set. These rows denormalize `tenant_id` from the owning Application specifically so a Postgres RLS policy (and, for `isolated`/`dedicated_region`, the physical cluster-routing lookup) can enforce it without a join on every request — see [Database Schema](../database-schema/#tenants).

**Doesn't:** a User's own global [Profile](../profiles/) or [Settings](../settings/) — those remain platform-wide, "one identity, used everywhere," regardless of which Tenant(s) the Applications they use happen to live in. It also doesn't reach any Application's *own*, separately-hosted infrastructure (see [Trust Model → Applications are separately hosted](../../trust-model/#applications-are-separately-hosted)). Tenancy governs where *this API's own* data lives, never where an Application runs.

An Application's `tenant_id`/`tier`/`region` are exposed as static metadata on the Tenant and [Application](../applications/) resources, for the owning developer's benefit. They are deliberately **not** a live JWT claim on every request: placement is set once per Application, not computed per end user per request the way `effective_permissions` is (see [Trust Model](../../trust-model/#1-short-lived-jwt-at-launch)).

{: .note }
This entity is scoped to **Substratal's own customers** — Application owners — not to a developer's own end customers (e.g. one big end-user Organization wanting its own carve-out inside a shared Application). Nothing here precludes extending `Tenant.owner_*` to an Entitlement-holder later; it just isn't built, because it isn't the problem this round of the spec was asked to solve. Flagging it here rather than guessing its shape speculatively.

## Tenant vs. Organization

They're separate concepts, tied to different things:

- **[Organization](../users-and-organizations/#organization)** is a domain/grouping object: a company, or a group within one. It organizes which Users share admin standing and which Applications a group is granted as a whole. It carries no infrastructure meaning.
- **Tenant** is the infrastructure-placement and data-isolation boundary, tied to **the subscription**. Concretely, it belongs to whoever owns an Application's catalog entry (`owner_user_id` or `owner_organization_id`), because that's the only subscription relationship Substratal has directly. A developer's billing of their own end users is their concern, not this API's (see [Orders & Audit → Billing system of record](../orders-and-audit/#billing-system-of-record)).

In practice, a User can belong to many Organizations, even across multiple Tenants: someone building one app might also use a seat on someone else's app. But exactly one Tenant governs where a given Application's data physically lives. The two axes are also two separate columns and two separate RLS policies: `organization_id` and `tenant_id` (see [Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy)). How Organization-level access decisions interact with individual Users is a related but orthogonal question; see [Access Control → Organization vs. User precedence](../../access-control/#organization-vs-user-precedence).

## Relationship to everything else

An [Application](../applications/)'s `tenant_id` is set once, at creation, from its owner's Tenant (creating one with `tier: shared` if the owner doesn't have one yet). [Organization](../users-and-organizations/) is unrelated to this entity except as a possible *owner* of one — see [Tenant vs. Organization](#tenant-vs-organization).
