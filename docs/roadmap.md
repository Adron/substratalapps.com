---
layout: default
title: Roadmap
nav_order: 9
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

**Ships:** the full per-app customization model, and the mechanics apps need to actually trust the hub in production rather than trusting it "eventually, on next login."

## Phase 3

- [Organizations](../domain-model/users-and-organizations/) / seats — team plans, delegated admin (an org admin manages their own members without needing platform-admin rights).
- Richer audit/compliance views — filtering, export, retention policy enforcement.
- SCIM-style provisioning for larger customers who want their own IdP to push user lifecycle events into the hub automatically.

**Ships:** the features that only matter once there are customers big enough to need them — deliberately not pulled earlier, since building Organizations before there's a real multi-seat customer tends to guess the shape wrong.

## What's explicitly not on this roadmap

- Any end-user interface. This is an API-first roadmap; a UI project consumes whatever phase is live, on its own timeline. See [Getting Started → What's deliberately not here](../getting-started/#whats-deliberately-not-here).
- Payment processing. Orders/Subscriptions are referenced, not owned, by this API — see [Decisions → Billing system of record](../decisions/#4-billing-system-of-record).
