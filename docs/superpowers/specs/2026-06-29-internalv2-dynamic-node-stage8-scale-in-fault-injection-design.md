# internalv2 Dynamic Node Stage 8 Scale-In Fault Injection Design

Date: 2026-06-29
Status: Proposed for review
Scope: internalv2 dynamic node scale-in and removal, e2ev2, gofail fault injection

## 1. Purpose

Stage 8 hardens the dynamic node removal path under reproducible failures.
Stages 5 and 6 proved the scale-in and removal happy paths through real
`cmd/wukongimv2` processes. Stage 7 added gofail-backed fault injection for
dynamic join, onboarding, and Slot replica movement. The remaining high-risk
gap is scale-in under failure: a leaving node must never be half removed, and
every failed operation must leave enough evidence to explain the root cause.

Stage 8 is therefore a test and observability stage first. It should reuse the
existing clusterv2 gofail markers where possible. Production failpoints should
only be added if a real failure window cannot be reached with the existing
network and Slot replica-move markers.

## 2. Current State

Useful existing coverage:

- `test/e2ev2/cluster/dynamic_node_join` proves dynamic join, activation,
  cross-node delivery, onboarding, live-session drain, scale-in Slot drain,
  safety conflicts, idempotent remove, negative join paths, and concurrent task
  creation.
- `test/e2ev2/cluster/dynamic_node_faults` proves gofail binary exposure,
  seed-join retry through node-lifecycle RPC faults, onboarding control-write
  retry, delayed Slot replica-move leader transfer, and joining-node restart
  during onboarding.
- `pkg/clusterv2/net/transport.go` already exposes service-scoped gofail
  markers for typed RPC calls and sends.
- `pkg/clusterv2/tasks/slot_replica_move.go` already exposes delay markers for
  `promote learner`, `transfer leader`, and `remove voter`.

The gap is not basic scale-in behavior. The gap is failure-time proof for:

- `MarkNodeLeaving` and `MarkNodeRemoved` control writes when a manager node is
  forwarding to the Controller leader.
- Slot replica movement away from a leaving node while the leaving node is
  still a Slot leader or voter.
- Final remove safety while a scale-in task is delayed, retried, or recovered
  after process restart.

## 3. Goals

1. Prove scale-in Slot drain remains safe when the replica-move executor is
   delayed at the leaving-node leader transfer or remove-voter phase.
2. Prove Channel drain inventory failures are fail-closed and cannot permit
   final removal.
3. Prove gateway drain and runtime summary RPC failures are fail-closed and
   cannot permit final removal.
4. Prove a final `MarkNodeRemoved` commit whose response is lost remains
   idempotent: retry must observe `removed` or return `changed=false`.
5. Prove a leaving node restart during scale-in Slot drain does not lose the
   durable task and eventually reaches `safe_to_remove=true`.
6. Every failure scenario must capture root-cause evidence through gofail count,
   manager scale-in status, task counters, join state, and diagnostics.

## 4. Non-Goals

- Do not add or remove Controller Raft voters.
- Do not add random chaos, long-running soak, or unbounded fault matrices.
- Do not create a generic fault framework beyond the existing gofail pattern.
- Do not physically delete removed node identities from ControllerV2 state.
- Do not test storage corruption, clock skew, or network partitions in Stage 8.
- Do not expand Channel physical migration semantics; Stage 8 only verifies the
  existing scale-in blockers and Slot drain path.

## 5. Design

Stage 8 adds new opt-in tests under:

```text
test/e2ev2/cluster/dynamic_node_faults
```

The tests keep the existing Stage 7 contract:

- They require `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.
- They require a gofail-enabled `cmd/wukongimv2` binary through
  `WK_E2EV2_BINARY`.
- Every node receives a distinct loopback `GOFAIL_HTTP` endpoint.
- Tests use manager HTTP, WKProto, process restart helpers, and public status
  APIs only.

Stage 8 should reuse existing markers where they reach the real fault window:

- `wkSlotReplicaMoveTransferLeaderDelay` and
  `wkSlotReplicaMoveRemoveVoterDelay` for scale-in Slot drain tasks, because
  scale-in uses the same `slot_replica_move` task kind as onboarding.
- `wkClusterNetCallShardFault` with `return("manager_connections:...")` for
  manager runtime-summary or drain-mode RPC failures.

Two scale-in/remove-specific windows need narrow markers because generic
transport faults only prove pre-write failure:

- `wkScaleInChannelDrainInventoryFault` in the Channel drain inventory scan
  path, to prove Channel inventory unknown remains fail-closed.
- `wkMarkNodeRemovedPostCommitFault` after `MarkNodeRemoved` has committed and
  published state, to prove response loss after commit remains idempotent.

## 6. Required Scenarios

### Scenario A: Delayed Scale-In Slot Drain

Onboard one Slot replica to node 4, mark node 4 leaving, enable gateway drain,
and plan one scale-in Slot move away from node 4. Ensure the selected Slot has
node 4 as source, then enable `wkSlotReplicaMoveTransferLeaderDelay` or
`wkSlotReplicaMoveRemoveVoterDelay` on node 4 and static nodes.

The test must prove while the delay is active:

- node 4 is `leaving`.
- `safe_to_remove=false`.
- Slot or task blockers are visible, including `blocked_by_tasks=true`,
  `active_task_count>0`, `slot_replica_count>0`, or
  `blocked_by_slot_runtime=true`.
- gofail count is at least one.

After disabling the delay, the test must prove:

- scale-in Slot drain completes.
- `blocked_by_slots=false`.
- failed task count stays zero.
- node 4 can be removed.

### Scenario B: Channel Drain Inventory Fails Closed

Add a narrow `wkScaleInChannelDrainInventoryFault` marker around Channel drain
inventory scanning in `internalv2/usecase/management/channel_drain.go`. Activate
node 4, mark it leaving, enable gateway drain, and inject the inventory fault
before requesting scale-in status and final remove.

The test must prove:

- gofail count is at least one.
- `unknown_channel_inventory=true`.
- `blocked_by_channels=true`.
- `safe_to_remove=false`.
- final remove returns a bounded conflict and node 4 stays `leaving`.
- after disabling the failpoint, Channel counters return to zero and remove can
  reach `removed`.

### Scenario C: Gateway Runtime Summary Fails Closed

Use existing `wkClusterNetCallShardFault` with
`return("manager_connections:temporary runtime summary fault")` to fault the
owner-node manager connection RPC used for drain mode and runtime summary.
The alias is plural because it comes from the manager connections RPC/service
alias used by the clusterv2 network fault injector.
Activate node 4, mark it leaving, then request drain/status/remove through
manager HTTP while the RPC is faulted.

The test must prove:

- gofail count is at least one.
- `unknown_runtime=true` or `runtime_unknown=true`.
- `blocked_by_runtime_drain=true`.
- `safe_to_remove=false`.
- final remove returns a bounded conflict and node 4 stays `leaving`.
- after disabling the failpoint, drain mode can be set, runtime counters are
  zero, and final remove can reach `removed`.

### Scenario D: Removed Post-Commit Response Loss Is Idempotent

Add a narrow `wkMarkNodeRemovedPostCommitFault` marker after
`MarkNodeRemoved` has committed, published state, and before the success result
returns. Activate node 4, mark it leaving, enable gateway drain, and wait until
`safe_to_remove=true`. Enable the post-commit marker and call final remove.

The test must prove:

- the first remove returns a bounded injected failure after the commit window.
- gofail count is at least one.
- manager inventory eventually observes `join_state=removed`, or the retry
  returns `join_state=removed`.
- a second remove returns `changed=false`.
- `failed_task_count=0`, and Slot/task counters do not regress.

### Scenario E: Restart During Scale-In Slot Drain

Prepare the same onboarded node 4 scale-in drain as Scenario A. Hold the task
with a Slot replica-move delay, wait until manager status reports an active
scale-in task, then restart node 4. Disable the delay on stable endpoints and
tolerate a disabled failpoint on the restarted endpoint, matching the Stage 7
restart pattern.

The test must prove:

- the task survives process restart.
- node 4 returns ready and stays `leaving`.
- scale-in status eventually reaches `safe_to_remove=true`.
- final status has zero failed tasks and no active task blockers.
- final remove reaches `removed`.

This scenario proves durable Slot task recovery across a leaving-node restart.
Gateway drain admission is runtime state and may need to be re-applied after
restart before final remove; that must not be confused with recreating Slot
drain tasks.

## 7. Evidence Rules

Each Stage 8 e2ev2 failure must include enough evidence for root-cause-first
debugging:

- failpoint name, configured expression, and execution count.
- raw manager response status and body for intentionally faulted calls.
- `join_state`, `safe_to_remove`, `blocked_by_slots`,
  `blocked_by_tasks`, `active_task_count`, and `failed_task_count`.
- Slot drain candidate details: Slot ID, source node, target node, desired
  peers, target peers, and config epoch.
- `cluster.DumpDiagnostics()` on every assertion failure.

Fixed sleeps are allowed only for failpoint delays that intentionally block a
phase. All readiness and convergence checks must poll public manager status or
gofail HTTP state.

## 8. Exit Criteria

Stage 8 is complete when:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 12m -p=1
```

pass on the implementation branch and again after local merge to `main`.
