---
layout: default
title: Applications
parent: Domain Model
nav_order: 2
---

# Applications
{: .no_toc }

1. TOC
{: toc }

---

## What it represents

A catalog entry for one developer's app — built on Substratal Apps for its user/org/tenancy/settings/storage layer. "Product," not "running process": the app itself, as a piece of software, lives and is built elsewhere; this record is how the platform knows it exists, what it's called, where to send a user at launch, and what settings/roles it defines for itself.

Every Application today is built by Substratal itself — `owner_user_id` is a Substratal-internal account either way, so nothing below changes shape when that stops being true. A marketplace where outside developers register and manage their own Applications is an explicit later phase ([Phase 3](../../roadmap/#phase-3)); see [The review lifecycle](#the-review-lifecycle). What *is* already in this entity because of that future — not bolted on afterward — is an owner and a review status, both described below.

## Fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | `app_` plus the `slug`, verbatim (`app_timetrack`). Derived, never chosen, and immutable because `slug` is. One of the two documented exceptions to random ULID ids; see [Conventions → IDs](../../api-reference/conventions/#ids). |
| `slug` | string | Stable, URL-safe identifier matching `^[a-z][a-z0-9-]{1,63}$` (no `_`), immutable once set, unique across live and test mode. Used in the `id`, in permission keys (`app.<slug>.export`), and in Role ids. `platform` is reserved, because Role ids would otherwise collide with platform Roles. |
| `name` | string | Display name. |
| `description` | string | |
| `icon_url` | string | |
| `launch_url` | string | Where the future dashboard sends a user to open the app. |
| `redirect_uris` | array of strings | Exact-match callback URIs for the hosted authorization-code + PKCE flow, up to 10. See [Auth → Getting an app token](../../api-reference/auth/#getting-an-app-token). |
| `support_url`, `email_from_name` | string, nullable | Used in transactional emails sent on this app's behalf. |
| `settings_schema` | object (JSON Schema) | What this app declares its [AppSettings](../settings/) must validate against. See [Settings → Schema validation](../settings/#schema-validation). |
| `permissions` | array of `{key, description}` | The app's own permission catalog. Every key is `app.<slug>.<name>`. This is the only set this app's Roles can grant, and what `GET /v1/permissions` lists for it. |
| `available_app_roles` | array of strings | The role vocabulary this app defines for itself, e.g. `["admin", "editor", "viewer"]`. Each name has exactly one app-scoped [Role](../roles-and-permissions/#approle) (`role_<slug>_<name>`, slug verbatim), seeded automatically when the name is added. The plan's AppRoles limit caps this list's length. |
| `default_app_role` | string, nullable | One of `available_app_roles`. Every user with active access implicitly holds this Role, with no assignment row. This is how "every entitled user is a member" is expressed. |
| `visibility` | enum | `public` (anyone can acquire it) \| `invite_only` \| `internal` (Substratal's own tooling, not end-user-facing). |
| `owner_user_id` | string, nullable | The developer who registered and manages this Application. **Exactly one** of `owner_user_id`/`owner_organization_id` is always set — see the constraint below — so this is nullable only because `owner_organization_id` is the one set instead, never because an Application can be ownerless. A Substratal-run "system" Application still sets this to Substratal's own reserved internal account (`usr_01JAG0SYSTEM00000000000000`, the id used throughout this site's own examples below), not a null owner — [`tenant_id`](#fields) below has to resolve from *something*, and a genuinely ownerless row would have nothing to resolve it from. |
| `owner_organization_id` | string, nullable | Set instead of `owner_user_id` when an [Organization](../users-and-organizations/#organization), not an individual, owns the app. |
| `review_status` | enum | `approved` \| `pending_review` \| `rejected` \| `suspended`. Every Application created today is admin-created and defaults to `approved` — see [below](#who-can-manage-an-applications-catalog-entry). This exists now specifically so self-service submission doesn't need a breaking schema change later. |
| `review_notes` | string, nullable | Required when `review_status` is set to `rejected` or `suspended`; optional on `approved`. The reviewer's reasoning, visible to the Application's owner. |
| `tenant_id` | string | Denormalized from the owner's [Tenant](../tenancy/) at creation — resolves (and creates, at `tier: shared`, if the owner doesn't have one yet) from whichever of `owner_user_id`/`owner_organization_id` is set. Determines where this Application's Entitlements, AppProfile, and AppSettings rows physically live. See [Tenancy](../tenancy/). |
| `settings_schema_stats` | object, read-only | `{"stale_override_counts": {"<key>": <count>}}`: how many users hold an override for each key that no longer validates against the current schema. See [Settings → Example: a schema change makes an override stale](../settings/#example-a-schema-change-makes-an-override-stale). |
| `test_mode` | boolean | Created by a test-mode key. See [Conventions → Authentication](../../api-reference/conventions/#authentication). |
| `created_at`, `updated_at` | timestamp | |

The database row also carries `version`, the source of the resource's `ETag`. The owning Tenant's `tier` and `region` aren't copied onto the Application; read them from [`GET /v1/tenants/{tenant_id}`](../../api-reference/tenancy/#get-v1tenantsid).

**Constraint:** exactly one of `owner_user_id` / `owner_organization_id` is set — never both, never neither, the same pattern [Tenant](../tenancy/#fields) uses for its own owner fields. This is what keeps `tenant_id` always resolvable: there is no case where neither owner column has anything for it to resolve from.

## Example

```json
{
  "id": "app_timetrack",
  "slug": "timetrack",
  "name": "TimeTrack",
  "description": "Time tracking and timesheet export for teams.",
  "icon_url": "https://assets.substratalapps.com/apps/timetrack/icon.png",
  "launch_url": "https://timetrack.substratalapps.com/sso/launch",
  "redirect_uris": ["https://timetrack.substratalapps.com/oauth/callback", "com.substratal.timetrack:/oauth/callback"],
  "support_url": "https://timetrack.substratalapps.com/support",
  "email_from_name": "TimeTrack",
  "settings_schema": {
    "type": "object",
    "properties": {
      "default_billable": { "type": "boolean", "default": true },
      "week_start": { "type": "string", "enum": ["sunday", "monday"], "default": "sunday" }
    }
  },
  "permissions": [
    { "key": "app.timetrack.export", "description": "Export timesheets as CSV/PDF." },
    { "key": "app.timetrack.manage_members", "description": "Add and remove team members inside TimeTrack." }
  ],
  "available_app_roles": ["admin", "member"],
  "default_app_role": "member",
  "visibility": "public",
  "owner_user_id": "usr_01JAG0SYSTEM00000000000000",
  "owner_organization_id": null,
  "review_status": "approved",
  "review_notes": null,
  "tenant_id": "tnt_01JAG1SYSTEM00000000000000",
  "settings_schema_stats": { "stale_override_counts": {} },
  "test_mode": false,
  "created_at": "2025-11-03T00:00:00Z",
  "updated_at": "2026-09-01T00:00:00Z"
}
```

## Who can manage an Application's catalog entry

**Onboarding before Phase 3 is concierge.** Paying external developers exist before self-service registration does. Through Phase 2, Substratal staff create every Application with the paying customer as its owner. From then on, the owner self-serves everything else for their own Application: configuration, app-scoped API Keys, webhooks, reading its Tenant, and their subscription via [Billing](../../api-reference/billing/). For an Organization-owned Application, "owner" means any `org_admin` of the owning Organization.

Two distinct rights, not one:

- **The app's own owner** (`owner_user_id`, or any `org_admin` of `owner_organization_id`; ordinary members can read but not edit) can edit their own Application's configuration (`name`, `description`, `icon_url`, `launch_url`, `redirect_uris`, `settings_schema`, `permissions`, `available_app_roles`, `default_app_role`). The owner can also manage the app's [API Keys](../../api-reference/api-keys/), [webhooks](../../api-reference/webhooks/), Roles and Role assignments, and [Entitlements](../../api-reference/entitlements/) (personal and org-wide), and the owning Tenant's [billing](../../api-reference/billing/), without any platform permission. For Entitlements and Roles, the owner acting with their own User token can do exactly what the app's own key can with app-confined `entitlements.manage`/`roles.manage`, confined to this Application the same way. Through Phase 2, Substratal staff create the Application on the customer's behalf (concierge onboarding), and from then on the owner self-serves.
- **A platform role with `applications.manage`** (Substratal staff) can edit anything, including `visibility` and `review_status` — moderation, not configuration.

Today, with every Application first-party, these two rights are usually held by the same people and the distinction is invisible. It stops being invisible the moment a third-party developer registers their own app — `POST /v1/applications` still requires platform `applications.manage` in the current spec (first-party only, per [The review lifecycle](#the-review-lifecycle)), and self-service registration into a `pending_review` queue is the marketplace-phase feature that `review_status` already models the shape of. See [API Reference → Applications](../../api-reference/applications/) for the endpoint-level detail.

## The review lifecycle

```
pending_review ──approve──► approved ──suspend──► suspended
       │                        ▲                      │
       └──────reject──────┐     └───────reinstate───────┘
                           ▼
                        rejected ──edit + resubmit──► pending_review
```

- **`POST /v1/applications` stays platform-admin-only through the MVP and [Phase 2](../../roadmap/#phase-2).** Self-service registration ships in [Phase 3](../../roadmap/#phase-3). The `owner_user_id`/`owner_organization_id`/`review_status` fields exist now specifically so that it won't need a breaking schema change.
- **No dedicated reviewer assignment** — any platform User holding `applications.manage` can act on anything in the queue (`GET /v1/applications?review_status=pending_review`). At this team's scale a FIFO queue is enough. Auto-assignment waits until submission volume makes the queue genuinely insufficient, which can't be known in advance.
- **A 5-business-day review target** is policy, not a system-enforced SLA: nothing automatically escalates or refunds over a miss. Revisit if real volume makes that insufficient.
- **`rejected` is not deletion.** It means "reviewed, declined," distinct from "never submitted." The record, and the owner's work configuring it, is retained with a required `review_notes`. The owner can edit and resubmit the same Application (`rejected → pending_review`) rather than starting over.
- **`suspended` does not cascade to existing Entitlements.** It removes the Application from the public catalog, blocks new Entitlement grants, and blocks new launch-JWT issuance — but a User who already holds an active Entitlement keeps it, and the suspension alone doesn't force-kill an already-open session. Revoking existing users' access on top of a suspension is a separate, explicit act via [Entitlements](../../api-reference/entitlements/), for the admin who decides it's warranted (an egregious case, say) — not an automatic consequence every suspension carries. The alternative, suspension silently cutting off every existing paying user, is the same problem [Access Control → Organization vs. User precedence](../../access-control/#organization-vs-user-precedence) rules out: a decision about one thing reaching into access an individual holds.
- Every transition into `rejected` or `suspended` requires `review_notes` and produces an [Audit Event](../orders-and-audit/#audit-event) (`application.review_status_changed`).

## Relationship to everything else

An Application is the scope for: [AppRole](../roles-and-permissions/#approle) (what roles it defines), [AppProfile](../profiles/#appprofile) and [AppSettings](../settings/#appsettings) (per-user, per-app data), [Entitlement](../entitlements/) (what a user actually owns), and [Tenant](../tenancy/) (where all of the above physically lives). It doesn't hold any per-user state itself — it's the catalog definition, placed on infrastructure by its owner's Tenant.
