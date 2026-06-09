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

- Make the internalv2 send path easier to read by exposing the send pipeline as
  named batch stages instead of a chain of implicit helper calls.
- Improve multi-channel throughput by allowing bounded parallel append across
  independent channel groups while preserving order within each channel.
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

Every local or remote authority call carries the exact UID authority target that
the router resolved: hash slot, physical Slot ID, leader node ID, route
revision, and authority epoch. The receiving node must verify that the target is
still locally authoritative before committing. Stale targets return
`not_leader`, `stale_route`, or `route_not_ready`; the router resolves a fresh
target and retries within the caller's deadline.

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

Refactor `message.App.SendBatch` into an explicit batch-oriented pipeline
without adding a generic middleware framework:

```text
SendBatchPipeline.Execute(items)
  -> normalize contexts and deadlines
  -> precheck authenticated sender, channel shape, payload, and supported flags
  -> canonicalize request-scoped and person-channel commands
  -> authorize and recover idempotent sends
  -> allocate durable message IDs
  -> group active sends by canonical channel
  -> append channel groups
  -> publish committed events for successful append results
```

The pipeline should use concrete batch structs and named stage methods instead
of per-item handler objects. This keeps the code readable in Go, avoids
interface churn in the hot path, and makes each stage independently testable.

Channel grouping remains stable and first-seen ordered. Within one channel
group, append order is preserved exactly. Across different channel groups, the
append stage may use a bounded executor:

```text
ChannelAppendExecutor
  -> concurrency = 1 by default during the behavior-preserving refactor
  -> later configurable bounded concurrency for independent channels
  -> per-channel items stay sequential inside one append request
  -> item-aligned results are merged back into the original batch indexes
```

This is the primary throughput lever in this refactor. It should not create one
goroutine per message. It should schedule channel groups, not individual sends,
so large batches with many channels can use available CPU and IO while preserving
single-channel ordering.

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
executes a fixed sequence for the exact recipient UID group carried by the
request:

```text
project recipient-scoped conversation active patches
  -> admit/update UID-owned conversation authority
  -> submit delivery envelope scoped to this recipient UID group
```

Recipient-scoped means every UID in the group gets its own active conversation
patch before that UID is eligible for delivery. The old large-group
conversation behavior that only writes a sparse sender row is not used for
recipient-authority delivery groups. Sender-only sparse projection may remain
for sender display policy if needed, but it cannot satisfy the
conversation-before-delivery requirement for recipients.

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

### Subscriber Paging

The recipient pipeline owns subscriber selection for post-commit delivery. It
must scan subscribers page by page with bounded limits, group each page by UID
authority target, and dispatch each target group independently. It must not load
a full large channel into memory before grouping.

The existing delivery runtime may keep connection-owner push, pending ack,
presence lookup, and retry/drop behavior. It must not rescan channel subscribers
for the new post-commit path; it receives scoped UID groups through
`MessageScopedUIDs` and delivers only those UIDs.

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

The package import-boundary test must continue to reject concrete routing and
entry packages such as `pkg/clusterv2`, `internalv2/access`, and
`internalv2/app`. Concrete authority resolution and RPC live behind ports.

### `internalv2/usecase/recipient`

Add a narrow usecase package for recipient-authority post-commit orchestration.
This package owns business ordering and recipient grouping policy:

- `Dispatcher.SubmitCommitted` admits committed events into a bounded async
  worker.
- `Dispatcher` resolves recipient UIDs by scoped list, person participants, or
  paged channel subscribers.
- `Dispatcher` groups recipient UIDs by fenced UID authority target.
- `Processor.Process` validates the request target, projects recipient-scoped
  conversation patches, admits those patches, then submits scoped delivery.
- Ports provide subscriber paging, authority target resolution, remote
  recipient forwarding, conversation patch admission, and delivery submission.

The package must not import gateway frames, clusterv2, channelv2, access
adapters, or the app composition root.

The async worker contract:

- bounded event queue, with admission failure recorded but not reflected in
  `SENDACK`;
- bounded subscriber page size and per-target UID batch size;
- retry by `(event key, target, recipient UID group, cursor)` with max attempts
  and max age;
- route movement retries after resolving a fresh target;
- graceful stop drains accepted work until the caller context expires;
- low-cardinality observer events for admission, paging, target dispatch,
  conversation update, delivery submit, retry, drop, and terminal result.

### `internalv2/access/node`

Add sender authority RPC:

- sender authority service ID in clusterv2 RPC service constants.
- deterministic binary codec for send batch request and response.
- request includes the fenced sender UID authority target.
- `Client.SendToSenderAuthority(ctx, nodeID, batch)`.
- `Adapter.HandleSenderAuthorityRPC`.

The adapter decodes, calls the injected local submitter, and encodes results. It
does not resolve routes or make business decisions.

Add recipient authority RPC:

- recipient authority service ID in clusterv2 RPC service constants.
- deterministic binary codec for a committed event plus recipient UID list.
- request includes the fenced recipient UID authority target for the UID group.
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

- converts each `clusterv2.RouteKey(uid)` result into a fenced authority target
  containing hash slot, Slot ID, leader node ID, route revision, and authority
  epoch.
- groups recipient UIDs by exact authority target.
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
AuthorityTarget
  HashSlot uint16
  SlotID uint32
  LeaderNodeID uint64
  RouteRevision uint64
  AuthorityEpoch uint64

SenderAuthoritySendBatchRequest
  Target AuthorityTarget
  Items []SenderAuthoritySendItem

SenderAuthoritySendItem
  Command message.SendCommand
  TimeoutMS int64

SenderAuthoritySendBatchResponse
  Status SenderAuthorityStatus
  Results []message.SendBatchItemResult

RecipientAuthorityProcessRequest
  Target AuthorityTarget
  Event messageevents.MessageCommitted
  RecipientUIDs []string

RecipientAuthorityProcessResponse
  Status RecipientAuthorityStatus
```

Use relative timeout metadata for sender authority RPC instead of serializing
process-local contexts. The receiving node creates fresh bounded contexts.

`RecipientAuthorityProcessor` sets `Event.MessageScopedUIDs` to the received
recipient UID group before submitting delivery so the delivery runtime only
delivers that group. It also uses that same recipient UID group to build
conversation active patches; delivery must not receive a UID before that UID's
conversation patch has been admitted.

Sender authority RPC response loss after a remote durable commit is an
at-least-once edge unless sender-side idempotency is implemented. The first
implementation should add sender-authority idempotency keyed by
`FromUID + ClientMsgNo` for gateway-origin sends with a non-empty
`ClientMsgNo`, returning the existing committed message ID and sequence on
retry. If `ClientMsgNo` is empty, the send remains at-least-once and the
gateway/system should surface the system-error `SENDACK` on transport failure.

### RPC Codec Contract

Use the existing `internalv2/access/node` deterministic codec style:

- sender authority request magic: `W K V S 1`
- sender authority response magic: `W K V s 1`
- recipient authority request magic: `W K V A 1`
- recipient authority response magic: `W K V a 1`
- length-delimited strings and byte slices;
- varint/uvarint numeric fields matching the existing node codecs;
- bounded collection sizes for batch items and recipient UIDs;
- bounded payload size matching the surrounding gateway/message limits;
- malformed payloads return decode errors and do not call usecases.

Sender authority statuses:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `rejected`

Recipient authority statuses:

- `ok`
- `not_leader`
- `stale_route`
- `route_not_ready`
- `retryable`
- `rejected`

Status-to-error mapping must use the same sentinel style as the existing
presence and conversation authority RPCs so route retry logic stays outside the
RPC adapter.

## Error Handling

Synchronous errors affect `SENDACK`:

- missing authentication or malformed send command returns the existing auth or
  invalid request reason.
- sender UID authority route-not-ready, stale-route, or not-leader maps to
  `ReasonNodeNotMatch` through existing message error mapping.
- sender authority RPC transport failure maps to a system-error `SENDACK` when
  no idempotent result can be recovered.
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
- Sender authority retry uses `FromUID + ClientMsgNo` when `ClientMsgNo` is
  present to recover durable results after response loss.
- Conversation active writes are idempotent UID/channel upserts or active
  patches.
- Recipient pipeline retry is keyed by committed event, authority target,
  recipient UID group, and subscriber cursor so partial target failures do not
  replay unrelated groups.
- Delivery retry is allowed; pending ack and sender echo suppression continue
  to rely on `MessageID`, `SessionID`, `SenderNodeID`, and `SenderSessionID`.
- Preserve append-result order within one channel batch.
- Do not invent a global order across unrelated channels.

## Refactoring Policy

The implementation should be bold but bounded:

- First make the `message.App.SendBatch` path explicit without changing
  behavior; the first executor implementation may keep concurrency at one.
- Introduce bounded cross-channel append concurrency only after the
  behavior-preserving pipeline tests pass.
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

- `SendBatchPipeline` runs validation, normalization, idempotency, allocation,
  grouping, append, and committed publication in the expected order.
- `SendBatchPipeline` keeps item-aligned results for mixed immediate rejects,
  context cancellations, append failures, and successes.
- channel grouping preserves first-seen channel order and per-channel item
  order.
- `ChannelAppendExecutor` with concurrency one matches the existing sequential
  append behavior.
- `ChannelAppendExecutor` with bounded concurrency runs independent channels in
  parallel and merges results back to original batch indexes.
- `ChannelAppendExecutor` does not reorder messages inside the same channel.
- `SenderAuthorityRouter` routes local UID leaders to local submitter.
- `SenderAuthorityRouter` routes remote UID leaders through RPC.
- sender router preserves item-aligned batch results.
- sender router maps route-not-ready, stale-route, and not-leader correctly.
- sender router recovers an idempotent result for duplicate
  `FromUID + ClientMsgNo` after response loss.
- sender authority RPC codec round-trips commands and results.
- sender authority RPC rejects malformed payloads.
- sender authority RPC rejects stale fenced targets without committing.
- `RecipientAuthorityDispatcher` handles scoped UIDs, person participants, and
  group subscriber pages.
- recipient dispatcher scans group subscribers page by page with bounded limits.
- recipient dispatcher groups by exact recipient UID authority target.
- recipient dispatcher isolates remote target failures to the affected group.
- `RecipientAuthorityProcessor` calls conversation before delivery.
- processor creates conversation patches for every UID in the recipient group,
  including large groups.
- processor skips delivery when conversation update fails.
- processor returns retryable errors for delivery submit failures.
- recipient authority RPC codec round-trips fenced target, event, and UIDs.
- recipient authority RPC rejects stale fenced targets without updating
  conversation or delivery.
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
- `internalv2/usecase/recipient/FLOW.md` when the package is added
- `internalv2/usecase/conversation/FLOW.md`
- `internalv2/usecase/delivery/FLOW.md` if delivery entry semantics change
- `internalv2/infra/cluster/FLOW.md`
- `internalv2/app` flow documentation if added

No config change is planned. If implementation introduces tunables for retry,
queue, or timeout behavior, update `wukongim.conf.example` with detailed English
comments.
