# Message Event Projection Migration Design

## Goal

Migrate the legacy message event append behavior into the promoted WuKongIM
architecture as a first-stage, high-performance message event projection. This
stage adds `POST /message/event` and enriches `POST /channel/messagesync` with
event metadata. It does not add `/message/eventsync`, durable per-event replay,
or realtime `EventPacket` fanout.

## Source References

- Legacy design: `learn_project/WuKongIM/docs/design/Message_Event_Stream_Design.md`
- Legacy API behavior: `learn_project/WuKongIM/internal/api/message.go`
- Current API boundary: `internal/access/api/FLOW.md`
- Current message usecase boundary: `internal/usecase/message/FLOW.md`
- Current storage ownership: `pkg/db/meta/FLOW.md` and `pkg/db/message/FLOW.md`

## Scope

### In Scope

- Add a compatible `POST /message/event` HTTP route.
- Store one projection state per `(channel_id, channel_type, client_msg_no, event_key)`.
- Assign a message-scoped `msg_event_seq` for every accepted event append.
- Keep `stream.delta` as a reducer update, not a durable per-event row.
- Enrich `SyncChannelMessages` results with `event_meta`, `event_sync_hint`,
  and legacy stream fields (`stream_data`, `end`, `end_reason`, `error`) when a
  main event key exists.
- Preserve cluster semantics for single-node cluster and multi-node cluster
  deployments.

### Out Of Scope

- `POST /message/eventsync`.
- Persisting raw event rows for full offline replay.
- Realtime `EventPacket` delivery. The current delivery runtime is built around
  committed message fanout; event frame fanout should be a later delivery-frame
  extension rather than a shortcut in the HTTP adapter.
- Creating anchor messages when `client_msg_no` is unknown.
- New configuration switches.

## Architecture

The migration should follow the existing access/usecase/infra/pkg boundaries:

```text
POST /message/event
  -> internal/access/api request validation and compatible JSON response
  -> internal/usecase/message AppendEvent
  -> internal/infra/cluster message event store adapter
  -> pkg/cluster channel-owned Slot proposal
  -> pkg/db/meta message event projection table
```

`internal/access/api` owns only request/response DTOs and legacy envelope
mapping. It must not route to cluster leaders or mutate storage directly.

`internal/usecase/message` owns event normalization, command validation, and
messagesync enrichment orchestration. It should stay independent from concrete
HTTP, gateway, cluster, and app packages.

`internal/infra/cluster` adapts the usecase ports to the cluster node facade.
The facade should route event projection mutations by channel ID, matching the
channel-owned metadata pattern used by `ChannelLatest`.

`pkg/db/meta` stores the replicated event projection because Slot metadata is
already replicated through Slot Raft and snapshot/import flows. Storing this as
local message DB system rows would make failover behavior incomplete unless the
channel log replication format also changed.

## Data Model

Add `metadb.MessageEventState` as a channel-owned table:

```go
type MessageEventState struct {
    ChannelID       string
    ChannelType     int64
    ClientMsgNo     string
    EventKey        string
    Status          string
    LastMsgEventSeq uint64
    LastEventID     string
    LastEventType   string
    LastVisibility  string
    LastOccurredAt  int64
    SnapshotPayload []byte
    EndReason       uint8
    Error           string
    UpdatedAt       int64
}
```

The primary key is:

```text
(channel_id, channel_type, client_msg_no, event_key)
```

Add a small channel-owned sequence row for:

```text
(channel_id, channel_type, client_msg_no) -> last_msg_event_seq
```

The state update and sequence increment must commit atomically in one Slot FSM
apply. This preserves monotonic `msg_event_seq` and state consistency after
leader transfer or follower catch-up.

Constants should use the legacy-compatible names:

```text
event_key default: main
event statuses: open, closed, error, cancelled
event types: stream.delta, stream.snapshot, stream.close, stream.error,
             stream.cancel, stream.finish
visibility: public, private, restricted
```

`stream.finish` maps to the special event key `__finish__` and marks the
message as completed in `event_meta`, but does not count as a normal lane in
the returned `events` list.

## Reducer Semantics

The reducer is pure and should be unit-tested without storage:

- Empty `event_key` becomes `main`.
- Empty `occurred_at` becomes the server time at the usecase or storage
  boundary.
- `stream.delta` opens the key if needed and merges text deltas of the shape
  `{"kind":"text","delta":"..."}` into `{"kind":"text","text":"..."}`.
- Non-text deltas replace `snapshot_payload` with the event payload.
- `stream.snapshot` replaces `snapshot_payload`.
- `stream.close` sets `closed`, optionally extracts `snapshot`, and records
  `end_reason`.
- `stream.error` sets `error`, optionally extracts `snapshot`, and records
  `error`.
- `stream.cancel` sets `cancelled`, optionally extracts `snapshot`.
- `stream.finish` sets `closed` on `__finish__`.
- Repeating the last `event_id` for the same event key is idempotent and returns
  the existing `LastMsgEventSeq`.
- Appending a new event after a terminal status returns the existing terminal
  state without advancing the sequence.

## Message Existence

`POST /message/event` should require that the target message already exists. The
lookup can use the existing channel idempotency index when `from_uid` is present:

```text
(channel_id, channel_type, from_uid, client_msg_no)
```

If the message is missing, return a compatible error response instead of
creating an anchor message. This avoids inventing a new send path and keeps the
message as the required foundation.

For this stage, `from_uid` is required. A future stage can relax this only after
an efficient channel-scoped lookup exists that does not scan channel history.

## HTTP API

### `POST /message/event`

Request fields:

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "u1",
  "client_msg_no": "cmn_abc_001",
  "event_id": "evt_0001",
  "event_type": "stream.delta",
  "event_key": "main",
  "visibility": "public",
  "occurred_at": 1772860800000,
  "payload": {"kind": "text", "delta": "hello"},
  "headers": {"schema": "v1"}
}
```

Response data:

```json
{
  "client_msg_no": "cmn_abc_001",
  "event_key": "main",
  "event_id": "evt_0001",
  "msg_event_seq": 1,
  "stream_status": "open",
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "u1"
}
```

The access adapter should preserve the existing compatible error envelope style
used by message routes.

### `POST /channel/messagesync`

The request already accepts `event_summary_mode`. The usecase should enrich only
messages with a non-empty `client_msg_no`. It should batch state reads for the
current page to avoid one storage read per message where possible.

Response additions per message:

```json
{
  "event_meta": {
    "has_events": true,
    "completed": false,
    "event_version": 12,
    "last_msg_event_seq": 12,
    "event_count": 1,
    "open_event_count": 1,
    "events": [
      {
        "event_key": "main",
        "status": "open",
        "last_msg_event_seq": 12,
        "snapshot": {"kind": "text", "text": "hello"}
      }
    ]
  },
  "event_sync_hint": {
    "client_msg_no": "cmn_abc_001",
    "from_msg_event_seq": 0
  },
  "stream_data": "hello",
  "end": 0
}
```

For `event_summary_mode=basic`, omit snapshots from event key entries. For an
empty mode, use `full` to match legacy behavior.

## Performance

- Writes update one sequence row and one event-key state row per append.
- The reducer is O(payload size) and does not scan previous event rows.
- Messagesync enrichment uses a batched state read for the returned page.
- The maximum messagesync page remains capped by the existing limit.
- The design does not fan out event writes to channel subscribers, which avoids
  introducing a high-cardinality hot path into the first stage.
- The Slot proposal payload should contain only the normalized event command and
  not an unbounded history of deltas.

## Testing

Use TDD for implementation.

Unit tests:

- Reducer lifecycle: delta, snapshot, close, error, cancel, finish.
- Text delta merge and non-text replacement.
- Idempotent duplicate event ID.
- Terminal state rejects advancement.
- Empty event key normalization.
- Basic vs full messagesync metadata rendering.

Storage tests:

- Atomic sequence increment plus state update.
- Channel isolation for the same `client_msg_no`.
- Batch state reads for multiple `client_msg_no` values.
- Snapshot/import table registration if the table is included in meta snapshots.

Usecase tests:

- `AppendEvent` validates required fields and normalizes person-channel IDs.
- Missing message returns a stable error.
- Messagesync enriches only messages with states and clones payload/snapshot bytes.

Access tests:

- `POST /message/event` registers and returns the compatible response.
- Invalid JSON and missing required fields use existing JSON error envelope style.
- `/channel/messagesync` includes event metadata when the usecase returns it.

Focused verification:

```bash
go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster ./internal/usecase/message ./internal/infra/cluster ./internal/access/api
```

Run broader `go test ./internal/... ./pkg/...` after the focused package tests
pass if the implementation touches shared Slot FSM or cluster facades.

## Acceptance Criteria

- `POST /message/event` can append stream projection updates for an existing
  message in a single-node cluster.
- Event projection updates are persisted through Slot metadata, not node-local
  unreplicated state.
- `/channel/messagesync` returns event metadata for messages with event state.
- `/message/eventsync` remains unregistered.
- No new bypass of cluster semantics is introduced.
- Flow docs for modified packages are updated.
