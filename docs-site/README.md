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
Phase 12 publishes the first reproducible application-developer path: a pinned
JavaScript/Web SDK snapshot, a framework-neutral TypeScript laboratory behind a
loopback-only BFF, three slice-level Product HTTP contracts, generated
compatibility and Reason Code facts, and a real Alice/Bob reconnect-and-sync
smoke scenario. It deliberately remains a v3 Beta golden-path subset rather
than a complete SDK or API reference.

## Develop

The site requires Bun `1.3.11`. The standalone golden sample uses Node.js
`>=20.11` and npm so application developers can run it without adopting the
site toolchain.

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
from sitemap and LLM outputs. Phase 12 also checks the golden sample build,
shared compatibility and slice-level OpenAPI facts, Reason Code alignment, and
unique executable source anchors plus MDX publication checkpoints. Relevant
sample or runtime-contract changes select the real Chromium integration check.

## Golden-path attestation

A normal build is fail-closed: without a receipt, `/compatibility.json` reports
`verified: false` and `verification.status: "missing"`. A publishing build may
set `WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH` to a non-empty JSON file no larger
than 16 KiB. Relative paths resolve from this `docs-site` directory; absolute
paths are accepted. There is no boolean override.

The receipt has an exact-key schema. The real E2E gate writes it only after the
complete scenario passes:

```json
{
  "schema": "wukongim.docs.golden-path-verification/v1",
  "result": "passed",
  "source_revision": "0123456789abcdef0123456789abcdef01234567",
  "sample": {
    "scenario": "javascript-web-quickstart/alice-bob-reconnect-sync/v1",
    "package_lock_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  },
  "sdk": {
    "package": "wukongimjssdk",
    "version": "1.3.5"
  },
  "runtime": {
    "node": "22.12.0",
    "browser": {
      "engine": "chromium",
      "playwright_package": "@playwright/test",
      "playwright_version": "1.62.1",
      "revision": "1234",
      "browser_version": "151.0.7922.34"
    }
  }
}
```

Only an exact match against the build's source revision, computed sample-lock
SHA-256, scenario, SDK, Node.js, Playwright, and Chromium identifiers produces
`verified: true`. Missing, unreadable, malformed, oversized, extra-field, or
drifted receipts remain unverified. The raw `WK_DOCS_GOLDEN_PATH_RECEIPT_JSON`
variable is internal to the build wrapper; setting it alongside the path is an
error and setting it alone is not a supported publishing input.

```bash
WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=/tmp/wukongim-docs-receipt.json bun run build
WK_DOCS_REQUIRE_VERIFIED=1 bun run test:output
```

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
message-push and AI/IoT tutorial boundaries. `PHASE_12_SPEC.md` defines the
JavaScript/Web golden path, Product HTTP subset, generated contract facts, and
integration-test boundaries.
