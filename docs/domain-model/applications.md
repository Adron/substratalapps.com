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

A catalog entry for one developer's app — built on Substratal Apps for its user/org/tenancy/settings/storage layer. "Product," not "running process": the app itself, as a piece of software, lives and is built elsewhere; this record is how the platform knows it exists, what it's called, where to send a user at launch, and what settings/roles it defines for itself.

Every Application today is built by Substratal itself — `owner_user_id` is a Substratal-internal account either way, so nothing below changes shape when that stops being true. A marketplace where outside developers register and manage their own Applications is an explicit later phase; see [Decisions → App developer/publisher model](../../decisions/#9-app-developerpublisher-model). What *is* already in this entity because of that future — not bolted on afterward — is an owner and a review status, both described below.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `app_` prefix. |
| `slug` | string | Stable, URL-safe identifier — used in permission keys (`app.<slug>.export`) and settings scoping. |
| `name` | string | Display name. |
| `description` | string | |
| `icon_url` | string | |
| `launch_url` | string | Where the platform redirects an authenticated user. See [Trust Model](../../trust-model/) for what travels with that redirect. |
| `settings_schema` | object (JSON Schema) | What this app declares its [AppSettings](../settings/) must validate against. See [Decisions → Settings schema ownership](../../decisions/#5-settings-schema-ownership). |
| `available_app_roles` | array of strings | The role vocabulary this app defines for itself, e.g. `["admin", "editor", "viewer"]`. Used by role-assignment UI and by [AppRole](../roles-and-permissions/#approle) validation. |
| `visibility` | enum | `public` (anyone can acquire it) \| `invite_only` \| `internal` (Substratal's own tooling, not end-user-facing). |
| `owner_user_id` | string, nullable | The developer who registered and manages this Application. Null only for Applications with no single human owner (rare — effectively a system app). |
| `owner_organization_id` | string, nullable | Set instead of `owner_user_id` when an [Organization](../users-and-organizations/#organization), not an individual, owns the app. |
| `review_status` | enum | `approved` \| `pending_review` \| `suspended`. Every Application created today is admin-created and defaults to `approved` — see [below](#who-can-manage-an-applications-catalog-entry). This exists now specifically so self-service submission doesn't need a breaking schema change later. |
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
  "owner_user_id": "usr_01JAG0SUBSTRATAL0000000000",
  "owner_organization_id": null,
  "review_status": "approved",
  "created_at": "2025-11-03T00:00:00Z"
}
```

## Who can manage an Application's catalog entry

Two distinct rights, not one:

- **The app's own owner** (`owner_user_id`, or any member of `owner_organization_id`) can edit their own Application's `description`, `icon_url`, `launch_url`, `settings_schema`, and `available_app_roles` — the day-to-day configuration of their own app.
- **A platform role with `applications.manage`** (Substratal staff) can edit anything, including `visibility` and `review_status` — moderation, not configuration.

Today, with every Application first-party, these two rights are usually held by the same people and the distinction is invisible. It stops being invisible the moment a third-party developer registers their own app — `POST /v1/applications` still requires platform `applications.manage` in the current spec (first-party only, consistent with [Decisions → App developer/publisher model](../../decisions/#9-app-developerpublisher-model)), and self-service registration into a `pending_review` queue is the marketplace-phase feature that `review_status` already models the shape of. See [API Reference → Applications](../../api-reference/applications/) for the endpoint-level detail.

## Relationship to everything else

An Application is the scope for: [AppRole](../roles-and-permissions/#approle) (what roles it defines), [AppProfile](../profiles/#appprofile) and [AppSettings](../settings/#appsettings) (per-user, per-app data), and [Entitlement](../entitlements/) (what a user actually owns). It doesn't hold any per-user state itself — it's the catalog definition, not a tenant record.
