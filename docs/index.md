---
layout: default
title: Home
nav_order: 1
description: "Substratal Apps is the hub customers use to reach every app, product, and service they've purchased from Substratal. This is the API-first specification for that hub."
permalink: /
---

# Substratal Apps — Platform API
{: .fs-9 }

Substratal Apps is the hub a customer lands on to reach every app, product, and service they've purchased from Substratal, sign into all of them with one identity, and manage their account across the whole catalog.
{: .fs-6 .fw-300 }

This site specifies the **API** behind that hub: who a user is, what they're allowed to touch, and how each app they own is configured for them. It is **API-first** — no end-user interface is specified here. The interface (dashboard, admin console, whatever shape it takes) is a separate, later project that will be built as a client of this API.
{: .fs-5 .fw-300 }

---

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
| [Non-Functional Requirements](non-functional-requirements/) | Security, multi-tenancy, audit, rate limits, versioning. |
| [Roadmap](roadmap/) | What ships in the MVP vs. later phases. |
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
