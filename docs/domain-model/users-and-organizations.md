---
layout: default
title: Users & Organizations
parent: Domain Model
nav_order: 1
---

# Users & Organizations
{: .no_toc }

1. TOC
{: toc }

---

## User

A person with an account on Substratal. One identity, used everywhere in the hub and, via the [Trust Model](../../trust-model/), in every app they launch from it.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `usr_` prefix, opaque, stable. |
| `email` | string | Unique. Carries a separate `email_verified` flag. |
| `status` | enum | `active` \| `invited` \| `suspended` \| `deleted`. |
| `auth` | object | Password hash or SSO subject + MFA state. Likely delegated to an external identity provider rather than owned here — see [Decisions](../../decisions/#1-identity-provider). |
| `created_at` | timestamp | |
| `last_login_at` | timestamp, nullable | |

### Lifecycle

```
invited → active → suspended ⇄ active
                 ↘ deleted (soft) → hard-deleted (right-to-erasure, separate process)
```

An `invited` user has an account shell (so an Entitlement or Role can be assigned before they've logged in once) but can't authenticate until they complete signup. `suspended` blocks login but preserves every record — Entitlements, Roles, Profile, Settings — unlike `deleted`, which is the start of actual data removal. See [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention).

### Example

```json
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": true,
  "status": "active",
  "created_at": "2026-01-14T18:02:11Z",
  "last_login_at": "2026-10-02T09:41:03Z"
}
```

A User's Organization memberships are not a field on this record — see [OrganizationMembership](#organizationmembership) below. A single `organization_id` field would cap a User at one Organization; a User can belong to any number, including across different owners' Applications.

---

## Organization

A domain/grouping object for Users — a company, or a group within a company (a department, a team) — used to organize who shares admin standing over a set of Users and which Applications a group is granted as a whole, rather than one admin re-granting access per person. See [Decisions → Organizations](../../decisions/#2-organizations) — both individual and team/company end users are expected, so this isn't a someday-maybe feature; it's pulled into [Phase 2](../../roadmap/#phase-2).

{: .note }
Organization carries no infrastructure meaning. Where a group's data physically lives is [Tenant](../tenancy/)'s job, not this entity's — see [Decisions → Tenant vs. Organization](../../decisions/#11-tenant-vs-organization) for why these are deliberately separate. An Organization can incidentally *own* a Tenant (if it also owns an Application — see [Applications](../applications/#fields)), but most Organizations, most of the time, don't own one at all.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `org_` prefix. |
| `name` | string | |
| `status` | enum | `active` \| `suspended`. |
| `created_at` | timestamp | |

### OrganizationMembership

The join record between a User and an Organization — a User can belong to any number of Organizations at once, including ones that own Applications in different [Tenants](../tenancy/).

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `organization_id` | string | |
| `role` | enum | `org_admin` \| `member`. Standing *within this one Organization* — independent of any [PlatformRole](../roles-and-permissions/#platformrole) or [AppRole](../roles-and-permissions/#approle) the same User holds. Being `org_admin` of one Organization says nothing about standing in another, the same independence pattern [AppRole](../roles-and-permissions/#approle) already uses across Applications. |
| `joined_at` | timestamp | |

`org_admin` is what [API Reference → Organizations → Delegated admin](../../api-reference/organizations/#delegated-admin) resolves to — an org admin is simply a User whose `OrganizationMembership.role` is `org_admin` for that specific Organization, not a third Role scope alongside `platform` and `application_id`.

### Why it matters even before Organizations are customer-facing everywhere

[Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy) recommends every end-user-scoped table carry `organization_id` from the MVP onward, enforced at the data-access layer, specifically so Organization features can land without a backfill migration across every table that should have been scoped from day one. This `organization_id` (team/seat grouping) is a different axis from the `tenant_id` introduced in [Tenancy](../tenancy/) (infrastructure placement) — see [Decisions → Tenant vs. Organization](../../decisions/#11-tenant-vs-organization).

### Relationship to Entitlements

An Entitlement can belong to a User directly, or to an Organization (an "org seat") — see [`Entitlement.source: org_seat`](../entitlements/). Org-wide entitlements are how a team plan grants every current and future member access to an app without an admin re-granting it per seat, and how an org admin can include or exclude specific members from that grant — see [Entitlements → Org-wide entitlements: scoping members in or out](../entitlements/#org-wide-entitlements-scoping-members-in-or-out) for the precedence rules when an Organization's decision and a User's own standing could otherwise conflict.
