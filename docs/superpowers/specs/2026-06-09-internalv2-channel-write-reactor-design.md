# internalv2 Channel Write Reactor Design

## Context

`internalv2` currently routes gateway and HTTP sends through a message usecase
that appends to the channel log, then emits committed-message events into a
separate recipient and delivery chain. That model makes `SENDACK` depend only on
durable append, but channel-level pressure is only visible after the append
request is already in flight.

This design replaces the append and post-commit path with one channel authority
write reactor. It intentionally does not preserve old internalv2 committed-sink,
recipient dispatcher, or delivery fanout manager layers when the reactor makes
them redundant.

Single-node deployment remains a single-node cluster. The design must not add a
business branch that bypasses cluster routing semantics.

## Decisions

- Only the channel authority node owns `channelState`.
- Non-authority nodes do not create proxy `channelState` and do not enter a
  local channel reactor for remote channels.
- Non-authority nodes only resolve the channel authority and forward send
  requests to that authority.
- Append runs inside the authority reactor state machine, with blocking append
  IO executed by a bounded worker pool.
- Post-commit recipient, conversation, and delivery scheduling continue from
  the same authority `channelState` after append succeeds.
- `SENDACK` success still means durable channel append success. It does not wait
  for conversation projection or online delivery.

## Goals

- Make channel authority the single owner of channel write state, append
  batching, flow control, committed backlog, and fanout progress.
- Apply channel-level backpressure before append, not only after committed
  events are produced.
- Keep non-authority forwarding simple: route cache, per-target outbound limits,
  RPC deadline handling, and stale-route retry only.
- Replace old append-after chains instead of wrapping them with compatibility
  adapters.
- Keep recipient authority and connection owner semantics separate: recipient
  UID authority owns conversation and recipient-scoped delivery decisions;
  connection owner nodes only write concrete gateway sessions.

## Non-Goals

- Do not change the legacy `internal` send path.
- Do not create one goroutine per channel or per message.
- Do not maintain proxy channel state on non-authority nodes.
- Do not make `SENDACK` wait for recipient fanout, delivery, or `RECVACK`.
- Do not introduce a generic service layer around the reactor.

## Architecture

The send path becomes channel-authority first:

```text
Gateway/API/Node ingress
  -> ChannelWriteRouter
       local channel authority  -> ChannelWriteReactorGroup.Submit
       remote channel authority -> ForwardSendBatch RPC
  -> remote ChannelWriteReactorGroup.Submit
  -> append future
  -> SENDACK result
```

The authority reactor continues the committed path asynchronously:

```text
append success
  -> authority channelState committed queue
  -> subscriber/member selection
  -> recipient authority batches
  -> conversation update
  -> online endpoint resolution
  -> local or remote connection-owner push
```

`ChannelWriteRouter` is not a state owner. It may cache routes and limit
outbound requests per remote node, but it must not keep per-channel queues,
subscriber caches, append windows, or fanout cursors.

## Components

### Channel Write Router

The router is used by gateway, HTTP API, and node RPC ingress.

Responsibilities:

- Resolve `channelID/channelType` to the current channel authority target.
- If the target is local, submit the batch to the local reactor group.
- If the target is remote, call `ForwardSendBatch` on the target node.
- Retry stale route, not-leader, and route-not-ready errors within the caller
  deadline.
- Apply lightweight per-target outbound limits to avoid overloading a remote
  node.

It does not create `channelState`.

### Channel Write Reactor Group

Each node runs a multi-reactor group. A channel key maps to one reactor by a
stable hash, but a reactor only creates state for channels that are authoritative
on the current node.

Responsibilities:

- Admit send batches for local-authority channels.
- Lazily create and evict authority `channelState`.
- Preserve order within a channel.
- Build append batches.
- Track append futures and complete item-aligned send results.
- Continue committed events into recipient and delivery effects.
- Apply channel-level flow control based on pending sends, append inflight work,
  committed backlog, fanout backlog, and retry pressure.

If a request reaches a reactor for a channel that is no longer locally
authoritative, the reactor returns a retryable authority error. The router then
resolves a fresh target.

### Authority Channel State

`channelState` exists only on the authority node.

State owned by the reactor:

```text
channel key
authority target and route revision
pending send queue
append batch builder
append inflight window
send result waiters
committed queue
subscriber/member cache
subscriber page cursor
recipient authority dispatch progress
post-commit retry state
flow-control counters
last committed sequence observed
last active time
```

The reactor goroutine mutates this state. Worker goroutines execute blocking
effects and return completion events back to the same reactor.

### Effect Workers

Blocking or slow work is represented as reactor effects and executed by bounded
worker pools:

```text
sender/session validation
message authorization
idempotency lookup
message ID allocation
channel log append
subscriber page load
recipient authority RPC
conversation update
presence endpoint lookup
owner-node delivery push
post-commit cursor checkpoint
```

The reactor owns state transitions. Effects own IO.

## Sender Validation

The previous sender-authority-first pipeline is replaced.

Channel authority is the write owner. Sender and permission checks become
pre-append effects scheduled by the channel authority reactor:

```text
authority channelState
  -> validate sender/session or API identity
  -> authorize channel send
  -> check idempotency
  -> allocate message IDs
  -> append
```

Gateway-origin sends carry sender node, session ID, owner boot ID, and owner
sequence data. The validator must verify that the sender is still valid through
the appropriate presence or online authority. HTTP-origin sends use the existing
token and permission path. UID authority may still be queried for fencing, but
it is no longer the first write-path owner.

## Flow Control

Channel flow control is enforced at authority admission time.

Inputs:

- pending send items and bytes
- append inflight items and bytes
- committed queue depth
- subscriber paging backlog
- recipient authority RPC backlog
- delivery retry backlog

Initial behavior should be simple:

- Below the low watermark: admit immediately.
- Between low and high watermark: admit while batching remains efficient.
- Above the high watermark: wait for capacity until the caller deadline.
- If the caller deadline expires: return a channel-busy send result.

This gives each hot channel a bounded write surface without penalizing unrelated
channels on the same node.

## Post-Commit Reliability

Append success must not depend on in-memory committed events being delivered
immediately. The authority reactor should maintain a per-channel post-commit
cursor:

```text
last post-commit completed sequence
```

On authority activation or reactor recovery, the state reads the cursor and
replays committed messages from the durable channel log starting at the next
sequence. Recipient conversation updates and online delivery must be idempotent
or tolerate at-least-once retries.

The initial implementation may checkpoint after recipient authority dispatch is
accepted, not after every concrete session write. Online push remains
best-effort and retry-bounded, while conversation projection should be
recoverable from the channel log.

## Recipient And Delivery Effects

The old recipient committed worker and recipient usecase dispatcher are removed.
The channel authority reactor owns recipient selection and dispatch.

Recipient selection rules:

- If a committed event has scoped UIDs, use those UIDs directly.
- For person channels, derive the two canonical participants.
- For other channels, page durable channel subscribers with bounded page size.

Recipient groups are routed by recipient UID authority:

```text
channel authority reactor
  -> recipient authority batch
  -> update recipient-scoped conversation projection
  -> resolve online endpoints
  -> local or remote owner-node push
```

The recipient authority effect must update the conversation projection before
submitting delivery for that UID group.

Connection owner delivery remains a lower-level primitive. It validates the
exact online session fence before writing a `RECV` packet and keeps pending ack
tracking separate from channel write state.

## Error Handling

Pre-append validation or authorization failures complete the corresponding
send result as failed.

Append failures complete `SENDACK` as failed or retryable according to the
typed append error. Route stale, not leader, and route not ready are retried by
the router while the caller deadline allows it.

If channel authority changes while requests are queued, the old authority
reactor stops admitting new work for that channel, fails or drains pending
pre-append work with a retryable authority error, and lets callers resolve the
new authority.

If append succeeds but post-commit work fails, `SENDACK` is not changed. The
reactor retries post-commit effects with bounded backoff and relies on the
post-commit cursor to recover after restart or authority transfer.

## Deletions And Replacements

Replace these internalv2 layers:

- sender-authority-first send pipeline
- `recipientCommittedWorker`
- `internalv2/usecase/recipient` dispatcher and processor
- delivery fanout planner that rescans channel subscribers after commit
- committed sink fanout plumbing that exists only to bridge old layers

Keep or move these lower-level capabilities:

- online session registry
- presence authority routing and fencing
- concrete local session writer
- pending ack tracker
- node RPC transport codecs
- durable channel append adapter
- durable subscriber/member source
- conversation projection storage port

## Testing

Focused unit tests:

- channel key maps to one reactor and preserves per-channel order
- non-authority router forwards without creating channel state
- local authority submits create channel state lazily
- channel high watermark blocks or rejects before append
- append completion returns item-aligned send results
- append success continues post-commit without delaying `SENDACK`
- stale authority errors trigger route refresh and retry
- authority loss stops local admission
- post-commit cursor replay resumes from durable channel log
- recipient effect updates conversation before delivery

Targeted integration tests:

- single-node cluster gateway `SEND -> SENDACK -> RECV`
- two-node remote channel authority forward
- channel authority transfer during queued send
- hot channel flow control does not block an unrelated channel
- restart after append success before post-commit completion replays committed
  messages

## Implementation Boundary

This spec is intentionally a replacement design. The implementation should
remove obsolete internalv2 send-after-append layers as the new reactor path
lands, instead of keeping both paths live behind compatibility switches.

