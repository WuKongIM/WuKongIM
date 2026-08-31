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
