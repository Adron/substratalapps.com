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
| `organization_id` | string, nullable | Set if the user belongs to an [Organization](#organization). |
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
  "organization_id": null,
  "created_at": "2026-01-14T18:02:11Z",
  "last_login_at": "2026-10-02T09:41:03Z"
}
```

---

## Organization

{: .decision }
Optional in the current draft. See [Decisions → Organizations](../../decisions/#2-organizations) before treating this as settled.

A billing/access group of Users — e.g. a team plan where seats share entitlements and an org admin manages membership. If Organizations are out of scope, every User is implicitly its own org of one, and `organization_id` stays `null` everywhere.

| Field | Type | Notes |
|---|---|---|
| `id` | string | `org_` prefix. |
| `name` | string | |
| `status` | enum | `active` \| `suspended`. |
| `created_at` | timestamp | |

### Why it matters even before it ships

[Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy) recommends every scoped table carry `organization_id` from the MVP onward, enforced at the data-access layer, specifically so Organizations can land in [Phase 3](../../roadmap/#phase-3) without a backfill migration across every table that should have been scoped from day one.

### Relationship to Entitlements

An Entitlement can belong to a User directly, or to an Organization (an "org seat") — see [`Entitlement.source: org_seat`](../entitlements/). Org-wide entitlements are how a team plan grants every current and future member access to an app without an admin re-granting it per seat.
