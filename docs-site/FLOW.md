# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for the public WuKongIM v3
documentation. It owns the `/zh` and `/en` sites, their shared information
architecture, static search, SEO outputs, and machine-readable documentation
outputs. It does not own the legacy v2 site or product runtime documentation
under the repository-level `docs/` directory.

## Source and publishing flow

```text
lib/navigation.ts
  -> Fumadocs sidebars and top-level tabs
  -> static params for visible published/planned routes
  -> NAVIGATION.md planning reference

content/docs/**/*.mdx
  -> fumadocs-mdx source
  -> published domain and onboarding pages
  -> Orama search + sitemap + llms.txt + per-page Markdown
```

- Chinese and English MUST have the same menu structure.
- A page is `published` only when both locale variants exist and are ready.
- A `planned` route remains visible in navigation and renders a scope summary,
  but MUST be excluded from search, sitemap, and LLM outputs and MUST emit
  `noindex`.
- `lib/source.ts` filters generated MDX files through the navigation registry;
  unknown or still-planned content paths fail closed before any index consumer.
- Change `lib/navigation.ts`, then run `bun run navigation:write`; CI-style
  validation uses `bun run navigation:check`.
- `redirects.json` is only a phase-one seed. The complete legacy URL audit and
  host-specific redirect adapter belong to the migration/deployment phase.
- Phase 2 publishes the product overview, core-concepts overview, complete
  source-based quick start, and basic configuration overview. Their commands
  and defaults MUST stay aligned with the root README, `wukongim.toml.example`,
  and the embedded Chat Demo.

## Static delivery

`next.config.mjs` uses Next.js static export. `bun run build` writes `out/`;
`bun run test:output` validates publishing boundaries against that artifact.
No deployment, DNS change, or production cutover is performed by phases 1 or 2.
