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

{: .decision }
This entity formalizes "tenancy," named in the product pitch alongside user/settings/organization/storage (see [Home](../../#what-substratal-apps-actually-is)) but previously unspecified. See [Decisions → Tenant vs. Organization](../../decisions/#11-tenant-vs-organization) and [→ Tenancy tiers & dedicated infrastructure](../../decisions/#12-tenancy-tiers--dedicated-infrastructure) for how the open questions here were resolved.

## What it represents

A **Tenant** is the infrastructure-placement and data-isolation boundary for one of Substratal's own paying customers — concretely, whoever owns an [Application](../applications/)'s catalog entry (`owner_user_id` or `owner_organization_id`). It answers one question only: *where does this customer's data live, and how separated is it from every other customer's?* It has nothing to do with who can log in or what they can do — that's [Entitlement](../entitlements/) and [Role](../roles-and-permissions/).

Every Application owner gets a Tenant automatically, the moment they register their first Application — defaulting to the cheapest tier, `shared`. This is deliberate: "even a customer who signs up for the service as-is" already has their own protected, logically-isolated slice of data, with no action required and no cost — see [Tiers](#tiers) below. Upgrading to a more isolated tier is an explicit, paid, support-arranged step, not a different starting state.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `tnt_` prefix. |
| `owner_type` | enum | `user` \| `organization`. |
| `owner_user_id` | string, nullable | Set iff `owner_type: user`. |
| `owner_organization_id` | string, nullable | Set iff `owner_type: organization`. |
| `tier` | enum | `shared` (default) \| `isolated` \| `dedicated_region`. See [Tiers](#tiers). **Infrastructure placement** — where the data lives. |
| `plan` | enum | `starter` (default) \| `team` \| `enterprise`. See [Pricing](../../pricing/). **Commercial subscription tier** — what's being paid for. Independent of `tier`, except that `isolated`/`dedicated_region` are only offered on `enterprise` — see [Pricing → Enterprise tenancy tier options](../../pricing/#enterprise-tenancy-tier-options). |
| `region` | string, nullable | An AWS region code. Set only when `tier: dedicated_region` — `null` otherwise, including for `isolated` (same region as [Tier 0](../../deployment-architecture/#first-deployment-tier-0), just a dedicated cluster within it). |
| `status` | enum | `active` \| `migrating` \| `suspended`. `migrating` is the transitional state during a tier-change maintenance window — see [Deployment Architecture → Tenancy tiers](../../deployment-architecture/#tenancy-tiers--where-they-run). |
| `created_at` | timestamp | |

**Constraint:** exactly one of `owner_user_id` / `owner_organization_id` is set — the same mutual-exclusivity pattern already used by [Entitlement](../entitlements/#fields) (`user_id`/`organization_id`) and [Application](../applications/#fields) (`owner_user_id`/`owner_organization_id`).

### Example

```json
{
  "id": "tnt_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "owner_type": "organization",
  "owner_user_id": null,
  "owner_organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "tier": "isolated",
  "plan": "enterprise",
  "region": null,
  "status": "active",
  "created_at": "2026-04-02T10:00:00Z"
}
```

## Tiers

| Tier | What changes physically | Cost | Who it's for |
|---|---|---|---|
| `shared` | Rows live in the shared Aurora Serverless v2 cluster from [Tier 0](../../deployment-architecture/#first-deployment-tier-0), logically isolated by a Postgres Row-Level Security policy keyed on `tenant_id`. | $0 incremental — the default. | Every customer, from day one, with no action taken. |
| `isolated` | A dedicated Aurora Serverless v2 cluster (own Secrets Manager secret), same AWS account and region as Tier 0 — stronger blast-radius and noisy-neighbor separation, same Lambda codebase routed by a tenant→cluster lookup. | A full extra Aurora floor (~$45–55/month, see [Deployment Architecture](../../deployment-architecture/#illustrative-tier-0-floor-cost)) — a paid add-on, priced to at least cover it. | A customer with a real compliance or isolation requirement, not just a preference. |
| `dedicated_region` | Like `isolated`, but the dedicated cluster sits in the customer's chosen AWS region instead of the default one. | The `isolated` floor again, in a second region — the most expensive tier. | A customer with a genuine data-residency requirement (e.g. "our EU users' data must stay in the EU"). |

This is a strict ladder — `dedicated_region` implies everything `isolated` provides, plus region choice; there's no "dedicated region, still logically shared" combination.

## How a tier change happens today

There is no self-service API for this — `tenants.manage` (support/superadmin only) gates every write to a Tenant's `tier`. The flow:

1. A customer asks (support is the intake, not a self-serve button) for a tier upgrade.
2. Support calls `POST /v1/tenants/{id}/tier-change-requests` — see [API Reference → Tenancy](../../api-reference/tenancy/).
3. Support schedules a brief maintenance window, flips `status` to `migrating`, and runs the snapshot/restore cutover into the new infrastructure (new cluster, and for `dedicated_region`, a new region) described in [Deployment Architecture → Tenancy tiers](../../deployment-architecture/#tenancy-tiers--where-they-run).
4. `tier`, `region`, and `status` are updated back to `active`; an [Audit Event](../orders-and-audit/#audit-event) (`tenant.tier_changed`) records the change.

A scheduled, brief downtime window during step 3 is an accepted tradeoff at the current scale — see [Decisions → Tenancy tiers](../../decisions/#12-tenancy-tiers--dedicated-infrastructure). Automating this into a customer-triggered, zero-downtime flow is explicitly deferred until real tier-change volume — gated by revenue, not by a roadmap phase number — justifies the engineering investment and the operational risk of removing the human from the loop.

## What this does — and doesn't — isolate

**Does:** an Application's catalog entry and everything scoped to it — [Entitlement](../entitlements/), [AppProfile](../profiles/#appprofile), [AppSettings](../settings/#appsettings), and the slice of the [Audit Event](../orders-and-audit/#audit-event) log with that `application_id` set. These rows denormalize `tenant_id` from the owning Application specifically so a Postgres RLS policy (and, for `isolated`/`dedicated_region`, the physical cluster-routing lookup) can enforce it without a join on every request — see [Database Schema](../database-schema/#tenants).

**Doesn't:** a User's own global [Profile](../profiles/) or [Settings](../settings/) — those remain platform-wide, "one identity, used everywhere," regardless of which Tenant(s) the Applications they use happen to live in. It also doesn't reach any Application's *own*, separately-hosted infrastructure — see [Decisions → Tenancy tiers](../../decisions/#12-tenancy-tiers--dedicated-infrastructure) for why this is deliberately Hub-data-only.

{: .note }
This entity is scoped to **Substratal's own customers** — Application owners — not to a developer's own end customers (e.g. one big end-user Organization wanting its own carve-out inside a shared Application). Nothing here precludes extending `Tenant.owner_*` to an Entitlement-holder later; it just isn't built, because it isn't the problem this round of the spec was asked to solve. Flagging it here rather than guessing its shape speculatively.

## Relationship to everything else

An [Application](../applications/)'s `tenant_id` is set once, at creation, from its owner's Tenant (creating one with `tier: shared` if the owner doesn't have one yet). [Organization](../users-and-organizations/) is unrelated to this entity except as a possible *owner* of one — see [Decisions → Tenant vs. Organization](../../decisions/#11-tenant-vs-organization) for why these are deliberately not the same concept.
