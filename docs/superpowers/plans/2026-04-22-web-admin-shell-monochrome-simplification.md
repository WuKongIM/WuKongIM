# Web Admin Shell Monochrome Simplification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework the existing `web/` admin shell into a light monochrome, table-oriented management console without changing routes, data scope, or backend integration boundaries.

**Architecture:** Keep the current React SPA structure and route map intact, and concentrate the simplification in the shared shell layers first. Establish neutral global tokens in `globals.css`, simplify the shell chrome in `SidebarNav` and `Topbar`, flatten the shared shell building blocks, then align each route page to the approved table/list/status-row grammar.

**Tech Stack:** Bun, Vite, React, TypeScript, React Router, Tailwind CSS v4, shadcn-ui, Vitest, Testing Library.

---

## References

- Approved design spec: `docs/superpowers/specs/2026-04-22-web-admin-shell-monochrome-simplification-design.md`
- Existing web shell implementation: `web/src/app`, `web/src/components/shell`, `web/src/pages`
- Current route metadata: `web/src/lib/navigation.ts`
- Current tests: `web/src/app/layout/sidebar-nav.test.tsx`, `web/src/app/layout/topbar.test.tsx`, `web/src/pages/page-shells.test.tsx`
- Follow `@superpowers:test-driven-development` for each behavior change.
- Run `@superpowers:verification-before-completion` before claiming implementation is done.

## File Structure

- Modify: `web/src/styles/globals.css` — replace the blue/glass visual system with neutral monochrome tokens and flat page surfaces.
- Modify: `web/src/lib/navigation.ts` — keep the route map, but simplify route descriptions so the chrome and pages use neutral operational copy.
- Modify: `web/src/app/layout/sidebar-nav.tsx` — remove menu descriptions, add monochrome child icons, flatten the brand block, and simplify the bottom cluster context.
- Modify: `web/src/app/layout/topbar.tsx` — remove decorative pills, compress the chrome row, and keep only quiet route context plus global actions.
- Modify: `web/src/components/shell/page-container.tsx` — tighten page density only if the new table-first layout needs smaller vertical spacing.
- Modify: `web/src/components/shell/page-header.tsx` — convert the hero-style header into a flat tool-page header with optional filter/tool row support.
- Modify: `web/src/components/shell/section-card.tsx` — flatten the panel chrome into a data-pane title bar.
- Modify: `web/src/components/shell/metric-placeholder.tsx` — turn decorative KPI cards into compact metric cells.
- Modify: `web/src/components/shell/placeholder-block.tsx` — replace decorative backgrounds with structural table/list/detail wireframes.
- Create: `web/src/components/shell/shell-components.test.tsx` — focused component-level tests for the simplified shell primitives.
- Modify: `web/src/pages/dashboard/page.tsx` — turn the landing page into a quiet operations workbench.
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/slots/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/topology/page.tsx`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx` — update shell expectations for the simplified sidebar.
- Modify: `web/src/app/layout/topbar.test.tsx` — update shell expectations for the thinner top bar.
- Modify: `web/src/pages/page-shells.test.tsx` — lock in the simplified page headings and table-first section labels.

### Task 1: Simplify the shell chrome and lock it with focused tests

**Files:**
- Modify: `web/src/app/layout/sidebar-nav.test.tsx`
- Modify: `web/src/app/layout/topbar.test.tsx`
- Modify: `web/src/styles/globals.css`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/layout/sidebar-nav.tsx`
- Modify: `web/src/app/layout/topbar.tsx`

- [ ] **Step 1: Write the failing sidebar and topbar tests**

Update the layout tests first so they describe the approved monochrome shell:

```tsx
test("renders sidebar links without description copy", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/slots"] })

  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("link", { name: "Slots" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  expect(screen.queryByText("Slot distribution and status shell.")).not.toBeInTheDocument()
})

test("removes legacy topbar pills while keeping global actions", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })

  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("button", { name: /refresh/i })).toBeInTheDocument()
  expect(screen.getByRole("button", { name: /search/i })).toBeInTheDocument()
  expect(screen.queryByText("Control plane")).not.toBeInTheDocument()
  expect(screen.queryByText("Manager shell")).not.toBeInTheDocument()
})
```

Keep the existing active-link assertion. Remove or rewrite assertions that depend on the old decorative status panel wording.

- [ ] **Step 2: Run the focused layout tests to verify they fail**

Run:

```bash
cd web && bunx vitest run src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx
```

Expected: FAIL because sidebar descriptions still render and the top bar still shows the old pills.

- [ ] **Step 3: Implement the minimal shell-chrome simplification**

Apply the approved shell changes:

1. In `web/src/styles/globals.css`, replace the accent-led surface language with neutral tokens:

```css
:root {
  --background: oklch(0.995 0 0);
  --foreground: oklch(0.18 0 0);
  --card: oklch(0.985 0 0);
  --muted: oklch(0.965 0 0);
  --border: oklch(0.88 0 0);
  --primary: oklch(0.2 0 0);
  --sidebar: oklch(0.97 0 0);
  --radius: 0.5rem;
}

body {
  background: var(--background);
}
```

Also remove the fixed grid overlay and the radial gradient background.

2. In `web/src/lib/navigation.ts`, rewrite route descriptions into quiet operational text, for example:

```ts
{
  href: "/network",
  title: "Network",
  description: "Transport summary and runtime status.",
}
```

3. In `web/src/app/layout/sidebar-nav.tsx`, flatten the structure:

```tsx
<NavLink
  className={({ isActive }) =>
    cn(
      "flex items-center gap-3 rounded-md border px-3 py-2 text-sm transition-colors",
      isActive
        ? "border-border bg-background text-foreground"
        : "border-transparent text-muted-foreground hover:bg-background hover:text-foreground",
    )
  }
  to={item.href}
>
  <item.icon aria-hidden className="size-4" />
  <span className="font-medium">{item.title}</span>
</NavLink>
```

Do not render `item.description` in the sidebar anymore. Keep the bottom cluster context, but render it as plain bordered rows without gradient treatment.

4. In `web/src/app/layout/topbar.tsx`, keep only quiet route context plus actions:

```tsx
<header className="border-b border-border bg-background px-6 py-3" role="banner">
  <div className="flex items-center justify-between gap-4">
    <div className="min-w-0">
      <h1 className="text-sm font-semibold text-foreground">{page?.title}</h1>
      <p className="text-xs text-muted-foreground">{page?.description}</p>
    </div>
    <div className="flex items-center gap-2">
      <Button size="sm" variant="outline">Refresh</Button>
      <Button size="sm" variant="outline">Search</Button>
    </div>
  </div>
</header>
```

Remove `Control plane`, `Manager shell`, `Precision Console`, `Sparkles`, and other showcase-style chrome.

- [ ] **Step 4: Re-run the focused layout tests to verify they pass**

Run:

```bash
cd web && bunx vitest run src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the shell chrome slice**

```bash
git add web/src/styles/globals.css web/src/lib/navigation.ts web/src/app/layout/sidebar-nav.tsx web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.test.tsx web/src/app/layout/topbar.test.tsx
git commit -m "style: simplify web shell chrome"
```

### Task 2: Flatten the shared shell primitives into table-first building blocks

**Files:**
- Modify: `web/src/components/shell/page-container.tsx`
- Modify: `web/src/components/shell/page-header.tsx`
- Modify: `web/src/components/shell/section-card.tsx`
- Modify: `web/src/components/shell/metric-placeholder.tsx`
- Modify: `web/src/components/shell/placeholder-block.tsx`
- Create: `web/src/components/shell/shell-components.test.tsx`

- [ ] **Step 1: Write the failing shared-component tests**

Create `web/src/components/shell/shell-components.test.tsx` so the shared primitives can be finished and committed with green tests before any page-specific work starts. Add focused expectations such as:

```tsx
test("page header renders a flat tool row", () => {
  render(
    <PageHeader
      title="Dashboard"
      description="Runtime summary."
      actions={<button type="button">Refresh</button>}
    >
      <div>Scope: single-node cluster</div>
    </PageHeader>,
  )

  expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Scope: single-node cluster")).toBeInTheDocument()
})

test("metric placeholder is a compact data cell", () => {
  render(<MetricPlaceholder label="Nodes" hint="Registered node count." />)

  expect(screen.getByText("Nodes")).toBeInTheDocument()
  expect(screen.getByText("Registered node count.")).toBeInTheDocument()
  expect(screen.queryByText("Ready")).not.toBeInTheDocument()
})
```

Add a placeholder-block assertion too if you need a stable hook, for example with `data-testid` or visible section-like wireframe labels.

- [ ] **Step 2: Run the focused shared-component test file to verify it fails**

Run:

```bash
cd web && bunx vitest run src/components/shell/shell-components.test.tsx
```

Expected: FAIL because the current shared shell components still render the hero-style header and decorative metric card state.

- [ ] **Step 3: Implement the minimal shared shell simplification**

Refactor the shared shell components before touching all route pages:

1. In `web/src/components/shell/page-container.tsx`, reduce excess spacing only if needed for denser table layouts:

```tsx
<div className={cn("mx-auto flex w-full max-w-7xl flex-col gap-4 px-6 py-6", className)}>
```

2. In `web/src/components/shell/page-header.tsx`, replace the hero-card treatment with a flat bordered tool header:

```tsx
<section className="rounded-md border border-border bg-card">
  <div className="flex flex-col gap-4 p-5 lg:flex-row lg:items-start lg:justify-between">
    <div>
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
      <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">{description}</p>
    </div>
    {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
  </div>
  {children ? <div className="border-t border-border bg-muted/50 p-4">{children}</div> : null}
</section>
```

Remove decorative gradient fills and eyebrow-pill reliance.

3. In `web/src/components/shell/section-card.tsx`, flatten the panel header:

```tsx
<Card className="border border-border bg-card shadow-none">
  <CardHeader className="border-b border-border bg-muted/40">
    <CardTitle className="text-sm font-semibold">{title}</CardTitle>
  </CardHeader>
  <CardContent className="pt-4">{children}</CardContent>
</Card>
```

4. In `web/src/components/shell/metric-placeholder.tsx`, convert the KPI card into a compact metric cell:

```tsx
<Card className="border border-border bg-muted/30 shadow-none">
  <CardContent className="space-y-2 pt-4">
    <div className="text-[11px] font-medium uppercase tracking-[0.18em] text-muted-foreground">
      {label}
    </div>
    <div className="text-2xl font-semibold text-foreground">--</div>
    <p className="text-xs leading-5 text-muted-foreground">{hint ?? "Waiting for live data."}</p>
  </CardContent>
</Card>
```

Remove the green ready badge, gradient inner block, and arrow ornament.

5. In `web/src/components/shell/placeholder-block.tsx`, replace ambient art with structural wireframes:

```tsx
const kindClasses = {
  panel: "min-h-40",
  list: "min-h-36",
  table: "min-h-56",
  canvas: "min-h-[22rem]",
  detail: "min-h-48",
}
```

Render neutral horizontal lines, simple column headers for `table`, and stacked bordered rows for `list`/`detail`.

- [ ] **Step 4: Re-run the shared-component test file to verify it passes**

Run:

```bash
cd web && bunx vitest run src/components/shell/shell-components.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the shared shell primitive slice**

```bash
git add web/src/components/shell/page-container.tsx web/src/components/shell/page-header.tsx web/src/components/shell/section-card.tsx web/src/components/shell/metric-placeholder.tsx web/src/components/shell/placeholder-block.tsx web/src/components/shell/shell-components.test.tsx
git commit -m "style: flatten web shell building blocks"
```

### Task 3: Turn the dashboard into the approved monochrome operations workbench

**Files:**
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/pages/dashboard/page.tsx`

- [ ] **Step 1: Write the failing dashboard-specific assertions**

Extend `web/src/pages/page-shells.test.tsx` with a dashboard-specific test that locks in the quieter table-first landing page:

```tsx
test("dashboard shows monochrome workbench sections", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Operations Summary")).toBeInTheDocument()
  expect(screen.getByText("Alert List")).toBeInTheDocument()
  expect(screen.getByText("Control Queue")).toBeInTheDocument()
  expect(screen.queryByText("Pin board")).not.toBeInTheDocument()
})
```

Adjust the exact section names if you choose different neutral labels, but make sure the test reflects the approved copy direction.

- [ ] **Step 2: Run the dashboard test to verify it fails**

Run:

```bash
cd web && bunx vitest run src/pages/page-shells.test.tsx -t "dashboard shows monochrome workbench sections"
```

Expected: FAIL because `Dashboard workspace`, decorative action labels, and old section titles still render.

- [ ] **Step 3: Implement the minimal dashboard rewrite**

Refactor `web/src/pages/dashboard/page.tsx` to follow the approved grammar:

```tsx
export function DashboardPage() {
  return (
    <PageContainer>
      <PageHeader
        title="Dashboard"
        description="Runtime summary, queues, and operator-facing overview."
        actions={
          <>
            <Button size="sm" variant="outline">Refresh</Button>
            <Button size="sm">Export</Button>
          </>
        }
      >
        <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
          <div className="rounded-md border border-border bg-background px-3 py-2">
            Scope: single-node cluster
          </div>
          <div className="rounded-md border border-border bg-background px-3 py-2">
            Status: static
          </div>
        </div>
      </PageHeader>

      <section className="grid gap-3 md:grid-cols-4">
        <MetricPlaceholder label="Nodes" hint="Registered node count." />
        <MetricPlaceholder label="Channels" hint="Channel summary placeholder." />
        <MetricPlaceholder label="Connections" hint="Connection summary placeholder." />
        <MetricPlaceholder label="Slots" hint="Slot summary placeholder." />
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
        <SectionCard title="Operations Summary" description="Primary runtime table placeholder.">
          <PlaceholderBlock kind="table" />
        </SectionCard>
        <SectionCard title="Alert List" description="Compact operator-facing status items.">
          <PlaceholderBlock kind="list" />
        </SectionCard>
      </section>

      <section className="grid gap-4 xl:grid-cols-[0.95fr_1.05fr]">
        <SectionCard title="Replication Status" description="Dense status-row placeholder.">
          <PlaceholderBlock kind="detail" />
        </SectionCard>
        <SectionCard title="Control Queue" description="Secondary work queue table placeholder.">
          <PlaceholderBlock kind="table" />
        </SectionCard>
      </section>
    </PageContainer>
  )
}
```

Keep the page quiet and neutral. Do not reintroduce pills, hero labels, or glossy metric cards.

- [ ] **Step 4: Re-run the dashboard assertions to verify they pass**

Run:

```bash
cd web && bunx vitest run src/pages/page-shells.test.tsx -t "dashboard shows monochrome workbench sections"
```

Expected: PASS.

- [ ] **Step 5: Commit the dashboard slice**

```bash
git add web/src/pages/dashboard/page.tsx web/src/pages/page-shells.test.tsx
git commit -m "style: simplify dashboard workbench shell"
```

### Task 4: Align the remaining route shells and finish with full verification

**Files:**
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/pages/connections/page.tsx`
- Modify: `web/src/pages/slots/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/topology/page.tsx`

- [ ] **Step 1: Write the failing route-shell expectations first**

Update the route table in `web/src/pages/page-shells.test.tsx` so every route expects simplified page titles and neutral primary sections:

```tsx
it.each([
  ["/dashboard", "Dashboard", "Operations Summary"],
  ["/nodes", "Nodes", "Node Inventory"],
  ["/channels", "Channels", "Channel List"],
  ["/connections", "Connections", "Connection Table"],
  ["/slots", "Slots", "Slot Status"],
  ["/network", "Network", "Transport Summary"],
  ["/topology", "Topology", "Topology View"],
])("renders %s shell", async (path, title, section) => {
  const router = createMemoryRouter(routes, { initialEntries: [path] })

  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument()
  expect(screen.getByText(section)).toBeInTheDocument()
  expect(screen.queryByText(/workspace/i)).not.toBeInTheDocument()
})
```

Make the test red before changing the route pages.

- [ ] **Step 2: Run the route-shell test file to verify it fails**

Run:

```bash
cd web && bunx vitest run src/pages/page-shells.test.tsx
```

Expected: FAIL because the remaining route pages still render `workspace`, `shell`, `lane`, and older section labels.

- [ ] **Step 3: Implement the minimal page-by-page copy and layout alignment**

Apply the same grammar to every route page:

1. Simplify titles:

```tsx
<PageHeader
  title="Nodes"
  description="Node inventory, roles, and runtime status."
  actions={...}
>
```

Use the same pattern for `Channels`, `Connections`, `Slots`, `Network`, and `Topology`.

2. Replace decorative child content under `PageHeader` with plain filter/tool rows:

```tsx
<div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
  <div className="rounded-md border border-border bg-background px-3 py-2">
    Scope: all nodes
  </div>
  <div className="rounded-md border border-border bg-background px-3 py-2">
    Status: static
  </div>
</div>
```

3. Keep each page focused on one primary structured surface:

- `web/src/pages/nodes/page.tsx` → `Node Inventory` table plus a compact filter row
- `web/src/pages/channels/page.tsx` → `Channel List` table plus compact filter row
- `web/src/pages/connections/page.tsx` → `Connection Table` table plus compact filter row
- `web/src/pages/slots/page.tsx` → `Slot Status` summary cells plus primary table
- `web/src/pages/network/page.tsx` → `Transport Summary` plus compact detail panel
- `web/src/pages/topology/page.tsx` → `Topology View` canvas placeholder plus `Context Detail`

4. Replace terms like `workspace`, `shell`, `lane`, and `reserved` with neutral operator-facing copy.

- [ ] **Step 4: Re-run the full route-shell test file to verify it passes**

Run:

```bash
cd web && bunx vitest run src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Run the full web test suite and production build**

Run:

```bash
cd web && bun run test
cd web && bun run build
```

Expected:

- `bun run test` → PASS across all Vitest suites
- `bun run build` → PASS with a clean production bundle

- [ ] **Step 6: Commit the final page-alignment slice**

```bash
git add web/src/pages/page-shells.test.tsx web/src/pages/nodes/page.tsx web/src/pages/channels/page.tsx web/src/pages/connections/page.tsx web/src/pages/slots/page.tsx web/src/pages/network/page.tsx web/src/pages/topology/page.tsx
git commit -m "style: align web pages with monochrome shell"
```

## Notes for the Implementer

- Do not change the route map or add data-fetching logic.
- Keep icons monochrome and `aria-hidden` unless they communicate unique meaning.
- Prefer updating shared shell components over one-off page-specific styling forks.
- If a button style can be achieved through the neutral token set, do not add new button variants.
- Keep tests focused and fast; this is shell-level UI work, not integration behavior.

## Manual Review Checklist

- [ ] Sidebar items render only titles plus icons; descriptions are not visible.
- [ ] Top bar no longer shows `Control plane`, `Manager shell`, or `Precision Console`.
- [ ] Page headers no longer use hero-card treatment.
- [ ] Metric blocks read like compact data cells, not glossy KPI cards.
- [ ] Placeholder surfaces read like tables, lists, or status rows.
- [ ] Route pages no longer use `workspace`, `shell`, or `lane` in visible copy.
- [ ] `cd web && bun run test` passes.
- [ ] `cd web && bun run build` passes.
