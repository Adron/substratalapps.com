---
layout: default
title: Changelog
nav_order: 14
---

# Changelog
{: .no_toc }

What's changed in this specification over time. [Decisions](../decisions/) tracks what's still *open*; this page tracks what's already *changed* — the two are related but answer different questions, so keep entries here even after a decision above gets resolved (add a new dated entry, don't just flip the status).
{: .fs-6 .fw-300 }

---

## 2026-10-04

- Added [API Keys](../api-reference/api-keys/) as its own resource — service-to-service credentials were referenced throughout (Conventions, NFR, Trust Model) but never actually specified.
- Added a global `GET /v1/entitlements` (admin/support, filterable) alongside the existing per-user list — there was no way to answer "who currently has app X enabled" without it.
- Added a canonical [Platform permission catalog](../domain-model/roles-and-permissions/#platform-permission-catalog) and [Platform role grants](../domain-model/roles-and-permissions/#platform-role-grants) table, and added an explicit `Requires: <permission>` line to every endpoint that was missing one.
- Added a canonical [Audit Event action catalog](../domain-model/orders-and-audit/#action-catalog).
- Filled in previously-deferred numbers: rate limits, the idempotency-key TTL (24h), and the version-deprecation window (6 months) — see [Non-Functional Requirements](../non-functional-requirements/).
- Added the `me` self-addressing convention and a credential-vs-resource-ID distinction to [Conventions](../api-reference/conventions/).
- Added [Quickstart](../quickstart/) (a runnable walkthrough) and this Changelog.
- Fixed: the Audit endpoint's documented filter name (`user_id` → `target_user_id`) didn't match its own example.
- Added [Deployment Architecture](../deployment-architecture/): first-deployment and scale-out plans on AWS, replacing an earlier Vercel-based plan, designed around predictable, capped cost rather than deploy-and-see.
- **Reframed the product.** Substratal Apps is the user/organization/tenancy/settings/storage API layer an app developer builds on — not a storefront a customer browses. See [Home](../) and [What Substratal Apps actually is](../#what-substratal-apps-actually-is). Every page that described the platform as "the hub a customer lands on to reach apps they purchased" was describing the eventual onboarding UI, not the API's own purpose.
- Added `owner_user_id`/`owner_organization_id` and `review_status` to [Application](../domain-model/applications/) — a real gap once outside developers can register their own apps: there was no concept of who owns an Application's catalog entry, only platform-admin control. [API Reference → Applications](../api-reference/applications/) now distinguishes an owner's self-service rights from platform moderation.
- Added [Trust Model → Trust runs the other direction too](../trust-model/#trust-runs-the-other-direction-too): the existing API Key scoping and webhook signing already made third-party apps safe to onboard later — now stated explicitly instead of left implicit.
- Resolved [Decisions #2 (Organizations)](../decisions/#2-organizations) and [#4 (Billing system of record)](../decisions/#4-billing-system-of-record) based on product clarification: both individual and team end users are expected (Organizations pulled forward to [Phase 2](../roadmap/#phase-2)), and each Application's developer owns their own billing relationship — Substratal Apps was never going to be a payment processor. Added three new open decisions: [#8 Storage primitive scope](../decisions/#8-storage-primitive-scope), [#9 App developer/publisher model](../decisions/#9-app-developerpublisher-model), [#10 Compliance scope](../decisions/#10-compliance-scope).
- Added [Compliance & Data Protection](../compliance/): a calibrated recommendation on SOC 2 (design the posture now, defer the formal audit), GDPR/CCPA (not optional, build for it now), and HIPAA (don't build for it speculatively — it's a function of which apps join the platform, not the core product).
- Added [Database Schema](../domain-model/database-schema/): Postgres types, constraints, and indexes for every entity — the implementer-facing counterpart to the API-caller-facing field tables.
- Added implementer-readiness gaps across [Non-Functional Requirements](../non-functional-requirements/) (concurrency control via `ETag`/`If-Match`, transaction boundaries, the concrete Postgres Row-Level Security multi-tenancy mechanism, enum forward-compatibility policy, request tracing, testing strategy) and [Conventions](../api-reference/conventions/) (test-vs-live API keys, multi-field validation error shape, default exclusion of soft-deleted/terminal records from lists, idempotency-key storage detail).
- Added [Webhooks → Verifying the signature](../api-reference/webhooks/#verifying-the-signature): the concrete `Substratal-Signature: t=…,v1=…` header format and replay-window guidance — "HMAC-SHA256 over the raw body" wasn't enough to actually implement against.
- Added [Deployment Architecture → Local development](../deployment-architecture/#local-development) (the RDS Data API choice has no clean local emulator — addressed via a repository-pattern seam, not by changing the AWS choice) and [→ Growth trajectory](../deployment-architecture/#growth-trajectory) (the stated dozens → 10x → 100x plan mapped against Tier 0 and the Scale-out triggers).

## 2026-10-03

- Initial publication: domain model, access control, full API reference, workflows, trust model, non-functional requirements, roadmap, open decisions, and glossary — elaborated from the original single-document draft at `docs/specs/Substratal-Hub-Access-API.md` into this site.
