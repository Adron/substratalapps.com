-- 0001_init: the full schema from docs/domain-model/database-schema.md.
--
-- Forward-only (README → Migrations: expand, then contract). Applied by
-- cmd/migrate, one statement at a time, in a transaction per file.

create extension if not exists citext;
create schema if not exists substratal_util;

-- ── Helpers ─────────────────────────────────────────────────────────────

-- updated_at is maintained by trigger, not application code, so it's
-- correct even for a direct UPDATE from a migration or support script.
create function substratal_util.touch_updated_at() returns trigger language plpgsql as $$
begin
  new.updated_at := now();
  return new;
end $$;

-- version backs ETag / If-Match. Bumped only when the row actually changes.
create function substratal_util.bump_version() returns trigger language plpgsql as $$
begin
  if row(new.*) is distinct from row(old.*) then
    new.version := old.version + 1;
  end if;
  return new;
end $$;

-- Test/live isolation (Conventions → Authentication). Every request's
-- transaction sets app.current_test_mode to 'true' or 'false'; scheduled
-- jobs that span both modes set 'any'. Unset fails closed to live-only.
create function substratal_util.mode_visible(row_mode boolean) returns boolean
language sql stable as $$
  select case coalesce(nullif(current_setting('app.current_test_mode', true), ''), 'false')
           when 'any'  then true
           when 'true' then row_mode
           else not row_mode
         end
$$;

-- Null-on-failure casts for typed per-Application settings views
-- (Database Schema → Typed fields). A stale value never breaks a read.
create function substratal_util.try_bool(j jsonb) returns boolean immutable language sql as $$
  select case when jsonb_typeof(j) = 'boolean' then (j #>> '{}')::boolean end $$;
create function substratal_util.try_num(j jsonb) returns numeric immutable language sql as $$
  select case when jsonb_typeof(j) = 'number' then (j #>> '{}')::numeric end $$;
create function substratal_util.try_int(j jsonb) returns bigint immutable language sql as $$
  select case when jsonb_typeof(j) = 'number' and (j #>> '{}') ~ '^-?[0-9]+$' then (j #>> '{}')::bigint end $$;
create function substratal_util.try_text(j jsonb) returns text immutable language sql as $$
  select case when jsonb_typeof(j) = 'string' then j #>> '{}' end $$;
create function substratal_util.try_timestamptz(j jsonb) returns timestamptz immutable language plpgsql as $$
begin
  if jsonb_typeof(j) <> 'string' then return null; end if;
  return (j #>> '{}')::timestamptz;
exception when others then
  return null;
end $$;

-- ── Users & identity ────────────────────────────────────────────────────

create table users (
  id text primary key,
  email citext not null,
  email_verified boolean not null default false,
  status text not null check (status in ('active','invited','suspended','deleted')),
  pending_email citext,
  signup_application_id text,               -- FK added after applications exists
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  last_login_at timestamptz,
  version integer not null default 1,
  deleted_at timestamptz
);
create unique index users_email_mode_key on users (email, test_mode) where deleted_at is null;
create index users_email_prefix on users (lower(email::text) text_pattern_ops);
create index users_status on users (status);

create table organizations (
  id text primary key,
  name text not null,
  status text not null default 'active' check (status in ('active','suspended')),
  created_by text not null references users(id),
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1
);

create table sso_connections (
  id text primary key,
  organization_id text not null references organizations(id),
  provider text not null,
  domain text,
  status text not null default 'active' check (status in ('active','inactive')),
  created_at timestamptz not null default now()
);
create unique index sso_connections_domain_key on sso_connections (domain)
  where domain is not null and status = 'active';

create table user_identities (
  id text primary key,
  user_id text not null references users(id),
  method text not null check (method in ('password','sso')),
  password_hash text,
  sso_connection_id text references sso_connections(id),
  external_subject_id text,
  mfa_enabled boolean not null default false,
  mfa_secret text,
  mfa_pending_secret text,
  mfa_pending_expires_at timestamptz,
  mfa_last_step bigint,                     -- last accepted TOTP time step: a code can't be reused in its window
  failed_login_count int not null default 0,
  failed_login_window_start timestamptz,
  locked_until timestamptz,
  last_used_at timestamptz,
  created_at timestamptz not null default now(),
  -- Database Schema requires password_hash on a password identity; the
  -- erasure cascade (NFR → Hard-delete cascade, step 1) then deletes it
  -- while keeping the row, so a password row may lose its hash only once
  -- its User is erased (enforced by the cascade, which nulls it last).
  constraint user_identities_method_shape check (
    (method = 'password' and sso_connection_id is null) or
    (method = 'sso' and sso_connection_id is not null and password_hash is null)
  )
);
create index user_identities_user on user_identities (user_id);

create table sessions (
  id text primary key,
  user_id text not null references users(id),
  amr text[] not null,
  ip_address inet,
  user_agent text,
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  idle_expires_at timestamptz not null,
  absolute_expires_at timestamptz not null,
  revoked_at timestamptz,
  revoked_reason text
);
create index sessions_user_active on sessions (user_id) where revoked_at is null;

create table refresh_tokens (
  token_hash text primary key,
  session_id text not null references sessions(id) on delete cascade,
  application_id text,
  rotated_at timestamptz,
  created_at timestamptz not null default now()
);
create index refresh_tokens_session on refresh_tokens (session_id);

create table auth_tokens (
  token_hash text primary key,
  kind text not null check (kind in ('email_verify','password_reset','invitation','mfa_challenge','authorization_code')),
  user_id text not null references users(id),
  payload jsonb not null default '{}',
  attempts int not null default 0,
  expires_at timestamptz not null,
  consumed_at timestamptz,
  created_at timestamptz not null default now()
);
create index auth_tokens_user_kind_open on auth_tokens (user_id, kind) where consumed_at is null;

create table mfa_recovery_codes (
  user_identity_id text not null references user_identities(id),
  code_hash text not null,
  used_at timestamptz,
  primary key (user_identity_id, code_hash)
);

create table organization_memberships (
  user_id text not null references users(id),
  organization_id text not null references organizations(id),
  role text not null check (role in ('org_admin','member')),
  status text not null default 'active' check (status in ('pending','active')),
  invited_by text references users(id),
  invited_at timestamptz not null default now(),
  joined_at timestamptz,
  test_mode boolean not null default false,
  primary key (user_id, organization_id),
  check ((status = 'active') = (joined_at is not null))
);
create index organization_memberships_org on organization_memberships (organization_id);

-- ── Tenancy & catalog ──────────────────────────────────────────────────

create table tenants (
  id text primary key,
  owner_type text not null check (owner_type in ('user','organization')),
  owner_user_id text references users(id),
  owner_organization_id text references organizations(id),
  tier text not null default 'shared' check (tier in ('shared','isolated','dedicated_region')),
  plan text not null default 'starter' check (plan in ('starter','team','enterprise')),
  region text not null default 'us-east-1'
    check (region in ('us-east-1','us-west-2','ca-central-1','eu-west-1','eu-central-1','ap-southeast-2')),
  stripe_customer_id text unique,
  stripe_subscription_id text unique,
  subscription_status text not null default 'none'
    check (subscription_status in ('none','active','trialing','past_due','canceled','unpaid','incomplete','incomplete_expired','paused')),
  current_period_start timestamptz,
  current_period_end timestamptz,
  cancel_at_period_end boolean not null default false,
  restricted boolean not null default false,
  seat_count_synced integer,
  seat_count_synced_at timestamptz,
  status text not null default 'active' check (status in ('active','migrating','suspended')),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  check ((owner_type = 'user') = (owner_user_id is not null)),
  check (owner_user_id is null or owner_organization_id is null),
  check (owner_user_id is not null or owner_organization_id is not null),
  check (tier = 'dedicated_region' or region = 'us-east-1'),
  check (plan = 'enterprise' or tier = 'shared'),
  check (plan <> 'starter' or owner_type = 'user')
);
create unique index tenants_owner_user_key on tenants (owner_user_id) where owner_user_id is not null;
create unique index tenants_owner_org_key on tenants (owner_organization_id) where owner_organization_id is not null;

create table applications (
  id text primary key,
  slug text not null unique,
  name text not null,
  description text,
  icon_url text,
  launch_url text not null,
  support_url text,
  email_from_name text,
  redirect_uris text[] not null default '{}',
  settings_schema jsonb not null default '{"type":"object","properties":{}}',
  permissions jsonb not null default '[]',
  available_app_roles text[] not null default '{}',
  default_app_role text,
  visibility text not null check (visibility in ('public','invite_only','internal')),
  owner_user_id text references users(id),
  owner_organization_id text references organizations(id),
  review_status text not null default 'approved'
    check (review_status in ('approved','pending_review','rejected','suspended')),
  review_notes text,
  tenant_id text not null references tenants(id),
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  check (id = 'app_' || slug),
  check (slug ~ '^[a-z][a-z0-9-]{1,63}$' and slug <> 'platform'),
  check (cardinality(redirect_uris) <= 10),
  check (default_app_role is null or default_app_role = any(available_app_roles)),
  check (review_notes is not null or review_status not in ('rejected','suspended')),
  check (owner_user_id is null or owner_organization_id is null),
  check (owner_user_id is not null or owner_organization_id is not null)
);
create index applications_owner_user on applications (owner_user_id);
create index applications_owner_org on applications (owner_organization_id);
create index applications_catalog on applications (visibility, review_status) where review_status = 'approved';
create index applications_tenant on applications (tenant_id);

alter table users add constraint users_signup_application_fk
  foreign key (signup_application_id) references applications(id);
alter table refresh_tokens add constraint refresh_tokens_application_fk
  foreign key (application_id) references applications(id);

-- ── Entitlements ───────────────────────────────────────────────────────

create table entitlements (
  id text primary key,
  user_id text references users(id),
  organization_id text references organizations(id),
  application_id text not null references applications(id),
  tenant_id text not null references tenants(id),
  status text not null check (status in ('active','disabled','expired','revoked')),
  source text not null check (source in ('purchase','trial','admin_grant','org_seat')),
  order_id text check (char_length(order_id) <= 255),
  starts_at timestamptz not null default now(),
  ends_at timestamptz,
  disabled_reason text,
  member_scope text check (member_scope in ('all_members','allowlist','denylist')),
  member_overrides text[] check (cardinality(member_overrides) <= 1000),
  start_notified_at timestamptz,            -- set once access.granted has fired for a future starts_at
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  check ((user_id is null) <> (organization_id is null)),
  check ((source = 'org_seat') = (organization_id is not null)),
  check (source <> 'trial' or ends_at is not null),
  check (status not in ('disabled','revoked') or disabled_reason is not null)
);
create unique index entitlements_user_app_key on entitlements (user_id, application_id, test_mode)
  where user_id is not null and status in ('active','disabled');
create unique index entitlements_org_app_key on entitlements (organization_id, application_id, test_mode)
  where organization_id is not null and status in ('active','disabled');
create index entitlements_order on entitlements (application_id, order_id) where order_id is not null;
create index entitlements_expiry on entitlements (status, ends_at)
  where status in ('active','disabled') and ends_at is not null;
create index entitlements_starts on entitlements (starts_at) where status = 'active';
create index entitlements_app_status on entitlements (application_id, status);
create index entitlements_org on entitlements (organization_id);
create index entitlements_user on entitlements (user_id);

-- ── Roles ──────────────────────────────────────────────────────────────

create table roles (
  id text primary key,
  name text not null check (name ~ '^[a-z][a-z0-9_]{1,40}$'),
  scope text not null,
  description text,
  permissions text[] not null default '{}',
  seed boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  test_mode boolean not null default false,
  unique (scope, name, test_mode)
);

create table user_role_assignments (
  user_id text not null references users(id),
  role_id text not null references roles(id),
  application_id text,
  assigned_at timestamptz not null default now(),
  assigned_by_type text not null check (assigned_by_type in ('user','api_key','system')),
  assigned_by_id text not null,
  test_mode boolean not null default false,
  primary key (user_id, role_id)
);
create index user_role_assignments_user_app on user_role_assignments (user_id, application_id);
create index user_role_assignments_role on user_role_assignments (role_id);

-- ── Profiles & settings ────────────────────────────────────────────────

create table profiles (
  user_id text primary key references users(id),
  display_name text not null,
  avatar_url text,
  contact_email citext,
  contact_phone text,
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1
);

create table settings (
  user_id text primary key references users(id),
  locale text not null default 'en-US',
  timezone text not null default 'UTC',
  theme text not null default 'system' check (theme in ('light','dark','system')),
  notifications jsonb not null default '{"email": true}',
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1
);

create table app_profiles (
  user_id text not null references users(id),
  application_id text not null references applications(id),
  tenant_id text not null references tenants(id),
  display_handle text check (char_length(display_handle) between 1 and 64),
  custom jsonb not null default '{}',
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  primary key (user_id, application_id)
);

create table app_settings (
  user_id text not null references users(id),
  application_id text not null references applications(id),
  tenant_id text not null references tenants(id),
  overrides jsonb not null default '{}',
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  primary key (user_id, application_id)
);

-- ── Audit ──────────────────────────────────────────────────────────────

create table audit_events (
  id text primary key,
  action text not null check (action in (
    'user.created','user.updated','user.email_changed','user.suspended','user.reactivated',
    'user.deleted','user.erasure_requested','user.erasure_cancelled','user.erased',
    'user.password_changed','user.password_reset','user.mfa_enabled','user.mfa_disabled','user.mfa_reset',
    'entitlement.granted','entitlement.disabled','entitlement.revoked','entitlement.expired',
    'entitlement.updated','entitlement.member_scope_changed','entitlement.deleted',
    'role.created','role.updated','role.deleted','role.assigned','role.removed',
    'profile.updated','settings.updated',
    'application.created','application.updated','application.review_status_changed',
    'organization.created','organization.updated','organization.member_invited','organization.member_added',
    'organization.member_removed','organization.member_role_changed',
    'tenant.plan_changed','tenant.subscription_status_changed','tenant.tier_change_requested',
    'tenant.tier_change_request_updated','tenant.tier_changed',
    'api_key.created','api_key.updated','api_key.rotated','api_key.revoked','api_key.restrict_destructive_disabled',
    'webhook.created','webhook.updated','webhook.deleted','webhook.secret_rotated')),
  actor_type text not null check (actor_type in ('user','api_key','system')),
  actor_id text not null,
  target_type text not null,
  target_id text not null,
  target_user_id text,
  application_id text,
  organization_id text,
  tenant_id text,
  before jsonb,
  after jsonb,
  request_id text,
  test_mode boolean not null default false,
  timestamp timestamptz not null default now()
);
create index audit_events_target_user on audit_events (target_user_id, timestamp desc);
create index audit_events_application on audit_events (application_id, timestamp desc);
create index audit_events_actor on audit_events (actor_id, timestamp desc);
create index audit_events_target on audit_events (target_type, target_id, timestamp desc);
create index audit_events_organization on audit_events (organization_id, timestamp desc);
create index audit_events_tenant on audit_events (tenant_id, timestamp desc);
create index audit_events_timestamp on audit_events (timestamp desc, id desc);

-- Append-only (NFR → Audit). The archival and erasure jobs are the only
-- writers after insert; they set app.audit_maintenance = 'on' for their
-- own transaction. Everything else is rejected at the database.
create function substratal_util.audit_events_guard() returns trigger language plpgsql as $$
begin
  if coalesce(current_setting('app.audit_maintenance', true), '') <> 'on' then
    raise exception 'audit_events is append-only' using errcode = '42501';
  end if;
  if tg_op = 'DELETE' then return old; end if;
  return new;
end $$;
create trigger audit_events_append_only before update or delete on audit_events
  for each row execute function substratal_util.audit_events_guard();

-- ── API keys ───────────────────────────────────────────────────────────

create table api_keys (
  id text primary key,
  name text not null,
  scope text not null,
  permissions text[] not null,
  mode text not null check (mode in ('live','test')),
  intended_use text not null default 'service' check (intended_use in ('service','agent')),
  restrict_destructive boolean not null,
  secret_hash text not null unique,
  secret_hint text not null,
  expires_at timestamptz,
  created_by_type text not null check (created_by_type = 'user'),
  created_by_id text not null,
  test_mode boolean not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1,
  last_used_at timestamptz,
  revoked_at timestamptz,
  check (test_mode = (mode = 'test'))
);
create index api_keys_scope on api_keys (scope);

-- ── Webhooks ───────────────────────────────────────────────────────────

create table webhook_subscriptions (
  id text primary key,
  scope text not null,
  tenant_id text references tenants(id),
  url text not null,
  events text[] not null,
  description text,
  api_version text not null,
  secret_ciphertext text not null,
  previous_secret_ciphertext text,
  previous_secret_expires_at timestamptz,
  status text not null default 'healthy' check (status in ('healthy','unhealthy','disabled')),
  consecutive_failures int not null default 0,
  unhealthy_since timestamptz,
  last_delivery_at timestamptz,
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  version integer not null default 1
);
create index webhook_subscriptions_scope on webhook_subscriptions (scope);

create table webhook_events (
  id text primary key,
  type text not null,
  application_id text,
  payload jsonb not null,
  test_mode boolean not null default false,
  created_at timestamptz not null default now(),
  dispatched_at timestamptz
);
create index webhook_events_undispatched on webhook_events (created_at) where dispatched_at is null;

create table webhook_deliveries (
  id text primary key,
  subscription_id text not null references webhook_subscriptions(id) on delete cascade,
  event_id text not null references webhook_events(id),
  attempt int not null,
  status text not null check (status in ('pending','succeeded','failed')),
  response_status int,
  duration_ms int,
  error text,
  next_retry_at timestamptz,
  test_mode boolean not null default false,
  created_at timestamptz not null default now()
);
create index webhook_deliveries_subscription on webhook_deliveries (subscription_id, created_at desc);
create index webhook_deliveries_due on webhook_deliveries (next_retry_at) where status = 'pending';

-- ── Tenancy requests, erasure, Stripe, idempotency ─────────────────────

create table tier_change_requests (
  id text primary key,
  tenant_id text not null references tenants(id),
  from_tier text not null,
  from_region text not null,
  requested_tier text not null check (requested_tier in ('isolated','dedicated_region')),
  requested_region text,
  status text not null default 'pending'
    check (status in ('pending','scheduled','in_progress','completed','cancelled')),
  reason text not null,
  notes text,
  scheduled_for timestamptz,
  requested_by text not null references users(id),
  created_at timestamptz not null default now(),
  started_at timestamptz,
  completed_at timestamptz,
  version integer not null default 1
);
create unique index tier_change_requests_open_key on tier_change_requests (tenant_id)
  where status in ('pending','scheduled','in_progress');

create table erasure_requests (
  user_id text primary key references users(id),
  requested_by_type text not null,
  requested_by_id text not null,
  reason text,
  requested_at timestamptz not null default now(),
  scheduled_for timestamptz not null,
  status text not null default 'scheduled' check (status in ('scheduled','cancelled','completed')),
  completed_at timestamptz
);

create table stripe_events (
  id text primary key,
  type text not null,
  tenant_id text references tenants(id),
  received_at timestamptz not null default now(),
  processed_at timestamptz
);

create table idempotency_keys (
  caller_id text not null,
  idempotency_key text not null check (char_length(idempotency_key) between 1 and 255),
  state text not null check (state in ('in_flight','completed')),
  request_body_hash text not null,
  response_status int,
  response_body jsonb,
  expires_at timestamptz not null,
  primary key (caller_id, idempotency_key)
);

-- Fixed-window request counters behind NFR → Rate limiting. One upsert per
-- request, in the request's own transaction. Pruned nightly.
create table rate_limit_counters (
  bucket text not null,
  window_start timestamptz not null,
  count int not null,
  primary key (bucket, window_start)
);

-- ── Triggers ───────────────────────────────────────────────────────────

do $$
declare t text;
begin
  foreach t in array array['users','organizations','tenants','applications','entitlements','roles',
                           'profiles','settings','app_profiles','app_settings','api_keys','webhook_subscriptions']
  loop
    execute format('create trigger %I before update on %I for each row execute function substratal_util.touch_updated_at()',
                   t || '_touch_updated_at', t);
  end loop;
  foreach t in array array['users','organizations','applications','entitlements','roles','profiles',
                           'settings','app_profiles','app_settings','api_keys','webhook_subscriptions',
                           'tier_change_requests']
  loop
    execute format('create trigger %I before update on %I for each row execute function substratal_util.bump_version()',
                   t || '_bump_version', t);
  end loop;
end $$;

-- ── Row-Level Security: test/live isolation ────────────────────────────
--
-- FORCE applies the policy to the table owner too, which is the role the
-- API connects as. A superuser bypasses RLS regardless, which is why the
-- local Docker setup connects as a non-superuser role (docker/postgres-init.sql).

do $$
declare t text;
begin
  foreach t in array array['users','organizations','organization_memberships','sessions','entitlements',
                           'user_role_assignments','profiles','settings','app_profiles','app_settings',
                           'audit_events','webhook_subscriptions','webhook_events','webhook_deliveries']
  loop
    execute format('alter table %I enable row level security', t);
    execute format('alter table %I force row level security', t);
    execute format('create policy %I on %I using (substratal_util.mode_visible(test_mode)) with check (substratal_util.mode_visible(test_mode))',
                   t || '_mode', t);
  end loop;
end $$;

-- api_keys deliberately has no mode policy: the schema applies it to tables
-- a test-mode credential can write, and an API Key can never create API
-- Keys. test_mode there marks the mode the key acts in, and a live owner
-- manages their Application's test keys alongside its live ones.

-- Roles: live definitions (including the four seed Roles) are shared
-- reference data visible in both modes, the same exception Applications
-- get; Roles a test credential creates are visible only in test mode.
alter table roles enable row level security;
alter table roles force row level security;
create policy roles_mode on roles
  using (not test_mode or substratal_util.mode_visible(test_mode))
  with check (substratal_util.mode_visible(test_mode));

-- ── Seed data the platform can't run without ───────────────────────────

-- The four platform seed Roles (Domain Model → Platform role grants).
insert into roles (id, name, scope, description, permissions, seed) values
  ('role_platform_superadmin', 'superadmin', 'platform', 'Every platform permission.',
   '{users.list,users.manage,entitlements.manage,applications.manage,roles.manage,organizations.manage,billing.manage,billing.refund,audit.view,webhooks.manage,api_keys.manage,tenants.manage}', true),
  ('role_platform_support', 'support', 'platform', 'Customer support.',
   '{users.list,entitlements.manage,audit.view,tenants.manage,billing.manage}', true),
  ('role_platform_billing_admin', 'billing_admin', 'platform', 'Platform billing administration.',
   '{billing.manage,billing.refund,audit.view,users.list}', true),
  ('role_platform_member', 'member', 'platform', 'Default for every User; grants nothing beyond self-service.',
   '{}', true);

-- Substratal's reserved internal account: the owner of Substratal-run
-- "system" Applications (Database Schema → applications). It has no
-- identity, so it can never sign in.
insert into users (id, email, email_verified, status)
  values ('usr_01JAG0SYSTEM00000000000000', 'system@substratal.invalid', true, 'active');

-- Its Tenant: Substratal's own Applications aren't a Starter customer, so
-- they aren't held to Starter's 1-Application cap.
insert into tenants (id, owner_type, owner_user_id, plan)
  values ('tnt_01JAG1SYSTEM00000000000000', 'user', 'usr_01JAG0SYSTEM00000000000000', 'enterprise');
