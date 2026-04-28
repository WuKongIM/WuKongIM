# Slot Preferred Leader Design

> Status: Draft for review
> Date: 2026-04-28
> Scope: Controller planner, Controller metadata, managed Slot reconciliation, Slot leader transfer, manager observability
> Non-scope: Replacing Raft leader election, forcing unsafe leadership, channel leader placement, Kubernetes orchestration

## 1. Background

Managed physical Slots currently persist desired replica membership in `SlotAssignment.DesiredPeers`, but they do not persist a desired or preferred Slot leader. The initial bootstrap path can place a Slot leader on a planned target node, yet that target only lives in the bootstrap `ReconcileTask`. Once the task succeeds, the task is deleted and the long-term assignment has no leader intent.

After a node restart, the Slot runtime reopens persisted Raft state and Raft elects a leader through randomized election timeouts. This keeps correctness, but it means Slot leaders can drift after every restart or rolling restart. The controller planner also balances replica count only; it does not count or rebalance `SlotRuntimeView.LeaderID`. A cluster can therefore have evenly distributed Slot replicas while Slot leaders remain uneven.

This design introduces controller-owned **preferred Slot leaders** as a soft placement intent. Raft remains the final authority for safety. The controller recommends and repairs leader placement when safe; Raft still elects or rejects leadership according to its protocol.

## 2. Goals

- Persist a controller recommendation for each managed Slot's preferred leader.
- Keep Raft election and safety semantics unchanged.
- Rebalance Slot leaders toward an even distribution across active data nodes.
- Let Slot leaders recover toward controller preference after restart-driven drift.
- Avoid leader-transfer storms during failover, rolling restart, repair, rebalance, and hash-slot migration.
- Keep single-node cluster behavior as a no-op: the only voter is the preferred leader.
- Make the behavior observable from manager APIs and existing Slot detail/runtime views.

## 3. Non-goals

- Do not modify etcd/raft election internals.
- Do not make controller force an unavailable or non-voter node to become leader.
- Do not block Slot availability when the preferred leader is down.
- Do not couple Slot leader placement to Channel leader or ISR placement in this stage.
- Do not create a global re-sharding or physical Slot migration feature.
- Do not transfer leaders on every minor observation change.

## 4. Core Semantics

A preferred Slot leader is a **soft controller intent**:

1. Controller chooses a preferred leader from the Slot's desired peers.
2. Raft may elect any valid leader when availability requires it.
3. Controller observes the actual leader through `SlotRuntimeView.LeaderID`.
4. If actual leader differs from preferred leader and safety checks pass, controller schedules a bounded leader transfer.
5. If the transfer fails or target becomes unsafe, the current Raft leader remains valid and the controller retries later with backoff or recomputes the preference.

The invariant is: **preferred leader guides convergence; Raft leader determines current authority**.

## 5. Data Model

Extend `controllermeta.SlotAssignment`:

```go
type SlotAssignment struct {
    SlotID          uint32
    DesiredPeers    []uint64
    PreferredLeader uint64
    ConfigEpoch     uint64
    BalanceVersion  uint64
}
```

Rules:

- `PreferredLeader == 0` means legacy assignment with no durable recommendation. Readers should treat it as "unset".
- When set, `PreferredLeader` must be one of `DesiredPeers`.
- Normalization should sort/deduplicate `DesiredPeers` without dropping `PreferredLeader`.
- Decode paths must remain backward compatible with old persisted assignments and set `PreferredLeader=0` for old records.
- Any assignment write that changes `DesiredPeers` must either preserve a still-valid preferred leader or choose a new one.

`SlotRuntimeView.LeaderID` remains observed runtime state. It is not durable steady-state metadata.

## 6. Planner Behavior

### 6.1 Bootstrap

When planning a new Slot:

1. Select `DesiredPeers` using existing replica-load logic.
2. Choose `PreferredLeader` from those peers using planned leader load, not only `slotID % len(peers)`.
3. Persist `SlotAssignment.PreferredLeader` with the assignment.
4. Set bootstrap task `TargetNode` to `PreferredLeader`.
5. Executor still calls `ensureLeaderOnTarget` after bootstrap.

Planned leader load should include:

- actual leaders from `RuntimeViews` when available;
- `PreferredLeader` from assignments that do not have live runtime views yet;
- the new decisions being produced in the current planning sequence.

This avoids repeatedly biasing low NodeIDs during initial cluster creation.

### 6.2 AddSlot

`AddSlot` should not derive bootstrap target from the minimum peer ID. The operator/controller path should select a preferred leader with the same leader-load policy used by bootstrap planning, then persist it in the new assignment and use it as the bootstrap target.

If `AddSlotRequest` remains the command boundary, add `PreferredLeader` to the request and validate it in the state machine.

### 6.3 Membership Repair and Replica Rebalance

When `DesiredPeers` changes because of repair or rebalance:

- If the old preferred leader is still in `DesiredPeers` and healthy enough, preserve it.
- If the old preferred leader is removed, choose a replacement from the new peers using leader-load policy.
- If the current leader is the removed source node, the existing task should move leadership away before removing the old voter.
- After the membership task completes, the leader placement loop can later refine the leader if the immediate safe target differs from the balanced preferred target.

### 6.4 Leader Rebalance

Add a planner decision path after urgent bootstrap/repair and before or after replica rebalance, depending on safety policy. Recommended order:

1. Bootstrap and repair first.
2. Running onboarding/scale-in coordination next.
3. Replica rebalance next.
4. Leader rebalance last.

Leader rebalance should be opportunistic. It creates work only when:

- all strict observations are available;
- no task already exists for the Slot;
- Slot is not locked by onboarding/scale-in;
- Slot is not involved in hash-slot migration;
- Slot has quorum and a non-zero actual leader;
- target is in `DesiredPeers` and is active/alive/data;
- `maxLeaderLoad - minLeaderLoad > LeaderSkewThreshold`.

A conservative default `LeaderSkewThreshold=1` is recommended.

## 7. Task Model

Add a lightweight task kind:

```go
const TaskKindLeaderTransfer TaskKind = ...
```

For this task:

- `SourceNode` is optional but useful for diagnostics. It should usually be the observed current leader.
- `TargetNode` is the preferred leader transfer target.
- `Step` can reuse `TaskStepTransferLeader`, or a simplified single-step execution path can ignore multi-step progression for this kind.
- The executor must not run add-learner/promote/remove logic for this task.

Execution flow:

```text
LeaderTransfer task:
  1. Read current Slot leader.
  2. If current leader already equals TargetNode, succeed.
  3. Validate TargetNode is assigned and eligible.
  4. Call TransferLeadership(slotID, TargetNode).
  5. Wait until observed/current leader equals TargetNode, or timeout.
  6. Report success or retryable error.
```

Failures should follow the existing task retry mechanism, with extra care to avoid rapid repeated transfers.

## 8. Safety and Rate Limiting

Leader transfer is safe only when all checks pass:

- Controller leader has completed warmup and has strict runtime observations.
- Slot has quorum.
- Actual leader is known.
- Target node is active, alive, and data-capable.
- Target node is in the Slot's desired peer set.
- Target node has opened the Slot runtime or can be confirmed through managed Slot status.
- No conflicting bootstrap/repair/rebalance/hash-slot migration/onboarding task is active for the Slot.

Rate limits:

- Limit leader-transfer tasks per controller tick.
- Limit leader transfers per target/source node per time window.
- Add per-Slot cooldown after successful or failed transfer.
- During rolling restart, prefer waiting for observations to stabilize before transferring leaders back.

Recommended initial defaults:

```text
LeaderSkewThreshold = 1
MaxLeaderTransfersPerTick = 1
LeaderTransferCooldown = 30s
LeaderTransferRetryBudget = existing cluster timeout budget
```

## 9. Data Flow

```text
Controller leader tick:
  snapshot metadata + strict runtime observations
  compute replica load and leader load
  bootstrap/repair/rebalance decisions first
  if no urgent task:
    find leader skew
    choose a safe Slot whose current leader is overloaded
    choose underloaded target from that Slot's desired peers
    upsert PreferredLeader if needed
    create TaskKindLeaderTransfer

Node reconciler:
  apply assignment cache
  load tasks
  execute TaskKindLeaderTransfer on current leader or deterministic executor
  call managed Slot TransferLeadership
  report task result

Runtime observation:
  nodes report SlotRuntimeView.LeaderID
  controller confirms convergence
  task success deletes task
```

## 10. Executor Ownership

`TaskKindLeaderTransfer` should be executed by the current Slot leader when possible. If the current leader cannot be read, use a deterministic fallback among desired peers, but the actual `TransferLeadership` call must still be routed to the current leader through existing managed Slot RPC logic.

This keeps entry adapters thin and reuses `slotManager.transferLeadership`.

## 11. Observability and Manager API

Slot detail should expose:

- `assignment.preferred_leader_id`
- `runtime.leader_id`
- `leader_match`: whether actual leader equals preferred leader
- `leader_transfer_pending`: whether a leader-transfer task exists

Node detail should continue reporting current Slot leader count from runtime observations. A later UI/API enhancement can show preferred leader count separately.

Metrics should include:

- total Slot leader transfers requested by controller;
- successful transfers;
- failed transfers by reason;
- leader skew gauge per controller observation.

## 12. Backward Compatibility

- Existing persisted assignments decode with `PreferredLeader=0`.
- Planner should lazily fill missing `PreferredLeader` when it next safely updates an assignment or schedules leader rebalance.
- No config migration is required for single-node clusters.
- Rolling upgrade safety: nodes that do not understand the new assignment codec must not be mixed after the codec changes. If rolling binary compatibility is required, use a versioned codec field and ensure old nodes reject newer controller metadata rather than corrupting it.

## 13. Testing Plan

Unit tests:

- Assignment codec round-trip with `PreferredLeader` and old-format decode.
- Planner bootstrap chooses preferred leaders evenly with uneven peer sets.
- Planner preserves preferred leader when still valid after repair/rebalance.
- Planner replaces preferred leader when removed from desired peers.
- Leader rebalance skips slots without quorum, with tasks, in migration, or with invalid targets.
- Leader rebalance creates at most the configured number of tasks per tick.

Cluster tests:

- Initial multi-node cluster has near-even Slot leader distribution.
- Restarting a Slot leader causes temporary drift, then controller transfers back or rebalances after cooldown.
- Rolling restart does not trigger transfer storms.
- Single-node cluster remains stable and creates no leader-transfer tasks.
- AddSlot selects a balanced preferred leader instead of always the smallest peer.

Manager/usecase tests:

- Slot detail includes preferred leader and leader match status.
- Node leader counts remain based on actual runtime leaders.

## 14. Open Questions

1. Should leader rebalance run strictly after replica rebalance, or may it run before replica rebalance when replica skew is already below threshold?
2. Should `PreferredLeader` be changed proactively to the balanced target before transfer, or only after transfer succeeds?
3. Should a failed leader transfer keep the same preferred leader and retry, or clear/recompute preference after N failures?

Recommended answers for V1:

1. Run leader rebalance after replica rebalance.
2. Persist `PreferredLeader` before scheduling the transfer, because it represents controller intent.
3. Keep preference during retry budget, then recompute after task failure if target is no longer eligible.

## 15. Implementation Stages

### Stage 1: Durable Preferred Leader

Add `PreferredLeader` to controller metadata, codec, DTOs, bootstrap planner, and AddSlot path. Keep behavior compatible with existing leader transfer code.

### Stage 2: Leader Transfer Task

Add `TaskKindLeaderTransfer`, executor handling, safety checks, retry behavior, and observability hooks.

### Stage 3: Planner Leader Rebalance

Add leader load computation, skew detection, candidate selection, cooldown/rate limits, and tests for restart convergence.

### Stage 4: Manager Visibility

Expose preferred leader and leader-match fields in manager Slot detail and node diagnostics.
