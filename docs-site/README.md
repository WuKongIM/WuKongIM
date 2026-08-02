# WuKongIM v3 Documentation Site

This directory contains the standalone [Fumadocs](https://fumadocs.dev/)
application for the public WuKongIM v3 documentation. Phase 1 established the
bilingual shell and complete menu plan. Phase 2 publishes the first complete
onboarding path: product orientation, core concepts, source-based single-node
cluster startup, two-way message verification, and basic configuration. Phase 3
publishes the business-integration path: responsibility boundaries,
authentication constraints, messaging, and webhooks. Phase 4 publishes the
server-deployment path: deployment selection, source-built Docker and Linux
artifacts, static multi-node planning, readiness, and production checks. Phase 5
publishes the server-configuration path: cluster identity, network contracts,
storage and workload controls, security boundaries, observability, and an
exhaustive bilingual TOML-to-environment reference.

## Develop

Requires Bun.

```bash
bun install
bun run dev
```

Open `http://localhost:3000`. The canonical local entry points are `/zh` and
`/en`.

## Validate

```bash
bun run verify
```

The verification suite checks the navigation contract, redirect seed, generated
menu plan, lint and TypeScript, static export, language-isolated search indexes,
the inclusion of every published route, and the exclusion of planned routes
from sitemap and LLM outputs.

## Content lifecycle

- Edit the full bilingual plan in `lib/navigation.ts`.
- Run `bun run navigation:write` to update `NAVIGATION.md`.
- Add both `page.mdx` and `page.en.mdx` content variants before changing a menu
  entry from `planned` to `published`.
- Keep planned routes visible, but never include them in public indexes.
- Treat `redirects.json` as a non-exhaustive migration seed, not a deployment
  configuration.

See `FLOW.md` for the publishing flow, `PHASE_1_SPEC.md` for the shell scope,
`PHASE_2_SPEC.md` for the onboarding scope, and `PHASE_3_SPEC.md` for the
business-integration scope. `PHASE_4_SPEC.md` defines the server-deployment
scope, and `PHASE_5_SPEC.md` defines the server-configuration scope.
