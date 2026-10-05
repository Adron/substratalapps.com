# Substratal Apps

The user/organization/tenancy/settings/storage API layer an app developer would otherwise have to build themselves — comparable in shape to Auth0, Clerk, or WorkOS, scoped around everything a multi-tenant app needs: accounts, roles and permissions, per-app settings, team/tenancy structure, and per-user storage, all behind one API. When your business is building applications — on iOS, macOS, Windows, Linux, or the web — Substratal Apps removes the user-management burden so you can focus on the application itself.

**Full API specification: [adron.github.io/substratalapps.com](https://adron.github.io/substratalapps.com/)**

## Where things are

This repository is split deliberately, by audience:

| Audience | Lives | Why |
|---|---|---|
| **An API consumer** — a developer building an Application on this platform, or their AI coding agent | [`docs/`](docs/), published at [adron.github.io/substratalapps.com](https://adron.github.io/substratalapps.com/) | The API specification, and nothing else: domain model, access control, every endpoint and schema, workflows, the MCP server for agent callers, pricing. Everything a consumer needs to build *against* this API. |
| **Whoever is building *this* API** | The project root (this file and its siblings) | Deployment/infrastructure, the implementation build order, and the internal decision log aren't things a consumer needs to use the API — they're how it gets built and run. Kept separate so the docs site stays focused, per its own [Home page](https://adron.github.io/substratalapps.com/#what-this-site-is). |

Root-level documents:

- **[PLAN.md](PLAN.md)** — the build order: what to implement first, which spec page governs each piece, phase-by-phase exit criteria.
- **[DEPLOYMENT.md](DEPLOYMENT.md)** — AWS infrastructure (cost-capped, Aurora Serverless v2, Lambda, no VPC/NAT at Tier 0), Stripe Billing implementation, the MCP servers recommended for building against AWS/Postgres/Stripe, and the scale-out triggers for when any of this needs to change.
- **[LICENSE](LICENSE)** — MIT.
- **[ORIGINAL-SPEC-DRAFT.md](ORIGINAL-SPEC-DRAFT.md)** — the original single-document draft this whole spec was elaborated from. Kept for history, moved here (out of `docs/`) specifically so it can't be mistaken for current spec — it contradicts several later decisions.

Not yet moved to the root, but should be, by the same reasoning above — tracked so this doesn't get silently forgotten:

- `docs/decisions.md` — the open-questions/decision log. Internal engineering record, not API specification, but moving it touches ~25 cross-referencing pages across the docs site, so it's deferred to its own pass rather than rushed alongside everything else that landed with this README.
- `docs/roadmap.md` — feature phasing (MVP/Phase 2/Phase 3). Same reasoning, smaller blast radius (~8 files).
- `docs/domain-model/database-schema.md` — Postgres implementation detail (types, constraints, indexes), explicitly written for an implementer rather than an API caller. Same reasoning.

Deliberately **staying** on the docs site despite being borderline: `compliance.md` (a prospective developer's own compliance due-diligence is part of evaluating whether to build on this platform) and `changelog.md` (standard practice for an API's own consumers to track what changed). Revisit either if that judgment turns out wrong.

## Status

**Specification-complete for an MVP build; implementation has not started.** The API described at [adron.github.io/substratalapps.com](https://adron.github.io/substratalapps.com/) is a target contract, not a running service — there is no code in this repository yet beyond the documentation site itself and this planning layer. See [PLAN.md](PLAN.md) for the build order and [the live Decisions page](https://adron.github.io/substratalapps.com/decisions/) for the full log — every numbered decision is currently 🟢 Resolved, including [#1, Identity provider](https://adron.github.io/substratalapps.com/decisions/#1-identity-provider) (native auth, real and in-house from Phase 1; per-Organization SSO architected now, broker integration deliberately deferred). It's a living log, not a one-time list — check it before starting a phase anyway, since a resolved decision can still gain new rows as real implementation surfaces a sub-question nobody asked yet.

## Working on the docs site locally

```bash
cd docs
bundle install
bundle exec jekyll serve
```

Requires Ruby/Bundler. The site is pinned to the exact `github-pages` gem version GitHub's own Pages build uses, so a local build matches what actually deploys — see the comment in `docs/Gemfile`.

## Docs versions and publishing

The docs site is versioned automatically. Every push to `main` that touches `docs/` runs [`.github/workflows/docs.yml`](.github/workflows/docs.yml), which:

1. Assigns the next docs version: a patch bump by default. Put `[docs:minor]` or `[docs:major]` in any commit message in the push to bump further, or run the workflow by hand from the Actions tab and pick the bump.
2. Builds that version as a frozen, self-contained snapshot (its own nav, search, and links, labelled archived, linking back to latest). It commits the snapshot to the **`docs-versions`** branch along with `manifest.yml`, the version history. CI never commits to `main`.
3. Builds the live site, adds every archived snapshot under `/versions/<version>/`, and deploys it to GitHub Pages.

The current version shows in the top right of every page and links to the [Versions](https://adron.github.io/substratalapps.com/versions/) page. A local `jekyll serve` has no version data and shows `local` instead. To reproduce the full versioned build locally, check out `docs-versions` into a folder and run:

```bash
git worktree add ../docs-versions docs-versions
scripts/docs-build.rb --archive ../docs-versions --out /tmp/docs-site
```

Don't commit the result from a local run; only the GitHub build should write to `docs-versions`.

## License

[MIT](LICENSE).
