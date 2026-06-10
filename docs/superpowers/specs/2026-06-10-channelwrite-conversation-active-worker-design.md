# Channelwrite Conversation Active Worker Design

## Goal

Split recent-conversation updates out of `internalv2/runtime/channelwrite`
without adding another compatibility layer around the old conversation authority
cache.

After this change, `channelwrite` only owns durable channel append and recipient
expansion. The recent-conversation runtime owns the UID active-index cache,
conversation-list cache reads, and periodic database flushes.

## Current Problem

`channelwrite` currently performs post-commit recipient selection and then calls
a recipient processor that can update recent conversations and deliver messages.
That makes the channel-authority write runtime responsible for too much work
after durable append.

The existing conversation authority cache already gives `/conversation/list`
visibility before rows flush to the database, but keeping it as-is and adding a
new worker outside it would create layered patches:

```text
channelwrite -> worker queue -> old conversation authority cache -> DB
```

That would blur ownership and make the read path harder to reason about.

## Target Architecture

```text
SEND
  -> channelwrite.Router
  -> channelwrite.Group
  -> prepare / append / SENDACK
  -> recipient expansion
  -> conversation active admit
       update worker-owned active cache
       mark entries dirty
       return
  -> delivery worker handoff when delivery is enabled

/conversation/list
  -> route UID to the active worker authority
  -> active worker reads cache + DB
  -> merge and sort by ActiveAt
  -> conversation usecase hydrates last message from channel log
```

The new recent-conversation runtime should live under a focused package such as
`internalv2/runtime/conversationactive`. App wiring and node RPC adapters remain
in `internalv2/app` and `internalv2/access/node`.

## Responsibility Boundaries

`channelwrite` keeps:

- Channel append authority admission.
- Send preparation, validation, message ID allocation, and durable append.
- SENDACK completion after durable append.
- Scoped UID, person-channel, and subscriber-page recipient expansion.
- Handoff attempts to downstream runtimes.

`conversationactive` owns:

- UID-owned recent conversation active cache.
- Sender `ReadSeq` advancement.
- Dirty entry tracking.
- Periodic and pressure-triggered flush to the metadata database.
- Conversation active list reads that merge cache rows with DB rows.
- UID authority target validation, warming, draining, and handoff.

Delivery owns:

- Presence route resolution.
- Owner-node push.
- Retry scheduling and recvack tracking.

## Active Model

The recent-conversation worker is intentionally an active-index runtime, not a
general conversation-state runtime.

```go
type ActiveEntry struct {
	UID         string
	ChannelID   string
	ChannelType int64
	ActiveAt    int64
	ReadSeq     uint64
}
```

`ActiveAt` is the core field and determines conversation-list ordering.

`ReadSeq` exists only so a sender's own conversation does not show the message
they just sent as unread. The worker does not own `DeletedToSeq`, `MessageSeq`,
`SparseActive`, unread calculation, or last-message payloads.

## Patch Semantics

The active worker uses an internal patch shape with explicit read-sequence
intent:

```go
type ActivePatch struct {
	UID         string
	ChannelID   string
	ChannelType int64
	ActiveAt    int64
	ReadSeq     uint64
	ReadSeqSet  bool
}
```

For every effective active participant:

```text
ActiveAt = committed event ServerTimestampMS
```

For the sender only:

```text
ReadSeq = committed event MessageSeq
ReadSeqSet = true
```

Merge rules are monotonic:

```text
entry.ActiveAt = max(entry.ActiveAt, patch.ActiveAt)

if patch.ReadSeqSet:
    entry.ReadSeq = max(entry.ReadSeq, patch.ReadSeq)
```

Receiver patches never overwrite an existing `ReadSeq` with zero.

## Active Participants

Recent-conversation participants are not exactly the same as online-delivery
recipients.

`channelwrite` must build a conversation-active batch from:

```text
expanded recipients + sender UID
```

and then deduplicate by UID.

This prevents future delivery optimizations, such as skipping sender echo, from
removing the sender's recent-conversation row. For the sender row, `ReadSeq`
must advance to the committed message sequence.

## Admission Contract

The active worker entrypoint should express cache admission, not queue submit:

```go
type Admitter interface {
	AdmitActiveBatch(context.Context, RouteTarget, ActiveBatch) error
}
```

Success means the target authority's in-memory active cache has been updated
and the row is immediately visible to list reads. It does not mean the row has
flushed to the database.

Admission flow:

```text
validate authority target
derive active participants
convert participants to ActivePatch values
lock UID shard
merge entries into cache
mark changed entries dirty
unlock
return
```

If the target UID authority is remote, the app/node adapter forwards the batch
to the remote node. The remote node returns success only after its local active
cache has been updated.

## Cache Layout

Use UID sharding to avoid one global mutex:

```text
shard[hash(uid) % shardCount]
  byUID[uid][channelKey] = activeEntry
  dirty set keyed by uid + channelKey
```

Each cached entry also needs non-business metadata:

```text
target route identity
version
dirty flag
```

The route identity fences stale authority writes. The version prevents a flush
snapshot from clearing a row that was updated again while the flush was in
flight.

## List Read Flow

Conversation list reads must go through the active worker authority:

```text
ListActiveView(uid, cursor, limit)
  -> validate the exact UID authority target
  -> read cache rows for uid after cursor
  -> read DB active rows for uid after cursor
  -> merge rows by (uid, channel_id, channel_type)
  -> sort by active_at desc, channel_id asc, channel_type asc
  -> return the active rows
```

Because the cache is part of the read model, a row admitted into the worker is
visible before database flush. This is the central correctness requirement for
the split.

The message usecase or conversation usecase continues to hydrate last messages
from the channel log after it receives active rows. Last-message hydration does
not move the active ordering anchor.

## Flush Flow

Flush is a background durability path for dirty active entries:

```text
ticker, dirty-count threshold, pressure, Stop, or handoff
  -> snapshot dirty entries with their versions
  -> persist active_at/read_seq batch to DB
  -> on success:
       clear dirty only if the current entry version matches the snapshot
  -> on failure:
       keep dirty for the next flush
```

The flush path may batch across UIDs, but it must not hold shard locks while
calling the database.

Stop order should be:

```text
stop channelwrite admission
wait for in-flight active admit calls to return
flush active worker dirty rows until context expires
stop active worker
```

## Pressure Policy

The active worker should avoid an ordinary queue in front of cache admission.
Admission either updates the cache or fails.

When cache capacity is under pressure:

```text
try synchronous spill of a bounded dirty snapshot
retry the admission once
if pressure remains or spill fails:
    record cache_pressure / admit_failed
    drop the active batch
```

Dropping an active batch remains best-effort behavior and must not affect
durable append or SENDACK. Message durability remains in the channel log.

## Configuration

Reuse existing conversation authority settings where they still describe the
same concept:

- `WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID`
- `WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS`
- `WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX`
- `WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT`
- `WK_CONVERSATION_AUTHORITY_ADMIT_BATCH_ROWS`
- `WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY`

Rename these later only if the operator-facing terminology becomes confusing.
The first implementation should prefer minimal configuration churn.

`WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS` should no longer imply recent
conversation processing. It should describe only channelwrite recipient
expansion / downstream handoff work after append.

## Observability

Add low-cardinality observations for:

- Active admission result: `ok`, `stale_route`, `not_leader`, `route_not_ready`,
  `cache_pressure`, `error`.
- Cache pressure by phase: `admit`, `list`, `flush`, `handoff`.
- Flush result and batch size.
- List result and whether cache rows participated.
- Dropped active batches.

Avoid UID, channel, slot, or node labels in metrics. Structured logs may include
sample UID/channel fields for failures.

## Migration Plan

1. Introduce the new `conversationactive` runtime with unit tests.
2. Move active cache/list/flush semantics out of the app-local
   `conversationAuthority` implementation into the runtime.
3. Keep app and access packages as adapters for cluster routing and RPC.
4. Change `channelwrite` post-commit work to perform recipient expansion and
   call the active worker admitter.
5. Remove recent-conversation mutation from `channelwrite.RecipientProcessor`.
6. Keep delivery split separate so recent conversation remains enabled even
   when `Delivery.Enabled=false`.
7. Update `FLOW.md` files for `channelwrite`, conversation usecase, app wiring,
   and the new runtime package.

## Test Strategy

Unit tests:

- Sender active row advances `ReadSeq` to the committed message sequence.
- Receiver active row updates `ActiveAt` without changing existing `ReadSeq`.
- Duplicate recipients are deduplicated, with sender preserved.
- Cache merge is monotonic for `ActiveAt` and `ReadSeq`.
- A successful admit is immediately visible to list before DB flush.
- Flush clears dirty rows only when versions match.
- Flush failure keeps dirty rows.
- Cache pressure attempts bounded spill before dropping an active batch.
- Stale or non-local authority targets are rejected.

App and integration-style tests:

- `/conversation/list` observes a newly admitted active row before DB flush.
- Channelwrite SEND still returns SENDACK after durable append without waiting
  for active DB flush.
- Delivery disabled still updates recent conversations.
- Route handoff drains dirty active entries before the old authority stops
  serving reads.

## Non-Goals

- No persistent outbox for recent conversation active updates in this phase.
- No unread/read-state redesign beyond sender `ReadSeq` advancement.
- No delivery presence or owner push logic inside the active worker.
- No subscriber scanning inside the active worker.
- No fallback branch that bypasses cluster authority semantics.
