# AI Agent Docs-Site Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `AI Agent` top-level docs-site section that reorganizes the event stream design docs into public-facing MDX pages and includes architecture landing guidance plus an end-to-end walkthrough.

**Architecture:** Keep all changes inside `docs-site`. Create a new top-level docs section, convert the two design documents into MDX pages, add a dedicated architecture guidance page and a lifecycle walkthrough page, and link the section from existing entry pages so the new section is discoverable without mixing it back into generic message API docs.

**Tech Stack:** Fumadocs, React Router docs-site, MDX, JSON meta navigation

---

### Task 1: Create the docs information architecture

**Files:**
- Create: `docs-site/content/docs/ai-agent/meta.json`
- Create: `docs-site/content/docs/ai-agent/index.mdx`
- Modify: `docs-site/content/docs/meta.json`
- Modify: `docs-site/app/lib/icons.ts`

- [ ] Step 1: Add `ai-agent` to the top-level docs tree.
- [ ] Step 2: Add an icon mapping for the new top-level section.
- [ ] Step 3: Create the `AI Agent` landing page with reading guidance.

### Task 2: Convert source design docs into MDX pages

**Files:**
- Create: `docs-site/content/docs/ai-agent/message-event-stream.mdx`
- Create: `docs-site/content/docs/ai-agent/event-merge-rules.mdx`
- Source: `docs/design/Message_Event_Stream_Design.md`
- Source: `docs/design/Event_Merge_Rules.md`

- [ ] Step 1: Extract the public-facing structure from the event stream design doc.
- [ ] Step 2: Extract the public-facing structure from the merge rules doc.
- [ ] Step 3: Rewrite both into concise docs-site MDX pages with examples and cross-links.

### Task 3: Add architecture landing guidance

**Files:**
- Create: `docs-site/content/docs/ai-agent/architecture-guidance.mdx`
- Modify: `docs-site/content/docs/ai-agent/meta.json`
- Modify: `docs-site/content/docs/ai-agent/index.mdx`
- Modify: `docs-site/content/docs/ai-agent/message-event-stream.mdx`
- Modify: `docs-site/content/docs/ai-agent/event-merge-rules.mdx`

- [ ] Step 1: Add the new page to the AI Agent section order.
- [ ] Step 2: Write the guidance page covering layering, write path, read path, rollout, and anti-patterns.
- [ ] Step 3: Add cross-links so the reading order flows into the guidance page.

### Task 4: Add end-to-end walkthrough page

**Files:**
- Create: `docs-site/content/docs/ai-agent/end-to-end-walkthrough.mdx`
- Modify: `docs-site/content/docs/ai-agent/meta.json`
- Modify: `docs-site/content/docs/ai-agent/index.mdx`
- Modify: `docs-site/content/docs/ai-agent/architecture-guidance.mdx`

- [ ] Step 1: Add the walkthrough page to the AI Agent section order.
- [ ] Step 2: Write one complete lifecycle walkthrough from anchor message to event replay.
- [ ] Step 3: Add links so readers can move from architecture guidance into the walkthrough.

### Task 5: Add discovery links from existing docs

**Files:**
- Modify: `docs-site/content/docs/index.mdx`
- Modify: `docs-site/content/docs/server/message/index.mdx`

- [ ] Step 1: Add an `AI Agent` entry on the docs homepage.
- [ ] Step 2: Add a cross-link from the server message landing page to the new AI Agent docs.

### Task 6: Verify the docs-site build

**Files:**
- Verify only

- [ ] Step 1: Run `cd docs-site && npm run types:check`
- [ ] Step 2: Run `cd docs-site && npm run build`
- [ ] Step 3: Review the output and confirm there are no new failures caused by this work.
