# Channel-Keyed ISR Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the numeric `GroupID uint64` identity in the ISR data-plane stack with a strong string `GroupKey`, so `channel` is the only business replication unit and the replication runtime stays business-agnostic.

**Architecture:** The refactor is a direct cutover in dependency order: first lock `GroupKey` into `pkg/replication/isr`, then propagate it through `pkg/replication/isrnode`, then remove `ChannelMeta.GroupID` in `pkg/storage/channellog` and derive runtime keys locally from `ChannelKey`. The only business-to-runtime translation lives in `pkg/storage/channellog/groupkey.go`; no compatibility layer, numeric fallback, or controller-assigned runtime group id remains after this work.

**Tech Stack:** Go 1.23, standard library `encoding/base64/context/errors/testing/time`, existing `pkg/replication/isr`, `pkg/replication/isrnode`, `pkg/storage/channellog`, `internal/usecase/message`

---

## Scope Check

This plan is intentionally narrower than `docs/superpowers/plans/2026-04-03-channelcluster-implementation.md`.

It only covers the identity-model cutover required by `docs/superpowers/specs/2026-04-03-channel-keyed-isr-design.md`:

- `pkg/replication/isr`
- `pkg/replication/isrnode`
- `pkg/storage/channellog`
- directly dependent tests/docs

It does not add new channel features, protocol changes, or controller work.

## File Structure

### Production files

- Modify: `pkg/replication/isr/types.go`
  Responsibility: define `type GroupKey string` and replace public `GroupID` fields with `GroupKey`.
- Modify: `pkg/replication/isr/meta.go`
  Responsibility: key validation, stale-meta checks, and status snapshots keyed by `GroupKey`.
- Modify: `pkg/replication/isr/replica.go`
  Responsibility: replica construction and shared key-aware state.
- Modify: `pkg/replication/isr/fetch.go`
  Responsibility: leader-side fetch validation using `GroupKey`.
- Modify: `pkg/replication/isr/replication.go`
  Responsibility: follower-side `ApplyFetch` validation using `GroupKey`.
- Modify: `pkg/replication/isr/append.go`
  Responsibility: append path preserving `GroupKey` identity in commit flow.
- Modify: `pkg/replication/isr/doc.go`
  Responsibility: package docs updated to the `GroupKey` contract.

- Modify: `pkg/replication/isrnode/types.go`
  Responsibility: public runtime, envelope, generation store, and group handle contracts keyed by `isr.GroupKey`.
- Modify: `pkg/replication/isrnode/runtime.go`
  Responsibility: runtime maps, lifecycle wiring, and scheduler dispatch keyed by `GroupKey`.
- Modify: `pkg/replication/isrnode/group.go`
  Responsibility: per-group handle identity and lease-checked append wrapper.
- Modify: `pkg/replication/isrnode/registry.go`
  Responsibility: active-group lookup and tombstones keyed by `GroupKey`.
- Modify: `pkg/replication/isrnode/generation.go`
  Responsibility: generation allocation/loading by `GroupKey`.
- Modify: `pkg/replication/isrnode/scheduler.go`
  Responsibility: ordered queues storing `GroupKey`.
- Modify: `pkg/replication/isrnode/snapshot.go`
  Responsibility: snapshot wait queues and dispatch keyed by `GroupKey`.
- Modify: `pkg/replication/isrnode/limits.go`
  Responsibility: queued envelopes continue carrying the new key type.
- Modify: `pkg/replication/isrnode/transport.go`
  Responsibility: inbound envelope demux using `(GroupKey, Generation)`.
- Modify: `pkg/replication/isrnode/doc.go`
  Responsibility: package docs updated to the new identity model.

- Create: `pkg/storage/channellog/groupkey.go`
  Responsibility: the only `ChannelKey -> isr.GroupKey` normalization function.
- Modify: `pkg/storage/channellog/types.go`
  Responsibility: remove `ChannelMeta.GroupID`, update runtime/log interfaces to use `GroupKey`.
- Modify: `pkg/storage/channellog/meta.go`
  Responsibility: metadata compare rules without `GroupID`.
- Modify: `pkg/storage/channellog/send.go`
  Responsibility: runtime lookup and idempotency log reads via derived `GroupKey`.
- Modify: `pkg/storage/channellog/fetch.go`
  Responsibility: runtime lookup and log reads via derived `GroupKey`.
- Modify: `pkg/storage/channellog/apply.go`
  Responsibility: checkpoint replay bridge via `GroupKey`.
- Modify: `pkg/storage/channellog/doc.go`
  Responsibility: package docs updated to reflect the new identity boundary.

- Modify: `docs/superpowers/specs/2026-04-02-channelcluster-design.md`
  Responsibility: remove stale `channel -> GroupID` rules and point to local `GroupKey` derivation.
- Modify: `docs/superpowers/plans/2026-04-03-channelcluster-implementation.md`
  Responsibility: remove or annotate stale `GroupID` assumptions so later execution does not regress the new model.

### Test files

- Modify: `pkg/replication/isr/api_test.go`
- Modify: `pkg/replication/isr/meta_test.go`
- Modify: `pkg/replication/isr/fetch_test.go`
- Modify: `pkg/replication/isr/replication_test.go`
- Modify: `pkg/replication/isr/recovery_test.go`
- Modify: `pkg/replication/isr/append_test.go`
- Modify: `pkg/replication/isr/snapshot_test.go`
- Modify: `pkg/replication/isr/testenv_test.go`
  Responsibility: convert all helper metadata and assertions from numeric ids to `GroupKey`.

- Modify: `pkg/replication/isrnode/api_test.go`
- Modify: `pkg/replication/isrnode/runtime_test.go`
- Modify: `pkg/replication/isrnode/registry_test.go`
- Modify: `pkg/replication/isrnode/scheduler_test.go`
- Modify: `pkg/replication/isrnode/session_test.go`
- Modify: `pkg/replication/isrnode/limits_test.go`
- Modify: `pkg/replication/isrnode/snapshot_test.go`
- Modify: `pkg/replication/isrnode/testenv_test.go`
- Modify: `pkg/replication/isrnode/pressure_testutil_test.go`
- Modify: `pkg/replication/isrnode/stress_test.go`
- Modify: `pkg/replication/isrnode/benchmark_test.go`
  Responsibility: move all fake stores, envelopes, and helper keys to `GroupKey`.

- Create: `pkg/storage/channellog/groupkey_test.go`
- Modify: `pkg/storage/channellog/api_test.go`
- Modify: `pkg/storage/channellog/meta_test.go`
- Modify: `pkg/storage/channellog/send_test.go`
- Modify: `pkg/storage/channellog/fetch_test.go`
- Modify: `pkg/storage/channellog/apply_test.go`
- Modify: `pkg/storage/channellog/deleting_test.go`
- Modify: `pkg/storage/channellog/testenv_test.go`
  Responsibility: validate deterministic key derivation and remove fake runtime/log dependence on `GroupID`.

- Modify: `internal/usecase/message/send_test.go`
  Responsibility: update `ChannelMeta` fixtures after `GroupID` removal.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task.
- Keep the field name `GroupKey` on public structs instead of renaming fields to plain `Key`; this minimizes churn while making the type change explicit.
- `GroupKey("")` is invalid everywhere the old code treated `GroupID == 0` as invalid.
- `pkg/storage/channellog/groupkey.go` must be the only translation layer from business channel identity to runtime identity.
- Use `base64.RawURLEncoding.EncodeToString([]byte(channelID))` for the `channel/<channelType>/<encodedChannelID>` format.
- `pkg/replication/isrnode` does not own a concrete envelope wire codec today. This plan only changes the abstract `Envelope` contract and all package-local demux logic.
- Do not add a compatibility alias, dual-read path, or controller fallback lookup.

## Task 1: Lock `GroupKey` into the public `pkg/replication/isr` contract

**Files:**
- Modify: `pkg/replication/isr/types.go`
- Modify: `pkg/replication/isr/meta.go`
- Modify: `pkg/replication/isr/doc.go`
- Modify: `pkg/replication/isr/api_test.go`
- Modify: `pkg/replication/isr/meta_test.go`
- Modify: `pkg/replication/isr/testenv_test.go`

- [ ] **Step 1: Write the failing public-contract tests**

```go
func TestGroupMetaRejectsEmptyGroupKey(t *testing.T) {
	r := newTestReplica(t)
	err := r.ApplyMeta(isr.GroupMeta{
		GroupKey: "",
		Epoch:    1,
		Leader:   1,
		Replicas: []isr.NodeID{1, 2},
		ISR:      []isr.NodeID{1, 2},
		MinISR:   1,
	})
	if !errors.Is(err, isr.ErrInvalidMeta) {
		t.Fatalf("expected ErrInvalidMeta, got %v", err)
	}
}

func TestReplicaSurfaceUsesGroupKey(t *testing.T) {
	var req isr.FetchRequest
	req.GroupKey = isr.GroupKey("channel/1/YzE")

	var apply isr.ApplyFetchRequest
	apply.GroupKey = req.GroupKey
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/replication/isr -run 'TestGroupMetaRejectsEmptyGroupKey|TestReplicaSurfaceUsesGroupKey' -v`

Expected: FAIL because the public types still expose numeric `GroupID`.

- [ ] **Step 3: Replace `GroupID` with `GroupKey` in the public types**

```go
type GroupKey string

type GroupMeta struct {
	GroupKey   GroupKey
	Epoch      uint64
	Leader     NodeID
	Replicas   []NodeID
	ISR        []NodeID
	MinISR     int
	LeaseUntil time.Time
}
```

Implementation details:
- add `type GroupKey string` to `pkg/replication/isr/types.go`
- replace `GroupID` fields on `GroupMeta`, `ReplicaState`, `Snapshot`, `FetchRequest`, and `ApplyFetchRequest`
- replace `GroupID == 0` validation with `GroupKey == ""`
- update reusable test helpers in `pkg/replication/isr/testenv_test.go`
- update package docs in `pkg/replication/isr/doc.go`

- [ ] **Step 4: Re-run the targeted tests**

Run: `go test ./pkg/replication/isr -run 'TestGroupMetaRejectsEmptyGroupKey|TestReplicaSurfaceUsesGroupKey' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/replication/isr/types.go pkg/replication/isr/meta.go pkg/replication/isr/doc.go pkg/replication/isr/api_test.go pkg/replication/isr/meta_test.go pkg/replication/isr/testenv_test.go
git commit -m "refactor: add group key to isr api"
```

## Task 2: Propagate `GroupKey` through ISR behavior and invariants

**Files:**
- Modify: `pkg/replication/isr/replica.go`
- Modify: `pkg/replication/isr/fetch.go`
- Modify: `pkg/replication/isr/replication.go`
- Modify: `pkg/replication/isr/append.go`
- Modify: `pkg/replication/isr/recovery.go`
- Modify: `pkg/replication/isr/fetch_test.go`
- Modify: `pkg/replication/isr/replication_test.go`
- Modify: `pkg/replication/isr/recovery_test.go`
- Modify: `pkg/replication/isr/append_test.go`
- Modify: `pkg/replication/isr/snapshot_test.go`
- Modify: `pkg/replication/isr/benchmark_test.go`

- [ ] **Step 1: Write the failing behavioral tests**

```go
func TestFetchRejectsMismatchedGroupKey(t *testing.T) {
	r := newLeaderReplica(t, "channel/1/YzE")
	_, err := r.Fetch(context.Background(), isr.FetchRequest{
		GroupKey:     isr.GroupKey("channel/1/YzI"),
		Epoch:        1,
		ReplicaID:    2,
		FetchOffset:  0,
		OffsetEpoch:  0,
		MaxBytes:     1024,
	})
	if !errors.Is(err, isr.ErrStaleMeta) {
		t.Fatalf("expected ErrStaleMeta, got %v", err)
	}
}

func TestApplyFetchRejectsMismatchedGroupKey(t *testing.T) {
	r := newFollowerReplica(t, "channel/1/YzE")
	err := r.ApplyFetch(context.Background(), isr.ApplyFetchRequest{
		GroupKey: isr.GroupKey("channel/1/YzI"),
		Epoch:    1,
		Leader:   1,
	})
	if !errors.Is(err, isr.ErrStaleMeta) {
		t.Fatalf("expected ErrStaleMeta, got %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/replication/isr -run 'TestFetchRejectsMismatchedGroupKey|TestApplyFetchRejectsMismatchedGroupKey' -v`

Expected: FAIL because the fetch and apply paths still compare numeric ids.

- [ ] **Step 3: Convert the internal behavior to `GroupKey`**

Implementation details:
- update every stale-meta comparison in `fetch.go`, `replication.go`, `recovery.go`, and `append.go`
- preserve the existing behavior, changing only the identifier type
- update snapshot and recovery helpers so persisted and returned state keeps the same `GroupKey`
- update benchmark/test helpers to construct reusable string keys instead of ints

- [ ] **Step 4: Run the full `pkg/replication/isr` test suite**

Run: `go test ./pkg/replication/isr -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/replication/isr/replica.go pkg/replication/isr/fetch.go pkg/replication/isr/replication.go pkg/replication/isr/append.go pkg/replication/isr/recovery.go pkg/replication/isr/fetch_test.go pkg/replication/isr/replication_test.go pkg/replication/isr/recovery_test.go pkg/replication/isr/append_test.go pkg/replication/isr/snapshot_test.go pkg/replication/isr/benchmark_test.go
git commit -m "refactor: propagate group key through isr"
```

## Task 3: Convert `pkg/replication/isrnode` lifecycle and scheduler state to `GroupKey`

**Files:**
- Modify: `pkg/replication/isrnode/types.go`
- Modify: `pkg/replication/isrnode/runtime.go`
- Modify: `pkg/replication/isrnode/group.go`
- Modify: `pkg/replication/isrnode/registry.go`
- Modify: `pkg/replication/isrnode/generation.go`
- Modify: `pkg/replication/isrnode/scheduler.go`
- Modify: `pkg/replication/isrnode/snapshot.go`
- Modify: `pkg/replication/isrnode/doc.go`
- Modify: `pkg/replication/isrnode/api_test.go`
- Modify: `pkg/replication/isrnode/runtime_test.go`
- Modify: `pkg/replication/isrnode/registry_test.go`
- Modify: `pkg/replication/isrnode/scheduler_test.go`
- Modify: `pkg/replication/isrnode/snapshot_test.go`
- Modify: `pkg/replication/isrnode/testenv_test.go`

- [ ] **Step 1: Write the failing runtime-contract tests**

```go
func TestRuntimeSurfaceUsesGroupKey(t *testing.T) {
	var rt isrnode.Runtime
	key := isr.GroupKey("channel/1/YzE")
	meta := isr.GroupMeta{
		GroupKey: key,
		Epoch:    1,
		Leader:   1,
		Replicas: []isr.NodeID{1, 2},
		ISR:      []isr.NodeID{1, 2},
		MinISR:   1,
	}

	_ = rt.EnsureGroup(meta)
	_ = rt.RemoveGroup(key)
	_, _ = rt.Group(key)
}

func TestEnsureGroupStoresGenerationByGroupKey(t *testing.T) {
	env := newTestEnv(t)
	meta := testMeta(isr.GroupKey("channel/1/YzE"), 1, 1, []isr.NodeID{1, 2})
	if err := env.runtime.EnsureGroup(meta); err != nil {
		t.Fatalf("EnsureGroup() error = %v", err)
	}
	if got := env.generations.stored[meta.GroupKey]; got != 1 {
		t.Fatalf("expected generation 1, got %d", got)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/replication/isrnode -run 'TestRuntimeSurfaceUsesGroupKey|TestEnsureGroupStoresGenerationByGroupKey' -v`

Expected: FAIL because the runtime contracts and fake generation store still use numeric ids.

- [ ] **Step 3: Convert runtime state and helpers to `GroupKey`**

```go
type Runtime interface {
	EnsureGroup(meta isr.GroupMeta) error
	RemoveGroup(groupKey isr.GroupKey) error
	ApplyMeta(meta isr.GroupMeta) error
	Group(groupKey isr.GroupKey) (GroupHandle, bool)
}

type GenerationStore interface {
	Load(groupKey isr.GroupKey) (uint64, error)
	Store(groupKey isr.GroupKey, generation uint64) error
}
```

Implementation details:
- change `group.id`, runtime maps, tombstones, scheduler queues, and snapshot wait queues to `isr.GroupKey`
- keep public field names as `GroupKey`
- update all test helpers and fake stores in `pkg/replication/isrnode/testenv_test.go`
- update package docs in `pkg/replication/isrnode/doc.go`

- [ ] **Step 4: Re-run the targeted tests**

Run: `go test ./pkg/replication/isrnode -run 'TestRuntimeSurfaceUsesGroupKey|TestEnsureGroupStoresGenerationByGroupKey' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/replication/isrnode/types.go pkg/replication/isrnode/runtime.go pkg/replication/isrnode/group.go pkg/replication/isrnode/registry.go pkg/replication/isrnode/generation.go pkg/replication/isrnode/scheduler.go pkg/replication/isrnode/snapshot.go pkg/replication/isrnode/doc.go pkg/replication/isrnode/api_test.go pkg/replication/isrnode/runtime_test.go pkg/replication/isrnode/registry_test.go pkg/replication/isrnode/scheduler_test.go pkg/replication/isrnode/snapshot_test.go pkg/replication/isrnode/testenv_test.go
git commit -m "refactor: key isrnode runtime by group key"
```

## Task 4: Propagate `GroupKey` through `isrnode` envelopes, queues, and full-package tests

**Files:**
- Modify: `pkg/replication/isrnode/transport.go`
- Modify: `pkg/replication/isrnode/limits.go`
- Modify: `pkg/replication/isrnode/session_test.go`
- Modify: `pkg/replication/isrnode/limits_test.go`
- Modify: `pkg/replication/isrnode/pressure_testutil_test.go`
- Modify: `pkg/replication/isrnode/stress_test.go`
- Modify: `pkg/replication/isrnode/benchmark_test.go`

- [ ] **Step 1: Write the failing transport/queue tests**

```go
func TestHandleEnvelopeDropsLateGenerationForGroupKey(t *testing.T) {
	env := newTestEnv(t)
	meta := testMeta(isr.GroupKey("channel/1/YzE"), 1, 1, []isr.NodeID{1, 2})
	mustEnsure(t, env.runtime, meta)
	mustRemove(t, env.runtime, meta.GroupKey)

	env.transport.deliver(isrnode.Envelope{
		GroupKey:   meta.GroupKey,
		Generation: 1,
		Epoch:      1,
		Kind:       isrnode.MessageKindFetchResponse,
	})
}

func TestPeerQueueRetainsEnvelopeGroupKeyOrder(t *testing.T) {
	state := newPeerRequestState()
	peer := isr.NodeID(2)
	state.enqueue(isrnode.Envelope{Peer: peer, GroupKey: "channel/1/YzE"})
	state.enqueue(isrnode.Envelope{Peer: peer, GroupKey: "channel/1/YzI"})
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/replication/isrnode -run 'TestHandleEnvelopeDropsLateGenerationForGroupKey|TestPeerQueueRetainsEnvelopeGroupKeyOrder' -v`

Expected: FAIL because `Envelope` and related tests still depend on `GroupID`.

- [ ] **Step 3: Convert envelope and queue paths**

Implementation details:
- replace `Envelope.GroupID` with `Envelope.GroupKey isr.GroupKey`
- update inbound demux in `transport.go` to use `r.groups[env.GroupKey]` and tombstones keyed by `GroupKey`
- update queue assertions in `limits_test.go` and pressure helpers to use string keys
- update remaining package tests and benchmarks so `go test ./pkg/replication/isrnode` compiles cleanly

- [ ] **Step 4: Run the full `pkg/replication/isrnode` test suite**

Run: `go test ./pkg/replication/isrnode -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/replication/isrnode/transport.go pkg/replication/isrnode/limits.go pkg/replication/isrnode/session_test.go pkg/replication/isrnode/limits_test.go pkg/replication/isrnode/pressure_testutil_test.go pkg/replication/isrnode/stress_test.go pkg/replication/isrnode/benchmark_test.go
git commit -m "refactor: use group key in isrnode envelopes"
```

## Task 5: Remove `ChannelMeta.GroupID` and derive runtime keys inside `pkg/storage/channellog`

**Files:**
- Create: `pkg/storage/channellog/groupkey.go`
- Modify: `pkg/storage/channellog/types.go`
- Modify: `pkg/storage/channellog/meta.go`
- Modify: `pkg/storage/channellog/send.go`
- Modify: `pkg/storage/channellog/fetch.go`
- Modify: `pkg/storage/channellog/apply.go`
- Modify: `pkg/storage/channellog/doc.go`
- Create: `pkg/storage/channellog/groupkey_test.go`
- Modify: `pkg/storage/channellog/api_test.go`
- Modify: `pkg/storage/channellog/meta_test.go`
- Modify: `pkg/storage/channellog/send_test.go`
- Modify: `pkg/storage/channellog/fetch_test.go`
- Modify: `pkg/storage/channellog/apply_test.go`
- Modify: `pkg/storage/channellog/deleting_test.go`
- Modify: `pkg/storage/channellog/testenv_test.go`

- [ ] **Step 1: Write the failing key-derivation and metadata tests**

```go
func TestChannelGroupKeyIsDeterministicAndEscaped(t *testing.T) {
	key := channellog.ChannelKey{ChannelID: "a/b:c", ChannelType: 3}
	got := channelGroupKey(key)
	if got != isr.GroupKey("channel/3/YS9iOmM") {
		t.Fatalf("unexpected key %q", got)
	}
}

func TestApplyMetaTreatsIdenticalVersionWithoutGroupIDAsIdempotent(t *testing.T) {
	cluster := newTestCluster(t)
	meta := testMeta("c1", 1, 7, 9)
	if err := cluster.ApplyMeta(meta); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	if err := cluster.ApplyMeta(meta); err != nil {
		t.Fatalf("expected idempotent replay, got %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/storage/channellog -run 'TestChannelGroupKeyIsDeterministicAndEscaped|TestApplyMetaTreatsIdenticalVersionWithoutGroupIDAsIdempotent' -v`

Expected: FAIL because `groupkey.go` does not exist and `ChannelMeta` still includes `GroupID`.

- [ ] **Step 3: Implement `ChannelKey -> GroupKey` as the only boundary translation**

```go
func channelGroupKey(key ChannelKey) isr.GroupKey {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID))
	return isr.GroupKey(fmt.Sprintf("channel/%d/%s", key.ChannelType, encoded))
}
```

Implementation details:
- remove `GroupID` from `ChannelMeta`
- change `MessageLog.Read` and `Runtime.Group` interfaces to accept `isr.GroupKey`
- in `send.go`, `fetch.go`, and `apply.go`, derive the key from `ChannelKey` instead of reading metadata for a numeric id
- remove `GroupID` from conflict checks in `meta.go`
- update fake runtime and fake log in `pkg/storage/channellog/testenv_test.go`
- update package docs in `pkg/storage/channellog/doc.go`

- [ ] **Step 4: Run the full `pkg/storage/channellog` test suite**

Run: `go test ./pkg/storage/channellog -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/channellog/groupkey.go pkg/storage/channellog/types.go pkg/storage/channellog/meta.go pkg/storage/channellog/send.go pkg/storage/channellog/fetch.go pkg/storage/channellog/apply.go pkg/storage/channellog/doc.go pkg/storage/channellog/groupkey_test.go pkg/storage/channellog/api_test.go pkg/storage/channellog/meta_test.go pkg/storage/channellog/send_test.go pkg/storage/channellog/fetch_test.go pkg/storage/channellog/apply_test.go pkg/storage/channellog/deleting_test.go pkg/storage/channellog/testenv_test.go
git commit -m "refactor: derive channel runtime group keys locally"
```

## Task 6: Update dependent tests and stale design docs

**Files:**
- Modify: `internal/usecase/message/send_test.go`
- Modify: `docs/superpowers/specs/2026-04-02-channelcluster-design.md`
- Modify: `docs/superpowers/plans/2026-04-03-channelcluster-implementation.md`

- [ ] **Step 1: Write the failing dependent test**

```go
func TestSendDurablePersonUsesChannelMetaWithoutGroupID(t *testing.T) {
	app := newTestApp(t, withClusterMeta(channellog.ChannelMeta{
		ChannelID:    "u2",
		ChannelType:  wkframe.ChannelTypePerson,
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Replicas:     []isr.NodeID{1, 2},
		ISR:          []isr.NodeID{1, 2},
		Leader:       1,
		MinISR:       1,
		Status:       channellog.ChannelStatusActive,
	}))
	_, _ = app.Send(validSendCommand())
}
```

- [ ] **Step 2: Run the targeted test and confirm it fails**

Run: `go test ./internal/usecase/message -run 'TestSendDurablePersonUsesChannelMetaWithoutGroupID' -v`

Expected: FAIL because test fixtures still construct `ChannelMeta` with `GroupID`.

- [ ] **Step 3: Update the dependent tests and docs**

Implementation details:
- update `internal/usecase/message/send_test.go` fixtures and fake metadata refresh responses
- rewrite the old `channelcluster` spec sections that still require controller-assigned `GroupID`
- annotate or rewrite the old `channelcluster` implementation plan sections that still assume `meta.GroupID` lookups

- [ ] **Step 4: Run the package-level regression checks**

Run: `go test ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/storage/channellog ./internal/usecase/message -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/send_test.go docs/superpowers/specs/2026-04-02-channelcluster-design.md docs/superpowers/plans/2026-04-03-channelcluster-implementation.md
git commit -m "test: align channel metadata with group key runtime"
```

## Final Verification

- [ ] Run: `go test ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/storage/channellog ./internal/usecase/message -count=1`
- [ ] Run: `go test ./...`
- [ ] Inspect `git diff --stat` and confirm no numeric `GroupID` remains in the ISR data-plane stack except in unrelated raft/control-plane packages
- [ ] If `go test ./...` exposes unrelated failures outside this scope, capture them separately before final handoff
