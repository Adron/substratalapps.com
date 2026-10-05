---
layout: default
title: Database Schema
parent: Domain Model
nav_order: 8
---

# Database Schema
{: .no_toc }

The entity pages describe *what* each field means to an API caller. This page is for an implementer: concrete Postgres types, constraints, and the indexes the documented query patterns actually need. See [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) for why Postgres (Aurora Serverless v2) is the chosen engine.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Conventions used below

- Every table's primary key is the entity's own prefixed id (`usr_…`, `ent_…`, …) stored as `text`, not a surrogate `bigint` — the prefix convention in [Conventions → IDs](../../api-reference/conventions/#ids) *is* the primary key, not a display layer on top of one.
- Every end-user-scoped table carries `organization_id text null references organizations(id)`, per [Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy), with a Row-Level Security policy — not repeated per-table below. This is **team/seat grouping**, not infrastructure placement.
- Every table scoped to one Application also carries `tenant_id text not null references tenants(id)`, denormalized from `applications.tenant_id` at write time — a **second, independent** RLS dimension used to route a row to the right physical cluster (see [Tenancy](../tenancy/) and [Deployment Architecture → Tenancy tiers](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)). Don't conflate this with `organization_id` above — a row can carry both, either, or neither.
- Every table carries `created_at timestamptz not null default now()`; tables with mutable fields also carry `updated_at timestamptz not null default now()`, maintained by a trigger, not application code (so it's correct even for a direct `UPDATE` run by a migration or a support script).
- Soft-deletable tables carry `deleted_at timestamptz null` rather than a boolean — `null` means active, a timestamp means both *that* it's deleted and *when*, which a boolean throws away.

## users

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `email` | `citext` | `unique`, not null |
| `email_verified` | `boolean` | not null, default `false` |
| `status` | `text` | not null, `check (status in ('active','invited','suspended','deleted'))` |
| `created_at`, `last_login_at` | `timestamptz` | `last_login_at` nullable |
| `deleted_at` | `timestamptz` | nullable — soft-delete |

**Indexes:** `unique (email) where deleted_at is null` (a deleted user's email should be reusable by a new signup — a plain unique index would block that); `(status)` for admin filtering. No `organization_id` here — see `organization_memberships` below; a single column on `users` would cap a User at one Organization. No `auth`/password column here either — see `user_identities` below; a User can hold more than one login method, per [Decisions → Identity provider](../../decisions/#1-identity-provider).

## user_identities, sso_connections

```sql
user_identities (
  id text primary key,                    -- uid_...
  user_id text not null references users(id),
  method text not null check (method in ('password','sso')),
  password_hash text,                      -- set iff method = 'password'
  sso_connection_id text references sso_connections(id),  -- set iff method = 'sso'
  external_subject_id text,                -- the IdP's own user id, set iff method = 'sso'
  mfa_enabled boolean not null default false,
  mfa_secret text,                         -- encrypted TOTP secret; set iff mfa_enabled
  last_used_at timestamptz,
  created_at timestamptz not null default now()
);

sso_connections (
  id text primary key,                     -- ssc_...
  organization_id text not null references organizations(id),
  provider text not null,                  -- e.g. 'workos'
  domain text,                             -- auto-routes a matching-email signup to this connection
  status text not null default 'active' check (status in ('active','inactive')),
  created_at timestamptz not null default now()
);
```

**Constraint:** `check (method = 'password' and password_hash is not null and sso_connection_id is null) or (method = 'sso' and sso_connection_id is not null and password_hash is null)` — a `user_identities` row is exactly one method's worth of credential, never a mix. **Index:** `(user_id)` on `user_identities` — a login attempt looks up every identity a User holds and tries to match the presented credential against one of them, per [Domain Model → UserIdentity](../users-and-organizations/#useridentity): there's no single "the" credential row to look up directly. `unique (organization_id, domain) where domain is not null` on `sso_connections` — one connection claims a given email domain per Organization, not several competing for it.

## organizations

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `name` | `text` | not null |
| `status` | `text` | not null, `check (status in ('active','suspended'))` |

No surprising constraints — small table, lookup is by primary key and via `organization_memberships` below.

## organization_memberships

```sql
organization_memberships (
  user_id text not null references users(id),
  organization_id text not null references organizations(id),
  role text not null check (role in ('org_admin','member')),
  joined_at timestamptz not null default now(),
  primary key (user_id, organization_id)
);
```

**Index:** `(organization_id)` for member-listing queries — the inverse of the primary key's natural lookup direction (by user). This is what makes a User's membership many-to-many: no `organization_id` column on `users` to collide with.

## tenants

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `owner_user_id` | `text` | nullable, `references users(id)` |
| `owner_organization_id` | `text` | nullable, `references organizations(id)` |
| `tier` | `text` | not null, default `'shared'`, `check (tier in ('shared','isolated','dedicated_region'))` |
| `plan` | `text` | not null, default `'starter'`, `check (plan in ('starter','team','enterprise'))` — see [Pricing](../../pricing/) |
| `region` | `text` | nullable — set only when `tier = 'dedicated_region'` |
| `status` | `text` | not null, default `'active'`, `check (status in ('active','migrating','suspended'))` |

**Constraint:** `check (owner_user_id is null or owner_organization_id is null)` and `check (owner_user_id is not null or owner_organization_id is not null)` — exactly one owner, same pattern as `applications.owner_*` below. `check (plan = 'enterprise' or tier = 'shared')` — enforces [Pricing](../../pricing/#enterprise-tenancy-tier-options)'s rule that `isolated`/`dedicated_region` are Enterprise-only at the database level, not just in application code. **Indexes:** `unique (owner_user_id) where owner_user_id is not null`, `unique (owner_organization_id) where owner_organization_id is not null` — one Tenant per owner, not a list. See [Tenancy](../tenancy/).

## applications

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `slug` | `text` | `unique`, not null, `check (slug ~ '^[a-z][a-z0-9-]{1,63}$')` — see [Domain Model → Applications](../applications/) |
| `name`, `description`, `icon_url`, `launch_url` | `text` | `launch_url` not null |
| `settings_schema` | `jsonb` | not null, default `'{}'` |
| `available_app_roles` | `text[]` | not null, default `'{}'` |
| `visibility` | `text` | not null, `check (visibility in ('public','invite_only','internal'))` |
| `owner_user_id` | `text` | nullable, `references users(id)` |
| `owner_organization_id` | `text` | nullable, `references organizations(id)` |
| `review_status` | `text` | not null, default `'approved'`, `check (review_status in ('approved','pending_review','rejected','suspended'))` |
| `review_notes` | `text` | nullable — `check (review_notes is not null or review_status not in ('rejected','suspended'))` enforces it's required on exactly those two transitions, at the database level, not just in application code |
| `tenant_id` | `text` | not null, `references tenants(id)` — resolved from whichever owner column is set at insert time, see [Tenancy](../tenancy/) |

**Constraint:** `check (owner_user_id is null or owner_organization_id is null)` — an Application has at most one kind of owner, never both. **Index:** `(owner_user_id)`, `(owner_organization_id)` for "my apps" queries; `(visibility, review_status) where review_status = 'approved'` for the public catalog listing; `(tenant_id)` for a support/ops query of "every Application on this Tenant" during a tier-change migration.

## entitlements

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `user_id` | `text` | nullable, `references users(id)` |
| `organization_id` | `text` | nullable, `references organizations(id)` — the org-seat grant, not the general multi-tenancy column |
| `application_id` | `text` | not null, `references applications(id)` |
| `tenant_id` | `text` | not null, `references tenants(id)` — denormalized from `applications.tenant_id`, see [conventions above](#conventions-used-below) |
| `status` | `text` | not null, `check (status in ('active','disabled','expired','revoked'))` |
| `source` | `text` | not null, `check (source in ('purchase','trial','admin_grant','org_seat'))` |
| `order_id` | `text` | nullable |
| `starts_at`, `ends_at` | `timestamptz` | `ends_at` nullable |
| `disabled_reason` | `text` | nullable |
| `member_scope` | `text` | nullable, `check (member_scope in ('all_members','allowlist','denylist'))` — only set when `organization_id` is set, see [Entitlements → Org-wide entitlements](../entitlements/#org-wide-entitlements-scoping-members-in-or-out) |
| `member_overrides` | `text[]` | nullable — `user_id`s this grant's default is flipped for, only meaningful alongside `member_scope in ('allowlist','denylist')` |

**Constraint:** `check (user_id is not null or organization_id is not null)`. **Indexes:** `(user_id, application_id) where status = 'active'` partial-unique — this is what [`entitlement_already_exists`](../../api-reference/entitlements/#errors-specific-to-this-resource) enforces at the database level, not just in application code; `(application_id, status)` for `GET /v1/entitlements` filtering (see [API Reference → Entitlements](../../api-reference/entitlements/)); `(organization_id)` for org-seat listing; `member_overrides` is read with `= any(member_overrides)` at resolution time rather than its own index — the row count per org-wide grant is small enough that a sequential scan of one array is cheaper than maintaining a GIN index for it.

## roles, user_role_assignments

```sql
roles (
  id text primary key,            -- role_... — see the ID exception in Conventions
  name text not null,
  scope text not null,            -- 'platform' or an application_id
  permissions text[] not null default '{}'
);

user_role_assignments (
  user_id text not null references users(id),
  role_id text not null references roles(id),
  application_id text,            -- denormalized from roles.scope, null for platform roles
  assigned_at timestamptz not null default now(),
  assigned_by text not null references users(id),
  primary key (user_id, role_id)
);
```

`application_id` on the assignment is denormalized from the Role's own `scope` specifically so "does this user hold any role for app X" is an index lookup (`(user_id, application_id)`) rather than a join through `roles` on every [Access Control](../../access-control/) check — this table is read on every single authorized request, so it's worth the denormalization.

## profiles, app_profiles, settings, app_settings

Four small tables, same shape pattern — global vs. per-app, each keyed by `user_id` (profiles/settings) or `(user_id, application_id)` (app_profiles/app_settings). `app_settings` stores only the `overrides` object described in [API Reference → Settings](../../api-reference/settings/) — the resolved view is computed at read time, never materialized, so an Application's default change (in its own `settings_schema`) is reflected for every user who never overrode it without a backfill.

```sql
app_settings (
  user_id text not null references users(id),
  application_id text not null references applications(id),
  tenant_id text not null references tenants(id),
  overrides jsonb not null default '{}',
  updated_at timestamptz not null default now(),
  primary key (user_id, application_id)
);
```

`app_profiles` carries the same `tenant_id`, denormalized from `applications.tenant_id` the same way — both are per-Application data, so both are placed by that Application's Tenant.

### Typed fields: generated columns over jsonb

Per [Decisions → Storage primitive scope](../../decisions/#8-storage-primitive-scope), `overrides` (and `custom`, on `app_profiles`) stays `jsonb` — but every property an Application has actually declared in its `settings_schema` also gets a Postgres [generated column](https://www.postgresql.org/docs/current/ddl-generated-columns.html), added (and dropped) by the same code path that validates a `PATCH .../settings` write against that schema:

```sql
alter table app_settings
  add column week_start text
    generated always as (overrides->>'week_start') stored;

create index on app_settings (application_id, week_start)
  where week_start is not null;
```

The cast in the generated column's expression matches the schema's declared type (`->>'...'` plus an explicit `::boolean`/`::integer`/`::timestamptz` cast for anything non-text) — this is what makes the field queryable and sortable with a normal index, instead of every read needing a `jsonb` containment or path operator. `overrides` itself is never rewritten; the generated column is a read-optimized projection of one key in it, recomputed by Postgres automatically on every write, never drifting from the source.

This only applies to fields an Application has declared — an arbitrary, undeclared key written into `overrides` lives in the `jsonb` only, exactly as flexible (and exactly as unindexed) as before.

## audit_events

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `actor_user_id` | `text` | not null — `usr_system` for system-initiated events, see [Domain Model → Orders & Audit](../orders-and-audit/) |
| `action` | `text` | not null, `check` against the [Action catalog](../orders-and-audit/#action-catalog) |
| `target_user_id` | `text` | not null |
| `application_id` | `text` | nullable |
| `tenant_id` | `text` | nullable — set whenever `application_id` is, denormalized the same way as other per-Application tables; `null` for platform-level events (e.g. `user.created`) with no single Application to place |
| `before`, `after` | `jsonb` | |
| `timestamp` | `timestamptz` | not null, default `now()` |

No `updated_at`, no soft-delete column — this table is append-only by design (see [Non-Functional Requirements → Audit](../../non-functional-requirements/#audit)); revoke `UPDATE`/`DELETE` grants on this table for the application's own database role, so an application-layer bug can't violate the "never edited" guarantee even accidentally. **Indexes:** `(target_user_id, timestamp desc)`, `(application_id, timestamp desc)`, `(actor_user_id, timestamp desc)`, `(tenant_id, timestamp desc)` — one per documented filter in [API Reference → Audit](../../api-reference/audit/), the last one being what a tier-change migration uses to pull "every event for this Tenant" without scanning the whole table.

## api_keys

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `name` | `text` | not null |
| `scope` | `text` | not null |
| `permissions` | `text[]` | not null |
| `mode` | `text` | not null, `check (mode in ('live','test'))` |
| `intended_use` | `text` | not null, default `'service'`, `check (intended_use in ('service','agent'))` |
| `restrict_destructive` | `boolean` | not null, default computed from `intended_use` at insert time (`true` iff `intended_use = 'agent'`), overridable — see [API Keys → Agent keys](../../api-reference/api-keys/#agent-keys--restrict_destructive) |
| `secret_hash` | `text` | not null — **never store the secret itself**, only a hash (e.g. SHA-256) of it, the same way a password would be stored. The API can verify a presented key against the hash; it can never display the original value again, which is exactly the contract [API Keys](../../api-reference/api-keys/) describes ("returned once"). |
| `last_used_at`, `revoked_at` | `timestamptz` | nullable |

**Enforcement, not just storage:** the `restrict_destructive` check happens in the same request-handling layer as the permission check — both read from the authenticated key's row, both must pass. The classification of which operations count as destructive lives in exactly one place in the implementation (a lookup by HTTP method + the specific Entitlement/Role/Organization-member/Tenant-tier mutations named in [Decisions → MCP server authorization scope](../../decisions/#14-mcp-server-authorization-scope)), shared by this check and by the MCP server's `destructiveHint` tool-annotation generation — not reimplemented twice and left to drift.

## idempotency_keys

```sql
idempotency_keys (
  caller_id text not null,
  idempotency_key uuid not null,
  request_body_hash text not null,
  response_status int not null,
  response_body jsonb not null,
  expires_at timestamptz not null,
  primary key (caller_id, idempotency_key)
);
```

Per [Conventions → Idempotency](../../api-reference/conventions/#idempotency)'s implementation note — a scheduled job (see [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) deletes expired rows rather than relying on unbounded table growth.
