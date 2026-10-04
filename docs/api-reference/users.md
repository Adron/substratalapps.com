---
layout: default
title: Users
parent: API Reference
nav_order: 3
---

# Users
{: .no_toc }

See [Domain Model → Users & Organizations](../../domain-model/users-and-organizations/) for the full field reference and lifecycle diagram.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/users` | List users. Requires a platform role with `users.list`. |
| `POST` | `/v1/users` | Create a user (invite or direct creation). |
| `GET` | `/v1/users/{id}` | Fetch one user. `{id}` may be the literal string `me` for the authenticated caller. |
| `PATCH` | `/v1/users/{id}` | Update mutable fields (`email`, `status`). |
| `DELETE` | `/v1/users/{id}` | Soft-delete. See [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention). |
| `POST` | `/v1/users/{id}/suspend` | Shortcut for `PATCH {status: "suspended"}` — kept as its own endpoint since suspension is a common, audited, one-click admin action. |

## `POST /v1/users`

```json
// Request
{ "email": "jordan@example.com", "status": "invited" }
```

```json
// Response — 201
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": false,
  "status": "invited",
  "organization_id": null,
  "created_at": "2026-10-03T12:00:00Z",
  "last_login_at": null
}
```

Creating a user also creates a default [Profile](../../domain-model/profiles/) and [Settings](../../domain-model/settings/) record and assigns the default `member` platform role — see [Workflows → New user, first app](../../workflows/#new-user-first-app). None of that is a separate call; it's implied by this one.

## `GET /v1/users/{id}`

```json
// Response — 200
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": true,
  "status": "active",
  "organization_id": null,
  "created_at": "2026-01-14T18:02:11Z",
  "last_login_at": "2026-10-02T09:41:03Z"
}
```

## `POST /v1/users/{id}/suspend`

```json
// Request
{}
```
```json
// Response — 200
{ "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "status": "suspended", "...": "..." }
```

Produces an [Audit Event](../../domain-model/orders-and-audit/#audit-event) (`action: "user.suspended"`). Does not touch Entitlements — a suspended user's apps remain entitled, they just can't log in to reach them. To cut off one specific app instead of the whole account, use [Entitlements](../entitlements/), not this.

## Errors specific to this resource

| Code | When |
|---|---|
| `email_taken` | `email` collides with an existing user on create or update. |
| `user_not_found` | `{id}` doesn't resolve — including for a soft-deleted user, which 404s rather than returning a `deleted` status, to avoid leaking existence past the point of deletion. |
