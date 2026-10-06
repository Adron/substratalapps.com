---
layout: default
title: Profiles
parent: Domain Model
nav_order: 5
---

# Profiles
{: .no_toc }

1. TOC
{: toc }

---

## The split: global vs. per-app

Identity data comes in two layers. **Profile** is who the user is, site-wide. **AppProfile** is who they are *inside one specific app* — data that only makes sense scoped to that product and that the global Profile shouldn't be cluttered with.

| Field | Global `Profile` | `AppProfile` (per User × Application) |
|---|---|---|
| `display_name`, `avatar_url` | ✓ | Not stored per app. `display_handle` (below) overrides the *name* for display inside the app, and the API returns `effective_display_name` so the app never re-implements the fallback. The avatar is always the global one. |
| `display_handle` | — | ✓ an app-local identity, e.g. a username meaningful only inside one product |
| contact email/phone | ✓ | — |
| custom fields | — | ✓ arbitrary JSON the owning app defines for itself |

## Profile

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | One per User. |
| `display_name` | string | |
| `avatar_url` | string, nullable | |
| `contact_email` | string, nullable | May differ from the User's login email. |
| `contact_phone` | string, nullable | |
| `locale` | string, read-only | IETF tag, e.g. `en-US`. A read-only mirror of the User's global [Settings](../settings/#settings-global) `locale`, which is the only place it's stored and the only place it's written. It's repeated here because it's identity-adjacent (how to address the person), so an app reading a Profile doesn't need a second call. Sending it in a Profile `PATCH` returns `422 read_only_field`. |
| `updated_at` | timestamp | |

### Example

```json
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

## AppProfile

Created lazily. A read for a user with no record returns the defaults (`display_handle: null`, `custom: {}`) without writing anything, and the first write creates the row. No separate provisioning step exists. See [Workflows → New user, first app](../../workflows/#new-user-first-app).

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `application_id` | string | |
| `display_handle` | string, nullable | Overrides `Profile.display_name` for display inside this one app. 1–64 characters, not unique. |
| `effective_display_name` | string, read-only | Computed on read, never stored: `display_handle` if set, otherwise the global `Profile.display_name`. |
| `custom` | object | Arbitrary JSON, up to 16 KB serialized. Shape is owned entirely by the Application — the hub stores and returns it, but (unlike [AppSettings](../settings/#appsettings)) does not validate its contents against a schema, since this is display/identity data rather than configuration that drives behavior. |
| `updated_at` | timestamp, nullable | `null` until the first write creates the row. |

Erasure treats all of an AppProfile as personal data: the hard-delete cascade sets `display_handle` to `null` and `custom` to `{}` (see [Non-Functional Requirements → Hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade)).

### Example

```json
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "display_handle": "j.alvarez",
  "effective_display_name": "j.alvarez",
  "custom": { "department": "Engineering" },
  "updated_at": "2026-09-20T14:15:00Z"
}
```

## Why keep them separate instead of one big Profile object

Two reasons. First, blast radius: an app shouldn't be able to write fields that show up in another app's view of the user, and a schema change one app wants (adding a custom field) shouldn't risk the global identity record every app reads. Second, most apps don't need per-app identity data at all — AppProfile only gets created for apps that actually use it, instead of every app carrying empty fields it never reads.
