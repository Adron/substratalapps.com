---
layout: default
title: Database Schema
parent: Domain Model
nav_order: 8
---

# Database Schema
{: .no_toc }

The entity pages describe *what* each field means to an API caller. This page is for an implementer: concrete Postgres types, constraints, and the indexes the documented query patterns actually need. See [Deployment Architecture](../../deployment-architecture/) for why Postgres (Aurora Serverless v2) is the chosen engine.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Conventions used below

- Every table's primary key is the entity's own prefixed id (`usr_…`, `ent_…`, …) stored as `text`, not a surrogate `bigint` — the prefix convention in [Conventions → IDs](../../api-reference/conventions/#ids) *is* the primary key, not a display layer on top of one.
- Every multi-tenant table carries `organization_id text null references organizations(id)`, per [Non-Functional Requirements → Multi-tenancy](../../non-functional-requirements/#multi-tenancy), with a Row-Level Security policy — not repeated per-table below.
- Every table carries `created_at timestamptz not null default now()`; tables with mutable fields also carry `updated_at timestamptz not null default now()`, maintained by a trigger, not application code (so it's correct even for a direct `UPDATE` run by a migration or a support script).
- Soft-deletable tables carry `deleted_at timestamptz null` rather than a boolean — `null` means active, a timestamp means both *that* it's deleted and *when*, which a boolean throws away.

## users

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `email` | `citext` | `unique`, not null |
| `email_verified` | `boolean` | not null, default `false` |
| `status` | `text` | not null, `check (status in ('active','invited','suspended','deleted'))` |
| `organization_id` | `text` | nullable, `references organizations(id)` |
| `auth` | `jsonb` | shape depends on [Decisions → Identity provider](../../decisions/#1-identity-provider) |
| `created_at`, `last_login_at` | `timestamptz` | `last_login_at` nullable |
| `deleted_at` | `timestamptz` | nullable — soft-delete |

**Indexes:** `unique (email) where deleted_at is null` (a deleted user's email should be reusable by a new signup — a plain unique index would block that); `(organization_id)` for member listing; `(status)` for admin filtering.

## organizations

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `name` | `text` | not null |
| `status` | `text` | not null, `check (status in ('active','suspended'))` |

No surprising constraints — small table, no high-cardinality query pattern beyond primary key lookup and the `users.organization_id` index above.

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
| `review_status` | `text` | not null, default `'approved'`, `check (review_status in ('approved','pending_review','suspended'))` |

**Constraint:** `check (owner_user_id is null or owner_organization_id is null)` — an Application has at most one kind of owner, never both. **Index:** `(owner_user_id)`, `(owner_organization_id)` for "my apps" queries; `(visibility, review_status) where review_status = 'approved'` for the public catalog listing.

## entitlements

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `user_id` | `text` | nullable, `references users(id)` |
| `organization_id` | `text` | nullable, `references organizations(id)` — the org-seat grant, not the general multi-tenancy column |
| `application_id` | `text` | not null, `references applications(id)` |
| `status` | `text` | not null, `check (status in ('active','disabled','expired','revoked'))` |
| `source` | `text` | not null, `check (source in ('purchase','trial','admin_grant','org_seat'))` |
| `order_id` | `text` | nullable |
| `starts_at`, `ends_at` | `timestamptz` | `ends_at` nullable |
| `disabled_reason` | `text` | nullable |

**Constraint:** `check (user_id is not null or organization_id is not null)`. **Indexes:** `(user_id, application_id) where status = 'active'` partial-unique — this is what [`entitlement_already_exists`](../../api-reference/entitlements/#errors-specific-to-this-resource) enforces at the database level, not just in application code; `(application_id, status)` for `GET /v1/entitlements` filtering (see [API Reference → Entitlements](../../api-reference/entitlements/)); `(organization_id)` for org-seat listing.

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
  overrides jsonb not null default '{}',
  updated_at timestamptz not null default now(),
  primary key (user_id, application_id)
);
```

## audit_events

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `actor_user_id` | `text` | not null — `usr_system` for system-initiated events, see [Domain Model → Orders & Audit](../orders-and-audit/) |
| `action` | `text` | not null, `check` against the [Action catalog](../orders-and-audit/#action-catalog) |
| `target_user_id` | `text` | not null |
| `application_id` | `text` | nullable |
| `before`, `after` | `jsonb` | |
| `timestamp` | `timestamptz` | not null, default `now()` |

No `updated_at`, no soft-delete column — this table is append-only by design (see [Non-Functional Requirements → Audit](../../non-functional-requirements/#audit)); revoke `UPDATE`/`DELETE` grants on this table for the application's own database role, so an application-layer bug can't violate the "never edited" guarantee even accidentally. **Indexes:** `(target_user_id, timestamp desc)`, `(application_id, timestamp desc)`, `(actor_user_id, timestamp desc)` — one per documented filter in [API Reference → Audit](../../api-reference/audit/).

## api_keys

| Column | Type | Constraint |
|---|---|---|
| `id` | `text` | primary key |
| `name` | `text` | not null |
| `scope` | `text` | not null |
| `permissions` | `text[]` | not null |
| `mode` | `text` | not null, `check (mode in ('live','test'))` |
| `secret_hash` | `text` | not null — **never store the secret itself**, only a hash (e.g. SHA-256) of it, the same way a password would be stored. The API can verify a presented key against the hash; it can never display the original value again, which is exactly the contract [API Keys](../../api-reference/api-keys/) describes ("returned once"). |
| `last_used_at`, `revoked_at` | `timestamptz` | nullable |

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

Per [Conventions → Idempotency](../../api-reference/conventions/#idempotency)'s implementation note — a scheduled job (see [Deployment Architecture](../../deployment-architecture/)) deletes expired rows rather than relying on unbounded table growth.
