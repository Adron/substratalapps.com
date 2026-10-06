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
| The Application's owner, with a User token, passing `scope: "<application_id>"` | that `application_id` | Same as above. |
| A platform key or User with `webhooks.manage` | `platform` | Every event. Meant for Substratal's own tooling. |

The request's `scope` field picks it. An app-scoped key may omit `scope` (it defaults to the key's own Application) or pass that same id; anything else is `403 forbidden`. An owner's User token must pass their Application's id. A `webhooks.manage` caller passes `"platform"` or any `application_id`.

A subscription is managed by its scope's owners (the app's own keys, the app's owner) and by `webhooks.manage`. Anyone else gets `404`. `POST` is rejected with `409 plan_limit_reached` if the Application's Tenant is at its plan's cap (1/10/unlimited on Starter/Team/Enterprise, counted per Tenant across all its Applications), and with `402 subscription_required` if the Tenant is restricted. Test-mode keys create test-mode subscriptions, which only ever receive test-mode events; live subscriptions never receive test traffic.

## `POST /v1/webhooks`

```json
// Request
{
  "scope": "app_timetrack",
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["access.revoked", "access.granted", "role.assigned", "role.removed"],
  "description": "Production session-kill listener"
}
```
```json
// Response — 201
{
  "id": "whk_01JAGC3D4E5F6G7H8J9K011M2N",
  "scope": "app_timetrack",
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["access.revoked", "access.granted", "role.assigned", "role.removed"],
  "description": "Production session-kill listener",
  "status": "healthy",
  "signing_secret": "whsec_EXAMPLE_not_a_real_secret_created",
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
- `api_version` pins the payload shape. It's the date of the current payload version at creation, and payloads for this subscription keep that shape until you `PATCH` it forward. See [Payload versions](#payload-versions) and [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning).
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

Writable: `url`, `events`, `description`, `api_version` (forward only, to a [published version](#payload-versions)), and `status` (only `disabled → healthy`). `scope` and `test_mode` are fixed (`422 read_only_field`). Writes `webhook.updated`.

```json
// Response — 200, the subscription (never the secret)
{
  "id": "whk_01JAGC3D4E5F6G7H8J9K011M2N",
  "scope": "app_timetrack",
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["*"],
  "description": "Production session-kill listener",
  "status": "healthy",
  "api_version": "2026-10-05",
  "test_mode": false,
  "consecutive_failures": 0,
  "last_delivery_at": "2026-10-05T09:14:02Z",
  "created_at": "2026-10-03T12:10:00Z",
  "updated_at": "2026-10-05T12:45:00Z"
}
```

## `DELETE /v1/webhooks/{id}`

`204`. Takes effect immediately: deliveries already in the retry queue are dropped, and nothing new is enqueued. Writes `webhook.deleted`.

## `POST /v1/webhooks/{id}/rotate-secret`

```json
// Response — 200
{ "id": "whk_01JAGC3D4E5F6G7H8J9K011M2N", "signing_secret": "whsec_EXAMPLE_not_a_real_secret_rotated", "previous_secret_expires_at": "2026-10-06T12:10:00Z" }
```

For 24 hours, every delivery is signed with **both** secrets (two `v1=` entries in the header), so a receiver can deploy the new secret with no window where verification fails. Writes `webhook.secret_rotated`.

## `POST /v1/webhooks/{id}/test`

Sends a synthetic `webhook.test` event (`data: {"message": "Test event from Substratal"}`) through the real signing and delivery path, and returns `202` with the delivery id. It's delivered whatever the subscription's `events` filter says (you asked for it), even to a `disabled` subscription, and it doesn't count toward health.

## Delivery log

### `GET /v1/webhooks/{id}/deliveries`

```json
// Response — 200
{
  "data": [
    {
      "id": "dlv_01JAGM1N2P3Q4R5S6T7V8V9W0X",
      "event_id": "wev_01JAGD4E5F6G7H8J9K011M2N30",
      "event_type": "access.revoked",
      "attempt": 2,
      "status": "failed",
      "response_status": null,
      "duration_ms": 5000,
      "error": "timeout",
      "next_retry_at": "2026-09-30T16:28:41Z",
      "created_at": "2026-09-30T16:23:41Z"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

`response_status` is `null` when no response arrived (a timeout or connection error). Filters: `event_id`, `status` (`succeeded`/`failed`/`pending`), and `since`. Retained for 30 days. Response bodies aren't stored; only the first 1 KB of an error body is kept.

### `POST /v1/webhooks/{id}/deliveries/{deliveryId}/redeliver`

`202`. Re-sends the same event (same `wev_` id, so receivers dedupe correctly) as a fresh attempt, outside the retry schedule. Allowed within the 30-day log window.

## Event types

Why the derived, per-user `access.granted`/`access.revoked` events exist, and why `allow()` requires `user.status == "active"`: the raw `entitlement.*` events miss real ways a user loses access. Those are an org-wide grant being disabled (the event carries only `organization_id`), removal from an Organization, a `member_scope` exclusion, and a suspended or deleted user. The required set for [immediate revocation](../../trust-model/#how-fast-does-revocation-need-to-land) is therefore `access.revoked` + `role.removed`.

**Derived access events** fire per (user, Application) pair whenever a user's *resolved* access flips, whatever caused it. They're what an Application should build session handling on:

| Event | Fires when | `data` |
|---|---|---|
| `access.granted` | A user's resolved access to the Application goes from not-allowed to allowed. | `user_id`, `application_id`, `reason`, `entitlement_status: "active"` |
| `access.revoked` | A user's resolved access goes from allowed to not-allowed. | `user_id`, `application_id`, `reason`, `entitlement_status` (the new resolved status, or `none`) |

`reason` is one of the following:

- `entitlement_granted`, `entitlement_started` (a future `starts_at` arrived), `entitlement_enabled`, `entitlement_disabled`, `entitlement_revoked`, `entitlement_expired`, `entitlement_deleted`
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
| `organization.member_added` / `organization.member_removed` | A membership becomes `active` (on acceptance, or a direct add), or an active membership is removed. Pending invitations don't fire either. | `organization_id`, `user_id`, `role` |
| `application.review_status_changed` | The Application is approved, rejected, suspended, or reinstated. | `application_id`, `review_status`, `review_notes` |
| `webhook.test` | `POST …/test`. | `message` |

{: .note }
An `entitlement.*` event about an **org-wide** grant carries `organization_id` and `user_id: null`. It doesn't tell you which users were affected. The `access.*` events that accompany it do, one per affected member. That's why the compliance minimum is `access.revoked`, not `entitlement.revoked`.

## Delivery

```
POST https://timetrack.substratalapps.com/hooks/substratal
Content-Type: application/json
User-Agent: Substratal-Webhooks/1.0
Substratal-Event-Id: wev_01JAGD4E5F6G7H8J9K011M2N30
Substratal-Event-Type: access.revoked
Substratal-Delivery-Id: dlv_01JAGM1N2P3Q4R5S6T7V8V9W0X
Substratal-Delivery-Attempt: 1
Substratal-Signature: t=1730649761,v1=5257a869e7ecebeda32affa62cdca3fa51cad7e77a0e56ff536d0ce8e108d8bd
```
```json
{
  "id": "wev_01JAGD4E5F6G7H8J9K011M2N30",
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

`v1` is `HMAC-SHA256(signing_secret, "{t}.{raw_request_body}")`, hex-encoded. `t` is the Unix time the **attempt** was sent: every retry and every redeliver is signed fresh, so a delivery that's been retrying for hours still passes the 5-minute check below. Sign the timestamp concatenated with the exact raw bytes you received, not a re-serialized copy of the parsed JSON. Re-serializing can reorder keys or change whitespace and silently break the signature. To verify:

1. Recompute the HMAC the same way, using the `signing_secret` from creation or rotation.
2. Compare it to each `v1` value in the header (there are two during a rotation overlap) using a constant-time comparison, not `==`. Accept if any one matches.
3. Reject if `t` is more than 5 minutes old. That makes a captured, replayed delivery useless to a third party, even one who has never seen the secret.

```js
// Node.js (Express): mount with express.raw({ type: "application/json" }) so req.body is the raw Buffer
const crypto = require("crypto");

function verifySubstratal(req, secrets /* [current] or [new, old] during a rotation */) {
  const header = req.get("Substratal-Signature") || "";
  const parts = header.split(",").map((p) => p.split("="));
  const t = Number(parts.find(([k]) => k === "t")?.[1]);
  const sigs = parts.filter(([k]) => k === "v1").map(([, v]) => Buffer.from(v, "hex"));
  if (!t || Math.abs(Date.now() / 1000 - t) > 300) return false;

  return secrets.some((secret) => {
    const expected = crypto.createHmac("sha256", secret).update(`${t}.`).update(req.body).digest();
    return sigs.some((sig) => sig.length === expected.length && crypto.timingSafeEqual(sig, expected));
  });
}
```

```python
# Python (Flask): request.get_data() is the raw body
import hmac, hashlib, time

def verify_substratal(raw_body: bytes, header: str, secrets: list[str]) -> bool:
    pairs = [p.split("=", 1) for p in header.split(",")]
    t = next((v for k, v in pairs if k == "t"), None)
    sigs = [v for k, v in pairs if k == "v1"]
    if t is None or abs(time.time() - int(t)) > 300:
        return False
    for secret in secrets:
        expected = hmac.new(secret.encode(), f"{t}.".encode() + raw_body, hashlib.sha256).hexdigest()
        if any(hmac.compare_digest(expected, s) for s in sigs):
            return True
    return False
```

During a [secret rotation](#post-v1webhooksidrotate-secret), deploy with both secrets in the list (new first). The sender signs with both for 24 hours, so either one matching is enough. Drop the old secret once `previous_secret_expires_at` has passed.

### Retries, ordering, and idempotency

- **Success** is any `2xx` within 5 seconds. Redirects aren't followed and count as a failure.
- **Retries:** attempt 1 is immediate. After a failure there are up to 5 more attempts, at +1 minute, +5 minutes, +30 minutes, +2 hours, and +12 hours, for **6 attempts** in total. Then that delivery is marked `failed` and counts toward `unhealthy`.
- **A `410 Gone` response** disables the subscription immediately. That's the receiver's way to say "stop".
- **No ordering guarantee.** Events are delivered independently, and a failing one doesn't block the next. Use `created_at`, and for entitlements `entitlement.updated_at`, to discard stale events. When it matters, re-read current state with [effective-permissions](../roles-and-permissions/#get-v1usersidappsappideffective-permissions) rather than trusting event order.
- **At-least-once.** The same event can arrive more than once, after a retry or a redeliver. Dedupe on `Substratal-Event-Id` (the `id` in the body). It's stable across attempts.
- **Latency target:** p95 under 10 seconds from the committing write to the first delivery attempt. Events are enqueued in the same transaction as the change (an outbox table), so a committed change can never fail to produce its event.

## Payload versions

Each subscription's `api_version` fixes the shape of every payload it receives. Versions are dates, and a new one is published only for a breaking change to an existing event's payload. New event types and new optional fields in `data` are additive and ship to every version.

| `api_version` | Status | Changes from the previous version |
|---|---|---|
| `2026-10-05` | Current, and the only version so far | The initial payload shape documented on this page. |

When a new version ships, it's added to this table with its changes, existing subscriptions keep their pinned version, and a pinned version is supported for at least 12 months after its successor ships (see [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning)). `PATCH` an `api_version` that isn't in this table, or that's earlier than the current one, and you get `422 validation_failed`.

## This is an addition to, not a replacement for, JWT/introspection

A webhook tells an app "something changed". It's how an app reacts almost immediately instead of waiting out a token TTL. The app still has to act on it, typically by force-expiring the user's local session for that `sid`/user and making them re-authenticate, which re-runs the [Trust Model](../../trust-model/) checks from scratch. See [Trust Model → How fast does revocation need to land?](../../trust-model/#how-fast-does-revocation-need-to-land) for how this fits with the other two mechanisms.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `webhook_not_found` | 404 | `{id}` doesn't resolve or isn't visible. |
| `invalid_webhook_url` | 422 | Not https, private or loopback IP, credentials in the URL, or doesn't resolve. |
| `unknown_event_type` | 422 | An entry in `events` isn't a known type. |
| `delivery_not_found` | 404 | `{deliveryId}` doesn't exist or is past the 30-day log. |
| `plan_limit_reached` | 409 | Over the plan's webhook-subscription cap (`details.resource: "webhooks"`). |
| `subscription_required` | 402 | `POST` on a `restricted` Tenant. |
| `forbidden` | 403 | `scope` names an Application the caller can't manage, or `"platform"` without `webhooks.manage`. |
| `read_only_field` | 422 | `PATCH` touches `scope`, `test_mode`, `id`, or `created_at`. |
| `invalid_status_transition` | 409 | `PATCH status` anything other than `disabled → healthy`. |
| `validation_failed` | 422 | For example an `api_version` that isn't published, or one earlier than the subscription's current version. |
