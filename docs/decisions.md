---
layout: default
title: Decisions
nav_order: 14
---

# Decisions
{: .no_toc }

The open questions this spec currently depends on. This page is a log, not a one-time list — update a row's status in place as it gets resolved, and add new rows as new forks appear. Don't let an answer live only in a chat thread; if it's decided, it belongs here.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

| # | Decision | Status |
|---|---|---|
| 1 | [Identity provider](#1-identity-provider) | 🟡 Open |
| 2 | [Organizations](#2-organizations) | 🟢 Leaning resolved |
| 3 | [Downstream app architecture](#3-downstream-app-architecture) | 🟡 Open |
| 4 | [Billing system of record](#4-billing-system-of-record) | 🟢 Mostly resolved |
| 5 | [Settings schema ownership](#5-settings-schema-ownership) | 🟡 Open |
| 6 | [Session model for revocation](#6-session-model-for-revocation) | 🟡 Open |
| 7 | [AWS account and region](#7-aws-account-and-region) | 🟡 Open |
| 8 | [Storage primitive scope](#8-storage-primitive-scope) | 🟢 Resolved |
| 9 | [App developer/publisher model](#9-app-developerpublisher-model) | 🟡 Open |
| 10 | [Compliance scope](#10-compliance-scope) | 🟡 Open |
| 11 | [Tenant vs. Organization](#11-tenant-vs-organization) | 🟢 Resolved |
| 12 | [Tenancy tiers & dedicated infrastructure](#12-tenancy-tiers--dedicated-infrastructure) | 🟢 Resolved |
| 13 | [Organization-vs-User entitlement precedence](#13-organization-vs-user-entitlement-precedence) | 🟢 Resolved |
| 14 | [MCP server authorization scope](#14-mcp-server-authorization-scope) | 🟡 Open |
| 15 | [Platform subscription billing processor](#15-platform-subscription-billing-processor) | 🟡 Open |

---

## 1. Identity provider

Build auth in-house, or delegate to an IdP (Auth0, Clerk, WorkOS, Cognito, …)?

This changes what [`User.auth`](../domain-model/users-and-organizations/) actually stores (a password hash owned here, vs. a foreign subject ID), and how the JWT in the [Trust Model](../trust-model/) gets issued — self-signed by the hub either way, but the login step in front of it looks very different.

**Leans toward:** delegating. Auth is a solved, security-sensitive problem; building it in-house is rarely where the differentiated value of this hub lives.

## 2. Organizations

~~Is multi-seat/team access a day-one requirement~~ — **resolved: both individual and team/business end users are expected**, so Organizations isn't a someday-maybe feature. It's pulled forward to [Phase 2](../roadmap/#phase-2) rather than [Phase 3](../roadmap/#phase-3) — not the MVP itself (the first dozens of users are expected to be mostly individual early adopters), but needed well before the 10x/100x growth horizon in [Deployment Architecture → Growth trajectory](../deployment-architecture/#growth-trajectory) hits, where team accounts are assumed to matter.

`organization_id` being load-bearing in the schema from day one (per [Non-Functional Requirements](../non-functional-requirements/#multi-tenancy)) was the right call regardless of timing — this just confirms it wasn't a hedge against a hypothetical.

Note this `organization_id` (end-user team/seat grouping, scoped *within* one Application) is a different axis from the infrastructure-placement `tenant_id` introduced in [Decision #11](#11-tenant-vs-organization) — don't conflate the two when reading [Database Schema](../domain-model/database-schema/).

## 3. Downstream app architecture

The single biggest fork in the API's shape. Two real options:

- **Apps are separately hosted services** that need SSO + token verification — the assumption this entire site currently builds on (see [Trust Model](../trust-model/)).
- **Apps are modules/iframes inside one deployment**, where access control could live entirely in the hub's own session and nothing in [Trust Model](../trust-model/) is needed at all.

**This needs to be resolved before the API Reference pages are treated as final** — everything about JWT issuance, introspection, and webhooks in [Trust Model](../trust-model/) assumes the first option.

## 4. Billing system of record

**Mostly resolved:** Substratal Apps is not a payment processor or marketplace billing engine — each Application's developer owns the billing relationship with their own end users (their own Stripe or equivalent), and simply calls this API's [Entitlements](../api-reference/entitlements/) endpoints to reflect the outcome. `Order`/`order_id` is reference metadata the developer supplies for their own reconciliation, not a record this API's own billing system pushes webhooks about — there is no "this API's billing processor" to choose.

**Still open:** once the eventual UI (see [Home](../)) exists and end users can discover apps through a Substratal-run marketplace surface, does payment ever flow *through* Substratal on a developer's behalf (Apple App Store-style), or does every purchase always happen on the developer's own site even when discovery happens here? This doesn't block the API as specified — [Workflows → Purchase → access](../workflows/#purchase--access) already models the developer-initiated grant correctly either way — but it's a real product decision for the UI phase, not an API concern today.

## 5. Settings schema ownership

Does each Application register its own JSON Schema with the hub (what [`AppSettings`](../domain-model/settings/) currently assumes), or does the hub stay fully opaque to app settings and just store/return a blob with no server-side validation?

**Leans toward:** schema-on-file with the hub. Centralized validation is why `GET /v1/users/{id}/apps/{appId}/settings` can promise a resolved, valid object instead of "whatever blob was last written."

## 6. Session model for revocation

How fast must "turn off this user's access" take effect inside an already-open app session?

- **Immediately** — requires apps to subscribe to webhooks and force-kill live sessions.
- **By next login / token refresh** — much simpler for apps to implement, weaker guarantee.

See [Trust Model → How fast does revocation need to land?](../trust-model/#how-fast-does-revocation-need-to-land) for the tradeoff table. This likely doesn't need one global answer — it may be a per-Application setting (some apps are compliance-sensitive enough to need immediate revocation, most aren't) — but that's itself an open sub-decision.

## 7. AWS account and region

Which AWS account hosts this (new, dedicated account vs. an existing one under an AWS Organization) and which region?

Doesn't change anything in [Deployment Architecture](../deployment-architecture/) — every component and cost guardrail there holds regardless of the answer — it only changes where the build checklist's step 1 actually points. A dedicated account is the safer default for billing isolation (a Budget/Cost Anomaly Detection setup on a shared account is easy to mis-scope), and a single-region start (e.g. `us-east-1` or `us-west-2`) is enough until [Scale-out](../deployment-architecture/#scale-out)'s multi-region trigger is actually hit.

## 8. Storage primitive scope

The product pitch names "storage" as one of the five things a developer shouldn't have to build (alongside user, settings, organization, tenancy) — but the spec as written only has two narrow storage primitives: [AppProfile](../domain-model/profiles/#appprofile)'s `custom` field and [AppSettings](../domain-model/settings/#appsettings)' `overrides`, both flat JSON blobs with no server-side structure beyond the Application's own `settings_schema`. Is that actually what "storage" means, or does a developer need something more general — arbitrary collections, file/blob storage — before this product does what it says on the label?

**Resolved: Postgres-backed, and typed where it's declared.** The engine is Postgres (see [Deployment Architecture → Database engine](../deployment-architecture/#database-engine-aws-options-compared)), so the storage primitive follows that directly rather than needing a separate decision:

- **The source of truth stays `jsonb`** — `AppProfile.custom` and `AppSettings.overrides` remain flexible JSON blobs, so a developer never has to pre-declare a migration to store a new field.
- **Every field a developer *has* declared in their `settings_schema` gets a real Postgres type, not just JSON.** The implementation backs each declared schema property with a Postgres [generated column](../domain-model/database-schema/#typed-fields-generated-columns-over-jsonb) (`GENERATED ALWAYS AS (overrides->>'week_start') STORED`, cast to the schema's declared type — `text`, `boolean`, `integer`, `timestamptz`, whatever it specifies) and an index on it. This is what "map to a respective PostgreSQL data type" means concretely: the JSON is where a value *lives*, the generated column is how it's *queried and type-checked* once a developer has told the platform what shape to expect.
- **A general-purpose storage API (arbitrary collections, file/blob storage) is still not in scope today** — the above is enough for the flag/preference/small-record use case every Application needs, and it's the same mechanism regardless of how large that use case grows, since adding a new declared field just adds another generated column rather than requiring a new kind of storage object. Revisit only if a real Application — including the three-plus the platform's own developer is building — hits something a typed JSON field genuinely can't represent (e.g. actual file bytes).

## 9. App developer/publisher model

Third-party developers registering their own Applications is an explicit later phase, not day one (see [Home](../#what-substratal-apps-actually-is)) — but "later" still needs a real shape: self-service submission into [`review_status: pending_review`](../domain-model/applications/), who reviews it and against what criteria, what SLA a developer should expect, and what happens to an already-launched app that gets `suspended` mid-flight (do its existing users' Entitlements stay `active`, or does suspension cascade to them?).

**Leans toward:** `POST /v1/applications` stays platform-admin-only through the [MVP and Phase 2](../roadmap/) — the `owner_user_id`/`owner_organization_id`/`review_status` fields on [Application](../domain-model/applications/) exist now specifically so this doesn't require a breaking schema change when self-service registration actually ships in [Phase 3](../roadmap/#phase-3). The review-queue mechanics themselves (reviewer assignment, SLA, suspension cascade) are unspecified on purpose — designing that process before there's ever been a single real submission to learn from is more likely to guess wrong than to save time.

## 10. Compliance scope

Which of SOC 2, HIPAA, GDPR, and CCPA actually get pursued, and on what timeline?

See [Compliance & Data Protection](../compliance/) for the full recommendation — GDPR/CCPA mechanisms built now (not optional), SOC 2 posture built now with the formal audit deferred until a customer requires it, and HIPAA deliberately not pursued unless and until a healthcare-vertical Application actually wants onto the platform. This row exists to track the one decision that page can't make on its own: confirming that recommendation (or overriding it) is a real legal/business call, not an engineering one.

This page's [Data residency](../compliance/#gdpr-and-ccpa--build-for-it-now) row, previously open, is now resolved by [Decision #12](#12-tenancy-tiers--dedicated-infrastructure) — a customer needing EU residency gets a `dedicated_region` [Tenant](../domain-model/tenancy/), not a platform-wide region change.

## 11. Tenant vs. Organization

Does "tenancy" — named in the product pitch alongside user/settings/organization/storage (see [Home](../#what-substratal-apps-actually-is)) — mean the same thing as [Organization](../domain-model/users-and-organizations/#organization), or is it a separate concept?

**Resolved: separate, and tied to different things.** `Organization` is a domain/grouping object — a company, or a group within a company — used to organize which Users share admin standing and which Applications a group is granted access to as a whole. It carries no infrastructure meaning. `Tenant` is new: the infrastructure-placement and data-isolation boundary, tied to **the subscription** — concretely, to whoever owns an Application's catalog entry (`owner_user_id` or `owner_organization_id`, see [Applications](../domain-model/applications/)), since that's the only subscription relationship Substratal has directly (see [Decision #4](#4-billing-system-of-record): a developer's own end-user billing is their own concern, not this API's).

Practically: a User or Organization can hold membership in many Organizations (even, now, across multiple Tenants — a person building one app and also using a seat on someone else's app), but there is exactly one Tenant governing where a given Application's data physically lives. See [Domain Model → Tenancy](../domain-model/tenancy/) for the full entity and [Decision #13](#13-organization-vs-user-entitlement-precedence) for how `Organization`-level access decisions interact with individual Users — a related but orthogonal question.

## 12. Tenancy tiers & dedicated infrastructure

Some customers (Application owners) want — or need, for compliance — their own dedicated infrastructure rather than the shared Tier 0 database, and some need a specific geographic region for their data. Four sub-questions, all resolved together:

- **How many tiers?** Three: `shared` (default — [Tier 0](../deployment-architecture/#first-deployment-tier-0), logical isolation only), `isolated` (a dedicated Aurora Serverless v2 cluster, same region), `dedicated_region` (a dedicated cluster in a customer-chosen AWS region — real data residency). See [Domain Model → Tenancy](../domain-model/tenancy/) and [Deployment Architecture → Tenancy tiers](../deployment-architecture/#tenancy-tiers--where-they-run).
- **Self-serve or gatekept?** Gatekept by support today — no public API lets a customer trigger their own migration. `tenants.manage` (support/superadmin only) is required even to request a tier change. Automated, customer-initiated tier changes are a later, explicitly revenue-gated step, not a roadmap-phase trigger: it means accepting a real amount of migration risk (a failed cutover, a narrower support safety net) that a human currently absorbs step by step, and that trade only makes sense once the volume of tier-change requests justifies building it.
- **Downtime during a tier change?** A brief, scheduled maintenance window is acceptable — this is support-run and infrequent at current scale, so a snapshot/restore cutover is enough. Zero-downtime (logical replication) migration is a legitimate future upgrade once this is self-serve and frequent enough to need it, not a day-one requirement.
- **Does this reach downstream Applications?** No — tenancy governs where *this API's own* data lives (Entitlements, AppProfile, AppSettings, the relevant Audit Events), never an Application's own separately-hosted infrastructure. An Application's `tenant_id`/`tier`/`region` are exposed as static metadata on the [Tenant](../domain-model/tenancy/) and [Application](../domain-model/applications/) resources, for the owning developer's own benefit — not as a live JWT claim on every request, since placement is set once per Application, not computed per end-user per request the way `effective_permissions` is.

## 13. Organization-vs-User entitlement precedence

When an end-user [Organization](../domain-model/users-and-organizations/#organization) holds an org-wide (`org_seat`) [Entitlement](../domain-model/entitlements/) to an Application, and a member of that Organization also holds (or could hold) their own individual standing for the same app, which wins?

**Resolved**, with one deliberate scoping refinement flagged below:

- **Within one Organization's own grant, the Organization's decision is authoritative.** A member cannot opt themselves in or out of their org's seat grant — see [Entitlements → Org-wide entitlements](../domain-model/entitlements/#org-wide-entitlements-scoping-members-in-or-out) for the `member_scope` mechanism (`all_members` / `allowlist` / `denylist`) that lets an org admin include or exclude specific members.
- **A User's own personal Entitlement to the same Application (purchased or granted independently of any Organization) is a separate, untouched access path.** An Organization's exclusion of a member from its own org-wide grant does not reach into and revoke a personal Entitlement that member holds some other way. This is the one place this resolution departs from a literal "Organization always overrides User" rule — the alternative (an org silently revoking something a member individually holds) creates a real billing/legal defensibility problem ("the company turned off access to something I personally paid for"), and the scoped version below still satisfies the actual goal — an org's decision about its own grant is final — without that side effect. Revisit this if it doesn't match intent.
- **A User's effective access to an Application is the union of every active path**: their own personal Entitlement (if any) OR any Organization they belong to whose grant includes them. Because each Organization's grant is independently evaluated, a User in multiple Organizations (even across different Tenants) never hits a real "Org A says yes, Org B says no" conflict — Org B's answer only ever governs Org B's own grant.
- **Attribution is always surfaced.** Reading a User's entitlement to an app that came from (or was blocked by) an Organization's grant shows `source: org_seat`, the `organization_id`, and whether `member_scope` included or excluded this specific member — so anyone pulling a User's access record can see which Organization is responsible, rather than seeing a bare allow/deny. See [Entitlements](../domain-model/entitlements/#org-wide-entitlements-scoping-members-in-or-out) for the exact shape.

## 14. MCP server authorization scope

[MCP Server](../mcp-server/) deliberately introduces no new authorization model — every tool call carries the same Bearer credential (user token or [API Key](../api-reference/api-keys/)) as the equivalent REST call, and is checked against the exact same permissions. The open question is narrower: **should Substratal recommend — or eventually require — a purpose-scoped class of API Key specifically for agent/MCP callers**, rather than relying on whatever key a human happens to hand their agent?

The case for a narrower default: an LLM deciding *which* tool to call based on a prompt (possibly influenced by untrusted data it has read, e.g. a `disabled_reason` or an `AppProfile.custom` field written by someone else) is a different risk shape than deterministic service code making the same call — not because the platform's own enforcement is any weaker (it isn't; [Access Control](../access-control/) doesn't know or care whether its caller is an agent), but because the *decision to call* a destructive tool at all is now made by something a prompt can influence, where a service integration's call sites are fixed at write time.

**Leans toward:** no new API Key *type* (that would be a parallel, redundant scoping system next to the one that already exists) — instead, operational guidance to scope an agent-facing Key as narrowly as the integration actually needs (read-only `audit.view`/`users.list` for a query-only assistant; `entitlements.manage` only for an assistant that's actually meant to toggle access), plus the [tool annotations](../mcp-server/#tool-annotations--safety) that let a compliant client prompt for confirmation before a `destructiveHint` tool runs. Still open: whether that guidance should harden into something enforced server-side (e.g. a key flag that *disables* destructive operations outright, independent of the permissions it otherwise carries) once there's a real incident or a real customer asking for it — not built speculatively ahead of either.

## 15. Platform subscription billing processor

[Decision #4](#4-billing-system-of-record) resolved who processes payment for a developer's *own* end users (the developer, never this API). It left open the other direction, now a real question with [Pricing](../pricing/) specified: **who processes payment for the platform subscription itself** — the Starter/Team/Enterprise charge an Application owner pays Substratal?

**Leans toward:** Stripe Billing, specifically because adopting it changes nothing already decided — [Decisions → Compliance scope](#10-compliance-scope) already keeps PCI scope minimal by design (card data never touches this API directly either way), and a subscription-billing product used only for Substratal's own direct customer relationship is a far smaller integration than the marketplace-payment-processor role [Decision #4](#4-billing-system-of-record) explicitly ruled out. Not yet built or confirmed — flagged here rather than assumed, since it's the one piece of [Pricing](../pricing/) with no corresponding API surface anywhere in this spec today (no `POST /v1/subscriptions`, no `plan` change endpoint — only the `plan` field on [Tenant](../domain-model/tenancy/#fields) recording the outcome). Building that surface is explicitly deferred until this decision is actually made.
