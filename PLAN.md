# Build Plan

How to actually build the API this project specifies, in what order, and which document governs each piece. This is a plan for *implementation* — what to build and when. The API itself is specified at **[adron.github.io/substratalapps.com](https://adron.github.io/substratalapps.com/)**; this document doesn't repeat that spec, it sequences building it.

See [README.md](README.md) for why this file — and the others alongside it — live in the project root instead of the docs site.

1. [Before writing code](#before-writing-code)
2. [Build order](#build-order)
3. [Phase 0 — Infrastructure skeleton](#phase-0--infrastructure-skeleton)
4. [Phase 1 — MVP](#phase-1--mvp)
5. [Phase 2 — Per-app customization & trust](#phase-2--per-app-customization--trust)
6. [Phase 3 — Marketplace & scale features](#phase-3--marketplace--scale-features)
7. [Cross-cutting, build throughout](#cross-cutting-build-throughout)
8. [What's still open, and what it blocks](#whats-still-open-and-what-it-blocks)

---

## Before writing code

Read, in this order — each is short, and skipping one tends to show up as a wrong assumption two phases later:

1. **[Getting Started](https://adron.github.io/substratalapps.com/getting-started/)** and **[Domain Model](https://adron.github.io/substratalapps.com/domain-model/)** — the nine-ish entities and how they relate. Everything else assumes this.
2. **[Access Control](https://adron.github.io/substratalapps.com/access-control/)** — the one algorithm (`allow(user, application, permission)`) every endpoint ultimately defers to.
3. **[Database Schema](https://adron.github.io/substratalapps.com/domain-model/database-schema/)** — Postgres types, constraints, indexes, Row-Level Security. This is the literal starting migration.
4. **This project's own [DECISIONS](https://adron.github.io/substratalapps.com/decisions/) status** — do not start building against an entity or flow whose underlying decision is still 🟡 Open without checking whether it changed since this was written.
5. **[DEPLOYMENT.md](DEPLOYMENT.md)** — where it all runs, and the infrastructure build checklist that interleaves with the phases below.

## Build order

The short version: **infrastructure skeleton, then the access-control spine, then everything else radiates outward from Entitlement.** Every phase below is buildable and demoable on its own — this isn't "build the whole schema, then wire up endpoints," it's vertical slices that happen to accumulate into the full spec.

## Phase 0 — Infrastructure skeleton

Before any endpoint handler exists. Maps directly to [DEPLOYMENT.md → Build checklist](DEPLOYMENT.md#build-checklist) steps 1–4:

- [ ] AWS account, `us-east-1`, under Organizations — [DEPLOYMENT.md → AWS account & region](DEPLOYMENT.md#aws-account--region).
- [ ] **AWS Budgets + Cost Anomaly Detection first**, before any billable resource — [DEPLOYMENT.md → Cost guardrails](DEPLOYMENT.md#cost-guardrails). Not optional, not step 2.
- [ ] Aurora Serverless v2 cluster, Data API enabled, Secrets Manager credential.
- [ ] The local-dev repository/data-access interface described in [DEPLOYMENT.md → Local development](DEPLOYMENT.md#local-development), *before* the first table is written against it — retrofitting this seam after business logic exists is real rework, building it first is not.
- [ ] CI running the [Testing strategy](https://adron.github.io/substratalapps.com/non-functional-requirements/#testing-strategy) categories (contract tests against `openapi.yaml`, unit tests on `effective_permissions`, integration tests against local Postgres) against an empty schema — so the pipeline exists before there's much to test, not retrofitted.

**Exit criteria:** `GET /v1/health` deployed and returning `200` through the real Lambda → API Gateway → (nothing else yet) path. Proves the skeleton, not the product.

## Phase 1 — MVP

Matches the [Roadmap → MVP](https://adron.github.io/substratalapps.com/roadmap/#mvp) feature scope, sequenced for implementation:

1. **`users`, `profiles`, `settings` tables + [Users](https://adron.github.io/substratalapps.com/api-reference/users/) and [Profiles](https://adron.github.io/substratalapps.com/api-reference/profiles/)/[Settings](https://adron.github.io/substratalapps.com/api-reference/settings/) (global only) endpoints.** `POST /v1/users` first specifically — per [Non-Functional Requirements → Transaction boundaries](https://adron.github.io/substratalapps.com/non-functional-requirements/#transaction-boundaries), its "also creates a Profile, Settings, and default Role" behavior is one transaction; build it as one from the start rather than three calls glued together later.
2. **Platform Roles & the [permission catalog](https://adron.github.io/substratalapps.com/domain-model/roles-and-permissions/#platform-permission-catalog)** — seed the `superadmin`/`support`/`billing_admin`/`member` rows. This is also literally how the first real `superadmin` gets into the system — see [Non-Functional Requirements → Bootstrapping](https://adron.github.io/substratalapps.com/non-functional-requirements/#authentication): seeded directly, not through the API.
3. **`applications` table + [Applications](https://adron.github.io/substratalapps.com/api-reference/applications/) (list/fetch; writes admin-only).**
4. **`entitlements` table + [Entitlements](https://adron.github.io/substratalapps.com/api-reference/entitlements/), especially the `PATCH` toggle.** This is the feature the rest of the MVP exists to support — see [Access Control](https://adron.github.io/substratalapps.com/access-control/) for why Entitlement and Role are evaluated as independent axes, which should shape the authorization-check code from the first line, not get refactored in later.
5. **`audit_events` table, written transactionally alongside every Entitlement/Role write from step 2 onward** — not bolted on after. See [Non-Functional Requirements → Transaction boundaries](https://adron.github.io/substratalapps.com/non-functional-requirements/#transaction-boundaries): an Audit Event that doesn't correspond to a committed change is worse than no Audit Event, which is only true if it's written in the same transaction from day one.
6. **`api_keys` table + [API Keys](https://adron.github.io/substratalapps.com/api-reference/api-keys/).** Needed before any service-to-service caller (including your own billing webhook handler, Phase 2) can authenticate.

**Exit criteria:** the [Quickstart](https://adron.github.io/substratalapps.com/quickstart/) walkthrough runs end to end against the real deployment — create a user, grant an app, check access, turn it off. This is the MVP's own acceptance test, already written.

## Phase 2 — Per-app customization & trust

Matches [Roadmap → Phase 2](https://adron.github.io/substratalapps.com/roadmap/#phase-2):

1. **App-scoped Roles** — extend the Phase 1 Role/permission machinery with `scope: <application_id>` and `available_app_roles` on Application. Not a new system, the same tables with a column that was already reserved for this.
2. **`app_profiles`/`app_settings` tables**, with the [typed generated-column mechanism](https://adron.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-generated-columns-over-jsonb) for declared `settings_schema` fields.
3. **[Webhooks](https://adron.github.io/substratalapps.com/api-reference/webhooks/)** — delivery queue, signing, the retry/backoff schedule. This is also where [Decision #6](https://adron.github.io/substratalapps.com/decisions/#6-session-model-for-revocation)'s **required** immediate-revocation integration becomes real: an Application can't actually meet that requirement until this ships, so treat Phase 2 webhooks as a hard prerequisite for telling any real Application developer their integration is compliant, not a nice-to-have.
4. **Live introspection** (`effective-permissions`) — the other half of the [Trust Model](https://adron.github.io/substratalapps.com/trust-model/), alongside the launch JWT from Phase 1's Auth work.
5. **[Organizations](https://adron.github.io/substratalapps.com/api-reference/organizations/)** — `organizations`, `organization_memberships`, org-wide Entitlements with `member_scope`/`member_overrides`. Pulled forward from a hypothetical later phase per [Decisions #2](https://adron.github.io/substratalapps.com/decisions/#2-organizations) — both individual and team end users are expected from early on.
6. **[Tenancy](https://adron.github.io/substratalapps.com/domain-model/tenancy/)** — `tenants` table, the `shared` tier (free, automatic, every Application owner gets one). `isolated`/`dedicated_region` provisioning mechanics ([DEPLOYMENT.md → Tenancy tiers](DEPLOYMENT.md#tenancy-tiers--where-they-run)) can lag slightly behind the schema, since they're support-run and gated by an actual customer need, not a launch-day requirement.
7. **Stripe Billing** — [DEPLOYMENT.md → Stripe Billing](DEPLOYMENT.md#stripe-billing) in full: the `stripe_customer_id`/`stripe_subscription_id`/`subscription_status` columns, the webhook handler, Products/Prices for Starter/Team/Enterprise in Stripe test mode first. This is what makes [Pricing](https://adron.github.io/substratalapps.com/pricing/) real rather than aspirational — build it alongside Tenancy, not after, since `plan` lives on the same `tenants` row.
8. **The [MCP server](https://adron.github.io/substratalapps.com/mcp-server/)** — generated from `openapi.yaml`'s `operationId`s, per [DEPLOYMENT.md → MCP server](DEPLOYMENT.md#mcp-server). Natural to build once the REST surface it wraps is otherwise stable, late in this phase rather than early.

**Exit criteria:** a real third-party-shaped integration test — an app that isn't Substratal's own — can complete the native (PKCE) *and* web launch flows, receive a webhook, call introspection, and have its own `settings_schema` validated and typed on write.

## Phase 3 — Marketplace & scale features

Matches [Roadmap → Phase 3](https://adron.github.io/substratalapps.com/roadmap/#phase-3) — explicitly **not** built speculatively ahead of the triggers named in [DEPLOYMENT.md → Scale-out](DEPLOYMENT.md#scale-out) and [Decisions #9](https://adron.github.io/substratalapps.com/decisions/#9-app-developerpublisher-model):

- Self-service Application registration + review queue (`review_status: pending_review` already exists in the schema for exactly this; the review *process* doesn't yet).
- SCIM-style provisioning.
- Richer audit/compliance views (filtering, export) — driven by actual [Compliance](https://adron.github.io/substratalapps.com/compliance/) needs (a SOC 2 audit, a customer's data-export request), not built ahead of either.

## Cross-cutting, build throughout

Not a phase — these apply from Phase 0 onward and should never be "added later":

- **[Access Control](https://adron.github.io/substratalapps.com/access-control/)'s permission checks**, on every endpoint, from the first one. Retrofitting authorization onto endpoints built "open" is how it gets missed somewhere.
- **[Conventions](https://adron.github.io/substratalapps.com/api-reference/conventions/)'s error shape, pagination, idempotency, and `me` self-addressing** — pick these once, in Phase 1, as shared middleware/helpers. Every later endpoint should get them for free, not reimplement them.
- **Contract tests against `openapi.yaml`** — every endpoint, the day it's built, not batched at the end of a phase.
- **[Changelog](https://adron.github.io/substratalapps.com/changelog/) and this project's [DECISIONS.md](https://adron.github.io/substratalapps.com/decisions/)** stay live documents during implementation, the same way they were during spec-writing — an implementation detail that contradicts or resolves something in either belongs there immediately, not in a end-of-project cleanup pass.

## What's still open, and what it blocks

See [DECISIONS.md](https://adron.github.io/substratalapps.com/decisions/) for the full, current list. As of this writing, the one still-open item with real scope impact:

- **[Decision #1 — Identity provider](https://adron.github.io/substratalapps.com/decisions/#1-identity-provider).** Native auth, SSO delegation (Auth0/WorkOS/Cognito/etc.), or both simultaneously — still being clarified. This blocks finalizing `users.auth`'s shape and the Auth endpoints' request/response contracts specifically; it does **not** block Phase 0 or the non-auth parts of Phase 1 (Entitlements, Applications, Roles can all be built and tested against a stub auth layer). Don't let this one open decision stall everything else.
