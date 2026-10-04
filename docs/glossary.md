---
layout: default
title: Glossary
nav_order: 11
---

# Glossary
{: .no_toc }

Precise definitions for every term used across this site. If a page uses a capitalized term (`Entitlement`, `AppRole`) without defining it inline, it means exactly what's written here.
{: .fs-6 .fw-300 }

---

#### Application
(also "App," "Product") A catalog entry for a product or service Substratal sells. The thing a user ultimately gets routed to from the hub. See [Domain Model → Applications](../domain-model/applications/).

#### AppProfile
Per-app identity data for one user — e.g. a display handle meaningful only inside that one app. Distinct from the global [Profile](#profile).

#### AppRole
A [Role](#role) scoped to one Application, defined by that app's own catalog entry rather than by the platform. See [Domain Model → Roles & Permissions](../domain-model/roles-and-permissions/).

#### AppSettings
Per-app configuration for one user, layered over that user's global [Settings](#settings) and the Application's declared defaults. See [Workflows → Settings resolution](../workflows/#settings-resolution-in-practice).

#### Audit Event
An immutable record of who changed what access, when. See [Domain Model → Orders & Audit](../domain-model/orders-and-audit/).

#### Effective permissions
The resolved set of [Permissions](#permission) a user holds for a given Application — the union of their platform roles' permissions and their app-scoped roles' permissions for that app. See [Access Control](../access-control/#step-2-effective-permissions).

#### Entitlement
The join record between a User (or Organization) and an Application: do they own it, and is it currently switched on. The record behind "turn an app on/off for a user." See [Domain Model → Entitlements](../domain-model/entitlements/).

#### Hub
Shorthand used throughout this site for Substratal Apps itself — the platform this API belongs to.

#### Order
(also "Subscription") The commerce record an Entitlement traces back to. Owned by billing; referenced, not duplicated, here. See [Decisions → Billing system of record](../decisions/#4-billing-system-of-record).

#### Organization
A billing/access group of Users — e.g. a team plan. Optional in the current draft; see [Decisions → Organizations](../decisions/#2-organizations).

#### Permission
An atomic, checkable capability, written as a dotted key (`billing.manage`, `app.timetrack.export`). Never assigned directly to a user — always granted through a [Role](#role).

#### PlatformRole
A [Role](#role) that applies across the whole hub rather than one Application — `superadmin`, `support`, `billing_admin`, `member`.

#### Profile
Global, cross-app identity and contact data for a User — name, avatar, email, locale. Distinct from [AppProfile](#appprofile).

#### Role
A named bundle of [Permissions](#permission). Either a [PlatformRole](#platformrole) or an [AppRole](#approle).

#### Settings
Global, cross-app configuration for a User — locale, timezone, notification preferences. Distinct from [AppSettings](#appsettings).

#### User
A person with an account on Substratal. One identity, used everywhere in the hub and, via the [Trust Model](../trust-model/), in every app they launch from it.
