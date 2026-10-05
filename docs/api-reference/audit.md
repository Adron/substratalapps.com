---
layout: default
title: Audit
parent: API Reference
nav_order: 11
---

# Audit
{: .no_toc }

See [Domain Model → Audit Event](../../domain-model/orders-and-audit/#audit-event) for the full field reference and the authoritative action catalog.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/audit-events` | `audit.view` (platform or app-confined); or self via `target_user_id=me`; or a Tenant owner via `tenant_id` | Query the audit log. |
| `GET` | `/v1/audit-events/{id}` | same as above, for that event | Fetch one event. |

There's deliberately no write endpoint. Audit Events are produced only as a side effect of other writes, never created directly, and never edited or deleted through the API.

## `GET /v1/audit-events`

```
GET /v1/audit-events?target_user_id=usr_01JAG3Z9X8QS3F6K2M4N5P6R7S&since=2026-09-01T00:00:00Z
```

```json
// Response — 200
{
  "data": [
    {
      "id": "evt_01JAG7X3P8QY1L0M9N8O7P6Q5R",
      "action": "entitlement.disabled",
      "actor": { "type": "user", "id": "usr_01JAG9SUPPORT0000000000000", "via_api_key_id": null },
      "target": { "type": "entitlement", "id": "ent_01JAG9F4Q1W2E3R4T5Y6U7I8O9" },
      "target_user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "application_id": "app_invoicer",
      "organization_id": null,
      "tenant_id": "tnt_01JAG1SUBSTRATAL0000000000",
      "before": { "status": "active" },
      "after": { "status": "disabled", "disabled_reason": "billing_dispute" },
      "request_id": "req_7c1e9a2f4b",
      "timestamp": "2026-09-30T16:22:41Z"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

| Filter | Notes |
|---|---|
| `target_user_id` | The User whose access or data changed. `me` is allowed. |
| `target_type`, `target_id` | For example `target_type=api_key&target_id=key_…`. |
| `actor_id` | A `usr_…`, a `key_…`, or `system`. |
| `actor_type` | `user`, `api_key`, or `system`. |
| `action` | One action. Repeat the parameter for several (`?action=role.assigned&action=role.removed`). |
| `application_id`, `organization_id`, `tenant_id` | Scope filters. |
| `since`, `until` | ISO 8601, inclusive/exclusive. |

**Who sees what:**

| Caller | Sees |
|---|---|
| Platform `audit.view` | Everything. |
| App-scoped key with `audit.view` | Only events whose `application_id` is its own Application. The filter is forced. |
| Any User, with `target_user_id=me` | Their own trail. Seeing the history of changes made *to* your own account is a self-service right, not an admin privilege. Calling without that filter and without `audit.view` returns `403`. |
| A Tenant owner, with `tenant_id=<their tenant>` | Events for their own Applications. That's the developer's view of what happened in their apps, including what support did. |

**Hot window only.** The API returns only events still in hot storage: 30 days on Starter, 1 year on Team, and negotiated on Enterprise. Older events are archived in a shape-only form and are retrievable through support, not through this endpoint. A `since` earlier than the window is allowed and simply returns nothing older. The response header `Audit-Hot-Window-Start` gives the earliest timestamp available for the caller's scope. See [Non-Functional Requirements → Audit log lifecycle](../../non-functional-requirements/#audit-log-lifecycle).

## `GET /v1/audit-events/{id}`

Returns one event, same shape. `404 audit_event_not_found` if it doesn't exist, isn't visible to the caller, or has been archived.

## Retention

Audit Events are never deleted or edited through the API, including after the record they describe is hard-deleted. For example, a user's right-to-erasure request removes their Profile/Settings, but the audit trail of *that deletion itself* persists, with snapshot values redacted. See [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention).

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `audit_event_not_found` | 404 | See above. |
| `forbidden` | 403 | No `audit.view`, and the query isn't scoped to `me` or an owned Tenant. |
