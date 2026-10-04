---
layout: default
title: Applications
parent: Domain Model
nav_order: 2
---

# Applications
{: .no_toc }

1. TOC
{: toc }

---

## What it represents

A catalog entry for a product or service Substratal sells — "Product," not "running process." The thing a user sees listed on their hub dashboard and gets routed to when they launch it. The app itself, as a piece of software, lives and is built elsewhere; this record is how the hub knows it exists, what it's called, where to send a user, and what settings/roles it defines for itself.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `app_` prefix. |
| `slug` | string | Stable, URL-safe identifier — used in permission keys (`app.<slug>.export`) and settings scoping. |
| `name` | string | Display name. |
| `description` | string | |
| `icon_url` | string | |
| `launch_url` | string | Where the hub redirects an authenticated user. See [Trust Model](../../trust-model/) for what travels with that redirect. |
| `settings_schema` | object (JSON Schema) | What this app declares its [AppSettings](../settings/) must validate against. See [Decisions → Settings schema ownership](../../decisions/#5-settings-schema-ownership). |
| `available_app_roles` | array of strings | The role vocabulary this app defines for itself, e.g. `["admin", "editor", "viewer"]`. Used by role-assignment UI and by [AppRole](../roles-and-permissions/#approle) validation. |
| `visibility` | enum | `public` (anyone can acquire it) \| `invite_only` \| `internal` (Substratal's own tooling, not customer-facing). |
| `created_at` | timestamp | |

## Example

```json
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

## Catalog writes are not a customer-facing operation

`GET /v1/applications` is open to any authenticated user (it's the catalog the dashboard renders). Creating or editing an Application is an internal/admin operation — a new product launch, not something any customer-facing flow triggers. See [API Reference → Applications](../../api-reference/applications/).

## Relationship to everything else

An Application is the scope for: [AppRole](../roles-and-permissions/#approle) (what roles it defines), [AppProfile](../profiles/#appprofile) and [AppSettings](../settings/#appsettings) (per-user, per-app data), and [Entitlement](../entitlements/) (what a user actually owns). It doesn't hold any per-user state itself — it's the catalog definition, not a tenant record.
