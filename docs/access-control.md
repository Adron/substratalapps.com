---
layout: default
title: Access Control
nav_order: 5
---

# Access Control
{: .no_toc }

1. TOC
{: toc }

---

## The two axes

Every access decision in this system factors into two independent questions, checked in order:

1. **Is the app itself live for this user right now?** → the **Entitlement**.
2. **If it is, what are they allowed to do?** → **Roles**, resolved into **Permissions**.

These are independent on purpose. A support agent can hold a platform Role that lets them *view* every user's entitlements without holding a single entitlement themselves — the ability to administer access is not the same axis as the ability to use an app. Conversely, a user can own an app (active entitlement) but hold no special role inside it beyond the implicit default member role — most users, most of the time.

## The algorithm

```
allow(user, application, permission) :=
    user.status == "active"
    AND resolved_entitlement_status(user, application) == "active"
    AND permission ∈ effective_permissions(user, application)
```

Where:

```
path_active(e) :=
    e.status == "active" AND e.starts_at <= now AND (e.ends_at IS NULL OR e.ends_at > now)

resolved_entitlement_status(user, application) :=
    "active" if path_active(personal_entitlement(user, application))
    "active" if ∃ org ∈ organizations(user) :
                   path_active(org_entitlement(org, application))
                   AND member_included(org_entitlement(org, application), user)
    else the most relevant non-active status among the user's paths, by precedence
         disabled > expired > revoked, or "none" if no path exists at all

member_included(grant, user) :=
    grant.member_scope == "all_members"
    OR (grant.member_scope == "allowlist" AND user ∈ grant.member_overrides)
    OR (grant.member_scope == "denylist"  AND user ∉ grant.member_overrides)

effective_permissions(user, application) :=
    ⋃ permissions(r) for r ∈ app_roles(user, application)
      where app_roles = explicit assignments scoped to application
                      ∪ { application.default_app_role } if set
    — only ever app.<slug>.* keys; empty unless user.status and the entitlement are both active
```

Hub endpoints themselves are authorized by a separate, simpler check, `has_platform_permission(caller, permission)`. That's the union of the permissions on the caller's *platform* Roles, or of an API Key's own `permissions` (confined to its Application for an app-scoped key). Platform permissions never leak into an Application's `effective_permissions`, and app permissions never authorize a hub endpoint.

`resolved_entitlement_status` is a union across every path the user has to this one Application — their own personal Entitlement, plus every Organization they belong to that holds an org-wide grant that includes them — not a single row lookup. See [Step 1](#step-1-entitlement-status) for why this is a union rather than a single answer, and [Organization vs. User precedence](#organization-vs-user-precedence) for how a conflict between an Organization's decision and a User's own standing resolves. `effective_permissions` itself is unaffected by any of this — Role is a separate axis from *how* the entitlement was resolved.

This is the function exposed directly as [`GET /v1/users/{id}/apps/{appId}/effective-permissions`](../api-reference/roles-and-permissions/) — any service, including a downstream app, can ask the hub for the resolved answer instead of re-implementing the union above.

## Step 1: Entitlement status

An [Entitlement](../domain-model/entitlements/) is the join between a User (or Organization) and an Application, carrying a `status`. A given User can have more than one *path* to the same Application at once — their own personal Entitlement, and/or an org-wide grant through any Organization they belong to — so "the entitlement" for a (user, application) pair is really the union of every path, not guaranteed to be a single row. Each individual path still carries one of these statuses:

| Status | Meaning | Can the user reach the app? |
|---|---|---|
| `active` | Owned and switched on. | Yes. |
| `disabled` | Owned, but an admin/support agent turned it off. | No. |
| `expired` | A trial or subscription window lapsed. | No. |
| `revoked` | Ownership itself was removed (refund, chargeback, ToS action). | No. |

"Turn an app on/off for a user" is a write to `status`, nothing more. It does not touch Roles, Profile, or Settings — those are preserved so that re-enabling an app restores the user's exact prior configuration rather than re-provisioning from scratch.

{: .note }
A `disabled` entitlement is a soft, reversible toggle (support can flip it back to `active`). A `revoked` one implies the purchase itself is gone — re-granting access means a new Entitlement, not reinstating the old one.

## Step 2: Effective permissions

**Platform roles** apply everywhere and typically govern the hub itself rather than any one product: `superadmin`, `support`, `billing_admin`, `member` (the default every user gets on signup, granting nothing beyond managing their own account).

**App roles** are scoped to one [Application](../domain-model/applications/). They grant only that app's own `app.<slug>.*` permissions. An app can name one of them its `default_app_role`, which every user with active access holds implicitly, so "every entitled user is a member" needs no assignment rows. App roles are defined by whoever owns that app's catalog entry — one app might define `admin` / `editor` / `viewer`; another might only need `admin` / `member`. The hub stores and enforces the assignment; the app defines the vocabulary.

A user can hold any number of app roles across different apps, and they're independent of each other — being an `admin` of one owned app says nothing about their role in another.

### Worked example

User `usr_01JAG...` has:
- Platform role: `member` (default, no special permissions)
- Entitlement to `app_timetrack`: `active`
- App role on `app_timetrack`: `admin` → grants `app.timetrack.export`, `app.timetrack.manage_members`
- Entitlement to `app_invoicer`: `disabled` (support turned it off after a billing dispute)
- App role on `app_invoicer`: `member` → grants `app.invoicer.view`

Calling `allow(user, app_timetrack, "app.timetrack.export")` → entitlement is `active` and the permission is in the union → **allowed**.

Calling `allow(user, app_invoicer, "app.invoicer.view")` → entitlement is `disabled` → **denied**, regardless of the role held. The role assignment is untouched and will apply again the moment support re-enables the entitlement.

## Organization vs. User precedence

[Organizations](../domain-model/users-and-organizations/#organization) are in scope from [Phase 2](../roadmap/#phase-2) onward. Once a User can belong to an Organization that itself holds an org-wide Entitlement, a real question follows: if the Organization's decision and the User's own standing could point different ways, which wins?

1. **Within one Organization's own grant, the Organization's decision is final.** An org admin's `member_scope` (see [Entitlements → Org-wide entitlements](../domain-model/entitlements/#org-wide-entitlements-scoping-members-in-or-out)) decides who among the org's members actually receives that grant — a member can't opt themselves in or out of it.
2. **A User's own personal Entitlement to the same Application is a separate, untouched path.** Being excluded from one Organization's seat grant never revokes a personal Entitlement held some other way — see the worked example below. This is the one place the rule deliberately departs from a literal "Organization always overrides User." The alternative, an org silently revoking something a member holds individually, creates a real billing and legal defensibility problem ("the company turned off access to something I personally paid for"). The scoped version still meets the actual goal: an org's decision about its own grant is final.
3. **Effective access is the union of every active path**, so a User in multiple Organizations never hits a real conflict between them — each Organization's grant only ever speaks for itself. `resolved_entitlement_status` above *is* this union, formally.
4. **Attribution is always visible**: a User's own entitlements list shows which Organization a given `org_seat` path came from, and whether `member_scope` included or excluded them — never a bare allow/deny with no source. See [Entitlements → Attribution](../domain-model/entitlements/#attribution).

### Worked example: multi-org, one Application

User `usr_jordan` is:
- A member of `org_acme`, which holds an active org-wide Entitlement to `app_invoicer` with `member_scope: all_members` (no exclusions).
- A member of `org_beta`, which holds an active org-wide Entitlement to `app_invoicer` with `member_scope: denylist`, and `usr_jordan` is on that list.
- The holder of their own personal (`source: admin_grant`) Entitlement to `app_timetrack` — unrelated to either Organization.

Resolving `allow(usr_jordan, app_invoicer, "app.invoicer.view")`: `org_beta` excludes them from *its* grant, but that only removes `org_beta`'s path — `org_acme`'s grant still includes them, so `resolved_entitlement_status` is `active` via `org_acme`, and the call is **allowed**. There's no "which Organization wins" conflict to resolve, because `org_beta`'s exclusion was never a global deny — it only ever governed `org_beta`'s own grant.

If `usr_jordan` were *not* a member of `org_acme` at all, the same call would resolve to **denied** — `org_beta`'s exclusion is the only path to `app_invoicer`, and it says no. Their personal Entitlement to `app_timetrack` is unaffected either way; it was never part of either Organization's decision.

## What apps should actually call

Don't re-derive this algorithm inside a downstream app. Three options, see [Trust Model](../trust-model/) for when to use which:

- Decode the JWT issued at login/SSO — carries a pre-resolved `entitlement_status` and `effective_permissions` claim for that one app, cheap but can go stale within its TTL.
- Call `GET /v1/users/{id}/apps/{appId}/effective-permissions` for a live answer.
- Subscribe to the `access.revoked` and `role.removed` [webhooks](../api-reference/webhooks/#event-types) (required) to react immediately rather than poll.
