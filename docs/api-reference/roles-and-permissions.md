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

Platform-scoped keys (fixed, built in) plus every app-scoped key declared by an Application the caller can see. See [Domain Model → Platform permission catalog](../../domain-model/roles-and-permissions/#platform-permission-catalog) for what each platform key grants.

```json
// Response — 200
{
  "data": [
    { "key": "users.list", "scope": "platform" },
    { "key": "users.manage", "scope": "platform" },
    { "key": "entitlements.manage", "scope": "platform" },
    { "key": "applications.manage", "scope": "platform" },
    { "key": "roles.manage", "scope": "platform" },
    { "key": "organizations.manage", "scope": "platform" },
    { "key": "billing.manage", "scope": "platform" },
    { "key": "billing.refund", "scope": "platform" },
    { "key": "audit.view", "scope": "platform" },
    { "key": "webhooks.manage", "scope": "platform" },
    { "key": "api_keys.manage", "scope": "platform" },
    { "key": "app.timetrack.export", "scope": "app_timetrack" },
    { "key": "app.timetrack.manage_members", "scope": "app_timetrack" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

## `GET /v1/roles`

```json
// Response — 200
{
  "data": [
    { "id": "role_platform_superadmin", "name": "superadmin", "scope": "platform", "permissions": ["users.list", "users.manage", "entitlements.manage", "applications.manage", "roles.manage", "organizations.manage", "billing.manage", "billing.refund", "audit.view", "webhooks.manage", "api_keys.manage"] },
    { "id": "role_platform_support", "name": "support", "scope": "platform", "permissions": ["users.list", "entitlements.manage", "audit.view"] },
    { "id": "role_timetrack_admin", "name": "admin", "scope": "app_timetrack", "permissions": ["app.timetrack.export", "app.timetrack.manage_members"] }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

`?scope=platform` or `?scope=app_timetrack` narrows the list to one scope.

## `POST /v1/roles`

```json
// Request
{
  "name": "support_readonly",
  "scope": "platform",
  "permissions": ["users.list", "audit.view"]
}
```
```json
// Response — 201
{
  "id": "role_platform_support_readonly",
  "name": "support_readonly",
  "scope": "platform",
  "permissions": ["users.list", "audit.view"]
}
```

Requires a platform role with `roles.manage`. `permissions` must be a subset of the known Permission keys (see `GET /v1/permissions` above) for the given `scope` — an unknown key returns `422` with `code: "unknown_permission"`.

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

`application_id` on the response is inherited from the Role's own `scope` — you don't pass it separately. Assigning an app-scoped Role to a user who holds no Entitlement to that app is allowed (the assignment is harmless until they do) but won't grant anything in practice — see [Access Control](../../access-control/#worked-example). Requires a platform role with `roles.manage`. Writes an [Audit Event](../audit/) and emits `role.assigned`.

## `DELETE /v1/users/{id}/roles/{roleId}`

```json
// Response — 204
```

Requires a platform role with `roles.manage`. Idempotent — removing a Role assignment that's already gone also returns `204`, not `404`, since the end state ("user does not hold this Role") is already true. Writes an [Audit Event](../audit/) and emits `role.removed`.

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
| `unknown_permission` | `POST /v1/roles` includes a permission key that isn't in the known set for the given `scope`. |
