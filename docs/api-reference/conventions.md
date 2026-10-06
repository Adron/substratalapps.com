---
layout: default
title: Conventions
parent: API Reference
nav_order: 1
---

# Conventions
{: .no_toc }

Read once, applies to every page in this section.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Machine-readable

Everything on this and the following pages is also available as a single [OpenAPI 3.1 document](../../openapi.yaml) — generate a client, import into an API tool, or diff it against an implementation to check drift. The prose here is authoritative when the two disagree; the YAML is kept in sync by hand, not generated from this site.

## Base URL

```
https://api.substratalapps.com/v1
```

This is the production base URL. Two endpoints deliberately live outside `/v1`, at the host root: `GET /.well-known/jwks.json` and `GET /.well-known/openid-configuration` (see [Auth → Signing keys](../auth/#signing-keys-jwks)), because standard JWT and OAuth libraries look for them there. So does the [MCP Server](../../mcp-server/) at `/mcp`. The Stripe webhook sink at `/internal/stripe/webhook` is internal to Substratal and not part of the public contract.

Every request and response body is `application/json; charset=utf-8`. The one exception is `POST /v1/auth/oauth/token`, which also accepts `application/x-www-form-urlencoded` for OAuth-library compatibility.

## Authentication

```
Authorization: Bearer <token>
```

User-facing requests carry a user access token (issued at login, see [Auth](../auth/)). Service-to-service requests (billing, an app's backend) carry a scoped [API Key](../api-keys/). Both go in the same header — the token type is distinguishable server-side by prefix, not by a different header name.

**Test vs. live:** every API Key is created with `satk_test_…` or `satk_live_…` (see [API Keys](../api-keys/)) — there is no separate sandbox deployment to point at. A `test` key operates against the same database, but every record it creates is tagged `test_mode: true`. Test and live rows are invisible to each other: a test credential reads and writes only test rows, a live credential only live rows, and no query parameter crosses that line. Test-mode events only reach test-mode webhook subscriptions, and test traffic is excluded from analytics counters. A User created in test mode signs in with `"test_mode": true` on the auth endpoints (see [Auth → Test-mode Users](../auth/#test-mode-users)), and every token issued to them carries `test_mode: true`, which confines it to test rows the same way. This is cheaper to build and run than a parallel environment, and it's the right call at the current scale (see [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) — a true isolated sandbox is a Scale-out-trigger-shaped decision, not a day-one one.

## IDs

Every resource ID is prefixed by type. Most are the prefix plus a ULID (26 Crockford base32 characters, so never `I`, `L`, `O`, or `U`): generated, never reused, never recomputed from other fields, and not meant to be parsed for meaning beyond the prefix. Application and Role ids are the two exceptions, described below the table.

| Prefix | Entity |
|---|---|
| `usr_` | User |
| `org_` | Organization |
| `tnt_` | Tenant — see [Domain Model → Tenancy](../../domain-model/tenancy/) |
| `tcr_` | Tenant tier-change request — see [Tenancy](../tenancy/) |
| `app_` | Application |
| `role_` | Role |
| `ent_` | Entitlement |
| `evt_` | Audit Event |
| `whk_` | Webhook subscription |
| `key_` | API Key — see [API Keys](../api-keys/) |
| `uid_` | UserIdentity — see [Users & Organizations](../../domain-model/users-and-organizations/#useridentity) |
| `ssc_` | SSOConnection — see [Users & Organizations](../../domain-model/users-and-organizations/#ssoconnection) |
| `ses_` | Session — see [Auth → Sessions](../auth/#sessions) |
| `wev_` | Webhook event (one delivered event payload) — see [Webhooks](../webhooks/#delivery). Distinct from `evt_`, an Audit Event. |
| `dlv_` | Webhook delivery attempt — see [Webhooks](../webhooks/#delivery-log) |

An [Erasure request](../users/#post-v1usersiderasure-requests) has no id of its own: there's at most one per User, so it's addressed by the User's id (`/v1/users/{id}/erasure-requests/current`).

`order_id` on an Entitlement is **not** a resource ID of this API. It's an opaque reference string the developer supplies from their own billing system (for example, their own Stripe subscription id), up to 255 characters. Examples on this site use Stripe-style subscription ids (`sub_…`), the most common case. See [Orders & Audit → Order references](../../domain-model/orders-and-audit/#order).

**Application and Role are the deliberate exceptions.** Both are commonly referenced from code and config (seed scripts, permission checks, launch URLs), where a stable, meaningful id is more useful than an opaque one, so both ids are derived rather than random:

- An **Application**'s `id` is `app_` plus its `slug`, verbatim (`app_timetrack`, `app_time-track`). `slug` is immutable, so the id is too. See [Applications](../applications/#the-application-object).
- A **Role**'s `id` is `role_platform_<name>` or `role_<slug>_<name>`, with the Application's slug verbatim (`role_timetrack_admin`, `role_time-track_admin`, `role_platform_member`). A slug never contains `_`, so the first `_` after `role_` always ends the slug, and two different (slug, name) pairs can never derive the same id. The slug `platform` is reserved for the same reason. See [Domain Model → Roles & Permissions](../../domain-model/roles-and-permissions/#role).

**Credential strings are not resource IDs** and follow their own prefix conventions, since they're secrets rather than addressable resources. Never log these or echo them back after their initial issuance. None is stored in plaintext. Tokens and API Key secrets are stored as SHA-256 hashes, since the API only ever needs to *verify* them. A webhook signing secret is stored KMS-encrypted instead, because the API has to *use* it to sign every delivery, and a hash can't sign. MFA recovery codes are stored as Argon2id hashes, since they're short enough to brute-force against a fast hash.

| Prefix | Credential | Lifetime |
|---|---|---|
| `rtk_` | Refresh token (platform or app) — see [Auth](../auth/) | 30 days idle / 90 days absolute, single-use |
| `mfa_` | MFA challenge token | 5 minutes, single-use |
| `ac_` | OAuth authorization code | 60 seconds, single-use |
| `emv_` | Email-verification token | 24 hours, single-use |
| `pwr_` | Password-reset token | 1 hour, single-use |
| `inv_` | Invitation token | 7 days, single-use |
| `whsec_` | Webhook signing secret — see [Webhooks](../webhooks/) | Until rotated |
| `satk_live_` / `satk_test_` | API Key secret — see [API Keys](../api-keys/) | Until revoked |

`atk_` and `apt_` appear only as `jti` values inside JWTs (platform access token and app token respectively), never as standalone credentials.

## Addressing yourself: `me`

Anywhere a path takes a `{id}` for a User, you may pass the literal string `me` instead of the caller's own `usr_…` id — `GET /v1/users/me`, `GET /v1/users/me/entitlements`, `GET /v1/users/me/apps/{appId}/settings`, `PATCH /v1/users/me/profile`, and so on. This resolves server-side from the auth token, so a client never needs to know its own user id just to read or update its own data. `me` only means something for a User's token. An [API Key](../api-keys/) has no User behind it, so `me` with an API Key returns `400 user_token_required`.

## Pagination

List endpoints take `limit` (default 25, max 100) and `cursor`, and return:

```json
{
  "data": [ /* ... */ ],
  "page": { "next_cursor": "eyJpZCI6Im9yZ18wMUoi...", "has_more": true }
}
```

Pass `next_cursor` back as `cursor` to get the next page. `has_more: false` means `next_cursor` is `null` and there's nothing further. Cursors are opaque and valid for 24 hours; an expired or tampered cursor returns `400 invalid_cursor`. A cursor is only valid with the same filters it was issued under.

**Ordering:** unless a page says otherwise, lists are ordered newest first by `created_at`, with ties broken by `id`. Audit Events are ordered by `timestamp`, newest first. There's no caller-selectable sort.

## Filtering

List endpoints that support filtering take plain query parameters named after the field being matched (`?status=active`, `?application_id=app_timetrack`) — there's no separate filter DSL. Each resource page's endpoint table states which fields are filterable; passing an unsupported filter parameter is ignored rather than erroring, so adding a new filterable field later is never a breaking change.

**Soft-deleted and terminal-state records are excluded from lists by default.** A list endpoint doesn't return a soft-deleted User, a `revoked` or `expired` Entitlement, or a revoked API Key unless the caller explicitly asks for it, with an explicit `?status=…` or with `?include_inactive=true` on the lists that support it. This is the default precisely so "list my entitlements" doesn't require every caller to remember to filter out the ones that don't matter anymore.

**Fetching by id** follows one rule:

- A **terminal-state** record (a `revoked` or `expired` Entitlement, a revoked API Key, a `cancelled` tier-change request) is still fetchable by id by anyone allowed to see it, and shows its terminal state. Write operations on it return `409` with the resource's own code.
- A **soft-deleted User** returns `404 user_not_found` to everyone except `users.manage` holders, so a deleted account's existence isn't leaked. `users.manage` can still fetch it (`status: deleted`), restore it, and manage its erasure request. See [Users → `DELETE`](../users/#delete-v1usersid).

**Boolean scoping parameters.** A few list filters aren't named after a field because they widen or narrow the default set rather than match a value: `include_inactive=true` (include terminal-state rows) and `owned=true` (only what the caller owns, on [Applications](../applications/#get-v1applications)). They're the only two, and they mean the same thing wherever they appear.

## Delete semantics: hard vs. soft

"Delete" doesn't mean the same thing on every resource, and this site doesn't pick one convention and force every resource into it — it picks the right one per resource and documents which, here, once, instead of leaving a caller to infer it from each page's own wording.

**A soft delete** flips a `status` (or sets a `deleted_at`/`revoked_at` timestamp) rather than removing the row. The record is excluded from default list results, and whether it's still fetchable by id follows the rule in [Filtering](#filtering) above. Every other field is untouched. Nothing about a soft delete scrubs, redacts, or moves data anywhere; it's purely a status change. Where a soft delete is reversible, reversing it restores exactly the prior state, nothing re-provisioned.

**A hard delete** removes the row entirely. Where this site allows it at all, it's reserved for correcting a mistake, not for the ordinary lifecycle of a real record — see each resource's own endpoint description for the specific line it draws.

| Resource | `DELETE` behavior |
|---|---|
| [User](../../domain-model/users-and-organizations/) | **Soft.** `status: deleted`; 404s afterward to everyone but `users.manage`, who can restore it until the erasure cascade (if one was requested) completes. A separate, two-stage hard-delete cascade exists for right-to-erasure requests specifically — see [Non-Functional Requirements → Hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade). Not triggered by this call. |
| [API Key](../api-keys/) | **Soft, but irreversible.** Sets `revoked_at`; the row and its usage history are retained, but unlike every other soft delete on this list, there is no un-revoke — a replacement means creating a new key. |
| [Entitlement](../entitlements/) | **Hard** — but only for a grant with no `order_id`. Reserved for correcting a mistake (wrong user, wrong app, duplicate); real revocations use `PATCH status: revoked`/`disabled` instead, so the history survives. See [Entitlements → `DELETE`](../entitlements/#delete-v1entitlementsid). |
| Organization member | **Hard.** The [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) join row is removed outright, whether it was `pending` or `active`. Membership has no "soft-removed" state of its own. |
| Role assignment | **Hard.** The [UserRoleAssignment](../../domain-model/roles-and-permissions/#userroleassignment) join row is removed outright; idempotent (removing an already-gone assignment still returns `204`). |
| [Webhook](../webhooks/) subscription | **Hard.** Unsubscribing removes the subscription and its delivery log. Deliveries already in the retry queue are dropped, and nothing new is enqueued. |
| [Application](../applications/), [Organization](../organizations/), [Role](../roles-and-permissions/) (the definition), [Tenant](../tenancy/) | **No `DELETE` endpoint at all.** Each has its own terminal-but-not-deleted state instead — `review_status: suspended`/`rejected` for an Application, `status: suspended` for an Organization (via `PATCH`) — because removing the catalog/definition entry itself would orphan everything that still references it (Entitlements, Role assignments, Roles scoped to it). |
| [Profile](../../domain-model/profiles/) | No `DELETE` endpoint. Deleted outright, but only as step 2 of the [hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade) above — never independently. |
| [AppProfile](../../domain-model/profiles/#appprofile) / [AppSettings](../../domain-model/settings/#appsettings) | No `DELETE` endpoint. The hard-delete cascade's step 3 clears personal data in place: all of an AppProfile (`display_handle` set to `null`, `custom` to `{}`), and only the `x-pii`-marked keys of AppSettings `overrides`. The rows themselves are retained even then. |
| [Settings](../../domain-model/settings/) (global) | No `DELETE` endpoint, and not touched by the hard-delete cascade either — none of its fields (`locale`, `timezone`, `theme`, `notifications`) are personally identifying, so there's nothing on it the erasure right reaches. |
| [Audit Event](../../domain-model/orders-and-audit/#audit-event) | Never deletable or editable through the API, by anyone, under any permission. Two scheduled infrastructure jobs are the only exceptions, and both only ever reduce an event to its shape: the archival job moves an aged-out event to cold storage, and the erasure cascade redacts `before`/`after` on a deleted User's events. See [Non-Functional Requirements → Audit log lifecycle](../../non-functional-requirements/#audit-log-lifecycle). |

## Partial updates (`PATCH`)

Every `PATCH` body is a partial object: send only the fields to change.

- An **omitted** field is left unchanged.
- An explicit **`null`** clears a nullable field. Sending `null` for a non-nullable field is `422 validation_failed`.
- **Free-form object fields** ([AppProfile](../profiles/)`.custom`, [AppSettings](../settings/)`.overrides`, Settings `.notifications`) are merged one level deep. Each key you send replaces that key, a key sent as `null` is removed, and keys you don't send are untouched. Nested objects inside them are replaced wholesale, not merged recursively.
- Arrays are always replaced wholesale (for example `member_overrides`, `permissions`, `events`, `redirect_uris`).
- Read-only fields (`id`, `created_at`, `tenant_id`, …) sent in a `PATCH` body are rejected with `422 read_only_field`, not silently ignored, so a client bug can't hide.

Every successful `PATCH` returns `200` with the full updated resource.

## Concurrency (`ETag` / `If-Match`)

Every single-resource `GET` and every successful write returns an `ETag` header, a weak validator over the row's version counter (`ETag: W/"7"`). Any `PATCH` or `DELETE` **may** send `If-Match: W/"7"`. If the resource has changed since, the write is rejected with `409 version_conflict` and `details.current_etag`. Without `If-Match` the write is last-write-wins. Admin tooling and agents should always send it on Entitlement, Role, Settings, and AppSettings writes. See [Non-Functional Requirements → Concurrency control](../../non-functional-requirements/#concurrency-control).

## Size and length limits

| Thing | Limit | Error |
|---|---|---|
| Request body | 1 MB | `413 payload_too_large` |
| Any string field, unless stated otherwise | 255 characters | `422 validation_failed` (`too_long`) |
| `description`-style free text, `review_notes`, `reason` | 2,000 characters | same |
| `Application.settings_schema` | 64 KB serialized, ≤ 200 declared properties | same |
| `AppProfile.custom`, `AppSettings.overrides` | 16 KB serialized each | same |
| `member_overrides` | 1,000 user ids | same |
| `redirect_uris` | 10 entries | same |
| Webhook subscriptions' `events` | every event type, no duplicates | same |
| List `limit` | 1–100 (default 25) | values above 100 are clamped to 100, not rejected |

## Single-resource responses

A single resource is returned as a bare JSON object — no envelope:

```json
{ "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "...": "..." }
```

## Errors

```json
{
  "error": {
    "code": "entitlement_required",
    "message": "This user has no active entitlement to app_invoicer.",
    "details": { "application_id": "app_invoicer" }
  }
}
```

`code` is a stable, machine-matchable string — build logic against it, not against `message`, which is for humans and can change wording without notice.

**There's no single global code catalog for resource-specific codes.** Each resource's own API Reference page carries an "Errors specific to this resource" table — that table is the authoritative source for the codes that resource returns, the same way [Orders & Audit → Action catalog](../../domain-model/orders-and-audit/#action-catalog) is authoritative for `action` values. A handful of codes are cross-cutting enough to define once, here, instead of repeating identically on every page. Everything else, `entitlement_required` above included, belongs to and is defined on one specific resource page.

| Code | Status | When |
|---|---|---|
| `invalid_request` | 400 | Body isn't valid JSON, or a required field is missing. |
| `invalid_cursor` | 400 | Pagination cursor expired, tampered with, or reused with different filters. |
| `user_token_required` | 400 | An endpoint that acts *as a User* (`me`, `app-tokens`, password change, …) was called with an API Key. |
| `idempotency_key_required` | 400 | A `POST` that requires `Idempotency-Key` was sent without one. |
| `unauthenticated` | 401 | No Bearer token, a malformed or expired one, or a revoked API Key. |
| `session_revoked` | 401 | The token's session was logged out or revoked. |
| `forbidden` | 403 | Authenticated, but the caller lacks the required permission. `details.required_permission` names it. |
| `tenant_suspended` | 403 | The Tenant that owns the data being written (or the Application an app token is requested for) is `suspended` by the platform. See [Tenancy](../../domain-model/tenancy/#fields). |
| `destructive_operation_restricted` | 403 | A `restrict_destructive` API Key attempted a destructive operation — see [API Keys](../api-keys/#agent-keys--restrict_destructive). |
| `subscription_required` | 402 | The owning Tenant is `restricted` after a lapsed subscription, and this write would add usage — see [Pricing → Subscription lapse](../../pricing/#subscription-lapse--downgrades). |
| `not_found` | 404 | Generic not-found for a path that matches no route. Resource pages define their own `<resource>_not_found` codes. |
| `version_conflict` | 409 | `If-Match` didn't match the current `ETag`. |
| `idempotency_key_reused` | 409 | Same `Idempotency-Key`, different request body. |
| `idempotency_key_in_flight` | 409 | The first request with this `Idempotency-Key` is still being processed. Retry after a second. |
| `plan_limit_reached` | 409 | The owning Tenant is at a plan limit — see [Plan limit errors](#plan-limit-errors) below and [Pricing → Enforcement](../../pricing/#enforcement). |
| `payload_too_large` | 413 | Body over 1 MB. |
| `validation_failed` | 422 | One or more fields fail validation. Uses `details.fields` — see below. |
| `read_only_field` | 422 | A `PATCH` body included a read-only field. |
| `rate_limited` | 429 | See [Rate limiting](../../non-functional-requirements/#rate-limiting). |
| `internal_error` | 500 | Unexpected server error. The response carries `X-Request-Id`; quote it to support. |
| `service_unavailable` | 503 | Planned maintenance (for example, a Tenant mid-migration on a write path) or a dependency outage. `Retry-After` is set. |

HTTP status follows a fixed mapping from error *class*, not a judgment call per endpoint:

| Status | Class | Example |
|---|---|---|
| `400` | Malformed request — the body isn't valid JSON, or is missing a required field with no sensible default. | Missing `application_id` on a grant. |
| `402` | Payment required — the owning Tenant's subscription has lapsed, and this write would add usage. | `subscription_required`. |
| `401` | Missing or invalid auth — no Bearer token, an expired one, or a revoked API Key's secret. | A revoked key's secret used after `revoked_at`. |
| `403` | Authenticated, but not permitted — a real permission or ownership check failed. | `moderation_field_forbidden`, `destructive_operation_restricted`. |
| `404` | Not found — including a never-existed resource, one the caller isn't allowed to see, and a soft-deleted User; see [Filtering](#filtering) for which records 404 after deletion and which stay fetchable. | `entitlement_not_found`. |
| `409` | Conflict — the request is individually valid, but the current state of the resource makes it impossible to apply as-is. | `entitlement_already_exists`, `version_conflict`, `plan_limit_reached`, an idempotency key reused with a different body. |
| `422` | Semantically invalid — the request is well-formed but violates a declared rule beyond basic shape. | A `settings_schema` violation, `validation_failed`. |
| `413` | Body too large. | `payload_too_large`. |
| `429` | Rate limited. | See [Rate limiting](../../non-functional-requirements/#rate-limiting). |
| `5xx` | Server-side failure; safe to retry idempotent requests with backoff. | `internal_error`, `service_unavailable`. |

The dividing line between `409` and `422` worth internalizing: `409` is about *state* ("this would conflict with something that already exists or already happened"), `422` is about the *request's own content* ("this value, on its own, doesn't satisfy a rule"). `plan_limit_reached` is `409`, not `422`, for exactly this reason — the request is well-formed, it just can't be satisfied against the owning Tenant's current count.

**Multiple field errors** (a `422` from a request that fails validation on more than one field at once) use a structured `details.fields` array instead of forcing the client to parse `message`:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "2 fields failed validation.",
    "details": {
      "fields": [
        { "field": "email", "code": "invalid_format" },
        { "field": "available_app_roles", "code": "too_long", "max": 20 }
      ]
    }
  }
}
```

A single-field error (like `entitlement_required` above) skips the array and puts the relevant IDs directly in `details` — the array form is specifically for "more than one thing wrong with this request body."

Each entry's `code` is one of a small fixed set: `required`, `invalid_format`, `too_short`, `too_long`, `out_of_range`, `enum_mismatch`, `not_unique`, `unknown_value`, `undeclared_key`. Entries carry whatever bound applies (`min`, `max`, `allowed`, `pattern`). A few resource pages show examples for their own rules: [Applications](../applications/#validation-errors), [Profiles](../profiles/#patch-v1usersidprofile), and [Settings](../settings/#patch-v1usersidsettings).

### Plan limit errors

`409 plan_limit_reached` always carries the same `details`, so a client can render one "upgrade to continue" screen for every limit:

```json
{
  "error": {
    "code": "plan_limit_reached",
    "message": "This Tenant's Starter plan allows 1 Application.",
    "details": {
      "resource": "applications",
      "limit": 1,
      "current": 1,
      "plan": "starter",
      "tenant_id": "tnt_01JAG2STARTER0000000000000"
    }
  }
}
```

`resource` is one of `applications`, `app_roles`, `webhooks`, or `seats`. These are the same keys [`GET /v1/tenants/{id}/usage`](../billing/#get-v1tenantsidusage) reports under `limits`, so the error and the usage screen always agree on names. See [Pricing → Enforcement](../../pricing/#enforcement) for what each one counts.

## Idempotency

Any `POST` that creates or transitions an Entitlement accepts:

```
Idempotency-Key: <client-generated string, 1–255 characters; a UUID v4 is recommended>
```

A repeated key with an identical body returns the original response (same status code, same body) instead of creating a duplicate. A repeated key with a *different* body returns `409`. Keys are remembered for 24 hours, scoped per API key/caller — after that window a repeated key is treated as new. A request that arrives while the first one with the same key is still in flight gets `409 idempotency_key_in_flight`; retry after a second. Responses replayed from the store carry `Idempotent-Replayed: true`.

Endpoints that **require** `Idempotency-Key`: `POST /v1/users/{id}/entitlements`, `POST /v1/organizations/{id}/entitlements`, and `POST /v1/tenants/{id}/billing/checkout-sessions`. Every other `POST` **accepts** it optionally and honors it the same way. See [Non-Functional Requirements → Idempotency & retries](../../non-functional-requirements/#idempotency--retries) for why this is mandatory rather than optional on those endpoints — billing webhooks retry, and a duplicate Entitlement is a real-money bug, not a cosmetic one.

**Implementation note:** store `(caller_id, idempotency_key) → (request_body_hash, response_status, response_body, expires_at)`, written in the same transaction as the mutation it guards (see [Non-Functional Requirements → Transaction boundaries](../../non-functional-requirements/#transaction-boundaries)) so a crash between "wrote the Entitlement" and "recorded the idempotency key" can't produce a duplicate on retry. Compare the stored hash, not the raw body, to decide same-vs-different.

## Health check

```
GET /v1/health
```
```json
{ "status": "ok" }
```

Unauthenticated, uncached, for uptime monitoring and load balancer health checks — not a dependency check (it doesn't query the database). Always `200` unless the service itself can't respond.

## Response headers on every request

| Header | Meaning |
|---|---|
| `X-Request-Id` | Echoes the caller's `X-Request-Id` if sent (≤ 128 characters), otherwise a generated one. Quote it in support requests. |
| `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset` | The caller's current token bucket — see [Rate limiting](../../non-functional-requirements/#rate-limiting). |
| `ETag` | On single-resource responses — see [Concurrency](#concurrency-etag--if-match). |
| `Deprecation`, `Sunset` | Only on a deprecated version — see [Versioning](#versioning). |

## Versioning

`/v1` is the only version today. Additive changes (new optional fields, new endpoints) ship without a version bump; breaking changes get a new prefix and a deprecation window for the old one. See [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning).

## Timestamps

ISO 8601, UTC, always with a `Z` suffix: `2026-09-30T16:22:41Z`.
