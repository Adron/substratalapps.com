---
layout: default
title: Entitlements
parent: API Reference
nav_order: 7
---

# Entitlements
{: .no_toc }

The on/off switch for a user's access to an app. See [Domain Model → Entitlements](../../domain-model/entitlements/) for the full field reference and status lifecycle.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/users/{id}/entitlements` | self, `entitlements.manage`, or app-confined `entitlements.manage`/`users.list` | A user's entitlements, personal and org-sourced — what a dashboard renders as "your apps." |
| `POST` | `/v1/users/{id}/entitlements` | `entitlements.manage` (platform or app-confined) | Grant a personal Entitlement. |
| `GET` | `/v1/entitlements` | `entitlements.manage` (platform or app-confined) | Cross-user list: "who has app X." |
| `GET` | `/v1/entitlements/{id}` | self, `entitlements.manage`, or the org admin of an `org_seat` row | Fetch one Entitlement. |
| `PATCH` | `/v1/entitlements/{id}` | `entitlements.manage` (platform or app-confined); an org admin may change only `status` (`active` ⇄ `disabled`) and `member_scope`/`member_overrides` on their own Organization's `org_seat` row | **The toggle.** Change status, term, or org-grant scope. |
| `DELETE` | `/v1/entitlements/{id}` | `entitlements.manage` | Hard-remove a grant made in error. |

Org-wide grants are created through [`POST /v1/organizations/{id}/entitlements`](../organizations/#post-v1organizationsidentitlements). Once created, they're read and changed through `/v1/entitlements/{id}` like any other Entitlement.

**App-confined callers.** An app-scoped [API Key](../api-keys/#app-confined-permissions) holding `entitlements.manage` can do everything on this page, but only for Entitlements whose `application_id` is its own Application. That includes its own Application's org-wide grants (a developer's backend reflecting a team purchase). Anything else returns `404 entitlement_not_found`, so other apps' rows aren't revealed. This is how an Application developer reflects their own billing outcome. See [Workflows → Purchase → access](../../workflows/#purchase--access).

## The Entitlement object

```json
{
  "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "organization_id": null,
  "application_id": "app_timetrack",
  "status": "active",
  "source": "purchase",
  "order_id": "sub_1Q2w3E4r5T6y7U8i",
  "starts_at": "2026-01-14T18:05:00Z",
  "ends_at": null,
  "disabled_reason": null,
  "member_scope": null,
  "member_overrides": null,
  "test_mode": false,
  "created_at": "2026-01-14T18:05:00Z",
  "updated_at": "2026-01-14T18:05:00Z"
}
```

`order_id` is the developer's own opaque reference (up to 255 characters, often their Stripe subscription id). It isn't a resource of this API. See [Orders & Audit → Order references](../../domain-model/orders-and-audit/#order).

## `GET /v1/users/{id}/entitlements`

```json
// Response — 200
{
  "data": [
    {
      "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
      "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "organization_id": null,
      "application_id": "app_timetrack",
      "status": "active",
      "source": "purchase",
      "order_id": "sub_1Q2w3E4r5T6y7U8i",
      "starts_at": "2026-01-14T18:05:00Z",
      "ends_at": null
    },
    {
      "id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9",
      "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "organization_id": null,
      "application_id": "app_invoicer",
      "status": "disabled",
      "source": "purchase",
      "order_id": "sub_1Q9z8X7c6V5b4N3m",
      "starts_at": "2026-03-02T09:00:00Z",
      "ends_at": null,
      "disabled_reason": "billing_dispute"
    },
    {
      "id": "ent_01JAGD4E5F6G7H8J9K0L1M2N3O",
      "user_id": null,
      "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
      "application_id": "app_payroll",
      "status": "active",
      "source": "org_seat",
      "order_id": null,
      "starts_at": "2026-04-02T10:00:00Z",
      "ends_at": null,
      "granted_via": { "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "member_decision": "included" }
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

- Returns the user's **personal** rows, plus every **org-wide** row held by an Organization they belong to, each annotated with `granted_via`. See [Domain Model → Entitlements → Attribution](../../domain-model/entitlements/#attribution).
- `revoked` and `expired` rows are excluded by default. Pass `?status=revoked`, or `?include_inactive=true` for everything.
- Filters: `application_id`, `status`, `source`.
- An app-confined caller sees only rows for its own Application.

The question "does this user have access to app X right now?" is answered by [effective-permissions](../roles-and-permissions/#get-v1usersidappsappideffective-permissions), not by scanning this list.

## `POST /v1/users/{id}/entitlements`

```json
// Request — an admin comp
{ "application_id": "app_invoicer", "source": "admin_grant" }
```
```json
// Request — a developer's backend reflecting a purchase in their own billing system (app-scoped key)
{ "application_id": "app_invoicer", "source": "purchase", "order_id": "sub_1Q9z8X7c6V5b4N3m" }
```
```json
// Request — a 14-day trial
{ "application_id": "app_invoicer", "source": "trial", "ends_at": "2026-10-19T12:00:00Z" }
```
```json
// Response — 201
{
  "id": "ent_01JAGA1B2C3D4E5F6G7H8J9K0L",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "organization_id": null,
  "application_id": "app_invoicer",
  "status": "active",
  "source": "admin_grant",
  "order_id": null,
  "starts_at": "2026-10-05T12:00:00Z",
  "ends_at": null,
  "disabled_reason": null,
  "test_mode": false,
  "created_at": "2026-10-05T12:00:00Z",
  "updated_at": "2026-10-05T12:00:00Z"
}
```

| Field | Rules |
|---|---|
| `application_id` | Required. The Application must be `approved` and visible to the caller (see [Applications](../applications/#who-can-see-an-application)); `internal` apps only accept grants from platform `entitlements.manage`. |
| `source` | Required: `purchase`, `trial`, or `admin_grant`. `org_seat` is rejected here (`422 invalid_source`), because org grants go through Organizations. |
| `order_id` | Optional, up to 255 characters. Strongly recommended for `purchase`. |
| `starts_at` | Optional, defaults to now. It may be in the future: the row is created `active`, but access doesn't resolve until `starts_at` passes. Can't be more than 1 year ahead. |
| `ends_at` | **Required** for `trial` (`422 ends_at_required`). Optional for the others. Must be after `starts_at`. |

- Requires an `Idempotency-Key` header. A developer reflecting a purchase should key it on their own order or event id, so a retried billing webhook on their side never double-grants. See [Conventions → Idempotency](../conventions/#idempotency).
- **One live personal row per (user, app):** if the user already has an `active` or `disabled` personal Entitlement to this Application, the call returns `409 entitlement_already_exists` with `details.existing_entitlement_id` and `details.status`. Re-enable the existing one with `PATCH` instead. An `expired` or `revoked` row doesn't block a new grant.
- The user must exist and not be `deleted`. Granting to an `invited` user is allowed: access starts when they activate.
- **Plan checks on the Application's Tenant** (independent of the caller's permission): a grant that would add a new seat beyond Starter's 1,000 returns `409 plan_limit_reached` (`resource: "seats"`). On a `restricted` Tenant, a grant that would add a new seat returns `402 subscription_required`. See [Pricing → Enforcement](../../pricing/#enforcement).
- Effects, in one transaction: writes Audit Event `entitlement.granted`, emits the `entitlement.granted` webhook, and emits `access.granted` if this flips the user's resolved access to the app on.

## `GET /v1/entitlements`

```
GET /v1/entitlements?application_id=app_invoicer&status=active
```
```json
// Response — 200
{
  "data": [
    {
      "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
      "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "organization_id": null,
      "application_id": "app_invoicer",
      "status": "active",
      "source": "purchase",
      "order_id": "sub_1Q2w3E4r5T6y7U8i",
      "starts_at": "2026-01-14T18:05:00Z",
      "ends_at": null
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Filters: `application_id`, `status`, `user_id`, `organization_id`, `source`, `order_id` (exact match, for a developer reconciling against their own billing), `ends_before` (find trials about to lapse), and `include_inactive`. This is the support-dashboard query ("who currently has app X", "every Entitlement disabled in the last billing dispute"). The per-user list above doesn't support it. Org-wide grants appear as single rows here (`user_id: null`), not expanded per member.

## `GET /v1/entitlements/{id}`

Returns the Entitlement object. Self may fetch their own personal rows, plus org rows of Organizations they belong to (with `granted_via`). An org admin may fetch their Organization's rows.

## `PATCH /v1/entitlements/{id}` — the toggle

```json
// Request — turn off
{ "status": "disabled", "disabled_reason": "billing_dispute" }
```
```json
// Request — turn back on
{ "status": "active" }
```
```json
// Request — the developer's customer refunded
{ "status": "revoked", "disabled_reason": "refunded" }
```
```json
// Request — extend a trial
{ "ends_at": "2026-11-02T12:00:00Z" }
```
```json
// Request — convert a trial to a purchase
{ "source": "purchase", "order_id": "sub_1Q9z8X7c6V5b4N3m", "ends_at": null }
```
```json
// Request — on an org-wide grant, narrow who it applies to
{ "member_scope": "denylist", "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"] }
```
```json
// Response — 200, the full updated Entitlement
```

**Status transitions.** Anything not listed here returns `409 invalid_status_transition`.

| From → to | Allowed for | Notes |
|---|---|---|
| `active → disabled` | `entitlements.manage`; org admin on `org_seat` | `disabled_reason` **required** (`422 disabled_reason_required`), 1–255 characters. Reversible. |
| `disabled → active` | same | `disabled_reason` is cleared automatically. |
| `active`/`disabled`/`expired → revoked` | `entitlements.manage` only (not org admins) | Terminal. `disabled_reason` is required and records why ("refunded", "chargeback", "tos_violation"). |
| `expired → active` | `entitlements.manage` only | Renewal. Only valid together with an `ends_at` in the future, or `ends_at: null`. |
| `active → expired` | system only | Happens when `ends_at` passes. A sweep runs every 5 minutes, and access resolution also treats `ends_at <= now` as expired immediately, so expiry is never late. |
| `revoked → *` | nobody | Grant a new Entitlement instead. |

**Other writable fields:**

| Field | Rule |
|---|---|
| `ends_at` | Changeable on `active`/`disabled`/`expired` rows. Setting it in the past on an `active` row expires it on the next sweep. |
| `source` | Only `trial → purchase` (a conversion). Any other change returns `422 immutable_field`. |
| `order_id` | Settable while `null`. Changing an existing value returns `422 immutable_field`, so it stays a stable reconciliation key. |
| `member_scope`, `member_overrides` | `org_seat` rows only (`422 not_an_org_grant` otherwise). Every listed user must be a current member (`422 member_override_not_a_member`). |
| `starts_at`, `user_id`, `organization_id`, `application_id` | Read-only (`422 read_only_field`). |

**Effects:** every change writes an Audit Event and emits a webhook in the same transaction. The action/event is the target status (`entitlement.granted`, `entitlement.disabled`, `entitlement.revoked`), or `entitlement.updated` for term/source/order changes, or `entitlement.member_scope_changed` for scope changes. Separately, `access.revoked` / `access.granted` fire once per affected user whose *resolved* access to the Application actually flips. For an org grant that can mean many users. A user who still has another active path, such as a personal Entitlement, gets no `access.*` event. See [Webhooks → Event types](../webhooks/#event-types).

`disabled`/`revoked` transitions are destructive for [MCP / `restrict_destructive`](../../mcp-server/#tool-annotations--safety) purposes. Send `If-Match` when two operators might act on the same row. See [Conventions → Concurrency](../conventions/#concurrency-etag--if-match).

{: .important }
`revoked` is terminal. You can't `PATCH` a `revoked` entitlement back to `active`; grant a new one instead. `disabled` is the reversible switch, and re-enabling restores exactly the prior state (Roles, AppProfile, AppSettings untouched).

## `DELETE /v1/entitlements/{id}`

```json
// Response — 204
```

Removes the record entirely. It's reserved for correcting a grant made by mistake (wrong user, wrong app, duplicate). For every other case, such as a real purchase ending or a real admin decision to cut off access, use `PATCH` with `status: revoked` or `disabled` so the history survives.

- Requires `entitlements.manage` (platform or app-confined).
- **Rejected with `409 entitlement_order_linked` whenever `order_id` is set**, whoever the caller is, whatever `restrict_destructive` is set to. An order-linked Entitlement is a real financial record, never "a mistake" in this endpoint's sense.
- Rejected with `409 entitlement_too_old` if the row is more than 24 hours old. After a day it has very likely been relied on, so revoke it instead.
- Writes Audit Event `entitlement.deleted` (the audit trail of the correction survives, even though the row doesn't), emits the `entitlement.deleted` webhook, and emits `access.revoked` if it was the user's only active path.
- Destructive: a `restrict_destructive` key gets `403 destructive_operation_restricted`.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `entitlement_not_found` | 404 | `{id}` doesn't resolve, or isn't visible to the caller. |
| `entitlement_already_exists` | 409 | A live (`active`/`disabled`) personal row already exists for this user and app. |
| `invalid_status_transition` | 409 | See the transition table. |
| `invalid_source` | 422 | `source: org_seat` on the personal grant endpoint. |
| `ends_at_required` | 422 | A `trial` without `ends_at`. |
| `disabled_reason_required` | 422 | `disabled`/`revoked` without a reason. |
| `immutable_field` | 422 | Changing `source` (other than trial → purchase) or an existing `order_id`. |
| `not_an_org_grant` | 422 | `member_scope`/`member_overrides` on a personal row. |
| `member_override_not_a_member` | 422 | A `member_overrides` id isn't a current member. |
| `application_not_available` | 409 | The Application isn't `approved`, or is `internal` and the caller isn't platform. |
| `entitlement_order_linked` | 409 | `DELETE` on a row with `order_id`. |
| `entitlement_too_old` | 409 | `DELETE` on a row older than 24 hours. |
