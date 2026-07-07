# Web Editorial Console Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the WuKongIM `web` manager UI into the approved white editorial console direction while preserving operational density.

**Architecture:** Start with semantic tokens and shared shell components so most pages inherit the new visual language, then apply page-specific layout polish only to `cluster-monitor`, `nodes`, `plugins`, and `login`. Keep API behavior, routing, permissions, and i18n semantics unchanged; visual changes should be expressed through existing React components and Tailwind classes.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, shadcn/radix primitives, lucide-react, Vitest, Testing Library, Bun.

---

## File Structure

- Modify `web/src/styles/globals.css`: map existing CSS variables to the `web/DESIGN.md` editorial console palette, remove glow backgrounds, tighten radius tokens, and keep dark mode usable.
- Modify `web/src/app/layout/app-shell.tsx`: remove decorative fixed radial overlay and keep the app shell flat.
- Modify `web/src/app/layout/topbar.tsx`: flatten the topbar, remove glowing logo/card treatment, keep existing accessible nav links and auth controls.
- Modify `web/src/app/layout/sidebar-nav.tsx`: convert sidebar from card-heavy cockpit block to rule-separated section navigation.
- Modify `web/src/components/ui/button.tsx`: make default buttons near-black pills and flatten secondary/outline/destructive variants.
- Modify `web/src/components/ui/card.tsx`: reduce default radius and shadow-like ring treatment.
- Modify `web/src/components/shell/page-container.tsx`: keep a wide operations container but tune spacing for open headers and dense pages.
- Modify `web/src/components/shell/page-header.tsx`: replace rounded shadow header card with open heading plus optional ruled child toolbar.
- Modify `web/src/components/shell/section-card.tsx`: make section containers flat, thin-bordered, and 8px-radius.
- Modify `web/src/components/shell/status-metric-card.tsx`: align metrics with editorial data cells.
- Modify `web/src/components/manager/status-badge.tsx`: keep small semantic badges and align fills/borders with the new palette.
- Modify `web/src/pages/cluster-monitor/page.tsx`: keep the data flow unchanged and use the new shell spacing.
- Modify `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`: make the monitor toolbar a compact rule-based control row.
- Modify `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`: convert snapshot cards into compact key-value cells.
- Modify `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`: tune grid density.
- Modify `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.tsx`: flatten monitor cards while preserving chart dimensions.
- Modify `web/src/pages/nodes/page.tsx`: keep node APIs/actions unchanged, flatten the summary/list surfaces, and preserve lifecycle dialogs.
- Modify `web/src/pages/plugins/page.tsx`: keep plugin APIs/actions unchanged, separate inventory and binding surfaces visually, and flatten rows/forms.
- Modify `web/src/pages/login/page.tsx`: remove glow background and restyle the two-column login surface.
- Modify tests only where the planned DOM/class changes make existing visual assertions stale:
  - `web/src/app/layout/sidebar-nav.test.tsx`
  - `web/src/app/layout/topbar.test.tsx`
  - `web/src/components/shell/shell-components.test.tsx`
  - `web/src/pages/login/page.test.tsx`
  - `web/src/pages/cluster-monitor/page.test.tsx`
  - `web/src/pages/nodes/page.test.tsx`
  - `web/src/pages/plugins/page.test.tsx`

---

### Task 1: Global Tokens And Flat Shell Base

**Files:**
- Modify: `web/src/styles/globals.css`
- Modify: `web/src/app/layout/app-shell.tsx`
- Test: `web/src/app/layout/sidebar-nav.test.tsx`

- [ ] **Step 1: Write the failing shell flatness assertion**

In `web/src/app/layout/sidebar-nav.test.tsx`, update `keeps the primary menu outside the scrollable content area` so it also asserts the shell no longer has the decorative radial overlay.

```tsx
test("keeps the primary menu outside the scrollable content area", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  const main = await screen.findByRole("main")
  const contentFrame = main.parentElement
  const appShell = contentFrame?.parentElement

  expect(appShell).toHaveClass("h-screen", "overflow-hidden")
  expect(contentFrame).toHaveClass("min-h-0", "flex-1")
  expect(main).toHaveClass("min-h-0", "overflow-y-auto")
  expect(screen.getByRole("navigation", { name: "Primary navigation" }).parentElement).toBe(contentFrame)
  expect(document.querySelector("[class*='radial-gradient']")).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
cd web && bun run test -- src/app/layout/sidebar-nav.test.tsx
```

Expected: FAIL because `AppShell` still renders the fixed decorative radial overlay.

- [ ] **Step 3: Update light/dark CSS tokens**

In `web/src/styles/globals.css`, replace the current green-glow token set with this editorial console token set. Keep the imports and Tailwind `@theme inline` mapping intact.

```css
:root {
  --background: #ffffff;
  --foreground: #212121;
  --card: #ffffff;
  --card-foreground: #212121;
  --popover: #ffffff;
  --popover-foreground: #212121;
  --primary: #17171c;
  --primary-foreground: #ffffff;
  --secondary: #eeece7;
  --secondary-foreground: #212121;
  --muted: #f6f6f4;
  --muted-foreground: #616161;
  --accent: #edfce9;
  --accent-foreground: #003c33;
  --destructive: #b30000;
  --success: #167a42;
  --warning: #9b6516;
  --input: #d9d9dd;
  --border: #d9d9dd;
  --ring: #4c6ee6;
  --chart-1: #1863dc;
  --chart-2: #167a42;
  --chart-3: #9b6516;
  --chart-4: #b30000;
  --chart-5: #75758a;
  --status-healthy: #167a42;
  --status-running: #1863dc;
  --status-warning: #9b6516;
  --status-error: #b30000;
  --radius: 0.5rem;
  --sidebar: #ffffff;
  --sidebar-foreground: #212121;
  --sidebar-primary: #17171c;
  --sidebar-primary-foreground: #ffffff;
  --sidebar-accent: #f6f6f4;
  --sidebar-accent-foreground: #212121;
  --sidebar-border: #d9d9dd;
  --sidebar-ring: #4c6ee6;
  --top-section-active: #17171c;
  --top-section-active-foreground: #ffffff;
  font-family: "Geist Variable", "Inter", "Arial", ui-sans-serif, system-ui, sans-serif;
  color: var(--foreground);
  background: var(--background);
  color-scheme: light;
  font-synthesis: none;
  text-rendering: optimizeLegibility;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

body {
  margin: 0;
  min-height: 100vh;
  background: var(--background);
  color: var(--foreground);
}

.dark {
  --background: #071829;
  --foreground: #f7fafc;
  --card: #0b1f33;
  --card-foreground: #f7fafc;
  --popover: #0b1f33;
  --popover-foreground: #f7fafc;
  --primary: #ffffff;
  --primary-foreground: #17171c;
  --secondary: #112a3f;
  --secondary-foreground: #f7fafc;
  --muted: #102438;
  --muted-foreground: #b8c2cc;
  --accent: #003c33;
  --accent-foreground: #edfce9;
  --destructive: #ff8a7a;
  --success: #65d88a;
  --warning: #f5c76b;
  --input: #26394d;
  --border: #2c4054;
  --ring: #8fa2ff;
  --chart-1: #8fb7ff;
  --chart-2: #65d88a;
  --chart-3: #f5c76b;
  --chart-4: #ff8a7a;
  --chart-5: #b8c2cc;
  --status-healthy: #65d88a;
  --status-running: #8fb7ff;
  --status-warning: #f5c76b;
  --status-error: #ff8a7a;
  --sidebar: #071829;
  --sidebar-foreground: #f7fafc;
  --sidebar-primary: #ffffff;
  --sidebar-primary-foreground: #17171c;
  --sidebar-accent: #102438;
  --sidebar-accent-foreground: #f7fafc;
  --sidebar-border: #2c4054;
  --sidebar-ring: #8fa2ff;
  --top-section-active: #ffffff;
  --top-section-active-foreground: #17171c;
  color-scheme: dark;
}

.top-section-link-active {
  background: var(--top-section-active);
  color: var(--top-section-active-foreground);
  box-shadow: none;
}
```

- [ ] **Step 4: Remove the app shell glow overlay**

In `web/src/app/layout/app-shell.tsx`, replace the component with:

```tsx
import { Outlet } from "react-router-dom"

import { SidebarNav } from "@/app/layout/sidebar-nav"
import { Topbar } from "@/app/layout/topbar"

export function AppShell() {
  return (
    <div className="relative flex h-screen flex-col overflow-hidden bg-background text-foreground">
      <Topbar />
      <div className="relative flex min-h-0 flex-1 flex-col lg:flex-row">
        <SidebarNav />
        <main className="min-h-0 min-w-0 flex-1 overflow-y-auto" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run focused shell test**

Run:

```bash
cd web && bun run test -- src/app/layout/sidebar-nav.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit Task 1**

```bash
git add web/src/styles/globals.css web/src/app/layout/app-shell.tsx web/src/app/layout/sidebar-nav.test.tsx
git commit -m "style(web): reset manager shell tokens"
```

---

### Task 2: Editorial Topbar, Sidebar, And Shared Components

**Files:**
- Modify: `web/src/app/layout/topbar.tsx`
- Modify: `web/src/app/layout/sidebar-nav.tsx`
- Modify: `web/src/components/ui/button.tsx`
- Modify: `web/src/components/ui/card.tsx`
- Modify: `web/src/components/shell/page-container.tsx`
- Modify: `web/src/components/shell/page-header.tsx`
- Modify: `web/src/components/shell/section-card.tsx`
- Modify: `web/src/components/shell/status-metric-card.tsx`
- Modify: `web/src/components/manager/status-badge.tsx`
- Test: `web/src/app/layout/topbar.test.tsx`
- Test: `web/src/components/shell/shell-components.test.tsx`

- [ ] **Step 1: Update visual assertions for flat topbar and page header**

In `web/src/app/layout/topbar.test.tsx`, keep all existing accessible assertions and add:

```tsx
expect(screen.getByRole("banner")).toHaveClass("bg-background", "border-b")
expect(screen.getByRole("banner").querySelector("[data-brand-mark]")).toHaveClass("rounded-sm")
```

In `web/src/components/shell/shell-components.test.tsx`, update `page header renders a flat tool row`:

```tsx
const header = screen.getByRole("heading", { name: "Dashboard" }).closest("section")
expect(header).toHaveClass("border-b", "bg-background")
expect(header).not.toHaveClass("rounded-3xl")
expect(screen.getByText("Scope: single-node cluster").parentElement).toHaveClass("border-t")
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd web && bun run test -- src/app/layout/topbar.test.tsx src/components/shell/shell-components.test.tsx
```

Expected: FAIL because current topbar brand mark is rounded-xl/glowing and `PageHeader` is still a rounded shadow card.

- [ ] **Step 3: Flatten `Topbar`**

In `web/src/app/layout/topbar.tsx`, keep the same component logic and replace the JSX class structure with:

```tsx
return (
  <header
    className="sticky top-0 z-30 border-b border-border bg-background px-3 py-2 sm:px-4"
    role="banner"
  >
    <div className="flex min-h-10 items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3 xl:gap-5">
        <div className="flex shrink-0 items-center gap-2">
          <div
            aria-hidden
            className="size-7 rounded-sm border border-foreground bg-foreground dark:bg-primary"
            data-brand-mark
          />
          <div className="hidden sm:block">
            <div className="font-mono text-[12px] font-semibold tracking-[0.22em] text-foreground">WUKONGIM</div>
            <div className="text-[10px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
              {intl.formatMessage({ id: "shell.operationsCockpit" })}
            </div>
          </div>
        </div>
        <nav
          aria-label={intl.formatMessage({ id: "nav.topSections" })}
          className="flex min-w-0 items-center gap-1 overflow-x-auto border-l border-border pl-3"
        >
          {navigationSections.map((section) => {
            const active = section.id === activeSection.id
            return (
              <Link
                aria-current={active ? "page" : undefined}
                className={cn(
                  "shrink-0 rounded-full px-3 py-1.5 text-xs font-medium transition-colors sm:text-sm",
                  active
                    ? "top-section-link-active"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
                key={section.id}
                to={section.href}
              >
                {intl.formatMessage({ id: section.titleMessageId })}
              </Link>
            )
          })}
        </nav>
        <div className="hidden min-w-0 border-l border-border pl-4 xl:block">
          <div className="truncate text-sm font-medium text-foreground">
            {page ? intl.formatMessage({ id: page.titleMessageId }) : null}
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {page ? intl.formatMessage({ id: page.descriptionMessageId }) : null}
          </p>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2 sm:gap-3">
        <div className="hidden items-center gap-2 rounded-full border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground md:flex">
          <ShieldCheck className="size-3.5 text-success" />
          {intl.formatMessage({ id: "shell.singleNodeClusterHealthy" })}
        </div>
        <ThemeSwitcher />
        <LocaleSwitcher />
        <div className="flex items-center gap-2 border-l border-border pl-2 sm:pl-3">
          <span className="hidden text-xs text-muted-foreground sm:inline">{username}</span>
          <Button onClick={logout} size="sm" variant="outline">
            <Activity className="size-3.5" />
            {intl.formatMessage({ id: "common.logout" })}
          </Button>
        </div>
      </div>
    </div>
  </header>
)
```

- [ ] **Step 4: Flatten `SidebarNav`**

In `web/src/app/layout/sidebar-nav.tsx`, replace the returned nav markup with a rule-separated structure:

```tsx
return (
  <nav
    aria-label="Primary navigation"
    className="flex w-full shrink-0 flex-col border-b border-sidebar-border bg-sidebar px-3 py-3 lg:w-[244px] lg:border-b-0 lg:border-r lg:px-4 lg:py-5"
  >
    <div className="border-b border-sidebar-border pb-4">
      <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
        {intl.formatMessage({ id: activeSection.titleMessageId })}
      </div>
      <div className="mt-2 text-sm font-medium text-sidebar-foreground">WuKongIM</div>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">
        {intl.formatMessage({ id: "shell.runtimeConsoleDescription" })}
      </p>
    </div>

    <div className="mt-3 flex gap-1 overflow-x-auto pb-1 lg:mt-4 lg:flex-col lg:overflow-visible lg:pb-0">
      {activeSection.items.map((item) => (
        <NavLink
          key={item.href}
          aria-label={intl.formatMessage({ id: item.titleMessageId })}
          className={({ isActive }) =>
            cn(
              "flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors lg:w-full",
              isActive
                ? "bg-sidebar-primary text-sidebar-primary-foreground"
                : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
            )
          }
          to={item.href}
        >
          {({ isActive }) => (
            <>
              <item.icon
                aria-hidden
                className={cn("size-4 shrink-0", isActive ? "text-current" : "text-muted-foreground")}
              />
              <span className="font-medium">
                {intl.formatMessage({ id: item.titleMessageId })}
              </span>
            </>
          )}
        </NavLink>
      ))}
    </div>

    <div className="mt-3 border-t border-sidebar-border pt-4 lg:mt-auto">
      <div className="font-mono text-[10px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
        {intl.formatMessage({ id: "shell.clusterStatus" })}
      </div>
      <div className="mt-3 space-y-2 text-xs text-muted-foreground">
        <div className="flex items-center justify-between gap-2 border-b border-border pb-2">
          <span className="inline-flex items-center gap-2 text-sidebar-foreground">
            <span className="size-1.5 rounded-full bg-[var(--status-healthy)]" />
            {intl.formatMessage({ id: "shell.singleNodeCluster" })}
          </span>
          <span>{intl.formatMessage({ id: "shell.ready" })}</span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="inline-flex items-center gap-2 text-sidebar-foreground">
            <Cpu className="size-3.5" />
            {intl.formatMessage({ id: "shell.noLiveFeedYet" })}
          </span>
          <span>{intl.formatMessage({ id: "shell.static" })}</span>
        </div>
      </div>
    </div>
  </nav>
)
```

- [ ] **Step 5: Update shared component styles**

Apply these targeted replacements:

`web/src/components/ui/button.tsx`

```tsx
const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center rounded-full border border-transparent bg-clip-padding text-sm font-medium whitespace-nowrap transition-colors outline-none select-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/35 active:not-aria-[haspopup]:translate-y-px disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-2 aria-invalid:ring-destructive/20 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        outline:
          "border-border bg-background text-foreground hover:bg-muted aria-expanded:bg-muted aria-expanded:text-foreground",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-secondary/80 aria-expanded:bg-secondary aria-expanded:text-secondary-foreground",
        ghost:
          "border-transparent text-muted-foreground hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground",
        destructive:
          "border-destructive/25 bg-destructive/8 text-destructive hover:bg-destructive/12 focus-visible:border-destructive/40 focus-visible:ring-destructive/20",
        link: "h-auto rounded-none border-transparent px-0 text-primary underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 gap-1.5 px-4 has-data-[icon=inline-end]:pr-3 has-data-[icon=inline-start]:pl-3",
        xs: "h-6 gap-1 px-2 text-xs has-data-[icon=inline-end]:pr-1.5 has-data-[icon=inline-start]:pl-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 px-3 text-[0.8rem] has-data-[icon=inline-end]:pr-2 has-data-[icon=inline-start]:pl-2 [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 gap-1.5 px-5 has-data-[icon=inline-end]:pr-4 has-data-[icon=inline-start]:pl-4",
        icon: "size-8 p-0",
        "icon-xs": "size-6 p-0 [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-7 p-0",
        "icon-lg": "size-9 p-0",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)
```

`web/src/components/ui/card.tsx`

```tsx
"group/card flex flex-col gap-4 overflow-hidden rounded-lg border border-border bg-card py-4 text-sm text-card-foreground has-data-[slot=card-footer]:pb-0 has-[>img:first-child]:pt-0 data-[size=sm]:gap-3 data-[size=sm]:py-3 data-[size=sm]:has-data-[slot=card-footer]:pb-0 *:[img:first-child]:rounded-t-lg *:[img:last-child]:rounded-b-lg"
```

`web/src/components/shell/page-container.tsx`

```tsx
return (
  <div className={cn("mx-auto flex w-full max-w-[1560px] flex-col gap-4 px-4 py-5 sm:px-5 lg:px-7", className)}>
    {children}
  </div>
)
```

`web/src/components/shell/page-header.tsx`

```tsx
return (
  <section className={cn("border-b border-border bg-background pb-4", className)}>
    <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
      <div className="space-y-2">
        {eyebrow ? <HeaderEyebrow>{eyebrow}</HeaderEyebrow> : null}
        {!eyebrow && inRouter ? <RouteEyebrow /> : null}
        <h1 className="text-4xl font-normal leading-none tracking-normal text-foreground sm:text-5xl">{title}</h1>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
    {children ? <div className="mt-4 border-t border-border pt-4">{children}</div> : null}
  </section>
)
```

`web/src/components/shell/section-card.tsx`

```tsx
<Card id={id} className={cn("border border-border bg-card shadow-none", className)}>
  <CardHeader className="border-b border-border bg-background py-3">
    <CardTitle className="font-mono text-xs font-semibold uppercase tracking-[0.14em] text-foreground">{title}</CardTitle>
    {description ? <CardDescription className="leading-6">{description}</CardDescription> : null}
    {action ? <CardAction>{action}</CardAction> : null}
  </CardHeader>
  <CardContent className="pt-4">{children}</CardContent>
</Card>
```

`web/src/components/shell/status-metric-card.tsx`

```tsx
<SectionCard className="min-h-28" title={label}>
  <div className="flex items-start justify-between gap-3">
    <div className="font-mono text-3xl font-medium tabular-nums text-foreground">{value}</div>
    <StatusDot tone={tone} />
  </div>
  {description ? <p className="mt-2 text-xs text-muted-foreground">{description}</p> : null}
</SectionCard>
```

`web/src/components/manager/status-badge.tsx`

```tsx
<span
  className={cn(
    "inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium capitalize",
    variant === "success" && "border-success/25 bg-success/8 text-success",
    variant === "warning" && "border-warning/25 bg-warning/8 text-warning",
    variant === "danger" && "border-destructive/30 bg-destructive/8 text-destructive",
    variant === "neutral" && "border-border bg-background text-muted-foreground",
  )}
  data-variant={variant}
>
  {formatValue(value)}
</span>
```

- [ ] **Step 6: Run focused component tests**

Run:

```bash
cd web && bun run test -- src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/components/shell/shell-components.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.tsx web/src/components/ui/button.tsx web/src/components/ui/card.tsx web/src/components/shell/page-container.tsx web/src/components/shell/page-header.tsx web/src/components/shell/section-card.tsx web/src/components/shell/status-metric-card.tsx web/src/components/manager/status-badge.tsx web/src/app/layout/topbar.test.tsx web/src/components/shell/shell-components.test.tsx
git commit -m "style(web): flatten manager shell components"
```

---

### Task 3: Cluster Monitor Workbench Polish

**Files:**
- Modify: `web/src/pages/cluster-monitor/page.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`
- Modify: `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.tsx`
- Test: `web/src/pages/cluster-monitor/page.test.tsx`

- [ ] **Step 1: Add visual-role assertions to monitor tests**

In `web/src/pages/cluster-monitor/page.test.tsx`, add assertions to the main ready-state test so it checks for the new workbench surfaces without asserting brittle full class strings:

```tsx
expect(screen.getByRole("heading", { name: /live monitor/i }).closest("section")).toHaveClass("border-b")
expect(screen.getByLabelText(/category/i).closest("section")).toHaveAttribute("data-monitor-toolbar", "true")
expect(screen.getAllByTestId("cluster-monitor-snapshot-cell").length).toBeGreaterThan(0)
expect(screen.getAllByTestId("cluster-monitor-metric-card")[0]).toHaveClass("shadow-none")
```

- [ ] **Step 2: Run monitor test and verify it fails**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: FAIL because toolbar and snapshot cells do not yet expose the new test ids/attributes and metric cards still include the inset shadow class.

- [ ] **Step 3: Flatten toolbar**

In `web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx`, update the wrapping `section`:

```tsx
<section
  className="flex flex-col gap-3 border-b border-border bg-background pb-4 lg:flex-row lg:items-center lg:justify-between"
  data-monitor-toolbar="true"
>
```

Update the scope label class:

```tsx
className="rounded-full border border-border bg-muted px-2.5 py-1 text-xs font-medium text-foreground"
```

Update control wrappers from `rounded-lg bg-background` to `rounded-md bg-card`, keeping all labels and handlers unchanged.

- [ ] **Step 4: Flatten snapshot strip**

In `web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx`, update each entry wrapper:

```tsx
<div
  className="border-b border-border px-1 py-3 sm:px-3"
  data-testid="cluster-monitor-snapshot-cell"
  key={entry.key}
>
```

Use this value class:

```tsx
<span className="font-mono text-xl font-medium tabular-nums text-foreground">{entry.value}</span>
```

- [ ] **Step 5: Flatten metric cards and tune grid density**

In `web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx`:

```tsx
<section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
```

In `web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.tsx`, update the article class:

```tsx
className="flex min-h-[320px] flex-col rounded-lg border border-border bg-card p-4 shadow-none"
```

Update stat cell class:

```tsx
className="min-w-0 rounded-md border border-border bg-background px-2 py-2"
```

Keep chart height at `h-[120px]` to prevent regressions.

- [ ] **Step 6: Run monitor test**

Run:

```bash
cd web && bun run test -- src/pages/cluster-monitor/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 3**

```bash
git add web/src/pages/cluster-monitor/page.tsx web/src/pages/cluster-monitor/components/cluster-monitor-toolbar.tsx web/src/pages/cluster-monitor/components/cluster-monitor-snapshot-strip.tsx web/src/pages/cluster-monitor/components/cluster-monitor-card-grid.tsx web/src/pages/cluster-monitor/components/cluster-monitor-metric-card.tsx web/src/pages/cluster-monitor/page.test.tsx
git commit -m "style(web): polish cluster monitor workbench"
```

---

### Task 4: Nodes List Editorial Density

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Test: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Add a summary strip assertion**

In `web/src/pages/nodes/page.test.tsx`, update the ready-state/list test to assert the new summary strip and table role:

```tsx
expect(screen.getByTestId("nodes-summary-strip")).toBeInTheDocument()
expect(screen.getByRole("table", { name: /nodes/i })).toHaveClass("w-full")
```

- [ ] **Step 2: Run node tests and verify failure**

Run:

```bash
cd web && bun run test -- src/pages/nodes/page.test.tsx
```

Expected: FAIL because there is not yet a `nodes-summary-strip` test id and the table does not yet have an accessible name.

- [ ] **Step 3: Add focused node summary helpers**

In `web/src/pages/nodes/page.tsx`, near existing summary helpers, add:

```tsx
function NodeSummaryCell({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="border-b border-border px-1 py-3 sm:px-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-2xl font-medium tabular-nums text-foreground">{value}</div>
    </div>
  )
}
```

- [ ] **Step 4: Flatten the list panel header and table**

Inside `NodeClusterListPanel`, replace the table container block with:

```tsx
<div className="border border-border bg-card">
  {state.nodes.items.length > 0 ? (
    <div className="overflow-x-auto">
      <table aria-label={intl.formatMessage({ id: "nav.nodes.title" })} className="w-full border-collapse">
```

Update table header class:

```tsx
className="border-b border-border bg-background text-left text-xs uppercase tracking-[0.14em] text-muted-foreground"
```

Update row class:

```tsx
className="border-t border-border align-top hover:bg-muted/45"
```

Keep all action buttons, permission checks, dialog state, and API calls unchanged.

- [ ] **Step 5: Flatten `NodeClusterTabContent` summary**

In `NodeClusterTabContent`, replace the `SectionCard` metric grid with:

```tsx
<div
  className="grid gap-0 border-y border-border sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
  data-testid="nodes-summary-strip"
>
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.total" })} value={nodes.length} />
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.alive" })} value={aliveCount} />
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.unhealthy" })} value={unhealthyCount} />
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.draining" })} value={drainingCount} />
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.schedulable" })} value={schedulableCount} />
  <NodeSummaryCell label={intl.formatMessage({ id: "nodes.metric.controllerVoters" })} value={controllerVoterCount} />
</div>
```

Leave unhealthy summary and runtime overview sections present, but rely on the new flattened `SectionCard` styling from Task 2.

- [ ] **Step 6: Run node tests**

Run:

```bash
cd web && bun run test -- src/pages/nodes/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx
git commit -m "style(web): refine node list density"
```

---

### Task 5: Plugins Inventory And Binding Surfaces

**Files:**
- Modify: `web/src/pages/plugins/page.tsx`
- Test: `web/src/pages/plugins/page.test.tsx`

- [ ] **Step 1: Add inventory/bindings surface assertions**

In `web/src/pages/plugins/page.test.tsx`, update the ready-state test:

```tsx
expect(screen.getByTestId("plugins-summary-strip")).toBeInTheDocument()
expect(screen.getByRole("table", { name: /plugin inventory/i })).toHaveClass("w-full")
expect(screen.getByRole("form", { name: /plugin bindings/i })).toBeInTheDocument()
```

- [ ] **Step 2: Run plugin tests and verify failure**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx
```

Expected: FAIL because the summary strip test id, inventory table accessible name, and bindings form accessible name are not present yet.

- [ ] **Step 3: Replace summary pills with compact cells**

In `web/src/pages/plugins/page.tsx`, replace the summary grid after `PageHeader`:

```tsx
<div
  className="grid gap-0 border-y border-border sm:grid-cols-2 xl:grid-cols-4"
  data-testid="plugins-summary-strip"
>
  <SummaryPill label={intl.formatMessage({ id: "plugins.totalValue" }, { count: summary.total })} />
  <SummaryPill label={intl.formatMessage({ id: "plugins.runningValue" }, { count: summary.running })} />
  <SummaryPill label={intl.formatMessage({ id: "plugins.failedValue" }, { count: summary.failed })} />
  <SummaryPill label={intl.formatMessage({ id: "plugins.enabledValue" }, { count: summary.enabled })} />
</div>
```

Update `SummaryPill` to:

```tsx
function SummaryPill({ label }: { label: string }) {
  return (
    <div className="border-b border-border px-1 py-3 text-sm text-foreground sm:px-3">
      {label}
    </div>
  )
}
```

- [ ] **Step 4: Add table and form accessible names**

Inventory table:

```tsx
<table
  aria-label={intl.formatMessage({ id: "plugins.inventory.title" })}
  className="w-full border-collapse"
>
```

Bindings form:

```tsx
<form
  aria-label={intl.formatMessage({ id: "plugins.bindings.title" })}
  className="flex flex-col gap-3 lg:flex-row lg:items-end"
  onSubmit={(event) => {
    void submitBindingSearch(event)
  }}
>
```

- [ ] **Step 5: Flatten table surfaces**

For both plugin tables, replace outer table container classes:

```tsx
className="overflow-x-auto border border-border"
```

Replace header class:

```tsx
className="border-b border-border bg-background text-left text-xs uppercase tracking-[0.14em] text-muted-foreground"
```

Replace row class:

```tsx
className="border-t border-border align-top hover:bg-muted/45"
```

Keep all action handlers, dialog state, config parsing, and API calls unchanged.

- [ ] **Step 6: Run plugin tests**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Task 5**

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx
git commit -m "style(web): separate plugin inventory surfaces"
```

---

### Task 6: Login Page Brand Panel

**Files:**
- Modify: `web/src/pages/login/page.tsx`
- Test: `web/src/pages/login/page.test.tsx`

- [ ] **Step 1: Add login visual assertions**

In `web/src/pages/login/page.test.tsx`, update `renders the cockpit brand and single-node cluster promise`:

```tsx
expect(screen.getByTestId("login-brand-band")).toHaveClass("bg-[var(--primary)]")
expect(screen.getByTestId("login-form-card")).toHaveClass("rounded-[22px]")
expect(document.querySelector("[class*='radial-gradient']")).not.toBeInTheDocument()
```

- [ ] **Step 2: Run login test and verify failure**

Run:

```bash
cd web && bun run test -- src/pages/login/page.test.tsx
```

Expected: FAIL because current login page has the decorative glow overlay and does not expose the new test ids.

- [ ] **Step 3: Restyle login wrapper and brand area**

In `web/src/pages/login/page.tsx`, remove the fixed radial overlay and update the main layout:

```tsx
<main className="min-h-screen overflow-hidden bg-background px-5 py-8 sm:px-6 lg:py-12">
  <div className="relative mx-auto grid min-h-[calc(100vh-4rem)] w-full max-w-6xl items-center gap-8 lg:grid-cols-[1.08fr_0.92fr]">
    <section className="max-w-2xl">
```

Update the brand mark:

```tsx
<div
  aria-hidden
  className="size-9 rounded-sm border border-foreground bg-foreground"
/>
```

Add a compact brand band after the proof chips:

```tsx
<div
  className="mt-8 rounded-[22px] bg-[var(--primary)] p-6 text-primary-foreground"
  data-testid="login-brand-band"
>
  <div className="font-mono text-[11px] uppercase tracking-[0.22em] opacity-80">
    {intl.formatMessage({ id: "auth.operationsCockpit" })}
  </div>
  <div className="mt-3 text-2xl font-normal leading-tight">
    {intl.formatMessage({ id: "auth.singleNodeClusterReady" })}
  </div>
</div>
```

- [ ] **Step 4: Restyle form card and inputs**

Update the form section:

```tsx
<section
  className="w-full rounded-[22px] border border-border bg-card p-6 text-card-foreground shadow-none sm:p-8"
  data-testid="login-form-card"
>
```

Update both inputs:

```tsx
className="w-full rounded-md border border-input bg-background px-3 py-2.5 text-sm text-foreground outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/30"
```

Update error block:

```tsx
className="rounded-md border border-destructive/25 bg-destructive/8 px-3 py-2 text-sm text-destructive"
```

- [ ] **Step 5: Run login tests**

Run:

```bash
cd web && bun run test -- src/pages/login/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

```bash
git add web/src/pages/login/page.tsx web/src/pages/login/page.test.tsx
git commit -m "style(web): restyle manager login"
```

---

### Task 7: Final Verification And Build Cleanup

**Files:**
- Modify only if verification exposes source issues.
- Inspect generated build output before final status.

- [ ] **Step 1: Run focused layout and shell tests**

Run:

```bash
cd web && bun run test -- src/app/layout/sidebar-nav.test.tsx src/app/layout/topbar.test.tsx src/components/shell/shell-components.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run focused page tests**

Run:

```bash
cd web && bun run test -- src/pages/login/page.test.tsx src/pages/cluster-monitor/page.test.tsx src/pages/nodes/page.test.tsx src/pages/plugins/page.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run TypeScript check**

Run:

```bash
cd web && bunx tsc -b
```

Expected: exits 0 with no TypeScript errors.

- [ ] **Step 4: Run production build**

Run:

```bash
cd web && bun run build
```

Expected: exits 0 and produces a Vite build.

- [ ] **Step 5: Check whitespace and generated output**

Run:

```bash
git diff --check
git status --short
```

Expected: `git diff --check` exits 0. If `web/dist/index.html` or other generated hash-only build output changed and the implementation did not intentionally update checked-in build artifacts, restore those generated files with a non-destructive targeted checkout:

```bash
git restore web/dist/index.html
```

Do not restore unrelated user files.

- [ ] **Step 6: Commit verification fixes or final source state**

If Step 1-5 required source fixes, commit them:

```bash
git add web/src web/package.json web/bun.lock
git commit -m "style(web): complete editorial console verification"
```

If all source changes were already committed in earlier tasks and only generated output was restored, do not create an empty commit.

---

## Self-Review

Spec coverage:

- Global visual tokens and flat shell are covered by Tasks 1-2.
- Shared UI/shell components are covered by Task 2.
- Cluster monitor workbench is covered by Task 3.
- Nodes list density is covered by Task 4.
- Plugins inventory and bindings separation is covered by Task 5.
- Login brand panel is covered by Task 6.
- Focused tests, TypeScript, build, and diff cleanup are covered by Task 7.

Placeholder scan:

- No blocked marker terms or unspecified "add tests" steps are used.
- Every task includes exact files, concrete edits, exact commands, and expected results.

Type and behavior consistency:

- The plan preserves current component names, route behavior, API functions, auth store usage, i18n ids, and page-level data flow.
- New test ids are limited to visual/structural landmarks: `nodes-summary-strip`, `plugins-summary-strip`, `login-brand-band`, `login-form-card`, and `cluster-monitor-snapshot-cell`.
