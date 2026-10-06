---
layout: default
title: Applications
parent: API Reference
nav_order: 6
---

# Applications
{: .no_toc }

See [Domain Model → Applications](../../domain-model/applications/) for the full field reference.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Endpoints

| Method | Path | Requires | Purpose |
|---|---|---|---|
| `GET` | `/v1/applications` | any authenticated caller | List the catalog, filtered by visibility rules. |
| `GET` | `/v1/applications/{id}` | any caller who can see it | Fetch one Application. |
| `POST` | `/v1/applications` | `applications.manage` | Register a new Application. Platform-admin only through Phase 2. |
| `PATCH` | `/v1/applications/{id}` | the owner (configuration fields) or `applications.manage` (anything) | Update a catalog entry. |

## Who can see an Application

`visibility` decides who gets an Application from `GET /v1/applications` and `GET /v1/applications/{id}`. A caller who can't see one gets `404 application_not_found` on a direct fetch, so its existence isn't leaked.

| `visibility` | Listed / fetchable by | Who can grant an Entitlement to it |
|---|---|---|
| `public` | Every authenticated caller, as long as `review_status: approved`. | Platform `entitlements.manage`, the app's own confined key, or the app's owner. |
| `invite_only` | Users with any access path to it, its owner, its own app key, and `applications.manage`. Access paths come from grants made by the parties in the next column, so nobody needs to *see* the app before being granted it. | Same as `public`. |
| `internal` | Only `applications.manage` holders and its own app key. | Platform `entitlements.manage` only. |

Applications that aren't `approved` are visible only to their owner, their own key, and `applications.manage`, whatever their `visibility`.

## The Application object

```json
{
  "id": "app_timetrack",
  "slug": "timetrack",
  "name": "TimeTrack",
  "description": "Time tracking and timesheet export for teams.",
  "icon_url": "https://assets.substratalapps.com/apps/timetrack/icon.png",
  "launch_url": "https://timetrack.substratalapps.com/sso/launch",
  "redirect_uris": [
    "https://timetrack.substratalapps.com/oauth/callback",
    "com.substratal.timetrack:/oauth/callback"
  ],
  "support_url": "https://timetrack.substratalapps.com/support",
  "email_from_name": "TimeTrack",
  "settings_schema": {
    "type": "object",
    "properties": {
      "default_billable": { "type": "boolean", "default": true },
      "week_start": { "type": "string", "enum": ["sunday", "monday"], "default": "sunday" },
      "invoice_footer": { "type": "string", "maxLength": 500, "x-pii": true }
    }
  },
  "settings_schema_stats": { "stale_override_counts": {} },
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
  "test_mode": false,
  "created_at": "2025-11-03T00:00:00Z",
  "updated_at": "2026-09-01T00:00:00Z"
}
```

| Field | Rules |
|---|---|
| `id` | Read-only and derived: `app_` plus `slug` (see [Conventions → IDs](../conventions/#ids)). |
| `slug` | Matches `^[a-z][a-z0-9-]{1,63}$` (no `_`), isn't `platform` (reserved), is unique across live and test mode, and is **immutable** once set. It's embedded in the `id`, permission keys (`app.<slug>.*`), and Role ids (`role_<slug>_<name>`, verbatim). |
| `name` | 1–100 characters. |
| `launch_url` | `https` URL. Where the future dashboard sends a user to open the app. |
| `redirect_uris` | 0–10 entries, each an exact-match string: an `https://` URL, or a private-use URI scheme for native apps (reverse-DNS, `com.example.app:/path`). `http://localhost` and `http://127.0.0.1` with any port are allowed for development only on a `test_mode` Application. Required (non-empty) before the hosted authorization-code flow will issue a code — see [Auth](../auth/#post-v1authoauthauthorization-codes). |
| `support_url`, `email_from_name` | Optional. Used in transactional emails sent on behalf of this app. `email_from_name` is 1–50 characters; the sending address is always Substratal's own. |
| `settings_schema` | See [Domain Model → Settings → `settings_schema` rules](../../domain-model/settings/#settings_schema-rules). |
| `settings_schema_stats` | Read-only. `stale_override_counts` is a key → count map of how many users hold overrides that no longer validate. |
| `permissions` | The Application's own permission catalog, 0–100 entries. Each `key` must start with `app.<slug>.`, then match `[a-z0-9_.]{1,64}`. `description` is up to 255 characters. This is what `GET /v1/permissions` lists, and it's the only set an AppRole may draw from. |
| `available_app_roles` | The role-name vocabulary: 0–20 names, each matching `^[a-z][a-z0-9_]{1,40}$`. Every app-scoped [Role](../roles-and-permissions/) for this Application must use one of these names. |
| `default_app_role` | `null`, or one of `available_app_roles`. If set, every user with active access to the app **implicitly** holds the app-scoped Role with that name (`role_<slug>_<name>`, always present because it's seeded with the name), without an assignment row. |
| `tenant_id` | Read-only. Resolved from the owner's Tenant at creation, and the Tenant is created if needed. |

## `GET /v1/applications`

```json
// Response — 200
{
  "data": [
    {
      "id": "app_timetrack",
      "slug": "timetrack",
      "name": "TimeTrack",
      "description": "Time tracking and timesheet export for teams.",
      "icon_url": "https://assets.substratalapps.com/apps/timetrack/icon.png",
      "visibility": "public",
      "review_status": "approved"
    }
  ],
  "page": { "next_cursor": null, "has_more": false }
}
```

List responses return a trimmed view. They leave out `settings_schema`, `permissions`, `redirect_uris`, and owner fields, so fetch the single resource for the full object.

| Filter | Who may use it |
|---|---|
| `visibility` | Anyone, applied within what they can already see. |
| `review_status` | Anyone, but non-admins only ever see `approved` apps plus their own. `?review_status=pending_review` with `applications.manage` is the **review queue**, oldest first. No separate queue resource exists. |
| `owned=true` | Only Applications the caller owns (directly, or as `org_admin` of the owning Organization). |
| `tenant_id`, `owner_user_id`, `owner_organization_id` | `applications.manage` or `tenants.manage`. |

## `GET /v1/applications/{id}`

Returns the full Application object above.

## `POST /v1/applications`

```json
// Request
{
  "slug": "invoicer",
  "name": "Invoicer",
  "description": "Send and track invoices.",
  "launch_url": "https://invoicer.substratalapps.com/sso/launch",
  "redirect_uris": ["https://invoicer.substratalapps.com/oauth/callback"],
  "settings_schema": { "type": "object", "properties": { "currency": { "type": "string", "enum": ["USD", "EUR"], "default": "USD" } } },
  "permissions": [ { "key": "app.invoicer.view", "description": "View invoices." }, { "key": "app.invoicer.send", "description": "Send invoices." } ],
  "available_app_roles": ["admin", "member"],
  "default_app_role": "member",
  "visibility": "public",
  "owner_user_id": "usr_01JAG0SYSTEM00000000000000"
}
```
```json
// Response — 201
{
  "id": "app_invoicer",
  "slug": "invoicer",
  "name": "Invoicer",
  "description": "Send and track invoices.",
  "icon_url": null,
  "launch_url": "https://invoicer.substratalapps.com/sso/launch",
  "redirect_uris": ["https://invoicer.substratalapps.com/oauth/callback"],
  "support_url": null,
  "email_from_name": null,
  "settings_schema": { "type": "object", "properties": { "currency": { "type": "string", "enum": ["USD", "EUR"], "default": "USD" } } },
  "settings_schema_stats": { "stale_override_counts": {} },
  "permissions": [ { "key": "app.invoicer.view", "description": "View invoices." }, { "key": "app.invoicer.send", "description": "Send invoices." } ],
  "available_app_roles": ["admin", "member"],
  "default_app_role": "member",
  "visibility": "public",
  "owner_user_id": "usr_01JAG0SYSTEM00000000000000",
  "owner_organization_id": null,
  "review_status": "approved",
  "review_notes": null,
  "tenant_id": "tnt_01JAG1SYSTEM00000000000000",
  "test_mode": false,
  "created_at": "2026-10-05T12:00:00Z",
  "updated_at": "2026-10-05T12:00:00Z"
}
```

The Roles `role_invoicer_admin` and `role_invoicer_member` now exist with no permissions.

- Requires `applications.manage`. Through Phase 2 this is how **every** Application comes to exist, including a paying customer's. Staff create it with the customer as owner, and the owner manages it from then on ("concierge onboarding").
- `slug`, `name`, `launch_url`, and exactly one of `owner_user_id`/`owner_organization_id` are required. Everything else is optional.
- Resolves the owner's [Tenant](../tenancy/), creating one if needed. A new Tenant gets `tier: shared`, and `plan: starter` for a User owner or `plan: team` for an Organization owner.
- **Seeds the app's Roles.** For each name in `available_app_roles`, a Role `role_<slug>_<name>` is created with no permissions, and the owner then fills them in with `PATCH /v1/roles/{id}`. Because every app-scoped Role must use a name from `available_app_roles`, the plan's **AppRoles limit** (Starter 3, Team and Enterprise unlimited) is simply a cap on the length of `available_app_roles`. Exceeding it on `POST` or `PATCH` returns `409 plan_limit_reached` (`resource: "app_roles"`; see [Conventions → Plan limit errors](../conventions/#plan-limit-errors)).
- Rejected with `409 plan_limit_reached` if the owner's Tenant is already at its plan's Applications cap (1/5/unlimited). Rejected with `402 subscription_required` if the Tenant is `restricted`. Both are independent of the caller's own permission. See [Pricing → Enforcement](../../pricing/#enforcement).
- Writes `application.created`.

## `PATCH /v1/applications/{id}`

```json
// Request — owner adding a role name and a permission
{
  "available_app_roles": ["admin", "editor", "member"],
  "permissions": [
    { "key": "app.timetrack.export", "description": "Export timesheets as CSV/PDF." },
    { "key": "app.timetrack.manage_members", "description": "Add and remove team members inside TimeTrack." },
    { "key": "app.timetrack.approve", "description": "Approve submitted timesheets." }
  ]
}
```
```json
// Response — 200, the full updated Application (same shape as above), now with
// "available_app_roles": ["admin", "editor", "member"], three permissions, and a new updated_at.
// The Role role_timetrack_editor was seeded with no permissions.
```

There are two kinds of caller, and they can change different fields:

| Field | Owner | `applications.manage` |
|---|---|---|
| `name`, `description`, `icon_url`, `launch_url`, `redirect_uris`, `support_url`, `email_from_name`, `settings_schema`, `permissions`, `available_app_roles`, `default_app_role` | ✓ | ✓ |
| `visibility`, `review_status`, `review_notes`, `owner_user_id`/`owner_organization_id` | — (`403 moderation_field_forbidden`) | ✓ |
| `slug`, `tenant_id`, `id`, `created_at` | — | — (`422 read_only_field`) |

"Owner" means the `owner_user_id` User, or any `org_admin` of `owner_organization_id`. Ordinary members of the owning Organization can read the Application but not change it.

- **Removing a name** from `available_app_roles` that still has a Role with assignments returns `409 app_role_in_use`. If the Role has no assignments, it's deleted along with the name.
- **Removing a `permissions` entry** still referenced by any Role returns `409 permission_in_use` (`details.role_ids`).
- **Adding a name** to `available_app_roles` seeds the matching empty Role, same as on create.
- **Changing ownership** is a platform action. It moves the Application to the new owner's Tenant, which is a support-run data migration, so it's only allowed when both Tenants are on the `shared` tier. Otherwise it returns `409 ownership_change_requires_migration`.
- Writes `application.updated`. Changes to `review_status` write `application.review_status_changed`.

### Reviewing a submission

```json
// Request — reject
{ "review_status": "rejected", "review_notes": "launch_url does not resolve; resubmit once it's live." }
```
```json
// Request — suspend an already-launched app
{ "review_status": "suspended", "review_notes": "Repeated webhook signature failures suggest a compromised signing secret; paused pending developer confirmation." }
```

Allowed `review_status` transitions:

| From → to | Who | Notes |
|---|---|---|
| `pending_review → approved` | `applications.manage` | `review_notes` optional. |
| `pending_review → rejected` | `applications.manage` | `review_notes` required. |
| `approved → suspended` | `applications.manage` | `review_notes` required. Blocks new grants and new app-token issuance; existing Entitlements and open sessions are untouched. |
| `suspended → approved` | `applications.manage` | Reinstatement. |
| `rejected → pending_review` | automatic | The owner's next `PATCH` to any configuration field resubmits it. There's no separate resubmit endpoint. |

Any other transition returns `409 invalid_review_transition`. A transition to `rejected` or `suspended` without `review_notes` returns `422 review_notes_required`. Rejecting or suspending does **not** touch existing Entitlements. See [Domain Model → The review lifecycle](../../domain-model/applications/#the-review-lifecycle) for why.

## Errors specific to this resource

| Code | Status | When |
|---|---|---|
| `slug_taken` | 409 | `slug` collides with an existing Application on create. |
| `application_not_found` | 404 | `{id}` doesn't resolve, or isn't visible to the caller. |
| `app_role_in_use` | 409 | `PATCH` would remove a role name whose Role still has assignments. |
| `permission_in_use` | 409 | `PATCH` would remove a permission still referenced by a Role. |
| `invalid_settings_schema` | 422 | The schema breaks a [`settings_schema` rule](../../domain-model/settings/#settings_schema-rules). |
| `reserved_settings_key` | 422 | The schema declares `locale`, `timezone`, `theme`, or `notifications`. |
| `moderation_field_forbidden` | 403 | A non-admin owner's `PATCH` touches a moderation field. |
| `review_notes_required` | 422 | Transition to `rejected`/`suspended` without `review_notes`. |
| `invalid_review_transition` | 409 | A `review_status` change not in the table above. |
| `ownership_change_requires_migration` | 409 | An ownership change across non-`shared` Tenants. |
| `plan_limit_reached` | 409 | `POST` would exceed the owner Tenant's Applications cap, or `POST`/`PATCH` would exceed its AppRoles cap. |
| `subscription_required` | 402 | `POST` on a `restricted` Tenant. |
| `read_only_field` | 422 | `PATCH` touches `id`, `slug`, `tenant_id`, `created_at`, or `settings_schema_stats`. |
| `validation_failed` | 422 | A field breaks a rule in the table above; see [Validation errors](#validation-errors). |

### Validation errors

Every rule in [The Application object](#the-application-object) table reports through `details.fields`, all failures at once:

```json
// POST /v1/applications with a bad slug, an http launch_url, and too many role names
{
  "error": {
    "code": "validation_failed",
    "message": "3 fields failed validation.",
    "details": {
      "fields": [
        { "field": "slug", "code": "invalid_format", "pattern": "^[a-z][a-z0-9-]{1,63}$" },
        { "field": "launch_url", "code": "invalid_format", "allowed": ["https"] },
        { "field": "available_app_roles", "code": "too_long", "max": 20 }
      ]
    }
  }
}
```

A reserved slug is `{ "field": "slug", "code": "unknown_value", "allowed": "anything but platform" }`, and a taken one is `409 slug_taken`, not a field error, because it's about existing state.

## Owner onboarding, end to end

What a paying customer does after staff have created their Application (concierge onboarding), all with their own User token and no platform permission:

1. **Find your Tenant:** `GET /v1/tenants` returns the one Tenant you own, with its `plan` and `tier`. `GET /v1/applications?owned=true` lists your Applications.
2. **Configure the app:** `PATCH /v1/applications/app_invoicer` with your `redirect_uris`, `settings_schema`, `permissions`, and `available_app_roles`. Then give each seeded Role its permissions with `PATCH /v1/roles/role_invoicer_admin` `{"permissions": ["app.invoicer.view", "app.invoicer.send"]}`.
3. **Create your backend's key:** `POST /v1/api-keys` `{"name": "invoicer-backend", "scope": "app_invoicer", "permissions": ["entitlements.manage", "users.list"]}`. Store the `secret`; it's shown once. Create a `"mode": "test"` key too for integration tests.
4. **Subscribe to revocations:** `POST /v1/webhooks` `{"scope": "app_invoicer", "url": "https://invoicer.example.com/hooks/substratal", "events": ["access.revoked", "role.removed"]}`. Store the `signing_secret`. This pair is the [compliance minimum](../webhooks/#post-v1webhooks).
5. **Grant access as your billing says:** from your backend, with the app key, `POST /v1/users/{id}/entitlements` for a personal purchase, or `POST /v1/organizations/{id}/entitlements` for a team purchase. Both need an `Idempotency-Key`; key it on your own order id.
6. **Check your bill:** `GET /v1/tenants/{id}/usage` shows where you are against your plan's limits, and `POST /v1/tenants/{id}/billing/checkout-sessions` upgrades to Team when you need to.
