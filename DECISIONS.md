# Decisions

Where questions that need a product or business decision get asked. This file is a queue, not the record of what was decided: once a question is answered, the answer is written into the spec page(s) it affects, and its entry here moves to [Resolved](#resolved) as a one-line pointer to where the answer now lives.

## How to use this file

- **Asking for a decision:** add an entry under [Awaiting a decision](#awaiting-a-decision) with the next free number. State the question, what the spec currently assumes (if anything), the proposal, and the alternative. On every spec page the question affects, add an "Awaiting decision" callout linking back here:

  ```markdown
  {: .decision }
  **Proposed — confirm** ([DECISIONS.md #31](https://github.com/Adron/substratalapps.com/blob/main/DECISIONS.md#31-short-title)). Why, and the alternative.
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
| 16 | [Login surface & app-token issuance](#16-login-surface--app-token-issuance) | 🟡 Proposed — confirm | [Auth → Getting an app token](https://adron.github.io/substratalapps.com/api-reference/auth/#getting-an-app-token) |
| 17 | [App-scoped API key confinement](#17-app-scoped-api-key-confinement) | 🟡 Proposed — confirm | [API Keys → App-confined permissions](https://adron.github.io/substratalapps.com/api-reference/api-keys/#app-confined-permissions) |
| 18 | [Developer onboarding before Phase 3](#18-developer-onboarding-before-phase-3) | 🟡 Proposed — confirm | [Applications → Who can manage an Application's catalog entry](https://adron.github.io/substratalapps.com/domain-model/applications/#who-can-manage-an-applications-catalog-entry) |
| 19 | [Derived access webhooks & user status in access](#19-derived-access-webhooks--user-status-in-access) | 🟡 Proposed — confirm | [Webhooks → Event types](https://adron.github.io/substratalapps.com/api-reference/webhooks/#event-types) |
| 20 | [Order references & billing permissions](#20-order-references--billing-permissions) | 🟡 Proposed — confirm | [Orders & Audit → Order](https://adron.github.io/substratalapps.com/domain-model/orders-and-audit/#order) |
| 21 | [Typed settings storage implementation](#21-typed-settings-storage-implementation) | 🟡 Proposed — confirm | [Database Schema → Typed fields](https://adron.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-per-application-views-and-expression-indexes) |
| 22 | [Who can create an Organization](#22-who-can-create-an-organization) | 🟡 Proposed — confirm | [Organizations → `POST /v1/organizations`](https://adron.github.io/substratalapps.com/api-reference/organizations/#post-v1organizations) |
| 23 | [Seat counting, caps & Stripe quantity sync](#23-seat-counting-caps--stripe-quantity-sync) | 🟡 Proposed — confirm | [Pricing → What "seat" means here](https://adron.github.io/substratalapps.com/pricing/#what-seat-means-here) |
| 24 | [Subscription lapse & downgrade behavior](#24-subscription-lapse--downgrade-behavior) | 🟡 Proposed — confirm | [Pricing → Subscription lapse & downgrades](https://adron.github.io/substratalapps.com/pricing/#subscription-lapse--downgrades) |
| 25 | [Stripe object model & plan changes](#25-stripe-object-model--plan-changes) | 🟡 Proposed — confirm | [Pricing → Stripe catalog](https://adron.github.io/substratalapps.com/pricing/#stripe-catalog) |
| 26 | [Erasure grace period](#26-erasure-grace-period) | 🟡 Proposed — confirm | [Users → `POST /v1/users/{id}/erasure-requests`](https://adron.github.io/substratalapps.com/api-reference/users/#post-v1usersiderasure-requests) |
| 27 | [Supported regions for `dedicated_region`](#27-supported-regions-for-dedicated_region) | 🟡 Proposed — confirm | [Tenancy → tier-change requests](https://adron.github.io/substratalapps.com/api-reference/tenancy/#post-v1tenantsidtier-change-requests) |
| 28 | [Transactional email provider & branding](#28-transactional-email-provider--branding) | 🟡 Proposed — confirm | [Auth → Email verification](https://adron.github.io/substratalapps.com/api-reference/auth/#email-verification) |
| 29 | [Path naming: `/apps/` vs. `/applications/`](#29-path-naming-apps-vs-applications) | 🟡 Open — not applied | [Roles & Permissions → effective-permissions](https://adron.github.io/substratalapps.com/api-reference/roles-and-permissions/#get-v1usersidapplicationsappideffective-permissions) |
| 30 | [Test-mode data isolation](#30-test-mode-data-isolation) | 🟡 Proposed — confirm | [API Keys → Test vs. live](https://adron.github.io/substratalapps.com/api-reference/api-keys/#test-vs-live) |

### 16. Login surface & app-token issuance

**Question:** may first-party Applications use embedded login (the app renders its own sign-in form and calls `POST /v1/auth/login`, then `POST /v1/auth/app-tokens`) until Substratal's hosted sign-in page exists?

**Proposed:** two issuance paths. Embedded login is allowed now, for first-party Applications only, and blocked for any Application not owned by Substratal once self-service registration ships. Hosted authorization code + PKCE (`POST /v1/auth/oauth/authorization-codes`, `POST /v1/auth/oauth/token`) is fully specified API-side and ships with the dashboard; the hosted page is required before any third-party Application goes live.

**Alternative:** build the hosted login page in Phase 1 and never allow embedded login. More secure for the eventual marketplace, but the MVP then depends on a UI project.

### 17. App-scoped API key confinement

**Question:** may an app-scoped API Key carry a fixed set of platform permissions (`entitlements.manage`, `roles.manage`, `users.list`, `audit.view`), automatically confined to its own Application?

**Why it came up:** a developer's backend has to reflect its own billing outcomes into Entitlements, but an app-scoped key couldn't hold `entitlements.manage`, and a platform-scoped key that could would reach every Application.

**Proposed:** yes, with `users.manage`, `applications.manage`, `organizations.manage`, `api_keys.manage`, `tenants.manage`, `billing.manage`, and `billing.refund` never grantable to an app-scoped key.

**Alternative:** a platform-run "billing bridge" that developers post outcomes to. It adds a component without actually reducing risk.

### 18. Developer onboarding before Phase 3

**Question:** will paying external developers exist before Phase 3, when `POST /v1/applications` is still platform-admin-only?

**Proposed:** yes, through concierge onboarding. Staff create the Application with the customer as owner; the owner then self-serves configuration, app-scoped API Keys, webhooks, reading its Tenant, and their subscription. For an Organization-owned Application, "owner" means any `org_admin` of the owning Organization.

**Alternative:** no paying external developers before Phase 3, in which case Pricing/Stripe can slip to Phase 3 with the marketplace.

### 19. Derived access webhooks & user status in access

**Question:** should the required revocation webhooks be derived, per-user access events rather than raw resource events?

**Why it came up:** `entitlement.revoked`/`entitlement.disabled`/`role.removed` missed real ways a user loses access: an org-wide grant disabled (the event carries only `organization_id`), removal from an Organization, a `member_scope` exclusion, and a suspended or deleted user. The access algorithm also didn't check user status.

**Proposed:** `allow()` also requires `user.status == "active"`. Add `access.granted`/`access.revoked`, fired once per affected (user, Application) pair whenever resolved access flips, with the cause in `reason`. The required subscription set becomes `access.revoked` + `role.removed`. The `entitlement.*` events stay for consumers who want raw record changes.

### 20. Order references & billing permissions

**Question:** is `order_id` just an opaque, developer-supplied reference, and what do `billing.manage`/`billing.refund` govern?

**Proposed:** `order_id` is an opaque string of up to 255 characters, typically the developer's own Stripe subscription or invoice id, with no entity, table, endpoint, or validation beyond length. `billing.manage` lets support view any Tenant's subscription and usage and open its billing portal. `billing.refund` is reserved for Stripe credits and refunds, done in the Stripe dashboard today, so it has no endpoint yet.

### 21. Typed settings storage implementation

**Question:** should typed settings fields use per-Application expression indexes and typed views instead of generated columns on the shared `app_settings` table?

**Why it came up:** generated columns collide when two Applications declare the same key with different types, hit Postgres's 1,600-column cap, and make every insert fail if a cast stops matching a changed schema.

**Proposed:** per declared scalar property, a partial expression index using a safe-cast function that returns `null` on mismatch; per Application, a typed view `app_settings_<slug>`. No new physical columns. No endpoint queries settings across users today, so add one only if a real Application needs it.

### 22. Who can create an Organization

**Question:** may any authenticated, active User create an Organization?

**Proposed:** yes. The creator becomes its first `org_admin`, creation is rate-limited to 10 per user per day, and platform `organizations.manage` is still required to suspend one. The guard: an org admin can never *create* an org-wide grant for their own Organization; only whoever controls the Application can.

**Alternative:** keep creation admin-only until Phase 3.

### 23. Seat counting, caps & Stripe quantity sync

**Question:** how are seats counted and capped, and how does the count reach Stripe?

**Proposed:**

- A seat is a distinct non-test-mode User whose *resolved* access is `active` to at least one of the Tenant's Applications.
- Starter has a hard cap of 1,000 (`409 plan_limit_reached`, `resource: "seats"`); org-wide grants count every included member.
- Team has no cap; seats 26 and up are billed at $6 each.
- Enterprise has no hard cap; the contracted seat price applies.
- A daily job (00:15 UTC) writes the count to the seat subscription item's `quantity` with `proration_behavior: "none"`.

**Alternative:** real-time sync with prorations. It's more accurate, but noisier for customers and makes more Stripe API calls.

### 24. Subscription lapse & downgrade behavior

**Question:** what happens when a paid subscription lapses?

**Proposed:**

- `past_due`: full service continues while Stripe retries.
- On lapse, a User-owned Tenant whose usage fits Starter drops to `plan: starter`. Otherwise, including every Organization-owned Tenant, `plan` stays as it was and the Tenant becomes `restricted: true`.
- Restricted: reads, existing grants, and every end user's access keep working. New Applications, AppRoles, webhook subscriptions, and seat-adding grants return `402 subscription_required`. Resubscribing clears it.
- Team → Starter downgrades go through cancel-at-period-end and are only allowed when usage fits Starter.

**Alternative:** suspend the Tenant on lapse. Rejected as proposed, because it would cut off end users who did nothing wrong.

### 25. Stripe object model & plan changes

**Proposed:** Starter Tenants have a Stripe Customer but no Subscription. Team upgrades use Stripe Checkout (`POST /v1/tenants/{id}/billing/checkout-sessions`). Payment method, invoices, and cancellation go through the Customer Portal (`POST /v1/tenants/{id}/billing/portal-sessions`). Enterprise is sales-led, created in Stripe by staff and synced in by webhook. Billing is monthly, USD only, and tax-exclusive with Stripe Tax.

**Still open, none blocking the build:** annual plans, a Team free trial, and whether Starter requires a card on file.

### 26. Erasure grace period

**Question:** when does the hard-delete cascade run after an erasure request?

**Proposed:** `POST /v1/users/{id}/erasure-requests` soft-deletes immediately and runs the cascade 7 days later, inside GDPR's 30-day ceiling. Support can cancel it within those 7 days if it was made in error or under account takeover.

**Alternatives:** run the cascade immediately (no recovery), or wait the full 30 days.

### 27. Supported regions for `dedicated_region`

**Proposed initial allow-list:** `us-east-1` (the shared default, also valid as a dedicated target), `us-west-2`, `ca-central-1`, `eu-west-1`, `eu-central-1`, `ap-southeast-2`. Any other region returns `422 unsupported_region`. Adding a region is a support/ops decision with no API change.

### 28. Transactional email provider & branding

**Proposed:** Amazon SES in the same AWS account, so there's no new sub-processor. Substratal-branded templates at MVP; an Application may set `email_from_name` and `support_url` so a message triggered from inside it names the app. A full per-Application custom domain and template is deferred.

### 29. Path naming: `/apps/` vs. `/applications/`

**Question:** which spelling do per-user, per-Application sub-resources use? Today it's `/v1/users/{id}/apps/{appId}/profile|settings` but `/v1/users/{id}/applications/{appId}/effective-permissions`.

**Not applied:** the effective-permissions path is referenced on about 15 pages and in `openapi.yaml`.

**Recommendation:** standardize on `/apps/{appId}` before the first endpoint is implemented; renaming is free until then and a breaking change after.

### 30. Test-mode data isolation

**Question:** how does test-mode data avoid colliding with live data in the same database?

**Proposed:** uniqueness constraints include `test_mode` (e.g. `unique (email, test_mode) where deleted_at is null`). A test-mode credential sees only test-mode rows and a live credential only live rows, enforced by the same Row-Level Security mechanism as `tenant_id`. Test-mode rows older than 30 days are purged nightly.

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
| 8 | Storage: Postgres `jsonb`, typed where declared | [Database Schema → Typed fields](https://adron.github.io/substratalapps.com/domain-model/database-schema/#typed-fields-per-application-views-and-expression-indexes) (mechanism revised by [#21](#21-typed-settings-storage-implementation)) |
| 9 | App developer/publisher model and review lifecycle | [Applications → The review lifecycle](https://adron.github.io/substratalapps.com/domain-model/applications/#the-review-lifecycle) |
| 10 | Compliance scope: GDPR/CCPA now, SOC 2 posture now, HIPAA out | [Compliance & Data Protection](https://adron.github.io/substratalapps.com/compliance/) |
| 11 | Tenant and Organization are separate concepts | [Tenancy → Tenant vs. Organization](https://adron.github.io/substratalapps.com/domain-model/tenancy/#tenant-vs-organization) |
| 12 | Three tenancy tiers, support-gated tier changes | [Tenancy → Tiers](https://adron.github.io/substratalapps.com/domain-model/tenancy/#tiers), [How a tier change happens today](https://adron.github.io/substratalapps.com/domain-model/tenancy/#how-a-tier-change-happens-today) |
| 13 | Organization vs. User entitlement precedence | [Access Control → Organization vs. User precedence](https://adron.github.io/substratalapps.com/access-control/#organization-vs-user-precedence) |
| 14 | Agent API keys: `intended_use` + `restrict_destructive` | [API Keys → Agent keys & `restrict_destructive`](https://adron.github.io/substratalapps.com/api-reference/api-keys/#agent-keys--restrict_destructive) |
| 15 | Platform subscription billed through Stripe Billing | [Pricing → How the subscription is charged](https://adron.github.io/substratalapps.com/pricing/#how-the-subscription-is-charged) |
