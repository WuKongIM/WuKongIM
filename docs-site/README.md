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
workload-qualified capabilities and use cases, approachable message/Channel/
user/device/conversation concepts, a separate server-architecture path for
cluster internals, and the current node-local plugin boundary. Phase
10 publishes the first scenario tutorials: direct chat plus group chat through
bounded 100,000-member membership and fanout workflows. Phase 11 completes the
scenario set with application-owned mobile push, recoverable AI stream
projections, bounded device telemetry, and durable or online-only IoT commands.
Phase 12 publishes the first reproducible application-developer path: a pinned
JavaScript/Web SDK snapshot, a framework-neutral TypeScript laboratory behind a
loopback-only BFF, one three-operation Product HTTP contract, generated
compatibility and Reason Code facts, and a real Alice/Bob reconnect-and-sync
smoke scenario. It deliberately remains a v3 Beta golden-path subset rather
than a complete SDK or API reference.
Phase 13 publishes a reader-oriented SDK chooser with current official source
discovery, followed by the platform-neutral integration foundation: eight
common SDK behavior guides for identity, connection, messaging, payloads,
conversations, offline recovery, push, multi-device state, and bounded
reconnect handling, plus source-checked Channel Type, Device Flag / Level, and
Message Flag dictionaries. Repository availability, tutorial publication, and
executable verification stay distinct. The JavaScript/Web golden path remains
the only client-artifact/browser execution target, so publishing other SDK
platform chapters does not turn them into platform-runtime support claims.
Phase 14 publishes an integrator acceptance loop: an evidence-backed
JavaScript/Web capability matrix, a bilingual production gate guide, and a
one-command local compatibility smoke report. The report is bounded and
redacted. It records the acceptance-harness revision and observed installed SDK
identity, leaves the tested cluster source and production readiness
`not_assessed`, and cannot replace the protected clean-HEAD publication
receipt. Documentation quality passes only when the bilingual pages participate
in the browser run. Phase 15 introduced bilingual, source-aligned EasySDK
tutorials for iOS, Android, Flutter, and Web. They are published as reviewable
documentation. A later server change added JSON-RPC CONNECT and online
bidirectional-message fixtures for all four wire profiles plus a real-process
iOS/Android-profile E2E; platform builds and device runs remain separate
evidence. Later security fixes made diagnostics default-off and limited enabled
output to sanitized operational metadata while redacting public model strings.
They are now included in the pinned official iOS `1.1.0`, Android `1.0.4`,
Flutter `1.1.0`, and Web `2.0.3` distributions; package publication still does
not add a platform-build, device, browser, or production-readiness receipt.
Phase 16 publishes a separate, non-exhaustive management OpenAPI contract:
ten reviewed Channel mutations and six canonical Conversation operations. It records the missing
built-in authentication boundary, exact compatibility error shapes, bounded
Conversation traversal, and explicitly defers weakly validated, unbounded, and
legacy routes. The Phase 12 three-operation whitelist and receipt remain
frozen; Phase 16 only corrects their shared restore-maintenance response Schema
to the current runtime body.
Phase 17 publishes a third bounded OpenAPI contract for ordinary persistent
`POST /message/send`, plus concise client-protocol pages for the authenticated
connection lifecycle and the complete Frame Type catalog. At the end of Phase
17, byte-level framing, JSON-RPC, and encryption details were deferred pending
separate contract reconciliation and security review; Phase 18 below publishes
those references.
Phase 18 published the then-current API and protocol reference: all 41 Product
HTTP registrations, separate Operations HTTP and outbound Webhook OpenAPI 3.1
contracts, exact WKProto wire and compatibility-encryption pages, an
experimental JSON-RPC Schema then marked unsupported, and exhaustive private
Manager, transport, MCP, plugin, worker, and agent interface inventories. The
later server change described above promoted only the pinned EasySDK core path;
the wider JSON-RPC Schema remains experimental. Older OpenAPI files remain
narrow adoption profiles and keep their original verification boundaries.
Phases 19 through 22 publish pinned, source/artifact-aligned first-message paths
for iOS `1.1.1`, Android `1.5.5`, Flutter `1.7.9`, and HarmonyOS `1.1.7` without
claiming site device receipts. Phase 23 retires the deprecated UniApp package
and gives existing projects a bounded migration to `wukongimjssdk@1.3.5`.
Phase 24 completes the maintained backlog: Kubernetes Beta plus platform
capability, API-reference, and upgrade chapters for the pinned SDKs. All
maintained routes are now published, while runtime and production evidence
remain explicitly narrower than documentation coverage.

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

The Product HTTP reference is generated from the complete 41-operation OpenAPI
3.1 contract. It follows the Fumadocs example structure: six tag indexes and 41
independent operation pages per locale, for 94 tracked MDX files. After changing
the contract, regenerate and review the tracked MDX:

```bash
bun run openapi:write
bun run openapi:check
```

The static reference intentionally disables the request playground because the
documented endpoints require trusted network boundaries. Operations HTTP and
outbound Webhooks use separate OpenAPI documents; Webhooks use the top-level
`webhooks` object. TCP/WKProto and JSON-RPC remain protocol documentation rather
than fake OpenAPI paths.

## Validate

```bash
bun run verify
```

The verification suite checks the navigation contract, redirect seed, generated
menu plan, lint and TypeScript, static export, language-isolated search indexes,
the inclusion of every published route, the exclusion of any future planned
routes from sitemap and LLM outputs, and the reverse invariant that no existing
MDX page is hidden behind planned or unknown navigation. Phase 12 also checks the golden sample build,
shared compatibility and slice-level OpenAPI facts, Reason Code alignment, and
unique executable source anchors plus MDX publication checkpoints. Phase 13
also checks every common-guide publication boundary and compares protocol
dictionary names, values, and bit positions with their current Go authorities.
Phase 14 also checks capability-status drift, exact local-report shape,
fail-closed write ordering, observed SDK identity, and the separation between
the harness, tested cluster, documentation quality, compatibility smoke, and
production readiness. Phase 15 also checks exact EasySDK package/source pins,
source tutorials, listener cleanup, platform-specific adoption boundaries,
logging-fix provenance and released-package inclusion, and non-sensitive
application logging examples; navigation publishes the content while every page
distinguishes server wire, source, and package evidence from platform-runtime
and production-readiness evidence. Relevant sample or runtime-contract changes
select the real Chromium integration check.
Phase 16 also checks the exact management-operation whitelist, explicit route
deferrals, bilingual operation-per-page generation grouped by tag, nested Conversation schemas, search
and LLM output, and continued separation from the golden receipt.
Phase 17 also checks the exact message-send contract, trusted-backend boundary,
Frame Type values, lifecycle claims, and the publication or deferral state of
each client-protocol page.
Phase 18 also checks the exact 41-operation Product surface, Operations,
Debug/Bench, Manager, node transport, MCP/agent/plugin inventories, WKProto
layout, WebSocket carrier, JSON-RPC bridge, encryption, Webhook delivery, and
all downloadable specifications against current source.
Phases 19 through 23 add the pinned iOS, Android, Flutter, HarmonyOS, and UniApp
migration contracts. Phase 24 adds exact platform reference/upgrade boundaries,
the Kubernetes stateful-deployment contract, zero-planned-route enforcement,
and bilingual published-route/MDX parity.

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
- Edit `contracts/javascript-web-quickstart.openapi.json`,
  `contracts/product-http-management.openapi.json`, or
  `contracts/product-http-messaging.openapi.json`, then run
  `bun run openapi:write`; do not edit
  generated files under `content/docs/api/product-http/` directly.
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
integration-test boundaries. `PHASE_13_SPEC.md` defines SDK selection and
source-discovery boundaries, cross-SDK behavior guides, source-checked protocol
dictionaries, and the platform-support boundary.
`PHASE_14_SPEC.md` defines the JavaScript capability evidence, local acceptance
report, production gate, and publication-attestation boundaries.
`PHASE_15_SPEC.md` defines the source-aligned EasySDK overview and iOS, Android,
Flutter, and Web tutorial boundaries.
`PHASE_16_SPEC.md` defines the trusted Product HTTP Channel/Conversation
management subset, its exact deferrals, and its separation from golden-path
attestation.
`PHASE_17_SPEC.md` defines the Product HTTP message-send subset and the first
published client-protocol baseline.
`PHASE_18_SPEC.md` defines the complete source-aligned API and protocol surface,
publication classes, machine-readable artifacts, and drift checks.
`PHASE_19_SPEC.md` through `PHASE_22_SPEC.md` define the pinned iOS, Android,
Flutter, and HarmonyOS tutorial baselines. `PHASE_23_SPEC.md` defines UniApp
retirement and JSSDK migration. `PHASE_24_SPEC.md` defines completion of the
Kubernetes and SDK platform-capability, API-reference, and upgrade backlog.
