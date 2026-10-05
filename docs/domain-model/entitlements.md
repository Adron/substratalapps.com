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
| `order_id` | string, nullable | An opaque reference into the *developer's own* billing system, typically set when `source: purchase`. Up to 255 characters. It isn't a resource of this API; see [Orders & Audit → Order](../orders-and-audit/#order). It can be set once and is then immutable. |
| `starts_at` | timestamp | |
| `ends_at` | timestamp, nullable | Set for trials and fixed-term subscriptions. |
| `disabled_reason` | string, nullable | Why the row last left `active`. It's required on any transition to `disabled` or `revoked` (for example `billing_dispute`, `refunded`, `chargeback`, `tos_violation`), and cleared automatically on re-enable. |
| `member_scope` | enum, nullable | Only meaningful when `source: org_seat`. `all_members` (default) \| `allowlist` \| `denylist`. See [Org-wide entitlements: scoping members in or out](#org-wide-entitlements-scoping-members-in-or-out). |
| `member_overrides` | array, nullable | Only meaningful when `member_scope` is `allowlist` or `denylist`. Array of `user_id`s (up to 1,000, all current members) this org-wide grant's default is flipped for. |
| `test_mode` | boolean | Created by a test-mode key. |
| `created_at`, `updated_at` | timestamp | `updated_at` changes on every write. Webhook consumers use it to discard out-of-order events. |

**Uniqueness:** at most one *live* (`active` or `disabled`) personal row per (user, Application), and one live org row per (Organization, Application). `expired` and `revoked` rows are history and don't block a new grant.

### `source`

| Value | When it's used |
|---|---|
| `purchase` | Standard commerce path — see [Workflows → Purchase → access](../../workflows/#purchase--access). |
| `trial` | Time-boxed access, no payment yet. `ends_at` drives the transition to `expired`. |
| `admin_grant` | Support/sales gave access directly — comped account, early access, internal testing. Usually no `order_id`. |
| `org_seat` | Granted to an Organization as a whole. Members get access through it because they belong to that Organization, which *holds a grant* to the app. It doesn't have to own the app. `user_id` is null — the grant is modeled at the org level and resolved per-member at read time, per [member_scope](#org-wide-entitlements-scoping-members-in-or-out) below. |

## Org-wide entitlements: scoping members in or out

An org-wide (`source: org_seat`) Entitlement's default is that *every current and future member* of the Organization gets the app — no per-member row to create or maintain. `member_scope` narrows that default, giving an org admin ([`OrganizationMembership.role: org_admin`](../users-and-organizations/#organizationmembership)) a whitelist/exclusion list rather than all-or-nothing:

| `member_scope` | Who gets this grant |
|---|---|
| `all_members` (default) | Every current and future member — unchanged behavior. |
| `allowlist` | Only the members listed in `member_overrides`. Everyone else in the Organization does not get this app through this grant. |
| `denylist` | Every member *except* the ones listed in `member_overrides`. |

**This governs only this one Organization's own grant.** It does not reach into, and cannot revoke, a member's separate Entitlement to the same Application sourced some other way (a personal purchase, a trial, an `admin_grant`) — see [Access Control → Organization vs. User precedence](../../access-control/#organization-vs-user-precedence) for why that scoping is deliberate. A User's actual access to an app is always the union of every active path available to them; excluding someone from one Organization's seat grant only removes *that* path.

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
                      grant (starts_at ≤ now)
                               │
                               ▼
            admin disables ┌────────┐ ends_at passes (system)
         ┌─────────────────│ active │──────────────────────┐
         ▼                 └────────┘                       ▼
    ┌──────────┐ re-enable    ▲  ▲  renew (new ends_at)  ┌─────────┐
    │ disabled │──────────────┘  └───────────────────────│ expired │
    └──────────┘                                         └─────────┘
         │              active / disabled / expired           │
         └───────────────────────┬────────────────────────────┘
                                 ▼  refund, chargeback, ToS
                            ┌─────────┐
                            │ revoked │  terminal — grant a new Entitlement instead
                            └─────────┘
```

| From | To | Who | Notes |
|---|---|---|---|
| (new) | `active` | `entitlements.manage` (platform or the app's own key), the app's owner (org grants) | A `starts_at` in the future is allowed. The row is `active`, but access doesn't resolve until `starts_at`. |
| `active` | `disabled` | `entitlements.manage`; an org admin for their own Organization's `org_seat` row | Reversible. `disabled_reason` required. |
| `disabled` | `active` | same | Restores exactly the prior state. |
| `active` | `expired` | system | `ends_at` passed. A 5-minute sweep writes it, and access resolution treats `ends_at ≤ now` as expired immediately. |
| `expired` | `active` | `entitlements.manage` | Renewal, with a new future `ends_at` or `null`. |
| `active` / `disabled` / `expired` | `revoked` | `entitlements.manage` | Terminal. `disabled_reason` required. |

`disabled` is the soft, reversible toggle: support flips it back to `active`, and everything (Roles, AppProfile, AppSettings for that app) is exactly as it was. `revoked` means the underlying ownership is gone, so restoring access later means a brand-new Entitlement, not reinstating this one. A hard `DELETE` is reserved for removing a grant made in error. It's limited to rows without `order_id` that are less than 24 hours old. See [API Reference → Entitlements](../../api-reference/entitlements/#delete-v1entitlementsid).

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
  "order_id": "sub_1Q2w3E4r5T6y7U8i",
  "starts_at": "2026-01-14T18:05:00Z",
  "ends_at": null,
  "disabled_reason": null
}
```
