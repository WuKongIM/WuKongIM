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
4. If actual leader differs from preferred leader, safety checks pass, and the move preserves or improves global leader balance, controller schedules a bounded leader transfer.
5. If the transfer fails or target becomes unsafe, the current Raft leader remains valid and the controller retries later with backoff or recomputes the preference.

The invariant is: **preferred leader guides convergence; Raft leader determines current authority**.

V1 prioritizes global leader balance over exact sticky per-Slot leadership. A balanced-but-wrong leader layout may remain unchanged if moving one Slot at a time would temporarily violate the leader skew threshold.

## 5. Data Model

Extend `controllermeta.SlotAssignment`:

```go
type SlotAssignment struct {
    SlotID                      uint32
    DesiredPeers                []uint64
    PreferredLeader             uint64
    LeaderTransferCooldownUntil time.Time
    ConfigEpoch                 uint64
    BalanceVersion              uint64
}
```

Rules:

- `PreferredLeader == 0` means legacy assignment with no durable recommendation. Readers should treat it as "unset".
- When set, `PreferredLeader` must be one of `DesiredPeers`.
- `LeaderTransferCooldownUntil` is controller-owned durable cooldown state. The planner must not create a new leader-transfer task for the Slot before this time.
- Normalization should sort/deduplicate `DesiredPeers` without dropping `PreferredLeader`.
- Decode paths must remain backward compatible with old persisted assignments and set `PreferredLeader=0` for old records.
- Any assignment write that changes `DesiredPeers` must either preserve a still-valid preferred leader or choose a new one.
- Successful leader-transfer task results should update `LeaderTransferCooldownUntil` and delete the task atomically.
- Failed leader-transfer retries should use the existing task `NextRunAt`; task presence blocks competing Slot tasks while the retry budget is active.

`SlotRuntimeView.LeaderID` remains observed runtime state. It is not durable steady-state metadata.

Extend runtime observation with explicit current voters:

```go
type SlotRuntimeView struct {
    SlotID        uint32
    CurrentPeers  []uint64
    CurrentVoters []uint64
    LeaderID      uint64
    // existing fields omitted
}
```

Rules:

- `CurrentPeers` may remain a legacy compatibility field for the currently known peer set.
- `CurrentVoters` is the authoritative observed voter set used by leader-transfer planning and execution.
- If `CurrentVoters` is empty, leader transfer must treat voter membership as unknown and skip the Slot.
- `multiraft.Status` or managed Slot status should expose the Raft current voter set from RawNode status/config so observation can populate `CurrentVoters`.

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

Add a planner decision path after urgent bootstrap/repair and replica rebalance. V1 uses this order:

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
- Slot has more than one desired voter;
- Slot is not inside `LeaderTransferCooldownUntil`;
- target is in the observed current voter set;
- target is in `DesiredPeers` and is active/alive/data;
- the trigger-specific skew rule below is satisfied.

A conservative default `LeaderSkewThreshold=1` is recommended.

The planner should persist `PreferredLeader` before scheduling the transfer because it represents controller intent. If a transfer task exhausts retries and the target is no longer eligible, the planner should recompute the preference on a later tick instead of blindly retrying the same target forever.

Terminal failed `TaskKindLeaderTransfer` tasks must not remain as durable blockers. When a leader-transfer task reaches the retry limit, the controller should delete that task and set `LeaderTransferCooldownUntil` to a failure cooldown. Repair/rebalance/bootstrap tasks may still use durable `TaskStatusFailed` for operator inspection; opportunistic leader-transfer tasks self-clear so future safety-critical tasks are not blocked.

V1 has two leader-placement triggers:

- **Skew correction**: when actual leader load skew exceeds `LeaderSkewThreshold`, choose a Slot whose transfer reduces skew. This path may update `PreferredLeader` to the chosen target before creating the task.
- **Preference convergence**: when actual leader differs from an existing valid `PreferredLeader`, create a task only if the transfer keeps resulting actual leader skew within `LeaderSkewThreshold` or otherwise improves skew. V1 does not chase exact preferred-leader matching if doing so would temporarily create an imbalanced layout.

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
  3. Validate TargetNode is assigned, eligible, and in the observed current voter set.
  4. Call TransferLeadership(slotID, TargetNode).
  5. Wait until observed/current leader equals TargetNode, or timeout.
  6. On success, update the Slot assignment cooldown and delete the task.
  7. On failure, report a retryable error through the existing task retry path.
```

Failures should follow the existing task retry mechanism, with extra care to avoid rapid repeated transfers.

The task must not call `TransferLeadership` when the current Slot leader or current voter set is unknown. Unknown leader/voter state is a safety stop for the transfer itself, but it must not leave the task pending forever. A deterministic checker node reports a retryable safety failure so `Attempt`/`NextRunAt` advances; if the condition stays unknown through the retry budget, the task self-clears with a failure cooldown.

## 8. Safety and Rate Limiting

Leader transfer is safe only when all checks pass:

- Controller leader has completed warmup and has strict runtime observations.
- Slot has quorum.
- Actual leader is known.
- Target node is active, alive, and data-capable.
- Target node is in the Slot's desired peer set.
- Target node is in the Slot's observed current voter set.
- Target node has opened the Slot runtime or can be confirmed through managed Slot status.
- No conflicting bootstrap/repair/rebalance/hash-slot migration/onboarding task is active for the Slot.

Rate limits:

- Limit leader-transfer tasks per controller tick.
- Use the planner's existing one-decision-per-tick behavior as the V1 global transfer limit.
- Persist per-Slot cooldown in `SlotAssignment.LeaderTransferCooldownUntil`.
- Use task `NextRunAt` for failed-transfer retry backoff while a leader-transfer task exists.
- Delete terminal failed leader-transfer tasks and apply a failure cooldown instead of storing a durable failed leader-transfer task.
- During rolling restart, prefer waiting for observations to stabilize before transferring leaders back.
- Do not implement per-node sliding transfer windows in V1. If needed later, add explicit durable controller metadata for them; do not hide them in process-local state.

State ownership:

- Policy defaults and limits live in controller/cluster config.
- Per-Slot successful-transfer cooldown is durable controller metadata on `SlotAssignment`.
- Failed-transfer backoff is durable task metadata through `ReconcileTask.NextRunAt`.
- Terminal failed leader-transfer cooldown is durable controller metadata on `SlotAssignment`; the task itself is deleted.
- Per-tick limiting is process-local and safe to reset after controller failover because durable cooldown/task state prevents immediate per-Slot storms.

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
    try skew correction when actual leader skew exceeds threshold
    otherwise try preference convergence only when resulting skew stays allowed
    require target in observed CurrentVoters
    skip Slots inside durable cooldown
    upsert PreferredLeader if needed
    create TaskKindLeaderTransfer

Node reconciler:
  apply assignment cache
  load tasks
  execute TaskKindLeaderTransfer on current leader when known
  if leader/voters are unknown, deterministic checker reports retryable safety failure
  call managed Slot TransferLeadership only after leader and voters are known
  report task result

Runtime observation:
  nodes report SlotRuntimeView.LeaderID
  controller confirms convergence
  task success deletes task
```

## 10. Executor Ownership

`TaskKindLeaderTransfer` should be executed by the current Slot leader when possible. If the current leader or current voter set cannot be resolved, a deterministic checker among active desired peers should report a retryable safety failure. The checker must not bypass leader resolution or call transfer without a known current leader and voter set.

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
- The assignment codec should move to a versioned v2 layout that includes `PreferredLeader` and `LeaderTransferCooldownUntil`.
- Runtime observation and controller RPC/control assignment payload codecs must also carry the new fields needed by planner and manager readers, including `PreferredLeader`, `LeaderTransferCooldownUntil`, and `CurrentVoters`.
- Decoders must accept the old v1 layout and produce zero values for the new fields. Empty `CurrentVoters` means leader-transfer target membership is unknown and must fail closed.
- Mixed-version rolling upgrades are not supported for this metadata change unless old binaries explicitly reject unknown v2 assignment records. Operators should upgrade all controller-capable nodes before enabling preferred-leader balancing.

## 13. Testing Plan

Unit tests:

- Assignment codec round-trip with `PreferredLeader` and old-format decode.
- Planner bootstrap chooses preferred leaders evenly with uneven peer sets.
- Planner preserves preferred leader when still valid after repair/rebalance.
- Planner replaces preferred leader when removed from desired peers.
- Leader rebalance skips slots without quorum, with tasks, in migration, or with invalid targets.
- Leader rebalance creates at most the configured number of tasks per tick.
- Leader rebalance skips single-voter Slots.
- Leader rebalance honors durable `LeaderTransferCooldownUntil`.
- Leader rebalance requires target in observed `CurrentVoters`.
- Terminal failed leader-transfer tasks self-clear and set failure cooldown, allowing later repair/rebalance tasks.
- Unknown leader/current-voter leader-transfer tasks advance retry state through a deterministic checker and do not remain pending forever.
- Preference convergence skips balanced-but-wrong layouts when a single transfer would violate leader skew threshold.

Cluster tests:

- Initial multi-node cluster has near-even Slot leader distribution.
- Restarting a Slot leader causes temporary drift, then controller transfers back or rebalances after cooldown.
- Rolling restart does not trigger transfer storms.
- Single-node cluster may fill `PreferredLeader`, remains stable, and creates no leader-transfer tasks.
- AddSlot selects a balanced preferred leader instead of always the smallest peer.

Manager/usecase tests:

- Slot detail includes preferred leader and leader match status.
- Node leader counts remain based on actual runtime leaders.

## 14. V1 Decisions

- Leader rebalance runs after replica rebalance.
- `PreferredLeader` is persisted before scheduling the transfer because it represents controller intent.
- A failed leader transfer keeps the same preferred leader during the task retry budget.
- After a leader-transfer task reaches failed terminal status, the planner may recompute the preference if the target is no longer eligible.
- Per-Slot successful-transfer cooldown is durable assignment metadata.
- Terminal failed leader-transfer tasks are deleted and converted into a durable per-Slot failure cooldown.
- Leader-transfer target eligibility requires the target to be present in the observed current voter set.
- Unknown leader/current-voter tasks are reported as retryable safety failures by a deterministic checker; they do not wait forever.
- V1 repairs preferred-leader mismatches only when the move preserves or improves global leader balance.
- Per-node sliding transfer windows are out of scope for V1.

## 15. Implementation Stages

### Stage 1: Durable Preferred Leader

Add `PreferredLeader`, `LeaderTransferCooldownUntil`, and observed `CurrentVoters` to controller metadata, codecs, DTOs, bootstrap planner, and AddSlot path. Keep behavior compatible with existing leader transfer code.

### Stage 2: Leader Transfer Task

Add `TaskKindLeaderTransfer`, executor handling, known-leader/current-voter safety checks, retry behavior, durable success/failure cooldown updates, terminal failure self-clear, and observability hooks.

### Stage 3: Planner Leader Rebalance

Add leader load computation, skew detection, candidate selection, cooldown/rate limits, and tests for restart convergence.

### Stage 4: Manager Visibility

Expose preferred leader and leader-match fields in manager Slot detail and node diagnostics.
