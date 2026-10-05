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

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/permissions` | any authenticated caller | List every Permission key the caller can see. |
| `GET` | `/v1/roles` | any authenticated caller (filtered) | List Roles. |
| `POST` | `/v1/roles` | `roles.manage` (platform scope); owner or app-confined `roles.manage` (app scope) | Define a new Role. |
| `GET` | `/v1/roles/{id}` | same visibility as list | Fetch one Role. |
| `PATCH` | `/v1/roles/{id}` | same as `POST` for that scope | Change `permissions` or `description`. |
| `DELETE` | `/v1/roles/{id}` | same as `POST` for that scope | Delete an unassigned, non-seed Role. |
| `GET` | `/v1/users/{id}/roles` | self, `roles.manage`, `users.list`, or app-confined | List a user's Role assignments. |
| `POST` | `/v1/users/{id}/roles/{roleId}` | see [Who may assign](#who-may-assign-what) | Assign a Role. |
| `DELETE` | `/v1/users/{id}/roles/{roleId}` | see [Who may assign](#who-may-assign-what) | Remove a Role assignment. |
| `GET` | `/v1/users/{id}/apps/{appId}/effective-permissions` | self, the app's own key, `users.list`, or `entitlements.manage` | **The live access check.** |

## `GET /v1/permissions`

Platform-scoped keys (fixed, built in), plus each visible Application's own declared keys (from its `permissions` field — see [Applications](../applications/#the-application-object)). See [Domain Model → Platform permission catalog](../../domain-model/roles-and-permissions/#platform-permission-catalog) for what each platform key grants.

```json
// Response — 200
{
  "data": [
    { "key": "users.list", "scope": "platform", "description": "List/search User accounts." },
    { "key": "users.manage", "scope": "platform", "description": "Create, update, suspend, delete User accounts." },
    { "key": "entitlements.manage", "scope": "platform", "description": "Grant, toggle, and revoke Entitlements." },
    { "key": "applications.manage", "scope": "platform", "description": "Create and moderate Application catalog entries." },
    { "key": "roles.manage", "scope": "platform", "description": "Define Roles and assign/remove them." },
    { "key": "organizations.manage", "scope": "platform", "description": "Manage any Organization, its members, and org-wide grants." },
    { "key": "billing.manage", "scope": "platform", "description": "View and manage any Tenant's platform subscription." },
    { "key": "billing.refund", "scope": "platform", "description": "Issue platform-subscription refunds and credits." },
    { "key": "audit.view", "scope": "platform", "description": "Query the Audit log for any user." },
    { "key": "webhooks.manage", "scope": "platform", "description": "Manage any caller's webhook subscriptions." },
    { "key": "api_keys.manage", "scope": "platform", "description": "Create, rotate, and revoke any API Key." },
    { "key": "tenants.manage", "scope": "platform", "description": "View any Tenant and run tier changes." },
    { "key": "app.timetrack.export", "scope": "app_timetrack", "description": "Export timesheets as CSV/PDF." },
    { "key": "app.timetrack.manage_members", "scope": "app_timetrack", "description": "Add and remove team members inside TimeTrack." }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Filter with `?scope=platform` or `?scope=app_timetrack`. An app-scoped key sees platform keys plus its own Application's keys only.

## The Role object

```json
{
  "id": "role_timetrack_admin",
  "name": "admin",
  "scope": "app_timetrack",
  "description": "Full control inside TimeTrack.",
  "permissions": ["app.timetrack.export", "app.timetrack.manage_members"],
  "seed": false,
  "assignment_count": 12,
  "created_at": "2025-11-03T00:00:00Z",
  "updated_at": "2026-09-01T00:00:00Z"
}
```

- **`id` is derived, never chosen:** `role_platform_<name>` for platform Roles, and `role_<slug>_<name>` for app Roles, with any `-` in the slug turned into `_`. It's stable and human-readable, by design. See [Conventions → IDs](../conventions/#ids).
- `name` matches `^[a-z][a-z0-9_]{1,40}$` and is unique within its `scope`. For an app Role it **must** be one of the Application's `available_app_roles`.
- `seed: true` marks the four built-in platform Roles (`superadmin`, `support`, `billing_admin`, `member`). They can't be changed or deleted through the API.
- `permissions` must be keys valid for the Role's own scope. A platform Role can hold only platform keys, and an app Role can hold only that Application's `app.<slug>.*` keys. Mixing them returns `422 unknown_permission`.

## `GET /v1/roles`

```json
// Response — 200
{
  "data": [
    { "id": "role_platform_superadmin", "name": "superadmin", "scope": "platform", "seed": true, "permissions": ["users.list", "users.manage", "entitlements.manage", "applications.manage", "roles.manage", "organizations.manage", "billing.manage", "billing.refund", "audit.view", "webhooks.manage", "api_keys.manage", "tenants.manage"] },
    { "id": "role_platform_support", "name": "support", "scope": "platform", "seed": true, "permissions": ["users.list", "entitlements.manage", "audit.view", "tenants.manage", "billing.manage"] },
    { "id": "role_timetrack_admin", "name": "admin", "scope": "app_timetrack", "seed": false, "permissions": ["app.timetrack.export", "app.timetrack.manage_members"] }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Filter by `?scope=platform` or `?scope=<application_id>`. What a caller sees:

- Platform Roles are visible to any User with any platform permission.
- An Application's Roles are visible to its owner, its own key, and `roles.manage` holders.

## `POST /v1/roles`

```json
// Request — a narrower platform role
{ "name": "support_readonly", "scope": "platform", "description": "Read-only support.", "permissions": ["users.list", "audit.view"] }
```
```json
// Response — 201
{ "id": "role_platform_support_readonly", "name": "support_readonly", "scope": "platform", "description": "Read-only support.", "permissions": ["users.list", "audit.view"], "seed": false, "assignment_count": 0, "created_at": "2026-10-05T12:00:00Z", "updated_at": "2026-10-05T12:00:00Z" }
```

- **Platform scope:** requires platform `roles.manage`, and every permission in the Role must be one the caller holds (`403 role_escalation_forbidden`), so nobody can define a Role more powerful than themselves.
- **App scope:** requires the Application's owner, its own key holding `roles.manage`, or platform `roles.manage`. `name` must be in `available_app_roles` (`422 app_role_not_declared`), which means a new app Role usually starts with a `PATCH` to the Application's `available_app_roles`, and that `PATCH` seeds the Role automatically. In practice, `POST` for an app scope is only needed to recreate a deleted Role.
- `409 role_name_taken` if the derived `id` already exists.
- Writes `role.created`.

## `PATCH /v1/roles/{id}`

```json
// Request
{ "permissions": ["app.timetrack.export", "app.timetrack.manage_members", "app.timetrack.approve"] }
```
```json
// Response — 200, the full updated Role
```

`permissions` (replaced wholesale) and `description` only. A change takes effect immediately for every holder, on their next request, introspection call, or app token. Writes `role.updated`, which records the before and after permission sets. The same escalation rule as `POST` applies to platform Roles. Seed Roles return `403 seed_role_immutable`.

{: .note }
Changing a Role's permissions doesn't fire a webhook per holder. Apps learn of the change the next time they check (an app token is valid for at most 5 minutes, so they find out within 5 minutes). A permission *removed* from a Role is the one case that could matter for immediate revocation. If that's a concern, remove the Role assignment instead, which fires `role.removed`.

## `DELETE /v1/roles/{id}`

`204`. Only for a non-seed Role with zero assignments (`409 role_in_use`). For an app Role, it's simpler to remove the name from `available_app_roles`, which deletes the Role too. Writes `role.deleted`. Destructive.

## `GET /v1/users/{id}/roles`

```json
// Response — 200
{
  "data": [
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "role_id": "role_platform_member", "application_id": null, "assigned_at": "2026-01-14T18:02:11Z", "assigned_by": { "type": "system", "id": "system" }, "implicit": false },
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "role_id": "role_timetrack_admin", "application_id": "app_timetrack", "assigned_at": "2026-10-03T12:05:00Z", "assigned_by": { "type": "user", "id": "usr_01JAG9SUPPORT0000000000000" }, "implicit": false },
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "role_id": "role_invoicer_member", "application_id": "app_invoicer", "assigned_at": null, "assigned_by": null, "implicit": true }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

`implicit: true` rows come from an Application's `default_app_role`. They're listed for transparency but have no assignment row, so they can't be removed. Filter with `?application_id=`. An app-confined caller sees only its own Application's rows.

## Who may assign what

| Role being assigned | Caller must be |
|---|---|
| Platform Role | Platform `roles.manage` **and** hold every permission the Role grants (`403 role_escalation_forbidden`). Assigning `superadmin` therefore requires being a `superadmin`. |
| App Role | Platform `roles.manage`, **or** the Application's owner, **or** the Application's own key with `roles.manage`. An app key assigning another app's Role gets `403 role_scope_mismatch`. |

## `POST /v1/users/{id}/roles/{roleId}`

```json
// Request
{}
```
```json
// Response — 201 (or 200 if the user already held it — assignment is idempotent)
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "role_id": "role_timetrack_admin",
  "application_id": "app_timetrack",
  "assigned_at": "2026-10-03T12:05:00Z",
  "assigned_by": { "type": "user", "id": "usr_01JAG9SUPPORT0000000000000" },
  "implicit": false
}
```

`application_id` is inherited from the Role's own `scope`, so you don't pass it. Assigning an app Role to a user with no Entitlement to that app is allowed. The assignment is harmless until they get access, but it grants nothing in practice (see [Access Control](../../access-control/#worked-example)). Writes `role.assigned` and emits `role.assigned`. Re-assigning a Role the user already holds returns `200` with the existing assignment, and no event fires.

## `DELETE /v1/users/{id}/roles/{roleId}`

```json
// Response — 204
```

Idempotent: removing an assignment that's already gone also returns `204`, since the end state ("user doesn't hold this Role") is already true. Writes `role.removed` and emits `role.removed`, but only when something was actually removed. A user's last `member` platform Role can't be removed (`409 member_role_required`), because every User holds it. Destructive.

## `GET /v1/users/{id}/apps/{appId}/effective-permissions`

The live introspection check described throughout [Trust Model](../../trust-model/#2-live-introspection).

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "allowed": true,
  "user_status": "active",
  "entitlement_status": "active",
  "access_paths": [
    { "source": "purchase", "entitlement_id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A", "status": "active" }
  ],
  "roles": ["role_timetrack_admin", "role_timetrack_member"],
  "effective_permissions": ["app.timetrack.export", "app.timetrack.manage_members"],
  "computed_at": "2026-10-05T12:00:00Z"
}
```

```json
// Response — 200, entitlement not active
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_invoicer",
  "allowed": false,
  "user_status": "active",
  "entitlement_status": "disabled",
  "access_paths": [
    { "source": "purchase", "entitlement_id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9", "status": "disabled" }
  ],
  "roles": ["role_invoicer_member"],
  "effective_permissions": [],
  "computed_at": "2026-10-05T12:00:00Z"
}
```

- `allowed` is `user_status == "active" && entitlement_status == "active"`. It's the one boolean most callers need.
- `entitlement_status` is the *resolved* status across every path (see [Access Control](../../access-control/#the-algorithm)). It's `"none"` if the user has no path at all.
- `effective_permissions` is **always empty when `allowed` is false**. When `allowed` is true, it contains only this Application's `app.<slug>.*` keys, from explicit and `default_app_role` assignments. Platform permissions never appear here.
- `access_paths` lists every path, including org grants with their `organization_id` and `member_decision`, so a support agent can see *why*.
- This always returns `200` with the current state, never `403`. It's an introspection query ("what's true right now"), not an attempted action. The caller enforcing access decides what to do with `allowed: false`.
- Rate limit is 300/min per key (see [Non-Functional Requirements → Rate limiting](../../non-functional-requirements/#rate-limiting)). Apps on a hot path should prefer the app token, and call this before sensitive actions.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `role_not_found` | 404 | `{roleId}`/`{id}` doesn't resolve or isn't visible. |
| `role_name_taken` | 409 | `POST` would create a Role whose derived id exists. |
| `app_role_not_declared` | 422 | App Role `name` isn't in the Application's `available_app_roles`. |
| `unknown_permission` | 422 | A permission key isn't valid for the Role's scope. |
| `role_escalation_forbidden` | 403 | Defining or assigning a platform Role with permissions the caller doesn't hold. |
| `role_scope_mismatch` | 403 | An app key acting on another Application's Role. |
| `seed_role_immutable` | 403 | `PATCH`/`DELETE` on a seed Role. |
| `role_in_use` | 409 | `DELETE` on a Role with assignments. |
| `member_role_required` | 409 | Removing a user's `member` platform Role. |
| `plan_limit_reached` | 409 | Exceeding the AppRoles cap — see [Pricing → Enforcement](../../pricing/#enforcement). |
