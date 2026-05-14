# Web Admin Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the WuKongIM web manager into a 4-section top-navigation shell with filtered side navigation, consolidated operation pages, and an engineering-monitor visual system.

**Architecture:** Keep the existing React/Vite app in `web/` and do this as a frontend-only route/layout refactor. Centralize the new information architecture in `web/src/lib/navigation.ts`, keep `AppShell` as the single authenticated layout, consolidate old route-level pages behind tabbed cluster pages, and preserve old URLs with `<Navigate replace>` redirects. Do not change manager APIs or introduce any deployment branch that bypasses cluster semantics; continue to describe standalone installs as single-node clusters.

**Tech Stack:** React 19, React Router DOM 7, TypeScript, Tailwind CSS 4, shadcn/Radix primitives already in the app, lucide-react, zustand auth store, react-intl, Vitest, Testing Library, Bun/Vite, `@fontsource-variable/geist`, new `@fontsource-variable/geist-mono`.

---

## References

- Source spec: `docs/superpowers/specs/2026-05-14-web-admin-redesign.md`
- Current shell: `web/src/app/layout/app-shell.tsx`, `web/src/app/layout/topbar.tsx`, `web/src/app/layout/sidebar-nav.tsx`
- Current route tree: `web/src/app/router.tsx`
- Current navigation metadata: `web/src/lib/navigation.ts`
- Current visual tokens: `web/src/styles/globals.css`
- Current i18n copy: `web/src/i18n/messages/en.ts`, `web/src/i18n/messages/zh-CN.ts`
- Existing tests to update first: `web/src/app/router.test.tsx`, `web/src/app/layout/sidebar-nav.test.tsx`, `web/src/app/layout/topbar.test.tsx`, `web/src/pages/page-shells.test.tsx`
- Existing consolidated-source pages: `web/src/pages/channel-cluster/page.tsx`, `web/src/pages/channel-cluster/unhealthy/page.tsx`, `web/src/pages/channels/page.tsx`, `web/src/pages/diagnostics/page.tsx`, `web/src/pages/network/page.tsx`, `web/src/pages/controller/page.tsx`, `web/src/pages/slot-logs/page.tsx`, `web/src/pages/onboarding/page.tsx`
- Follow `@superpowers:test-driven-development` for each implementation slice.
- Run `@superpowers:verification-before-completion` before claiming implementation complete.
- Re-check for `FLOW.md` before editing packages. At plan time, no `FLOW.md` existed under `web/`.

## Scope And Non-Goals

In scope:

- Replace the 19-item flat sidebar with 4 top sections: overview, cluster operations, business management, system.
- Side navigation displays only the active top section items.
- Move old routes to the new route design and add legacy redirects.
- Merge onboarding into Nodes, channel-cluster subpages into Channel Cluster tabs, and diagnostics/network/log pages into Diagnostics tabs.
- Apply the dark engineering-monitor visual language, Geist Mono numeric styling, status dots, compact cards, and uppercase monospace page paths.
- Keep i18n coverage for English and Simplified Chinese.

Out of scope:

- New backend APIs.
- Reworking page business logic beyond extraction needed for tab consolidation.
- Webhook write APIs or monitor real-time streaming if they are still placeholders.
- Full e2e/integration test runs; use focused frontend tests and build for this refactor.

## File Structure

Navigation and routing:

- Modify: `web/src/lib/navigation.ts` — replace six grouped sidebar sections with four top-level navigation sections, item metadata, legacy redirect map, and route matching helpers.
- Create: `web/src/lib/navigation.test.ts` — unit tests for section selection, page metadata, path breadcrumbs, and legacy redirect targets.
- Modify: `web/src/app/router.tsx` — add new route paths and redirect old paths with query-string-preserving helpers.
- Modify: `web/src/app/router.test.tsx` — authenticated redirect tests for new routes and legacy URLs.

Shell and styling:

- Modify: `web/package.json` and `web/bun.lock` — add `@fontsource-variable/geist-mono`.
- Modify: `web/src/styles/globals.css` — engineering-monitor color tokens, Geist Mono import/theme token, background treatment, status semantic variables.
- Modify: `web/src/app/layout/app-shell.tsx` — 48px top bar above a sidebar/content row.
- Modify: `web/src/app/layout/topbar.tsx` — brand, top section tabs, locale switcher, refresh/search actions, logged-in admin controls.
- Modify: `web/src/app/layout/sidebar-nav.tsx` — active-section-only side navigation, 200px width, active green left rail.
- Modify: `web/src/app/layout/topbar.test.tsx` — top tab, active state, user actions, i18n tests.
- Modify: `web/src/app/layout/sidebar-nav.test.tsx` — filtered side nav and active item tests.
- Modify: `web/src/components/shell/page-header.tsx` — uppercase monospace path eyebrow styling.
- Modify: `web/src/components/shell/page-container.tsx` — tighter max width/padding for dense dashboards.
- Modify: `web/src/components/shell/section-card.tsx` — 1px border, no shadow, compact dark-card style.
- Create: `web/src/components/shell/status-dot.tsx` — 6px semantic status indicator.
- Create: `web/src/components/shell/status-metric-card.tsx` — reusable metric card with status dot and Geist Mono value.
- Create: `web/src/components/shell/page-tabs.tsx` — accessible tab strip backed by search params for consolidated pages.
- Modify: `web/src/components/shell/shell-components.test.tsx` — shell component style/semantics coverage.

Page consolidation:

- Modify: `web/src/pages/nodes/page.tsx` — add onboarding operation panel and support `?panel=onboarding`.
- Modify: `web/src/pages/nodes/page.test.tsx` — onboarding panel visibility and permission behavior.
- Modify: `web/src/pages/onboarding/page.tsx` — extract `NodeOnboardingPanel` and keep a compatibility wrapper only if still imported by tests during the refactor.
- Create: `web/src/pages/cluster/channels/page.tsx` — Channel Cluster page with `overview`, `list`, `unhealthy` tabs.
- Create: `web/src/pages/cluster/channels/page.test.tsx` — tab rendering, default tab, legacy tab redirect expectations.
- Modify: `web/src/pages/channel-cluster/page.tsx` — export overview panel without its own route header/container.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx` — export unhealthy panel without its own route header/container.
- Modify: `web/src/pages/channels/page.tsx` — export runtime list panel for cluster-channel tab reuse.
- Create: `web/src/pages/cluster/diagnostics/page.tsx` — Diagnostics page with `trace`, `network`, `controller-logs`, `slot-logs` tabs.
- Create: `web/src/pages/cluster/diagnostics/page.test.tsx` — tab rendering and query preservation for log views.
- Modify: `web/src/pages/diagnostics/page.tsx` — export trace panel.
- Modify: `web/src/pages/network/page.tsx` — export network panel.
- Modify: `web/src/pages/controller/page.tsx` — export controller-log panel.
- Modify: `web/src/pages/slot-logs/page.tsx` — export slot-log panel.
- Modify: `web/src/pages/page-shells.test.tsx` — new route labels and consolidated pages.

Copy and docs:

- Modify: `web/src/i18n/messages/en.ts` — section labels, path labels, tab labels, consolidated page copy.
- Modify: `web/src/i18n/messages/zh-CN.ts` — matching Simplified Chinese copy.
- Modify: `web/README.md` — update page/API matrix to new route paths and note legacy redirects.
- Modify only if needed: `AGENTS.md` — update directory tree if implementation creates a meaningful new top-level directory. New `web/src/pages/cluster/*` does not require this.

## Route Mapping

New routes:

| Route | Section | Side item | Page/tab behavior |
| --- | --- | --- | --- |
| `/` | overview | dashboard | Redirect to `/dashboard` |
| `/dashboard` | overview | dashboard | Existing dashboard |
| `/monitor` | overview | monitor | Existing monitor |
| `/cluster/nodes` | cluster | nodes | Existing nodes plus onboarding panel |
| `/cluster/slots` | cluster | slots | Existing slots |
| `/cluster/channels` | cluster | channel cluster | Tabs: `overview`, `list`, `unhealthy` |
| `/cluster/tasks` | cluster | tasks | Existing tasks |
| `/cluster/topology` | cluster | topology | Existing topology |
| `/cluster/diagnostics` | cluster | diagnostics | Tabs: `trace`, `network`, `controller-logs`, `slot-logs` |
| `/business/users` | business | users | Existing users |
| `/business/channels` | business | channels | Existing channels-biz |
| `/business/messages` | business | messages | Existing messages |
| `/business/system-users` | business | system users | Existing system-users |
| `/system/permissions` | system | permissions | Existing permissions |
| `/system/webhooks` | system | webhooks | Existing webhooks |
| `/system/connections` | system | connections | Existing connections |

Legacy redirects:

| Old route | Redirect target |
| --- | --- |
| `/nodes` | `/cluster/nodes` |
| `/onboarding` | `/cluster/nodes?panel=onboarding` |
| `/slots` | `/cluster/slots` |
| `/tasks` | `/cluster/tasks` |
| `/topology` | `/cluster/topology` |
| `/channel-cluster` | `/cluster/channels?tab=overview` |
| `/channel-cluster/list` | `/cluster/channels?tab=list` |
| `/channel-cluster/unhealthy` | `/cluster/channels?tab=unhealthy` |
| `/channels` | `/cluster/channels?tab=list` |
| `/diagnostics` | `/cluster/diagnostics?tab=trace` |
| `/network` | `/cluster/diagnostics?tab=network` |
| `/controller` | `/cluster/diagnostics?tab=controller-logs` and preserve existing search params such as `node_id` |
| `/slot-logs` | `/cluster/diagnostics?tab=slot-logs` |
| `/users` | `/business/users` |
| `/channels-biz` | `/business/channels` |
| `/messages` | `/business/messages` |
| `/system-users` | `/business/system-users` |
| `/settings/permissions` | `/system/permissions` |
| `/settings/webhooks` | `/system/webhooks` |
| `/connections` | `/system/connections` |

## Task 1: Replace Navigation Metadata With Four Top-Level Sections

**Files:**
- Modify: `web/src/lib/navigation.ts`
- Create: `web/src/lib/navigation.test.ts`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing navigation metadata tests**

Add `web/src/lib/navigation.test.ts` with tests like:

```ts
import { describe, expect, test } from "vitest"

import {
  getActiveNavigationItem,
  getActiveNavigationSection,
  legacyRouteRedirects,
  navigationSections,
  pageMetadata,
} from "@/lib/navigation"

describe("navigationSections", () => {
  test("defines the four redesigned top-level sections", () => {
    expect(navigationSections.map((section) => section.id)).toEqual([
      "overview",
      "cluster",
      "business",
      "system",
    ])
  })

  test("selects the active section from new nested routes", () => {
    expect(getActiveNavigationSection("/cluster/nodes")?.id).toBe("cluster")
    expect(getActiveNavigationSection("/business/messages")?.id).toBe("business")
    expect(getActiveNavigationSection("/system/connections")?.id).toBe("system")
  })

  test("exposes metadata and path label message ids for page headers", () => {
    expect(pageMetadata.get("/cluster/nodes")?.titleMessageId).toBe("nav.nodes.title")
    expect(getActiveNavigationItem("/cluster/nodes")?.pathLabelMessageId).toBe("nav.path.cluster.nodes")
  })

  test("maps legacy routes to new routes", () => {
    expect(legacyRouteRedirects["/channel-cluster/list"]).toBe("/cluster/channels?tab=list")
    expect(legacyRouteRedirects["/connections"]).toBe("/system/connections")
  })
})
```

- [ ] **Step 2: Run the metadata tests to verify RED**

Run:

```bash
cd web && bun run test -- src/lib/navigation.test.ts
```

Expected: FAIL because the new exports do not exist and `navigationGroups` still has six sidebar groups.

- [ ] **Step 3: Implement section metadata**

In `web/src/lib/navigation.ts`, replace the six sidebar groups with this shape:

```ts
export type NavigationSectionId = "overview" | "cluster" | "business" | "system"

export type NavigationItem = {
  href: string
  titleMessageId: string
  descriptionMessageId: string
  pathLabelMessageId: string
  icon: LucideIcon
  aliases?: string[]
}

export type NavigationSection = {
  id: NavigationSectionId
  href: string
  titleMessageId: string
  items: NavigationItem[]
}
```

Use the spec grouping exactly:

- `overview`: `/dashboard`, `/monitor`
- `cluster`: `/cluster/nodes`, `/cluster/slots`, `/cluster/channels`, `/cluster/tasks`, `/cluster/topology`, `/cluster/diagnostics`
- `business`: `/business/users`, `/business/channels`, `/business/messages`, `/business/system-users`
- `system`: `/system/permissions`, `/system/webhooks`, `/system/connections`

Add helpers:

```ts
export const navigationItems = navigationSections.flatMap((section) => section.items)

export const pageMetadata = new Map(
  navigationItems.map((item) => [item.href, item] as const),
)

export function getActiveNavigationSection(pathname: string) {
  return navigationSections.find((section) =>
    section.items.some((item) => pathname === item.href || pathname.startsWith(`${item.href}/`) || item.aliases?.includes(pathname)),
  ) ?? navigationSections[0]
}

export function getActiveNavigationItem(pathname: string) {
  return navigationItems.find((item) => pathname === item.href || pathname.startsWith(`${item.href}/`) || item.aliases?.includes(pathname))
    ?? pageMetadata.get("/dashboard")
}
```

Add `legacyRouteRedirects` as a `Record<string, string>` for routes that do not need search preservation. Use a separate helper in the router for `/controller` so `node_id` survives.

- [ ] **Step 4: Add i18n keys for new section and path labels**

Add English and Chinese message IDs:

```ts
"nav.section.overview": "Overview",
"nav.section.cluster": "Cluster Ops",
"nav.section.business": "Business",
"nav.section.system": "System",
"nav.path.overview.dashboard": "OVERVIEW / DASHBOARD",
"nav.path.cluster.nodes": "CLUSTER / NODES",
```

Add matching keys for every new route. Keep old page title/description IDs where possible so page-level copy stays stable.

- [ ] **Step 5: Re-run metadata tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/lib/navigation.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/navigation.ts web/src/lib/navigation.test.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): model redesigned admin navigation"
```

## Task 2: Add New Routes And Legacy Redirects

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`
- Later tasks will create `web/src/pages/cluster/channels/page.tsx` and `web/src/pages/cluster/diagnostics/page.tsx`; for this task, use temporary imports only if the compiler needs them.

- [ ] **Step 1: Write failing router tests for new paths**

Extend `web/src/app/router.test.tsx`:

```ts
test("renders the shell for redesigned cluster routes", async () => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: ["/cluster/nodes"] })

  render(<AppProviders><RouterProvider router={router} /></AppProviders>)

  expect(await screen.findByLabelText("Primary navigation")).toBeInTheDocument()
  expect(screen.getByRole("main")).toBeInTheDocument()
})

test.each([
  ["/nodes", "/cluster/nodes"],
  ["/channel-cluster/unhealthy", "/cluster/channels?tab=unhealthy"],
  ["/network", "/cluster/diagnostics?tab=network"],
  ["/connections", "/system/connections"],
])("redirects legacy %s to %s", async (from, to) => {
  useAuthStore.setState(authenticatedState())
  const router = createMemoryRouter(routes, { initialEntries: [from] })

  render(<AppProviders><RouterProvider router={router} /></AppProviders>)

  await screen.findByRole("main")
  expect(router.state.location.pathname + router.state.location.search).toBe(to)
})
```

Add a helper in the test file to avoid duplicating the authenticated state.

- [ ] **Step 2: Run router tests to verify RED**

Run:

```bash
cd web && bun run test -- src/app/router.test.tsx
```

Expected: FAIL because new routes and redirects do not exist.

- [ ] **Step 3: Update the route tree**

In `web/src/app/router.tsx`:

- Import new cluster wrappers once created; until Task 6/7, temporarily map `/cluster/channels` to `ChannelClusterPage` and `/cluster/diagnostics` to `DiagnosticsPage` to keep the tree compiling.
- Add the new paths under the existing protected root.
- Replace old concrete page routes with redirects.
- Keep `/login` public behavior unchanged.

Use a small redirect component to preserve search params for log routes:

```tsx
function RedirectWithSearch({ to, tab }: { to: string; tab?: string }) {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  if (tab && !params.has("tab")) {
    params.set("tab", tab)
  }
  const search = params.toString()
  return <Navigate replace to={`${to}${search ? `?${search}` : ""}`} />
}
```

Use it for `/controller` and `/slot-logs`; simple `<Navigate replace>` is enough for paths with no meaningful search params.

- [ ] **Step 4: Re-run router tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/app/router.test.tsx
```

Expected: PASS or only failures caused by page consolidation tests that will be addressed in later tasks.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/router.tsx web/src/app/router.test.tsx
git commit -m "refactor(web): route admin pages under redesigned sections"
```

## Task 3: Build The Top Navigation Shell

**Files:**
- Modify: `web/src/app/layout/app-shell.tsx`
- Modify: `web/src/app/layout/topbar.tsx`
- Modify: `web/src/app/layout/sidebar-nav.tsx`
- Modify: `web/src/app/layout/topbar.test.tsx`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx`

- [ ] **Step 1: Write failing topbar tests**

Update `web/src/app/layout/topbar.test.tsx` to assert:

- the banner contains `WUKONGIM`, four top tabs, locale switcher, username, logout;
- the active section tab has `aria-current="page"` on `/cluster/nodes`;
- Chinese locale translates top tabs and actions.

Example:

```ts
expect(within(screen.getByRole("banner")).getByRole("link", { name: "Cluster Ops" })).toHaveAttribute("aria-current", "page")
expect(within(screen.getByRole("banner")).getByText("admin")).toBeInTheDocument()
```

- [ ] **Step 2: Write failing sidebar tests**

Update `web/src/app/layout/sidebar-nav.test.tsx` to assert:

```ts
expect(await screen.findByRole("link", { name: "Nodes" })).toBeInTheDocument()
expect(screen.getByRole("link", { name: "Slots" })).toBeInTheDocument()
expect(screen.queryByRole("link", { name: "Users" })).not.toBeInTheDocument()
expect(screen.getByRole("link", { name: "Nodes" })).toHaveAttribute("aria-current", "page")
```

Keep the single-node cluster context assertion because the project wording requires that term.

- [ ] **Step 3: Run shell layout tests to verify RED**

Run:

```bash
cd web && bun run test -- src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx
```

Expected: FAIL because `Topbar` is not a top navigation bar and `SidebarNav` still renders all groups.

- [ ] **Step 4: Update `AppShell` layout**

Change `web/src/app/layout/app-shell.tsx` from left-sidebar-first to topbar-first:

```tsx
export function AppShell() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Topbar />
      <div className="flex min-h-[calc(100vh-3rem)]">
        <SidebarNav />
        <main className="min-w-0 flex-1" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Implement top section tabs in `Topbar`**

Use `navigationSections`, `getActiveNavigationSection`, `useLocation`, `NavLink`, `LocaleSwitcher`, and `useAuthStore`. Keep the banner height near 48px:

```tsx
<nav aria-label={intl.formatMessage({ id: "nav.topSections" })} className="flex items-center gap-1">
  {navigationSections.map((section) => {
    const active = section.id === activeSection.id
    return (
      <NavLink
        aria-current={active ? "page" : undefined}
        className={cn("rounded-md px-3 py-1.5 text-sm font-medium", active ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground")}
        key={section.id}
        to={section.href}
      >
        {intl.formatMessage({ id: section.titleMessageId })}
      </NavLink>
    )
  })}
</nav>
```

Keep refresh/search buttons visually available; they can remain non-wired as before.

- [ ] **Step 6: Implement active-section-only sidebar**

In `SidebarNav`, derive active section from `useLocation()` and render only `activeSection.items`. Set width to `w-[200px]`, active item to dark background plus `border-l-2 border-l-[var(--status-healthy)]`, and keep the cluster status card at the bottom.

- [ ] **Step 7: Re-run shell layout tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/app/layout/app-shell.tsx web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.tsx web/src/app/layout/topbar.test.tsx web/src/app/layout/sidebar-nav.test.tsx
git commit -m "refactor(web): add top-section admin shell"
```

## Task 4: Apply Engineering-Monitor Visual Tokens And Reusable Shell Components

**Files:**
- Modify: `web/package.json`
- Modify: `web/bun.lock`
- Modify: `web/src/styles/globals.css`
- Modify: `web/src/components/shell/page-header.tsx`
- Modify: `web/src/components/shell/page-container.tsx`
- Modify: `web/src/components/shell/section-card.tsx`
- Create: `web/src/components/shell/status-dot.tsx`
- Create: `web/src/components/shell/status-metric-card.tsx`
- Create: `web/src/components/shell/page-tabs.tsx`
- Modify: `web/src/components/shell/shell-components.test.tsx`

- [ ] **Step 1: Install Geist Mono**

Run:

```bash
cd web && bun add @fontsource-variable/geist-mono
```

Expected: `package.json` and `bun.lock` include `@fontsource-variable/geist-mono`.

- [ ] **Step 2: Write failing shell component tests**

Extend `web/src/components/shell/shell-components.test.tsx`:

```tsx
render(<StatusDot tone="healthy" />)
expect(screen.getByTestId("status-dot")).toHaveAttribute("data-tone", "healthy")

render(<StatusMetricCard label="NODES" value="3" tone="healthy" />)
expect(screen.getByText("3")).toHaveClass("font-mono")

render(<PageTabs tabs={[{ id: "overview", label: "Overview" }]} activeTab="overview" onTabChange={() => {}} />)
expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
```

- [ ] **Step 3: Run shell component tests to verify RED**

Run:

```bash
cd web && bun run test -- src/components/shell/shell-components.test.tsx
```

Expected: FAIL because the new components do not exist.

- [ ] **Step 4: Update CSS tokens**

In `web/src/styles/globals.css`:

- import `@fontsource-variable/geist-mono`;
- set root tokens to the spec colors (`#09090b`, `#0a0a0a`, `#1a1a1a`, `#fafafa`, `#a1a1aa`, `#525252`);
- add semantic status variables (`--status-healthy`, `--status-running`, `--status-warning`, `--status-error`);
- add `--font-mono: "Geist Mono Variable", "SFMono-Regular", Consolas, monospace` in `@theme inline`;
- give `body` a subtle engineering background using radial/linear gradients without reducing readability.

Keep the existing shadcn variable names so current components continue to work.

- [ ] **Step 5: Implement reusable shell components**

`StatusDot`:

```tsx
export type StatusTone = "healthy" | "running" | "warning" | "error" | "muted"

export function StatusDot({ tone = "muted" }: { tone?: StatusTone }) {
  return <span aria-hidden className={cn("size-1.5 rounded-full", toneClass[tone])} data-testid="status-dot" data-tone={tone} />
}
```

`StatusMetricCard`:

```tsx
export function StatusMetricCard({ label, value, tone, description }: Props) {
  return (
    <SectionCard className="min-h-32" title={label}>
      <div className="flex items-start justify-between gap-3">
        <div className="font-mono text-4xl font-semibold tabular-nums text-foreground">{value}</div>
        <StatusDot tone={tone} />
      </div>
      {description ? <p className="mt-2 text-xs text-muted-foreground">{description}</p> : null}
    </SectionCard>
  )
}
```

`PageTabs` should render `role="tablist"` and `button role="tab"` elements; callers own search-param updates so the component stays reusable.

- [ ] **Step 6: Update shell wrappers**

- `PageHeader`: make `eyebrow` uppercase monospace and use no shadow.
- `PageContainer`: use dense spacing (`gap-4 px-5 py-5`) and `max-w-[1440px]`.
- `SectionCard`: compact 1px-border card with `bg-card`, no shadows, monospace-friendly table headers.

- [ ] **Step 7: Re-run tests and build CSS/compiler**

Run:

```bash
cd web && bun run test -- src/components/shell/shell-components.test.tsx
cd web && bun run build
```

Expected: component tests PASS and build PASS.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/bun.lock web/src/styles/globals.css web/src/components/shell
git commit -m "style(web): apply engineering monitor shell system"
```

## Task 5: Show Monospace Path Headers Across Pages

**Files:**
- Modify: `web/src/components/shell/page-header.tsx`
- Modify: every page that renders `PageHeader` under `web/src/pages/**/page.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write failing page-shell path tests**

Update `web/src/pages/page-shells.test.tsx` to render representative new routes and assert path labels:

```ts
expect(await screen.findByText("CLUSTER / NODES")).toBeInTheDocument()
expect(await screen.findByText("BUSINESS / MESSAGES")).toBeInTheDocument()
expect(await screen.findByText("SYSTEM / CONNECTIONS")).toBeInTheDocument()
```

- [ ] **Step 2: Run page shell tests to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/page-shells.test.tsx
```

Expected: FAIL because most `PageHeader` calls do not pass the new path eyebrow.

- [ ] **Step 3: Add a path-label helper or explicit eyebrows**

Preferred minimal approach: create `PagePathHeader` only if many pages need identical boilerplate. Otherwise pass `eyebrow={intl.formatMessage({ id: "nav.path..." })}` explicitly to each page's existing `PageHeader`.

Examples:

```tsx
<PageHeader
  eyebrow={intl.formatMessage({ id: "nav.path.cluster.nodes" })}
  title={intl.formatMessage({ id: "nodes.title" })}
  description={intl.formatMessage({ id: "nodes.description" })}
/>
```

Do this for all final routes in the route mapping table. For consolidated pages, the wrapper page owns the path label.

- [ ] **Step 4: Re-run page shell tests**

Run:

```bash
cd web && bun run test -- src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/shell/page-header.tsx web/src/pages web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "style(web): add route path headers to admin pages"
```

## Task 6: Merge Node Onboarding Into The Nodes Page

**Files:**
- Modify: `web/src/pages/onboarding/page.tsx`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing Nodes page tests for onboarding panel**

Add tests in `web/src/pages/nodes/page.test.tsx`:

```ts
test("opens the node onboarding operation panel from the nodes page", async () => {
  getNodesMock.mockResolvedValue({ items: [nodeRow] })
  getNodeOnboardingCandidatesMock.mockResolvedValue({ items: [] })
  getNodeOnboardingJobsMock.mockResolvedValue({ items: [], next_cursor: "", has_more: false })

  renderNodesPage("/cluster/nodes?panel=onboarding")

  expect(await screen.findByText(/onboarding/i)).toBeInTheDocument()
  expect(screen.queryByRole("heading", { name: /dashboard/i })).not.toBeInTheDocument()
})
```

Mock onboarding API calls in this test file the same way `onboarding/page.test.tsx` currently does.

- [ ] **Step 2: Run the focused test to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/nodes/page.test.tsx
```

Expected: FAIL because `NodesPage` does not render onboarding content.

- [ ] **Step 3: Extract `NodeOnboardingPanel`**

In `web/src/pages/onboarding/page.tsx`:

- Move the current state/effects/action UI into `export function NodeOnboardingPanel()`.
- Keep `export function OnboardingPage()` as a thin wrapper only during compatibility:

```tsx
export function OnboardingPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.nodes" })}
        title={intl.formatMessage({ id: "onboarding.title" })}
        description={intl.formatMessage({ id: "onboarding.description" })}
      />
      <NodeOnboardingPanel />
    </PageContainer>
  )
}
```

- [ ] **Step 4: Render onboarding inside `NodesPage`**

In `web/src/pages/nodes/page.tsx`:

- read `useSearchParams()`;
- add an action/button that links to `?panel=onboarding`;
- render `<NodeOnboardingPanel />` in a `SectionCard` or directly below the nodes lifecycle area when `panel=onboarding`;
- keep node list visible unless it causes duplicate headings. Prefer a page-local operation panel below the nodes table.

- [ ] **Step 5: Re-run Nodes and Onboarding tests**

Run:

```bash
cd web && bun run test -- src/pages/nodes/page.test.tsx src/pages/onboarding/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/pages/onboarding/page.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): merge onboarding operations into nodes"
```

## Task 7: Consolidate Channel Cluster Pages Into Tabs

**Files:**
- Create: `web/src/pages/cluster/channels/page.tsx`
- Create: `web/src/pages/cluster/channels/page.test.tsx`
- Modify: `web/src/pages/channel-cluster/page.tsx`
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing consolidated page tests**

Create `web/src/pages/cluster/channels/page.test.tsx` with mocks copied from the three source page tests. Cover:

- default `/cluster/channels` renders overview tab selected;
- `?tab=list` renders runtime channel list;
- `?tab=unhealthy` renders unhealthy list;
- tab clicks update the search param without leaving `/cluster/channels`.

- [ ] **Step 2: Run consolidated page tests to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/cluster/channels/page.test.tsx
```

Expected: FAIL because the page does not exist.

- [ ] **Step 3: Extract source panels**

Refactor source pages without changing behavior:

- `web/src/pages/channel-cluster/page.tsx`: export `ChannelClusterOverviewPanel`; keep `ChannelClusterPage` wrapper for old focused tests if useful.
- `web/src/pages/channels/page.tsx`: export `ChannelClusterListPanel`; keep `ChannelsPage` wrapper for tests/imports.
- `web/src/pages/channel-cluster/unhealthy/page.tsx`: export `ChannelClusterUnhealthyPanel`; keep `ChannelClusterUnhealthyPage` wrapper.

Panel components must not render `PageContainer` or route-level `PageHeader`; wrappers may still do so.

- [ ] **Step 4: Implement `ClusterChannelsPage`**

Create `web/src/pages/cluster/channels/page.tsx`:

```tsx
const tabs = [
  { id: "overview", labelMessageId: "channelCluster.tabs.overview" },
  { id: "list", labelMessageId: "channelCluster.tabs.list" },
  { id: "unhealthy", labelMessageId: "channelCluster.tabs.unhealthy" },
] as const

type ChannelClusterTab = (typeof tabs)[number]["id"]

export function ClusterChannelsPage() {
  const intl = useIntl()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = normalizeTab(searchParams.get("tab"))

  return (
    <PageContainer>
      <PageHeader
        eyebrow={intl.formatMessage({ id: "nav.path.cluster.channels" })}
        title={intl.formatMessage({ id: "channelCluster.title" })}
        description={intl.formatMessage({ id: "channelCluster.description" })}
      />
      <PageTabs
        activeTab={activeTab}
        tabs={tabs.map((tab) => ({ id: tab.id, label: intl.formatMessage({ id: tab.labelMessageId }) }))}
        onTabChange={(tab) => setSearchParams({ tab })}
      />
      {activeTab === "overview" ? <ChannelClusterOverviewPanel /> : null}
      {activeTab === "list" ? <ChannelClusterListPanel /> : null}
      {activeTab === "unhealthy" ? <ChannelClusterUnhealthyPanel /> : null}
    </PageContainer>
  )
}
```

Normalize unknown tabs to `overview`.

- [ ] **Step 5: Wire the route**

In `web/src/app/router.tsx`, map `cluster/channels` to `<ClusterChannelsPage />`. Keep old channel-cluster routes as redirects only.

- [ ] **Step 6: Re-run channel tests**

Run:

```bash
cd web && bun run test -- src/pages/cluster/channels/page.test.tsx src/pages/channel-cluster/page.test.tsx src/pages/channel-cluster/unhealthy/page.test.tsx src/pages/channels/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/cluster/channels web/src/pages/channel-cluster web/src/pages/channels/page.tsx web/src/app/router.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): consolidate channel cluster views"
```

## Task 8: Consolidate Diagnostics, Network, And Logs Into Diagnostics Tabs

**Files:**
- Create: `web/src/pages/cluster/diagnostics/page.tsx`
- Create: `web/src/pages/cluster/diagnostics/page.test.tsx`
- Modify: `web/src/pages/diagnostics/page.tsx`
- Modify: `web/src/pages/network/page.tsx`
- Modify: `web/src/pages/controller/page.tsx`
- Modify: `web/src/pages/slot-logs/page.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing diagnostics tab tests**

Create tests for:

- default `/cluster/diagnostics` renders trace tab;
- `?tab=network` renders the network panel;
- `?tab=controller-logs&node_id=1` renders controller logs and preserves `node_id` while switching tabs;
- legacy `/controller?node_id=1` redirects to `/cluster/diagnostics?node_id=1&tab=controller-logs`.

- [ ] **Step 2: Run diagnostics tab tests to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/cluster/diagnostics/page.test.tsx src/app/router.test.tsx
```

Expected: FAIL because the consolidated page does not exist and redirects are incomplete.

- [ ] **Step 3: Extract source panels**

Refactor source pages:

- `DiagnosticsPage` exports `DiagnosticsTracePanel`.
- `NetworkPage` exports `DiagnosticsNetworkPanel`.
- `ControllerPage` exports `ControllerLogsPanel`.
- `SlotLogsPage` exports `SlotLogsPanel`.

Panels must not render route-level `PageContainer` or duplicate `PageHeader`. Keep compatibility wrappers only as long as tests import them directly.

- [ ] **Step 4: Implement `ClusterDiagnosticsPage`**

Create tabs:

```ts
const tabs = [
  { id: "trace", labelMessageId: "diagnostics.tabs.trace" },
  { id: "network", labelMessageId: "diagnostics.tabs.network" },
  { id: "controller-logs", labelMessageId: "diagnostics.tabs.controllerLogs" },
  { id: "slot-logs", labelMessageId: "diagnostics.tabs.slotLogs" },
] as const
```

When changing tabs, preserve existing non-`tab` search params:

```ts
function setTab(nextTab: DiagnosticsTab) {
  const next = new URLSearchParams(searchParams)
  next.set("tab", nextTab)
  setSearchParams(next)
}
```

Render the selected panel under a single `PageHeader` with `nav.path.cluster.diagnostics`.

- [ ] **Step 5: Wire route and redirects**

In `web/src/app/router.tsx`:

- map `cluster/diagnostics` to `<ClusterDiagnosticsPage />`;
- redirect `/diagnostics`, `/network`, `/controller`, `/slot-logs` to the consolidated page with proper `tab` values;
- preserve `node_id`, `slot_id`, and similar search params for log pages.

- [ ] **Step 6: Re-run focused diagnostics tests**

Run:

```bash
cd web && bun run test -- src/pages/cluster/diagnostics/page.test.tsx src/pages/diagnostics/page.test.tsx src/pages/network/page.test.tsx src/pages/controller/page.test.tsx src/pages/slot-logs/page.test.tsx src/app/router.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/cluster/diagnostics web/src/pages/diagnostics/page.tsx web/src/pages/network/page.tsx web/src/pages/controller/page.tsx web/src/pages/slot-logs/page.tsx web/src/app/router.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "refactor(web): consolidate diagnostics workbench tabs"
```

## Task 9: Finalize Business/System Route Moves And Documentation

**Files:**
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/README.md`

- [ ] **Step 1: Update page shell route coverage**

Ensure tests cover every final route:

```ts
const finalRoutes = [
  "/dashboard",
  "/monitor",
  "/cluster/nodes",
  "/cluster/slots",
  "/cluster/channels",
  "/cluster/tasks",
  "/cluster/topology",
  "/cluster/diagnostics",
  "/business/users",
  "/business/channels",
  "/business/messages",
  "/business/system-users",
  "/system/permissions",
  "/system/webhooks",
  "/system/connections",
]
```

For each route, render inside `RouterProvider` with authenticated state and assert `role="main"` plus a route-specific heading/path label.

- [ ] **Step 2: Run page shell tests to verify failures**

Run:

```bash
cd web && bun run test -- src/pages/page-shells.test.tsx src/app/router.test.tsx
```

Expected: FAIL if any old business/system route remains mounted as a primary route or any new route lacks metadata.

- [ ] **Step 3: Remove old primary routes**

Old routes must be redirects only. Keep imports only for components rendered by new paths. Verify these old paths are not used as top-level navigation item `href` values:

```bash
rg 'href: "/(nodes|slots|tasks|topology|users|messages|connections|settings|channel-cluster|network|diagnostics|slot-logs|controller|onboarding)' web/src/lib/navigation.ts
```

Expected: no matches except inside `aliases` or redirect definitions.

- [ ] **Step 4: Update `web/README.md` page matrix**

Replace old page paths with the new route map. Note that old routes redirect with `replace` and that `/cluster/channels` and `/cluster/diagnostics` use tab search params for previously separate pages.

- [ ] **Step 5: Re-run route/page tests**

Run:

```bash
cd web && bun run test -- src/pages/page-shells.test.tsx src/app/router.test.tsx src/lib/navigation.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/app/router.tsx web/src/app/router.test.tsx web/src/pages/page-shells.test.tsx web/README.md
git commit -m "docs(web): document redesigned admin routes"
```

## Task 10: Full Frontend Verification

**Files:**
- No source changes expected unless verification exposes a bug.

- [ ] **Step 1: Run focused shell/navigation tests**

Run:

```bash
cd web && bun run test -- src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/topbar.test.tsx src/app/layout/sidebar-nav.test.tsx src/components/shell/shell-components.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run consolidated page tests**

Run:

```bash
cd web && bun run test -- src/pages/cluster/channels/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx src/pages/nodes/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Run the full frontend test suite**

Run:

```bash
cd web && bun run test
```

Expected: PASS.

- [ ] **Step 4: Run the frontend production build**

Run:

```bash
cd web && bun run build
```

Expected: PASS with `tsc -b` and Vite production build complete.

- [ ] **Step 5: Inspect final diff for accidental backend or unrelated changes**

Run:

```bash
git status --short
git diff --stat
git diff -- web/src/lib/navigation.ts web/src/app/router.tsx web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.tsx web/src/styles/globals.css
```

Expected: only frontend redesign/docs files changed.

- [ ] **Step 6: Final commit**

```bash
git add web docs/superpowers/plans/2026-05-14-web-admin-redesign.md
git commit -m "feat(web): redesign admin navigation shell"
```

## Risks And Mitigations

- Large existing pages may be difficult to extract cleanly. Mitigation: extract panels first without behavior changes, keep compatibility wrappers, and run the original page tests before creating consolidated wrappers.
- Search params can collide between tab state and existing filters. Mitigation: reserve only `tab` for consolidation and preserve all other params on tab changes and redirects.
- Visual token changes may affect many snapshots/queries indirectly. Mitigation: prefer semantic queries over class assertions except for the new components where monospace/status behavior is the requirement.
- `bun.lock` can change substantially when adding Geist Mono. Mitigation: add only the one dependency and inspect the lockfile diff.
- Old route bookmarks may break if a redirect misses query preservation. Mitigation: router tests must cover `/controller?node_id=1` and tab redirects.

## Completion Criteria

- Four top tabs are visible in the authenticated shell; sidebar shows only the active section's 2-6 items.
- Active sidebar item uses dark background plus a green left rail.
- Page headers show uppercase monospace paths such as `CLUSTER / NODES`.
- `/cluster/channels` contains overview/list/unhealthy tabs.
- `/cluster/diagnostics` contains trace/network/controller logs/slot logs tabs.
- `/cluster/nodes?panel=onboarding` exposes the old onboarding operation UI inside Nodes.
- Every route in the spec exists and every old route redirects with `replace`.
- English and Chinese UI copy remain available for new navigation, path labels, and tabs.
- `cd web && bun run test` passes.
- `cd web && bun run build` passes.
