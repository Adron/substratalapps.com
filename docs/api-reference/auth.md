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

{: .decision }
Whether these endpoints are implemented in-house or are a thin wrapper around a delegated identity provider is open — see [Decisions → Identity provider](../../decisions/#1-identity-provider). The contract below holds either way; what changes is what's behind it.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/auth/login` | Exchange credentials for a session. |
| `POST` | `/v1/auth/token/refresh` | Exchange a refresh token for a new access token. |
| `POST` | `/v1/auth/sso/{provider}/callback` | Complete an SSO login. |
| `POST` | `/v1/auth/logout` | Invalidate the current session. |

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

Completes a federated login. Request/response shape depends on the provider chosen per [Decisions → Identity provider](../../decisions/#1-identity-provider); this page will be filled in once that's resolved rather than guessed at now.

## `POST /v1/auth/logout`

```json
// Request
{}
```
```json
// Response — 204
```

Invalidates the current access/refresh token pair. Does not affect per-app JWTs already issued to downstream apps — those expire on their own short TTL per [Trust Model](../../trust-model/); logging out of the hub does not retroactively revoke them. If immediate cross-app session kill on logout is a requirement, it needs the webhook path described there, not this endpoint.
