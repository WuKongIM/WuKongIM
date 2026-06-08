# internalv2 Send Authority Pipeline Design

## Context

`internalv2` currently accepts gateway `SEND` frames through
`internalv2/access/gateway`, maps them to `message.SendCommand`, and calls
`internalv2/usecase/message.App.SendBatch`. The message usecase validates the
command, groups sends by channel, appends through the clusterv2/channelv2
channel path, then emits committed-message events to configured sinks.

The target architecture makes the send path authority boundaries explicit:

- Sender authority is the sender UID hash-slot leader.
- Channel authority is the channel append/replication leader.
- Recipient authority is each recipient UID hash-slot leader.
- Connection owner is the node that owns the concrete gateway session.

Single-node deployment remains a single-node cluster. The design must not add a
business branch that bypasses cluster semantics.

## Goals

- Route gateway-origin sends to the sender UID hash-slot leader before channel
  commit.
- Return `SENDACK` after the channel authority durably commits the message.
- Continue recipient-side work asynchronously after `SENDACK`.
- Process recipient-side work on recipient UID hash-slot leaders.
- Guarantee recipient authority order: update recent conversation first, then
  submit delivery.
- Keep the code simple by replacing obsolete internalv2 send/delivery plumbing
  instead of layering new behavior around old behavior.

## Non-Goals

- Do not change the legacy `internal` send path.
- Do not make `SENDACK` wait for conversation projection or online delivery.
- Do not merge UID authority and connection owner concepts.
- Do not introduce a new generic service layer.
- Do not preserve old internalv2 committed sink code when the new pipeline makes
  it redundant.

## Architecture

Split the path into a synchronous send pipeline and an asynchronous recipient
pipeline:

```text
Gateway access node
  -> Sender Authority Pipeline        synchronous, decides SENDACK
  -> Channel Commit                   synchronous, durable commit boundary
  -> Recipient Authority Pipeline     asynchronous, conversation then delivery
  -> Connection Owner Delivery        asynchronous, writes concrete sessions
```

### Sender Authority Pipeline

The gateway-facing send usecase is a sender authority router:

```text
access/gateway
  -> SenderAuthorityRouter
       local sender UID leader  -> LocalSubmitter
       remote sender UID leader -> node RPC -> remote LocalSubmitter
```

`SenderAuthorityRouter` only routes by `FromUID`. It does not own send
validation, message ID allocation, channel grouping, or durable append. Remote
RPC handlers call the local submitter directly so remote sends do not recurse
through the router.

### Channel Commit

The local submitter is the existing channel commit usecase:

```text
LocalSubmitter
  -> message.App.SendBatch
  -> ChannelAppender
  -> clusterv2/channelv2
  -> SendResult
```

Channel commit success is the `SENDACK` success boundary:

```text
SENDACK = channel authority durable commit success
```

`message.App` remains entry-agnostic and cluster-agnostic. It owns validation,
authorization, person-channel normalization, message ID allocation, per-channel
batching, append retries, and committed event submission.

### Recipient Authority Pipeline

After channel commit, committed events enter one post-commit recipient pipeline
instead of independent conversation and delivery sinks:

```text
MessageCommitted
  -> RecipientAuthorityDispatcher
       resolve recipient UIDs
       group by recipient UID hash-slot leader
       local group  -> RecipientAuthorityProcessor
       remote group -> node RPC -> remote RecipientAuthorityProcessor
```

Recipient UID source rules:

- `MessageScopedUIDs` present: use the scoped UIDs directly.
- person channel: decode the two canonical participants.
- other channels: page durable channel subscribers.

`RecipientAuthorityProcessor` runs on the recipient UID authority node and
executes a fixed sequence:

```text
project conversation active patches
  -> admit/update UID-owned conversation authority
  -> submit delivery envelope scoped to this recipient UID group
```

The delivery runtime then resolves online routes and pushes to connection owner
nodes:

```text
delivery submit
  -> presence authority lookup
  -> group by connection owner node
  -> local write or delivery push RPC
```

This keeps UID authority responsible for business ownership and connection
owner responsible only for concrete session writes.

## Components

### `internalv2/usecase/message`

Keep `App.SendBatch` as the local channel commit usecase.

Add a sender authority router:

- `SenderAuthorityRouter.Send` and `SendBatch`.
- `UIDAuthorityResolver` port for resolving `FromUID` to a route target.
- `LocalSubmitter` port for local channel commit.
- `RemoteSender` port for node RPC forwarding.

The router returns item-aligned `SendBatchItemResult` values so gateway
micro-batching semantics stay unchanged.

### `internalv2/access/node`

Add sender authority RPC:

- sender authority service ID in clusterv2 RPC service constants.
- deterministic binary codec for send batch request and response.
- `Client.SendToSenderAuthority(ctx, nodeID, batch)`.
- `Adapter.HandleSenderAuthorityRPC`.

The adapter decodes, calls the injected local submitter, and encodes results. It
does not resolve routes or make business decisions.

Add recipient authority RPC:

- deterministic binary codec for a committed event plus recipient UID list.
- `Client.ProcessRecipientAuthority(ctx, nodeID, request)`.
- `Adapter.HandleRecipientAuthorityRPC`.

The adapter decodes, calls the injected recipient processor, and encodes a
bounded status.

### `internalv2/infra/cluster`

Add sender authority routing adapter:

- uses `clusterv2.RouteKey(fromUID)`.
- maps local leader to local submitter.
- maps remote leader to sender authority RPC.
- maps clusterv2 route errors into message sentinel errors.

Add recipient authority routing adapter:

- groups recipient UIDs by `clusterv2.RouteKey(uid)`.
- dispatches local groups to the local processor.
- dispatches remote groups to recipient authority RPC.
- retries route-not-ready, stale-route, and not-leader with bounded backoff.

### `internalv2/app`

Wire the new graph explicitly:

- `message.App` is the local channel commit submitter.
- `SenderAuthorityRouter` is the gateway-facing message usecase.
- gateway receives the router, not the raw `message.App`.
- sender authority RPC receives the raw local submitter, not the router.
- committed sink becomes the recipient authority pipeline.
- remove the old `combineCommittedSinks(conversationProjector, deliverySink)`
  path once the recipient pipeline owns conversation-before-delivery ordering.

The composition root should prefer deleting replaced plumbing over preserving
parallel paths. Tests should lock the new graph so old and new flows do not run
side by side.

## DTOs

Existing `messageevents.MessageCommitted` already carries the required data:

- `MessageID`
- `MessageSeq`
- `ChannelID`
- `ChannelType`
- `FromUID`
- `SenderNodeID`
- `SenderSessionID`
- `ClientMsgNo`
- `ServerTimestampMS`
- `Payload`
- `RedDot`
- `MessageScopedUIDs`

Add narrow RPC envelopes:

```text
SenderAuthoritySendBatchRequest
  Items []SenderAuthoritySendItem

SenderAuthoritySendItem
  Command message.SendCommand
  TimeoutMS int64

SenderAuthoritySendBatchResponse
  Results []message.SendBatchItemResult

RecipientAuthorityProcessRequest
  Event messageevents.MessageCommitted
  RecipientUIDs []string

RecipientAuthorityProcessResponse
  Status
```

Use relative timeout metadata for sender authority RPC instead of serializing
process-local contexts. The receiving node creates fresh bounded contexts.

`RecipientAuthorityProcessor` sets `Event.MessageScopedUIDs` to the received
recipient UID group before submitting delivery so the delivery runtime only
delivers that group.

## Error Handling

Synchronous errors affect `SENDACK`:

- missing authentication or malformed send command returns the existing auth or
  invalid request reason.
- sender UID authority route-not-ready, stale-route, or not-leader maps to
  `ReasonNodeNotMatch` through existing message error mapping.
- sender authority RPC transport failure maps to a system-error `SENDACK`.
- channel append route-not-ready, stale-route, or not-leader preserves the
  existing `ReasonNodeNotMatch` behavior.
- channel append success followed by recipient pipeline admission failure does
  not change `SENDACK`.

Asynchronous errors do not affect `SENDACK`:

- subscriber scan failure retries the post-commit work.
- remote recipient authority failure retries only the failed target group.
- conversation update failure prevents delivery for that group and retries the
  group.
- delivery submit failure retries delivery work; conversation updates remain
  idempotent.
- connection owner push failures keep using delivery retry/drop semantics.
- route movement retries after resolving a fresh UID authority target.

## Idempotency and Ordering

- Use `(MessageID, MessageSeq, ChannelID, ChannelType)` as the committed-event
  idempotency basis.
- Conversation active writes are idempotent UID/channel upserts or active
  patches.
- Delivery retry is allowed; pending ack and sender echo suppression continue
  to rely on `MessageID`, `SessionID`, `SenderNodeID`, and `SenderSessionID`.
- Preserve append-result order within one channel batch.
- Do not invent a global order across unrelated channels.

## Refactoring Policy

The implementation should be bold but bounded:

- Prefer one clear pipeline over compatibility layers.
- Delete internalv2 code paths made redundant by the new sender or recipient
  authority pipeline.
- Do not keep old conversation and delivery committed sinks running in parallel
  with the recipient pipeline.
- Do not hide old behavior behind configuration switches unless a test or
  deployment requirement proves it is needed.
- Keep `access`, `usecase`, `infra`, and `app` responsibilities separate.
- Update `FLOW.md` files whenever package behavior changes.

## Testing Strategy

Unit tests first:

- `SenderAuthorityRouter` routes local UID leaders to local submitter.
- `SenderAuthorityRouter` routes remote UID leaders through RPC.
- sender router preserves item-aligned batch results.
- sender router maps route-not-ready, stale-route, and not-leader correctly.
- sender authority RPC codec round-trips commands and results.
- sender authority RPC rejects malformed payloads.
- `RecipientAuthorityDispatcher` handles scoped UIDs, person participants, and
  group subscriber pages.
- recipient dispatcher groups by recipient UID hash-slot leader.
- recipient dispatcher isolates remote target failures to the affected group.
- `RecipientAuthorityProcessor` calls conversation before delivery.
- processor skips delivery when conversation update fails.
- processor returns retryable errors for delivery submit failures.
- `internalv2/app` injects gateway with sender router.
- `internalv2/app` injects sender RPC with local submitter to avoid recursive
  forwarding.
- `internalv2/app` uses the recipient pipeline as the committed sink and does
  not wire old parallel conversation/delivery sinks.

Targeted e2e tests:

- single-node cluster `SEND -> SENDACK -> conversation visible -> recv`.
- three-node cluster where gateway access node differs from sender UID leader.
- three-node cluster where recipient UID leader differs from connection owner;
  conversation is updated before delivery push.

## Documentation Updates

Implementation must update relevant `FLOW.md` files:

- `internalv2/FLOW.md`
- `internalv2/access/gateway/FLOW.md`
- `internalv2/access/node/FLOW.md`
- `internalv2/usecase/message/FLOW.md`
- `internalv2/usecase/delivery/FLOW.md` if delivery entry semantics change
- `internalv2/infra/cluster/FLOW.md`
- `internalv2/app` flow documentation if added

No config change is planned. If implementation introduces tunables for retry,
queue, or timeout behavior, update `wukongim.conf.example` with detailed English
comments.
