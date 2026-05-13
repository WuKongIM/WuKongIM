# Send CMD Conversation Intent P2d-d Design

## Overview

P2d-d restores the legacy CMD conversation update model without adding a durable per-message subscriber snapshot store.

The old WuKongIM implementation in `learn_project/WuKongIM/internal/manager/manager_conversation.go` did not persist message subscriber snapshots. It used the delivery tag that had already been resolved by the distribute path, converted the tag's local UID partition into a pending conversation update, and periodically flushed that pending update to `ConversationTypeCMD` rows. On graceful stop it saved only pending conversation updates to a local JSON file and reloaded them on start.

The current WuKongIM implementation already has a dedicated `CMDConversationState` and UID-owned `/message/sync` routing from P2d-c. P2d-d should add the missing old-compatible update path:

```text
resolved CMD recipients
  -> CMD conversation update intent
  -> UID-owner pending buffer
  -> periodic flush to CMDConversationState
  -> /message/sync overlays pending + persisted state
```

This replaces the earlier idea of storing request-scoped subscriber snapshots in channel logs, slot meta, or a side table.

## Goals

- Use the already-resolved recipient source for CMD conversation updates.
- Preserve old `ConversationManager` semantics: store pending conversation update results, not raw message subscriber snapshots.
- Keep CMD conversation state separate from ordinary conversation state.
- Route updates to the UID owner before they become visible to `/message/sync`.
- Support graceful-stop recovery of unflushed CMD conversation intents with a local pending file.
- Make `/message/sync` see unflushed pending CMD activity on the UID owner, similar to the old `GetUserChannelsFromCache` overlay.
- Avoid introducing any non-cluster single-node shortcut. A single-node deployment remains a single-node cluster.

## Non-Goals

- No new durable subscriber snapshot table.
- No channel-log message format change.
- No slot-meta snapshot of request-scoped subscribers.
- No kill -9 durability guarantee for intents accepted only in memory. This phase follows the old graceful-stop persistence model.
- No `/message/sendbatch` restoration.
- No `expire`, `AllowStranger`, plugin, webhook, AI, or offline webhook changes.
- No change that makes ordinary `/conversation/sync` return CMD channels.
- No reintroduction of a global service-layer `ConversationManager` singleton.

## Legacy Behavior Reference

Relevant old behavior:

```text
Handler.distributeByTag(...)
  -> local node has tag UIDs
  -> h.conversation(channelId, channelType, tag.Key, events)
  -> ConversationManager.Push(channelId, channelType, tagKey, events)
  -> tag := TagManager.Get(tagKey)
  -> nodeUsers := tag.GetNodeUsers(localNodeID)
  -> UserReadSeqs[uid] = messageSeq for sender, 0 for receivers
  -> pendingUpdates[channelKey] = channelUpdate
  -> loopStoreConversations()
  -> Store.AddConversationsIfNotExist(... ConversationTypeCMD ...)
```

Important old properties:

- CMD channels are typed as `ConversationTypeCMD` and excluded from ordinary chat conversation sync.
- Live channels and non-persist events are skipped.
- The pending cache has a `userIndex` so sync can see channels before DB flush.
- Stop saves pending updates to `DataDir/conversationv2/conversations.json`; start reloads and deletes the file.
- Sender read state is initialized to the sent message sequence if the sender is part of the recipient set.
- Receiver read state starts at zero.

## Current State Before P2d-d

P2d-c already added:

- Dedicated `CMDConversationState` in slot meta.
- `/message/sync` and `/message/syncack` access APIs.
- UID-owner routing for CMD sync APIs.
- Command-channel log reads from `source____cmd` authority.
- Ordinary `/conversation/sync` filtering of CMD channels and CMD overlays.
- A `cmdsync.Projector` that can build CMD state from `MessageScopedUIDs` or by paging subscribers.

Remaining issue:

- The current projection still models CMD updates as a post-commit subscriber scan/projection. It does not yet mirror old delivery-tag-driven conversation update semantics.
- Request-scoped durable sends still depend on request-local `MessageScopedUIDs`; replay after process restart cannot reconstruct that exact request scope from the channel log.
- Unflushed CMD activity is not represented as an old-style pending conversation update visible to `/message/sync`.

## Design Decision

P2d-d should not store subscriber snapshots. It should store and recover conversation update intents.

A conversation update intent is the business result needed for CMD sync:

```text
command channel became active for these UIDs at this command message seq
these UIDs should have this initial read seq
```

The intent is independent of how the recipient set was produced. It may come from:

- request-scoped `SendCommand.RequestSubscribers`;
- message-scoped delivery metadata already attached to an envelope;
- delivery tag local UID partitions;
- a local subscriber resolver fallback that pages UIDs before presence expansion.

### Migration from the P2d-c projector

P2d-d replaces the current `cmdsync.Projector` subscriber-scan projection path as the CMD conversation state writer.

Implementation must do one of these two equivalent refactors:

- remove `cmdsync.Projector` from committed fanout and move its useful helper logic into the intent builder / pending updater; or
- keep the type as a compatibility wrapper, but make it enqueue already-resolved `ConversationIntent` values only.

It must not keep this P2d-c behavior:

```text
MessageCommitted
  -> cmdsync.Projector
  -> page SubscriberResolver after commit
  -> write CMDConversationState directly
```

Keeping both paths would preserve the rejected subscriber-scan model and can double-write CMD state. After P2d-d, CMD conversation updates should be produced by request-scoped recipients or delivery-resolved UID pages, then flushed through the owner-local pending updater.

Committed replay should follow the same rule. It may replay delivery so the delivery resolver emits UID pages again, but it must not run an independent CMD subscriber scan projector. Request-scoped replay without `MessageScopedUIDs` remains best effort and is not repaired by a snapshot table in this phase.

## Core Data Structures

Add a usecase-level value in `internal/usecase/cmdsync`:

```go
// ConversationIntent describes one CMD conversation update before it is flushed.
type ConversationIntent struct {
    CommandChannelID string
    ChannelType      uint8
    MessageSeq       uint64
    ActiveAt         int64
    SenderUID        string
    UserReadSeqs     map[string]uint64
}
```

Rules:

- `CommandChannelID` must be a `source____cmd` ID.
- `MessageSeq` must be non-zero.
- `UserReadSeqs` must include only non-empty UIDs.
- Sender UID gets `MessageSeq` only when the sender is part of the resolved recipient set.
- Receivers get `0`.
- Duplicate UIDs are collapsed before the intent is enqueued.
- `NoPersist` and seq-zero realtime messages never create intents.
- `ActiveAt` should be derived from the message timestamp when present, otherwise from the injected clock. Tests should inject the clock rather than relying on wall time.

The pending buffer can store a normalized shape close to the old `channelUpdate`:

```go
// PendingConversationUpdate is the owner-local buffered update for one CMD channel.
type PendingConversationUpdate struct {
    CommandChannelID string            `json:"command_channel_id"`
    ChannelType      uint8             `json:"channel_type"`
    LastMsgSeq       uint64            `json:"last_msg_seq"`
    ActiveAt         int64             `json:"active_at"`
    UserReadSeqs     map[string]uint64 `json:"user_read_seqs"`
}
```

`/message/sync` should read a UID-specific view rather than exposing the whole pending update:

```go
// PendingConversationView is the pending overlay for one UID and CMD channel.
type PendingConversationView struct {
    CommandChannelID string
    ChannelType      uint8
    LastMsgSeq       uint64
    ActiveAt         int64
    ReadSeq          uint64
}
```

Field meanings:

- `CommandChannelID` and `ChannelType` identify the command-channel log to fetch.
- `LastMsgSeq` is the highest pending message seq for cleanup and diagnostics.
- `ActiveAt` participates in candidate ordering and active-state merge.
- `ReadSeq` is this UID's pending read seq for the channel, usually `0` for receivers and `MessageSeq` for sender self-read.

The owner-local buffer also maintains an inverted index:

```text
pendingByChannel[channelKey] -> PendingConversationUpdate
userIndex[uid] -> set(channelKey)
```

This mirrors the old cache and lets `/message/sync` discover unflushed CMD channels for one UID without scanning the whole buffer.

All channel-scoped pending operations must key by both `CommandChannelID` and `ChannelType`. Implementations may reuse the existing `cmdsync.CommandChannelKey` shape:

```go
type CommandChannelKey struct {
    ChannelID   string
    ChannelType uint8
}
```

No pending cleanup or merge operation should key by channel ID alone.

## Component Design

### 1. CMD conversation intent builder

`internal/usecase/cmdsync` should expose a small builder/helper that takes a committed CMD message and recipient UIDs:

```text
BuildConversationIntent(message, recipientUIDs)
```

It should:

- verify durable CMD message eligibility;
- normalize `source` to `source____cmd`;
- remove empty/duplicate UIDs;
- assign sender read seq only if sender is in the normalized UID set;
- return no intent for empty recipient sets.

This keeps business rules out of `internal/access/*` and prevents delivery routing from duplicating sender-read logic.

### 2. UID-owner intent router

Add an app-layer router, because UID ownership requires cluster/runtime dependencies:

```text
cmdsync.ConversationIntent
  -> group UserReadSeqs by UID owner node
  -> local owner: PendingUpdater.PushIntent
  -> remote owner: access/node RPC PushCMDConversationIntent
```

Routing uses:

```text
slotID := cluster.SlotForKey(uid)
owner  := cluster.LeaderOf(slotID)
```

The router may split one channel intent into multiple owner-local intents, each containing only the UIDs owned by that target node. This keeps the pending buffer on the same node that serves `/message/sync` for those UIDs.

The remote RPC is an entry adapter only:

```text
internal/access/node/cmdsync_intent_rpc.go
  -> decode request
  -> call local cmdsync pending updater
  -> encode status
```

It must not contain business rules beyond validation and transport mapping.

The receiver should preserve the owner-local invariant. If the RPC target no longer owns a UID in the intent, it should reject that UID or the whole request with a retryable stale-owner error rather than storing it locally. The caller can then re-resolve UID ownership and retry.

### 3. Owner-local pending updater

Add an owner-local lifecycle component in `internal/usecase/cmdsync`:

```text
type ConversationUpdater interface {
    Start() error
    Stop() error
    PushIntent(ctx, intent) error
    ListPending(ctx, uid, limit) []PendingConversationView
    MarkSynced(ctx, uid, commandChannelKey, throughSeq) error
    Flush(ctx) error
}
```

Responsibilities:

- shard pending updates by command channel key to reduce lock contention;
- maintain `userIndex` for UID lookups;
- coalesce multiple intents for the same command channel;
- flush bounded batches to `CMDConversationState`;
- on graceful stop, attempt a final bounded flush, then save whatever remains pending to a local file;
- load pending updates on start and remove the file after successful load.

Coalescing rules:

- `LastMsgSeq = max(existing.LastMsgSeq, intent.MessageSeq)`.
- `ActiveAt = max(existing.ActiveAt, intent.ActiveAt)`.
- For each UID, `UserReadSeqs[uid] = max(existingReadSeq, newReadSeq)`.
- A receiver update with `0` must not regress a sender or acked read seq.

Idempotency rules:

- Duplicate intent submission is safe.
- Pending coalescing is monotonic by `(CommandChannelID, ChannelType, UID)`.
- State upsert must be monotonic and must not regress `ReadSeq`, `ActiveAt`, or latest activity.
- This allows request-scoped direct intent routing and delivery-observer retry to overlap without creating duplicate unread state.

Flush rules:

- Convert pending UID/channel entries into `metadb.CMDConversationState`.
- Use existing state upsert semantics that do not regress `ReadSeq`.
- Remove only UID/channel entries that were successfully flushed. A successful flush for one UID must not remove other UIDs still pending on the same command channel.
- If a batch flush partially fails, keep the unconfirmed entries pending.
- The flush loop must be bounded by a `SyncOnce`-style limit to avoid long stalls.

The pending file should live under the node data directory, for example:

```text
<DataDir>/conversationv2/cmd_conversation_updates.json
```

The write should use temp-file plus rename so a partial write does not replace the last complete file.

### 4. Request-scoped durable send hook

For durable request-scoped CMD sends, `SendCommand.RequestSubscribers` is already the exact business recipient set. The safest old-compatible path is:

```text
message.App.sendDurable
  -> append to temp____cmd channel
  -> build CMD conversation intent from RequestSubscribers
  -> route intent to UID owners
  -> submit committed delivery envelope with MessageScopedUIDs and intent-submitted flag
```

This avoids waiting for async delivery to rediscover the same request-local recipient list.

The send result should not become a false durable failure only because the best-effort conversation intent route failed after append succeeded. The router should log/trace the failure and return a non-fatal side-effect result. Durable append success remains the source of truth for the message write.

Duplicate suppression must be explicit. Add an in-memory event/envelope metadata flag, for example:

```go
CMDConversationIntentSubmitted bool
```

Rules:

- Set the flag only when all UID-owner partitions for the request-scoped intent were accepted by the local pending updater or remote owner RPC.
- If any owner partition fails, leave the flag false and let the delivery UID observer make a second best-effort attempt from `MessageScopedUIDs`.
- Partial success is acceptable because duplicate submission to already-accepted partitions is idempotent.
- The flag is not written to the channel log and is not expected to survive committed replay.
- The delivery observer skips request-scoped intent generation only when `CMDConversationIntentSubmitted` is true.
- P2d-d does not relax the existing strictness of request-scoped committed delivery submission. If current message-scoped delivery submission fails after append, the send path may keep returning the existing dispatch error. Only the CMD conversation intent side effect is non-fatal.

### 5. Delivery-tag UID hook for ordinary CMD channels

For ordinary durable CMD channels, the recipient set should come from the delivery resolver path, not from a separate post-commit subscriber scan.

Add a narrow observer interface near the app delivery resolver wiring:

```text
type ResolvedUIDPage struct {
    Envelope                       CommittedEnvelope
    UIDs                           []string
    CMDConversationIntentSubmitted bool
}

type ResolvedUIDObserver interface {
    OnResolvedUIDPage(ctx, page)
}
```

Call it before presence expansion, because CMD sync needs offline users too:

```text
subscriber/tag UID page
  -> observer receives UIDs
  -> presence authority expands online routes
  -> delivery runtime pushes online packets
```

The observer should receive UID pages and the envelope metadata, not online `RouteKey` values. Route keys would drop offline users and would make CMD sync incomplete. The envelope metadata lets the observer skip request-scoped pages when `CMDConversationIntentSubmitted=true`.

Observer calls are side effects and must not fail delivery resolution. The observer implementation may enqueue work, log failures, and record metrics, but delivery code should not branch on observer success. If the app layer wants retries, it should implement them inside the intent router or pending updater queue, not by returning errors to the delivery actor.

For tag-based delivery:

- use the tag local UID page currently being expanded by `tagDeliveryResolver`;
- if a follower receives only its local partition, it submits intents for that partition only;
- if the local node is also the UID owner for that partition, the router writes local pending directly;
- if topology changes or a partition contains UIDs whose owner moved, the router re-resolves UID owner and forwards correctly.

For local resolver fallback:

- use the UIDs returned by `SubscriberResolver.NextPage` before presence expansion;
- route by UID owner exactly the same way.

The hook must be no-op for non-CMD messages and `NoPersist` messages.

### 6. `/message/sync` pending overlay

`cmdsync.App.Sync` should merge persisted state and owner-local pending state.

Flow:

```text
Sync(uid)
  -> list persisted CMDConversationState for uid
  -> list pending CMD conversation views for uid
  -> merge by command channel key
  -> for each candidate:
       effectiveReadSeq = max(persisted.ReadSeq, pending.UserReadSeqForUID)
       activeAt = max(persisted.ActiveAt, pending.ActiveAt)
       fetch source____cmd messages from effectiveReadSeq + 1
  -> return messages and record sync generation as P2d-c already does
```

Important rules:

- Pending receiver read seq `0` activates the channel but does not override a persisted read seq.
- Pending sender read seq equal to `MessageSeq` prevents the sender's own CMD message from being returned as unread when no persisted state exists yet.
- If pending indicates a channel is active but fetch returns no messages beyond `effectiveReadSeq`, no sync record is created for that channel.
- Persisted and pending candidates share the same deterministic ordering and global limit rules from P2d-c. The merge must decide candidate order before fetching enough messages to satisfy the global response limit.
- Ordinary `/conversation/sync` must not use this pending buffer.

### 7. `/message/syncack` pending cleanup

`cmdsync.App.SyncAck` should continue to advance persisted `CMDConversationState` from the latest sync generation. It should also notify the pending updater after advancement:

```text
MarkSynced(uid, CommandChannelKey{ChannelID: commandChannelID, ChannelType: channelType}, ackSeq)
```

Cleanup rules:

- If `ackSeq >= pending.LastMsgSeq` for that UID/channel, remove the UID from that pending update.
- If no UIDs remain in the pending channel update, remove the channel entry and index entry.
- If `ackSeq` is lower than `LastMsgSeq`, keep the pending entry for later sync.
- Persisted read seq advancement must remain monotonic; pending cleanup must never be the only source of read progress.

## Failure Handling

- Missing UID owner leader: keep the intent on the originating node only if the router has a retry queue; otherwise log and drop as a side effect failure. Do not fake local ownership.
- Remote RPC failure: retry a bounded number of times if the caller is already async; otherwise log and rely on later delivery/replay paths where available.
- Pending queue full: log, increment a metric, and drop the side effect rather than blocking the delivery actor indefinitely.
- Flush failure: keep pending entries in memory and retry on the next tick.
- Stop save failure: log clearly; do not panic during shutdown.
- Load file decode failure: log and leave the bad file for operator inspection, preferably by renaming it with a `.bad` suffix.

The phase intentionally preserves old best-effort conversation side-effect semantics. It improves graceful-stop recovery for accepted pending updates but does not claim complete crash recovery for every append-before-intent window.

## Layering

- `internal/usecase/cmdsync`: intent model, builder, pending updater, sync overlay, ack cleanup.
- `internal/app`: UID-owner router, lifecycle wiring, delivery observer wiring, cluster/node dependencies.
- `internal/access/node`: thin RPC adapter for remote owner intent submission.
- `internal/usecase/message`: only calls the app-provided intent sink for request-scoped durable sends; no cluster routing logic.
- `internal/runtime/delivery`: no CMD business rules; if touched, expose only generic UID page observer hooks through app wiring.

No new global service object should be introduced.

## Testing Strategy

### `internal/usecase/cmdsync`

- Intent builder strips/normalizes command channel IDs and rejects non-durable or `NoPersist` messages.
- Intent builder deduplicates UIDs and assigns sender read seq only when sender is included.
- Pending updater coalesces multiple messages without read seq regression.
- Pending updater maintains `userIndex` and lists only the requested UID's channels.
- Flush converts pending entries to `CMDConversationState` and removes only successful entries.
- Stop/save and start/load round trip pending updates.
- Sync merges persisted state and pending state with `max(readSeq)` semantics.
- SyncAck advances persisted state and cleans pending entries only through the acked seq.

### `internal/app`

- UID-owner router sends local UID partitions to the local pending updater.
- UID-owner router forwards remote UID partitions through node RPC.
- Request-scoped durable send builds exactly one intent from `RequestSubscribers`.
- If direct request-scoped intent routing fails after durable append, send does not fail only because of the intent side effect, the committed delivery envelope has `CMDConversationIntentSubmitted=false`, and the delivery UID observer retries intent submission once from `MessageScopedUIDs`.
- Partial request-scoped owner-route success leaves `CMDConversationIntentSubmitted=false`; the delivery UID observer may retry all UIDs, and already-accepted UID/channel entries remain correct because intent submission is idempotent.
- Message-scoped delivery path does not double-submit intents for the same request-scoped send.
- Delivery UID observer receives UID pages before presence expansion and includes offline users.
- Delivery UID observer failures are logged/recorded but do not fail delivery resolution or online push.
- Ordinary non-CMD committed messages do not produce CMD intents.

### `internal/access/node`

- CMD conversation intent RPC codec round trips command channel ID, type, seq, active time, and per-UID read seqs.
- RPC rejects malformed intents and missing local updater wiring.
- RPC remains a thin adapter; business validation stays in `cmdsync`.

### Regression

- `/message/sync` still strips exactly one `____cmd` suffix in responses.
- `/message/syncack` still uses the latest sync record generation, not `last_message_seq` as a channel selector.
- Ordinary `/conversation/sync` does not return CMD channels, including pending CMD channels.
- `NoPersist` command-style messages remain online-only.
- Partial owner-route failure on a request-scoped durable send retries through the delivery observer without duplicating unread state for already-accepted partitions.
- Focused import-boundary tests still pass.

## Rollout Notes

- No config changes are required for the first implementation. Use existing internal defaults or existing conversation sync interval values if already available in the composition root.
- After implementation, update `docs/raw/send-path-business-logic-diff.md` to replace the previous subscriber-snapshot gap with the pending intent model.
- After implementation, add a concise project knowledge note if the intent-vs-snapshot distinction is not already documented.
- Full crash-safe recovery, if later required, should be designed separately as an intent WAL or replicated side-effect log. It should not be confused with subscriber snapshot storage.
