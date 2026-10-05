---
layout: default
title: Users
parent: API Reference
nav_order: 3
---

# Users
{: .no_toc }

See [Domain Model → Users & Organizations](../../domain-model/users-and-organizations/) for the full field reference and lifecycle diagram. Signup, login, password, and MFA live on [Auth](../auth/).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/users` | `users.list` (or an app-confined `users.list`) | List/search users. |
| `POST` | `/v1/users` | `users.manage` | Create a user (invite, or direct creation). |
| `GET` | `/v1/users/{id}` | self, `users.list`, or app-confined `users.list` | Fetch one user. `{id}` may be `me`. |
| `PATCH` | `/v1/users/{id}` | self (`email` only) or `users.manage` | Update `email`; `users.manage` may also set `status`. |
| `DELETE` | `/v1/users/{id}` | self or `users.manage` | Soft-delete. |
| `POST` | `/v1/users/{id}/suspend` | `users.manage` | Shortcut for `PATCH {status: "suspended"}`. |
| `POST` | `/v1/users/{id}/invitation` | `users.manage` | Re-send the invitation email to an `invited` user. |
| `GET` | `/v1/users/{id}/export` | self or `users.manage` | Everything this API holds about this User, as one bundle (GDPR Article 20 / CCPA right-to-know). |
| `POST` | `/v1/users/{id}/erasure-requests` | self or `users.manage` | Request right-to-erasure: soft-delete now, hard-delete cascade after 7 days. |
| `DELETE` | `/v1/users/{id}/erasure-requests/current` | `users.manage` | Cancel a scheduled erasure inside its 7-day window. |

Password, MFA, and session endpoints under `/v1/users/{id}/…` are specified on [Auth](../auth/).

## The User object

```json
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": true,
  "pending_email": null,
  "status": "active",
  "mfa_enabled": false,
  "signup_application_id": "app_timetrack",
  "test_mode": false,
  "created_at": "2026-01-14T18:02:11Z",
  "updated_at": "2026-09-20T14:12:00Z",
  "last_login_at": "2026-10-02T09:41:03Z"
}
```

| Field | Notes |
|---|---|
| `pending_email` | Set while a self-service email change awaits verification of the new address; `email` still holds the old one until then. |
| `mfa_enabled` | `true` if any `password` identity has confirmed TOTP. Read-only here — see [Auth → MFA](../auth/#mfa-totp). |
| `signup_application_id` | Which Application the User signed up through, if any. Read-only, informational; grants nothing. |
| `test_mode` | `true` if created by a `satk_test_` key — see [Conventions → Authentication](../conventions/#authentication). |

## `GET /v1/users`

```
GET /v1/users?status=active&email=jordan@example.com
```

```json
// Response — 200
{
  "data": [ { "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "status": "active", "...": "..." } ],
  "page": { "next_cursor": null, "has_more": false }
}
```

| Filter | Matches |
|---|---|
| `status` | `active`, `invited`, `suspended`. (`deleted` users are never returned — they 404.) |
| `email` | Exact, case-insensitive. |
| `q` | Case-insensitive prefix match on `email` or Profile `display_name`, minimum 3 characters. |
| `application_id` | Users holding any access path — personal Entitlement in any status, or membership in an Organization holding a grant — to that Application. |
| `organization_id` | Members of that Organization. |
| `created_after`, `created_before` | ISO 8601 bounds on `created_at`. |

**App-confined callers:** an app-scoped [API Key](../api-keys/#app-confined-permissions) holding `users.list` gets exactly the `application_id=<its own app>` result, whatever it passes — the filter is forced, not optional.

## `POST /v1/users`

```json
// Request — invite (the default)
{ "email": "jordan@example.com", "display_name": "Jordan Alvarez" }
```
```json
// Request — create active, no email sent
{ "email": "migrated.user@example.com", "status": "active", "send_invitation": false }
```

```json
// Response — 201
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": false,
  "pending_email": null,
  "status": "invited",
  "mfa_enabled": false,
  "signup_application_id": null,
  "test_mode": false,
  "created_at": "2026-10-03T12:00:00Z",
  "updated_at": "2026-10-03T12:00:00Z",
  "last_login_at": null
}
```

- `status` may be `invited` (default) or `active`. An `invited` user gets an invitation email (unless `send_invitation: false`) and activates via [`POST /v1/auth/invitations/accept`](../auth/#post-v1authinvitationsaccept). An `active` user created this way has no password; they sign in by using [`password/forgot`](../auth/#post-v1authpasswordforgot) (or SSO, once live). An admin can never set a user's password directly.
- Creating a user also creates a default [Profile](../../domain-model/profiles/) (with `display_name` if given) and [Settings](../../domain-model/settings/), and assigns the `member` platform role — one transaction (see [Non-Functional Requirements → Transaction boundaries](../../non-functional-requirements/#transaction-boundaries)).
- Writes Audit Event `user.created`.

## `GET /v1/users/{id}`

Self, `users.list`, or an app-confined key (only for users with an access path to its Application — anyone else 404s, so existence isn't leaked). Returns the User object above.

A User's Organization memberships aren't a field here — see `GET /v1/organizations` (self-scoped) in [Organizations](../organizations/).

## `PATCH /v1/users/{id}`

```json
// Request — self-service email change
{ "email": "jordan.alvarez@example.com" }
```
```json
// Response — 200: email unchanged, pending_email set, verification email sent to the new address
{ "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "pending_email": "jordan.alvarez@example.com", "...": "..." }
```

- **Self** may change `email` only. The change is **pending** until the new address is verified via [`POST /v1/auth/email/verify`](../auth/#post-v1authemailverify). A notice also goes to the old address. Writes `user.email_changed` when it completes.
- **`users.manage`** may change `email` immediately (`email_verified` resets to `false`, a verification email is sent), and may change `status`:

| Transition | Effect | Audit action |
|---|---|---|
| `active → suspended` | Same as `POST /suspend`, below. | `user.suspended` |
| `suspended → active` | Login works again; `access.granted` fires for every Application the user still has active access to. | `user.reactivated` |
| `invited → active` | Activates without a password (see `POST` above). | `user.updated` |
| anything `→ deleted` | Rejected — use `DELETE`. | — |
| `→ invited` | Rejected. | — |

Rejected transitions return `409 invalid_status_transition`. A self-service caller sending `status` gets `403 status_change_forbidden`.

## `DELETE /v1/users/{id}`

```json
// Response — 204
```

Soft-delete: sets `status: deleted` and `deleted_at`, revokes every session, fires `access.revoked` (reason `user_deleted`) for every Application the user had active access to, and from then on `GET /v1/users/{id}` 404s. Self, or `users.manage`. Entitlements, Roles, Profile, and Settings are retained, not removed — hard deletion is the separate erasure process below. The email address becomes reusable by a new signup immediately. Writes `user.deleted`.

## `POST /v1/users/{id}/suspend`

```json
// Request
{ "reason": "Chargeback fraud investigation, ticket SUP-2231" }
```
```json
// Response — 200, the User with status "suspended"
```

Requires `users.manage`. `reason` is optional but recorded on the Audit Event (`user.suspended`). Revokes every session and fires `access.revoked` (reason `user_suspended`) for every Application the user had active access to — the user's Entitlements themselves are **not** changed, so reactivation restores exactly what was there. To cut off one specific app instead of the whole account, use [Entitlements](../entitlements/). Destructive — see [MCP Server → Tool annotations](../../mcp-server/#tool-annotations--safety).

## `POST /v1/users/{id}/invitation`

```json
// Response — 202
```

Requires `users.manage`. Issues a fresh invitation token (invalidating the previous one) and re-sends the email. `409 invalid_status_transition` if the user isn't `invited`.

## `GET /v1/users/{id}/export`

Self, or `users.manage`. Limited to 5 calls per user per day.

```json
// Response — 200
{
  "exported_at": "2026-10-05T12:00:00Z",
  "user": { "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "status": "active", "created_at": "2026-01-14T18:02:11Z" },
  "profile": { "display_name": "Jordan Alvarez", "contact_email": "jordan@example.com", "locale": "en-US" },
  "settings": { "theme": "dark", "timezone": "America/Denver", "notifications": { "email": true, "sms": false } },
  "identities": [
    { "method": "password", "mfa_enabled": true, "last_used_at": "2026-10-02T09:41:03Z" }
  ],
  "sessions": [
    { "id": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8", "created_at": "2026-10-02T09:41:03Z", "ip_address": "203.0.113.24", "user_agent": "TimeTrack/2.4 (iOS 19.0)" }
  ],
  "organizations": [
    { "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "role": "member", "joined_at": "2026-09-20T14:00:00Z" }
  ],
  "roles": [
    { "role_id": "role_timetrack_admin", "application_id": "app_timetrack", "assigned_at": "2026-10-03T12:05:00Z" }
  ],
  "applications": [
    {
      "application_id": "app_timetrack",
      "entitlement": { "status": "active", "source": "purchase", "starts_at": "2026-01-14T18:05:00Z" },
      "app_profile": { "display_handle": "j.alvarez", "custom": { "department": "Engineering" } },
      "app_settings": { "overrides": { "week_start": "monday" } }
    }
  ],
  "audit_events": [
    { "action": "entitlement.granted", "application_id": "app_timetrack", "timestamp": "2026-01-14T18:05:00Z" }
  ]
}
```

Every section reads from exactly the tables the [hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade) writes to — export and erasure are deliberately symmetric. `identities` never includes `password_hash` or `mfa_secret`; `audit_events` is this User's own trail as `target_user_id` (hot storage only — events already archived per [Audit log lifecycle](../../non-functional-requirements/#audit-log-lifecycle) are available on request through support). The response is always complete and synchronous; there is no pagination. If a real account ever makes this too large to return in one response (practically, over 10 MB), the endpoint gains an asynchronous `202` + download-link mode as an additive change.

## `POST /v1/users/{id}/erasure-requests`

{: .decision }
**Proposed — confirm** ([DECISIONS.md #26](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#26-erasure-grace-period)). The 7-day delay before the hard-delete cascade sits well inside GDPR's 30-day ceiling, and gives support a window to cancel a request made in error or under account takeover. **Alternatives:** run the cascade immediately (no recovery), or wait the full 30 days.

```json
// Request
{ "reason": "User request via support ticket SUP-4410" }
```
```json
// Response — 202
{
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "status": "scheduled",
  "requested_at": "2026-10-05T12:00:00Z",
  "scheduled_for": "2026-10-12T12:00:00Z"
}
```

Self, or `users.manage`. Performs the soft-delete immediately (same effects as `DELETE` above), then runs the [hard-delete cascade](../../non-functional-requirements/#hard-delete-cascade) 7 days later — well inside GDPR's 30-day ceiling, with a short window to catch a request made in error or by someone who took over the account. `users.manage` can cancel inside that window with `DELETE /v1/users/{id}/erasure-requests/current` (`204`; the user stays soft-deleted, and an admin can then reactivate with `PATCH status: active`). Writes `user.erasure_requested`; the cascade writes `user.erased` when it completes. Destructive.

Calling this for an already-scheduled user returns the existing request (`202`, same body) — it's idempotent.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `email_taken` | 409 | `email` collides with an existing user on create or update. |
| `user_not_found` | 404 | `{id}` doesn't resolve — including a soft-deleted user, which 404s rather than returning a `deleted` status, to avoid leaking existence past deletion. |
| `status_change_forbidden` | 403 | A self-service caller's `PATCH` attempts to change `status`. |
| `invalid_status_transition` | 409 | A `status` change not in the table above, or an invitation re-send for a non-`invited` user. |
| `erasure_not_scheduled` | 404 | Cancelling an erasure that isn't pending. |
