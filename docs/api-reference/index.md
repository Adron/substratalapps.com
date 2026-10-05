---
layout: default
title: API Reference
nav_order: 6
has_children: true
---

# API Reference
{: .no_toc }

Resource-oriented REST, JSON, versioned at `/v1`. Read [Conventions](conventions/) first — every other page here assumes it.
{: .fs-6 .fw-300 }

| Group | Covers |
|---|---|
| [Conventions](conventions/) | Base URL, auth header, pagination, idempotency, error shape, ID format. |
| [Auth](auth/) | Signup, login, MFA, sessions, password and email lifecycle, invitations, and app tokens (embedded, and hosted + PKCE). |
| [Users](users/) | Account lifecycle. |
| [Profiles](profiles/) | Global and per-app identity data. |
| [Settings](settings/) | Global and per-app configuration. |
| [Applications](applications/) | The app catalog. |
| [Entitlements](entitlements/) | Grant, toggle, revoke access to an app — the on/off switch. |
| [Roles & Permissions](roles-and-permissions/) | Assign/remove roles; resolve effective permissions. |
| [Organizations](organizations/) | Team/seat management. |
| [Tenancy](tenancy/) | Where a Tenant's data lives, and the (support-only) process to change it. |
| [Billing](billing/) | The Application owner's own Substratal subscription: plan, usage, Stripe Checkout and Portal. |
| [Audit](audit/) | Query the audit log. |
| [Webhooks](webhooks/) | Subscribe to access-change events, including the required `access.revoked`. |
| [API Keys](api-keys/) | Service-to-service credentials — how billing or an app's own backend authenticates. |

{: .note }
These pages specify the target contract for an API that does not yet exist. If you're implementing against this, these are the shapes to build toward — check the repository's actual code for what's already real before assuming a page here is live.

Calling this from an AI agent rather than writing HTTP calls by hand? See [MCP Server](../mcp-server/) — a tool-call interface generated from [openapi.yaml](../openapi.yaml), not a separate contract from the one on this page.
