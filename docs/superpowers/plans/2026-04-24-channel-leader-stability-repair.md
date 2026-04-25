# Channel Leader Stability And Repair Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `ChannelRuntimeMeta.Leader` from drifting with slot leader changes, add cached node-liveness-based dead leader detection, and make channel leader failover an authoritative slot-leader repair that only elects replicas with the strongest quorum-safe prefix.

**Architecture:** First narrow `RefreshChannelMeta()` so hot-path refresh no longer rewrites leader or ISR from slot topology, then add a unified app-layer node liveness cache fed by controller-leader committed updates and `SyncObservationDelta()` diffs everywhere else. On top of that, add node RPCs for authoritative leader repair and candidate evaluation, extract a dry-run promotion evaluator from the channel reconcile model, and wire `RefreshChannelMeta()` to route dead-leader repair through the current slot leader before applying authoritative runtime metadata.

**Tech Stack:** Go, `testing`, `testify`, `internal/app`, `internal/access/node`, `pkg/cluster`, `pkg/channel`, Markdown specs/plans.

---

## References

- Spec: `docs/superpowers/specs/2026-04-24-channel-leader-stability-repair-design.md`
- Current refresh lifecycle: `internal/app/channelmeta.go`, `internal/app/channelmeta_activate.go`, `internal/app/channelmeta_lifecycle.go`, `internal/app/channelmeta_statechange.go`
- Current bootstrap semantics: `internal/app/channelmeta_bootstrap.go`
- Current send hot path: `internal/usecase/message/retry.go`
- Current node RPC patterns: `internal/access/node/options.go`, `internal/access/node/client.go`, `internal/access/node/channel_append_rpc.go`, `internal/access/node/channel_messages_rpc.go`
- Cluster observation and hooks: `pkg/cluster/config.go`, `pkg/cluster/controller_host.go`, `pkg/cluster/agent.go`, `pkg/cluster/observation_sync.go`
- Channel reconcile model: `pkg/channel/replica/reconcile.go`, `pkg/channel/replica/progress.go`, `pkg/channel/runtime/backpressure.go`, `pkg/channel/transport/transport.go`
- Flow docs to keep aligned: `internal/FLOW.md`, `pkg/channel/FLOW.md`, `pkg/cluster/FLOW.md`, `docs/wiki/channel/leader-switch-reconcile.md`
- Follow `@superpowers:test-driven-development` for every behavior change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- Add English comments on new exported methods, DTOs, and errors to satisfy `AGENTS.md`.

## File Structure

- Modify: `pkg/channel/errors.go` — add the explicit `ErrNoSafeChannelLeader` error used when repair cannot safely elect anyone.
- Modify: `internal/app/channelmeta.go` — rework `RefreshChannelMeta()` and hold the new liveness cache and repairer wiring.
- Create: `internal/app/channelmeta_liveness.go` — app-local node liveness cache helpers and dead-leader repair predicate.
- Modify: `internal/app/channelmeta_lifecycle.go` — narrow lifecycle reconcile to lease renewal only; stop rewriting leader and ISR from slot topology.
- Create: `internal/app/channelmeta_repair.go` — authoritative slot-leader repair coordinator with per-channel singleflight.
- Modify: `internal/app/build.go` — wire new cluster observer hook, app repairer, and node access dependencies.
- Modify: `internal/app/channelmeta_test.go` — unit tests for stable refresh behavior, liveness cache, and repair triggering.
- Modify: `internal/app/multinode_integration_test.go` — integration tests for stable leader across slot changes, dead-leader repair, and restart persistence.
- Modify: `pkg/cluster/config.go` — extend `ObserverHooks` with `OnNodeStatusChange`.
- Modify: `pkg/cluster/controller_host.go` — emit `OnNodeStatusChange` from committed `NodeStatusUpdate` handling on the controller leader.
- Modify: `pkg/cluster/agent.go` — diff `delta.Nodes` during `SyncObservationDelta()` and emit the same hook on every non-controller-leader node.
- Modify: `pkg/cluster/observer_hooks_test.go` — hook emission coverage for committed node-status updates.
- Modify: `pkg/cluster/agent_internal_integration_test.go` — hook emission coverage for observation delta node diffs.
- Modify: `internal/access/node/service_ids.go` — allocate service IDs for channel leader repair and candidate evaluation.
- Modify: `internal/access/node/options.go` — extend adapter dependencies with repairer and evaluator interfaces.
- Modify: `internal/access/node/client.go` — add client helpers for repair redirect flow and candidate evaluation.
- Create: `internal/access/node/channel_leader_repair_rpc.go` — slot-leader-authoritative repair RPC.
- Create: `internal/access/node/channel_leader_repair_rpc_test.go` — RPC redirect and error mapping tests.
- Create: `internal/access/node/channel_leader_evaluate_rpc.go` — slot-leader-to-candidate evaluation RPC.
- Create: `internal/access/node/channel_leader_evaluate_rpc_test.go` — candidate validation and report-shape tests.
- Create: `pkg/channel/transport/probe_client.go` — synchronous reconcile-proof client reusable outside runtime lane orchestration.
- Modify: `pkg/channel/transport/transport.go` — expose or bind the proof client without disturbing existing runtime transport behavior.
- Create: `pkg/channel/replica/promotion_types.go` — durable-view and promotion-report types shared by evaluator tests and app orchestration.
- Create: `pkg/channel/replica/promotion_evaluator.go` — pure quorum-safe promotion evaluator built from existing reconcile logic.
- Create: `pkg/channel/replica/promotion_evaluator_test.go` — evaluator correctness tests.
- Modify: `internal/FLOW.md` — document stable channel leader semantics and liveness-driven repair.
- Modify: `pkg/channel/FLOW.md` — document repair authority split and evaluator reuse.
- Modify: `pkg/cluster/FLOW.md` — document `OnNodeStatusChange`, controller-leader committed updates, and delta-diff propagation everywhere else.
- Modify: `docs/wiki/channel/leader-switch-reconcile.md` — replace the current slot-leader-driven explanation with the authoritative repair flow.

### Task 1: Lock the new refresh and repair semantics with failing tests

**Files:**
- Modify: `pkg/channel/errors.go`
- Modify: `internal/app/channelmeta_test.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Add the new explicit repair error and write the failing unit tests**

Add the error constant in `pkg/channel/errors.go`:

```go
var ErrNoSafeChannelLeader = errors.New("channel: no safe leader candidate")
```

Then add focused unit tests in `internal/app/channelmeta_test.go`, for example:

```go
func TestChannelMetaSyncRefreshDoesNotRewriteLeaderOnSlotLeaderChange(t *testing.T) {
    id := channel.ChannelID{ID: "stable", Type: 1}
    source := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
        ChannelID:    id.ID,
        ChannelType:  int64(id.Type),
        ChannelEpoch: 9,
        LeaderEpoch:  11,
        Replicas:     []uint64{2, 3},
        ISR:          []uint64{2, 3},
        Leader:       3,
        MinISR:       2,
        Status:       uint8(channel.StatusActive),
    }}}
    syncer := &channelMetaSync{source: source, cluster: &fakeChannelMetaCluster{}, localNode: 2}
    syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusAlive)

    got, err := syncer.RefreshChannelMeta(context.Background(), id)

    require.NoError(t, err)
    require.Equal(t, channel.NodeID(3), got.Leader)
    require.Empty(t, source.upserts)
}

func TestChannelMetaSyncRefreshSkipsRepairWhenLeaderLivenessUnknown(t *testing.T) { /* want no repair call */ }
func TestChannelMetaSyncRefreshRepairsDeadLeaderViaSlotLeader(t *testing.T) { /* want changed leader + leader epoch increment */ }
func TestChannelMetaSyncRefreshReturnsErrNoSafeChannelLeaderWhenRepairHasNoCandidate(t *testing.T) { /* want explicit error */ }
func TestChannelMetaSyncRefreshSingleflightsConcurrentLeaderRepair(t *testing.T) { /* want one repair invocation */ }
```

Add one high-value integration test in `internal/app/multinode_integration_test.go`, for example:

```go
func TestSlotLeaderChangeDoesNotDriftHealthyChannelLeader(t *testing.T) { /* keep leader stable after slot leader transfer */ }
```

- [ ] **Step 2: Run the focused app tests to verify they fail**

Run:

```bash
go test ./internal/app -run 'TestChannelMetaSyncRefresh|TestSlotLeaderChangeDoesNotDriftHealthyChannelLeader' -count=1
```

Expected: FAIL because the refresh path still rewrites leader/ISR from slot topology and no repair/liveness plumbing exists yet.

- [ ] **Step 3: Add the minimal test scaffolding only**

Extend the existing app fakes rather than creating a second fake hierarchy. Add only the fields the next slices need, for example:

```go
repairCalls   int
repairResult  metadb.ChannelRuntimeMeta
repairErr     error
```

Keep the tests failing on missing behavior, not on missing fake plumbing.

- [ ] **Step 4: Re-run the same focused tests to confirm they still fail for the intended reason**

Run:

```bash
go test ./internal/app -run 'TestChannelMetaSyncRefresh|TestSlotLeaderChangeDoesNotDriftHealthyChannelLeader' -count=1
```

Expected: FAIL with assertion or compile failures that point directly at the missing refresh / repair behavior.

- [ ] **Step 5: Commit the red test slice**

```bash
git add pkg/channel/errors.go internal/app/channelmeta_test.go internal/app/multinode_integration_test.go
git commit -m "test: pin channel leader stability semantics"
```

### Task 2: Add unified node liveness propagation and cache usage

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/observer_hooks_test.go`
- Modify: `pkg/cluster/agent_internal_integration_test.go`
- Create: `internal/app/channelmeta_liveness.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/channelmeta_test.go`

- [ ] **Step 1: Write the failing hook and liveness-cache tests**

Add cluster-level tests first, for example:

```go
func TestControllerLeaderHandleCommittedCommandInvokesOnNodeStatusChange(t *testing.T) { /* expect from/to status callback */ }
func TestSyncObservationDeltaInvokesOnNodeStatusChangeForNodeDiff(t *testing.T) { /* expect hook on non-leader delta apply */ }
```

Then add app-level tests such as:

```go
func TestChannelMetaSyncNeedsLeaderRepairOnlyForDeadOrDraining(t *testing.T) {
    syncer := &channelMetaSync{}
    syncer.UpdateNodeLiveness(3, controllermeta.NodeStatusDead)

    need, reason := syncer.needsLeaderRepair(metadb.ChannelRuntimeMeta{
        Leader: 3,
        Status: uint8(channel.StatusActive),
        Replicas: []uint64{2, 3},
        ISR: []uint64{2, 3},
    })

    require.True(t, need)
    require.Equal(t, "leader_dead", reason)
}
```

- [ ] **Step 2: Run the focused cluster and app tests to verify they fail**

Run:

```bash
go test ./pkg/cluster -run 'Test(ControllerLeaderHandleCommittedCommandInvokesOnNodeStatusChange|SyncObservationDeltaInvokesOnNodeStatusChangeForNodeDiff)' -count=1
go test ./internal/app -run 'TestChannelMetaSyncNeedsLeaderRepair' -count=1
```

Expected: FAIL because `ObserverHooks` has no node-status callback and `channelMetaSync` has no liveness cache yet.

- [ ] **Step 3: Implement the minimal hook and cache plumbing**

Extend `pkg/cluster/config.go`:

```go
OnNodeStatusChange func(nodeID uint64, from, to controllermeta.NodeStatus)
```

Emit the hook from `pkg/cluster/controller_host.go` only when the local node is the controller leader and a committed command applies a node-status transition. All other nodes, including controller followers, should emit the hook from `pkg/cluster/agent.go` when `SyncObservationDelta()` merges a node whose status changed.

Create `internal/app/channelmeta_liveness.go` with helpers such as:

```go
func (s *channelMetaSync) UpdateNodeLiveness(nodeID uint64, status controllermeta.NodeStatus)
func (s *channelMetaSync) nodeLivenessStatus(nodeID uint64) (controllermeta.NodeStatus, bool)
func (s *channelMetaSync) needsLeaderRepair(meta metadb.ChannelRuntimeMeta) (bool, string)
```

Wire the hook in `internal/app/build.go` so every node updates `app.channelMetaSync` through one callback path.

- [ ] **Step 4: Re-run the focused tests to verify they pass**

Run:

```bash
go test ./pkg/cluster -run 'Test(ControllerLeaderHandleCommittedCommandInvokesOnNodeStatusChange|SyncObservationDeltaInvokesOnNodeStatusChangeForNodeDiff)' -count=1
go test ./internal/app -run 'TestChannelMetaSyncNeedsLeaderRepair' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the liveness slice**

```bash
git add pkg/cluster/config.go pkg/cluster/controller_host.go pkg/cluster/agent.go pkg/cluster/observer_hooks_test.go pkg/cluster/agent_internal_integration_test.go internal/app/channelmeta_liveness.go internal/app/channelmeta.go internal/app/build.go internal/app/channelmeta_test.go
git commit -m "feat: add channel leader liveness cache"
```

### Task 3: Narrow refresh lifecycle to lease renewal only and stop leader drift

**Files:**
- Modify: `internal/app/channelmeta_lifecycle.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelmeta_activate.go`
- Modify: `internal/app/channelmeta_statechange.go`
- Modify: `internal/app/channelmeta_test.go`

- [ ] **Step 1: Write the failing lifecycle-focused tests**

Add narrow tests that pin the new lifecycle boundaries, for example:

```go
func TestReconcileChannelRuntimeMetaRenewsLocalLeaderLeaseWithoutChangingLeaderOrISR(t *testing.T) { /* only LeaseUntilMS changes */ }
func TestRefreshAuthoritativeByKeyOnSlotLeaderChangeDoesNotRewriteChannelLeader(t *testing.T) { /* slot leader changes but channel leader stays */ }
```

- [ ] **Step 2: Run the focused lifecycle tests to verify they fail**

Run:

```bash
go test ./internal/app -run 'Test(ReconcileChannelRuntimeMetaRenewsLocalLeaderLeaseWithoutChangingLeaderOrISR|RefreshAuthoritativeByKeyOnSlotLeaderChangeDoesNotRewriteChannelLeader)' -count=1
```

Expected: FAIL because `reconciledChannelRuntimeMeta()` still rewrites `Replicas`, `ISR`, and `Leader` from slot topology.

- [ ] **Step 3: Implement the minimal lifecycle split**

Replace the current helper shape in `internal/app/channelmeta_lifecycle.go` with a lease-only helper, for example:

```go
func (b *channelMetaBootstrapper) RenewChannelLeaderLease(ctx context.Context, meta metadb.ChannelRuntimeMeta, localNode uint64, renewBefore time.Duration) (metadb.ChannelRuntimeMeta, bool, error)
```

Rules:
- return unchanged when `meta.Status != Active`
- return unchanged when `meta.Leader != localNode`
- never rewrite `Replicas`, `ISR`, `Leader`, `ChannelEpoch`, or `MinISR`
- only upsert if the lease is missing/expired/near expiry

Update `internal/app/channelmeta.go` and `internal/app/channelmeta_activate.go` so refresh calls the new lease helper before dead-leader repair checks.

- [ ] **Step 4: Re-run the focused app tests to verify they pass**

Run:

```bash
go test ./internal/app -run 'TestChannelMetaSyncRefresh|Test(ReconcileChannelRuntimeMetaRenewsLocalLeaderLeaseWithoutChangingLeaderOrISR|RefreshAuthoritativeByKeyOnSlotLeaderChangeDoesNotRewriteChannelLeader)' -count=1
```

Expected: PASS for the non-repair cases; the repair tests may still fail until later tasks land.

- [ ] **Step 5: Commit the lifecycle slice**

```bash
git add internal/app/channelmeta_lifecycle.go internal/app/channelmeta.go internal/app/channelmeta_activate.go internal/app/channelmeta_statechange.go internal/app/channelmeta_test.go
git commit -m "refactor: stop channel leader drift in refresh path"
```

### Task 4: Add node RPCs for authoritative repair and candidate evaluation

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Create: `internal/access/node/channel_leader_repair_rpc.go`
- Create: `internal/access/node/channel_leader_repair_rpc_test.go`
- Create: `internal/access/node/channel_leader_evaluate_rpc.go`
- Create: `internal/access/node/channel_leader_evaluate_rpc_test.go`

- [ ] **Step 1: Write the failing RPC tests**

Add focused repair RPC tests such as:

```go
func TestChannelLeaderRepairRPCRedirectsToCurrentSlotLeader(t *testing.T) { /* expect not_leader + leader_id */ }
func TestChannelLeaderRepairRPCMapsNoSafeCandidateStatus(t *testing.T) { /* expect ErrNoSafeChannelLeader */ }
func TestChannelLeaderRepairRPCReturnsAuthoritativeMetaAfterRepair(t *testing.T) { /* expect meta payload */ }
```

Add evaluation RPC tests such as:

```go
func TestChannelLeaderEvaluateCandidateRPCRejectsReplicaOutsideISR(t *testing.T) { /* expect not_member */ }
func TestChannelLeaderEvaluateCandidateRPCReturnsPromotionReport(t *testing.T) { /* expect report */ }
```

- [ ] **Step 2: Run the focused node RPC tests to verify they fail**

Run:

```bash
go test ./internal/access/node -run 'TestChannelLeader(RepairRPC|EvaluateCandidateRPC)' -count=1
```

Expected: FAIL because the new service IDs, DTOs, and handlers do not exist yet.

- [ ] **Step 3: Implement the minimal DTOs, client helpers, and handlers**

Add service IDs in `internal/access/node/service_ids.go` and extend `Options` / `Adapter` in `internal/access/node/options.go`:

```go
type ChannelLeaderRepairer interface {
    RepairChannelLeader(ctx context.Context, req ChannelLeaderRepairRequest) (ChannelLeaderRepairResult, error)
}

type ChannelLeaderEvaluator interface {
    EvaluateChannelLeaderCandidate(ctx context.Context, req ChannelLeaderEvaluateRequest) (ChannelLeaderPromotionReport, error)
}
```

Implement the repair handler so it checks current slot leadership before delegating:

```go
slotID := a.cluster.SlotForKey(req.ChannelID.ID)
leaderID, err := a.cluster.LeaderOf(slotID)
if err != nil { /* encode no_leader */ }
if !a.cluster.IsLocal(leaderID) { /* encode not_leader with leaderID */ }
```

Implement the candidate evaluation handler so it validates that the local node is inside `req.Meta.ISR` before delegating.

- [ ] **Step 4: Re-run the focused node RPC tests to verify they pass**

Run:

```bash
go test ./internal/access/node -run 'TestChannelLeader(RepairRPC|EvaluateCandidateRPC)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the node RPC slice**

```bash
git add internal/access/node/service_ids.go internal/access/node/options.go internal/access/node/client.go internal/access/node/channel_leader_repair_rpc.go internal/access/node/channel_leader_repair_rpc_test.go internal/access/node/channel_leader_evaluate_rpc.go internal/access/node/channel_leader_evaluate_rpc_test.go
git commit -m "feat: add channel leader repair rpc"
```

### Task 5: Extract the reusable proof client and dry-run promotion evaluator

**Files:**
- Create: `pkg/channel/transport/probe_client.go`
- Modify: `pkg/channel/transport/transport.go`
- Create: `pkg/channel/replica/promotion_types.go`
- Create: `pkg/channel/replica/promotion_evaluator.go`
- Create: `pkg/channel/replica/promotion_evaluator_test.go`

- [ ] **Step 1: Write the failing evaluator tests**

Add focused tests in `pkg/channel/replica/promotion_evaluator_test.go`, for example:

```go
func TestPromotionEvaluatorRejectsLaggingReplica(t *testing.T) { /* stale candidate cannot lead */ }
func TestPromotionEvaluatorPrefersHighestProjectedSafeHW(t *testing.T) { /* larger safe prefix wins */ }
func TestPromotionEvaluatorNeverProjectsBeyondLocalLEO(t *testing.T) { /* candidate <= LEO */ }
func TestPromotionEvaluatorReturnsNoCandidateWhenProofIsInsufficient(t *testing.T) { /* CanLead=false */ }
```

- [ ] **Step 2: Run the focused evaluator tests to verify they fail**

Run:

```bash
go test ./pkg/channel/replica -run 'TestPromotionEvaluator' -count=1
```

Expected: FAIL because the new evaluator files do not exist yet.

- [ ] **Step 3: Implement the minimal pure evaluator and proof client**

Create `pkg/channel/replica/promotion_types.go`:

```go
type DurableReplicaView struct {
    EpochHistory  []channel.EpochPoint
    LEO           uint64
    HW            uint64
    CheckpointHW  uint64
    OffsetEpoch   uint64
}

type PromotionReport struct {
    ProjectedSafeHW     uint64
    ProjectedTruncateTo uint64
    CommitReadyNow      bool
    CanLead             bool
    Reason              string
}
```

Create `pkg/channel/replica/promotion_evaluator.go` with a pure function such as:

```go
func EvaluateLeaderPromotion(meta channel.Meta, local DurableReplicaView, proofs []channel.ReplicaReconcileProof) (PromotionReport, error)
```

The implementation should reuse the same candidate computation rules as reconcile:
- never project beyond local `LEO`
- use quorum-safe prefix calculation, not max `LEO`
- mark `CanLead=false` if no proof can establish a safe prefix

Create `pkg/channel/transport/probe_client.go` to expose a synchronous helper around the existing reconcile proof RPC so app repair code does not have to hand-encode transport frames.

- [ ] **Step 4: Re-run the focused evaluator tests to verify they pass**

Run:

```bash
go test ./pkg/channel/replica -run 'TestPromotionEvaluator' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the evaluator slice**

```bash
git add pkg/channel/transport/probe_client.go pkg/channel/transport/transport.go pkg/channel/replica/promotion_types.go pkg/channel/replica/promotion_evaluator.go pkg/channel/replica/promotion_evaluator_test.go
git commit -m "feat: add channel leader promotion evaluator"
```

### Task 6: Wire authoritative slot-leader repair into the app refresh path

**Files:**
- Create: `internal/app/channelmeta_repair.go`
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/channelmeta_test.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: Write the failing repair-coordinator tests**

Add focused tests before implementation, for example:

```go
func TestChannelLeaderRepairerRereadsAuthoritativeMetaBeforeChoosingCandidate(t *testing.T) { /* stale observed leader repaired against reread meta */ }
func TestChannelLeaderRepairerSelectsCandidateWithHighestProjectedSafeHW(t *testing.T) { /* evaluator reports drive choice */ }
func TestChannelLeaderRepairerPersistsLeaderEpochAndLeaseOnly(t *testing.T) { /* membership unchanged */ }
func TestChannelLeaderRepairerReturnsChangedFalseWhenRepairNoLongerNeeded(t *testing.T) { /* reread says healthy */ }
```

Add or extend one integration test:

```go
func TestDeadChannelLeaderRepairPersistsAcrossRestart(t *testing.T) { /* restart still reads repaired leader */ }
```

- [ ] **Step 2: Run the focused repair tests to verify they fail**

Run:

```bash
go test ./internal/app -run 'TestChannelLeaderRepairer|TestDeadChannelLeaderRepairPersistsAcrossRestart' -count=1
```

Expected: FAIL because no repair coordinator exists yet.

- [ ] **Step 3: Implement the minimal repair coordinator and refresh wiring**

Create `internal/app/channelmeta_repair.go` with a coordinator like:

```go
type channelLeaderRepairer struct {
    store     channelMetaSource
    cluster   channelMetaBootstrapCluster
    remote    *accessnode.Client
    localNode uint64
    now       func() time.Time
    sf        singleflight.Group
}
```

Implement:

```go
func (r *channelLeaderRepairer) RepairIfNeeded(ctx context.Context, meta metadb.ChannelRuntimeMeta, reason string) (metadb.ChannelRuntimeMeta, bool, error)
```

Rules:
- singleflight by `channelID/channelType`
- authoritative reread first
- if reread says no repair needed, return latest with `Changed=false`
- evaluate only candidates in persisted `ISR`
- persist only `Leader`, `LeaderEpoch`, and `LeaseUntilMS`
- reread authoritative meta after `UpsertChannelRuntimeMeta()` and return it

Wire `channelMetaSync.RefreshChannelMeta()` to call the repairer only when `needsLeaderRepair(...)` returns true.

- [ ] **Step 4: Re-run the focused repair tests to verify they pass**

Run:

```bash
go test ./internal/app -run 'TestChannelMetaSyncRefresh|TestChannelLeaderRepairer|TestDeadChannelLeaderRepairPersistsAcrossRestart' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the app repair slice**

```bash
git add internal/app/channelmeta_repair.go internal/app/channelmeta.go internal/app/build.go internal/app/channelmeta_test.go internal/app/multinode_integration_test.go
git commit -m "feat: add authoritative channel leader repair"
```

### Task 7: Align documentation and run focused verification

**Files:**
- Modify: `internal/FLOW.md`
- Modify: `pkg/channel/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `docs/wiki/channel/leader-switch-reconcile.md`
- No source changes expected unless verification exposes a proven defect.

- [ ] **Step 1: Update flow and wiki docs to match the new behavior**

Make sure the docs explicitly state:
- slot leader change no longer directly rewrites channel leader
- `RefreshChannelMeta()` uses `nodeLivenessCache` first
- dead-leader failover is executed by the current slot leader
- candidate selection uses the shared quorum-safe evaluator

- [ ] **Step 2: Run focused verification on all changed slices**

Run:

```bash
go test ./internal/app ./internal/access/node ./pkg/cluster ./pkg/channel/replica -count=1
```

Expected: PASS.

- [ ] **Step 3: Run one targeted send-path regression command**

Run:

```bash
go test ./internal/usecase/message -run 'TestSendWithEnsuredMeta' -count=1
```

Expected: PASS without introducing new hot-path controller reads.

- [ ] **Step 4: If verification exposes a failure, fix only the proven issue and re-run the same commands**

Do not bundle unrelated cleanup. Keep fixes scoped to the failing output.

- [ ] **Step 5: Commit docs and verification-driven fixes**

```bash
git add internal/FLOW.md pkg/channel/FLOW.md pkg/cluster/FLOW.md docs/wiki/channel/leader-switch-reconcile.md
git commit -m "docs: describe stable channel leader repair flow"
```
