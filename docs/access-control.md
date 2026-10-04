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
    entitlement(user, application).status == "active"
    AND permission ∈ effective_permissions(user, application)
```

Where:

```
effective_permissions(user, application) :=
    permissions(platform_roles(user))
    ∪ permissions(app_roles(user, application))
    ∪ org_override_permissions(organization(user), application)   # if orgs are in scope, see §Org overrides below
```

This is the function exposed directly as [`GET /v1/users/{id}/applications/{appId}/effective-permissions`](../api-reference/roles-and-permissions/) — any service, including a downstream app, can ask the hub for the resolved answer instead of re-implementing the union above.

## Step 1: Entitlement status

An [Entitlement](../domain-model/entitlements/) is the join between a User (or Organization) and an Application, carrying a `status`:

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

**App roles** are scoped to one [Application](../domain-model/applications/) and defined by whoever owns that app's catalog entry — one app might define `admin` / `editor` / `viewer`; another might only need `admin` / `member`. The hub stores and enforces the assignment; the app defines the vocabulary.

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

## Org overrides

If [Organizations](../domain-model/users-and-organizations/) are in scope (see [Decisions](../decisions/)), an org admin's role can widen or narrow what members inherit by default — e.g. an org-level `app.invoicer.view` grant applied to every seat, independent of each member's individual app role. This layer is additive to, not a replacement for, the per-user roles above; the full union is what `effective_permissions` returns.

## What apps should actually call

Don't re-derive this algorithm inside a downstream app. Three options, see [Trust Model](../trust-model/) for when to use which:

- Decode the JWT issued at login/SSO — carries a pre-resolved `entitlement_status` and `effective_permissions` claim for that one app, cheap but can go stale within its TTL.
- Call `GET /v1/users/{id}/applications/{appId}/effective-permissions` for a live answer.
- Subscribe to the `entitlement.*` and `role.*` [webhooks](../api-reference/webhooks/) to react immediately rather than poll.
