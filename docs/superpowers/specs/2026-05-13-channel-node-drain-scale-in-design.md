# Channel Node Drain Scale-in Design

**Date:** 2026-05-13  
**Status:** Proposed  
**Scope:** Extend existing manager-driven NodeScaleIn with Channel leader/replica drain

## Goal

Integrate channel-level drain into the existing manager-driven node scale-in flow so a node is not reported as `ready_to_remove` while it still owns any channel leader or channel replica.

V1 reuses the newly added channel migration task system. It does not introduce a separate Channel Drain API or a durable node-drain job. The operator continues to use the existing NodeScaleIn endpoints:

```text
POST /manager/nodes/:node_id/scale-in/plan
POST /manager/nodes/:node_id/scale-in/start
GET  /manager/nodes/:node_id/scale-in/status
POST /manager/nodes/:node_id/scale-in/advance
POST /manager/nodes/:node_id/scale-in/cancel
```

## Context

Current NodeScaleIn already handles the Slot and gateway sides of safe node removal:

- preflight and status reporting through `internal/usecase/management`
- `MarkNodeDraining` / `ResumeNode`
- Slot replica migration and Slot leader transfer progress
- runtime connection safety checks
- manager API and UI report rendering

Channel migration now provides the missing channel-level primitives:

- safe single-channel leader transfer
- safe single-channel replica replacement
- durable `ChannelMigrationTask`
- authoritative `ChannelRuntimeMeta` guards
- executor-driven phase advancement
- write-fence based cutover for replica replacement

The next step is to make NodeScaleIn consume these primitives so node removal covers both Slot ownership and Channel ownership.

## Non-goals

- Do not add a separate Channel Drain API in V1.
- Do not add a durable `ChannelNodeDrainJob` in V1.
- Do not automatically create channel migration tasks as a hidden side effect of `MarkNodeDraining`.
- Do not support concurrent node scale-in jobs.
- Do not support automatic load balancing.
- Do not change channel replica count, `MinISR`, or channel slot/hash-slot ownership.
- Do not shrink controller voters or remove physical Slots.
- Do not add channel drain behavior to append, fetch, or delivery hot paths.

## Recommended Approach

Use the existing NodeScaleIn model:

- `plan/status` are side-effect-free and fail closed.
- `start` marks the target node as `Draining` after preflight passes.
- `advance` performs bounded work.
- `cancel` resumes the node, but does not roll back already-completed channel migrations.
- `safe_to_remove` is true only after Slot, Channel, and connection dimensions are clear.

Channel drain becomes a new phase after Slot leader transfer and before connection drain:

```text
blocked
not_started
migrating_replicas
transferring_leaders
waiting_channel_migrations
draining_channels
waiting_connections
ready_to_remove
```

The status order is important:

1. Slot replicas and Slot reconcile tasks must clear first.
2. Slot leaders must move away from the target.
3. Existing channel migrations involving the target must finish.
4. Remaining channel leaders/replicas are drained by creating bounded channel migration tasks.
5. Runtime connections and gateway sessions must drain.
6. Only then is the node ready for external removal.

## API Changes

The existing routes remain unchanged. Only request and response bodies are extended.

### Advance Request

Extend the existing `advance` request:

```json
{
  "max_leader_transfers": 1,
  "max_channel_migrations": 1,
  "force_close_connections": false
}
```

Rules:

- `max_channel_migrations` defaults to `1`.
- `max_channel_migrations` is capped to a conservative value, initially `5`.
- V1 uses a fixed internal ordering: channel leaders first, then channel replicas.
- `force_close_connections` remains a connection action only. It must not override Channel drain safety.

### Progress Fields

Extend `NodeScaleInProgress` and manager JSON `progress`:

```json
{
  "channel_leaders": 12,
  "channel_replicas": 48,
  "active_channel_migrations_involving_node": 3,
  "channel_inventory_scanned": true,
  "channel_inventory_partial": false,
  "channel_inventory_error": ""
}
```

Field semantics:

- `channel_leaders`: authoritative channels whose `ChannelRuntimeMeta.Leader == targetNodeID`.
- `channel_replicas`: authoritative channels whose `Replicas` or `ISR` contains the target node. Leader channels also count as replicas.
- `active_channel_migrations_involving_node`: active migration tasks whose source, target, or current channel metadata still references the target node.
- `channel_inventory_scanned`: true when the full authoritative scan completed.
- `channel_inventory_partial`: true when the scan failed, timed out, or was intentionally stopped before full coverage.
- `channel_inventory_error`: trimmed diagnostic text for report/debug visibility.

### Check Fields

Extend `NodeScaleInChecks` and manager JSON `checks`:

```json
{
  "channel_inventory_available": true,
  "no_active_channel_migrations_involving_target": true,
  "no_channel_leaders_on_target": true,
  "no_channel_replicas_on_target": true
}
```

### Blocked Reasons

Add stable reason codes:

```text
channel_inventory_unavailable
active_channel_migrations_involving_target
channel_leaders_exist
channel_replicas_exist
no_channel_migration_target
```

Recommended semantics:

- Before `start`, channel leaders/replicas are progress, not blockers. `start` should be allowed when preflight is otherwise safe and inventory is readable.
- After the node is `Draining`, channel leaders/replicas drive `draining_channels` instead of generic `blocked`.
- Inventory uncertainty is always fail-closed.
- No available migration target is an action blocker and should surface as `ErrInvalidNodeScaleInState` with the latest report.

### Next Action

Extend `next_action`:

```text
not_started -> start
migrating_replicas -> wait_reconcile_tasks
transferring_leaders -> transfer_slot_leaders
waiting_channel_migrations -> wait_channel_migrations
draining_channels -> drain_channels
waiting_connections -> wait_connections
ready_to_remove -> remove_node
```

## Channel Inventory Scan

V1 uses a full authoritative scan because node scale-in is a low-frequency operator action and correctness is more important than speed.

Algorithm:

```text
for each physical slot in cluster.SlotIDs():
  cursor = empty
  while not done:
    page = ChannelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(slotID, cursor, limit)
    for each meta in page:
      if meta.Leader == target:
        channel_leaders++
        record leader-transfer candidate up to an internal cap

      if target in meta.Replicas or target in meta.ISR:
        channel_replicas++
        record replica-replacement candidate up to an internal cap
```

Rules:

- Read through `ChannelRuntimeMetaReader.ScanChannelRuntimeMetaSlotPage`.
- Do not fall back to local shard scans.
- Scan physical Slots in ascending order.
- Process channels in `(slotID, channelID, channelType)` order for deterministic behavior.
- Any slot scan failure makes inventory unavailable and `safe_to_remove=false`.
- Context timeout is treated as inventory unavailable.
- Report counters are returned, but the response should not include every candidate channel.
- `advance` rescans instead of trusting stale report candidates.

An internal candidate cap, such as `scaleInChannelCandidateLimit = 32`, is enough for diagnostics and action selection. Full counts still require completing the scan.

## Target Selection

The source node is the scale-in target. Channel migration target candidates must be active data nodes:

```text
node.Role == data
node.JoinState == active
node.Status == alive
node.ID != source
```

Draining, dead, suspect, joining, rejected, and controller-only nodes are excluded.

### Leader Transfer Candidate

For a channel whose leader is the source node, a leader-transfer target must satisfy:

```text
target in meta.ISR
target != source
target is an eligible active data node
```

### Replica Replacement Candidate

For a channel whose replicas or ISR include the source node, a replica replacement target must satisfy:

```text
target is an eligible active data node
target not in meta.Replicas
target not in meta.ISR
```

If the source is also the channel leader:

- Prefer creating a replica-replacement task when existing migration can safely perform embedded leader transfer.
- Embedded leader transfer requires an eligible ISR node other than source and replacement target.
- If embedded leader transfer is not possible, create a leader-transfer task first and let the next `advance` create the replica-replacement task.

This keeps scale-in orchestration policy thin and reuses channel migration's safety-critical flow.

## Advance Flow

`AdvanceNodeScaleIn` should keep the existing Slot ordering and add Channel drain after Slot leaders are clear.

```text
1. Refresh report.
2. If target is not Draining, return ErrInvalidNodeScaleInState.
3. If Slot replicas or Slot tasks still reference target, return report without channel actions.
4. If Slot leaders remain on target, transfer bounded Slot leaders and return refreshed report.
5. If active channel migrations involve target, return waiting_channel_migrations without creating new tasks.
6. If channel leaders or replicas remain:
   - scan authoritative ChannelRuntimeMeta
   - choose candidates in leaders-first order
   - create up to max_channel_migrations channel migration tasks
   - return refreshed report
7. If channels are clear, continue to connection drain state.
```

Task creation reuses management usecase methods:

```go
TransferChannelLeader(ctx, channelID, TransferChannelLeaderRequest{
    TargetNodeID: selectedISRTarget,
    DryRun: false,
})

MigrateChannelReplica(ctx, channelID, MigrateChannelReplicaRequest{
    SourceNodeID: scaleInTarget,
    TargetNodeID: selectedReplacement,
    DryRun: false,
})
```

`advance` must not wait for migration completion. The durable channel migration executor continues to drive tasks in the background. NodeScaleIn observes completion through later `status` calls.

## Fail-closed Behavior

`plan/status` should return a report when possible:

- strict controller read failure: existing blocked report behavior
- channel inventory failure: report with `channel_inventory_unavailable`
- active migration read failure: report with `channel_inventory_unavailable`
- timeout: report with `channel_inventory_unavailable`

`advance` action errors:

- non-Draining target: `ErrInvalidNodeScaleInState`
- active channel migration exists: no error, return `waiting_channel_migrations`
- task creation race such as already-exists or stale meta: refresh and return report
- no eligible target: `ErrInvalidNodeScaleInState` with reason `no_channel_migration_target`
- all candidate attempts fail: `ErrInvalidNodeScaleInState` with the latest report

## Data Flow

```text
manager HTTP
  -> internal/usecase/management.NodeScaleIn
    -> ClusterReader strict Slot/node/task reads
    -> RuntimeSummaryReader connection reads
    -> ChannelRuntimeMetaReader authoritative channel scan
    -> ChannelMigrationStore active task checks / task creation through existing methods
      -> pkg/slot/proxy
        -> pkg/slot/fsm + pkg/slot/meta
    -> channel migration executor advances created tasks
```

The manager HTTP layer remains a thin adapter. The composition root continues to wire concrete dependencies in `internal/app`.

## UI Impact

The existing scale-in sheet/report should add channel counters and new actions:

- show channel leaders, channel replicas, active channel migrations
- show inventory unavailable as a blocking safety issue
- show `drain_channels` and `wait_channel_migrations` next actions
- keep existing start/advance/cancel controls

The UI should not list every channel in V1. A later diagnostics panel can add drilldown if needed.

## Testing Plan

### Unit Tests

Add tests in `internal/usecase/management`:

- `PlanNodeScaleInCountsChannelLeadersAndReplicas`
- `PlanNodeScaleInFailsClosedWhenChannelInventoryUnavailable`
- `ScaleInStatusWaitsForChannelMigrationsBeforeConnections`
- `AdvanceNodeScaleInCreatesLeaderTransferBeforeReplicaReplace`
- `AdvanceNodeScaleInDoesNotCreateNewTasksWhileActiveChannelMigrationExists`
- `AdvanceNodeScaleInSelectsOnlyAliveNonDrainingTargets`
- `AdvanceNodeScaleInReturnsInvalidStateWhenNoChannelTarget`
- `AdvanceNodeScaleInKeepsSlotLeaderTransferBeforeChannelDrain`

### Manager Adapter Tests

- report JSON includes new progress and checks fields
- `max_channel_migrations` defaults and clamps
- `next_action` maps `drain_channels` and `wait_channel_migrations`
- scale-in blocked errors include expanded report fields

### App Wiring Tests

- `management.Options` receives `ChannelRuntimeMeta` and `ChannelMigration`
- missing channel inventory dependencies fail closed
- channel drain does not bypass single-node cluster semantics

### Integration / E2E Follow-up

After unit and app wiring coverage, add a real three-node E2E:

1. Start a three-node cluster.
2. Create or activate a person channel.
3. Pick a node that owns a channel replica.
4. Start node scale-in.
5. Advance until channel migration task is created.
6. Wait for migration completion.
7. Verify status no longer reports channel replica on that node.
8. Send messages after migration and verify delivery.

## Delivery Batches

### Batch 1: Report Model And Inventory

- Extend `NodeScaleInProgress` and `NodeScaleInChecks`.
- Add channel inventory scanner in `internal/usecase/management`.
- Count channel leaders, replicas, and active migrations involving target.
- Update status calculation and next-action mapping.
- Add unit tests for counts and fail-closed inventory.

### Batch 2: Bounded Channel Advance

- Extend `AdvanceNodeScaleInRequest`.
- Add channel target selection.
- Create bounded channel migration tasks.
- Preserve Slot leader transfer precedence.
- Add unit tests for ordering, active task waiting, and no-target errors.

### Batch 3: Manager DTO And UI

- Extend manager request/response DTOs.
- Clamp `max_channel_migrations`.
- Show new counters in the existing scale-in UI.
- Add manager adapter and UI tests.

### Batch 4: Integration Confidence

- Add app wiring tests.
- Run targeted package tests.
- Design and implement the optional real-process E2E scenario.

## Acceptance Criteria

- A Draining node with channel leaders or replicas never reports `safe_to_remove=true`.
- `advance` creates no more than the requested/capped number of channel migration tasks.
- Existing active channel migrations cause `waiting_channel_migrations` and no new task creation.
- Channel migration completion lets NodeScaleIn continue draining remaining channels.
- Slot drain still takes precedence over Channel drain.
- Connection drain still occurs after Channel drain.
- Inventory uncertainty fails closed.
- Single-node cluster deployments remain “单节点集群” and do not get a bypass path.

## Risks And Mitigations

| Risk | Mitigation |
|---|---|
| Full channel scan is slow on large clusters | V1 favors correctness. Add durable cursor/job/index later if needed. |
| `advance` creates too many tasks | Default to one task and cap `max_channel_migrations`. |
| Candidate selection races with metadata changes | Existing task creation uses runtime guards; stale meta refreshes report. |
| Active channel migration targets the draining node | Count it as involving target and wait/fail closed until resolved. |
| Operator cancels node scale-in after channel tasks complete | Do not auto-rollback channel migrations; later rebalance can restore load. |
| UI overwhelms users with channel details | Show counters and stable reasons only in V1. |
