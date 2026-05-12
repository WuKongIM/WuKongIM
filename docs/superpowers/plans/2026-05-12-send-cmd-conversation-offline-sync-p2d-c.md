# Send CMD Conversation Offline Sync P2d-c Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore legacy-compatible durable CMD offline sync through `/message/sync` and `/message/syncack` without leaking CMD state into ordinary conversation sync.

**Architecture:** Add a dedicated UID-owned CMD conversation state table instead of extending the current type-less chat conversation state. A new `internal/usecase/cmdsync` package owns CMD projection, sync record generations, and sync/ack business rules; app-level adapters route sync APIs to the UID owner and fetch command-channel logs from the `source____cmd` channel owner. Existing ordinary conversation projection remains unchanged and continues to skip CMD/`SyncOnce` messages.

**Tech Stack:** Go, `testing` + `github.com/stretchr/testify/require`, Pebble-backed `pkg/slot/meta`, slot FSM/proxy RPC, Gin HTTP API, WuKongIM channel log/read APIs, `GOWORK=off go test`.

---

## Spec Reference

- Design spec: `docs/superpowers/specs/2026-05-12-send-cmd-conversation-offline-sync-design.md`
- Earlier P2d plan: `docs/superpowers/plans/2026-05-12-send-cmd-channel-convergence-p2d.md`
- Business diff tracker: `docs/raw/send-path-business-logic-diff.md`
- Flow docs to inspect/update: `internal/FLOW.md`, `pkg/slot/FLOW.md`, `pkg/channel/FLOW.md`

## Scope

Implement P2d-c only:

- Restore `POST /message/sync` for durable CMD messages.
- Restore `POST /message/syncack` for CMD sync read cursor advancement.
- Add a separate CMD working set and read cursor, keyed by UID + `source____cmd` + channel type.
- Fetch offline CMD messages from command-channel logs and return client-facing source channel IDs.
- Keep `NoPersist` CMD online-only and absent from CMD state/offline sync.
- Keep ordinary `/conversation/sync`, clear unread, set unread, and delete isolated from CMD state.

Do not implement:

- Persistent request-scoped subscriber snapshot replay recovery. Live durable request-scoped CMD projection is in scope; replay recovery remains P2d-d.
- `/message/sendbatch`, plugin/webhook/AI side effects, `AllowStranger`, or `expire` behavior changes.
- A global durable `systemcmdonline` channel.
- Direct non-cluster local shortcuts. A single-node deployment is still a single-node cluster.

## Chosen Persistence Shape

Use a dedicated CMD state store/table:

```text
TableIDCMDConversationState = 8
primary key = (uid, channel_type, channel_id)
active index = (uid, active_at DESC, channel_type, channel_id)
channel_id = source____cmd
```

Rationale:

- The current `UserConversationState` active index has no `ConversationType` dimension, so inserting CMD rows there can leak into `/conversation/sync` and chat maintenance APIs.
- Extending `UserConversationState` with `ConversationType` would touch every chat state key, index, codec, RPC, and compatibility path.
- A dedicated table keeps CMD sync isolated, minimizes regression risk, and still reuses the same UID-owner slot authority model.

## Authority And Business Rules

### UID CMD State Authority

- `/message/sync`, `/message/syncack`, and sync record generations are UID-owned.
- App routing uses:

```go
slotID := cluster.SlotForKey(uid)
owner, err := cluster.LeaderOf(slotID)
```

- If `owner == localNodeID`, call the local `cmdsync.App`; otherwise use an `internal/access/node` RPC to the owner.
- The CMD state store itself still writes through `pkg/slot/proxy.Store`, grouped by `hashSlotForKey(uid)`, so projector writes can originate on any node.

### Command Channel Log Authority

- CMD message facts are command-channel-owned.
- The command log key is always `source____cmd` with the original channel type.
- The app facts adapter resolves the channel owner with `GetChannelRuntimeMeta(ctx, cmdChannelID, channelType).Leader`; local reads use `channelhandler.SyncMessages`, remote reads use existing `access/node.ChannelMessagesQuery{SyncMode:true}`.
- The UID owner must not assume it owns command-channel logs.

### CMD Subscriber Resolution

- Non-request-scoped CMD projection uses the P2d-a source-authoritative resolver:

```go
token, err := resolver.BeginSnapshotWithRequest(ctx, channel.ChannelID{ID: msg.ChannelID, Type: msg.ChannelType}, delivery.SubscriberSnapshotRequest{})
uids, cursor, done, err := resolver.NextPage(ctx, token, cursor, pageLimit)
```

- `delivery.SubscriberResolver` strips one `____cmd` suffix internally and resolves person/agent/visitors/info/group sources according to the current delivery behavior.
- Durable request-scoped CMD projection uses `MessageCommitted.MessageScopedUIDs` as the exact recipient snapshot for the live committed path; it must not rescan a temporary channel store.
- If `msg.FromUID` appears in the resolved recipient set, the sender CMD state is written with `ReadSeq = msg.MessageSeq`; if not, no sender-only CMD state is created.

### Realtime Delivery Boundary

- Realtime delivery remains owned by the existing committed dispatcher and `delivery.App`.
- The new CMD projector only creates/updates offline sync state; it must not send packets.
- `NoPersist` CMD messages have `MessageSeq == 0`, never append to the channel log, and must not create CMD state or appear in `/message/sync`.

## File Structure

### New Files

- `pkg/slot/meta/cmd_conversation_state.go`: Dedicated CMD state model, primary/active key encoding, list/get/upsert/read-advance methods.
- `pkg/slot/meta/cmd_conversation_state_test.go`: Meta-store isolation and cursor tests.
- `pkg/slot/proxy/cmd_conversation_state_rpc.go`: Distributed CMD state methods and authoritative RPC handler.
- `pkg/slot/proxy/cmd_conversation_state_codec.go`: Binary codec for CMD state RPC requests/responses.
- `pkg/slot/proxy/cmd_conversation_state_rpc_test.go`: RPC codec/unit tests.
- `internal/usecase/cmdsync/types.go`: Usecase commands/results/interfaces.
- `internal/usecase/cmdsync/app.go`: Sync and syncack orchestration.
- `internal/usecase/cmdsync/records.go`: UID-owned in-memory sync record generation cache.
- `internal/usecase/cmdsync/projector.go`: Durable CMD committed-message projector.
- `internal/usecase/cmdsync/app_test.go`: Sync/syncack usecase tests.
- `internal/usecase/cmdsync/records_test.go`: Sync generation cache tests.
- `internal/usecase/cmdsync/projector_test.go`: CMD projection recipient/read-state tests.
- `internal/access/api/message_sync.go`: Legacy `/message/sync` and `/message/syncack` HTTP adapters.
- `internal/access/node/cmdsync_rpc.go`: Node RPC for UID-owner CMD sync/syncack forwarding.
- `internal/access/node/cmdsync_codec.go`: Binary codec for CMD sync RPC.
- `internal/access/node/cmdsync_rpc_test.go`: Node RPC tests.
- `internal/app/cmdsync.go`: App facts adapter and UID-owner routing wrapper.
- `internal/app/cmdsync_test.go`: App adapter/routing tests.

### Modified Files

- `pkg/slot/meta/catalog.go`: Add `TableIDCMDConversationState`, table descriptor, column/index constants.
- `pkg/slot/meta/batch.go`: Add batch staging for CMD upsert/read-advance.
- `pkg/slot/meta/codec_test.go`: Include CMD state methods in codec coverage if the test enumerates store APIs.
- `pkg/slot/fsm/command.go`: Add CMD state FSM command types and TLV encoders/decoders.
- `pkg/slot/fsm/state_machine_test.go`: Add apply tests for CMD state upsert/read-advance.
- `pkg/slot/proxy/store.go`: Register the CMD state proxy RPC service.
- `pkg/slot/proxy/integration_test.go`: Add multi-node UID-owner CMD state routing tests.
- `internal/app/app.go`: Add `cmdSyncApp`, `cmdSyncProjector`, lifecycle state, and test hooks/getter if needed by tests.
- `internal/app/build.go`: Instantiate cmdsync, add to committed fanout/replay, and later wire API/node access after those contracts exist.
- `internal/app/lifecycle.go`: Start/stop the CMD projector if it has a background queue.
- `internal/app/lifecycle_components.go`: Insert CMD projector lifecycle before committed dispatch/replay.
- `internal/app/lifecycle_test.go`: Verify lifecycle start/stop order includes CMD projector.
- `internal/app/committed_replay.go`: Replay durable CMD messages into the CMD projector without scoped UIDs.
- `internal/app/committed_replay_test.go`: Verify replay calls CMD projector for command messages.
- `internal/access/api/server.go`: Add `CMDSyncUsecase` interface, options, server field.
- `internal/access/api/routes.go`: Register `/message/sync` and `/message/syncack`.
- `internal/access/api/server_test.go`: Add legacy API mapping/validation tests.
- `internal/access/node/options.go`: Add CMDSync provider to node adapter and register RPC handler.
- `internal/access/node/service_ids.go`: Allocate a non-conflicting CMD sync service ID.
- `docs/raw/send-path-business-logic-diff.md`: Mark P2d-c behavior restored and note remaining P2d-d replay boundary.
- `internal/FLOW.md`: Add CMD sync usecase/API/projection flow if the final implementation changes the documented internal flow.
- `pkg/slot/FLOW.md`: Add CMD conversation state table/proxy/FSM entries.
- `AGENTS.md`: Add `internal/usecase/cmdsync` to the directory structure because this plan creates a new package.

## Implementation Tasks

### Task 1: Dedicated CMD Conversation State In Meta Store

**Files:**
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/batch.go`
- Create: `pkg/slot/meta/cmd_conversation_state.go`
- Create: `pkg/slot/meta/cmd_conversation_state_test.go`
- Modify: `pkg/slot/meta/codec_test.go` if the API list is explicit

- [ ] **Step 1: Write failing tests for CMD state isolation and ordering**

Add tests to `pkg/slot/meta/cmd_conversation_state_test.go`:

```go
func TestShardListCMDConversationActiveReturnsOnlyCMDRows(t *testing.T) {
    ctx := context.Background()
    db := mustOpenTestDB(t)
    shard := db.ForHashSlot(7)

    require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100}))
    require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 200}))

    cmdRows, err := shard.ListCMDConversationActive(ctx, "u1", 10)
    require.NoError(t, err)
    require.Equal(t, []CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100}}, cmdRows)

    chatRows, err := shard.ListUserConversationActive(ctx, "u1", 10)
    require.NoError(t, err)
    require.Equal(t, []UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 200}}, chatRows)
}

func TestShardAdvanceCMDConversationReadSeqIsMonotonic(t *testing.T) {
    ctx := context.Background()
    db := mustOpenTestDB(t)
    shard := db.ForHashSlot(7)
    require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 5, ActiveAt: 100}))

    require.NoError(t, shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 4, UpdatedAt: 200}))
    got, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
    require.NoError(t, err)
    require.Equal(t, uint64(5), got.ReadSeq)

    require.NoError(t, shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 300}))
    got, err = shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
    require.NoError(t, err)
    require.Equal(t, uint64(9), got.ReadSeq)
    require.Equal(t, int64(300), got.UpdatedAt)
}
```

Also cover:

- active index desc ordering and stale index cleanup;
- upsert preserving max `ReadSeq`, max `DeletedToSeq`, max `ActiveAt`, and max `UpdatedAt`;
- missing `AdvanceCMDConversationReadSeq` is a no-op;
- write-batch upsert/read-advance sees earlier batch writes.

- [ ] **Step 2: Run meta tests and verify they fail**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta -run 'CMDConversation|UserConversation' -count=1
```

Expected: FAIL with undefined `CMDConversationState` / methods.

- [ ] **Step 3: Add CMD table descriptor and constants**

In `pkg/slot/meta/catalog.go`, add table ID 8 and descriptors:

```go
const (
    TableIDCMDConversationState uint32 = 8
)

const (
    cmdConversationStatePrimaryFamilyID uint16 = 0
    cmdConversationStatePrimaryIndexID  uint16 = 1
    cmdConversationStateActiveIndexID   uint16 = 2

    cmdConversationStateColumnIDUID          uint16 = 1
    cmdConversationStateColumnIDChannelID    uint16 = 2
    cmdConversationStateColumnIDChannelType  uint16 = 3
    cmdConversationStateColumnIDReadSeq      uint16 = 4
    cmdConversationStateColumnIDDeletedToSeq uint16 = 5
    cmdConversationStateColumnIDActiveAt     uint16 = 6
    cmdConversationStateColumnIDUpdatedAt    uint16 = 7
)
```

Use table name `cmd_conversation_state`; primary and active indexes mirror `UserConversationStateTable` but use the CMD table ID.

- [ ] **Step 4: Implement CMD state model and shard methods**

In `pkg/slot/meta/cmd_conversation_state.go`, define English-commented exported structs:

```go
// CMDConversationState stores one user's durable command-channel sync cursor.
type CMDConversationState struct {
    UID          string
    ChannelID    string
    ChannelType  int64
    ReadSeq      uint64
    DeletedToSeq uint64
    ActiveAt     int64
    UpdatedAt    int64
}

// CMDConversationReadPatch advances one CMD conversation read cursor.
type CMDConversationReadPatch struct {
    UID         string
    ChannelID   string
    ChannelType int64
    ReadSeq     uint64
    UpdatedAt   int64
}
```

Implement:

```go
func (s *ShardStore) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (CMDConversationState, error)
func (s *ShardStore) UpsertCMDConversationState(ctx context.Context, state CMDConversationState) error
func (s *ShardStore) AdvanceCMDConversationReadSeq(ctx context.Context, patch CMDConversationReadPatch) error
func (s *ShardStore) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]CMDConversationState, error)
```

Rules:

- Reuse validation style from `validateConversationUID`, `validateConversationKey`, and `validateConversationLimit`.
- Primary key and active index must use `CMDConversationStateTable.ID`, not `UserConversationStateTable.ID`.
- `UpsertCMDConversationState` merges monotonically: never decrease `ReadSeq`, `DeletedToSeq`, `ActiveAt`, or `UpdatedAt`.
- `AdvanceCMDConversationReadSeq` is a no-op for missing state and stale/lower read seq.
- If active_at changes, delete the old CMD active index key and write the new one.

- [ ] **Step 5: Add WriteBatch support**

In `pkg/slot/meta/batch.go`:

- Add `cmdConversationStates map[string]cmdConversationStateBatchEntry`.
- Add `cmdConversationStateBatchEntry`.
- Add methods:

```go
func (b *WriteBatch) UpsertCMDConversationState(hashSlot uint16, state CMDConversationState) error
func (b *WriteBatch) AdvanceCMDConversationReadSeq(hashSlot uint16, patches []CMDConversationReadPatch) error
```

Use the same same-batch visibility pattern as `loadUserConversationState`.

- [ ] **Step 6: Run meta tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta -count=1
```

Expected: PASS.

Commit:

```bash
git add pkg/slot/meta/catalog.go pkg/slot/meta/batch.go pkg/slot/meta/cmd_conversation_state.go pkg/slot/meta/cmd_conversation_state_test.go pkg/slot/meta/codec_test.go
git commit -m "feat: add cmd conversation state store"
```

### Task 2: Slot FSM And Proxy RPC For CMD State

**Files:**
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/proxy/store.go`
- Create: `pkg/slot/proxy/cmd_conversation_state_rpc.go`
- Create: `pkg/slot/proxy/cmd_conversation_state_codec.go`
- Create: `pkg/slot/proxy/cmd_conversation_state_rpc_test.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing FSM command tests**

In `pkg/slot/fsm/state_machine_test.go`, add tests that apply:

```go
EncodeUpsertCMDConversationStatesCommand([]metadb.CMDConversationState{{
    UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100,
}})

EncodeAdvanceCMDConversationReadSeqCommand([]metadb.CMDConversationReadPatch{{
    UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 7, UpdatedAt: 200,
}})
```

Assert `ApplyResultOK` and read the persisted state from `db.ForHashSlot(hashSlot).GetCMDConversationState(ctx, "u1", "g1____cmd", 2)`.

- [ ] **Step 2: Run FSM test and verify it fails**

Run:

```bash
GOWORK=off go test ./pkg/slot/fsm -run 'CMDConversation' -count=1
```

Expected: FAIL with undefined command encoders.

- [ ] **Step 3: Implement FSM command types**

In `pkg/slot/fsm/command.go`:

- Do not reuse removed/reserved command types 13 or 14.
- Add:

```go
cmdTypeUpsertCMDConversationStates   uint8 = 17
cmdTypeAdvanceCMDConversationReadSeq uint8 = 18
```

- Add TLV encoders/decoders for:

```go
func EncodeUpsertCMDConversationStatesCommand(states []metadb.CMDConversationState) []byte
func EncodeAdvanceCMDConversationReadSeqCommand(patches []metadb.CMDConversationReadPatch) []byte
```

- Apply via `wb.UpsertCMDConversationState(hashSlot, state)` and `wb.AdvanceCMDConversationReadSeq(hashSlot, patches)`.
- Keep decode forward-compatible by ignoring unknown TLV tags, matching existing command style.

- [ ] **Step 4: Write failing proxy RPC tests**

In `pkg/slot/proxy/cmd_conversation_state_rpc_test.go` and/or `pkg/slot/proxy/integration_test.go`, cover:

- Binary codec round-trips `get`, `list_active`, `upsert`, and `advance_read` requests.
- `Store.UpsertCMDConversationStates` groups writes by UID slot/hash slot.
- `Store.ListCMDConversationActive` reads from the authoritative UID slot.
- `Store.AdvanceCMDConversationReadSeq` routes to the UID owner and does not affect chat `UserConversationState`.

- [ ] **Step 5: Implement proxy RPC**

Use a dedicated proxy service ID that does not conflict with current app/node services:

```go
const cmdConversationStateRPCServiceID uint8 = 14
```

In `pkg/slot/proxy/store.go`, register:

```go
cluster.RPCMux().Handle(cmdConversationStateRPCServiceID, store.handleCMDConversationStateRPC)
```

In `pkg/slot/proxy/cmd_conversation_state_rpc.go`, implement distributed APIs:

```go
func (s *Store) GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.CMDConversationState, error)
func (s *Store) ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]metadb.CMDConversationState, error)
func (s *Store) UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
func (s *Store) AdvanceCMDConversationReadSeq(ctx context.Context, patches []metadb.CMDConversationReadPatch) error
```

Use `slotID := s.cluster.SlotForKey(uid)` and `hashSlotForKey(s.cluster, uid)` for all UID-owned state operations.

- [ ] **Step 6: Run FSM/proxy tests and commit**

Run:

```bash
GOWORK=off go test ./pkg/slot/fsm ./pkg/slot/proxy -run 'CMDConversation|ConversationState' -count=1
```

Expected: PASS.

Commit:

```bash
git add pkg/slot/fsm/command.go pkg/slot/fsm/state_machine_test.go pkg/slot/proxy/store.go pkg/slot/proxy/cmd_conversation_state_rpc.go pkg/slot/proxy/cmd_conversation_state_codec.go pkg/slot/proxy/cmd_conversation_state_rpc_test.go pkg/slot/proxy/integration_test.go
git commit -m "feat: route cmd conversation state through slots"
```

### Task 3: CMD Sync Record Cache And Sync/Ack Usecase

**Files:**
- Create: `internal/usecase/cmdsync/types.go`
- Create: `internal/usecase/cmdsync/records.go`
- Create: `internal/usecase/cmdsync/app.go`
- Create: `internal/usecase/cmdsync/records_test.go`
- Create: `internal/usecase/cmdsync/app_test.go`

- [ ] **Step 1: Write failing sync record cache tests**

In `internal/usecase/cmdsync/records_test.go`, test:

```go
func TestSyncRecordCacheLatestGenerationWins(t *testing.T) {
    cache := NewSyncRecordCache(SyncRecordCacheOptions{TTL: time.Minute, MaxUIDs: 10, MaxRecordsPerUID: 10, Now: fakeNow})
    cache.Replace("u1", []SyncRecord{{CommandChannelID: "g1____cmd", ChannelType: 2, LastReturnedMsgSeq: 3}})
    cache.Replace("u1", []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}})

    records := cache.Pop("u1")
    require.Equal(t, []SyncRecord{{CommandChannelID: "g2____cmd", ChannelType: 2, LastReturnedMsgSeq: 9}}, records)
    require.Empty(t, cache.Pop("u1"))
}
```

Also cover TTL expiry, empty replace clearing previous generation, max records per UID truncation/rejection according to the chosen implementation, and UID capacity eviction.

- [ ] **Step 2: Write failing usecase sync/ack tests**

In `internal/usecase/cmdsync/app_test.go`, use fake state store and fake message store. Cover:

- `Sync` loads CMD active rows and fetches each command channel from `ReadSeq + 1`.
- Response strips one `____cmd` suffix while records keep command channel IDs.
- Global limit is applied after deterministic merge/sort.
- Records store only the last sequence actually returned per channel.
- Empty channels do not create records.
- `SyncAck` advances only records popped from latest generation.
- Duplicate `SyncAck` is a no-op.
- `last_message_seq` does not blindly advance all CMD states.
- Missing CMD state during ack is ignored.

- [ ] **Step 3: Run cmdsync tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 4: Implement public usecase types and interfaces**

In `internal/usecase/cmdsync/types.go`:

```go
// SyncQuery is the legacy /message/sync request after access-layer mapping.
type SyncQuery struct {
    UID        string
    MessageSeq uint64
    Limit      int
}

// SyncAckCommand is the legacy /message/syncack request after validation.
type SyncAckCommand struct {
    UID            string
    LastMessageSeq uint64
}

// SyncResult contains durable CMD messages ready for legacy response mapping.
type SyncResult struct {
    Messages []channel.Message
}

type StateStore interface {
    ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]metadb.CMDConversationState, error)
    AdvanceCMDConversationReadSeq(ctx context.Context, patches []metadb.CMDConversationReadPatch) error
}

type MessageStore interface {
    LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error)
}

type CommandChannelKey struct {
    ChannelID   string
    ChannelType uint8
}
```

Add exported errors for missing UID, invalid limit if needed, and missing dependencies.

- [ ] **Step 5: Implement `SyncRecordCache`**

In `internal/usecase/cmdsync/records.go`, implement:

```go
type SyncRecord struct {
    CommandChannelID    string
    ChannelType         uint8
    LastReturnedMsgSeq  uint64
}

type SyncRecordCache struct { /* mutex protected */ }

func NewSyncRecordCache(opts SyncRecordCacheOptions) *SyncRecordCache
func (c *SyncRecordCache) Replace(uid string, records []SyncRecord)
func (c *SyncRecordCache) Pop(uid string) []SyncRecord
```

Defaults:

- TTL: 5 minutes.
- MaxUIDs: 4096.
- MaxRecordsPerUID: 2048.

Requirements:

- Each successful `Sync` replaces the previous UID generation.
- `SyncAck` atomically reads and clears only the latest generation.
- Losing/expiring records is safe and produces no-op acks.

- [ ] **Step 6: Implement `App.Sync`**

In `internal/usecase/cmdsync/app.go`:

```go
type Options struct {
    States          StateStore
    Messages        MessageStore
    Records         *SyncRecordCache
    Now             func() time.Time
    ActiveScanLimit int
    DefaultLimit    int
    MaxLimit        int
    Logger          wklog.Logger
}

type App struct { /* dependencies */ }

func New(opts Options) *App
func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error)
func (a *App) SyncAck(ctx context.Context, cmd SyncAckCommand) error
```

Sync behavior:

- Trim and validate `UID`.
- Accept `MessageSeq` but do not use it for selection.
- Normalize `Limit`: default 200, max 10000 unless options override.
- Load active CMD states with `ActiveScanLimit` default 2000.
- For each state, fetch `fromSeq := max(ReadSeq, DeletedToSeq) + 1` from `state.ChannelID`.
- Fail fast on a real message fetch error and do not replace sync records on failure.
- Treat missing/not-ready channel errors as empty in the app facts adapter, not in core usecase.
- Merge candidate messages and sort ascending by `(Timestamp, commandChannelID, ChannelType, MessageSeq, MessageID)`.
- Return at most global `Limit` messages.
- Convert each returned message's `ChannelID` by stripping one `____cmd` suffix.
- Replace sync records with only channels that returned messages, using the last returned seq per command channel.

- [ ] **Step 7: Implement `App.SyncAck`**

Rules:

- Validate trimmed `UID`.
- Accept `LastMessageSeq` only as a compatibility field; the API layer will reject zero.
- Pop latest records from the cache; if empty, return nil.
- Skip records with non-command channel IDs or zero seq.
- Persist patches using `AdvanceCMDConversationReadSeq`.
- `UpdatedAt` is `now().UnixNano()`.

- [ ] **Step 8: Run cmdsync tests and commit**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/usecase/cmdsync
git commit -m "feat: add cmd sync usecase"
```

### Task 4: Durable CMD Projector

**Files:**
- Modify/Create: `internal/usecase/cmdsync/projector.go`
- Create/Modify: `internal/usecase/cmdsync/projector_test.go`

- [ ] **Step 1: Write failing projector tests**

In `internal/usecase/cmdsync/projector_test.go`, cover:

```go
func TestProjectorCreatesCMDStateForGroupSubscribers(t *testing.T) { /* command channel g1____cmd, subscribers u1/u2 */ }
func TestProjectorMarksSenderReadWhenSenderIsRecipient(t *testing.T) { /* FromUID u1 included => ReadSeq == MessageSeq */ }
func TestProjectorDoesNotCreateSenderStateWhenSenderIsNotRecipient(t *testing.T) { /* FromUID not in subscribers */ }
func TestProjectorUsesMessageScopedUIDsExactly(t *testing.T) { /* temp____cmd + scoped uids, no store subscriber scan */ }
func TestProjectorIgnoresTempCommandWithoutScopedUIDs(t *testing.T) { /* replay boundary: no scoped snapshot, no rescan, no state */ }
func TestProjectorSubmitCommittedDoesNotBlockOnSubscriberScan(t *testing.T) { /* blocking resolver/store must not block enqueue */ }
func TestProjectorIgnoresOrdinaryChatMessages(t *testing.T) { /* g1 non SyncOnce */ }
func TestProjectorIgnoresNoPersistOrZeroSeqMessages(t *testing.T) { /* NoPersist/MessageSeq 0 */ }
```

Use a fake `delivery.SubscriberResolver` or the real resolver with a fake subscriber store where practical.

- [ ] **Step 2: Run projector tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -run 'Projector' -count=1
```

Expected: FAIL with missing projector.

- [ ] **Step 3: Implement projector options and interface**

In `projector.go`, implement a committed subscriber compatible with `internal/app.committedFanout`:

```go
// Projector projects durable command-channel commits into UID-owned CMD sync state.
type Projector interface {
    Start() error
    Stop() error
    Flush(ctx context.Context) error
    SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error
}
```

Options:

```go
type ProjectorOptions struct {
    Store          ProjectorStore
    Subscribers    delivery.SubscriberResolver
    QueueSize      int
    PageSize       int
    FlushInterval  time.Duration
    Now            func() time.Time
    Async          func(func())
    Logger         wklog.Logger
}
```

`ProjectorStore` only needs:

```go
UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
```

- [ ] **Step 4: Implement durable CMD detection, non-blocking enqueue, and recipient expansion**

Rules:

- `SubmitCommitted` must only validate/clone/enqueue the event and return; subscriber paging and state writes run in the projector worker or `Flush`, never inline on the sendack path.
- Use a bounded queue; on overflow, drop the CMD projection event and log a warning rather than blocking the caller.
- `Flush(ctx)` drains queued work synchronously for tests/shutdown.
- Ignore nil projector/store.
- Ignore `MessageSeq == 0` or `msg.Framer.NoPersist`.
- Process only `runtimechannelid.IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce`.
- Ensure state key is a command channel:

```go
commandChannelID := msg.ChannelID
if !runtimechannelid.IsCommandChannel(commandChannelID) {
    commandChannelID = runtimechannelid.ToCommandChannel(commandChannelID)
}
```

- If `event.MessageScopedUIDs` is non-empty, use that exact unique list.
- If `msg.ChannelType == frame.ChannelTypeTemp` and `event.MessageScopedUIDs` is empty, return without creating state; this is committed replay for request-scoped CMD without a persisted subscriber snapshot and remains P2d-d.
- Otherwise page subscribers via `delivery.SubscriberResolver` against the command channel ID; this uses source-channel authority internally.
- Batch writes by page.
- For each UID, create `metadb.CMDConversationState{UID, ChannelID: commandChannelID, ChannelType: int64(msg.ChannelType), ActiveAt: activeAtFromMessage(msg), UpdatedAt: now}`.
- If `uid == msg.FromUID`, set `ReadSeq = msg.MessageSeq`.

- [ ] **Step 5: Run projector tests and commit**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/usecase/cmdsync/projector.go internal/usecase/cmdsync/projector_test.go
git commit -m "feat: project durable cmd conversations"
```

### Task 5: App Local CMD Message Facts, Projector Lifecycle, And Replay

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_components.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/committed_replay.go`
- Modify: `internal/app/committed_replay_test.go`
- Create: `internal/app/cmdsync.go`
- Create: `internal/app/cmdsync_test.go`

- [ ] **Step 1: Write failing app local adapter tests**

In `internal/app/cmdsync_test.go`, cover:

- `cmdsyncMessageStore.LoadCommandMessages` reads local command-channel messages starting at `fromSeq`.
- Remote command-channel owner is addressed through existing `accessnode.QueryChannelMessages` with `SyncMode: true`, `StartSeq: fromSeq`, `PullMode: up`, and command channel ID.
- `channel.ErrNotReady` and `channel.ErrChannelNotFound` produce an empty message slice.
- Committed fanout includes the CMD projector but still submits delivery/conversation side effects.
- Committed replay calls the CMD projector for durable command messages.
- Committed replay of `frame.ChannelTypeTemp` without `MessageScopedUIDs` does not create CMD state because request-scoped replay recovery is P2d-d.

Do not wire API/node CMD sync forwarding in this task; those contracts are added in Tasks 6 and 7.

- [ ] **Step 2: Run app tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/app -run 'CMD|CommittedReplay|Lifecycle|Build' -count=1
```

Expected: FAIL because app cmdsync adapters/lifecycle are missing.

- [ ] **Step 3: Implement command-channel message facts adapter**

In `internal/app/cmdsync.go`, implement:

```go
type cmdsyncMessageStore struct {
    localNodeID uint64
    channelLog  *channelstore.Engine
    metas       interface { GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) }
    remote      interface { QueryChannelMessages(context.Context, uint64, accessnode.ChannelMessagesQuery) (accessnode.ChannelMessagesPage, error) }
}

func (s cmdsyncMessageStore) LoadCommandMessages(ctx context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error)
```

Implementation details:

- Require command `key.ChannelID`; do not strip it here.
- `meta, err := metas.GetChannelRuntimeMeta(ctx, key.ChannelID, int64(key.ChannelType))`.
- `minAvailableSeq := channel.EffectiveMinAvailableSeq(meta.RetentionThroughSeq, 0)`.
- If `meta.Leader == localNodeID`, use `channelhandler.LoadCommittedHW` and `channelhandler.SyncMessages`.
- If remote, call `remote.QueryChannelMessages(ctx, meta.Leader, accessnode.ChannelMessagesQuery{ChannelID: channel.ChannelID{ID:key.ChannelID, Type:key.ChannelType}, SyncMode:true, StartSeq:fromSeq, Limit:limit, PullMode:uint8(channelhandler.SyncPullModeUp), MinAvailableSeq:minAvailableSeq})`.
- Return copies of messages.

- [ ] **Step 4: Wire local cmdsync runtime without API/node forwarding**

In `internal/app/app.go`, add fields:

```go
cmdSyncApp       *cmdsync.App
cmdSyncProjector cmdsync.Projector
```

In `internal/app/build.go`:

- Instantiate `cmdSyncApp` after `app.store` exists:

```go
app.cmdSyncApp = cmdsync.New(cmdsync.Options{
    States:   app.store,
    Messages: cmdsyncMessageStore{localNodeID: cfg.Node.ID, channelLog: app.channelLogDB, metas: app.store, remote: app.nodeClient},
    Logger:   app.logger.Named("cmdsync"),
})
```

- Instantiate `cmdSyncProjector` after `subscriberResolver` exists:

```go
app.cmdSyncProjector = cmdsync.NewProjector(cmdsync.ProjectorOptions{
    Store:       app.store,
    Subscribers: subscriberResolver,
    Logger:      app.logger.Named("cmdsync.projector"),
})
```

- Include projector in live committed fanout:

```go
committedDispatcher := committedFanout{subscribers: []committedSubscriber{app.committedDispatcher, app.cmdSyncProjector}}
```

Do not yet pass `app.cmdSyncApp` to `accessnode.Options` or `accessapi.Options`; doing so before Tasks 6/7 would make this checkpoint unbuildable.

- [ ] **Step 5: Add CMD projector lifecycle wiring**

Because `cmdsync.Projector` owns a bounded background queue, wire it into app lifecycle before committed dispatcher/replay starts. In `internal/app/lifecycle_components.go`, add a component such as `cmdsync_projector` after `conversation_projector` and before `delivery_runtime`. In `internal/app/lifecycle.go`, add `startCMDSyncProjector` and `stopCMDSyncProjector` methods mirroring `startConversationProjector`/`stopConversationProjector`. In `internal/app/app.go`, add an atomic on/off flag plus test hook fields if lifecycle tests need them. Update `internal/app/lifecycle_test.go` expected order so stop order is the reverse of start order.

- [ ] **Step 6: Update committed replay for CMD projector**

In `internal/app/committed_replay.go`, add a `CMDSync committedSubscriber` field or equivalent. In `submitMessage`, call:

```go
if r.cfg.CMDSync != nil {
    _ = r.cfg.CMDSync.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: msg})
}
```

Important: replay lacks `MessageScopedUIDs`; the Task 4 projector rule must ignore temp CMD replay with empty scoped UIDs, so request-scoped replay recovery remains incomplete and documented as P2d-d.

- [ ] **Step 7: Run app tests and commit**

Run:

```bash
GOWORK=off go test ./internal/app -run 'CMD|CommittedReplay|Lifecycle|Build' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/lifecycle_components.go internal/app/lifecycle_test.go internal/app/committed_replay.go internal/app/committed_replay_test.go internal/app/cmdsync.go internal/app/cmdsync_test.go
git commit -m "feat: wire local cmd sync runtime"
```

### Task 6: Node RPC For UID-Owned CMD Sync APIs

**Files:**
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/service_ids.go`
- Create: `internal/access/node/cmdsync_rpc.go`
- Create: `internal/access/node/cmdsync_codec.go`
- Create: `internal/access/node/cmdsync_rpc_test.go`

- [ ] **Step 1: Write failing node RPC tests**

In `internal/access/node/cmdsync_rpc_test.go`, cover:

- `Client.SyncCMD` encodes query and decodes returned messages.
- `Client.SyncAckCMD` encodes ack command and handles OK.
- Adapter handler calls local CMDSync usecase.
- Missing local CMDSync provider returns a clear error/status.
- Codec round-trips `SyncQuery`, `SyncResult`, `SyncAckCommand`, and `channel.Message` payload/header fields.

- [ ] **Step 2: Run node RPC tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'CMDSync' -count=1
```

Expected: FAIL with missing RPC methods.

- [ ] **Step 3: Add node adapter interface and service ID**

In `internal/access/node/options.go`:

```go
type CMDSyncUsecase interface {
    Sync(ctx context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
    SyncAck(ctx context.Context, cmd cmdsync.SyncAckCommand) error
}
```

Add `CMDSync CMDSyncUsecase` to `Options` and `cmdSync CMDSyncUsecase` to `Adapter`.

In `internal/access/node/service_ids.go`, allocate next free access-node service ID:

```go
cmdSyncRPCServiceID uint8 = 46
```

Register the handler in `New`.

- [ ] **Step 4: Implement node RPC client/handler/codec**

In `internal/access/node/cmdsync_rpc.go`, implement:

```go
func (c *Client) SyncCMD(ctx context.Context, nodeID uint64, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
func (c *Client) SyncAckCMD(ctx context.Context, nodeID uint64, cmd cmdsync.SyncAckCommand) error
func (a *Adapter) handleCMDSyncRPC(ctx context.Context, body []byte) ([]byte, error)
```

Use op IDs `sync` and `sync_ack`. Responses carry `Status`, optional `Error`, and `Messages`.

In `cmdsync_codec.go`, use a new magic/version, for example:

```go
var cmdSyncRPCRequestMagic  = [...]byte{'W', 'K', 'M', 'S', 1}
var cmdSyncRPCResponseMagic = [...]byte{'W', 'K', 'M', 'T', 1}
```

Reuse existing channel message binary helpers where possible.

- [ ] **Step 5: Run node RPC tests and commit**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'CMDSync|Codec' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/access/node/options.go internal/access/node/service_ids.go internal/access/node/cmdsync_rpc.go internal/access/node/cmdsync_codec.go internal/access/node/cmdsync_rpc_test.go
git commit -m "feat: add cmd sync node rpc"
```

### Task 7: Legacy HTTP `/message/sync` And `/message/syncack`

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Create: `internal/access/api/message_sync.go`
- Modify: `internal/access/api/server_test.go`

- [ ] **Step 1: Write failing API tests**

In `internal/access/api/server_test.go`, add tests for:

- `POST /message/sync` maps `uid`, `message_seq`, and `limit` to `cmdsync.SyncQuery`.
- Response is a bare JSON array of `legacyMessageResp`.
- Returned `ChannelID` is source channel (`g1`), not `g1____cmd`.
- Person channel response still uses `newLegacyMessageResp(uid, msg)` peer-view behavior.
- Negative limit returns legacy JSON error.
- Missing UID returns legacy JSON error.
- `POST /message/syncack` validates nonzero `last_message_seq` and calls usecase.
- Empty/no-op ack returns `200` and `{"status":200}`.

- [ ] **Step 2: Run API tests and verify they fail**

Run:

```bash
GOWORK=off go test ./internal/access/api -run 'MessageSync|MessageSyncAck' -count=1
```

Expected: FAIL with missing routes/fields.

- [ ] **Step 3: Add API interface and route registration**

In `internal/access/api/server.go`:

```go
type CMDSyncUsecase interface {
    Sync(ctx context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
    SyncAck(ctx context.Context, cmd cmdsync.SyncAckCommand) error
}
```

Add `CMDSync CMDSyncUsecase` to `Options` and `cmdSync CMDSyncUsecase` to `Server`.

In `routes.go`:

```go
s.engine.POST("/message/sync", s.handleMessageSync)
s.engine.POST("/message/syncack", s.handleMessageSyncAck)
```

- [ ] **Step 4: Implement `message_sync.go` access adapter**

Request structs:

```go
type messageSyncRequest struct {
    UID        string `json:"uid"`
    MessageSeq uint64 `json:"message_seq"`
    Limit      int    `json:"limit"`
}

type messageSyncAckRequest struct {
    UID            string `json:"uid"`
    LastMessageSeq uint64 `json:"last_message_seq"`
}
```

Handler rules:

- Use `ShouldBindJSON`.
- Trim/validate UID.
- For sync, reject `Limit < 0`; let usecase default/clamp zero/large limits.
- For syncack, reject `LastMessageSeq == 0` with legacy error.
- Use `writeLegacyJSONError` for legacy compatibility.
- Map messages with `newLegacyMessageResp(req.UID, msg)`.
- Return sync response as `[]legacyMessageResp`.
- Return syncack success as `gin.H{"status": http.StatusOK}`.

- [ ] **Step 5: Run API tests and commit**

Run:

```bash
GOWORK=off go test ./internal/access/api -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/access/api/server.go internal/access/api/routes.go internal/access/api/message_sync.go internal/access/api/server_test.go
git commit -m "feat: restore legacy cmd message sync api"
```

### Task 8: Final App API/RPC Wiring And End-To-End Regression Coverage

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/cmdsync.go`
- Modify: `internal/app/cmdsync_test.go`
- Modify: `internal/usecase/message/send_test.go` only if needed for new app-level assertions
- Modify: `internal/usecase/conversation/sync_test.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Implement final UID-owner API/RPC wiring**

Now that Tasks 6 and 7 added the node and API contracts, implement `clusterCMDSyncUsecase` in `internal/app/cmdsync.go`:

```go
type clusterCMDSyncUsecase struct {
    local       *cmdsync.App
    remote      interface {
        SyncCMD(context.Context, uint64, cmdsync.SyncQuery) (cmdsync.SyncResult, error)
        SyncAckCMD(context.Context, uint64, cmdsync.SyncAckCommand) error
    }
    cluster     interface {
        SlotForKey(string) multiraft.SlotID
        LeaderOf(multiraft.SlotID) (multiraft.NodeID, error)
    }
    localNodeID uint64
}
```

Rules:

- Trim/validate UID before routing enough to avoid empty slot lookup.
- Resolve owner via UID `SlotForKey` + `LeaderOf`.
- Local owner calls local app; remote owner calls `SyncCMD` / `SyncAckCMD` node RPC.
- Return infrastructure errors to API unchanged.

In `internal/app/build.go`:

- Pass local `app.cmdSyncApp` to `accessnode.Options{CMDSync: app.cmdSyncApp}`.
- Create `cmdSyncAPI := clusterCMDSyncUsecase{local: app.cmdSyncApp, remote: app.nodeClient, cluster: app.cluster, localNodeID: cfg.Node.ID}` and pass it to `accessapi.Options{CMDSync: cmdSyncAPI}`.

Add tests proving local and remote UID-owner routing for `Sync` and `SyncAck`.

- [ ] **Step 2: Add regression tests for no chat leakage**

In `internal/usecase/conversation/sync_test.go`, add a test where the ordinary state store returns only chat rows and the CMD rows exist in a separate fake store. Assert `/conversation/sync` cannot see CMD state because it has no dependency on the CMD store.

If a broader integration test is practical, add to `internal/app/cmdsync_test.go`:

- create chat `UserConversationState` and CMD `CMDConversationState` for the same UID;
- call ordinary conversation sync;
- assert only chat appears.

- [ ] **Step 3: Add live durable CMD projection + sync + ack integration test**

In `internal/usecase/cmdsync/app_test.go` or `internal/app/cmdsync_test.go`, assemble:

- fake subscribers `g1 -> u1,u2`;
- projector receives `MessageCommitted{Message: channel.Message{ChannelID:"g1____cmd", ChannelType:group, FromUID:"u1", MessageSeq:7, Framer:SyncOnce}}`;
- state for `u1` has `ReadSeq=7`, state for `u2` has `ReadSeq=0`;
- `Sync(uid:u2)` returns message with `ChannelID:"g1"`;
- `SyncAck(uid:u2,last_message_seq:7)` advances only u2 CMD state;
- second `Sync(uid:u2)` returns no messages.

- [ ] **Step 4: Add request-scoped live projection regression**

Test `MessageCommitted{MessageScopedUIDs: []string{"u2","u3"}, Message.ChannelType: frame.ChannelTypeTemp}` creates CMD state only for `u2` and `u3`, not for sender unless included.

- [ ] **Step 5: Add multi-node authority regression**

In `pkg/slot/proxy/integration_test.go` or `internal/app/cmdsync_test.go` with fakes:

- UID owner and command-channel owner are different node IDs.
- `/message/sync` routing goes to UID owner.
- UID owner fetches messages from command-channel owner using `QueryChannelMessages`.

This is the critical regression for “UID-owned CMD state != command-channel log owner”.

- [ ] **Step 6: Run focused regression tests and commit**

Run:

```bash
GOWORK=off go test ./internal/usecase/cmdsync ./internal/usecase/conversation ./internal/app ./pkg/slot/proxy -run 'CMD|ConversationSync|ListUserConversationActive' -count=1
```

Expected: PASS.

Commit:

```bash
git add internal/app/build.go internal/app/cmdsync.go internal/app/cmdsync_test.go internal/usecase/cmdsync internal/usecase/conversation/sync_test.go pkg/slot/proxy/integration_test.go
git commit -m "feat: wire cmd sync api routing"
```

### Task 9: Documentation And Flow Updates

**Files:**
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `internal/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Modify: `pkg/channel/FLOW.md` only if channel message sync/facts behavior is changed
- Modify: `AGENTS.md`

- [ ] **Step 1: Update raw business diff**

In `docs/raw/send-path-business-logic-diff.md`, record:

- `/message/sync` restored for durable CMD messages.
- `/message/syncack` restored and advances only latest sync record generation.
- CMD state is dedicated and UID-owned.
- Messages are fetched from `source____cmd` but returned as source-channel client views.
- `NoPersist` CMD remains online-only.
- Request-scoped replay recovery remains P2d-d.

- [ ] **Step 2: Update internal flow doc**

In `internal/FLOW.md`, add or adjust:

- Component list includes `cmdsync.App` and CMD projector.
- Committed event flow includes ordinary delivery/conversation and CMD sync projection.
- API list includes `/message/sync` and `/message/syncack`.
- Explicitly state ordinary `/conversation/sync` excludes CMD state.

Use “单节点集群” when describing deployment shape.

- [ ] **Step 3: Update slot flow doc**

In `pkg/slot/FLOW.md`, add:

- `CMDConversationStateTable` / table ID 8.
- FSM command types 17/18.
- Proxy service `cmdConversationStateRPCServiceID` if a service list exists.

- [ ] **Step 4: Update AGENTS directory structure**

In `AGENTS.md`, add the new package under `internal/usecase/`:

```text
    cmdsync/             CMD 离线同步、syncack 与独立 CMD 会话状态用例
```

Keep the description short and consistent with the existing directory tree.

- [ ] **Step 5: Run doc-adjacent tests and commit**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

Commit:

```bash
git add docs/raw/send-path-business-logic-diff.md internal/FLOW.md pkg/slot/FLOW.md pkg/channel/FLOW.md AGENTS.md
git commit -m "docs: document cmd sync restoration"
```

### Task 10: Final Verification

**Files:**
- No new implementation files unless fixing failures.

- [ ] **Step 1: Run storage and slot verification**

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 2: Run cmd sync and access/app verification**

```bash
GOWORK=off go test ./internal/usecase/cmdsync ./internal/usecase/conversation ./internal/access/api ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run send/delivery regression verification**

```bash
GOWORK=off go test ./internal/usecase/message ./internal/usecase/delivery -count=1
```

Expected: PASS.

- [ ] **Step 4: Run import boundary verification**

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 5: Inspect git status**

```bash
git status --short
```

Expected: only intentional P2d-c files are modified. Ignore the pre-existing untracked file `docs/superpowers/specs/2026-05-12-channel-cluster-leader-transfer-p06-design.md` unless the user asks otherwise.

- [ ] **Step 6: Final commit if any verification fixes were needed**

```bash
git add pkg/slot/meta pkg/slot/fsm pkg/slot/proxy internal/usecase/cmdsync internal/access/api internal/access/node internal/app docs/raw/send-path-business-logic-diff.md internal/FLOW.md pkg/slot/FLOW.md pkg/channel/FLOW.md AGENTS.md
git commit -m "fix: stabilize cmd sync integration"
```

## Implementation Notes

- Keep `internal/access/*` as adapters only. Do not put sync business rules in HTTP or node RPC handlers.
- Keep `internal/usecase/cmdsync` independent of `internal/access/*` and `internal/app/*`.
- Do not import `cmdsync` from ordinary conversation usecase; isolation should be structural.
- Add English comments to new exported structs, interfaces, and important fields.
- Do not change existing ordinary conversation state keys or active indexes.
- Do not reuse table ID 7 or FSM command types 13/14; they are reserved for removed conversation projection.
- Do not claim request-scoped replay parity. Live committed request-scoped projection is restored; replay recovery remains deferred.
- CMD projector queue overflow is best-effort: non-request-scoped durable CMD can be repaired by committed replay, while request-scoped overflow remains part of the P2d-d durable snapshot gap and must be logged/observable.
- `ActiveScanLimit` bounds each `/message/sync` pass. If a UID has more active CMD conversations than the cap, lower-priority conversations are omitted from that response; add pagination only in a later design if compatibility tests require it.
