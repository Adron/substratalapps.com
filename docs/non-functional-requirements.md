---
layout: default
title: Non-Functional Requirements
nav_order: 9
---

# Non-Functional Requirements
{: .no_toc }

1. TOC
{: toc }

---

## Authentication

- **User-facing login:** OAuth2/OIDC. Whether the hub is its own identity provider or delegates to one (Auth0, Clerk, WorkOS, Cognito, …) is open — see [Decisions](../decisions/#1-identity-provider).
- **Service-to-service:** billing → hub, app → hub, and any other backend caller authenticates with scoped API keys or mTLS, not user credentials.
- **MFA:** supported at the identity-provider layer; not re-implemented in this API.
- **Bootstrapping:** the very first `superadmin` cannot be created through the public API — nothing can call `POST /v1/users/{id}/roles/{roleId}` with `roles.manage` before a `superadmin` exists to grant it. It's seeded directly against the database (or via a one-time, non-API admin CLI command) as part of standing up a new environment, not specified as an endpoint.

## CORS

`GET` endpoints intended for direct browser use by a future first-party UI (the catalog, a user's own entitlements/profile/settings via `me`) allow that UI's origin. Admin and service-to-service endpoints do not allow browser-origin requests at all — an admin console calls through its own backend, not directly from the browser with a platform API key. The exact allow-listed origin(s) are an environment-level config value, not part of this spec.

## Authorization enforcement point

The hub is the **only** writer of Entitlement and Role state. Apps read (via JWT claim, introspection, or webhook) — they never maintain a parallel copy they write to independently. If an app needs a role concept finer than what [App Roles](../domain-model/roles-and-permissions/) supports, that's a sign the app needs a new permission defined in its catalog entry, not a local workaround.

## Multi-tenancy

Every record that can belong to an Organization is scoped by `organization_id`, enforced at the data-access layer (row-level isolation), not just filtered in application code. This matters even before Organizations ship as a user-facing feature (see [Decisions](../decisions/#2-organizations)) — the column should exist and be enforced from day one so it isn't a migration later.

## Audit

Every write to an Entitlement, a Role assignment, or an admin-initiated change to a user's Profile or Settings produces an immutable [Audit Event](../domain-model/orders-and-audit/): actor, action, target, before/after snapshot, timestamp. Audit events are never deleted or edited, including when the record they describe is later deleted.

## Idempotency & retries

Billing webhooks retry. Admin tooling double-clicks. Any mutating endpoint that creates or transitions an Entitlement or Order-linked record **must** accept an `Idempotency-Key` header and return the original result on a repeated key rather than creating a duplicate. See [Conventions](../api-reference/conventions/#idempotency).

## Rate limiting

Per-API-key token bucket, returning `429` with a `Retry-After` header on exhaustion:

| Scope | Limit |
|---|---|
| Default, all endpoints | 100 requests/minute |
| `POST /v1/entitlements`, `POST /v1/users` | 20 requests/minute — tighter, since these are the endpoints scripted abuse would hit first |
| `GET /v1/users/{id}/applications/{appId}/effective-permissions` | 300 requests/minute — expected to be called on a hot path by downstream apps (see [Trust Model](../trust-model/)), so it's deliberately not bottlenecked at the default rate |

These are starting defaults, not a promise — tune them against real traffic once the API is live, and raise per-key limits for high-volume service integrations (billing) rather than exempting them from limiting entirely.

## Versioning

- `/v1` now. Additive, backward-compatible changes (new optional fields, new endpoints) ship without a version bump.
- A breaking change gets a new version prefix. The old version keeps working for a minimum 6-month deprecation window, announced in the [Changelog](../changelog/) the day the replacement ships, with a `Deprecation` and `Sunset` response header (RFC 8594) added to every response the old version serves from that point on.
- Webhook payload versions are versioned independently of the URL version, since webhook consumers can't negotiate a version the way a request-time client can.

## Data retention

- Audit Events: retained indefinitely (compliance system of record).
- Disabled/revoked Entitlements: retained, not deleted — re-enabling or investigating a dispute depends on the history.
- Deleted Users: soft-deleted first (status transition), with a separate, deliberate hard-delete process for right-to-erasure requests that cascades through Profile, Settings, and Entitlements while preserving the Audit trail of the deletion itself.

## Availability expectations on the trust model

Because downstream apps verify access against this API (see [Trust Model](../trust-model/)), this API's availability is a dependency of every app in the catalog, not just the hub dashboard. JWT verification keeps apps functioning through a brief hub outage; live introspection calls do not. This is a reason to lean on JWTs for routine checks and reserve introspection for genuinely sensitive actions, independent of the latency tradeoff discussed on that page.
