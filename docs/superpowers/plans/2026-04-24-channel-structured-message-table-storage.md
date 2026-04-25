# Channel Structured Message Table Storage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `pkg/channel/store` raw durable payload storage with a catalog-driven structured `message` table, switch handler reads/queries/idempotency to table/index lookups, and preserve existing replica offset/HW/checkpoint semantics.

**Architecture:** Keep `channel.Record` as a compatibility ingress/egress format, but move Pebble persistence to a new `message` table with `primary` and `payload` column families plus direct secondary indexes for `message_id`, `client_msg_no`, and `(from_uid, client_msg_no)`. Refactor `Append`, `Read`, `StoreApplyFetch`, truncation, and handler query paths to use a shared `messageTable` + `messageRow` runtime, while leaving `checkpoint/history/snapshot` as system-state keys and preserving `message_seq = offset + 1` semantics.

**Tech Stack:** Go, Pebble, `pkg/channel/store`, `pkg/channel/handler`, existing commit/checkpoint coordinators, `testing`, `testify`, `go test`.

**Spec:** `docs/superpowers/specs/2026-04-24-channel-structured-message-table-storage-design.md`

---

## Execution Notes

- Use `@superpowers/test-driven-development` on every implementation task: write the failing test first, run it to see the right failure, then write the minimum code to pass.
- Keep the store source of truth as structured rows only. Any `channel.Record.Payload` bytes used after this refactor are compatibility codec output, not Pebble primary values.
- Preserve current runtime semantics: `LEO`, `HW`, `CheckpointHW`, `Truncate(to)`, and `StoreApplyFetch(...)` must remain offset-based externally even after switching storage to `message_seq` primary keys.
- `StoreApplyFetch(...)` must derive `message_seq` from the post-truncate local `LEO` supplied by replica sequencing, never from a separate counter.
- `message_id` is mandatory and non-zero for persisted rows; a zero `message_id` is invalid input or corruption.
- Only write `idx_client_msg_no` when `client_msg_no != ""`; only write `uidx_from_uid_client_msg_no` when `from_uid != "" && client_msg_no != ""`.
- Do not introduce a shared cross-package generic table engine. Keep the catalog/runtime minimal and scoped to `pkg/channel/store`.
- Keep `checkpoint/history/snapshot` in system-state keys. Do not fold them into the `message` table.
- Update `pkg/channel/FLOW.md` in the same implementation as the storage/query behavior change so the docs never lag the code.
- Before claiming the implementation is complete, run `@superpowers/verification-before-completion` with fresh test output.

## File Map

| Path | Responsibility |
|------|----------------|
| `pkg/channel/store/catalog.go` | New table/catalog declarations for `message`, columns, families, and indexes |
| `pkg/channel/store/message_row.go` | New persistent row model plus conversions to/from `channel.Message` and `channel.Record` |
| `pkg/channel/store/table_codec.go` | New state/index/system key encoders plus family/index value codecs |
| `pkg/channel/store/message_table.go` | New table runtime for append/get/scan/truncate/index maintenance |
| `pkg/channel/store/keys.go` | Replace raw log/idempotency key layout with state/index/system helpers |
| `pkg/channel/store/logstore.go` | Rebuild append/read/LEO/truncate compatibility APIs on top of `messageTable` |
| `pkg/channel/store/channel_store.go` | Expose handler-facing structured query methods (`GetMessageBySeq`, `GetMessageByMessageID`, `ListMessagesBySeq`, `ListMessagesByClientMsgNo`, `LookupIdempotency`) |
| `pkg/channel/store/commit.go` | Rebuild `StoreApplyFetch(...)` and committed replay helpers on structured rows |
| `pkg/channel/store/codec.go` | Keep checkpoint/history/idempotency snapshot codecs aligned with the new table/index payload formats |
| `pkg/channel/store/idempotency.go` | Convert idempotency API to unique-index lookups/snapshots or remove obsolete raw-key helpers |
| `pkg/channel/store/checkpoint.go` | Move checkpoint keys to the new system keyspace without changing semantics |
| `pkg/channel/store/history.go` | Move history keys to the new system keyspace without changing semantics |
| `pkg/channel/store/snapshot.go` | Move snapshot keys to the new system keyspace without changing semantics |
| `pkg/channel/store/testenv_test.go` | Add helpers for appending/reading structured messages in store tests |
| `pkg/channel/store/logstore_test.go` | Cover append/read/LEO/restart behavior through the compatibility API |
| `pkg/channel/store/apply_fetch_test.go` | Cover structured apply-fetch writes, seq preservation, checkpoint coupling |
| `pkg/channel/store/idempotency_test.go` | Cover unique-index idempotency hits/conflicts/snapshot behavior |
| `pkg/channel/store/channel_store_test.go` | High-level storage restart/system-state coverage |
| `pkg/channel/handler/append.go` | Switch idempotency hits from raw offset lookup to structured index lookup |
| `pkg/channel/handler/seq_read.go` | Replace log-record decoding with direct table reads/scans |
| `pkg/channel/handler/fetch.go` | Replace compatibility `Read(...)` loop with structured row reads if appropriate |
| `pkg/channel/handler/message_query.go` | Route `MessageID` / `ClientMsgNo` queries to direct indexes |
| `pkg/channel/handler/codec.go` | Keep compatibility durable payload codec, but make it purely ingress/egress glue |
| `pkg/channel/handler/append_test.go` | Lock idempotency/index-backed append behavior |
| `pkg/channel/handler/seq_read_test.go` | Lock direct table reads and seq semantics |
| `pkg/channel/handler/message_query_test.go` | Lock direct `MessageID` / `ClientMsgNo` query behavior and paging |
| `pkg/channel/FLOW.md` | Update storage and query flow documentation |

## Task 1: Add the message catalog, row model, and table codecs

**Files:**
- Create: `pkg/channel/store/catalog.go`
- Create: `pkg/channel/store/message_row.go`
- Create: `pkg/channel/store/table_codec.go`
- Modify: `pkg/channel/store/keys.go`
- Test: `pkg/channel/store/table_codec_test.go`
- Test: `pkg/channel/store/message_row_test.go`

- [ ] **Step 1: Write failing tests for catalog shape and row round-trips**

Add tests in `pkg/channel/store/table_codec_test.go` and `pkg/channel/store/message_row_test.go` that lock these requirements:

```go
func TestMessageTableCatalogDeclaresPrimaryPayloadAndIndexes(t *testing.T) {
    require.Equal(t, "message", MessageTable.Name)
    require.Len(t, MessageTable.Families, 2)
    require.Equal(t, "primary", MessageTable.Families[0].Name)
    require.Equal(t, "payload", MessageTable.Families[1].Name)
    require.Equal(t, "uidx_message_id", MessageTable.SecondaryIndexes[0].Name)
}

func TestMessageRowRoundTripPreservesStructuredFields(t *testing.T) {
    row := messageRow{
        MessageSeq:  9,
        MessageID:   42,
        ClientMsgNo: "c-1",
        FromUID:     "u1",
        ChannelID:   "room",
        ChannelType: 1,
        Payload:     []byte("hello"),
        PayloadHash: 123,
    }

    primary, payload, err := encodeMessageFamilies(row)
    require.NoError(t, err)

    decoded, err := decodeMessageFamilies(9, primary, payload)
    require.NoError(t, err)
    require.Equal(t, row.MessageID, decoded.MessageID)
    require.Equal(t, row.Payload, decoded.Payload)
    require.Equal(t, row.PayloadHash, decoded.PayloadHash)
}

func TestDecodeMessageFamiliesSkipsUnknownColumns(t *testing.T) {
    // encode one valid family plus an unknown column ID entry, then assert decode still succeeds.
}

func TestMessageRowRejectsZeroMessageID(t *testing.T) {
    // attempt to encode or validate a row with message_id == 0 and expect invalid/corrupt input rejection.
}
```

- [ ] **Step 2: Run the focused tests and verify they fail for the right reason**

Run: `go test ./pkg/channel/store -run "TestMessageTableCatalog|TestMessageRowRoundTrip" -count=1`

Expected: FAIL because the catalog, row model, and family codecs do not exist yet.

- [ ] **Step 3: Implement the minimal catalog and row/codec helpers**

Create `pkg/channel/store/catalog.go` with a minimal schema layer mirroring `pkg/slot/meta/catalog.go` concepts:

```go
type ColumnType int

type ColumnDesc struct {
    ID       uint16
    Name     string
    Type     ColumnType
    Nullable bool
}

type ColumnFamilyDesc struct {
    ID              uint16
    Name            string
    ColumnIDs       []uint16
    DefaultColumnID uint16
}

type IndexDesc struct {
    ID        uint16
    Name      string
    Unique    bool
    ColumnIDs []uint16
    Primary   bool
}

type TableDesc struct {
    ID               uint32
    Name             string
    Columns          []ColumnDesc
    Families         []ColumnFamilyDesc
    PrimaryIndex     IndexDesc
    SecondaryIndexes []IndexDesc
}
```

Define the `message` table, including:

- primary key column `message_seq`
- `primary` family for metadata columns
- `payload` family for `payload`
- `uidx_message_id`
- `idx_client_msg_no`
- `uidx_from_uid_client_msg_no`

Create `pkg/channel/store/message_row.go` with:

```go
type messageRow struct {
    MessageSeq  uint64
    MessageID   uint64
    FramerFlags uint8
    Setting     uint8
    StreamFlag  uint8
    MsgKey      string
    Expire      uint32
    ClientSeq   uint64
    ClientMsgNo string
    StreamNo    string
    StreamID    uint64
    Timestamp   int32
    ChannelID   string
    ChannelType uint8
    Topic       string
    FromUID     string
    Payload     []byte
    PayloadHash uint64
}
```

Also add conversions:

```go
func messageRowFromChannelMessage(msg channel.Message) messageRow
func (r messageRow) toChannelMessage() channel.Message
func messageRowFromRecordPayload(payload []byte) (messageRow, error)
func (r messageRow) toRecord() (channel.Record, error)
```

Create `pkg/channel/store/table_codec.go` with helpers for:

- state/index/system key encoding
- family value encoding/decoding by column ID
- index value encoding for `message_id` and idempotency hits

Update `pkg/channel/store/keys.go` to host only shared key-prefix helpers that call the new table codec primitives.

- [ ] **Step 4: Re-run the focused tests and verify they pass**

Run: `go test ./pkg/channel/store -run "TestMessageTableCatalog|TestMessageRowRoundTrip" -count=1`

Expected: PASS

- [ ] **Step 5: Commit the schema foundation**

```bash
git add pkg/channel/store/catalog.go pkg/channel/store/message_row.go pkg/channel/store/table_codec.go pkg/channel/store/keys.go pkg/channel/store/table_codec_test.go pkg/channel/store/message_row_test.go
git commit -m "refactor(channel-store): add message table schema"
```

## Task 2: Build `messageTable` and move append/read/LEO/truncate to structured storage

**Files:**
- Create: `pkg/channel/store/message_table.go`
- Modify: `pkg/channel/store/logstore.go`
- Modify: `pkg/channel/store/channel_store.go`
- Modify: `pkg/channel/store/engine.go`
- Modify: `pkg/channel/store/logstore_test.go`
- Modify: `pkg/channel/store/channel_store_test.go`
- Modify: `pkg/channel/store/testenv_test.go`

- [ ] **Step 1: Write failing tests for structured append/read/truncate behavior**

Add or update tests to lock these outcomes:

```go
func TestChannelStoreAppendPersistsStructuredRowsAndCompatibilityRead(t *testing.T) {
    st := newTestChannelStore(t)
    payload := mustEncodeStoreMessage(t, channel.Message{MessageID: 11, ChannelID: "c1", ChannelType: 1, Payload: []byte("one")})

    base, err := st.Append([]channel.Record{{Payload: payload, SizeBytes: len(payload)}})
    require.NoError(t, err)
    require.Equal(t, uint64(0), base)

    row, ok, err := st.getMessageBySeq(1)
    require.NoError(t, err)
    require.True(t, ok)
    require.Equal(t, uint64(11), row.MessageID)

    records, err := st.Read(0, 1024)
    require.NoError(t, err)
    require.Len(t, records, 1)
}

func TestChannelStoreTruncateRemovesRowsAndIndexes(t *testing.T) {
    // append two structured messages, truncate to offset 1, assert seq 2 row and its indexes are gone
}

func TestChannelStoreExposesStructuredQueryAPIs(t *testing.T) {
    // append rows, assert GetMessageByMessageID and ListMessagesByClientMsgNo return direct indexed results.
}
```

- [ ] **Step 2: Run the focused store tests and verify they fail correctly**

Run: `go test ./pkg/channel/store -run "TestChannelStoreAppendPersistsStructuredRows|TestChannelStoreTruncateRemovesRows|TestChannelStoreExposesStructuredQueryAPIs" -count=1`

Expected: FAIL because `Append`, `Read`, `LEO`, and `Truncate` still use raw log keys.

- [ ] **Step 3: Implement `messageTable` and refactor the compatibility APIs**

Create `pkg/channel/store/message_table.go` with a channel-scoped runtime such as:

```go
type messageTable struct {
    channelKey channel.ChannelKey
    db         *pebble.DB
}

func (t *messageTable) append(writeBatch *pebble.Batch, rows []messageRow) error
func (t *messageTable) getBySeq(seq uint64) (messageRow, bool, error)
func (t *messageTable) getByMessageID(messageID uint64) (messageRow, bool, error)
func (t *messageTable) scanBySeq(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error)
func (t *messageTable) scanBySeqReverse(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error)
func (t *messageTable) scanByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]messageRow, uint64, bool, error)
func (t *messageTable) truncateFromSeq(writeBatch *pebble.Batch, fromSeq uint64) error
```

Refactor `pkg/channel/store/logstore.go` so that:

- `Append([]channel.Record)` decodes `Record.Payload` into `messageRow`, assigns `MessageSeq = baseOffset + i + 1`, and writes families plus indexes through `messageTable.append(...)`
- `Append([]channel.Record)` first probes `uidx_message_id`; if the `message_id` already exists for a different row, fail the write as a corruption/state error instead of overwriting the unique index
- `Read(...)` and `ReadOffsets(...)` use `messageTable.scanBySeq(...)` / `scanBySeqReverse(...)` and convert rows back to compatibility `channel.Record` / `LogRecord`
- `LEO()` loads the max persisted `message_seq` and returns `message_seq - 1` offset semantics externally
- `Truncate(to)` translates to `message_seq >= to+1` deletions via `messageTable.truncateFromSeq(...)`

Keep the existing commit coordinator and `writeInProgress` publication rules unchanged.

Expose the new query surface from `pkg/channel/store/channel_store.go` so handler code never reaches into `messageTable` directly:

```go
func (s *ChannelStore) GetMessageBySeq(seq uint64) (channel.Message, bool, error)
func (s *ChannelStore) GetMessageByMessageID(messageID uint64) (channel.Message, bool, error)
func (s *ChannelStore) ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]channel.Message, error)
func (s *ChannelStore) ListMessagesByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]channel.Message, uint64, bool, error)
func (s *ChannelStore) LookupIdempotency(key channel.IdempotencyKey) (channel.IdempotencyEntry, uint64, bool, error)
```

- [ ] **Step 4: Re-run the focused store tests and the existing append/truncate suite**

Run: `go test ./pkg/channel/store -run "TestChannelStoreAppend|TestEngineReadScopesByChannelKeyAndBudget|TestChannelStoreLEOTracksAppendAndTruncate|TestChannelStoreSyncPreservesTrimmedLogAcrossRestart|TestChannelStoreExposesStructuredQueryAPIs" -count=1`

Expected: PASS

- [ ] **Step 5: Commit the structured append/read foundation**

```bash
git add pkg/channel/store/message_table.go pkg/channel/store/logstore.go pkg/channel/store/channel_store.go pkg/channel/store/engine.go pkg/channel/store/logstore_test.go pkg/channel/store/channel_store_test.go pkg/channel/store/testenv_test.go
git commit -m "refactor(channel-store): persist structured message rows"
```

## Task 3: Move apply-fetch, idempotency, and system-state handling onto the new table/index model

**Files:**
- Modify: `pkg/channel/store/commit.go`
- Modify: `pkg/channel/store/idempotency.go`
- Modify: `pkg/channel/store/codec.go`
- Modify: `pkg/channel/store/checkpoint.go`
- Modify: `pkg/channel/store/history.go`
- Modify: `pkg/channel/store/snapshot.go`
- Modify: `pkg/channel/store/apply_fetch_test.go`
- Modify: `pkg/channel/store/idempotency_test.go`
- Modify: `pkg/channel/store/checkpoint_test.go`
- Modify: `pkg/channel/store/history_test.go`
- Modify: `pkg/channel/store/snapshot_test.go`
- Modify: `pkg/channel/store/checkpoint_commit_test.go`
- Modify: `pkg/channel/store/logstore_commit_test.go`

- [ ] **Step 1: Write failing tests for seq preservation, unique-index idempotency, and new system keys**

Add tests that lock these behaviors:

```go
func TestStoreApplyFetchPreservesLeaderSequenceAfterTruncate(t *testing.T) {
    // append seq 1..3, truncate to offset 1, apply two fetched records,
    // assert resulting rows are seq 2 and 3, not re-numbered from max historical seq.
}

func TestGetIdempotencyUsesUniqueIndexWithoutRawLogReplay(t *testing.T) {
    // persist one message with from_uid/client_msg_no, assert GetIdempotency returns message_seq/message_id via index
}

func TestAppendRejectsDuplicateMessageID(t *testing.T) {
    // append one message, then append another row with the same message_id but different seq/payload,
    // expect a corruption/state error instead of silent unique-index overwrite.
}

func TestSystemStateRoundTripUsesSystemKeyspace(t *testing.T) {
    // checkpoint/history/snapshot still round-trip after the keyspace move
}
```

- [ ] **Step 2: Run the focused tests and verify they fail correctly**

Run: `go test ./pkg/channel/store -run "TestStoreApplyFetchPreservesLeaderSequence|TestGetIdempotencyUsesUniqueIndex|TestAppendRejectsDuplicateMessageID|TestSystemStateRoundTripUsesSystemKeyspace" -count=1`

Expected: FAIL because `StoreApplyFetch`, idempotency, and system-state helpers still rely on old raw-key logic.

- [ ] **Step 3: Refactor apply-fetch and idempotency to use structured rows and indexes**

In `pkg/channel/store/commit.go`:

- Replace raw-log writes in `writeApplyFetchedRecords(...)` with `messageTable.append(...)`
- Derive `MessageSeq` strictly from `base` / post-truncate `LEO` (`seq = base + i + 1`)
- Remove `decodeIdempotencyFields(...)` and `appliedMessageFromLogRecord(...)` raw-payload parsing in favor of row-based helpers

In `pkg/channel/store/idempotency.go`:

- Implement `GetIdempotency(...)` as a lookup against `uidx_from_uid_client_msg_no`
- Preserve `MessageID`, `MessageSeq`, and offset return values by converting `message_seq -> offset`
- Skip unique-index writes when `from_uid == ""` or `client_msg_no == ""`
- Preserve snapshot/restore semantics by exporting/importing the unique-index entries directly; do not leave snapshot strategy as an open design choice during implementation

In `pkg/channel/store/checkpoint.go`, `history.go`, and `snapshot.go`:

- Move keys into the `System` keyspace helpers from `table_codec.go`
- Keep behavior and public methods unchanged

- [ ] **Step 4: Run the focused tests plus existing commit/coordinator suites**

Run: `go test ./pkg/channel/store -run "TestStoreApplyFetch|TestGetIdempotency|TestAppendRejectsDuplicateMessageID|TestChannelStoreCheckpoint|TestChannelStoreStoreCheckpoint|TestChannelStoreAppendUsesCommitCoordinatorAcrossChannels|TestCommitCoordinator" -count=1`

Expected: PASS

- [ ] **Step 5: Commit the apply-fetch/idempotency/system-state migration**

```bash
git add pkg/channel/store/commit.go pkg/channel/store/idempotency.go pkg/channel/store/codec.go pkg/channel/store/checkpoint.go pkg/channel/store/history.go pkg/channel/store/snapshot.go pkg/channel/store/apply_fetch_test.go pkg/channel/store/idempotency_test.go pkg/channel/store/checkpoint_test.go pkg/channel/store/history_test.go pkg/channel/store/snapshot_test.go pkg/channel/store/checkpoint_commit_test.go pkg/channel/store/logstore_commit_test.go
git commit -m "refactor(channel-store): move apply fetch and indexes to message table"
```

## Task 4: Switch handler append, seq reads, fetch, and message queries to table/index lookups

**Files:**
- Modify: `pkg/channel/handler/append.go`
- Modify: `pkg/channel/handler/seq_read.go`
- Modify: `pkg/channel/handler/fetch.go`
- Modify: `pkg/channel/handler/message_query.go`
- Modify: `pkg/channel/handler/append_test.go`
- Modify: `pkg/channel/handler/seq_read_test.go`
- Modify: `pkg/channel/handler/fetch_test.go`
- Modify: `pkg/channel/handler/message_query_test.go`
- Modify: `pkg/channel/handler/apply_test.go`

- [ ] **Step 1: Write failing handler tests that demand direct table/index usage**

Add or update tests to lock these outcomes:

```go
func TestAppendIdempotencyHitReturnsStoredMessageFromUniqueIndex(t *testing.T) {
    // seed one stored message row + unique index, append same from_uid/client_msg_no/payload,
    // expect original message_id/message_seq without scanning compatibility log payloads.
}

func TestQueryMessagesFiltersByMessageIDViaDirectIndex(t *testing.T) {
    // append several messages, query by MessageID, expect a single exact hit.
}

func TestQueryMessagesFiltersByClientMsgNoViaDirectIndexAcrossPages(t *testing.T) {
    // append repeated client_msg_no values, page with BeforeSeq, expect exact-match descending results.
}
```

- [ ] **Step 2: Run the focused handler tests and verify they fail correctly**

Run: `go test ./pkg/channel/handler -run "TestAppendIdempotencyHitReturnsStoredMessageFromUniqueIndex|TestQueryMessagesFiltersByMessageID|TestQueryMessagesFiltersByClientMsgNoAcrossPages|TestLoadMsgUsesExplicitCommittedHW" -count=1`

Expected: FAIL because handler code still depends on `GetIdempotency(...)` + offset lookup and log-record decoding.

- [ ] **Step 3: Refactor handler code to use the new store APIs**

In `pkg/channel/handler/append.go`:

- Replace `loadMessageViewAtOffset(...)` on idempotency hits with a structured lookup path:
  - query unique idempotency index
  - compare `payload_hash`
  - if equal, load the message row by `message_seq`

In `pkg/channel/handler/seq_read.go`:

- Replace `ReadOffsets(...)` + `decodeMessageRecord(...)` with direct store row reads/scans
- Preserve all current committed-HW fencing behavior

In `pkg/channel/handler/fetch.go`:

- Switch the end state to direct structured reads via `ChannelStore.ListMessagesBySeq(...)`; do not leave `Fetch` on the compatibility `Read(...)` path once the task is complete
- Preserve the current `Limit` / `MaxBytes` / committed-HW fencing behavior when converting row scans into `FetchResult`

In `pkg/channel/handler/message_query.go`:

- Route `MessageID` queries to the direct unique index
- Route `ClientMsgNo` queries to the non-unique index with exact match + `BeforeSeq` pagination
- Keep the no-filter path as reverse `message_seq` scan

- [ ] **Step 4: Run the full handler package tests**

Run: `go test ./pkg/channel/handler -count=1`

Expected: PASS

- [ ] **Step 5: Commit the handler query migration**

```bash
git add pkg/channel/handler/append.go pkg/channel/handler/seq_read.go pkg/channel/handler/fetch.go pkg/channel/handler/message_query.go pkg/channel/handler/append_test.go pkg/channel/handler/seq_read_test.go pkg/channel/handler/fetch_test.go pkg/channel/handler/message_query_test.go pkg/channel/handler/apply_test.go
git commit -m "refactor(channel-handler): use structured message table queries"
```

## Task 5: Update documentation, finish regression coverage, and verify the whole channel slice

**Files:**
- Modify: `pkg/channel/FLOW.md`
- Modify: `pkg/channel/store/channel_store_test.go`
- Modify: `pkg/channel/store/logstore_test.go`
- Modify: `pkg/channel/handler/message_query_test.go`
- Modify: `pkg/channel/handler/seq_read_test.go`

- [ ] **Step 1: Add the final failing regression/docs tests if any gaps remain**

Before touching docs, add any missing regression tests discovered during Tasks 1-4, especially for:

- restart recovery of `LEO` from max `message_seq`
- truncate removing `message_id` and `client_msg_no` indexes
- `client_msg_no == ""` rows not being indexed
- `message_id == 0` being rejected as invalid input/corruption

- [ ] **Step 2: Run the targeted regression suite and confirm it is green before doc updates**

Run: `go test ./pkg/channel/store ./pkg/channel/handler -count=1`

Expected: PASS

- [ ] **Step 3: Update `pkg/channel/FLOW.md` to match the new storage/query model**

Revise the store and query sections so they explicitly say:

- messages are persisted as a structured `message` table, not raw log payload values
- `State / Index / System` are the new storage areas
- idempotency is enforced by `uidx_from_uid_client_msg_no`, not a standalone keyspace
- `MessageID` / `ClientMsgNo` queries are index-backed
- compatibility `Record.Payload` encoding is now ingress/egress glue only

- [ ] **Step 4: Run the full verification command for this refactor**

Run: `go test ./pkg/channel/... -count=1`

Expected: PASS

If this fails in unrelated areas, immediately capture the exact failing package/test names in the implementation notes and fall back to the narrowest passing command that still proves the refactor, such as:

`go test ./pkg/channel/store ./pkg/channel/handler ./pkg/channel/replica -count=1`

Only report the narrower command if you actually ran it and it passed.

- [ ] **Step 5: Commit the docs and regression cleanup**

```bash
git add pkg/channel/FLOW.md pkg/channel/store/channel_store_test.go pkg/channel/store/logstore_test.go pkg/channel/handler/message_query_test.go pkg/channel/handler/seq_read_test.go
git commit -m "docs(channel): describe structured message table storage"
```

## Final Verification Checklist

Before execution is declared complete, re-read the spec and confirm all of these are true with code and tests:

- [ ] `pkg/channel/store` no longer persists raw durable payload bytes as the Pebble source of truth
- [ ] `message` is represented by a catalog-described table with explicit families and indexes
- [ ] `message_id` lookup is direct-index backed
- [ ] `client_msg_no` lookup is direct-index backed and paged by `BeforeSeq`
- [ ] idempotency uses the `(from_uid, client_msg_no)` unique index
- [ ] `StoreApplyFetch(...)` preserves leader sequence identity through post-truncate `LEO`
- [ ] `checkpoint/history/snapshot` still round-trip under the new system keyspace
- [ ] `pkg/channel/FLOW.md` reflects the new storage model
- [ ] fresh verification output exists for the final reported test command
