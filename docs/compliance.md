---
layout: default
title: Compliance & Data Protection
nav_order: 10
---

# Compliance & Data Protection
{: .no_toc }

What this API needs to do, legally and contractually, with the user data flowing through it — and a calibrated recommendation on which of SOC 2, HIPAA, GDPR, and CCPA actually apply, since "comply with everything" isn't a real plan.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

{: .decision }
This page is a starting recommendation, not a verified compliance posture — see [Decisions → Compliance scope](../decisions/#10-compliance-scope). Confirm the applicable regimes (and get real legal review before claiming any certification publicly) rather than treating this page as the final word.

## The short version

| Regime | Is it optional? | Recommendation |
|---|---|---|
| **GDPR** | No — triggered by whose data you process, not a choice | **Build for it now.** Mechanisms are cheap to add early and expensive to retrofit. |
| **CCPA/CPRA** | No, if any California residents' data passes through | **Covered by the same mechanisms as GDPR** below — no separate workstream. |
| **SOC 2** | Yes — a market/contractual requirement, not a law | **Design for the posture now** (most of it is engineering practice you want anyway); **defer the formal audit** until a customer's security questionnaire actually requires it. |
| **HIPAA** | Only if PHI flows through the platform | **Don't build for it yet.** Decide explicitly whether health-vertical apps are even allowed on the platform first — see [below](#hipaa--the-one-to-think-hardest-about). |

## Why this order

Substratal Apps is infrastructure other developers' apps are built on — per [Home](../), it's the user/org/tenancy/settings/storage layer so a developer doesn't have to build it themselves. That framing is exactly why **GDPR isn't a choice**: it applies based on whose personal data is processed, regardless of what Substratal's own business is. If even one app on the platform has an EU end user, Substratal is a data processor for that user's data and GDPR applies — no opt-in, no "we'll decide later."

SOC 2, by contrast, is a *trust signal* a B2B customer asks for in a security questionnaire — not a law. It's extremely likely to matter here precisely because the product is B2B infrastructure (every comparable — Auth0, Clerk, WorkOS — has it), but a formal audit costs real money and months of evidence collection, so it should be timed to when a real customer actually asks, not built preemptively by a team of one plus an AI assistant with dozens of users.

## GDPR (and CCPA) — build for it now

The mechanisms below cover both regimes; CCPA/CPRA's individual-rights requirements (access, deletion, opt-out of sale) are a subset of GDPR's. Where this spec already has the right primitive, it's noted — where it doesn't yet, that's the gap to close.

| Requirement | Status |
|---|---|
| Right to erasure | **Partial.** [Users](../domain-model/users-and-organizations/) has soft-delete today; the "separate, deliberate hard-delete process" in [Non-Functional Requirements → Data retention](../non-functional-requirements/#data-retention) needs to actually exist, not just be named. |
| Right to access / data portability | **Missing.** No endpoint today returns "everything this API holds about user X" in one call. Worth a `GET /v1/users/{id}/export` before this is a real requirement, not after. |
| Lawful basis & consent records | **Missing from the spec** — not a schema problem, a product/legal one: each Application's developer is the one collecting consent from their own end users (Substratal doesn't run the signup form), so this is as much about what the *developer* agrees to via Substratal's terms of service as what the API stores. |
| Data Processing Agreement with sub-processors | **Operational, not code** — needed with AWS (the infrastructure in [Deployment Architecture](../deployment-architecture/)) before real EU user data flows through it. |
| Data residency | **Open** — [Deployment Architecture](../deployment-architecture/#open-questions-this-depends-on) picks a single region for Tier 0; if EU data residency is a hard requirement for a customer, that's a second region, not a config flag, and should be a deliberate later decision, not retrofitted under pressure. |
| Breach notification (72-hour clock under GDPR) | **Missing** — needs a named incident-response process, independent of this API's own design. |
| Audit trail vs. erasure tension | **Already resolved in the data model.** [Audit Events](../domain-model/orders-and-audit/#audit-event) are retained after a user is hard-deleted, but should retain only the *shape* of what happened (`action`, `timestamp`, which fields changed) — never a copy of the erased personal data itself. Enforce this at write time: an Audit Event's `before`/`after` snapshot should never be the sole remaining copy of a field that erasure is supposed to remove. |

## SOC 2 — design the posture, defer the audit

Everything in this list is good engineering practice independent of SOC 2, which is exactly why it's worth doing now rather than treating it as compliance overhead:

- **Access control** — already the spine of this entire spec: [Entitlements](../domain-model/entitlements/) + [Roles & Permissions](../domain-model/roles-and-permissions/), least-privilege [API Keys](../api-reference/api-keys/).
- **Audit logging** — already specified: the [Action catalog](../domain-model/orders-and-audit/#action-catalog).
- **Encryption in transit and at rest** — HTTPS everywhere (already assumed throughout the [API Reference](../api-reference/)); Aurora's encryption-at-rest in [Deployment Architecture](../deployment-architecture/) should be turned on by default, not left as a checkbox for later.
- **Change management** — the [Changelog](../changelog/) convention already *is* this, applied to the spec; the same discipline (what changed, when, why) needs to carry into infrastructure and code changes once implementation starts.
- **Vendor risk management** — one sub-processor today (AWS). Keep the list explicit as it grows (payment processors are each Application developer's own choice, per [Decisions → Billing system of record](../decisions/#4-billing-system-of-record) — not Substratal's vendor risk to own).
- **Incident response plan** — the one item on this list that's pure process, no code. Write it down before it's needed, not during an actual incident.

**When to pursue the formal Type I → Type II audit:** the first time a prospective customer's security review asks for it — typically the point at which a B2B infrastructure product starts closing deals with companies that have their own procurement/security process. Not before; the posture above is what makes that audit fast and cheap when the time comes, rather than a scramble.

## HIPAA — the one to think hardest about

HIPAA applies if Protected Health Information (PHI) flows through the platform — which, for Substratal Apps specifically, depends entirely on **whether a healthcare-vertical app ever joins the marketplace** and stores health data in [AppProfile](../domain-model/profiles/#appprofile) or [AppSettings](../domain-model/settings/#appsettings)'s free-form `custom` fields. Nothing about the core product (user/org/tenancy/settings/storage infrastructure) requires HIPAA — it's entirely a function of which apps choose to build on it.

This is the most expensive of the three to take on (a Business Associate Agreement process, PHI-specific technical safeguards beyond what GDPR/SOC 2 already cover, workforce training, breach notification under HITECH's stricter timeline) and the recommendation is to **not** build for it speculatively. Instead, make an explicit product decision — see [Decisions → Compliance scope](../decisions/#10-compliance-scope) — about whether PHI is even allowed on the platform in its current form, with the default being "not yet, revisit if a healthcare-vertical developer actually wants in."

## What this doesn't cover

Payment data: per [Decisions → Billing system of record](../decisions/#4-billing-system-of-record), Substratal Apps isn't a payment processor — each Application's developer handles billing with their own end users directly. That keeps PCI-DSS scope minimal here (no card data ever touches this API), but it's each developer's own PCI obligation for their own processor integration, not something this spec needs to solve.
