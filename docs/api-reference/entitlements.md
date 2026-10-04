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

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/users/{id}/entitlements` | List a user's entitlements — this is what the hub dashboard renders as "your apps." |
| `POST` | `/v1/users/{id}/entitlements` | Grant access to an app (`admin_grant`, or internal use by the billing webhook handler for `purchase`). |
| `GET` | `/v1/entitlements` | **Admin/support view.** List across all users, filterable by `application_id`, `status`, `user_id`, `source`. The "who has app X enabled" query. |
| `GET` | `/v1/entitlements/{id}` | Fetch one entitlement by its own ID. |
| `PATCH` | `/v1/entitlements/{id}` | Change status. **This is the on/off toggle.** |
| `DELETE` | `/v1/entitlements/{id}` | Hard-remove a grant made in error. Distinct from setting `status: revoked` — see below. |

## `GET /v1/users/{id}/entitlements`

```json
// Response — 200
{
  "data": [
    {
      "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
      "application_id": "app_timetrack",
      "status": "active",
      "source": "purchase",
      "order_id": "ord_01JAG5D1C2E3F4G5H6J7K8L9M0",
      "starts_at": "2026-01-14T18:05:00Z",
      "ends_at": null
    },
    {
      "id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9",
      "application_id": "app_invoicer",
      "status": "disabled",
      "source": "purchase",
      "order_id": "ord_01JAG8B3N4M5K6J7H8G9F0D1S2",
      "starts_at": "2026-03-02T09:00:00Z",
      "ends_at": null
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

## `POST /v1/users/{id}/entitlements`

```json
// Request
{
  "application_id": "app_invoicer",
  "source": "admin_grant"
}
```
```json
// Response — 201
{
  "id": "ent_01JAGA1B2C3D4E5F6G7H8J9K0L",
  "application_id": "app_invoicer",
  "status": "active",
  "source": "admin_grant",
  "order_id": null,
  "starts_at": "2026-10-03T12:00:00Z",
  "ends_at": null
}
```

Requires `Idempotency-Key` — see [Conventions → Idempotency](../conventions/#idempotency). Requires a platform role with `entitlements.manage` for `source: admin_grant`; the billing webhook handler calls this same endpoint internally (via a service [API key](../api-keys/) scoped to `entitlements.manage`) with `source: "purchase"` and `order_id` set, keyed on the order ID so a retried webhook never double-grants.

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
      "application_id": "app_invoicer",
      "status": "active",
      "source": "purchase",
      "order_id": "ord_01JAG5D1C2E3F4G5H6J7K8L9M0",
      "starts_at": "2026-01-14T18:05:00Z",
      "ends_at": null
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Requires a platform role with `entitlements.manage`. This is the endpoint a support dashboard calls to answer "who currently has app X" or "show me every disabled entitlement from the last billing dispute" — the per-user list above doesn't support that cross-user query.

## `GET /v1/entitlements/{id}`

```json
// Response — 200
{
  "id": "ent_01JAG6R2N7HX0K9T4V5W6Y7Z8A",
  "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "application_id": "app_timetrack",
  "status": "active",
  "source": "purchase",
  "order_id": "ord_01JAG5D1C2E3F4G5H6J7K8L9M0",
  "starts_at": "2026-01-14T18:05:00Z",
  "ends_at": null,
  "disabled_reason": null
}
```

Self (for one's own entitlement), or a platform role with `entitlements.manage`.

## `PATCH /v1/entitlements/{id}` — the toggle

```json
// Request — turn off
{ "status": "disabled", "disabled_reason": "billing_dispute" }
```
```json
// Response — 200
{ "id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9", "status": "disabled", "disabled_reason": "billing_dispute", "...": "..." }
```

```json
// Request — turn back on
{ "status": "active", "disabled_reason": null }
```

Requires a platform role with `entitlements.manage` (e.g. `support` or `superadmin` — see [Roles & Permissions](../roles-and-permissions/)). Writes an [Audit Event](../audit/) and emits `entitlement.disabled` / `entitlement.granted` on the matching transition — see [Webhooks](../webhooks/) and [Workflows → Admin turns an app off for a user](../../workflows/#admin-turns-an-app-off-for-a-user).

{: .important }
`status` transitions are not unrestricted — `revoked` is terminal; you cannot `PATCH` a `revoked` entitlement back to `active`. Grant a new one instead. See [Domain Model → Entitlements → Status transitions](../../domain-model/entitlements/#status-transitions).

## `DELETE /v1/entitlements/{id}`

Requires a platform role with `entitlements.manage`. Removes the record entirely — reserved for correcting a grant made by mistake (wrong user, wrong app, duplicate), where no Audit trail of a real access change should persist. For every other case — a real purchase ending, a real admin decision to cut off access — use `PATCH` with `status: revoked` or `disabled` instead, so the history survives in the Audit log.

## Errors specific to this resource

| Code | When |
|---|---|
| `entitlement_already_exists` | A `POST` would create a duplicate active grant for the same `(user_id, application_id)`. |
| `invalid_status_transition` | e.g. attempting to reactivate a `revoked` entitlement. |
| `entitlement_not_found` | `{id}` doesn't resolve. |
