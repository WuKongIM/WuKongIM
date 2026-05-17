# Web Plugin Management Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `/cluster/plugins` admin page for node-local plugin inventory, config editing, restart, and UID binding management.

**Architecture:** Extend the existing manager API client and shell routing, then add one focused page under `web/src/pages/plugins`. The page follows existing `PageContainer`, `PageHeader`, `NodeFilter`, `ResourceState`, `DetailSheet`, `ActionFormDialog`, and `ConfirmDialog` patterns and keeps plugin uninstall out of the UI.

**Tech Stack:** React 19, React Router, React Intl, Vitest, Testing Library, existing manager REST APIs.

---

## File Structure

- Modify `web/src/lib/manager-api.types.ts`: manager plugin DTOs and request/response types.
- Modify `web/src/lib/manager-api.ts`: URL builders and exported plugin API functions.
- Modify `web/src/lib/manager-api.test.ts`: API path/body coverage.
- Modify `web/src/lib/navigation.ts`: Cluster Plugins nav item and metadata.
- Modify `web/src/app/router.tsx`: add `/cluster/plugins` route.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: English and Chinese copy.
- Create `web/src/pages/plugins/page.tsx`: plugin management page.
- Create `web/src/pages/plugins/page.test.tsx`: page behavior tests.
- Modify `web/src/pages/page-shells.test.tsx`: route shell and i18n coverage.
- Modify `web/README.md`: page/API matrix entry.

Do not modify non-web files except plan/spec docs. Preserve unrelated dirty files.

## Task 1: Manager API Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Test: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API tests**

Add tests that call:

```ts
await getNodePlugins(2)
await getNodePlugin(2, "wk.echo")
await updateNodePluginConfig(2, "wk.echo", { api_key: "******" })
await restartNodePlugin(2, "wk.echo")
await getPluginBindings({ uid: "u1" })
await getPluginBindings({ pluginNo: "wk.echo", limit: 25, cursor: "abc" })
await createPluginBinding({ uid: "u1", pluginNo: "wk.echo" })
await deletePluginBinding({ uid: "u1", pluginNo: "wk.echo" })
```

Assert these paths/bodies:

```text
GET    /manager/nodes/2/plugins
GET    /manager/nodes/2/plugins/wk.echo
PUT    /manager/nodes/2/plugins/wk.echo/config      {"api_key":"******"}
POST   /manager/nodes/2/plugins/wk.echo/restart
GET    /manager/plugin-bindings?uid=u1
GET    /manager/plugin-bindings?plugin_no=wk.echo&limit=25&cursor=abc
POST   /manager/plugin-bindings                     {"uid":"u1","plugin_no":"wk.echo"}
DELETE /manager/plugin-bindings                     {"uid":"u1","plugin_no":"wk.echo"}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`

Expected: fail because plugin API functions/types are not exported.

- [ ] **Step 3: Implement minimal API client support**

Add plugin DTO types, `buildPluginBindingsPath`, and exported functions. Encode `pluginNo` with `encodeURIComponent` for node plugin detail/mutations. Keep config as `Record<string, unknown>`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat(web): add plugin manager api client"
```

## Task 2: Route, Navigation, and Shell Copy

**Files:**
- Modify: `web/src/lib/navigation.ts`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Modify: `web/src/pages/page-shells.test.tsx`
- Test later page placeholder from Task 3 if needed.

- [ ] **Step 1: Write failing shell tests**

Update `page-shells.test.tsx` expectations so `/cluster/plugins` renders:

```text
English heading: Plugins
English section text: Node plugin inventory
English path label: CLUSTER / PLUGINS
Chinese heading: 插件管理
Chinese section text: 节点插件清单
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && bun run test -- src/pages/page-shells.test.tsx`

Expected: fail because route/nav/page do not exist yet.

- [ ] **Step 3: Add route metadata and i18n messages**

Add Cluster nav item with an icon such as `Puzzle`, path label messages, page messages, action labels, table labels, dialogs, errors, and binding labels.

- [ ] **Step 4: Run test after Task 3 page exists**

This task depends on a page export. If a temporary placeholder is necessary, keep it minimal and replace in Task 3.

## Task 3: Plugin Page Read Model

**Files:**
- Create: `web/src/pages/plugins/page.tsx`
- Create: `web/src/pages/plugins/page.test.tsx`
- Modify: `web/src/app/router.tsx`

- [ ] **Step 1: Write failing page read tests**

Mock `getNodes`, `getNodePlugins`, and `getNodePlugin`. Verify:

- Default local node is selected.
- Plugin rows render plugin number, name/version, status, methods, priority, PID, and last error.
- Summary cards show total, running, failed, and enabled counts.
- Detail button opens detail sheet with config/template/runtime fields.
- `403`, `501`, and `503` map to resource states.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx`

Expected: fail because page does not exist.

- [ ] **Step 3: Implement minimal read-only page**

Use `NodeFilter`, `PageContainer`, `PageHeader`, `SectionCard`, `StatusBadge`, `ResourceState`, `DetailSheet`, and table markup matching existing pages.

- [ ] **Step 4: Run read tests**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx src/pages/page-shells.test.tsx`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx web/src/app/router.tsx web/src/lib/navigation.ts web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts web/src/pages/page-shells.test.tsx
git commit -m "feat(web): add plugin management page shell"
```

## Task 4: Config Update and Restart Actions

**Files:**
- Modify: `web/src/pages/plugins/page.tsx`
- Modify: `web/src/pages/plugins/page.test.tsx`

- [ ] **Step 1: Write failing action tests**

Verify:

- Configure opens a dialog prefilled with formatted JSON.
- Invalid JSON shows local validation and does not call the API.
- Non-object JSON shows local validation and does not call the API.
- Valid JSON calls `updateNodePluginConfig(nodeId, pluginNo, object)` and refreshes list/detail.
- Restart opens confirmation, calls `restartNodePlugin`, and refreshes list/detail.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx`

Expected: fail because actions are absent.

- [ ] **Step 3: Implement minimal actions**

Use `ActionFormDialog` for config and `ConfirmDialog` for restart. Keep uninstall hidden/out of scope.

- [ ] **Step 4: Run tests**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx
git commit -m "feat(web): manage plugin config and restart"
```

## Task 5: Plugin Binding Management

**Files:**
- Modify: `web/src/pages/plugins/page.tsx`
- Modify: `web/src/pages/plugins/page.test.tsx`

- [ ] **Step 1: Write failing binding tests**

Verify:

- Searching by UID calls `getPluginBindings({ uid })`.
- Searching by plugin number calls `getPluginBindings({ pluginNo, limit: 50 })`.
- Empty selector input shows local validation.
- Add binding dialog calls `createPluginBinding({ uid, pluginNo })` and refreshes current search.
- Delete binding confirmation calls `deletePluginBinding({ uid, pluginNo })` and refreshes current search.
- Binding row warnings render when present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx`

Expected: fail because binding UI is absent.

- [ ] **Step 3: Implement binding panel**

Add a `SectionCard` below inventory with selector mode, query input, result table, add dialog, and delete confirmation.

- [ ] **Step 4: Run tests**

Run: `cd web && bun run test -- src/pages/plugins/page.test.tsx`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/plugins/page.tsx web/src/pages/plugins/page.test.tsx
git commit -m "feat(web): manage plugin bindings"
```

## Task 6: Docs and Full Verification

**Files:**
- Modify: `web/README.md`

- [ ] **Step 1: Update README matrix**

Add `/cluster/plugins` row listing plugin manager APIs and status Implemented.

- [ ] **Step 2: Run focused tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/plugins/page.test.tsx src/pages/page-shells.test.tsx src/lib/navigation.test.ts src/app/router.test.tsx
```

Expected: pass.

- [ ] **Step 3: Run full web test and build**

Run:

```bash
cd web && bun run test
cd web && bun run build
```

Expected: pass.

- [ ] **Step 4: Commit docs/verification updates**

```bash
git add web/README.md
git commit -m "docs(web): document plugin management page"
```
