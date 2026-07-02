# ChannelV2 Disaster Recovery Design

**Date:** 2026-07-02
**Status:** Approved for implementation planning
**Scope:** ChannelV2 data-plane disaster recovery for a three-node cluster that loses one data node.

## Goal

ChannelV2 should tolerate one failed data node in a three-node cluster without losing quorum-acknowledged messages. Existing channels whose ChannelV2 leader is on the failed node should recover write availability after a bounded failover window. Existing channels whose leader survives should keep writing as long as `MinISR` can still be satisfied. New channel placement remains fail-closed when the configured replica count cannot be met.

This design keeps cluster semantics intact for single-node clusters and multi-node clusters. It does not add a bypass path around Slot metadata, ControllerV2 health, or ChannelV2 routing.

## Current State

The current system already has important building blocks:

- `ChannelRuntimeMeta` is the authoritative channel data-plane routing record in Slot metadata. It carries `ChannelEpoch`, `LeaderEpoch`, `Replicas`, `ISR`, `Leader`, `MinISR`, `LeaseUntilMS`, and `WriteFence*` fields.
- Slot metadata writes are replicated through Slot Multi-Raft, so a three-node Slot group can continue metadata proposals with one failed replica when quorum remains.
- ChannelV2 append uses a fixed leader from `ChannelRuntimeMeta`. Followers pull and durably apply records, and leader HW advances from ISR match offsets.
- `pkg/db/meta` and `pkg/slot/fsm` already contain `ChannelMigrationTask` primitives and atomic task+metadata commands for write fence, leader transfer, learner add, learner promote/remove, fence clear, and abort.

The missing pieces are the runtime and policy layers that make those primitives effective:

- `WriteFence*` is not projected into `channelv2.Meta`, so append admission is not yet fenced by migration or failover tasks.
- `LeaseUntilMS` is not enforced in the ChannelV2 write path.
- There is no ChannelV2 migration executor that drives the existing durable task phases.
- There is no automatic repair loop that creates leader failover or replica replacement tasks from node health evidence.
- There is no e2ev2 kill-node test proving message safety and write recovery.

## HA Contract

For the first production-ready scope:

- Cluster shape: three active data nodes, ChannelV2 replica count `3`, `MinISR=2`.
- Failure tolerance: one data node can stop or become health-stale.
- RPO: no loss of messages that received quorum `SENDACK` before the failure.
- RTO: bounded by node health detection plus ChannelV2 failover execution. The default detection source is the existing ControllerV2 node health TTL.
- New channel policy: fail-closed when health-schedulable data nodes are fewer than the configured replica count.
- Degraded mode: after one node is down, existing channels may continue with the remaining ISR quorum. Replica replacement waits for a healthy replacement node.

The design may preserve an unacknowledged tail if that tail is present on the selected survivor. That is acceptable because it avoids losing acknowledged records. The new leader must not expose gaps or conflicting tails, and tail records beyond a proven committed frontier must become committed only after quorum coverage under the new leader epoch.

## Recommended Approach

Use the existing durable `ChannelMigrationTask` model as the common mechanism for planned migration and automatic repair.

Rejected alternatives:

- Directly rewriting `ChannelRuntimeMeta.Leader` is too risky because it lacks write fencing, target proof, and stale leader fencing.
- Making every channel a Raft group would solve failover generically, but it is too expensive for large groups, high message rates, and large channel counts.

The implementation should land in two layers:

1. Complete the manual ChannelV2 migration executor and write-fence admission.
2. Add automatic ChannelV2 repair scheduling that creates the same task types when node health proves a leader or replica is unavailable.

## Architecture

```text
ControllerV2 node health
  -> clusterv2 control snapshot
  -> bounded ChannelV2 repair scanner on current Slot leaders
  -> ChannelMigrationTask create/claim/advance through Slot metadata
  -> ChannelV2 migration executor
  -> fenced ChannelRuntimeMeta changes
  -> ChannelV2 runtime applies new Meta and reopens writes
```

Ownership:

- `pkg/channelv2` owns reusable append admission, fencing, leader/follower state transitions, runtime probes, and safe leader activation primitives.
- `pkg/clusterv2/channels` owns Slot-backed metadata projection, append authority resolution, ChannelV2 transport, and repair/executor composition.
- `pkg/db/meta`, `pkg/slot/fsm`, and `pkg/slot/proxy` own authoritative task and metadata storage commands.
- `internalv2/usecase/management` owns manager-facing validation and read/write management APIs.
- `internalv2/access/manager` remains HTTP adaptation only.
- `internalv2/app` wires the executor, observers, and configuration.

## Phase 1: Fence And Lease Admission

Add a narrow write-fence projection into `channelv2.Meta`:

```go
type WriteFence struct {
	Token     string
	Version   uint64
	Reason    WriteFenceReason
	UntilTime time.Time
}
```

Append admission rules:

- No fence: append can proceed if the node is the authoritative leader and all existing epoch checks pass.
- Active fence: append returns a retryable write-fenced/not-ready error.
- Expired fence: append must refresh authoritative metadata. Wall-clock expiry alone never reopens writes.
- Drained fail-closed state: append remains blocked until the runtime applies authoritative metadata with a cleared or superseded fence version.

Leader lease rules:

- Do not renew leases per channel in the hot path.
- Use a node-level data-plane lease derived from the node's ability to keep fresh ControllerV2 health and Slot/control visibility.
- A node that cannot maintain the data-plane lease must stop admitting ChannelV2 leader writes, even if its local runtime still thinks it is leader.
- `LeaderEpoch` remains the primary distributed fence. The node lease prevents an isolated stale leader from continuing to accept writes before it observes the newer epoch.

## Phase 2: Manual Migration Executor

Complete the executor for two durable task kinds:

- `LeaderTransfer`: move leadership to an existing ISR replica without changing `Replicas` or `ISR`.
- `ReplicaReplace`: replace one ISR replica with a healthy target, using learner catch-up before promotion.

The executor must use the existing atomic Slot FSM commands:

- `CreateChannelMigrationTaskWithRuntimeGuard`
- `ClaimChannelMigrationTask`
- `AdvanceChannelMigrationTask`
- `SetChannelWriteFence`
- `CommitChannelLeaderTransfer`
- `AddChannelLearner`
- `PromoteLearnerAndRemoveReplica`
- `ClearChannelWriteFence`
- `AbortChannelMigration`

Leader transfer sequence:

```text
Validate
  -> ProbeTarget
  -> SetWriteFence
  -> DrainOldLeader
  -> FinalTargetCatchUp
  -> CommitLeaderMeta
  -> VerifyNewLeader
  -> ClearFence
  -> Completed
```

Replica replacement sequence:

```text
Validate
  -> TransferLeaderIfSourceIsLeader
  -> AddLearner
  -> BootstrapTarget
  -> WarmCatchUp
  -> SetWriteFence
  -> FinalCatchUp
  -> PromoteLearnerAndRemoveSource
  -> VerifyMembership
  -> ClearFence
  -> Completed
```

Manual migration is the safest first implementation target because it validates the fence, proof, and executor model without relying on automatic crash detection.

## Phase 3: Automatic Leader Failover

Add automatic repair scheduling after manual migration is reliable.

Detection:

- The current Slot leader scans a bounded page of `ChannelRuntimeMeta` rows for the hash slots it owns.
- A channel is a failover candidate when `meta.Leader` is stale, missing, down, removed, leaving, or not runtime-ready according to ControllerV2 health.
- Existing active migration tasks block duplicate repair for the same channel.

Candidate selection:

- Consider only `ISR` replicas that are healthy, active, data-role, runtime-ready, and not leaving.
- Probe all surviving ISR replicas under the current `ChannelEpoch` and `LeaderEpoch`.
- Prefer the replica with the highest compatible durable LEO. This preserves any quorum-acknowledged tail that survived on exactly one follower.
- Require compatible epoch lineage and no pending truncation below the safe cutover prefix.
- If no candidate can prove safety, leave the task blocked with a precise blocker reason.

Failover commit:

```text
Leader = selected survivor
LeaderEpoch = old LeaderEpoch + 1
ChannelEpoch unchanged
Replicas unchanged
ISR unchanged
WriteFence remains set until new leader is verified
```

After commit:

- The selected node applies the new meta and becomes leader.
- Other surviving replicas pull from the new leader.
- The new leader only reports append success after quorum coverage under the new leader epoch.
- The old leader, if it later returns, is fenced by `LeaderEpoch` and the node-level lease. It must rejoin as follower or be repaired by a later replica replacement task.

## Phase 4: Automatic Replica Repair

Replica repair handles long-lived degraded placement after leader failover or follower loss.

Rules:

- Do not reduce `MinISR` automatically in the first scope.
- Do not create a new channel with fewer than the configured replica count.
- If a healthy replacement node exists, create a `ReplicaReplace` task from the failed source to the target.
- If no replacement exists, report degraded channel replica health and keep existing channels running when their remaining ISR can satisfy `MinISR`.
- If the failed source is the current leader, first complete leader failover or embedded leader transfer.

Replica repair should be concurrency-limited globally, per source node, and per target node so a failed node does not create an unbounded migration storm.

## Performance Rules

The repair path must stay out of the foreground SEND hot path.

- Scans are bounded by slot, page size, and per-tick budgets.
- Task creation is rate-limited and deduplicated by the active task index.
- Probes are batched per node where practical and capped by concurrency.
- The append path performs only local admission checks, cached metadata checks, and explicit authoritative refresh on retryable fence/stale errors.
- Large-channel recipient fanout and post-commit effects are not part of the failover decision.
- Metrics use low-cardinality labels: task kind, phase, result, blocker code, source role, and target role. Do not label by channel ID.

## Observability

Expose:

- active ChannelV2 migration/failover task count,
- task phase and duration,
- failover candidate scan backlog,
- blocked task count by blocker code,
- selected target lag and final catch-up lag,
- write-fence duration and timeout/reset count,
- leader epoch change count,
- replica repair success/failure count,
- node-level data-plane lease state.

Manager pages should separate:

- Slot Raft leader status,
- ChannelV2 leader status,
- channel replica health,
- active migration/failover task state.

This separation avoids repeating the existing confusion between Slot preferred leader, live Slot Raft leader, and ChannelV2 data leader.

## Testing Strategy

Unit tests:

- `pkg/channelv2`: active fence blocks append; expired fence does not reopen writes without newer authoritative metadata; role changes with higher `LeaderEpoch` clear stale waiters.
- `pkg/channelv2`: selected new leader can serve follower pulls under a newer leader epoch without exposing gaps.
- `pkg/db/meta`: stale task/meta guards reject outdated leader transfer and replica replacement commands.
- `pkg/db/meta`: active fences are preserved by unrelated retention or repair writes.
- `pkg/clusterv2/channels`: metadata projection carries write fence and lease fields.
- `pkg/clusterv2/channels`: failover candidate selection chooses the highest compatible healthy ISR survivor.

Integration tests:

- Three-node channel leader transfer with writes before, during, and after transfer.
- Three-node replica replacement where source is follower.
- Three-node replica replacement where source is leader.
- Executor restart during fence and catch-up phases.
- Slot leader change during active migration and owner lease handoff.
- Old leader restart after leader failover; stale leader must not accept writes.
- e2ev2 real-process test: create channels distributed across leaders, kill one node, verify surviving-leader channels continue, dead-leader channels recover after failover, and committed message sequences remain contiguous.

Verification commands should stay focused during development:

```bash
GOWORK=off go test -count=1 ./pkg/channelv2/... ./pkg/clusterv2/channels ./pkg/clusterv2
GOWORK=off go test -count=1 ./pkg/db/meta ./pkg/slot/fsm ./pkg/slot/proxy
GOWORK=off go test -count=1 ./internalv2/usecase/management ./internalv2/infra/cluster ./internalv2/app
```

Real crash/restart coverage belongs in e2ev2 or integration-tagged suites when it needs real processes or longer timing windows.

## Rollout Sequence

1. Land fence and node-lease append admission without automatic repair.
2. Land manual `LeaderTransfer` executor and manager read APIs.
3. Land manual `ReplicaReplace` executor.
4. Add automatic leader failover scheduling, disabled by default if a rollout flag is needed.
5. Add automatic replica repair scheduling.
6. Add e2ev2 one-node-failure coverage and manager observability.
7. Enable automatic failover in staged deployments after metrics prove bounded task volume and no write-fence leaks.

## Decisions

- Use durable `ChannelMigrationTask` for both manual migration and automatic repair.
- Keep `ChannelRuntimeMeta` as the only authoritative ChannelV2 topology source.
- Use `LeaderEpoch` for leader fencing and `ChannelEpoch` for replica membership changes.
- Keep new channel placement fail-closed when the configured replica count cannot be satisfied.
- Do not decrease `MinISR` automatically in the first production scope.
- Use bounded background scans instead of foreground SEND-triggered repair.
- Require real e2ev2 kill-node validation before calling the feature complete.
