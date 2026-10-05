---
layout: default
title: Organizations
parent: API Reference
nav_order: 9
---

# Organizations
{: .no_toc }

Team/seat management. Part of [Phase 2](../../roadmap/#phase-2), not the MVP. See [Domain Model → Organization](../../domain-model/users-and-organizations/#organization).
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/organizations` | any User | Your own Organizations; everything with `organizations.manage`. |
| `POST` | `/v1/organizations` | any active User | Create an Organization; the caller becomes its first `org_admin`. |
| `GET` | `/v1/organizations/{id}` | member, or `organizations.manage` | Fetch one. |
| `PATCH` | `/v1/organizations/{id}` | org admin (`name`), `organizations.manage` (`name`, `status`) | Rename, or suspend/reactivate. |
| `GET` | `/v1/organizations/{id}/members` | member, or `organizations.manage` | List members. |
| `POST` | `/v1/organizations/{id}/members` | org admin, or `organizations.manage` | Add a member by `user_id` or email. |
| `PATCH` | `/v1/organizations/{id}/members/{userId}` | org admin, or `organizations.manage` | Change a member's `role`. |
| `DELETE` | `/v1/organizations/{id}/members/{userId}` | org admin, `organizations.manage`, or the member themselves (`me`) | Remove a member, or leave. |
| `GET` | `/v1/organizations/{id}/entitlements` | member, or `organizations.manage` | List org-wide ("seat") grants. |
| `POST` | `/v1/organizations/{id}/entitlements` | `entitlements.manage` (platform, or the Application's own confined key), or the Application's owner | Grant an app to the whole Organization. |

## The Organization object

```json
{
  "id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "name": "Acme Co.",
  "status": "active",
  "member_count": 42,
  "created_by": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "test_mode": false,
  "created_at": "2026-10-04T09:00:00Z",
  "updated_at": "2026-10-04T09:00:00Z"
}
```

## `GET /v1/organizations`

```json
// Response — 200, non-admin caller (scoped to their own memberships)
{
  "data": [
    { "id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "name": "Acme Co.", "status": "active", "member_count": 42, "my_role": "org_admin", "created_at": "2026-10-04T09:00:00Z" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

A non-admin caller sees every Organization they hold an [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) in. A User can belong to more than one. `my_role` is that caller's standing in each. A `organizations.manage` holder sees every Organization (with no `my_role`) and can filter with `?status=` and `?q=` (a name prefix).

## `POST /v1/organizations`

{: .decision }
**Proposed — confirm** ([DECISIONS.md #22](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#22-who-can-create-an-organization)). Self-service creation (any active User; rate-limited) is what makes team end users from [Phase 2](../../roadmap/#phase-2) workable, instead of a Substratal staff member creating every team. **The guard that makes it safe:** an org admin can never *create* an org-wide grant for their own Organization. Only whoever controls the Application can. Without that rule, anyone could create an Organization and grant themselves free access to any app. **Alternative:** keep creation admin-only until Phase 3.

```json
// Request
{ "name": "Acme Co." }
```
```json
// Response — 201
{ "id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P", "name": "Acme Co.", "status": "active", "member_count": 1, "created_by": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "test_mode": false, "created_at": "2026-10-04T09:00:00Z", "updated_at": "2026-10-04T09:00:00Z" }
```

Any active User may create one, with a limit of 10 per User per day (`429`). The caller becomes the first member, with `role: org_admin`, in the same transaction. A `name` is 1–100 characters and doesn't have to be unique. Must be called with a User token, not an API Key (`400 user_token_required`). Writes `organization.created`.

## `GET /v1/organizations/{id}`

Returns the Organization object. Members and `organizations.manage` only; anyone else gets `404 organization_not_found`.

## `PATCH /v1/organizations/{id}`

```json
// Request — rename (org admin)
{ "name": "Acme Corporation" }
```
```json
// Request — suspend (platform only)
{ "status": "suspended", "reason": "ToS investigation SUP-5120" }
```
```json
// Response — 200, the full updated Organization
```

- An org admin may change `name`. Only `organizations.manage` can change `status` (`403 forbidden` otherwise), and `reason` is recorded on the Audit Event.
- **What suspension does:** the Organization's own admin actions freeze. Every member, entitlement, and SSO write under it returns `403 organization_suspended`, and no new members can join.
- **What it doesn't do:** it doesn't touch existing org-wide Entitlements or members' access, the same non-cascading choice made for a suspended Application. A platform admin who actually wants to cut access disables or revokes the org's grants explicitly. Destructive.
- Writes `organization.updated`.

## `GET /v1/organizations/{id}/members`

```json
// Response — 200
{
  "data": [
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "display_name": "Jordan Alvarez", "status": "active", "role": "org_admin", "joined_at": "2026-10-04T09:05:00Z" },
    { "user_id": "usr_01JAGK7L8M9N0P1Q2R3S4T5U6V", "email": "sam@acme.example", "display_name": null, "status": "invited", "role": "member", "joined_at": "2026-10-05T08:00:00Z" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Members and `organizations.manage`. Filters: `?role=org_admin|member` and `?q=` (a prefix of email or name). `status` is the member's User status, so invited members show as `invited`.

## `POST /v1/organizations/{id}/members`

```json
// Request — an existing user
{ "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "role": "member" }
```
```json
// Request — by email (invites them if no account exists)
{ "email": "sam@acme.example", "role": "member" }
```
```json
// Response — 201
{ "user_id": "usr_01JAGK7L8M9N0P1Q2R3S4T5U6V", "email": "sam@acme.example", "display_name": null, "status": "invited", "role": "member", "joined_at": "2026-10-05T08:00:00Z" }
```

- Exactly one of `user_id` or `email` is required. `role` defaults to `member`.
- By `email`: if an active, invited, or suspended User has that email, they're added. Otherwise an `invited` User is created and gets an invitation email naming the Organization (see [Auth → Invitations](../auth/#invitations)). This is the one way a non-platform caller can cause a User to be created.
- Membership is immediate. There's no accept step for an existing User: being in an Organization only ever *adds* access paths, and the User can leave at any time.
- The new member immediately falls under every `all_members`/`denylist` org grant. `access.granted` fires for each Application they newly gain.
- Writes `organization.member_added`, and emits the `organization.member_added` webhook.

## `PATCH /v1/organizations/{id}/members/{userId}`

```json
// Request
{ "role": "org_admin" }
```
```json
// Response — 200, the member object
```

Demoting the Organization's last `org_admin` returns `409 last_org_admin`, because an Organization always has at least one. Writes `organization.member_role_changed`.

## `DELETE /v1/organizations/{id}/members/{userId}`

```json
// Response — 204
```

- An org admin or `organizations.manage` can remove anyone. Any member can remove themselves (`/members/me`).
- Removing the last `org_admin` returns `409 last_org_admin`. Promote someone else first.
- The removed User loses every org-wide (`source: org_seat`) access path through this Organization. `access.revoked` fires for each Application where that was their only active path, so a personal Entitlement held some other way is untouched. Their user id is also removed from `member_overrides` on this Organization's grants, so re-adding them later starts clean.
- **App Role assignments aren't touched.** Roles belong to the User, not the Organization. If the user has no other access path, those Roles simply grant nothing until they do (see [Access Control](../../access-control/)).
- Writes `organization.member_removed`, and emits the `organization.member_removed` webhook. Destructive.

## `GET /v1/organizations/{id}/entitlements`

```json
// Response — 200
{
  "data": [
    {
      "id": "ent_01JAGB2C3D4E5F6G7H8J9K0L1M",
      "user_id": null,
      "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
      "application_id": "app_invoicer",
      "status": "active",
      "source": "org_seat",
      "order_id": "sub_1QaB2cD3eF4gH5iJ",
      "starts_at": "2026-10-04T09:10:00Z",
      "ends_at": null,
      "member_scope": "denylist",
      "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"],
      "included_member_count": 41
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

`included_member_count` is computed: how many current members this grant reaches after `member_scope`. Same filters as `GET /v1/entitlements`.

## `POST /v1/organizations/{id}/entitlements`

```json
// Request
{
  "application_id": "app_invoicer",
  "order_id": "sub_1QaB2cD3eF4gH5iJ",
  "member_scope": "denylist",
  "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"]
}
```
```json
// Response — 201
{
  "id": "ent_01JAGB2C3D4E5F6G7H8J9K0L1M",
  "user_id": null,
  "organization_id": "org_01JAFZ8Y7X6W5V4U3T2S1R0Q9P",
  "application_id": "app_invoicer",
  "status": "active",
  "source": "org_seat",
  "order_id": "sub_1QaB2cD3eF4gH5iJ",
  "starts_at": "2026-10-05T12:00:00Z",
  "ends_at": null,
  "member_scope": "denylist",
  "member_overrides": ["usr_01JAG3Z9X8QS3F6K2M4N5P6R7S"],
  "included_member_count": 41
}
```

- Requires `Idempotency-Key`. `source` is always `org_seat` and isn't accepted in the request.
- `order_id`, `starts_at`, and `ends_at` follow the same rules as a [personal grant](../entitlements/#post-v1usersidentitlements). `member_scope` defaults to `all_members`. `member_overrides` must list current members only.
- **Who:** whoever controls access to the *Application*, never the Organization itself. That means platform `entitlements.manage`, the Application's own key with app-confined `entitlements.manage` (the normal path: the developer's backend reflecting a team purchase in their own billing), or the Application's owner. **An org admin can't create a grant for their own Organization.** Organizations are self-service ([`POST /v1/organizations`](#post-v1organizations)), so allowing it would let anyone create an Organization and grant themselves free access to any app. `403 forbidden` otherwise.
- **One live org grant per (Organization, app):** `409 entitlement_already_exists` if an `active`/`disabled` one exists.
- Plan checks apply to the **Application's** Tenant: every newly included member counts as a seat (see [Pricing → Enforcement](../../pricing/#enforcement)).
- Writes `entitlement.granted`. Emits `entitlement.granted`, and `access.granted` per newly included member.
- Change the grant afterwards with [`PATCH /v1/entitlements/{id}`](../entitlements/#patch-v1entitlementsid--the-toggle) (status, term, `member_scope`).

`effective_permissions` for an included member still resolves their own app-scoped Role on top. The org grant only answers the on/off question, same as a personal one would.

## Delegated admin

An org admin is simply a User whose [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) `role` is `org_admin` for that specific Organization. It's not a third [Role](../../domain-model/roles-and-permissions/#role) scope alongside `platform` and `application_id`. This standing lets them manage their own Organization's members, and decide *who within the Organization* an existing org-wide grant reaches (`member_scope`/`member_overrides`) and whether it's switched on (`active` ⇄ `disabled`), without a platform-wide admin Role. They can't create, revoke, re-term, or delete an org grant. Those belong to whoever controls access to the Application (see [`POST …/entitlements`](#post-v1organizationsidentitlements)). It says nothing about their standing in any other Organization, the same independence [AppRole](../../domain-model/roles-and-permissions/#approle) already has across Applications. It's unrelated to infrastructure-level admin (`tenants.manage`), which an org admin never holds. If the Organization owns an Application, its org admins are that Application's owners (see [Applications](../applications/#patch-v1applicationsid)).

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `organization_not_found` | 404 | `{id}` doesn't resolve, or the caller isn't a member. |
| `organization_suspended` | 403 | Any org-admin write on a suspended Organization. |
| `already_member` | 409 | Adding a user who's already a member. |
| `member_not_found` | 404 | `{userId}` isn't a member. |
| `last_org_admin` | 409 | Removing or demoting the last `org_admin`. |
| `member_override_not_a_member` | 422 | A `member_overrides` id isn't a current member. |
| `entitlement_already_exists` | 409 | A live org grant for this app already exists. |
