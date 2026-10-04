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
| `GET` | `/v1/webhooks` | List this caller's webhook subscriptions. |
| `POST` | `/v1/webhooks` | Subscribe a URL to one or more event types. |
| `DELETE` | `/v1/webhooks/{id}` | Unsubscribe. |

A subscription belongs to whichever caller created it — there's no separate permission check beyond being authenticated as that Application's service [API key](../api-keys/) or a platform role with `webhooks.manage`. An app can only ever see and manage its own subscriptions; `webhooks.manage` is for platform admins troubleshooting another app's delivery issues.

## `GET /v1/webhooks`

```json
// Response — 200
{
  "data": [
    {
      "id": "whk_01JAGC3D4E5F6G7H8J9K0L1M2N",
      "url": "https://timetrack.substratalapps.com/hooks/substratal",
      "events": ["entitlement.granted", "entitlement.disabled", "entitlement.revoked", "role.assigned", "role.removed"],
      "status": "healthy",
      "created_at": "2026-10-03T12:10:00Z"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

`status` is `healthy` or `unhealthy` (see [Delivery](#delivery) below) — `signing_secret` is never included in a list or fetch response, only at creation.

## `POST /v1/webhooks`

```json
// Request
{
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["entitlement.granted", "entitlement.disabled", "entitlement.revoked", "role.assigned", "role.removed"]
}
```
```json
// Response — 201
{
  "id": "whk_01JAGC3D4E5F6G7H8J9K0L1M2N",
  "url": "https://timetrack.substratalapps.com/hooks/substratal",
  "events": ["entitlement.granted", "entitlement.disabled", "entitlement.revoked", "role.assigned", "role.removed"],
  "signing_secret": "whsec_7f3a9c...",
  "status": "healthy",
  "created_at": "2026-10-03T12:10:00Z"
}
```

`signing_secret` is returned once, at creation — used to verify the `Substratal-Signature` header on every delivered event, the same pattern as most webhook systems (HMAC-SHA256 over the raw request body). `url` must be HTTPS; `http://` is rejected with `422`.

## `DELETE /v1/webhooks/{id}`

```json
// Response — 204
```

Unsubscribes immediately — in-flight deliveries already queued are still attempted, but no new events are enqueued after this returns.

## Event types

| Event | Fired when | Payload highlights |
|---|---|---|
| `entitlement.granted` | An Entitlement transitions to `active` (new grant, or re-enable). | `entitlement`, `user_id`, `application_id` |
| `entitlement.disabled` | An admin flips `status` to `disabled`. | `entitlement`, `disabled_reason` |
| `entitlement.revoked` | `status` becomes `revoked` (refund, ToS action). | `entitlement` |
| `entitlement.expired` | A trial/subscription's `ends_at` passes without renewal. | `entitlement` |
| `role.assigned` | A Role assignment is created. | `user_id`, `role_id`, `application_id` |
| `role.removed` | A Role assignment is deleted. | `user_id`, `role_id`, `application_id` |

## Delivery

```json
// POST to your subscribed URL
{
  "id": "evt_01JAGD4E5F6G7H8J9K0L1M2N3O",
  "type": "entitlement.disabled",
  "created_at": "2026-09-30T16:22:41Z",
  "data": {
    "entitlement": {
      "id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9",
      "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "application_id": "app_invoicer",
      "status": "disabled",
      "disabled_reason": "billing_dispute"
    }
  }
}
```

### Verifying the signature

```
Substratal-Signature: t=1730649600,v1=5257a869e7ecebeda32affa62cdca3fa51cad7e77a0e56ff536d0ce8e108d8bd
```

`v1` is `HMAC-SHA256(signing_secret, "{t}.{raw_request_body}")`, hex-encoded — sign the timestamp concatenated with the exact raw bytes received, not a re-serialized copy of the parsed JSON (re-serialization can reorder keys or change whitespace and silently break the signature). To verify:

1. Recompute the HMAC the same way, using the `signing_secret` returned when this subscription was created (see `POST /v1/webhooks` above).
2. Compare to `v1` using a constant-time comparison, not `==`.
3. Reject if `t` is more than 5 minutes old — this is what makes a captured, replayed delivery useless to a third party even if they never see the secret itself.

Expects a `2xx` within a 5-second timeout. On failure or timeout, retries with exponential backoff — 1m, 5m, 30m, 2h, 12h — up to 5 attempts total, then gives up on that delivery and flips `status` to `unhealthy` (visible via `GET /v1/webhooks`) after 3 consecutive failed deliveries, so a persistently broken endpoint doesn't retry forever without anyone noticing. A failed delivery doesn't block subsequent events — they're delivered independently, not queued behind it. Idempotency on the receiving end: dedupe on the delivery's `id`, since retries resend the same `id`.

## This is an addition to, not a replacement for, JWT/introspection

A webhook tells an app "something changed, go check" — it's how an app achieves near-immediate reaction instead of waiting out a token TTL, but the app still needs a way to act on it (typically: force-expire the user's local session and make them re-authenticate, which re-triggers the [Trust Model](../../trust-model/) checks from scratch). See [Trust Model → How fast does revocation need to land?](../../trust-model/#how-fast-does-revocation-need-to-land) for how this fits with the other two mechanisms.
