---
layout: default
title: Organizations
parent: API Reference
nav_order: 9
---

# Organizations
{: .no_toc }

{: .decision }
See [Decisions → Organizations](../../decisions/#2-organizations) before building against this page — multi-seat access may not be a day-one requirement, in which case this entire resource is [Phase 3](../../roadmap/#phase-3).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/organizations` | List (admin) or, for a non-admin caller, just their own org. |
| `POST` | `/v1/organizations` | Create an org. |
| `GET` | `/v1/organizations/{id}/members` | List member Users. |
| `POST` | `/v1/organizations/{id}/members` | Add a member (by `user_id` or by email invite). |
| `DELETE` | `/v1/organizations/{id}/members/{userId}` | Remove a member. |
| `GET` | `/v1/organizations/{id}/entitlements` | List org-wide ("seat") entitlements. |
| `POST` | `/v1/organizations/{id}/entitlements` | Grant an app to the whole org. |

## `POST /v1/organizations/{id}/entitlements`

```json
// Request
{ "application_id": "app_invoicer", "source": "purchase", "order_id": "ord_01JAG8B3N4M5K6J7H8G9F0D1S2" }
```
```json
// Response — 201
{
  "id": "ent_01JAGB2C3D4E5F6G7H8J9K0L1M",
  "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "user_id": null,
  "application_id": "app_invoicer",
  "status": "active",
  "source": "org_seat"
}
```

Every current and future member of the org inherits access without a per-member grant — see [Domain Model → Organizations → Relationship to Entitlements](../../domain-model/users-and-organizations/#relationship-to-entitlements). `effective_permissions` for a member still resolves their own app-scoped Role on top of this; the org entitlement only answers the on/off question, same as a personal one would.

## Delegated admin

An org admin (a Role scoped to the Organization, not the whole platform) can manage their own org's members and app roles without needing a platform-wide admin Role. The exact shape of an "org admin" Role — whether it's a third Role scope alongside `platform` and `application_id`, or modeled as an app-scoped Role on a special internal "org management" Application — is unresolved; flagged here rather than specified prematurely. See [Roadmap → Phase 3](../../roadmap/#phase-3).

## Errors specific to this resource

| Code | When |
|---|---|
| `already_member` | Adding a user who's already a member. |
| `member_has_active_app_sessions` | Removing a member while they hold an app-scoped Role that would otherwise orphan — surfaced as a warning in `details`, not necessarily a hard block; exact behavior TBD. |
