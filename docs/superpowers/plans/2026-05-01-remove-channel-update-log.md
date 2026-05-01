# Remove ChannelUpdateLog Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the persistent `ChannelUpdateLog` conversation projection and replace it with a best-effort, cached `ActiveAt` hint path backed by Channel Log facts.

**Architecture:** Conversation sync becomes working-set based: it reads `UserConversationState.active_at` plus hot UID-owner active hints, then loads latest messages from Channel Log for ordering and unread calculation. Active hints are produced asynchronously from committed messages, routed to the UID owner cache, flushed to `UserConversationState` in throttled batches, and dropped when necessary without affecting message durability.

**Tech Stack:** Go, Pebble-backed metadb, slot FSM TLV commands, slot proxy RPC, `internal/usecase/conversation`, `internal/app` composition, `go test`.

---

## Scope Notes

This is a cross-layer change. Keep each task independently testable and commit after each task. Do not remove old `ChannelUpdateLog` files until all read/write paths have moved away from them and tests prove they are unused.

The implementation must preserve these accepted semantics:

- `/conversation/sync` no longer uses request `version` for full historical incremental discovery.
- `ActiveAt` is a best-effort working-set hint and may be dropped.
- `TouchUserConversationActiveAt` remains an upsert: missing user conversation state is created.
- Delete hides messages through `DeletedToSeq`, but a later message with `MessageSeq > DeletedToSeq` reactivates the conversation.
- `ListUserConversationActive` must merge persisted active rows with UID-owner cache hints.

## File Map

- Modify: `pkg/slot/meta/user_conversation_state.go`
  - Add `UserConversationActiveHint`, `UserConversationDeleteBarrier`, and `MessageSeq` on active patches.
  - Skip stale active touches when `MessageSeq <= DeletedToSeq`.
- Modify: `pkg/slot/meta/batch.go`
  - Apply the same stale-touch protection in batch writes.
- Modify: `pkg/slot/fsm/command.go`
  - Add an optional TLV tag for active patch `MessageSeq`.
  - Add a dedicated hide/delete user conversation command that can clear `ActiveAt` while setting `DeletedToSeq`.
- Modify: `pkg/slot/fsm/command_inspection.go`
  - Include `message_seq` in touch command inspection payload.
- Modify: `pkg/slot/meta/user_conversation_state_test.go`
  - Test active touch upsert and stale-delete protection.
- Modify: `pkg/slot/fsm/command_inspection_test.go`
  - Test touch inspection includes `message_seq`.
- Modify: `pkg/slot/proxy/store.go`
  - Add user active overlay registration and public best-effort active hint routing methods.
- Modify: `pkg/slot/proxy/user_conversation_state_rpc.go`
  - Merge active overlay into local and remote authoritative active listing.
  - Add RPC operations to apply hot hints and delete barriers on UID owner without Raft propose.
  - Add a durable hide/delete user conversation RPC path for delete semantics.
- Modify: `pkg/slot/proxy/integration_test.go`
  - Test local/remote active overlay merge and hot hint routing.
- Create: `internal/usecase/conversation/active_hint_cache.go`
  - UID-owner cache, delete barriers, dedupe, bounded reads, batch flush, start/stop.
- Create: `internal/usecase/conversation/active_hint_cache_test.go`
  - Unit tests for merge, delete barrier, upsert, TTL/capacity behavior.
- Modify: `internal/usecase/conversation/deps.go`
  - Replace channel update dependencies with active hint dependencies.
- Modify: `internal/usecase/conversation/projector.go`
  - Stop writing `ChannelUpdateLog`; produce active hints only.
- Modify: `internal/usecase/conversation/projector_test.go`
  - Replace channel-update flush tests with active-hint tests.
- Modify: `internal/usecase/conversation/sync.go`
  - Remove `ChannelUpdateLog` and positive-version full scan logic.
  - Build candidates from active working set plus client-known conversations.
- Modify: `internal/usecase/conversation/sync_test.go`
  - Rewrite version-positive tests to assert no full directory/channel-update scan.
- Modify: `internal/app/build.go`
  - Construct active hint cache, register it with store, wire projector to active hint routing.
- Modify: `internal/app/app.go`, `internal/app/lifecycle_components.go`, `internal/app/lifecycle.go`
  - Add lifecycle for active hint cache if it has background flush workers.
- Modify: `internal/app/committed_replay.go`
  - Stop flushing conversation projection before committed replay cursor advancement.
- Modify: `internal/app/committed_replay_test.go`
  - Update cursor tests for non-blocking active hints.
- Modify: `internal/app/deliveryrouting.go`
  - Remove fallback immediate conversation flush behavior or make it a no-op for active hints.
- Modify: `internal/app/deliveryrouting_test.go`
  - Update overflow/fallback tests.
- Modify: `pkg/slot/proxy/channel_update_log_rpc.go`, `pkg/slot/meta/channel_update_log.go`, related tests
  - Delete or deprecate after all usages are gone.
- Modify: `pkg/slot/FLOW.md`, `internal/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md`
  - Document the new working-set sync and deprecated table/commands.

---

### Task 1: Add MessageSeq-Aware ActiveAt Touch Semantics

**Files:**
- Modify: `pkg/slot/meta/user_conversation_state.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Test: `pkg/slot/meta/user_conversation_state_test.go`
- Test: `pkg/slot/fsm/command_inspection_test.go`

- [ ] **Step 1: Write failing metadb tests for stale active touch protection**

Add tests that prove an old hint cannot reactivate a deleted conversation, but a newer hint can:

```go
func TestShardTouchUserConversationActiveAtSkipsDeletedMessageSeq(t *testing.T) {
    ctx := context.Background()
    shard := openConversationTestShard(t)

    require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
        UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0,
    }))

    require.NoError(t, shard.TouchUserConversationActiveAt(ctx, UserConversationActivePatch{
        UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 10,
    }))
    got, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
    require.NoError(t, err)
    require.Equal(t, int64(0), got.ActiveAt)

    require.NoError(t, shard.TouchUserConversationActiveAt(ctx, UserConversationActivePatch{
        UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, MessageSeq: 11,
    }))
    got, err = shard.GetUserConversationState(ctx, "u1", "g1", 2)
    require.NoError(t, err)
    require.Equal(t, int64(200), got.ActiveAt)
}
```

Also add the same case for `WriteBatch.TouchUserConversationActiveAt`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./pkg/slot/meta -run 'TestShardTouchUserConversationActiveAtSkipsDeletedMessageSeq|TestWriteBatchTouchUserConversationActiveAtSkipsDeletedMessageSeq' -count=1`

Expected: FAIL because `UserConversationActivePatch.MessageSeq` and patch-based API do not exist yet.

- [ ] **Step 3: Change `UserConversationActivePatch` and add hint/barrier types**

In `pkg/slot/meta/user_conversation_state.go`, change the patch type and add exported types with English comments:

```go
// UserConversationActivePatch advances a user's recent conversation hint.
// MessageSeq is optional for legacy callers; when set, stale touches at or below DeletedToSeq are ignored.
type UserConversationActivePatch struct {
    UID         string
    ChannelID   string
    ChannelType int64
    ActiveAt    int64
    MessageSeq  uint64
}

// UserConversationActiveHint is an in-memory, best-effort recent conversation hint.
type UserConversationActiveHint struct {
    UID         string
    ChannelID   string
    ChannelType int64
    ActiveAt    int64
    MessageSeq  uint64
}

// UserConversationDeleteBarrier prevents delayed old active hints from reviving deleted conversations.
type UserConversationDeleteBarrier struct {
    UID          string
    ChannelID    string
    ChannelType  int64
    DeletedToSeq uint64
}
```

Change `ShardStore.TouchUserConversationActiveAt` to accept `UserConversationActivePatch` instead of separate fields. Keep a helper if needed for old tests:

```go
func (s *ShardStore) TouchUserConversationActiveAt(ctx context.Context, patch UserConversationActivePatch) error {
    // validate patch fields
    // load or create state
    if patch.MessageSeq > 0 && patch.MessageSeq <= current.DeletedToSeq {
        return nil
    }
    if patch.ActiveAt <= current.ActiveAt {
        return nil
    }
    // write state and active index
}
```

- [ ] **Step 4: Update batch touch logic**

In `pkg/slot/meta/batch.go`, before advancing `ActiveAt`:

```go
if patch.MessageSeq > 0 && patch.MessageSeq <= current.DeletedToSeq {
    continue
}
```

Preserve upsert behavior when the state does not exist.

- [ ] **Step 5: Add optional MessageSeq TLV support**

In `pkg/slot/fsm/command.go`:

```go
tagUserConversationActivePatchEntryMessageSeq uint8 = 5
```

Encode it only when non-zero or always encode it; decoder must tolerate missing tags:

```go
buf = appendUint64TLVField(buf, tagUserConversationActivePatchEntryMessageSeq, patch.MessageSeq)
```

Decoder should not require `MessageSeq` for old commands.

- [ ] **Step 6: Update command inspection**

Include `message_seq` in the `touch_user_conversation_active_at` inspection payload. Add/adjust a test in `pkg/slot/fsm/command_inspection_test.go`.

- [ ] **Step 7: Run focused tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm -run 'ConversationActive|UserConversationActive|TouchUserConversationActive|CommandInspection' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/meta/user_conversation_state.go pkg/slot/meta/batch.go pkg/slot/fsm/command.go pkg/slot/fsm/command_inspection.go pkg/slot/meta/user_conversation_state_test.go pkg/slot/fsm/command_inspection_test.go
git commit -m "feat(conversation): guard active touches by message sequence"
```

---

### Task 2: Build the UID-Owner Active Hint Cache

**Files:**
- Create: `internal/usecase/conversation/active_hint_cache.go`
- Create: `internal/usecase/conversation/active_hint_cache_test.go`
- Modify: `internal/usecase/conversation/deps.go`

- [ ] **Step 1: Write failing cache tests**

Cover these cases:

```go
func TestActiveHintCacheDedupesByMessageSeqAndActiveAt(t *testing.T) { /* newer seq wins */ }
func TestActiveHintCacheDeleteBarrierDropsOldHintsButAcceptsNewerHints(t *testing.T) { /* seq <= deleted skipped; seq > deleted accepted */ }
func TestActiveHintCacheListSynthesizesHotHintsInActiveOrder(t *testing.T) { /* ListHotUserConversationActive returns top limit */ }
func TestActiveHintCacheFlushBatchesAndKeepsUpsertSemantics(t *testing.T) { /* store receives max hints */ }
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/usecase/conversation -run 'TestActiveHintCache' -count=1`

Expected: FAIL because cache does not exist.

- [ ] **Step 3: Implement cache options and store dependency**

In `active_hint_cache.go`:

```go
type ActiveHintStore interface {
    TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
}

type ActiveHintCacheOptions struct {
    Store         ActiveHintStore
    FlushInterval time.Duration
    HintTTL        time.Duration
    BarrierTTL     time.Duration
    MaxHints       int
    MaxHintsPerUID int
    FlushBatchSize int
    Now            func() time.Time
    Logger         wklog.Logger
}
```

Use defaults when options are zero. Add English comments for exported types and important fields.

- [ ] **Step 4: Implement public methods**

```go
func NewActiveHintCache(opts ActiveHintCacheOptions) *ActiveHintCache
func (c *ActiveHintCache) Start() error
func (c *ActiveHintCache) Stop() error
func (c *ActiveHintCache) SubmitHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
func (c *ActiveHintCache) RemoveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
func (c *ActiveHintCache) ListHotUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationActiveHint, error)
func (c *ActiveHintCache) Flush(ctx context.Context) error
```

`SubmitHints` must drop hints blocked by in-memory delete barriers. `RemoveHints` must remove pending hints and install/update barriers.

- [ ] **Step 5: Implement bounded in-memory state**

Use a simple implementation first:

```go
type activeHintKey struct { uid string; channelID string; channelType int64 }
type activeHintEntry struct { hint metadb.UserConversationActiveHint; touchedAt time.Time }
type deleteBarrierEntry struct { barrier metadb.UserConversationDeleteBarrier; expiresAt time.Time }
```

Protect maps with a mutex. Enforce global and per-UID caps by dropping the lowest `ActiveAt` entries when over capacity. Keep this simple and testable; no heap until profiling proves it is needed.

- [ ] **Step 6: Implement flush**

`Flush` snapshots hot hints, converts them to `UserConversationActivePatch`, calls the store in batches, and removes flushed entries only if no newer hint replaced them while flushing.

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/usecase/conversation -run 'TestActiveHintCache' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/conversation/active_hint_cache.go internal/usecase/conversation/active_hint_cache_test.go internal/usecase/conversation/deps.go
git commit -m "feat(conversation): add active hint cache"
```

---

### Task 3: Merge Hot Active Hints in Slot Proxy Active Listing

**Files:**
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/user_conversation_state_rpc.go`
- Test: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing proxy tests**

Add tests for:

```go
func TestStoreListUserConversationActiveMergesLocalOverlay(t *testing.T) { /* DB row + hot row sorted by max ActiveAt */ }
func TestStoreListUserConversationActiveOverlayCanSynthesizeMissingState(t *testing.T) { /* hot-only row appears */ }
func TestStoreListUserConversationActiveOverlaySkipsInactiveDeletedState(t *testing.T) { /* DB has ActiveAt=0 + DeletedToSeq; hot seq <= DeletedToSeq ignored */ }
func TestStoreListUserConversationActiveOverlayReactivatesInactiveDeletedStateForNewerSeq(t *testing.T) { /* DB has ActiveAt=0 + DeletedToSeq; hot seq > DeletedToSeq appears */ }
func TestStoreListUserConversationActiveRemoteOwnerMergesOverlay(t *testing.T) { /* non-owner RPC returns UID-owner hot hints */ }
func TestStoreSubmitUserConversationActiveHintsRoutesToUIDOwnerOverlay(t *testing.T) { /* remote owner cache receives hint */ }
func TestStoreRemoveUserConversationActiveHintsRoutesDeleteBarrierToUIDOwnerOverlay(t *testing.T) { /* remote owner receives barrier */ }
```

Use a small recording overlay stub in the test file.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./pkg/slot/proxy -run 'TestStoreListUserConversationActive.*Overlay|TestStoreSubmitUserConversationActiveHints|TestStoreRemoveUserConversationActiveHints' -count=1`

Expected: FAIL because overlay registration and RPC operations do not exist.

- [ ] **Step 3: Add overlay interface and registration**

In `pkg/slot/proxy/store.go`:

```go
type UserConversationActiveOverlay interface {
    ListHotUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationActiveHint, error)
    SubmitHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
    RemoveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
}

func (s *Store) RegisterUserConversationActiveOverlay(overlay UserConversationActiveOverlay) { ... }
```

Do not reuse `ChannelUpdateOverlay`.

- [ ] **Step 4: Add best-effort routing methods**

In `pkg/slot/proxy/user_conversation_state_rpc.go`, add public methods used by the projector:

```go
func (s *Store) SubmitUserConversationActiveHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
func (s *Store) RemoveUserConversationActiveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
```

Group by UID owner slot/hash slot. If local, call overlay directly. If remote, call user conversation state RPC with new ops. These methods must not propose to Raft.

- [ ] **Step 5: Add RPC ops**

Extend `userConversationStateRPCRequest` with `Hints []metadb.UserConversationActiveHint` and `Barriers []metadb.UserConversationDeleteBarrier`. Add op constants such as:

```go
const (
    userConversationStateRPCHotHint = "hot_hint"
    userConversationStateRPCRemoveHotHint = "remove_hot_hint"
)
```

The handler should call the registered overlay on the authoritative UID owner. If no overlay is registered, return OK so active hints remain best-effort.

- [ ] **Step 6: Merge overlay in authoritative list**

Change `listUserConversationActiveAuthoritative` and the `list_active` branch in `handleUserConversationStateRPC` to call the same local merge helper. Do not let the RPC handler call `s.db.ForHashSlot(...).ListUserConversationActive` directly, or non-owner API requests will miss UID-owner hot hints.


```go
func (s *Store) listUserConversationActiveLocal(ctx context.Context, hashSlot uint16, uid string, limit int) ([]metadb.UserConversationState, error) {
    persisted, err := s.db.ForHashSlot(hashSlot).ListUserConversationActive(ctx, uid, expandedLimit(limit))
    if err != nil { return nil, err }
    var hot []metadb.UserConversationActiveHint
    if s.userConversationActiveOverlay != nil {
        hot, err = s.userConversationActiveOverlay.ListHotUserConversationActive(ctx, uid, expandedLimit(limit))
        if err != nil { hot = nil } // overlay is best-effort
    }
    inactiveStates, err := s.loadInactiveStatesForHotHints(ctx, hashSlot, uid, persisted, hot)
    if err != nil { return nil, err }
    return mergeUserConversationActive(persisted, inactiveStates, hot, limit), nil
}
```

Before synthesizing hot-only states, point-read existing `UserConversationState` rows for hot hint keys that were not returned by the active index. This is required because deleted/inactive rows have `ActiveAt=0` and will not appear in `ListUserConversationActive`, but their `DeletedToSeq` must still block stale hints.

`mergeUserConversationActive` must preserve persisted `ReadSeq`, `DeletedToSeq`, `UpdatedAt`, skip hot hints with `MessageSeq <= DeletedToSeq`, synthesize only when the point read returns `metadb.ErrNotFound`, sort by `ActiveAt desc`, and truncate.

- [ ] **Step 7: Run focused proxy tests**

Run: `go test ./pkg/slot/proxy -run 'TestStoreListUserConversationActive|TestStoreSubmitUserConversationActiveHints|TestStoreRemoveUserConversationActiveHints' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/proxy/store.go pkg/slot/proxy/user_conversation_state_rpc.go pkg/slot/proxy/integration_test.go
git commit -m "feat(conversation): merge active hints in user active listing"
```

---

### Task 4: Rewrite Conversation Projector to Produce Active Hints

**Files:**
- Modify: `internal/usecase/conversation/deps.go`
- Modify: `internal/usecase/conversation/projector.go`
- Modify: `internal/usecase/conversation/projector_test.go`

- [ ] **Step 1: Write failing projector tests for hint production**

Replace old channel update log expectations with tests like:

```go
func TestProjectorSubmitsPersonActiveHints(t *testing.T) { /* two UIDs receive MessageSeq-aware hints */ }
func TestProjectorSubmitCommittedDoesNotBlockOnSlowActiveHintRouting(t *testing.T) { /* bounded enqueue/drop, no inline RPC */ }
func TestProjectorDoesNotFlushChannelUpdateLogs(t *testing.T) { /* no UpsertChannelUpdateLogs dependency */ }
func TestProjectorThrottlesGroupActiveFanout(t *testing.T) { /* same group fanout interval dedupes */ }
func TestProjectorDropsGroupFanoutWhenSubscriberBudgetExceeded(t *testing.T) { /* no send failure */ }
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/usecase/conversation -run 'TestProjector.*ActiveHint|TestProjector.*ChannelUpdate|TestProjector.*Group' -count=1`

Expected: FAIL under old projector behavior.

- [ ] **Step 3: Change projector store dependency**

Update `ProjectorStore` to remove channel update methods and add active hint routing:

```go
type ProjectorStore interface {
    SubmitUserConversationActiveHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
    ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}
```

If group fanout is delayed to a later task, keep `ListChannelSubscribers` but guard all group work behind config. Add projector options for a bounded active hint queue and async runner so `SubmitCommitted` never performs remote RPC or subscriber scanning inline.

- [ ] **Step 4: Implement person hint production**

For person messages, decode channel ID and enqueue two hints into the projector's bounded active hint queue:

```go
hints := []metadb.UserConversationActiveHint{
    {UID: left, ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAt, MessageSeq: msg.MessageSeq},
    {UID: right, ChannelID: msg.ChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAt, MessageSeq: msg.MessageSeq},
}
p.enqueueActiveHints(hints)
```

`enqueueActiveHints` must use a non-blocking send to a bounded queue or protected pending map. If the queue is full, drop and record/log; return nil. A background worker drains the queue and calls `store.SubmitUserConversationActiveHints`. Return nil on best-effort failures after logging/metrics; do not fail committed dispatch.

- [ ] **Step 5: Implement group throttling**

Add projector options:

```go
GroupActiveFanoutInterval time.Duration
GroupActiveFanoutMaxSubscribers int
```

Track last fanout per channel in memory inside the async worker. If too soon, skip. If subscriber count exceeds threshold or queue/budget is exhausted, stop and drop remaining hints. Use `MessageSeq` in every hint. Subscriber scanning must not happen inside `SubmitCommitted`.

- [ ] **Step 6: Remove channel update flush state**

Delete `hot`, `dirty`, `BatchGetHotChannelUpdates`, `UpsertChannelUpdateLogs` usage, and cold channel update lookups from the projector. Keep lifecycle only if group workers need it; otherwise `Start`/`Stop` can be no-op or only manage periodic work.

- [ ] **Step 7: Run focused conversation projector tests**

Run: `go test ./internal/usecase/conversation -run 'TestProjector' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/conversation/deps.go internal/usecase/conversation/projector.go internal/usecase/conversation/projector_test.go
git commit -m "feat(conversation): project active hints instead of channel updates"
```

---

### Task 5: Remove ChannelUpdateLog from Conversation Sync

**Files:**
- Modify: `internal/usecase/conversation/app.go`
- Modify: `internal/usecase/conversation/deps.go`
- Modify: `internal/usecase/conversation/sync.go`
- Modify: `internal/usecase/conversation/sync_test.go`
- Modify: `internal/usecase/conversation/types.go` if comments need clarification

- [ ] **Step 1: Write failing sync tests for working-set-only version semantics**

Replace positive-version tests with new accepted behavior:

```go
func TestSyncPositiveVersionDoesNotScanFullUserDirectory(t *testing.T) { /* repo.scan calls remain 0 */ }
func TestSyncUsesActiveSetAndClientKnownConversations(t *testing.T) { /* active + last_msg_seqs only */ }
func TestSyncDeletedConversationReappearsForNewerLatestMessage(t *testing.T) { /* latest seq > DeletedToSeq visible */ }
func TestSyncDeletedConversationHiddenWhenLatestAtDeleteBarrier(t *testing.T) { /* latest seq <= DeletedToSeq hidden */ }
```

Update the stub to fail if `BatchGetChannelUpdateLogs` is called.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/usecase/conversation -run 'TestSync' -count=1`

Expected: FAIL because old code scans channel updates for positive versions.

- [ ] **Step 3: Remove channel update dependency**

In `deps.go`, remove `ChannelUpdateStore`. In `app.go`, remove the `channelUpdate` field and option.

- [ ] **Step 4: Simplify candidate collection**

In `sync.go`, make `Sync` do only:

```text
active states -> candidates
client last_msg_seqs -> candidates
facts.LoadLatestMessages(candidate keys)
build views
sort by latest message timestamp/display order
limit
load recents
```

Remove `collectIncrementalViews`, `buildIncrementalCandidate`, cold demotion logic based on `ChannelUpdateLog`, and any `a.channelUpdate.BatchGetChannelUpdateLogs` calls.

- [ ] **Step 5: Keep delete and unread semantics**

`buildSyncConversationView` already filters `latest.MessageSeq <= DeletedToSeq`. Keep that behavior. Ensure `assignConversationRecents` still filters recents at or below `DeletedToSeq`.

- [ ] **Step 6: Clarify version comments**

Update comments for `SyncQuery.Version` and `SyncConversation.Version`: request `Version` is legacy compatibility and no longer a full historical cursor.

- [ ] **Step 7: Run focused sync tests**

Run: `go test ./internal/usecase/conversation -run 'TestSync|TestClearUnread|TestSetUnread' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/conversation/app.go internal/usecase/conversation/deps.go internal/usecase/conversation/sync.go internal/usecase/conversation/sync_test.go internal/usecase/conversation/types.go
git commit -m "feat(conversation): make sync working-set based"
```

---

### Task 6: Wire Active Hint Cache in App Composition and Lifecycle

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write failing config and lifecycle tests**

Add tests for active hint config parsing and lifecycle start/stop ordering:

```go
func TestLoadConfigParsesConversationActiveHintTuning(t *testing.T) { /* WK_CONVERSATION_ACTIVE_HINT_* */ }
func TestStartStopIncludesConversationActiveHintCache(t *testing.T) { /* lifecycle includes cache before projector */ }
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/wukongim ./internal/app -run 'ConversationActiveHint|StartStopIncludesConversation' -count=1`

Expected: FAIL because config/lifecycle fields are missing.

- [ ] **Step 3: Add config fields**

In `internal/app/config.go`, add English comments:

```go
// ActiveHintFlushInterval is the background interval for flushing best-effort active hints.
ActiveHintFlushInterval time.Duration
// ActiveHintTTL controls how long unflushed hot active hints remain visible in memory.
ActiveHintTTL time.Duration
// ActiveHintBarrierTTL controls how long delete barriers block stale delayed hints in memory.
ActiveHintBarrierTTL time.Duration
// ActiveHintMaxHints limits total hot active hints held in memory.
ActiveHintMaxHints int
// ActiveHintMaxHintsPerUID limits hot active hints per UID.
ActiveHintMaxHintsPerUID int
// ActiveHintFlushBatchSize limits active hints written per flush batch.
ActiveHintFlushBatchSize int
// GroupActiveFanoutInterval throttles subscriber fanout for group active hints.
GroupActiveFanoutInterval time.Duration
// GroupActiveFanoutMaxSubscribers caps subscriber fanout for large groups.
GroupActiveFanoutMaxSubscribers int
```

Defaults should be conservative, for example `2s`, `30m`, `30m`, `100000`, `1000`, `1024`, `5m`, `0` where `0` disables large group fanout until explicitly enabled.

- [ ] **Step 4: Parse `WK_` keys**

In `cmd/wukongim/config.go`, parse keys such as:

```text
WK_CONVERSATION_ACTIVE_HINT_FLUSH_INTERVAL
WK_CONVERSATION_ACTIVE_HINT_TTL
WK_CONVERSATION_ACTIVE_HINT_BARRIER_TTL
WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS
WK_CONVERSATION_ACTIVE_HINT_MAX_HINTS_PER_UID
WK_CONVERSATION_ACTIVE_HINT_FLUSH_BATCH_SIZE
WK_CONVERSATION_GROUP_ACTIVE_FANOUT_INTERVAL
WK_CONVERSATION_GROUP_ACTIVE_FANOUT_MAX_SUBSCRIBERS
```

Update `wukongim.conf.example` because config changed.

- [ ] **Step 5: Wire app build**

In `internal/app/build.go`:

```go
app.conversationActiveHints = conversationusecase.NewActiveHintCache(conversationusecase.ActiveHintCacheOptions{
    Store: app.store,
    FlushInterval: cfg.Conversation.ActiveHintFlushInterval,
    HintTTL: cfg.Conversation.ActiveHintTTL,
    BarrierTTL: cfg.Conversation.ActiveHintBarrierTTL,
    MaxHints: cfg.Conversation.ActiveHintMaxHints,
    MaxHintsPerUID: cfg.Conversation.ActiveHintMaxHintsPerUID,
    FlushBatchSize: cfg.Conversation.ActiveHintFlushBatchSize,
    Logger: app.logger.Named("conversation.active_hint"),
})
app.store.RegisterUserConversationActiveOverlay(app.conversationActiveHints)
```

Pass group fanout options to `NewProjector`.

- [ ] **Step 6: Add lifecycle**

Start active hint cache before conversation projector and stop it after projector so final flush can run. Use existing lifecycle component patterns.

- [ ] **Step 7: Run focused tests**

Run: `go test ./cmd/wukongim ./internal/app -run 'ConversationActiveHint|ConversationProjector|Lifecycle' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/lifecycle_components.go internal/app/lifecycle_test.go internal/app/config.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example
git commit -m "feat(conversation): wire active hint cache"
```

---

### Task 7: Make Committed Replay and Fallback Non-Blocking for Active Hints

**Files:**
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/committed_replay_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`

- [ ] **Step 1: Write failing replay/fallback tests**

Update tests to assert replay cursor advancement does not depend on conversation flush:

```go
func TestCommittedReplayerAdvancesCursorWithoutConversationFlush(t *testing.T) { /* active hints are best-effort */ }
func TestCommittedReplayerWithProjectorAdvancesCursorWhenActiveHintStoreBlocks(t *testing.T) { /* projector enqueue is non-blocking */ }
func TestCommittedDispatchQueueOverflowFallbackDoesNotFlushConversation(t *testing.T) { /* no per-message flush */ }
```

Remove or rewrite old tests that expected `Conversation.Flush` before cursor advancement.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/app -run 'CommittedReplayer|CommittedDispatchQueueOverflowFallback' -count=1`

Expected: FAIL under old flush-required behavior.

- [ ] **Step 3: Remove replay flush gate**

In `committed_replay.go`, remove the `Conversation.Flush(ctx)` call before `StoreCommittedDispatchCursor`. Keep delivery/conversation `SubmitCommitted` calls.

- [ ] **Step 4: Remove fallback immediate flush**

In `deliveryrouting.go`, change `submitConversationFallback` so it only submits the message to conversation projector. Do not type-assert and call `Flush`.

- [ ] **Step 5: Run focused app tests**

Run: `go test ./internal/app -run 'CommittedReplayer|CommittedDispatch|AsyncCommittedDispatcher' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/committed_replay.go internal/app/committed_replay_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go
git commit -m "feat(conversation): make active hints non-blocking"
```

---

### Task 8: Deprecate and Remove ChannelUpdateLog Read/Write Paths

**Files:**
- Modify/Delete: `pkg/slot/meta/channel_update_log.go`
- Modify/Delete: `pkg/slot/meta/channel_update_log_test.go`
- Modify/Delete: `pkg/slot/proxy/channel_update_log_rpc.go`
- Modify: `pkg/slot/proxy/integration_test.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/FLOW.md`

- [ ] **Step 1: Search for remaining ChannelUpdateLog usages**

Run: `rg -n "ChannelUpdateLog|channel_update_log|UpsertChannelUpdateLogs|BatchGetChannelUpdateLogs|DeleteChannelUpdateLogs|channelUpdateOverlay"`

Expected: only old storage/RPC/FSM files and tests remain.

- [ ] **Step 2: Remove store overlay and RPC registration**

Delete `ChannelUpdateOverlay` from `pkg/slot/proxy/store.go` and stop registering `channelUpdateLogRPCServiceID` in `New`.

- [ ] **Step 3: Deprecate command IDs without reuse**

In `pkg/slot/fsm/command.go`, keep numeric constants reserved but remove encode/decode use from production. Add comments:

```go
// Deprecated: reserved for old ChannelUpdateLog projection. Do not reuse.
```

Keep old command IDs reserved. Prefer retaining decode/apply compatibility as deprecated no-ops or legacy-compatible handlers until a clear log-compaction/compatibility window has passed; ensure no caller can create new commands.

- [ ] **Step 4: Remove or mark meta table as deprecated**

Prefer deleting `channel_update_log.go` only after compile proves no production path imports it. If table descriptors are part of snapshot compatibility, keep the table ID descriptor with a deprecated comment and no public write path.

- [ ] **Step 5: Update tests**

Remove `TestStoreBatchGetChannelUpdateLogsGroupsByChannelSlot`, `TestApplyBatchUpsertChannelUpdateLogs`, and `channel_update_log_test.go` if APIs are deleted. If decode compatibility remains, keep only command decode/inspection tests.

- [ ] **Step 6: Update FLOW docs**

In `pkg/slot/FLOW.md`, remove `ChannelUpdateLog` as an active business table or mark it deprecated/reserved. Note commands 13/14 are reserved and unused.

- [ ] **Step 7: Run package tests**

Run: `go test ./pkg/slot/... ./internal/usecase/conversation/... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot pkg/slot/FLOW.md internal/usecase/conversation
git commit -m "refactor(conversation): remove channel update log projection"
```

---

### Task 9: Add Durable DeleteConversation and Cache Invalidation Hook

**Files:**
- Modify: `pkg/slot/meta/user_conversation_state.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/proxy/user_conversation_state_rpc.go`
- Test: `pkg/slot/meta/user_conversation_state_test.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/slot/proxy/integration_test.go`
- Create: `internal/usecase/conversation/delete.go`
- Modify: `internal/usecase/conversation/types.go`
- Modify: `internal/usecase/conversation/deps.go`
- Test: `internal/usecase/conversation/delete_test.go`
- Modify: `internal/access/api/server.go` if API exposure is added
- Modify: `internal/access/api/conversation_sync.go` or create `internal/access/api/conversation_delete.go` if API exposure is added
- Modify: `internal/access/api/routes.go` if API exposure is added
- Test: `internal/access/api/server_test.go` if API exposure is added

- [ ] **Step 1: Write failing storage tests for atomic hide/delete**

Add tests for a dedicated storage operation, not `UpsertUserConversationStates`:

```go
func TestShardHideUserConversationSetsDeletedSeqAndClearsActiveAt(t *testing.T) { /* ActiveAt index removed, DeletedToSeq set */ }
func TestWriteBatchHideUserConversationClearsActiveAtDespiteMonotonicUpsert(t *testing.T) { /* proves not using max ActiveAt upsert */ }
func TestApplyBatchHideUserConversation(t *testing.T) { /* FSM command applies atomically */ }
```

These tests must start with an existing state whose `ActiveAt > 0`, call the hide/delete operation with `DeletedToSeq=N`, and assert `ListUserConversationActive` no longer returns the row.

- [ ] **Step 2: Run storage tests to verify failure**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm -run 'HideUserConversation|DeleteUserConversation' -count=1`

Expected: FAIL because the operation and command do not exist.

- [ ] **Step 3: Add metadb delete request type and write path**

In `pkg/slot/meta/user_conversation_state.go`:

```go
// UserConversationDelete hides a user conversation through DeletedToSeq and clears its active hint.
type UserConversationDelete struct {
    UID          string
    ChannelID    string
    ChannelType  int64
    DeletedToSeq uint64
    UpdatedAt    int64
}
```

Add `ShardStore.HideUserConversation(ctx, req UserConversationDelete) error` and `WriteBatch.HideUserConversation(hashSlot uint16, req UserConversationDelete) error`. This operation must:

- create the state if it does not exist and `DeletedToSeq > 0`;
- set `DeletedToSeq = max(existing.DeletedToSeq, req.DeletedToSeq)`;
- set `ActiveAt = 0` even when existing `ActiveAt` is higher;
- set `UpdatedAt = max(existing.UpdatedAt, req.UpdatedAt)` or the provided `UpdatedAt` if greater;
- delete the old active index entry when existing `ActiveAt > 0`;
- write the primary state in the same batch.

Do not change general `UpsertUserConversationStates` monotonic `ActiveAt` behavior.

- [ ] **Step 4: Add FSM command and proxy method**

In `pkg/slot/fsm/command.go`, add a new command ID such as `cmdTypeHideUserConversations uint8 = 16` and TLV encode/decode helpers for `[]metadb.UserConversationDelete`. Do not reuse deprecated `ChannelUpdateLog` command IDs.

In `pkg/slot/proxy/user_conversation_state_rpc.go`, add:

```go
func (s *Store) HideUserConversations(ctx context.Context, reqs []metadb.UserConversationDelete) error
```

Route by UID owner slot/hash slot and propose the new FSM command. Add an RPC op for remote owner writes.

- [ ] **Step 5: Run storage/proxy tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -run 'HideUserConversation|DeleteUserConversation' -count=1`

Expected: PASS.

- [ ] **Step 6: Write failing usecase tests for durable delete and cache invalidation**

Create `internal/usecase/conversation/delete_test.go`:

```go
func TestDeleteConversationHidesStateClearsActiveAtAndRemovesHotHint(t *testing.T) { /* durable hide first, then barrier */ }
func TestDeleteConversationUsesLatestMessageSeqWhenCommandSeqIsZero(t *testing.T) { /* latest facts fallback */ }
func TestDeleteConversationAllowsNewerMessageReactivation(t *testing.T) { /* hint seq > DeletedToSeq appears through active listing */ }
```

The first test should assert `HideUserConversations` receives `DeletedToSeq=N`, `ActiveAt` is not updated through monotonic upsert, and `RemoveUserConversationActiveHints` receives a barrier with `DeletedToSeq=N` after the durable call succeeds.

- [ ] **Step 7: Add command type and dependencies**

In `types.go`:

```go
// DeleteConversationCommand hides a conversation through MessageSeq for one user.
type DeleteConversationCommand struct {
    UID         string
    ChannelID   string
    ChannelType uint8
    // MessageSeq is the delete barrier. When zero, the usecase loads the latest channel sequence.
    MessageSeq uint64
}
```

In `deps.go`, add focused interfaces rather than reusing monotonic upsert:

```go
type ConversationDeleteStore interface {
    HideUserConversations(ctx context.Context, reqs []metadb.UserConversationDelete) error
    RemoveUserConversationActiveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
}
```

Add this dependency to `Options` and `App` as `Deletes ConversationDeleteStore`. In `New`, default it to `opts.States` only when the provided store implements `ConversationDeleteStore`; otherwise leave it nil and make `DeleteConversation` return a clear "conversation delete store not configured" error.

- [ ] **Step 8: Implement `DeleteConversation`**

Create `delete.go`. The usecase must load latest seq when `cmd.MessageSeq == 0`, call durable `HideUserConversations` first, then best-effort `RemoveUserConversationActiveHints`:

```go
func (a *App) DeleteConversation(ctx context.Context, cmd DeleteConversationCommand) error {
    key := conversationKey(cmd.ChannelID, cmd.ChannelType)
    deleteSeq := cmd.MessageSeq
    if deleteSeq == 0 {
        latestSeq, ok, err := a.latestConversationSeq(ctx, key, 0)
        if err != nil { return err }
        if ok { deleteSeq = latestSeq }
    }
    req := metadb.UserConversationDelete{UID: cmd.UID, ChannelID: key.ChannelID, ChannelType: int64(key.ChannelType), DeletedToSeq: deleteSeq, UpdatedAt: a.now().UnixNano()}
    if err := a.deletes.HideUserConversations(ctx, []metadb.UserConversationDelete{req}); err != nil { return err }
    _ = a.deletes.RemoveUserConversationActiveHints(context.Background(), []metadb.UserConversationDeleteBarrier{{UID: cmd.UID, ChannelID: key.ChannelID, ChannelType: int64(key.ChannelType), DeletedToSeq: deleteSeq}})
    return nil
}
```

Use a separate `a.deletes` dependency on `App` if needed; do not call `UpsertUserConversationStates` for delete.

- [ ] **Step 9: Add API adapter if conversation delete is exposed**

Add `DeleteConversation` to the API conversation usecase interface in `internal/access/api/server.go` and expose a route only if this API compatibility surface is desired now. If added, use a legacy-compatible route such as `POST /conversations/delete` and map request fields to `DeleteConversationCommand`.

Do not block the core usecase on API availability; the required invariant is in the usecase, storage operation, and active hint cache.

- [ ] **Step 10: Preserve reactivation semantics in tests**

Ensure combined usecase/proxy tests cover:

```text
hint seq <= DeletedToSeq -> not listed
hint seq > DeletedToSeq -> listed again
```

- [ ] **Step 11: Run focused tests**

Run: `go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/usecase/conversation ./internal/access/api -run 'HideUserConversation|DeleteConversation|ConversationDelete|ActiveHint|ConversationActive' -count=1`

Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add pkg/slot/meta pkg/slot/fsm pkg/slot/proxy internal/usecase/conversation internal/access/api
git commit -m "feat(conversation): delete conversations with active hint barriers"
```


### Task 10: Update App Integration and Stress Tests

**Files:**
- Modify: `internal/app/conversation_sync_integration_test.go`
- Modify: `internal/app/conversation_sync_stress_test.go`
- Modify: `internal/app/send_stress_test.go` if assumptions mention channel update logs
- Modify: `docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md`
- Modify: `docs/superpowers/specs/2026-04-07-conversation-sync-design.md` if it remains a live spec reference
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Update integration expectations**

Adjust tests so they wait for active hints/cache visibility rather than persisted `ChannelUpdateLog` flush. Add a test for hot-cache visibility before flush if possible. Add an app-level scenario for "delete conversation, then newer message reappears" to cover durable delete, cache barrier, projector hint, and sync wiring together.

- [ ] **Step 2: Remove cutover requirement for ChannelUpdateLog**

Update runbook language that currently says new messages must write `ChannelUpdateLog`. Replace with active hint cache and Channel Log facts checks.

- [ ] **Step 3: Update architecture docs**

Update `internal/FLOW.md` conversation sections:

```text
conversation_projector -> active hint cache -> best-effort batch touch active_at
/conversation/sync -> active working set + client known -> Channel Log facts
```

- [ ] **Step 4: Run focused integration-ish unit tests**

Run: `go test ./internal/app -run 'ConversationSync|CommittedReplay|CommittedDispatch' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md docs/superpowers/specs/2026-04-07-conversation-sync-design.md internal/FLOW.md
git commit -m "docs(conversation): document working-set sync"
```

---

### Task 11: Final Verification

**Files:**
- No planned source edits unless verification finds issues.

- [ ] **Step 1: Search for forbidden production usages**

Run:

```bash
rg -n "ChannelUpdateLog|BatchGetHotChannelUpdates|UpsertChannelUpdateLogs|BatchGetChannelUpdateLogs|DeleteChannelUpdateLogs|RegisterChannelUpdateOverlay|channelUpdateOverlay|upsert_channel_update_logs" internal pkg cmd
```

Expected: no production read/write path remains. Deprecated tests may mention reserved IDs only.

- [ ] **Step 2: Run targeted tests**

Run:

```bash
go test ./internal/usecase/conversation ./internal/app ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader unit tests if time allows**

Run: `go test ./internal/... ./pkg/...`

Expected: PASS. Do not run integration-tag tests unless explicitly requested.

- [ ] **Step 4: Update project knowledge if implementation revealed new rules**

If new operational constraints were discovered, add short bullets to `docs/development/PROJECT_KNOWLEDGE.md`.

- [ ] **Step 5: Commit verification fixes if any**

```bash
git status --short
git add <changed-files>
git commit -m "test(conversation): verify active hint sync" # only if fixes/docs changed
```

---

## Handoff Notes

- Use @superpowers:subagent-driven-development for implementation because the user allowed subagents.
- Split workers by disjoint write sets when possible: metadb/FSM, proxy overlay/RPC, conversation usecase, app wiring/docs.
- Do not let any worker remove `ChannelUpdateLog` storage before sync/projector/app paths no longer compile against it.
- Keep `ActiveAt` best-effort: no sendack, delivery, or committed replay cursor may depend on active hint flush success.
