---
layout: default
title: Settings
parent: API Reference
nav_order: 5
---

# Settings
{: .no_toc }

See [Domain Model → Settings](../../domain-model/settings/) for the three-layer resolution model (`AppSettings → Settings → Application default`) this resource implements.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/users/{id}/settings` | Fetch global Settings. |
| `PATCH` | `/v1/users/{id}/settings` | Update global Settings. |
| `GET` | `/v1/users/{id}/apps/{appId}/settings` | Fetch the fully-resolved per-app settings. |
| `PATCH` | `/v1/users/{id}/apps/{appId}/settings` | Write per-app overrides. |

## `GET /v1/users/{id}/settings`

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "locale": "en-US",
  "timezone": "America/Denver",
  "theme": "dark",
  "notifications": { "email": true, "sms": false },
  "updated_at": "2026-08-11T10:00:00Z"
}
```

## `GET /v1/users/{id}/apps/{appId}/settings`

Returns the resolved object, plus the override layer that produced it:

```json
// Response — 200
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

See [Workflows → Settings resolution, in practice](../../workflows/#settings-resolution-in-practice) for exactly how `resolved` is computed.

## `PATCH /v1/users/{id}/apps/{appId}/settings`

```json
// Request — only the override being set
{ "overrides": { "week_start": "monday" } }
```
```json
// Response — 200, same shape as the GET above, reflecting the new override
```

Validated against the target Application's `settings_schema` (see [Applications](../applications/)) — a key not declared in the schema, or a value of the wrong type, returns `422` with `code: "settings_schema_violation"` and the schema validation error in `details`. See [Decisions → Settings schema ownership](../../decisions/#5-settings-schema-ownership) if this validation step turns out to be out of scope for the MVP.

To clear a single override back to inherited, send that key's value as `null`:

```json
{ "overrides": { "week_start": null } }
```
