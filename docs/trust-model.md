---
layout: default
title: Trust Model
nav_order: 8
---

# Trust Model
{: .no_toc }

How a separately-hosted app verifies a user's identity and access without maintaining its own user table.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .decision }
This whole page assumes each Application is an independently hosted service. If that's not the eventual architecture, see [Decisions → Downstream app architecture](../decisions/#3-downstream-app-architecture) — this page would need rewriting, not patching.

## Why apps shouldn't own their own user table

Substratal Apps is the hub precisely so that a user's identity, their access, and their account-wide settings exist once. An app that keeps its own copy of "is this user allowed in, and what are they allowed to do" will drift from the hub's answer — most dangerously in the direction of an app still honoring access the hub has revoked.

Three mechanisms, meant to be layered, not chosen between:

## 1. Short-lived JWT at launch

When the hub redirects a user into an app (SSO-style launch from the dashboard, or a deep link), it issues a signed JWT scoped to that one app:

```json
{
  "iss": "https://api.substratalapps.com",
  "sub": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "aud": "app_timetrack",
  "org_id": null,
  "entitlement_status": "active",
  "effective_permissions": ["app.timetrack.export", "app.timetrack.manage_members"],
  "iat": 1730649600,
  "exp": 1730649900
}
```

- Verified locally by the app (standard JWT signature check) — cheap, no network call, no dependency on the hub being reachable for every request.
- `exp` should be short — minutes, not hours — because the claims are a snapshot. If an admin revokes the entitlement one minute after this token was issued, the app has no way to know until the token expires and the user re-authenticates.
- Good fit for: read-mostly requests, UI rendering, anything where being a few minutes stale is an acceptable risk.

## 2. Live introspection

```
GET /v1/users/{id}/applications/{appId}/effective-permissions
```

Returns the current, authoritative answer — same shape as the JWT claims, but computed fresh. Good fit for:

- Anything before a destructive or sensitive action.
- The moment right after an admin says "I just turned this off" and needs it to actually be off, not off-in-five-minutes.
- Apps that don't want to manage JWT verification at all and are fine with a network round trip per check (with normal caching discipline on their side).

## 3. Webhooks for push-based revocation

Subscribe to `entitlement.revoked`, `entitlement.disabled`, `role.removed` (see [Webhooks](../api-reference/webhooks/)) to kill an active session the moment access changes, instead of waiting for a JWT to expire or polling introspection. This is the only one of the three mechanisms that achieves near-immediate revocation without the app checking on every request.

## How fast does revocation need to land?

This is a real design tradeoff, not a solved problem — see [Decisions → Session model for revocation](../decisions/#6-session-model-for-revocation):

| Approach | Revocation latency | App complexity |
|---|---|---|
| JWT only, short TTL | Up to one TTL window | Lowest — just verify a signature |
| JWT + webhook-driven session kill | Near-immediate | Needs a webhook receiver and a way to force-expire a live session |
| Introspection on every sensitive action | Immediate, for the actions it guards | One extra network call per guarded action |

Most apps will want JWT for general use plus introspection before anything destructive. Apps with a strict compliance requirement around immediate de-provisioning (e.g. "access must end within 60 seconds of revocation") need the webhook path.

## What the hub guarantees, what it doesn't

**Guarantees:** the hub is the only writer of Entitlement and Role state. `effective_permissions`, however computed (JWT claim or live call), always reflects the hub's current records at the moment it was computed.

**Doesn't guarantee:** that every app enforces it correctly or promptly. A JWT with a 15-minute TTL means a revoked user can act for up to 15 minutes inside that one app. That's a choice each app makes by picking its TTL and whether it subscribes to webhooks — not something the hub can enforce on the app's behalf.
