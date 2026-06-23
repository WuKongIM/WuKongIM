# Plugin Uninstall UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a tested uninstall action to the existing web manager plugin page.

**Architecture:** Extend the manager API client with one DELETE helper, then
wire that helper into the existing `/cluster/plugins` table using the same
`ConfirmDialog` pattern already used for restart and binding deletion. Keep the
page single-node scoped and refresh the selected node inventory after mutation.

**Tech Stack:** React, React Intl, Vitest, Testing Library, Bun, existing
manager REST client.

---

## File Structure

- Modify `web/src/lib/manager-api.ts`: export `deleteNodePlugin`.
- Modify `web/src/lib/manager-api.test.ts`: verify DELETE URL and method.
- Modify `web/src/pages/plugins/page.tsx`: add uninstall state, action button,
  confirmation dialog, and refresh behavior.
- Modify `web/src/pages/plugins/page.test.tsx`: verify UI flow.
- Modify `web/src/i18n/messages/en.ts`: English uninstall labels.
- Modify `web/src/i18n/messages/zh-CN.ts`: Chinese uninstall labels.

## Task 1: Manager API Client

**Files:**

- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write the failing API test**

In `web/src/lib/manager-api.test.ts`, import `deleteNodePlugin` from
`@/lib/manager-api`. In the existing `fetches and mutates node plugins` test,
add one extra mocked response and assertion:

```ts
fetchMock.mockResolvedValueOnce(new Response("", { status: 204 }))

await expect(deleteNodePlugin(2, "wk.echo")).resolves.toBeUndefined()

expect(fetchMock).toHaveBeenNthCalledWith(
  5,
  "/manager/nodes/2/plugins/wk.echo",
  expect.objectContaining({ method: "DELETE", headers: expect.any(Headers) }),
)
```

- [ ] **Step 2: Run the API test to verify RED**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: fail because `deleteNodePlugin` is not exported.

- [ ] **Step 3: Implement minimal API client support**

Add this function next to `restartNodePlugin` in `web/src/lib/manager-api.ts`:

```ts
export async function deleteNodePlugin(nodeId: number, pluginNo: string) {
  await managerFetch(`/manager/nodes/${nodeId}/plugins/${encodeURIComponent(pluginNo)}`, {
    method: "DELETE",
  })
}
```

- [ ] **Step 4: Run the API test to verify GREEN**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts
```

Expected: pass.

## Task 2: Plugin Page Uninstall Flow

**Files:**

- Modify: `web/src/pages/plugins/page.tsx`
- Modify: `web/src/pages/plugins/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing page test**

In `web/src/pages/plugins/page.test.tsx`, mock `deleteNodePlugin` and add:

```ts
test("confirms plugin uninstall and refreshes inventory", async () => {
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 1, items: [pluginRow] })
  getNodePluginsMock.mockResolvedValueOnce({ node_id: 2, total: 0, items: [] })
  deleteNodePluginMock.mockResolvedValueOnce(undefined)

  const user = userEvent.setup()
  renderPluginsPage()

  expect(await screen.findByText("wk.echo")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Uninstall plugin wk.echo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Uninstall plugin" }))

  await waitFor(() => {
    expect(deleteNodePluginMock).toHaveBeenCalledWith(2, "wk.echo")
  })
  expect(getNodePluginsMock).toHaveBeenCalledTimes(2)
  expect(await screen.findByText("No data available.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the page test to verify RED**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx
```

Expected: fail because the uninstall button and mock do not exist.

- [ ] **Step 3: Implement minimal page support**

Add `deleteNodePlugin` to the page import list. Add state:

```ts
const [uninstallPlugin, setUninstallPlugin] = useState<ManagerPlugin | null>(null)
const [uninstallPending, setUninstallPending] = useState(false)
const [uninstallError, setUninstallError] = useState("")
```

Add a `confirmUninstall` handler:

```ts
const confirmUninstall = async () => {
  if (!uninstallPlugin || !selectedNodeId) {
    return
  }
  setUninstallPending(true)
  setUninstallError("")
  try {
    await deleteNodePlugin(selectedNodeId, uninstallPlugin.plugin_no)
    setUninstallPlugin(null)
    await loadPlugins(selectedNodeId, true)
  } catch (error) {
    setUninstallError(error instanceof Error ? error.message : "uninstall plugin failed")
  } finally {
    setUninstallPending(false)
  }
}
```

Add a row action button and a `ConfirmDialog` using new i18n ids:

```text
plugins.action.uninstall
plugins.action.uninstallPlugin
plugins.uninstall.title
plugins.uninstall.description
plugins.uninstall.confirm
```

- [ ] **Step 4: Run the page test to verify GREEN**

Run:

```bash
cd web && bun run test -- src/pages/plugins/page.test.tsx
```

Expected: pass.

## Task 3: Focused Verification

**Files:**

- Verify modified web files.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/plugins/page.test.tsx
```

Expected: pass.

- [ ] **Step 2: Run web build**

Run:

```bash
cd web && bun run build
```

Expected: pass.

- [ ] **Step 3: Check formatting-sensitive git diff**

Run:

```bash
git diff --check
```

Expected: no output and exit code 0.
