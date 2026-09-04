---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3 docs
under `/zh` and `/en`. It owns navigation, MDX, search, SEO, machine-readable
output, SDK and API references, and the runnable JavaScript/Web example. It
documents runtime contracts but does not define them.

## Boundaries

- Repository `docs/` is the engineering knowledge base. Legacy docs aid topic
  discovery only; current code and released SDKs decide API facts.
- `lib/navigation.ts` is the shared bilingual publication registry.
- `SDK_DOCUMENTATION_SPEC.md` owns maintained WuKongIMSDK versions, learning
  order, and reader contract. WuKongEasySDK remains a separate path.
- `.github/workflows/docs-pages.yml` deploys the export, verifies the direct Pages
  data plane, and may refresh a CDN when `DOCS_CDN_ENABLED=true`. It also accepts
  a successful binary-release `workflow_run` after authenticating the immutable
  tag and source. Its migration input stages the export before one domain change;
  its only Alibaba mutation is four bounded cache invalidations. Its fixed-origin
  RSC inventory is inert and cannot reconfigure DNS, CDN, RAM, or certificates.
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
7. `lib/release-version.ts` treats the latest exact root Changelog version
   heading as the current public release, validates an expected release tag,
   derives its container tag, and replaces only reviewed MDX placeholders.
8. Static export writes `out/`; all checks run before that exact Pages artifact
   uploads. The build derives an inert, fixed-origin, ordered, unique, bounded RSC
   URL inventory from physical page pairs and retains hidden files such as `.nojekyll`.
9. Direct Pages API and content GETs gate bounded CDN refreshes. Migration runs
   skip refresh, and refresh failure does not undo a successful deployment.

## Invariants and Failure Semantics

- Chinese and English share one menu structure. A route is published only when
  both locale variants are ready.
- Product facts preserve cluster-only and 256-hash-slot semantics, durable
  commit versus downstream effects, and current security boundaries.
- Full SDK examples pin exact released versions in Java, Objective-C,
  TypeScript, Dart, and ArkTS, explaining core terms before relying on them.
- A trusted backend supplies identity, tokens, routing, history, Channel metadata,
  and media URLs. Untrusted clients never call Product HTTP management directly.
- The JavaScript example is a tested development aid, not a production backend
  or a substitute for testing on actual devices, networks, and releases.
- EasySDK evidence names exact client and server revisions. When verified source
  is ahead of a package release, pages must not attribute that run to the older
  npm, Maven, CocoaPods, or Release artifact.
- The complete Product HTTP contract must match current route registrations.
  Missing authentication, weak validation, legacy behavior, and unbounded
  responses stay explicit rather than being normalized away.
- The static API reference keeps its playground disabled. Generated examples
  come only from reviewed samples that state the trusted-backend boundary.
- Publication uses one non-canceling `github-pages` group. Deploy receives only
  `pages: write` and `id-token: write`; build and origin verification stay
  read-only. CDN refresh and certificate rotation use separate Environments and
  OIDC roles. Domain, build, or certificate state never replaces a fresh
  deployment and the direct content gate. The RSC inventory exactly matches eligible static routes, is capped at 500 fixed-origin URLs, and is not refresh or prefetch authority.
- Current release examples use `WK_CURRENT_RELEASE_TAG` or
  `WK_CURRENT_IMAGE_TAG`; they never repeat a manually maintained version.
  Release-triggered builds require the Changelog version to equal the exact
  immutable Release tag. Historical introduction and migration versions remain
  literal evidence.

## Read First

- [SDK specification](SDK_DOCUMENTATION_SPEC.md), [navigation](lib/navigation.ts), and [developer contracts](lib/developer-contracts.ts)
- [Phase 18 API specification](PHASE_18_SPEC.md) and [OpenAPI generator](scripts/generate-openapi.ts)

## Update Triggers

Update this file when publication ownership, SDK learning order, locale parity, generated outputs, authoritative sources, or the hosting boundary changes.
