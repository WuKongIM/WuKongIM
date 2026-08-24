---
scope: subtree
summary: Owns the bilingual static v3 documentation site, shared navigation, publication state, search, SEO, and machine-readable outputs.
---

# Documentation Site Flow

## Responsibility

`docs-site` is the standalone Fumadocs application for public WuKongIM v3
documentation under `/zh` and `/en`. It owns shared information architecture,
MDX publication, static search, sitemap/SEO, and LLM/Markdown outputs.
It does not define product runtime behavior or replace authoritative code contracts.

## Boundaries

- Repository `docs/` and the legacy v2 site are separate sources; older wiki
  material is not authoritative unless recalibrated against promoted code.
- `lib/navigation.ts` is the shared bilingual publication registry. Phase specs
  own detailed content plans and claims.
- Static export produces artifacts only; deployment, DNS, redirects, and
  production cutover are external operations.

## Main Flows

1. Navigation metadata generates locale-equal menus, tabs, static parameters,
   and the planning reference.
2. Published bilingual MDX is filtered through that registry and feeds pages,
   search, sitemap, LLM output, and per-page Markdown.
3. Next.js static export writes `out/`, whose publishing boundaries are checked
   before any external hosting step.

## Invariants and Failure Semantics

- Chinese and English share the same menu structure; a route is published only
  when both locale variants are ready.
- Planned routes remain visible but are `noindex` and excluded from search,
  sitemap, and machine-readable content. Unknown content fails closed.
- Product facts must preserve cluster-only/256-hash-slot semantics, authority
  versus observation, durable commit versus side effects, and current security
  boundaries.
- Operational guidance must use `/readyz`, retain Manager safety gates, avoid
  invented compatibility/image promises, and keep unimplemented procedures
  visibly planned.
- Configuration reference covers every public schema field exactly once and
  distinguishes examples from runtime defaults.

## Read First

- [Navigation registry](lib/navigation.ts)
- [Navigation plan](NAVIGATION.md)
- [Site configuration](next.config.mjs)
- [Phase 9 specification](PHASE_9_SPEC.md)
- [Documentation landing page](content/docs/guide/index.mdx)

## Update Triggers

Update this file when publication ownership, locale parity, planned/published
behavior, generated outputs, authoritative content sources, or hosting boundary
changes.
