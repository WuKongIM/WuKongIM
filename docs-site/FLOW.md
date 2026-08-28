---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3
documentation under `/zh` and `/en`. It owns navigation, MDX publication,
search, sitemap/SEO, LLM/Markdown output, SDK guidance, compatibility artifacts,
and the JavaScript Web golden-path laboratory. It also owns the complete
source-calibrated Product HTTP reference; separate Operations and Webhook
contracts; WKProto, encryption, and experimental JSON-RPC references; and the
private-interface inventory. It documents runtime contracts but does not define
them; the Web laboratory's bounded report proves only its compatibility smoke.

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
   publication planning, and machine-readable inclusion.
2. Published bilingual MDX feeds pages, search, sitemap, LLM output, and
   per-page Markdown; planned and unknown content fail closed.
3. `scripts/generate-openapi.ts` turns the complete 41-operation Product HTTP
   OpenAPI 3.1 contract into 94 tracked operation/tag pages. Operations pages
   and Webhook payloads use separate OpenAPI 3.1 contracts through the same
   Fumadocs loader; Webhooks use the top-level `webhooks` object.
4. Source-locked registries compare Product HTTP, Operations, internal HTTP,
   node transport, MCP, agent CLI, plugin RPC, WKProto, JSON-RPC, and Webhooks
   with current Go authorities.
5. SDK pages distinguish source availability, tutorial publication, and exact
   executable verification. The Web laboratory uses a loopback BFF and isolated
   SDK clients to produce bounded single-node-cluster/Chromium evidence.
6. Static export writes `out/`; publication, canonical, link, structure, and
   machine-artifact checks run before any external hosting step.

## Invariants and Failure Semantics

- Chinese and English share the same menu structure; a route is published only
  when both locale variants are ready.
- Planned routes remain visible but are `noindex` and excluded from search,
  sitemap, and machine-readable content. Unknown content fails closed.
- Product facts must preserve cluster-only/256-hash-slot semantics, authority
  versus observation, durable commit versus side effects, and current security
  boundaries.
- SDK method and compatibility claims require exact-version evidence. The Web
  client gets connection material from a trusted BFF; untrusted clients never
  call Product HTTP management operations.
- Operational guidance uses `/readyz`, preserves Manager safety gates, and
  labels unimplemented or unverified behavior explicitly.
- Configuration reference covers each public field once and distinguishes
  examples from runtime defaults.
- The complete Product HTTP contract must match all 41 current route
  registrations. Weak validation, unbounded responses, legacy aliases, and
  compatibility-only behavior remain visible in operation descriptions rather
  than being normalized away. `security: []` records missing built-in
  authentication; it never grants anonymous public access.
- The three-operation golden path, 16-operation management contract, and
  one-operation message-send contract remain downloadable narrow profiles.
  They do not constrain the complete reference or expand verification evidence.
- Fumadocs OpenAPI applies to real Product and Operations HTTP paths and to
  outbound callbacks through OpenAPI's `webhooks` object. WKProto, JSON-RPC,
  MCP, plugin RPC, and node transport MUST NOT become fake HTTP paths.
- WKProto wire layout and compatibility encryption are published as protocol
  contracts. JSON-RPC is published only as an experimental codec Schema and
  support matrix; the current Product Gateway does not support it as a client
  integration.
- Webhook delivery is unsigned, bounded, in-memory, HTTP-200-only, and has no
  crash replay. Manager with `auth_on=false` exposes most ordinary mutations;
  only explicitly gated backup, restore, and MCP administration fail closed.
- The static API reference keeps its playground disabled. Generated request
  examples come only from reviewed `x-codeSamples` that state the trusted
  backend boundary.
- Golden-path verification defaults false and attests only the complete source,
  lockfile, SDK, runtime, browser, and fixed three-operation tuple. The redacted
  local report is not a publication receipt or production-readiness claim.

## Read First

- [Navigation registry](lib/navigation.ts)
- [Phase 18 complete API and protocol specification](PHASE_18_SPEC.md)
- [Developer contract source](lib/developer-contracts.ts)
- [OpenAPI page generator](scripts/generate-openapi.ts)

## Update Triggers

Update this file when publication ownership, locale parity, generated outputs, authoritative sources, or the hosting boundary changes.
