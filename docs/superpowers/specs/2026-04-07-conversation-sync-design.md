# Conversation Sync Design

## Current Contract

`/conversation/sync` is working-set based. It does not use request `version` as a complete historical incremental cursor. Correctness comes from `UserConversationState` plus Channel Log facts; performance comes from persisted `active_at` and the UID-owner hot active hint cache.

Accepted semantics:

- Channel Log is the latest-message fact source.
- `UserConversationState` is the user-scoped state source for `read_seq`, `deleted_to_seq`, `active_at`, and `updated_at`.
- `active_at` is a best-effort working-set hint and may be dropped.
- Active hint writes are async, batched, droppable, and must not block durable send, Sendack, delivery, or committed replay cursor advancement.
- `ListUserConversationActive` merges persisted active rows with UID-owner hot hints.
- Delete hides messages through `DeletedToSeq`, clears active hints, and allows reappearance when a later message has `MessageSeq > DeletedToSeq`.

## Problem Framing

Recent conversation sync has three separate concerns:

1. `catalog correctness`: user-specific state and delete/read progress.
2. `message facts`: latest message, recents, and sequence boundaries from Channel Log.
3. `working set performance`: avoid scanning every historical conversation for every sync.

The design separates correctness and acceleration:

- Truth layer: `UserConversationState + Channel Log`
- Acceleration layer: persisted `UserConversationState.active_at + ActiveHintCache`

The send path must not synchronously write per-user conversation state for every message.

## Components

### API Adapter

`internal/access/api/conversation_sync.go`

Responsibilities:

- Parse legacy-compatible `/conversation/sync` requests.
- Parse `last_msg_seqs` as client-known conversation overlays.
- Map external person channel IDs to normalized internal person channel IDs.
- Return legacy-compatible array responses.

### Conversation Usecase

`internal/usecase/conversation`

Responsibilities:

- Build candidates from active working set and client-known overlays.
- Load latest and recent messages from Channel Log facts.
- Apply `deleted_to_seq` visibility.
- Compute unread and response `version` compatibility timestamps.
- Expose delete/read APIs that update user state through focused store interfaces.

### Active Hint Cache

`internal/usecase/conversation/active_hint_cache.go`

Responsibilities:

- Store UID-owner hot `UserConversationActiveHint` entries in bounded memory.
- Deduplicate by `(uid, channel_id, channel_type)` and prefer newer `MessageSeq` / `ActiveAt`.
- Enforce global and per-UID caps.
- Flush hot hints to `UserConversationState.active_at` in batches.
- Install delete barriers so delayed hints with `MessageSeq <= DeletedToSeq` cannot revive a deleted conversation.

### Async Projector

`internal/usecase/conversation/projector.go`

Responsibilities:

- Consume committed messages after Channel Log quorum commit.
- Submit active hints asynchronously to the UID-owner store overlay.
- Use `MessageSeq` on every hint so delete barriers can reject stale hints.
- Fan out group active hints only within configured subscriber/budget limits.
- Drop hints on queue pressure; never fail the durable message path.

### Slot Store / Proxy

`pkg/slot/meta`, `pkg/slot/fsm`, `pkg/slot/proxy`

Responsibilities:

- Persist `UserConversationState` rows by UID-owner hash slot.
- Provide `TouchUserConversationActiveAt` upsert semantics for missing states.
- Ignore active touches with `MessageSeq <= DeletedToSeq`.
- Provide durable `HideUserConversations` that advances `DeletedToSeq`, clears `ActiveAt`, and removes active index entries atomically.
- Route hot hint submit/remove operations to UID owners without Raft propose.
- Merge hot hints into `ListUserConversationActive` on the authoritative UID owner.

## Data Model

### UserConversationState

Primary key: `(uid, channel_type, channel_id)`

Fields:

- `uid`
- `channel_id`
- `channel_type`
- `read_seq`
- `deleted_to_seq`
- `active_at`
- `updated_at`

Index:

- `(uid, active_at desc, channel_type, channel_id)` for working-set reads.

Semantics:

- `read_seq` is the user's read cursor.
- `deleted_to_seq` hides messages at or below the sequence.
- `active_at` is a best-effort recent activity hint.
- `updated_at` is the user-state compatibility watermark.

### UserConversationActiveHint

In-memory hint:

- `uid`
- `channel_id`
- `channel_type`
- `active_at`
- `message_seq`

Semantics:

- Stored on the UID owner.
- Visible through `ListUserConversationActive` before durable flush.
- May be dropped without affecting message durability.
- Stale if `message_seq <= deleted_to_seq`.

### UserConversationDeleteBarrier

In-memory delete barrier:

- `uid`
- `channel_id`
- `channel_type`
- `deleted_to_seq`

Semantics:

- Created after durable delete succeeds.
- Removes pending stale hints.
- Blocks delayed stale hints until TTL expiry.

## Sync Algorithm

Single request semantics; no cursor paging.

1. Route to the UID owner.
2. Parse `last_msg_seqs` into normalized `ConversationKey`s.
3. Read active working set with `ListUserConversationActive(uid, active_scan_limit)`.
4. `ListUserConversationActive` merges:
   - persisted active rows from the active index;
   - hot active hints from UID-owner overlay;
   - point-read inactive rows for hot hints so `DeletedToSeq` can fence stale hints.
5. Add client-known candidates from `last_msg_seqs`.
6. Apply `exclude_channel_types` to candidates.
7. Load latest messages for candidates from Channel Log facts.
8. Drop candidates whose latest `MessageSeq <= DeletedToSeq`.
9. Compute unread as `max(0, latest_seq - max(read_seq, deleted_to_seq))`.
10. If latest message was sent by the querying UID, return unread `0` and readed-to `latest_seq` for compatibility.
11. Apply `only_unread`.
12. Sort by latest message display timestamp desc, then `channel_type`, then `channel_id`.
13. Truncate to request `limit`.
14. Load recents for returned conversations only.
15. Filter recents to `message_seq > deleted_to_seq`.
16. Return the legacy-compatible array response.

`SyncQuery.Version` remains accepted for legacy clients but is not used to discover a complete historical delta.

## Ordering And Version

`active_at` only selects candidate working set entries. It does not define final ordering.

Final display order:

1. latest message timestamp desc
2. channel type asc
3. channel ID asc

Returned item `version` is a compatibility watermark:

- `max(latest_message_timestamp_unixnano, UserConversationState.updated_at)`

Clients should continue sending `last_msg_seqs`; server-side candidate discovery is based on working-set hints, not a complete version cursor.

## Delete Semantics

`DeleteConversation` must:

1. Validate UID and conversation key.
2. Use command `MessageSeq` as delete barrier, or load latest Channel Log sequence when it is zero.
3. Return an error if no non-zero delete barrier can be determined.
4. Persist `UserConversationDelete` through `HideUserConversations` before touching hot cache.
5. Clear `ActiveAt` and active index atomically with `DeletedToSeq` update.
6. Best-effort remove UID-owner hot hints and install `UserConversationDeleteBarrier`.

Visibility rules:

- latest message `MessageSeq <= DeletedToSeq`: conversation hidden.
- hint `MessageSeq <= DeletedToSeq`: hint ignored.
- recent message `MessageSeq <= DeletedToSeq`: recent hidden.
- later message `MessageSeq > DeletedToSeq`: conversation can reappear.

## Failure And Fallback Behavior

- Active hint submit failure: log/drop; durable send remains successful after Channel Log quorum commit.
- Active hint overlay read failure: fall back to persisted active rows.
- Active hint flush failure: keep hot hints until TTL/capacity or retry; do not block send/replay.
- Missing hot hint: client `last_msg_seqs` can keep known conversations visible.
- Missing latest Channel Log fact: candidate is omitted for sync; delete with no explicit sequence returns an error.

Correctness must always reduce to `UserConversationState + Channel Log` for candidates already selected by the working-set/client overlay.

## Cutover Notes

- Backfill `UserConversationState` before enabling default traffic for migrated users.
- No separate channel update projection table is required for new messages.
- Observe active hint cache drops, flush errors, overlay hit behavior, and sync latency.
- Verify delete/reappear behavior during rollout.

## Proposed Package Layout

- `internal/access/api/conversation_sync.go`
- `internal/usecase/conversation/app.go`
- `internal/usecase/conversation/sync.go`
- `internal/usecase/conversation/delete.go`
- `internal/usecase/conversation/projector.go`
- `internal/usecase/conversation/active_hint_cache.go`
- `pkg/slot/meta/user_conversation_state.go`
- `pkg/slot/fsm/command.go`
- `pkg/slot/proxy/user_conversation_state_rpc.go`

## Test Requirements

- active hint upsert creates missing user conversation state.
- stale active hint cannot revive deleted conversations.
- `ListUserConversationActive` merges persisted active rows and hot hints.
- sync uses active working set plus client-known overlays only.
- request `version` does not trigger full directory/channel probing.
- committed replay and overflow fallback do not wait for active hint flush.
- delete clears durable active state, removes hot hints, and allows newer messages to reappear.
