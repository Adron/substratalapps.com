---
layout: default
title: API Keys
parent: API Reference
nav_order: 13
---

# API Keys
{: .no_toc }

How a service — billing, an Application's own backend, an internal tool — authenticates to this API without a user behind it. See [Conventions → Authentication](../conventions/#authentication) for how an API key's bearer token differs from a user access token.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## What an API Key is

A long-lived credential scoped to either one Application (for an app's own backend calling back into the hub — reading its users' effective permissions via [Roles & Permissions](../roles-and-permissions/), writing [AppProfile](../profiles/)/[AppSettings](../settings/)) or the platform as a whole (for billing, internal tooling). Unlike a user access token, it doesn't expire on a short TTL and isn't tied to a login session — it's rotated deliberately, not refreshed automatically.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `key_` prefix. |
| `name` | string | Human label, e.g. `"billing-webhook-handler"`. |
| `scope` | string | `"platform"` or an `application_id` — same shape as [Role scope](../../domain-model/roles-and-permissions/#role). |
| `permissions` | array of strings | Permission keys this key carries. For an app-scoped key, limited to that Application's own `app.<slug>.*` keys plus a fixed read-only platform permission (`entitlements.manage` is never grantable to an app-scoped key — see below). |
| `mode` | enum | `live` \| `test` — see [Test vs. live](#test-vs-live) below. Fixed at creation; a key can't switch modes, only be replaced. |
| `intended_use` | enum | `service` \| `agent` — see [Agent keys & `restrict_destructive`](#agent-keys--restrict_destructive) below. Metadata with one real effect: it sets the default for `restrict_destructive`. |
| `restrict_destructive` | boolean | Default follows `intended_use` (`true` for `agent`, `false` for `service`) unless set explicitly. See below. |
| `last_used_at` | timestamp, nullable | |
| `created_at` | timestamp | |
| `revoked_at` | timestamp, nullable | |

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/api-keys` | List keys in scope for the caller. |
| `POST` | `/v1/api-keys` | Create a key. Returns the secret value once. |
| `POST` | `/v1/api-keys/{id}/rotate` | Issue a new secret for the same key record, invalidating the old one. |
| `DELETE` | `/v1/api-keys/{id}` | Revoke permanently. |

All four require a platform role with `api_keys.manage` — creating a credential is always an admin action, never self-service, even for an Application's own key.

## `POST /v1/api-keys`

```json
// Request
{
  "name": "timetrack-backend",
  "scope": "app_timetrack",
  "permissions": ["app.timetrack.export"],
  "mode": "live",
  "intended_use": "service"
}
```
```json
// Response — 201
{
  "id": "key_01JAGE5F6G7H8J9K0L1M2N3O4P",
  "name": "timetrack-backend",
  "scope": "app_timetrack",
  "permissions": ["app.timetrack.export"],
  "mode": "live",
  "intended_use": "service",
  "restrict_destructive": false,
  "secret": "satk_live_9f2a1c7e4b3d8f0a2c5e7b1d9f3a6c8e",
  "last_used_at": null,
  "created_at": "2026-10-04T09:30:00Z",
  "revoked_at": null
}
```

`secret` is returned **only** in this response — store it immediately; it's not retrievable afterward, only rotatable. The key is used exactly like a user access token: `Authorization: Bearer satk_live_...`.

## Agent keys & `restrict_destructive`

```json
// Request — a key meant for an MCP/agent integration
{
  "name": "support-assistant-mcp",
  "scope": "platform",
  "permissions": ["audit.view", "users.list", "entitlements.manage"],
  "mode": "live",
  "intended_use": "agent"
}
```
```json
// Response — 201, restrict_destructive defaulted to true from intended_use
{
  "id": "key_01JAGF6G7H8J9K0L1M2N3O4P5Q",
  "name": "support-assistant-mcp",
  "scope": "platform",
  "permissions": ["audit.view", "users.list", "entitlements.manage"],
  "mode": "live",
  "intended_use": "agent",
  "restrict_destructive": true,
  "secret": "satk_live_...",
  "...": "..."
}
```

This key carries `entitlements.manage` — it *can* toggle an Entitlement off — but with `restrict_destructive: true`, a call that would set `status` to `disabled`/`revoked` on the [Entitlements toggle](../entitlements/) is rejected before the permission check even matters:

```json
// Response — 403
{
  "error": {
    "code": "destructive_operation_restricted",
    "message": "This API Key has restrict_destructive enabled and cannot perform this operation.",
    "details": { "key_id": "key_01JAGF6G7H8J9K0L1M2N3O4P5Q" }
  }
}
```

"Destructive" is one rule, not a per-endpoint list maintained twice: the same classification [MCP Server → Tool annotations & safety](../../mcp-server/#tool-annotations--safety) uses to derive `destructiveHint` for a tool call — a `DELETE`, or a `PATCH`/`POST` moving an Entitlement to `disabled`/`revoked`, removing an Organization member, or requesting a Tenant tier change. A `restrict_destructive` key can still read everything its permissions allow and call every non-destructive write; it's blocked from exactly that set, regardless of what permissions it otherwise carries — the permission check and the destructive-operation check are independent gates, both have to pass.

Setting `restrict_destructive: false` explicitly on an `intended_use: "agent"` key — overriding the safer default — writes an [Audit Event](../../domain-model/orders-and-audit/#audit-event) (`api_key.restrict_destructive_disabled`), since it's the deliberate exception worth a record, not the default worth none.

## Test vs. live

`mode: "test"` produces a `satk_test_…` secret instead of `satk_live_…` — same permissions and scope, but every resource it creates (Users, Entitlements, anything) is tagged `test_mode: true`:

- Excluded from `GET` list endpoints by default, same as [soft-deleted records](../conventions/#filtering) — pass `?include_test=true` to see them.
- Webhook deliveries from test-mode data only reach webhook subscriptions that were themselves created with a `test` key — a `live` integration never receives test traffic.
- Subject to periodic cleanup (test data isn't held to the same [retention](../../non-functional-requirements/#data-retention) requirements as live data).

This is how an Application's developer integration-tests against the real API without a separate sandbox deployment or risk to live data — see [Conventions → Authentication](../conventions/#authentication).

{: .important }
An app-scoped key can never carry `entitlements.manage`, `users.manage`, or any other identity/access-control permission, even though those exist in the platform catalog — it's restricted to its own `app.<slug>.*` keys plus read access to the specific per-user data its scope implies (its own users' AppProfile, AppSettings, and effective-permissions). This is what makes it safe for an Application's backend to hold one: a compromised app-scoped key can't be used to grant itself access to a different app, or disable another app's entitlements.

## `POST /v1/api-keys/{id}/rotate`

```json
// Request
{}
```
```json
// Response — 200
{
  "id": "key_01JAGE5F6G7H8J9K0L1M2N3O4P",
  "secret": "satk_live_2b8e4a1f9c3d7e0b5a8f1c4e9b2d7a0f",
  "...": "..."
}
```

The old secret stops working immediately — there's no overlap window, so rotating is a coordinated action (update the caller's stored secret in the same change), not a background migration. For a zero-downtime rotation, create a second key, cut the caller over, then revoke the first, rather than rotating in place.

## `DELETE /v1/api-keys/{id}`

```json
// Response — 204
```

Immediate and permanent — sets `revoked_at`, and any request bearing that secret afterward gets `401`. Does not touch Entitlements, Roles, or any data the key was used to write; it only removes the credential itself.

## Errors specific to this resource

| Code | When |
|---|---|
| `permission_not_grantable_to_scope` | `POST` includes a permission the given `scope` isn't allowed to hold (see the app-scoped restriction above). |
| `api_key_not_found` | `{id}` doesn't resolve, or is already revoked. |
| `destructive_operation_restricted` | A `restrict_destructive: true` key attempted an operation classified as destructive — see [Agent keys & `restrict_destructive`](#agent-keys--restrict_destructive). Not specific to key creation; this is returned by *any* endpoint the key calls. |
