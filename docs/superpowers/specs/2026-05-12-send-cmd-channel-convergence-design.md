# Send CMD Channel Convergence P2d Design

## Overview

P2d defines the target CMD channel model for the current WuKongIM send path. It builds on the already merged P2a/P2b/P2c/P2 permission work and keeps the current architecture rule: access adapters stay thin, `internal/usecase/message` owns send orchestration, `internal/usecase/delivery` owns subscriber resolution, and channel-log durability still goes through the cluster runtime.

A CMD channel is not an independent business channel. It is a command-style delivery/storage view derived from a source channel by appending `____cmd`. The source channel remains the authority for send permissions and subscriber membership. The derived CMD channel is the channel-log key and delivery actor key for durable command messages.

This document is intentionally staged. P2d-a/P2d-b are the next implementation target. P2d-c/P2d-d describe the required future model for full legacy parity, but they should become separate implementation plans because they touch conversation schema/offline sync and replay metadata persistence.

## Goals

- Make the CMD identity model explicit and shared by send, delivery, conversation, replay, and diagnostics.
- Define how a CMD channel's write owner is resolved versus how its source-channel business authority is resolved.
- Define how subscribers are resolved for CMD channels, including person, agent, group, customer-service, visitors, info, and request-scoped temp channels.
- Restore online-only command delivery for `NoPersist + SyncOnce` without writing a channel log.
- Keep request-scoped `subscribers` delivery exact during live dispatch and avoid polluting reusable channel-level delivery tags.
- Keep ordinary conversation projection protected from CMD messages until a dedicated CMD conversation model is designed.
- Identify the replay/offline gaps that must be solved before claiming full CMD offline-sync parity with legacy WuKongIM.

## Non-Goals

- No `/message/sendbatch` restoration.
- No `expire` behavior change.
- No `AllowStranger` restoration.
- No plugin, webhook, AI, or `PersistAfter` hooks.
- No new bypass path around cluster semantics; a single-node deployment remains a single-node cluster.
- No ordinary `NoPersist` realtime restoration unless the message is command-style (`SyncOnce`, already-addressed CMD channel, or request-scoped subscribers).
- No CMD conversation/offline-sync implementation in P2d-a/P2d-b.
- No durable persistence of request-scoped subscriber snapshots in P2d-a/P2d-b.

## Legacy Behavior Reference

Relevant legacy points in `learn_project/WuKongIM`:

- `internal/options/options.go` defines `CmdSuffix = "____cmd"` and `OnlineCmdChannelId = "systemcmdonline"`.
- `internal/options/options.go` provides `IsCmdChannel`, `OrginalConvertCmdChannel`, `CmdChannelConvertOrginalChannel`, and `IsOnlineCmdChannel`.
- `internal/api/message.go` converts `SyncOnce` sends to CMD channels when the target is not the online CMD channel and not a temp channel.
- `internal/api/message.go` sends request-scoped non-durable commands through `systemcmdonline` with a generated tag.
- `internal/service/permission.go` strips the CMD suffix before checking channel permissions.
- `internal/channel/handler/event_distribute.go` builds CMD tags from source-channel subscribers through `getCmdSubscribers`.
- `internal/channel/handler/event_distribute.go` has `distributeOnlineCmd` for `systemcmdonline` tag delivery.
- `internal/manager/manager_conversation.go` treats CMD conversations separately from ordinary chat conversations through `ConversationTypeCMD`.

Current WuKongIM should port the business semantics, not the old eventbus/tag implementation.

## Current State

Already restored in the current project:

- `internal/runtime/channelid/command.go` defines `CommandChannelSuffix`, `IsCommandChannel`, `ToCommandChannel`, and `FromCommandChannel`.
- `internal/usecase/message/send.go` strips an incoming CMD suffix before permission checks and appends durable `SyncOnce` messages to `source____cmd`.
- `internal/usecase/message/send.go` supports request-scoped subscribers and carries `MessageScopedUIDs` during live committed dispatch.
- `internal/usecase/delivery/subscriber.go` resolves CMD subscribers from the source channel.
- `internal/app/deliveryrouting.go` passes `MessageScopedUIDs` to the delivery resolver and strips `____cmd` when building realtime packets.
- `internal/usecase/conversation/projector.go` skips CMD channels and `SyncOnce` messages, so ordinary conversation projection is not polluted.

Remaining gaps:

- Ordinary `NoPersist + SyncOnce` currently returns success without realtime delivery.
- There is no `systemcmdonline` compatibility model for online-only CMD delivery.
- CMD conversation/offline sync is not restored.
- Durable request-scoped subscriber snapshots cannot be reconstructed by committed replay after a crash.

## CMD Identity Model

A CMD channel has three related identities:

```text
input channel      = channel ID received from gateway/API/node adapter
source channel     = input channel with one optional ____cmd suffix removed
command channel    = source channel with one ____cmd suffix appended
```

Rules:

- `ToCommandChannel("g1") == "g1____cmd"`.
- `ToCommandChannel("g1____cmd") == "g1____cmd"`.
- `FromCommandChannel("g1____cmd") == ("g1", true)`.
- `FromCommandChannel("g1") == ("g1", false)`.
- `ChannelType` does not change when deriving a CMD channel.
- The CMD suffix must be applied at most once.
- The source channel is the business authority for permission and subscriber reads.
- The command channel is the durable append key and delivery actor key for durable CMD messages.
- Realtime packet views shown to clients use the source channel, not the command channel.

Person and agent channels still normalize after stripping the CMD suffix:

```text
input:   u2@u1____cmd / person
source:  u2@u1 / person
view:    recipient-specific peer UID
append:  u1@u2____cmd or canonical-person-source____cmd after normalization
```

Agent bare-channel normalization also happens on the source identity. A bare agent UID from sender `u1` becomes `u1@agentUID`, then durable `SyncOnce` appends to `u1@agentUID____cmd`.

## Authoritative Node Rules

CMD semantics use two authorities. They must not be collapsed into one authority.

### Durable Append Authority

Durable CMD messages are written to the command channel log:

```text
append target = channel.ChannelID{ID: source + "____cmd", Type: originalType}
```

The write owner is resolved exactly like any other channel log:

```text
message.App.Send
  -> sendDurable(command channel)
    -> sendWithEnsuredMeta
      -> MetaRefresher.RefreshChannelMeta(command channel)
        -> channelmeta.Sync.ActivateByID(command channel)
        -> slot/controller metadata for command channel runtime meta
      -> local leader: ChannelCluster.Append
      -> remote leader: RemoteAppender.AppendToLeader
```

Consequences:

- `g1` and `g1____cmd` can have different channel-log leaders.
- This is acceptable because they are different channel logs.
- Retention, replay cursors, and message sequences are scoped to the command channel log.
- No direct local shortcut is allowed; even a single-node deployment follows single-node cluster semantics.

### Business Authority

Permissions and subscribers use the source channel:

```text
permission target  = channel.ChannelID{ID: source, Type: originalType}
subscriber target  = channel.ChannelID{ID: source, Type: originalType}
```

Examples:

- Sending to `g1____cmd` checks denylist/allowlist/subscriber state for `g1`.
- Sending to `u2@u1____cmd` checks person-channel rules for the normalized person source.
- Sending to `visitorA____cmd` with visitors type resolves visitor/customer-service overlays from the source visitors/customer-service model.

### Delivery Routing Authority

The delivery runtime owns ack/retry by the delivery channel key, which remains the command channel for durable CMD messages:

```text
ack/retry key = command channel + channel type + message id
subscriber source = source channel + channel type
```

This split keeps retries tied to the message actually delivered while still resolving recipients from the correct business source.

## Subscriber Resolution Rules

`internal/usecase/delivery.SubscriberResolver` is the canonical place to resolve CMD subscribers. The resolver must first strip `____cmd` and then choose a source strategy by `ChannelType`.

### Person

```text
requested: u1@u2____cmd / person
source:    u1@u2 / person
uids:      [u1, u2]
```

The resolver derives both UIDs from the normalized person channel. It does not read subscriber storage.

### Agent

```text
requested: userA@agentB____cmd / agent
source:    userA@agentB / agent
uids:      [userA, agentB]
```

The resolver derives both UIDs from the normalized agent channel. Invalid agent IDs are rejected before delivery routing.

### Group and Ordinary Store-Backed Channels

```text
requested: g1____cmd / group
source:    g1 / group
uids:      ListChannelSubscribers(ctx, "g1", group, cursor, limit)
```

The authoritative storage call targets the source channel. The source channel's `SubscriberMutationVersion` fences reusable delivery-tag state.

### Customer Service

```text
requested: visitorA|csA____cmd / customer_service
source:    visitorA|csA / customer_service
uids:      source subscribers plus visitor overlay when the visitor UID is encoded in the channel ID
```

The visitor overlay follows the current resolver behavior. The command suffix must not hide the visitor UID extraction.

### Visitors

```text
requested: visitorA____cmd / visitors
source:    visitorA / visitors
store:     visitorA / customer_service
uids:      customer-service subscribers plus visitorA overlay
```

This matches the restored visitors permission model, where non-self sends check the customer-service dimension for the visitor source.

### Info

```text
requested: infoA____cmd / info
source:    infoA / info
uids:      info subscribers plus temporary overlay when available
```

The resolver keeps the existing info-channel temporary overlay behavior.

### Temp / Request-Scoped

```text
requested: hash____cmd / temp
source:    hash / temp
uids:      MessageScopedUIDs for this message only
```

Rules:

- Non-empty `MessageScopedUIDs` are the exact subscriber source.
- The resolver de-duplicates empty/repeated UIDs while preserving first-seen order.
- `ReusableTagState=false` prevents this snapshot from becoming a reusable channel-level tag.
- If `MessageScopedUIDs` are absent during replay, exact request-scoped delivery cannot be reconstructed in P2d-a/P2d-b.

## Send Flow

### Ordinary Durable SyncOnce

```text
1. Access adapter maps protocol flags into message.SendCommand.
2. message.App.Send rejects unauthenticated senders.
3. The usecase strips an optional ____cmd suffix from cmd.ChannelID.
4. The usecase normalizes person/agent source channel IDs.
5. The usecase checks send permissions on the source channel.
6. If Framer.SyncOnce or the input was already a CMD channel, append to ToCommandChannel(source).
7. sendDurable appends through the command channel log.
8. The committed dispatcher submits delivery and best-effort conversation side effects.
9. The conversation projector drops the message because it is CMD or SyncOnce.
10. Realtime packets sent to clients show the source channel view.
```

### Already-Addressed CMD Input

A caller can send directly to `source____cmd`. This is treated as command-style even when `SyncOnce=false`:

```text
inputDerived = true
appendChannel = ToCommandChannel(source)
permissionChannel = source
packetViewChannel = source
```

This preserves compatibility with callers that already know the CMD address while avoiding double suffixes.

### Ordinary Non-Durable Online CMD

P2d-b restores realtime delivery for command-style non-durable sends:

```text
condition = Framer.NoPersist && (Framer.SyncOnce || inputDerived)
```

Flow:

```text
1. Validate and check permissions on the source channel.
2. Do not append to channel log.
3. Allocate a transient message ID from the message ID generator.
4. Build a transient channel.Message with:
   - ChannelID = ToCommandChannel(source)
   - ChannelType = original type
   - Framer.NoPersist = true
   - Framer.SyncOnce preserved
   - MessageSeq = 0
5. Submit MessageRealtime to app delivery routing.
6. Resolve subscribers from the source channel.
7. Deliver online packets with source channel view and MessageSeq=0.
8. Return ReasonSuccess with the transient MessageID and MessageSeq=0.
```

Ordinary `NoPersist` messages that are not command-style keep the existing P2a behavior in this phase: permission checks run, then the usecase returns success without durable append or realtime delivery.

### Request-Scoped Durable CMD

This path already exists from P2c and remains the model:

```text
1. HTTP /message/send supplies subscribers and sync_once=1.
2. message.App.Send rejects non-empty channel_id in request-scoped mode.
3. The usecase normalizes subscribers and derives a temp source channel.
4. Durable messages append to tempSource____cmd / temp.
5. The committed event carries MessageScopedUIDs.
6. Delivery resolves only MessageScopedUIDs.
7. Conversation projection drops the temp/CMD/SyncOnce message.
```

### Request-Scoped Non-Durable CMD

This path also exists from P2c and remains the preferred replacement for legacy `systemcmdonline` tag dispatch:

```text
1. Derive tempSource____cmd / temp.
2. Allocate transient MessageID.
3. Set MessageSeq=0.
4. Submit MessageRealtime with MessageScopedUIDs.
5. Deliver online only.
```

## `systemcmdonline` Compatibility Model

Legacy used a global online command channel named `systemcmdonline` for non-durable request-scoped sends. The current project should not reintroduce it as a durable or globally shared channel log in P2d-a/P2d-b.

Compatibility rule:

- Business outcome: online-only command delivery to an explicit subscriber snapshot or source-channel subscriber snapshot.
- Internal implementation: `MessageRealtime` with a transient message ID, `MessageSeq=0`, and either source-channel subscriber resolution or `MessageScopedUIDs`.
- Client-facing packet: source channel view, not `systemcmdonline`.
- No channel-log append to `systemcmdonline`.
- No reusable subscriber tag keyed by `systemcmdonline`.

If a future API must expose `systemcmdonline` as an input channel for legacy clients, the adapter should translate it into the non-durable realtime CMD mode and keep that translation in access/usecase boundaries. It must not create a global shared durable channel.

## Delivery Flow

### Durable CMD Delivery

```text
message.App.Send
  -> sendDurable(command channel)
    -> channel log append succeeds
    -> messageevents.MessageCommitted{Message: command-channel message}
      -> asyncCommittedDispatcher
        -> owner node for command channel side effects
          -> delivery.App.SubmitCommitted
            -> delivery runtime actor keyed by command channel
              -> localDeliveryResolver.BeginResolve
                -> SubscriberResolver strips ____cmd and reads source subscribers
              -> route online sessions
              -> buildRealtimeRecvPacket strips ____cmd for clients
```

Important properties:

- ACK/retry bindings use the command-channel delivery key.
- Clients receive a packet whose `ChannelID` is the source view.
- Sender session skip still uses `SenderSessionID`.
- Conversation side effects are submitted but projector drops CMD/SyncOnce messages.

### Non-Durable CMD Delivery

```text
message.App.Send
  -> build transient message with command channel identity
  -> messageevents.MessageRealtime
    -> app realtime dispatcher
      -> delivery.App.SubmitCommitted or equivalent realtime adapter
        -> delivery runtime actor keyed by command channel
          -> resolve source subscribers or MessageScopedUIDs
          -> push online packets
```

Important properties:

- No committed replay can recover the message because it is intentionally non-durable.
- ACK/retry owner is the node that accepted the realtime submission.
- Retry is bounded by the delivery runtime. The send response does not wait for client ACK.
- Offline push and conversation updates are not generated by this phase.

### Delivery Tags

Normal CMD channel delivery may reuse channel-level tag state only when the subscriber source is reusable:

```text
reusable source = person / agent derived set, group store, customer-service store, visitors overlay, info store
non-reusable source = request-scoped MessageScopedUIDs
```

For `MessageScopedUIDs`:

- Build an ephemeral tag body for this dispatch.
- Do not write a current tag ref for the channel key.
- Do not overwrite reusable tag state for the temp command channel.
- Let normal delivery-tag cleanup remove the ephemeral body.

## Realtime Packet View

Realtime packets must hide internal CMD channel IDs.

Rules:

- For non-person channels, `RecvPacket.ChannelID = FromCommandChannel(msg.ChannelID)`.
- For person channels, strip `____cmd` first, then show the peer UID for the recipient.
- `RecvPacket.ChannelType` remains the original type.
- `Framer.SyncOnce` and `Framer.NoPersist` are preserved.
- `MessageSeq=0` is allowed for non-durable online CMD packets.

Examples:

```text
stored/delivery: g1____cmd / group
recipient sees:  g1 / group

stored/delivery: u1@u2____cmd / person, recipient u1
recipient sees:  u2 / person

stored/delivery: tempHash____cmd / temp, request-scoped subscribers
recipient sees:  tempHash / temp
```

## Conversation and Offline Sync Position

Current conversation projection intentionally skips CMD channels and `SyncOnce` messages. P2d-a/P2d-b keep that behavior.

Full legacy parity needs a separate CMD conversation/offline model because the current `metadb.UserConversationState` key only has `UID + ChannelID + ChannelType`. Legacy had a separate `ConversationTypeCMD`; mixing CMD and chat into the same key would corrupt ordinary unread/sort behavior.

Future P2d-c requirements:

- Add a distinct conversation kind or equivalent dimension for CMD state.
- Keep ordinary chat conversation and CMD conversation independently deletable/readable.
- Make `/conversation/sync` either expose CMD conversations through a compatibility option or keep them behind a dedicated endpoint.
- Load recent CMD messages from the command channel log while presenting source channel IDs to clients.
- Define unread semantics for `SyncOnce` before writing code; command messages may be online-only, durable-but-one-shot, or offline-syncable.

Offline CMD sync is also future work. Durable command messages exist in command channel logs, but current sync APIs do not discover or expose CMD conversation state. Non-durable online CMD messages are intentionally not offline-syncable.

## Committed Replay Semantics

### Ordinary Durable CMD

Committed replay can replay ordinary durable CMD messages from the command channel log. On replay:

```text
message = command-channel log row
subscriber resolution = strip ____cmd and read current source subscribers
packet view = strip ____cmd for clients
conversation = dropped by projector
```

This provides best-effort repair of missed realtime fanout. It does not guarantee that the subscriber set is identical to the set at original send time if source membership changed after append.

### Durable Request-Scoped CMD

P2c live dispatch carries `MessageScopedUIDs` in memory and node RPC. The channel log row does not contain those UIDs. Therefore committed replay cannot reconstruct exact directed delivery after a crash.

Future P2d-d requirements:

- Persist a per-message subscriber snapshot before or atomically with the durable append.
- Make committed replay load that snapshot by `channel key + message seq` or `message id`.
- Keep snapshot retention aligned with command channel message retention.
- Cap snapshot size and reject malformed oversized metadata.
- Keep the snapshot out of ordinary channel subscriber state.

Until P2d-d exists, durable request-scoped sends are durable as messages but live-directed delivery is exact only while the committed event metadata survives.

## Error Handling

- Invalid source channel IDs fail before append.
- Invalid agent CMD IDs map to the same reason/error behavior as invalid non-CMD agent IDs.
- Permission denials return `SendResult.Reason` and do not append or realtime-dispatch.
- Missing cluster dependencies still fail durable CMD sends.
- Missing realtime dispatcher or message ID generator fails non-durable online CMD sends.
- Realtime dispatch failure for non-durable CMD returns an error because there is no durable replay fallback.
- Durable committed-dispatch failure keeps existing behavior: ordinary durable sends return success after append, while request-scoped durable sends may surface dispatch failure when exact live delivery is required.

## Implementation Phases

### P2d-a: Identity and Subscriber Authority Hardening

Acceptance criteria:

- All CMD suffix logic uses `internal/runtime/channelid` helpers.
- Tests prove send permissions run on source channels for already-addressed CMD input.
- Tests prove subscriber resolver reads source-channel subscribers for group CMD channels.
- Tests prove person/agent CMD subscribers are derived from the source identity.
- Tests prove visitors/customer-service/info CMD overlays use source identities.
- Tests prove reusable tag fences use source subscriber mutation version where applicable.

### P2d-b: Online Non-Durable CMD Delivery

Acceptance criteria:

- `NoPersist + SyncOnce` ordinary channel sends dispatch realtime after permission checks.
- Already-addressed CMD input with `NoPersist=true` dispatches realtime even when `SyncOnce=false`.
- Transient CMD messages return non-zero `MessageID`, `MessageSeq=0`, and `ReasonSuccess`.
- The command channel is used internally for delivery routing and ACK/retry ownership.
- The source channel view is sent to clients.
- Ordinary `NoPersist` without command semantics keeps P2a behavior.

### P2d-c: CMD Conversation and Offline Sync

Acceptance criteria for the future spec:

- CMD conversation state is separate from ordinary chat conversation state.
- Sync responses can include durable CMD messages without corrupting chat unread counters.
- Deleting or clearing chat conversations does not accidentally delete/clear CMD conversations.
- Non-durable online CMD messages remain excluded from offline sync.

### P2d-d: Durable Request-Scoped Replay Snapshots

Acceptance criteria for the future spec:

- Request-scoped subscriber snapshots survive process restart.
- Committed replay can reconstruct exact `MessageScopedUIDs` for durable temp CMD messages.
- Snapshot retention and channel message retention cannot diverge into unbounded orphan data.
- Oversized snapshots are rejected before append.

## Testing Strategy

Focused tests for P2d-a/P2d-b:

- `internal/runtime/channelid`: command suffix idempotency and source stripping.
- `internal/usecase/message`: source-channel permission checks for CMD input, durable append channel derivation, non-durable online CMD dispatch.
- `internal/usecase/delivery`: subscriber source resolution for CMD group/person/agent/customer-service/visitors/info/temp channels.
- `internal/app`: realtime packet view strips CMD suffix and preserves `NoPersist`/`SyncOnce` flags.
- `internal/app`: delivery routing uses `MessageScopedUIDs` as non-reusable subscriber source.
- `internal/access/api`: HTTP `sync_once`/`no_persist` flags map into usecase command without adapter-side CMD business logic.
- `internal/access/gateway`: gateway passes trusted session/device fields and framer flags through without doing CMD derivation.

Suggested commands during implementation:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/usecase/delivery ./internal/access/api ./internal/access/gateway ./internal/app -count=1
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

## Architectural Guardrails

- Keep CMD business rules out of `pkg/channel`.
- Keep access adapters as DTO/protocol mappers only.
- Keep reusable send orchestration in `internal/usecase/message`.
- Keep subscriber-source decisions in `internal/usecase/delivery`.
- Keep runtime online routing and ACK/retry mechanics in `internal/runtime/delivery` and `internal/app` wiring.
- Do not add global service objects or a new all-purpose service layer.
- Do not describe single-node deployments as bypassing cluster; use single-node cluster semantics.
