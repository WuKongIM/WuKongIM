# ChannelCluster Implementation Plan

> Note (2026-04-03): This historical plan predates the channel-keyed ISR cutover.
> For current identity rules, use `docs/superpowers/specs/2026-04-03-channel-keyed-isr-design.md`
> and `docs/superpowers/plans/2026-04-03-channel-keyed-isr-implementation.md`.
> Current code uses `isr.GroupKey` derived locally from `ChannelKey`; do not introduce numeric `GroupID`.

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/channelcluster` as the channel-oriented data plane on top of `pkg/isr` / `pkg/multiisr`, then upgrade the access and wire layers to carry `uint64 messageSeq` safely.

**Architecture:** The work stays split into two dependent workstreams inside one plan. Workstream A builds the new library with its own metadata cache, send/fetch/status APIs, message codec, and committed-state apply bridge so idempotency survives recovery and leader changes without changing the current `pkg/isr` public surface. Workstream B upgrades `wkpacket` / `wkproto` / `jsonrpc` and the access/usecase layer to use the new `uint64` API, negotiate legacy-vs-u64 protocol behavior, and map the new semantic errors without dragging control-plane logic into request paths.

**Tech Stack:** Go 1.23, standard library `context/errors/hash/fnv/math/sync/testing/time`, existing `pkg/isr`, `pkg/multiisr`, `pkg/wkpacket`, `pkg/wkproto`, `pkg/jsonrpc`

---

## Scope Check

This spec spans two workstreams, but they are not independent enough to justify two separate plan files:

1. Workstream A (`pkg/channelcluster`) must finish first because it defines the stable internal `uint64 messageSeq` API.
2. Workstream B depends on that API to perform the wire upgrade and access-layer mapping.

Two explicit items are intentionally kept out of this plan even though they are relevant to rollout:

- `internal/usecase/channelmeta` does not exist yet in this repo, so this plan only introduces refresh/application seams that can consume metadata once that control-plane usecase lands.
- `internal/app/build.go` still wires the old `pkg/controller/wkcluster` / `pkg/multiraft` stack, so this plan does not try to replace the whole composition root with `pkg/multiisr` in one shot.

## File Structure

### Workstream A production files

- Create: `pkg/channelcluster/doc.go`
  Responsibility: package overview and contract notes for channel-oriented replication semantics.
- Create: `pkg/channelcluster/types.go`
  Responsibility: public IDs, enums, metadata, features, requests, results, config, runtime/store interfaces, and message record models.
- Create: `pkg/channelcluster/errors.go`
  Responsibility: exported sentinel errors and small helpers for semantic error classification.
- Create: `pkg/channelcluster/cluster.go`
  Responsibility: concrete cluster constructor, dependency validation, cache/store/runtime ownership, and top-level method wiring.
- Create: `pkg/channelcluster/meta.go`
  Responsibility: `ApplyMeta` compare-and-swap rules, cache lookup state, request-token compatibility checks, and metadata snapshot helpers.
- Create: `pkg/channelcluster/codec.go`
  Responsibility: log-record encoding/decoding, payload hashing for idempotency-conflict detection, and offset/message translation helpers.
- Create: `pkg/channelcluster/apply.go`
  Responsibility: committed-range replay, internal state-commit helper, checkpoint bridge, and snapshot restore bridge.
- Create: `pkg/channelcluster/send.go`
  Responsibility: send path validation, idempotency lookup/conflict logic, message ID allocation, append execution, and error translation.
- Create: `pkg/channelcluster/fetch.go`
  Responsibility: fetch and status paths, committed-only reads, range translation, and delete-state fences.

### Workstream A test files

- Create: `pkg/channelcluster/testenv_test.go`
  Responsibility: fake runtime, fake group handle, fake message log, fake state store, fake message ID generator, and deterministic clock helpers.
- Create: `pkg/channelcluster/api_test.go`
  Responsibility: constructor validation and public-surface compile tests.
- Create: `pkg/channelcluster/meta_test.go`
  Responsibility: metadata cache replacement, stale/conflict rejection, and missing-cache semantics.
- Create: `pkg/channelcluster/apply_test.go`
  Responsibility: committed-range replay, checkpoint ordering, snapshot restore, and recovery behavior.
- Create: `pkg/channelcluster/send_test.go`
  Responsibility: happy-path send, idempotent replay, conflict rejection, not-leader/stale-meta handling, and legacy-u32 exhaustion.
- Create: `pkg/channelcluster/fetch_test.go`
  Responsibility: `FromSeq` translation, committed-only fetch, invalid argument handling, and runtime status reporting.
- Create: `pkg/channelcluster/deleting_test.go`
  Responsibility: `Deleting` fences, `Deleted` behavior, and in-flight write completion/failure semantics.

### Workstream B production files

- Modify: `pkg/wkpacket/common.go`
  Responsibility: protocol-version constants, message-seq size constants, and dedicated reason codes for `channel deleting`, `protocol upgrade required`, `idempotency conflict`, and `message seq exhausted`.
- Modify: `pkg/wkpacket/recv.go`
  Responsibility: `RecvPacket.MessageSeq` upgrade to `uint64`.
- Modify: `pkg/wkpacket/recvack.go`
  Responsibility: `RecvackPacket.MessageSeq` upgrade to `uint64`.
- Modify: `pkg/wkpacket/sendack.go`
  Responsibility: `SendackPacket.MessageSeq` upgrade to `uint64`.
- Modify: `pkg/wkproto/recv.go`
  Responsibility: versioned `uint32`/`uint64` decode-encode for recv packets.
- Modify: `pkg/wkproto/recvack.go`
  Responsibility: versioned `uint32`/`uint64` decode-encode for recv-ack packets.
- Modify: `pkg/wkproto/sendack.go`
  Responsibility: versioned `uint32`/`uint64` decode-encode for send-ack packets.
- Modify: `internal/gateway/auth.go`
  Responsibility: store negotiated protocol version in session values.
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
  Responsibility: use the session’s negotiated version for decode/encode instead of hard-coding `wkpacket.LatestVersion`.
- Modify: `internal/usecase/message/app.go`
  Responsibility: add narrow channelcluster and metadata-refresh ports without reintroducing a generic service layer.
- Modify: `internal/usecase/message/deps.go`
  Responsibility: define channelcluster-backed dependencies and keep online delivery/registry seams explicit.
- Modify: `internal/usecase/message/command.go`
  Responsibility: upgrade message-seq-bearing commands and request tokens to `uint64`.
- Modify: `internal/usecase/message/result.go`
  Responsibility: upgrade `SendResult.MessageSeq` to `uint64`.
- Modify: `internal/usecase/message/send.go`
  Responsibility: channelcluster-backed send path, one-shot metadata refresh/retry for `ErrStaleMeta`/`ErrNotLeader`, and local delivery after durable send success.
- Create: `internal/usecase/message/retry.go`
  Responsibility: small retry helper that keeps refresh policy out of access handlers.
- Modify: `internal/access/gateway/handler.go`
  Responsibility: keep handler surface stable while new message errors are mapped centrally.
- Modify: `internal/access/gateway/mapper.go`
  Responsibility: map `uint64 messageSeq` and protocol capability information between frames and usecase commands/results.
- Create: `internal/access/gateway/error_map.go`
  Responsibility: map channelcluster semantic errors to gateway protocol outcomes and retry decisions.
- Modify: `internal/access/api/message_send.go`
  Responsibility: emit `uint64 message_seq` and surface stable HTTP error mapping.
- Create: `internal/access/api/error_map.go`
  Responsibility: map channelcluster semantic errors to HTTP status/body without embedding protocol rules into the core library.
- Modify: `pkg/jsonrpc/types.go`
  Responsibility: upgrade JSON-RPC request/response/notification message-seq fields to `uint64`.
- Modify: `pkg/jsonrpc/wukongim_rpc_schema.json`
  Responsibility: schema updates from `uint32` to `uint64`.
- Modify: `pkg/jsonrpc/protocol.md`
  Responsibility: protocol documentation updates for `uint64 messageSeq` and upgrade-required behavior.

### Workstream B test files

- Modify: `pkg/wkpacket/common_test.go`
  Responsibility: protocol-version constant coverage.
- Modify: `pkg/wkpacket/packet_test.go`
  Responsibility: packet reset/round-trip expectations with `uint64`.
- Modify: `pkg/wkproto/recv_test.go`
  Responsibility: recv round-trip coverage across protocol versions.
- Modify: `pkg/wkproto/recvack_test.go`
  Responsibility: recv-ack round-trip coverage across protocol versions.
- Modify: `pkg/wkproto/sendack_test.go`
  Responsibility: send-ack round-trip coverage and legacy overflow rejection.
- Modify: `internal/gateway/protocol/wkproto/adapter_test.go`
  Responsibility: session-version-aware encode/decode behavior.
- Modify: `internal/usecase/message/send_test.go`
  Responsibility: retry-on-refresh, local delivery after durable send, and `uint64` result handling.
- Modify: `internal/usecase/message/recvack_test.go`
  Responsibility: `uint64 messageSeq` compile/runtime coverage.
- Modify: `internal/access/gateway/handler_test.go`
  Responsibility: gateway mapping for new reason codes and `uint64` send-ack fields.
- Modify: `internal/access/gateway/integration_test.go`
  Responsibility: mixed-version wkproto client behavior and upgrade-required coverage.
- Modify: `internal/access/api/server_test.go`
  Responsibility: `uint64` JSON payload coverage and HTTP error mapping.
- Modify: `internal/access/api/integration_test.go`
  Responsibility: end-to-end HTTP payload coverage with `uint64`.
- Modify: `pkg/jsonrpc/frame_bridge_test.go`
  Responsibility: frame bridge mapping with `uint64`.
- Modify: `pkg/jsonrpc/codec_test.go`
  Responsibility: JSON-RPC encode/decode with `uint64`.

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task.
- Keep `pkg/channelcluster` self-contained. Do not reintroduce a new “global service” object in `internal/usecase` or `internal/app`.
- Current `pkg/isr` does not expose a business-state apply callback, but it does call `CheckpointStore.Store(...)` whenever `HW` advances. Workstream A uses that durable commit boundary to replay committed records into the idempotency state without changing `pkg/isr`’s public API.
- `pkg/multiisr` already exposes enough append/status surface for Workstream A. Channel message fetches should read the channel message log through `pkg/channelcluster`’s own store seam instead of trying to reuse internal ISR replication fetches.
- Treat `messageSeq = committed offset + 1` as a locked invariant in both directions:
  - send path: committed `HW` becomes the returned `messageSeq`
  - fetch path: request `FromSeq` maps to `offset = FromSeq - 1`
- `internal/usecase/message` currently performs local-only sequence allocation. Workstream B removes that source of truth and treats `pkg/channelcluster` as the durable authority for `MessageID` and `MessageSeq`.
- `internal/gateway/protocol/wkproto/adapter.go` currently hard-codes `wkpacket.LatestVersion` for all frames. That must change before mixed-version behavior is trustworthy.
- `wkpacket.LatestVersion` should advance only after the old-version compatibility tests exist. The compatibility suite must explicitly exercise version `5` and the new u64 version.
- Do not attempt control-plane leader election or metadata quorum work here. The only metadata write entry in this plan is `ApplyMeta`.

## Workstream A: `pkg/channelcluster`

## Task 1: Lock the public API and dependency seams

**Files:**
- Create: `pkg/channelcluster/doc.go`
- Create: `pkg/channelcluster/types.go`
- Create: `pkg/channelcluster/errors.go`
- Create: `pkg/channelcluster/cluster.go`
- Test: `pkg/channelcluster/api_test.go`

- [ ] **Step 1: Write the failing public-surface tests**

```go
func TestNewValidatesRequiredDependencies(t *testing.T) {
	_, err := channelcluster.New(channelcluster.Config{})
	if !errors.Is(err, channelcluster.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestClusterSurfaceExposesMetaSendFetchStatus(t *testing.T) {
	var c channelcluster.Cluster
	_ = c.ApplyMeta(channelcluster.ChannelMeta{})
	_, _ = c.Send(context.Background(), channelcluster.SendRequest{})
	_, _ = c.Fetch(context.Background(), channelcluster.FetchRequest{})
	_, _ = c.Status(channelcluster.ChannelKey{})
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestNewValidatesRequiredDependencies|TestClusterSurfaceExposesMetaSendFetchStatus' -v`

Expected: FAIL because `pkg/channelcluster` does not exist yet.

- [ ] **Step 3: Add the package shell and exported contracts**

```go
type Config struct {
	Runtime    Runtime
	Log        MessageLog
	States     StateStoreFactory
	MessageIDs MessageIDGenerator
	Now        func() time.Time
}

type Runtime interface {
	Group(groupKey isr.GroupKey) (GroupHandle, bool)
}

type GroupHandle interface {
	Append(ctx context.Context, records []isr.Record) (isr.CommitResult, error)
	Status() isr.ReplicaState
}
```

Implementation details:
- define `ChannelKey`, `ChannelMeta`, `ChannelStatus`, `ChannelFeatures`, `MessageSeqFormat`, `SendRequest`, `SendResult`, `FetchRequest`, `FetchResult`, `ChannelRuntimeStatus`, `ChannelMessage`
- define `IdempotencyKey`, `IdempotencyEntry`, `MessageLog`, `ChannelStateStore`, `StateStoreFactory`, and `MessageIDGenerator`
- define exported sentinel errors including `ErrInvalidConfig`, `ErrConflictingMeta`, `ErrChannelDeleting`, `ErrChannelNotFound`, `ErrIdempotencyConflict`, `ErrProtocolUpgradeRequired`, `ErrMessageSeqExhausted`, and `ErrInvalidFetchArgument`
- add constructor signature `func New(cfg Config) (Cluster, error)`

- [ ] **Step 4: Add constructor validation and defaults**

Validation rules for V1:
- `Runtime != nil`
- `Log != nil`
- `States != nil`
- `MessageIDs != nil`
- `Now` defaults to `time.Now` when nil

- [ ] **Step 5: Re-run the targeted tests**

Run: `go test ./pkg/channelcluster -run 'TestNewValidatesRequiredDependencies|TestClusterSurfaceExposesMetaSendFetchStatus' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/channelcluster/doc.go pkg/channelcluster/types.go pkg/channelcluster/errors.go pkg/channelcluster/cluster.go pkg/channelcluster/api_test.go
git commit -m "feat: add channelcluster api shell"
```

## Task 2: Implement metadata cache, stale/conflict handling, and request-token checks

**Files:**
- Modify: `pkg/channelcluster/cluster.go`
- Create: `pkg/channelcluster/meta.go`
- Create: `pkg/channelcluster/testenv_test.go`
- Create: `pkg/channelcluster/meta_test.go`

- [ ] **Step 1: Write failing metadata tests**

```go
func TestApplyMetaRejectsConflictingReplay(t *testing.T) {
	c := newTestCluster(t)
	meta := testMeta("c1", 1, 7, 3, 9)
	require.NoError(t, c.ApplyMeta(meta))

	err := c.ApplyMeta(conflictingReplay(meta))
	if !errors.Is(err, channelcluster.ErrConflictingMeta) {
		t.Fatalf("expected ErrConflictingMeta, got %v", err)
	}
}

func TestStatusReturnsErrStaleMetaWhenCacheMisses(t *testing.T) {
	c := newTestCluster(t)
	_, err := c.Status(channelcluster.ChannelKey{ChannelID: "missing", ChannelType: 1})
	if !errors.Is(err, channelcluster.ErrStaleMeta) {
		t.Fatalf("expected ErrStaleMeta, got %v", err)
	}
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestApplyMetaRejectsConflictingReplay|TestStatusReturnsErrStaleMetaWhenCacheMisses' -v`

Expected: FAIL because metadata caching and conflict checks are not implemented.

- [ ] **Step 3: Build the test doubles**

```go
type fakeGroupHandle struct {
	state     isr.ReplicaState
	appendErr error
}

type fakeRuntime struct {
	groups map[uint64]*fakeGroupHandle
}

type fakeStateStore struct {
	idempotency map[channelcluster.IdempotencyKey]channelcluster.IdempotencyEntry
}
```

Requirements:
- `newTestCluster(t)` must create fake runtime, fake message log, fake state stores, and a deterministic `MessageIDGenerator`
- `testMeta(...)` must build valid metadata with `ChannelEpoch`, `LeaderEpoch`, replicas, ISR, and features; runtime `GroupKey` is derived locally from `ChannelKey`

- [ ] **Step 4: Implement `ApplyMeta` compare-and-replace rules**

Implementation details:
- cache key is always `ChannelKey{ChannelID, ChannelType}`
- missing cache state is represented by absence in the local map, not by a zero-value meta
- reject `ChannelEpoch` rollback with `ErrStaleMeta`
- reject `LeaderEpoch` rollback within equal `ChannelEpoch` with `ErrStaleMeta`
- treat identical versions plus identical key fields as idempotent replay
- treat identical versions plus any differing `Status/Replicas/ISR/Leader/MinISR/Features` as `ErrConflictingMeta`
- replace cache on higher `ChannelEpoch`
- replace cache on equal `ChannelEpoch` and higher `LeaderEpoch`

- [ ] **Step 5: Add request-token compatibility helpers and status lookup**

Rules:
- `ExpectedChannelEpoch == 0 && ExpectedLeaderEpoch == 0` means “no request-scoped token supplied”
- when a token is supplied, reject incompatible `(ChannelEpoch, LeaderEpoch)` with `ErrStaleMeta`
- `Status(...)` must use only local cache + local runtime state and never call a controller

- [ ] **Step 6: Re-run metadata tests plus API tests**

Run: `go test ./pkg/channelcluster -run 'TestNewValidatesRequiredDependencies|TestApplyMetaRejectsConflictingReplay|TestStatusReturnsErrStaleMetaWhenCacheMisses' -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/channelcluster/cluster.go pkg/channelcluster/meta.go pkg/channelcluster/testenv_test.go pkg/channelcluster/meta_test.go
git commit -m "feat: add channelcluster metadata cache rules"
```

## Task 3: Implement the record codec and committed-state apply bridge

**Files:**
- Modify: `pkg/channelcluster/types.go`
- Create: `pkg/channelcluster/codec.go`
- Create: `pkg/channelcluster/apply.go`
- Create: `pkg/channelcluster/apply_test.go`

- [ ] **Step 1: Write failing apply/recovery tests**

```go
func TestCheckpointBridgeReplaysCommittedRecordsIntoIdempotencyState(t *testing.T) {
	env := newApplyEnv(t)
	require.NoError(t, env.bridge.Store(isr.Checkpoint{Epoch: 3, HW: 2}))

	entry, ok, err := env.state.GetIdempotency(env.key)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(2), entry.MessageSeq)
}

func TestSnapshotBridgeRestoresStateBeforeServingRecoveredReads(t *testing.T) {
	env := newApplyEnv(t)
	require.NoError(t, env.snapshot.InstallSnapshot(context.Background(), isr.Snapshot{
		GroupKey:  isr.GroupKey("channel/1/YzE"),
		Epoch:     4,
		EndOffset: 9,
		Payload:   []byte("snapshot"),
	}))
	require.True(t, env.state.restoreCalled)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestCheckpointBridgeReplaysCommittedRecordsIntoIdempotencyState|TestSnapshotBridgeRestoresStateBeforeServingRecoveredReads' -v`

Expected: FAIL because the codec and apply bridge do not exist yet.

- [ ] **Step 3: Add the log-record codec and payload hashing**

```go
type storedMessage struct {
	MessageID   uint64
	SenderUID   string
	ClientMsgNo string
	PayloadHash uint64
	Payload     []byte
}

func hashPayload(payload []byte) uint64 { /* fnv.New64a */ }
```

Implementation details:
- encode exactly one business message per ISR record in V1
- include `MessageID`, `SenderUID`, `ClientMsgNo`, raw payload, and payload hash in the encoded record
- decoding helpers must convert offsets to `MessageSeq` with `seq = offset + 1`

- [ ] **Step 4: Add the committed-range replay helper**

Implementation details:
- track `prevHW` inside the bridge so each `Checkpoint.Store` replays only `[prevHW, nextHW)`
- decode each newly committed record and derive the idempotency entry from the committed offset
- keep an internal helper interface in `apply.go`:

```go
type committingStateStore interface {
	ChannelStateStore
	CommitCommitted(checkpoint isr.Checkpoint, batch []appliedMessage) error
}
```

- [ ] **Step 5: Add the snapshot restore bridge**

Rules:
- on snapshot install, restore channel state from snapshot payload before exposing the new checkpointed range
- recovery tests must cover “idempotency survives replica reload without resending the original message”

- [ ] **Step 6: Re-run apply tests**

Run: `go test ./pkg/channelcluster -run 'TestCheckpointBridgeReplaysCommittedRecordsIntoIdempotencyState|TestSnapshotBridgeRestoresStateBeforeServingRecoveredReads' -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/channelcluster/types.go pkg/channelcluster/codec.go pkg/channelcluster/apply.go pkg/channelcluster/apply_test.go
git commit -m "feat: add channelcluster commit apply bridge"
```

## Task 4: Implement the send path, idempotency, and legacy-u32 exhaustion checks

**Files:**
- Modify: `pkg/channelcluster/cluster.go`
- Modify: `pkg/channelcluster/send.go`
- Modify: `pkg/channelcluster/testenv_test.go`
- Create: `pkg/channelcluster/send_test.go`

- [ ] **Step 1: Write failing send-path tests**

```go
func TestSendReturnsCommittedMessageSeqFromHW(t *testing.T) {
	env := newSendEnv(t)
	result, err := env.cluster.Send(context.Background(), testSendRequest())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.MessageSeq)
}

func TestSendReturnsExistingEntryOnIdempotentRetry(t *testing.T) {
	env := newSendEnv(t)
	first, err := env.cluster.Send(context.Background(), testSendRequest())
	require.NoError(t, err)
	second, err := env.cluster.Send(context.Background(), testSendRequest())
	require.NoError(t, err)
	require.Equal(t, first, second)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestSendReturnsCommittedMessageSeqFromHW|TestSendReturnsExistingEntryOnIdempotentRetry' -v`

Expected: FAIL because the send path is not implemented.

- [ ] **Step 3: Implement request validation and leader checks**

Validation order:
- resolve metadata from local cache or return `ErrStaleMeta`
- reject `Status == Deleting` with `ErrChannelDeleting`
- reject `Status == Deleted` with `ErrChannelNotFound`
- reject request-token mismatch with `ErrStaleMeta`
- resolve local group handle by the local `ChannelKey -> isr.GroupKey` derivation; if missing, return `ErrStaleMeta`
- inspect `group.Status()` and return `ErrNotLeader` unless local role is an unfenced leader

- [ ] **Step 4: Implement idempotency hit/conflict behavior**

Implementation details:
- idempotency key is `(channelID, channelType, senderUID, clientMsgNo)`
- when `clientMsgNo == ""`, skip idempotency lookup entirely in V1
- when the key hits and payload hashes match, return the stored `MessageID` / `MessageSeq`
- when the key hits and payload hashes differ, return `ErrIdempotencyConflict`

- [ ] **Step 5: Append one record, translate commit to `messageSeq`, and enforce legacy exhaustion**

Rules:
- allocate `MessageID` before append using `cfg.MessageIDs.Next()`
- append exactly one encoded ISR record
- derive `messageSeq` from `commit.NextCommitHW` because committed `HW` is the exclusive frontier and `messageSeq = committed offset + 1`
- when `meta.Features.MessageSeqFormat == LegacyU32` and `messageSeq > math.MaxUint32`, return `ErrMessageSeqExhausted`

- [ ] **Step 6: Add negative-path coverage**

Add tests for:
- `ErrNotLeader`
- `ErrStaleMeta`
- `ErrIdempotencyConflict`
- `ErrChannelDeleting`
- `ErrMessageSeqExhausted`

- [ ] **Step 7: Re-run send tests plus prior suites**

Run: `go test ./pkg/channelcluster -run 'TestSendReturnsCommittedMessageSeqFromHW|TestSendReturnsExistingEntryOnIdempotentRetry|TestApplyMetaRejectsConflictingReplay' -v`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add pkg/channelcluster/cluster.go pkg/channelcluster/send.go pkg/channelcluster/testenv_test.go pkg/channelcluster/send_test.go
git commit -m "feat: implement channelcluster send path"
```

## Task 5: Implement committed fetch and runtime status

**Files:**
- Modify: `pkg/channelcluster/fetch.go`
- Modify: `pkg/channelcluster/testenv_test.go`
- Create: `pkg/channelcluster/fetch_test.go`

- [ ] **Step 1: Write failing fetch/status tests**

```go
func TestFetchFromSeqOneReturnsCommittedMessagesOnly(t *testing.T) {
	env := newFetchEnv(t)
	result, err := env.cluster.Fetch(context.Background(), channelcluster.FetchRequest{
		Key:      env.key,
		FromSeq:  1,
		Limit:    10,
		MaxBytes: 1024,
	})
	require.NoError(t, err)
	require.Len(t, result.Messages, 2)
	require.Equal(t, uint64(3), result.NextSeq)
	require.Equal(t, uint64(2), result.CommittedSeq)
}

func TestFetchRejectsInvalidBudget(t *testing.T) {
	env := newFetchEnv(t)
	_, err := env.cluster.Fetch(context.Background(), channelcluster.FetchRequest{
		Key: env.key, FromSeq: 1, Limit: 1, MaxBytes: 0,
	})
	require.ErrorIs(t, err, channelcluster.ErrInvalidFetchBudget)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestFetchFromSeqOneReturnsCommittedMessagesOnly|TestFetchRejectsInvalidBudget' -v`

Expected: FAIL because fetch/status logic is not implemented.

- [ ] **Step 3: Implement fetch argument validation and range translation**

Rules:
- `Limit <= 0` returns `ErrInvalidFetchArgument`
- `MaxBytes <= 0` returns `ErrInvalidFetchBudget`
- `FromSeq == 0` maps to `max(1, state.LogStartOffset+1)`
- `FromSeq >= 1` maps to `offset = FromSeq - 1`
- `FromSeq > CommittedSeq` returns an empty result with `NextSeq = FromSeq`

- [ ] **Step 4: Read only committed offsets and decode them back to channel messages**

Implementation details:
- calculate `committedSeq` from the local group status `HW`
- only decode offsets `< HW`
- stop at `Limit` or `MaxBytes`, whichever is hit first
- return messages ordered strictly by `MessageSeq`

- [ ] **Step 5: Implement `Status(...)` translation**

`ChannelRuntimeStatus` fields:
- `Key` from the cache key
- `Status/Leader/LeaderEpoch` from cached metadata
- `HW/CommittedSeq` from the live group status

- [ ] **Step 6: Re-run fetch/status tests**

Run: `go test ./pkg/channelcluster -run 'TestFetchFromSeqOneReturnsCommittedMessagesOnly|TestFetchRejectsInvalidBudget' -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/channelcluster/fetch.go pkg/channelcluster/testenv_test.go pkg/channelcluster/fetch_test.go
git commit -m "feat: add channelcluster fetch and status"
```

## Task 6: Lock delete fences and in-flight write semantics

**Files:**
- Modify: `pkg/channelcluster/send.go`
- Modify: `pkg/channelcluster/fetch.go`
- Modify: `pkg/channelcluster/testenv_test.go`
- Create: `pkg/channelcluster/deleting_test.go`

- [ ] **Step 1: Write failing delete-state tests**

```go
func TestDeletingFencesNewSendAndFetchRequests(t *testing.T) {
	env := newDeletingEnv(t)
	_, err := env.cluster.Send(context.Background(), testSendRequest())
	require.ErrorIs(t, err, channelcluster.ErrChannelDeleting)

	_, err = env.cluster.Fetch(context.Background(), channelcluster.FetchRequest{
		Key: env.key, FromSeq: 1, Limit: 1, MaxBytes: 128,
	})
	require.ErrorIs(t, err, channelcluster.ErrChannelDeleting)
}

func TestInFlightSendReturnsDeletingWhenFenceWinsBeforeCommit(t *testing.T) {
	env := newDeletingEnv(t)
	env.group.blockAppend()
	errCh := env.asyncSend()
	env.applyDeletingMeta()
	require.ErrorIs(t, <-errCh, channelcluster.ErrChannelDeleting)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/channelcluster -run 'TestDeletingFencesNewSendAndFetchRequests|TestInFlightSendReturnsDeletingWhenFenceWinsBeforeCommit' -v`

Expected: FAIL because delete fences and in-flight semantics are not complete.

- [ ] **Step 3: Add post-append fence rechecks**

Rules:
- if metadata flips to `Deleting` before append starts, reject immediately
- if metadata flips to `Deleting` after append starts but before committed result returns, treat the operation as failed with `ErrChannelDeleting`
- if the commit has already completed successfully, return success and keep the idempotency entry

- [ ] **Step 4: Add fetch/status behavior for `Deleted`**

Rules:
- `Send` and `Fetch` against cached `Deleted` metadata return `ErrChannelNotFound`
- cache miss still returns `ErrStaleMeta`

- [ ] **Step 5: Re-run the full `pkg/channelcluster` suite**

Run: `go test ./pkg/channelcluster -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/channelcluster/send.go pkg/channelcluster/fetch.go pkg/channelcluster/testenv_test.go pkg/channelcluster/deleting_test.go
git commit -m "feat: lock channelcluster deleting semantics"
```

## Workstream B: access layer and protocol upgrade

## Task 7: Upgrade packet structs and wkproto to dual-stack `messageSeq`

**Files:**
- Modify: `pkg/wkpacket/common.go`
- Modify: `pkg/wkpacket/recv.go`
- Modify: `pkg/wkpacket/recvack.go`
- Modify: `pkg/wkpacket/sendack.go`
- Modify: `pkg/wkpacket/common_test.go`
- Modify: `pkg/wkpacket/packet_test.go`
- Modify: `pkg/wkproto/recv.go`
- Modify: `pkg/wkproto/recvack.go`
- Modify: `pkg/wkproto/sendack.go`
- Modify: `pkg/wkproto/recv_test.go`
- Modify: `pkg/wkproto/recvack_test.go`
- Modify: `pkg/wkproto/sendack_test.go`

- [ ] **Step 1: Write failing protocol-version tests**

```go
func TestEncodeRecvVersion5KeepsUint32MessageSeq(t *testing.T) {
	packet := &wkpacket.RecvPacket{MessageID: 9, MessageSeq: 42}
	wire, err := wkproto.New().EncodeFrame(packet, 5)
	require.NoError(t, err)
	decoded, _, err := wkproto.New().DecodeFrame(wire, 5)
	require.NoError(t, err)
	require.Equal(t, uint64(42), decoded.(*wkpacket.RecvPacket).MessageSeq)
}

func TestEncodeSendackVersion6SupportsUint64MessageSeq(t *testing.T) {
	packet := &wkpacket.SendackPacket{MessageID: 9, MessageSeq: uint64(math.MaxUint32) + 7}
	wire, err := wkproto.New().EncodeFrame(packet, wkpacket.LatestVersion)
	require.NoError(t, err)
	decoded, _, err := wkproto.New().DecodeFrame(wire, wkpacket.LatestVersion)
	require.NoError(t, err)
	require.Equal(t, packet.MessageSeq, decoded.(*wkpacket.SendackPacket).MessageSeq)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/wkpacket ./pkg/wkproto -run 'TestEncodeRecvVersion5KeepsUint32MessageSeq|TestEncodeSendackVersion6SupportsUint64MessageSeq' -v`

Expected: FAIL because packet fields and codec logic are still `uint32`.

- [ ] **Step 3: Upgrade packet fields and protocol constants**

Implementation details:
- change `RecvPacket.MessageSeq`, `RecvackPacket.MessageSeq`, and `SendackPacket.MessageSeq` to `uint64`
- introduce explicit constants:

```go
const (
	LegacyMessageSeqVersion = 5
	MessageSeqU64Version    = 6
	LatestVersion           = MessageSeqU64Version
)
```

- add separate byte-size constants for `uint32` vs `uint64` message-seq encodings
- add dedicated reason codes `ReasonChannelDeleting`, `ReasonProtocolUpgradeRequired`, `ReasonIdempotencyConflict`, and `ReasonMessageSeqExhausted`

- [ ] **Step 4: Add version-aware encode/decode helpers**

Implementation details:
- for version `<= 5`, read/write `uint32` and upcast to `uint64` in memory
- for version `>= 6`, read/write `uint64`
- reject legacy encode attempts when `messageSeq > math.MaxUint32`

- [ ] **Step 5: Re-run packet and wkproto tests**

Run: `go test ./pkg/wkpacket ./pkg/wkproto -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/wkpacket/common.go pkg/wkpacket/recv.go pkg/wkpacket/recvack.go pkg/wkpacket/sendack.go pkg/wkpacket/common_test.go pkg/wkpacket/packet_test.go pkg/wkproto/recv.go pkg/wkproto/recvack.go pkg/wkproto/sendack.go pkg/wkproto/recv_test.go pkg/wkproto/recvack_test.go pkg/wkproto/sendack_test.go
git commit -m "feat: add dual-stack message seq wire codec"
```

## Task 8: Negotiate and preserve the session protocol version in gateway wkproto handling

**Files:**
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
- Modify: `internal/gateway/protocol/wkproto/adapter_test.go`
- Modify: `internal/gateway/gateway_test.go`

- [ ] **Step 1: Write failing gateway-version tests**

```go
func TestAuthenticatorStoresNegotiatedProtocolVersion(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{})
	result, err := auth.Authenticate(nil, &wkpacket.ConnectPacket{Version: 5, UID: "u1"})
	require.NoError(t, err)
	require.Equal(t, uint8(5), result.SessionValues[gateway.SessionValueProtocolVersion])
}

func TestWKProtoAdapterUsesSessionVersionForOutboundFrames(t *testing.T) {
	sess := testSessionWithVersion(5)
	wire, err := wkprotoadapter.New().Encode(sess, &wkpacket.SendackPacket{MessageSeq: 9}, session.OutboundMeta{})
	require.NoError(t, err)
	decoded, _, err := wkproto.New().DecodeFrame(wire, 5)
	require.NoError(t, err)
	require.Equal(t, uint64(9), decoded.(*wkpacket.SendackPacket).MessageSeq)
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./internal/gateway/... -run 'TestAuthenticatorStoresNegotiatedProtocolVersion|TestWKProtoAdapterUsesSessionVersionForOutboundFrames' -v`

Expected: FAIL because the session does not store protocol version and the adapter hard-codes `LatestVersion`.

- [ ] **Step 3: Add a dedicated session-value key for the negotiated version**

Implementation details:
- add `SessionValueProtocolVersion = "gateway.protocol_version"`
- store the clamped server version in `AuthResult.SessionValues`
- continue returning the same `Connack.ServerVersion` behavior

- [ ] **Step 4: Make the wkproto adapter session-version-aware**

Rules:
- decode `CONNECT` with `wkpacket.LatestVersion`
- after authentication, decode and encode all subsequent frames using the version stored on the session
- default to `wkpacket.LegacyMessageSeqVersion` only when no version is present

- [ ] **Step 5: Re-run gateway protocol tests**

Run: `go test ./internal/gateway/... ./internal/access/gateway -run 'TestAuthenticatorStoresNegotiatedProtocolVersion|TestWKProtoAdapterUsesSessionVersionForOutboundFrames' -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/auth.go internal/gateway/protocol/wkproto/adapter.go internal/gateway/protocol/wkproto/adapter_test.go internal/gateway/gateway_test.go
git commit -m "feat: track negotiated wkproto version per session"
```

## Task 9: Switch the message usecase, API, and gateway to channelcluster-backed `uint64` semantics

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/result.go`
- Modify: `internal/usecase/message/send.go`
- Create: `internal/usecase/message/retry.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/message/recvack.go`
- Modify: `internal/usecase/message/recvack_test.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/mapper.go`
- Create: `internal/access/gateway/error_map.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/access/api/message_send.go`
- Create: `internal/access/api/error_map.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/api/integration_test.go`

- [ ] **Step 1: Write failing usecase/access tests**

```go
func TestSendRetriesOnceAfterRefreshingMeta(t *testing.T) {
	app := newChannelClusterBackedApp(t, staleThenSuccessCluster())
	result, err := app.Send(testSendCommand())
	require.NoError(t, err)
	require.Equal(t, uint64(7), result.MessageSeq)
}

func TestAPIRespondsWithUint64MessageSeq(t *testing.T) {
	srv := New(Options{Messages: stubMessageUsecase(message.SendResult{MessageSeq: uint64(math.MaxUint32) + 3})})
	// issue request and assert JSON message_seq is the full uint64 value
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./internal/usecase/message ./internal/access/api ./internal/access/gateway -run 'TestSendRetriesOnceAfterRefreshingMeta|TestAPIRespondsWithUint64MessageSeq' -v`

Expected: FAIL because the usecase is still local-sequence-based and the access layer still assumes `uint32`.

- [ ] **Step 3: Add narrow channelcluster and metadata-refresh ports**

```go
type ChannelCluster interface {
	ApplyMeta(meta channelcluster.ChannelMeta) error
	Send(ctx context.Context, req channelcluster.SendRequest) (channelcluster.SendResult, error)
}

type MetaRefresher interface {
	RefreshChannelMeta(ctx context.Context, key channelcluster.ChannelKey) (channelcluster.ChannelMeta, error)
}
```

Rules:
- keep these seams in `internal/usecase/message`; do not create a new global service layer
- preserve `online.Registry` and `online.Delivery` ownership in the usecase for local push behavior

- [ ] **Step 4: Replace local sequence allocation with durable cluster send + one retry**

Implementation details:
- build `channelcluster.SendRequest` from `SendCommand`
- on `ErrStaleMeta` or `ErrNotLeader`, call `MetaRefresher.RefreshChannelMeta(...)`, then `cluster.ApplyMeta(meta)` through the injected port, and retry exactly once
- on success, use returned `MessageID` / `MessageSeq` to build outbound recv/send-ack packets
- keep unsupported channel-type behavior explicit until group-channel handling lands

- [ ] **Step 5: Add centralized access-layer error mapping**

Map at minimum:
- `ErrChannelNotFound` -> gateway `ReasonChannelNotExist`, HTTP `404`
- `ErrChannelDeleting` -> gateway `ReasonChannelDeleting`, HTTP `409`
- `ErrProtocolUpgradeRequired` -> gateway `ReasonProtocolUpgradeRequired`, HTTP `426`
- `ErrIdempotencyConflict` -> gateway `ReasonIdempotencyConflict`, HTTP `409`
- `ErrMessageSeqExhausted` -> gateway `ReasonMessageSeqExhausted`, HTTP `409`

- [ ] **Step 6: Upgrade `messageSeq` fields to `uint64` across usecase/API/gateway**

Implementation details:
- `message.SendResult.MessageSeq` becomes `uint64`
- `message.RecvAckCommand.MessageSeq` becomes `uint64`
- API response JSON stays `message_seq` but now serializes `uint64`
- gateway mappers stop narrowing `messageSeq` before writing frames

- [ ] **Step 7: Re-run usecase and access tests**

Run: `go test ./internal/usecase/message ./internal/access/api ./internal/access/gateway -v`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/message/app.go internal/usecase/message/deps.go internal/usecase/message/command.go internal/usecase/message/result.go internal/usecase/message/send.go internal/usecase/message/retry.go internal/usecase/message/send_test.go internal/usecase/message/recvack.go internal/usecase/message/recvack_test.go internal/access/gateway/handler.go internal/access/gateway/mapper.go internal/access/gateway/error_map.go internal/access/gateway/handler_test.go internal/access/gateway/integration_test.go internal/access/api/message_send.go internal/access/api/error_map.go internal/access/api/server_test.go internal/access/api/integration_test.go
git commit -m "feat: wire channelcluster semantics into access layer"
```

## Task 10: Upgrade JSON-RPC and add mixed-version compatibility coverage

**Files:**
- Modify: `pkg/jsonrpc/types.go`
- Modify: `pkg/jsonrpc/frame_bridge_test.go`
- Modify: `pkg/jsonrpc/codec_test.go`
- Modify: `pkg/jsonrpc/wukongim_rpc_schema.json`
- Modify: `pkg/jsonrpc/protocol.md`
- Modify: `internal/access/gateway/integration_test.go`

- [ ] **Step 1: Write failing JSON-RPC and mixed-version tests**

```go
func TestJSONRPCSendResultCarriesUint64MessageSeq(t *testing.T) {
	msg, err := jsonrpc.FromFrame("req-1", &wkpacket.SendackPacket{
		MessageID:  9,
		MessageSeq: uint64(math.MaxUint32) + 55,
		ReasonCode: wkpacket.ReasonSuccess,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(math.MaxUint32)+55, msg.(jsonrpc.SendResponse).Result.MessageSeq)
}

func TestGatewayVersion5ClientGetsUpgradeRequiredOnU64OnlyChannel(t *testing.T) {
	// connect with version 5, inject a send result path that returns ErrProtocolUpgradeRequired,
	// assert the gateway response is the dedicated upgrade-required reason code
}
```

- [ ] **Step 2: Run the targeted tests and confirm they fail**

Run: `go test ./pkg/jsonrpc ./internal/access/gateway -run 'TestJSONRPCSendResultCarriesUint64MessageSeq|TestGatewayVersion5ClientGetsUpgradeRequiredOnU64OnlyChannel' -v`

Expected: FAIL because JSON-RPC still uses `uint32` and compatibility behavior is not covered.

- [ ] **Step 3: Upgrade JSON-RPC message-seq fields and schema**

Implementation details:
- switch all JSON-RPC message-seq fields in `types.go` from `uint32` to `uint64`
- update `wukongim_rpc_schema.json` `format` values accordingly
- update `protocol.md` examples and field descriptions

- [ ] **Step 4: Add mixed-version and exhaustion coverage**

Minimum coverage:
- wkproto version `5` client on `LegacyU32` channel still works
- wkproto version `5` client on `U64` channel receives upgrade-required behavior
- `LegacyU32` channel rejects writes past `math.MaxUint32` with `ErrMessageSeqExhausted`

- [ ] **Step 5: Re-run JSON-RPC, gateway, and protocol suites**

Run: `go test ./pkg/jsonrpc ./pkg/wkproto ./internal/access/gateway -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/jsonrpc/types.go pkg/jsonrpc/frame_bridge_test.go pkg/jsonrpc/codec_test.go pkg/jsonrpc/wukongim_rpc_schema.json pkg/jsonrpc/protocol.md internal/access/gateway/integration_test.go
git commit -m "feat: finish message seq u64 integration"
```

## Final Verification

- [ ] Run the full focused suites for this plan:

```bash
go test ./pkg/channelcluster ./pkg/wkpacket ./pkg/wkproto ./pkg/jsonrpc ./internal/usecase/message ./internal/access/api ./internal/access/gateway
```

Expected: PASS

- [ ] Run the broader regression slice that covers the touched runtime and gateway packages:

```bash
go test ./pkg/isr ./pkg/multiisr ./internal/gateway/... ./internal/app/...
```

Expected: PASS, or any unrelated pre-existing failures are documented before proceeding.
