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
| `member_scope` | enum, nullable | Only meaningful when `source: org_seat`. `all_members` (default) \| `allowlist` \| `denylist`. See [Org-wide entitlements: scoping members in or out](#org-wide-entitlements-scoping-members-in-or-out). |
| `member_overrides` | array, nullable | Only meaningful when `member_scope` is `allowlist` or `denylist`. Array of `user_id`s this org-wide grant's default is flipped for. |

### `source`

| Value | When it's used |
|---|---|
| `purchase` | Standard commerce path — see [Workflows → Purchase → access](../../workflows/#purchase--access). |
| `trial` | Time-boxed access, no payment yet. `ends_at` drives the transition to `expired`. |
| `admin_grant` | Support/sales gave access directly — comped account, early access, internal testing. No `order_id`. |
| `org_seat` | Granted implicitly because the user belongs to an Organization that owns the app. `user_id` is null — the grant is modeled at the org level and resolved per-member at read time, per [member_scope](#org-wide-entitlements-scoping-members-in-or-out) below. |

## Org-wide entitlements: scoping members in or out

An org-wide (`source: org_seat`) Entitlement's default is that *every current and future member* of the Organization gets the app — no per-member row to create or maintain. `member_scope` narrows that default, giving an org admin ([`OrganizationMembership.role: org_admin`](../users-and-organizations/#organizationmembership)) a whitelist/exclusion list rather than all-or-nothing:

| `member_scope` | Who gets this grant |
|---|---|
| `all_members` (default) | Every current and future member — unchanged behavior. |
| `allowlist` | Only the members listed in `member_overrides`. Everyone else in the Organization does not get this app through this grant. |
| `denylist` | Every member *except* the ones listed in `member_overrides`. |

**This governs only this one Organization's own grant.** It does not reach into, and cannot revoke, a member's separate Entitlement to the same Application sourced some other way (a personal purchase, a trial, an `admin_grant`) — see [Decisions → Organization-vs-User entitlement precedence](../../decisions/#13-organization-vs-user-entitlement-precedence) for why that scoping is deliberate. A User's actual access to an app is always the union of every active path available to them; excluding someone from one Organization's seat grant only removes *that* path.

### Attribution

Reading a member's own entitlements (`GET /v1/users/{id}/entitlements`) surfaces *why* an org-sourced row is what it is, not just the resulting status — so anyone pulling a User's data can see which Organization is responsible:

```json
{
  "id": "ent_01JAGD4E5F6G7H8J9K0L1M2N3O",
  "user_id": null,
  "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "application_id": "app_invoicer",
  "status": "active",
  "source": "org_seat",
  "member_scope": "denylist",
  "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"],
  "granted_via": {
    "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
    "member_decision": "excluded"
  }
}
```

`granted_via` is computed per member at read time (it's not stored — `member_scope` + `member_overrides` on the org-wide row is the source of truth) and only appears when the caller is asking about a specific member's resolved standing, e.g. via `GET /v1/users/{id}/entitlements`, rather than the raw org-wide grant itself.

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
