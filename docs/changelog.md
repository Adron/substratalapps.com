---
layout: default
title: Changelog
nav_order: 13
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

## 2026-10-03

- Initial publication: domain model, access control, full API reference, workflows, trust model, non-functional requirements, roadmap, open decisions, and glossary — elaborated from the original single-document draft at `docs/specs/Substratal-Hub-Access-API.md` into this site.
