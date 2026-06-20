# Presence Authority Raft-Term Fencing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the long-term route-authority fencing flaw exposed by controller task startup churn: cross-node presence and conversation authority calls must be fenced by distributed Slot Raft identity, not by each node's local `AuthorityEpoch` counter.

**Architecture:** Carry actual Slot Raft `Term` and control `ConfigEpoch` from `pkg/slot/multiraft` through `pkg/clusterv2/slots`, `pkg/clusterv2/routing`, `pkg/clusterv2.Route`, and `RouteAuthority`. Keep `AuthorityEpoch` only as a local observation/diagnostic sequence. Internalv2 presence and conversation authority targets validate `(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)` and treat `RouteRevision` as ordering/diagnostic metadata.

**Tech Stack:** Go, `pkg/clusterv2`, `internalv2/runtime/presence`, `internalv2/infra/cluster`, `internalv2/app`, black-box smoke script under `scripts/`.

---

## Why This Is The Right Boundary

The failure mode was:

```text
gateway on node1
  -> RouteKey(uid) says hash_slot=19 slot_id=1 leader=2 authority_epoch=4
  -> node1 RPCs node2
  -> node2 runtime/presence.Directory has same Slot leader but local authority_epoch=1
  -> Directory rejects with ErrNotLeader
```

`AuthorityEpoch` is process-local, so it cannot be a distributed hard fence. The distributed authority identity must come from Raft/control facts:

```text
HashSlot
SlotID
LeaderNodeID       actual observed Slot Raft leader, not PreferredLeader
LeaderTerm         actual Slot Raft term for that leader observation
ConfigEpoch        control-plane Slot configuration epoch
```

This also preserves the Raft principle discussed earlier: `PreferredLeader` remains a recommendation. If a target node is behind, Raft should elect any legal leader; the application follows the observed `(LeaderNodeID, LeaderTerm)`.

## File Structure

```text
pkg/clusterv2/
  api.go
  node.go
  node_snapshot.go
  default_slot_leaders.go
  FLOW.md
  routing/
    table.go
    router_test.go
  slots/
    types.go
    observer.go
    slots_test.go

internalv2/runtime/presence/
  types.go
  directory.go
  directory_test.go
  FLOW.md

internalv2/usecase/conversation/
  types.go

internalv2/infra/cluster/
  presence.go
  presence_test.go
  conversation_authority.go
  conversation_authority_test.go
  FLOW.md

internalv2/access/node/
  presence_codec.go
  presence_codec_test.go
  presence_rpc_test.go
  FLOW.md

internalv2/app/
  app.go
  presence_touch.go
  conversation_route_lifecycle.go
  app_test.go
  FLOW.md
```

## Task 1: Carry Slot Raft Term And ConfigEpoch Through Routing

- [ ] Add failing tests in `pkg/clusterv2/slots/slots_test.go`.

```go
func TestStatusSnapshotMapsRuntimeStatus(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.status[1] = multiraft.Status{
		SlotID: 1, LeaderID: 2, Term: 9,
		CurrentVoters: []multiraft.NodeID{1, 2, 3},
	}
	got := StatusSnapshot(runtime, []uint32{1})
	if len(got) != 1 || got[0].Term != 9 {
		t.Fatalf("StatusSnapshot() = %#v, want term 9", got)
	}
}
```

- [ ] Add failing tests in `pkg/clusterv2/routing/router_test.go`.

```go
func TestRouterRouteIncludesLeaderTermAndConfigEpoch(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}})
	route, err := r.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.LeaderTerm != 9 || route.ConfigEpoch != 1 {
		t.Fatalf("route = %#v, want leaderTerm=9 configEpoch=1", route)
	}
}
```

- [ ] Modify `pkg/clusterv2/slots/types.go`.

```go
type Status struct {
	SlotID uint32
	Leader uint64
	// Term is the Slot Raft term observed with Leader.
	Term uint64
	Peers []uint64
}
```

- [ ] Modify `pkg/clusterv2/slots/observer.go` to map `multiraft.Status.Term`.

```go
item := Status{
	SlotID: uint32(status.SlotID),
	Leader: uint64(status.LeaderID),
	Term: status.Term,
	Peers: make([]uint64, 0, len(status.CurrentVoters)),
}
```

- [ ] Modify `pkg/clusterv2/routing/table.go`.

Add immutable maps:

```go
SlotLeaderTerms map[uint32]uint64
SlotConfigEpochs map[uint32]uint64
```

Add fields:

```go
type Route struct {
	LeaderTerm uint64
	ConfigEpoch uint64
}

type SlotStatus struct {
	SlotID uint32
	Leader uint64
	// LeaderTerm is the Slot Raft term for Leader.
	LeaderTerm uint64
}
```

Populate `SlotConfigEpochs` from `control.SlotAssignment.ConfigEpoch`, copy both maps in `cloneWithLeaders`, and set route fields in `routeHashSlot`.

- [ ] Keep the existing `Leader=0` behavior: an unknown Slot status observation must not erase the previous non-zero leader or term.

```go
if item.Leader == 0 {
	continue
}
out.SlotLeaders[item.SlotID] = item.Leader
out.SlotLeaderTerms[item.SlotID] = item.LeaderTerm
```

- [ ] Modify `pkg/clusterv2/default_slot_leaders.go`.

```go
out = append(out, routing.SlotStatus{
	SlotID: status.SlotID,
	Leader: status.Leader,
	LeaderTerm: status.Term,
})
```

- [ ] Run focused tests.

```bash
go test ./pkg/clusterv2/slots ./pkg/clusterv2/routing -count=1
```

Expected: both packages pass.

## Task 2: Expose Distributed Authority Identity In clusterv2 Public API

- [ ] Add fields to `pkg/clusterv2/api.go`.

```go
type RouteAuthority struct {
	HashSlot uint16
	SlotID uint32
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence. It is not a distributed fence.
	AuthorityEpoch uint64
}

type Route struct {
	LeaderTerm uint64
	ConfigEpoch uint64
}
```

- [ ] Modify `pkg/clusterv2/node_snapshot.go`.

Update `routeAuthorityKey`:

```go
type routeAuthorityKey struct {
	slotID uint32
	leaderNodeID uint64
	leaderTerm uint64
	configEpoch uint64
	revision uint64
}
```

Build keys from `routing.Table`:

```go
current := routeAuthorityKey{
	slotID: slotID,
	leaderNodeID: after.SlotLeaders[slotID],
	leaderTerm: after.SlotLeaderTerms[slotID],
	configEpoch: after.SlotConfigEpochs[slotID],
	revision: after.Revision,
}
```

Populate public events:

```go
RouteAuthority{
	HashSlot: hashSlotID,
	SlotID: current.slotID,
	LeaderNodeID: current.leaderNodeID,
	LeaderTerm: current.leaderTerm,
	ConfigEpoch: current.configEpoch,
	RouteRevision: current.revision,
	AuthorityEpoch: n.authorityEpochForChange(hashSlotID, previous, ok, current),
}
```

Update `convertRoute` to copy `LeaderTerm` and `ConfigEpoch`.

- [ ] Keep `authorityEpochForChange` only for compatibility/diagnostics. It may still increment when `(SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)` changes, but no internalv2 hard fence should rely on it after this plan.

- [ ] Update tests in `pkg/clusterv2/route_authority_test.go`.

Add/adjust assertions:

```go
if got.LeaderTerm != 9 || got.ConfigEpoch != 1 {
	t.Fatalf("authority = %#v, want leaderTerm=9 configEpoch=1", got)
}
```

Replace the route test intent from `AuthorityEpoch`-only to the new fields:

```text
TestRouteIncludesLeaderTermAndConfigEpoch
  create Node with routing.NewRouter()
  install routeAuthoritySnapshot(1)
  call UpdateSlotLeaders with SlotID=1 Leader=2 LeaderTerm=9
  mark node started
  call RouteHashSlot(0)
  assert route.LeaderTerm == 9
  assert route.ConfigEpoch == 1
```

- [ ] Move authority publication ahead of task execution in `Node.applySnapshot`.

Current order publishes after `tasks.Reconcile`, so a slow/blocked task executor can delay presence authority events. Keep Slot reconciliation before publish, then publish before task reconcile:

```go
if n.slots != nil && (firstSnapshot || changes.slots) {
	if err := n.slots.Reconcile(ctx, snapshot); err != nil {
		return err
	}
}
n.publishRouteAuthority(routeAuthorities...)
routeAuthorities = nil
if n.tasks != nil && (firstSnapshot || changes.tasks || changes.slots) {
	if err := n.tasks.Reconcile(ctx, snapshot); err != nil {
		return err
	}
}
```

- [ ] Add a regression test in `pkg/clusterv2/route_authority_test.go`.

Use a fake `taskExecutor` that blocks in `Reconcile`. Start `applySnapshot` in a goroutine and assert the watcher receives the `RouteAuthorityEvent` before unblocking the executor.

- [ ] Run focused tests.

```bash
go test ./pkg/clusterv2 ./pkg/clusterv2/routing ./pkg/clusterv2/slots -count=1
```

Expected: all pass.

## Task 3: Change Presence Directory Fence To Raft Identity

- [ ] Add fields to `internalv2/runtime/presence/types.go`.

```go
type RouteTarget struct {
	HashSlot uint16
	SlotID uint32
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence kept for diagnostics only.
	AuthorityEpoch uint64
}
```

- [ ] Add failing tests in `internalv2/runtime/presence/directory_test.go`.

The key regression: a different local `AuthorityEpoch` must not reject an otherwise identical distributed authority identity.

```go
func TestDirectoryAcceptsDifferentLocalAuthorityEpochWithSameRaftIdentity(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	installed := RouteTarget{
		HashSlot: 1, SlotID: 2, LeaderNodeID: 1,
		LeaderTerm: 9, ConfigEpoch: 3,
		RouteRevision: 10, AuthorityEpoch: 1,
	}
	remote := installed
	remote.AuthorityEpoch = 4
	dir.BecomeAuthority(installed)

	if err := dir.TouchRoutes(remote, []Route{{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10}}); err != nil {
		t.Fatalf("TouchRoutes() error = %v, want same raft identity accepted", err)
	}
}
```

Add stale distributed identity tests:

```text
TestDirectoryRejectsOldLeaderTerm
  install target with LeaderTerm=9 ConfigEpoch=3
  call TouchRoutes with otherwise identical target but LeaderTerm=8
  assert errors.Is(err, ErrNotLeader)

TestDirectoryRejectsDifferentConfigEpoch
  install target with LeaderTerm=9 ConfigEpoch=3
  call TouchRoutes with otherwise identical target but ConfigEpoch=2
  assert errors.Is(err, ErrNotLeader)
```

- [ ] Modify `internalv2/runtime/presence/directory.go`.

Rename and change the fence helper:

```go
func sameAuthorityIdentity(left, right RouteTarget) bool {
	return left.HashSlot == right.HashSlot &&
		left.SlotID == right.SlotID &&
		left.LeaderNodeID == right.LeaderNodeID &&
		left.LeaderTerm == right.LeaderTerm &&
		left.ConfigEpoch == right.ConfigEpoch
}
```

Use it in `BecomeAuthority` and `validateTargetLocked`.

- [ ] Preserve revision-only update behavior.

```go
if sameAuthorityIdentity(current.target, target) {
	if target.RouteRevision >= current.target.RouteRevision {
		current.target = target
	}
	return
}
```

- [ ] Update existing presence directory tests that intentionally used `AuthorityEpoch` as the stale fence. Convert those cases to use `LeaderTerm` or `ConfigEpoch` changes instead. Tests about `RouteRevision` ordering should remain revision-based.

- [ ] Update `internalv2/runtime/presence/FLOW.md`.

Replace:

```text
(HashSlot, SlotID, LeaderNodeID, AuthorityEpoch)
```

with:

```text
(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)
```

Also state that `AuthorityEpoch` is local diagnostic metadata and is not a distributed authority fence.

- [ ] Run focused tests.

```bash
go test ./internalv2/runtime/presence -count=1
```

Expected: package passes.

## Task 4: Propagate The New Identity Through internalv2 Adapters

- [ ] Add fields to `internalv2/usecase/conversation/types.go`.

```go
type RouteTarget struct {
	HashSlot uint16
	SlotID uint32
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence kept for diagnostics only.
	AuthorityEpoch uint64
}
```

- [ ] Modify `internalv2/infra/cluster/presence.go`.

```go
func routeTargetFromClusterRoute(route clusterv2.Route) presence.RouteTarget {
	return presence.RouteTarget{
		HashSlot: route.HashSlot,
		SlotID: route.SlotID,
		LeaderNodeID: route.Leader,
		LeaderTerm: route.LeaderTerm,
		ConfigEpoch: route.ConfigEpoch,
		RouteRevision: route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}
```

- [ ] Modify `internalv2/infra/cluster/conversation_authority.go` the same way.

```go
func conversationRouteTargetFromClusterRoute(route clusterv2.Route) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot: route.HashSlot,
		SlotID: route.SlotID,
		LeaderNodeID: route.Leader,
		LeaderTerm: route.LeaderTerm,
		ConfigEpoch: route.ConfigEpoch,
		RouteRevision: route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}
```

- [ ] Modify `internalv2/access/node/presence_codec.go`.

Presence authority RPC must transport the same distributed route identity
across nodes. Extend the stable v2 payload for `presence.RouteTarget` with
`LeaderTerm` and `ConfigEpoch`, and update the corresponding decode path. This
codec does not provide mixed-version rolling-upgrade compatibility yet, so keep
the existing magic version unless the code already uses a version bump pattern.

- [ ] Update node RPC tests.

Files:

```text
internalv2/access/node/presence_codec_test.go
internalv2/access/node/presence_rpc_test.go
```

Any `testPresenceTarget` helper or encoded target assertion should use non-zero
`LeaderTerm` and `ConfigEpoch`, and the decoded/requested `presence.RouteTarget`
must preserve them.

- [ ] Update adapter tests.

Files:

```text
internalv2/infra/cluster/presence_test.go
internalv2/infra/cluster/conversation_authority_test.go
```

Any fake `clusterv2.Route` used for authority target assertions should include non-zero `LeaderTerm` and `ConfigEpoch`, and expected `RouteTarget` should match them.

- [ ] Run focused tests.

```bash
go test ./internalv2/infra/cluster ./internalv2/access/node -count=1
```

Expected: package passes.

## Task 5: Update App Authority Lifecycles And Pull-Based Reconcile

- [ ] Modify `internalv2/app/app.go`.

`currentPresenceAuthorities` must copy the new fields:

```go
RouteAuthority{
	HashSlot: route.HashSlot,
	SlotID: route.SlotID,
	LeaderNodeID: route.Leader,
	LeaderTerm: route.LeaderTerm,
	ConfigEpoch: route.ConfigEpoch,
	RouteRevision: route.Revision,
	AuthorityEpoch: route.AuthorityEpoch,
}
```

- [ ] Modify `internalv2/app/presence_touch.go`.

Copy new fields in `handleAuthority`:

```go
target := presence.RouteTarget{
	HashSlot: authority.HashSlot,
	SlotID: authority.SlotID,
	LeaderNodeID: authority.LeaderNodeID,
	LeaderTerm: authority.LeaderTerm,
	ConfigEpoch: authority.ConfigEpoch,
	RouteRevision: authority.RouteRevision,
	AuthorityEpoch: authority.AuthorityEpoch,
}
```

Change ordering from `AuthorityEpoch` to distributed identity first:

```go
func authorityTargetNewer(next, current presence.RouteTarget) bool {
	if next.RouteRevision != current.RouteRevision {
		return next.RouteRevision > current.RouteRevision
	}
	if next.ConfigEpoch != current.ConfigEpoch {
		return next.ConfigEpoch > current.ConfigEpoch
	}
	if next.LeaderTerm != current.LeaderTerm {
		return next.LeaderTerm > current.LeaderTerm
	}
	return next.AuthorityEpoch > current.AuthorityEpoch
}
```

- [ ] Add pull-based reconcile to `presenceTouchWorker`.

Keep the current watch path, but make the authoritative repair path pull current routes periodically. This protects against dropped `WatchRouteAuthorities` events and startup races.

Implementation shape:

```go
func (w *presenceTouchWorker) reconcileAuthorities() {
	for _, authority := range w.initialAuthorities() {
		w.handleAuthority(authority)
	}
}

func (w *presenceTouchWorker) Start(ctx context.Context) error {
	// keep the existing event setup, cancel guard, and goroutine accounting
	go w.watch(runCtx, events)
	go w.tick(runCtx)
	w.reconcileAuthorities()
	return nil
}

func (w *presenceTouchWorker) tick(ctx context.Context) {
	// keep the existing ticker setup
	case now := <-ticker.C:
		w.reconcileAuthorities()
		w.flushOnce(ctx, now)
	}
}
```

No public config is required for v0. The existing `FlushInterval` is enough for this internal repair loop.

- [ ] Modify `internalv2/app/conversation_route_lifecycle.go`.

Copy new fields in `conversationRouteTarget`, and update `conversationAuthorityRouteTargetNewer` with the same ordering rule as presence.

- [ ] Add a small periodic reconcile to `conversationAuthorityRouteLifecycle`.

Use its existing `Initial` function as the pull source. Add a goroutine only when `initial != nil`; default interval can be the existing `handoffTimeout` capped to a small floor, or a private constant such as `5 * time.Second`. Keep it package-local to avoid new config churn.

Implementation shape:

```go
const defaultAuthorityReconcileInterval = 5 * time.Second

func (l *conversationAuthorityRouteLifecycle) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultAuthorityReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.applyRouteAuthorities(ctx, l.initialAuthorities())
		}
	}
}
```

- [ ] Update `internalv2/app/app_test.go`.

Add/update tests:

```text
TestCurrentPresenceAuthoritiesIncludesLeaderTermAndConfigEpoch
  fakeWriteReadyCluster returns route with LeaderTerm=9 ConfigEpoch=3
  call app.currentPresenceAuthorities()
  assert returned RouteAuthority carries LeaderTerm=9 ConfigEpoch=3

TestPresenceTouchWorkerAcceptsSameRaftIdentityWithDifferentAuthorityEpoch
  handle local authority with LeaderTerm=9 ConfigEpoch=3 AuthorityEpoch=1
  handle same local authority with LeaderTerm=9 ConfigEpoch=3 AuthorityEpoch=4
  assert the second update is accepted and does not clear authority

TestPresenceTouchWorkerPeriodicReconcileRepairsDroppedAuthorityEvent
  Initial first returns remote/no-local authority, then local authority
  start worker without sending watch event
  wait for tick reconcile
  assert Directory.BecomeAuthority receives the local target

TestConversationAuthorityRouteLifecycleAcceptsSameRaftIdentityWithDifferentAuthorityEpoch
  apply local target with LeaderTerm=9 ConfigEpoch=3 AuthorityEpoch=1
  apply same local target with AuthorityEpoch=4
  assert lifecycle accepts the newer diagnostic epoch without treating it as a distributed leader change
```

Existing tests named around epoch fencing should be renamed or rewritten because `AuthorityEpoch` no longer owns the hard fence.

- [ ] Update `internalv2/app/FLOW.md`.

Update Presence Touch Worker and Conversation Authority Handoff sections:

```text
ignore stale events by route revision, config epoch, and Slot leader term
```

Also mention that the worker/lifecycle periodically pulls current route authorities so dropped watch events are repaired.

- [ ] Run focused tests.

```bash
go test ./internalv2/app -run 'Presence|ConversationAuthorityRouteLifecycle|CurrentPresenceAuthorities|StartSeedsPresenceAuthority' -count=1
```

Expected: focused app tests pass.

## Task 6: Align Shared Authority Target Contract

- [ ] Modify `internalv2/contracts/authority/target.go`.

This target is reused by channelappend recipient-effect grouping. It should carry the same distributed identity even if its v0 `Validate` still only checks for a usable leader.

```go
type Target struct {
	HashSlot uint16
	SlotID uint32
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence kept for diagnostics only.
	AuthorityEpoch uint64
}
```

- [ ] Modify `internalv2/app/channel_append.go`.

Copy `LeaderTerm` and `ConfigEpoch` in `channelAppendRecipientTargetFromRoute`.

```go
return channelappend.RecipientAuthorityTarget{
	HashSlot: route.HashSlot,
	SlotID: route.SlotID,
	LeaderNodeID: route.Leader,
	LeaderTerm: route.LeaderTerm,
	ConfigEpoch: route.ConfigEpoch,
	RouteRevision: route.Revision,
	AuthorityEpoch: route.AuthorityEpoch,
}, nil
```

- [ ] Update tests.

Files:

```text
internalv2/contracts/authority/target_test.go
internalv2/app/channel_append_test.go
internalv2/runtime/channelappend/recipient_test.go
internalv2/runtime/channelappend/commit_test.go
```

Most channelappend tests can keep zero values when they do not assert identity. Tests that assert exact target equality should use non-zero `LeaderTerm` and `ConfigEpoch`.

- [ ] Run focused tests.

```bash
go test ./internalv2/contracts/authority ./internalv2/runtime/channelappend ./internalv2/app -run 'Recipient|ChannelAppend|authority.Target' -count=1
```

Expected: focused tests pass.

## Task 7: Update Docs And Knowledge

- [ ] Update `pkg/clusterv2/FLOW.md`.

Replace the old route authority tuple:

```text
(HashSlot, SlotID, LeaderNodeID, RouteRevision, AuthorityEpoch)
```

with:

```text
(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch, RouteRevision, AuthorityEpoch)
```

State:

```text
LeaderTerm and ConfigEpoch are the distributed authority fence. AuthorityEpoch is a node-local observation sequence retained for diagnostics and compatibility.
```

- [ ] Update `internalv2/infra/cluster/FLOW.md`.

In Conversation Authority and Presence Authority sections, say exact `RouteTarget` includes Raft leader term and Slot config epoch.

- [ ] Update `internalv2/runtime/presence/FLOW.md` and `internalv2/app/FLOW.md` as listed in previous tasks.

- [ ] Add one concise note to `docs/development/PROJECT_KNOWLEDGE.md`.

Suggested text:

```text
- V2 UID authority targets must use distributed Slot identity `(hash slot, slot id, leader node, leader term, config epoch)` as the hard fence. Node-local `AuthorityEpoch` is diagnostic/order metadata only and must not be used as a cross-node hard fence.
```

## Task 8: Verification

- [ ] Run the focused package suite.

```bash
go test ./pkg/clusterv2/slots ./pkg/clusterv2/routing ./pkg/clusterv2 ./internalv2/contracts/authority ./internalv2/runtime/presence ./internalv2/infra/cluster ./internalv2/runtime/channelappend ./internalv2/app -count=1
```

Expected: all packages pass.

- [ ] Run the e2ev2 test suite most likely to catch startup/activation regressions.

```bash
go test ./test/e2ev2/... -count=1
```

Expected: all e2ev2 packages pass. If a package is slow or flaky, capture the exact failing package/test and log path before making any follow-up changes.

- [ ] Run a short smoke before the 1h command.

```bash
./scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh --duration 5m --users 1000 --groups 1000 --members 10 --rate 3/s
```

Expected:

```text
messages_sent > 0
clients_connected grows instead of failing at first CONNECT
no repeated activation_not_leader / presence: not leader loop
script runs until requested duration or manual stop
```

- [ ] Run the user-reported smoke command.

```bash
./scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh --duration 1h --users 1000 --groups 1000 --members 10 --rate 3/s
```

Expected:

```text
the script does not stop after a few seconds
CONNECT succeeds across routed nodes
no control raft connection-refused error after startup settles
summary artifacts show sustained traffic
```

- [ ] Inspect logs if smoke fails.

Search for these exact strings first:

```bash
rg -n "activation_not_leader|activation_context_deadline|presence: not leader|authorityEpoch|leaderTerm|configEpoch|control raft send failed|connect: connection refused" /tmp/wukongimv2-three-nodes-*/logs
```

Expected after fix: if `presence: not leader` appears during startup, it should be transient and followed by successful activation for the same UID/hash slot after route reconcile.

## Rollback Notes

If implementation introduces broad failures:

- Keep Task 1 and Task 2 if they pass. Carrying `LeaderTerm` and `ConfigEpoch` is independently useful and low-risk.
- Temporarily disable the pull-based reconcile loops by setting their intervals to zero only in tests if they cause lifecycle hangs. Do not revert the identity fence back to distributed `AuthorityEpoch`.
- Do not remove `AuthorityEpoch` from public structs in this change. It is intentionally retained to keep existing diagnostics, logs, and tests migratable in small steps.

## Execution Order

Implement in this order:

1. Task 1 and Task 2 together, then run clusterv2 tests.
2. Task 3, then run presence runtime tests.
3. Task 4 and Task 5, then run infra/app focused tests.
4. Task 6, then run channelappend/app focused tests.
5. Task 7 documentation.
6. Task 8 verification.

Do not start the 1h smoke until the focused Go test suite and the 5m smoke pass.
