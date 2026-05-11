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

1. Access adapters map protocol/API flags to `SendCommand.Framer` only.
2. `message.App.Send` normalizes and checks permissions against the original channel.
3. `message.App.Send` derives the durable append channel only after permissions and after the `NoPersist` shortcut.
4. Delivery subscriber resolution strips the cmd suffix and reads subscribers from the original channel.
5. Realtime packet construction strips the cmd suffix before presenting channel IDs to clients.
6. Conversation projection skips cmd/sync-once messages in P2b.

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

Gateway send packets already carry `pkt.Framer`, including `SyncOnce`; no gateway business branch is needed. The gateway should continue to normalize person channel IDs before calling the usecase.

### Message Usecase

`message.App.Send` should keep two identities conceptually:

```text
permission/original channel = cmd.ChannelID after existing normalization
append channel = original channel unless SyncOnce requires a cmd channel
```

Proposed flow:

```text
1. Reject empty sender.
2. Reject unsupported channel type.
3. Normalize person channel.
4. Check send permissions on normalized original channel.
5. If permission denied, return reason.
6. If NoPersist, return success with zero message ID/seq.
7. If SyncOnce and channel should derive, set durable append channel to ToCommandChannel(original).
8. Require cluster and sendDurable using append channel.
```

Cmd derivation condition in P2b:

```text
cmd.Framer.SyncOnce == true
AND channel type != ChannelTypeTemp
AND channelID is not already a command channel
```

The current project does not have a configured `systemcmdonline` channel. P2b therefore only avoids double-deriving already-derived command channels and temp channels.

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

For group and store-backed channels, `pkg/slot/proxy.Store.ListChannelSubscribers` already finds the authoritative slot owner using `cluster.SlotForKey(sourceChannelID)`. Therefore P2b should not add a legacy-style `RequestSubscribers` RPC.

Delivery tag cache fences should be based on the source channel mutation version where possible. Existing `SubscriberSource.SourceChannelID`, `SourceChannelType`, and `SourceSubscriberMutationVersion` are the right model for cmd channels.

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

Implementation should keep the storage message unchanged and only adjust the packet-building view.

## Conversation Projection

Current committed dispatch submits every durable message to both delivery and conversation projection. If P2b does nothing, ordinary conversation state may be polluted with cmd channel IDs such as `g1____cmd`.

P2b should use a conservative rule:

- Do not submit cmd-channel / `SyncOnce` messages to the ordinary conversation projector.
- Continue realtime delivery for those messages.
- Leave full CMD conversation/offline cmd sync semantics for a later phase.

The cleanest location is the committed dispatcher boundary before `submitConversation`, for example:

```go
if isCommandOrSyncOnce(msg) {
    return
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
- Committed dispatcher does not submit `SyncOnce` / cmd-channel messages to ordinary conversation projection.
- Delivery still receives cmd-channel committed envelopes.

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
