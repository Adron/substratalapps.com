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
| `name`, `avatar_url` | ✓ | Inherits unless explicitly overridden |
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
| `locale` | string | IETF tag, e.g. `en-US`. Also readable via [Settings](../settings/) — kept here too since it's identity-adjacent (how to address the person) as much as preference. |
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

Created lazily — the first time a user's per-app profile is read or written for an app they're entitled to, a record is created seeded from defaults, rather than requiring a separate provisioning step. See [Workflows → New user, first app](../../workflows/#new-user-first-app).

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `application_id` | string | |
| `display_handle` | string, nullable | Overrides `Profile.display_name` for display inside this one app. |
| `custom` | object | Arbitrary JSON. Shape is owned entirely by the Application — the hub stores and returns it, but (unlike [AppSettings](../settings/#appsettings)) does not validate its contents against a schema, since this is display/identity data rather than configuration that drives behavior. |
| `updated_at` | timestamp | |

### Example

```json
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "display_handle": "j.alvarez",
  "custom": { "department": "Engineering" },
  "updated_at": "2026-09-20T14:15:00Z"
}
```

## Why keep them separate instead of one big Profile object

Two reasons. First, blast radius: an app shouldn't be able to write fields that show up in another app's view of the user, and a schema change one app wants (adding a custom field) shouldn't risk the global identity record every app reads. Second, most apps don't need per-app identity data at all — AppProfile only gets created for apps that actually use it, instead of every app carrying empty fields it never reads.
