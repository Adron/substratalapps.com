---
layout: default
title: Roles & Permissions
parent: API Reference
nav_order: 8
---

# Roles & Permissions
{: .no_toc }

See [Domain Model → Roles & Permissions](../../domain-model/roles-and-permissions/) and [Access Control](../../access-control/) for the underlying model.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/permissions` | List every known Permission key. |
| `GET` | `/v1/roles` | List Roles (platform-wide, or filter by `?scope=app_timetrack`). |
| `POST` | `/v1/roles` | Define a new Role. |
| `GET` | `/v1/users/{id}/roles` | List a user's Role assignments. |
| `POST` | `/v1/users/{id}/roles/{roleId}` | Assign a Role to a user. |
| `DELETE` | `/v1/users/{id}/roles/{roleId}` | Remove a Role assignment. |
| `GET` | `/v1/users/{id}/applications/{appId}/effective-permissions` | **The live access check.** Resolves everything in [Access Control](../../access-control/) into one answer. |

## `GET /v1/permissions`

```json
// Response — 200
{
  "data": [
    { "key": "users.manage", "scope": "platform" },
    { "key": "entitlements.manage", "scope": "platform" },
    { "key": "app.timetrack.export", "scope": "app_timetrack" },
    { "key": "app.timetrack.manage_members", "scope": "app_timetrack" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

## `POST /v1/users/{id}/roles/{roleId}`

```json
// Request
{}
```
```json
// Response — 201
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "role_id": "role_timetrack_admin",
  "application_id": "app_timetrack",
  "assigned_at": "2026-10-03T12:05:00Z",
  "assigned_by": "usr_01JAG9SUPPORT0000000000000"
}
```

`application_id` on the response is inherited from the Role's own `scope` — you don't pass it separately. Assigning an app-scoped Role to a user who holds no Entitlement to that app is allowed (the assignment is harmless until they do) but won't grant anything in practice — see [Access Control](../../access-control/#worked-example). Writes an [Audit Event](../audit/) and emits `role.assigned`.

## `GET /v1/users/{id}/applications/{appId}/effective-permissions`

The endpoint described throughout [Trust Model](../../trust-model/) as the live introspection check.

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "entitlement_status": "active",
  "effective_permissions": ["app.timetrack.export", "app.timetrack.manage_members"]
}
```

```json
// Response — 200, entitlement not active
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_invoicer",
  "entitlement_status": "disabled",
  "effective_permissions": []
}
```

Note this always returns `200` with the current state, rather than `403` — it's an introspection query ("what's true right now"), not an attempted action. The caller enforcing access decides what to do with `entitlement_status != "active"`, typically refusing whatever action prompted the check.

## Errors specific to this resource

| Code | When |
|---|---|
| `role_scope_mismatch` | Assigning an app-scoped Role via a call that doesn't match the Role's own `application_id`. |
| `role_not_found` | `{roleId}` doesn't resolve. |
