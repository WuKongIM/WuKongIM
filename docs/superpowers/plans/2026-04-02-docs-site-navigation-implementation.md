# Docs Site Navigation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the approved `docs-site` navigation skeleton, landing pages, and placeholder docs structure for the new WuKongIM documentation site.

**Architecture:** Keep routing driven by the existing Fumadocs content loader, then introduce a task-oriented folder structure under `content/docs` with `meta.json` ordering files. Update the homepage and current `/getting-started` page so both act as routing pages into the new structure.

**Tech Stack:** React Router, Fumadocs, MDX, TypeScript

---

### Task 1: Record the approved IA

**Files:**
- Create: `docs/superpowers/specs/2026-04-02-docs-site-navigation-design.md`
- Create: `docs/superpowers/plans/2026-04-02-docs-site-navigation-implementation.md`

- [ ] Step 1: Save the approved navigation design for future migration work.
- [ ] Step 2: Save the implementation plan beside the spec.

### Task 2: Build the new docs tree

**Files:**
- Modify: `docs-site/content/docs/index.mdx`
- Modify: `docs-site/content/docs/getting-started.mdx`
- Create: `docs-site/content/docs/meta.json`
- Create: `docs-site/content/docs/**/meta.json`
- Create: `docs-site/content/docs/**.mdx`

- [ ] Step 1: Create top-level section folders and ordering metadata.
- [ ] Step 2: Add section landing pages for overview, quick start, client, server, operations, and resources.
- [ ] Step 3: Add placeholder child pages so the sidebar already reflects the target migration shape.

### Task 3: Update routing pages

**Files:**
- Modify: `docs-site/content/docs/index.mdx`
- Modify: `docs-site/content/docs/getting-started.mdx`

- [ ] Step 1: Turn the homepage into a task-based entry page with path cards.
- [ ] Step 2: Convert `/getting-started` into a short bridge page pointing to `/quick-start` and `/overview`.

### Task 4: Verify the new structure

**Files:**
- Verify: `docs-site/**`

- [ ] Step 1: Run `npm run types:check` in `docs-site`.
- [ ] Step 2: Run `npm run build` in `docs-site`.
- [ ] Step 3: Review the diff and confirm only docs-site and spec/plan files changed.
