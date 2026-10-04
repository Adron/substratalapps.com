---
layout: default
title: Webhooks
parent: API Reference
nav_order: 11
---

# Webhooks
{: .no_toc }

How a downstream app reacts to an access change immediately instead of polling. See [Trust Model → 3. Webhooks for push-based revocation](../../trust-model/#3-webhooks-for-push-based-revocation).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Managing subscriptions

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/webhooks` | List this caller's webhook subscriptions. |
| `POST` | `/v1/webhooks` | Subscribe a URL to one or more event types. |
| `DELETE` | `/v1/webhooks/{id}` | Unsubscribe. |

```json
// POST /v1/webhooks — request
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
  "created_at": "2026-10-03T12:10:00Z"
}
```

`signing_secret` is returned once, at creation — used to verify the `Substratal-Signature` header on every delivered event, the same pattern as most webhook systems (HMAC-SHA256 over the raw request body).

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

Expects a `2xx` within a short timeout; retries with backoff on failure or timeout, for a bounded number of attempts before the subscription is marked unhealthy (visible via `GET /v1/webhooks`). Idempotency on the receiving end: dedupe on the delivery's `id`, since retries resend the same `id`.

## This is an addition to, not a replacement for, JWT/introspection

A webhook tells an app "something changed, go check" — it's how an app achieves near-immediate reaction instead of waiting out a token TTL, but the app still needs a way to act on it (typically: force-expire the user's local session and make them re-authenticate, which re-triggers the [Trust Model](../../trust-model/) checks from scratch). See [Trust Model → How fast does revocation need to land?](../../trust-model/#how-fast-does-revocation-need-to-land) for how this fits with the other two mechanisms.
