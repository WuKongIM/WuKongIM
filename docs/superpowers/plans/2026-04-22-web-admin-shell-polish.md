# Web Admin Shell Polish Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refine the existing `web/` management shell into a more distinctive light operations console without changing the route structure, feature scope, or backend integration boundary.

**Architecture:** Keep the current SPA structure and page routing intact, and concentrate the polish in the design-token layer plus shell-level reusable components. Use `globals.css` to establish the `Precision Console` visual system, then update `SidebarNav`, `Topbar`, the shell placeholder components, and the `Dashboard` page so the whole app reads as one coherent operations console rather than a generic admin shell.

**Tech Stack:** Bun, Vite, React, TypeScript, React Router, Tailwind CSS v4, shadcn-ui, Vitest, Testing Library.

---

## References

- Existing shell implementation: `web/src/app`, `web/src/components/shell`, `web/src/pages`
- Current style entry: `web/src/styles/globals.css`
- Current tests: `web/src/app/router.test.tsx`, `web/src/app/layout/sidebar-nav.test.tsx`, `web/src/app/layout/topbar.test.tsx`, `web/src/pages/page-shells.test.tsx`
- Follow `@superpowers:test-driven-development` for behavior-level changes.
- Run `@superpowers:verification-before-completion` before claiming the polish is complete.
- Preserve the current route map and keep `ui/` untouched.

## File Structure

- Modify: `web/src/styles/globals.css` — upgrade tokens, surfaces, gradients, and global shell atmosphere.
- Modify: `web/src/app/layout/sidebar-nav.tsx` — add a stronger brand block, grouped nav treatment, active-state expression, and a bottom runtime status panel.
- Modify: `web/src/app/layout/topbar.tsx` — add global status pills, clearer view title composition, and a more console-like action strip.
- Modify: `web/src/components/shell/page-header.tsx` — make page headers feel like operational context panels rather than generic cards.
- Modify: `web/src/components/shell/section-card.tsx` — sharpen module boundaries and section hierarchy.
- Modify: `web/src/components/shell/placeholder-block.tsx` — differentiate `panel`, `list`, `table`, `canvas`, and `detail` placeholders visually.
- Modify: `web/src/components/shell/metric-placeholder.tsx` — turn metric cards into richer status-oriented shells.
- Modify: `web/src/pages/dashboard/page.tsx` — make dashboard the visual showcase of the new language.
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/slots/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/topology/page.tsx`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx` — update assertions only if accessible labels or structural hooks change.
- Modify: `web/src/app/layout/topbar.test.tsx` — update expectations only if route metadata placement changes.
- Modify: `web/src/pages/page-shells.test.tsx` — keep route-level skeleton verification aligned with the updated page copy.

### Task 1: Refresh the global visual system and shell chrome

**Files:**
- Modify: `web/src/styles/globals.css`
- Modify: `web/src/app/layout/sidebar-nav.tsx`
- Modify: `web/src/app/layout/topbar.tsx`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx`
- Modify: `web/src/app/layout/topbar.test.tsx`

- [ ] **Step 1: Write the failing layout tests for the stronger console chrome**

Extend the layout tests to lock in the new visible shell elements before implementation. Add checks like:

```tsx
test("renders the runtime status panel in the sidebar", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByText("Cluster status")).toBeInTheDocument()
  expect(screen.getByText("Single-node cluster")).toBeInTheDocument()
})

test("shows topbar environment and control pills", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByText("Control plane")).toBeInTheDocument()
  expect(screen.getByText("Manager shell")).toBeInTheDocument()
})
```

Keep the existing active-nav and route-title assertions.

- [ ] **Step 2: Run the focused shell tests to verify they fail**

Run:

```bash
cd web && bun run test src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx
```

Expected: FAIL because the new status panel and topbar pills do not exist yet.

- [ ] **Step 3: Implement the minimal token and shell-chrome changes**

Update `web/src/styles/globals.css` to define the new `Precision Console` tokens. The core direction should include:

```css
:root {
  --background: oklch(0.985 0.008 240);
  --foreground: oklch(0.23 0.02 252);
  --card: oklch(0.995 0.004 240);
  --sidebar: oklch(0.958 0.013 240);
  --primary: oklch(0.57 0.19 255);
  --muted: oklch(0.95 0.01 240);
  --success: oklch(0.72 0.14 160);
  --warning: oklch(0.79 0.14 84);
  --danger: oklch(0.64 0.19 26);
}

body {
  background:
    radial-gradient(circle at top left, color-mix(in oklab, var(--primary) 18%, transparent), transparent 32%),
    linear-gradient(180deg, #f8fbff 0%, #f2f6fb 100%);
}
```

Then update `SidebarNav` to add:
- a stronger product block,
- active item treatment with a left glow/bar,
- a bottom `Cluster status` panel with short static labels such as `Single-node cluster`, `Stable shell`, and `No live feed yet`.

Update `Topbar` to add:
- a small eyebrow or section label,
- pills or badges such as `Control plane` and `Manager shell`,
- the existing title + description in a more structured layout.

- [ ] **Step 4: Re-run the focused shell tests to verify they pass**

Run:

```bash
cd web && bun run test src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the shell chrome slice**

```bash
git add web/src/styles/globals.css web/src/app/layout/sidebar-nav.tsx web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.test.tsx web/src/app/layout/topbar.test.tsx
git commit -m "feat: polish web shell chrome"
```

### Task 2: Upgrade the shell building blocks and dashboard showcase

**Files:**
- Modify: `web/src/components/shell/page-header.tsx`
- Modify: `web/src/components/shell/section-card.tsx`
- Modify: `web/src/components/shell/placeholder-block.tsx`
- Modify: `web/src/components/shell/metric-placeholder.tsx`
- Modify: `web/src/pages/dashboard/page.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write the failing dashboard polish tests**

Update `web/src/pages/page-shells.test.tsx` so the dashboard is verified against the new showcase language. Keep the route coverage table, and add at least one dashboard-specific assertion such as:

```tsx
test("dashboard shows the operations showcase blocks", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByText("Operations Snapshot")).toBeInTheDocument()
  expect(screen.getByText("Active alerts lane")).toBeInTheDocument()
})
```

If you change the primary section labels for other pages, update the route table literals in the same file.

- [ ] **Step 2: Run the page-shell tests to verify they fail**

Run:

```bash
cd web && bun run test src/pages/page-shells.test.tsx
```

Expected: FAIL because the updated dashboard labels and richer shell blocks are not implemented yet.

- [ ] **Step 3: Implement the minimal shell-block and dashboard changes**

Refine the shell components with the new visual direction:

- `PageHeader` should feel like a view-context panel, using a softer gradient, tighter title grouping, and a clearer action rail.
- `SectionCard` should get a more deliberate header line, sharper module framing, and a subtler elevated surface.
- `PlaceholderBlock` should vary per `kind` so `table` feels tabular, `canvas` feels spatial, and `detail` feels panel-like.
- `MetricPlaceholder` should expose `label`, a stronger faux-value area, and a compact status chip.

Then make `DashboardPage` the showcase page with sections such as:

```tsx
<PageHeader title="Dashboard workspace" ... />
<section className="grid ...">
  <MetricPlaceholder label="Nodes" hint="..." />
  ...
</section>
<section className="grid ...">
  <SectionCard title="Operations Snapshot">...</SectionCard>
  <SectionCard title="Active alerts lane">...</SectionCard>
</section>
<section className="grid ...">
  <SectionCard title="Replication posture">...</SectionCard>
  <SectionCard title="Control queue">...</SectionCard>
</section>
```

Do not add real data logic — stay at shell level.

- [ ] **Step 4: Re-run the page-shell tests to verify they pass**

Run:

```bash
cd web && bun run test src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the dashboard polish slice**

```bash
git add web/src/components/shell/page-header.tsx web/src/components/shell/section-card.tsx web/src/components/shell/placeholder-block.tsx web/src/components/shell/metric-placeholder.tsx web/src/pages/dashboard/page.tsx web/src/pages/page-shells.test.tsx
git commit -m "feat: polish web dashboard shell"
```

### Task 3: Align the remaining route skeletons with the polished console language

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/slots/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/topology/page.tsx`
- Modify: `web/src/pages/page-shells.test.tsx` only if route section labels change again.

- [ ] **Step 1: Write or tighten failing route-shell assertions if labels change**

If the updated page polish changes any of the current section labels (`Node Inventory`, `Channel List`, `Connection Table`, `Slot Health`, `Traffic Overview`, `Topology Canvas`), update the route table in `web/src/pages/page-shells.test.tsx` first and keep it failing until the pages match.

- [ ] **Step 2: Run the route-shell test file to verify the mismatch**

Run:

```bash
cd web && bun run test src/pages/page-shells.test.tsx
```

Expected: FAIL only if you changed route-section copy; otherwise skip to Step 3 and preserve the passing assertions.

- [ ] **Step 3: Implement the minimal page-level polish across the remaining routes**

Bring the six non-dashboard pages into the same design language by:
- upgrading each `PageHeader` usage with better eyebrow/description/action composition,
- making filter/tool rows feel more operational,
- ensuring the main section card and placeholder kinds match the page role,
- preserving each page’s current route-level identity.

Example direction:

```tsx
<PageHeader
  eyebrow="Runtime"
  title="Nodes workspace"
  description="Node operations land here first: inventory, role, and lifecycle shells."
  actions={...}
/>
<SectionCard title="Node Inventory" description="...">
  <div className="mb-4 grid ...">...</div>
  <PlaceholderBlock kind="table" />
</SectionCard>
```

Use the same language family for `Channels`, `Connections`, `Slots`, `Network`, and `Topology`.

- [ ] **Step 4: Re-run the full web test suite to verify all route-level assertions pass**

Run:

```bash
cd web && bun run test
```

Expected: PASS.

- [ ] **Step 5: Commit the page-alignment slice**

```bash
git add web/src/pages web/src/pages/page-shells.test.tsx
git commit -m "feat: align web route shells"
```

### Task 4: Final verification on the polished shell

**Files:**
- Modify: `web/README.md` only if the updated polish changes local workflow wording.

- [ ] **Step 1: Re-read the plan and verify the scope stayed inside shell polish**

Check that the implementation did not add:
- API calls,
- auth,
- charts,
- topology rendering,
- route changes,
- `ui/` edits.

- [ ] **Step 2: Run the complete verification suite fresh**

Run:

```bash
cd web && bun run test
cd web && bun run build
```

Expected: both commands PASS.

- [ ] **Step 3: Run the dev server once for a quick manual sanity check**

Run:

```bash
cd web && bun run dev --host 127.0.0.1 --port 4173
```

Then confirm manually:
- sidebar reads as a console navigation panel,
- topbar carries route context plus status pills,
- dashboard is visibly stronger than the first-pass shell,
- the other six pages still render without blank states or broken layout.

- [ ] **Step 4: If verification exposes failures, fix only the proven issue and re-run the same verification commands**

Do not expand scope into API integration or new interaction systems.

- [ ] **Step 5: Commit the final cleanup if needed**

```bash
git add web
git commit -m "style: refine web admin shell"
```
