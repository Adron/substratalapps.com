---
layout: default
title: Home
nav_order: 1
description: "Substratal Apps is the API that gives app developers user accounts, settings, organizations/tenancy, and per-user storage, so they can focus on their own app instead of rebuilding that layer. This is the API-first specification."
permalink: /
---

# Substratal Apps — Platform API
{: .fs-9 }

When your business is building applications, Substratal Apps removes the user-management burden: one API for user accounts, settings, organizations/tenancy, and per-user storage, so a developer can focus entirely on the app they're actually trying to build.
{: .fs-6 .fw-300 }

This site specifies that **API**: who a user is, what they're allowed to touch within a given app, and how each app's own settings and data are stored and scoped. It is **API-first** — no end-user interface is specified here. A dashboard that makes onboarding easier is planned, but it's a client of this API, built later, not a requirement to use it today.
{: .fs-5 .fw-300 }

---

## What Substratal Apps actually is

Think of it as the user/organization/tenancy layer that an app developer would otherwise have to build themselves — comparable in shape to Auth0, Clerk, or WorkOS, but scoped around the full set of things a multi-tenant app needs from day one, not just login: accounts, per-app and per-org settings, team/tenancy structure, and a place to store the app's own per-user data, all behind one API.

An **Application** in this spec is one developer's app, built on top of Substratal Apps for that layer. Today, every Application is built by Substratal itself; a marketplace where outside developers register and manage their own Applications is an explicit later phase — see [Decisions → App developer/publisher model](decisions/#9-app-developerpublisher-model). The **Entitlement** that gates a user's access to an app, and the billing relationship behind it, belongs to that app's own developer — Substratal Apps tracks entitlement state, it doesn't run payments; see [Decisions → Billing system of record](decisions/#4-billing-system-of-record).

## What this site is

This is a living specification and the system of record for the platform API. It isn't a one-time design doc — as decisions get made and the API gets built, this site is where that gets reflected. If this site and a conversation, a Slack thread, or an old draft disagree, this site wins; update it the moment something here goes stale.

It's written to be read by people building the API **and** by AI coding tools asked to work against it — see [`/llms.txt`](llms.txt) for a machine-oriented index of every page.

## How it's organized

| Section | Answers |
|---|---|
| [Getting Started](getting-started/) | Where to start if you're new to this — human or AI tool. |
| [Quickstart](quickstart/) | A runnable curl walkthrough — create a user, grant an app, check access, turn it off. |
| [Domain Model](domain-model/) | What are the core entities, and how do they relate? |
| [Access Control](access-control/) | Given a user and an app, what decides if a request is allowed? |
| [API Reference](api-reference/) | What are the actual resources, endpoints, and payloads? |
| [Workflows](workflows/) | How do the pieces move together for real scenarios — a purchase, an admin revoking access, a role change? |
| [Trust Model](trust-model/) | How does a separately-hosted app verify a user's access without maintaining its own user table? |
| [MCP Server](mcp-server/) | How does an AI agent call this API directly, as a tool-using client rather than writing HTTP calls by hand? |
| [Non-Functional Requirements](non-functional-requirements/) | Security, multi-tenancy, audit, rate limits, versioning. |
| [Compliance & Data Protection](compliance/) | Which of SOC 2, HIPAA, GDPR, and CCPA apply, and when to act on each. |
| [Roadmap](roadmap/) | What ships in the MVP vs. later phases. |
| [Pricing](pricing/) | Starter, Team, and Enterprise — what Substratal itself charges the developer. |
| [Deployment Architecture](deployment-architecture/) | Where this runs — the cost-capped first deployment, and the path to scale. |
| [Decisions](decisions/) | The open questions this spec depends on, and their current status. |
| [Changelog](changelog/) | What's changed in this spec over time. |
| [Glossary](glossary/) | Precise definitions for every term used here. |

## The one idea worth remembering

Two questions get asked, separately, every time a user touches an app:

1. **Is the app itself switched on for this user?** This is the **Entitlement** — a literal on/off record, independent of what the user could do if it were on. An admin flipping a user's access to an app off is a write to this record, nothing else.
2. **If it's on, what can they do?** This is the **Role** — platform-wide or scoped to that one app — resolved into a set of **Permissions**.

Everything else in this spec (profiles, settings, orders, audit) hangs off of that split. See [Access Control](access-control/) for the full model.

{: .note }
This site is self-hosting its own context: it's generated from Markdown committed to this same repository, under `docs/`, via GitHub Pages. See the repo's `docs/specs/` folder for the original single-document draft this site was built out from.
