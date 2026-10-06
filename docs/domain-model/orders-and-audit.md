---
layout: default
title: Orders & Audit
parent: Domain Model
nav_order: 7
---

# Orders & Audit
{: .no_toc }

1. TOC
{: toc }

---

## Order

`order_id` is an opaque, developer-supplied reference (no entity, table, or endpoint), following from the [billing system of record](#billing-system-of-record). `billing.manage` and `billing.refund` apply to the one billing relationship Substratal actually has, platform subscriptions. `billing.manage` lets support view a Tenant's subscription and usage and open its billing portal. `billing.refund` is reserved for Stripe credits and refunds, which are done in the Stripe dashboard today, so it has no endpoint yet.

**An Order isn't an entity in this API.** There's no `orders` table, no `ord_` resource, and no Orders endpoint. The commerce record an Entitlement traces back to lives in the *developer's own* billing system (see [Billing system of record](#billing-system-of-record) below). What this API stores is a **reference** to it:

| Field (on [Entitlement](../entitlements/#fields)) | Type | Notes |
|---|---|---|
| `order_id` | string, nullable, ≤ 255 characters | Opaque. Typically the developer's own Stripe subscription id (`sub_…`), invoice id, or internal order number. It isn't validated beyond its length, never dereferenced, and never used to drive state on its own. It's settable once, so it stays a stable reconciliation key, and it's filterable on `GET /v1/entitlements?order_id=…`. |

Its one behavioral effect: an Entitlement with `order_id` set can't be hard-deleted ([`entitlement_order_linked`](../../api-reference/entitlements/#delete-v1entitlementsid)), because it represents a real financial event rather than a mistake.

When the developer's billing changes, for example a refund or a lapsed subscription, **the developer's backend calls this API** to change the Entitlement: `PATCH /v1/entitlements/{id}` with `status: revoked`, or by setting `ends_at` so it lapses on schedule, using an app-scoped [API Key](../../api-reference/api-keys/#app-confined-permissions). This API never learns about the payment itself. See [Workflows → Purchase → access](../../workflows/#purchase--access).

### Billing system of record

Substratal Apps is not a payment processor or marketplace billing engine for any Application, ever, including after the marketplace phase ships. Each Application's developer owns the billing relationship with their own end users (their own Stripe or equivalent), entirely outside this API, and calls [Entitlements](../../api-reference/entitlements/) to reflect the outcome. `Order`/`order_id` is reference metadata the developer supplies for their own reconciliation. It is never a record that a Substratal-run billing system pushes webhooks about. When the marketplace ships, it makes Applications discoverable, each with its developer's own pricing and billing. It is not a checkout that Substratal runs.

The **only** payment flow that runs through Substratal Apps itself is its own platform subscription: what an Application owner pays Substratal for Starter/Team/Enterprise. See [Pricing → How the subscription is charged](../../pricing/#how-the-subscription-is-charged).

---

## Audit Event

An immutable record of who changed what access, when. Never edited or deleted through the API, including after the record it describes is itself deleted. Only two scheduled jobs touch an event after it's written, and both only reduce it to its shape: archival at the end of the hot window, and the erasure cascade's redaction of `before`/`after` for a deleted User (see [Non-Functional Requirements → Audit](../../non-functional-requirements/#audit)).

| Field | Type | Notes |
|---|---|---|
| `id` | string | `evt_` prefix. |
| `action` | string | See [Action catalog](#action-catalog) below. |
| `actor` | object | Who made the change: `{ "type": "user" \| "api_key" \| "system", "id": "usr_…" \| "key_…" \| "system" }`. A change made by a User through an MCP or agent session that used their own token is `type: user`; the audit log doesn't distinguish which client a User's token was used from. A change made by an API Key is `type: api_key`, with the key's id. System-initiated changes, such as a trial expiring, an erasure cascade, or a Stripe sync, are `type: system`, `id: "system"`. |
| `target` | object | What changed: `{ "type": "user" \| "entitlement" \| "role" \| "role_assignment" \| "application" \| "organization" \| "tenant" \| "tier_change_request" \| "api_key" \| "webhook", "id": "…" }`. `id` is the target's own id, with one exception: a Role assignment has no id of its own, so for `role_assignment` it's the `role_id`, and the User is in `target_user_id`. |
| `target_user_id` | string, nullable | Whose access or data changed, when there is such a User. It's `null` for events with no user subject, such as `application.updated`, `api_key.created`, or `tenant.plan_changed`. For an org-wide Entitlement change it's also `null`. The per-member effect shows up as [`access.*` webhooks](../../api-reference/webhooks/#event-types), not as one Audit Event per member. |
| `application_id`, `organization_id`, `tenant_id` | string, nullable | Scope, where relevant. `tenant_id` is the Tenant the change belongs to: denormalized from the Application when there is one, set directly for `tenant.*` events, and `null` for platform-level events with neither (for example `user.created`). |
| `before` / `after` | object, nullable | A snapshot of the changed fields only, not the whole record. Secrets and hashes never appear, even as before/after values. |
| `request_id` | string, nullable | The `X-Request-Id` of the API call that caused it, or `null` for system events. It joins audit to request logs. |
| `timestamp` | timestamp | The commit time of the change. |

### Example

```json
{
  "id": "evt_01JAG7X3P8QY110M9N807P6Q5R",
  "action": "entitlement.disabled",
  "actor": { "type": "user", "id": "usr_01JAG9STAFF000000000000000" },
  "target": { "type": "entitlement", "id": "ent_01JAG9F4Q1W2E3R4T5Y6V7J809" },
  "target_user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_invoicer",
  "organization_id": null,
  "tenant_id": "tnt_01JAG1SYSTEM00000000000000",
  "before": { "status": "active" },
  "after": { "status": "disabled", "disabled_reason": "billing_dispute" },
  "request_id": "req_7c1e9a2f4b",
  "timestamp": "2026-09-30T16:22:41Z"
}
```

### Action catalog

Every value `action` can take. It's mirrored exactly by the `AuditAction` enum in [openapi.yaml](../../openapi.yaml), and by the `check` constraint on `audit_events.action` (see [Database Schema](../database-schema/#audit_events)). Entries marked **admin-only** never fire for a self-service change. See [What triggers an Audit Event](#what-triggers-an-audit-event) below.

| Action | Fires when | `target.type` |
|---|---|---|
| `user.created` | `POST /v1/users`, `POST /v1/auth/signup`, or an invite via org membership | `user` |
| `user.updated` | `users.manage` activates an `invited` User without a password (`PATCH status: active`). Suspension, reactivation, restore, email, and deletion each have their own action — **admin-only** | `user` |
| `user.email_changed` | An email change completes (self, after verification; or admin, immediately) | `user` |
| `user.suspended` | `POST /v1/users/{id}/suspend`, or `PATCH status: suspended` — **admin-only** | `user` |
| `user.reactivated` | `PATCH status: active` on a `suspended` User, or on a soft-deleted one (a restore) — **admin-only** | `user` |
| `user.deleted` | `DELETE /v1/users/{id}`, or the soft-delete step of an erasure request | `user` |
| `user.erasure_requested` | `POST /v1/users/{id}/erasure-requests` | `user` |
| `user.erasure_cancelled` | `DELETE /v1/users/{id}/erasure-requests/current` — **admin-only** | `user` |
| `user.erased` | The hard-delete cascade completes (actor `system`) | `user` |
| `user.password_changed` | `POST /v1/users/{id}/password` | `user` |
| `user.password_reset` | `POST /v1/auth/password/reset` | `user` |
| `user.mfa_enabled` / `user.mfa_disabled` | TOTP confirmed / disabled by the user | `user` |
| `user.mfa_reset` | `DELETE /v1/users/{id}/mfa/totp` — **admin-only** | `user` |
| `entitlement.granted` | An Entitlement is created, or returns to `active` | `entitlement` |
| `entitlement.disabled` | `status` set to `disabled` | `entitlement` |
| `entitlement.revoked` | `status` set to `revoked` | `entitlement` |
| `entitlement.expired` | Automatic transition to `expired` (actor `system`) | `entitlement` |
| `entitlement.updated` | `ends_at`, `source`, or `order_id` changes without a status change | `entitlement` |
| `entitlement.member_scope_changed` | An org grant's `member_scope`/`member_overrides` changes — see [Entitlements → Org-wide entitlements](../entitlements/#org-wide-entitlements-scoping-members-in-or-out) | `entitlement` |
| `entitlement.deleted` | `DELETE /v1/entitlements/{id}` (error correction) | `entitlement` |
| `role.created` / `role.updated` / `role.deleted` | Role definition changes, including Roles seeded or removed via an Application's `available_app_roles` | `role` |
| `role.assigned` / `role.removed` | `POST`/`DELETE /v1/users/{id}/roles/{roleId}` (only when something actually changed) | `role_assignment` |
| `profile.updated` | An admin changes another user's Profile or AppProfile — **admin-only** | `user` |
| `settings.updated` | An admin changes another user's Settings or AppSettings — **admin-only** | `user` |
| `application.created` | `POST /v1/applications` | `application` |
| `application.updated` | `PATCH /v1/applications/{id}` (configuration fields) | `application` |
| `application.review_status_changed` | A reviewer approves, rejects, suspends, or reinstates; or a rejected app is resubmitted — see [Applications → The review lifecycle](../applications/#the-review-lifecycle) | `application` |
| `organization.created` / `organization.updated` | `POST`/`PATCH /v1/organizations…` | `organization` |
| `organization.member_invited` | A `pending` membership is created by email; the person hasn't accepted yet | `organization` |
| `organization.member_added` / `organization.member_removed` | A membership becomes `active` (accepted, or added directly by `organizations.manage`), or is removed, including leaving and declining | `organization` |
| `organization.member_role_changed` | `PATCH /v1/organizations/{id}/members/{userId}` | `organization` |
| `tenant.plan_changed` | `plan` changes via a Stripe sync (actor `system`) | `tenant` |
| `tenant.subscription_status_changed` | `subscription_status` or `restricted` changes via a Stripe sync (actor `system`) | `tenant` |
| `tenant.tier_change_requested` | `POST /v1/tenants/{id}/tier-change-requests` — the request itself, not yet the migration | `tier_change_request` |
| `tenant.tier_change_request_updated` | `PATCH` on a tier-change request (scheduled, started, cancelled) | `tier_change_request` |
| `tenant.tier_changed` | A tier-change request reaches `completed`: `tier`/`region` change and `status` returns to `active` — see [Tenancy → How a tier change happens today](../tenancy/#how-a-tier-change-happens-today) | `tenant` |
| `api_key.created` / `api_key.updated` / `api_key.rotated` / `api_key.revoked` | [API Key](../../api-reference/api-keys/) lifecycle | `api_key` |
| `api_key.restrict_destructive_disabled` | `restrict_destructive` explicitly set to `false` on an `intended_use: "agent"` key (written *in addition to* `api_key.created`/`updated`) — see [API Keys → Agent keys](../../api-reference/api-keys/#agent-keys--restrict_destructive) | `api_key` |
| `webhook.created` / `webhook.updated` / `webhook.deleted` / `webhook.secret_rotated` | [Webhook](../../api-reference/webhooks/) subscription lifecycle | `webhook` |

This list is the authoritative source for `action` values. If an endpoint's page describes a write that isn't represented here, that's a spec bug; file it the same way as any other inconsistency.

### What triggers an Audit Event

Every write to an [Entitlement](../entitlements/), every [Role](../roles-and-permissions/) definition or assignment change, every change to a credential (API Key, webhook secret, password, MFA), every Application, Organization, and Tenant change, and every admin-initiated (not self-service) change to a user's [Profile](../profiles/) or [Settings](../settings/). Self-service changes a user makes to their own Profile/Settings aren't audited at this level of detail. That's ordinary account activity, not an access-control event.

**Not** Audit Events: logins, failed logins, token refreshes, and reads. Those go to the security log (structured application logs with `X-Request-Id`, retained 1 year in CloudWatch Logs; see [Non-Functional Requirements → Security logging](../../non-functional-requirements/#security-logging)). The Audit log records *changes to state*, and the security log records *access attempts*. See [Non-Functional Requirements → Audit](../../non-functional-requirements/#audit).
