---
layout: default
title: Entitlements
parent: Domain Model
nav_order: 4
---

# Entitlements
{: .no_toc }

The record behind "turn an app on/off for a user." If this site has one load-bearing entity, it's this one.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## What it represents

The join between a User (or Organization) and an Application: does this party own this app, and is it currently switched on. Independent of [Role](../roles-and-permissions/) — see [Access Control](../../access-control/) for why that split matters.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `ent_` prefix. |
| `user_id` | string, nullable | Set unless this is an org-wide seat grant. |
| `organization_id` | string, nullable | Set for an org-wide grant — see [`source: org_seat`](#source) below. |
| `application_id` | string | |
| `status` | enum | `active` \| `disabled` \| `expired` \| `revoked`. See [Access Control → Step 1](../../access-control/#step-1-entitlement-status). |
| `source` | enum | `purchase` \| `trial` \| `admin_grant` \| `org_seat`. |
| `order_id` | string, nullable | Set when `source: purchase` — links to the [Order](../orders-and-audit/). |
| `starts_at` | timestamp | |
| `ends_at` | timestamp, nullable | Set for trials and fixed-term subscriptions. |
| `disabled_reason` | string, nullable | Free text or enum, set when an admin flips `status` to `disabled` manually. |

### `source`

| Value | When it's used |
|---|---|
| `purchase` | Standard commerce path — see [Workflows → Purchase → access](../../workflows/#purchase--access). |
| `trial` | Time-boxed access, no payment yet. `ends_at` drives the transition to `expired`. |
| `admin_grant` | Support/sales gave access directly — comped account, early access, internal testing. No `order_id`. |
| `org_seat` | Granted implicitly because the user belongs to an Organization that owns the app. `user_id` may be null if the grant is modeled at the org level and resolved per-member at read time — see [Decisions → Organizations](../../decisions/#2-organizations). |

## Status transitions

```
            ┌─────────────┐
 grant ───► │   active    │ ◄────────┐
            └──────┬──────┘          │ admin re-enables
                    │ admin disables │
                    ▼                │
            ┌─────────────┐          │
            │  disabled   ├──────────┘
            └─────────────┘

active ──(ends_at passes)──► expired   (trial/subscription lapse)
active ──(refund / ToS)────► revoked   (ownership itself removed)
```

`disabled` is the soft, reversible toggle — support flips it back to `active` and everything (Roles, AppProfile, AppSettings for that app) is exactly as it was. `revoked` means the underlying ownership is gone; restoring access later means a brand-new Entitlement, not reinstating this one.

## This is the endpoint everything else points back to

```
PATCH /v1/entitlements/{id}
{ "status": "disabled", "disabled_reason": "billing_dispute" }
```

is the entire "turn this app off for this user" feature. See [API Reference → Entitlements](../../api-reference/entitlements/) for the full endpoint list, and [Workflows → Admin turns an app off for a user](../../workflows/#admin-turns-an-app-off-for-a-user) for the end-to-end sequence including the Audit Event and webhook it triggers.

## Example

```json
{
  "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "organization_id": null,
  "application_id": "app_timetrack",
  "status": "active",
  "source": "purchase",
  "order_id": "ord_01JAG5D1C2E3F4G5H6J7K8L9M0",
  "starts_at": "2026-01-14T18:05:00Z",
  "ends_at": null,
  "disabled_reason": null
}
```
