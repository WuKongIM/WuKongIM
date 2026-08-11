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
exhaustive bilingual TOML-to-environment reference. Phase 6 publishes the
server-operations path: Manager safety, health and monitoring, explicit node
onboarding and fail-closed scale-in, verified backup and restore, and
compatibility-gated upgrades and migrations. Phase 7 publishes symptom-led
troubleshooting plus the official wkcli, wkdb, wkbench, and bounded-diagnostics
guides. Phase 8 publishes the server-architecture path: Controller intent and
materialization, 256 physical hash-slot routing into logical Slot Raft Groups,
Channel quorum commit, bounded transport, the end-to-end send flow, and
target-fenced online routing. Phase 9 completes the guide foundation with
workload-qualified capabilities and use cases, precise cluster/message/Channel/
user/conversation concepts, and the current node-local plugin boundary. Phase
10 publishes the first scenario tutorials: direct chat plus group chat through
bounded 100,000-member membership and fanout workflows. Phase 11 completes the
scenario set with application-owned mobile push, recoverable AI stream
projections, bounded device telemetry, and durable or online-only IoT commands.

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
scope, `PHASE_5_SPEC.md` defines the server-configuration scope, and
`PHASE_6_SPEC.md` defines the server-operations scope. `PHASE_7_SPEC.md`
defines troubleshooting and official-tool boundaries. `PHASE_8_SPEC.md`
defines the current server-architecture boundaries. `PHASE_9_SPEC.md` defines
the guide-foundation and plugin boundaries. `PHASE_10_SPEC.md` defines the
direct-chat and group-tutorial boundaries. `PHASE_11_SPEC.md` defines the
message-push and AI/IoT tutorial boundaries.
