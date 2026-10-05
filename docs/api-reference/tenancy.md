---
layout: default
title: Tenancy
parent: API Reference
nav_order: 10
---

# Tenancy
{: .no_toc }

Where a Tenant's data lives, and the support-run process to change it. See [Domain Model → Tenancy](../../domain-model/tenancy/) for the full field reference, and [Billing](../billing/) for the Tenant's platform subscription.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .important }
There's no customer self-service endpoint to change a Tenant's `tier` or `region`. Every tier write below requires `tenants.manage` (support/superadmin). A customer asks support, and support places the request on their behalf. A Tenant's own owner can **read** everything here.

## Who counts as a Tenant's owner

A Tenant is owned by either a User or an Organization. "Owner" here means the `owner_user_id` User, or any `org_admin` of `owner_organization_id`. Owners can read their Tenant, its tier-change requests, and its [billing](../billing/). Ordinary members of the owning Organization can't.

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/tenants` | any User (own Tenants) or `tenants.manage` (all) | List Tenants. |
| `GET` | `/v1/tenants/{id}` | owner, `tenants.manage`, or `billing.manage` | Fetch one Tenant. |
| `POST` | `/v1/tenants/{id}/tier-change-requests` | `tenants.manage` | Open a tier-change request on a customer's behalf. |
| `GET` | `/v1/tenants/{id}/tier-change-requests` | owner or `tenants.manage` | List a Tenant's tier-change requests. |
| `GET` | `/v1/tenants/{id}/tier-change-requests/{requestId}` | owner or `tenants.manage` | Fetch one request. |
| `PATCH` | `/v1/tenants/{id}/tier-change-requests/{requestId}` | `tenants.manage` | Schedule, start, complete, or cancel a request. |

Tenants are never created or deleted directly. One is created automatically the first time its owner gets an Application (see [Applications → `POST`](../applications/#post-v1applications)), and it persists after that.

## The Tenant object

```json
{
  "id": "tnt_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "owner_type": "organization",
  "owner_user_id": null,
  "owner_organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "tier": "isolated",
  "plan": "enterprise",
  "region": "us-east-1",
  "status": "active",
  "subscription_status": "active",
  "restricted": false,
  "application_count": 3,
  "created_at": "2026-04-02T10:00:00Z",
  "updated_at": "2026-09-14T03:12:00Z"
}
```

- `region` is always set. It's `us-east-1` for `shared` and `isolated`, and the chosen region for `dedicated_region`. Earlier drafts used `null` for "the default region". Always returning a real value means a reader never has to know what the default is.
- `subscription_status` and `restricted` mirror the Stripe-synced billing state. See [Billing](../billing/) and [Pricing → Subscription lapse](../../pricing/#subscription-lapse--downgrades).
- `status: migrating` holds for the duration of an `in_progress` tier change. While it does, reads keep working, and writes to that Tenant's data return `503 service_unavailable` with `Retry-After`.

## `GET /v1/tenants`

A non-`tenants.manage` caller gets the Tenants they own: at most one owned personally, plus one per Organization where they're `org_admin` and the Organization owns a Tenant. This is how a developer finds their own `tenant_id` without knowing it in advance. `tenants.manage` holders see every Tenant and can filter by `plan`, `tier`, `status`, `restricted`, `owner_user_id`, and `owner_organization_id`.

## `GET /v1/tenants/{id}`

Returns the Tenant object above. `404 tenant_not_found` for a caller who isn't an owner and lacks `tenants.manage`/`billing.manage`.

## `POST /v1/tenants/{id}/tier-change-requests`

{: .decision }
**Proposed — confirm** ([DECISIONS.md #27](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#27-supported-regions-for-dedicated_region)). The initial `dedicated_region` allow-list is `us-east-1`, `us-west-2`, `ca-central-1`, `eu-west-1`, `eu-central-1`, `ap-southeast-2`. Any other region returns `422 unsupported_region`. Adding a region is a support/ops decision and needs no API change.

```json
// Request
{
  "requested_tier": "dedicated_region",
  "requested_region": "eu-west-1",
  "reason": "Customer requires EU data residency; contract ref ENT-2026-014."
}
```
```json
// Response — 201
{
  "id": "tcr_01JAGE5F6G7H8J9K0L1M2N3O4P",
  "tenant_id": "tnt_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "from_tier": "isolated",
  "from_region": "us-east-1",
  "requested_tier": "dedicated_region",
  "requested_region": "eu-west-1",
  "status": "pending",
  "reason": "Customer requires EU data residency; contract ref ENT-2026-014.",
  "scheduled_for": null,
  "notes": null,
  "requested_by": "usr_01JAG9SUPPORT0000000000000",
  "created_at": "2026-10-04T15:00:00Z",
  "started_at": null,
  "completed_at": null
}
```

Validation:

- `requested_tier` must be strictly higher on the ladder `shared < isolated < dedicated_region` (`409 invalid_tier_transition`). Moving down implies tearing down dedicated infrastructure and is handled by ops directly, not as a modeled transition. Changing region *within* `dedicated_region` counts as an upgrade request.
- The Tenant's `plan` must be `enterprise` (`409 plan_does_not_allow_tier`). Upgrade the plan first; see [Billing](../billing/).
- `requested_region` is required for `dedicated_region` and must be in the supported list: `us-east-1`, `us-west-2`, `ca-central-1`, `eu-west-1`, `eu-central-1`, `ap-southeast-2` (`422 unsupported_region`). It's forbidden for `isolated`.
- `reason` is required, up to 2,000 characters.
- There can be only one open (`pending`/`scheduled`/`in_progress`) request per Tenant (`409 tier_change_already_pending`).

Creating a request doesn't start a migration. It's the record and the queue entry. Writes `tenant.tier_change_requested`. Destructive (it leads to a maintenance window), so a `restrict_destructive` key can't call it.

## `PATCH /v1/tenants/{id}/tier-change-requests/{requestId}`

```json
// Request — schedule the maintenance window
{ "status": "scheduled", "scheduled_for": "2026-10-11T06:00:00Z", "notes": "Customer confirmed Sunday 06:00 UTC window." }
```
```json
// Request — begin the cutover
{ "status": "in_progress" }
```
```json
// Request — finish
{ "status": "completed", "notes": "Snapshot restored to eu-west-1, routing flipped, smoke tests green." }
```
```json
// Response — 200, the full updated request
```

| From → to | Side effects |
|---|---|
| `pending → scheduled` | `scheduled_for` is required and must be in the future. Emails the owner the window. |
| `pending`/`scheduled → in_progress` | The Tenant's `status` becomes `migrating`. `started_at` is set. |
| `in_progress → completed` | The Tenant's `tier`/`region` become the requested values and its `status` returns to `active`. `completed_at` is set. Writes `tenant.tier_changed`. |
| `pending`/`scheduled → cancelled` | No Tenant change. `notes` is required. |
| `in_progress → cancelled` | A rollback: the Tenant's `status` returns to `active` on its *original* tier/region. `notes` is required. |

Any other transition returns `409 invalid_request_transition`. Every change writes `tenant.tier_change_request_updated`. The physical migration steps are ops runbook work. See [root `DEPLOYMENT.md` → Migration mechanics](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md). This endpoint records and gates those steps; it doesn't perform them.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `tenant_not_found` | 404 | `{id}` doesn't resolve or isn't visible. |
| `tier_change_request_not_found` | 404 | `{requestId}` doesn't resolve for this Tenant. |
| `tier_change_already_pending` | 409 | An open request already exists. |
| `invalid_tier_transition` | 409 | Not an upgrade on the ladder. |
| `plan_does_not_allow_tier` | 409 | `isolated`/`dedicated_region` on a non-Enterprise plan. |
| `unsupported_region` | 422 | `requested_region` isn't on the supported list. |
| `invalid_request_transition` | 409 | A request `status` change not in the table above. |
