---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3
documentation under `/zh` and `/en`. It owns shared information architecture,
MDX publication, static search, sitemap/SEO, and LLM/Markdown outputs.
It also owns the SDK chooser and official-source directory, source-aligned
WuKongEasySDK tutorials for iOS, Android, Flutter, and Web, the narrow
JavaScript Web golden-path laboratory, its generated compatibility/OpenAPI
artifacts and Fumadocs operation pages, platform-neutral SDK behavior guides,
the source-calibrated Product HTTP management and message-send subsets,
source-checked protocol dictionaries, and the published WKProto lifecycle and
Frame Type baseline. It does not define runtime behavior or replace code contracts. The JavaScript laboratory
also owns a bounded local integration-acceptance report. The report identifies
its harness and observed installed SDK, proves only its compatibility smoke,
and leaves the tested cluster source and production readiness unassessed.

## Boundaries

- Repository `docs/` and the legacy v2 site are separate sources; older wiki
  material is not authoritative unless recalibrated against promoted code.
- Guide Core Concepts is the reader-first application vocabulary for Message,
  Channel, User, Device, and Conversation. Cluster, node, Slot, replica, and
  leadership mechanics belong in Server Architecture rather than that path.
- `lib/navigation.ts` is the shared bilingual publication registry. Phase specs
  own detailed content plans and claims.
- Static export produces artifacts only; deployment, DNS, redirects, and
  production cutover are external operations.

## Main Flows

1. Navigation metadata generates locale-equal menus, tabs, static parameters,
   and the planning reference.
2. Published bilingual MDX is filtered through that registry and feeds pages,
   search, sitemap, LLM output, and per-page Markdown.
3. The published SDK chooser separates official repository availability,
   tutorial publication, and exact-version executable verification. Repository
   links do not expand the JavaScript compatibility snapshot or publish planned
   platform APIs.
4. Published EasySDK tutorials preserve the legacy short integration sequence
   while pinning current released packages and exact source revisions. They
   route every platform through trusted-backend identity, listener lifecycle,
   and an Alice/Bob acceptance proof without issuing a runtime receipt.
5. `lib/developer-contracts.ts` joins source-checked Reason Codes, Channel
   Types, Device Flags / Levels, Message Flags, build identity, SDK/runtime
   pins, and the three-endpoint Product HTTP Beta subset into both human pages
   and machine-readable outputs.
6. `scripts/generate-openapi.ts` turns three bounded OpenAPI 3.1 contracts into
   52 tracked Fumadocs pages: 40 bilingual one-operation variants and 12 concise
   tag indexes. Navigation mirrors the tag hierarchy and HTTP methods; static
   rendering preloads all three contracts, disables the playground, and uses
   only contract-owned trusted-backend examples.
7. `examples/javascript-web-quickstart/` runs a loopback-only Node.js BFF and one
   isolated SDK singleton per browser context; its opt-in E2E scenario supplies
   the real single-node cluster/Chromium verification evidence and can write a
   redacted local acceptance report only after fast and real gates pass.
8. Next.js static export writes `out/`, whose publication, canonical, link,
   accessibility-structure, and machine-artifact boundaries are checked before
   any external hosting step.

## Invariants and Failure Semantics

- Chinese and English share the same menu structure; a route is published only
  when both locale variants are ready.
- Planned routes remain visible but are `noindex` and excluded from search,
  sitemap, and machine-readable content. Unknown content fails closed.
- Product facts must preserve cluster-only/256-hash-slot semantics, authority
  versus observation, durable commit versus side effects, and current security
  boundaries.
- Common SDK guides publish server- and wire-proven behavior only. They do not
  claim platform method names or expand compatibility beyond the executable
  JavaScript/Web snapshot.
- The SDK chooser may link current official repositories, but must label source
  availability, site tutorial status, and executable verification separately.
  Legacy timing, universal-platform, or family-wide capability claims are not
  republished without exact-version evidence.
- EasySDK platform tutorials may name public methods only when they record the
  exact released package, tag, and source revision used for review. Source
  alignment is not a compatibility receipt and cannot expand the protected
  JavaScript/Web golden-path evidence.
- EasySDK adoption guidance must retain the iOS availability discrepancy,
  iOS/Android device and Payload drift, Android JSON-field drift, Flutter
  receive decoding and listener lifecycle, and all four platforms' sensitive
  request/response or parse-error logging risks until upstream source or
  executable evidence closes each item.
- The EasySDK Web client receives connection material through a trusted BFF;
  Product HTTP management calls remain outside every untrusted client.
- Plain non-command `NoPersist` is terminal compatibility success without
  realtime delivery. Only command-style `NoPersist` enters transient online
  delivery, and neither branch has durable recovery.
- Operational guidance must use `/readyz`, retain Manager safety gates, avoid
  invented compatibility/image promises, and keep unimplemented procedures
  visibly planned.
- Configuration reference covers every public schema field exactly once and
  distinguishes examples from runtime defaults.
- Browsers never call Product HTTP directly in the JavaScript golden path. The
  localhost BFF owns `/user/token`, `/route`, and `/channel/messagesync`; it is
  a development boundary, not production authentication.
- The management contract publishes only its reviewed 10-Channel/6-Conversation
  whitelist. Its `security: []` records missing built-in authentication rather
  than granting anonymous access. Weakly validated Channel operations, the
  unbounded allowlist read, and legacy `/conversation/sync` remain explicitly
  deferred.
- The message-send contract publishes only ordinary persistent
  `POST /message/send` with five canonical required fields. Legacy aliases,
  request-scoped subscribers, and transient flags remain outside it. Its
  `security: []` records missing built-in authentication, so it is trusted-backend-only.
- Golden-path verification attests only the fixed three-operation whitelist and
  receipt tuple. Phase 16 corrects the shared restore-maintenance response
  Schema without expanding that scope. Source alignment and entry/use-case
  tests calibrate the separate management contract; they are not a scenario
  receipt.
- Fumadocs OpenAPI applies only to Product HTTP. WKProto lifecycle, Frame Types,
  TCP framing, JSON-RPC, encryption, and dictionaries MUST NOT become fake HTTP
  paths. Lifecycle and Frame Types are published; the rest remain planned.
- The static API reference keeps its playground disabled. Generated request
  examples come only from reviewed `x-codeSamples` that state the trusted
  backend boundary.
- Compatibility output identifies the exact source revision, sample lockfile,
  SDK, Node.js, Playwright, and Chromium target. Verification defaults to false
  and becomes true only when a successful receipt matches that complete tuple.
  Chromium is the only eligible browser target; other browsers remain
  explicitly unverified.
- The local acceptance report uses its own exact schema, records no endpoint,
  token, UID, message body, DOM, or browser capture, and always marks production
  readiness `not_assessed`, tested cluster source `not_assessed`, and publication
  attestation `not_issued`. Its documentation-quality result passes only when
  both locale routes participate in the browser run. It cannot be supplied as
  the protected golden-path verification receipt.

## Read First

- [Navigation registry](lib/navigation.ts)
- [Phase 17 API and client-protocol specification](PHASE_17_SPEC.md)
- [Developer contract source](lib/developer-contracts.ts)
- [OpenAPI page generator](scripts/generate-openapi.ts)

## Update Triggers

Update this file when publication ownership, locale parity, planned/published
behavior, generated outputs, authoritative content sources, or hosting boundary
changes.
