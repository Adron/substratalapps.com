---
layout: default
title: Compliance & Data Protection
nav_order: 11
---

# Compliance & Data Protection
{: .no_toc }

What this API needs to do, legally and contractually, with the user data flowing through it — and a calibrated recommendation on which of SOC 2, HIPAA, GDPR, and CCPA actually apply, since "comply with everything" isn't a real plan.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .note }
This recommendation is now the decision — see [Decisions → Compliance scope](../decisions/#10-compliance-scope): build for GDPR/CCPA/CPRA now, design the SOC 2 posture now and defer the audit, leave HIPAA out of scope. Get real legal review before claiming any certification publicly; that caveat doesn't go away just because the scope is decided.

## The short version

| Regime | Is it optional? | Decision |
|---|---|---|
| **GDPR** | No — triggered by whose data you process, not a choice | **Built now.** Mechanisms are cheap to add early and expensive to retrofit. |
| **CCPA/CPRA** | No, if any California residents' data passes through | **Covered by the same mechanisms as GDPR** below — no separate workstream. |
| **SOC 2** | Yes — a market/contractual requirement, not a law | **Design for the posture now** (most of it is engineering practice you want anyway); **defer the formal audit** until a customer's security questionnaire actually requires it. |
| **HIPAA** | Only if PHI flows through the platform | **Out of scope.** Decided, not just recommended — see [below](#hipaa--the-one-to-think-hardest-about) for what would have to change before revisiting this. |

## Why this order

Substratal Apps is infrastructure other developers' apps are built on — per [Home](../), it's the user/org/tenancy/settings/storage layer so a developer doesn't have to build it themselves. That framing is exactly why **GDPR isn't a choice**: it applies based on whose personal data is processed, regardless of what Substratal's own business is. If even one app on the platform has an EU end user, Substratal is a data processor for that user's data and GDPR applies — no opt-in, no "we'll decide later."

SOC 2, by contrast, is a *trust signal* a B2B customer asks for in a security questionnaire — not a law. It's extremely likely to matter here precisely because the product is B2B infrastructure (every comparable — Auth0, Clerk, WorkOS — has it), but a formal audit costs real money and months of evidence collection, so it should be timed to when a real customer actually asks, not built preemptively by a team of one plus an AI assistant with dozens of users.

## GDPR (and CCPA) — build for it now

The mechanisms below cover both regimes; CCPA/CPRA's individual-rights requirements (access, deletion, opt-out of sale) are a subset of GDPR's. Where this spec already has the right primitive, it's noted — where it doesn't yet, that's the gap to close.

| Requirement | Status |
|---|---|
| Right to erasure | **Specified.** [Non-Functional Requirements → Data retention](../non-functional-requirements/#data-retention) now names the actual hard-delete cascade: soft-delete is immediate on request; hard-delete runs within GDPR Article 12(3)'s 30-day ceiling (not "eventually") and, in order, deletes `password_hash`/`mfa_secret` from [UserIdentity](../domain-model/users-and-organizations/#useridentity), the User's [Profile](../domain-model/profiles/), scrubs PII out of every [AppProfile](../domain-model/profiles/#appprofile)/[AppSettings](../domain-model/settings/#appsettings) `custom`/`overrides` blob the User holds, and redacts (not deletes — see below) the `before`/`after` snapshots on their [Audit Events](../domain-model/orders-and-audit/#audit-event). Entitlement and Order rows are retained as pseudonymized business/financial records, which GDPR permits under a legitimate-interest basis distinct from the erasure right. |
| Right to access / data portability | **Specified.** `GET /v1/users/{id}/export` — see [API Reference → Users](../api-reference/users/) — returns everything this API holds about one User: Profile, Settings, every AppProfile/AppSettings, Entitlements, Role assignments, Organization memberships, UserIdentity methods (never `password_hash`/`mfa_secret`), and their own Audit trail, as one JSON bundle. Satisfies GDPR Article 20 portability and the equivalent CCPA/CPRA right-to-know in one endpoint rather than two. |
| Lawful basis & consent records | **Missing from the spec on purpose** — not a schema problem, a product/legal one: each Application's developer is the one collecting consent from their own end users (Substratal doesn't run the signup form), so this is as much about what the *developer* agrees to via Substratal's terms of service as what the API stores. Unchanged by this round of decisions. |
| Data Processing Agreement with sub-processors | **Operational, not code** — two sub-processors now, not one: AWS (the infrastructure in [root `DEPLOYMENT.md`](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md)) and **Stripe** (platform subscription billing, per [Decision #15](../decisions/#15-platform-subscription-billing-processor)). Both publish standard DPAs (AWS Artifact; Stripe's own, plus Stripe's existing PCI-DSS Level 1 status, which is part of why [Decision #4](../decisions/#4-billing-system-of-record) keeps this API's own PCI scope minimal). Executing either is still a step to actually take, not code to write. |
| Data residency | **Resolved.** [Tenancy](../domain-model/tenancy/)'s `dedicated_region` tier is the mechanism — a customer with a hard EU-residency requirement gets a dedicated Tenant in an EU region, not a platform-wide region change. Still support-gated today, not self-service — see [Decisions → Tenancy tiers](../decisions/#12-tenancy-tiers--dedicated-infrastructure). |
| Breach notification (72-hour clock under GDPR) | **Commitment stated, runbook still operational.** The 72-hour clock to notify affected supervisory authorities (and, where risk is high, the data subjects themselves) starts at confirmed awareness of a breach — [Audit Events](../domain-model/orders-and-audit/#audit-event) are the forensic source for scoping what was accessed and when. The specific internal runbook (who's paged, who approves the notification, the template) is operational process, deliberately not specified in an API doc — but the 72-hour commitment itself is now a stated requirement, not an unnamed gap. |
| Audit trail vs. erasure tension | **Resolved in the data model.** [Audit Events](../domain-model/orders-and-audit/#audit-event) are retained after a user is hard-deleted, but only the *shape* of what happened (`action`, `timestamp`, which fields changed) — never a copy of the erased personal data itself. The hard-delete cascade above redacts `before`/`after` snapshot values at the same time it deletes the source data, specifically so an Audit Event never becomes the one remaining copy of something erasure was supposed to remove. This is the same redaction every Audit Event eventually gets on its own, by age rather than by request — see [Non-Functional Requirements → Audit log lifecycle](../non-functional-requirements/#audit-log-lifecycle) — so "retained indefinitely" and [Pricing](../pricing/#enforcement)'s plan-tiered hot-storage window describe different things, not a contradiction. |

## SOC 2 — design the posture, defer the audit

Everything in this list is good engineering practice independent of SOC 2, which is exactly why it's worth doing now rather than treating it as compliance overhead:

- **Access control** — already the spine of this entire spec: [Entitlements](../domain-model/entitlements/) + [Roles & Permissions](../domain-model/roles-and-permissions/), least-privilege [API Keys](../api-reference/api-keys/).
- **Audit logging** — already specified: the [Action catalog](../domain-model/orders-and-audit/#action-catalog).
- **Encryption in transit and at rest** — HTTPS everywhere (already assumed throughout the [API Reference](../api-reference/)); Aurora's encryption-at-rest in [Deployment Architecture](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) should be turned on by default, not left as a checkbox for later.
- **Change management** — the [Changelog](../changelog/) convention already *is* this, applied to the spec; the same discipline (what changed, when, why) needs to carry into infrastructure and code changes once implementation starts.
- **Vendor risk management** — two sub-processors now: AWS and Stripe (see the DPA row above). Keep the list explicit as it grows — each Application developer's *own* payment processor choice for their own end users is their vendor risk to own, per [Decisions → Billing system of record](../decisions/#4-billing-system-of-record), not added here.
- **Incident response plan** — the one item on this list that's pure process, no code. Write it down before it's needed, not during an actual incident.

**When to pursue the formal Type I → Type II audit:** the first time a prospective customer's security review asks for it — typically the point at which a B2B infrastructure product starts closing deals with companies that have their own procurement/security process. Not before; the posture above is what makes that audit fast and cheap when the time comes, rather than a scramble.

## HIPAA — the one to think hardest about

HIPAA applies if Protected Health Information (PHI) flows through the platform — which, for Substratal Apps specifically, depends entirely on **whether a healthcare-vertical app ever joins the marketplace** and stores health data in [AppProfile](../domain-model/profiles/#appprofile) or [AppSettings](../domain-model/settings/#appsettings)'s free-form `custom` fields. Nothing about the core product (user/org/tenancy/settings/storage infrastructure) requires HIPAA — it's entirely a function of which apps choose to build on it.

This is the most expensive of the three to take on (a Business Associate Agreement process, PHI-specific technical safeguards beyond what GDPR/SOC 2 already cover, workforce training, breach notification under HITECH's stricter timeline) — **decided: out of scope, not pursued.** See [Decisions → Compliance scope](../decisions/#10-compliance-scope). PHI is not an intended use of the current `AppProfile.custom`/`AppSettings.overrides` free-form storage; revisit only if a real healthcare-vertical developer wants to build on the platform, at which point this becomes a deliberate, scoped project — a BAA process and PHI-specific safeguards added for that purpose — not a retrofit assumed in advance.

## What this doesn't cover

Payment data: per [Decisions → Billing system of record](../decisions/#4-billing-system-of-record), Substratal Apps isn't a payment processor — each Application's developer handles billing with their own end users directly. That keeps PCI-DSS scope minimal here (no card data ever touches this API), but it's each developer's own PCI obligation for their own processor integration, not something this spec needs to solve.
