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

Settings is configuration, not identity — see [Profiles](../profiles/) for the identity-data counterpart to this same global/per-app split. There are three layers, but no single key passes through all three. Keys come in two disjoint kinds, and each kind falls through two layers:

```
Reserved global keys (locale, timezone, theme, notifications):
  AppSettings override (this user, this app)
    → global Settings (this user)

Keys the Application declares in its settings_schema:
  AppSettings override (this user, this app)
    → the Application's declared default for that key
```

An Application can't declare a reserved key, and global Settings holds nothing but reserved keys, so the two chains never overlap. The hub resolves both server-side, exactly as in [Resolution rules](#resolution-rules) below. See [Workflows → Settings resolution, in practice](../../workflows/#settings-resolution-in-practice).

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

### Schema validation

Validated on write against `Application.settings_schema` (see [Applications](../applications/)). The schema is on file with the hub: each Application registers its own JSON Schema, and the hub validates every write against it rather than storing an opaque blob. Centralized validation is why `GET /v1/users/{id}/apps/{appId}/settings` can promise a resolved, valid object instead of whatever was last written. The same schema also tells the storage layer which declared fields to back with typed, indexed columns; see [Database Schema → Typed fields](../database-schema/#typed-fields-per-application-views-and-expression-indexes).

### Example

Write (only the override):
```json
{ "overrides": { "week_start": "monday" } }
```

Read, resolved (`GET /v1/users/{id}/apps/{appId}/settings`):
```json
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "resolved": {
    "locale": "en-US",
    "timezone": "America/Denver",
    "theme": "dark",
    "notifications": { "email": true, "sms": false },
    "default_billable": true,
    "week_start": "monday"
  },
  "sources": {
    "locale": "global",
    "timezone": "global",
    "theme": "global",
    "notifications": "global",
    "default_billable": "app_default",
    "week_start": "app_override"
  },
  "overrides": { "week_start": "monday" },
  "stale_overrides": [],
  "updated_at": "2026-09-20T14:15:00Z"
}
```

Here, all four reserved keys fell through to global [Settings](#settings-global) (they always appear), `default_billable` fell through to the Application's declared default, and only `week_start` reflects an explicit per-app override. `sources` says which layer each resolved value came from: `global`, `app_override`, or `app_default`.

## Resolution rules

The exact algorithm behind `GET /v1/users/{id}/apps/{appId}/settings`. Implementations and tests should follow it literally.

There are two disjoint key spaces:

- **Reserved global keys:** `locale`, `timezone`, `theme`, `notifications`. These are defined by the platform. An Application's `settings_schema` may **not** declare them, and declaring one returns `422 reserved_settings_key` on the Application write.
- **Declared keys:** every property in the Application's `settings_schema`.

```
for key in reserved_global_keys:
    if key == "notifications":
        resolved.notifications = merge_one_level(global.notifications, valid_override("notifications") or {})
    else:
        resolved[key] = valid_override(key) ?? global[key]

for key, property in settings_schema.properties:
    if valid_override(key) exists:   resolved[key] = override
    elif "default" in property:      resolved[key] = property.default
    else:                            omit key

valid_override(key) := overrides[key] if it is valid right now, else
                       treat as absent and list key in stale_overrides

"valid right now" means:
  reserved key  → passes the same platform rules as PATCH /v1/users/{id}/settings
                  (BCP 47 locale, IANA timezone, theme enum, notifications channel map)
  declared key  → validates against that property in the Application's CURRENT settings_schema
```

The Application's declared default is the JSON Schema `default` keyword on each property, so there's no separate defaults field to keep in sync. A user can override a reserved global key per app. For example, `theme: "light"` in one app while their global theme stays `dark`.

### `settings_schema` rules

The rules an Application's schema must follow. They're checked on `POST`/`PATCH /v1/applications`, and a violation returns `422 invalid_settings_schema` with per-path details.

- JSON Schema **draft 2020-12**. The root must be `{"type": "object", "properties": {...}}`. The server always treats `additionalProperties` as `false`, whatever the schema says.
- At most 200 properties, at most 64 KB serialized, and property names must match `^[a-z][a-z0-9_]{0,62}$`.
- Supported property types: `string` (optionally `enum`, `format: "date-time"`, `maxLength`), `boolean`, `integer`, `number` (with optional `minimum`/`maximum`), and `object`/`array`. Objects and arrays are stored, but never get a typed projection; see [Database Schema → Typed fields](../database-schema/#typed-fields-per-application-views-and-expression-indexes).
- `$ref` is supported only within the document, and remote `$ref` is rejected.
- A `default`, if present, must itself validate against its property.
- **`x-pii: true`** on a property marks it as personal data. The [hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade) removes PII-marked keys from a deleted user's overrides and leaves the rest, such as a `week_start` preference. A schema without `x-pii` marks is treated as having no PII.
- **Changing a schema never rewrites stored data.** Removing a property, or changing its type, makes existing overrides for it *stale* (see `stale_overrides` above). It doesn't make them errors. The Application's owner can see how many users hold stale values per key in `GET /v1/applications/{id}` → `settings_schema_stats`.

### Example: a schema change makes an override stale

TimeTrack's schema declares `week_start` as a string, and a user overrides it:

```json
// PATCH /v1/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/apps/app_timetrack/settings
{ "overrides": { "week_start": "monday" } }
```

Later, the owner changes `week_start` to an integer day number (0 = Sunday) with a default of `0`:

```json
// PATCH /v1/applications/app_timetrack
{ "settings_schema": { "type": "object", "properties": {
    "default_billable": { "type": "boolean", "default": true },
    "week_start": { "type": "integer", "minimum": 0, "maximum": 6, "default": 0 }
} } }
```

Nothing stored is rewritten. The user's next read skips the stale value and falls back to the new default:

```json
// GET /v1/users/usr_01JAG3Z9X8QS3F6K2M4N5P6R7S/apps/app_timetrack/settings (excerpt)
{
  "resolved": { "default_billable": true, "week_start": 0, "...": "..." },
  "sources": { "default_billable": "app_default", "week_start": "app_default", "...": "..." },
  "overrides": { "week_start": "monday" },
  "stale_overrides": ["week_start"]
}
```

The owner sees the count across all users on the Application:

```json
// GET /v1/applications/app_timetrack (excerpt)
{ "settings_schema_stats": { "stale_override_counts": { "week_start": 1 } } }
```

The stale value stays in `overrides` until that user (or the app's backend) writes `week_start` again, for example `{ "overrides": { "week_start": 1 } }`, or clears it with `null`. Either write removes it from `stale_overrides` and the count drops back to zero.

## Why the write shape and the read shape differ

Writing only the override keeps a clean record of what the *user* actually chose versus what they're merely inheriting — necessary so that if the Application changes its default for `default_billable` tomorrow, every user who never overrode it picks up the new default automatically, while anyone who explicitly set it keeps their choice. Returning the fully resolved object on read means callers never re-implement the three-layer fallthrough themselves.
