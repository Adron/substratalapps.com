---
layout: default
title: Tenancy
parent: API Reference
nav_order: 10
---

# Tenancy
{: .no_toc }

Where a Tenant's data lives, and the (currently support-only) process to change it. See [Domain Model → Tenancy](../../domain-model/tenancy/) for the full field reference.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .important }
There is no customer self-service endpoint to change a Tenant's `tier` or `region` today. Every write below requires `tenants.manage` (support/superadmin only) — see [Decisions → Tenancy tiers & dedicated infrastructure](../../decisions/#12-tenancy-tiers--dedicated-infrastructure) for why this is gatekept rather than exposed.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/tenants` | List Tenants. Requires `tenants.manage`. |
| `GET` | `/v1/tenants/{id}` | Fetch one Tenant. Its owner, or `tenants.manage`. |
| `POST` | `/v1/tenants/{id}/tier-change-requests` | Request a tier change on a customer's behalf. Requires `tenants.manage` — this is support placing the request, not the customer. |
| `GET` | `/v1/tenants/{id}/tier-change-requests` | List tier-change requests for a Tenant. Its owner, or `tenants.manage`. |

## `GET /v1/tenants/{id}`

```json
// Response — 200
{
  "id": "tnt_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "owner_type": "organization",
  "owner_user_id": null,
  "owner_organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "tier": "isolated",
  "region": null,
  "status": "active",
  "created_at": "2026-04-02T10:00:00Z"
}
```

A Tenant's own owner can always read this — it's useful for a developer to confirm their own placement — but can't write to it; see the `important` callout above.

## `POST /v1/tenants/{id}/tier-change-requests`

```json
// Request
{
  "requested_tier": "dedicated_region",
  "requested_region": "eu-west-1",
  "reason": "customer requires EU data residency, see SFDC-4821"
}
```

```json
// Response — 201
{
  "id": "tcr_01JAGE5F6G7H8J9K0L1M2N3O4P",
  "tenant_id": "tnt_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "requested_tier": "dedicated_region",
  "requested_region": "eu-west-1",
  "status": "pending",
  "requested_by": "usr_01JAG9SUPPORT0000000000000",
  "created_at": "2026-10-04T15:00:00Z",
  "completed_at": null
}
```

Requires `tenants.manage`. Creating this does not itself start the migration — it's the audit trail of the request; see [Domain Model → Tenancy → How a tier change happens today](../../domain-model/tenancy/#how-a-tier-change-happens-today) for the (currently manual, ops-run) steps that follow, and [Deployment Architecture → Migration mechanics](../../deployment-architecture/#migration-mechanics) for what those steps actually do. `status` moves `pending → in_progress → completed` (or `cancelled`) as ops works the request; the Tenant's own `status` flips to `migrating` for the `in_progress` duration — see [Tenant → `status`](../../domain-model/tenancy/#fields).

## Errors specific to this resource

| Code | When |
|---|---|
| `tenant_not_found` | `{id}` doesn't resolve. |
| `tier_change_already_pending` | `POST` would create a second open request while one is already `pending` or `in_progress` for this Tenant. |
| `invalid_tier_transition` | e.g. requesting `shared` as a downgrade target — not supported; moving to a cheaper tier is a new Tenant assignment handled directly by ops, not a modeled transition, since it implies deleting dedicated infrastructure. |
