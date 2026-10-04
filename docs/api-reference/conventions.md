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

User-facing requests carry a user access token (issued at login, see [Auth](../auth/)). Service-to-service requests (billing, an app's backend) carry a scoped API key. Both go in the same header — the token type is distinguishable server-side by prefix, not by a different header name.

## IDs

Every resource ID is prefixed by type. Most are opaque ULIDs — generated, never reused, never recomputed from other fields, and not meant to be parsed for meaning beyond the prefix:

| Prefix | Entity |
|---|---|
| `usr_` | User |
| `org_` | Organization |
| `app_` | Application |
| `role_` | Role |
| `ent_` | Entitlement |
| `ord_` | Order |
| `evt_` | Audit Event |
| `whk_` | Webhook subscription |
| `key_` | API Key — see [API Keys](../api-keys/) |

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

## Single-resource responses

A single resource is returned as a bare JSON object — no envelope:

```json
{ "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "...": "..." }
```

## Errors

```json
{
  "error": {
    "code": "entitlement_not_active",
    "message": "This user's entitlement to app_invoicer is disabled.",
    "details": { "entitlement_id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A" }
  }
}
```

`code` is a stable, machine-matchable string — build logic against it, not against `message`, which is for humans and can change wording without notice. HTTP status follows normal semantics (`400` malformed request, `401` missing/invalid auth, `403` authenticated but not permitted, `404` not found, `409` conflict — e.g. idempotency key reuse with a different body, `429` rate limited).

## Idempotency

Any `POST` that creates or transitions an Entitlement- or Order-linked record accepts:

```
Idempotency-Key: <client-generated UUID>
```

A repeated key with an identical body returns the original response (same status code, same body) instead of creating a duplicate. A repeated key with a *different* body returns `409`. Keys are remembered for 24 hours, scoped per API key/caller — after that window a repeated key is treated as new. See [Non-Functional Requirements → Idempotency & retries](../../non-functional-requirements/#idempotency--retries) for why this is mandatory rather than optional on those endpoints — billing webhooks retry, and a duplicate Entitlement is a real-money bug, not a cosmetic one.

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
