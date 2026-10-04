---
layout: default
title: Roadmap
nav_order: 11
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

**Ships:** a dashboard-ready API — list a user's apps, their status, and let an admin flip that status — plus the data model other phases build on without a migration.

## Phase 2

- App-scoped [Roles & Permissions](../domain-model/roles-and-permissions/) — apps start defining their own role vocabulary.
- Per-app [Profile](../domain-model/profiles/) and [Settings](../domain-model/settings/), with schema validation against what each Application declares.
- [Webhooks](../api-reference/webhooks/) — `entitlement.*`, `role.*` — so downstream apps can react instead of poll.
- The live introspection endpoint (`effective-permissions`) — see [Trust Model](../trust-model/).
- [Organizations](../domain-model/users-and-organizations/) / seats — team plans, delegated admin (an org admin manages their own members without needing platform-admin rights). Pulled forward from a later phase: end users are expected to be both individuals and teams from early on, not teams-later — see [Decisions → Organizations](../decisions/#2-organizations).

**Ships:** the full per-app customization model, team accounts, and the mechanics apps need to actually trust the hub in production rather than trusting it "eventually, on next login."

## Phase 3

- Richer audit/compliance views — filtering, export, retention policy enforcement; see [Compliance & Data Protection](../compliance/) for what's driving this (SOC 2 readiness, GDPR data-subject requests).
- SCIM-style provisioning for larger customers who want their own IdP to push user lifecycle events into the hub automatically.
- Self-service Application registration and review queue for third-party developers — the marketplace phase. See [Decisions → App developer/publisher model](../decisions/#9-app-developerpublisher-model).

**Ships:** the features that only matter once there are customers (or outside developers) big enough to need them — deliberately not pulled earlier, since building any of these before there's a real need tends to guess the shape wrong.

## What's explicitly not on this roadmap

- Any end-user interface. This is an API-first roadmap; a UI project consumes whatever phase is live, on its own timeline. See [Getting Started → What's deliberately not here](../getting-started/#whats-deliberately-not-here).
- Payment processing. Orders/Subscriptions are referenced, not owned, by this API — see [Decisions → Billing system of record](../decisions/#4-billing-system-of-record).

## Where this runs

These phases are about feature scope, not infrastructure — the MVP and Phase 2/3 all deploy onto the same [Deployment Architecture](../deployment-architecture/), which has its own, independent phasing (first deployment vs. scale-out) driven by traffic and cost, not by which of these feature phases is live.
