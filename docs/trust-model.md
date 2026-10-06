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

## Applications are separately hosted

Every Application is separately hosted software that Substratal doesn't host, and it isn't necessarily a web app. It can be an iOS, macOS, Windows, Linux, or web app, or run on any other platform. This API serves it purely as a backend: it authenticates users and reads/writes their settings and related data. It has no say in how or where the app itself runs. The alternative, apps as modules or iframes inside one deployment, is ruled out because a native iOS or desktop app can't be an iframe inside anything.

This affects one thing: how the token is handed to the app. There are two paths, both fully specified in [Auth → Getting an app token](../api-reference/auth/#getting-an-app-token). **Embedded:** the app draws its own sign-in screen, calls `POST /v1/auth/login`, then `POST /v1/auth/app-tokens`. This is available now and is acceptable only while every Application is first-party. **Hosted:** the user signs in on Substratal's hosted page, which hands the app a one-time code; the app exchanges it with its PKCE verifier at `POST /v1/auth/oauth/token`. That's the standard flow for a public client with no safe place to hold a secret, with control returning via a custom URL scheme or app link. The hosted page ships with the dashboard and is required before any third-party Application goes live. The claims and verification model below stay the same for both: short-lived JWT, `effective_permissions`, introspection, webhooks.

## Why apps shouldn't own their own user table

Substratal Apps is the hub precisely so that a user's identity, their access, and their account-wide settings exist once. An app that keeps its own copy of "is this user allowed in, and what are they allowed to do" will drift from the hub's answer — most dangerously in the direction of an app still honoring access the hub has revoked.

Three mechanisms, meant to be layered, not chosen between:

## 1. Short-lived JWT at launch

Whenever a user enters an Application, the Application holds a signed **app token**: a JWT scoped to that one app (`aud` is its `application_id`), obtained by either path in [Applications are separately hosted](#applications-are-separately-hosted). Same claims, same signature, same 5-minute TTL either way; only the hand-off mechanics differ.

```json
{
  "iss": "https://api.substratalapps.com",
  "aud": "app_timetrack",
  "sub": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "sid": "ses_01JAG8K4Q9R0S1T2V3V4W5X6Y8",
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

- Verified locally by the app against the keys published at `https://api.substratalapps.com/.well-known/jwks.json`: cheap, no network call, no dependency on the hub being reachable for every request. [Auth → Signing keys](../api-reference/auth/#signing-keys-jwks) has the exact verification checklist and the key-rotation schedule.
- `exp` is 5 minutes after `iat`. It's short because the claims are a snapshot. If an admin revokes the entitlement one minute after this token was issued, the app has no way to know until the token expires and the user re-authenticates.
- Good fit for: read-mostly requests, UI rendering, anything where being a few minutes stale is an acceptable risk.
- `org_id` is which *one* Organization context this particular launch resolved through (or `null` for a personal, non-org-sourced launch) — not a list of every Organization the user belongs to. See [Domain Model → Users & Organizations → OrganizationMembership](../domain-model/users-and-organizations/#organizationmembership) for why a User can hold more than one. The exact selection rule is in [Auth → The app token](../api-reference/auth/#the-app-token).
- `sid` is the hub session the token was minted from. Key the app's own local session by it, so a logout-everywhere or an [`access.revoked`](../api-reference/webhooks/#event-types) webhook can be matched to the right local session.
- `effective_permissions` only ever contains this Application's own `app.<slug>.*` keys, never platform permissions like `users.manage`. See [Access Control → Step 2](../access-control/#step-2-effective-permissions).

{: .note }
Deliberately absent from this claim: anything about [Tenant](../domain-model/tenancy/) placement (`tier`/`region`). Tenancy is set once per Application by its owner, not computed per end-user per request — an app that wants to know its own placement reads it from `GET /v1/applications/{id}` or `GET /v1/tenants/{id}`, not from a launch token. See [Tenancy → What this does — and doesn't — isolate](../domain-model/tenancy/#what-this-does--and-doesnt--isolate).

## 2. Live introspection

```
GET /v1/users/{id}/apps/{appId}/effective-permissions
```

Returns the current, authoritative answer — same shape as the JWT claims, but computed fresh. Good fit for:

- Anything before a destructive or sensitive action.
- The moment right after an admin says "I just turned this off" and needs it to actually be off, not off-in-five-minutes.
- Apps that don't want to manage JWT verification at all and are fine with a network round trip per check (with normal caching discipline on their side).

## 3. Webhooks for push-based revocation

Subscribe to `access.revoked` and `role.removed` (see [Webhooks → Event types](../api-reference/webhooks/#event-types)) to kill an active session the moment access changes. `access.revoked` is a derived, per-user event. It fires whenever a user's *resolved* access to your app turns off for **any** reason: their own Entitlement disabled, revoked, or expired; an org-wide grant disabled; removal from the Organization; exclusion via `member_scope`; or the user being suspended or deleted. An app never has to reconstruct org membership itself to know whom a change affects. Use it instead of waiting for a JWT to expire or polling introspection. This is the only one of the three mechanisms that achieves near-immediate revocation without the app checking on every request.

## How fast does revocation need to land?

**Immediately.** "Turn off this user's access" must take effect inside an already-open app session right away, not by the next login or token refresh. This is not a per-Application choice between approaches; it's a single global guarantee every Application must meet, which makes the webhook path **required integration**, not an option for the compliance-sensitive minority:

| Approach | Revocation latency | Status |
|---|---|---|
| JWT only, short TTL | Up to one TTL window | **Not sufficient on its own** — a live session surviving for the length of a TTL window after revocation doesn't meet "immediately." |
| JWT + webhook-driven session kill | Near-immediate | **Required.** The JWT TTL is the backstop for the gap between an event firing and the app acting on it, not the primary revocation mechanism. |
| Introspection on every sensitive action | Immediate, for the actions it guards | Recommended in addition, for destructive actions specifically — same as before, still the right belt-and-suspenders check immediately before something irreversible. |

Concretely: every Application **must** subscribe to `access.revoked` and `role.removed` ([Webhooks](../api-reference/webhooks/#event-types)) and force-expire the affected session the moment one arrives — not "may, if compliance-sensitive." JWT TTLs should still be kept short (minutes), but short-TTL-alone is a degraded, non-compliant integration under this resolution, not a lighter-weight valid option.

## What the hub guarantees, what it doesn't

**Guarantees:** the hub is the only writer of Entitlement and Role state. `effective_permissions`, however computed (JWT claim or live call), always reflects the hub's current records at the moment it was computed.

**Doesn't guarantee:** that every app actually implements the [required webhook-driven revocation](#how-fast-does-revocation-need-to-land) correctly. The hub emits the event the moment access changes; it can't force a third-party Application's own code to act on it promptly, or at all. An Application that only relies on JWT expiry is out of compliance with the [immediate-revocation requirement](#how-fast-does-revocation-need-to-land), not exercising a lighter-weight valid option — but enforcing that compliance is an onboarding/review concern (see [Applications → The review lifecycle](../domain-model/applications/#the-review-lifecycle)), not something this API can verify at the protocol level.

## Trust runs the other direction too

Everything above is about an app trusting the hub's claims about a user. Once a third-party developer can register their own Application (see [Applications → The review lifecycle](../domain-model/applications/#the-review-lifecycle)), the hub also needs to limit how much it trusts the app:

- An app-scoped [API Key](../api-reference/api-keys/) can hold `entitlements.manage`, `roles.manage`, `users.list`, and `audit.view` only in **app-confined** form: the server restricts each one to rows belonging to the key's own Application. It can never hold `users.manage`, `applications.manage`, `api_keys.manage`, `tenants.manage`, or any other platform-wide permission. See [API Keys → App-confined permissions](../api-reference/api-keys/#app-confined-permissions). A malicious or compromised third-party app's key can corrupt data within its own app, never grant itself access to another app or escalate a user's platform-wide standing.
- An Application's `review_status` (see [Applications](../domain-model/applications/)) gates whether it's discoverable and launchable at all — `pending_review` keeps a newly self-registered app off the catalog until someone at Substratal looks at it.
- The webhook signing secret (see [Webhooks → Delivery](../api-reference/webhooks/#delivery)) exists specifically so an app receiving a webhook can prove it came from the hub — the same mechanism, aimed the other way, is why the hub signs the launch JWT rather than just passing a bare user id.

None of this is new machinery bolted on for the marketplace phase — it's why the API Key scoping and JWT/webhook signing were designed this way from the start, even while every Application is still first-party.
