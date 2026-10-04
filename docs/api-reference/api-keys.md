---
layout: default
title: API Keys
parent: API Reference
nav_order: 12
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
  "permissions": ["app.timetrack.export"]
}
```
```json
// Response — 201
{
  "id": "key_01JAGE5F6G7H8J9K0L1M2N3O4P",
  "name": "timetrack-backend",
  "scope": "app_timetrack",
  "permissions": ["app.timetrack.export"],
  "secret": "satk_live_9f2a1c7e4b3d8f0a2c5e7b1d9f3a6c8e",
  "last_used_at": null,
  "created_at": "2026-10-04T09:30:00Z",
  "revoked_at": null
}
```

`secret` is returned **only** in this response — store it immediately; it's not retrievable afterward, only rotatable. The key is used exactly like a user access token: `Authorization: Bearer satk_live_...`.

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
