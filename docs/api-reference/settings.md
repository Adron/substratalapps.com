---
layout: default
title: Settings
parent: API Reference
nav_order: 5
---

# Settings
{: .no_toc }

See [Domain Model → Settings](../../domain-model/settings/) for the resolution model this resource implements: reserved keys fall through `AppSettings → global Settings`, declared keys fall through `AppSettings → the Application's default`.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/users/{id}/settings` | self, or platform `users.list` | Fetch global Settings. |
| `PATCH` | `/v1/users/{id}/settings` | self or `users.manage` | Update global Settings. |
| `GET` | `/v1/users/{id}/apps/{appId}/settings` | self, the app's own key, or platform `users.list` | Fetch the fully resolved per-app settings. |
| `PATCH` | `/v1/users/{id}/apps/{appId}/settings` | self, the app's own key, or `users.manage` | Write per-app overrides. |

As on [Profiles](../profiles/), "the app's own key" means any app-scoped [API Key](../api-keys/) scoped to `{appId}`, with no extra permission needed. Self and app-key access to an Application's settings requires the user to have *active* access to that Application. Otherwise the call returns `403 entitlement_required`. Support bypasses that check for investigations: platform `users.list` (held by the `support` Role) can read regardless, and `users.manage` can read and write regardless.

## `GET /v1/users/{id}/settings`

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "locale": "en-US",
  "timezone": "America/Denver",
  "theme": "dark",
  "notifications": { "email": true, "sms": false, "push": true },
  "updated_at": "2026-08-11T10:00:00Z"
}
```

## `PATCH /v1/users/{id}/settings`

```json
// Request
{ "timezone": "Europe/Dublin", "notifications": { "sms": true } }
```
```json
// Response — 200, the full updated Settings — notifications merged one level deep
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "locale": "en-US",
  "timezone": "Europe/Dublin",
  "theme": "dark",
  "notifications": { "email": true, "sms": true, "push": true },
  "updated_at": "2026-10-05T10:05:00Z"
}
```

| Field | Validation |
|---|---|
| `locale` | BCP 47 tag the API recognizes. |
| `timezone` | IANA zone name, for example `America/Denver`. |
| `theme` | `light`, `dark`, or `system`. |
| `notifications` | An object of channel name to boolean. Channel names match `^[a-z][a-z0-9_]{0,31}$`, with at most 20 channels. The keys are merged one level deep, and a channel sent as `null` is removed. |

Defaults on user creation: `locale: "en-US"`, `timezone: "UTC"`, `theme: "system"`, `notifications: {"email": true}`. This is the only place `locale` is written; the Profile's `locale` mirrors it.

A failed validation lists every field at once:

```json
// PATCH { "timezone": "Mountain Time", "theme": "blue", "notifications": { "Push-Alerts": true } }
{
  "error": {
    "code": "validation_failed",
    "message": "3 fields failed validation.",
    "details": {
      "fields": [
        { "field": "timezone", "code": "unknown_value" },
        { "field": "theme", "code": "enum_mismatch", "allowed": ["light", "dark", "system"] },
        { "field": "notifications.Push-Alerts", "code": "invalid_format", "pattern": "^[a-z][a-z0-9_]{0,31}$" }
      ]
    }
  }
}
```

## `GET /v1/users/{id}/apps/{appId}/settings`

Returns the resolved object, the override layer that produced it, and the source of each resolved value:

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "resolved": {
    "locale": "en-US",
    "timezone": "America/Denver",
    "theme": "dark",
    "notifications": { "email": true, "sms": false, "push": false },
    "default_billable": true,
    "week_start": "monday"
  },
  "sources": {
    "locale": "global",
    "timezone": "global",
    "theme": "global",
    "notifications": "app_override",
    "default_billable": "app_default",
    "week_start": "app_override"
  },
  "overrides": { "week_start": "monday", "notifications": { "push": false } },
  "stale_overrides": [],
  "updated_at": "2026-09-20T14:15:00Z"
}
```

The exact rules are in [Domain Model → Settings → Resolution rules](../../domain-model/settings/#resolution-rules). In short:

- The **reserved global keys** (`locale`, `timezone`, `theme`, `notifications`) always appear. Each comes from the per-app override if one is set, and from global Settings otherwise. For `notifications`, an override merges over the global object one channel at a time.
- **Keys the Application declares** in its `settings_schema` come from the override if one is set, and otherwise from the schema property's `default`. A declared key with neither is **omitted** from `resolved` rather than returned as `null`.
- **`stale_overrides`** lists override keys that no longer validate against the Application's *current* schema. This happens when the schema changed after the value was written: the property was removed, or its type changed. Stale values are skipped during resolution, so the default applies instead, and they stay in `overrides` until the next write to that key replaces or clears them.
- If the user has no per-app record yet, `overrides` is `{}` and `updated_at` is `null`. Nothing is written on a read.

## `PATCH /v1/users/{id}/apps/{appId}/settings`

```json
// Request — set one override
{ "overrides": { "week_start": "monday" } }
```
```json
// Request — clear an override back to inherited
{ "overrides": { "week_start": null } }
```
```json
// Response — 200, same shape as the GET above, reflecting the write
```

Each key in `overrides` is validated on its own before anything is written:

- A **reserved global key** must satisfy the same rules as on `PATCH /v1/users/{id}/settings`.
- **Any other key** must be declared in the Application's `settings_schema`, and its value must validate against that property's schema.
- `null` always means "clear this override" and is never validated.
- The serialized `overrides` object is limited to 16 KB after the merge.

A key the schema doesn't declare, a value of the wrong type, or a reserved key that fails its platform rule returns `422 settings_schema_violation`. The error lists every failure, not just the first:

```json
{
  "error": {
    "code": "settings_schema_violation",
    "message": "2 overrides failed validation against app_timetrack's settings_schema.",
    "details": {
      "fields": [
        { "field": "overrides.week_start", "code": "enum_mismatch", "allowed": ["sunday", "monday"] },
        { "field": "overrides.color", "code": "undeclared_key" }
      ]
    }
  }
}
```

A self-service write or an app-key write produces no Audit Event. An admin writing someone else's settings produces `settings.updated`. Send `If-Match` to avoid two writers clobbering each other. See [Conventions → Concurrency](../conventions/#concurrency-etag--if-match).

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `settings_schema_violation` | 422 | An override fails the Application's `settings_schema`. Per-field detail is in `details.fields`. |
| `entitlement_required` | 403 | Self or app-key access for a user without active access to the Application. |
| `application_not_found` | 404 | `{appId}` doesn't resolve, or isn't the calling app key's own Application. |
| `validation_failed` | 422 | A global Settings `PATCH` breaks a field rule; see the example above. (Per-app overrides report through `settings_schema_violation` instead, even for reserved keys.) |
