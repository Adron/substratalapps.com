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
| `GET` | `/v1/applications` | List the catalog. Open to any authenticated user — this is what the hub dashboard renders. |
| `GET` | `/v1/applications/{id}` | Fetch one Application. |
| `POST` | `/v1/applications` | Add a catalog entry. Admin/internal only. |
| `PATCH` | `/v1/applications/{id}` | Update a catalog entry. Admin/internal only. |

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
  "visibility": "public"
}
```
```json
// Response — 201, full object with defaults filled in
```

Requires a platform role with `applications.manage` — this is a catalog operation for launching or configuring a product, not something any customer-facing flow triggers. `slug` is immutable once set; it's embedded in permission keys (`app.<slug>.*`) that may already be referenced by Roles.

## Errors specific to this resource

| Code | When |
|---|---|
| `slug_taken` | `slug` collides with an existing Application on create. |
| `application_not_found` | `{id}` doesn't resolve. |
