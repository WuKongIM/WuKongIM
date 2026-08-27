---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3
documentation under `/zh` and `/en`. It owns shared information architecture,
MDX publication, static search, sitemap/SEO, and LLM/Markdown outputs.
It also owns the narrow JavaScript Web golden-path laboratory, its generated
compatibility/OpenAPI artifacts, platform-neutral SDK behavior guides, and
source-checked protocol dictionaries. It does not define product runtime
behavior or replace authoritative code contracts. The JavaScript laboratory
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
3. `lib/developer-contracts.ts` joins source-checked Reason Codes, Channel
   Types, Device Flags / Levels, Message Flags, build identity, SDK/runtime
   pins, and the three-endpoint Product HTTP Beta subset into both human pages
   and machine-readable outputs.
4. `examples/javascript-web-quickstart/` runs a loopback-only Node.js BFF and one
   isolated SDK singleton per browser context; its opt-in E2E scenario supplies
   the real single-node cluster/Chromium verification evidence and can write a
   redacted local acceptance report only after fast and real gates pass.
5. Next.js static export writes `out/`, whose publication, canonical, link,
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
- [Phase 14 acceptance specification](PHASE_14_SPEC.md)
- [Developer contract source](lib/developer-contracts.ts)
- [JavaScript Web golden sample](examples/javascript-web-quickstart/README.md)
- [Documentation landing page](content/docs/guide/index.mdx)

## Update Triggers

Update this file when publication ownership, locale parity, planned/published
behavior, generated outputs, authoritative content sources, or hosting boundary
changes.
