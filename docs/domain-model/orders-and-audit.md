---
layout: default
title: Orders & Audit
parent: Domain Model
nav_order: 7
---

# Orders & Audit
{: .no_toc }

1. TOC
{: toc }

---

## Order

{: .decision }
Owned by billing, not this API. See [Decisions → Billing system of record](../../decisions/#4-billing-system-of-record) — the fields below are the minimum this API needs to react to, not a full commerce schema.

The record an [Entitlement](../entitlements/) traces back to when `source: purchase`.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `ord_` prefix. |
| `user_id` / `organization_id` | string | Whoever paid. |
| `application_id` | string or array | An order can cover more than one app (a bundle). |
| `status` | enum | `paid` \| `past_due` \| `cancelled` \| `refunded`. |
| `renews_at` | timestamp, nullable | Set for subscriptions. |

A billing webhook updates the linked Entitlement's `status` off of changes here — e.g. `status: refunded` drives the Entitlement to `revoked`. This API does not process payment itself; it reacts to the outcome. See [Workflows → Purchase → access](../../workflows/#purchase--access).

### Example

```json
{
  "id": "ord_01JAG5D1C2E3F4G5H6J7K8L9M0",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "status": "paid",
  "renews_at": "2026-11-14T18:05:00Z"
}
```

---

## Audit Event

An immutable record of who changed what access, when. Never edited, never deleted — including after the record it describes is itself deleted.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `evt_` prefix. |
| `actor_user_id` | string | Who made the change. System-initiated changes (e.g. a trial expiring) use the reserved id `usr_system` rather than a null or a real user. |
| `action` | string | See [Action catalog](#action-catalog) below. |
| `target_user_id` | string | Whose access/data changed. |
| `application_id` | string, nullable | Set when the action is app-scoped. |
| `before` / `after` | object | Snapshot of the changed fields, not the whole record. |
| `timestamp` | timestamp | |

### Example

```json
{
  "id": "evt_01JAG7X3P8QY1L0M9N8O7P6Q5R",
  "actor_user_id": "usr_01JAG9SUPPORT0000000000000",
  "action": "entitlement.disabled",
  "target_user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_invoicer",
  "before": { "status": "active" },
  "after": { "status": "disabled", "disabled_reason": "billing_dispute" },
  "timestamp": "2026-09-30T16:22:41Z"
}
```

### Action catalog

Every value `action` can take. Entries marked **admin-only** never fire for a self-service change — see [What triggers an Audit Event](#what-triggers-an-audit-event) below.

| Action | Fires when |
|---|---|
| `user.created` | `POST /v1/users` |
| `user.suspended` | `POST /v1/users/{id}/suspend`, or `PATCH` setting `status: suspended` — **admin-only** |
| `user.reactivated` | `PATCH` setting `status: active` on a suspended user — **admin-only** |
| `user.deleted` | `DELETE /v1/users/{id}` |
| `entitlement.granted` | An Entitlement is created or returns to `active` |
| `entitlement.disabled` | An Entitlement's `status` is set to `disabled` |
| `entitlement.revoked` | An Entitlement's `status` is set to `revoked` |
| `entitlement.expired` | An Entitlement transitions to `expired` automatically |
| `role.assigned` | `POST /v1/users/{id}/roles/{roleId}` |
| `role.removed` | `DELETE /v1/users/{id}/roles/{roleId}` |
| `profile.updated` | An admin changes another user's Profile or AppProfile — **admin-only** |
| `settings.updated` | An admin changes another user's Settings or AppSettings — **admin-only** |
| `application.created` | `POST /v1/applications` |
| `application.updated` | `PATCH /v1/applications/{id}` |
| `organization.member_added` | `POST /v1/organizations/{id}/members` |
| `organization.member_removed` | `DELETE /v1/organizations/{id}/members/{userId}` |

This list is the authoritative source for `action` values — if an endpoint's page describes a write that isn't represented here, that's a spec bug; file it the same way as any other inconsistency.

### What triggers an Audit Event

Every write to an [Entitlement](../entitlements/), every [Role](../roles-and-permissions/) assignment or removal, and every admin-initiated (not self-service) change to a user's [Profile](../profiles/) or [Settings](../settings/). Self-service changes a user makes to their own Profile/Settings are not audited at this level of detail — ordinary account activity, not an access-control event. See [Non-Functional Requirements → Audit](../../non-functional-requirements/#audit).
