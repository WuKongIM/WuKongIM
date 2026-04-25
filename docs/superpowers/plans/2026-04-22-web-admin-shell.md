# Web Admin Shell Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a first-pass management UI shell in `web/` using Bun, TypeScript, React, Tailwind CSS v4, and shadcn-ui, with a fixed sidebar, topbar, and seven route-level page skeletons.

**Architecture:** Create a standalone Vite SPA under `web/` and keep `ui/` as a reference-only static prototype. Put routing and the app shell in `src/app`, keep shadcn-generated primitives in `src/components/ui`, keep shell-only reusable layout blocks in `src/components/shell`, and let each page under `src/pages/*` compose those blocks without introducing business logic or API calls.

**Tech Stack:** Bun, Vite, React, TypeScript, React Router, Tailwind CSS v4, shadcn-ui, lucide-react, Vitest, Testing Library.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-web-admin-shell-design.md`
- Existing static reference: `ui/dashboard.html`, `ui/nodes.html`, `ui/channels.html`, `ui/connections.html`, `ui/network.html`, `ui/topology.html`
- Tailwind CSS Vite setup: https://tailwindcss.com/docs/installation/using-vite
- shadcn-ui Vite setup: https://ui.shadcn.com/docs/installation/vite
- Vite scaffolding: https://vite.dev/guide/
- Follow `@superpowers:test-driven-development` for every behavior change after the scaffold step.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Use ASCII by default in source files.

## File Structure

- Create: `web/package.json` — Bun scripts and dependencies.
- Create: `web/tsconfig.json` — root TypeScript project references and `@/*` alias.
- Create: `web/tsconfig.app.json` — app TypeScript options and `@/*` alias.
- Create: `web/tsconfig.node.json` — Vite/Vitest config typing.
- Create: `web/vite.config.ts` — React plugin, Tailwind v4 Vite plugin, `@` alias.
- Create: `web/vitest.config.ts` — `jsdom` test environment and setup file.
- Create: `web/components.json` — shadcn-ui registry/config file.
- Create: `web/index.html` — Vite HTML entry.
- Create: `web/src/main.tsx` — React bootstrap.
- Create: `web/src/styles/globals.css` — Tailwind import plus first-pass design tokens.
- Create: `web/src/test/setup.ts` — Testing Library and jest-dom setup.
- Create: `web/src/lib/cn.ts` — `clsx` + `tailwind-merge` helper.
- Create: `web/src/lib/navigation.ts` — sidebar groups and page metadata.
- Create: `web/src/app/providers.tsx` — app-level provider wrapper.
- Create: `web/src/app/router.tsx` — route tree and `/ -> /dashboard` redirect.
- Create: `web/src/app/layout/app-shell.tsx` — sidebar + topbar + content layout.
- Create: `web/src/app/layout/sidebar-nav.tsx` — fixed left navigation.
- Create: `web/src/app/layout/topbar.tsx` — route-aware top bar.
- Create: `web/src/components/ui/*` — only the shadcn-ui primitives used by the shell.
- Create: `web/src/components/shell/page-container.tsx` — page spacing wrapper.
- Create: `web/src/components/shell/page-header.tsx` — page title and description block.
- Create: `web/src/components/shell/section-card.tsx` — shared card wrapper for content sections.
- Create: `web/src/components/shell/placeholder-block.tsx` — generic placeholder surface.
- Create: `web/src/components/shell/metric-placeholder.tsx` — compact metric cards.
- Create: `web/src/pages/dashboard/page.tsx`
- Create: `web/src/pages/nodes/page.tsx`
- Create: `web/src/pages/channels/page.tsx`
- Create: `web/src/pages/connections/page.tsx`
- Create: `web/src/pages/slots/page.tsx`
- Create: `web/src/pages/network/page.tsx`
- Create: `web/src/pages/topology/page.tsx`
- Create: `web/src/app/router.test.tsx` — route and redirect coverage.
- Create: `web/src/app/layout/sidebar-nav.test.tsx` — current-nav highlighting coverage.
- Create: `web/src/app/layout/topbar.test.tsx` — route metadata rendering coverage.
- Create: `web/src/pages/page-shells.test.tsx` — page skeleton coverage for all seven routes.
- Create: `web/README.md` — local run/build/test instructions and scope note.

### Task 1: Scaffold the `web/` toolchain and baseline configuration

**Files:**
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/tsconfig.app.json`
- Create: `web/tsconfig.node.json`
- Create: `web/vite.config.ts`
- Create: `web/vitest.config.ts`
- Create: `web/components.json`
- Create: `web/index.html`
- Create: `web/src/main.tsx`
- Create: `web/src/styles/globals.css`
- Create: `web/src/test/setup.ts`
- Create: `web/src/lib/cn.ts`

- [ ] **Step 1: Scaffold the Vite React TypeScript app with Bun**

Run:

```bash
bun create vite web --template react-ts --no-interactive
```

Expected: a fresh `web/` app exists with Vite React TypeScript baseline files.

- [ ] **Step 2: Install runtime and dev dependencies needed for the shell**

Run:

```bash
cd web && bun add react-router-dom lucide-react clsx tailwind-merge
cd web && bun add -D tailwindcss @tailwindcss/vite vitest jsdom @testing-library/react @testing-library/jest-dom @testing-library/user-event @types/node
```

Expected: `package.json` lists the UI, Tailwind, and test dependencies for the plan.

- [ ] **Step 3: Configure Vite, Tailwind v4, aliases, and test setup**

Update the config files to match the official Vite/Tailwind/shadcn requirements:

```ts
// web/vite.config.ts
import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
```

```ts
// web/vitest.config.ts
import { defineConfig } from "vitest/config"
import viteConfig from "./vite.config"

export default defineConfig({
  ...viteConfig,
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
})
```

```css
/* web/src/styles/globals.css */
@import "tailwindcss";

:root {
  --background: #f5f7fb;
  --foreground: #111827;
  --sidebar: #eef2f7;
  --card: #ffffff;
  --muted: #64748b;
  --border: #d9e2ec;
  --accent: #2563eb;
}

body {
  background: var(--background);
  color: var(--foreground);
}
```

Also make these script-level adjustments:

```json
{
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "test": "vitest run",
    "test:watch": "vitest"
  }
}
```

- [ ] **Step 4: Initialize shadcn-ui and add only the shell primitives**

Run the Bun form of the official shadcn Vite setup in the existing `web/` project, then add the small primitive set the shell needs:

```bash
cd web && bunx --bun shadcn@latest init -y
cd web && bunx --bun shadcn@latest add button card separator scroll-area tooltip breadcrumb sheet
```

Expected: `components.json`, `src/components/ui/*`, and shadcn theme wiring are present without adding unrelated components.

- [ ] **Step 5: Verify the scaffold builds before any feature work**

Run:

```bash
cd web && bun run build
```

Expected: PASS, confirming the toolchain is wired correctly before behavior work starts.

- [ ] **Step 6: Commit the scaffold slice**

```bash
git add web
git commit -m "feat: scaffold web admin shell app"
```

### Task 2: Add the route tree and app shell with a redirect-first test

**Files:**
- Create: `web/src/app/providers.tsx`
- Create: `web/src/app/router.tsx`
- Create: `web/src/app/layout/app-shell.tsx`
- Create: `web/src/app/router.test.tsx`
- Modify: `web/src/main.tsx`
- Create: `web/src/lib/navigation.ts`

- [ ] **Step 1: Write the failing routing and shell tests**

Create `web/src/app/router.test.tsx` with focused expectations:

```tsx
import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { routes } from "@/app/router"

test("redirects / to /dashboard", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
})

test("renders sidebar, topbar, and page outlet inside the app shell", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/nodes"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByLabelText("Primary navigation")).toBeInTheDocument()
  expect(screen.getByRole("banner")).toBeInTheDocument()
  expect(screen.getByRole("main")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the router test file to verify it fails**

Run:

```bash
cd web && bun run test web/src/app/router.test.tsx
```

Expected: FAIL because the route tree, app shell, and route pages do not exist yet.

- [ ] **Step 3: Implement the minimal providers, routes, and shell layout**

Start with a route tree that is easy to test:

```tsx
// web/src/app/router.tsx
export const routes = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: "dashboard", element: <DashboardPage /> },
      { path: "nodes", element: <NodesPage /> },
      { path: "channels", element: <ChannelsPage /> },
      { path: "connections", element: <ConnectionsPage /> },
      { path: "slots", element: <SlotsPage /> },
      { path: "network", element: <NetworkPage /> },
      { path: "topology", element: <TopologyPage /> },
    ],
  },
]
```

```tsx
// web/src/app/layout/app-shell.tsx
export function AppShell() {
  return (
    <div className="flex min-h-screen bg-[var(--background)] text-[var(--foreground)]">
      <SidebarNav />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar />
        <main className="flex-1" role="main">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
```

Wire `main.tsx` to `RouterProvider` through `providers.tsx` so future providers have one home.

- [ ] **Step 4: Re-run the router tests to verify they pass**

Run:

```bash
cd web && bun run test web/src/app/router.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the routing shell slice**

```bash
git add web/src/main.tsx web/src/app/providers.tsx web/src/app/router.tsx web/src/app/layout/app-shell.tsx web/src/app/router.test.tsx web/src/lib/navigation.ts
git commit -m "feat: add web admin shell routing"
```

### Task 3: Add navigation metadata, sidebar highlighting, and route-aware topbar

**Files:**
- Create: `web/src/app/layout/sidebar-nav.tsx`
- Create: `web/src/app/layout/topbar.tsx`
- Create: `web/src/app/layout/sidebar-nav.test.tsx`
- Create: `web/src/app/layout/topbar.test.tsx`
- Modify: `web/src/lib/navigation.ts`

- [ ] **Step 1: Write failing tests for sidebar highlighting and topbar metadata**

Add `web/src/app/layout/sidebar-nav.test.tsx`:

```tsx
test("marks the current navigation item with aria-current", () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/slots"] })
  render(<RouterProvider router={router} />)

  expect(screen.getByRole("link", { name: "Slots" })).toHaveAttribute("aria-current", "page")
})
```

Add `web/src/app/layout/topbar.test.tsx`:

```tsx
test("shows the current route title and description", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("heading", { name: "Network" })).toBeInTheDocument()
  expect(screen.getByText("Cluster traffic and transport observation shell.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the nav-related tests to verify they fail**

Run:

```bash
cd web && bun run test web/src/app/layout/sidebar-nav.test.tsx web/src/app/layout/topbar.test.tsx
```

Expected: FAIL because the nav data and route-aware components are not implemented yet.

- [ ] **Step 3: Implement the nav model, sidebar, and topbar with minimal behavior**

Use a single metadata source so sidebar and topbar cannot drift:

```ts
// web/src/lib/navigation.ts
export const navigationGroups = [
  {
    label: "Overview",
    items: [{ title: "Dashboard", href: "/dashboard", description: "Cluster summary and entry point." }],
  },
  {
    label: "Runtime",
    items: [
      { title: "Nodes", href: "/nodes", description: "Node inventory and lifecycle shell." },
      { title: "Channels", href: "/channels", description: "Channel list and drill-in shell." },
      { title: "Connections", href: "/connections", description: "Connection inventory shell." },
      { title: "Slots", href: "/slots", description: "Slot distribution and status shell." },
    ],
  },
  {
    label: "Observability",
    items: [
      { title: "Network", href: "/network", description: "Cluster traffic and transport observation shell." },
      { title: "Topology", href: "/topology", description: "Replica and node relationship shell." },
    ],
  },
]
```

Make `SidebarNav` use `NavLink` for active styling and `Topbar` derive title/description from `useLocation()` + that metadata map.

- [ ] **Step 4: Re-run the nav-related tests to verify they pass**

Run:

```bash
cd web && bun run test web/src/app/layout/sidebar-nav.test.tsx web/src/app/layout/topbar.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the nav slice**

```bash
git add web/src/app/layout/sidebar-nav.tsx web/src/app/layout/topbar.tsx web/src/app/layout/sidebar-nav.test.tsx web/src/app/layout/topbar.test.tsx web/src/lib/navigation.ts
git commit -m "feat: add web admin shell navigation"
```

### Task 4: Add reusable shell blocks and the seven page skeletons

**Files:**
- Create: `web/src/components/shell/page-container.tsx`
- Create: `web/src/components/shell/page-header.tsx`
- Create: `web/src/components/shell/section-card.tsx`
- Create: `web/src/components/shell/placeholder-block.tsx`
- Create: `web/src/components/shell/metric-placeholder.tsx`
- Create: `web/src/pages/dashboard/page.tsx`
- Create: `web/src/pages/nodes/page.tsx`
- Create: `web/src/pages/channels/page.tsx`
- Create: `web/src/pages/connections/page.tsx`
- Create: `web/src/pages/slots/page.tsx`
- Create: `web/src/pages/network/page.tsx`
- Create: `web/src/pages/topology/page.tsx`
- Create: `web/src/pages/page-shells.test.tsx`

- [ ] **Step 1: Write the failing page-skeleton tests before building the pages**

Create one test file that checks each route for a unique shell signature instead of snapshotting the whole DOM:

```tsx
it.each([
  ["/dashboard", "Dashboard", "Cluster Summary"],
  ["/nodes", "Nodes", "Node Inventory"],
  ["/channels", "Channels", "Channel List"],
  ["/connections", "Connections", "Connection Table"],
  ["/slots", "Slots", "Slot Health"],
  ["/network", "Network", "Traffic Overview"],
  ["/topology", "Topology", "Topology Canvas"],
])("renders %s shell", async (path, title, section) => {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  render(<RouterProvider router={router} />)

  expect(await screen.findByRole("heading", { name: title })).toBeInTheDocument()
  expect(screen.getByText(section)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the page-skeleton tests to verify they fail**

Run:

```bash
cd web && bun run test web/src/pages/page-shells.test.tsx
```

Expected: FAIL because the page-level shell blocks and route components are still missing.

- [ ] **Step 3: Implement the shared shell blocks and compose each page**

Keep the shared blocks small and composable:

```tsx
// web/src/components/shell/page-header.tsx
export function PageHeader({ title, description, actions }: Props) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-[var(--border)] pb-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-2 text-sm text-[var(--muted)]">{description}</p>
      </div>
      {actions ? <div>{actions}</div> : null}
    </div>
  )
}
```

```tsx
// web/src/pages/dashboard/page.tsx
export function DashboardPage() {
  return (
    <PageContainer>
      <PageHeader title="Dashboard" description="Cluster summary and entry point." />
      <section className="grid gap-4 md:grid-cols-4">
        <MetricPlaceholder label="Nodes" />
        <MetricPlaceholder label="Channels" />
        <MetricPlaceholder label="Connections" />
        <MetricPlaceholder label="Slots" />
      </section>
      <section className="grid gap-4 xl:grid-cols-[1.4fr_1fr]">
        <SectionCard title="Cluster Summary">
          <PlaceholderBlock kind="panel" />
        </SectionCard>
        <SectionCard title="Recent Events">
          <PlaceholderBlock kind="list" />
        </SectionCard>
      </section>
    </PageContainer>
  )
}
```

Mirror that pattern across the other pages using their approved labels:

- `Nodes` → `Node Inventory`
- `Channels` → `Channel List`
- `Connections` → `Connection Table`
- `Slots` → `Slot Health`
- `Network` → `Traffic Overview`
- `Topology` → `Topology Canvas`

- [ ] **Step 4: Re-run the page-skeleton tests to verify they pass**

Run:

```bash
cd web && bun run test web/src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the page-shell slice**

```bash
git add web/src/components/shell web/src/pages web/src/pages/page-shells.test.tsx
git commit -m "feat: add web admin page skeletons"
```

### Task 5: Add developer docs and run the full focused verification set

**Files:**
- Create: `web/README.md`
- Modify: `web/package.json` if verification exposes script gaps.

- [ ] **Step 1: Write a short `web/README.md` for local usage and scope**

Document exactly:

```md
# Web Admin Shell

## Commands
- `bun install`
- `bun run dev`
- `bun run test`
- `bun run build`

## Scope
This first pass only provides the management shell and page skeletons.
It does not call backend APIs or include auth, data fetching, charts, or topology rendering.
```

- [ ] **Step 2: Run the complete focused verification suite**

Run:

```bash
cd web && bun run test
cd web && bun run build
```

Expected: both commands PASS.

- [ ] **Step 3: Open the running app once and do a quick manual shell check**

Run:

```bash
cd web && bun run dev
```

Then verify manually in the browser:
- sidebar is fixed on the left
- topbar title changes per route
- all seven routes render without blank screens
- `ui/` remains untouched

- [ ] **Step 4: If verification exposes failures, fix only the proven issue and re-run the same commands**

Do not expand scope into API integration, dark mode, or extra component work.

- [ ] **Step 5: Commit the docs and verification slice**

```bash
git add web/README.md web/package.json
git commit -m "docs: document web admin shell"
```
