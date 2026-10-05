---
layout: default
title: Auth
parent: API Reference
nav_order: 2
---

# Auth
{: .no_toc }

Signup, login, MFA, sessions, password and email lifecycle, and how an Application gets the per-app JWT it trusts. See [Trust Model](../../trust-model/) for what an Application does with that token once it has one.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Native auth and per-Organization SSO

Both, not either/or: native in-house auth, architected from the start for pluggable, per-Organization SSO.

- **Native email/password is owned here**, not delegated. It's a real `password_hash` and real sessions, live from Phase 1, with zero external integration needed: no Auth0/WorkOS/Cognito account, no third-party API key, nothing to configure. A single developer standing up their first Application has a fully working login system the moment Phase 1 ships.
- **SSO is a per-Organization bridge**, not a platform-wide or per-Application choice. An Organization admin connects their own company's identity provider (Okta, Azure AD, Google Workspace, …) through a federation broker (leaning WorkOS, for exactly this "bring your own enterprise IdP" case), and it becomes available to every member of that Organization. See [Domain Model → SSOConnection](../../domain-model/users-and-organizations/#ssoconnection).
- **A User can hold both at once**, a `password` identity and an `sso` identity, and choose either at login. See [Domain Model → UserIdentity](../../domain-model/users-and-organizations/#useridentity).
- **MFA (TOTP) is optional and user-enabled** for native accounts, through [self-service enrollment](#mfa-totp). It isn't built for SSO identities, whose MFA policy belongs to the member's own IdP.
- **Sequencing:** the `UserIdentity`/`SSOConnection` schema and the auth endpoints are built in this shape now, so adding a real SSO broker later is additive, not a breaking migration. The broker integration itself is explicitly **not** being built yet. [`POST /v1/auth/sso/{provider}/callback`](#post-v1authssoprovidercallback) exists as a route and returns `501 not_implemented` until it is.

How an Application obtains its per-app JWT is covered under [Getting an app token](#getting-an-app-token): embedded login now, hosted authorization code + PKCE once Substratal's hosted sign-in page exists. That hosted page is required before any third-party Application goes live.

## Token types at a glance

| Token | Format | TTL | Issued by | Used for |
|---|---|---|---|---|
| **Platform access token** | JWT (RS256) | 15 minutes | `login`, `mfa/verify`, `token/refresh`, `signup`, `invitations/accept` | Calling this API as a User (`Authorization: Bearer …`). |
| **Refresh token** | Opaque, `rtk_…` | 30 days idle, 90 days absolute | Same as above | Getting a new platform access token. Single-use, rotating. |
| **App token** | JWT (RS256), `aud` = one `application_id` | 5 minutes | `app-tokens`, `oauth/token` | Presented by a User to one Application, verified locally by that Application. See [Trust Model](../../trust-model/#1-short-lived-jwt-at-launch). |
| **App refresh token** | Opaque, `rtk_…`, bound to one Application | 30 days idle, 90 days absolute | `oauth/token` | Getting a new app token without a platform session (hosted flow only). Re-checks access every time. |
| **API Key secret** | Opaque, `satk_live_…` / `satk_test_…` | Until revoked | [API Keys](../api-keys/) | Service-to-service calls. Not a User session. |

Every platform access token and app token is signed with a key published at `https://api.substratalapps.com/.well-known/jwks.json` (see [Signing keys](#signing-keys-jwks)). Every login creates a **session** (`ses_…`); refresh tokens and app refresh tokens belong to exactly one session, and revoking the session revokes all of them at once.

## Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/auth/signup` | none | Self-service account creation with email + password. |
| `POST` | `/v1/auth/login` | none | Exchange email + password for a session (or an MFA challenge). |
| `POST` | `/v1/auth/mfa/verify` | none (`mfa_token`) | Complete a login that returned an MFA challenge. |
| `POST` | `/v1/auth/token/refresh` | none (`refresh_token`) | Rotate a platform refresh token for a new access token. |
| `POST` | `/v1/auth/logout` | Bearer (user) | Revoke the current session, or every session. |
| `POST` | `/v1/auth/app-tokens` | Bearer (user) | Mint a per-app JWT from a platform session (embedded login). |
| `POST` | `/v1/auth/oauth/authorization-codes` | Bearer (user) | Mint a one-time authorization code for a hosted launch (PKCE). |
| `POST` | `/v1/auth/oauth/token` | none (code / refresh token) | Exchange an authorization code or app refresh token for an app token. |
| `POST` | `/v1/auth/email/verify` | none (token) | Confirm an email address from the link in a verification email. |
| `POST` | `/v1/auth/email/verify/resend` | none | Re-send a verification email. Always `202`. |
| `POST` | `/v1/auth/password/forgot` | none | Send a password-reset email. Always `202`. |
| `POST` | `/v1/auth/password/reset` | none (token) | Set a new password from a reset token. |
| `POST` | `/v1/auth/invitations/accept` | none (token) | Set a password on an `invited` account and activate it. |
| `POST` | `/v1/auth/sso/{provider}/callback` | none | Complete an SSO login. `501` until the broker integration ships. |
| `POST` | `/v1/users/{id}/password` | Bearer (self) | Change password, given the current one. |
| `POST` | `/v1/users/{id}/mfa/totp` | Bearer (self) | Begin TOTP enrollment. |
| `POST` | `/v1/users/{id}/mfa/totp/confirm` | Bearer (self) | Confirm enrollment with a first code; returns recovery codes. |
| `POST` | `/v1/users/{id}/mfa/totp/disable` | Bearer (self) | Turn TOTP off, given a current code or recovery code. |
| `DELETE` | `/v1/users/{id}/mfa/totp` | Bearer (`users.manage`) | Support reset for a user who lost their device. |
| `GET` | `/v1/users/{id}/sessions` | Bearer (self or `users.manage`) | List active sessions. |
| `DELETE` | `/v1/users/{id}/sessions/{sessionId}` | Bearer (self or `users.manage`) | Revoke one session. |
| `GET` | `/.well-known/jwks.json` | none | Public signing keys. Outside `/v1`. |
| `GET` | `/.well-known/openid-configuration` | none | Issuer metadata (issuer, JWKS URI, token endpoint). Outside `/v1`. |

All unauthenticated `POST /v1/auth/*` endpoints are rate-limited per IP address as well as per account — see [Non-Functional Requirements → Rate limiting](../../non-functional-requirements/#rate-limiting).

## The `AuthSession` response

Every endpoint that establishes or refreshes a platform session returns this shape:

```json
{
  "token_type": "Bearer",
  "access_token": "eyJhbGciOiJSUzI1NiIsImtpZCI6InNrXzIwMjYxMCJ9...",
  "expires_in": 900,
  "refresh_token": "rtk_01JAG8K4Q9R0S1T2U3V4W5X6Y7",
  "refresh_token_expires_in": 2592000,
  "session_id": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email_verified": true
}
```

The platform access token's claims:

```json
{
  "iss": "https://api.substratalapps.com",
  "aud": "substratal-platform",
  "sub": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "sid": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8",
  "amr": ["pwd", "otp"],
  "email_verified": true,
  "test_mode": false,
  "iat": 1730649600,
  "exp": 1730650500,
  "jti": "atk_01JAG8K5A1B2C3D4E5F6G7H8J9"
}
```

`amr` lists the authentication methods used (`pwd`, `otp`, `recovery_code`, `sso`). The platform access token carries **no permissions** — the API resolves the caller's Roles fresh on every request, so a Role change takes effect on the very next call, not at the next refresh.

## `POST /v1/auth/signup`

```json
// Request
{
  "email": "jordan@example.com",
  "password": "correct horse battery staple",
  "display_name": "Jordan Alvarez",
  "application_id": "app_timetrack"
}
```

```json
// Response — 201, an AuthSession
{
  "token_type": "Bearer",
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 900,
  "refresh_token": "rtk_01JAG8K4Q9R0S1T2U3V4W5X6Y7",
  "refresh_token_expires_in": 2592000,
  "session_id": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email_verified": false
}
```

- Creates the User (`status: active`, `email_verified: false`), a `password` [UserIdentity](../../domain-model/users-and-organizations/#useridentity), a default Profile (with `display_name` if given) and Settings, and the `member` platform Role — one transaction, same as `POST /v1/users`.
- Sends a verification email (token TTL 24 hours). An unverified User **can** log in; `email_verified` is carried in every token so an Application can decide for itself whether to gate anything on it.
- `application_id` is optional and **grants nothing** — it records which Application the signup came from (`signup_application_id` on the User, used to brand the verification email with that app's `email_from_name`) and is otherwise informational. Access to an Application still requires an [Entitlement](../entitlements/), which the developer's own backend grants (see [Workflows → Purchase → access](../../workflows/#purchase--access)).
- If the email belongs to an `invited` User, signup completes the invitation instead: the password is set, `status` becomes `active`, and the invitation token is consumed.
- **Password policy:** 12–128 characters; rejected if it appears in the bundled breached-password list (top 100,000) or equals the email address. No composition rules. Stored as Argon2id (`m=19456 KiB, t=2, p=1`).

| Error | Status | When |
|---|---|---|
| `email_taken` | 409 | An `active` or `suspended` User already has this email. |
| `weak_password` | 422 | Fails the password policy; `details.reason` is `too_short`, `too_long`, `breached`, or `matches_email`. |
| `validation_failed` | 422 | Malformed email, unknown `application_id`, etc. |

## `POST /v1/auth/login`

```json
// Request
{ "email": "jordan@example.com", "password": "correct horse battery staple" }
```

```json
// Response — 200, no MFA enrolled: an AuthSession (see above)
```

```json
// Response — 200, MFA enrolled: a challenge instead of a session
{
  "mfa_required": true,
  "mfa_token": "mfa_01JAG8M2N3P4Q5R6S7T8U9V0W1",
  "methods": ["totp", "recovery_code"],
  "expires_in": 300
}
```

Clients must branch on `mfa_required` before reading `access_token`.

| Error | Status | When |
|---|---|---|
| `invalid_credentials` | 401 | Unknown email, wrong password, or an `invited` User with no password yet. Deliberately one code for all three — no account enumeration. |
| `account_suspended` | 403 | Correct credentials, but `status: suspended`. |
| `too_many_attempts` | 429 | 5 failed attempts for this account in 15 minutes locks it for 15 minutes; `Retry-After` is set. Successful login resets the counter. |

Updates `last_login_at` on the User and `last_used_at` on the matching UserIdentity.

## `POST /v1/auth/mfa/verify`

```json
// Request — a TOTP code
{ "mfa_token": "mfa_01JAG8M2N3P4Q5R6S7T8U9V0W1", "code": "492039" }
```
```json
// Request — or a recovery code instead
{ "mfa_token": "mfa_01JAG8M2N3P4Q5R6S7T8U9V0W1", "recovery_code": "7hq2-kx9m-a4vd" }
```
```json
// Response — 200, an AuthSession
```

TOTP is RFC 6238, SHA-1, 6 digits, 30-second step, accepting ±1 step of clock drift; a code can't be reused within its window. A recovery code is single-use. The `mfa_token` is single-use and valid 5 minutes; 5 wrong codes invalidate it.

| Error | Status | When |
|---|---|---|
| `invalid_mfa_code` | 401 | Wrong or reused code. |
| `mfa_token_invalid` | 401 | Expired, already used, or exhausted. Start over at `login`. |

## `POST /v1/auth/token/refresh`

```json
// Request
{ "refresh_token": "rtk_01JAG8K4Q9R0S1T2U3V4W5X6Y7" }
```
```json
// Response — 200, an AuthSession with a new access_token AND a new refresh_token
```

Refresh tokens are single-use. Presenting one that has **already been rotated** is treated as theft: the whole session is revoked, and every token in it stops working (`401 refresh_token_reused`). Refresh fails with `401 session_revoked` after logout, password reset, suspension, or deletion.

## `POST /v1/auth/logout`

```json
// Request — this session only (default)
{}
```
```json
// Request — every session this User has, on every device
{ "all_sessions": true }
```
```json
// Response — 204
```

Revokes the session(s): the refresh token and any app refresh tokens in them stop working immediately, and the platform access token is rejected from then on — the API checks `sid` against the session table on every request, so logout is immediate, not "at access-token expiry". Already-issued **app tokens** are not recalled (they're verified locally by Applications, by design); they expire within 5 minutes. Logout does not by itself fire `access.revoked` — the User still *has* access; they've just signed out.

## Getting an app token

Embedded login is allowed today because every Application is first-party, so no third party ever handles a Substratal password. It's blocked for any Application not owned by Substratal once self-service registration ships, and the hosted sign-in page must exist before any third-party Application goes live. Requiring the hosted page from Phase 1 instead was considered and not chosen: it would make the MVP depend on a UI project.

An **app token** is the per-Application JWT that [Trust Model](../../trust-model/) describes. Two ways to get one:

| Path | When | Calls |
|---|---|---|
| **Embedded** | The Application draws its own sign-in screen (any first-party app, native or web — the only option until the hosted page exists). | `login` → `app-tokens`. Refresh the platform session with `token/refresh`, then call `app-tokens` again. |
| **Hosted + PKCE** | The Application sends the user to Substratal's hosted sign-in page (ships with the dashboard), and never sees the password. Required for third-party Applications. | Hosted page calls `oauth/authorization-codes` → app calls `oauth/token`. Refresh with `oauth/token` (`grant_type: refresh_token`). |

Both paths produce the identical app token. Either way, issuance **fails** if the User doesn't have active access to the Application right now:

| Error | Status | When |
|---|---|---|
| `entitlement_required` | 403 | Resolved entitlement status isn't `active` (see [Access Control](../../access-control/)). `details.entitlement_status` says what it is. |
| `application_not_available` | 403 | The Application's `review_status` is `suspended`/`rejected`/`pending_review` — see [Applications → review lifecycle](../../domain-model/applications/#the-review-lifecycle). |
| `account_suspended` | 403 | The User is suspended. |

### The app token

```json
{
  "iss": "https://api.substratalapps.com",
  "aud": "app_timetrack",
  "sub": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "sid": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8",
  "org_id": null,
  "email": "jordan@example.com",
  "email_verified": true,
  "entitlement_status": "active",
  "effective_permissions": ["app.timetrack.export", "app.timetrack.manage_members"],
  "test_mode": false,
  "iat": 1730649600,
  "exp": 1730649900,
  "jti": "apt_01JAG8P1Q2R3S4T5U6V7W8X9Y0"
}
```

- `effective_permissions` contains only this Application's own `app.<slug>.*` keys — never platform permissions. See [Access Control → Step 2](../../access-control/#step-2-effective-permissions).
- `org_id` is the Organization whose org-wide grant provided access, if access came *only* through an Organization; `null` if the User has a personal Entitlement (personal wins for attribution when both exist). If multiple Organizations provide access and there's no personal path, it's the one whose grant was created first. Pass `organization_id` to `app-tokens` to pick a specific one explicitly.
- `sid` lets an Application key its own local session to the hub session, so that an `access.revoked` webhook or a logout-everywhere can be matched to the right local session.

### `POST /v1/auth/app-tokens`

```json
// Request
{ "application_id": "app_timetrack" }
```
```json
// Response — 200
{
  "token_type": "Bearer",
  "app_token": "eyJhbGciOiJSUzI1NiIsImtpZCI6InNrXzIwMjYxMCJ9...",
  "expires_in": 300,
  "application_id": "app_timetrack"
}
```

Bearer must be a **platform access token** (a User), not an API Key — `400 user_token_required` otherwise. Optional `organization_id` selects which Organization's grant to attribute `org_id` to; `403 entitlement_required` if that Organization doesn't actually include this User.

### `POST /v1/auth/oauth/authorization-codes`

Called by Substratal's hosted sign-in page (or the future dashboard's "Launch" button) on the signed-in User's behalf — not by the Application.

```json
// Request
{
  "application_id": "app_timetrack",
  "redirect_uri": "com.substratal.timetrack:/oauth/callback",
  "code_challenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  "code_challenge_method": "S256",
  "state": "af0ifjsldkj"
}
```
```json
// Response — 201
{
  "code": "ac_01JAG8R4S5T6U7V8W9X0Y1Z2A3",
  "expires_in": 60,
  "redirect_to": "com.substratal.timetrack:/oauth/callback?code=ac_01JAG8R4S5T6U7V8W9X0Y1Z2A3&state=af0ifjsldkj"
}
```

`redirect_uri` must exactly match one of the Application's registered `redirect_uris` (`422 redirect_uri_not_registered`); `code_challenge_method` must be `S256` (`plain` is rejected). The code is single-use and expires in 60 seconds. Access is checked here too, with the same errors as above, so a User without access is never redirected into the app holding a code.

### `POST /v1/auth/oauth/token`

Called by the Application. A public-client endpoint: there's no client secret, the PKCE verifier is the proof.

```json
// Request — exchange an authorization code
{
  "grant_type": "authorization_code",
  "client_id": "app_timetrack",
  "code": "ac_01JAG8R4S5T6U7V8W9X0Y1Z2A3",
  "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
  "redirect_uri": "com.substratal.timetrack:/oauth/callback"
}
```
```json
// Request — refresh
{
  "grant_type": "refresh_token",
  "client_id": "app_timetrack",
  "refresh_token": "rtk_01JAG8S5T6U7V8W9X0Y1Z2A3B4"
}
```
```json
// Response — 200
{
  "token_type": "Bearer",
  "app_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 300,
  "refresh_token": "rtk_01JAG8T6U7V8W9X0Y1Z2A3B4C5",
  "refresh_token_expires_in": 2592000,
  "application_id": "app_timetrack"
}
```

Accepts `application/json` and, for compatibility with standard OAuth client libraries (AppAuth, etc.), `application/x-www-form-urlencoded`. Errors on this one endpoint use the OAuth 2.0 shape (`{"error": "invalid_grant", "error_description": "..."}`) instead of this API's usual envelope, again for library compatibility: `invalid_grant` (bad/expired/reused code or verifier mismatch), `invalid_client` (unknown `client_id`), `unsupported_grant_type`, and `access_denied` (access no longer active — `error_description` carries the `entitlement_required` detail). A refresh **re-checks access every time** — a refresh after revocation fails, which is what makes the hosted flow safe without a platform session.

## Email verification

Transactional email (verification, password reset, invitations, MFA changes) is sent through **Amazon SES** in the same AWS account, so it adds no new sub-processor. Templates are Substratal-branded; an Application can set `email_from_name` and `support_url` so a message triggered from inside it names the app. A full per-Application custom domain and template is deferred.

### `POST /v1/auth/email/verify`

```json
// Request — token from the emailed link
{ "token": "emv_01JAG8V8W9X0Y1Z2A3B4C5D6E7" }
```
```json
// Response — 204
```

Sets `email_verified: true` (or, for a pending email change, swaps `pending_email` into `email` — see [Users → `PATCH`](../users/#patch-v1usersid)). Token: single-use, 24-hour TTL. `400 token_invalid` if expired, used, or unknown.

### `POST /v1/auth/email/verify/resend`

```json
// Request
{ "email": "jordan@example.com" }
```
```json
// Response — 202, always, whether or not the email exists or is already verified
```

Limited to 3 per email per hour.

## Password lifecycle

### `POST /v1/auth/password/forgot`

```json
// Request
{ "email": "jordan@example.com" }
```
```json
// Response — 202, always — never reveals whether the account exists
```

Sends a reset link (token `pwr_…`, single-use, 1-hour TTL) to an `active` User with a `password` identity. An SSO-only User gets an email explaining they sign in through their organization instead. Limited to 3 per email per hour.

### `POST /v1/auth/password/reset`

```json
// Request
{ "token": "pwr_01JAG8W9X0Y1Z2A3B4C5D6E7F8", "new_password": "a new long passphrase" }
```
```json
// Response — 204
```

Sets the new password (same policy as signup), marks the email verified (the User just proved control of it), **revokes every session**, and emails a "your password was changed" notice. Writes Audit Event `user.password_reset`. Errors: `400 token_invalid`, `422 weak_password`.

### `POST /v1/users/{id}/password`

```json
// Request
{ "current_password": "correct horse battery staple", "new_password": "a new long passphrase" }
```
```json
// Response — 204
```

Self only (an admin cannot set a User's password — they can only trigger `password/forgot` on the User's behalf). Revokes every **other** session; the calling session survives. Errors: `401 invalid_credentials` (wrong current password), `422 weak_password`. Writes `user.password_changed`.

## Invitations

`POST /v1/users` with `status: "invited"` (the default — see [Users](../users/#post-v1users)) or `POST /v1/organizations/{id}/members` with an unknown email creates an `invited` User and emails an invitation link (token `inv_…`, single-use, 7-day TTL).

### `POST /v1/auth/invitations/accept`

```json
// Request
{ "token": "inv_01JAG8X0Y1Z2A3B4C5D6E7F8G9", "password": "correct horse battery staple", "display_name": "Jordan Alvarez" }
```
```json
// Response — 200, an AuthSession
```

Creates the `password` identity, sets `status: active` and `email_verified: true`, and signs the User in. Errors: `400 token_invalid` (expired invitations are re-sent with `POST /v1/users/{id}/invitation` — see [Users](../users/)), `422 weak_password`.

## MFA (TOTP)

Optional and self-service for `password` identities. Meaningless for a `method: sso` identity, whose MFA policy belongs to the Organization's own IdP (`409 mfa_not_applicable` if the User has no `password` identity).

### `POST /v1/users/{id}/mfa/totp` — begin enrollment

```json
// Request
{}
```
```json
// Response — 200
{
  "secret": "JBSWY3DPEHPK3PXP",
  "otpauth_uri": "otpauth://totp/Substratal:jordan%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Substratal&digits=6&period=30",
  "expires_in": 600
}
```

The client renders `otpauth_uri` as a QR code itself; the API never serves the secret through a URL. Enrollment is *pending* until confirmed; calling this again replaces a pending secret. `409 mfa_already_enabled` if already confirmed.

### `POST /v1/users/{id}/mfa/totp/confirm`

```json
// Request
{ "code": "492039" }
```
```json
// Response — 200
{
  "mfa_enabled": true,
  "recovery_codes": ["7hq2-kx9m-a4vd", "p3nc-8wz1-qt6r", "..."]
}
```

Ten recovery codes, shown **once** — stored hashed. Writes `user.mfa_enabled` and emails a notice. `401 invalid_mfa_code` on a wrong code; `409 mfa_enrollment_expired` if the pending secret is older than 10 minutes.

### `POST /v1/users/{id}/mfa/totp/disable`

```json
// Request — current TOTP or a recovery code
{ "code": "492039" }
```
```json
// Response — 204
```

Writes `user.mfa_disabled`, emails a notice.

### `DELETE /v1/users/{id}/mfa/totp` — support reset

Requires `users.manage`. For a User who has lost their device *and* their recovery codes; support verifies identity out of band first. Revokes every session. Writes `user.mfa_reset`. Destructive — see [MCP Server → Tool annotations](../../mcp-server/#tool-annotations--safety).

## Sessions

### `GET /v1/users/{id}/sessions`

```json
// Response — 200
{
  "data": [
    {
      "id": "ses_01JAG8K4Q9R0S1T2U3V4W5X6Y8",
      "created_at": "2026-10-02T09:41:03Z",
      "last_seen_at": "2026-10-05T08:12:44Z",
      "expires_at": "2026-12-31T09:41:03Z",
      "ip_address": "203.0.113.24",
      "user_agent": "TimeTrack/2.4 (iOS 19.0)",
      "amr": ["pwd", "otp"],
      "current": true
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

### `DELETE /v1/users/{id}/sessions/{sessionId}`

`204`. Same effect as logout for that one session.

## `POST /v1/auth/sso/{provider}/callback`

Reserved. Returns `501 not_implemented` until the SSO broker integration ships (see [Native auth and per-Organization SSO](#native-auth-and-per-organization-sso)). When live, it creates or matches a `method: sso` [UserIdentity](../../domain-model/users-and-organizations/#useridentity) against the Organization's [SSOConnection](../../domain-model/users-and-organizations/#ssoconnection) and returns an `AuthSession` — SSO is a different way to obtain a session, not a different kind of session.

## Signing keys (JWKS)

```
GET https://api.substratalapps.com/.well-known/jwks.json
```
```json
{
  "keys": [
    { "kty": "RSA", "kid": "sk_202610", "use": "sig", "alg": "RS256", "n": "0vx7agoebGcQSuu...", "e": "AQAB" }
  ]
}
```

- RS256, 2048-bit keys, held in AWS KMS (the private key never leaves KMS — signing is a KMS `Sign` call).
- Every token's JWT header carries `kid`. Applications verify against the matching key and should cache the JWKS for up to 1 hour, re-fetching on an unknown `kid`.
- Rotated every 90 days. A new key is published 7 days before it starts signing, and a retired key stays published for 7 days after its last use (longer than any token's TTL), so a verifying Application never sees an unknown `kid` in normal operation.
- Verification checklist for an Application: signature against JWKS, `iss` equals `https://api.substratalapps.com`, `aud` equals its own `application_id`, `exp` in the future (allow ≤60 s clock skew), `entitlement_status` is `active`.

`GET /.well-known/openid-configuration` returns `issuer`, `jwks_uri`, `token_endpoint` (`/v1/auth/oauth/token`), `grant_types_supported` (`authorization_code`, `refresh_token`), and `code_challenge_methods_supported` (`S256`). It is metadata for standard libraries, not a claim of full OpenID Connect conformance (there is no `id_token` or userinfo endpoint).

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `invalid_credentials` | 401 | Login or password change with a wrong password/unknown email. |
| `account_suspended` | 403 | The User is suspended. |
| `too_many_attempts` | 429 | Account lockout after repeated failures. |
| `email_taken` | 409 | Signup with an email already in use. |
| `weak_password` | 422 | Password fails the policy. |
| `token_invalid` | 400 | Verification, reset, or invitation token is expired, used, or unknown. |
| `invalid_mfa_code` | 401 | Wrong or reused TOTP/recovery code. |
| `mfa_token_invalid` | 401 | MFA challenge expired or exhausted. |
| `mfa_already_enabled` / `mfa_not_applicable` / `mfa_enrollment_expired` | 409 | See [MFA](#mfa-totp). |
| `refresh_token_reused` | 401 | A rotated refresh token was presented again — session revoked. |
| `session_revoked` | 401 | The session was logged out, reset, or the User suspended/deleted. |
| `user_token_required` | 400 | An endpoint that acts *as a User* was called with an API Key. |
| `entitlement_required` | 403 | App-token issuance for a User without active access. |
| `application_not_available` | 403 | App-token issuance for an Application not `approved`. |
| `redirect_uri_not_registered` | 422 | Authorization code requested for an unregistered `redirect_uri`. |
| `not_implemented` | 501 | SSO callback before the broker integration exists. |
