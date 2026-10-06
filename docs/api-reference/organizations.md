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
| `GET` | `/v1/organizations/{id}` | active member, or `organizations.manage` | Fetch one. |
| `PATCH` | `/v1/organizations/{id}` | org admin (`name`), `organizations.manage` (`name`, `status`) | Rename, or suspend/reactivate. |
| `GET` | `/v1/organizations/{id}/members` | active member, or `organizations.manage` | List members. |
| `POST` | `/v1/organizations/{id}/members` | org admin (by `email`), or `organizations.manage` (by `email` or `user_id`) | Invite a member. |
| `POST` | `/v1/organizations/{id}/members/me/accept` | the invited User | Accept a pending membership. |
| `PATCH` | `/v1/organizations/{id}/members/{userId}` | org admin, or `organizations.manage` | Change a member's `role`. |
| `DELETE` | `/v1/organizations/{id}/members/{userId}` | org admin, `organizations.manage`, or the member themselves (`me`) | Remove a member, withdraw an invitation, leave, or decline. |
| `GET` | `/v1/organizations/{id}/entitlements` | active member, or `organizations.manage` | List org-wide ("seat") grants. |
| `POST` | `/v1/organizations/{id}/entitlements` | `entitlements.manage` (platform, or the Application's own confined key), or the Application's owner | Grant an app to the whole Organization. |

## The Organization object

```json
{
  "id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P",
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
    { "id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P", "name": "Acme Co.", "status": "active", "member_count": 42, "my_role": "org_admin", "my_membership_status": "active", "created_at": "2026-10-04T09:00:00Z" },
    { "id": "org_01JAGR5S6T7V8W9X0Y1Z2A3B4C", "name": "Globex Design Team", "my_role": "member", "my_membership_status": "pending" }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

A non-admin caller sees every Organization they hold an [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) in. A User can belong to more than one. `my_role` is that caller's standing in each, and `my_membership_status` is `active` or `pending`. A `pending` row is an invitation waiting for the caller to accept, and shows only `id`, `name`, `my_role`, and `my_membership_status`. `member_count` counts active members only. A `organizations.manage` holder sees every Organization (with no `my_role`) and can filter with `?status=` and `?q=` (a name prefix).

## `POST /v1/organizations`

Self-service creation (any active User, rate-limited) is what makes team end users from [Phase 2](../../roadmap/#phase-2) workable, rather than a Substratal staff member creating every team. **The guard that makes it safe:** an org admin can never *create* an org-wide grant for their own Organization. Only whoever controls the Application can. Without that rule, anyone could create an Organization and grant themselves free access to any app.

```json
// Request
{ "name": "Acme Co." }
```
```json
// Response — 201
{ "id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P", "name": "Acme Co.", "status": "active", "member_count": 1, "created_by": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "test_mode": false, "created_at": "2026-10-04T09:00:00Z", "updated_at": "2026-10-04T09:00:00Z" }
```

Any active User may create one, with a limit of 10 per User per day (`429`). The caller becomes the first member, with `role: org_admin`, in the same transaction. A `name` is 1–100 characters and doesn't have to be unique. Must be called with a User token, not an API Key (`400 user_token_required`). Writes `organization.created`.

## `GET /v1/organizations/{id}`

Returns the Organization object. Active members and `organizations.manage` only; anyone else, including someone with only a `pending` invitation, gets `404 organization_not_found`.

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
// Response — 200, the full updated Organization (here, after the suspend)
{ "id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P", "name": "Acme Co.", "status": "suspended", "member_count": 42, "created_by": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "test_mode": false, "created_at": "2026-10-04T09:00:00Z", "updated_at": "2026-10-06T10:00:00Z" }
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
    { "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "email": "jordan@example.com", "display_name": "Jordan Alvarez", "membership_status": "active", "role": "org_admin", "invited_at": "2026-10-04T09:00:00Z", "joined_at": "2026-10-04T09:05:00Z" },
    { "user_id": "usr_01JAGK718M9N0P1Q2R3S4T5V6V", "email": "sam@acme.example", "display_name": null, "membership_status": "pending", "role": "member", "invited_at": "2026-10-05T08:00:00Z", "joined_at": null }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

Active members and `organizations.manage`. Filters: `?role=org_admin|member`, `?membership_status=pending|active`, and `?q=` (a prefix of email or name). The member object deliberately says nothing about the person's *account*: `membership_status` is the membership's own state, and `display_name` is `null` until the membership is `active`. An org admin can't use this list to learn whether an email had an account before they invited it, or what that account's name is.

## `POST /v1/organizations/{id}/members`

```json
// Request — an org admin inviting by email
{ "email": "sam@acme.example", "role": "member" }
```
```json
// Request — organizations.manage adding a known User directly
{ "user_id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S", "role": "member" }
```
```json
// Response — 201 (the email invite)
{ "user_id": "usr_01JAGK718M9N0P1Q2R3S4T5V6V", "email": "sam@acme.example", "display_name": null, "membership_status": "pending", "role": "member", "invited_at": "2026-10-05T08:00:00Z", "joined_at": null }
```

- Exactly one of `user_id` or `email` is required. `role` defaults to `member`.
- **By `email`** (org admins and `organizations.manage`): creates a `pending` membership and emails the address an invitation naming the Organization. If no live User has that email, an `invited` User is created first, and the email is the account invitation (see [Auth → Invitations](../auth/#invitations)). This is the one way a non-platform caller can cause a User to be created. **The response is the same shape either way**, `membership_status: "pending"` and `display_name: null`, so inviting an address never reveals whether it already had an account. Because any User can create an Organization, anything else would let anyone test arbitrary emails for accounts.
- **By `user_id`** (`organizations.manage` only, `403 forbidden` otherwise): the membership is `active` immediately. Ids aren't something an org admin can look up, and accepting on someone's behalf is a platform action.
- A `pending` membership grants nothing: no org-wide grant reaches the person, they're not counted as a seat, and they can't see the Organization. It becomes `active` when they accept, with [`POST …/members/me/accept`](#post-v1organizationsidmembersmeaccept) if they already have an account, or by accepting the account invitation if they don't. An unaccepted invitation never expires on its own; an org admin can withdraw it with `DELETE`.
- Writes `organization.member_invited`. `organization.member_added` (audit and webhook) and any `access.granted` events fire later, on acceptance.
- **Seats:** adding a member never fails on a plan limit, because the membership isn't usage. When it becomes active, every `all_members`/`denylist` org grant reaches the new member, and each one counts as a seat on that Application's Tenant. If a Starter Tenant is at its seat cap, the member still joins, but their `member_decision` on that grant reads `seat_limit` and they get no access through it until a seat frees up or the plan is upgraded. See [Pricing → Enforcement](../../pricing/#enforcement).

## `POST /v1/organizations/{id}/members/me/accept`

```json
// Request
{}
```
```json
// Response — 200, the member object, now active
{ "user_id": "usr_01JAGK718M9N0P1Q2R3S4T5V6V", "email": "sam@acme.example", "display_name": "Sam Rivera", "membership_status": "active", "role": "member", "invited_at": "2026-10-05T08:00:00Z", "joined_at": "2026-10-05T08:30:00Z" }
```

Called with the invited User's own token (`400 user_token_required` for an API Key). `404 member_not_found` if the caller has no pending membership in this Organization; accepting an already-active membership returns `200` with no change. On acceptance the new member falls under every `all_members`/`denylist` org grant, `access.granted` fires for each Application they newly gain, and `organization.member_added` is written and emitted. To decline instead, the User calls `DELETE /v1/organizations/{id}/members/me`.

## `PATCH /v1/organizations/{id}/members/{userId}`

```json
// Request
{ "role": "org_admin" }
```
```json
// Response — 200, the member object
{ "user_id": "usr_01JAGK718M9N0P1Q2R3S4T5V6V", "email": "sam@acme.example", "display_name": "Sam Rivera", "membership_status": "active", "role": "org_admin", "invited_at": "2026-10-05T08:00:00Z", "joined_at": "2026-10-05T08:30:00Z" }
```

Demoting the Organization's last active `org_admin` returns `409 last_org_admin`, because an Organization always has at least one. A `pending` member's role can be changed too; it takes effect when they accept. Writes `organization.member_role_changed`.

## `DELETE /v1/organizations/{id}/members/{userId}`

```json
// Response — 204
```

- An org admin or `organizations.manage` can remove anyone, including withdrawing a `pending` invitation. Any member can remove themselves (`/members/me`), which for a `pending` membership means declining it. Removing a `pending` membership fires no `access.*` events and writes `organization.member_removed` only to the audit log.
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
      "id": "ent_01JAGB2C3D4E5F6G7H8J9K011M",
      "user_id": null,
      "organization_id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P",
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

`included_member_count` is computed: how many active members this grant reaches (excluded and `seat_limit` members don't count) after `member_scope`. Same filters as `GET /v1/entitlements`.

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
  "id": "ent_01JAGB2C3D4E5F6G7H8J9K011M",
  "user_id": null,
  "organization_id": "org_01JAFZ8Y7X6W5V4V3T2S1R0Q9P",
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
- `order_id`, `starts_at`, and `ends_at` follow the same rules as a [personal grant](../entitlements/#post-v1usersidentitlements). `member_scope` defaults to `all_members`. `member_overrides` must list active members only.
- **Who:** whoever controls access to the *Application*, never the Organization itself. That means platform `entitlements.manage`, the Application's own key with app-confined `entitlements.manage` (the normal path: the developer's backend reflecting a team purchase in their own billing), or the Application's owner. **An org admin can't create a grant for their own Organization.** Organizations are self-service ([`POST /v1/organizations`](#post-v1organizations)), so allowing it would let anyone create an Organization and grant themselves free access to any app. `403 forbidden` otherwise.
- **One live org grant per (Organization, app):** `409 entitlement_already_exists` if an `active`/`disabled` one exists.
- Plan checks apply to the **Application's** Tenant: every newly included member counts as a seat (see [Pricing → Enforcement](../../pricing/#enforcement)).
- Writes `entitlement.granted`. Emits `entitlement.granted`, and `access.granted` per newly included member.
- Change the grant afterwards with [`PATCH /v1/entitlements/{id}`](../entitlements/#patch-v1entitlementsid--the-toggle) (status, term, `member_scope`).

`effective_permissions` for an included member still resolves their own app-scoped Role on top. The org grant only answers the on/off question, same as a personal one would.

## Delegated admin

An org admin is simply a User whose active [OrganizationMembership](../../domain-model/users-and-organizations/#organizationmembership) has `role: org_admin` for that specific Organization (a `pending` invitation as `org_admin` confers nothing until accepted). It's not a third [Role](../../domain-model/roles-and-permissions/#role) scope alongside `platform` and `application_id`. This standing lets them manage their own Organization's members, and decide *who within the Organization* an existing org-wide grant reaches (`member_scope`/`member_overrides`) and whether it's switched on (`active` ⇄ `disabled`), without a platform-wide admin Role. They can't create, revoke, re-term, or delete an org grant. Those belong to whoever controls access to the Application (see [`POST …/entitlements`](#post-v1organizationsidentitlements)). It says nothing about their standing in any other Organization, the same independence [AppRole](../../domain-model/roles-and-permissions/#approle) already has across Applications. It's unrelated to infrastructure-level admin (`tenants.manage`), which an org admin never holds. If the Organization owns an Application, its org admins are that Application's owners (see [Applications](../applications/#patch-v1applicationsid)).

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `organization_not_found` | 404 | `{id}` doesn't resolve, or the caller isn't a member. |
| `organization_suspended` | 403 | Any org-admin write on a suspended Organization, and accepting an invitation to one. |
| `forbidden` | 403 | An org admin adding by `user_id`, or an org admin creating an org-wide grant. |
| `user_not_found` | 404 | `organizations.manage` added a `user_id` that doesn't resolve. |
| `already_member` | 409 | Inviting or adding a user who's already a member, `pending` or `active`. |
| `member_not_found` | 404 | `{userId}` isn't a member. |
| `last_org_admin` | 409 | Removing or demoting the last `org_admin`. |
| `member_override_not_a_member` | 422 | A `member_overrides` id isn't an active member. |
| `entitlement_already_exists` | 409 | A live org grant for this app already exists. |
