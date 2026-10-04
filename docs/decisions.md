---
layout: default
title: Decisions
nav_order: 12
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
| 2 | [Organizations](#2-organizations) | 🟡 Open |
| 3 | [Downstream app architecture](#3-downstream-app-architecture) | 🟡 Open |
| 4 | [Billing system of record](#4-billing-system-of-record) | 🟡 Open |
| 5 | [Settings schema ownership](#5-settings-schema-ownership) | 🟡 Open |
| 6 | [Session model for revocation](#6-session-model-for-revocation) | 🟡 Open |
| 7 | [AWS account and region](#7-aws-account-and-region) | 🟡 Open |

---

## 1. Identity provider

Build auth in-house, or delegate to an IdP (Auth0, Clerk, WorkOS, Cognito, …)?

This changes what [`User.auth`](../domain-model/users-and-organizations/) actually stores (a password hash owned here, vs. a foreign subject ID), and how the JWT in the [Trust Model](../trust-model/) gets issued — self-signed by the hub either way, but the login step in front of it looks very different.

**Leans toward:** delegating. Auth is a solved, security-sensitive problem; building it in-house is rarely where the differentiated value of this hub lives.

## 2. Organizations

Is multi-seat/team access a day-one requirement, or does every account start as a single user, with Organizations bolted on in [Phase 3](../roadmap/#phase-3)?

Affects whether `organization_id` is load-bearing in the MVP schema or added later via migration. The [Non-Functional Requirements](../non-functional-requirements/#multi-tenancy) page already recommends reserving the column now regardless of which way this goes, specifically to avoid that migration.

## 3. Downstream app architecture

The single biggest fork in the API's shape. Two real options:

- **Apps are separately hosted services** that need SSO + token verification — the assumption this entire site currently builds on (see [Trust Model](../trust-model/)).
- **Apps are modules/iframes inside one deployment**, where access control could live entirely in the hub's own session and nothing in [Trust Model](../trust-model/) is needed at all.

**This needs to be resolved before the API Reference pages are treated as final** — everything about JWT issuance, introspection, and webhooks in [Trust Model](../trust-model/) assumes the first option.

## 4. Billing system of record

Which processor, and does it push webhooks to the hub or does the hub poll it? Determines the real contract behind [`Order`](../domain-model/orders-and-audit/) and the first step of [Purchase → access](../workflows/#purchase--access).

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
