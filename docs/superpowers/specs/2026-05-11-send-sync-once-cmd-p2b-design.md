# Send SyncOnce Cmd Channel P2b Design

## Overview

P2b restores the durable `SyncOnce` / cmd-channel boundary from legacy `learn_project/WuKongIM` while preserving the current architecture: thin access adapters, reusable message usecase, channel-log durability, committed-event routing, and delivery runtime fanout.

The key rule is: the original channel remains the business authority for validation, permissions, and subscriber source; the derived cmd channel is the durable append and delivery actor key; clients should still see the original channel view in `RecvPacket`.

This phase intentionally does not restore request-scoped `subscribers`, temp channels, online-only cmd delivery, CMD conversation/offline sync, sendbatch, plugins, webhooks, or AI hooks.

## Goals

- Accept legacy-compatible `header.sync_once` on HTTP `/message/send` and map it to `frame.Framer.SyncOnce`.
- Convert durable `SyncOnce` sends to a derived cmd channel after validation and P0/P1 permission checks pass.
- Keep permission checks on the original channel, not on the derived cmd channel.
- Resolve cmd-channel subscribers from the original channel.
- Route durable cmd messages through the current committed-event and delivery runtime path.
- Present original channel IDs to clients during realtime delivery, even when the durable storage channel is derived.
- Prevent cmd messages from polluting ordinary conversation projections in this phase.

## Non-Goals

- No request-scoped `subscribers` support.
- No temporary channel generation.
- No `systemcmdonline` online-only cmd channel support.
- No direct non-durable realtime delivery for `NoPersist + SyncOnce`.
- No CMD conversation type or offline cmd sync restoration.
- No `/message/sendbatch`.
- No plugin, `PersistAfter`, storage webhook, offline webhook, or AI integration.
- No new config for cmd suffix; use the legacy default suffix `____cmd` in this phase.

## Legacy Behavior Reference

Relevant legacy behavior:

- `learn_project/WuKongIM/internal/api/message_model.go` accepts `header.sync_once`.
- `learn_project/WuKongIM/internal/api/message.go` maps header flags into `wkproto.SendPacket`.
- `learn_project/WuKongIM/internal/api/message.go` converts `SyncOnce` messages to cmd fake channels before channel dispatch when the target is not online-cmd and not temp.
- `learn_project/WuKongIM/internal/options/options.go` uses `____cmd` as the default cmd suffix.
- `learn_project/WuKongIM/internal/options/options.go` provides `OrginalConvertCmdChannel`, `CmdChannelConvertOrginalChannel`, and `IsCmdChannel`.
- `learn_project/WuKongIM/internal/service/permission.go` strips cmd suffix before checking ordinary channel permissions.
- `learn_project/WuKongIM/internal/channel/handler/event_distribute.go` builds cmd-channel tags from original-channel subscribers.
- `learn_project/WuKongIM/internal/manager/manager_conversation.go` treats cmd conversation state separately from ordinary chat conversations.

P2b ports only the durable cmd-channel boundary and realtime delivery presentation into the current architecture.

## Current Architecture Fit

The current send path is:

```text
access adapter
  -> message.App.Send
      -> validate sender/channel type
      -> normalize person channel
      -> checkSendPermission
      -> NoPersist shortcut
      -> sendDurable
          -> RefreshChannelMeta
          -> local/remote channel append
          -> committed dispatcher
              -> delivery runtime
              -> conversation projector
```

P2b should keep this layering and add cmd semantics at explicit boundaries:

1. Access adapters map protocol/API flags to `SendCommand.Framer` and must not perform irreversible person-channel normalization before the usecase can strip a cmd suffix.
2. `message.App.Send` strips an incoming cmd suffix first, then normalizes and checks permissions against the original source channel.
3. `message.App.Send` derives the durable append channel only after permissions and after the `NoPersist` shortcut.
4. Delivery subscriber resolution strips the cmd suffix and reads subscribers from the original channel.
5. Realtime packet construction strips the cmd suffix before presenting channel IDs to clients.
6. Conversation projection rejects cmd/sync-once messages at the projector boundary so both live dispatch and committed replay inherit the same rule.

## Cmd Channel Identity

Use a single helper package for cmd-channel identity. Preferred location is `internal/runtime/channelid`, next to person-channel helpers.

Constants and functions:

```go
const CommandChannelSuffix = "____cmd"

func IsCommandChannel(channelID string) bool
func ToCommandChannel(channelID string) string
func FromCommandChannel(channelID string) (sourceID string, derived bool)
```

Rules:

- `ToCommandChannel("g1") == "g1____cmd"`.
- `ToCommandChannel("g1____cmd") == "g1____cmd"`.
- `FromCommandChannel("g1____cmd") == ("g1", true)`.
- `FromCommandChannel("g1") == ("g1", false)`.
- The suffix is part of `ChannelID`; `ChannelType` does not change.

Existing delivery resolver code has an internal `derivedCommandSuffix = "____cmd"`. P2b should converge that code on the shared helper to avoid duplicate definitions.

## Send Flow

### HTTP API

Extend `sendMessageHeaderRequest`:

```go
type sendMessageHeaderRequest struct {
    // NoPersist marks the send as non-durable when non-zero.
    NoPersist int `json:"no_persist"`
    // SyncOnce marks the message as command-style one-shot sync when non-zero.
    SyncOnce int `json:"sync_once"`
}
```

Extend `sendMessageRequest` with an optional top-level alias:

```go
SyncOnce int `json:"sync_once"`
```

Mapping:

```go
syncOnce := req.Header.SyncOnce != 0 || req.SyncOnce != 0
noPersist := req.Header.NoPersist != 0 || req.NoPersist != 0

Framer: frame.Framer{
    NoPersist: noPersist,
    SyncOnce:  syncOnce,
}
```

HTTP remains an adapter. It must not perform cmd-channel derivation itself.

### Gateway

Gateway send packets already carry `pkt.Framer`, including `SyncOnce`; no gateway business branch is needed. P2b should avoid rejecting an already-derived person cmd channel such as `u2@u1____cmd` before the usecase can strip `____cmd`.

The preferred P2b direction is to let `message.App.Send` be the canonical send-channel normalizer and have gateway/API adapters pass the request channel ID through. If an adapter keeps any local validation for compatibility, that validation must be command-aware and must not lose whether the input was already derived.

### Message Usecase

`message.App.Send` should keep three identities conceptually:

```text
input channel = cmd.ChannelID as received from access
source/original channel = input channel with optional cmd suffix stripped, then person-normalized
append channel = source channel unless SyncOnce or derived input requires a cmd channel
```

Proposed flow:

```text
1. Reject empty sender.
2. Reject unsupported channel type.
3. Strip an optional cmd suffix from `cmd.ChannelID`, recording `inputDerived`.
4. Normalize the stripped source channel for person channels.
5. Check send permissions on the normalized source channel.
6. If permission denied, return reason.
7. If NoPersist, return success with zero message ID/seq.
8. If `SyncOnce` or `inputDerived` requires a cmd channel, set durable append channel to `ToCommandChannel(source)`.
9. Require cluster and sendDurable using append channel.
```

Cmd append condition in P2b:

```text
(cmd.Framer.SyncOnce == true OR inputDerived == true)
AND current send usecase supports the channel type
```

The current `message.App.Send` supports only person and group channels. P2b must not expand the supported send channel types. Mentions of temp or other store-backed channel types in this design describe delivery resolver compatibility and future phases, not new send-type support in P2b.

For already-derived input, permissions still use the source channel:

- `g1____cmd` checks metadata/subscribers for `g1`.
- `u2@u1____cmd` strips to `u2@u1` before person normalization/permission checks, so sender `u1` is still valid.
- The durable append target remains `ToCommandChannel(source)`, preventing double suffixes.

The current project does not have a configured `systemcmdonline` channel. P2b therefore does not implement online-cmd special handling.

Important ordering:

- Permissions use the original channel.
- `NoPersist` bypasses durable append before cmd derivation has any effect.
- `sendDurable` receives the append channel, so `channel.Append` stores `Message.ChannelID` as the cmd channel.

## Cmd Channel Authoritative Node

A durable cmd channel is a normal channel-log target with a derived channel ID. Its authoritative write owner is resolved through current channel runtime metadata:

```text
append target: channel.ChannelID{ID: "g1____cmd", Type: group}
  -> MetaRefresher.RefreshChannelMeta("g1____cmd", group)
      -> channelmeta.Sync.ActivateByID
      -> Store.GetChannelRuntimeMeta("g1____cmd", group)
      -> if missing, RuntimeBootstrapper.EnsureChannelRuntimeMeta
          -> cluster.SlotForKey("g1____cmd")
          -> slot leader writes runtime meta
      -> returns cmd channel meta with Leader
  -> sendWithEnsuredMeta
      -> local leader: ChannelCluster.Append
      -> remote leader: RemoteAppender.AppendToLeader
```

This means the cmd channel can have a different owner than the original channel. That is acceptable in the current architecture because channel logs are independently keyed by `ChannelID + ChannelType`. There is no bypass-cluster branch; a single-node deployment remains a single-node cluster.

P2b does not force cmd channels to share original-channel runtime metadata. Sharing metadata would couple two independent channel logs and complicate repair, retention, and committed replay. Subscriber resolution is the part that points back to the original channel.

## Cmd Channel Subscriber Source

Cmd channel subscriber source is derived from the original channel ID:

```text
requested delivery channel: g1____cmd / group
source subscriber channel:  g1 / group
```

Current `internal/usecase/delivery.SubscriberResolver` already strips `____cmd` before source resolution. P2b should make this behavior explicit and test it.

Expected behavior:

- Person cmd channel:
  - requested: `u2@u1____cmd`, type `person`
  - source: `u2@u1`, type `person`
  - subscribers: derived from person fake channel (`u2`, `u1`)
- Group cmd channel:
  - requested: `g1____cmd`, type `group`
  - source: `g1`, type `group`
  - subscribers: `Store.ListChannelSubscribers(ctx, "g1", group, ...)`
- Other store-backed channel types:
  - requested: `<source>____cmd`
  - source: `<source>` with the same type
  - subscribers come from the normal resolver path for that type

The other store-backed channel behavior is delivery-resolver compatibility only. P2b does not make `message.App.Send` accept those channel types.

For group and store-backed channels, `pkg/slot/proxy.Store.ListChannelSubscribers` already finds the authoritative slot owner using `cluster.SlotForKey(sourceChannelID)`. Therefore P2b should not add a legacy-style `RequestSubscribers` RPC.

Delivery tag cache fences should be based on the source channel mutation version where possible. Existing `SubscriberSource.SourceChannelID`, `SourceChannelType`, and `SourceSubscriberMutationVersion` are the right model for cmd channels.

For cmd channels, the source mutation version used by delivery tags should come from the same authoritative metadata path used for send-time permission checks, so tag reuse is fenced by the source channel's durable state rather than by a stale view of the derived cmd channel.

## Cmd Channel Delivery

After durable append, cmd messages use the existing committed-event path:

```text
message.Send
  -> sendDurable(cmd channel)
  -> MessageCommitted{Message.ChannelID: "g1____cmd"}
  -> asyncCommittedDispatcher.routeCommitted
      -> ChannelLog.Status("g1____cmd")
      -> local or remote cmd channel owner
  -> deliveryruntime.Manager.Submit
      -> actor key: "g1____cmd"
      -> SubscriberResolver strips suffix for subscribers
      -> presence authority resolves endpoints
      -> distributedDeliveryPush / localDeliveryPush writes recv packets
```

The cmd channel actor owns retry and ack binding for this durable message, which matches its storage key and sequence.

## Client-Facing Channel View

Clients should not receive derived cmd channel IDs as ordinary channel IDs. A durable cmd message must be presented as the original channel view while preserving `Header.SyncOnce=1`.

Current risk:

- Group clients would receive `channel_id="g1____cmd"`.
- Person clients could get wrong views because `DecodePersonChannel("u2@u1____cmd")` treats `u1____cmd` as a UID.

P2b delivery presentation rule:

```text
packetChannelID = FromCommandChannel(msg.ChannelID) before person-channel view calculation
```

This rule must apply in every realtime push path:

- `buildRealtimeRecvPacket` must build packets from a client-facing view channel ID, not blindly from the durable `msg.ChannelID`.
- `recipientChannelView` or its caller must strip the cmd suffix before decoding person channels.
- `distributedDeliveryPush.deliveryPushItems` groups person routes by recipient view, so its grouping key must also use the stripped source channel.
- Remote `DeliveryPushItem.ChannelID` must remain the durable cmd channel for ack ownership binding; only the encoded `RecvPacket.ChannelID` is presented as the original channel.

Examples:

- Stored message:
  - `ChannelID="g1____cmd"`, `ChannelType=group`, `Framer.SyncOnce=true`
- Realtime packet:
  - `ChannelID="g1"`, `ChannelType=group`, `Header.SyncOnce=1`

Person example:

- Stored message:
  - `ChannelID="u2@u1____cmd"`, `ChannelType=person`, `FromUID="u1"`
- Recipient `u2` receives:
  - `ChannelID="u1"`, `ChannelType=person`, `Header.SyncOnce=1`
- Sender's other device `u1` receives:
  - `ChannelID="u2"`, `ChannelType=person`, `Header.SyncOnce=1`

Implementation should keep the storage/ack identity unchanged and only adjust the packet-building view:

- Durable message and delivery actor key: `g1____cmd`.
- Remote `DeliveryPushItem.ChannelID`: `g1____cmd`.
- Encoded `RecvPacket.ChannelID`: `g1`.

## Conversation Projection

Current committed dispatch submits every durable message to both delivery and conversation projection. If P2b does nothing, ordinary conversation state may be polluted with cmd channel IDs such as `g1____cmd`.

P2b should use a conservative rule:

- The ordinary conversation projector is the canonical gate for cmd / `SyncOnce` filtering.
- `Projector.SubmitCommitted` must reject messages whose durable channel ID is derived with `____cmd` or whose `Framer.SyncOnce` is true.
- `asyncCommittedDispatcher.submitConversation`, `submitConversationFallback`, and committed replay should continue calling the projector normally; they should not need their own duplicate filters if the projector gate is correct.
- Continue realtime delivery for those messages.
- Leave full CMD conversation/offline cmd sync semantics for a later phase.

The cleanest location is the conversation projector entrypoint, for example:

```go
if isCommandOrSyncOnce(msg) {
    return nil
}
```

This preserves the existing projector as an ordinary chat projection and avoids introducing a partial CMD conversation model.

## NoPersist + SyncOnce

P2a remains authoritative:

```text
NoPersist=true + SyncOnce=true
  -> validate and check permissions
  -> return ReasonSuccess with MessageID=0 and MessageSeq=0
  -> no durable cmd append
  -> no committed event
  -> no realtime delivery in P2b
```

This is intentionally incomplete versus legacy online-only cmd delivery. That missing behavior belongs to P2c, alongside request-scoped subscribers and message-scoped delivery.

## Error Handling

- Unsupported channel types keep returning `ReasonNotSupportChannelType` before any cmd derivation.
- Invalid person channels still fail during normalization before permissions.
- Permission infrastructure errors still return errors before cmd derivation.
- Cmd channel meta refresh, bootstrap, local append, and remote append use existing durable append error behavior.
- No new HTTP error shape is required for `sync_once`; it is a header flag.

## Testing Strategy

### Unit tests: `internal/runtime/channelid`

- `ToCommandChannel` appends `____cmd` once.
- `FromCommandChannel` strips only the suffix.
- `IsCommandChannel` detects derived IDs.

### Unit tests: `internal/usecase/message`

- `SyncOnce` durable send appends to `original____cmd` after permission checks.
- Group permissions read the original channel and subscribers, not `original____cmd`.
- Person channel is normalized before cmd derivation: `u2` from `u1` appends to `u2@u1____cmd`.
- Already-derived group input `g1____cmd` checks permissions on `g1` and appends to `g1____cmd`.
- Already-derived person input `u2@u1____cmd` strips before normalization/permission checks and appends to `u2@u1____cmd`.
- Already-derived cmd channel is not double-suffixed.
- `NoPersist + SyncOnce` still skips durable append and returns zero IDs/seq.
- Durable non-SyncOnce sends remain unchanged.

### Unit tests: `internal/access/api`

- `/message/send` maps `header.sync_once` to `SendCommand.Framer.SyncOnce`.
- Top-level `sync_once` alias maps the same flag if implemented.
- Existing `header.no_persist` behavior remains unchanged.

### Unit tests: `internal/usecase/delivery`

- Group cmd channel subscriber snapshot reads original group subscribers.
- Person cmd channel subscriber snapshot derives original person participants correctly.
- Source metadata reports original channel source fields for cmd channels.

### Unit tests: `internal/app`

- Realtime delivery strips cmd suffix from group recv packets.
- Realtime delivery strips cmd suffix before person recipient view calculation.
- Distributed person delivery groups remote routes by stripped recipient view, not by `u1____cmd`-style derived views.
- Remote push keeps `DeliveryPushItem.ChannelID` as the durable cmd channel for ack ownership while the encoded `RecvPacket.ChannelID` shows the original channel view.
- Committed replay and live committed dispatch both rely on the conversation projector to reject `SyncOnce` / cmd-channel messages.
- Delivery still receives cmd-channel committed envelopes.

### Unit tests: `internal/usecase/conversation`

- `SubmitCommitted` rejects messages whose durable channel ID ends with `____cmd`.
- `SubmitCommitted` rejects messages whose `Framer.SyncOnce` is true.
- Committed replay and live dispatch both depend on the same projector gate.

### Integration or focused app tests

If cheap with existing harnesses:

- Send HTTP `header.sync_once=1` to a group and verify append target is `g1____cmd` and committed delivery receives original subscribers.
- Avoid broad end-to-end tests in this phase unless existing helpers make them fast.

## Rollout Notes

- No config changes are required.
- No slot metadata schema changes are required.
- No channel-log format changes are required; `Framer.SyncOnce` is already persisted.
- No new RPC is required for subscriber reads; slot proxy authoritative subscriber reads already exist.
- Update `docs/raw/send-path-business-logic-diff.md` after implementation to mark P2b as restoring durable cmd-channel sends and realtime presentation, while keeping `subscribers`, temp delivery, and CMD conversation/offline sync as remaining gaps.
- Add a concise note to `docs/development/PROJECT_KNOWLEDGE.md` after implementation.
