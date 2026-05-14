# Topology Readonly Page Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `/topology` placeholder with a read-only global cluster and Slot topology MVP backed by existing manager APIs.

**Architecture:** Keep the feature entirely in the web project. The page aggregates `GET /manager/overview`, `GET /manager/nodes`, and `GET /manager/slots` through the existing manager API client, derives a local view model, and renders summary cards, node topology cards, and a Slot placement matrix. No backend endpoint, write action, or channel-level topology is added.

**Tech Stack:** React 19, TypeScript, React Intl, Vitest, Testing Library, existing manager shell components.

---

## Pre-Execution Notes

- Execute implementation in an isolated worktree, not the dirty main worktree.
- Suggested branch/worktree:
  - branch: `feat/topology-readonly`
  - path: `.worktrees/topology-readonly`
- `ui/` is prototype-only and is out of scope.
- Read `AGENTS.md` before implementation.
- If a touched package has `FLOW.md`, read it first and update it only if the behavior description becomes stale.
- Use TDD: write the failing test, verify RED, implement, verify GREEN, then commit.
- Do not add `GET /manager/topology`.
- Do not add write controls such as rebalance, leader transfer, drain, repair, or onboarding.
- Do not claim channel-level replica topology; this MVP is global cluster + Slot topology only.

## File Structure

### Web page

- Create: `web/src/pages/topology/page.test.tsx`
  - Page-specific tests for summary rendering, node filter behavior, error mapping, and empty state.
- Modify: `web/src/pages/topology/page.tsx`
  - Replace placeholder with read-only topology page.
  - Load `getOverview()`, `getNodes()`, and `getSlots()` in parallel.
  - Derive local view model helpers in this file for MVP scope.
- Modify: `web/src/pages/page-shells.test.tsx`
  - Update `/topology` shell expectations from placeholder copy to implemented topology copy.

### i18n

- Modify: `web/src/i18n/messages/en.ts`
  - Replace placeholder topology copy and add topology MVP strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add matching Chinese copy.

### Docs

- Modify: `web/README.md`
  - Mark `/topology` implemented using overview/nodes/slots APIs.
- Modify: `docs/raw/web-admin-restructure.md`
  - Mark topology read-only MVP complete and keep channel-level topology as follow-up.

---

## Task 0: Create Isolated Worktree

**Files:**
- None.

- [ ] **Step 1: Confirm main worktree is dirty but unrelated**

Run:

```bash
git status --short --branch
```

Expected: output may include existing user changes unrelated to topology. Do not stage or revert them.

- [ ] **Step 2: Create the feature worktree**

Run:

```bash
git worktree add .worktrees/topology-readonly -b feat/topology-readonly
```

Expected: worktree created on branch `feat/topology-readonly`.

- [ ] **Step 3: Enter the worktree and verify branch**

Run:

```bash
cd .worktrees/topology-readonly
git branch --show-current
git status --short
```

Expected: branch is `feat/topology-readonly`; status is clean.

---

## Task 1: Implement Topology Page With TDD

**Files:**
- Create: `web/src/pages/topology/page.test.tsx`
- Modify: `web/src/pages/topology/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write the failing page tests**

Create `web/src/pages/topology/page.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { TopologyPage } from "@/pages/topology/page"

const getOverviewMock = vi.fn()
const getNodesMock = vi.fn()
const getSlotsMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getOverview: (...args: unknown[]) => getOverviewMock(...args),
    getNodes: (...args: unknown[]) => getNodesMock(...args),
    getSlots: (...args: unknown[]) => getSlotsMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getOverviewMock.mockReset()
  getNodesMock.mockReset()
  getSlotsMock.mockReset()
  useAuthStore.setState({
    ...createAnonymousAuthState(),
    isHydrated: true,
    status: "authenticated",
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [
      { resource: "cluster.overview", actions: ["r"] },
      { resource: "cluster.node", actions: ["r"] },
      { resource: "cluster.slot", actions: ["r"] },
    ],
  })
})

function renderTopologyPage() {
  return render(
    <I18nProvider>
      <TopologyPage />
    </I18nProvider>,
  )
}

function mockSuccessfulTopology() {
  getOverviewMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    cluster: { controller_leader_id: 1 },
    nodes: { total: 2, alive: 2, suspect: 0, dead: 0, draining: 0 },
    slots: {
      total: 3,
      ready: 2,
      quorum_lost: 1,
      leader_missing: 0,
      unreported: 0,
      peer_mismatch: 1,
      epoch_lag: 0,
    },
    tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: {
          count: 1,
          items: [{
            slot_id: 2,
            quorum: "lost",
            sync: "peer_mismatch",
            leader_id: 0,
            desired_peers: [1, 2],
            current_peers: [1],
            last_report_at: "2026-05-14T01:00:00Z",
          }],
        },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 1, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: 0, items: [] },
      },
    },
  })
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    controller_leader_id: 1,
    total: 2,
    items: [
      {
        node_id: 1,
        name: "wk-node-1",
        addr: "127.0.0.1:11110",
        status: "alive",
        last_heartbeat_at: "2026-05-14T01:00:00Z",
        is_local: true,
        capacity_weight: 1,
        membership: { role: "controller_voter", join_state: "active", schedulable: true },
        health: { status: "alive", last_heartbeat_at: "2026-05-14T01:00:00Z" },
        controller: { role: "leader", voter: true, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 2, leader_count: 1 },
        slots: { replica_count: 2, leader_count: 1, follower_count: 1, quorum_lost_count: 0, unreported_count: 0 },
        runtime: {
          node_id: 1,
          active_online: 8,
          closing_online: 0,
          total_online: 8,
          gateway_sessions: 5,
          sessions_by_listener: {},
          accepting_new_sessions: true,
          draining: false,
          unknown: false,
        },
      },
      {
        node_id: 2,
        name: "wk-node-2",
        addr: "127.0.0.1:11111",
        status: "alive",
        last_heartbeat_at: "2026-05-14T01:00:00Z",
        is_local: false,
        capacity_weight: 1,
        membership: { role: "data", join_state: "active", schedulable: true },
        health: { status: "alive", last_heartbeat_at: "2026-05-14T01:00:00Z" },
        controller: { role: "follower", voter: false, leader_id: 1, raft_health: "healthy" },
        slot_stats: { count: 2, leader_count: 1 },
        slots: { replica_count: 2, leader_count: 1, follower_count: 1, quorum_lost_count: 1, unreported_count: 0 },
      },
    ],
  })
  getSlotsMock.mockResolvedValueOnce({
    total: 3,
    items: [
      {
        slot_id: 1,
        hash_slots: { count: 2, items: [0, 1] },
        state: { quorum: "ready", sync: "synced" },
        assignment: { desired_peers: [1, 2], config_epoch: 3, balance_version: 1 },
        runtime: {
          current_peers: [1, 2],
          current_voters: [1, 2],
          leader_id: 1,
          healthy_voters: 2,
          has_quorum: true,
          observed_config_epoch: 3,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
      {
        slot_id: 2,
        hash_slots: { count: 1, items: [2] },
        state: { quorum: "lost", sync: "peer_mismatch" },
        assignment: { desired_peers: [1, 2], config_epoch: 4, balance_version: 1 },
        runtime: {
          current_peers: [1],
          current_voters: [1],
          leader_id: 0,
          healthy_voters: 1,
          has_quorum: false,
          observed_config_epoch: 4,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
      {
        slot_id: 3,
        hash_slots: null,
        state: { quorum: "ready", sync: "synced" },
        assignment: { desired_peers: [2], config_epoch: 5, balance_version: 1 },
        runtime: {
          current_peers: [2],
          current_voters: [2],
          leader_id: 2,
          healthy_voters: 1,
          has_quorum: true,
          observed_config_epoch: 5,
          last_report_at: "2026-05-14T01:00:00Z",
        },
      },
    ],
  })
}

test("renders topology summary, nodes, and slot matrix", async () => {
  mockSuccessfulTopology()

  renderTopologyPage()

  expect(await screen.findByRole("heading", { name: "Topology" })).toBeInTheDocument()
  expect(screen.getByText("Topology Summary")).toBeInTheDocument()
  expect(screen.getByText("Controller leader: 1")).toBeInTheDocument()
  expect(screen.getByText("Nodes: 2")).toBeInTheDocument()
  expect(screen.getByText("Slots: 3")).toBeInTheDocument()
  expect(screen.getByText("Slot anomalies: 3")).toBeInTheDocument()
  expect(screen.getByText("wk-node-1")).toBeInTheDocument()
  expect(screen.getByText("wk-node-2")).toBeInTheDocument()
  expect(screen.getByText("Slot Placement")).toBeInTheDocument()
  expect(screen.getByText("Slot 1")).toBeInTheDocument()
  expect(screen.getByText("Leader 1")).toBeInTheDocument()
  expect(screen.getByText("quorum lost")).toBeInTheDocument()
})

test("filters slot matrix by selected node", async () => {
  const user = userEvent.setup()
  mockSuccessfulTopology()

  renderTopologyPage()

  await screen.findByText("Slot 1")
  await user.selectOptions(screen.getByLabelText("Node filter"), "2")

  expect(screen.getByText("Showing 2 of 3 slots")).toBeInTheDocument()
  expect(screen.getByText("Slot 1")).toBeInTheDocument()
  expect(screen.getByText("Slot 3")).toBeInTheDocument()
  expect(screen.queryByText("Slot 2")).not.toBeInTheDocument()
})

test("maps forbidden and unavailable errors", async () => {
  getOverviewMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  getNodesMock.mockResolvedValueOnce({ generated_at: "", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  const { unmount } = renderTopologyPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getOverviewMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  getNodesMock.mockResolvedValueOnce({ generated_at: "", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  renderTopologyPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})

test("renders empty node and slot states", async () => {
  getOverviewMock.mockResolvedValueOnce({
    generated_at: "2026-05-14T01:00:00Z",
    cluster: { controller_leader_id: 0 },
    nodes: { total: 0, alive: 0, suspect: 0, dead: 0, draining: 0 },
    slots: { total: 0, ready: 0, quorum_lost: 0, leader_missing: 0, unreported: 0, peer_mismatch: 0, epoch_lag: 0 },
    tasks: { total: 0, pending: 0, retrying: 0, failed: 0 },
    anomalies: {
      slots: {
        quorum_lost: { count: 0, items: [] },
        leader_missing: { count: 0, items: [] },
        sync_mismatch: { count: 0, items: [] },
      },
      tasks: {
        failed: { count: 0, items: [] },
        retrying: { count: 0, items: [] },
      },
    },
  })
  getNodesMock.mockResolvedValueOnce({ generated_at: "2026-05-14T01:00:00Z", controller_leader_id: 0, total: 0, items: [] })
  getSlotsMock.mockResolvedValueOnce({ total: 0, items: [] })

  renderTopologyPage()

  expect(await screen.findByText("No cluster nodes are available for this topology view.")).toBeInTheDocument()
  expect(screen.getByText("No slots are available for this topology view.")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the page tests and verify RED**

Run:

```bash
cd web && bun run test -- src/pages/topology/page.test.tsx
```

Expected: FAIL because the page is still placeholder and the new test file expects implemented topology UI.

- [ ] **Step 3: Implement minimal topology page**

Modify `web/src/pages/topology/page.tsx`:

- Import:
  - React state/effect helpers.
  - `ManagerApiError`, `getOverview`, `getNodes`, `getSlots`.
  - `ManagerNode`, `ManagerOverviewResponse`, `ManagerSlot`, `ManagerSlotsResponse`, `ManagerNodesResponse`.
  - Existing `ResourceState`, `StatusBadge`, `PageContainer`, `PageHeader`, `SectionCard`, and `Button`/`Link` if needed.
- Add local state:
  - `overview`, `nodes`, `slots`, `loading`, `refreshing`, `error`, `selectedNodeId`.
- Add `loadTopology()`:
  - `Promise.all([getOverview(), getNodes(), getSlots()])`.
  - set all resources together.
- Add helper functions:
  - `mapErrorKind(error)`.
  - `formatNodeList(nodeIds)`.
  - `slotMentionsNode(slot, nodeId)`.
  - `nodeSlotSummary(node)`.
  - `topologyAnomalyCount(overview)`.
  - `slotQuorumLabel(slot)` returning text such as `quorum lost` when state is not ready.
- Render:
  - loading `ResourceState`.
  - error `ResourceState`.
  - summary cards.
  - node filter `<select aria-label={intl.formatMessage({ id: "topology.nodeFilter" })}>`.
  - node card grid.
  - Slot placement table.
  - empty states for no nodes or no filtered Slots.

Keep implementation simple. Do not extract new files during MVP unless the page becomes unmanageable.

- [ ] **Step 4: Add i18n strings**

Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`.

Recommended English strings:

```ts
"topology.description": "Read-only global cluster and Slot topology from existing manager APIs.",
"topology.scopeClusterGraph": "Scope: global cluster and Slot topology",
"topology.statusReadonly": "Status: read-only",
"topology.summary.title": "Topology Summary",
"topology.summary.description": "Controller, node, and Slot placement overview.",
"topology.controllerLeader": "Controller leader: {id}",
"topology.controllerLeader.empty": "Controller leader: -",
"topology.nodesValue": "Nodes: {count}",
"topology.slotsValue": "Slots: {count}",
"topology.anomaliesValue": "Slot anomalies: {count}",
"topology.nodeTopology.title": "Node Topology",
"topology.nodeTopology.description": "Cluster nodes with controller role and Slot placement counters.",
"topology.nodeFilter": "Node filter",
"topology.allNodes": "All nodes",
"topology.localNode": "local",
"topology.controllerVoter": "controller voter",
"topology.controllerNonVoter": "controller non-voter",
"topology.nodeSlots": "replicas {replicas} / leaders {leaders} / followers {followers}",
"topology.nodeRuntime": "sessions {sessions} / online {online}",
"topology.nodeRuntimeUnknown": "runtime unknown",
"topology.nodes.empty": "No cluster nodes are available for this topology view.",
"topology.slotPlacement.title": "Slot Placement",
"topology.slotPlacement.description": "Slot leader, desired peers, current peers, quorum, and sync status.",
"topology.filteredSlots": "Showing {shown} of {total} slots",
"topology.table.slot": "Slot",
"topology.table.leader": "Leader",
"topology.table.desiredPeers": "Desired peers",
"topology.table.currentPeers": "Current peers",
"topology.table.quorum": "Quorum",
"topology.table.sync": "Sync",
"topology.table.hashSlots": "Hash slots",
"topology.slotValue": "Slot {id}",
"topology.leaderValue": "Leader {id}",
"topology.leaderMissing": "Leader missing",
"topology.hashSlotCount": "{count} hash slots",
"topology.slots.empty": "No slots are available for this topology view.",
```

Replace or stop using old placeholder strings:

- `topology.statusNotExposed`
- `topology.coverageTitle`
- `topology.coverageDescription`
- `topology.coverageEmpty`

- [ ] **Step 5: Run the page tests and verify GREEN**

Run:

```bash
cd web && bun run test -- src/pages/topology/page.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit topology page**

Run:

```bash
git add web/src/pages/topology/page.tsx web/src/pages/topology/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: implement topology readonly page"
```

---

## Task 2: Update Shell Tests And Documentation

**Files:**
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/README.md`
- Modify: `docs/raw/web-admin-restructure.md`

- [ ] **Step 1: Update page shell expectations**

Modify `web/src/pages/page-shells.test.tsx`:

- Move `/topology` into the implemented shell cases.
- Expect `"Topology Summary"` in English.
- Expect `"拓扑摘要"` or the chosen Chinese equivalent in Chinese.
- Remove the unavailable manager scope test for `/topology`.

- [ ] **Step 2: Run shell tests and verify they pass**

Run:

```bash
cd web && bun run test -- src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 3: Update web README**

Modify `web/README.md` matrix:

```md
| `/topology` | `GET /manager/overview`, `GET /manager/nodes`, `GET /manager/slots` | Implemented |
| `/monitor`, `/settings/webhooks` | Requires follow-up read/write API design | Placeholder |
```

- [ ] **Step 4: Update restructure doc**

Modify `docs/raw/web-admin-restructure.md`:

- In the global cluster topology section, mark read-only MVP complete.
- In current status, add:

```md
- [x] 拓扑只读 MVP：复用 overview/nodes/slots 展示全局集群与 Slot 拓扑
```

- In pending items, keep channel-level replica topology or dedicated topology API as follow-up, not as the implemented MVP.

- [ ] **Step 5: Commit docs and shell test updates**

Run:

```bash
git add web/src/pages/page-shells.test.tsx web/README.md docs/raw/web-admin-restructure.md
git commit -m "docs: update topology page status"
```

---

## Task 3: Final Verification

**Files:**
- No new files.

- [ ] **Step 1: Run focused web tests**

Run:

```bash
cd web && bun run test -- src/pages/topology/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run production build**

Run:

```bash
cd web && bun run build
```

Expected: PASS. A Vite chunk size warning is acceptable if the build exits 0.

- [ ] **Step 3: Revert generated dist hash changes if needed**

If `web/dist/index.html` changes only because of a build hash, run:

```bash
git show HEAD:web/dist/index.html > web/dist/index.html
```

- [ ] **Step 4: Check worktree status**

Run:

```bash
git status --short
git log --oneline -8
```

Expected:

- No unintended generated files.
- Only committed topology changes.

- [ ] **Step 5: Use finishing branch flow**

Use `superpowers:finishing-a-development-branch`:

- Verify tests.
- Present merge/PR/keep/discard options.
- If merging locally, merge back to `main`, verify again on merged result, then delete the feature branch and worktree.
