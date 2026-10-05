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

Provisional — the real host is whatever gets decided alongside [Decisions → Identity provider](../../decisions/#1-identity-provider), but every example on this site uses this value so they're copy-pasteable and consistent with each other.

## Authentication

```
Authorization: Bearer <token>
```

User-facing requests carry a user access token (issued at login, see [Auth](../auth/)). Service-to-service requests (billing, an app's backend) carry a scoped [API Key](../api-keys/). Both go in the same header — the token type is distinguishable server-side by prefix, not by a different header name.

**Test vs. live:** every API Key is created with `satk_test_…` or `satk_live_…` (see [API Keys](../api-keys/)) — there is no separate sandbox deployment to point at. A `test` key operates against the same database, but every record it creates is tagged `test_mode: true`, excluded from webhooks firing to any other caller's `live` subscriptions, and from rate-limit/analytics counters. This is cheaper to build and run than a parallel environment, and it's the right call at the current scale (see [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) — a true isolated sandbox is a Scale-out-trigger-shaped decision, not a day-one one.

## IDs

Every resource ID is prefixed by type. Most are opaque ULIDs — generated, never reused, never recomputed from other fields, and not meant to be parsed for meaning beyond the prefix:

| Prefix | Entity |
|---|---|
| `usr_` | User |
| `org_` | Organization |
| `tnt_` | Tenant — see [Domain Model → Tenancy](../../domain-model/tenancy/) |
| `tcr_` | Tenant tier-change request — see [Tenancy](../tenancy/) |
| `app_` | Application |
| `role_` | Role |
| `ent_` | Entitlement |
| `ord_` | Order |
| `evt_` | Audit Event |
| `whk_` | Webhook subscription |
| `key_` | API Key — see [API Keys](../api-keys/) |
| `uid_` | UserIdentity — see [Users & Organizations](../../domain-model/users-and-organizations/#useridentity) |
| `ssc_` | SSOConnection — see [Users & Organizations](../../domain-model/users-and-organizations/#ssoconnection) |

**Role is the deliberate exception.** A Role's `id` is a human-readable slug (`role_timetrack_admin`, `role_platform_member`), not a random ULID — Roles are commonly referenced from code and config (seed scripts, permission checks), where a stable, meaningful id is more useful than an opaque one. See [Domain Model → Roles & Permissions](../../domain-model/roles-and-permissions/#role).

**Credential strings are not resource IDs** and follow their own prefix conventions, since they're secrets rather than addressable resources: a refresh token is `rtk_…` (see [Auth](../auth/)), a webhook signing secret is `whsec_…` (see [Webhooks](../webhooks/)), and an API key's secret value is `satk_live_…` (see [API Keys](../api-keys/)). Never log these or echo them back after their initial issuance.

## Addressing yourself: `me`

Anywhere a path takes a `{id}` for a User, you may pass the literal string `me` instead of the caller's own `usr_…` id — `GET /v1/users/me`, `GET /v1/users/me/entitlements`, `GET /v1/users/me/apps/{appId}/settings`, `PATCH /v1/users/me/profile`, and so on. This resolves server-side from the auth token, so a client never needs to know its own user id just to read or update its own data.

## Pagination

List endpoints take `limit` (default 25, max 100) and `cursor`, and return:

```json
{
  "data": [ /* ... */ ],
  "page": { "next_cursor": "eyJpZCI6Im9yZ18wMUoi...", "has_more": true }
}
```

Pass `next_cursor` back as `cursor` to get the next page. `has_more: false` means `next_cursor` is `null` and there's nothing further.

## Filtering

List endpoints that support filtering take plain query parameters named after the field being matched (`?status=active`, `?application_id=app_timetrack`) — there's no separate filter DSL. Each resource page's endpoint table states which fields are filterable; passing an unsupported filter parameter is ignored rather than erroring, so adding a new filterable field later is never a breaking change.

**Soft-deleted and terminal-state records are excluded by default.** A list endpoint doesn't return a soft-deleted User, a `revoked` Entitlement, or a `suspended` API Key unless the caller explicitly asks for it (`?status=revoked`, or a resource-specific `?include_deleted=true` where noted on that page). This is the default precisely so "list my entitlements" doesn't require every caller to remember to filter out the ones that don't matter anymore.

## Delete semantics: hard vs. soft

"Delete" doesn't mean the same thing on every resource, and this site doesn't pick one convention and force every resource into it — it picks the right one per resource and documents which, here, once, instead of leaving a caller to infer it from each page's own wording.

**A soft delete** flips a `status` (or sets a `deleted_at`/`revoked_at` timestamp) rather than removing the row. The record is excluded from default list results — same rule as [Filtering](#filtering) above — but remains fetchable by id for an authorized caller, and every other field is untouched. Nothing about a soft delete scrubs, redacts, or moves data anywhere; it's purely a status change. Where a soft delete is reversible, reversing it restores exactly the prior state, nothing re-provisioned.

**A hard delete** removes the row entirely. Where this site allows it at all, it's reserved for correcting a mistake, not for the ordinary lifecycle of a real record — see each resource's own endpoint description for the specific line it draws.

| Resource | `DELETE` behavior |
|---|---|
| [User](../../domain-model/users-and-organizations/) | **Soft.** `status: deleted`; 404s afterward. A separate, two-stage hard-delete cascade exists for right-to-erasure requests specifically — see [Non-Functional Requirements → Hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade). Not triggered by this call. |
| [API Key](../api-keys/) | **Soft, but irreversible.** Sets `revoked_at`; the row and its usage history are retained, but unlike every other soft delete on this list, there is no un-revoke — a replacement means creating a new key. |
| [Entitlement](../entitlements/) | **Hard** — but only for a grant with no `order_id`. Reserved for correcting a mistake (wrong user, wrong app, duplicate); real revocations use `PATCH status: revoked`/`disabled` instead, so the history survives. See [Entitlements → `DELETE`](../entitlements/#delete-v1entitlementsid). |
| Organization member | **Hard.** The [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) join row is removed outright — membership has no "soft-removed" state of its own. |
| Role assignment | **Hard.** The [UserRoleAssignment](../../domain-model/roles-and-permissions/#userroleassignment) join row is removed outright; idempotent (removing an already-gone assignment still returns `204`). |
| [Webhook](../webhooks/) subscription | **Hard.** Unsubscribing removes the subscription; already-queued deliveries still attempt, nothing new is enqueued. |
| [Application](../applications/), [Organization](../organizations/), [Role](../roles-and-permissions/) (the definition), [Tenant](../tenancy/) | **No `DELETE` endpoint at all.** Each has its own terminal-but-not-deleted state instead — `review_status: suspended`/`rejected` for an Application, `status: suspended` for an Organization (via `PATCH`) — because removing the catalog/definition entry itself would orphan everything that still references it (Entitlements, Role assignments, Roles scoped to it). |
| [Profile](../../domain-model/profiles/) | No `DELETE` endpoint. Deleted outright, but only as step 2 of the [hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade) above — never independently. |
| [AppProfile](../../domain-model/profiles/#appprofile) / [AppSettings](../../domain-model/settings/#appsettings) | No `DELETE` endpoint. PII is scrubbed from `custom`/`overrides` in place by the hard-delete cascade's step 3; the row itself is retained even then. |
| [Settings](../../domain-model/settings/) (global) | No `DELETE` endpoint, and not touched by the hard-delete cascade either — none of its fields (`locale`, `timezone`, `theme`, `notifications`) are personally identifying, so there's nothing on it the erasure right reaches. |
| [Audit Event](../../domain-model/orders-and-audit/#audit-event) | Never deletable through the API, by anyone, under any permission — the one exception is the scheduled archival job moving an aged-out event to cold storage, which is an infrastructure process, not a caller-facing `DELETE`. See [Non-Functional Requirements → Audit log lifecycle](../../non-functional-requirements/#audit-log-lifecycle). |

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

**There's no single global code catalog.** Each resource's own API Reference page carries an "Errors specific to this resource" table — that table is the authoritative source for the codes that resource returns, the same way [Orders & Audit → Action catalog](../../domain-model/orders-and-audit/#action-catalog) is authoritative for `action` values. A handful of codes are cross-cutting enough to define once, here, instead of repeating identically on every page: `validation_failed`, `version_conflict`, `rate_limited`. Everything else — `entitlement_required` above included — belongs to, and is defined on, one specific resource page.

HTTP status follows a fixed mapping from error *class*, not a judgment call per endpoint:

| Status | Class | Example |
|---|---|---|
| `400` | Malformed request — the body isn't valid JSON, or is missing a required field with no sensible default. | Missing `application_id` on a grant. |
| `401` | Missing or invalid auth — no Bearer token, an expired one, or a revoked API Key's secret. | A revoked key's secret used after `revoked_at`. |
| `403` | Authenticated, but not permitted — a real permission or ownership check failed. | `moderation_field_forbidden`, `destructive_operation_restricted`. |
| `404` | Not found — including a soft-deleted or never-existed resource; see [Filtering](#filtering) for why a soft-deleted record 404s rather than returning a `deleted` status. | `entitlement_not_found`. |
| `409` | Conflict — the request is individually valid, but the current state of the resource makes it impossible to apply as-is. | `entitlement_already_exists`, `version_conflict`, `plan_limit_reached`, an idempotency key reused with a different body. |
| `422` | Semantically invalid — the request is well-formed but violates a declared rule beyond basic shape. | A `settings_schema` violation, `validation_failed`. |
| `429` | Rate limited. | See [Rate limiting](../../non-functional-requirements/#rate-limiting). |

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

## Idempotency

Any `POST` that creates or transitions an Entitlement- or Order-linked record accepts:

```
Idempotency-Key: <client-generated UUID>
```

A repeated key with an identical body returns the original response (same status code, same body) instead of creating a duplicate. A repeated key with a *different* body returns `409`. Keys are remembered for 24 hours, scoped per API key/caller — after that window a repeated key is treated as new. See [Non-Functional Requirements → Idempotency & retries](../../non-functional-requirements/#idempotency--retries) for why this is mandatory rather than optional on those endpoints — billing webhooks retry, and a duplicate Entitlement is a real-money bug, not a cosmetic one.

**Implementation note:** store `(caller_id, idempotency_key) → (request_body_hash, response_status, response_body, expires_at)`, written in the same transaction as the mutation it guards (see [Non-Functional Requirements → Transaction boundaries](../../non-functional-requirements/#transaction-boundaries)) so a crash between "wrote the Entitlement" and "recorded the idempotency key" can't produce a duplicate on retry. Compare the stored hash, not the raw body, to decide same-vs-different.

## Health check

```
GET /v1/health
```
```json
{ "status": "ok" }
```

Unauthenticated, uncached, for uptime monitoring and load balancer health checks — not a dependency check (it doesn't query the database). Always `200` unless the service itself can't respond.

## Versioning

`/v1` is the only version today. Additive changes (new optional fields, new endpoints) ship without a version bump; breaking changes get a new prefix and a deprecation window for the old one. See [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning).

## Timestamps

ISO 8601, UTC, always with a `Z` suffix: `2026-09-30T16:22:41Z`.
