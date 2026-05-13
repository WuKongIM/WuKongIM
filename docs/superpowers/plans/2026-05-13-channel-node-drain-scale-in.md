# Channel Node Drain Scale-in Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend manager-driven node scale-in so a draining node cannot become `ready_to_remove` while it still owns channel leaders, channel replicas, or active channel migration work.

**Architecture:** Keep NodeScaleIn as the single operator workflow. Add authoritative channel inventory scans and bounded channel migration task creation behind `PlanNodeScaleIn`, `GetNodeScaleInStatus`, and `AdvanceNodeScaleIn`; reuse existing `ChannelRuntimeMeta` and `ChannelMigrationTask` safety primitives. Keep manager HTTP thin and update the web scale-in report to display channel drain progress.

**Tech Stack:** Go management usecases, slot proxy/meta stores, Gin manager adapters, React/Vitest manager UI, `GOWORK=off go test`, `yarn test`.

---

## Reference Documents

- Spec: `docs/superpowers/specs/2026-05-13-channel-node-drain-scale-in-design.md`
- Existing scale-in design: `docs/superpowers/specs/2026-04-27-management-node-scale-in-design.md`
- Channel migration design: `docs/superpowers/specs/2026-05-11-channel-replica-migration-design.md`

Use `@superpowers:test-driven-development` for each implementation task, `@superpowers:systematic-debugging` for failures, and `@superpowers:verification-before-completion` before claiming the plan is complete.

## File Structure

- `pkg/slot/proxy/channel_migration_rpc.go`
  - Add an authoritative active-task scan API that manager scale-in can use from any node.
- `pkg/slot/proxy/channel_migration_codec.go`
  - Extend the channel migration RPC binary codec for the new list-active operation fields.
- `pkg/slot/proxy/channel_migration_rpc_test.go`
  - Cover active task scan routing, node filtering, and pagination/limit behavior.
- `pkg/slot/FLOW.md`
  - Document the new channel migration active-task scan API because package flow/API changed.
- `internal/usecase/management/app.go`
  - Extend `ChannelMigrationStore` with the new active-task scan method.
- `internal/usecase/management/channel_migration_test.go`
  - Update existing fake migration store to satisfy the expanded interface.
- `internal/usecase/management/node_scalein.go`
  - Add statuses, request fields, progress/check fields, status ordering, and advance branch hooks.
- `internal/usecase/management/node_scalein_channel.go`
  - New focused file for channel inventory scanning, active-task accounting, candidate selection, and bounded task creation helpers.
- `internal/usecase/management/node_scalein_test.go`
  - Add TDD coverage for channel counters, fail-closed inventory, status ordering, and advance behavior.
- `internal/access/manager/node_scalein.go`
  - Expose new request/response fields and `next_action` mappings.
- `internal/access/manager/node_scalein_test.go`
  - Cover JSON fields, request clamping, and new status actions.
- `internal/app/build.go`
  - Confirm existing `ChannelMigration: app.store` wiring satisfies the expanded interface; update only if compile requires it.
- `internal/app/*test.go`
  - Add or update wiring tests if the expanded interface creates a regression risk.
- `web/src/lib/manager-api.types.ts`
  - Add new scale-in progress/check fields and `maxChannelMigrations` input.
- `web/src/lib/manager-api.ts`
  - Send `max_channel_migrations` in advance requests.
- `web/src/lib/manager-api.test.ts`
  - Cover request/response shape.
- `web/src/pages/nodes/page.tsx`
  - Display channel counters and inventory status in the existing scale-in report.
- `web/src/pages/nodes/page.test.tsx`
  - Cover visible channel counters and advance request.
- `web/src/i18n/messages/en.ts`
  - Add scale-in channel labels.
- `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese scale-in channel labels.

## Task 1: Add Authoritative Active Channel Migration Task Scan

**Files:**
- Modify: `pkg/slot/proxy/channel_migration_rpc.go`
- Modify: `pkg/slot/proxy/channel_migration_codec.go`
- Modify: `pkg/slot/proxy/channel_migration_rpc_test.go`
- Modify: `pkg/slot/FLOW.md`
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/channel_migration_test.go`

- [ ] **Step 1: Write failing proxy tests**

Add tests proving a caller can list active migration tasks involving one node from any store node:

```go
func TestChannelMigrationListActiveTasksForNodeRoutesToAuthoritativeSlots(t *testing.T) {
    ctx := context.Background()
    nodes := startTwoNodeHashSlotStores(t, 8)
    task := proxyTestChannelMigrationTask("task-node", "channel-node")
    task.SourceNode = 3
    task.TargetNode = 4

    require.NoError(t, nodes[1].store.CreateChannelMigrationTask(ctx, task))

    got, hasMore, err := nodes[0].store.ListActiveChannelMigrationTasksForNode(ctx, 3, 10)
    require.NoError(t, err)
    require.False(t, hasMore)
    require.Len(t, got, 1)
    require.Equal(t, task.TaskID, got[0].TaskID)
}
```

Also add:

```go
func TestChannelMigrationListActiveTasksForNodeFiltersTerminalAndUnrelated(t *testing.T)
func TestChannelMigrationListActiveTasksForNodeReportsHasMore(t *testing.T)
```

- [ ] **Step 2: Run proxy tests and verify RED**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy -run 'TestChannelMigrationListActiveTasksForNode' -count=1
```

Expected: FAIL because `ListActiveChannelMigrationTasksForNode` does not exist.

- [ ] **Step 3: Implement the proxy method and RPC op**

In `pkg/slot/proxy/channel_migration_rpc.go`:

- Add op constant `channelMigrationRPCListActiveForNode = "list_active_for_node"`.
- Extend the request with `NodeID uint64` and `Limit int` if not already present.
- Extend the response with:

```go
Tasks   []metadb.ChannelMigrationTask `json:"tasks,omitempty"`
HasMore bool                          `json:"has_more,omitempty"`
```

- Add:

```go
func (s *Store) ListActiveChannelMigrationTasksForNode(ctx context.Context, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error)
```

Implementation rules:

- Return empty result when `limit <= 0`.
- Iterate `s.cluster.SlotIDs()` in ascending order.
- For local slot leaders, scan local hash slots with `ListChannelMigrationTasks`.
- For remote slot leaders, call `channelMigrationRPCListActiveForNode`.
- Include only non-terminal tasks where `task.SourceNode == nodeID || task.TargetNode == nodeID`.
- Do not treat `OwnerNodeID` as node involvement; ownership only says which executor is currently driving the task.
- Stop after `limit`; set `has_more=true` if another matching task exists.
- Treat `ErrNoLeader` / `ErrSlotNotFound` as errors for manager safety rather than silently ignoring them.

Add helper:

```go
func channelMigrationTaskActive(task metadb.ChannelMigrationTask) bool {
    switch task.Status {
    case metadb.ChannelMigrationStatusCompleted, metadb.ChannelMigrationStatusFailed, metadb.ChannelMigrationStatusAborted:
        return false
    default:
        return true
    }
}
```

- [ ] **Step 4: Extend channel migration RPC codec**

In `pkg/slot/proxy/channel_migration_codec.go`:

- Add binary encoding/decoding support for the new request fields `NodeID` and `Limit`.
- Add binary encoding/decoding support for response fields `Tasks` and `HasMore`.
- Add codec round-trip tests in `pkg/slot/proxy/channel_migration_rpc_test.go` or the existing codec test file if one exists.

The test must prove that a `list_active_for_node` request/response round-trips without losing `NodeID`, `Limit`, `Tasks`, or `HasMore`.

- [ ] **Step 5: Extend management store interface**

In `internal/usecase/management/app.go`, add to `ChannelMigrationStore`:

```go
// ListActiveChannelMigrationTasksForNode returns active channel migration tasks whose source or target is the node.
ListActiveChannelMigrationTasksForNode(ctx context.Context, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error)
```

`pkg/slot/proxy.Store` should satisfy it after the RPC and codec steps.

Update the existing `fakeChannelMigrationStore` in `internal/usecase/management/channel_migration_test.go` with a no-op implementation so unrelated channel migration tests continue to compile.

- [ ] **Step 6: Update slot package flow documentation**

In `pkg/slot/FLOW.md`, add one short entry to the RPC Service IDs / proxy flow section explaining:

- `Store.ListActiveChannelMigrationTasksForNode` scans active channel migration tasks by `SourceNode` or `TargetNode`.
- NodeScaleIn additionally joins this result with current `ChannelRuntimeMeta` scan results to count active tasks whose channel metadata still references the target node.

- [ ] **Step 7: Run proxy tests and compile management**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy -run 'TestChannelMigrationListActiveTasksForNode|TestChannelMigrationRPCServiceIDDoesNotCollide' -count=1
GOWORK=off go test ./internal/usecase/management -run TestNonExistent -count=1
```

Expected: proxy tests PASS; management package compiles.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/proxy/channel_migration_rpc.go pkg/slot/proxy/channel_migration_codec.go pkg/slot/proxy/channel_migration_rpc_test.go pkg/slot/FLOW.md internal/usecase/management/app.go internal/usecase/management/channel_migration_test.go
git commit -m "feat: expose active channel migration task scan"
```

## Task 2: Add Channel Inventory To NodeScaleIn Reports

**Files:**
- Create: `internal/usecase/management/node_scalein_channel.go`
- Modify: `internal/usecase/management/node_scalein.go`
- Modify: `internal/usecase/management/node_scalein_test.go`

- [ ] **Step 1: Write failing channel inventory tests**

Add tests in `internal/usecase/management/node_scalein_test.go`:

```go
func TestPlanNodeScaleInCountsChannelLeadersAndReplicas(t *testing.T) {
    fixture := newScaleInActionFixture()
    fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining
    fixture.cluster.assignments = []controllermeta.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 2}}
    fixture.cluster.views = []controllermeta.SlotRuntimeView{{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, LastReportAt: fixture.now}}
    fixture.channelRuntime = &fakeScaleInChannelRuntimeMeta{metas: map[multiraft.SlotID][]metadb.ChannelRuntimeMeta{
        1: {
            {ChannelID: "leader-on-3", ChannelType: 1, Leader: 3, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, Status: uint8(channel.StatusActive)},
            {ChannelID: "replica-on-3", ChannelType: 1, Leader: 1, Replicas: []uint64{1, 3}, ISR: []uint64{1, 3}, Status: uint8(channel.StatusActive)},
        },
    }}
    fixture.rebuildApp()

    report, err := fixture.app.PlanNodeScaleIn(context.Background(), 3, fixture.req)

    require.NoError(t, err)
    require.True(t, report.Progress.ChannelInventoryScanned)
    require.Equal(t, 1, report.Progress.ChannelLeaders)
    require.Equal(t, 2, report.Progress.ChannelReplicas)
    require.Equal(t, NodeScaleInStatusDrainingChannels, report.Status)
    require.False(t, report.SafeToRemove)
}
```

Also add:

```go
func TestPlanNodeScaleInFailsClosedWhenChannelInventoryUnavailable(t *testing.T)
func TestScaleInStatusWaitsForChannelMigrationsBeforeConnections(t *testing.T)
```

Add fakes:

```go
type fakeScaleInChannelRuntimeMeta struct {
    metas map[multiraft.SlotID][]metadb.ChannelRuntimeMeta
    err   error
}

type fakeScaleInChannelMigrationStore struct {
    active      []metadb.ChannelMigrationTask
    activeByKey map[string]metadb.ChannelMigrationTask
    err         error
}
```

Update `scaleInActionFixture` and `newScaleInActionFixture` in `internal/usecase/management/node_scalein_test.go`:

- add `channelRuntime *fakeScaleInChannelRuntimeMeta`
- add `channelMigration *fakeScaleInChannelMigrationStore`
- initialize both with empty, successful fakes so existing scale-in tests do not become fail-closed by missing dependencies
- set `fixture.cluster.slotIDs = []multiraft.SlotID{1, 2}` or derive slot IDs from assignments before rebuilding the app
- add `fixture.rebuildApp()` that recreates `fixture.app` with the existing cluster/runtime plus `ChannelRuntimeMeta: fixture.channelRuntime` and `ChannelMigration: fixture.channelMigration`

The fake channel runtime must implement both methods in `ChannelRuntimeMetaReader`:

```go
ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
```

The fake migration store must satisfy the full `ChannelMigrationStore` interface and capture created tasks:

```go
ListActiveChannelMigrationTasksForNode(ctx context.Context, nodeID uint64, limit int) ([]metadb.ChannelMigrationTask, bool, error)
GetActiveChannelMigrationTask(ctx context.Context, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error)
CreateChannelMigrationTaskWithRuntimeGuard(ctx context.Context, req metadb.ChannelMigrationTaskCreate) error
AbortChannelMigration(ctx context.Context, req metadb.ChannelMigrationAbortRequest) error
```

Use a `created []metadb.ChannelMigrationTask` field so advance tests can assert which task kind was created. Reuse or extend the existing `fakeChannelMigrationStore` from `internal/usecase/management/channel_migration_test.go` if that keeps duplication lower.

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestPlanNodeScaleInCountsChannel|TestPlanNodeScaleInFailsClosedWhenChannelInventory|TestScaleInStatusWaitsForChannelMigrations' -count=1
```

Expected: FAIL because fields/statuses/scanner do not exist.

- [ ] **Step 3: Add report fields and statuses**

In `internal/usecase/management/node_scalein.go`:

- Add statuses:

```go
NodeScaleInStatusWaitingChannelMigrations NodeScaleInStatus = "waiting_channel_migrations"
NodeScaleInStatusDrainingChannels         NodeScaleInStatus = "draining_channels"
```

- Add `MaxChannelMigrations int` to `AdvanceNodeScaleInRequest`.
- Add checks:

```go
ChannelInventoryAvailable              bool
NoActiveChannelMigrationsInvolvingTarget bool
NoChannelLeadersOnTarget               bool
NoChannelReplicasOnTarget              bool
```

- Add progress fields:

```go
ChannelLeaders                      int
ChannelReplicas                     int
ActiveChannelMigrationsInvolvingNode int
ChannelInventoryScanned             bool
ChannelInventoryPartial             bool
ChannelInventoryError               string
```

Keep English comments on every new exported field.

- [ ] **Step 4: Implement `node_scalein_channel.go`**

Create `internal/usecase/management/node_scalein_channel.go` with:

```go
const (
    scaleInChannelScanPageLimit = 128
    scaleInChannelTaskScanLimit = 1024
)

type nodeScaleInChannelInventory struct {
    leaders int
    replicas int
    activeMigrations int
    scanned bool
    partial bool
    errText string
}
```

Implement:

```go
func (a *App) loadNodeScaleInChannelInventory(ctx context.Context, nodeID uint64) nodeScaleInChannelInventory
```

Rules:

- If `a.channelRuntimeMeta == nil || a.channelMigration == nil`, return partial inventory with `channel inventory dependencies are not configured`.
- Iterate `a.cluster.SlotIDs()` sorted ascending.
- Page through `ScanChannelRuntimeMetaSlotPage`.
- Count leaders and replicas referencing target.
- Track active migration task IDs in a small `map[string]struct{}` to avoid double counting.
- Call `a.channelMigration.ListActiveChannelMigrationTasksForNode(ctx, nodeID, scaleInChannelTaskScanLimit)` and count returned active tasks whose `SourceNode` or `TargetNode` is the scale-in target.
- For each scanned `ChannelRuntimeMeta` whose `Leader`, `Replicas`, or `ISR` still references the target, call `a.channelMigration.GetActiveChannelMigrationTask(ctx, meta.ChannelID, meta.ChannelType)` and count that active task too. This implements the spec rule that active migrations involving a channel whose current metadata still references the target also block removal.
- Do not count `OwnerNodeID` as target involvement.
- If `hasMore` is true, mark partial with an error explaining the scan limit.
- Any scan error marks partial and stores a trimmed error.

- [ ] **Step 5: Wire inventory into `PlanNodeScaleIn` and status**

In `PlanNodeScaleIn`:

- After runtime connection progress is filled, load channel inventory.
- Fill progress and checks.
- Add `channel_inventory_unavailable` blocked reason when partial/unavailable.
- For a Draining node, do not add channel leaders/replicas as generic blockers; let status express progress.

Update `scaleInStatus` ordering:

```go
if progress.ActiveChannelMigrationsInvolvingNode > 0 {
    return NodeScaleInStatusWaitingChannelMigrations
}
if progress.ChannelLeaders > 0 || progress.ChannelReplicas > 0 {
    return NodeScaleInStatusDrainingChannels
}
```

Place this after Slot leader checks and before connection checks.

- [ ] **Step 6: Run tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestPlanNodeScaleIn|TestScaleInStatus' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_channel.go internal/usecase/management/node_scalein_test.go
git commit -m "feat: include channel inventory in node scale-in"
```

## Task 3: Create Bounded Channel Migration Tasks During Advance

**Files:**
- Modify: `internal/usecase/management/node_scalein.go`
- Modify: `internal/usecase/management/node_scalein_channel.go`
- Modify: `internal/usecase/management/node_scalein_test.go`

- [ ] **Step 1: Write failing advance tests**

Add:

```go
func TestAdvanceNodeScaleInPrefersReplicaReplaceWithEmbeddedLeaderTransfer(t *testing.T)
func TestAdvanceNodeScaleInFallsBackToLeaderTransferWhenEmbeddedLeaderUnavailable(t *testing.T)
func TestAdvanceNodeScaleInDrainsLeaderCandidatesBeforeReplicaOnlyCandidates(t *testing.T)
func TestAdvanceNodeScaleInDoesNotCreateNewTasksWhileActiveChannelMigrationExists(t *testing.T)
func TestAdvanceNodeScaleInSelectsOnlyAliveNonDrainingTargets(t *testing.T)
func TestAdvanceNodeScaleInReturnsInvalidStateWhenNoChannelTarget(t *testing.T)
func TestAdvanceNodeScaleInKeepsSlotLeaderTransferBeforeChannelDrain(t *testing.T)
func TestAdvanceNodeScaleInDoesNotDrainChannelsBeforeSlotReplicasClear(t *testing.T)
func TestAdvanceNodeScaleInRefreshesReportWithoutErrorOnChannelTaskRace(t *testing.T)
```

The embedded-transfer test should arrange a channel where the scale-in target is both leader and replica, with an eligible ISR node and an eligible replacement node, then call:

```go
report, err := fixture.app.AdvanceNodeScaleIn(context.Background(), 3, AdvanceNodeScaleInRequest{
    MaxChannelMigrations: 1,
})
```

Assert one replica-replacement task is created, not a standalone leader-transfer task:

```go
require.NoError(t, err)
require.Len(t, fixture.channelMigration.created, 1)
require.Equal(t, metadb.ChannelMigrationKindReplicaReplace, fixture.channelMigration.created[0].Kind)
require.Equal(t, uint64(3), fixture.channelMigration.created[0].SourceNode)
require.NotZero(t, fixture.channelMigration.created[0].TargetNode)
```

The fallback test should arrange a source-leader channel where no eligible embedded leader exists and assert that the first task is `ChannelMigrationKindLeaderTransfer`.

The leaders-first ordering test should arrange both a source-leader channel and a replica-only channel, with scan data ordered so the replica-only channel appears first lexicographically. The test must still assert the source-leader candidate is chosen first while preserving embedded replica-replace behavior when safe.

The Slot precedence test must assert no channel task is created when `report.Status` is still `migrating_replicas` or `transferring_leaders`.

The no-target test must assert the returned report includes:

```go
require.Contains(t, scaleInReasonCodes(report.BlockedReasons), "no_channel_migration_target")
```

The race test should make the only candidate return `metadb.ErrStaleMeta`, `metadb.ErrAlreadyExists`, or a validation result containing blocker `active_task_exists`. Assert:

```go
require.NoError(t, err)
require.NotContains(t, scaleInReasonCodes(report.BlockedReasons), "no_channel_migration_target")
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestAdvanceNodeScaleIn.*Channel|TestAdvanceNodeScaleInKeepsSlotLeaderTransferBeforeChannelDrain' -count=1
```

Expected: FAIL because channel advance is not implemented.

- [ ] **Step 3: Add channel advance helpers**

In `node_scalein_channel.go`, add:

```go
const (
    scaleInDefaultChannelMigrations = 1
    scaleInMaxChannelMigrations     = 5
)

func clampScaleInChannelMigrations(limit int) int
func (a *App) advanceNodeScaleInChannels(ctx context.Context, nodeID uint64, limit int) (created int, blocker string, race bool, err error)
func selectScaleInChannelLeaderTarget(nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source uint64) (uint64, bool)
func selectScaleInChannelReplicaTarget(nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source uint64) (uint64, bool)
```

Candidate rules:

- build two candidate lists from the full authoritative scan: source-leader candidates first, replica-only candidates second
- sort each list by `(slotID, channelID, channelType)` before creating tasks
- target data node must be active/alive and not the draining node
- leader transfer target must be in `meta.ISR`
- replica replacement target must not be in `meta.Replicas` or `meta.ISR`
- if source is also leader and an eligible embedded leader exists, prefer `MigrateChannelReplica` so the existing migration workflow performs embedded leader transfer
- if source is leader but embedded leader transfer is not safe, fall back to `TransferChannelLeader`; a later advance can create replica replacement
- stable ordering by node ID ascending is acceptable and testable

- [ ] **Step 4: Integrate into `AdvanceNodeScaleIn`**

After Slot leader transfer branch and before connection handling, gate strictly on the current status:

```go
switch report.Status {
case NodeScaleInStatusMigratingReplicas, NodeScaleInStatusTransferringLeaders, NodeScaleInStatusFailed, NodeScaleInStatusBlocked:
    return report, nil
}
if report.Progress.ActiveChannelMigrationsInvolvingNode > 0 {
    return report, nil
}
if report.Status == NodeScaleInStatusDrainingChannels {
    created, blocker, race, err := a.advanceNodeScaleInChannels(ctx, nodeID, clampScaleInChannelMigrations(req.MaxChannelMigrations))
    if race {
        refreshed, refreshErr := a.GetNodeScaleInStatus(ctx, nodeID)
        if refreshErr != nil {
            return report, refreshErr
        }
        return refreshed, nil
    }
    if err != nil {
        refreshed, refreshErr := a.GetNodeScaleInStatus(ctx, nodeID)
        if refreshErr == nil {
            report = refreshed
        }
        if blocker != "" {
            report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason(blocker, "no eligible channel migration target is available", 0, 0, nodeID))
        }
        return report, &NodeScaleInReportError{Err: ErrInvalidNodeScaleInState, Report: report}
    }
    if created == 0 {
        refreshed, refreshErr := a.GetNodeScaleInStatus(ctx, nodeID)
        if refreshErr == nil {
            report = refreshed
        }
        report.BlockedReasons = append(report.BlockedReasons, scaleInBlockedReason("no_channel_migration_target", "no eligible channel migration target is available", 0, 0, nodeID))
        return report, &NodeScaleInReportError{Err: ErrInvalidNodeScaleInState, Report: report}
    }
    return a.GetNodeScaleInStatus(ctx, nodeID)
}
```

Task creation should call existing usecase methods:

```go
_, err := a.TransferChannelLeader(ctx, channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}, TransferChannelLeaderRequest{
    TargetNodeID: target,
})
```

and:

```go
_, err := a.MigrateChannelReplica(ctx, channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}, MigrateChannelReplicaRequest{
    SourceNodeID: nodeID,
    TargetNodeID: target,
})
```

If one candidate fails due to stale meta or already-active task, skip it and continue scanning until the limit is reached or no candidates remain. If every otherwise-valid candidate was skipped only because of a race (`metadb.ErrStaleMeta`, `metadb.ErrAlreadyExists`, or blocker `active_task_exists`), `advanceNodeScaleInChannels` must return `race=true`; `AdvanceNodeScaleIn` refreshes and returns the report without `ErrInvalidNodeScaleInState` and without adding `no_channel_migration_target`.

If candidates exist but none have an eligible target, return blocker `no_channel_migration_target` and `ErrInvalidNodeScaleInState`.

- [ ] **Step 5: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'TestAdvanceNodeScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/usecase/management/node_scalein.go internal/usecase/management/node_scalein_channel.go internal/usecase/management/node_scalein_test.go
git commit -m "feat: drain channel ownership during node scale-in"
```

## Task 4: Expose Channel Drain Fields Through Manager API

**Files:**
- Modify: `internal/access/manager/node_scalein.go`
- Modify: `internal/access/manager/node_scalein_test.go`

- [ ] **Step 1: Write failing manager adapter tests**

Update `sampleManagerNodeScaleInReport()` with channel fields, then assert JSON output:

```go
require.Equal(t, float64(7), body["progress"].(map[string]any)["channel_leaders"])
require.Equal(t, true, body["checks"].(map[string]any)["channel_inventory_available"])
```

Update `TestManagerNodeScaleInAdvanceClampsRequest` to send:

```json
{"max_leader_transfers":99,"max_channel_migrations":99,"force_close_connections":true}
```

Assert:

```go
require.Equal(t, 5, gotReq.MaxChannelMigrations)
```

Add a test for `next_action`:

```go
func TestManagerNodeScaleInNextActionIncludesChannelDrainStates(t *testing.T)
```

This test must assert the full spec mapping, including:

```go
transferring_leaders -> "transfer_slot_leaders"
waiting_channel_migrations -> "wait_channel_migrations"
draining_channels -> "drain_channels"
ready_to_remove -> "remove_node"
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerNodeScaleIn' -count=1
```

Expected: FAIL because DTO fields are missing.

- [ ] **Step 3: Implement DTO fields and clamping**

In `internal/access/manager/node_scalein.go`:

- Add `MaxChannelMigrations int` to `advanceNodeScaleInRequest`.
- Add `managerMaxScaleInChannelMigrations = 5`.
- Clamp and pass `MaxChannelMigrations`.
- Add check DTO fields:
  - `channel_inventory_available`
  - `no_active_channel_migrations_involving_target`
  - `no_channel_leaders_on_target`
  - `no_channel_replicas_on_target`
- Add progress DTO fields:
  - `channel_leaders`
  - `channel_replicas`
  - `active_channel_migrations_involving_node`
  - `channel_inventory_scanned`
  - `channel_inventory_partial`
  - `channel_inventory_error`
- Add `nodeScaleInCanAdvance` cases for `waiting_channel_migrations` and `draining_channels`.
- Update `nodeScaleInNextAction` to match the spec exactly:
  - `not_started -> start`
  - `migrating_replicas -> wait_reconcile_tasks`
  - `transferring_leaders -> transfer_slot_leaders`
  - `waiting_channel_migrations -> wait_channel_migrations`
  - `draining_channels -> drain_channels`
  - `waiting_connections -> wait_connections`
  - `ready_to_remove -> remove_node`

- [ ] **Step 4: Run manager tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerNodeScaleIn' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/access/manager/node_scalein.go internal/access/manager/node_scalein_test.go
git commit -m "feat: expose channel drain scale-in report fields"
```

## Task 5: Update Web Scale-in Types, API Client, And Report UI

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing web API tests**

In `web/src/lib/manager-api.test.ts`, update the scale-in endpoint test so `advanceNodeScaleIn` is called with:

```ts
await expect(advanceNodeScaleIn(3, {
  maxLeaderTransfers: 2,
  maxChannelMigrations: 4,
})).resolves.toEqual(report)
```

Assert body:

```ts
expect(JSON.parse(fetchMock.mock.calls[3]?.[1]?.body as string)).toEqual({
  max_leader_transfers: 2,
  max_channel_migrations: 4,
  force_close_connections: false,
})
```

- [ ] **Step 2: Write failing page tests**

In `web/src/pages/nodes/page.test.tsx`, extend `scaleInPlanReport.progress` with channel counters and assert the dialog shows:

```ts
expect(within(dialog).getByText("Channel leaders")).toBeInTheDocument()
expect(within(dialog).getByText("Channel replicas")).toBeInTheDocument()
expect(within(dialog).getByText("Active channel migrations")).toBeInTheDocument()
```

Update the advance expectation:

```ts
expect(advanceNodeScaleInMock).toHaveBeenCalledWith(1, {
  maxLeaderTransfers: 1,
  maxChannelMigrations: 1,
})
```

- [ ] **Step 3: Run web tests and verify RED**

Run:

```bash
cd web && yarn test src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
```

Expected: FAIL because types/API/UI are missing new fields.

- [ ] **Step 4: Implement TypeScript types and API body**

In `web/src/lib/manager-api.types.ts`:

- Add `maxChannelMigrations?: number` to `AdvanceNodeScaleInInput`.
- Add new check and progress fields to scale-in report types.

In `web/src/lib/manager-api.ts`, send:

```ts
max_channel_migrations: input.maxChannelMigrations ?? 1,
```

- [ ] **Step 5: Implement UI labels and rendering**

In `web/src/pages/nodes/page.tsx`, add `MetricCard` entries for:

- channel leaders
- channel replicas
- active channel migrations
- channel inventory status

In i18n files add labels:

```ts
"nodes.scaleIn.metrics.channelLeaders": "Channel leaders"
"nodes.scaleIn.metrics.channelReplicas": "Channel replicas"
"nodes.scaleIn.metrics.activeChannelMigrations": "Active channel migrations"
"nodes.scaleIn.metrics.channelInventory": "Channel inventory"
```

and Chinese equivalents:

```ts
"nodes.scaleIn.metrics.channelLeaders": "频道 Leader"
"nodes.scaleIn.metrics.channelReplicas": "频道副本"
"nodes.scaleIn.metrics.activeChannelMigrations": "活跃频道迁移"
"nodes.scaleIn.metrics.channelInventory": "频道清单"
```

- [ ] **Step 6: Run web tests**

Run:

```bash
cd web && yarn test src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: show channel drain progress in scale-in UI"
```

## Task 6: App Wiring, Regression Verification, And Documentation

**Files:**
- Modify if needed: `internal/app/build.go`
- Modify if needed: `internal/app/*test.go`
- Modify if needed: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Run backend compile and focused tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS. If compile fails because `app.store` does not satisfy expanded `ChannelMigrationStore`, fix the adapter in `internal/app/build.go` or add a narrow wrapper.

- [ ] **Step 2: Add app wiring test if needed**

If the compiler did not already protect the wiring path, add a test in `internal/app/build_test.go` that constructs `app.New(testConfig)` and verifies management build does not panic with channel migration enabled. Keep it lightweight; do not start the full app lifecycle.

- [ ] **Step 3: Update project knowledge only if a durable rule was learned**

If implementation reveals an important invariant, append one concise bullet to `docs/development/PROJECT_KNOWLEDGE.md`, for example:

```md
- Node scale-in must consider channel leaders, channel replicas, and active channel migration tasks before reporting `ready_to_remove`.
```

Do not add verbose implementation notes.

- [ ] **Step 4: Run full focused verification**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
cd web && yarn test src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Run broad Go test if focused verification passes**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit final wiring/docs changes**

```bash
git add internal/app/build.go internal/app docs/development/PROJECT_KNOWLEDGE.md
git commit -m "test: verify channel drain scale-in wiring"
```

Only include files that actually changed.

## Implementation Notes

- Do not use `RemoveSlot` in this work.
- Do not mark `safe_to_remove=true` if channel inventory is partial or unavailable.
- Do not create channel migration tasks before Slot replicas and Slot leaders are clear.
- Do not create new channel migration tasks while active channel migrations involve the target node.
- Preserve “单节点集群” semantics. Do not add bypass branches for standalone deployment.
- Keep `internal/access/manager` as an adapter only; all orchestration belongs in `internal/usecase/management`.
- Keep new exported Go fields and interfaces documented with English comments.
- Use stable ordering in scans and target selection so tests are deterministic.

## Final Verification Checklist

- [ ] `GOWORK=off go test ./pkg/slot/proxy ./internal/usecase/management ./internal/access/manager ./internal/app -count=1`
- [ ] `cd web && yarn test src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx`
- [ ] `GOWORK=off go test ./...`
- [ ] `git diff --check`
- [ ] `git status --short` only shows intended files before final commit
