# Build Plan

How to actually build the API this project specifies, in what order, and which document governs each piece. This is a plan for *implementation* — what to build and when. The API itself is specified at **[compositecode.github.io/substratalapps.com](https://compositecode.github.io/substratalapps.com/)**; this document doesn't repeat that spec, it sequences building it.

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

1. **[Getting Started](https://compositecode.github.io/substratalapps.com/getting-started/)** and **[Domain Model](https://compositecode.github.io/substratalapps.com/domain-model/)** — the nine-ish entities and how they relate. Everything else assumes this.
2. **[Access Control](https://compositecode.github.io/substratalapps.com/access-control/)** — the one algorithm (`allow(user, application, permission)`) every endpoint ultimately defers to.
3. **[Database Schema](https://compositecode.github.io/substratalapps.com/domain-model/database-schema/)** — Postgres types, constraints, indexes, Row-Level Security. This is the literal starting migration.
4. **This project's own [DECISIONS.md](DECISIONS.md)** — do not start building against an entity or flow whose underlying decision is still 🟡 awaiting confirmation without checking whether it changed since this was written. Resolved decisions live on the spec pages themselves; `DECISIONS.md` only holds questions still waiting on an answer.
5. **[DEPLOYMENT.md](DEPLOYMENT.md)** — where it all runs, and the infrastructure build checklist that interleaves with the phases below.

## Build order

The short version: **infrastructure skeleton, then the access-control spine, then everything else radiates outward from Entitlement.** Every phase below is buildable and demoable on its own — this isn't "build the whole schema, then wire up endpoints," it's vertical slices that happen to accumulate into the full spec.

## Phase 0 — Infrastructure skeleton

Before any endpoint handler exists. Maps directly to [DEPLOYMENT.md → Build checklist](DEPLOYMENT.md#build-checklist) steps 1–4:

- [ ] AWS account, `us-east-1`, under Organizations — [DEPLOYMENT.md → AWS account & region](DEPLOYMENT.md#aws-account--region).
- [ ] **AWS Budgets + Cost Anomaly Detection first**, before any billable resource — [DEPLOYMENT.md → Cost guardrails](DEPLOYMENT.md#cost-guardrails). Not optional, not step 2.
- [ ] Aurora Serverless v2 cluster, Data API enabled, Secrets Manager credential.
- [x] The local-dev repository/data-access interface described in [DEPLOYMENT.md → Local development](DEPLOYMENT.md#local-development), *before* the first table is written against it — retrofitting this seam after business logic exists is real rework, building it first is not.
- [x] CI running the [Testing strategy](https://compositecode.github.io/substratalapps.com/non-functional-requirements/#testing-strategy) categories (contract tests against `openapi.yaml`, unit tests on `effective_permissions`, integration tests against local Postgres) against an empty schema — so the pipeline exists before there's much to test, not retrofitted.

**Exit criteria:** `GET /v1/health` deployed and returning `200` through the real Lambda → API Gateway → (nothing else yet) path. Proves the skeleton, not the product.

## Phase 1 — MVP

Matches the [Roadmap → MVP](https://compositecode.github.io/substratalapps.com/roadmap/#mvp) feature scope, sequenced for implementation:

1. **`users`, `user_identities`, `sessions`, `refresh_tokens`, `auth_tokens`, `profiles`, `settings` tables + native [Auth](https://compositecode.github.io/substratalapps.com/api-reference/auth/) (signup, login + MFA challenge, refresh with reuse detection, logout, TOTP, password reset/change, email verification, invitations, JWKS via KMS, SES email, and **`POST /v1/auth/app-tokens`**, without which no Application can log anyone in) and [Users](https://compositecode.github.io/substratalapps.com/api-reference/users/)/[Profiles](https://compositecode.github.io/substratalapps.com/api-reference/profiles/)/[Settings](https://compositecode.github.io/substratalapps.com/api-reference/settings/) (global only) endpoints.** `POST /v1/users` first specifically — per [Non-Functional Requirements → Transaction boundaries](https://compositecode.github.io/substratalapps.com/non-functional-requirements/#transaction-boundaries), its "also creates a Profile, Settings, and default Role" behavior is one transaction; build it as one from the start rather than three calls glued together later. Build `user_identities` as the multi-method table it's specified as from day one (see [Decisions #1](https://compositecode.github.io/substratalapps.com/api-reference/auth/#native-auth-and-per-organization-sso)) even though only `method: 'password'` rows exist until Phase 2+ — `sso_connections` and the broker integration are explicitly deferred, not this phase's problem.
2. **Platform Roles & the [permission catalog](https://compositecode.github.io/substratalapps.com/domain-model/roles-and-permissions/#platform-permission-catalog)** — seed the `superadmin`/`support`/`billing_admin`/`member` rows. This is also literally how the first real `superadmin` gets into the system — see [Non-Functional Requirements → Bootstrapping](https://compositecode.github.io/substratalapps.com/non-functional-requirements/#authentication): seeded directly, not through the API.
3. **`applications` table + [Applications](https://compositecode.github.io/substratalapps.com/api-reference/applications/) (list/fetch; writes admin-only).**
4. **`entitlements` table + [Entitlements](https://compositecode.github.io/substratalapps.com/api-reference/entitlements/), especially the `PATCH` toggle.** This is the feature the rest of the MVP exists to support — see [Access Control](https://compositecode.github.io/substratalapps.com/access-control/) for why Entitlement and Role are evaluated as independent axes, which should shape the authorization-check code from the first line, not get refactored in later.
5. **`audit_events` table, written transactionally alongside every Entitlement/Role write from step 2 onward** — not bolted on after. See [Non-Functional Requirements → Transaction boundaries](https://compositecode.github.io/substratalapps.com/non-functional-requirements/#transaction-boundaries): an Audit Event that doesn't correspond to a committed change is worse than no Audit Event, which is only true if it's written in the same transaction from day one.
6. **`api_keys` table + [API Keys](https://compositecode.github.io/substratalapps.com/api-reference/api-keys/), including app-confined permissions.** An Application's backend needs an app-scoped key with confined `entitlements.manage` to reflect its own billing outcomes. That's the developer's one required integration besides login.

**Exit criteria:** the [Quickstart](https://compositecode.github.io/substratalapps.com/quickstart/) walkthrough runs end to end against the real deployment — create a user, grant an app, check access, turn it off. This is the MVP's own acceptance test, already written.

## Phase 2 — Per-app customization & trust

Matches [Roadmap → Phase 2](https://compositecode.github.io/substratalapps.com/roadmap/#phase-2):

1. **App-scoped Roles** — extend the Phase 1 Role/permission machinery with `scope: <application_id>` and `available_app_roles` on Application. Not a new system, the same tables with a column that was already reserved for this.
2. **`app_profiles`/`app_settings` tables**, with the [typed generated-column mechanism](https://compositecode.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-per-application-views-and-expression-indexes) for declared `settings_schema` fields.
3. **[Webhooks](https://compositecode.github.io/substratalapps.com/api-reference/webhooks/)** — delivery queue, signing, the retry/backoff schedule. This is also where the [Trust Model's](https://compositecode.github.io/substratalapps.com/trust-model/#how-fast-does-revocation-need-to-land) **required** immediate-revocation integration becomes real: an Application can't actually meet that requirement until this ships, so treat Phase 2 webhooks as a hard prerequisite for telling any real Application developer their integration is compliant, not a nice-to-have.
4. **Live introspection** (`effective-permissions`) — the other half of the [Trust Model](https://compositecode.github.io/substratalapps.com/trust-model/), alongside the app token from Phase 1's Auth work.
5. **[Organizations](https://compositecode.github.io/substratalapps.com/api-reference/organizations/)** — `organizations`, `organization_memberships`, org-wide Entitlements with `member_scope`/`member_overrides`. Pulled forward from a hypothetical later phase per [Domain Model → Organization](https://compositecode.github.io/substratalapps.com/domain-model/users-and-organizations/#organization) — both individual and team end users are expected from early on.
6. **[Tenancy](https://compositecode.github.io/substratalapps.com/domain-model/tenancy/)** — `tenants` table, the `shared` tier (free, automatic, every Application owner gets one). `isolated`/`dedicated_region` provisioning mechanics ([DEPLOYMENT.md → Tenancy tiers](DEPLOYMENT.md#tenancy-tiers--where-they-run)) can lag slightly behind the schema, since they're support-run and gated by an actual customer need, not a launch-day requirement.
7. **Stripe Billing** — [DEPLOYMENT.md → Stripe Billing](DEPLOYMENT.md#stripe-billing) and [API Reference → Billing](https://compositecode.github.io/substratalapps.com/api-reference/billing/) in full: the `tenants` billing columns, the catalog sync script, the Checkout/Portal endpoints, the webhook handler, the daily seat sync, and restricted-mode enforcement (`402 subscription_required`), in Stripe test mode first. This is what makes [Pricing](https://compositecode.github.io/substratalapps.com/pricing/) real rather than aspirational — build it alongside Tenancy, not after, since `plan` lives on the same `tenants` row.
8. **The [MCP server](https://compositecode.github.io/substratalapps.com/mcp-server/)** — generated from `openapi.yaml`'s `operationId`s, per [DEPLOYMENT.md → MCP server](DEPLOYMENT.md#mcp-server). Natural to build once the REST surface it wraps is otherwise stable, late in this phase rather than early.

**Exit criteria:** a real third-party-shaped integration test, an app that isn't Substratal's own, can sign a user in through the embedded flow, receive `access.revoked` and kill the session, call introspection, and have its own `settings_schema` validated and typed on write. A test-mode Tenant can upgrade to Team through Checkout and lapse back to Starter. The hosted PKCE flow is Phase 3, with the hosted sign-in page.

## Phase 3 — Marketplace & scale features

Matches [Roadmap → Phase 3](https://compositecode.github.io/substratalapps.com/roadmap/#phase-3) — explicitly **not** built speculatively ahead of the triggers named in [DEPLOYMENT.md → Scale-out](DEPLOYMENT.md#scale-out) and [Applications → The review lifecycle](https://compositecode.github.io/substratalapps.com/domain-model/applications/#the-review-lifecycle):

- **SSO broker integration** (`sso_connections` becoming real, an actual WorkOS-or-equivalent wiring behind `POST /v1/auth/sso/{provider}/callback`) — the schema and the route exist from Phase 1 per [Auth → Native auth and per-Organization SSO](https://compositecode.github.io/substratalapps.com/api-reference/auth/#native-auth-and-per-organization-sso), deliberately built in advance precisely so this phase is additive wiring, not a migration. Triggered by a real Organization actually asking for it, not a phase-number default.
- Self-service Application registration + review queue. The *process* is already fully specified — reviewer model (no dedicated assignment, any `applications.manage` holder works the FIFO queue), a 5-business-day policy SLA, the `rejected` status with required `review_notes` and edit-to-resubmit, non-cascading suspension — see [Applications → The review lifecycle](https://compositecode.github.io/substratalapps.com/domain-model/applications/#the-review-lifecycle) and [API Reference → Applications → Reviewing a submission](https://compositecode.github.io/substratalapps.com/api-reference/applications/#reviewing-a-submission). What's actually deferred to this phase is just the self-service *submission* endpoint itself (`POST /v1/applications` opening up beyond platform-admin-only) — the review mechanics it feeds into are build-ready today.
- SCIM-style provisioning.
- Richer audit/compliance views (filtering, export) — driven by actual [Compliance](https://compositecode.github.io/substratalapps.com/compliance/) needs (a SOC 2 audit, a customer's data-export request), not built ahead of either.

## Cross-cutting, build throughout

Not a phase — these apply from Phase 0 onward and should never be "added later":

- **[Access Control](https://compositecode.github.io/substratalapps.com/access-control/)'s permission checks**, on every endpoint, from the first one. Retrofitting authorization onto endpoints built "open" is how it gets missed somewhere.
- **[Conventions](https://compositecode.github.io/substratalapps.com/api-reference/conventions/)'s error shape, pagination, idempotency, and `me` self-addressing** — pick these once, in Phase 1, as shared middleware/helpers. Every later endpoint should get them for free, not reimplement them.
- **Contract tests against `openapi.yaml`** — every endpoint, the day it's built, not batched at the end of a phase.
- **[Changelog](https://compositecode.github.io/substratalapps.com/changelog/) and this project's [DECISIONS.md](DECISIONS.md)** stay live documents during implementation, the same way they were during spec-writing — an implementation detail that contradicts or resolves something in either belongs there immediately, not in a end-of-project cleanup pass.

## What's still open, and what it blocks

**Build status (2026-10-06):** the code for every phase's API surface is implemented, including Phase 3's hosted PKCE endpoints (SSO still returns `501` until the broker integration, as specified), and the Quickstart acceptance test passes against the devserver. The Phase 0 *infrastructure* items above are written as Terraform (`infra/terraform/`) but not yet applied; see [README → One-time setup](README.md#one-time-setup-phase-0). Decisions #32–#37 record choices the implementation made where the spec was silent or self-contradictory.

See [DECISIONS.md](DECISIONS.md) for the current list. Decisions #1–#30 are resolved, and each answer is written into the spec page it governs. #16–#30 were confirmed on 2026-10-05, and #29 was applied: per-user, per-Application paths use `/apps/{appId}`.

The one open question, [#31](DECISIONS.md#31-published-prices-and-billing-options), covers the final Pricing dollar amounts plus annual plans, a Team trial, and whether Starter needs a card on file. **It blocks nothing in any phase:** prices are Stripe configuration, not code, and each billing option is additive. Settle it before the Phase 2 Billing work goes live to paying customers.
