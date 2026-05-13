# Send CMD Conversation Intent P2d-d Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace P2d-c's post-commit CMD subscriber scan with a legacy-compatible CMD conversation intent path that uses already-resolved recipients, UID-owner pending buffers, and `/message/sync` pending overlays.

**Architecture:** Add `cmdsync.ConversationIntent` and an owner-local pending updater in `internal/usecase/cmdsync`; route intents to UID owners in `internal/app`; expose a thin node RPC for remote owner submission; and observe delivery-resolved UID pages before presence expansion. Request-scoped durable sends submit an intent directly from `RequestSubscribers`, while ordinary CMD channels submit intents from delivery tag/local resolver UID pages.

**Tech Stack:** Go, `testing` + `github.com/stretchr/testify/require`, existing `pkg/slot/meta` CMD state store, existing `internal/access/node` binary RPC helpers, app delivery routing, WuKongIM lifecycle components, `GOWORK=off go test`.

---

## Spec Reference

- Approved design spec: `docs/superpowers/specs/2026-05-13-send-cmd-conversation-intent-p2d-d-design.md`
- Prior P2d-c implementation plan: `docs/superpowers/plans/2026-05-12-send-cmd-conversation-offline-sync-p2d-c.md`
- Business diff tracker to update after implementation: `docs/raw/send-path-business-logic-diff.md`
- Flow docs to inspect/update when code flow changes: `internal/FLOW.md`, `pkg/slot/FLOW.md`

## Scope

Implement P2d-d only:

- Build CMD conversation update intents from already-resolved UIDs.
- Add an owner-local pending CMD conversation updater with file restore on graceful stop/start.
- Merge pending CMD conversations into `/message/sync` candidate selection.
- Clean pending UID/channel entries on `/message/syncack`.
- Route intents to UID owners before inserting them into pending buffers.
- Replace P2d-c `cmdsync.Projector` subscriber-scan writes with request-recipient and delivery UID-page intent writes.
- Keep ordinary `/conversation/sync` isolated from CMD pending/state.

Do not implement:

- Subscriber snapshot persistence.
- Channel-log format changes.
- kill -9 intent durability.
- `/message/sendbatch`, `expire`, `AllowStranger`, plugin/webhook/AI behavior.
- Any local shortcut that bypasses cluster/UID-owner semantics. A single-node deployment remains a single-node cluster.

## Current Code Map

- `internal/usecase/cmdsync/app.go`: `/message/sync` and `/message/syncack` business rules. Currently lists persisted `CMDConversationState` only.
- `internal/usecase/cmdsync/projector.go`: P2d-c projector that pages subscribers after commit and writes `CMDConversationState` directly. P2d-d must remove this from app fanout/replay or refactor it so it no longer scans subscribers.
- `internal/app/cmdsync.go`: UID-owner routing for `/message/sync` APIs and command-channel message facts adapter.
- `internal/app/build.go`: currently wires `cmdsync.NewProjector` into committed fanout and replay. P2d-d must wire a pending updater/router/observer instead.
- `internal/app/deliveryrouting.go`: local/tag delivery resolvers already see UID pages before presence expansion. This is the correct hook for ordinary CMD intents.
- `internal/contracts/messageevents/events.go`: committed event contract passed from message usecase into app committed fanout.
- `internal/runtime/delivery/types.go`: runtime committed envelope carried into resolver and node delivery submit RPCs.
- `internal/access/node/cmdsync_rpc.go` and `internal/access/node/cmdsync_codec.go`: existing UID-owner CMD sync/syncack RPC. Extend this service with a push-intent operation.
- `pkg/slot/meta` and `pkg/slot/proxy`: dedicated `CMDConversationState` already exists from P2d-c; do not redesign storage.

## File Structure

### New Files

- `internal/usecase/cmdsync/intent.go`: `ConversationIntent`, `PendingConversationUpdate`, `PendingConversationView`, validation, and `BuildConversationIntent`.
- `internal/usecase/cmdsync/intent_test.go`: intent builder tests.
- `internal/usecase/cmdsync/pending.go`: owner-local pending updater, sharding, coalescing, list overlay, flush, ack cleanup.
- `internal/usecase/cmdsync/pending_file.go`: JSON file save/load using temp-file rename and `.bad` decode handling.
- `internal/usecase/cmdsync/pending_test.go`: pending updater tests.
- `internal/app/cmdsync_intent.go`: UID-owner router and delivery UID observer implementation.
- `internal/app/cmdsync_intent_test.go`: router and observer tests.
- `internal/access/node/cmdsync_intent_test.go`: node RPC codec/handler/client tests for push-intent.

### Modified Files

- `internal/usecase/cmdsync/types.go`: extend store interfaces and define errors/interfaces for pending and intent routing.
- `internal/usecase/cmdsync/app.go`: merge pending overlays into `Sync`; call pending cleanup from `SyncAck`.
- `internal/usecase/cmdsync/app_test.go`: add sync overlay and ack cleanup tests.
- `internal/usecase/cmdsync/projector.go`: delete the old projector file after moving reusable helpers into `intent.go`.
- `internal/usecase/cmdsync/projector_test.go`: delete obsolete subscriber-scan projector tests; coverage moves to intent, pending, app routing, and delivery observer tests.
- `internal/contracts/messageevents/events.go`: add `CMDConversationIntentSubmitted bool` and clone coverage.
- `internal/contracts/messageevents/events_test.go`: verify clone preserves the new flag.
- `internal/runtime/delivery/types.go`: add `CMDConversationIntentSubmitted bool` to `CommittedEnvelope`.
- `internal/access/node/delivery_submit_codec.go`: encode/decode the new envelope flag for remote committed delivery submit.
- `internal/access/node/delivery_submit_rpc_test.go`: verify remote submit preserves the new flag.
- `internal/usecase/delivery/submit.go`: copy the new flag between `MessageCommitted` and runtime envelopes.
- `internal/app/deliveryrouting.go`: propagate the flag, add UID-page observer hooks to local/tag resolvers, and ensure observer failures are non-fatal.
- `internal/app/deliveryrouting_test.go`: add UID observer, duplicate suppression, offline UID, and non-fatal observer tests.
- `internal/usecase/message/app.go`: add a `CMDConversationIntentSink` option/field.
- `internal/usecase/message/send.go`: submit request-scoped CMD intent after append and set `CMDConversationIntentSubmitted` only on full owner-route success.
- `internal/usecase/message/send_test.go`: add request-scoped direct intent and failure/flag tests.
- `internal/access/node/options.go`: extend `CMDSyncUsecase` or add `CMDConversationIntentSink`; wire adapter field.
- `internal/access/node/cmdsync_rpc.go`: add `cmdSyncOpPushIntent`, handler branch, and client method.
- `internal/access/node/cmdsync_codec.go`: encode/decode `cmdsync.ConversationIntent` in the existing CMD sync RPC request.
- `internal/access/node/service_ids.go`: no new ID if reusing `cmdSyncRPCServiceID`; update comments if needed.
- `internal/app/app.go`: replace `cmdSyncProjector` fields/lifecycle flags with `cmdConversationUpdater`, `cmdConversationIntentRouter`, and test hooks.
- `internal/app/build.go`: instantiate pending updater/router/observer, wire message sink, node RPC, delivery resolver observers, committed replay, and remove `cmdSyncProjector` from committed fanout.
- `internal/app/lifecycle_components.go`: replace CMD projector lifecycle component with CMD pending updater lifecycle.
- `internal/app/lifecycle_test.go`: update lifecycle expectations.
- `internal/app/committed_replay.go`: remove independent `CMDSync.ProjectCommitted` subscriber-scan replay. Replay should go through delivery only.
- `internal/app/committed_replay_test.go`: update replay expectations.
- `docs/raw/send-path-business-logic-diff.md`: document P2d-d intent model after implementation.
- `docs/development/PROJECT_KNOWLEDGE.md`: add concise intent-vs-snapshot note if not already present.
- `internal/FLOW.md`: update send/delivery/CMD sync flow if changed.
- `AGENTS.md`: no directory change expected unless new packages are added beyond existing paths.

## Implementation Tasks

### Task 1: CMD Intent Model And Builder

**Files:**
- Create: `internal/usecase/cmdsync/intent.go`
- Create: `internal/usecase/cmdsync/intent_test.go`
- Modify: `internal/usecase/cmdsync/types.go`
- Optionally Modify: `internal/usecase/cmdsync/projector.go` to move helper functions later

- [ ] **Step 1: Write failing tests for intent construction**

Add tests to `internal/usecase/cmdsync/intent_test.go`:

```go
func TestBuildConversationIntentUsesScopedUIDsAndSenderReadSeq(t *testing.T) {
    now := time.Unix(123, 0)
    msg := channel.Message{
        ChannelID:   "g1____cmd",
        ChannelType: frame.ChannelTypeGroup,
        MessageSeq:  9,
        FromUID:     "u1",
        Timestamp:   int32(now.Unix()),
        Framer:      frame.Framer{SyncOnce: true},
    }

    intent, ok := BuildConversationIntent(msg, []string{"u2", "u1", "u2", ""}, func() time.Time { return time.Unix(999, 0) })

    require.True(t, ok)
    require.Equal(t, ConversationIntent{
        CommandChannelID: "g1____cmd",
        ChannelType:      frame.ChannelTypeGroup,
        MessageSeq:       9,
        ActiveAt:         now.UnixNano(),
        SenderUID:        "u1",
        UserReadSeqs: map[string]uint64{
            "u1": 9,
            "u2": 0,
        },
    }, intent)
}

func TestBuildConversationIntentNormalizesSyncOnceSourceChannel(t *testing.T) {
    msg := channel.Message{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageSeq: 3, Framer: frame.Framer{SyncOnce: true}}
    intent, ok := BuildConversationIntent(msg, []string{"u1"}, func() time.Time { return time.Unix(100, 0) })
    require.True(t, ok)
    require.Equal(t, "g1____cmd", intent.CommandChannelID)
}

func TestBuildConversationIntentRejectsNoPersistZeroSeqOrEmptyRecipients(t *testing.T) {
    _, ok := BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 0}, []string{"u1"}, time.Now)
    require.False(t, ok)

    _, ok = BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, Framer: frame.Framer{NoPersist: true}}, []string{"u1"}, time.Now)
    require.False(t, ok)

    _, ok = BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1}, []string{"", " "}, time.Now)
    require.False(t, ok)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'TestBuildConversationIntent' -count=1
```

Expected: FAIL with undefined `BuildConversationIntent` / `ConversationIntent`.

- [ ] **Step 3: Implement intent types and builder**

In `internal/usecase/cmdsync/intent.go`, add:

```go
package cmdsync

import (
    "strings"
    "time"

    runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
    "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ConversationIntent describes one CMD conversation update before it is flushed.
type ConversationIntent struct {
    CommandChannelID string
    ChannelType      uint8
    MessageSeq       uint64
    ActiveAt         int64
    SenderUID        string
    UserReadSeqs     map[string]uint64
}

// PendingConversationUpdate is the owner-local buffered update for one CMD channel.
type PendingConversationUpdate struct {
    CommandChannelID string            `json:"command_channel_id"`
    ChannelType      uint8             `json:"channel_type"`
    LastMsgSeq       uint64            `json:"last_msg_seq"`
    ActiveAt         int64             `json:"active_at"`
    UserReadSeqs     map[string]uint64 `json:"user_read_seqs"`
}

// PendingConversationView is the pending overlay for one UID and CMD channel.
type PendingConversationView struct {
    CommandChannelID string
    ChannelType      uint8
    LastMsgSeq       uint64
    ActiveAt         int64
    ReadSeq          uint64
}

// BuildConversationIntent converts a durable CMD message and resolved UIDs into a pending update intent.
func BuildConversationIntent(msg channel.Message, uids []string, now func() time.Time) (ConversationIntent, bool) {
    if !isDurableCMDProjectionMessage(msg) {
        return ConversationIntent{}, false
    }
    commandChannelID := msg.ChannelID
    if !runtimechannelid.IsCommandChannel(commandChannelID) {
        commandChannelID = runtimechannelid.ToCommandChannel(commandChannelID)
    }
    normalized := uniqueNonEmptyStrings(uids)
    if len(normalized) == 0 {
        return ConversationIntent{}, false
    }
    readSeqs := make(map[string]uint64, len(normalized))
    for _, uid := range normalized {
        if uid == msg.FromUID {
            readSeqs[uid] = msg.MessageSeq
        } else {
            readSeqs[uid] = 0
        }
    }
    return ConversationIntent{
        CommandChannelID: commandChannelID,
        ChannelType:      msg.ChannelType,
        MessageSeq:       msg.MessageSeq,
        ActiveAt:         activeAtFromMessage(msg, now),
        SenderUID:        strings.TrimSpace(msg.FromUID),
        UserReadSeqs:     readSeqs,
    }, true
}
```

Keep `isDurableCMDProjectionMessage`, `activeAtFromMessage`, and `uniqueNonEmptyStrings` in `cmdsync` as reusable unexported helpers. If `projector.go` is later removed, move those helpers into `intent.go`.

- [ ] **Step 4: Run intent tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'TestBuildConversationIntent' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/cmdsync/intent.go internal/usecase/cmdsync/intent_test.go internal/usecase/cmdsync/types.go internal/usecase/cmdsync/projector.go
git commit -m "feat: add cmd conversation intents"
```

### Task 2: Owner-Local Pending Updater

**Files:**
- Create: `internal/usecase/cmdsync/pending.go`
- Create: `internal/usecase/cmdsync/pending_file.go`
- Create: `internal/usecase/cmdsync/pending_test.go`
- Modify: `internal/usecase/cmdsync/types.go`

- [ ] **Step 1: Write failing pending updater tests**

Add tests to `internal/usecase/cmdsync/pending_test.go` for these behaviors:

```go
func TestPendingUpdaterCoalescesByCommandChannelAndUID(t *testing.T) {
    store := &fakePendingStateStore{}
    updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000)})

    require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 5, ActiveAt: 50, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 5}}))
    require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 7, ActiveAt: 70, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0, "u3": 0}}))

    got := updater.ListPending(context.Background(), "u2", 10)
    require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 7, ActiveAt: 70, ReadSeq: 5}}, got)
}

func TestPendingUpdaterKeepsWholeFailedFlushBatch(t *testing.T) {
    store := &fakePendingStateStore{err: errors.New("store down")}
    updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000), FlushBatchSize: 10})
    require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}}))

    require.Error(t, updater.Flush(context.Background()))
    require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u1", 10))
    require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 9, ActiveAt: 90, ReadSeq: 0}}, updater.ListPending(context.Background(), "u2", 10))
}

func TestPendingUpdaterRemovesSuccessfulFlushBatch(t *testing.T) {
    store := &fakePendingStateStore{}
    updater := NewConversationUpdater(ConversationUpdaterOptions{Store: store, Now: fixedNano(1000), FlushBatchSize: 10})
    require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 90, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}}))

    require.NoError(t, updater.Flush(context.Background()))
    require.Empty(t, updater.ListPending(context.Background(), "u1", 10))
    require.Empty(t, updater.ListPending(context.Background(), "u2", 10))
}

func TestPendingUpdaterSaveLoadRoundTrip(t *testing.T) {
    dir := t.TempDir()
    updater := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
    require.NoError(t, updater.PushIntent(context.Background(), ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 3, ActiveAt: 30, UserReadSeqs: map[string]uint64{"u1": 0}}))
    require.NoError(t, updater.Stop())

    loaded := NewConversationUpdater(ConversationUpdaterOptions{Store: &fakePendingStateStore{}, DataDir: dir, Now: fixedNano(1000)})
    require.NoError(t, loaded.Start())
    require.Equal(t, []PendingConversationView{{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 3, ActiveAt: 30, ReadSeq: 0}}, loaded.ListPending(context.Background(), "u1", 10))
}
```

Also test:

- `MarkSynced(ctx, uid, CommandChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, throughSeq)` removes only that UID when `throughSeq >= LastMsgSeq`.
- `MarkSynced` keeps pending when `throughSeq < LastMsgSeq`.
- `ListPending` is deterministic by `ActiveAt desc`, `ChannelType`, `CommandChannelID` or by the same order used by sync candidates.
- decode failure renames the pending file to `.bad` and does not panic.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'TestPendingUpdater|TestConversationUpdater' -count=1
```

Expected: FAIL with undefined `NewConversationUpdater` / types.

- [ ] **Step 3: Define pending store/options interfaces**

In `internal/usecase/cmdsync/types.go`, add:

```go
var (
    ErrIntentRequired = errors.New("usecase/cmdsync: conversation intent required")
    ErrConversationIntentStaleOwner = errors.New("usecase/cmdsync: conversation intent stale owner")
)

// PendingStateStore persists flushed CMD conversation state.
type PendingStateStore interface {
    UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
}

// ConversationPendingStore provides owner-local pending overlays to sync/ack.
type ConversationPendingStore interface {
    ListPending(ctx context.Context, uid string, limit int) []PendingConversationView
    MarkSynced(ctx context.Context, uid string, key CommandChannelKey, throughSeq uint64) error
}
```

- [ ] **Step 4: Implement pending updater core**

In `internal/usecase/cmdsync/pending.go`, implement:

```go
type ConversationUpdaterOptions struct {
    Store         PendingStateStore
    DataDir       string
    FlushInterval time.Duration
    FlushBatchSize int
    ShardCount    int
    Now           func() time.Time
    Logger        wklog.Logger
}

type ConversationUpdater struct { /* shards, store, lifecycle, file path, clock */ }

func NewConversationUpdater(opts ConversationUpdaterOptions) *ConversationUpdater
func (u *ConversationUpdater) Start() error
func (u *ConversationUpdater) Stop() error
func (u *ConversationUpdater) PushIntent(ctx context.Context, intent ConversationIntent) error
func (u *ConversationUpdater) ListPending(ctx context.Context, uid string, limit int) []PendingConversationView
func (u *ConversationUpdater) MarkSynced(ctx context.Context, uid string, key CommandChannelKey, throughSeq uint64) error
func (u *ConversationUpdater) Flush(ctx context.Context) error
```

Implementation details:

- Use `fasthash.Hash(commandChannelID) % shardCount` or a local hash helper for shard selection.
- Store `pendingByChannel map[CommandChannelKey]*PendingConversationUpdate` and `userIndex map[string]map[CommandChannelKey]struct{}` per shard.
- Coalesce with monotonic max rules from the spec.
- Build flush batches as individual UID/channel states:

```go
state := metadb.CMDConversationState{
    UID:         uid,
    ChannelID:   update.CommandChannelID,
    ChannelType: int64(update.ChannelType),
    ReadSeq:     readSeq,
    ActiveAt:    update.ActiveAt,
    UpdatedAt:   u.now().UnixNano(),
}
```

- Flush in bounded batches through the existing all-or-error `UpsertCMDConversationStates(ctx, []state) error` API.
- If a batch succeeds, remove all UID/channel entries included in that successful batch.
- If a batch fails, keep every UID/channel entry in that failed batch pending and return the error. Do not claim per-row success because the store API does not expose per-row results.
- If earlier batches succeeded before a later batch failed, remove only the earlier successful batch entries.
- `Stop()` should stop the ticker, run one final bounded `Flush(context.Background())`, then save remaining pending updates.

- [ ] **Step 5: Implement pending file save/load**

In `internal/usecase/cmdsync/pending_file.go`:

- Path: `filepath.Join(DataDir, "conversationv2", "cmd_conversation_updates.json")`.
- Save all shard updates as `[]PendingConversationUpdate`.
- Write to `cmd_conversation_updates.json.tmp`, `Close`, then `os.Rename`.
- Load on `Start()` before the flush loop.
- On successful load, delete the file.
- On JSON decode error, rename to `cmd_conversation_updates.json.bad` or timestamped `.bad` and return nil after logging.

- [ ] **Step 6: Run pending updater tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'TestPendingUpdater|TestConversationUpdater' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/cmdsync/pending.go internal/usecase/cmdsync/pending_file.go internal/usecase/cmdsync/pending_test.go internal/usecase/cmdsync/types.go
git commit -m "feat: add cmd conversation pending updater"
```

### Task 3: `/message/sync` Pending Overlay And Ack Cleanup

**Files:**
- Modify: `internal/usecase/cmdsync/app.go`
- Modify: `internal/usecase/cmdsync/app_test.go`
- Modify: `internal/usecase/cmdsync/types.go`

- [ ] **Step 1: Write failing sync overlay tests**

Add tests to `internal/usecase/cmdsync/app_test.go`:

```go
func TestAppSyncMergesPendingCMDConversationOverlay(t *testing.T) {
    messages := &fakeMessageStore{byKey: map[CommandChannelKey][]channel.Message{
        {ChannelID: "g1____cmd", ChannelType: 2}: {{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, Timestamp: 10}},
    }}
    pending := &fakePendingStore{views: map[string][]PendingConversationView{
        "u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 1, ActiveAt: 10, ReadSeq: 0}},
    }}
    app := New(Options{States: &fakeStateStore{}, Messages: messages, Pending: pending})

    got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})

    require.NoError(t, err)
    require.Equal(t, []channel.Message{{ChannelID: "g1", ChannelType: 2, MessageSeq: 1, Timestamp: 10}}, got.Messages)
}

func TestAppSyncUsesMaxPersistedAndPendingReadSeq(t *testing.T) {
    states := &fakeStateStore{active: []metadb.CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 5, ActiveAt: 100}}}
    pending := &fakePendingStore{views: map[string][]PendingConversationView{"u1": {{CommandChannelID: "g1____cmd", ChannelType: 2, LastMsgSeq: 4, ActiveAt: 200, ReadSeq: 0}}}}
    messages := &fakeMessageStore{}
    app := New(Options{States: states, Messages: messages, Pending: pending})

    _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})

    require.NoError(t, err)
    require.Equal(t, uint64(6), messages.calls[0].fromSeq)
}

func TestAppSyncAckCleansPendingAfterPersistedAdvance(t *testing.T) {
    states := &fakeStateStore{}
    pending := &fakePendingStore{}
    records := NewSyncRecordCache(SyncRecordCacheOptions{Now: time.Now})
    records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
    app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

    require.NoError(t, app.SyncAck(context.Background(), SyncAckCommand{UID: "u1", LastMessageSeq: 9}))

    require.Equal(t, []CommandChannelKey{{ChannelID: "g1____cmd", ChannelType: 2}}, pending.markedKeys)
    require.Equal(t, []uint64{9}, pending.markedSeqs)
}

func TestAppSyncAckTreatsPendingCleanupFailureAsBestEffort(t *testing.T) {
    states := &fakeStateStore{}
    pending := &fakePendingStore{markErr: errors.New("cleanup failed")}
    records := NewSyncRecordCache(SyncRecordCacheOptions{Now: time.Now})
    records.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})
    app := New(Options{States: states, Messages: &fakeMessageStore{}, Records: records, Pending: pending})

    require.NoError(t, app.SyncAck(context.Background(), SyncAckCommand{UID: "u1", LastMessageSeq: 9}))
    require.NotEmpty(t, states.patches)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'TestAppSync.*Pending|TestAppSyncAckCleansPending' -count=1
```

Expected: FAIL with `Options.Pending` undefined or missing merge behavior.

- [ ] **Step 3: Extend app options and merge candidates**

In `internal/usecase/cmdsync/app.go`:

- Add `Pending ConversationPendingStore` to `Options` and `pending ConversationPendingStore` to `App`.
- Convert persisted states and pending views into a shared candidate state keyed by `CommandChannelKey`.
- For each key, merge:

```go
effectiveReadSeq := maxUint64(maxUint64(persisted.ReadSeq, persisted.DeletedToSeq), pending.ReadSeq)
activeAt := maxInt64(persisted.ActiveAt, pending.ActiveAt)
```

- Sort candidate channels deterministically before fetching: highest `activeAt` first, then `ChannelType`, then `CommandChannelID`.
- Fetch messages with `fromSeq = effectiveReadSeq + 1` and existing global `limit` rules.
- Keep sync records only for messages returned to the client.

- [ ] **Step 4: Clean pending on ack**

Use `records := a.records.Pop(uid)` as today. After successful `a.states.AdvanceCMDConversationReadSeq(ctx, patches)`, call pending cleanup as best effort:

```go
if a.pending != nil {
    for _, record := range validRecords {
        if err := a.pending.MarkSynced(ctx, uid, CommandChannelKey{ChannelID: record.CommandChannelID, ChannelType: record.ChannelType}, record.LastReturnedMsgSeq); err != nil {
            a.logger.Warn("cmd sync pending cleanup failed", wklog.Error(err))
        }
    }
}
```

Only call for valid records that generated a read patch. Do not return pending cleanup errors from `SyncAck`, because the sync records were already popped and a client retry cannot identify the same channel set. Pending cleanup is idempotent and best effort; any uncleaned pending entries will either be flushed or later ignored through max read-seq semantics.

- [ ] **Step 5: Run cmdsync tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/usecase/cmdsync/app.go internal/usecase/cmdsync/app_test.go internal/usecase/cmdsync/types.go
git commit -m "feat: overlay pending cmd conversations in sync"
```

### Task 4: Node RPC For UID-Owner Intent Submission

**Files:**
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/cmdsync_rpc.go`
- Modify: `internal/access/node/cmdsync_codec.go`
- Create: `internal/access/node/cmdsync_intent_test.go`
- Optionally Modify: `internal/access/node/cmdsync_rpc_test.go`

- [ ] **Step 1: Write failing node RPC tests**

Add to `internal/access/node/cmdsync_intent_test.go`:

```go
func TestCMDConversationIntentBinaryCodecRoundTrip(t *testing.T) {
    req := cmdSyncRPCRequest{
        Op: cmdSyncOpPushIntent,
        Intent: cmdsync.ConversationIntent{
            CommandChannelID: "g1____cmd",
            ChannelType:      2,
            MessageSeq:       9,
            ActiveAt:         100,
            SenderUID:        "u1",
            UserReadSeqs:     map[string]uint64{"u1": 9, "u2": 0},
        },
    }
    body, err := encodeCMDSyncRequestBinary(req)
    require.NoError(t, err)
    got, err := decodeCMDSyncRequest(body)
    require.NoError(t, err)
    require.Equal(t, req, got)
}

func TestCMDIntentAdapterMissingProviderReturnsRejectedStatus(t *testing.T) {
    adapter := New(Options{})
    body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, UserReadSeqs: map[string]uint64{"u1": 0}}}))
    require.NoError(t, err)
    resp := mustDecodeCMDSyncResponse(t, body)
    require.Equal(t, rpcStatusRejected, resp.Status)
    require.Contains(t, resp.Error, "cmd conversation intent")
}

func TestCMDIntentAdapterPushDoesNotRequireCMDSyncUsecase(t *testing.T) {
    sink := &recordingCMDIntentSink{}
    adapter := New(Options{CMDConversationIntents: sink}) // CMDSync intentionally nil.
    body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, UserReadSeqs: map[string]uint64{"u1": 0}}}))
    require.NoError(t, err)
    resp := mustDecodeCMDSyncResponse(t, body)
    require.Equal(t, rpcStatusOK, resp.Status)
    require.Len(t, sink.intents, 1)
}

func TestCMDIntentAdapterStaleOwnerIsRetryable(t *testing.T) {
    sink := &recordingCMDIntentSink{err: cmdsync.ErrConversationIntentStaleOwner}
    adapter := New(Options{CMDConversationIntents: sink})
    body, err := adapter.handleCMDSyncRPC(context.Background(), mustEncodeCMDSyncRequest(t, cmdSyncRPCRequest{Op: cmdSyncOpPushIntent, Intent: cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, UserReadSeqs: map[string]uint64{"u1": 0}}}))
    require.NoError(t, err)
    resp := mustDecodeCMDSyncResponse(t, body)
    require.Equal(t, rpcStatusStaleOwner, resp.Status)
}

func TestCMDIntentClientPushCallsRemoteOwner(t *testing.T) {
    network := newFakeClusterNetwork(map[uint64][]uint64{}, map[uint64]uint64{})
    sink := &recordingCMDIntentSink{}
    _ = New(Options{Cluster: network.cluster(2), CMDConversationIntents: sink})
    client := NewClient(network.cluster(1))

    intent := cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 100, UserReadSeqs: map[string]uint64{"u1": 0}}
    err := client.PushCMDConversationIntent(context.Background(), 2, intent)

    require.NoError(t, err)
    require.Equal(t, []cmdsync.ConversationIntent{intent}, sink.intents)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'CMDConversationIntent|CMDIntent' -count=1
```

Expected: FAIL with undefined op/client/provider.

- [ ] **Step 3: Extend node adapter options and RPC request**

In `internal/access/node/options.go`:

```go
// CMDConversationIntentSink accepts UID-owner CMD conversation update intents.
type CMDConversationIntentSink interface {
    PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error
}
```

Add `CMDConversationIntents CMDConversationIntentSink` to `Options` and `cmdConversationIntents CMDConversationIntentSink` to `Adapter`.

In `internal/access/node/cmdsync_rpc.go`:

- Add `cmdSyncOpPushIntent = "push_intent"`.
- Add a retryable status constant such as `rpcStatusStaleOwner = "stale_owner"` if no existing retryable status fits.
- Add `Intent cmdsync.ConversationIntent` to `cmdSyncRPCRequest`.
- In `handleCMDSyncRPC`, route the new op to `a.cmdConversationIntents.PushIntent(ctx, req.Intent)`.
- If the provider is missing or the intent is malformed, return `rpcStatusRejected`.
- If `errors.Is(err, cmdsync.ErrConversationIntentStaleOwner)`, return `rpcStatusStaleOwner` so the client can preserve retry semantics.
- Add client method:

```go
func (c *Client) PushCMDConversationIntent(ctx context.Context, nodeID uint64, intent cmdsync.ConversationIntent) error
```

The client must convert `rpcStatusStaleOwner` into an error that satisfies `errors.Is(err, cmdsync.ErrConversationIntentStaleOwner)`.

- [ ] **Step 4: Extend binary codec**

In `internal/access/node/cmdsync_codec.go`, append/read the intent after the ack command for backward-compatible single version within this branch:

```go
func appendCMDConversationIntent(dst []byte, intent cmdsync.ConversationIntent) []byte
func readCMDConversationIntent(body []byte, offset int) (cmdsync.ConversationIntent, int, error)
```

Map encoding must be deterministic. Sort UID keys before appending:

```go
uids := make([]string, 0, len(intent.UserReadSeqs))
for uid := range intent.UserReadSeqs { uids = append(uids, uid) }
sort.Strings(uids)
dst = appendUvarint(dst, uint64(len(uids)))
for _, uid := range uids {
    dst = appendString(dst, uid)
    dst = appendUvarint(dst, intent.UserReadSeqs[uid])
}
```

- [ ] **Step 5: Run node RPC tests**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'CMDSync|CMDConversationIntent|CMDIntent' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/access/node/options.go internal/access/node/cmdsync_rpc.go internal/access/node/cmdsync_codec.go internal/access/node/cmdsync_intent_test.go internal/access/node/cmdsync_rpc_test.go
git commit -m "feat: add cmd conversation intent rpc"
```

### Task 5: App UID-Owner Intent Router And Owner Validation

**Files:**
- Create: `internal/app/cmdsync_intent.go`
- Create: `internal/app/cmdsync_intent_test.go`
- Modify: `internal/app/cmdsync.go` if shared owner helpers are reused

- [ ] **Step 1: Write failing router tests**

Add to `internal/app/cmdsync_intent_test.go`:

```go
func TestCMDIntentRouterSplitsByUIDOwner(t *testing.T) {
    local := &recordingCMDIntentSink{}
    remote := &recordingCMDIntentRemote{}
    cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
    router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}

    accepted, err := router.PushIntent(context.Background(), cmdsync.ConversationIntent{
        CommandChannelID: "g1____cmd",
        ChannelType:      2,
        MessageSeq:       9,
        ActiveAt:         100,
        UserReadSeqs:     map[string]uint64{"u1": 9, "u2": 0},
    })

    require.NoError(t, err)
    require.True(t, accepted)
    require.Equal(t, map[string]uint64{"u1": 9}, local.intents[0].UserReadSeqs)
    require.Equal(t, map[string]uint64{"u2": 0}, remote.calls[0].intent.UserReadSeqs)
}

func TestCMDIntentRouterPartialFailureReturnsNotFullyAccepted(t *testing.T) {
    local := &recordingCMDIntentSink{}
    remote := &recordingCMDIntentRemote{err: errors.New("remote down")}
    cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
    router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}

    accepted, err := router.PushIntent(context.Background(), cmdsync.ConversationIntent{CommandChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9, ActiveAt: 100, UserReadSeqs: map[string]uint64{"u1": 0, "u2": 0}})

    require.Error(t, err)
    require.False(t, accepted)
    require.Len(t, local.intents, 1)
}
```

Also test owner-local receiver validation and stale-owner propagation:

- `ownerValidatingCMDIntentSink` rejects a UID not owned by local node with `cmdsync.ErrConversationIntentStaleOwner`.
- Router re-resolves UID ownership and retries once when the remote client returns an error satisfying `errors.Is(err, cmdsync.ErrConversationIntentStaleOwner)`.
- The node RPC mapping for that sentinel is covered in Task 4.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/app -run 'CMDIntentRouter|CMDConversationIntent' -count=1
```

Expected: FAIL with undefined router types.

- [ ] **Step 3: Implement router and observer types**

In `internal/app/cmdsync_intent.go`, add:

```go
type cmdConversationIntentRemote interface {
    PushCMDConversationIntent(ctx context.Context, nodeID uint64, intent cmdsync.ConversationIntent) error
}

type cmdConversationIntentLocal interface {
    PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error
}

type cmdConversationIntentRouter struct {
    local       cmdConversationIntentLocal
    remote      cmdConversationIntentRemote
    cluster     cmdsyncUIDOwnerCluster
    localNodeID uint64
    logger      wklog.Logger
}

func (r cmdConversationIntentRouter) PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) (fullyAccepted bool, err error)
```

Implementation:

- Trim/validate intent through cmdsync helper if available.
- Group `UserReadSeqs` by `ownerNodeID(uid)` using `SlotForKey` + `LeaderOf`.
- For each owner group, clone the intent with only that group's `UserReadSeqs`.
- Local group calls `local.PushIntent`.
- Remote group calls `remote.PushCMDConversationIntent`.
- Return `fullyAccepted=true` only when every group succeeds.
- If one group fails after earlier groups succeed, return `false` and joined error; duplicate retry is safe.
- If a remote group returns `cmdsync.ErrConversationIntentStaleOwner`, re-resolve that group's UIDs with `LeaderOf(SlotForKey(uid))` and retry once. If it still fails, return `false` and the joined error.

- [ ] **Step 4: Implement local owner validation wrapper**

Add an app wrapper used by node RPC:

```go
type ownerValidatingCMDIntentSink struct {
    local       cmdConversationIntentLocal
    cluster     cmdsyncUIDOwnerCluster
    localNodeID uint64
}

func (s ownerValidatingCMDIntentSink) PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error
```

For every UID in `intent.UserReadSeqs`, verify `ownerNodeID(uid) == localNodeID`. If not, return `cmdsync.ErrConversationIntentStaleOwner`; do not store partial invalid intents locally.

- [ ] **Step 5: Run router tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'CMDIntentRouter|CMDConversationIntent' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmdsync_intent.go internal/app/cmdsync_intent_test.go internal/app/cmdsync.go
git commit -m "feat: route cmd conversation intents to uid owners"
```

### Task 6: Event Metadata And Request-Scoped Send Hook

**Files:**
- Modify: `internal/contracts/messageevents/events.go`
- Modify: `internal/contracts/messageevents/events_test.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/usecase/delivery/submit.go`
- Modify: `internal/access/node/delivery_submit_codec.go`
- Modify: `internal/access/node/delivery_submit_rpc_test.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write failing tests for metadata propagation**

Add/modify tests:

```go
func TestMessageCommittedClonePreservesCMDConversationIntentSubmitted(t *testing.T) {
    event := MessageCommitted{CMDConversationIntentSubmitted: true}
    clone := event.Clone()
    require.True(t, clone.CMDConversationIntentSubmitted)
}
```

In `internal/access/node/delivery_submit_rpc_test.go`, add to existing round trip:

```go
Envelope: deliveryruntime.CommittedEnvelope{
    MessageScopedUIDs: []string{"u1"},
    CMDConversationIntentSubmitted: true,
}
```

and assert decoded value is true.

- [ ] **Step 2: Run metadata tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/contracts/messageevents ./internal/access/node -run 'CMDConversationIntentSubmitted|DeliverySubmit' -count=1
```

Expected: FAIL with missing field or codec mismatch.

- [ ] **Step 3: Add metadata fields and codec support**

Add `CMDConversationIntentSubmitted bool` to:

- `messageevents.MessageCommitted`
- `deliveryruntime.CommittedEnvelope`

Copy it in:

- `MessageCommitted.Clone()`
- `committedEnvelopeFromMessageEvent`
- `deliveryRuntimeCommittedSubmitter.SubmitCommitted`
- `delivery.App.SubmitRealtime` should default false
- `internal/access/node/delivery_submit_codec.go` encode/decode path

Delivery submit codec versioning:

- Add `deliverySubmitRequestMagicV3 = [...]byte{'W', 'K', 'D', 'C', 3}`.
- Encode V3 whenever `len(MessageScopedUIDs) > 0` or `CMDConversationIntentSubmitted` is true.
- V3 payload layout should be V2 fields plus one trailing bool for `CMDConversationIntentSubmitted`.
- V1 decodes with empty scoped UIDs and false flag; V2 decodes scoped UIDs and false flag; V3 decodes both scoped UIDs and the flag.
- Update `isDeliverySubmitRequestBinary` and legacy tests so existing V1/V2 compatibility remains covered.

- [ ] **Step 4: Write failing request-scoped send tests**

In `internal/usecase/message/send_test.go`, add tests:

```go
func TestRequestScopedDurableSendSubmitsCMDConversationIntent(t *testing.T) {
    sink := &recordingCMDIntentSink{accepted: true}
    dispatcher := &recordingCommittedDispatcher{}
    app := New(Options{Cluster: fakeCluster, MetaRefresher: refresher, RemoteAppender: appender, CommittedDispatcher: dispatcher, CMDConversationIntents: sink, Now: fixedNowFn})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", RequestSubscribers: []string{"u1", "u2"}, Framer: frame.Framer{SyncOnce: true}})

    require.NoError(t, err)
    require.NotZero(t, result.MessageSeq)
    require.Len(t, sink.intents, 1)
    require.True(t, dispatcher.calls[0].CMDConversationIntentSubmitted)
}

func TestRequestScopedDurableSendLeavesIntentSubmittedFalseOnPartialIntentFailure(t *testing.T) {
    sink := &recordingCMDIntentSink{err: errors.New("remote owner down")}
    dispatcher := &recordingCommittedDispatcher{}
    app := New(Options{Cluster: fakeCluster, MetaRefresher: refresher, RemoteAppender: appender, CMDConversationIntents: sink, CommittedDispatcher: dispatcher, Now: fixedNowFn})

    _, err := app.Send(context.Background(), requestScopedSyncOnceCommand())

    require.NoError(t, err) // intent side effect is non-fatal
    require.False(t, dispatcher.calls[0].CMDConversationIntentSubmitted)
}
```

- [ ] **Step 5: Run request-scoped tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'RequestScopedDurableSend.*CMDConversationIntent|CMDConversationIntentSubmitted' -count=1
```

Expected: FAIL with missing option/field.

- [ ] **Step 6: Add message intent sink**

In `internal/usecase/message/app.go`:

```go
type CMDConversationIntentSink interface {
    PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) (bool, error)
}
```

Add `CMDConversationIntents CMDConversationIntentSink` to `Options` and `cmdConversationIntents CMDConversationIntentSink` to `App`.

In `sendDurable` after append succeeds and before `dispatcher.SubmitCommitted`, when `len(cmd.RequestSubscribers) > 0`:

```go
intentSubmitted := false
if a.cmdConversationIntents != nil {
    if intent, ok := cmdsync.BuildConversationIntent(result.Message, cmd.RequestSubscribers, a.now); ok {
        accepted, err := a.cmdConversationIntents.PushIntent(ctx, intent)
        if err != nil { log side-effect failure }
        intentSubmitted = accepted
    }
}
```

Pass `CMDConversationIntentSubmitted: intentSubmitted` into `messageevents.MessageCommitted`.

Do not return an error only because the intent sink failed. Keep existing request-scoped delivery submit behavior unchanged.

- [ ] **Step 7: Run tests**

Run:

```bash
GOWORK=off go test ./internal/contracts/messageevents ./internal/access/node ./internal/usecase/message -run 'CMDConversationIntent|RequestScoped|DeliverySubmit' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/contracts/messageevents/events.go internal/contracts/messageevents/events_test.go internal/runtime/delivery/types.go internal/usecase/delivery/submit.go internal/access/node/delivery_submit_codec.go internal/access/node/delivery_submit_rpc_test.go internal/usecase/message/app.go internal/usecase/message/send.go internal/usecase/message/send_test.go
git commit -m "feat: submit request scoped cmd conversation intents"
```

### Task 7: Delivery UID-Page Observer And Projector Replacement

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/app/committed_events.go`
- Modify: `internal/app/committed_events_test.go`
- Modify: `internal/usecase/cmdsync/projector.go`
- Modify/Delete: `internal/usecase/cmdsync/projector_test.go`

- [ ] **Step 1: Write failing delivery observer tests**

In `internal/app/deliveryrouting_test.go`, add tests:

```go
func TestTagDeliveryResolverNotifiesUIDObserverBeforePresenceExpansion(t *testing.T) {
    observer := &recordingResolvedUIDObserver{}
    resolver := tagDeliveryResolver{localNodeID: 1, tags: tagManagerWithLocalUIDs("tag", []string{"offline", "online"}), subscribers: subscribers, authority: &presenceAuthorityWithOnly("online"), uidObserver: observer}

    token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{ChannelID: "g1____cmd", ChannelType: 2}, deliveryruntime.CommittedEnvelope{Message: channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 9}})
    require.NoError(t, err)
    _, _, _, err = resolver.ResolvePage(context.Background(), token, "", 10)

    require.NoError(t, err)
    require.Equal(t, []string{"offline", "online"}, observer.pages[0].UIDs)
}

func TestDeliveryUIDObserverSkipsWhenRequestScopedIntentAlreadySubmitted(t *testing.T) {
    observer := &recordingResolvedUIDObserver{}
    resolver := localDeliveryResolver{subscribers: scopedSubscribers, authority: &presenceAuthorityWithOnly("u1"), uidObserver: observer}
    env := deliveryruntime.CommittedEnvelope{Message: channel.Message{ChannelID: "temp____cmd", ChannelType: frame.ChannelTypeTemp, MessageSeq: 1}, MessageScopedUIDs: []string{"u1"}, CMDConversationIntentSubmitted: true}

    token, err := resolver.BeginResolve(context.Background(), deliveryruntime.ChannelKey{ChannelID: env.ChannelID, ChannelType: env.ChannelType}, env)
    require.NoError(t, err)
    _, _, _, err = resolver.ResolvePage(context.Background(), token, "", 10)

    require.NoError(t, err)
    require.Empty(t, observer.pages)
}
```

Also add:

- a non-fatal observer test where the observer records an internal error but `ResolvePage` still returns routes and nil error. Since the observer interface returns no error, use a fake observer that panics only if called incorrectly; do not allow panics in production observer.
- a fallback test where `tagDeliveryResolver` has nil tags or subscribers, delegates to an inline `localDeliveryResolver`, and the UID observer is still called. This prevents losing CMD intents on the fallback path.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/app -run 'Delivery.*UIDObserver|ResolvedUIDObserver|IntentAlreadySubmitted' -count=1
```

Expected: FAIL with missing observer fields/types.

- [ ] **Step 3: Add UID observer interfaces and token envelope fields**

In `internal/app/deliveryrouting.go`:

```go
type resolvedUIDPage struct {
    Envelope deliveryruntime.CommittedEnvelope
    UIDs     []string
}

type resolvedUIDObserver interface {
    OnResolvedUIDPage(context.Context, resolvedUIDPage)
}
```

Add `env deliveryruntime.CommittedEnvelope` to `localResolveToken` and `tagResolveToken`.

Add `uidObserver resolvedUIDObserver` to `localDeliveryResolver` and `tagDeliveryResolver`.

In both `BeginResolve` methods, store `env` in the token.

- [ ] **Step 4: Notify before presence expansion**

In `localDeliveryResolver.ResolvePage`, after `uids` are read from `SubscriberResolver.NextPage` and before `EndpointsByUIDs`, call:

```go
r.notifyResolvedUIDPage(ctx, resolveToken.env, uids)
```

In `tagDeliveryResolver.ResolvePage`, after `nextDeliveryTagUIDPageAt` returns `uids` and before `expandTagUIDs`, call the same helper.

Helper rules:

```go
func (r localDeliveryResolver) notifyResolvedUIDPage(ctx context.Context, env deliveryruntime.CommittedEnvelope, uids []string) {
    if r.uidObserver == nil || len(uids) == 0 {
        return
    }
    if env.CMDConversationIntentSubmitted && len(env.MessageScopedUIDs) > 0 {
        return
    }
    r.uidObserver.OnResolvedUIDPage(ctx, resolvedUIDPage{Envelope: env, UIDs: append([]string(nil), uids...)})
}
```

If no clone helper exists for `CommittedEnvelope`, copy `Message.Payload` and slices defensively in the observer implementation, not the hot path.

- [ ] **Step 5: Implement app observer that builds intents**

In `internal/app/cmdsync_intent.go`, add:

```go
type cmdConversationResolvedUIDObserver struct {
    sink   interface{ PushIntent(context.Context, cmdsync.ConversationIntent) (bool, error) }
    now    func() time.Time
    logger wklog.Logger
}

func (o cmdConversationResolvedUIDObserver) OnResolvedUIDPage(ctx context.Context, page resolvedUIDPage) {
    if o.sink == nil || page.Envelope.CMDConversationIntentSubmitted {
        return
    }
    intent, ok := cmdsync.BuildConversationIntent(page.Envelope.Message, page.UIDs, o.now)
    if !ok {
        return
    }
    if _, err := o.sink.PushIntent(ctx, intent); err != nil {
        o.logger.Warn("cmd conversation intent route failed", wklog.Error(err))
    }
}
```

- [ ] **Step 6: Remove P2d-c subscriber-scan projector from fanout semantics**

Refactor `internal/usecase/cmdsync/projector.go`:

- Delete `Projector`, `ProjectorOptions`, queue, subscriber resolver scan, and direct state writes after app wiring no longer uses it.
- Move `isDurableCMDProjectionMessage`, `activeAtFromMessage`, and `uniqueNonEmptyStrings` into `intent.go` if they still live in `projector.go`.
- Delete `internal/usecase/cmdsync/projector_test.go`; equivalent coverage belongs in `intent_test.go`, `pending_test.go`, and app delivery observer tests.

No test should expect post-commit subscriber scanning after this task.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/usecase/cmdsync -run 'Delivery.*UIDObserver|ResolvedUIDObserver|Projector|Intent' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/app/cmdsync_intent.go internal/app/cmdsync_intent_test.go internal/usecase/cmdsync/projector.go internal/usecase/cmdsync/projector_test.go
git commit -m "feat: project cmd intents from delivery uid pages"
```

### Task 8: App Composition, Lifecycle, Node Wiring, And Replay Cleanup

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/committed_replay_test.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/app/cmdsync_test.go` if test fakes need pending store methods

- [ ] **Step 1: Write failing composition/lifecycle tests**

Add/update tests:

```go
func TestAppLifecycleStartsAndStopsCMDConversationUpdaterHooks(t *testing.T) {
    var starts, stops int
    app := &App{
        cluster: &appLifecycleTestCluster{},
        gateway: &gateway.Gateway{},
        startClusterFn: func() error { return nil },
        startGatewayFn: func() error { return nil },
        startCMDConversationUpdaterFn: func() error { starts++; return nil },
        stopCMDConversationUpdaterFn:  func(context.Context) error { stops++; return nil },
        stopGatewayFn: func() error { return nil },
        stopClusterFn: func() {},
        closeRaftDBFn: func() error { return nil },
        closeWKDBFn: func() error { return nil },
    }

    require.NoError(t, app.Start())
    require.Equal(t, 1, starts)
    require.NoError(t, app.Stop())
    require.Equal(t, 1, stops)
}

func TestCommittedReplayDoesNotCallIndependentCMDSyncProjector(t *testing.T) {
    delivery := &fakeCommittedReplayDelivery{}
    replayer := newCommittedReplayer(committedReplayerConfig{Delivery: delivery})
    // Use the existing replay harness to append a command message and assert it reaches delivery.
    // There should be no CMDSync field in committedReplayerConfig after the cleanup.
    _ = replayer
}
```

Also update build tests to assert `cmdSyncApp` has a pending overlay dependency when app builds.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/app -run 'Lifecycle.*CMD|CommittedReplay.*CMD|Build' -count=1
```

Expected: FAIL with old `cmdsync_projector` lifecycle expectations.

- [ ] **Step 3: Replace app fields and lifecycle**

In `internal/app/app.go`:

- Remove or stop using `cmdSyncProjector cmdsync.Projector` and `cmdSyncProjectorOn`.
- Add concrete runtime fields:

```go
cmdConversationUpdater *cmdsync.ConversationUpdater
cmdConversationIntents cmdConversationIntentRouter
cmdConversationUpdaterOn atomic.Bool
```

Keep lifecycle test hook funcs because tests should not assign fakes to the concrete updater field:

```go
startCMDConversationUpdaterFn func() error
stopCMDConversationUpdaterFn  func(context.Context) error
```

In `internal/app/lifecycle_components.go`:

- Replace `appLifecycleCMDSyncProjector = "cmdsync_projector"` with `appLifecycleCMDConversationUpdater = "cmd_conversation_updater"`.
- Start/stop `cmdConversationUpdater`.
- Ensure stop order lets delivery stop before final pending save if that is how lifecycle ordering currently works. If current lifecycle stops in reverse start order, start the updater before delivery runtime so it stops after delivery has drained.

- [ ] **Step 4: Wire build composition**

In `internal/app/build.go`:

- Instantiate pending updater after `app.store` exists:

```go
app.cmdConversationUpdater = cmdsync.NewConversationUpdater(cmdsync.ConversationUpdaterOptions{
    Store:          app.store,
    DataDir:        cfg.Node.DataDir,
    FlushInterval:  cfg.Conversation.ActiveHintFlushInterval, // or a local default if config has no suitable field
    FlushBatchSize: cfg.Conversation.ActiveHintFlushBatchSize,
    Logger:         app.logger.Named("cmdsync.pending"),
})
```

- Instantiate router:

```go
cmdIntentRouter := cmdConversationIntentRouter{local: app.cmdConversationUpdater, remote: app.nodeClient, cluster: app.cluster, localNodeID: cfg.Node.ID, logger: app.logger.Named("cmdsync.intent")}
app.cmdConversationIntents = cmdIntentRouter
```

- Pass pending updater into `cmdsync.New`:

```go
app.cmdSyncApp = cmdsync.New(cmdsync.Options{
    States: app.store,
    Messages: cmdsyncMessageStore{
        localNodeID: cfg.Node.ID,
        channelLog:  app.channelLogDB,
        metas:       app.store,
        remote:      app.nodeClient,
    },
    Pending: app.cmdConversationUpdater,
    Logger:  app.logger.Named("cmdsync"),
})
```

- Remove the old `app.cmdSyncProjector` construction block from `internal/app/build.go`.
- Remove `app.cmdSyncProjector` from `committedFanout` subscribers. Fanout should include delivery dispatcher only for delivery and ordinary conversation projector as currently configured through dispatcher.
- Remove `CMDSync: app.cmdSyncProjector` from committed replayer config.
- Pass `cmdConversationResolvedUIDObserver{sink: cmdIntentRouter, now: time.Now, logger: app.logger.Named("cmdsync.intent")}` into both `tagDeliveryResolver` and fallback `localDeliveryResolver` constructors.
- Pass `CMDConversationIntents: cmdIntentRouter` into `message.New` options.
- Pass `CMDConversationIntents: ownerValidatingCMDIntentSink{local: app.cmdConversationUpdater, cluster: app.cluster, localNodeID: cfg.Node.ID}` into `accessnode.New` options.

- [ ] **Step 5: Clean committed replay config**

In `internal/app/committed_replay.go`:

- Remove `committedReplayCMDSync` interface and `CMDSync` config field.
- Remove the `ProjectCommitted` call on the old `CMDSync` replay dependency.
- Rely on replayed delivery to invoke the delivery UID observer for non-request-scoped CMD messages.
- Keep the documented limitation that request-scoped replay without `MessageScopedUIDs` remains best effort.

Update tests accordingly.

- [ ] **Step 6: Run app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'CMD|Lifecycle|CommittedReplay|DeliveryRouting|Build' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/app/app.go internal/app/build.go internal/app/lifecycle_components.go internal/app/lifecycle_test.go internal/app/committed_replay.go internal/app/committed_replay_test.go internal/access/node/options.go internal/app/cmdsync_test.go
git commit -m "feat: wire cmd conversation intent runtime"
```

### Task 9: End-To-End Behavior Regressions And Documentation

**Files:**
- Modify: `internal/usecase/conversation/sync_test.go` if pending overlay regression needs an explicit ordinary sync test.
- Modify: `internal/usecase/cmdsync/app_test.go`
- Modify: `internal/app/cmdsync_test.go`
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `internal/FLOW.md`

- [ ] **Step 1: Add regression tests for isolation and NoPersist**

Tests to add or verify already exist:

- Ordinary `/conversation/sync` ignores CMD channels, including if the CMD pending updater has an entry for the same UID.
- `/message/sync` strips exactly one `____cmd` suffix for pending-only channels.
- `/message/syncack` uses latest sync generation and cleans pending; it does not use `last_message_seq` as channel selector.
- `NoPersist` command-style messages do not call the intent sink and do not create pending entries.
- Partial owner-route failure on request-scoped durable send retries through delivery observer and keeps already accepted partition idempotent.

- [ ] **Step 2: Run focused regression tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync ./internal/usecase/message ./internal/usecase/conversation ./internal/app ./internal/access/node -count=1
```

Expected: PASS.

- [ ] **Step 3: Update docs**

Update `docs/raw/send-path-business-logic-diff.md`:

- Replace the P2d-c remaining gap that says durable request-scoped subscribers need snapshot storage.
- State P2d-d intentionally uses pending CMD conversation intents, not subscriber snapshots.
- Note graceful-stop recovery boundary and no kill -9 guarantee.

Update `docs/development/PROJECT_KNOWLEDGE.md` with one concise note:

```markdown
- CMD offline sync should persist/update conversation intents (`uid -> readSeq` per command channel) rather than storing per-message subscriber snapshots; request-scoped recipients and delivery tag UID pages are the authoritative sources for those intents.
```

Update `internal/FLOW.md` if it describes committed dispatch/CMD sync projector flow.

- [ ] **Step 4: Run doc-sensitive import/boundary test**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/conversation/sync_test.go internal/usecase/cmdsync/app_test.go internal/app/cmdsync_test.go docs/raw/send-path-business-logic-diff.md docs/development/PROJECT_KNOWLEDGE.md internal/FLOW.md
git commit -m "docs: document cmd conversation intent recovery"
```

Skip unchanged paths in `git add` if a regression was already covered elsewhere.

### Task 10: Final Verification

**Files:**
- No code changes unless verification finds failures.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync ./internal/usecase/message ./internal/usecase/delivery ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run storage/proxy sanity tests touched by CMD state interfaces**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run import boundary test**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 4: Inspect git status and recent commits**

Run:

```bash
git status --short
git log --oneline -10
```

Expected: only intentional changes remain; commits are small and task-scoped.

- [ ] **Step 5: Handoff summary**

Prepare final implementation summary with:

- intent builder/pending updater behavior;
- request-scoped send direct intent behavior;
- delivery UID-page observer behavior;
- `/message/sync` pending overlay and ack cleanup;
- tests run and results;
- any remaining known boundaries, especially no kill -9 guarantee.
