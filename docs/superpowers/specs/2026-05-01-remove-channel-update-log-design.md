# Remove ChannelUpdateLog Design

## Overview

This design removes the persistent `ChannelUpdateLog` table from the recent conversation read path.
Conversation sync will no longer use `version` to discover updates across a user's full historical
conversation directory. Instead, it will sync the recent working set plus client-known conversations,
using Channel Log as the only message fact source.

`ActiveAt` remains, but it becomes a best-effort working-set hint. It may be delayed or dropped and
must not block sendack, committed replay, or `/conversation/sync`.

## Goals

- Remove per-message `upsert_channel_update_logs` writes.
- Keep Channel Log as the only source for latest message facts.
- Keep `/conversation/sync` fast by using `UserConversationState.active_at` and an in-memory active hint cache.
- Allow `ActiveAt` updates to be batched, throttled, dropped, and repaired by reads.
- Preserve delete semantics: deleting a conversation hides messages through `deleted_to_seq`, but later messages make it reappear.

## Non-Goals

- Do not provide full historical incremental discovery through `/conversation/sync`.
- Do not update `ActiveAt` persistently for every message.
- Do not fan out every group message to every subscriber.
- Do not make active hint delivery part of message durability.

## Data Model Changes

### Removed Projection

`ChannelUpdateLog` is no longer part of the conversation sync model.

- `pkg/slot/meta/channel_update_log.go` should be removed or deprecated during migration.
- `pkg/slot/proxy/channel_update_log_rpc.go` should be removed or deprecated.
- Slot FSM command types for `UpsertChannelUpdateLogs` and `DeleteChannelUpdateLogs` should not be used.
- Existing command and table IDs must not be reused.

### UserConversationState

`UserConversationState` remains the user-scoped conversation state:

- `ReadSeq`: user read cursor.
- `DeletedToSeq`: delete/hide barrier.
- `ActiveAt`: best-effort recent working-set hint.
- `UpdatedAt`: user-state update version for low-frequency user operations.

`TouchUserConversationActiveAt` keeps upsert behavior. If a user conversation state does not exist,
touching `ActiveAt` creates it with default read/delete state.

## Active Hint Cache

Add an in-memory active hint cache on the UID owner node.

```go
type UserConversationActiveHint struct {
    UID         string
    ChannelID   string
    ChannelType int64
    ActiveAt    int64
    MessageSeq  uint64
}
```

The cache stores the latest hint per `(uid, channel_type, channel_id)`:

- keep only max `MessageSeq`;
- if `MessageSeq` is equal, keep max `ActiveAt`;
- enforce per-UID and global capacity limits;
- expire old hints by TTL;
- drop hints when queues are full.

The cache also tracks short-lived delete barriers:

```go
type UserConversationDeleteBarrier struct {
    UID          string
    ChannelID    string
    ChannelType  int64
    DeletedToSeq uint64
}
```

The barrier prevents delayed old hints from reviving a deleted conversation. It must not block new
messages after deletion.

```text
if hint.MessageSeq <= barrier.DeletedToSeq:
    drop hint
else:
    accept hint
```

## ActiveAt Write Strategy

Active hints are produced asynchronously after committed messages. `Projector.SubmitCommitted` must only update local memory or enqueue bounded work; it must not perform remote UID-owner RPCs, subscriber fanout, or durable active writes inline.

### Person Channels

For person channels, decode the two user IDs and enqueue one hint per user.

```text
person message committed
  -> enqueue hint for uid A
  -> enqueue hint for uid B
  -> uid owner cache dedupes and batches
```

The persistent `TouchUserConversationActiveAt` write happens later through a throttled batch flush.

### Group Channels

For group channels, avoid per-message subscriber fanout.

Recommended controls:

- per-channel fanout interval, for example one fanout per group per 5 minutes;
- max subscriber count for fanout;
- global max active touch patches per second;
- bounded queues with drop-on-overflow;
- short retry budget only, then drop.

Large groups may skip fanout entirely. Clients can still surface known conversations through
`last_msg_seqs`, and active users can be repaired by sync read-repair.

### Flush

The UID owner flushes cached hints to `TouchUserConversationActiveAt` in batches.

Flush behavior:

- group by UID hash slot / physical slot;
- write max active hint per conversation;
- do not block sendack;
- do not block committed replay cursor;
- on failure, keep a short retry budget or drop.

## ListUserConversationActive Overlay

`ListUserConversationActive(uid, limit)` must merge persisted active state with the UID owner cache.

Add a store overlay interface:

```go
type UserConversationActiveOverlay interface {
    ListHotUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationActiveHint, error)
    SubmitHints(ctx context.Context, hints []metadb.UserConversationActiveHint) error
    RemoveHints(ctx context.Context, barriers []metadb.UserConversationDeleteBarrier) error
}
```

Merge rules:

1. Read persisted active states from `UserConversationState.active_at`.
2. Read hot hints from the UID owner active hint cache.
3. Drop hints blocked by delete barriers.
4. Merge by `(uid, channel_type, channel_id)`.
5. For hot hint keys missing from the active index, point-read the persisted `UserConversationState` before synthesizing a state. Deleted/inactive rows can have `ActiveAt = 0` and still carry a required `DeletedToSeq` barrier.
6. If persisted state exists, preserve `ReadSeq`, `DeletedToSeq`, and `UpdatedAt`.
7. If `hint.MessageSeq <= state.DeletedToSeq`, do not use that hint to reactivate the conversation.
8. If persisted state does not exist, synthesize a `UserConversationState` from the hint.
9. Use max active time for ordering.
10. Sort by `ActiveAt` descending and return top `limit`.

The merge must run on the UID owner. Non-owner API nodes should RPC to the UID owner, and the owner
combines local persisted state with local hot hints in the RPC handler as well as in local calls.

## Delete Semantics

Deleting a conversation clears current recent visibility, but later messages must reappear. The durable state update must use a dedicated hide/delete operation that can clear `ActiveAt`; it must not reuse monotonic `UpsertUserConversationStates`, because that upsert preserves the maximum existing `ActiveAt`.

```text
delete conversation at seq N
  -> set DeletedToSeq = max(old, N)
  -> set ActiveAt = 0
  -> set UpdatedAt = now
  -> remove pending active hint from cache
  -> record delete barrier DeletedToSeq = N
```

Hint handling:

```text
hint seq <= DeletedToSeq: drop
hint seq > DeletedToSeq: accept and reactivate
```

Sync handling:

- latest message with `MessageSeq <= DeletedToSeq` is hidden;
- latest message with `MessageSeq > DeletedToSeq` is visible;
- `recents` must filter out messages at or below `DeletedToSeq`.

## Conversation Sync Flow

`/conversation/sync` becomes working-set based.

```text
1. ListUserConversationActive(uid, activeScanLimit)
2. Merge client last_msg_seqs as explicit known conversations
3. Batch load latest messages from Channel Log
4. Filter deleted conversations using DeletedToSeq
5. Compute unread, timestamp, last_msg_seq, last_client_msg_no
6. Sort by latest message timestamp
7. Return top limit
8. Load recents only for returned conversations
9. Optionally enqueue read-repair active hints for returned conversations
```

The request `version` no longer triggers a full scan of `UserConversationState` plus channel update
projection. Response `version` may remain for legacy compatibility, but it is not a full historical
incremental cursor.

## Read-Repair

Read-repair is optional and best-effort. It only touches conversations that were already returned by
the current sync response.

Rules:

- do not block sync response;
- do not fan out to group subscribers;
- throttle by `(uid, channel)`;
- cap repaired conversations per request;
- drop on queue overflow.

The purpose is to improve future `ListUserConversationActive` hit rate when earlier active hints were
dropped or delayed.

## Migration

Rollout should be staged:

1. Add active hint cache and `ListUserConversationActive` overlay.
2. Change sync to stop depending on `ChannelUpdateLog`.
3. Change projector to produce active hints instead of channel update logs.
4. Keep old `ChannelUpdateLog` RPC/table code unused for one compatibility window.
5. Remove or hard-deprecate `ChannelUpdateLog` code and update `pkg/slot/FLOW.md`.

Existing `ChannelUpdateLog` rows can be ignored. They are projection data and do not contain unique
message facts.

## Tests

Required unit tests:

- `ListUserConversationActive` merges DB state and hot cache.
- Cache hint can synthesize a missing user conversation state.
- Delete removes pending hint and blocks old hint by `DeletedToSeq`.
- Hint with `MessageSeq > DeletedToSeq` reactivates a deleted conversation.
- Sync no longer calls `BatchGetChannelUpdateLogs`.
- Sync uses Channel Log latest messages for ordering.
- Person channel active hints are deduped and batched.
- Group channel active fanout is throttled and droppable.

Required integration tests:

- multi-node sync reads hot active hints from UID owner;
- non-owner API request routes active list merge to UID owner;
- committed replay advances cursor without waiting for active hint flush;
- deleting a conversation then sending a newer message makes it reappear.

## Risks

- Cold historical conversations no longer appear through `version`-based full incremental discovery.
- Large group recent-list freshness depends on throttled fanout, client-known conversations, and read-repair.
- If active hint cache capacity is too small, recent working set hit rate may drop.
- If delete barriers expire too quickly, very delayed old hints may temporarily reactivate deleted conversations.

These risks are accepted because `ActiveAt` is a performance hint, not message durability or message
correctness state.
