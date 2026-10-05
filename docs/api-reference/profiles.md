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

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/users/{id}/profile` | self, `users.list`, or app-confined `users.list` | Fetch global Profile. |
| `PATCH` | `/v1/users/{id}/profile` | self or `users.manage` | Update global Profile. |
| `GET` | `/v1/users/{id}/apps/{appId}/profile` | self, the app's own key, or `users.manage` | Fetch per-app AppProfile. |
| `PATCH` | `/v1/users/{id}/apps/{appId}/profile` | self, the app's own key, or `users.manage` | Update per-app AppProfile. |

"The app's own key" means an app-scoped [API Key](../api-keys/) whose scope is `{appId}`. Any app-scoped key can read and write AppProfile for its own Application, with no extra permission, as long as the user has an access path to that Application. It can never reach another Application's AppProfile (`404`).

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

An app-scoped key with `users.list` can read (never write) the global Profile of users who have an access path to its Application, which is how an app gets a user's name and avatar.

## `PATCH /v1/users/{id}/profile`

```json
// Request — only the fields being changed
{ "display_name": "Jordan A.", "contact_phone": "+13035550142" }
```
```json
// Response — 200, full updated object
```

| Field | Validation |
|---|---|
| `display_name` | 1–100 characters. Required on the record, so it can't be set to `null`. |
| `avatar_url` | `https` URL, ≤ 2,048 characters, or `null`. The API stores the URL; it doesn't host or proxy images. |
| `contact_email` | Valid email, or `null`. Not verified, and not used for login. |
| `contact_phone` | E.164 (`+` and 8–15 digits), or `null`. |
| `locale` | BCP 47 tag, for example `en-US` or `fr-CA`. Must be a tag the API recognizes; otherwise `422`. |

A self-service change writes no Audit Event. A change made by an admin to someone else's Profile writes `profile.updated` — see [Orders & Audit](../../domain-model/orders-and-audit/#what-triggers-an-audit-event).

## `GET /v1/users/{id}/apps/{appId}/profile`

```json
// Response — 200
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "display_handle": "j.alvarez",
  "effective_display_name": "j.alvarez",
  "custom": { "department": "Engineering" },
  "updated_at": "2026-09-20T14:15:00Z"
}
```

- `effective_display_name` is read-only: `display_handle` if it's set, otherwise the global Profile's `display_name`. It saves every app from re-implementing that fallback.
- **No record yet?** You get the default: `display_handle: null`, `custom: {}`, and `updated_at: null`. Nothing is written on a read; the row is created by the first `PATCH`. That keeps `GET` safe, and still means no app ever has a separate "provision this user" step.
- **Access gate.** For the user themselves and for the app's own key, the user must currently have *active* access to the Application. Otherwise the response is `403 entitlement_required`, so a disabled or never-purchased app doesn't accumulate profile data. `users.manage` callers (support) can read and write regardless, for investigations.

## `PATCH /v1/users/{id}/apps/{appId}/profile`

```json
// Request — add a key, remove a key
{ "custom": { "seat_number": 14, "department": null } }
```
```json
// Response — 200, full updated object
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "display_handle": "j.alvarez",
  "effective_display_name": "j.alvarez",
  "custom": { "seat_number": 14 },
  "updated_at": "2026-10-05T10:02:00Z"
}
```

- `display_handle` is 1–64 characters, or `null` to fall back to the global name. It isn't unique, because the API doesn't enforce uniqueness of app-local handles. An Application that needs unique handles enforces that itself.
- `custom` follows [Conventions → Partial updates](../conventions/#partial-updates-patch): keys are merged one level deep, and a key sent as `null` is removed. The serialized size limit is 16 KB.
- `custom` isn't validated against a schema, unlike [AppSettings](../settings/). See [AppProfile](../../domain-model/profiles/#appprofile) for why. It's treated as personal data in its entirety: the erasure cascade clears it completely.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `entitlement_required` | 403 | Self or app-key access to an AppProfile for a user without active access to that Application. |
| `user_not_found` | 404 | `{id}` doesn't resolve, or isn't visible to an app-confined caller. |
| `application_not_found` | 404 | `{appId}` doesn't resolve, or isn't the calling app key's own Application. |
