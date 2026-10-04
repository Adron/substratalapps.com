---
layout: default
title: Roadmap
nav_order: 12
---

# Roadmap
{: .no_toc }

Phased so the hub is useful as early as possible: "see my apps, launch one, admin can turn access on or off" is the entire MVP bar.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## MVP

Enough to replace "someone manually emails a link and flips a flag in a spreadsheet" with a real system.

- [User](../domain-model/users-and-organizations/) accounts: create, suspend, basic lifecycle.
- [Profile](../domain-model/profiles/) — global only. No per-app profile yet.
- [Application](../domain-model/applications/) catalog — list and fetch; writes are admin/internal only.
- [Entitlement](../domain-model/entitlements/) — grant, revoke, and the on/off toggle. This is the feature the rest of the MVP exists to support.
- Platform [Roles](../domain-model/roles-and-permissions/) only — no app-scoped roles yet; every entitled user is an implicit full member of the app they own.
- Basic [Audit Event](../domain-model/orders-and-audit/) log for entitlement changes.
- [Tenant](../domain-model/tenancy/) ships structurally — every Application owner gets one automatically, at `tier: shared`, the moment they register their first Application. No tier change is reachable yet (that's Phase 2); the `tenant_id` column and its Row-Level Security policy exist and are enforced from day one, same reasoning as `organization_id` below.

**Ships:** a dashboard-ready API — list a user's apps, their status, and let an admin flip that status — plus the data model other phases build on without a migration.

## Phase 2

- App-scoped [Roles & Permissions](../domain-model/roles-and-permissions/) — apps start defining their own role vocabulary.
- Per-app [Profile](../domain-model/profiles/) and [Settings](../domain-model/settings/), with schema validation against what each Application declares.
- [Webhooks](../api-reference/webhooks/) — `entitlement.*`, `role.*` — so downstream apps can react instead of poll.
- The live introspection endpoint (`effective-permissions`) — see [Trust Model](../trust-model/).
- [Organizations](../domain-model/users-and-organizations/) / seats — team plans, delegated admin via `OrganizationMembership.role: org_admin` (an org admin manages their own members without needing platform-admin rights), and org-wide Entitlements' `member_scope` allowlist/denylist. Pulled forward from a later phase: end users are expected to be both individuals and teams from early on, not teams-later — see [Decisions → Organizations](../decisions/#2-organizations).
- Support-run [Tenancy](../domain-model/tenancy/) tier changes become operationally available — `isolated` and `dedicated_region` are real, reachable tiers via the support-gated `POST /v1/tenants/{id}/tier-change-requests` flow, even though there's still no customer self-serve path. See [Decisions → Tenancy tiers](../decisions/#12-tenancy-tiers--dedicated-infrastructure).

**Ships:** the full per-app customization model, team accounts, and the mechanics apps need to actually trust the hub in production rather than trusting it "eventually, on next login."

## Phase 3

- Richer audit/compliance views — filtering, export, retention policy enforcement; see [Compliance & Data Protection](../compliance/) for what's driving this (SOC 2 readiness, GDPR data-subject requests).
- SCIM-style provisioning for larger customers who want their own IdP to push user lifecycle events into the hub automatically.
- Self-service Application registration and review queue for third-party developers — the marketplace phase. See [Decisions → App developer/publisher model](../decisions/#9-app-developerpublisher-model).

**Ships:** the features that only matter once there are customers (or outside developers) big enough to need them — deliberately not pulled earlier, since building any of these before there's a real need tends to guess the shape wrong.

## Tenancy automation isn't phase-gated — it's revenue-gated

Self-service, customer-triggered [Tenancy](../domain-model/tenancy/) tier changes (no support ticket, no human running the migration) deliberately don't have a phase number above. It's not a feature-scope decision like the rest of this page — it's a decision to accept a real amount of migration risk (a failed cutover with no human checking each step) in exchange for support time, and that trade only makes sense once tier-change request volume justifies it. Track it against actual demand, not a calendar phase — see [Decisions → Tenancy tiers](../decisions/#12-tenancy-tiers--dedicated-infrastructure).

## The MCP server tracks the REST API automatically

[MCP Server](../mcp-server/) also doesn't have a phase number, for the opposite reason from Tenancy automation above: it needs none, because its tool surface is generated directly from [openapi.yaml](../openapi.yaml) (see [MCP Server → Tool surface is generated, not hand-authored](../mcp-server/#tool-surface-is-generated-not-hand-authored)). Whatever of the REST API is live at MVP is agent-callable at MVP; whatever Phase 2/3 adds becomes agent-callable the same day, with no separate MCP work item to schedule. The only real build step — adding `operationId` to new operations as they're written — is already folded into writing the endpoint, not a follow-up task.

## What's explicitly not on this roadmap

- Any end-user interface. This is an API-first roadmap; a UI project consumes whatever phase is live, on its own timeline. See [Getting Started → What's deliberately not here](../getting-started/#whats-deliberately-not-here).
- Payment processing. Orders/Subscriptions are referenced, not owned, by this API — see [Decisions → Billing system of record](../decisions/#4-billing-system-of-record).

## Where this runs

These phases are about feature scope, not infrastructure — the MVP and Phase 2/3 all deploy onto the same [Deployment Architecture](../deployment-architecture/), which has its own, independent phasing (first deployment vs. scale-out) driven by traffic and cost, not by which of these feature phases is live.
