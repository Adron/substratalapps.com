---
layout: default
title: Profiles
parent: API Reference
nav_order: 4
---

# Profiles
{: .no_toc }

See [Domain Model → Profiles](../../domain-model/profiles/) for the global-vs-per-app model this resource implements.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/users/{id}/profile` | Fetch global Profile. |
| `PATCH` | `/v1/users/{id}/profile` | Update global Profile. |
| `GET` | `/v1/users/{id}/apps/{appId}/profile` | Fetch per-app AppProfile. Creates a default one on first read if none exists. |
| `PATCH` | `/v1/users/{id}/apps/{appId}/profile` | Update per-app AppProfile. |

## `GET /v1/users/{id}/profile`

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "display_name": "Jordan Alvarez",
  "avatar_url": "https://assets.substratalapps.com/avatars/usr_01JAG3.png",
  "contact_email": "jordan@example.com",
  "contact_phone": null,
  "locale": "en-US",
  "updated_at": "2026-09-20T14:12:00Z"
}
```

## `PATCH /v1/users/{id}/profile`

```json
// Request — only the fields being changed
{ "display_name": "Jordan A." }
```
```json
// Response — 200, full updated object
```

## `GET /v1/users/{id}/apps/{appId}/profile`

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "display_handle": "j.alvarez",
  "custom": { "department": "Engineering" },
  "updated_at": "2026-09-20T14:15:00Z"
}
```

A caller may be the app itself (via its service API key, scoped to only its own `application_id`), the user themselves, or an admin — see [Trust Model](../../trust-model/) for how an app's key is scoped.

404s with `entitlement_required` instead of auto-creating a record if the user has no active [Entitlement](../../domain-model/entitlements/) to that app — a disabled or never-purchased app shouldn't silently accumulate profile data for a user who can't reach it.

## `PATCH /v1/users/{id}/apps/{appId}/profile`

```json
// Request
{ "custom": { "department": "Engineering", "seat_number": 14 } }
```
```json
// Response — 200, full updated object
```

`custom` is replaced wholesale per the keys you send merged over the existing object (shallow merge) — send the full set of custom fields you want present if you're clearing one out, since omitted keys are left untouched rather than deleted. Unlike [AppSettings](../../domain-model/settings/#appsettings), this is not validated against a schema — see [AppProfile](../../domain-model/profiles/#appprofile) for why.
