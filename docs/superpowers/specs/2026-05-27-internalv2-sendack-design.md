# internalv2 Send-to-Sendack Architecture Design

Date: 2026-05-27
Status: Approved design
Owner: Codex

## 1. Purpose

`internalv2` is a new parallel business kernel for WuKongIM. It will not replace
`internal` in the first phase. The first phase builds a testable skeleton that
runs the client `SEND -> SENDACK` path through `pkg/clusterv2` and
`pkg/channelv2`, while leaving the current production `internal` stack intact.

The goal is to create a long-term architecture that is easier to test, extend,
and optimize than copying the current `internal/app` shape. Phase 1 proves the
core write path and dependency direction before migrating delivery,
conversation, CMD, plugins, management APIs, or other side effects.

Single-node deployment remains a single-node cluster. `internalv2` must not add
business paths that bypass cluster semantics.

## 2. Selected Approach

The approved approach is a parallel dual-stack skeleton:

- Keep the existing `internal` production path unchanged.
- Add a new `internalv2` tree with its own app composition root and test
  harness.
- Wire phase-1 durable sends to `pkg/clusterv2.Node.AppendChannelBatch`.
- Verify `SendPacket -> SendackPacket` through focused tests and a
  single-node-cluster smoke path.

Two alternatives were considered and rejected for phase 1:

- Copying most of `internal` into `internalv2` would migrate quickly, but it
  would also copy the broad app wiring and old channelplane assumptions.
- Building a full send reactor immediately could offer a higher performance
  ceiling, but it would make the first milestone too wide. The phase-1 ports
  are batch-oriented so a reactor can be added later without changing usecase
  contracts.

## 3. Package Layout

```text
internalv2/
  app/                 Single composition root for config, dependency wiring, and lifecycle.
  access/
    gateway/           WKProto gateway adapter: frame mapping, Sendack writing, error mapping.
  usecase/
    message/           Entry-agnostic SEND usecase and ports.
  runtime/
    session/           Minimal node-local session registry for gateway/session context.
  infra/
    cluster/           clusterv2/channelv2 adapter for message append ports.
  contracts/
    messageevents/     Committed-message event contract; phase 1 defaults to no-op.
```

Dependency direction is fixed:

```text
access -> usecase
usecase -> contracts and usecase-defined ports
infra -> pkg/clusterv2 and pkg/channelv2, implementing usecase ports
app -> access, usecase, runtime, infra, and pkg composition dependencies
```

`internalv2/usecase/message` must not import `pkg/gateway`,
`pkg/protocol/frame`, `pkg/clusterv2`, `pkg/channelv2`, `internalv2/access`, or
`internalv2/app`.

## 4. Phase-1 Flow

```text
client SendPacket
  -> pkg/gateway
  -> internalv2/access/gateway.Handler
  -> internalv2/usecase/message.App.SendBatch
  -> internalv2/infra/cluster.ChannelAppender
  -> pkg/clusterv2.Node.AppendChannelBatch
  -> pkg/channelv2 append
  -> message.SendResult
  -> gateway writes SendackPacket
```

`Send` is a batch-of-one wrapper. `SendBatch` is the canonical usecase entry so
future gateway micro-batching or a dedicated send reactor does not create a
second correctness path.

## 5. Message Usecase Model

`internalv2/usecase/message` owns entry-agnostic command, result, append, and
event contracts.

Core command fields for phase 1:

```go
type SendCommand struct {
    FromUID         string
    SenderSessionID uint64
    ClientSeq       uint64
    ClientMsgNo     string
    ChannelID       string
    ChannelType     uint8
    Payload         []byte
    NoPersist       bool
    SyncOnce        bool
    RedDot          bool
    MessageID       uint64
    ProtocolVersion uint8
}
```

The usecase result uses an `internalv2` reason enum, not
`frame.ReasonCode`. Gateway adapters map usecase reasons to WKProto reason
codes.

```go
type SendResult struct {
    MessageID  uint64
    MessageSeq uint64
    Reason     Reason
}
```

Phase 1 behavior:

- Validate sender, channel, and payload basics.
- Allocate message IDs only for valid durable sends that do not already carry a
  trusted ID.
- Group adjacent sends by canonical channel and append each segment with
  `Appender.AppendBatch`.
- Return item-aligned `SendResult` values.
- Submit committed-message events after successful append through a
  `CommittedSink`; the default sink is no-op in phase 1, and sink failures are
  logged or observed without changing the successful Sendack result.
- Return explicit unsupported results for `NoPersist` until realtime delivery is
  implemented.

Gateway-origin sends must pass `MessageID=0`; only trusted internal callers may
provide a preallocated message ID.

Reserved ports:

```go
type Authorizer interface {
    AuthorizeSend(context.Context, SendCommand) (Decision, error)
}

type MessageIDAllocator interface {
    Next() uint64
}

type Appender interface {
    AppendBatch(context.Context, AppendBatchRequest) (AppendBatchResult, error)
}

type CommittedSink interface {
    Submit(context.Context, messageevents.MessageCommitted) error
}
```

The phase-1 authorizer may be an allow-all implementation after basic
validation. Full permissions, allowlist, denylist, user rate limits, plugin
hooks, request-scoped subscribers, CMD channel derivation, and realtime
`NoPersist` delivery are outside phase 1.

## 6. clusterv2 Adapter

`internalv2/infra/cluster` is the only phase-1 business-side adapter that knows
about `pkg/clusterv2` and `pkg/channelv2`.

```go
type ChannelAppender struct {
    node *clusterv2.Node
}

func (a *ChannelAppender) AppendBatch(ctx context.Context, req message.AppendBatchRequest) (message.AppendBatchResult, error)
```

Mapping rules:

- `message.ChannelID` maps to `channelv2.ChannelID`.
- `message.Message` maps to `channelv2.Message`.
- `message.CommitMode` maps to `channelv2.CommitMode`.
- `channelv2.AppendBatchResult` maps back to item-aligned message append
  results.

Phase 1 should only expose fields that `channelv2.Message` can persist or
replicate today: message ID, sequence, channel ID, channel type, sender UID,
client message number, and payload. Legacy fields such as topic, stream, expire,
message key, and extended frame settings should not be advertised in
`internalv2` until the channelv2 storage model supports them.

Typed error mapping:

```text
channelv2.ErrNotLeader / clusterv2.ErrNotLeader      -> ErrNotLeader
channelv2.ErrStaleMeta                               -> ErrStaleRoute
channelv2.ErrChannelNotFound                         -> ErrChannelNotFound
channelv2.ErrBackpressured                           -> ErrBackpressured
clusterv2.ErrRouteNotReady / channelv2.ErrNotReady   -> ErrRouteNotReady
context.Canceled / context.DeadlineExceeded          -> unchanged
other errors                                         -> ErrAppendFailed wrapping source
```

Gateway reason mapping:

```text
ErrNotLeader / ErrStaleRoute / ErrRouteNotReady -> ReasonNodeNotMatch
ErrChannelNotFound                              -> ReasonChannelNotExist
ErrBackpressured / ErrAppendFailed              -> ReasonSystemError
validation or auth rejection                    -> corresponding stable reason
```

## 7. App Lifecycle

`internalv2/app` is intentionally small in phase 1:

```go
type App struct {
    cfg      Config
    cluster  ClusterRuntime
    messages *message.App
    gateway  *gateway.Gateway
    handler  *accessgateway.Handler
}
```

Start order:

```text
cluster.Start(ctx)
gateway.Start()
```

Stop order:

```text
gateway.Stop()
cluster.Stop(ctx)
```

If gateway startup fails after the cluster starts, `app.Start` must stop the
cluster before returning. If cluster startup or readiness fails, gateway must
not start. `Start` and `Stop` should have simple idempotency guards and focused
rollback tests, but phase 1 should avoid a broad lifecycle framework.

Config is separate from the current `internal/app.Config`:

```go
type Config struct {
    NodeID  uint64
    DataDir string

    Cluster clusterv2.Config
    Gateway GatewayConfig
    Message MessageConfig
}
```

Phase 1 does not add `wukongim.conf.example` keys because the new stack is not
yet wired into `cmd/wukongim`. Configuration keys should be added when a runtime
switch or production startup path is introduced.

## 8. Testing Strategy

Required focused tests:

```text
internalv2/usecase/message
  - Send rejects missing sender/channel/payload.
  - SendBatch preserves item order.
  - Message IDs are allocated only for valid durable items.
  - Append item errors map to stable SendResult reasons.
  - CommittedSink failures are observable but do not change phase-1 sendack success.

internalv2/access/gateway
  - SendPacket maps to SendCommand.
  - Successful SendResult writes Sendack with client_seq, client_msg_no, message_id, and message_seq.
  - Validation or reject result still writes Sendack with a stable reason.
  - Unknown frames return unsupported.
  - Batch handler writes aligned Sendacks in order.

internalv2/infra/cluster
  - AppendBatchRequest maps to channelv2.AppendBatchRequest.
  - channelv2 append results map back item-aligned.
  - clusterv2/channelv2 typed errors map to message append errors.

internalv2/app
  - New builds app with fake cluster and gateway dependencies.
  - Start order is cluster then gateway.
  - Gateway start failure stops cluster.
  - Stop order is gateway then cluster.
  - Single-node-cluster smoke verifies SendPacket -> Sendack success with a non-zero message sequence.
```

Add an import-boundary test for `internalv2/usecase/message` that rejects
imports of gateway, protocol frame, clusterv2, channelv2, access, or app
packages.

## 9. Performance Posture

Phase 1 optimizes for architecture that can become fast without overbuilding the
first slice:

- Use batch APIs from the gateway adapter through the appender.
- Route all durable appends through `AppendChannelBatch`.
- Keep delivery, conversation, CMD, and plugin side effects off the synchronous
  sendack path.
- Define clear payload ownership and cloning rules in the implementation plan.
- Keep DTO conversion narrow and predictable.

A future `runtime/sendplane` can sit between `usecase/message` and the appender
to add channel-keyed batching, backpressure, and async scheduling without
changing gateway or usecase contracts.

## 10. Non-Goals

Phase 1 does not:

- Replace the existing `internal` production app.
- Wire `internalv2` into `cmd/wukongim`.
- Implement recv delivery, offline storage, conversation projection, CMD sync,
  plugin hooks, management APIs, or HTTP APIs.
- Implement full send permission checks.
- Add standalone or non-cluster deployment behavior.
- Claim compatibility with all legacy message fields before channelv2 supports
  them.

## 11. Migration Path

After the send-to-sendack skeleton is verified, migration should proceed in
small slices:

1. Add production startup switch or explicit internalv2 harness once clusterv2
   readiness is stable enough for app wiring.
2. Migrate committed delivery from the `CommittedSink` port.
3. Migrate conversation projection from committed events.
4. Add permission, channel business checks, and system UID behavior.
5. Add plugin send hooks and plugin committed hooks.
6. Add CMD and request-scoped subscriber behavior.
7. Add management/API surfaces once runtime state exists behind narrow ports.

Each slice should keep `internalv2/usecase/message` entry-agnostic and preserve
the import boundary guard.
