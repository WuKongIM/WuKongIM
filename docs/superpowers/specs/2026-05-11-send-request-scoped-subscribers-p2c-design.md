# Send Request-Scoped Subscribers P2c Design

## Overview

P2c restores the legacy `/message/send` branch where callers pass `subscribers` instead of a channel. This branch is used for command-style directed delivery: the request supplies an exact recipient snapshot, `header.sync_once` is mandatory, and the server chooses an internal temporary command channel.

The design keeps the current architecture: access remains a thin adapter, `internal/usecase/message` owns send validation and channel selection, durable writes still go through channel-log append, and realtime fanout still goes through the delivery runtime. The request-scoped subscriber list is delivery metadata, not ordinary channel membership.

P2c intentionally does not restore full CMD conversation/offline sync, `/message/sendbatch`, plugins, webhooks, AI hooks, or special channel types. Those remain later phases.

## Goals

- Accept legacy-compatible `/message/send.subscribers`.
- Enforce the legacy request rules: subscribers require `sync_once=1`, and `channel_id` cannot be combined with `subscribers`.
- Generate an internal temporary channel identity for request-scoped subscriber sends.
- For durable requests, append to a derived temp cmd channel and deliver only to the request subscriber snapshot.
- For non-durable requests, route an online-only cmd message without durable append.
- Keep ordinary channel permission checks on ordinary channel sends; request-scoped subscriber sends have no source channel and therefore use only sender/request validation in this phase.
- Keep delivery routing, ack binding, and retry ownership in the delivery runtime instead of adding a legacy eventbus/tag branch.
- Prevent request-scoped subscriber snapshots from polluting reusable channel-level delivery tags.

## Non-Goals

- No CMD conversation type restoration.
- No offline cmd sync API restoration.
- No `/message/sendbatch`.
- No `PluginSend`, `PersistAfter`, webhook, or AI integration.
- No special channel permission restoration for agent/customer-service/visitor/info sends.
- No external configuration for the legacy `____cmd` suffix.
- No durable persistence of request subscriber snapshots beyond what is needed for the immediate send path.

## Legacy Behavior Reference

Relevant legacy behavior in `learn_project/WuKongIM`:

- `internal/api/message_model.go` includes `Subscribers []string` on `/message/send`.
- `internal/api/message.go` rejects `subscribers` unless `header.sync_once == 1`.
- `internal/api/message.go` rejects requests that include both `channel_id` and `subscribers`.
- `internal/api/message.go` chooses `OnlineCmdChannelId` for non-persistent directed sends.
- `internal/api/message.go` chooses `hash(strings.Join(subscribers, ","))` for persistent directed sends, then converts that temp channel to a cmd channel.
- `internal/api/message.go` creates a temporary tag for non-persistent directed sends.
- `internal/channel/handler/event_distribute.go` has a separate `distributeOnlineCmd` branch for online-cmd tag delivery.

P2c ports the business contract, but not the eventbus/tag implementation. The current delivery runtime replaces the legacy online-cmd tag dispatcher.

## Current Architecture Fit

Current P2b send flow is:

```text
access adapter
  -> message.App.Send
      -> validate sender/channel type
      -> strip optional ____cmd suffix
      -> normalize person channel
      -> check source-channel permissions
      -> NoPersist shortcut
      -> durable append
      -> committed dispatcher
          -> delivery runtime
          -> conversation projector
```

P2c adds a request-scoped branch before ordinary channel validation:

```text
/message/send with subscribers
  -> access maps subscribers into message.SendCommand.RequestSubscribers
  -> message.App.Send detects request-scoped mode
      -> validate sender, payload, subscribers, sync_once, no channel_id
      -> generate temp source channel from subscribers
      -> durable path: append temp____cmd, dispatch with subscriber snapshot
      -> non-durable path: generate transient message id, dispatch online-only with subscriber snapshot
  -> delivery resolver uses MessageScopedUIDs instead of store-backed channel subscribers
```

The ordinary channel branch remains the P2b flow. Request-scoped subscribers must not weaken ordinary channel permission checks because they are mutually exclusive with `channel_id`.

## Request Contract

HTTP request example:

```json
{
  "from_uid": "system",
  "subscribers": ["u1", "u2"],
  "payload": "eyJ0eXBlIjoiY21kIn0=",
  "header": {
    "sync_once": 1,
    "no_persist": 0
  }
}
```

Validation rules:

1. `from_uid` is required by the current project API. P2c does not reintroduce implicit system UID defaulting.
2. `payload` is required and must remain base64 for HTTP.
3. `subscribers` is optional for ordinary sends, but when non-empty it enables request-scoped mode.
4. Request-scoped mode requires `sync_once=1`.
5. Request-scoped mode rejects non-empty `channel_id`.
6. Empty subscriber strings are removed; duplicates are removed while preserving first-seen order.
7. If the normalized subscriber list is empty, the request is rejected.
8. `channel_type` is ignored in request-scoped mode and the usecase sets `frame.ChannelTypeTemp`.

The API adapter should return HTTP 400 for shape/validation failures before calling the usecase only when the failure is purely request syntax. Business-mode validation should live in `internal/usecase/message` so gateway or future node/RPC entry points share the same rules.

HTTP validation must split the two request modes explicitly:

- Ordinary channel send: keep the current requirement that `channel_id`, `channel_type`, `from_uid`, and `payload` are present.
- Request-scoped subscriber send: allow `channel_id` and `channel_type` to be absent, require `from_uid`, `payload`, and at least one subscriber value, and pass the request to the usecase with `ChannelID=""` and `ChannelType=0`. If `channel_id` is present with subscribers, reject the request before any durable work.

This split is required because the existing API handler currently validates `channel_id` and `channel_type` before it knows whether the request is in subscriber mode.

## Temporary Channel Identity

P2c introduces a helper in `internal/runtime/channelid` for request-scoped temporary channels.

Rules:

```text
source temp channel id = decimal FNV-1a 64-bit hash of strings.Join(normalizedSubscribers, ",")
append/delivery channel id = ToCommandChannel(source temp channel id)
channel type = frame.ChannelTypeTemp
```

This mirrors the legacy behavior of deriving a numeric temporary channel from the request subscriber list while keeping the exact hash implementation local to the current project.

The helper should return both values:

```go
type RequestSubscriberChannel struct {
    SourceChannelID  string
    CommandChannelID string
    ChannelType      uint8
    Subscribers      []string
}
```

The source channel is used for client-facing packet views and future temp-subscriber lookups. The command channel is used as the delivery actor / ack owner / durable append key.

## Durable Request-Scoped Send

For `subscribers + sync_once + !no_persist`:

```text
1. Validate request-scoped mode.
2. Normalize subscriber list and generate temp source + temp cmd channel.
3. Build the durable message with:
   - ChannelID = temp____cmd
   - ChannelType = temp
   - Framer.SyncOnce = true
   - Framer.NoPersist = false
4. Append through the normal channel-log path.
5. Dispatch the committed event with MessageScopedUIDs = normalized subscribers.
6. Delivery runtime resolves routes from MessageScopedUIDs.
7. Conversation projector still ignores the message because it is cmd / SyncOnce / temp.
```

`message.Send` returns the durable append `MessageID` and `MessageSeq`, as current durable sends do.

This phase does not rely on `/tmpchannel/subscriber_set` for the send path. That endpoint can remain for compatibility, but request-scoped send should carry the subscriber snapshot in the message event to avoid a second distributed mutation before append.

This is a deliberate deviation from legacy. P2c does not write request subscribers into temporary-channel subscriber state, so the snapshot is not visible to `/tmpchannel/subscriber_set`, ordinary channel tag repair, committed replay, or future offline cmd sync. Those consumers need a separate durable subscriber-snapshot design if they become required.

## Non-Durable Request-Scoped Send

For `subscribers + sync_once + no_persist`:

```text
1. Validate request-scoped mode.
2. Normalize subscriber list and generate temp source + temp cmd channel.
3. Allocate a transient message id from the same node snowflake generator used by channel append.
4. Build a non-durable message with:
   - ChannelID = systemcmdonline-equivalent temp cmd identity
   - ChannelType = temp
   - Framer.SyncOnce = true
   - Framer.NoPersist = true
   - MessageID = transient id
   - MessageSeq = 0
5. Submit directly to the realtime delivery runtime with MessageScopedUIDs.
6. Return `ReasonSuccess`; keep `MessageSeq=0`.
```

The current project should not add a global `systemcmdonline` config in P2c. Instead, it should model online-cmd as a non-durable delivery runtime submission keyed by an internal temp cmd channel. This preserves the legacy business outcome while avoiding a special global channel that would mix unrelated subscriber snapshots.

## Message-Scoped Delivery Metadata

P2c should extend the in-process delivery envelope with request-scoped subscriber metadata:

```go
type CommittedEnvelope struct {
    channel.Message
    SenderSessionID uint64
    MessageScopedUIDs []string
}
```

The name can be adjusted during implementation, but the semantics should be exact:

- Empty `MessageScopedUIDs` means normal subscriber resolution.
- Non-empty `MessageScopedUIDs` means this exact list is the subscriber source for this message only.
- Message-scoped snapshots are not reusable channel state.
- Message-scoped snapshots must survive local async dispatch and node-forwarded committed submission.

`messageevents.MessageCommitted` needs the same optional metadata so durable send can pass the subscriber snapshot from `message.App.Send` into the app-level committed dispatcher.

Node-to-node committed delivery submission must also carry this metadata. The binary codec in `internal/access/node` currently serializes `deliveryruntime.CommittedEnvelope`; P2c must extend that codec and its round-trip tests so a temp cmd channel whose owner is remote still resolves the request-scoped subscriber list on the owner node.
The codec should encode the UIDs with the shared node codec string-slice style and reject unreasonable counts with an explicit cap, e.g. `maxDeliverySubmitMessageScopedUIDs = 10000`, so malformed RPC payloads cannot allocate unbounded memory.

Crash/replay semantics are intentionally best-effort in P2c. Durable request-scoped sends persist the message but keep `MessageScopedUIDs` only in dispatch metadata. If the process crashes after append succeeds and before the async committed dispatcher submits delivery, committed replay cannot reconstruct the exact request subscriber list from the channel log. In that case P2c may not perform the directed realtime fanout; full recovery requires a later durable snapshot/metadata design.

For non-durable delivery, use a new message-usecase dependency such as:

```go
type RealtimeDispatcher interface {
    SubmitRealtime(ctx context.Context, event messageevents.MessageRealtime) error
}
```

The app composition root should adapt this to `delivery.App.SubmitCommitted` or a new `delivery.App.SubmitRealtime` method. Avoid making `internal/usecase/message` import `internal/app` or route presence itself.

## Subscriber Resolver Changes

`internal/usecase/delivery.SubscriberResolver.BeginSnapshotWithRequest` already supports `SubscriberSnapshotRequest.MessageScopedUIDs`. P2c wires this capability into app delivery routing:

```text
localDeliveryResolver.BeginResolve(key, env)
  -> if env.MessageScopedUIDs non-empty:
       subscribers.BeginSnapshotWithRequest(key, MessageScopedUIDs)
     else:
       subscribers.BeginSnapshot(key)
```

Expected resolver behavior:

- `ChannelTypeTemp` with message-scoped UIDs returns those UIDs exactly, after de-duplication.
- Request channel identity remains `temp____cmd` for ack owner binding.
- Source channel identity should be the stripped source temp channel for diagnostics/tag fencing.
- `ReusableTagState=false` prevents the snapshot from becoming reusable channel-level subscriber state.

## Delivery Tag Changes

The current tag resolver can use delivery tags for normal fanout. Message-scoped snapshots need an ephemeral tag path:

- Do not reuse `CurrentRef(channelKey)` when `SubscriberSource.ReusableTagState=false`.
- Do not update the channel-level current tag ref for message-scoped snapshots.
- Mint a fresh ephemeral tag key for the request snapshot.
- Store only the tag body/partition needed for this dispatch and let normal TTL cleanup remove it.

A small delivery-tag runtime addition is preferred, for example:

```go
func (m *Manager) BuildEphemeralTag(request BuildRequest) (DeliveryTag, bool)
```

or a `BuildRequest.Ephemeral` flag. The implementation must avoid changing the channel ref for ephemeral tags.

## Realtime Packet View

For request-scoped temp cmd messages, clients should not see the internal temp channel unless that is already the legacy client contract for temp channels. P2c should keep P2b's cmd-suffix stripping rule:

```text
stored/delivery channel: <temp>____cmd / temp
client packet channel:  <temp> / temp
```

`Header.SyncOnce` and `Header.NoPersist` must be preserved in the encoded `RecvPacket`.

For `NoPersist` messages, retry and ack binding behavior should remain bounded by the delivery runtime. If a route expires or the client never acks, there is no offline durable recovery in P2c.

## Authority And Node Routing

Durable temp cmd channel authority:

```text
temp____cmd / temp
  -> channel meta refresh/bootstrap
  -> slot owner for temp____cmd
  -> local append or remote append
  -> committed dispatcher
```

Subscriber source authority:

```text
MessageScopedUIDs are request-local metadata.
No store-backed subscriber authority is consulted for request-scoped mode.
Presence authority still resolves each UID to online routes.
```

Non-durable request-scoped authority:

```text
message.App.Send
  -> realtime dispatcher on the local access node
  -> local delivery runtime actor for temp____cmd
  -> presence authority resolves each UID
  -> local/remote push handles actual sessions
  -> ack/offline route ownership stays on the local access node
```

This owner choice is an online-only tradeoff. Because there is no durable append, there is no channel-log owner that can recover or replay the message. The access node that accepted the request owns retry and ack binding for that transient message; remote pushes must preserve that owner node ID so recvacks route back to the access node.

There is no bypass of cluster semantics for durable writes. A single-node deployment remains a single-node cluster.

## Error Handling

- Missing sender: existing `ErrUnauthenticatedSender` behavior.
- `subscribers` plus missing `sync_once`: new validation error mapped to HTTP 400 / gateway sendack failure as appropriate.
- `subscribers` plus non-empty `channel_id`: new validation error mapped to HTTP 400.
- Empty normalized subscribers: new validation error mapped to HTTP 400.
- Durable append errors: existing durable send error behavior.
- Realtime non-durable dispatch errors: return an infrastructure error. Do not claim success if no realtime dispatch was accepted.
- Permission denials do not apply to request-scoped mode because there is no source channel. If product rules later require sender authorization for directed command sends, add that as a separate phase.

## Testing Strategy

### `internal/runtime/channelid`

- Normalizes subscriber lists by trimming empties and removing duplicates.
- Generates stable temp source IDs for the same ordered subscriber list.
- Derives temp cmd channel IDs with `____cmd` applied once.

### `internal/usecase/message`

- Rejects request-scoped subscribers without `SyncOnce`.
- Rejects request-scoped subscribers with `ChannelID`.
- Rejects request-scoped subscribers when the normalized list is empty.
- Durable request-scoped send appends to temp cmd channel and dispatches `MessageScopedUIDs`.
- Non-durable request-scoped send skips append and submits realtime with `MessageScopedUIDs`.
- Ordinary channel sends keep the P2b behavior unchanged.

### `internal/access/api`

- `/message/send` accepts `subscribers` and maps them to `SendCommand`.
- `subscribers + sync_once + no channel_id + no channel_type` reaches the usecase successfully.
- Ordinary sends without `channel_id` or `channel_type` remain HTTP 400.
- `subscribers + channel_id` returns HTTP 400.
- `subscribers` without `header.sync_once` returns HTTP 400 or the mapped usecase validation error.
- Durable request-scoped send response contains the durable message ID/seq.
- Non-durable request-scoped send response has `message_seq=0`.

### `internal/usecase/delivery` and `internal/app`

- Delivery resolvers call `BeginSnapshotWithRequest` when an envelope has message-scoped UIDs.
- Local and tag-based delivery expand only the specified UIDs.
- Message-scoped tag dispatch does not reuse or overwrite reusable channel-level tags.
- `RecvPacket` preserves `SyncOnce` and `NoPersist` flags and strips the cmd suffix from temp cmd channels.
- Remote node delivery carries message-scoped UIDs when forwarding committed durable dispatch.
- Non-durable remote route push preserves the local access node as owner for ack/offline routing.

### Regression tests

- P2b durable `SyncOnce` ordinary group/person sends still resolve subscribers from the original channel.
- P2a ordinary `NoPersist` channel sends still return success without realtime delivery.
- Conversation projector still ignores cmd / `SyncOnce` messages.

## Rollout Notes

- No config changes are required.
- No slot metadata schema changes are required for P2c.
- No channel-log message format change is required because P2c accepts best-effort realtime fanout after durable append.
- If later CMD offline sync or crash-safe replay requires recovering request-scoped temp recipients after process restart, a durable subscriber snapshot model must be designed separately.
- Update `docs/raw/send-path-business-logic-diff.md` after implementation to mark request-scoped subscribers and online cmd delivery as partially restored, while leaving CMD conversation/offline sync and sendbatch as remaining gaps.
- Add a concise note to `docs/development/PROJECT_KNOWLEDGE.md` after implementation.
