# Channel Message Retention Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add cluster-consistent automatic channel message retention that hides and physically trims expired channel message prefixes without leader/follower divergence.

**Architecture:** Retention is leader-driven and cluster-authoritative: the current channel leader computes a safe `RetentionThroughSeq`, commits it through slot metadata, and every replica applies that monotonic logical read fence. Physical deletion is local and asynchronous, tracked by separate store retention state; checkpoint `LogStartOffset` remains snapshot-only and is never advanced by retention.

**Tech Stack:** Go, Pebble, WuKongIM channel ISR runtime, slot metadata Raft/FSM, app lifecycle/config loaders, existing channel handler read APIs.

---

## File Structure

- `pkg/slot/meta/channel_runtime_meta.go`: Add authoritative retention fields, retention-only advance request, validation, and monotonic merge rules.
- `pkg/slot/meta/errors.go`: Add a stale metadata error for fenced retention advance rejections.
- `pkg/slot/meta/catalog.go`: Add retention columns to the `channel_runtime_meta` catalog.
- `pkg/slot/meta/codec.go`: Encode/decode retention fields with zero defaults for older records.
- `pkg/slot/meta/batch.go`: Add `WriteBatch.AdvanceChannelRetentionThroughSeq` for retention-only slot FSM application.
- `pkg/slot/meta/channel_runtime_meta_test.go`: Cover retention field persistence, no-regression, higher-epoch carry-forward, and narrow advance fences.
- `pkg/slot/meta/codec_test.go`: Cover old encoded values decoding retention fields as zero if existing codec fixtures are centralized here.
- `pkg/slot/fsm/command.go`: Add a narrow `AdvanceChannelRetentionThroughSeq` command and optional retention fields on full runtime metadata upsert.
- `pkg/slot/fsm/command_inspection.go`: Expose retention fields/advance commands in command inspection output.
- `pkg/slot/fsm/state_machine_test.go`: Verify FSM command round-trips and applies retention-only advances.
- `pkg/slot/fsm/command_inspection_test.go`: Verify inspection output includes retention details.
- `pkg/slot/proxy/store.go`: Add authoritative `AdvanceChannelRetentionThroughSeq` proxy method that proposes through slot Raft.
- `pkg/slot/proxy/runtime_meta_rpc.go`: No wire change expected for reads because JSON carries new struct fields; update tests if needed.
- `pkg/slot/proxy/integration_test.go`: Verify distributed read/write retention metadata through proxy APIs.
- `pkg/channel/types.go`: Add retention fields to `Meta`, `ReplicaState`, `ChannelRuntimeStatus`, and `FetchResult`; add `RetainedThroughOffset`/`MinAvailableSeq` helpers and a typed retention reset result/error.
- `internal/runtime/channelmeta/resolver.go`: Project authoritative `RetentionThroughSeq` from slot metadata into channel metadata.
- `internal/runtime/channelmeta/resolver_test.go`: Cover projection and authoritative apply behavior.
- `pkg/channel/runtime/runtime.go`: Include retention in meta equality, apply retention after meta refresh, and expose runtime retention view/apply methods.
- `pkg/channel/runtime/types.go`: Extend runtime interfaces with retention view/apply contracts.
- `pkg/channel/runtime/runtime_test.go`: Cover runtime routing to replica retention methods.
- `pkg/channel/runtime/replicator.go`: Ensure replication request/reset behavior respects retained fetch floors if runtime-level adjustments are needed.
- `pkg/channel/runtime/longpoll.go`: Include retention reset details in long-poll responses when a follower is below the retained-through offset.
- `pkg/channel/replica/types.go`: Extend `Replica` and optional store contracts for logical retention, physical trim, and retention progress view.
- `pkg/channel/replica/commands.go`: Add retention apply/view command/effect/result structs.
- `pkg/channel/replica/retention.go`: Implement logical fence publication, safe physical trim scheduling, and current-ISR retention progress view.
- `pkg/channel/replica/progress_pipeline.go`: Update observed ISR retention progress from fetch/cursor events without treating unknown followers as caught up.
- `pkg/channel/replica/fetch_pipeline.go`: Reject/reset follower fetches below the retained floor instead of serving sparse records.
- `pkg/channel/replica/recovery.go`: Recover retention state separately from checkpoint `LogStartOffset`.
- `pkg/channel/replica/invariant.go`: Add retention invariants that do not conflate retention with snapshot state.
- `pkg/channel/replica/retention_test.go`: Cover logical apply, physical trim gating, ISR progress, fetch floor, and no-op repeats.
- `pkg/channel/replica/FLOW.md`: Update package flow for retention state ownership and error semantics.
- `pkg/channel/FLOW.md`: Update channel package overview if retention changes public runtime/read semantics.
- `pkg/channel/store/keys.go`: Add channel-store system keys for retention state/log bounds.
- `pkg/channel/store/codec.go`: Encode/decode local retention state.
- `pkg/channel/store/message_log.go`: Preserve LEO after full prefix trim by using retained log bounds.
- `pkg/channel/store/engine.go`: Make `ListChannelKeys` include channels represented only by retention/system state after full prefix trim.
- `pkg/channel/store/message_table.go`: Add retention-specific prefix scanning/deletion helpers.
- `pkg/channel/store/message_retention.go`: New public store primitives for expired-prefix scan, durable retention floor adoption/reset, atomic physical trim, retention state, and durable cursor confirm.
- `pkg/channel/store/message_retention_test.go`: Cover expired scan, zero timestamp stop, atomic index cleanup, LEO retention, and idempotent trim.
- `pkg/channel/store/committed_cursor.go`: Add durable cursor confirm/advance paths while preserving hot-path `NoSync` replay cursor writes.
- `pkg/channel/store/committed_cursor_test.go`: Cover durable cursor confirm behavior.
- `pkg/channel/handler/fetch.go`: Clamp client fetches to `MinAvailableSeq` and return the floor.
- `pkg/channel/handler/message_sync.go`: Clamp legacy up/down/latest sync below `MinAvailableSeq`.
- `pkg/channel/handler/seq_read.go`: Add min-available-aware sequence helpers.
- `pkg/channel/handler/message_query.go`: Filter `MessageID`, `ClientMsgNo`, and latest queries below `MinAvailableSeq`.
- `pkg/channel/handler/*_test.go`: Add read-path floor tests.
- `internal/app/committed_replay.go`: Start replay from `max(cursor+1, MinAvailableSeq)` and durably skip already-retained prefixes.
- `internal/app/committed_replay_test.go`: Cover replay floor and durable cursor behavior.
- `internal/runtime/channelretention/`: New worker package with safe-boundary calculation, scanning orchestration, and unit tests.
- `internal/app/channelretention.go`: App adapter from worker ports to channel log DB, channel runtime, committed replay cursor, and slot metadata.
- `internal/app/app.go`: Add retention worker fields and test hooks.
- `internal/app/build.go`: Construct retention worker when TTL is enabled.
- `internal/app/lifecycle_components.go`: Start retention after committed replay and before gateway/API/manager; stop it before committed replay and channel log shutdown.
- `internal/app/lifecycle_test.go`: Verify lifecycle order and stop order.
- `internal/app/config.go`: Add retention config with defaults/validation and detailed English comments.
- `cmd/wukongim/config.go`: Parse `WK_CHANNEL_MESSAGE_RETENTION_*` keys.
- `cmd/wukongim/config_test.go`: Cover config parsing and env override.
- `internal/app/config_test.go`: Cover default disabled config and invalid values.
- `internal/access/node/channel_messages_rpc.go`: Include min-available-aware local query/sync/max-seq behavior for remote manager/node reads.
- `internal/app/manager_messages.go`: Use authoritative/runtime retention floor for manager query/sync paths.
- `internal/app/manager_messages_test.go`: Cover manager filtering below retention.
- `wukongim.conf.example`: Document retention keys.
- `docs/development/PROJECT_KNOWLEDGE.md`: Add one concise rule that retention uses slot metadata `RetentionThroughSeq`, not local TTL or checkpoint `LogStartOffset`.
- `internal/runtime/channelmeta/FLOW.md`: Update if resolver/apply flow changes are no longer described accurately.
- `internal/FLOW.md`: Update if app/runtime lifecycle flow is now stale because of the retention worker.

## Implementation Notes

- `RetentionThroughSeq` is the only authoritative retention boundary. `MinAvailableSeq` is derived as `max(RetentionThroughSeq + 1, LogStartOffset + 1, 1)`, with `LogStartOffset` coming only from snapshot state.
- `LocalRetentionThroughSeq` is the local durable adoption/catch-up floor. It can advance even when local `LEO < RetentionThroughSeq`, so old replicas can restart and fetch from the authoritative boundary plus one.
- `PhysicalRetentionThroughSeq` is a local store/runtime deletion progress marker only. It can lag behind both `RetentionThroughSeq` and `LocalRetentionThroughSeq` and must never affect whether old messages are visible.
- Use distinct helper names for replication offsets and client-readable sequences: `RetainedThroughOffset = max(RetentionThroughSeq, LogStartOffset)` is the last unavailable/proof offset; `MinAvailableSeq = RetainedThroughOffset + 1` (or `1` when zero) is the first sequence a client read may return.
- Do not update checkpoint `LogStartOffset` for retention. Existing recovery treats nonzero `LogStartOffset` as snapshot-backed and requires compatible snapshot payload state.
- TTL scanning uses message row server `Timestamp` in seconds. `Timestamp <= 0` and malformed rows are not expired; the scan stops at that row.
- Retention cannot advance beyond the current leader durable committed replay cursor. Current committed replay is channel-leader-authoritative; followers must durable fast-forward their local cursor when applying an authoritative boundary so a future leader never replays below `MinAvailableSeq`. The hot replay cursor write may remain `NoSync`, but retention must perform a Sync confirmation before using the leader cursor as a safety gate. Worker ordering is two-phase: compute a provisional boundary without replay cursor, durable-confirm the current leader cursor before metadata commit, then cap the final boundary at the confirmed cursor before proposing metadata. Cursor fast-forward during local boundary adoption happens after metadata commit and is not a substitute for the leader pre-commit confirm.
- Current ISR progress for retention must be observed in the current leader epoch. Missing/unknown follower progress must block advancement beyond the current retention boundary; do not reuse quorum HW defaults as proof that every ISR follower caught up.
- Retention catch-up is a named reset path, not snapshot install: replication fetch/long-poll below `RetainedThroughOffset` returns a retention reset result with the authoritative boundary; the follower durably adopts that boundary and resumes from `boundary + 1` while staying out of ISR/leader eligibility until caught up.
- Single-node deployments are still single-node clusters; do not add standalone retention paths.

---

### Task 1: Persist Authoritative Retention Fields In Slot Metadata

**Files:**
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/codec.go`
- Modify: `pkg/slot/meta/channel_runtime_meta_test.go`
- Modify: `pkg/slot/meta/codec_test.go` if codec tests are centralized there

- [ ] **Step 1: Write failing persistence test**

Add `TestShardStoreChannelRuntimeMetaPersistsRetentionBoundary` in `pkg/slot/meta/channel_runtime_meta_test.go`:

```go
func TestShardStoreChannelRuntimeMetaPersistsRetentionBoundary(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	meta := ChannelRuntimeMeta{
		ChannelID:           "retention-meta",
		ChannelType:         2,
		ChannelEpoch:        3,
		LeaderEpoch:         4,
		Replicas:            []uint64{1, 2},
		ISR:                 []uint64{1, 2},
		Leader:              1,
		MinISR:              2,
		Status:              2,
		Features:            1,
		LeaseUntilMS:        1234,
		RetentionThroughSeq: 99,
		RetentionUpdatedAtMS: 5678,
	}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	got, err := shard.GetChannelRuntimeMeta(ctx, meta.ChannelID, meta.ChannelType)
	require.NoError(t, err)
	require.Equal(t, normalizeChannelRuntimeMeta(meta), got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/slot/meta -run TestShardStoreChannelRuntimeMetaPersistsRetentionBoundary -count=1`

Expected: FAIL because `ChannelRuntimeMeta` does not yet contain retention fields.

- [ ] **Step 3: Add metadata fields and catalog columns**

In `pkg/slot/meta/channel_runtime_meta.go`, add fields with English comments:

```go
// RetentionThroughSeq is the highest channel message sequence that the
// authoritative cluster metadata declares unavailable for future reads.
RetentionThroughSeq uint64
// RetentionUpdatedAtMS is the wall-clock time in milliseconds when the
// authoritative retention boundary was last advanced.
RetentionUpdatedAtMS int64
```

In `pkg/slot/meta/catalog.go`, add column IDs after `channelRuntimeMetaColumnIDLeaseUntilMS`:

```go
channelRuntimeMetaColumnIDRetentionThroughSeq uint16 = 12
channelRuntimeMetaColumnIDRetentionUpdatedAtMS uint16 = 13
```

Add `retention_through_seq` and `retention_updated_at_ms` to `ChannelRuntimeMetaTable.Columns` and the primary family column list.

- [ ] **Step 4: Encode/decode fields with zero defaults**

In `pkg/slot/meta/codec.go`:

```go
payload = appendUint64Value(payload, channelRuntimeMetaColumnIDRetentionThroughSeq, channelRuntimeMetaColumnIDLeaseUntilMS, meta.RetentionThroughSeq)
payload = appendIntValue(payload, channelRuntimeMetaColumnIDRetentionUpdatedAtMS, channelRuntimeMetaColumnIDRetentionThroughSeq, meta.RetentionUpdatedAtMS)
```

Decode both columns, but do not require `haveRetentionThroughSeq` or `haveRetentionUpdatedAtMS`; absent fields must decode as zero for old records.

- [ ] **Step 5: Preserve retention on full metadata upserts**

Update `resolveMonotonicChannelRuntimeMeta` so accepted candidates never lower retention, including candidates with higher `ChannelEpoch`:

```go
func preserveRetentionBoundary(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
	if candidate.RetentionThroughSeq < existing.RetentionThroughSeq {
		candidate.RetentionThroughSeq = existing.RetentionThroughSeq
		candidate.RetentionUpdatedAtMS = existing.RetentionUpdatedAtMS
		return
	}
	if candidate.RetentionThroughSeq == existing.RetentionThroughSeq && candidate.RetentionUpdatedAtMS < existing.RetentionUpdatedAtMS {
		candidate.RetentionUpdatedAtMS = existing.RetentionUpdatedAtMS
	}
}
```

Call it before returning every accepted candidate when `exists == true`.

- [ ] **Step 6: Write monotonic regression tests**

Add tests in `pkg/slot/meta/channel_runtime_meta_test.go`:

- `TestResolveMonotonicChannelRuntimeMetaPreservesRetentionOnSameEpoch`
- `TestResolveMonotonicChannelRuntimeMetaPreservesRetentionAcrossHigherChannelEpoch`
- `TestResolveMonotonicChannelRuntimeMetaKeepsNewestRetentionTimestampForEqualBoundary`
- `TestDecodeChannelRuntimeMetaDefaultsMissingRetentionToZero` if no existing codec fixture covers old values

Each test should construct existing/candidate metadata and assert `RetentionThroughSeq` never decreases.

- [ ] **Step 7: Run metadata tests**

Run: `go test ./pkg/slot/meta -run 'ChannelRuntimeMeta|Codec' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/meta/channel_runtime_meta.go pkg/slot/meta/catalog.go pkg/slot/meta/codec.go pkg/slot/meta/channel_runtime_meta_test.go pkg/slot/meta/codec_test.go
git commit -m "feat: persist channel retention metadata"
```

### Task 2: Add Narrow Slot Command For Retention Boundary Advancement

**Files:**
- Modify: `pkg/slot/meta/channel_runtime_meta.go`
- Modify: `pkg/slot/meta/errors.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/meta/channel_runtime_meta_test.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/fsm/command_inspection_test.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing meta-store advance tests**

Add a request type test in `pkg/slot/meta/channel_runtime_meta_test.go` that first stores metadata, then advances only retention:

```go
func TestShardStoreAdvanceChannelRetentionThroughSeqOnlyMutatesRetention(t *testing.T) {
	shard := newTestShardStore(t, 7)
	ctx := context.Background()
	base := testRuntimeMeta("retain-advance", 2)
	base.ChannelEpoch = 10
	base.LeaderEpoch = 20
	base.Leader = 1
	base.LeaseUntilMS = 3000
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, base))

	req := ChannelRetentionAdvance{
		ChannelID:            base.ChannelID,
		ChannelType:          base.ChannelType,
		ExpectedChannelEpoch: base.ChannelEpoch,
		ExpectedLeaderEpoch:  base.LeaderEpoch,
		ExpectedLeader:       base.Leader,
		ExpectedLeaseUntilMS: base.LeaseUntilMS,
		RetentionThroughSeq:  42,
		RetentionUpdatedAtMS: 4000,
	}
	require.NoError(t, shard.AdvanceChannelRetentionThroughSeq(ctx, req))
	got, err := shard.GetChannelRuntimeMeta(ctx, base.ChannelID, base.ChannelType)
	require.NoError(t, err)
	base.RetentionThroughSeq = 42
	base.RetentionUpdatedAtMS = 4000
	require.Equal(t, normalizeChannelRuntimeMeta(base), got)
}
```

Add stale-fence cases for wrong channel epoch, wrong leader epoch, wrong leader, wrong lease, and lower/equal boundary no-op.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/slot/meta -run 'AdvanceChannelRetentionThroughSeq' -count=1`

Expected: FAIL because the request/API does not exist.

- [ ] **Step 3: Implement narrow meta-store API**

In `pkg/slot/meta/channel_runtime_meta.go`, add:

```go
// ChannelRetentionAdvance describes a fenced request to advance only the
// authoritative channel retention boundary.
type ChannelRetentionAdvance struct {
	ChannelID string
	ChannelType int64
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch uint64
	ExpectedLeader uint64
	ExpectedLeaseUntilMS int64
	RetentionThroughSeq uint64
	RetentionUpdatedAtMS int64
}
```

Implement `validateChannelRetentionAdvance`, `ShardStore.AdvanceChannelRetentionThroughSeq(ctx, req) error`, and a private helper that:

- loads existing metadata;
- returns `ErrNotFound` when missing;
- rejects mismatched `ChannelEpoch`, `LeaderEpoch`, `Leader`, or `LeaseUntilMS` with a new `metadb.ErrStaleMeta`;
- no-ops when `req.RetentionThroughSeq <= existing.RetentionThroughSeq`;
- sets only `RetentionThroughSeq` and `RetentionUpdatedAtMS` on the existing metadata and writes the normal family value.

- [ ] **Step 4: Add write-batch support**

In `pkg/slot/meta/batch.go`, add `WriteBatch.AdvanceChannelRetentionThroughSeq(hashSlot uint16, req ChannelRetentionAdvance) error` with the same semantics. It must call `rememberChannelRuntimeMeta` after a successful write so later batch reads see the advanced value.

- [ ] **Step 5: Run meta tests**

Run: `go test ./pkg/slot/meta -run 'ChannelRuntimeMeta|AdvanceChannelRetentionThroughSeq' -count=1`

Expected: PASS.

- [ ] **Step 6: Write failing FSM command tests**

In `pkg/slot/fsm/state_machine_test.go`, add a test that applies `EncodeAdvanceChannelRetentionThroughSeqCommand(req)` after storing base metadata and asserts only retention changed.

In `pkg/slot/fsm/command_inspection_test.go`, add a test that inspects the command and expects fields such as `retention_through_seq` and `expected_leader_epoch`.

- [ ] **Step 7: Run FSM tests to verify they fail**

Run: `go test ./pkg/slot/fsm -run 'Retention|ChannelRuntimeMeta|CommandInspection' -count=1`

Expected: FAIL because the command type does not exist.

- [ ] **Step 8: Implement FSM encode/decode/apply**

In `pkg/slot/fsm/command.go`:

- add a new command type such as `cmdTypeAdvanceChannelRetentionThroughSeq`;
- add TLV tags for channel ID/type, expected channel epoch, expected leader epoch, expected leader, expected lease-until ms, retention through seq, and retention updated-at ms;
- implement `EncodeAdvanceChannelRetentionThroughSeqCommand(req metadb.ChannelRetentionAdvance) []byte`;
- implement `decodeAdvanceChannelRetentionThroughSeq` and register it;
- make the command apply call `wb.AdvanceChannelRetentionThroughSeq(hashSlot, req)`.

Also add the two retention TLV fields to full `EncodeUpsertChannelRuntimeMetaCommand` / `decodeUpsertChannelRuntimeMeta` so old full-upsert paths preserve fields through FSM.

- [ ] **Step 9: Update command inspection**

In `pkg/slot/fsm/command_inspection.go`, include retention fields for full runtime meta commands and the new narrow advance command.

- [ ] **Step 10: Add proxy method and integration test**

In `pkg/slot/proxy/store.go`, add:

```go
// AdvanceChannelRetentionThroughSeq proposes a fenced retention-only metadata update.
func (s *Store) AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error
```

It should compute slot/hash slot from `req.ChannelID`, encode the narrow command, and call `proposeWithHashSlot`.

In `pkg/slot/proxy/integration_test.go`, add `TestStoreAdvanceChannelRetentionThroughSeqPreservesTopology`.

- [ ] **Step 11: Run slot tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -run 'Retention|ChannelRuntimeMeta|RuntimeMeta' -count=1`

Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add pkg/slot/meta/channel_runtime_meta.go pkg/slot/meta/errors.go pkg/slot/meta/batch.go pkg/slot/meta/channel_runtime_meta_test.go pkg/slot/fsm/command.go pkg/slot/fsm/command_inspection.go pkg/slot/fsm/state_machine_test.go pkg/slot/fsm/command_inspection_test.go pkg/slot/proxy/store.go pkg/slot/proxy/integration_test.go
git commit -m "feat: add fenced channel retention metadata advance"
```

### Task 3: Project Retention Metadata Into Channel Runtime Types

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `internal/runtime/channelmeta/resolver.go`
- Modify: `internal/runtime/channelmeta/resolver_test.go`
- Modify: `pkg/channel/runtime/runtime.go`
- Modify: `pkg/channel/runtime/runtime_test.go`
- Modify: `pkg/channel/handler/meta.go`
- Modify: `pkg/channel/handler/meta_test.go`

- [ ] **Step 1: Write failing projection test**

In `internal/runtime/channelmeta/resolver_test.go`, add:

```go
func TestProjectChannelMetaIncludesRetentionBoundary(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "g1", ChannelType: 2, ChannelEpoch: 3, LeaderEpoch: 4,
		Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1,
		Status: uint8(channel.StatusActive), RetentionThroughSeq: 88,
	}
	got := ProjectChannelMeta(meta)
	require.Equal(t, uint64(88), got.RetentionThroughSeq)
}
```

- [ ] **Step 2: Run projection test to verify it fails**

Run: `go test ./internal/runtime/channelmeta -run TestProjectChannelMetaIncludesRetentionBoundary -count=1`

Expected: FAIL because `channel.Meta.RetentionThroughSeq` does not exist.

- [ ] **Step 3: Add channel type fields and helper**

In `pkg/channel/types.go`, add English-commented fields:

```go
// RetentionThroughSeq is the authoritative highest message sequence hidden by retention.
RetentionThroughSeq uint64
```

to `Meta`, and add to `ReplicaState`:

```go
// RetentionThroughSeq is the local logical retention fence applied from metadata.
RetentionThroughSeq uint64
// MinAvailableSeq is the first message sequence that reads may return.
MinAvailableSeq uint64
// LocalRetentionThroughSeq is the highest authoritative retention boundary durably adopted locally.
LocalRetentionThroughSeq uint64
// PhysicalRetentionThroughSeq is the highest message sequence physically trimmed locally.
PhysicalRetentionThroughSeq uint64
```

Add to `ChannelRuntimeStatus` and `FetchResult`:

```go
// MinAvailableSeq is the first message sequence available to clients.
MinAvailableSeq uint64
// RetentionThroughSeq is the authoritative highest retained-away sequence.
RetentionThroughSeq uint64
```

Add helper:

```go
func EffectiveMinAvailableSeq(retentionThroughSeq, logStartOffset uint64) uint64 {
	minSeq := uint64(1)
	if retentionThroughSeq+1 > minSeq {
		minSeq = retentionThroughSeq + 1
	}
	if logStartOffset+1 > minSeq {
		minSeq = logStartOffset + 1
	}
	return minSeq
}
```

- [ ] **Step 4: Map projection and runtime equality**

In `internal/runtime/channelmeta/resolver.go`, set `RetentionThroughSeq: meta.RetentionThroughSeq` in `ProjectChannelMeta`.

In `pkg/channel/runtime/runtime.go`, update `metaEqualExceptLease` so changed retention metadata is not skipped as an identical meta refresh.

- [ ] **Step 5: Publish status fields**

In `pkg/channel/handler/meta.go`, include `state.RetentionThroughSeq` and `state.MinAvailableSeq` in `ChannelRuntimeStatus`.

Add a test in `pkg/channel/handler/meta_test.go` asserting status includes the floor.

- [ ] **Step 6: Run focused tests**

Run: `go test ./pkg/channel/handler ./pkg/channel/runtime ./internal/runtime/channelmeta -run 'Retention|ProjectChannelMeta|Status' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/channel/types.go internal/runtime/channelmeta/resolver.go internal/runtime/channelmeta/resolver_test.go pkg/channel/runtime/runtime.go pkg/channel/runtime/runtime_test.go pkg/channel/handler/meta.go pkg/channel/handler/meta_test.go
git commit -m "feat: project channel retention runtime state"
```

### Task 4: Add Local Store Retention State, LEO Bounds, And Durable Cursor Confirmation

**Files:**
- Modify: `pkg/channel/store/keys.go`
- Modify: `pkg/channel/store/codec.go`
- Modify: `pkg/channel/store/message_log.go`
- Modify: `pkg/channel/store/engine.go`
- Modify: `pkg/channel/store/committed_cursor.go`
- Modify: `pkg/channel/store/committed_cursor_test.go`
- Create: `pkg/channel/store/message_retention.go`
- Create: `pkg/channel/store/message_retention_test.go`

- [ ] **Step 1: Write failing LEO retention-state test**

In `pkg/channel/store/message_retention_test.go`, add a test that writes messages 1..3, records local retention state through seq 3, deletes message rows through 3 using a test helper or future API, reopens the store, and expects `LEO() == 3`.

Expected assertion:

```go
require.Equal(t, uint64(3), reopened.ForChannel(key, id).LEO())
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/channel/store -run TestChannelStoreRetentionStatePreservesLEOAfterFullPrefixTrim -count=1`

Expected: FAIL because no retention state/log-bounds key exists and `messageTable.maxSeq()` returns 0 after full trim.

- [ ] **Step 3: Add retention state system key and codec**

In `pkg/channel/store/keys.go`, add:

```go
channelSystemIDRetentionState uint16 = 5
```

and `encodeRetentionStateKey(channelKey channel.ChannelKey) []byte`.

In `pkg/channel/store/codec.go`, add:

```go
type retentionState struct {
	LocalRetentionThroughSeq uint64
	PhysicalRetentionThroughSeq uint64
	RetainedMaxSeq uint64
}
```

with `encodeRetentionState` / `decodeRetentionState`. Keep the format small and versioned if needed.

- [ ] **Step 4: Load retention state into LEO recovery**

In `pkg/channel/store/message_retention.go`, implement:

```go
func (s *ChannelStore) LoadRetentionState() (retentionState, error)
func (s *ChannelStore) writeRetentionState(batch *pebble.Batch, state retentionState) error
func (s *ChannelStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) error
```

In `pkg/channel/store/message_log.go`, update `leoLocked()` to publish:

```go
maxSeq := max(messageTableMaxSeq, retentionState.RetainedMaxSeq)
```

In `pkg/channel/store/engine.go`, update `ListChannelKeys()` so a channel with no remaining table-state message rows is still returned when it has a retention state, checkpoint, or committed cursor system key. Add `TestEngineListChannelKeysIncludesFullyTrimmedRetainedChannel`.

`AdoptRetentionBoundary` must durably update `LocalRetentionThroughSeq`, raise `RetainedMaxSeq` to at least `throughSeq`, and durably fast-forward the named committed replay cursor to at least `throughSeq`. It must work even when the current message-table max sequence / local LEO is below `throughSeq`.

- [ ] **Step 5: Write failing durable cursor confirm tests**

In `pkg/channel/store/committed_cursor_test.go`, add:

- `TestChannelStoreConfirmCommittedDispatchCursorDurableRejectsMissingCursor`
- `TestChannelStoreConfirmCommittedDispatchCursorDurableRejectsBelowMinSeq`
- `TestChannelStoreConfirmCommittedDispatchCursorDurableSyncsVisibleCursor`
- `TestChannelStoreAdvanceCommittedDispatchCursorDurableWritesMissingCursor`

The confirm success test should first call existing `StoreCommittedDispatchCursor("committed", 10)`, then `ConfirmCommittedDispatchCursorDurable("committed", 7)`, reopen, and expect cursor 10 still exists. The durable advance test should call the new durable advance path with a missing cursor and expect the cursor to be written with `pebble.Sync`.

- [ ] **Step 6: Implement durable cursor confirm**

In `pkg/channel/store/committed_cursor.go`, keep `StoreCommittedDispatchCursor` as `pebble.NoSync` for replay hot path. Add:

```go
// ConfirmCommittedDispatchCursorDurable syncs an existing replay cursor so it
// can be used as a retention safety gate when it is at least minSeq.
func (s *ChannelStore) ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error)

// AdvanceCommittedDispatchCursorDurable durably moves a replay cursor forward
// after authoritative retention already made the skipped prefix unavailable.
func (s *ChannelStore) AdvanceCommittedDispatchCursorDurable(name string, seq uint64) error
```

`ConfirmCommittedDispatchCursorDurable` must load the cursor, reject missing cursors and cursors below `minSeq`, write the observed cursor value with `pebble.Sync`, and return the observed cursor. The worker may pass `currentRetention + 1` so a cursor behind the provisional boundary can still cap the final safe boundary. `AdvanceCommittedDispatchCursorDurable` must be monotonic, use `pebble.Sync`, and reject attempts to move an existing cursor backwards.

- [ ] **Step 7: Run store tests**

Run: `go test ./pkg/channel/store -run 'RetentionState|CommittedDispatchCursor|LEO' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channel/store/keys.go pkg/channel/store/codec.go pkg/channel/store/message_log.go pkg/channel/store/engine.go pkg/channel/store/committed_cursor.go pkg/channel/store/committed_cursor_test.go pkg/channel/store/message_retention.go pkg/channel/store/message_retention_test.go
git commit -m "feat: track local channel retention bounds"
```

### Task 5: Implement Expired Prefix Scan And Atomic Physical Trim

**Files:**
- Modify: `pkg/channel/store/message_table.go`
- Modify: `pkg/channel/store/message_retention.go`
- Modify: `pkg/channel/store/message_retention_test.go`
- Modify: `pkg/channel/store/channel_store.go` if public wrappers are clearer there

- [ ] **Step 1: Write failing expired-prefix scan tests**

In `pkg/channel/store/message_retention_test.go`, add:

```go
func TestChannelStoreScanExpiredMessagePrefixStopsAtFirstUnexpired(t *testing.T) { /* messages 1,2 expired; 3 fresh; expect through 2 */ }
func TestChannelStoreScanExpiredMessagePrefixStopsAtZeroTimestamp(t *testing.T) { /* seq 1 expired; seq 2 timestamp 0; expect through 1 */ }
func TestChannelStoreScanExpiredMessagePrefixHonorsLimit(t *testing.T) { /* many expired; limit 2; expect through 2 */ }
```

Use message `Timestamp` seconds and pass a cutoff time.

- [ ] **Step 2: Run scan tests to verify they fail**

Run: `go test ./pkg/channel/store -run 'ScanExpiredMessagePrefix' -count=1`

Expected: FAIL because scan API does not exist.

- [ ] **Step 3: Implement scan API**

In `pkg/channel/store/message_retention.go`, add:

```go
// RetentionScanResult describes the continuous expired prefix found by a scan.
type RetentionScanResult struct {
	FromSeq uint64
	ThroughSeq uint64
	Count int
}

func (s *ChannelStore) ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (RetentionScanResult, error)
```

Implementation rules:

- normalize `fromSeq` to `1` when zero;
- scan forward by primary family only;
- stop at first missing/corrupt row by returning error for corrupt rows;
- stop without expiring when `Timestamp <= 0`;
- expire only when `time.Unix(int64(row.Timestamp), 0).Before(cutoff)` or `<= cutoff`, matching the spec's `Timestamp <= cutoff` boundary;
- return the highest continuous expired `ThroughSeq`.

- [ ] **Step 4: Write failing atomic trim tests**

Add tests:

- `TestChannelStoreTrimMessagesThroughDeletesRowsPayloadAndIndexes`
- `TestChannelStoreTrimMessagesThroughIsIdempotent`
- `TestChannelStoreTrimMessagesThroughDoesNotDeleteAboveBoundary`
- `TestChannelStoreTrimMessagesThroughPreservesLEOWhenAllRowsDeleted`
- `TestChannelStoreAdoptRetentionBoundaryRaisesLEOFloorWhenLocalLogBehind`
- `TestChannelStoreAdoptRetentionBoundaryDurablyFastForwardsReplayCursor`

Verify these accessors after trim:

```go
_, ok, err := st.GetMessageBySeq(1)
require.False(t, ok)
_, ok, err = st.GetMessageByMessageID(oldMessageID)
require.False(t, ok)
_, _, ok, err = st.LookupIdempotency(channel.IdempotencyKey{
	ChannelID: id,
	FromUID: "u1",
	ClientMsgNo: "trimmed-client-1",
})
require.False(t, ok)
```

- [ ] **Step 5: Run trim tests to verify they fail**

Run: `go test ./pkg/channel/store -run 'TrimMessagesThrough|Retention' -count=1`

Expected: FAIL because trim API does not exist or indexes remain.

- [ ] **Step 6: Implement atomic trim**

In `pkg/channel/store/message_table.go`, add a helper such as:

```go
func (t *messageTable) deletePrefixThrough(writeBatch *pebble.Batch, throughSeq uint64) (deleted int, err error)
```

It must delete primary family, payload family, message ID index, client message number index, and `(from_uid, client_msg_no)` idempotency index for every row `<= throughSeq`.

In `pkg/channel/store/message_retention.go`, add:

```go
func (s *ChannelStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) error
func (s *ChannelStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64) error
```

Implementation rules:

- validate `throughSeq > 0`;
- hold `writeMu` and use a Pebble batch;
- compute `leoBefore := s.leoLocked()` before deletion;
- `AdoptRetentionBoundary` no-ops only when `throughSeq <= state.LocalRetentionThroughSeq` and cursor is already high enough;
- `AdoptRetentionBoundary` must not reject `throughSeq > leoBefore`; instead it raises `RetainedMaxSeq` to `throughSeq` so reopened `LEO()` returns at least the adopted boundary;
- `TrimMessagesThrough` deletes rows/indexes through the actual message-table max/scanned rows up to `throughSeq`; do not use retained `LEO` floor alone as proof that rows existed;
- `AdoptRetentionBoundary` updates only `LocalRetentionThroughSeq = max(existing.LocalRetentionThroughSeq, throughSeq)`, `RetainedMaxSeq = max(existing.RetainedMaxSeq, leoBefore, throughSeq)`, and the durable replay cursor; it must not advance `PhysicalRetentionThroughSeq` because it has not deleted rows or indexes;
- `TrimMessagesThrough` updates `PhysicalRetentionThroughSeq = max(existing.PhysicalRetentionThroughSeq, deletedThroughSeq)` only after the Pebble batch has successfully deleted the message rows, payloads, and indexes through the actual deleted upper bound; if no rows existed locally, do not advance physical progress just because `RetainedMaxSeq` or the LEO floor is high;
- commit each durable adoption or physical trim batch with `pebble.Sync`;
- publish LEO as `max(leoBefore, throughSeq)` after adoption success, and keep it at least that value after physical trim.

- [ ] **Step 7: Run store retention tests**

Run: `go test ./pkg/channel/store -run 'Retention|MessageTable|CommittedDispatchCursor' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/channel/store/message_table.go pkg/channel/store/message_retention.go pkg/channel/store/message_retention_test.go pkg/channel/store/channel_store.go
git commit -m "feat: trim retained channel message prefixes"
```

### Task 6: Apply Retention Boundary In Replica And Runtime

**Files:**
- Modify: `pkg/channel/replica/types.go`
- Modify: `pkg/channel/replica/commands.go`
- Create: `pkg/channel/replica/retention.go`
- Create: `pkg/channel/replica/retention_test.go`
- Modify: `pkg/channel/replica/recovery.go`
- Modify: `pkg/channel/replica/durable_store.go`
- Modify: `pkg/channel/replica/invariant.go`
- Modify: `pkg/channel/replica/FLOW.md`
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/runtime/runtime.go`
- Create: `pkg/channel/runtime/retention.go` if a separate file keeps runtime code focused
- Modify: `pkg/channel/runtime/runtime_test.go`
- Modify: `internal/runtime/channelmeta/resolver.go`
- Modify: `internal/runtime/channelmeta/resolver_test.go`
- Modify: `internal/runtime/channelmeta/FLOW.md` if resolver flow changes materially

- [ ] **Step 1: Write failing replica logical apply test**

In `pkg/channel/replica/retention_test.go`, add `TestReplicaApplyRetentionBoundaryPublishesLogicalFloorBeforePhysicalTrim`:

- create a replica with HW/CheckpointHW lower than boundary or a fake trim store that records calls;
- call `ApplyRetentionBoundary(ctx, 5)`;
- expect `Status().RetentionThroughSeq == 5` and `Status().MinAvailableSeq == 6`;
- expect physical trim is not called when physical gates are not satisfied.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/channel/replica -run TestReplicaApplyRetentionBoundaryPublishesLogicalFloorBeforePhysicalTrim -count=1`

Expected: FAIL because the replica API does not exist.

- [ ] **Step 3: Extend replica interfaces and durable store**

In `pkg/channel/types.go`, add a typed retention reset result/error, for example:

```go
// RetentionReset describes the authoritative boundary a follower must adopt
// before it can continue replication from the first available sequence.
type RetentionReset struct {
	RetentionThroughSeq uint64
	RetainedThroughOffset uint64
	MinAvailableSeq uint64
}
```

In `pkg/channel/replica/types.go`, extend `Replica`:

```go
ApplyRetentionBoundary(ctx context.Context, throughSeq uint64) error
RetentionView() (channel.RetentionView, error)
```

If `RetentionView` lives in `pkg/channel/types.go`, include leader/epoch/lease, `HW`, `CheckpointHW`, `LEO`, `CommitReady`, `RetentionThroughSeq`, `MinAvailableSeq`, `LocalRetentionThroughSeq`, `PhysicalRetentionThroughSeq`, and `MinISRMatchOffset`.

In `pkg/channel/replica/durable_store.go`, add optional combined store contract:

```go
type combinedRetentionStore interface {
	LoadRetentionState() (retentionState, error) // or exported channel/store type via adapter
	AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) error
	TrimMessagesThrough(ctx context.Context, throughSeq uint64) error
}
```

Keep package boundaries valid: if the store state type is in `pkg/channel/store`, expose only neutral methods needed by replica through an interface.

- [ ] **Step 4: Implement logical apply command**

In `pkg/channel/replica/commands.go`, add `machineApplyRetentionCommand`, `retentionTrimEffect`, and `machineRetentionTrimmedEvent`.

In `pkg/channel/replica/retention.go`, implement `ApplyRetentionBoundary` so the loop:

- treats `throughSeq == 0` as no-op;
- does not lower the logical fence when `throughSeq <= state.RetentionThroughSeq`;
- sets `state.RetentionThroughSeq = max(state.RetentionThroughSeq, throughSeq)`;
- sets `state.MinAvailableSeq = channel.EffectiveMinAvailableSeq(state.RetentionThroughSeq, state.LogStartOffset)` or equivalent so snapshots can still raise the floor;
- publishes state and notifies state-change listeners when the logical fence advanced;
- still retries physical trim for an equal boundary when `throughSeq > PhysicalRetentionThroughSeq`;
- always emits a durable adoption/reset effect when `throughSeq > state.LocalRetentionThroughSeq` or the replay cursor must be fast-forwarded; this effect is allowed when `throughSeq > LEO`;
- emits a physical trim effect only when `CommitReady == true`, `throughSeq <= CheckpointHW`, `throughSeq <= HW`, `throughSeq <= LEO`, and `throughSeq > PhysicalRetentionThroughSeq`.

- [ ] **Step 5: Implement physical trim effect**

Execute durable adoption/reset and physical trim under `durableMu`. Adoption success should set `state.LocalRetentionThroughSeq = throughSeq` and raise runtime `LEO` to at least `throughSeq` when the local log was behind, while keeping the replica out of ISR/leader eligibility until it catches up. Physical trim success should set `state.PhysicalRetentionThroughSeq` to the actually deleted boundary. Store failures must leave logical `RetentionThroughSeq` in place and allow later retry.

- [ ] **Step 6: Recover local retention state**

Extend durable recovery to load local retention state. In `recoverFromStores`, initialize:

```go
r.state.LocalRetentionThroughSeq = recovered.LocalRetentionThroughSeq
r.state.PhysicalRetentionThroughSeq = recovered.PhysicalRetentionThroughSeq
if recovered.RetainedMaxSeq > r.state.LEO { r.state.LEO = recovered.RetainedMaxSeq }
r.state.MinAvailableSeq = channel.EffectiveMinAvailableSeq(r.state.RetentionThroughSeq, r.state.LogStartOffset)
```

Do not initialize logical `RetentionThroughSeq` from local store or checkpoint `LogStartOffset`; logical retention is applied from authoritative metadata through `ApplyMeta` / `ApplyRetentionBoundary`.

- [ ] **Step 7: Implement current-ISR retention progress view**

Add loop-owned tracking for retention progress separate from quorum HW if needed. Unknown followers in the current leader epoch must not be treated as caught up. A safe rule for first implementation:

- local leader progress starts at `state.LEO`;
- followers start at `state.RetentionThroughSeq` when leadership begins;
- update follower retention progress only from observed fetch/cursor progress in the current leader epoch;
- `MinISRMatchOffset` is the minimum across all current ISR members, not the quorum candidate.

Expose this via `RetentionView()` for the worker.

- [ ] **Step 8: Add replica tests**

Add tests covering:

- repeated boundary with completed physical trim is no-op;
- repeated boundary with lagging physical trim retries the trim effect;
- lower boundary is no-op;
- physical trim runs only when `CommitReady`, `CheckpointHW`, `HW`, and `LEO` gates pass;
- physical trim failure does not lower logical floor;
- `RetentionView().MinISRMatchOffset` does not advance for unknown followers by default;
- recovery loads `LocalRetentionThroughSeq` / `PhysicalRetentionThroughSeq` and retained LEO floor without changing `LogStartOffset`;
- local `LEO < RetentionThroughSeq` applies retention reset and remains non-ISR until caught up;
- fetch/long-poll below retention floor returns the typed retention reset result, not snapshot-required, unless `LogStartOffset` is the dominating snapshot boundary.

- [ ] **Step 9: Wire runtime apply/view methods**

In `pkg/channel/runtime/types.go`, expose runtime methods:

```go
ApplyRetentionBoundary(ctx context.Context, key core.ChannelKey, throughSeq uint64) error
RetentionView(key core.ChannelKey) (core.RetentionView, error)
```

In `pkg/channel/runtime/runtime.go` or `pkg/channel/runtime/retention.go`, look up the channel and call the replica. Add tests with a fake replica.

In `pkg/channel/replica/meta.go`, update `commitMetaLocked` or the retention apply path so authoritative `meta.RetentionThroughSeq` is applied to `state.RetentionThroughSeq`/`state.MinAvailableSeq` without waiting for the background worker.

- [ ] **Step 10: Apply retention on authoritative metadata refresh**

In `internal/runtime/channelmeta/resolver.go`, `ApplyAuthoritativeMeta` already projects `RetentionThroughSeq`. Ensure local runtime application calls replica retention application through runtime meta application. If runtime `ApplyMeta` handles retention because `channel.Meta.RetentionThroughSeq` changes, add a regression test proving `ApplyAuthoritativeMeta` applies the boundary to a local replica.

- [ ] **Step 11: Update FLOW docs**

Update `pkg/channel/replica/FLOW.md` to describe:

- `RetentionThroughSeq` / `MinAvailableSeq` state ownership;
- `LocalRetentionThroughSeq` adoption/reset and `PhysicalRetentionThroughSeq` durable effects;
- retention does not modify checkpoint `LogStartOffset`.

Update `internal/runtime/channelmeta/FLOW.md` only if the authoritative apply path description becomes stale.

- [ ] **Step 12: Run runtime/replica tests**

Run: `go test ./pkg/channel/replica ./pkg/channel/runtime ./internal/runtime/channelmeta -run 'Retention|ApplyAuthoritativeMeta|Invariant|Recovery' -count=1`

Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add pkg/channel/replica/types.go pkg/channel/replica/commands.go pkg/channel/replica/retention.go pkg/channel/replica/retention_test.go pkg/channel/replica/recovery.go pkg/channel/replica/durable_store.go pkg/channel/replica/invariant.go pkg/channel/replica/meta.go pkg/channel/replica/FLOW.md pkg/channel/FLOW.md pkg/channel/runtime/types.go pkg/channel/runtime/runtime.go pkg/channel/runtime/retention.go pkg/channel/runtime/runtime_test.go internal/runtime/channelmeta/resolver.go internal/runtime/channelmeta/resolver_test.go internal/runtime/channelmeta/FLOW.md
git commit -m "feat: apply channel retention boundaries"
```

### Task 7: Clamp Replication And Client Read Paths To MinAvailableSeq

**Files:**
- Modify: `pkg/channel/types.go`
- Modify: `pkg/channel/replica/fetch_pipeline.go`
- Modify: `pkg/channel/replica/fetch_test.go`
- Modify: `pkg/channel/replica/progress_pipeline.go`
- Modify: `pkg/channel/replica/reconcile_coordinator.go`
- Modify: `pkg/channel/replica/epoch_lineage.go`
- Modify: `pkg/channel/replica/reconcile_test.go`
- Modify: `pkg/channel/replica/promotion_evaluator.go`
- Modify: `pkg/channel/replica/promotion_evaluator_test.go`
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/runtime/replicator.go`
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/runtime/session_test.go`
- Modify: `pkg/channel/runtime/lanes_test.go`
- Modify: `pkg/channel/runtime/longpoll.go`
- Modify: `pkg/channel/runtime/longpoll_test.go`
- Modify: `pkg/channel/transport/transport.go`
- Modify: `pkg/channel/transport/session.go`
- Modify: `pkg/channel/transport/adapter_test.go`
- Modify: `pkg/channel/transport/codec.go`
- Modify: `pkg/channel/transport/codec_test.go`
- Modify: `pkg/channel/transport/longpoll.go`
- Modify: `pkg/channel/transport/longpoll_codec.go`
- Modify: `pkg/channel/transport/longpoll_codec_test.go`
- Modify: `pkg/channel/transport/longpoll_session_test.go`
- Modify: `pkg/channel/handler/fetch.go`
- Modify: `pkg/channel/handler/fetch_test.go`
- Modify: `pkg/channel/handler/message_sync.go`
- Modify: `pkg/channel/handler/message_sync_test.go`
- Modify: `pkg/channel/handler/seq_read.go`
- Modify: `pkg/channel/handler/seq_read_test.go`
- Modify: `pkg/channel/handler/message_query.go`
- Modify: `pkg/channel/handler/message_query_test.go`

- [ ] **Step 1: Write failing replica fetch floor test**

In `pkg/channel/replica/fetch_test.go`, add a test with `state.RetentionThroughSeq = 5`, then call `Fetch` with `FetchOffset = 1`. Expect the typed retention reset result/error carrying `RetentionThroughSeq: 5`, `RetainedThroughOffset: 5`, and `MinAvailableSeq: 6`; never return records starting after a hole and do not use snapshot-required for a retention-only floor.

- [ ] **Step 2: Run replica fetch test to verify it fails**

Run: `go test ./pkg/channel/replica -run 'Fetch.*Retention|Retention.*Fetch' -count=1`

Expected: FAIL because fetch only checks `LogStartOffset`.

- [ ] **Step 3: Clamp replica fetch/cursor boundary**

In `pkg/channel/replica/fetch_pipeline.go`, `pkg/channel/replica/progress_pipeline.go`, and lineage/reconcile helpers, replace `fetchOffset < state.LogStartOffset` checks with a retained-through helper for replication offsets:

```go
retainedThroughOffset := state.RetentionThroughSeq
if state.LogStartOffset > retainedThroughOffset {
	retainedThroughOffset = state.LogStartOffset
}
if req.FetchOffset < retainedThroughOffset {
	return channel.RetentionResetRequired(retainedThroughOffset)
}
firstFetchSeq := retainedThroughOffset + 1
```

Use `retainedThroughOffset` for replication proof/fetch-offset comparisons and `firstFetchSeq`/`MinAvailableSeq` for message sequence reads. When `req.FetchOffset < retainedThroughOffset` and `state.RetentionThroughSeq >= state.LogStartOffset`, return the typed retention reset result so the follower can adopt the boundary and retry from `RetentionThroughSeq + 1`. Keep checkpoint/snapshot `LogStartOffset` semantics unchanged; if `LogStartOffset` dominates and snapshot payload is required, keep the existing snapshot-required path. Reconcile proof matching must also use the retained-through offset so a peer below retention cannot prove leadership from retained-away data.

- [ ] **Step 4: Add retention reset runtime and transport tests**

Add runtime tests before implementation:

- `pkg/channel/runtime/session_test.go`: a normal fetch `FetchResponseEnvelope` with `RetentionReset` causes the follower runtime to call `ApplyRetentionBoundary`, fast-forward local state/cursor through the replica/store path, and the next fetch request uses `FetchOffset == RetainedThroughOffset` / starts from `MinAvailableSeq`.
- `pkg/channel/runtime/longpoll_test.go`: `ServeLanePoll` returns a lane item with `RetentionReset` immediately when the follower cursor is below retention; the response is not parked as empty/HW-only.
- `pkg/channel/runtime/session_test.go` or `pkg/channel/runtime/lanes_test.go`: handling a lane response item with `RetentionReset` triggers the same adoption path as normal fetch and reissues from `boundary + 1`.
- `pkg/channel/transport/longpoll_session_test.go`: a decoded `LongPollItem.RetentionReset` reaches runtime `LaneResponseItem.RetentionReset` through the inbound session path, not just through direct codec helpers.

Add round-trip tests in `pkg/channel/transport/codec_test.go` and `pkg/channel/transport/longpoll_codec_test.go` proving retention reset details survive normal fetch and long-poll codecs:

```go
reset := channel.RetentionReset{RetentionThroughSeq: 5, RetainedThroughOffset: 5, MinAvailableSeq: 6}
resp := runtime.FetchResponseEnvelope{ChannelKey: "g1", Epoch: 3, RetentionReset: &reset}
```

Expected decoded response has the same reset fields and no records. Add equivalent tests for lane poll `LongPollItem` / `runtime.LaneResponseItem` with a retention-reset item flag or field.

- [ ] **Step 5: Implement retention reset production and consumption**

In `pkg/channel/runtime/types.go`, add `RetentionReset *core.RetentionReset` to `FetchResponseEnvelope` and `LaneResponseItem`. In `pkg/channel/transport/longpoll.go`, add the same fields to `LongPollItem`. Update `pkg/channel/transport/codec.go`, `pkg/channel/transport/longpoll_codec.go`, adapter conversions in `pkg/channel/transport/transport.go`, and inbound session conversion in `pkg/channel/transport/session.go` (`toRuntimeLaneResponseItems` or equivalent) so both RPC paths carry `RetentionThroughSeq`, `RetainedThroughOffset`, and `MinAvailableSeq` all the way to runtime handling.

In `pkg/channel/runtime/backpressure.go` `ServeFetch`, map a replica retention reset result into `FetchResponseEnvelope.RetentionReset` instead of returning a generic fetch error. Update `shouldLongPollFetchResponse` so a response with `RetentionReset != nil` returns immediately and never parks as an empty long-poll response.

In `pkg/channel/runtime/longpoll.go`, when `ch.replica.Fetch` returns retention reset, build a `LaneResponseItem` with `RetentionReset`, a reset/data-ready flag that is not treated as HW-only, and `finished=true` so the item is delivered immediately instead of parked.

In `pkg/channel/runtime/backpressure.go` `applyFetchResponseEnvelope` and `handleLanePollResponse`, if `RetentionReset != nil`, call the channel replica/runtime retention apply path from Task 6 before normal record apply, durable-adopt/fast-forward local cursor/state, skip treating the response as empty/HW-only, and schedule the next replication from `RetentionReset.RetainedThroughOffset` / `RetentionReset.MinAvailableSeq`. The adoption path must be idempotent and must not mark the follower ISR-ready until it catches up.

In `pkg/channel/replica/promotion_evaluator.go`, pass `max(local.LogStartOffset, local.RetentionThroughSeq)` or a `RetainedThroughOffset` helper into `reconcileProofMatchOffset` instead of `local.LogStartOffset` alone. Add `pkg/channel/replica/promotion_evaluator_test.go` coverage where `RetentionThroughSeq > LogStartOffset` and a peer proof below the retained-through offset is rejected.

- [ ] **Step 6: Write failing handler fetch test**

In `pkg/channel/handler/fetch_test.go`, add a test where runtime status has `RetentionThroughSeq: 5`, `MinAvailableSeq: 6`, `HW: 10`, and request `FromSeq: 1`. Expect store scan starts at 6, `FetchResult.MinAvailableSeq == 6`, and no message below 6 is returned.

- [ ] **Step 7: Implement handler fetch clamp**

In `pkg/channel/handler/fetch.go`, compute:

```go
minAvailableSeq := state.MinAvailableSeq
if minAvailableSeq == 0 {
	minAvailableSeq = state.LogStartOffset + 1
	if minAvailableSeq == 0 { minAvailableSeq = 1 }
}
if startSeq == 0 || startSeq < minAvailableSeq {
	startSeq = minAvailableSeq
}
```

Set `FetchResult.MinAvailableSeq` and `FetchResult.RetentionThroughSeq`.

- [ ] **Step 8: Write failing message sync tests**

In `pkg/channel/handler/message_sync_test.go`, add tests for latest, up, and down modes with `MinAvailableSeq: 6`:

- latest returns only seq `>= 6`;
- up from `StartSeq: 1` starts at 6;
- down with `EndSeq` below floor filters retained messages and sets `HasMore` correctly.

- [ ] **Step 9: Add min-available to sync request and implementation**

In `pkg/channel/handler/message_sync.go`, add `MinAvailableSeq uint64` to `SyncMessagesRequest`. Clamp all helper functions with `effectiveMinAvailableSeq(req.MinAvailableSeq)` and filter below the floor.

- [ ] **Step 10: Write failing seq read tests**

In `pkg/channel/handler/seq_read_test.go`, add tests for:

- `LoadMsgWithFloor(st, committedHW, minAvailableSeq, seq)` returns `ErrMessageNotFound` below floor;
- `LoadNextRangeMsgsWithFloor` clamps start;
- `LoadPrevRangeMsgsWithFloor` does not return below floor.

Keep existing functions as wrappers using floor `1` for compatibility.

- [ ] **Step 11: Implement min-available sequence helpers**

In `pkg/channel/handler/seq_read.go`, add floor-aware variants and have old functions call them:

```go
func LoadMsgWithFloor(st *store.ChannelStore, committedHW, minAvailableSeq, seq uint64) (channel.Message, error)
func LoadNextRangeMsgsWithFloor(st *store.ChannelStore, committedHW, minAvailableSeq, startSeq, endSeq uint64, limit int) ([]channel.Message, error)
func LoadPrevRangeMsgsWithFloor(st *store.ChannelStore, committedHW, minAvailableSeq, startSeq, endSeq uint64, limit int) ([]channel.Message, error)
```

- [ ] **Step 12: Write failing message query tests**

In `pkg/channel/handler/message_query_test.go`, add tests proving:

- `MessageID` lookup below floor returns empty even if index exists;
- `ClientMsgNo` lookup below floor filters rows and does not expose stale index hits;
- latest query below floor returns only available rows;
- pagination `NextBeforeSeq` does not point below floor.

- [ ] **Step 13: Add min-available to query request and implementation**

In `pkg/channel/handler/message_query.go`, add `MinAvailableSeq uint64` to `QueryMessagesRequest` and filter/clamp every query path.

- [ ] **Step 14: Run handler, replica, runtime, and transport tests**

Run: `go test ./pkg/channel/handler ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/transport -run 'Retention|Fetch|MessageSync|QueryMessages|SeqRead|LongPoll|Promotion' -count=1`

Expected: PASS.

- [ ] **Step 15: Commit**

```bash
git add pkg/channel/types.go pkg/channel/replica/fetch_pipeline.go pkg/channel/replica/progress_pipeline.go pkg/channel/replica/reconcile_coordinator.go pkg/channel/replica/epoch_lineage.go pkg/channel/replica/fetch_test.go pkg/channel/replica/reconcile_test.go pkg/channel/replica/promotion_evaluator.go pkg/channel/replica/promotion_evaluator_test.go pkg/channel/runtime/types.go pkg/channel/runtime/backpressure.go pkg/channel/runtime/session_test.go pkg/channel/runtime/lanes_test.go pkg/channel/runtime/replicator.go pkg/channel/runtime/longpoll.go pkg/channel/runtime/longpoll_test.go pkg/channel/transport/transport.go pkg/channel/transport/session.go pkg/channel/transport/adapter_test.go pkg/channel/transport/codec.go pkg/channel/transport/codec_test.go pkg/channel/transport/longpoll.go pkg/channel/transport/longpoll_codec.go pkg/channel/transport/longpoll_codec_test.go pkg/channel/transport/longpoll_session_test.go pkg/channel/handler/fetch.go pkg/channel/handler/fetch_test.go pkg/channel/handler/message_sync.go pkg/channel/handler/message_sync_test.go pkg/channel/handler/seq_read.go pkg/channel/handler/seq_read_test.go pkg/channel/handler/message_query.go pkg/channel/handler/message_query_test.go
git commit -m "feat: clamp channel reads to retention floor"
```

### Task 8: Make Committed Replay Retention-Aware

**Files:**
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/committed_replay_test.go`
- Modify: `pkg/channel/store/committed_cursor.go` if Task 4 did not expose the needed durable confirm method to app adapters

- [ ] **Step 1: Write failing replay floor test**

In `internal/app/committed_replay_test.go`, add `TestCommittedReplayStartsAtMinAvailableSeq` with a fake log where cursor is 2, min available is 6, committed seq is 8, and messages exist from 6..8. Expect replay submits seq 6 first and does not request seq 3.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestCommittedReplayStartsAtMinAvailableSeq -count=1`

Expected: FAIL because replay starts from `cursor + 1`.

- [ ] **Step 3: Add replay state to interface**

Change `committedReplayLog` so the replayer can read committed seq and min available together:

```go
type committedReplayChannelState struct {
	CommittedSeq uint64
	MinAvailableSeq uint64
}
```

Add `CommittedReplayState(ctx, key, id)` or extend the existing `CommittedSeq` path. Update fakes and `channelStoreCommittedReplayLog` to source `MinAvailableSeq` from `channelLog.Status(id)`.

- [ ] **Step 4: Clamp replay start and durable-skip retained prefix**

In `replayChannel`, compute:

```go
startSeq := cursor + 1
if state.MinAvailableSeq > startSeq {
	startSeq = state.MinAvailableSeq
}
```

If `cursor + 1 < state.MinAvailableSeq`, advance the cursor to `state.MinAvailableSeq - 1` with `AdvanceCommittedDispatchCursorDurable` from Task 4 before loading messages. This records that retained messages are intentionally skipped because the authoritative boundary already guaranteed their side effects were replayed before retention advanced.

- [ ] **Step 5: Add cursor-missing boundary test**

Add a test where cursor is missing but `MinAvailableSeq > 1`. Expect replay durably stores `MinAvailableSeq - 1`, starts at `MinAvailableSeq`, and continues without reading below the floor.

- [ ] **Step 6: Run committed replay tests**

Run: `go test ./internal/app -run 'CommittedReplay|Retention' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/committed_replay.go internal/app/committed_replay_test.go pkg/channel/store/committed_cursor.go
git commit -m "feat: make committed replay honor retention floor"
```

### Task 9: Implement Retention Worker Core

**Files:**
- Create: `internal/runtime/channelretention/worker.go`
- Create: `internal/runtime/channelretention/types.go`
- Create: `internal/runtime/channelretention/worker_test.go`
- Create: `internal/runtime/channelretention/boundary.go` if boundary calculation is clearer separately
- Create: `internal/runtime/channelretention/boundary_test.go`

- [ ] **Step 1: Write safe-boundary unit tests**

In `internal/runtime/channelretention/boundary_test.go`, add table tests for the two-phase calculation:

```go
candidateThroughSeq = min(expiredThroughSeq, leaderCheckpointHW, leaderCommittedHW, minISRMatchOffset)
safeThroughSeq = min(candidateThroughSeq, durableCommittedReplayCursor)
```

Cases must cover each provisional gate blocking advancement, durable cursor capping the final boundary, `candidateThroughSeq <= currentRetention` returning no-op without cursor confirmation, and `safeThroughSeq <= currentRetention` returning no-op after cursor confirmation.

- [ ] **Step 2: Run boundary tests to verify they fail**

Run: `go test ./internal/runtime/channelretention -run TestSafeRetentionBoundary -count=1`

Expected: FAIL because package does not exist.

- [ ] **Step 3: Create worker types and boundary calculator**

In `internal/runtime/channelretention/types.go`, define focused interfaces:

```go
type ChannelLister interface { ListChannelKeys(context.Context) ([]core.ChannelKey, error) }
type Runtime interface { RetentionView(context.Context, core.ChannelKey) (RetentionView, error); ApplyRetentionBoundary(context.Context, core.ChannelKey, uint64) error }
type StoreProvider interface { StoreForChannel(context.Context, core.ChannelKey) (Store, error) }
type Store interface { ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (ScanResult, error); ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error) }
type MetadataStore interface { AdvanceChannelRetentionThroughSeq(context.Context, metadb.ChannelRetentionAdvance) error }
```

`StoreProvider` is keyed because one worker pass iterates many channels; the `internal/app` adapter owns mapping `core.ChannelKey` to the concrete channel log store. Keep the runtime package independent of `internal/app`.

- [ ] **Step 4: Implement boundary calculator**

In `boundary.go`, add a pure function returning `(throughSeq uint64, reason BlockedReason)` so tests can assert exact blocked reasons.

- [ ] **Step 5: Write worker orchestration tests**

In `worker_test.go`, add tests proving:

- worker skips non-leader channels;
- worker skips when no expired prefix exists;
- worker blocks when the current leader cursor is missing or cannot advance beyond current retention;
- worker calls durable leader cursor confirm before final boundary calculation and metadata advance;
- worker caps the final boundary at a confirmed leader cursor that is behind the provisional boundary but ahead of current retention;
- follower-local cursor lag does not block advancement under the documented leader-authoritative replay model, but followers must fast-forward when applying the boundary;
- worker uses expected channel epoch, leader epoch, leader, and lease fence in `ChannelRetentionAdvance`;
- worker applies local boundary only after metadata advance succeeds; if immediate local apply fails, it records the error and relies on authoritative metadata refresh plus the next worker pass to retry, because slot metadata is the source of truth.

- [ ] **Step 6: Implement worker RunOnce**

In `worker.go`, implement:

```go
func (w *Worker) RunOnce(ctx context.Context) error
```

Flow per channel:

1. iterate the `core.ChannelKey` returned by `ChannelLister`;
2. read runtime retention view for that key;
3. require local current leader, active status, valid lease, `CommitReady=true`;
4. call `StoreProvider.StoreForChannel(ctx, key)` and scan that channel store's expired prefix from `view.MinAvailableSeq` with `MaxTrimMessages`;
5. compute `candidateThroughSeq = min(expiredThroughSeq, CheckpointHW, HW, MinISRMatchOffset)` without replay cursor;
6. if `candidateThroughSeq <= view.RetentionThroughSeq`, record no-op and skip cursor confirmation;
7. durable-confirm the current leader committed replay cursor on the keyed store with `minSeq = view.RetentionThroughSeq + 1`, returning `durableCommittedReplayCursor`;
8. compute `safeThroughSeq = min(candidateThroughSeq, durableCommittedReplayCursor)` and skip if it is not above current retention;
9. refresh/revalidate leader lease freshness if the scan/cursor-confirm path took non-trivial time, then call `MetadataStore.AdvanceChannelRetentionThroughSeq` with expected fences and `RetentionThroughSeq = safeThroughSeq`;
10. call runtime local `ApplyRetentionBoundary` after metadata commit as an immediate best-effort apply; record failures for retry because authoritative metadata refresh remains the durable source of truth;
11. record blocked reason and continue to next channel.

- [ ] **Step 7: Add background lifecycle methods**

Implement `Start(ctx)` / `Stop(ctx)` with a ticker controlled by `ScanInterval`. `Start` must be idempotent and `Stop` must wait for the goroutine.

- [ ] **Step 8: Run worker tests**

Run: `go test ./internal/runtime/channelretention -count=1`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/runtime/channelretention
git commit -m "feat: add channel retention worker"
```

### Task 10: Wire Config, App Adapters, And Lifecycle

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Create: `internal/app/channelretention.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write failing app config defaults test**

In `internal/app/config_test.go`, add `TestConfigDefaultsChannelMessageRetentionDisabled`:

```go
require.Zero(t, cfg.ChannelMessageRetention.TTL)
require.Equal(t, time.Hour, cfg.ChannelMessageRetention.ScanInterval)
require.Equal(t, 128, cfg.ChannelMessageRetention.ChannelBatchSize)
require.Equal(t, 10000, cfg.ChannelMessageRetention.MaxTrimMessages)
```

- [ ] **Step 2: Run app config test to verify it fails**

Run: `go test ./internal/app -run TestConfigDefaultsChannelMessageRetentionDisabled -count=1`

Expected: FAIL because config does not exist.

- [ ] **Step 3: Add app config struct and validation**

In `internal/app/config.go`, add:

```go
// ChannelMessageRetentionConfig controls cluster-authoritative channel message retention.
type ChannelMessageRetentionConfig struct {
	// TTL is the duration to retain channel messages before leader-driven retention may hide them. Zero disables retention.
	TTL time.Duration
	// ScanInterval is the interval between retention worker passes when TTL is enabled.
	ScanInterval time.Duration
	// ChannelBatchSize limits how many local channel logs one retention pass scans.
	ChannelBatchSize int
	// MaxTrimMessages limits how many expired messages one channel may trim in one pass.
	MaxTrimMessages int
}
```

Add it to `Config` and defaults/validation:

- `TTL == 0` disables worker;
- explicit negative TTL invalid;
- when TTL enabled, scan interval and sizes must be positive;
- default scan interval `1h`, channel batch size `128`, max trim messages `10000`.

- [ ] **Step 4: Write failing cmd config parse test**

In `cmd/wukongim/config_test.go`, add `TestLoadConfigParsesChannelMessageRetention` with:

```text
WK_CHANNEL_MESSAGE_RETENTION_TTL=168h
WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL=30m
WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE=64
WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES=5000
```

Assert parsed values.

- [ ] **Step 5: Parse config keys**

In `cmd/wukongim/config.go`, parse the four keys using existing `parseDuration` and `parseInt`, set config fields, and add explicit flags if needed for distinguishing zero from absent.

- [ ] **Step 6: Write failing lifecycle order test**

In `internal/app/lifecycle_test.go`, add a test expecting order:

```text
cluster -> managed_slots_ready -> channelmeta -> presence -> conversation_projector -> delivery_runtime -> committed_dispatcher -> committed_replay -> channel_retention -> gateway -> api -> manager
```

Stop order is reverse, so retention stops before committed replay and before channel log shutdown.

- [ ] **Step 7: Add app adapter and lifecycle component**

In `internal/app/channelretention.go`, adapt worker interfaces to existing dependencies:

- list channel keys from `channelLogDB.ListChannelKeys()` after Task 4 makes it include retention/system-only channels;
- implement `StoreProvider.StoreForChannel(ctx, key)` by opening per-channel `ChannelStore` from `channelLogDB.ForChannel`;
- read runtime retention view/apply from `app.channelLog` or `app.isrRuntime` facade;
- confirm leader cursor through `ChannelStore.ConfirmCommittedDispatchCursorDurable` before slot metadata advance;
- advance metadata through `slotStore.AdvanceChannelRetentionThroughSeq` only after the durable leader cursor gate succeeds;
- fast-forward local/follower cursors during boundary adoption through `ChannelStore.AdoptRetentionBoundary` after the authoritative boundary is committed/applied.

In `internal/app/build.go`, construct the worker only when TTL is enabled.

In `internal/app/app.go`, add worker field and optional start/stop test hooks.

In `internal/app/lifecycle_components.go`, add `appLifecycleChannelRetention = "channel_retention"` after committed replay and before gateway.

- [ ] **Step 8: Document config example**

Update `wukongim.conf.example` with English comments for all four keys and default disabled TTL.

- [ ] **Step 9: Run app/cmd tests**

Run: `go test ./internal/app ./cmd/wukongim -run 'Retention|Lifecycle|Config' -count=1`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/app/config.go internal/app/config_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go internal/app/app.go internal/app/build.go internal/app/channelretention.go internal/app/lifecycle_components.go internal/app/lifecycle_test.go wukongim.conf.example
git commit -m "feat: wire channel message retention worker"
```

### Task 11: Make Manager And Node Message Queries Retention-Aware

**Files:**
- Modify: `internal/app/manager_messages.go`
- Modify: `internal/app/manager_messages_test.go`
- Modify: `internal/access/node/channel_messages_rpc.go`
- Modify: `internal/access/node/channel_messages_rpc_test.go` if present, otherwise add coverage to the closest node adapter test file
- Modify: `internal/usecase/management/messages.go` only if an existing response contract must explicitly expose `MinAvailableSeq`
- Modify: `internal/usecase/message/sync.go` only if an existing response contract must explicitly expose `MinAvailableSeq`

- [ ] **Step 1: Write failing manager query tests**

In `internal/app/manager_messages_test.go`, add tests where authoritative metadata has `RetentionThroughSeq: 5` and local store still contains messages 1..8:

- `QueryMessages` by `MessageID` for seq 3 returns empty;
- `QueryMessages` latest returns only seq `>= 6`;
- `SyncMessages` with `StartSeq: 1` returns from seq 6;
- `MaxMessageSeq` still reports committed max, not min available.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run 'Manager.*Retention|Retention.*Manager' -count=1`

Expected: FAIL because manager passes only committed HW.

- [ ] **Step 3: Pass min-available floor into handler requests**

In `internal/app/manager_messages.go`, compute min available from authoritative metadata or runtime status:

```go
minAvailableSeq := channel.EffectiveMinAvailableSeq(meta.RetentionThroughSeq, 0)
```

Pass it to `channelhandler.QueryMessagesRequest` and `SyncMessagesRequest`. Do not add new public DTO fields in this task unless a failing test proves an endpoint must expose `MinAvailableSeq`; the required behavior is filtering/clamping retained messages.

- [ ] **Step 4: Write failing node RPC query tests**

Add or update tests for `internal/access/node/channel_messages_rpc.go` so remote manager reads also pass `MinAvailableSeq` to handler queries and do not expose retained messages.

- [ ] **Step 5: Update node RPC adapter**

After `refreshMessageQueryMeta`, compute min available from `meta.RetentionThroughSeq` and pass it to `QueryMessagesRequest` / `SyncMessagesRequest`. Only populate `MinAvailableSeq` in response DTOs if the implementation deliberately adds that field to an existing contract.

- [ ] **Step 6: Run access/app tests**

Run: `go test ./internal/app ./internal/access/node -run 'Retention|ChannelMessages|ManagerMessages' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/manager_messages.go internal/app/manager_messages_test.go internal/access/node/channel_messages_rpc.go internal/access/node/channel_messages_rpc_test.go internal/usecase/management/messages.go internal/usecase/message/sync.go
git commit -m "feat: filter manager message queries by retention floor"
```

### Task 12: End-To-End Verification, Docs, And Knowledge Record

**Files:**
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `pkg/channel/replica/FLOW.md` if not already updated
- Modify: `internal/runtime/channelmeta/FLOW.md` if not already updated
- Add or modify: focused integration tests in `internal/app/*_test.go` where practical without long sleeps

- [ ] **Step 1: Add concise project knowledge**

Add one short bullet to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Channel message retention is cluster-authoritative: leaders advance slot metadata `RetentionThroughSeq`; local stores may lag physically and must not use checkpoint `LogStartOffset` for retention.
```

Keep the document concise.

- [ ] **Step 2: Add single-node cluster retention integration test**

In the most appropriate `internal/app` test file, add a deterministic test that avoids real TTL sleeps by using a fake clock or direct worker `RunOnce`:

- start a single-node cluster app or app-level harness;
- append messages with old timestamps and a fresh message;
- ensure committed replay cursor is durable/confirmed;
- run retention once;
- assert metadata `RetentionThroughSeq` advanced only through expired prefix;
- assert reads below `MinAvailableSeq` return empty and new messages remain readable.

- [ ] **Step 3: Add follower-lag consistency test if cheap**

Add a unit-level worker/replica test rather than a slow integration test:

- leader view has expired prefix through 10;
- follower ISR progress is unknown/current-retention only;
- worker does not advance beyond current retention;
- after observed follower progress reaches 10, worker advances to 10;
- an old non-ISR replica with local LEO below boundary applies retention reset, fetches from boundary + 1, and remains outside ISR until caught up.

- [ ] **Step 4: Run focused package tests**

Run:

```bash
go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./pkg/channel/store ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/handler ./internal/runtime/channelretention ./internal/runtime/channelmeta ./internal/app ./internal/access/node ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full unit suite if focused tests pass**

Run: `go test ./...`

Expected: PASS. If this is too slow or fails in unrelated packages, capture the exact failures and run the affected focused tests before reporting.

- [ ] **Step 6: Check formatting and docs drift**

Run:

```bash
gofmt -w pkg/slot/meta pkg/slot/fsm pkg/slot/proxy pkg/channel/store pkg/channel/replica pkg/channel/runtime pkg/channel/handler internal/runtime/channelretention internal/runtime/channelmeta internal/app internal/access/node cmd/wukongim
git diff --check
```

Expected: no formatting or whitespace errors.

- [ ] **Step 7: Commit final docs/tests if any remain**

```bash
git add docs/development/PROJECT_KNOWLEDGE.md pkg/channel/replica/FLOW.md pkg/channel/FLOW.md internal/runtime/channelmeta/FLOW.md internal/FLOW.md internal/app
git commit -m "test: verify channel message retention flow"
```
