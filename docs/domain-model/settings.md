---
layout: default
title: Settings
parent: Domain Model
nav_order: 6
---

# Settings
{: .no_toc }

1. TOC
{: toc }

---

## The split: global, per-app, and defaults

Settings is configuration, not identity — see [Profiles](../profiles/) for the identity-data counterpart to this same global/per-app split. Three layers, read in this order:

```
AppSettings override (this user, this app)
  → Settings override (this user, global)
    → Application's declared default for that key
```

The hub resolves this fallthrough server-side. See [Workflows → Settings resolution, in practice](../../workflows/#settings-resolution-in-practice).

| Field | Global `Settings` | `AppSettings` (per User × Application) |
|---|---|---|
| `locale`, `timezone`, `theme` | ✓ | Inherits from global unless set |
| notification preferences | ✓ (site-wide channel defaults) | ✓ (per-app channel overrides) |
| app-specific preferences | — | ✓ arbitrary JSON, validated against that app's `settings_schema` |

## Settings (global)

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | One per User. |
| `locale` | string | |
| `timezone` | string | IANA zone, e.g. `America/Denver`. |
| `theme` | enum | `light` \| `dark` \| `system`. |
| `notifications` | object | Site-wide channel defaults, e.g. `{"email": true, "sms": false}`. |
| `updated_at` | timestamp | |

### Example

```json
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "locale": "en-US",
  "timezone": "America/Denver",
  "theme": "dark",
  "notifications": { "email": true, "sms": false },
  "updated_at": "2026-08-11T10:00:00Z"
}
```

## AppSettings

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `application_id` | string | |
| `overrides` | object | Only the keys this user has explicitly overridden for this app — not a full merged object. The API's read endpoint returns the resolved merge; this is the write-layer shape. |
| `updated_at` | timestamp | |

Validated on write against `Application.settings_schema` (see [Applications](../applications/)) — see [Decisions → Settings schema ownership](../../decisions/#5-settings-schema-ownership) for whether this validation is in scope for the MVP.

### Example

Write (only the override):
```json
{ "overrides": { "week_start": "monday" } }
```

Read, resolved (`GET /v1/users/{id}/apps/{appId}/settings`):
```json
{
  "application_id": "app_timetrack",
  "resolved": {
    "locale": "en-US",
    "theme": "dark",
    "default_billable": true,
    "week_start": "monday"
  },
  "overrides": { "week_start": "monday" }
}
```

Here, `locale` and `theme` fell through to global [Settings](#settings-global), `default_billable` fell through to the Application's declared default, and only `week_start` reflects an explicit per-app override.

## Why the write shape and the read shape differ

Writing only the override keeps a clean record of what the *user* actually chose versus what they're merely inheriting — necessary so that if the Application changes its default for `default_billable` tomorrow, every user who never overrode it picks up the new default automatically, while anyone who explicitly set it keeps their choice. Returning the fully resolved object on read means callers never re-implement the three-layer fallthrough themselves.
