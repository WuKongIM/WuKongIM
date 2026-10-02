# WuKongIM v3 documentation site

This directory contains the bilingual Fumadocs site published under `/zh` and
`/en`. It covers application integration, server deployment and operations,
WuKongIMSDK and WuKongEasySDK, and the public API and protocol references.

## Develop

The site uses Bun `1.3.11`. The complete verification gate also needs Go
1.22+ for the dependency-free business Webhook example.

```bash
bun install
bun run dev
```

Open `http://localhost:3000/zh` or `http://localhost:3000/en`.

The JavaScript/Web SDK example is an independent Node.js `>=20.11` project:

```bash
cd examples/javascript-web-quickstart
npm ci
npm run dev
```

Its browser code talks to WuKongIM Gateway only. The loopback development BFF
owns Product HTTP calls and must be replaced by an authenticated application
backend in production.

The Go before-send Webhook example is a separate standard-library-only module:

```bash
cd examples/go-webhook
go run .
```

See its `README.md` for direct callback requests, server configuration, business
rules, and Product HTTP send/history checks. `bun run sample:go-webhook:check`
runs its fast unit tests from the documentation-site root and is part of `verify`.

## Content workflow

- `lib/navigation.ts` is the bilingual publication registry. Add both `.mdx`
  and `.en.mdx` variants for every published page.
- `SDK_DOCUMENTATION_SPEC.md` defines the maintained full-SDK versions,
  learning order, and writing contract.
- `EASY_SDK_DOCUMENTATION_SPEC.md` defines the eight EasySDK quickstarts,
  example-first learning path, and separate engineering validation history.
- [Tutorial screenshots](TUTORIAL_SCREENSHOTS.md) records capture versions and
  the process for refreshing real screenshots and bilingual numbered captions.
- `lib/easy-sdk-releases.json` is the sole current EasySDK version manifest.
  After validating a compatible released package against its tutorial, update
  that platform's `version`. Source checkouts default to `v<version>`; optional
  `sourceRevision` pins, and C++ `registryBaseline`, `vcpkgBaseline`, and
  `cmakeVersion`, must be reviewed independently. A version edit is not evidence
  of a successful SDK package or live messaging test.
  MDX uses `WK_EASYSDK_<PLATFORM>_VERSION`, `_TAG`, and `_SOURCE_REF` (Web uses
  `JAVASCRIPT`). C++ also has `_REGISTRY_BASELINE`, `_VCPKG_BASELINE`, and
  `_CMAKE_VERSION`. The remark plugin resolves prose, code, Markdown links and
  literal MDX attributes before HTML, search and Markdown generation. Keep
  frontmatter version-neutral and use literal attributes rather than JavaScript
  expressions for these tokens. Navigation reads the same manifest.
  Run `bun run navigation:write` and `bun run verify` after updating it; generated
  navigation is not edited by hand. Unknown tokens and unresolved output fail
  verification. API, lifecycle, platform or installation changes still require
  corresponding tutorial changes. Historical reports, fix-introduction versions
  and server commits remain literal evidence. The Web SDK pilot discovers npm
  releases, verifies the tutorial and proposes a PR through
  [`easy-sdk-web-docs-sync.yml`](../.github/workflows/easy-sdk-web-docs-sync.yml);
  see the [maintenance runbook](scripts/easy-sdk-sync/README.md). Other platforms
  still require manual package validation and selection.
- `redirects.json` records public route migrations. Removed pages must not be
  retained as duplicate MDX content.
- `NAVIGATION.md` is generated. Refresh it with `bun run navigation:write`.
- Product HTTP, Operations HTTP, and Webhook reference pages are generated from
  the contracts under `contracts/`. After changing one, run
  `bun run openapi:write` and review the generated MDX.

Downloadable operator examples live under `public/examples/monitoring/` and
`public/examples/wkbench/`. Keep both language guides aligned with these files.
Validate monitoring files with `promtool check config` and `promtool check rules`.
Validate benchmark inputs with the current `wkcli bench validate` command before
publishing; validation does not start traffic. Report illustrations must remain
explicitly separate from measured capacity results.

The static API reference deliberately disables its request playground because
the documented administrative endpoints require trusted network boundaries.
WKProto, JSON-RPC, and other non-HTTP protocols remain regular protocol pages,
not synthetic OpenAPI routes.

## Validate

Run the complete documentation gate before committing:

```bash
bun run verify
```

It checks focused content contracts, bilingual navigation and links, generated
files, lint, TypeScript, the static build, search indexes, sitemap, and
machine-readable outputs. The JavaScript example also has its own checks:

```bash
cd examples/javascript-web-quickstart
npm test
npm run build
```

See `FLOW.md` for repository navigation and ownership boundaries.

## Publish

Merges to `main` that change `docs-site/**` automatically run
`Safety Automation - Publish Documentation to GitHub Pages`. The Workflow pins
Bun `1.3.11`, runs the complete `bun run verify` gate, uploads only `out/`, and
deploys that verified artifact to the `github-pages` Environment. It can also
be started manually after an interrupted publication.

The production public URL is `https://docs.githubim.com`. The static export
carries `public/.nojekyll` but deliberately carries no `CNAME`. With the
Actions Pages source, repository Pages Settings/API is the sole authority for
the custom domain.

The planned production hosting path is:

```text
https://docs.githubim.com
  -> Alibaba Cloud CDN
  -> https://origin-docs.githubim.com
  -> GitHub Pages
```

`docs.githubim.com` remains the public URL used by canonical metadata, the
sitemap, and reader-facing links. GitHub Pages remains the only content origin
and owns the TLS certificate for `origin-docs.githubim.com`; Alibaba Cloud CDN
uses a separately renewed Let's Encrypt certificate for the public domain.

The CDN refresh and certificate Workflows are disabled by default through
`DOCS_CDN_ENABLED`. Publishing to GitHub Pages continues normally until an
administrator provisions the external DNS, CDN, RAM/OIDC, and ACME resources,
tests the origin, and explicitly enables the integration. See the
[Alibaba Cloud CDN runbook](../docs/superpowers/runbooks/docs-alibaba-cdn.md)
for configuration, cutover, validation, and rollback.
