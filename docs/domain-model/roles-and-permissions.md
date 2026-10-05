---
layout: default
title: Roles & Permissions
parent: Domain Model
nav_order: 3
---

# Roles & Permissions
{: .no_toc }

1. TOC
{: toc }

---

## Permission

An atomic, checkable capability, written as a dotted string key: `users.manage`, `billing.refund`, `app.timetrack.export`. Permissions are never assigned to a user directly — only through a Role. There's no standalone `Permission` resource with its own lifecycle; the catalog of valid keys is effectively static, defined by the platform (for platform-scoped keys) and by each Application (for its own `app.<slug>.*` keys).

```
GET /v1/permissions
```
returns the full known set, for building role-assignment UI — see [API Reference → Roles & Permissions](../../api-reference/roles-and-permissions/).

### Platform permission catalog

The full set of platform-scoped permission keys. App-scoped keys (`app.<slug>.*`) are defined per Application and aren't listed here — see [Applications](../applications/).

| Key | Grants |
|---|---|
| `users.list` | List/search User accounts. On an app-scoped key, confined to users with an access path to that Application. |
| `users.manage` | Create, update, suspend, delete User accounts. |
| `entitlements.manage` | Grant, toggle, and revoke Entitlements for any user. On an app-scoped key, confined to that Application. |
| `applications.manage` | Create and edit Application catalog entries. |
| `roles.manage` | Define Roles and assign/remove them on any user. Platform Roles can only be defined or assigned with permissions the caller already holds (no escalation). On an app-scoped key, confined to that Application's Roles. |
| `organizations.manage` | Manage *any* Organization: suspend/reactivate, membership, and member scope. Creating an Organization needs no permission, since any active User may. |
| `billing.manage` | View any Tenant's platform subscription and usage, and open its Stripe billing portal on the customer's behalf. See [API Reference → Billing](../../api-reference/billing/). (Orders aren't an entity of this API, so this permission is about Substratal's own subscription billing only.) |
| `billing.refund` | Issue platform-subscription refunds and credits. It's split from `billing.manage` so support can be granted refund authority separately. There's no API endpoint yet; refunds are issued in the Stripe dashboard, and this permission gates the future endpoint. |
| `audit.view` | Query the Audit log for any user. On an app-scoped key, confined to that Application's events. |
| `webhooks.manage` | Manage *any* webhook subscription, and create `platform`-scoped ones. An Application's own keys and owner manage that app's subscriptions without it. |
| `api_keys.manage` | Create, rotate, and revoke *any* API Key — see [API Keys](../../api-reference/api-keys/). An Application's owner manages that app's keys without it. |
| `tenants.manage` | View any Tenant and request a tier change on a customer's behalf — see [Tenancy](../tenancy/). Deliberately held by `support`: tier changes are gatekept by support today, not a self-service customer action, so this is the one permission a Tenant's own owner never holds, even for their own Tenant. |

### Platform role grants

The exact permission set behind each built-in [PlatformRole](#platformrole):

| Role | Permissions |
|---|---|
| `superadmin` | All of the above. |
| `support` | `users.list`, `entitlements.manage`, `audit.view`, `tenants.manage`, `billing.manage` |
| `billing_admin` | `billing.manage`, `billing.refund`, `audit.view`, `users.list` |
| `member` | None — the default on signup; every permission a `member` effectively has comes from self-service endpoints (`me`), not from a granted permission. |

These are the platform's own seed Roles, not a fixed enum — `superadmin` can define additional PlatformRoles with narrower grants (e.g. a `support_readonly` with only `users.list` and `audit.view`, no `entitlements.manage`) via `POST /v1/roles`.

## Role

A named bundle of Permissions, scoped either to the whole platform or to one Application.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `role_` prefix. |
| `name` | string | e.g. `support`, `admin`. |
| `scope` | string | `"platform"` or an `application_id`. |
| `permissions` | array of strings | Permission keys this role grants. |

### PlatformRole

Applies everywhere, and typically governs the hub itself rather than any one product. Four seed roles — `superadmin`, `support`, `billing_admin`, `member` — ship with the platform; see [Platform role grants](#platform-role-grants) above for the exact permission set each one carries.

### AppRole

Scoped to one Application (`scope` = that app's `application_id`), and defined by whoever owns that app's catalog entry via `Application.available_app_roles` — see [Applications](../applications/). The hub stores the definition and enforces the assignment; the app supplies the vocabulary and decides what each role name should be able to do inside it.

A user can hold different AppRoles across different apps they own, entirely independently — being `admin` of one app says nothing about their standing in another.

## UserRoleAssignment

The join record between a User and a Role.

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `role_id` | string | |
| `application_id` | string, nullable | Set only for an AppRole assignment; `null` for a PlatformRole. |
| `assigned_at` | timestamp | |
| `assigned_by` | object | `{ "type": "user" \| "api_key" \| "system", "id": "…" }`, the same actor shape as an [Audit Event](../orders-and-audit/#audit-event). The `member` Role assigned at signup is `system`. |

An Application's `default_app_role`, if set, is held **implicitly** by every user with active access, with no assignment row. `GET /v1/users/{id}/roles` lists it with `implicit: true`.

## Example: a user's role assignments

```json
[
  { "role_id": "role_platform_member", "application_id": null },
  { "role_id": "role_timetrack_admin", "application_id": "app_timetrack" }
]
```

Resolving this user's `effective_permissions` for `app_timetrack` means taking the permissions of every app-scoped Role they hold for `app_timetrack`, explicit and implicit: here, `role_timetrack_admin`. Platform Roles (`role_platform_member`) govern what the user can do *on the hub* and never contribute to an Application's `effective_permissions`, because platform Roles can only hold platform keys and app Roles can only hold that app's keys. See [Access Control](../../access-control/#step-2-effective-permissions) for the full algorithm, including how this interacts with the Entitlement check that has to pass first.

## This is deliberately not the same thing as Entitlement

Holding `role_timetrack_admin` grants nothing if the user's [Entitlement](../entitlements/) to `app_timetrack` isn't `active`. Roles describe what a user could do; Entitlement gates whether that matters right now. Don't model "remove access" as removing a Role — that loses the role assignment entirely, instead of just pausing it. Use the Entitlement toggle.
