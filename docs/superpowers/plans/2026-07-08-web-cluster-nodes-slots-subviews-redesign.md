# Web Cluster Nodes And Slots Subviews Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `/cluster/nodes` and `/cluster/slots` secondary surfaces into the approved editorial console visual language without changing cluster operations behavior.

**Architecture:** Keep changes local to the existing nodes and slots page modules and their focused tests. Preserve the current visible List/Logs tabs, list workbenches, manager API calls, action gates, dialogs, and detail behavior while adding stable subview surface markers, compact overview strips, and accessible unhealthy tables.

**Tech Stack:** Vite, React 19, TypeScript, Tailwind CSS v4, React Intl, Vitest, Testing Library, Bun.

---

## File Structure

- Modify: `web/src/pages/nodes/page.tsx`
  - Adds `data-node-surface="detail"` around the node detail `KeyValueList`.
  - Adds named compact surfaces for node overview unhealthy/runtime content.
  - Adds named and accessible surfaces for the node unhealthy table.
- Test: `web/src/pages/nodes/page.test.tsx`
  - Imports and renders exported node subview panels directly.
  - Adds surface assertions for node detail, overview, and unhealthy panels.
  - Keeps existing list, lifecycle, slot-move, controller-voter, controller-raft, error, and empty-state behavior tests intact.
- Modify: `web/src/pages/slots/page.tsx`
  - Adds a local `SlotSummaryCell` helper for the slots overview strip.
  - Adds `data-slot-surface="detail"` around the slot detail `KeyValueList`.
  - Tightens rebalance and batch transfer result surfaces.
  - Replaces slots overview summary cards with a compact strip.
  - Adds named and accessible surfaces for the slot unhealthy table.
- Test: `web/src/pages/slots/page.test.tsx`
  - Imports and renders exported slot subview panels directly.
  - Adds surface assertions for slot detail, operation results, overview, and unhealthy panels.
  - Keeps existing inventory, node filter, add/remove/recover/transfer/rebalance/batch-transfer, log, error, and empty-state behavior tests intact.
- Do not modify: backend manager APIs, route definitions, auth rules, i18n messages, `cluster-dashboard`, `business-dashboard`, topology, channels, diagnostics, monitor, business pages, or system pages.

## Task 1: Node Detail, Overview, And Unhealthy Surfaces

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Test: `web/src/pages/nodes/page.test.tsx`

- [ ] **Step 1: Write the failing node surface tests**

In `web/src/pages/nodes/page.test.tsx`, replace the page import:

```tsx
import { NodesPage } from "@/pages/nodes/page"
```

with:

```tsx
import {
  NodeClusterOverviewPanel,
  NodeClusterUnhealthyPanel,
  NodesPage,
} from "@/pages/nodes/page"
```

Add this test after `renders layered node inventory fields and slot move row actions`:

```tsx
test("marks node detail as a compact inspection surface", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)

  const user = userEvent.setup()
  renderNodesPage()

  expect(await screen.findByText("127.0.0.1:7000")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect node 1" }))

  const detailSurface = (await screen.findByText("Hosted IDs")).closest("[data-node-surface='detail']")
  expect(detailSurface).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(detailSurface).toHaveTextContent("127.0.0.1:7000")
  expect(detailSurface).toHaveTextContent("1, 2, 3")
})
```

Add this test near the other node page chrome tests:

```tsx
test("marks node overview secondary surfaces", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })

  render(
    <I18nProvider>
      <NodeClusterOverviewPanel />
    </I18nProvider>,
  )

  const summaryStrip = await screen.findByTestId("nodes-summary-strip")
  expect(summaryStrip).toBeInTheDocument()

  const unhealthySurface = screen
    .getByText("Unhealthy: 0; runtime unknown: 0; draining: 0.")
    .closest("[data-node-surface='overview-unhealthy']")
  expect(unhealthySurface).toHaveClass("rounded-md", "border", "border-border", "bg-muted/30")

  const runtimeSurface = screen.getByText("Gateway sessions").closest("[data-node-surface='overview-runtime']")
  expect(runtimeSurface).toHaveClass("grid", "gap-3", "sm:grid-cols-3")
  expect(runtimeSurface).toHaveTextContent("5")
  expect(runtimeSurface).toHaveTextContent("4")
  expect(runtimeSurface).toHaveTextContent("0")
})
```

Add this test near the overview test:

```tsx
test("marks node unhealthy rows as an accessible incident table", async () => {
  const unhealthyNode = {
    ...nodeRow,
    node_id: 2,
    name: "node-2",
    addr: "127.0.0.1:7002",
    status: "dead",
    health: { status: "dead", last_heartbeat_at: "2026-04-23T08:00:00Z" },
    runtime: { ...nodeRow.runtime, unknown: true },
  }
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [unhealthyNode],
  })

  render(
    <I18nProvider>
      <NodeClusterUnhealthyPanel />
    </I18nProvider>,
  )

  const table = await screen.findByRole("table", { name: "Unhealthy Nodes" })
  const panel = table.closest("[data-node-surface='unhealthy']")
  const tableSurface = table.closest("[data-node-surface='unhealthy-table']")

  expect(panel).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(tableSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(table).toHaveClass("w-full", "border-collapse", "text-sm")
  expect(within(table).getByText("127.0.0.1:7002")).toBeInTheDocument()
  expect(within(table).getByText("runtime unknown")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run node tests to verify they fail**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx -t "marks node"
```

Expected: FAIL because `data-node-surface="detail"`, `overview-unhealthy`, `overview-runtime`, `unhealthy`, `unhealthy-table`, and the unhealthy table accessible label are not present yet.

- [ ] **Step 3: Add the node detail surface**

In `web/src/pages/nodes/page.tsx`, replace the detail content wrapper:

```tsx
<div className="space-y-4">
  <KeyValueList
    items={[
```

with:

```tsx
<div className="rounded-md border border-border bg-card p-3" data-node-surface="detail">
  <KeyValueList
    items={[
```

Keep the entire `KeyValueList` item array unchanged.

- [ ] **Step 4: Add node overview surface markers and compact radii**

In `NodeClusterOverviewPanel`, replace the unhealthy breakdown wrapper:

```tsx
<div className="rounded-lg border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground">
```

with:

```tsx
<div
  className="rounded-md border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground"
  data-node-surface="overview-unhealthy"
>
```

In the same panel, replace the runtime grid opening:

```tsx
<div className="grid gap-3 sm:grid-cols-3">
```

with:

```tsx
<div className="grid gap-3 sm:grid-cols-3" data-node-surface="overview-runtime">
```

Then replace each runtime metric card class:

```tsx
className="rounded-lg border border-border bg-muted/20 px-3 py-3"
```

with:

```tsx
className="rounded-md border border-border bg-muted/20 px-3 py-3"
```

Do not change the summary strip, metric labels, metric values, refresh button, loading state, retry behavior, or error mapping.

- [ ] **Step 5: Add node unhealthy panel and table surfaces**

In `NodeClusterUnhealthyPanel`, replace the loaded panel wrapper:

```tsx
<div className="rounded-xl border border-border bg-card p-3 shadow-none">
```

with:

```tsx
<div className="rounded-md border border-border bg-card p-3 shadow-none" data-node-surface="unhealthy">
```

Replace the table wrapper and table opening:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-node-surface="unhealthy-table">
  <table aria-label={intl.formatMessage({ id: "nodes.unhealthy.title" })} className="w-full border-collapse text-sm">
```

Do not change columns, row mapping, row values, empty state, loading state, retry behavior, or error mapping.

- [ ] **Step 6: Run focused node tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx
```

Expected: PASS. Existing tests must still cover hidden overview/unhealthy tabs, lifecycle action gates, slot move actions, controller-voter promotion, controller-raft links, loading, empty, and error behavior.

- [ ] **Step 7: Commit node subview change**

Run:

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx
git commit -m "style(web): refine node subview surfaces"
```

Expected: commit succeeds with only nodes page and nodes test changes.

## Task 2: Slot Detail, Results, Overview, And Unhealthy Surfaces

**Files:**
- Modify: `web/src/pages/slots/page.tsx`
- Test: `web/src/pages/slots/page.test.tsx`

- [ ] **Step 1: Write the failing slot surface tests**

In `web/src/pages/slots/page.test.tsx`, replace the Testing Library import:

```tsx
import { render, screen } from "@testing-library/react"
```

with:

```tsx
import { render, screen, within } from "@testing-library/react"
```

Replace the page import:

```tsx
import { SlotsPage } from "@/pages/slots/page"
```

with:

```tsx
import {
  SlotClusterOverviewPanel,
  SlotClusterUnhealthyPanel,
  SlotsPage,
} from "@/pages/slots/page"
```

Add this test after `adds a physical slot and opens the returned detail`:

```tsx
test("marks slot detail as a compact inspection surface", async () => {
  getSlotsMock.mockResolvedValueOnce({ total: 1, items: [slotRow] })
  getSlotMock.mockResolvedValueOnce(slotDetail)

  const user = userEvent.setup()
  renderSlotsPage()

  expect(await screen.findByText("Slot 9")).toBeInTheDocument()
  await user.click(screen.getByRole("button", { name: "Inspect slot 9" }))

  const detailSurface = (await screen.findByText("Desired peers")).closest("[data-slot-surface='detail']")
  expect(detailSurface).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(detailSurface).toHaveTextContent("1, 2, 3")
  expect(detailSurface).toHaveTextContent("temporary failure")
  expect(screen.getByRole("button", { name: "Transfer leader" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Recover slot" })).toBeEnabled()
  expect(screen.getByRole("button", { name: "Remove slot" })).toBeEnabled()
})
```

In the existing `marks slot inventory and operation results as editorial workbench surfaces` test, keep the inventory assertions unchanged and replace the rebalance surface assertions with:

```tsx
  expect(rebalanceSurface).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(rebalanceSurface).not.toHaveClass("rounded-xl")
  expect(rebalanceSurface).not.toHaveClass("rounded-lg")
```

In the existing `previews and executes a batch slot leader transfer plan` test, add these assertions after `expect(await screen.findByText("Created 1 · Existing 0 · Failed 0")).toBeInTheDocument()`:

```tsx
  const batchSurface = screen
    .getByText("Created 1 · Existing 0 · Failed 0")
    .closest("[data-slot-surface='batch-transfer-result']")
  expect(batchSurface).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(batchSurface).not.toHaveClass("rounded-xl")
  expect(batchSurface).not.toHaveClass("rounded-lg")
```

Add this test near the slot page chrome tests:

```tsx
test("marks slot overview secondary surfaces", async () => {
  render(
    <I18nProvider>
      <SlotClusterOverviewPanel />
    </I18nProvider>,
  )

  const summaryStrip = await screen.findByTestId("slots-overview-summary-strip")
  expect(summaryStrip).toHaveClass("grid", "gap-0", "border-y", "border-border")
  expect(summaryStrip).toHaveTextContent("Total slots")
  expect(summaryStrip).toHaveTextContent("Ready slots")
  expect(summaryStrip).toHaveTextContent("1")

  const unhealthySurface = screen
    .getByText("Quorum lost: 0; leader missing: 0; sync mismatch: 0.")
    .closest("[data-slot-surface='overview-unhealthy']")
  expect(unhealthySurface).toHaveClass("rounded-md", "border", "border-border", "bg-muted/30")

  const runtimeSurface = screen.getByText("Epoch lag").closest("[data-slot-surface='overview-runtime']")
  expect(runtimeSurface).toHaveClass("grid", "gap-3", "sm:grid-cols-3")
  expect(runtimeSurface).toHaveTextContent("Retrying tasks")
  expect(runtimeSurface).toHaveTextContent("Failed tasks")
})
```

Add this test near the overview test:

```tsx
test("marks slot unhealthy rows as an accessible incident table", async () => {
  getOverviewMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 1, alive: 1, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 1,
      ready: 0,
      quorum_lost: 1,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 0,
      epoch_lag: 0,
    },
    tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: {
          count: 1,
          items: [{
            slot_id: 9,
            quorum: "lost",
            sync: "in_sync",
            desired_peers: [1, 2, 3],
            current_peers: [1, 2],
            leader_id: 0,
            last_report_at: "2026-04-23T08:00:00Z",
          }],
        },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 0, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: 0, items: [] },
      },
    },
  })

  render(
    <I18nProvider>
      <SlotClusterUnhealthyPanel />
    </I18nProvider>,
  )

  const table = await screen.findByRole("table", { name: "Unhealthy Slots" })
  const panel = table.closest("[data-slot-surface='unhealthy']")
  const tableSurface = table.closest("[data-slot-surface='unhealthy-table']")

  expect(panel).toHaveClass("rounded-md", "border", "border-border", "bg-card", "p-3")
  expect(tableSurface).toHaveClass("overflow-x-auto", "rounded-md", "border", "border-border")
  expect(table).toHaveClass("w-full", "border-collapse", "text-sm")
  expect(within(table).getByText("Slot 9")).toBeInTheDocument()
  expect(within(table).getByText("Quorum lost")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run slot tests to verify they fail**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx -t "marks slot"
```

Expected: FAIL because `data-slot-surface="detail"`, `slots-overview-summary-strip`, `overview-unhealthy`, `overview-runtime`, `unhealthy-table`, the unhealthy table accessible label, and compact result classes are not present yet.

- [ ] **Step 3: Add a local slot summary cell helper**

In `web/src/pages/slots/page.tsx`, add this helper after `formatTimestamp`:

```tsx
function SlotSummaryCell({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="border-b border-border px-1 py-3 sm:px-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-2xl font-medium tabular-nums text-foreground">{value}</div>
    </div>
  )
}
```

Do not export it and do not share it with the nodes page in this batch.

- [ ] **Step 4: Add the slot detail surface**

In `web/src/pages/slots/page.tsx`, replace the loaded detail fragment:

```tsx
{!detailLoading && !detailError && detail ? (
  <>
    <KeyValueList
      items={[
```

with:

```tsx
{!detailLoading && !detailError && detail ? (
  <div className="rounded-md border border-border bg-card p-3" data-slot-surface="detail">
    <KeyValueList
      items={[
```

Replace the matching closing fragment:

```tsx
    />
  </>
) : null}
```

with:

```tsx
    />
  </div>
) : null}
```

Keep the entire `KeyValueList` item array and detail footer actions unchanged.

- [ ] **Step 5: Tighten slot operation result surfaces**

In the rebalance result block, replace the wrapper:

```tsx
<div data-slot-surface="rebalance-result" className="rounded-lg border border-border bg-card p-3">
```

with:

```tsx
<div data-slot-surface="rebalance-result" className="rounded-md border border-border bg-card p-3">
```

In the same block, replace each result item shell:

```tsx
<div className="rounded-lg border border-border bg-muted/20 px-3 py-3" key={item.hash_slot}>
```

with:

```tsx
<div className="rounded-md border border-border bg-muted/20 px-3 py-3" key={item.hash_slot}>
```

In the batch transfer result block, replace the wrapper:

```tsx
<div data-slot-surface="batch-transfer-result" className="rounded-lg border border-border bg-card p-3">
```

with:

```tsx
<div data-slot-surface="batch-transfer-result" className="rounded-md border border-border bg-card p-3">
```

In the same block, replace each result item shell:

```tsx
<div className="rounded-lg border border-border bg-muted/20 px-3 py-2" key={item.slot_id}>
```

with:

```tsx
<div className="rounded-md border border-border bg-muted/20 px-3 py-2" key={item.slot_id}>
```

Do not change plan/result data, mutation calls, dialog state, or result text.

- [ ] **Step 6: Replace slot overview cards with a compact strip**

In `SlotClusterOverviewPanel`, replace the six-card summary section:

```tsx
<section className="grid gap-3 md:grid-cols-2 xl:grid-cols-6">
  <SectionCard title={intl.formatMessage({ id: "slots.metric.total" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.total}</div>
  </SectionCard>
  <SectionCard title={intl.formatMessage({ id: "slots.cards.ready.title" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.ready}</div>
  </SectionCard>
  <SectionCard title={intl.formatMessage({ id: "slots.metric.quorumLost" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.quorum_lost}</div>
  </SectionCard>
  <SectionCard title={intl.formatMessage({ id: "slots.metric.leaderMissing" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.leader_missing}</div>
  </SectionCard>
  <SectionCard title={intl.formatMessage({ id: "slots.metric.peerMismatch" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.peer_mismatch}</div>
  </SectionCard>
  <SectionCard title={intl.formatMessage({ id: "slots.metric.unreported" })}>
    <div className="text-3xl font-semibold text-foreground">{overview.slots.unreported}</div>
  </SectionCard>
</section>
```

with:

```tsx
<div
  className="grid gap-0 border-y border-border sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6"
  data-testid="slots-overview-summary-strip"
>
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.total" })} value={overview.slots.total} />
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.cards.ready.title" })} value={overview.slots.ready} />
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.quorumLost" })} value={overview.slots.quorum_lost} />
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.leaderMissing" })} value={overview.slots.leader_missing} />
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.peerMismatch" })} value={overview.slots.peer_mismatch} />
  <SlotSummaryCell label={intl.formatMessage({ id: "slots.metric.unreported" })} value={overview.slots.unreported} />
</div>
```

This intentionally mirrors the existing node summary strip without introducing a shared component.

- [ ] **Step 7: Add slot overview surface markers and compact radii**

In `SlotClusterOverviewPanel`, replace the unhealthy breakdown wrapper:

```tsx
<div className="rounded-lg border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground">
```

with:

```tsx
<div
  className="rounded-md border border-border bg-muted/30 px-3 py-3 text-sm leading-6 text-muted-foreground"
  data-slot-surface="overview-unhealthy"
>
```

In the same panel, replace the runtime grid opening:

```tsx
<div className="grid gap-3 sm:grid-cols-3">
```

with:

```tsx
<div className="grid gap-3 sm:grid-cols-3" data-slot-surface="overview-runtime">
```

Then replace each runtime metric card class:

```tsx
className="rounded-lg border border-border bg-muted/20 px-3 py-3"
```

with:

```tsx
className="rounded-md border border-border bg-muted/20 px-3 py-3"
```

Do not change overview loading, refresh, retry, error handling, anomaly counts, task counts, or metric values.

- [ ] **Step 8: Add slot unhealthy table surface**

In `SlotClusterUnhealthyPanel`, replace the loaded panel wrapper:

```tsx
<div data-slot-surface="unhealthy" className="rounded-lg border border-border bg-card p-3">
```

with:

```tsx
<div data-slot-surface="unhealthy" className="rounded-md border border-border bg-card p-3">
```

Replace the table wrapper and table opening:

```tsx
<div className="overflow-x-auto rounded-lg border border-border">
  <table className="w-full border-collapse">
```

with:

```tsx
<div className="overflow-x-auto rounded-md border border-border" data-slot-surface="unhealthy-table">
  <table aria-label={intl.formatMessage({ id: "slots.unhealthy.title" })} className="w-full border-collapse text-sm">
```

Do not change row mapping, anomaly reason labels, row values, empty state, loading state, retry behavior, or error mapping.

- [ ] **Step 9: Run focused slot tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/slots/page.test.tsx
```

Expected: PASS. Existing tests must still cover hidden overview/unhealthy tabs, inventory density, node filtering, add/remove/recover/transfer/rebalance/batch-transfer behavior, loading, empty, and error states.

- [ ] **Step 10: Commit slot subview change**

Run:

```bash
git add web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx
git commit -m "style(web): refine slot subview surfaces"
```

Expected: commit succeeds with only slots page and slots test changes.

## Task 3: Integrated Verification And Source Cleanup

**Files:**
- Check: `web/src/pages/nodes/page.tsx`
- Check: `web/src/pages/nodes/page.test.tsx`
- Check: `web/src/pages/slots/page.tsx`
- Check: `web/src/pages/slots/page.test.tsx`
- Check: `web/dist/index.html`

- [ ] **Step 1: Run focused frontend tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx src/pages/slots/page.test.tsx
```

Expected: PASS for both test files.

- [ ] **Step 2: Run TypeScript verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: command exits 0 with no TypeScript errors.

- [ ] **Step 3: Run production build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: command exits 0. A Vite chunk-size warning is acceptable if it matches the existing warning pattern from prior web batches.

- [ ] **Step 4: Restore generated dist hash churn if needed**

Run:

```bash
git status --short web/dist/index.html
```

If this prints a modified `web/dist/index.html` and no source file intentionally changed build assets, run:

```bash
git restore web/dist/index.html
```

Expected: generated asset-hash churn is not left in the source diff.

- [ ] **Step 5: Run whitespace check**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

- [ ] **Step 6: Inspect final source diff**

Run:

```bash
git diff --stat
git diff -- web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/pages/slots/page.tsx web/src/pages/slots/page.test.tsx
```

Expected: diff only contains the approved secondary-surface redesign changes. There are no backend, route, auth, i18n, dashboard, topology, channel, diagnostics, monitor, business, or system-page changes.

- [ ] **Step 7: Confirm git state**

Run:

```bash
git status --short --branch
```

Expected: local `main` contains the new source commits. The pre-existing untracked `docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md` may still appear and must remain untouched unless the user explicitly asks to handle it.
