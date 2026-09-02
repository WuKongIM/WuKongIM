---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3
documentation under `/zh` and `/en`. It owns navigation, MDX, search and SEO,
machine-readable output, SDK guidance, API and protocol references, and the
runnable JavaScript/Web SDK example. It documents runtime contracts but does
not define them.

## Boundaries

- Repository `docs/` is the engineering knowledge base. Legacy documentation
  is useful for topic discovery only; current code and released SDKs decide API
  facts.
- `lib/navigation.ts` is the shared bilingual publication registry.
- `SDK_DOCUMENTATION_SPEC.md` owns the maintained WuKongIMSDK versions,
  learning order, and reader contract. WuKongEasySDK remains a separate path.
- `.github/workflows/docs-pages.yml` deploys the exact export to GitHub Pages
  and may refresh a pre-provisioned CDN only when `DOCS_CDN_ENABLED=true`. It
  never provisions or reconfigures DNS, CDN topology, RAM, or certificates;
  its only Alibaba mutation is the four bounded cache invalidations.
- The canonical URL remains `https://docs.githubim.com`. After CDN cutover,
  GitHub Pages serves `https://origin-docs.githubim.com` as the only origin.
  Pages Settings/API is the sole domain authority; the export carries no CNAME.

## Main Flows

1. Navigation metadata generates locale-equal menus, tabs, static parameters,
   publication planning, and machine-readable inclusion.
2. Published bilingual MDX feeds pages, search, sitemap, LLM output, and
   per-page Markdown. Planned and unknown content fail closed.
3. The full SDK path starts at `/sdk`, explains shared concepts once, then uses
   the same task sequence for Android, iOS, JavaScript/Web, Flutter, and
   HarmonyOS: quickstart, connection, messages, conversations, channels,
   supported advanced topics, and API lookup. One shared upgrade page replaces
   per-platform upgrade pages.
4. The separate EasySDK path keeps released package pins distinct from exact
   repository-example receipts. Its shared runbook starts one server revision,
   maps host addresses for browser, emulators, and devices, and reproduces the
   four maintained examples before platform-specific integration.
5. Removed SDK pages exist only as redirects. UniApp migration lives under the
   JavaScript advanced section; there is no standalone UniApp documentation
   group.
6. `scripts/generate-openapi.ts` generates the complete Product HTTP reference.
   Operations HTTP and outbound Webhooks use separate OpenAPI contracts.
   WKProto, JSON-RPC, and private interfaces remain protocol documentation.
7. Static export writes `out/`; publication, canonical, link, structure, and
   machine-artifact checks run before the Workflow uploads that exact directory
   as the GitHub Pages artifact. Hidden files are retained so `.nojekyll` and
   future machine-readable well-known endpoints cannot be dropped silently.
8. After external CDN setup and enablement, the deploy Workflow refreshes only
   bounded stable URLs. It never broadly purges content-addressed assets, and
   refresh failure does not undo a successful Pages deployment.

## Invariants and Failure Semantics

- Chinese and English share one menu structure. A route is published only when
  both locale variants are ready.
- Product facts preserve cluster-only and 256-hash-slot semantics, durable
  commit versus downstream effects, and current security boundaries.
- Full SDK examples pin exact released versions and use Java, Objective-C,
  TypeScript, Dart, and ArkTS respectively. They explain Channel, Provider,
  local insertion, and the server send result before relying on those terms.
- A trusted application backend supplies identity, tokens, routing, history,
  channel metadata, and media URLs as needed. Untrusted clients never call
  Product HTTP management operations directly.
- The JavaScript example is a runnable development aid with unit and build
  checks. It is not a production backend or a substitute for testing on the
  application's actual browsers, devices, networks, and release configuration.
- EasySDK example evidence names the exact client and server revisions. When a
  verified repository revision is ahead of its package release, pages must not
  attribute that source run to the older npm, Maven, CocoaPods, or Release
  artifact.
- The complete Product HTTP contract must match current route registrations.
  Missing authentication, weak validation, legacy behavior, and unbounded
  responses stay explicit rather than being normalized away.
- The static API reference keeps its playground disabled. Generated examples
  come only from reviewed samples that state the trusted-backend boundary.
- Production publication uses one non-canceling `github-pages` concurrency
  group. The deploy job receives only `pages: write` and `id-token: write`; the
  build job remains read-only. CDN refresh and certificate rotation use the
  separate `docs-cdn` and `docs-cdn-certificate` Environments and OIDC roles.

## Read First

- [SDK documentation specification](SDK_DOCUMENTATION_SPEC.md)
- [Navigation registry](lib/navigation.ts)
- [Phase 18 API and protocol specification](PHASE_18_SPEC.md)
- [Developer contract source](lib/developer-contracts.ts)
- [OpenAPI page generator](scripts/generate-openapi.ts)

## Update Triggers

Update this file when publication ownership, SDK learning order, locale parity, generated outputs, authoritative sources, or the hosting boundary changes.
