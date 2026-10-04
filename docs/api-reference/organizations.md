---
layout: default
title: Organizations
parent: API Reference
nav_order: 9
---

# Organizations
{: .no_toc }

Team/seat management — see [Decisions → Organizations](../../decisions/#2-organizations) for why this is [Phase 2](../../roadmap/#phase-2), not the MVP itself.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/organizations` | List (admin) or, for a non-admin caller, just the orgs they're a member of. |
| `POST` | `/v1/organizations` | Create an org. |
| `GET` | `/v1/organizations/{id}/members` | List member Users. |
| `POST` | `/v1/organizations/{id}/members` | Add a member (by `user_id` or by email invite), optionally as `org_admin`. |
| `DELETE` | `/v1/organizations/{id}/members/{userId}` | Remove a member. |
| `GET` | `/v1/organizations/{id}/entitlements` | List org-wide ("seat") entitlements. |
| `POST` | `/v1/organizations/{id}/entitlements` | Grant an app to the whole org. |

## `GET /v1/organizations`

```json
// Response — 200, non-admin caller (scoped to their own memberships)
{
  "data": [
    { "id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "name": "Acme Co.", "status": "active" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Self-scoped: a non-admin caller sees every Organization they hold an [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) in — not just one, since a User can belong to more than one Organization at once. A platform role with `organizations.manage` sees every Organization and may filter with `?status=active`.

## `POST /v1/organizations`

```json
// Request
{ "name": "Acme Co." }
```
```json
// Response — 201
{ "id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "name": "Acme Co.", "status": "active", "created_at": "2026-10-04T09:00:00Z" }
```

Requires a platform role with `organizations.manage` — self-service org creation (e.g. as part of a team-plan signup flow) is a product decision for [Phase 3](../../roadmap/#phase-3), not assumed here.

## `GET /v1/organizations/{id}/members`

```json
// Response — 200
{
  "data": [
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "role": "org_admin", "joined_at": "2026-10-04T09:05:00Z" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Any member of the org, or a platform role with `organizations.manage`. `role` is the member's [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) standing — `org_admin` or `member` — within this one Organization specifically.

## `DELETE /v1/organizations/{id}/members/{userId}`

```json
// Response — 204
```

Requires org-admin standing on this Organization (see [Delegated admin](#delegated-admin)) or a platform role with `organizations.manage`. Removes the member's access to every org-wide (`source: org_seat`) Entitlement; any Entitlement granted to them individually is untouched.

## `POST /v1/organizations/{id}/entitlements`

```json
// Request
{
  "application_id": "app_invoicer",
  "source": "purchase",
  "order_id": "ord_01JAG8B3N4M5K6J7H8G9F0D1S2",
  "member_scope": "denylist",
  "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"]
}
```
```json
// Response — 201
{
  "id": "ent_01JAGB2C3D4E5F6G7H8J9K0L1M",
  "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "user_id": null,
  "application_id": "app_invoicer",
  "status": "active",
  "source": "org_seat",
  "member_scope": "denylist",
  "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"]
}
```

Requires a platform role with `organizations.manage`, or org-admin standing on this Organization. `member_scope`/`member_overrides` are optional and default to `all_members`/none — every current and future member of the org inherits access without a per-member grant — see [Domain Model → Entitlements → Org-wide entitlements](../../domain-model/entitlements/#org-wide-entitlements-scoping-members-in-or-out) for what `allowlist`/`denylist` change. `effective_permissions` for an included member still resolves their own app-scoped Role on top of this; the org entitlement only answers the on/off question, same as a personal one would. An org admin can `PATCH` this same resource (see [Entitlements](../entitlements/)) to change `member_scope`/`member_overrides` later without re-granting the whole app.

## Delegated admin

An org admin is simply a User whose [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) `role` is `org_admin` for that specific Organization — not a third [Role](../../domain-model/roles-and-permissions/#role) scope alongside `platform` and `application_id`. This standing lets them manage their own org's members (`POST`/`DELETE` on `/members`) and org-wide Entitlements (`POST`/`PATCH` on `/entitlements`, including `member_scope`) without needing a platform-wide admin Role — and says nothing about their standing in any other Organization, the same independence [AppRole](../../domain-model/roles-and-permissions/#approle) already has across Applications. See [Decisions → Tenant vs. Organization](../../decisions/#11-tenant-vs-organization) for why this is unrelated to infrastructure-level admin (`tenants.manage`), which an org admin never holds.

## Errors specific to this resource

| Code | When |
|---|---|
| `already_member` | Adding a user who's already a member. |
| `member_has_active_app_sessions` | Removing a member while they hold an app-scoped Role that would otherwise orphan it — the Role assignment is removed along with the membership, not blocked; this code labels the response so a client can surface what else changed. |
| `member_override_not_a_member` | A `user_id` in `member_overrides` isn't actually a member of this Organization. |
