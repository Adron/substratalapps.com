---
layout: default
title: MCP Server
nav_order: 9
---

# MCP Server
{: .no_toc }

A [Model Context Protocol](https://modelcontextprotocol.io) server that lets an AI agent or chat client operate the Platform API directly — list a user's entitlements, toggle one off, request a Tenant tier change — without the calling agent (or the person prompting it) hand-rolling REST calls.
{: .fs-6 .fw-300 }

1. TOC
{: toc }

---

## Why this exists

Everything in the [API Reference](../api-reference/) is already callable by any HTTP client. What an LLM-driven client specifically needs on top of that is a tool-call interface — a typed, discoverable list of operations it can invoke, with structured results it can reason over — rather than a human having to describe `curl` commands to it. MCP is the emerging standard shape for that interface; this server is Substratal's own implementation of it, aimed at the same audience as everything else in this spec: a support engineer's AI assistant, an internal ops agent, a customer's own agent calling `me`-scoped endpoints on their own behalf.

{: .note }
This is a new **surface**, not a new **capability**. Every tool call ultimately executes the exact same [Access Control](../access-control/) algorithm, the exact same permission checks, the exact same [Audit Event](../domain-model/orders-and-audit/#audit-event) trail as the equivalent REST call — see [Authentication](#authentication--no-new-model) below. Building this is translation work, not new business logic.

## What this is not

- **Not a new Application.** It doesn't appear in the [Application](../domain-model/applications/) catalog, doesn't hold an [Entitlement](../domain-model/entitlements/), and isn't owned by a [Tenant](../domain-model/tenancy/). It's a protocol gateway in front of the same API every other caller uses — closer in kind to the hub dashboard than to a downstream app.
- **Not a new trust relationship.** [Trust Model](../trust-model/) describes how a separately-hosted Application verifies a user's access; that's unrelated to this page, which describes how an agent *calls this hub's own API*. The two are complementary, not overlapping.
- **Not a replacement for the REST API or `openapi.yaml`.** It's a generated view over the same contract — see [Tool surface is generated, not hand-authored](#tool-surface-is-generated-not-hand-authored).

## Transport & endpoint

```
POST https://api.substratalapps.com/mcp
```

Implements MCP's **Streamable HTTP** transport: a single endpoint, JSON-RPC 2.0 request/response bodies, with the client's `Mcp-Session-Id` header (issued on `initialize`) tying together the calls in one session. Deliberately on the existing `api.substratalapps.com` domain rather than a new `mcp.` subdomain — no new Route 53 zone or ACM certificate needed, consistent with [Deployment Architecture → Cost principles](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md). Deliberately **outside** `/v1` — this endpoint isn't a REST resource, and MCP has its own protocol-version negotiation (a date-based version string exchanged during `initialize`) that's independent of this API's own `/v1` URL versioning — the same "versioned independently" pattern [Non-Functional Requirements → Versioning](../non-functional-requirements/#versioning) already applies to webhook payloads.

Tier 0's implementation is deliberately the simplest conforming option: every tool call returns one buffered JSON response, no server-initiated push, no mid-call progress notifications. See [Deployment Architecture → MCP server](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) for why that's also the cheapest option, and what upgrading past it looks like.

## Authentication — no new model

The MCP endpoint takes the exact same `Authorization: Bearer <token>` header as every other endpoint in [Conventions](../api-reference/conventions/#authentication) — a user access token, or a scoped [API Key](../api-reference/api-keys/). There is no MCP-specific credential, no separate consent/OAuth flow layered on top, and no elevated standing: the server holds no privilege of its own and simply forwards the caller's own credential to the underlying REST call it's translating a tool invocation into. A tool call a caller's credential isn't permitted to make fails exactly the way the equivalent `curl` call would — same `403`, same error `code`.

{: .important }
Because of this, the *effective* blast radius of connecting an agent to this server is entirely a function of which credential you hand it. Use an API Key created with `intended_use: "agent"`: it defaults to `restrict_destructive: true`, which blocks destructive operations server-side regardless of the key's permissions. An LLM deciding which tool to call is a different risk shape than deterministic service code doing the same thing. See [API Keys → Agent keys & `restrict_destructive`](../api-reference/api-keys/#agent-keys--restrict_destructive).

## Tool surface is generated, not hand-authored

Every MCP tool is generated from one operation in [openapi.yaml](../openapi.yaml) — its `operationId` becomes the tool name (dots replaced with underscores, `substratal_` prefixed), its parameters/request body become the tool's input schema, and its `summary`/`description` become the tool's description. This is a deliberate choice, not a shortcut: this spec already maintains `openapi.yaml` as the single machine-readable source of truth (see [Conventions → Machine-readable](../api-reference/conventions/#machine-readable)); a hand-authored, separately-maintained MCP tool list would be a *third* copy of the API surface to keep in sync, on top of the prose and the YAML. Generating it means adding an endpoint to `openapi.yaml` is the only step required to also add it as a tool — nothing here needs separate maintenance.

| `operationId` | MCP tool name | REST equivalent |
|---|---|---|
| `entitlements.listForUser` | `substratal_entitlements_listForUser` | `GET /v1/users/{id}/entitlements` |
| `entitlements.update` | `substratal_entitlements_update` | `PATCH /v1/entitlements/{id}` |
| `permissions.getEffective` | `substratal_permissions_getEffective` | `GET /v1/users/{id}/apps/{appId}/effective-permissions` |
| `tenants.requestTierChange` | `substratal_tenants_requestTierChange` | `POST /v1/tenants/{id}/tier-change-requests` |
| `audit.list` | `substratal_audit_list` | `GET /v1/audit-events` |

(Illustrative, not exhaustive — **every** operation in `openapi.yaml` generates a tool this way, and that count grows as the spec grows. Deliberately not stated as a number here — any specific count written on this page would be wrong again the next time an endpoint is added, which is exactly the kind of drift this generation scheme exists to avoid. See [openapi.yaml](../openapi.yaml) for the current, exact set; this page doesn't duplicate that list.)

{: .note }
**Build prerequisite:** every operation in `openapi.yaml` now carries an `operationId` (added alongside this page specifically so this generation scheme is actually buildable, not aspirational) — see the [Changelog](../changelog/) entry for this addition. Adding a new endpoint going forward means adding its `operationId` at the same time, following the `<resourceGroup>.<action>` convention already in use (`entitlements.update`, `tenants.requestTierChange`), or it won't get picked up by the generator.

## Tool annotations & safety

MCP's tool-definition schema supports annotations — `readOnlyHint`, `destructiveHint`, `idempotentHint` — that a compliant client (e.g. a chat app with a confirmation dialog) can use to decide whether to run a tool immediately or ask the user first. These are derived mechanically too, from information already in this spec rather than re-judged per tool:

| Annotation | Derived from |
|---|---|
| `readOnlyHint: true` | The operation is a `GET`. |
| `destructiveHint: true` | The operation carries `x-substratal-destructive` in [openapi.yaml](../openapi.yaml). That's either `always` or `conditional` (destructive only for certain request bodies; the hint is still `true`, since a client can't know in advance). The complete list is in [Destructive operations](#destructive-operations) below. |
| `idempotentHint: true` | The operation is a `GET`, `PATCH`, or `DELETE` (all idempotent in this API), or a `POST` that *requires* an `Idempotency-Key`. For those, the MCP server derives the key from a hash of the tool-call arguments, so a retried tool call can't double-apply. |

### Destructive operations

This table is the single source of truth. `openapi.yaml`'s `x-substratal-destructive` extension mirrors it operation by operation, and the server-side `restrict_destructive` check reads the same classification.

| Operation | When destructive |
|---|---|
| Every `DELETE` | Always. That covers: user soft-delete, Entitlement delete-in-error, Role delete, Role-assignment removal, Organization member removal, webhook unsubscribe, API Key revoke, session revoke, MFA support-reset, and erasure cancellation. |
| `PATCH /v1/entitlements/{id}` | When `status` becomes `disabled` or `revoked`. |
| `PATCH /v1/users/{id}` | When `status` becomes `suspended`. |
| `POST /v1/users/{id}/suspend` | Always. |
| `POST /v1/users/{id}/erasure-requests` | Always. |
| `PATCH /v1/organizations/{id}` | When `status` becomes `suspended`. |
| `PATCH /v1/organizations/{id}/members/{userId}` | When it demotes an `org_admin`. |
| `PATCH /v1/applications/{id}` | When `review_status` becomes `rejected` or `suspended`, or when `available_app_roles`/`permissions` shrink. |
| `PATCH /v1/roles/{id}` | When `permissions` shrink. |
| `POST /v1/api-keys/{id}/rotate` | Always. It breaks the current secret with no overlap. |
| `PATCH /v1/api-keys/{id}` | When `restrict_destructive` is set to `false`. |
| `POST /v1/tenants/{id}/tier-change-requests` and its `PATCH` | Always. They lead to a maintenance window. |
| `POST /v1/auth/logout` with `all_sessions: true` | Always. |

The same classification now has a second, server-side consumer, not just this client-facing hint: per [API Keys → Agent keys & `restrict_destructive`](../api-reference/api-keys/#agent-keys--restrict_destructive), an [API Key](../api-reference/api-keys/) with `restrict_destructive: true` — the default for `intended_use: "agent"` keys — gets a hard `403` on exactly the operations this table classifies as destructive, regardless of what permissions the key otherwise carries. One classification, two effects: a well-behaved MCP client uses `destructiveHint` to decide whether to ask a human first; the platform uses the identical rule to decide whether to allow the call at all, for keys explicitly scoped that way.

## Statelessness

An MCP session's state (negotiated protocol version, declared client capabilities) is kept entirely in the `Mcp-Session-Id` the server hands back on `initialize` — a short-lived, signed, self-contained token, not a database row. Tier 0 needs **no new table and no new database** for this: every individual tool call is a single stateless Lambda invocation that decodes the session token, forwards the caller's own Bearer credential to the matching REST call, and returns the result. See [Deployment Architecture → MCP server](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md) for when (and why) this stops being enough.

## Resources

Alongside tools, the server exposes a small, fixed set of MCP **Resources** — read-only context a client can fetch without it being a tool call — for an agent to ground itself before making tool calls: [`/llms.txt`](../llms.txt) (the page index) and [Glossary](../glossary/). Deliberately minimal; the tool surface is where the actual leverage is, and most of the rest of this site is already written to be read directly by an AI coding tool per [Getting Started → If you're an AI coding tool](../getting-started/#if-youre-an-ai-coding-tool).

## Where this runs

Build-out and AWS deployment — Tier 0 cost, and what triggers an upgrade — is specified alongside the rest of the platform's infrastructure, not duplicated here: see [Deployment Architecture → MCP server](https://github.com/Adron/substratalapps.com/blob/main/DEPLOYMENT.md).
