# Decisions

Where questions that need a product or business decision get asked. This file is a queue, not the record of what was decided: once a question is answered, the answer is written into the spec page(s) it affects, and its entry here moves to [Resolved](#resolved) as a one-line pointer to where the answer now lives.

## How to use this file

- **Asking for a decision:** add an entry under [Awaiting a decision](#awaiting-a-decision) with the next free number. State the question, what the spec currently assumes (if anything), the proposal, and the alternative. On every spec page the question affects, add an "Awaiting decision" callout linking back here:

  ```markdown
  {: .decision }
  **Proposed — confirm** ([DECISIONS.md #32](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#32-short-title)). Why, and the alternative.
  ```

- **Answering one:** update the spec pages to match the answer, remove their callouts, add a [Changelog](https://adron.github.io/substratalapps.com/changelog/) entry, and move the entry here to [Resolved](#resolved) with a link to where the answer lives. If the answer overrides a proposal, change every page the proposal touched in the same commit.
- **Don't let an answer live only in a chat thread, or only in this file.** Implementers read the spec pages, not this log.

Status meanings:

- **🟡 Proposed — confirm:** the spec already contains a complete, buildable answer, written so implementation isn't blocked, but nobody has explicitly confirmed it.
- **🟡 Open — not applied:** the spec has *not* been changed; the inconsistency stays until this is decided.

---

## Awaiting a decision

| # | Question | Status | Where it lives in the spec |
|---|---|---|---|
| 31 | [Published prices and billing options](#31-published-prices-and-billing-options) | 🟡 Open | [Pricing](https://adron.github.io/substratalapps.com/pricing/), [Pricing → Stripe catalog](https://adron.github.io/substratalapps.com/pricing/#stripe-catalog) |
| 32 | [Organization and Tenant Row-Level Security](#32-organization-and-tenant-row-level-security) | 🟡 Proposed — confirm | [NFR → Multi-tenancy](https://adron.github.io/substratalapps.com/non-functional-requirements/#multi-tenancy) |
| 33 | [Stripe webhook processing: inline, not SQS](#33-stripe-webhook-processing-inline-not-sqs) | 🟡 Proposed — confirm | [DEPLOYMENT.md → Webhook handling](DEPLOYMENT.md#webhook-handling) |
| 34 | [JWKS without CloudFront at Tier 0](#34-jwks-without-cloudfront-at-tier-0) | 🟡 Proposed — confirm | [DEPLOYMENT.md → Build checklist](DEPLOYMENT.md#build-checklist), [NFR → SLOs](https://adron.github.io/substratalapps.com/non-functional-requirements/#service-level-objectives) |
| 35 | [Seat cap on member-scope changes](#35-seat-cap-on-member-scope-changes) | 🟡 Proposed — confirm | [Pricing → Enforcement](https://adron.github.io/substratalapps.com/pricing/#enforcement) |
| 36 | [Test mode and seats](#36-test-mode-and-seats) | 🟡 Proposed — confirm | [Pricing → What "seat" means](https://adron.github.io/substratalapps.com/pricing/#what-seat-means-here) |
| 37 | [Audit immutability without a second database role](#37-audit-immutability-without-a-second-database-role) | 🟡 Proposed — confirm | [NFR → Audit](https://adron.github.io/substratalapps.com/non-functional-requirements/#audit), [Database Schema → audit_events](https://adron.github.io/substratalapps.com/domain-model/database-schema/#audit_events) |

### 31. Published prices and billing options

**Question 1:** are the [Pricing](https://adron.github.io/substratalapps.com/pricing/) dollar amounts final? Starter is free; Team is per-seat at $6 for seats 26 and up; Enterprise adds $750/month for `isolated` and $1,500/month for `dedicated_region` tenancy. The tiers, their structure, and how they're charged through Stripe are settled. Only the numbers are unconfirmed.

**Question 2:** which of these billing options ship at launch?
- annual plans
- a free trial for Team
- requiring a card on file for Starter

**Current assumption:** prices as listed, monthly USD billing only, no trial, and no card required for Starter. None of this blocks the build: prices are configuration in Stripe, not code, and each option is additive later.

### 32. Organization and Tenant Row-Level Security

Surfaced by the implementation (2026-10-06).

**Question:** [NFR → Multi-tenancy](https://adron.github.io/substratalapps.com/non-functional-requirements/#multi-tenancy) asks for RLS policies keyed on `app.current_org_id` and `app.current_tenant_id` that fail closed. Which org and tenant does a request set when the caller legitimately spans many: a User in several Organizations, a platform admin listing every Entitlement, a job sweeping every Tenant?

**What's built:** RLS enforces **test/live isolation** (`app.current_test_mode`) on every table a test credential can write, on both database backends. Tenant and Organization scoping is enforced in queries (app-confined keys, owner checks, explicit `tenant_id`/`organization_id` filters) and covered by integration tests, not by RLS.

**Proposal:** keep that for the `shared` tier, and add per-request allowed-set policies (`app.allowed_tenant_ids`, with an explicit platform bypass) only when an `isolated`/`dedicated_region` Tenant first needs a second line of defense. **Alternative:** design and add both policies now.

### 33. Stripe webhook processing: inline, not SQS

**Question:** [DEPLOYMENT.md → Webhook handling](DEPLOYMENT.md#webhook-handling) says to record the event, return `200`, and process from SQS.

**What's built:** the handler records the event id (insert-or-skip, so duplicates return `200` at once), then processes it in the same invocation, re-fetching the Subscription from Stripe. A failure leaves `processed_at` null, and the `stripe-events` job (every 10 minutes) re-fetches and reprocesses it.

**Proposal:** keep it inline; processing is one Stripe read and one transaction, well within Stripe's timeout, and the retry job gives the same durability as a queue. **Alternative:** add the SQS hop as specified.

### 34. JWKS without CloudFront at Tier 0

**Question:** the build checklist puts CloudFront in front of `/.well-known/jwks.json`, but JWKS lives on `api.substratalapps.com`, so caching just that path means putting CloudFront in front of the whole API, which [Scale-out](DEPLOYMENT.md#scale-out) lists as a later trigger.

**What's built:** JWKS is served by the API Lambda with `Cache-Control: public, max-age=3600`, and Applications cache it as the spec already tells them to.

**Proposal:** defer CloudFront to the Scale-out trigger. **Alternative:** add a CloudFront distribution for the whole domain now, to hold the 99.99% JWKS SLO through an API outage.

### 35. Seat cap on member-scope changes

**Question:** [Pricing → Enforcement](https://adron.github.io/substratalapps.com/pricing/#enforcement) lists "narrowing to an `allowlist` that includes new people" among writes rejected with `409 plan_limit_reached`.

**What's built:** personal grants, re-enables, and new org-wide grants are rejected at the cap. A `member_scope` change is never rejected; members it would add beyond a Starter Tenant's cap get `member_decision: seat_limit` instead, the same treatment the spec gives a member who joins past the cap.

**Proposal:** confirm that; it never cuts anyone off and matches the membership rule. **Alternative:** reject the scope change with `409`.

### 36. Test mode and seats

**Question:** seats count "non-test-mode Users". Does a Starter Tenant's seat cap gate test-mode grants?

**What's built:** no. Test-mode Users never count as seats, and seat limits never apply to test-mode writes, so live usage can't block integration tests and test traffic can't consume paid seats.

**Proposal:** confirm. **Alternative:** a separate test-mode cap.

### 37. Audit immutability without a second database role

**Question:** [NFR → Audit](https://adron.github.io/substratalapps.com/non-functional-requirements/#audit) gives the archival and erasure jobs a separate database role holding `UPDATE`/`DELETE` on `audit_events`. With the Data API, a second role means a second cluster secret and credential path.

**What's built:** a trigger rejects every `UPDATE`/`DELETE` on `audit_events` unless the transaction sets `app.audit_maintenance = 'on'`, which only those two jobs do. The guarantee is enforced by the database for every caller, including a direct SQL session.

**Proposal:** confirm the trigger. **Alternative:** add the second role and secret, and give only the jobs Lambda access to it.

---

## Resolved

Each answer lives in the spec page an implementer would read; this is only an index for older references by number.

| # | Decision | Where the answer lives |
|---|---|---|
| 1 | Identity provider: native auth now, per-Organization SSO architected | [Auth → Native auth and per-Organization SSO](https://adron.github.io/substratalapps.com/api-reference/auth/#native-auth-and-per-organization-sso) |
| 2 | Organizations pulled forward to Phase 2 | [Domain Model → Organization](https://adron.github.io/substratalapps.com/domain-model/users-and-organizations/#organization) |
| 3 | Applications are separately hosted, native or web | [Trust Model → Applications are separately hosted](https://adron.github.io/substratalapps.com/trust-model/#applications-are-separately-hosted) |
| 4 | Billing system of record: each developer's own | [Orders & Audit → Billing system of record](https://adron.github.io/substratalapps.com/domain-model/orders-and-audit/#billing-system-of-record) |
| 5 | Settings schema on file with the hub | [Settings → Schema validation](https://adron.github.io/substratalapps.com/domain-model/settings/#schema-validation) |
| 6 | Revocation must be immediate | [Trust Model → How fast does revocation need to land?](https://adron.github.io/substratalapps.com/trust-model/#how-fast-does-revocation-need-to-land) |
| 7 | Dedicated AWS account, `us-east-1` | [DEPLOYMENT.md → AWS account & region](DEPLOYMENT.md#aws-account--region) |
| 8 | Storage: Postgres `jsonb`, typed where declared | [Database Schema → Typed fields](https://adron.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-per-application-views-and-expression-indexes) (mechanism per #21) |
| 9 | App developer/publisher model and review lifecycle | [Applications → The review lifecycle](https://adron.github.io/substratalapps.com/domain-model/applications/#the-review-lifecycle) |
| 10 | Compliance scope: GDPR/CCPA now, SOC 2 posture now, HIPAA out | [Compliance & Data Protection](https://adron.github.io/substratalapps.com/compliance/) |
| 11 | Tenant and Organization are separate concepts | [Tenancy → Tenant vs. Organization](https://adron.github.io/substratalapps.com/domain-model/tenancy/#tenant-vs-organization) |
| 12 | Three tenancy tiers, support-gated tier changes | [Tenancy → Tiers](https://adron.github.io/substratalapps.com/domain-model/tenancy/#tiers), [How a tier change happens today](https://adron.github.io/substratalapps.com/domain-model/tenancy/#how-a-tier-change-happens-today) |
| 13 | Organization vs. User entitlement precedence | [Access Control → Organization vs. User precedence](https://adron.github.io/substratalapps.com/access-control/#organization-vs-user-precedence) |
| 14 | Agent API keys: `intended_use` + `restrict_destructive` | [API Keys → Agent keys & `restrict_destructive`](https://adron.github.io/substratalapps.com/api-reference/api-keys/#agent-keys--restrict_destructive) |
| 15 | Platform subscription billed through Stripe Billing | [Pricing → How the subscription is charged](https://adron.github.io/substratalapps.com/pricing/#how-the-subscription-is-charged) |
| 16 | App-token issuance: embedded login for first-party apps now, hosted authorization code + PKCE before any third-party app | [Auth → Getting an app token](https://adron.github.io/substratalapps.com/api-reference/auth/#getting-an-app-token) |
| 17 | App-scoped API keys may carry app-confined platform permissions | [API Keys → App-confined permissions](https://adron.github.io/substratalapps.com/api-reference/api-keys/#app-confined-permissions) |
| 18 | Concierge developer onboarding before Phase 3 | [Applications → Who can manage an Application's catalog entry](https://adron.github.io/substratalapps.com/domain-model/applications/#who-can-manage-an-applications-catalog-entry) |
| 19 | Derived `access.granted`/`access.revoked` webhooks; `allow()` checks user status | [Webhooks → Event types](https://adron.github.io/substratalapps.com/api-reference/webhooks/#event-types) |
| 20 | `order_id` is an opaque reference; billing permissions govern platform subscriptions | [Orders & Audit → Order](https://adron.github.io/substratalapps.com/domain-model/orders-and-audit/#order) |
| 21 | Typed settings via per-Application views and expression indexes | [Database Schema → Typed fields](https://adron.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-per-application-views-and-expression-indexes) |
| 22 | Any active User may create an Organization | [Organizations → `POST /v1/organizations`](https://adron.github.io/substratalapps.com/api-reference/organizations/#post-v1organizations) |
| 23 | Seat counting, Starter cap, daily Stripe quantity sync | [Pricing → What "seat" means here](https://adron.github.io/substratalapps.com/pricing/#what-seat-means-here) |
| 24 | A lapsed subscription restricts the Tenant; it never cuts off end users | [Pricing → Subscription lapse & downgrades](https://adron.github.io/substratalapps.com/pricing/#subscription-lapse--downgrades) |
| 25 | Stripe object model: Starter has no Subscription; Checkout, Portal, sales-led Enterprise | [Pricing → Stripe catalog](https://adron.github.io/substratalapps.com/pricing/#stripe-catalog) |
| 26 | 7-day erasure grace period | [Users → `POST /v1/users/{id}/erasure-requests`](https://adron.github.io/substratalapps.com/api-reference/users/#post-v1usersiderasure-requests) |
| 27 | Supported regions for `dedicated_region` | [Tenancy → tier-change requests](https://adron.github.io/substratalapps.com/api-reference/tenancy/#post-v1tenantsidtier-change-requests) |
| 28 | Amazon SES, Substratal-branded transactional email | [Auth → Email verification](https://adron.github.io/substratalapps.com/api-reference/auth/#email-verification) |
| 29 | Per-user, per-Application paths use `/apps/{appId}` | [Roles & Permissions → effective-permissions](https://adron.github.io/substratalapps.com/api-reference/roles-and-permissions/#get-v1usersidappsappideffective-permissions) |
| 30 | Test-mode data isolation | [API Keys → Test vs. live](https://adron.github.io/substratalapps.com/api-reference/api-keys/#test-vs-live) |
