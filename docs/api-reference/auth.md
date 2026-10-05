---
layout: default
title: Auth
parent: API Reference
nav_order: 2
---

# Auth
{: .no_toc }

1. TOC
{: toc }

---

{: .note }
Resolved — see [Decisions → Identity provider](../../decisions/#1-identity-provider). Native email/password is implemented in-house and real from Phase 1. SSO (per-[Organization](../../domain-model/users-and-organizations/#ssoconnection), broker-based) is architected for now — the [UserIdentity](../../domain-model/users-and-organizations/#useridentity)/[SSOConnection](../../domain-model/users-and-organizations/#ssoconnection) shape below is built to not need a breaking change — but the actual broker integration is deferred; `POST /v1/auth/sso/{provider}/callback` is a real route today, with its request/response shape still open.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/auth/login` | Exchange a credential (password, or a federated assertion once SSO is live) for a session. |
| `POST` | `/v1/auth/token/refresh` | Exchange a refresh token for a new access token. |
| `POST` | `/v1/auth/sso/{provider}/callback` | Complete an SSO login. Deferred — see the note above. |
| `POST` | `/v1/auth/logout` | Invalidate the current session. |
| `POST` | `/v1/users/{id}/mfa/totp` | Enroll TOTP-based MFA for a `password` [UserIdentity](../../domain-model/users-and-organizations/#useridentity). Optional, user-initiated — see [Decisions → Identity provider](../../decisions/#1-identity-provider). |

## `POST /v1/auth/login`

```json
// Request
{ "email": "jordan@example.com", "password": "..." }
```

```json
// Response — 200
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "rtk_01JAG8K4Q9R0S1T2U3V4W5X6Y7",
  "expires_in": 900,
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"
}
```

`access_token` is a platform-level JWT for talking to this API — distinct from the per-app JWT described in [Trust Model](../../trust-model/), which is issued separately at app-launch time and scoped to one Application's `aud`.

## `POST /v1/auth/token/refresh`

```json
// Request
{ "refresh_token": "rtk_01JAG8K4Q9R0S1T2U3V4W5X6Y7" }
```

Returns the same shape as login. Refresh tokens are single-use — each refresh issues a new one and invalidates the old.

## `POST /v1/auth/sso/{provider}/callback`

Completes a federated login, creating or matching a `method: sso` [UserIdentity](../../domain-model/users-and-organizations/#useridentity) against the Organization's [SSOConnection](../../domain-model/users-and-organizations/#ssoconnection). The exact request/response shape depends on the broker integration, which is deliberately not built yet — see [Decisions → Identity provider](../../decisions/#1-identity-provider). The response, once implemented, returns the same `AuthSession` shape as login — SSO is a different way to obtain a session, not a different kind of session.

## `POST /v1/users/{id}/mfa/totp`

```json
// Request — enroll
{}
```
```json
// Response — 200
{ "secret": "JBSWY3DPEHPK3PXP", "qr_code_url": "https://api.substratalapps.com/v1/users/usr_.../mfa/totp/qr" }
```

Self-service only — a `password` [UserIdentity](../../domain-model/users-and-organizations/#useridentity) opts itself into TOTP; nothing here is admin-initiated. Confirming enrollment (a follow-up `PATCH` with the first valid code) flips `mfa_enabled: true` on that identity. Meaningless for a `method: sso` identity — an Organization's own IdP owns MFA policy for its federated members, not this endpoint.

## `POST /v1/auth/logout`

```json
// Request
{}
```
```json
// Response — 204
```

Invalidates the current access/refresh token pair. Does not affect per-app JWTs already issued to downstream apps — those expire on their own short TTL per [Trust Model](../../trust-model/); logging out of the hub does not retroactively revoke them. If immediate cross-app session kill on logout is a requirement, it needs the webhook path described there, not this endpoint.
