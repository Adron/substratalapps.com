---
layout: default
title: Glossary
nav_order: 16
---

# Glossary
{: .no_toc }

Precise definitions for every term used across this site. If a page uses a capitalized term (`Entitlement`, `AppRole`) without defining it inline, it means exactly what's written here.
{: .fs-6 .fw-300 }

---

#### Access path
One way a User reaches an Application: their own personal [Entitlement](#entitlement), or an org-wide grant from an [Organization](#organization) that includes them. Resolved access is the union of every active path. See [Access Control](../access-control/#the-algorithm).

#### API Key
A long-lived service credential (`satk_live_…`/`satk_test_…`), scoped to the platform or to one Application. An app-scoped key can only ever touch its own Application's rows. See [API Keys](../api-reference/api-keys/).

#### App token
The short-lived (5-minute) JWT an Application verifies locally, with `aud` set to that Application. Carries `entitlement_status` and `effective_permissions`. See [Auth → The app token](../api-reference/auth/#the-app-token).

#### Application
(also "App") One developer's separately-hosted app (web, iOS, macOS, Windows, Linux, …) that uses Substratal Apps for its users, settings, organizations, tenancy, and storage. This is its catalog entry. See [Domain Model → Applications](../domain-model/applications/).

#### AppProfile
Per-app identity data for one user — e.g. a display handle meaningful only inside that one app. Distinct from the global [Profile](#profile).

#### AppRole
A [Role](#role) scoped to one Application, defined by that app's own catalog entry rather than by the platform. See [Domain Model → Roles & Permissions](../domain-model/roles-and-permissions/).

#### AppSettings
Per-app configuration for one user, layered over that user's global [Settings](#settings) and the Application's declared defaults. See [Workflows → Settings resolution](../workflows/#settings-resolution-in-practice).

#### Audit Event
An immutable record of who changed what access, when. See [Domain Model → Orders & Audit](../domain-model/orders-and-audit/).

#### Effective permissions
The resolved set of [Permissions](#permission) a user holds for a given Application — the union of the permissions on their app-scoped Roles for that app (explicit assignments plus the app's `default_app_role`), and empty unless the User and their Entitlement are both active. Only that app's own `app.<slug>.*` keys ever appear; platform Roles govern the hub, not apps. See [Access Control](../access-control/#step-2-effective-permissions).

#### Entitlement
The join record between a User (or Organization) and an Application: do they own it, and is it currently switched on. The record behind "turn an app on/off for a user." See [Domain Model → Entitlements](../domain-model/entitlements/).

#### Hub
Shorthand used throughout this site for Substratal Apps itself — the platform this API belongs to.

#### MCP
Model Context Protocol — the standard this site's AI-agent-facing tool-call interface implements. See [MCP Server](../mcp-server/). Introduces no new authorization model of its own; see [Authentication — no new model](../mcp-server/#authentication--no-new-model).

#### Order
Not an entity of this API. It's the developer's own commerce record, in their own billing system. An [Entitlement](#entitlement) carries only an opaque `order_id` reference to it. See [Domain Model → Orders & Audit](../domain-model/orders-and-audit/#order).

#### Organization
A domain/grouping object for Users — a company, or a group within a company — used to organize shared admin standing and group-wide Application access. Decoupled from infrastructure placement; see [Tenant](#tenant) and [Tenant vs. Organization](../domain-model/tenancy/#tenant-vs-organization). Pulled into [Phase 2](../roadmap/#phase-2); see [Organizations](../domain-model/users-and-organizations/#organization).

#### OrganizationMembership
The join record between a User and an Organization, carrying that User's standing *within* that one Organization (`org_admin` or `member`) — a User can hold this for any number of Organizations at once. See [Domain Model → Users & Organizations](../domain-model/users-and-organizations/#organizationmembership).

#### Permission
An atomic, checkable capability, written as a dotted key (`billing.manage`, `app.timetrack.export`). Never assigned directly to a user — always granted through a [Role](#role).

#### Plan
The commercial subscription on a [Tenant](#tenant): `starter`, `team`, or `enterprise`. Independent of [Tier](#tier). See [Pricing](../pricing/).

#### PlatformRole
A [Role](#role) that applies across the whole hub rather than one Application — `superadmin`, `support`, `billing_admin`, `member`.

#### Profile
Global, cross-app identity and contact data for a User — name, avatar, email, locale. Distinct from [AppProfile](#appprofile).

#### Restricted
The state of a [Tenant](#tenant) whose subscription lapsed while its usage exceeded what it could drop to. Existing access keeps working; new usage returns `402`. See [Pricing → Subscription lapse](../pricing/#subscription-lapse--downgrades).

#### Role
A named bundle of [Permissions](#permission). Either a [PlatformRole](#platformrole) or an [AppRole](#approle).

#### Seat
A distinct User with active resolved access to at least one Application a [Tenant](#tenant) owns. The unit Team and Enterprise are billed on. See [Pricing](../pricing/#what-seat-means-here).

#### Session
One successful login (`ses_…`). Every refresh token and app token minted from it carries its id as `sid`, and revoking it ends them all. See [Auth → Sessions](../api-reference/auth/#sessions).

#### Settings
Global, cross-app configuration for a User — locale, timezone, notification preferences. Distinct from [AppSettings](#appsettings).

#### Tenant
The infrastructure-placement and data-isolation boundary for one of Substratal's own paying customers — the owner of an Application's catalog entry. `shared` (default), `isolated`, or `dedicated_region`. Not the same thing as [Organization](#organization) — see [Tenant vs. Organization](../domain-model/tenancy/#tenant-vs-organization). See [Domain Model → Tenancy](../domain-model/tenancy/).

#### Tier
Where a [Tenant](#tenant)'s data physically lives: `shared`, `isolated`, or `dedicated_region`. Independent of [Plan](#plan), except that the non-`shared` tiers require Enterprise.

#### User
A person with an account on Substratal. One identity, used everywhere in the hub and, via the [Trust Model](../trust-model/), in every app they launch from it.

#### Webhook
A signed HTTPS POST the hub sends an Application when something changes. `access.revoked` and `role.removed` are required subscriptions. See [Webhooks](../api-reference/webhooks/).
