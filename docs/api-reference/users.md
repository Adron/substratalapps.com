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

Creating a user also creates a default [Profile](../../domain-model/profiles/) and [Settings](../../domain-model/settings/) record and assigns the default `member` platform role — see [Workflows → New user, first app](../../workflows/#new-user-first-app). None of that is a separate call; it's implied by this one. Requires a platform role with `users.manage`.

## `GET /v1/users/{id}`

Self, or a platform role with `users.list`.

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

## `PATCH /v1/users/{id}`

```json
// Request
{ "email": "jordan.alvarez@example.com" }
```
```json
// Response — 200, full updated object
```

Self may update their own `email` (triggers re-verification, `email_verified` resets to `false`). Changing `status` requires a platform role with `users.manage` — a self-service caller cannot suspend or reactivate their own account this way; see `POST /v1/users/{id}/suspend` below for the supported self-contained admin action.

## `DELETE /v1/users/{id}`

```json
// Response — 204
```

Soft-delete (see [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention)): sets `status: deleted`, and from that point `GET /v1/users/{id}` 404s. Requires a platform role with `users.manage`, or the user deleting their own account. Entitlements, Roles, Profile, and Settings are retained, not removed, by this call — hard deletion for right-to-erasure requests is a separate, internal, non-API process, since it needs to cascade in a controlled order and isn't something a single HTTP call should trigger.

## `POST /v1/users/{id}/suspend`

```json
// Request
{}
```
```json
// Response — 200
{ "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "status": "suspended", "...": "..." }
```

Requires a platform role with `users.manage`. Produces an [Audit Event](../../domain-model/orders-and-audit/#audit-event) (`action: "user.suspended"`). Does not touch Entitlements — a suspended user's apps remain entitled, they just can't log in to reach them. To cut off one specific app instead of the whole account, use [Entitlements](../entitlements/), not this.

## Errors specific to this resource

| Code | When |
|---|---|
| `email_taken` | `email` collides with an existing user on create or update. |
| `user_not_found` | `{id}` doesn't resolve — including for a soft-deleted user, which 404s rather than returning a `deleted` status, to avoid leaking existence past the point of deletion. |
| `status_change_forbidden` | A self-service caller's `PATCH` attempts to change `status`, which requires `users.manage`. |
