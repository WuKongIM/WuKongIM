# Unified Conversation Projection Design

## Background

`internalv2` needs CMD sync without adding another long-lived conversation
runtime. The project is still in development and does not need old on-disk data
compatibility, so the design can replace the current split between ordinary
conversation rows and CMD conversation rows.

The long-term model is one UID-owned conversation projection with an explicit
conversation kind. Ordinary conversation list and CMD sync become two views over
the same storage/runtime shape instead of two parallel implementations.

## Goals

- Store ordinary and CMD conversation cursors in one canonical conversation
  table.
- Make conversation kind part of durable keys and runtime cache keys.
- Keep UID as the ownership key so all writes route through the UID hash slot.
- Reuse one active-cache runtime for ordinary conversation activity and CMD
  sync activity.
- Avoid command-channel suffix filtering as a storage or routing boundary.
- Keep performance predictable for many active users, many active channels, and
  large channels.

## Non-Goals

- Preserve old `conversation` or `cmd_conversation` on-disk rows.
- Maintain permanent dual-read or dual-write compatibility paths.
- Merge CMD sync business rules into ordinary conversation list usecases.
- Change channel message log storage; CMD messages remain normal channel log
  messages under command-channel IDs.

## Conversation Kind

Add a durable kind enum in `pkg/db/meta`:

```go
type ConversationKind uint8

const (
    ConversationKindNormal ConversationKind = 1
    ConversationKindCMD    ConversationKind = 2
)
```

`ConversationKindNormal` backs ordinary conversation list, unread, delete, and
recent-message views. `ConversationKindCMD` backs `/message/sync` and
`/message/syncack`.

## Storage Model

Replace the ordinary/CMD split with one canonical conversation table:

```text
primary: (uid, kind, channel_id, channel_type)
active:  (uid, kind, active_at desc, channel_id, channel_type)
value:   read_seq, deleted_to_seq, active_at, updated_at, sparse_active
```

`kind` must be in the primary key and active index. Storing kind only in the
value would force list paths to scan unrelated rows and filter them after the
fact, which causes underfilled pages and unnecessary over-scans.

The typed row becomes:

```go
type ConversationState struct {
    UID          string
    Kind         ConversationKind
    ChannelID    string
    ChannelType  int64
    ReadSeq      uint64
    DeletedToSeq uint64
    ActiveAt     int64
    UpdatedAt    int64
    SparseActive bool
}
```

Read/delete/update patches also carry `Kind`.

## Runtime Model

`internalv2/runtime/conversationactive` becomes kind-aware instead of
ordinary-conversation-specific.

`ActiveBatch` and `ActivePatch` carry `Kind`. The manager cache key is:

```text
(uid, kind, channel_id, channel_type)
```

The manager keeps the existing responsibilities:

- merge active timestamps and sender read sequence,
- bound cache pressure,
- flush dirty rows,
- drain rows by UID hash slot during authority handoff,
- merge cache rows with durable active rows for active views,
- emit low-cardinality cache and flush observations.

The manager does not infer kind from channel ID. Callers must provide the kind.

## Store Interface

Replace user-specific method names with generic conversation methods:

```go
type ActiveStore interface {
    ListConversationActivePage(ctx context.Context, kind ConversationKind, uid string, after ActiveCursor, limit int) (ActiveViewPage, error)
    GetConversationState(ctx context.Context, kind ConversationKind, uid, channelID string, channelType uint8) (ConversationState, bool, error)
    GetConversationStates(ctx context.Context, keys []ConversationKey) (map[ConversationKey]ConversationState, error)
    TouchConversationActiveAt(ctx context.Context, patches []ActivePatch) error
}
```

`pkg/clusterv2` exposes the same generic shape at the cluster boundary. All
mutations are grouped by UID hash slot and proposed through Slot ownership.
Single-node cluster deployment uses the same route path.

## Slot FSM

The Slot FSM should own one family of conversation commands:

- `UpsertConversationStates`
- `TouchConversationActiveAt`
- `AdvanceConversationReadSeq`
- `HideConversations`

Command entries include `kind`. The old split between user conversation
commands and CMD conversation commands should not survive as a permanent v2
surface.

## Usecase Boundaries

Ordinary conversation usecases always request `ConversationKindNormal`.
They no longer filter command-channel suffixes because the active index already
separates kinds.

CMD sync usecases always request `ConversationKindCMD`. They still own CMD sync
business rules:

- build and cache sync generations,
- read command-channel messages from `max(read_seq, deleted_to_seq)+1`,
- acknowledge only the latest returned generation,
- hide one command-channel suffix in legacy-compatible API responses.

The shared storage/runtime layer does not know those CMD sync rules.

## Channel Append Flow

After a durable commit and recipient resolution, channel append creates explicit
conversation activity batches:

- ordinary message activity uses `ConversationKindNormal`,
- command-channel or `SyncOnce` activity uses `ConversationKindCMD`.

The channel append path must not decide kind by letting the conversation runtime
parse channel suffixes. It should produce the kind from message semantics before
calling the active manager.

## Performance Notes

- Active list scans are bounded by `(uid, kind)` prefixes, so ordinary and CMD
  views do not scan each other's rows.
- Large channel recipient expansion remains owned by channel append recipient
  processing; conversation active admission consumes the already-resolved UID
  set and does not perform another full member scan.
- Dirty flush can remain batch-oriented by UID hash slot. Metrics should expose
  kind-aware counts or labels where useful, especially dirty rows and flush
  errors.
- If one kind can dominate dirty rows, the flush selector should preserve basic
  fairness across kinds within a hash slot.

## Testing Strategy

- `pkg/db/meta`: primary/index encoding includes kind; normal and CMD rows with
  the same UID/channel are isolated; active pages scan only one kind; read,
  delete, sparse-active, and read-seq merge semantics remain monotonic.
- `pkg/slot/fsm`: conversation commands round-trip kind and reject rows whose
  UID hash slot does not belong to the target Slot.
- `pkg/clusterv2`: generic conversation facade groups writes by UID hash slot
  and lists by `(uid, kind)`.
- `internalv2/runtime/conversationactive`: cache merge, pagination, cooldown,
  flush, pressure spill, and hash-slot drain include kind in keys.
- `internalv2/usecase/conversation`: ordinary sync/list/unread/delete request
  only normal kind rows.
- `internalv2/usecase/cmdsync`: CMD sync reads only CMD kind rows and advances
  only CMD read cursors.
- E2E: single-node cluster SEND updates normal conversations; command
  SEND/SyncOnce updates CMD sync rows; `/conversation/list` and `/message/sync`
  do not leak into each other.

## Migration Decision

Because the project is not released, no old data migration is required. The
implementation should replace existing development-only split surfaces rather
than layering permanent compatibility code around them.
