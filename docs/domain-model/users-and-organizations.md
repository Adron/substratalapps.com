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
| `email` | string | Unique among non-deleted users (case-insensitive, and separately for test-mode users). Carries a separate `email_verified` flag. |
| `email_verified` | boolean | Set by verifying an emailed link, accepting an invitation, or completing a password reset. |
| `pending_email` | string, nullable | A self-service email change awaiting verification of the new address. |
| `status` | enum | `active` \| `invited` \| `suspended` \| `deleted`. |
| `mfa_enabled` | boolean | Read-only, derived: `true` if any of the User's `password` [identities](#useridentity) has confirmed TOTP. |
| `signup_application_id` | string, nullable | The Application the user signed up through, if any. Informational only; used for email branding. |
| `test_mode` | boolean | Created by a test-mode key. |
| `created_at`, `updated_at` | timestamp | |
| `last_login_at` | timestamp, nullable | |

The database row also carries `version` (the source of the `ETag`) and `deleted_at`; see [Database Schema → users](../database-schema/#users).

### Lifecycle

```
            accept invitation / signup / admin activation
 invited ───────────────────────────────────────────► active ◄──── reactivate ────┐
                                                         │                         │
                                                         ├──── suspend ──────► suspended
                                                         │                         │
                                                         ▼                         ▼
                                        deleted (soft: DELETE, or erasure request) ── restore (users.manage) ──► active
                                                         │
                                                         ▼ 7 days after an erasure request
                                              hard-deleted (cascade, `user.erased`)
```

Signup (`POST /v1/auth/signup`) creates a User directly in `active`. An admin invite (`POST /v1/users`) or an org-membership invite by email creates an `invited` one. Suspension and deletion revoke every session and fire `access.revoked` for every Application the user had active access to.

An `invited` user has an account shell (so an Entitlement or Role can be assigned before they've logged in once) but can't authenticate until they complete signup. `suspended` blocks login but preserves every record — Entitlements, Roles, Profile, Settings — unlike `deleted`, which is the start of actual data removal. A soft-deleted User can still be **restored** by `users.manage` (`PATCH status: active`) as long as no erasure request is scheduled and their email hasn't been taken by a new signup; once the erasure cascade completes, there's nothing to restore. See [Users → `PATCH`](../../api-reference/users/#patch-v1usersid). See [Non-Functional Requirements → Data retention](../../non-functional-requirements/#data-retention).

### Example

```json
{
  "id": "usr_01JAG3Z9X8QS3F6K2M4N5P6R7S",
  "email": "jordan@example.com",
  "email_verified": true,
  "pending_email": null,
  "status": "active",
  "mfa_enabled": false,
  "signup_application_id": "app_timetrack",
  "test_mode": false,
  "created_at": "2026-01-14T18:02:11Z",
  "updated_at": "2026-09-20T14:12:00Z",
  "last_login_at": "2026-10-02T09:41:03Z"
}
```

A User's Organization memberships are not a field on this record — see [OrganizationMembership](#organizationmembership) below. A single `organization_id` field would cap a User at one Organization; a User can belong to any number, including across different owners' Applications.

### UserIdentity

How a User actually authenticates isn't a field on `User` either, for the same reason `organization_id` isn't — see [Auth → Native auth and per-Organization SSO](../../api-reference/auth/#native-auth-and-per-organization-sso): a User can hold **more than one** login method at once (email/password *and* a federated SSO connection), picking which to use at each login, so this is its own join, not a column.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `uid_` prefix. |
| `user_id` | string | `references users(id)`. |
| `method` | enum | `password` \| `sso`. |
| `password_hash` | string, nullable | Set only when `method: password`. Native credential, owned directly by this API. |
| `sso_connection_id` | string, nullable | Set only when `method: sso` — `references sso_connections(id)`, see below. |
| `external_subject_id` | string, nullable | The identity provider's own user id for this person, set only when `method: sso`. |
| `mfa_enabled` | boolean | TOTP, opt-in, meaningful only alongside `method: password` — an SSO connection's own MFA policy is that provider's concern, not re-implemented here. |
| `last_used_at` | timestamp, nullable | Which method a User actually logs in with, in practice — not just which they've set up. |

**One User, multiple UserIdentity rows** is the normal case, not an edge case — the same person might hold a `password` identity *and* an `sso` identity through their employer's Organization, choosing either at login. [`POST /v1/auth/login`](../../api-reference/auth/) accepts whichever credential matches an existing row; there's no "primary" method to designate.

### Session

One successful login. Refresh tokens, app refresh tokens, and app-token `sid` claims all point at it, so revoking it ends everything that came from that login at once. See [Auth → Sessions](../../api-reference/auth/#sessions).

| Field | Type | Notes |
|---|---|---|
| `id` | string | `ses_` prefix. Appears as `sid` in every token minted from it. |
| `user_id` | string | |
| `amr` | array | How the login was authenticated: `pwd`, `otp`, `recovery_code`, `sso`. |
| `ip_address`, `user_agent` | string | Recorded at login. They're personal data: included in export, deleted by erasure. |
| `created_at`, `last_seen_at` | timestamp | `last_seen_at` is updated at most once a minute. |
| `idle_expires_at`, `absolute_expires_at` | timestamp | 30 days from last refresh; 90 days from creation. |
| `revoked_at`, `revoked_reason` | timestamp/string, nullable | `logout`, `logout_all`, `password_reset`, `password_changed`, `user_suspended`, `user_deleted`, `mfa_reset`, `refresh_token_reused`, or `admin`. |

### SSOConnection

The other half of [native auth and per-Organization SSO](../../api-reference/auth/#native-auth-and-per-organization-sso): SSO is configured **per-Organization**, not per-Application or platform-wide — an Organization admin connects their own company's identity provider (Okta, Azure AD, Google Workspace, …) once, and it becomes available to every member of that Organization.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `ssc_` prefix. |
| `organization_id` | string | `references organizations(id)` — the Organization this connection belongs to. |
| `provider` | string | The underlying federation service, e.g. `"workos"`. A federation broker, rather than integrating each enterprise IdP directly, because "bring your own enterprise IdP" is exactly the problem a broker solves; see [Auth](../../api-reference/auth/#native-auth-and-per-organization-sso). |
| `domain` | string, nullable | An email domain (e.g. `acme.com`) this connection auto-applies to, so a new member with a matching email can be routed to the right connection without manual setup. A domain can be claimed by at most one `active` connection across the whole platform, not just per Organization, so routing a login by email domain is never ambiguous. Claiming one will require proving control of it (a DNS TXT record), specified with the broker integration. |
| `status` | enum | `active` \| `inactive`. |

{: .note }
This table is deliberately thin — scoped to *that* a User can authenticate via their Organization's SSO, not *how* the broker integration works, which is explicitly deferred (see [Auth → Native auth and per-Organization SSO](../../api-reference/auth/#native-auth-and-per-organization-sso)). **There is no API for SSOConnection yet**: no endpoint creates, lists, or changes one. Its management endpoints (under `/v1/organizations/{id}/sso-connections`, org-admin owned) ship with the broker integration, alongside the fields and domain-verification flow they need. Until then, the table exists so the schema doesn't need a breaking migration later.

---

## Organization

A domain/grouping object for Users — a company, or a group within a company (a department, a team) — used to organize who shares admin standing over a set of Users and which Applications a group is granted as a whole, rather than one admin re-granting access per person.

Both individual and team/company end users are expected, so this isn't a someday-maybe feature. It's pulled forward to [Phase 2](../../roadmap/#phase-2) rather than [Phase 3](../../roadmap/#phase-3). It isn't in the MVP itself, because the first few dozen users are expected to be mostly individual early adopters. But it's needed well before the 10x/100x growth horizon in [Deployment Architecture → Growth trajectory](https://github.com/CompositeCode/substratalapps.com/blob/main/DEPLOYMENT.md), where team accounts are assumed to matter. Making `organization_id` load-bearing in the schema from day one (see [below](#why-it-matters-even-before-organizations-are-customer-facing-everywhere)) was never a hedge against a hypothetical.

{: .note }
Organization carries no infrastructure meaning. Where a group's data physically lives is [Tenant](../tenancy/)'s job, not this entity's — see [Tenancy → Tenant vs. Organization](../tenancy/#tenant-vs-organization) for why these are deliberately separate. An Organization can incidentally *own* a Tenant (if it also owns an Application — see [Applications](../applications/#fields)), but most Organizations, most of the time, don't own one at all.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `org_` prefix. |
| `name` | string | |
| `status` | enum | `active` \| `suspended`. Only `organizations.manage` can suspend. Suspension freezes the Organization's own admin actions but doesn't cut members' access (non-cascading). |
| `created_by` | string | The User who created it. Any active User may create an Organization and becomes its first `org_admin`. |
| `test_mode` | boolean | |
| `created_at`, `updated_at` | timestamp | |

### OrganizationMembership

The join record between a User and an Organization — a User can belong to any number of Organizations at once, including ones that own Applications in different [Tenants](../tenancy/).

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | |
| `organization_id` | string | |
| `status` | enum | `pending` \| `active`. A membership added by email starts `pending` and becomes `active` when the person accepts. Only an `active` membership counts for anything: org-wide grants, `org_admin` standing, `member_overrides`. See [Organizations → Adding members](../../api-reference/organizations/#post-v1organizationsidmembers). |
| `role` | enum | `org_admin` \| `member`. Standing *within this one Organization* — independent of any [PlatformRole](../roles-and-permissions/#platformrole) or [AppRole](../roles-and-permissions/#approle) the same User holds. Being `org_admin` of one Organization says nothing about standing in another, the same independence pattern [AppRole](../roles-and-permissions/#approle) already uses across Applications. |
| `invited_by` | string, nullable | The User who added this membership by email. `null` for a direct add by `organizations.manage` and for the creator. |
| `invited_at` | timestamp | When the membership row was created. |
| `joined_at` | timestamp, nullable | When it became `active`. `null` while `pending`. |

`org_admin` is what [API Reference → Organizations → Delegated admin](../../api-reference/organizations/#delegated-admin) resolves to — an org admin is simply a User whose `OrganizationMembership.role` is `org_admin` for that specific Organization, not a third Role scope alongside `platform` and `application_id`.

### Why it matters even before Organizations are customer-facing everywhere

[Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy) recommends every end-user-scoped table carry `organization_id` from the MVP onward, enforced at the data-access layer, specifically so Organization features can land without a backfill migration across every table that should have been scoped from day one. This `organization_id` (team/seat grouping) is a different axis from the `tenant_id` introduced in [Tenancy](../tenancy/) (infrastructure placement) — see [Tenancy → Tenant vs. Organization](../tenancy/#tenant-vs-organization). Don't conflate the two when reading [Database Schema](../database-schema/).

### Relationship to Entitlements

An Entitlement can belong to a User directly, or to an Organization (an "org seat") — see [`Entitlement.source: org_seat`](../entitlements/). Org-wide entitlements are how a team plan grants every current and future member access to an app without an admin re-granting it per seat, and how an org admin can include or exclude specific members from that grant — see [Entitlements → Org-wide entitlements: scoping members in or out](../entitlements/#org-wide-entitlements-scoping-members-in-or-out) for the precedence rules when an Organization's decision and a User's own standing could otherwise conflict.
