---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` owns bilingual navigation, references, examples, search, SEO and machine-readable output. Runtime contracts are defined elsewhere.

## Boundaries

- Repository `docs/` is the engineering knowledge base. Legacy docs aid topic
  discovery only; current code and released SDKs decide API facts.
- `lib/navigation.ts` is the shared bilingual publication registry.
- `SDK_DOCUMENTATION_SPEC.md` owns full-SDK versions and learning order; EasySDK stays separate.
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
4. EasySDK has one bilingual overview, one official-example guide, and eight
   platform quickstarts. Each follows prepare, install, connect/listen, exchange,
   cleanup, and troubleshooting. Alternative installs follow the first-message
   path. `EASY_SDK_DOCUMENTATION_SPEC.md` defines the reader contract.
   Historical package/source/server/harness identities, registry checksums,
   fault/soak receipts and failed cluster observations live in the engineering
   validation history linked from each tutorial; they never imply that a newer
   tutorial version passed an older matrix. Public pages retain actionable
   compatibility, lifecycle, and server limitations.
   `lib/easy-sdk-releases.json` owns current versions and independent source/vcpkg pins.
   Navigation reads it directly; `lib/easy-sdk-version.ts` resolves MDX tokens in prose,
   code, links and literal attributes before HTML, search and Markdown generation.
   Historical evidence stays literal. The hourly `easy-sdk-web-docs-sync.yml` Web pilot
   verifies released npm tutorials in Chromium and proposes a bounded PR; other SDKs remain manual.
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

- Chinese and English share one menu; publication requires both locale variants.
- Product facts preserve cluster-only and 256-hash-slot semantics, durable commit versus downstream effects, and current security boundaries.
- Full SDK examples pin exact released versions in Java, Objective-C, TypeScript, Dart, and ArkTS, explaining core terms first.
- A trusted backend supplies identity, tokens, routing, history, Channel metadata and media URLs. Untrusted clients never call Product HTTP management directly.
- The JavaScript browser gate uses BFF-issued credentials with Token auth enabled;
  its pinned Playwright runner verifies online exchange and offline recovery.
- The JavaScript example is a development aid; actual devices, networks, and releases need testing.
- EasySDK evidence names exact client and server revisions. When verified source
  is ahead of a package release, pages must not attribute that run to the older
  npm, Maven, CocoaPods, or Release artifact.
- The complete Product HTTP contract must match current route registrations.
  Message editing publishes separate update and single-channel edit-feed operations,
  latest content on both conversation reads, decimal edit versions, and restore
  epoch/cache reset semantics. Node transport inventory includes their private RPCs.
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
- [SDK specification](SDK_DOCUMENTATION_SPEC.md), [navigation](lib/navigation.ts), [developer contracts](lib/developer-contracts.ts), [Phase 18 API specification](PHASE_18_SPEC.md), and [OpenAPI generator](scripts/generate-openapi.ts)

## Update Triggers
Update this file when publication ownership, SDK learning order, locale parity, generated outputs, authoritative sources, or the hosting boundary changes.
