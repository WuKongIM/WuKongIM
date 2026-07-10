# Web System Logs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Cluster Ops “System Logs” menu and route that exposes existing node-scoped ordinary process logs.

**Architecture:** Reuse the existing manager application-log API and `AppLogsPage` UI. The implementation only changes web navigation, routing, i18n copy, and focused frontend tests.

**Tech Stack:** React, React Router, react-intl, lucide-react, Vitest, Testing Library, Bun.

## Global Constraints

- Keep the change frontend-only unless a current test proves the existing backend contract is insufficient.
- Preserve fixed log sources `app`, `warn`, `error`, and `debug`; do not expose arbitrary file paths.
- Use “single-node cluster” wording for deployment shape when needed.
- Follow `web/DESIGN.md` conventions and existing `web/src/lib/navigation.ts` route patterns.

---

### Task 1: Navigation and Routing Contract

**Files:**
- Modify: `web/src/lib/navigation.test.ts`
- Modify: `web/src/app/router.test.tsx`
- Modify: `web/src/app/layout/sidebar-nav.test.tsx`
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/pages/app-logs/page.tsx`

**Interfaces:**
- Consumes: existing `AppLogsPage`, `AppLogsPanel`, `getApplicationLogSources`, `getApplicationLogEntries`, and `streamApplicationLogEntries`.
- Produces: `/cluster/system-logs` route, `nav.systemLogs.*` i18n keys, and legacy `/app-logs` redirect to `/cluster/system-logs`.

- [x] **Step 1: Write failing frontend tests**

Add expectations that:

```ts
expect(pageMetadata.get("/cluster/system-logs")?.titleMessageId).toBe("nav.systemLogs.title")
expect(legacyRouteRedirects["/app-logs"]).toBe("/cluster/system-logs")
expect(await screen.findByRole("heading", { name: "System Logs" })).toBeInTheDocument()
expect(await screen.findByRole("link", { name: "系统日志" })).toHaveAttribute("aria-current", "page")
```

- [x] **Step 2: Run tests to verify RED**

Run:

```bash
cd web && bun run test -- src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/app-logs/page.test.tsx
```

Expected: FAIL because `/cluster/system-logs`, `nav.systemLogs.*`, and the new title are not implemented.

- [x] **Step 3: Implement navigation, route, and copy**

Modify `web/src/lib/navigation.ts` to add `/cluster/system-logs` with a log-oriented lucide icon and `/app-logs` alias. Modify `web/src/app/router.tsx` to import `AppLogsPage`, register `cluster/system-logs`, and redirect legacy `app-logs` to the new route. Update i18n copy so `appLogs.title` is “System Logs” / “系统日志”.

- [x] **Step 4: Run focused tests to verify GREEN**

Run:

```bash
cd web && bun run test -- src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/app-logs/page.test.tsx
```

Expected: PASS.

- [x] **Step 5: Run typecheck, build, and diff check**

Run:

```bash
cd web && bunx tsc -b
cd web && bun run build
git diff --check
```

Expected: all commands exit 0. If `web/dist/index.html` changes only because of build hash churn, restore it before final status.

### Task 2: Terminal Console Log Surface

**Files:**
- Modify: `web/src/pages/app-logs/page.test.tsx`
- Modify: `web/src/pages/app-logs/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

**Interfaces:**
- Consumes: existing `ManagerApplicationLogEntry` fields: `time`, `level`, `module`, `caller`, `message`, `raw`, and `fields`.
- Produces: a dark terminal-style log output surface marked with `data-system-log-console="terminal"`.

- [x] **Step 1: Write the failing console-style test**

Assert that returned log lines render inside a terminal surface with dark background, mono text, a `Console` header, and level-specific colors for `INFO` and `WARN`.

- [x] **Step 2: Run test to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
```

Expected: FAIL because the old log list has no `data-system-log-console="terminal"` surface.

- [x] **Step 3: Implement the terminal log surface**

Update the non-empty log result branch in `AppLogsPanel` to render a dark console panel, a compact console header, scroll-bounded log body, stable time/level/message columns, and low-saturation level colors.

- [x] **Step 4: Run focused tests and final verification**

Run:

```bash
cd web && bun run test -- src/pages/app-logs/page.test.tsx
cd web && bun run test -- src/lib/navigation.test.ts src/app/router.test.tsx src/app/layout/sidebar-nav.test.tsx src/pages/app-logs/page.test.tsx src/pages/cluster/diagnostics/page.test.tsx
cd web && bunx tsc -b
cd web && bun run build
git diff --check
```

Expected: all commands exit 0. Restore `web/dist/index.html` if build hash churn is the only dist change.
