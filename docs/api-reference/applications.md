---
layout: default
title: Applications
parent: API Reference
nav_order: 6
---

# Applications
{: .no_toc }

See [Domain Model → Applications](../../domain-model/applications/) for the full field reference.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/applications` | List the catalog. Open to any authenticated user. |
| `GET` | `/v1/applications/{id}` | Fetch one Application. |
| `POST` | `/v1/applications` | Register a new Application. Platform-admin only today. |
| `PATCH` | `/v1/applications/{id}` | Update a catalog entry. The app's own owner, or a platform admin. |

## `GET /v1/applications`

```json
// Response — 200
{
  "data": [
    {
      "id": "app_timetrack",
      "slug": "timetrack",
      "name": "TimeTrack",
      "icon_url": "https://assets.substratalapps.com/apps/timetrack/icon.png",
      "visibility": "public"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

List responses return a trimmed view (no `settings_schema`, no `launch_url`) — fetch the single resource for the full object when you need it.

## `GET /v1/applications/{id}`

```json
// Response — 200
{
  "id": "app_timetrack",
  "slug": "timetrack",
  "name": "TimeTrack",
  "description": "Time tracking and timesheet export for teams.",
  "icon_url": "https://assets.substratalapps.com/apps/timetrack/icon.png",
  "launch_url": "https://timetrack.substratalapps.com/sso/launch",
  "settings_schema": {
    "type": "object",
    "properties": {
      "default_billable": { "type": "boolean" },
      "week_start": { "type": "string", "enum": ["sunday", "monday"] }
    }
  },
  "available_app_roles": ["admin", "member"],
  "visibility": "public",
  "owner_user_id": "usr_01JAG0SUBSTRATAL0000000000",
  "owner_organization_id": null,
  "review_status": "approved",
  "created_at": "2025-11-03T00:00:00Z"
}
```

## `POST /v1/applications`

```json
// Request
{
  "slug": "invoicer",
  "name": "Invoicer",
  "launch_url": "https://invoicer.substratalapps.com/sso/launch",
  "available_app_roles": ["admin", "member"],
  "visibility": "public",
  "owner_user_id": "usr_01JAG0SUBSTRATAL0000000000"
}
```
```json
// Response — 201, full object with defaults filled in — review_status defaults to "approved"
```

Requires a platform role with `applications.manage`. Today this is Substratal-internal only — a new product launch, not something any developer-facing flow triggers — consistent with [Decisions → App developer/publisher model](../../decisions/#9-app-developerpublisher-model): self-service registration by a third-party developer is a later phase, not supported by this endpoint yet. `slug` is immutable once set; it's embedded in permission keys (`app.<slug>.*`) that may already be referenced by Roles.

## `PATCH /v1/applications/{id}`

```json
// Request — only the fields being changed
{ "description": "Time tracking, timesheet export, and billable-hours reporting for teams.", "available_app_roles": ["admin", "editor", "member"] }
```
```json
// Response — 200, full updated object
```

Two different callers, two different scopes:

- **The Application's own owner** (`owner_user_id`, or a member of `owner_organization_id`) may change `description`, `icon_url`, `launch_url`, `settings_schema`, `available_app_roles` — their own app's configuration.
- **A platform role with `applications.manage`** may change anything, including `visibility` and `review_status` — moderation. An owner attempting to change `review_status` or `visibility` gets `403`.

`slug` cannot be changed via this call by either caller (see above). Adding a new entry to `available_app_roles` is backward compatible; removing one that's still referenced by an existing [AppRole](../../domain-model/roles-and-permissions/#approle) assignment returns `409` with `code: "app_role_in_use"`.

## Errors specific to this resource

| Code | When |
|---|---|
| `slug_taken` | `slug` collides with an existing Application on create. |
| `application_not_found` | `{id}` doesn't resolve. |
| `app_role_in_use` | `PATCH` would remove an entry from `available_app_roles` that's still assigned to at least one user. |
| `moderation_field_forbidden` | A non-admin owner's `PATCH` attempts to change `visibility` or `review_status`. |
