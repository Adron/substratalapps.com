---
layout: default
title: Audit
parent: API Reference
nav_order: 10
---

# Audit
{: .no_toc }

See [Domain Model → Audit Event](../../domain-model/orders-and-audit/#audit-event) for the full field reference.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/audit-events` | Query the audit log, filterable by `user_id`, `application_id`, `actor_user_id`, `action`, and a `since`/`until` time range. |

There is deliberately no write endpoint — Audit Events are produced only as a side effect of other writes (an Entitlement change, a Role assignment, …), never created directly.

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
      "actor_user_id": "usr_01JAG9SUPPORT0000000000000",
      "action": "entitlement.disabled",
      "target_user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
      "application_id": "app_invoicer",
      "before": { "status": "active" },
      "after": { "status": "disabled", "disabled_reason": "billing_dispute" },
      "timestamp": "2026-09-30T16:22:41Z"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Requires a platform role with `audit.view` (typically `support` or `superadmin`). A user can also fetch their own audit trail via `GET /v1/audit-events?target_user_id=me` without that role — seeing the history of changes made *to* their own account is a self-service right, not an admin privilege.

## Retention

Audit Events are never deleted or edited, including after the record they describe is itself hard-deleted (e.g. a user's right-to-erasure request removes their Profile/Settings but the Audit trail of *that deletion itself* persists). See [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention).
