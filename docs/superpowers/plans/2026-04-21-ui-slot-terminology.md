# UI Slot Terminology Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all user-visible `Group` / `group-*` / `分区` terminology in `ui/` with `Slot` / `slot-*` / `槽`, while keeping internal file names, page keys, and JS identifiers unchanged.

**Architecture:** Keep the current static UI structure intact and limit changes to presentation strings and sample values in the UI content sources. Update shared data in `ui/assets/data.js` and render-copy in `ui/assets/app.js`, then verify no user-visible `Group`/`group-*`/`分区` terms remain in `ui/` content.

**Tech Stack:** Static HTML, vanilla JavaScript, CSS, shell/Python verification

---

### Task 1: Inventory visible terminology

**Files:**
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`
- Verify: `ui/groups.html`
- Verify: `ui/placeholder/groups.html`

- [ ] **Step 1: Write the failing verification check**

Run a search that shows remaining visible `Group`, `group-*`, and `分区` strings in `ui/` before any edits.

- [ ] **Step 2: Run the check to verify it fails**

Run: `rg -n "Group|group-[0-9]+|分区" ui`
Expected: matches in `ui/assets/data.js` and `ui/assets/app.js`

### Task 2: Apply the terminology updates

**Files:**
- Modify: `ui/assets/data.js`
- Modify: `ui/assets/app.js`

- [ ] **Step 3: Write the minimal implementation**

Update navigation labels, page copy, metrics, table headers, drawer copy, search placeholders, and sample IDs/tags from Group terminology to Slot terminology, without renaming internal data keys or file paths.

- [ ] **Step 4: Run the verification check to verify it passes**

Run: `python3 - <<'PY'`
Inspect `ui/assets/data.js` and `ui/assets/app.js` for banned visible terms and fail if any remain.
`PY`
Expected: exit 0 with no banned visible terms reported.

### Task 3: Final verification

**Files:**
- Verify: `ui/assets/data.js`
- Verify: `ui/assets/app.js`

- [ ] **Step 5: Run final targeted verification**

Run: `rg -n "Group|group-[0-9]+|分区" ui/assets/data.js ui/assets/app.js`
Expected: no matches.
