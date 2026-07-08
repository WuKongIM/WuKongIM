# Web System Admin And Tasks Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/system/permissions`, `/system/webhooks`, and `/cluster/tasks` into the approved editorial console visual language without changing manager API behavior.

**Architecture:** Keep every change page-local and reuse existing `PageHeader`, `SectionCard`, `ResourceState`, table, and i18n primitives. Add stable DOM markers only for visual-contract tests, keep all existing visible copy, and preserve the current permissions snapshot, webhook placeholder, task filters, task refresh, and task timeline flows.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, Vitest, Testing Library, Bun.

---

## File Structure

- Modify: `web/src/pages/settings/permissions/page.tsx`
  - Converts authentication summary cards into a compact summary strip.
  - Adds named static-users and permission-catalog table surfaces.
- Test: `web/src/pages/settings/permissions/page.test.tsx`
  - Adds rendered-DOM assertions for summary strip and named table surfaces.
- Modify: `web/src/pages/settings/webhooks/page.tsx`
  - Keeps the page as a placeholder and wraps the empty state in a restrained system placeholder surface.
- Create: `web/src/pages/settings/webhooks/page.test.tsx`
  - Adds focused coverage for the placeholder surface and absence of configuration affordances.
- Modify: `web/src/pages/tasks/page.tsx`
  - Adds named active-task and audit-history table surfaces while preserving filters, refresh, and timeline behavior.
- Test: `web/src/pages/tasks/page.test.tsx`
  - Extends the existing visual-contract test to cover active/audit table surfaces.
- Do not modify: `web/src/i18n/messages/en.ts`, `web/src/i18n/messages/zh-CN.ts`
  - This plan reuses existing visible copy.
- Do not modify: backend manager API code, route definitions, auth rules, dashboards, `/cluster/nodes`, `/cluster/slots`, `/cluster/channels`, `/cluster/topology`, `/cluster/diagnostics`, `/cluster/monitor`, or business pages.

## Task 1: Permissions Page Surfaces

**Files:**
- Modify: `web/src/pages/settings/permissions/page.tsx`
- Test: `web/src/pages/settings/permissions/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Update the first import in `web/src/pages/settings/permissions/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
```

Add this test after `renders auth summary, users, and permission catalog`:

```tsx
test("uses compact permission summary and named table surfaces", async () => {
  getPermissionsMock.mockResolvedValueOnce({
    auth_enabled: true,
    current_user: "admin",
    users: [
      { username: "admin", permissions: [{ resource: "*", actions: ["*"] }] },
      { username: "viewer", permissions: [{ resource: "cluster.node", actions: ["r"] }] },
    ],
    resources: [
      { resource: "cluster.permission", actions: ["r"], description: "Read manager authentication and permission configuration snapshots." },
      { resource: "cluster.node", actions: ["r", "w"], description: "Read node inventory and perform node lifecycle actions." },
    ],
  })

  renderPermissionsPage()

  const summaryStrip = await screen.findByTestId("permissions-summary-strip")
  expect(summaryStrip).toHaveClass("grid", "overflow-hidden", "rounded-md", "border", "border-border", "bg-card")
  expect(summaryStrip.querySelectorAll("[data-permission-summary-cell]")).toHaveLength(4)
  expect(summaryStrip.querySelector("[data-permission-summary-cell]")).not.toHaveClass("rounded-lg")

  const readonlyNotice = screen.getByTestId("permissions-readonly-notice")
  expect(readonlyNotice).toHaveClass("border-t", "border-border", "pt-3")

  const usersTable = screen.getByRole("table", { name: "Static Manager Users" })
  const usersSurface = usersTable.closest("[data-permissions-surface='users']")
  expect(usersSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(usersTable).toHaveClass("text-sm")
  expect(within(usersTable).getByText("viewer")).toBeInTheDocument()

  const catalogTable = screen.getByRole("table", { name: "Permission Catalog" })
  const catalogSurface = catalogTable.closest("[data-permissions-surface='catalog']")
  expect(catalogSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(catalogTable).toHaveClass("text-sm")
  expect(within(catalogTable).getByText("cluster.permission")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/permissions/page.test.tsx -t "uses compact permission summary and named table surfaces"
```

Expected: FAIL because `permissions-summary-strip`, `permissions-readonly-notice`, `data-permissions-surface`, and table accessible names are not present yet.

- [ ] **Step 3: Convert the permissions summary cards into a strip**

In `web/src/pages/settings/permissions/page.tsx`, replace the current summary grid inside the summary `SectionCard`:

```tsx
<div className="grid gap-3 md:grid-cols-4">
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.auth" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {snapshot.auth_enabled
        ? intl.formatMessage({ id: "permissions.auth.enabled" })
        : intl.formatMessage({ id: "permissions.auth.disabled" })}
    </div>
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.currentUser" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {snapshot.current_user
        ? intl.formatMessage({ id: "permissions.currentUser" }, { user: snapshot.current_user })
        : intl.formatMessage({ id: "permissions.currentUser.empty" })}
    </div>
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.staticUsers" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {intl.formatMessage({ id: "permissions.staticUsers" }, { count: snapshot.users.length })}
    </div>
  </div>
  <div className="rounded-lg border border-border bg-muted/30 p-3 text-sm">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.catalog" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {intl.formatMessage({ id: "permissions.catalogResources" }, { count: snapshot.resources.length })}
    </div>
  </div>
</div>
```

with:

```tsx
<div
  className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-4"
  data-testid="permissions-summary-strip"
>
  <div className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-permission-summary-cell="">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.auth" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {snapshot.auth_enabled
        ? intl.formatMessage({ id: "permissions.auth.enabled" })
        : intl.formatMessage({ id: "permissions.auth.disabled" })}
    </div>
  </div>
  <div className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-permission-summary-cell="">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.currentUser" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {snapshot.current_user
        ? intl.formatMessage({ id: "permissions.currentUser" }, { user: snapshot.current_user })
        : intl.formatMessage({ id: "permissions.currentUser.empty" })}
    </div>
  </div>
  <div className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-permission-summary-cell="">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.staticUsers" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {intl.formatMessage({ id: "permissions.staticUsers" }, { count: snapshot.users.length })}
    </div>
  </div>
  <div className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0" data-permission-summary-cell="">
    <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.catalog" })}</div>
    <div className="mt-1 font-semibold text-foreground">
      {intl.formatMessage({ id: "permissions.catalogResources" }, { count: snapshot.resources.length })}
    </div>
  </div>
</div>
```

Replace the current read-only notice:

```tsx
<p className="mt-4 rounded-lg border border-border bg-background px-3 py-2 text-sm text-muted-foreground">
  {intl.formatMessage({ id: "permissions.readonlyNotice" })}
</p>
```

with:

```tsx
<p
  className="mt-4 border-t border-border pt-3 text-sm text-muted-foreground"
  data-testid="permissions-readonly-notice"
>
  {intl.formatMessage({ id: "permissions.readonlyNotice" })}
</p>
```

- [ ] **Step 4: Add named table surfaces**

In `web/src/pages/settings/permissions/page.tsx`, replace the static-users table wrapper and table opening:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-permissions-surface="users"
>
  <table
    aria-label={intl.formatMessage({ id: "permissions.users.title" })}
    className="w-full border-collapse text-sm"
  >
```

Replace the permission-catalog table wrapper and table opening:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-permissions-surface="catalog"
>
  <table
    aria-label={intl.formatMessage({ id: "permissions.catalog.title" })}
    className="w-full border-collapse text-sm"
  >
```

- [ ] **Step 5: Run focused permissions tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/permissions/page.test.tsx
```

Expected: PASS. Existing tests still cover auth enabled/disabled, empty users, and forbidden/unavailable error mapping.

- [ ] **Step 6: Commit permissions page change**

Run:

```bash
git add web/src/pages/settings/permissions/page.tsx web/src/pages/settings/permissions/page.test.tsx
git commit -m "style(web): refine permissions admin surface"
```

Expected: commit succeeds with only permissions page and permissions test changes.

## Task 2: Webhooks Placeholder Surface

**Files:**
- Modify: `web/src/pages/settings/webhooks/page.tsx`
- Create: `web/src/pages/settings/webhooks/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/settings/webhooks/page.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { WebhooksPage } from "@/pages/settings/webhooks/page"

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

function renderWebhooksPage() {
  return render(
    <I18nProvider>
      <WebhooksPage />
    </I18nProvider>,
  )
}

test("renders a restrained placeholder without webhook configuration controls", () => {
  renderWebhooksPage()

  expect(screen.getByRole("heading", { name: "Webhook Configuration" })).toBeInTheDocument()
  expect(screen.getByText("Event callback URL configuration, event type filtering, and callback logs.")).toBeInTheDocument()

  const surface = screen.getByTestId("webhooks-placeholder-surface")
  expect(surface).toHaveClass("rounded-md", "border", "border-border", "bg-card")
  expect(within(surface).getByText("Coming Soon")).toBeInTheDocument()
  expect(within(surface).getByRole("status")).toHaveAttribute("data-kind", "empty")

  expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("button")).not.toBeInTheDocument()
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("switch")).not.toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/webhooks/page.test.tsx
```

Expected: FAIL because `webhooks-placeholder-surface` is not present yet.

- [ ] **Step 3: Add the restrained placeholder surface**

In `web/src/pages/settings/webhooks/page.tsx`, replace the current `SectionCard` block:

```tsx
<SectionCard
  description={intl.formatMessage({ id: "common.comingSoonDescription" })}
  title={intl.formatMessage({ id: "common.comingSoon" })}
>
  <ResourceState kind="empty" title={intl.formatMessage({ id: "webhooks.title" })} />
</SectionCard>
```

with:

```tsx
<SectionCard
  className="rounded-md"
  description={intl.formatMessage({ id: "common.comingSoonDescription" })}
  title={intl.formatMessage({ id: "common.comingSoon" })}
>
  <div
    className="rounded-md border border-border bg-card p-3"
    data-testid="webhooks-placeholder-surface"
  >
    <ResourceState kind="empty" title={intl.formatMessage({ id: "webhooks.title" })} />
  </div>
</SectionCard>
```

- [ ] **Step 4: Run focused webhooks tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/webhooks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS. `page-shells.test.tsx` still confirms `/system/webhooks` route metadata and coming-soon copy.

- [ ] **Step 5: Commit webhooks placeholder change**

Run:

```bash
git add web/src/pages/settings/webhooks/page.tsx web/src/pages/settings/webhooks/page.test.tsx
git commit -m "style(web): refine webhooks placeholder surface"
```

Expected: commit succeeds with only the webhooks page and new webhooks test changes.

## Task 3: Controller Tasks Table Surfaces

**Files:**
- Modify: `web/src/pages/tasks/page.tsx`
- Test: `web/src/pages/tasks/page.test.tsx`

- [ ] **Step 1: Write the failing test**

Extend the existing `uses a compact task-center summary strip and filter toolbar` test in `web/src/pages/tasks/page.test.tsx` by adding these assertions after the filter toolbar assertion:

```tsx
  const activeTable = screen.getByRole("table", { name: "Active tasks" })
  const activeSurface = activeTable.closest("[data-tasks-surface='active']")
  expect(activeSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(activeTable).toHaveClass("text-sm")
  expect(within(activeTable).getByText("slot-1-replica-move-2-to-4-r9")).toBeInTheDocument()

  const auditTable = screen.getByRole("table", { name: "Task audit history" })
  const auditSurface = auditTable.closest("[data-tasks-surface='audit']")
  expect(auditSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(auditTable).toHaveClass("text-sm")
  expect(within(auditTable).getByText("completed slot_replica_move task for slot 1")).toBeInTheDocument()
```

The full test body should still start with:

```tsx
test("uses a compact task-center summary strip and filter toolbar", async () => {
  renderTasksPage()

  expect(await screen.findByRole("heading", { name: "Controller Tasks" })).toBeInTheDocument()
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/tasks/page.test.tsx -t "uses a compact task-center summary strip and filter toolbar"
```

Expected: FAIL because the active and audit tables do not have accessible table names or `data-tasks-surface` wrappers yet.

- [ ] **Step 3: Add the active tasks table surface**

In `web/src/pages/tasks/page.tsx`, replace the active table wrapper and table opening:

```tsx
<div className="overflow-x-auto">
  <table className="w-full min-w-[960px] border-collapse text-left">
```

with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-tasks-surface="active"
>
  <table
    aria-label={intl.formatMessage({ id: "tasks.activeTitle" })}
    className="w-full min-w-[960px] border-collapse text-left text-sm"
  >
```

- [ ] **Step 4: Add the audit history table surface**

In `web/src/pages/tasks/page.tsx`, replace the audit table wrapper and table opening:

```tsx
<div className="overflow-x-auto">
  <table className="w-full min-w-[980px] border-collapse text-left">
```

with:

```tsx
<div
  className="overflow-x-auto rounded-md border border-border"
  data-tasks-surface="audit"
>
  <table
    aria-label={intl.formatMessage({ id: "tasks.auditTitle" })}
    className="w-full min-w-[980px] border-collapse text-left text-sm"
  >
```

- [ ] **Step 5: Keep timeline event surfaces behavior-only**

Do not change the timeline request, open state, close behavior, loading state, error state, or event ordering. The existing event surface already uses:

```tsx
<div className="rounded-md border border-border bg-background p-3" data-task-timeline-event="" key={event.event_id}>
```

Leave that structure unchanged in this task.

- [ ] **Step 6: Run focused tasks tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/tasks/page.test.tsx
```

Expected: PASS. Existing tests still cover rendering, summary/filter toolbar, filter request parameters, timeline loading, forbidden error mapping, and empty state.

- [ ] **Step 7: Commit tasks page change**

Run:

```bash
git add web/src/pages/tasks/page.tsx web/src/pages/tasks/page.test.tsx
git commit -m "style(web): refine controller task tables"
```

Expected: commit succeeds with only tasks page and tasks test changes.

## Task 4: Focused Verification And Build Hygiene

**Files:**
- Check: `web/src/pages/settings/permissions/page.test.tsx`
- Check: `web/src/pages/settings/webhooks/page.test.tsx`
- Check: `web/src/pages/tasks/page.test.tsx`
- Check: `web/src/pages/page-shells.test.tsx`
- Check: `web/dist/index.html`

- [ ] **Step 1: Run the combined page test gate**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/permissions/page.test.tsx src/pages/settings/webhooks/page.test.tsx src/pages/tasks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS for the three target page test files and route shell coverage.

- [ ] **Step 2: Run TypeScript project build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: PASS with no TypeScript errors.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: PASS. A Vite chunk-size warning is acceptable if the command exits with status 0.

- [ ] **Step 4: Restore build hash churn when it is the only dist change**

Inspect dist changes:

```bash
git diff -- web/dist/index.html
```

If the diff only changes `/assets/index-*.js` or `/assets/index-*.css` hash references, restore that file:

```bash
git restore web/dist/index.html
```

Expected: `web/dist/index.html` is clean unless the build produced a real checked-in asset change that must be reviewed with the user.

- [ ] **Step 5: Run whitespace and conflict-marker check**

Run:

```bash
git diff --check
```

Expected: no whitespace errors or conflict markers.

- [ ] **Step 6: Inspect final status**

Run:

```bash
git status --short
```

Expected: no uncommitted source changes from this plan. Pre-existing untracked files may remain:

```text
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
?? web/DESIGN.md
```

- [ ] **Step 7: Report completion**

Report:

```text
Implemented the approved system admin and tasks redesign for /system/permissions, /system/webhooks, and /cluster/tasks. Focused page tests, route shell coverage, TypeScript build, production build, and git diff check passed. No backend API, route, auth, webhook functionality, or unrelated page changes were made.
```
