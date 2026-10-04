---
layout: default
title: Decisions
nav_order: 13
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
| 8 | [Storage primitive scope](#8-storage-primitive-scope) | 🟡 Open |
| 9 | [App developer/publisher model](#9-app-developerpublisher-model) | 🟡 Open |
| 10 | [Compliance scope](#10-compliance-scope) | 🟡 Open |

---

## 1. Identity provider

Build auth in-house, or delegate to an IdP (Auth0, Clerk, WorkOS, Cognito, …)?

This changes what [`User.auth`](../domain-model/users-and-organizations/) actually stores (a password hash owned here, vs. a foreign subject ID), and how the JWT in the [Trust Model](../trust-model/) gets issued — self-signed by the hub either way, but the login step in front of it looks very different.

**Leans toward:** delegating. Auth is a solved, security-sensitive problem; building it in-house is rarely where the differentiated value of this hub lives.

## 2. Organizations

~~Is multi-seat/team access a day-one requirement~~ — **resolved: both individual and team/business end users are expected**, so Organizations isn't a someday-maybe feature. It's pulled forward to [Phase 2](../roadmap/#phase-2) rather than [Phase 3](../roadmap/#phase-3) — not the MVP itself (the first dozens of users are expected to be mostly individual early adopters), but needed well before the 10x/100x growth horizon in [Deployment Architecture → Growth trajectory](../deployment-architecture/#growth-trajectory) hits, where team accounts are assumed to matter.

`organization_id` being load-bearing in the schema from day one (per [Non-Functional Requirements](../non-functional-requirements/#multi-tenancy)) was the right call regardless of timing — this just confirms it wasn't a hedge against a hypothetical.

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

**Leans toward:** ship with the existing JSON-blob primitives for now — they're already built, already [cost-modeled](../deployment-architecture/) into Tier 0, and may well be enough for a flag/preference/small-record use case. Treat a general-purpose storage API (key-value collections, or file storage via S3) as a real Phase 2+ candidate the moment a real Application — including the three-plus the platform's own developer is building — actually hits the limit of a flat JSON blob, rather than guessing the shape of a more general primitive speculatively.

## 9. App developer/publisher model

Third-party developers registering their own Applications is an explicit later phase, not day one (see [Home](../#what-substratal-apps-actually-is)) — but "later" still needs a real shape: self-service submission into [`review_status: pending_review`](../domain-model/applications/), who reviews it and against what criteria, what SLA a developer should expect, and what happens to an already-launched app that gets `suspended` mid-flight (do its existing users' Entitlements stay `active`, or does suspension cascade to them?).

**Leans toward:** `POST /v1/applications` stays platform-admin-only through the [MVP and Phase 2](../roadmap/) — the `owner_user_id`/`owner_organization_id`/`review_status` fields on [Application](../domain-model/applications/) exist now specifically so this doesn't require a breaking schema change when self-service registration actually ships in [Phase 3](../roadmap/#phase-3). The review-queue mechanics themselves (reviewer assignment, SLA, suspension cascade) are unspecified on purpose — designing that process before there's ever been a single real submission to learn from is more likely to guess wrong than to save time.

## 10. Compliance scope

Which of SOC 2, HIPAA, GDPR, and CCPA actually get pursued, and on what timeline?

See [Compliance & Data Protection](../compliance/) for the full recommendation — GDPR/CCPA mechanisms built now (not optional), SOC 2 posture built now with the formal audit deferred until a customer requires it, and HIPAA deliberately not pursued unless and until a healthcare-vertical Application actually wants onto the platform. This row exists to track the one decision that page can't make on its own: confirming that recommendation (or overriding it) is a real legal/business call, not an engineering one.
