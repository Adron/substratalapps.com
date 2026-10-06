---
layout: default
title: Billing
parent: API Reference
nav_order: 10.5
---

# Billing
{: .no_toc }

The Application owner's **platform subscription**: what Substratal charges the developer (Starter / Team / Enterprise), processed by Stripe. See [Pricing](../../pricing/) for the plans, limits, and rules. This page has nothing to do with how a developer bills *their own* end users, which never touches this API (see [Orders & Audit → Billing system of record](../../domain-model/orders-and-audit/#billing-system-of-record)).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## How it fits together

- Every [Tenant](../tenancy/) has one Stripe Customer, created when the Tenant is created.
- A **Starter** Tenant has no Stripe Subscription. Starter is simply the absence of a paid plan.
- **Upgrading to Team** sends the owner through Stripe Checkout, using a URL from this API.
- **Payment methods, invoices, and cancellation** are handled in the Stripe Customer Portal, again through a URL from this API.
- **Enterprise** is sales-led. Staff create the subscription in Stripe, usually from a Quote, and it syncs in.

Stripe tells this API about every change through its webhook. This API never takes card data, and the Tenant's `plan` and `subscription_status` only change in response to Stripe events. The handler is documented in [root `DEPLOYMENT.md` → Stripe Billing](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md#stripe-billing).

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/tenants/{id}/subscription` | owner or `billing.manage` | Current plan, status, period, and seat count. |
| `GET` | `/v1/tenants/{id}/usage` | owner, `billing.manage`, or `tenants.manage` | Usage against every plan limit. |
| `POST` | `/v1/tenants/{id}/billing/checkout-sessions` | owner | Start a Stripe Checkout to subscribe to Team. |
| `POST` | `/v1/tenants/{id}/billing/portal-sessions` | owner or `billing.manage` | Open the Stripe Customer Portal. |

"Owner" means the same thing as on [Tenancy](../tenancy/#who-counts-as-a-tenants-owner). These endpoints act for a person, so they need a User token, not an API Key (`400 user_token_required`).

## `GET /v1/tenants/{id}/subscription`

```json
// Response — 200, a Team Tenant
{
  "tenant_id": "tnt_01JAGC3D4E5F6G7H8J9K011M2N",
  "plan": "team",
  "subscription_status": "active",
  "restricted": false,
  "current_period_start": "2026-10-01T00:00:00Z",
  "current_period_end": "2026-11-01T00:00:00Z",
  "cancel_at_period_end": false,
  "seats": { "current": 31, "included": 25, "billable": 6, "last_synced_at": "2026-10-05T00:15:03Z" },
  "add_ons": [],
  "currency": "usd"
}
```
```json
// Response — 200, a Starter Tenant
{
  "tenant_id": "tnt_01JAG2STARTER0000000000000",
  "plan": "starter",
  "subscription_status": "none",
  "restricted": false,
  "current_period_start": null,
  "current_period_end": null,
  "cancel_at_period_end": false,
  "seats": { "current": 212, "included": 1000, "billable": 0, "last_synced_at": null },
  "add_ons": [],
  "currency": "usd"
}
```

| Field | Notes |
|---|---|
| `subscription_status` | `none` (Starter), `active`, `trialing`, `past_due`, `canceled`, `unpaid`, `incomplete`, `incomplete_expired`, or `paused`. Mirrors Stripe. New values may be added; see [Non-Functional Requirements → Versioning](../../non-functional-requirements/#versioning). |
| `restricted` | `true` once a subscription has lapsed while usage exceeds what the Tenant can drop to. See [Pricing → Subscription lapse](../../pricing/#subscription-lapse--downgrades). |
| `seats.current` | Computed live. See [Pricing → What "seat" means](../../pricing/#what-seat-means-here). |
| `seats.billable` | Seats over the included count. It's what the next Team or Enterprise invoice will charge for, based on the last daily sync. |
| `add_ons` | Enterprise only: `[{"code": "isolated_tenancy", "amount": 75000}]` or `[{"code": "dedicated_region_tenancy", "amount": 150000}]`. Amounts are in cents. |

Invoices and receipts aren't mirrored here. Stripe is the system of record for those, and the Portal shows them.

## `GET /v1/tenants/{id}/usage`

```json
// Response — 200
{
  "tenant_id": "tnt_01JAGC3D4E5F6G7H8J9K011M2N",
  "plan": "team",
  "limits": {
    "applications":          { "limit": 5,    "current": 2 },
    "seats":                 { "limit": null, "current": 31, "included": 25 },
    "app_roles":             { "limit": null, "current": 4 },
    "webhooks":              { "limit": 10,   "current": 3 },
    "audit_hot_window_days": { "limit": 365 }
  },
  "fits_plans": ["team", "enterprise"]
}
```

`limit: null` means unlimited. The keys under `limits` are the same `resource` names a [`409 plan_limit_reached`](../conventions/#plan-limit-errors) error reports, so the two always agree. `app_roles` is per Application, so its `current` is the highest count on any one of the Tenant's Applications. `audit_hot_window_days` is a retention window, not a count, so it has no `current`. `fits_plans` lists which plans the Tenant's *current* usage would fit, which is what a "can I downgrade?" screen needs.

## `POST /v1/tenants/{id}/billing/checkout-sessions`

```json
// Request
{
  "plan": "team",
  "success_url": "https://dashboard.substratalapps.com/billing?checkout=success",
  "cancel_url": "https://dashboard.substratalapps.com/billing?checkout=cancelled"
}
```
```json
// Response — 201
{
  "url": "https://checkout.stripe.com/c/pay/cs_live_a1B2c3D4...",
  "expires_at": "2026-10-05T13:00:00Z"
}
```

- `plan` must be `team`. `enterprise` returns `422 plan_requires_sales`. Contact sales.
- Allowed only when the Tenant has no subscription, or a lapsed one (`none`, `canceled`, `incomplete_expired`, `unpaid`). An existing active subscription returns `409 subscription_exists`, and the owner should use the portal instead.
- Requires `Idempotency-Key`. A retry returns the same Checkout URL while it's still valid.
- `success_url`/`cancel_url` must be `https`. Stripe adds `session_id`.
- Returning to `success_url` does **not** by itself mean the upgrade happened. The plan changes when Stripe's `checkout.session.completed` / `customer.subscription.created` webhook arrives, usually within seconds. Clients should poll `GET …/subscription` until `plan` is `team`.

## `POST /v1/tenants/{id}/billing/portal-sessions`

```json
// Request
{ "return_url": "https://dashboard.substratalapps.com/billing" }
```
```json
// Response — 201
{ "url": "https://billing.stripe.com/p/session/live_YWNjdF8x...", "expires_at": "2026-10-05T12:05:00Z" }
```

The Portal is configured to allow updating the payment method, viewing invoices, and cancelling at period end. It does **not** allow switching plans, because a downgrade has to be checked against plan limits (see [Pricing → Subscription lapse](../../pricing/#subscription-lapse--downgrades)). A Starter Tenant with no payment history gets `409 no_billing_account`, since there's nothing to manage yet.

## Webhook events

Plan and billing changes write Audit Events (`tenant.plan_changed`, `tenant.subscription_status_changed`) visible to the owner. There are no outbound webhook events for billing. Stripe already emails the customer, and Applications have no reason to react to their developer's subscription state.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `tenant_not_found` | 404 | `{id}` doesn't resolve or isn't visible. |
| `plan_requires_sales` | 422 | Checkout requested for `enterprise`. |
| `subscription_exists` | 409 | Checkout requested while a non-lapsed subscription exists. |
| `no_billing_account` | 409 | Portal requested for a Tenant that has never paid. |
| `billing_unavailable` | 503 | Stripe is unreachable. `Retry-After` is set. |
| `user_token_required` | 400 | Any of these endpoints called with an API Key. |
| `idempotency_key_required` | 400 | `checkout-sessions` without an `Idempotency-Key`. |
| `forbidden` | 403 | Checkout by anyone but the owner, or another endpoint without the permission in the table above. |
