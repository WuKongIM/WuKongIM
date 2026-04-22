# Docs Site Homepage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refresh the `docs-site` root homepage so first-time users see a complete documentation homepage with product context, primary entry paths, secondary recommendations, and a guided reading sequence.

**Architecture:** Keep the change scoped to the existing docs homepage MDX entry and any shared styling/components it already relies on. Update content structure first, then adjust presentation only if the current docs primitives are not enough to express the approved hierarchy cleanly.

**Tech Stack:** Fumadocs, React Router docs-site, MDX, TypeScript, existing docs-site CSS

---

### Task 1: Lock in the written design

**Files:**
- Create: `docs/superpowers/specs/2026-04-03-docs-site-homepage-design.md`
- Create: `docs/superpowers/plans/2026-04-03-docs-site-homepage.md`

- [ ] Step 1: Save the approved homepage design in the spec file.
- [ ] Step 2: Save this implementation plan beside the spec.

### Task 2: Add a failing verification for homepage structure

**Files:**
- Create: `docs-site/app/lib/homepage.test.mjs`
- Test: `docs-site/app/lib/homepage.test.mjs`

- [ ] Step 1: Write a minimal test that reads `docs-site/content/docs/index.mdx` and asserts the homepage contains the approved sections:
  `WuKongIM 文档`, four primary entry cards, four capability blocks, three secondary entries, and the reading path.
- [ ] Step 2: Run `cd docs-site && node --test app/lib/homepage.test.mjs` and verify it fails against the current homepage.

### Task 3: Refresh the homepage content

**Files:**
- Modify: `docs-site/content/docs/index.mdx`

- [ ] Step 1: Rewrite the hero copy to describe WuKongIM and explain how readers should use the site.
- [ ] Step 2: Replace the current entry layout with the approved four primary cards and one-line descriptions.
- [ ] Step 3: Add the capability summary section with the approved four capability blocks.
- [ ] Step 4: Add the lighter-weight secondary recommendation cards for operations, resources, and AI Agent.
- [ ] Step 5: Add the recommended reading sequence section for new users.

### Task 4: Adjust styling only if needed

**Files:**
- Modify: `docs-site/content/docs/index.mdx`
- Modify: `docs-site/app/app.css`

- [ ] Step 1: Check whether existing MDX card/list styling already gives enough hierarchy.
- [ ] Step 2: If hierarchy is too weak, add the smallest CSS changes needed to distinguish hero, primary entries, and secondary entries without affecting the rest of the site.

### Task 5: Verify the homepage refresh

**Files:**
- Verify only

- [ ] Step 1: Run `cd docs-site && node --test app/lib/homepage.test.mjs`.
- [ ] Step 2: Run `cd docs-site && npm run types:check`.
- [ ] Step 3: Run `cd docs-site && npm run build`.
- [ ] Step 4: Review the generated homepage output and confirm `/` is still prerendered.
