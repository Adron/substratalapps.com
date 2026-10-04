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

Every resource ID is an opaque ULID prefixed by type:

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

IDs are never reused and never recomputed from other fields — don't parse them for meaning beyond the prefix.

## Pagination

List endpoints take `limit` (default 25, max 100) and `cursor`, and return:

```json
{
  "data": [ /* ... */ ],
  "page": { "next_cursor": "eyJpZCI6Im9yZ18wMUoi...", "has_more": true }
}
```

Pass `next_cursor` back as `cursor` to get the next page. `has_more: false` means `next_cursor` is `null` and there's nothing further.

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

A repeated key with an identical body returns the original response (same status code, same body) instead of creating a duplicate. A repeated key with a *different* body returns `409`. See [Non-Functional Requirements → Idempotency & retries](../../non-functional-requirements/#idempotency--retries) for why this is mandatory rather than optional on those endpoints — billing webhooks retry, and a duplicate Entitlement is a real-money bug, not a cosmetic one.

## Versioning

`/v1` is the only version today. Additive changes (new optional fields, new endpoints) ship without a version bump; breaking changes get a new prefix and a deprecation window for the old one. See [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning).

## Timestamps

ISO 8601, UTC, always with a `Z` suffix: `2026-09-30T16:22:41Z`.
