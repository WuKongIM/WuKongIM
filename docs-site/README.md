# WuKongIM v3 documentation site

This directory contains the bilingual Fumadocs site published under `/zh` and
`/en`. It covers application integration, server deployment and operations,
WuKongIMSDK and WuKongEasySDK, and the public API and protocol references.

## Develop

The site uses Bun `1.3.11`.

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

## Content workflow

- `lib/navigation.ts` is the bilingual publication registry. Add both `.mdx`
  and `.en.mdx` variants for every published page.
- `SDK_DOCUMENTATION_SPEC.md` defines the maintained full-SDK versions,
  learning order, and writing contract.
- `redirects.json` records public route migrations. Removed pages must not be
  retained as duplicate MDX content.
- `NAVIGATION.md` is generated. Refresh it with `bun run navigation:write`.
- Product HTTP, Operations HTTP, and Webhook reference pages are generated from
  the contracts under `contracts/`. After changing one, run
  `bun run openapi:write` and review the generated MDX.

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
