---
layout: default
title: Webhooks
parent: API Reference
nav_order: 12
---

# Webhooks
{: .no_toc }

How a downstream app reacts to an access change immediately instead of polling. See [Trust Model → 3. Webhooks for push-based revocation](../../trust-model/#3-webhooks-for-push-based-revocation).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/webhooks` | List subscriptions visible to the caller. |
| `POST` | `/v1/webhooks` | Subscribe a URL to one or more event types. |
| `GET` | `/v1/webhooks/{id}` | Fetch one subscription. |
| `PATCH` | `/v1/webhooks/{id}` | Change `url`, `events`, or `description`, or re-enable a `disabled` subscription. |
| `DELETE` | `/v1/webhooks/{id}` | Unsubscribe. |
| `POST` | `/v1/webhooks/{id}/rotate-secret` | Issue a new signing secret, with a 24-hour overlap. |
| `POST` | `/v1/webhooks/{id}/test` | Send a `webhook.test` event right now. |
| `GET` | `/v1/webhooks/{id}/deliveries` | Recent delivery attempts (30-day log). |
| `POST` | `/v1/webhooks/{id}/deliveries/{deliveryId}/redeliver` | Re-send one past event. |

## Who owns a subscription, and what it receives

Every subscription has a **scope**, fixed at creation, and the scope decides which events it receives:

| Created by | `scope` | Receives |
|---|---|---|
| An app-scoped [API Key](../api-keys/) (no extra permission needed) | that `application_id` | Only events whose `application_id` is that Application. For `user.*` and `organization.member_*` events, only when the user has an access path to that Application. |
| The Application's owner, with a User token, passing `application_id` | that `application_id` | Same as above. |
| A platform key or User with `webhooks.manage` | `platform` | Every event. Meant for Substratal's own tooling. |

A subscription is managed by its scope's owners (the app's own keys, the app's owner) and by `webhooks.manage`. Anyone else gets `404`. `POST` is rejected with `409 plan_limit_reached` if the Application's Tenant is at its plan's cap (1/10/unlimited on Starter/Team/Enterprise, counted per Tenant across all its Applications), and with `402 subscription_required` if the Tenant is restricted. Test-mode keys create test-mode subscriptions, which only ever receive test-mode events; live subscriptions never receive test traffic.

## `POST /v1/webhooks`

```json
// Request
{
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["access.revoked", "access.granted", "role.assigned", "role.removed"],
  "description": "Production session-kill listener"
}
```
```json
// Response — 201
{
  "id": "whk_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "scope": "app_timetrack",
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["access.revoked", "access.granted", "role.assigned", "role.removed"],
  "description": "Production session-kill listener",
  "status": "healthy",
  "signing_secret": "whsec_7f3a9c2e1b4d8f6a0c5e7b9d1f3a5c7e",
  "api_version": "2026-10-05",
  "test_mode": false,
  "consecutive_failures": 0,
  "last_delivery_at": null,
  "created_at": "2026-10-03T12:10:00Z",
  "updated_at": "2026-10-03T12:10:00Z"
}
```

- `url` must be `https`, must resolve to a public IP address (private, loopback, and link-local ranges are rejected to prevent SSRF), and can't carry credentials in the userinfo part. Otherwise `422 invalid_webhook_url`.
- `events` takes 1 or more types from [Event types](#event-types), or `["*"]` for every event the scope can receive.
- `signing_secret` is returned **only** here and on `rotate-secret`. It's never included in a `GET`.
- `api_version` pins the payload shape. It's the date of the current payload version at creation, and payloads for this subscription keep that shape until you `PATCH` it forward. See [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning).
- Writes `webhook.created`.

{: .important }
**Compliance minimum.** Every Application must subscribe to `access.revoked` and `role.removed` and end the affected local session as soon as one arrives. See [Trust Model → How fast does revocation need to land?](../../trust-model/#how-fast-does-revocation-need-to-land).

## `GET /v1/webhooks`, `GET /v1/webhooks/{id}`

Same object as above, minus `signing_secret`. `status` is one of:

- `healthy`.
- `unhealthy`, after 3 consecutive deliveries that exhausted every retry. Delivery still continues.
- `disabled`, after 5 consecutive days `unhealthy`. Delivery stops and the Application's owner is emailed. `PATCH {"status": "healthy"}` re-enables it and resets the counters. Events that occurred while it was disabled aren't backfilled. Use `redeliver`, or reconcile through the API.

## `PATCH /v1/webhooks/{id}`

```json
// Request
{ "events": ["*"], "api_version": "2026-10-05" }
```

Writable: `url`, `events`, `description`, `api_version` (forward only), and `status` (only `disabled → healthy`). Writes `webhook.updated`.

## `DELETE /v1/webhooks/{id}`

`204`. Takes effect immediately: deliveries already in the retry queue are dropped, and nothing new is enqueued. Writes `webhook.deleted`.

## `POST /v1/webhooks/{id}/rotate-secret`

```json
// Response — 200
{ "id": "whk_01JAGC3D4E5F6G7H8J9K0L1M2N", "signing_secret": "whsec_9b2d4f6a8c0e1b3d5f7a9c1e3b5d7f9a", "previous_secret_expires_at": "2026-10-06T12:10:00Z" }
```

For 24 hours, every delivery is signed with **both** secrets (two `v1=` entries in the header), so a receiver can deploy the new secret with no window where verification fails. Writes `webhook.secret_rotated`.

## `POST /v1/webhooks/{id}/test`

Sends a synthetic `webhook.test` event (`data: {"message": "Test event from Substratal"}`) through the real signing and delivery path, and returns `202` with the delivery id. It doesn't count toward health.

## Delivery log

### `GET /v1/webhooks/{id}/deliveries`

```json
// Response — 200
{
  "data": [
    {
      "id": "dlv_01JAGM1N2P3Q4R5S6T7U8V9W0X",
      "event_id": "wev_01JAGD4E5F6G7H8J9K0L1M2N3O",
      "event_type": "access.revoked",
      "attempt": 2,
      "status": "failed",
      "response_status": 503,
      "duration_ms": 5000,
      "error": "timeout",
      "next_retry_at": "2026-09-30T16:28:41Z",
      "created_at": "2026-09-30T16:23:41Z"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Filters: `event_id`, `status` (`succeeded`/`failed`/`pending`), and `since`. Retained for 30 days. Response bodies aren't stored; only the first 1 KB of an error body is kept.

### `POST /v1/webhooks/{id}/deliveries/{deliveryId}/redeliver`

`202`. Re-sends the same event (same `wev_` id, so receivers dedupe correctly) as a fresh attempt, outside the retry schedule. Allowed within the 30-day log window.

## Event types

{: .decision }
**Proposed — confirm** ([DECISIONS.md #19](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#19-derived-access-webhooks--user-status-in-access)). The derived, per-user `access.granted`/`access.revoked` events, and `allow()` requiring `user.status == "active"`, exist because the raw `entitlement.*` events missed real ways a user loses access: an org-wide grant being disabled (the event carries only `organization_id`), removal from an Organization, a `member_scope` exclusion, and a suspended or deleted user. The required set for [immediate revocation](../../trust-model/#how-fast-does-revocation-need-to-land) is therefore `access.revoked` + `role.removed`.

**Derived access events** fire per (user, Application) pair whenever a user's *resolved* access flips, whatever caused it. They're what an Application should build session handling on:

| Event | Fires when | `data` |
|---|---|---|
| `access.granted` | A user's resolved access to the Application goes from not-allowed to allowed. | `user_id`, `application_id`, `reason`, `entitlement_status: "active"` |
| `access.revoked` | A user's resolved access goes from allowed to not-allowed. | `user_id`, `application_id`, `reason`, `entitlement_status` (the new resolved status, or `none`) |

`reason` is one of the following:

- `entitlement_granted`, `entitlement_enabled`, `entitlement_disabled`, `entitlement_revoked`, `entitlement_expired`, `entitlement_deleted`
- `org_grant_changed` (an org grant's status or `member_scope` changed)
- `org_member_added`, `org_member_removed`
- `user_suspended`, `user_reactivated`, `user_deleted`

A change that doesn't flip someone's resolved access fires nothing for them. For example, an org grant is disabled, but the member also holds a personal Entitlement.

**Resource events** report raw record changes, for consumers that want them:

| Event | Fires when | `data` |
|---|---|---|
| `entitlement.granted` | An Entitlement is created, or returns to `active`. | `entitlement` |
| `entitlement.disabled` | `status` set to `disabled`. | `entitlement` |
| `entitlement.revoked` | `status` set to `revoked`. | `entitlement` |
| `entitlement.expired` | `ends_at` passed. | `entitlement` |
| `entitlement.updated` | `ends_at`, `source`, or `order_id` changed with no status change. | `entitlement`, `changed_fields` |
| `entitlement.member_scope_changed` | An org grant's `member_scope`/`member_overrides` changed. | `entitlement` |
| `entitlement.deleted` | Hard-deleted as an error correction. | `entitlement` (last state) |
| `role.assigned` / `role.removed` | A Role assignment is created or deleted. | `user_id`, `role_id`, `application_id` |
| `user.suspended` / `user.reactivated` / `user.deleted` | User status changes. | `user_id` |
| `organization.member_added` / `organization.member_removed` | Organization membership changes. | `organization_id`, `user_id`, `role` |
| `application.review_status_changed` | The Application is approved, rejected, suspended, or reinstated. | `application_id`, `review_status`, `review_notes` |
| `webhook.test` | `POST …/test`. | `message` |

{: .note }
An `entitlement.*` event about an **org-wide** grant carries `organization_id` and `user_id: null`. It doesn't tell you which users were affected. The `access.*` events that accompany it do, one per affected member. That's why the compliance minimum is `access.revoked`, not `entitlement.revoked`.

## Delivery

```
POST https://timetrack.substratalapps.com/hooks/substratal
Content-Type: application/json
User-Agent: Substratal-Webhooks/1.0
Substratal-Event-Id: wev_01JAGD4E5F6G7H8J9K0L1M2N3O
Substratal-Event-Type: access.revoked
Substratal-Delivery-Id: dlv_01JAGM1N2P3Q4R5S6T7U8V9W0X
Substratal-Delivery-Attempt: 1
Substratal-Signature: t=1730649761,v1=5257a869e7ecebeda32affa62cdca3fa51cad7e77a0e56ff536d0ce8e108d8bd
```
```json
{
  "id": "wev_01JAGD4E5F6G7H8J9K0L1M2N3O",
  "type": "access.revoked",
  "api_version": "2026-10-05",
  "created_at": "2026-09-30T16:22:41Z",
  "application_id": "app_invoicer",
  "test_mode": false,
  "data": {
    "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
    "application_id": "app_invoicer",
    "reason": "entitlement_disabled",
    "entitlement_status": "disabled"
  }
}
```

An `entitlement.*` payload's `data.entitlement` is the full [Entitlement object](../entitlements/#the-entitlement-object).

### Verifying the signature

`v1` is `HMAC-SHA256(signing_secret, "{t}.{raw_request_body}")`, hex-encoded. Sign the timestamp concatenated with the exact raw bytes you received, not a re-serialized copy of the parsed JSON. Re-serializing can reorder keys or change whitespace and silently break the signature. To verify:

1. Recompute the HMAC the same way, using the `signing_secret` from creation or rotation.
2. Compare it to each `v1` value in the header (there are two during a rotation overlap) using a constant-time comparison, not `==`. Accept if any one matches.
3. Reject if `t` is more than 5 minutes old. That makes a captured, replayed delivery useless to a third party, even one who has never seen the secret.

### Retries, ordering, and idempotency

- **Success** is any `2xx` within 5 seconds. Redirects aren't followed and count as a failure.
- **Retries:** attempt 1 is immediate. After a failure there are up to 5 more attempts, at +1 minute, +5 minutes, +30 minutes, +2 hours, and +12 hours, for **6 attempts** in total. Then that delivery is marked `failed` and counts toward `unhealthy`.
- **A `410 Gone` response** disables the subscription immediately. That's the receiver's way to say "stop".
- **No ordering guarantee.** Events are delivered independently, and a failing one doesn't block the next. Use `created_at`, and for entitlements `entitlement.updated_at`, to discard stale events. When it matters, re-read current state with [effective-permissions](../roles-and-permissions/#get-v1usersidappsappideffective-permissions) rather than trusting event order.
- **At-least-once.** The same event can arrive more than once, after a retry or a redeliver. Dedupe on `Substratal-Event-Id` (the `id` in the body). It's stable across attempts.
- **Latency target:** p95 under 10 seconds from the committing write to the first delivery attempt. Events are enqueued in the same transaction as the change (an outbox table), so a committed change can never fail to produce its event.

## This is an addition to, not a replacement for, JWT/introspection

A webhook tells an app "something changed". It's how an app reacts almost immediately instead of waiting out a token TTL. The app still has to act on it, typically by force-expiring the user's local session for that `sid`/user and making them re-authenticate, which re-runs the [Trust Model](../../trust-model/) checks from scratch. See [Trust Model → How fast does revocation need to land?](../../trust-model/#how-fast-does-revocation-need-to-land) for how this fits with the other two mechanisms.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `webhook_not_found` | 404 | `{id}` doesn't resolve or isn't visible. |
| `invalid_webhook_url` | 422 | Not https, private or loopback IP, credentials in the URL, or doesn't resolve. |
| `unknown_event_type` | 422 | An entry in `events` isn't a known type. |
| `delivery_not_found` | 404 | `{deliveryId}` doesn't exist or is past the 30-day log. |
| `plan_limit_reached` | 409 | Over the plan's webhook-subscription cap. |
