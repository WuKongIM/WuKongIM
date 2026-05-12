# Send CMD Conversation Offline Sync P2d-c Design

## Overview

P2d-c restores the legacy CMD conversation/offline sync compatibility surface for durable command messages while preserving the current WuKongIM layering and cluster semantics.

The current project already restored CMD send identity, source-channel permission/subscriber authority, and command-style realtime `NoPersist` delivery. It still intentionally filters CMD messages from ordinary conversation projection. P2d-c keeps that ordinary conversation boundary and adds a separate CMD sync model for the legacy `/message/sync` and `/message/syncack` APIs.

The key design split is:

```text
ordinary conversation sync  = /conversation/sync, chat working set, source channels only
CMD offline sync            = /message/sync + /message/syncack, CMD working set, source____cmd logs
```

## Goals

- Restore HTTP `/message/sync` for durable CMD messages.
- Restore HTTP `/message/syncack` for CMD read cursor advancement.
- Keep CMD conversations separate from ordinary chat conversations.
- Keep ordinary `/conversation/sync` filtering CMD channels and `SyncOnce` messages.
- Store and read durable CMD messages by the command channel log key: `source____cmd`.
- Show source-channel client views in `/message/sync` responses, not internal `____cmd` channel IDs.
- Keep non-durable command-style `NoPersist` messages online-only.
- Define how UID-owned CMD conversation state and command-channel log ownership interact in a cluster.
- Leave durable request-scoped subscriber snapshot replay recovery to P2d-d.

## Non-Goals

- No `/message/sendbatch` restoration.
- No `expire` behavior change.
- No `AllowStranger` restoration.
- No plugin, webhook, AI, or offline webhook side effects.
- No global durable `systemcmdonline` channel.
- No ordinary temporary channel delivery beyond the request-scoped behavior already restored.
- No change that makes `/conversation/sync` return CMD conversations.
- No direct local shortcut around cluster semantics; a single-node deployment remains a single-node cluster.

## Legacy Behavior Reference

Relevant legacy behavior in `learn_project/WuKongIM`:

- `internal/api/message.go` exposes `/message/sync` and `/message/syncack`.
- `/message/sync` returns messages from conversations of type `ConversationTypeCMD`.
- `/message/syncack` advances read state for the records returned by the last `/message/sync` call.
- `internal/manager/manager_conversation.go` updates CMD conversations separately from chat conversations when the channel ID has the CMD suffix.
- `internal/api/conversation.go` filters CMD channels out of ordinary conversation sync.
- `internal/api/message.go` uses `systemcmdonline` for old non-durable online CMD delivery; current WuKongIM should keep the compatibility outcome, not the old global channel implementation.

## Current State Before P2d-c

Already restored:

- `internal/usecase/message.Send` strips a single CMD suffix before permission checks.
- Durable `SyncOnce` and already-addressed durable CMD sends append to `source____cmd`.
- CMD permissions use source-channel business authority.
- CMD subscriber resolution uses source-channel subscriber authority.
- Visitors CMD uses `(source, CustomerService)` for non-self permission/subscriber authority.
- Realtime packets strip `____cmd` for client views.
- Ordinary conversation projection skips CMD channels and `SyncOnce` messages.
- Command-style `NoPersist` sends dispatch realtime without writing channel logs.

Still missing:

- No `/message/sync` API in the current access layer.
- No `/message/syncack` API in the current access layer.
- No CMD conversation working set for durable CMD messages.
- No CMD read cursor separate from ordinary conversation read cursor.
- Durable request-scoped subscriber snapshots are not recoverable from channel logs after committed replay.

## Data Model

P2d-c needs a CMD conversation state that is separate from ordinary conversation state.

Recommended logical key:

```text
UID              = receiver UID
ChannelID        = source____cmd
ChannelType      = original channel type
ConversationType = CMD
```

Fields should mirror the read/projection state needed for sync:

```text
ReadSeq       = last CMD message seq acknowledged by /message/syncack
DeletedToSeq  = optional future delete barrier for CMD sync
ActiveAt      = latest CMD activity timestamp
UpdatedAt     = state update timestamp/version
```

The storage key uses `source____cmd` because `MessageSeq` belongs to the command channel log. The client view is converted back to the source channel when responses are built:

```text
storage/sync key: g1____cmd / group
client view:      g1 / group
```

CMD state isolation is mandatory. The current ordinary `UserConversationState` path is type-less and `/conversation/sync` lists active states without a conversation-type filter. P2d-c must not write CMD rows into that existing active index unless it first adds a durable conversation-type dimension everywhere the state is keyed, indexed, encoded, and served over RPC.

The implementation plan must choose one of these two safe persistence shapes:

1. Add a dedicated CMD conversation state store/table and dedicated active index used only by `/message/sync` and `/message/syncack`.
2. Extend `UserConversationState` with `ConversationType` as part of the primary key, active index, codecs, and node RPC contracts.

Either shape must expose type-scoped APIs:

- ordinary conversation APIs read/write only `ConversationType=Chat`;
- CMD sync APIs read/write only `ConversationType=CMD`;
- chat clear unread, set unread, delete, and `/conversation/sync` must not touch CMD state;
- CMD `/message/syncack` must not touch chat state.

If the implementation cannot provide type-scoped storage in the current phase, P2d-c must stop before writing CMD conversation rows. A suffix-only filter on `____cmd` is not sufficient because it still allows CMD rows into ordinary active scans and future chat maintenance APIs.

## CMD Conversation Projection

P2d-c must not weaken the ordinary projector filter:

```text
ordinary Projector.SubmitCommitted:
  skip IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce
```

Instead, add a separate CMD projector/usecase path:

```text
MessageCommitted
  -> delivery.Submit
  -> conversation.SubmitCommitted       ordinary active hints, still skips CMD
  -> cmdsync.SubmitCommitted            CMD working set only
```

The CMD projector handles only durable committed CMD messages:

```text
is durable CMD = IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce
```

Rules:

- `NoPersist` realtime messages never reach this path and do not create CMD conversation state.
- The command channel log key remains `source____cmd`.
- Subscriber resolution must use the P2d-a source-authoritative `delivery.SubscriberResolver` behavior.
- For ordinary store-backed CMD channels, build/update CMD conversation state for resolved target UIDs.
- For person/agent CMD channels, use the derived UID pair from the resolver.
- For visitors CMD channels, use the `(source, CustomerService)` subscriber source plus visitor overlay.
- For info CMD channels with temporary overlays, use the non-reusable snapshot from the resolver.
- For durable request-scoped temp CMD messages carrying `MessageScopedUIDs`, create state only for those UIDs.
- Sender read behavior is deterministic: if the sender UID is included in the CMD recipient set, create/update the sender CMD state with `ReadSeq=MessageSeq` and `ActiveAt` updated, so self-sent durable CMD messages do not become unread for the sender. If the sender is not in the resolved recipient set, do not create an extra sender-only CMD state.

The projector should enqueue/write active hints or state asynchronously, similar to the current conversation projector. It must not block sendack on large subscriber scans or remote UID-owner writes.

## `/message/sync` API

P2d-c restores the legacy-compatible HTTP endpoint:

```http
POST /message/sync
```

Legacy-compatible request shape:

```json
{
  "uid": "u1",
  "message_seq": 0,
  "limit": 200
}
```

`message_seq` is the legacy client global maximum message sequence field. P2d-c should accept it for wire compatibility, but CMD sync selection is conversation-record based (`ReadSeq + 1` per command channel). The implementation plan may ignore `message_seq` for selection unless a later compatibility test proves an old client depends on it.

Access adapter responsibilities:

- Parse the legacy request fields, including `message_seq`.
- Apply default and maximum limits.
- Forward or RPC to the UID-owned node if the API node is not authoritative for the UID's CMD sync state.
- Call a reusable usecase, for example `cmdsync.App.Sync`.
- Return legacy-compatible message response objects.

Usecase flow:

```text
cmdsync.App.Sync(uid, limit)
  -> load UID's CMD conversation working set
  -> merge unflushed CMD active hints if P2d-c uses an in-memory hint cache
  -> for each CMD conversation:
       start seq = ReadSeq + 1
       fetch messages from command channel log source____cmd
  -> convert each returned message to source-channel client view
  -> sort deterministically by message timestamp, command channel key, and message seq
  -> globally limit the response to at most limit messages
  -> record per-channel last seq actually returned in a UID-owned sync record generation
  -> return messages
```

Ordering and limit rules:

- Response ordering must be deterministic across retries. Use ascending delivery order unless legacy API tests require a different order; tie-break by `CommandChannelID`, `ChannelType`, and `MessageSeq`.
- The global `limit` applies after merging candidate messages from all CMD conversations.
- If a channel fetch returns more messages than the remaining global limit, the sync record must store only the last sequence actually returned to the client, not the last sequence fetched.
- `/message/sync` must not create sync records for channels that produced no returned messages.

Message-log fetch authority:

- CMD conversation state is UID-owned.
- CMD message facts are command-channel-owned.
- Fetching from `source____cmd` must use the same channel runtime meta / node RPC style as existing conversation facts or channel message sync.
- The UID owner must not assume it also owns the command channel log.

Response view rules:

- `ChannelID` strips one `____cmd` suffix for clients.
- `ChannelType` remains unchanged.
- `SyncOnce` remains set in the message header.
- `NoPersist` should not appear in `/message/sync` because non-durable messages are not logged.
- Person channel views should follow the current client-facing person channel rules.

## `/message/syncack` API

P2d-c restores the legacy-compatible HTTP endpoint:

```http
POST /message/syncack
```

Legacy-compatible request shape:

```json
{
  "uid": "u1",
  "last_message_seq": 123
}
```

`last_message_seq` is required for legacy wire compatibility and should be validated as non-zero by the access adapter. It is not sufficient to choose which per-channel cursors to advance; exact channel advancement comes from the latest UID-owned sync record generation.

Access adapter responsibilities:

- Parse and validate `uid` and `last_message_seq`.
- Forward or RPC to the UID-owned node if needed.
- Call `cmdsync.App.SyncAck`.
- Return success for empty/no-op acks.

Usecase flow:

```text
cmdsync.App.SyncAck(uid)
  -> read and clear UID's sync records from the UID owner
  -> for each record:
       require ChannelID is a command channel
       ignore lastMsgSeq == 0
       advance CMD conversation ReadSeq to max(current, lastMsgSeq)
  -> persist updates in the CMD conversation state store
```

Rules:

- Sync ack updates only CMD conversation read state.
- Sync ack never advances ordinary conversation read state.
- Duplicate `syncack` calls are idempotent when no sync records remain.
- If the process restarts and the in-memory sync record cache is lost, a valid `syncack` request becomes a no-op; the next `/message/sync` may return those messages again.
- `last_message_seq` must not be used to advance every CMD conversation blindly.
- Missing CMD conversation rows should be ignored or logged as warnings, not surfaced as client errors.

## Sync Record Cache

Legacy `/message/syncack` does not carry explicit per-message ack details. It relies on server-side records from the previous `/message/sync`.

P2d-c should preserve that compatibility behavior with a UID-owned in-memory sync record cache:

```text
SyncRecordGeneration:
  UID
  GenerationID
  Records[]
  CreatedAt

SyncRecord:
  CommandChannelID
  ChannelType
  LastReturnedMsgSeq
```

Requirements:

- Records are scoped by UID and by generation.
- Each successful `/message/sync` replaces the previous generation for that UID with records from the latest response only.
- Latest sync wins for concurrent `/message/sync` calls on the same UID.
- `/message/syncack` atomically reads and clears only the latest generation for the UID.
- Records must be created only for channels that returned at least one message in that response.
- `LastReturnedMsgSeq` must be the last sequence actually returned to the client for that channel.
- Records should have a bounded TTL and bounded per-UID size to avoid unbounded memory growth.
- Losing records is safe but may cause duplicate sync until the client receives and acks again.

## Cluster Authority Model

P2d-c uses two authorities and must keep them separate.

### UID Conversation Authority

CMD conversation state and sync records are owned by UID:

```text
owner key = UID / person hash-slot or existing UID conversation owner rule
```

The API node should forward or RPC to the UID owner for `/message/sync` and `/message/syncack`.

### Command Channel Log Authority

CMD messages are fetched by command channel log owner:

```text
owner key = channel.ChannelID{ID: source____cmd, Type: originalType}
```

The UID owner may need to issue local or remote channel-log reads for each command channel. This should reuse existing channel message / conversation facts readers where possible.

### Single-Node Deployment

A single-node deployment is still a single-node cluster. The code path should use the same authority abstractions and simply resolve both authorities to the local node.

## `systemcmdonline` Compatibility Boundary

P2d-c should document `systemcmdonline` as a legacy implementation detail, not a current durable channel.

Current behavior after P2d-a/P2d-b:

- `NoPersist + SyncOnce` ordinary sends dispatch realtime directly.
- Already-addressed CMD `NoPersist` sends dispatch realtime directly.
- Request-scoped `NoPersist + SyncOnce + subscribers` sends dispatch realtime directly with `MessageScopedUIDs`.

P2d-c keeps all of these online-only:

- They do not write channel logs.
- They do not create CMD conversation state.
- They do not appear in `/message/sync`.
- They are not recoverable by committed replay.

## Durable Request-Scoped Boundary

P2c restored live request-scoped delivery by carrying `MessageScopedUIDs` in `MessageCommitted` and `MessageRealtime` envelopes.

P2d-c handles only the live durable committed path:

```text
durable request-scoped send
  -> temp____cmd channel log
  -> MessageCommitted{MessageScopedUIDs: exact UID snapshot}
  -> delivery.Submit exact live recipients
  -> cmdsync.SubmitCommitted exact CMD conversation recipients
```

P2d-c does not solve replay recovery:

- The channel log currently does not persist `MessageScopedUIDs`.
- A committed replay after process crash can see only the temp command channel message.
- It cannot reconstruct the exact request-scoped UID snapshot.

P2d-d should design either a persistent subscriber snapshot, a durable side-event, or a channel-log metadata extension before claiming full request-scoped CMD replay parity.

## Error Handling

- Invalid `/message/sync` UID should return a client error.
- Non-positive limits use defaults; limits above max are clamped.
- UID-owner routing failures return infrastructure errors.
- Command-channel log fetch `not ready` should be treated consistently with existing conversation facts: empty result if the channel is not ready, error for real storage/RPC failures.
- One bad command channel fetch should not corrupt sync records for other channels; implementation may choose fail-fast or partial success, but the choice must be explicit in the implementation plan.
- `/message/syncack` with no records returns success.

## Testing Strategy

P2d-c implementation should add focused unit/integration tests for:

- Durable group `SyncOnce` creates CMD conversation state for group subscribers.
- Durable already-addressed `source____cmd` send creates CMD conversation state.
- Ordinary `/conversation/sync` still does not return CMD conversations.
- `/message/sync` returns durable CMD messages and strips `____cmd` from client view.
- `/message/syncack` advances CMD read cursor from the latest sync record generation, and a second `/message/sync` does not return acked messages.
- `NoPersist + SyncOnce` realtime CMD does not create CMD conversation state and does not appear in `/message/sync`.
- Durable request-scoped CMD creates CMD conversation state only for `MessageScopedUIDs` during live committed dispatch.
- Multi-node sync reads CMD state from the UID owner and messages from the command channel owner.
- Process restart/lost sync-record cache makes a valid `/message/syncack` a no-op and allows safe duplicate sync.
- Stale sync records from an earlier generation cannot advance CMD read cursors after a later `/message/sync`.
- CMD state cannot leak into `/conversation/sync`, and chat clear/delete/read operations do not touch CMD state.
- Self-sent durable CMD messages create/update sender state with `ReadSeq=MessageSeq` when the sender is part of the resolved recipient set.

## Implementation Notes for the Future Plan

Likely implementation slices:

1. Define the CMD sync usecase contracts and sync record cache.
2. Add or extend CMD conversation state persistence with `ConversationType=CMD`.
3. Add CMD projector wiring after durable committed dispatch.
4. Add channel-log facts methods for command-channel message pulls.
5. Restore `/message/sync` and `/message/syncack` access API routes.
6. Add node RPCs only where UID-owner or command-channel-owner reads require them.
7. Update raw business diff docs and `internal/FLOW.md` once behavior changes land.

The implementation plan should decide whether CMD sync lives under `internal/usecase/conversation` as a clearly separated CMD submodule or under a new `internal/usecase/cmdsync` package. If it is added under conversation, the ordinary chat sync path must remain isolated and easy to reason about.
