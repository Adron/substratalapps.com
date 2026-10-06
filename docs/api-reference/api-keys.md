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
| `permissions` | array of strings | Permission keys this key carries. For an app-scoped key, limited to that Application's own `app.<slug>.*` keys plus the four [app-confined](#app-confined-permissions) platform permissions. |
| `mode` | enum | `live` \| `test` — see [Test vs. live](#test-vs-live) below. Fixed at creation; a key can't switch modes, only be replaced. |
| `intended_use` | enum | `service` \| `agent` — see [Agent keys & `restrict_destructive`](#agent-keys--restrict_destructive) below. Metadata with one real effect: it sets the default for `restrict_destructive`. |
| `restrict_destructive` | boolean | Default follows `intended_use` (`true` for `agent`, `false` for `service`) unless set explicitly. See below. |
| `secret_hint` | string | Last 4 characters of the current secret, e.g. `"…6c8e"`, so an operator can tell keys apart without the secret. |
| `expires_at` | timestamp, nullable | Optional hard expiry, set at creation. After it, the key behaves as revoked (`401`). |
| `created_by` | object | `{ "type": "user", "id": "usr_…" }`. Always a User: an API Key can never create API Keys (see below). |
| `last_used_at` | timestamp, nullable | Updated at most once a minute. |
| `created_at` | timestamp | |
| `revoked_at` | timestamp, nullable | |

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/api-keys` | List keys in scope for the caller. Filter by `scope`, `mode`, `intended_use`, and `include_inactive` (include revoked and expired keys). |
| `POST` | `/v1/api-keys` | Create a key. Returns the secret once. |
| `GET` | `/v1/api-keys/{id}` | Fetch one key (never the secret). |
| `PATCH` | `/v1/api-keys/{id}` | Change `name`, `permissions`, or `restrict_destructive`. |
| `POST` | `/v1/api-keys/{id}/rotate` | Issue a new secret for the same key record, invalidating the old one. |
| `DELETE` | `/v1/api-keys/{id}` | Revoke permanently. |

**Who may manage which keys:**

| Key's `scope` | Who can create, read, change, rotate, and revoke it |
|---|---|
| `platform` | Platform `api_keys.manage` only. Creating a key never grants more than the creator holds: every permission on a new platform key must be one the creator holds (`403 role_escalation_forbidden`). |
| an `application_id` | Platform `api_keys.manage`, **or the Application's owner** (its `owner_user_id`, or an `org_admin` of `owner_organization_id`). That's what lets a developer onboard without Substratal staff in the loop for every key. |

An API Key can never create, change, or rotate API Keys, including itself (`400 user_token_required`). Credential management is always done by a person. Every create, change, rotate, and revoke writes an Audit Event (`api_key.created`, `api_key.updated`, `api_key.rotated`, `api_key.revoked`).

## `POST /v1/api-keys`

```json
// Request
{
  "name": "timetrack-backend",
  "scope": "app_timetrack",
  "permissions": ["app.timetrack.export"],
  "mode": "live",
  "intended_use": "service",
  "expires_at": "2027-10-04T00:00:00Z"
}
```
```json
// Response — 201
{
  "id": "key_01JAGE5F6G7H8J9K011M2N304P",
  "name": "timetrack-backend",
  "scope": "app_timetrack",
  "permissions": ["app.timetrack.export"],
  "mode": "live",
  "intended_use": "service",
  "restrict_destructive": false,
  "secret": "satk_live_9f2a1c7e4b3d8f0a2c5e7b1d9f3a6c8e",
  "secret_hint": "…6c8e",
  "expires_at": "2027-10-04T00:00:00Z",
  "created_by": { "type": "user", "id": "usr_01JAG0SYSTEM00000000000000" },
  "last_used_at": null,
  "created_at": "2026-10-04T09:30:00Z",
  "revoked_at": null
}
```

`name`, `scope`, and `permissions` are required. `mode` defaults to `live`, `intended_use` to `service`, and `restrict_destructive` to the `intended_use` default. `expires_at` is optional and must be in the future.

`secret` is returned **only** in this response — store it immediately; it's not retrievable afterward, only rotatable. The key is used exactly like a user access token: `Authorization: Bearer satk_live_...`.

## Agent keys & `restrict_destructive`

An LLM agent calling this API (directly, or through the [MCP Server](../../mcp-server/)) has a different risk shape from deterministic service code making the same call. The platform's enforcement is just as strong: [Access Control](../../access-control/) doesn't know or care whether its caller is an agent. What changes is that the *decision to call* a destructive operation at all is made by something a prompt can influence, possibly via untrusted data it has read (a `disabled_reason`, or an `AppProfile.custom` field written by someone else). A service integration's call sites are fixed when it's written.

That's addressed with two fields on the existing API Key, not a new key *type* (which would be a parallel, redundant scoping system):

1. **`intended_use: "service" | "agent"`**, set at creation. It's metadata with one real effect: it sets the *default* of the field below, steering an agent-facing key toward the safer posture without forcing it.
2. **`restrict_destructive: boolean`**, which defaults to `true` for `agent` and `false` for `service`. When `true`, any request this key authorizes that classifies as destructive is rejected server-side, whatever permissions the key otherwise carries.

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
  "id": "key_01JAGF6G7H8J9K011M2N304P5Q",
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
    "details": { "key_id": "key_01JAGF6G7H8J9K011M2N304P5Q" }
  }
}
```

"Destructive" is one rule, not a per-endpoint list maintained twice: the same classification [MCP Server → Tool annotations & safety](../../mcp-server/#tool-annotations--safety) uses to derive `destructiveHint` for a tool call. The complete list of destructive operations is maintained there once, and marked on each operation in `openapi.yaml` as `x-substratal-destructive`. Some operations are always destructive (every `DELETE`, for example). Others are destructive only for certain request bodies: `PATCH /v1/entitlements/{id}` only when it sets `status` to `disabled` or `revoked`, so the same agent key can still extend a trial or convert one to a purchase. `openapi.yaml` marks these `x-substratal-destructive: conditional`, with the condition in `x-substratal-destructive-when`, and the server evaluates the condition against each request. A `restrict_destructive` key can still read everything its permissions allow and call every non-destructive write; it's blocked from exactly that set, regardless of what permissions it otherwise carries — the permission check and the destructive-operation check are independent gates, both have to pass.

Setting `restrict_destructive: false` explicitly on an `intended_use: "agent"` key — overriding the safer default — writes an [Audit Event](../../domain-model/orders-and-audit/#audit-event) (`api_key.restrict_destructive_disabled`), since it's the deliberate exception worth a record, not the default worth none.

The result: an agent integration is safer by default without being less capable by default. A read-only or toggle-only assistant never needs `restrict_destructive: false`. One that genuinely does (an assistant whose whole job is disabling compromised accounts, say) sets it explicitly, and that choice is audited.

## Test vs. live

Uniqueness constraints include `test_mode` (e.g. `unique (email, test_mode) where deleted_at is null`), so a test signup can't collide with a live one. A test-mode credential sees only test-mode rows and a live credential only live rows, enforced by the same Row-Level Security mechanism as `tenant_id`. Test-mode rows older than 30 days are purged nightly.

`mode: "test"` produces a `satk_test_…` secret instead of `satk_live_…` — same permissions and scope, but every resource it creates (Users, Entitlements, anything) is tagged `test_mode: true`:

- Invisible to live credentials, and vice versa: a live key or live User token never reads or writes test rows, under any parameter, and a test credential never sees live rows. There's no flag that crosses the line.
- Webhook deliveries from test-mode data only reach webhook subscriptions that were themselves created with a `test` key — a `live` integration never receives test traffic.
- Subject to periodic cleanup (test data isn't held to the same [retention](../../non-functional-requirements/#data-retention) requirements as live data).

This is how an Application's developer integration-tests against the real API without a separate sandbox deployment or risk to live data — see [Conventions → Authentication](../conventions/#authentication).

### Walkthrough: a test run end to end

1. **Create a test key** (as the Application's owner): `POST /v1/api-keys` with `"scope": "app_timetrack"`, `"mode": "test"`, `"permissions": ["entitlements.manage", "users.list"]`. The response's `secret` starts `satk_test_`. Applications are shared catalog rows, visible to test and live credentials alike (an Application's own `test_mode` flag only marks one created for testing, which is what allows `localhost` redirect URIs). Everything the test key *writes* is tagged `test_mode: true`.
2. **Create a test User** with that key: `POST /v1/users` `{"email": "qa+1@example.com", "status": "active", "send_invitation": false}`. The response has `"test_mode": true`. A live User with the same email can exist alongside it without conflict.
3. **Grant access**: `POST /v1/users/{id}/entitlements` with an `Idempotency-Key` and `{"application_id": "app_timetrack", "source": "trial", "ends_at": "…"}`. The Entitlement is `test_mode: true`, and the `entitlement.granted`/`access.granted` webhooks go only to subscriptions created with a test key.
4. **Sign in as that User**: set a password through `POST /v1/auth/password/forgot` `{"email": "qa+1@example.com", "test_mode": true}`, then `POST /v1/auth/login` with `"test_mode": true` (see [Auth → Test-mode Users](../auth/#test-mode-users)). The access token and every app token carry `"test_mode": true`.
5. **Check isolation**: `GET /v1/users?email=qa+1@example.com` with a *live* key returns an empty list. The test User doesn't exist as far as live credentials are concerned.
6. **Clean up**, or don't: test rows older than 30 days are purged nightly.

## App-confined permissions

Why app-scoped keys can carry these permissions at all: a developer's backend has to reflect its own billing outcomes into Entitlements (see [Billing system of record](../../domain-model/orders-and-audit/#billing-system-of-record)). Without app confinement it would have no safe credential to do that with. An app-scoped key couldn't hold `entitlements.manage`, and a platform-scoped key that could would reach every Application. Confinement keeps the safety property: a compromised app key still can't touch a *different* Application or escalate anyone's platform standing. A platform-run "billing bridge" for developers to post outcomes to was considered and not chosen, because it adds a component without reducing risk.

An app-scoped key may carry its Application's own `app.<slug>.*` keys, plus four platform permissions in **confined** form. The server automatically restricts each one to rows belonging to the key's own Application:

| Permission | What the app key can do with it |
|---|---|
| `entitlements.manage` | Grant, toggle, revoke, and delete-in-error Entitlements to **its own Application**, personal and org-wide. This is how a developer's backend reflects the outcome of their own billing. |
| `roles.manage` | Define its own Application's Roles, and assign or remove them on users. |
| `users.list` | List and read Users with any access path to its Application, and read their global Profile (read-only). |
| `audit.view` | Read Audit Events whose `application_id` is its Application. |

Without any extra permission, every app-scoped key can also read and write its own users' [AppProfile](../profiles/) and [AppSettings](../settings/), read their [effective permissions](../roles-and-permissions/#get-v1usersidappsappideffective-permissions) for its Application, and manage its Application's [webhooks](../webhooks/).

{: .important }
An app-scoped key can **never** carry `users.manage`, `applications.manage`, `organizations.manage`, `api_keys.manage`, `tenants.manage`, `webhooks.manage`, `billing.manage`, or `billing.refund`. Asking for one returns `422 permission_not_grantable_to_scope`. Any attempt to touch another Application's rows returns `404`, as though the row didn't exist. That's what makes it safe for an Application's backend, or a third party's, to hold one: a compromised key can't grant access to a different app, disable another app's Entitlements, or change anyone's platform standing.

## `GET /v1/api-keys/{id}`, `PATCH /v1/api-keys/{id}`

```json
// PATCH request — tighten an agent key
{ "permissions": ["audit.view", "users.list"], "restrict_destructive": true }
```
```json
// Response — 200, the key (no secret)
{
  "id": "key_01JAGF6G7H8J9K011M2N304P5Q",
  "name": "support-assistant-mcp",
  "scope": "platform",
  "permissions": ["audit.view", "users.list"],
  "mode": "live",
  "intended_use": "agent",
  "restrict_destructive": true,
  "secret_hint": "…a41f",
  "expires_at": null,
  "created_by": { "type": "user", "id": "usr_01JAG9STAFF000000000000000" },
  "last_used_at": "2026-10-05T11:58:12Z",
  "created_at": "2026-10-04T09:45:00Z",
  "revoked_at": null
}
```

`scope`, `mode`, and `intended_use` are fixed at creation (`422 read_only_field`), so replace the key to change them. A permission change takes effect on the key's next request.

## `POST /v1/api-keys/{id}/rotate`

```json
// Request
{}
```
```json
// Response — 200
{
  "id": "key_01JAGE5F6G7H8J9K011M2N304P",
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

| Code | Status | When |
|---|---|---|
| `permission_not_grantable_to_scope` | 422 | `POST`/`PATCH` includes a permission the given `scope` isn't allowed to hold (see [App-confined permissions](#app-confined-permissions)). |
| `unknown_permission` | 422 | A permission key that doesn't exist, or another Application's `app.*` key. |
| `role_escalation_forbidden` | 403 | A platform key with a permission its creator doesn't hold. |
| `user_token_required` | 400 | An API Key tried to create, change, rotate, or revoke an API Key. |
| `read_only_field` | 422 | A `PATCH` tried to change `scope`, `mode`, or `intended_use`. |
| `api_key_not_found` | 404 | `{id}` doesn't resolve, or isn't visible to the caller. A revoked key is still fetchable by id (with `revoked_at` set), per [Conventions → Filtering](../conventions/#filtering). |
| `api_key_revoked` | 409 | `PATCH`, `rotate`, or `DELETE` on a key that's already revoked or past `expires_at`. |
| `destructive_operation_restricted` | 403 | A `restrict_destructive: true` key attempted an operation classified as destructive — see [Agent keys & `restrict_destructive`](#agent-keys--restrict_destructive). Not specific to key creation; this is returned by *any* endpoint the key calls. |
