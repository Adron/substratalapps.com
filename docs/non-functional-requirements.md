---
layout: default
title: Non-Functional Requirements
nav_order: 10
---

# Non-Functional Requirements
{: .no_toc }

1. TOC
{: toc }

---

## Authentication

- **User-facing login:** OAuth2/OIDC. Whether the hub is its own identity provider or delegates to one (Auth0, Clerk, WorkOS, Cognito, …) is open — see [Decisions](../decisions/#1-identity-provider).
- **Service-to-service:** billing → hub, app → hub, and any other backend caller authenticates with scoped API keys or mTLS, not user credentials.
- **MFA:** supported at the identity-provider layer; not re-implemented in this API.
- **Bootstrapping:** the very first `superadmin` cannot be created through the public API — nothing can call `POST /v1/users/{id}/roles/{roleId}` with `roles.manage` before a `superadmin` exists to grant it. It's seeded directly against the database (or via a one-time, non-API admin CLI command) as part of standing up a new environment, not specified as an endpoint.
- **MCP:** the [MCP Server](../mcp-server/) is not a separate auth surface — it carries and forwards the same Bearer credential (user token or API Key) as any other caller. No new rule in this section applies to it that doesn't already apply here.

## CORS

`GET` endpoints intended for direct browser use by a future first-party UI (the catalog, a user's own entitlements/profile/settings via `me`) allow that UI's origin. Admin and service-to-service endpoints do not allow browser-origin requests at all — an admin console calls through its own backend, not directly from the browser with a platform API key. The exact allow-listed origin(s) are an environment-level config value, not part of this spec.

## Authorization enforcement point

The hub is the **only** writer of Entitlement and Role state. Apps read (via JWT claim, introspection, or webhook) — they never maintain a parallel copy they write to independently. If an app needs a role concept finer than what [App Roles](../domain-model/roles-and-permissions/) supports, that's a sign the app needs a new permission defined in its catalog entry, not a local workaround.

## Multi-tenancy

Two independent axes, both enforced the same way — **Postgres Row-Level Security policies**, not application-code `WHERE` clauses a future query can forget to add — but answering different questions, and easy to conflate if read too quickly:

- **`organization_id`** — team/seat grouping. Every record that can belong to an [Organization](../domain-model/users-and-organizations/#organization) is scoped by it, keyed off a session variable the API layer sets per request (`SET LOCAL app.current_org_id = ...`). This matters even before Organizations are customer-facing everywhere (see [Decisions](../decisions/#2-organizations)) — the column and the policy exist and are enforced from day one so neither is a migration later.
- **`tenant_id`** — infrastructure placement. Every record scoped to one [Application](../domain-model/applications/) carries it, denormalized from that Application's own `tenant_id` (see [Tenancy](../domain-model/tenancy/)), keyed off a second session variable (`SET LOCAL app.current_tenant_id = ...`). For a `shared`-tier [Tenant](../domain-model/tenancy/#tiers) this policy is the *only* isolation in place; for `isolated`/`dedicated_region`, the row additionally never lives on the same physical cluster as another Tenant's rows at all — the RLS policy and the physical placement are deliberately redundant, not an either/or.

A query that omits either session variable should fail closed (return nothing) by policy default, not fail open. See [Decisions → Tenant vs. Organization](../decisions/#11-tenant-vs-organization) for why these are two columns and two policies, not one.

## Concurrency control

Entitlement, Role assignment, and Settings writes can race — two support agents acting on the same user, or a webhook retry landing alongside a manual edit. Mutating endpoints on these resources accept an `If-Match` header carrying the resource's current version (returned as an `ETag` on every `GET`); a write with a stale or missing `ETag` where one was expected returns `409` with `code: "version_conflict"` rather than silently applying a last-write-wins update. Endpoints where this matters most: `PATCH /v1/entitlements/{id}`, `PATCH /v1/users/{id}/apps/{appId}/settings`, `POST`/`DELETE` on role assignments.

## Transaction boundaries

Any write described as "also creates" or "also assigns" elsewhere in this spec — most notably `POST /v1/users` creating a default Profile, Settings, and `member` Role assignment in one call — is one database transaction. A partial failure (e.g. the Role assignment insert fails) rolls back the whole call; the client sees a single error, not a half-created User. The same rule applies to `PATCH /v1/entitlements/{id}` and its [Audit Event](../domain-model/orders-and-audit/#audit-event): the state change and the audit record are written together or not at all — an Audit Event that doesn't actually correspond to a committed change is worse than no Audit Event.

## Audit

Every write to an Entitlement, a Role assignment, or an admin-initiated change to a user's Profile or Settings produces an immutable [Audit Event](../domain-model/orders-and-audit/): actor, action, target, before/after snapshot, timestamp. Audit events are never deleted or edited, including when the record they describe is later deleted.

## Idempotency & retries

Billing webhooks retry. Admin tooling double-clicks. Any mutating endpoint that creates or transitions an Entitlement or Order-linked record **must** accept an `Idempotency-Key` header and return the original result on a repeated key rather than creating a duplicate. See [Conventions](../api-reference/conventions/#idempotency).

## Rate limiting

Per-API-key token bucket, returning `429` with a `Retry-After` header on exhaustion:

| Scope | Limit |
|---|---|
| Default, all endpoints | 100 requests/minute |
| `POST /v1/entitlements`, `POST /v1/users` | 20 requests/minute — tighter, since these are the endpoints scripted abuse would hit first |
| `GET /v1/users/{id}/applications/{appId}/effective-permissions` | 300 requests/minute — expected to be called on a hot path by downstream apps (see [Trust Model](../trust-model/)), so it's deliberately not bottlenecked at the default rate |

These are starting defaults, not a promise — tune them against real traffic once the API is live, and raise per-key limits for high-volume service integrations (billing) rather than exempting them from limiting entirely. A call made through the [MCP Server](../mcp-server/) counts against the same bucket as the REST call it translates to — there's no separate "MCP traffic" dimension, since it's the same credential hitting the same underlying endpoint.

## Versioning

- `/v1` now. Additive, backward-compatible changes (new optional fields, new endpoints) ship without a version bump.
- A breaking change gets a new version prefix. The old version keeps working for a minimum 6-month deprecation window, announced in the [Changelog](../changelog/) the day the replacement ships, with a `Deprecation` and `Sunset` response header (RFC 8594) added to every response the old version serves from that point on.
- Webhook payload versions are versioned independently of the URL version, since webhook consumers can't negotiate a version the way a request-time client can.
- The [MCP Server](../mcp-server/)'s protocol version (negotiated per MCP's own date-based scheme during `initialize`) is likewise independent of `/v1` — a protocol-version bump there doesn't imply a `/v2` here, and vice versa.
- **Enums can grow new values without a version bump.** `EntitlementStatus`, `UserStatus`, and similar closed-looking lists in [openapi.yaml](../openapi.yaml) are allowed to gain new members as additive, non-breaking changes. Clients — and any implementation — must treat an unrecognized enum value as "handle generically / no special case," never as an error to reject the response over. This is a contract, not just a suggestion: don't write a `switch` with no default case against any enum in this spec.

## Request tracing

Every response carries an `X-Request-Id` (server-generated if the caller didn't send one in the request). Log it at every layer a request touches. This is what turns "a user says something failed around 2pm" into a specific, searchable trace — cheap to add at the start, expensive to retrofit once there's production traffic to correlate.

## Testing strategy

- **Contract tests** validate the implementation's actual responses against [openapi.yaml](../openapi.yaml) — this is what keeps the machine-readable spec from silently drifting from reality, which is the normal failure mode for hand-maintained API docs.
- **Unit tests** on the [Access Control](../access-control/) resolution logic (`effective_permissions`) specifically — it's the one piece of logic every single request depends on, and it's pure/deterministic enough to be cheap to test exhaustively (every combination of entitlement status × platform role × app role).
- **Integration tests** run against a real local Postgres (see [Deployment Architecture → Local development](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)), not a mocked data layer — the Row-Level Security policies in [Multi-tenancy](#multi-tenancy) are exactly the kind of thing a mock would let silently pass while actually being broken.
- **Seed/fixture data** for local and test environments should be generated from the same request/response examples already in the [API Reference](../api-reference/) — one source of realistic data, not a second hand-maintained copy that drifts from the docs.

## Data retention

- Audit Events: retained indefinitely (compliance system of record) — see [Audit log lifecycle](#audit-log-lifecycle) immediately below for how that stays true without every plan paying for the same amount of hot, directly-queryable storage.
- Disabled/revoked Entitlements: retained, not deleted — re-enabling or investigating a dispute depends on the history.
- Deleted Users: soft-deleted first (status transition), with a separate, deliberate hard-delete process for right-to-erasure requests — see [Hard-delete cascade](#hard-delete-cascade) immediately below for what that process actually does, not just that it exists.

### Audit log lifecycle

"Retained indefinitely" and [Pricing](../pricing/#enforcement)'s plan-tiered "audit log hot-storage window" (30 days Starter / 1 year Team / negotiated Enterprise) are two different axes, not a contradiction:

1. **Hot storage.** A freshly-written Audit Event lives in the primary `audit_events` table (Aurora Postgres) with its full `before`/`after` snapshot — queryable via [API Reference → Audit](../api-reference/audit/) exactly as specified elsewhere on this page.
2. **Archival, at the end of the plan's hot window.** A scheduled job (see [Deployment Architecture → Audit log archival](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) moves every Audit Event older than the owning Tenant's hot window out of the hot table: `action`, `timestamp`, `actor_user_id`, and every other shape field move to cheap, indefinite cold storage; the `before`/`after` snapshot values — the one place the row could hold personal data — are dropped at this point rather than carried forward, the same redaction [Hard-delete cascade](#hard-delete-cascade) step 4 already performs for a deleted User, just applied uniformly by age instead of by erasure request.
3. **Cold storage, forever.** The shape-only archive is never deleted, satisfying "retained indefinitely" literally — what's indefinite is the record that something happened, not a guarantee that its full payload stays warm (or stays at all) past the hot window. Retrieving an archived event for a dispute or investigation is a deliberately manual, support-mediated process, not a live API path — see [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) for the actual retrieval SLA.

This is also why the hot/cold split doesn't weaken [Audit trail vs. erasure tension](../compliance/#gdpr-and-ccpa--build-for-it-now): a snapshot value is gone (not just hidden) the moment it leaves hot storage, on every plan, regardless of whether a User ever requests erasure.

### Hard-delete cascade

Triggered by a verified erasure request, completing well within GDPR Article 12(3)'s 30-day ceiling — soft-delete (the `status: deleted` transition) is immediate; this is the follow-up. Runs in this order, each step committed before the next:

1. Delete `password_hash` and `mfa_secret` from every [UserIdentity](../domain-model/users-and-organizations/#useridentity) row the User holds — the credential itself, not the row (the row's `method`/`created_at` stays, as a record that an identity of that kind existed).
2. Delete the User's [Profile](../domain-model/profiles/) row outright.
3. For every [AppProfile](../domain-model/profiles/#appprofile)/[AppSettings](../domain-model/settings/#appsettings) row the User holds: scrub personally-identifying values out of `custom`/`overrides` (the Application's own `settings_schema` is consulted to know which declared fields are PII-shaped — a free-text `display_handle` or similar, not a structural `boolean` preference), rather than deleting the row, since an Application may have a legitimate reason to know an entitlement-shaped record existed.
4. Redact `before`/`after` snapshot values (not the whole row) on every [Audit Event](../domain-model/orders-and-audit/#audit-event) where this User is `target_user_id` — preserves `action`/`timestamp`/`actor_user_id` (the *shape* of what happened) while removing the one place a deleted field's value could otherwise still be read back.
5. Leave [Entitlement](../domain-model/entitlements/) and [Order](../domain-model/orders-and-audit/) rows in place, pseudonymized by the cascade above having already removed the identifying data they'd otherwise join against — retained under a legitimate-interest basis (financial/business records) distinct from, and not overridden by, the erasure right.

`GET /v1/users/{id}/export` (see [API Reference → Users](../api-reference/users/)) reads from the same tables this cascade writes to — the two are deliberately symmetric: export answers "what do you have on me," erasure answers "stop having it," and both need to agree on what "it" actually comprises.

## Availability expectations on the trust model

Because downstream apps verify access against this API (see [Trust Model](../trust-model/)), this API's availability is a dependency of every app in the catalog, not just the hub dashboard. JWT verification keeps apps functioning through a brief hub outage; live introspection calls do not. This is a reason to lean on JWTs for routine checks and reserve introspection for genuinely sensitive actions, independent of the latency tradeoff discussed on that page.
