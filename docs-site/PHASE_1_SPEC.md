# WuKongIM v3 Documentation — Phase 1 Specification

## Goal

Create a standalone, statically exported Fumadocs application that establishes
the complete public v3 documentation skeleton and menu plan without migrating
the body content of the existing documentation.

## Required experience

- Canonical routes are `/{lang}/guide`, `/{lang}/server`, `/{lang}/sdk`, and
  `/{lang}/api`, where `lang` is `zh` or `en`.
- `/zh` and `/en` are card-driven documentation landing pages.
- Both locales share one user-journey information architecture.
- Every menu entry has a short Chinese and English scope description.
- Phase-one domain landing pages are published; all descendant menu entries
  are visible as planned pages.
- Planned pages are excluded from search, SEO indexing, sitemap, and LLM
  outputs.
- Search uses Orama with a Mandarin tokenizer for Chinese and an English index
  for English.
- Machine-readable outputs include `llms.txt`, `llms-full.txt`, per-page
  Markdown, `sitemap.xml`, and `robots.txt`.
- The site uses light WuKongIM branding, Bun, Next.js static export, and the
  repository's existing logo.
- A seed manifest records permanent legacy redirects for a later hosting
  adapter.

## Menu domains

- Guides: product overview, quick start, core concepts, integration, tutorials.
- Server: deployment, configuration, operations, tools, architecture.
- SDK: common guidance plus Android, iOS, JavaScript, Flutter, UniApp, and
  HarmonyOS platform sections.
- API & Protocols: conventions, authentication, compatibility, product and
  operations HTTP APIs, webhooks, client protocols, dictionaries, and
  specifications.

`NAVIGATION.md` contains the full bilingual leaf-level plan generated from the
runtime navigation registry.

## Excluded from phase 1

- Migrating the existing Mintlify page bodies or all SDK documentation.
- Publishing the known-stale v2 OpenAPI document as v3 reference.
- Ask-AI, analytics, deployment, DNS, production cutover, or changing current
  production documentation links.
- Completing the exhaustive legacy URL audit or a host-specific redirect
  implementation.
