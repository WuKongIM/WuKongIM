# Internal Target Architecture Design

## 1. Goal

将 `internal/` 从“组合根承担过多运行时细节 + 多处手写生命周期 + 用例之间有少量 DTO 耦合”的形态，演进为“集群语义优先的模块化单体”。

目标不是拆微服务，也不是改变部署语义。单节点部署仍然是单节点集群；所有消息、presence、delivery、conversation、channel runtime meta 都继续遵循统一集群路径。

最终目标：

- `app` only composes dependencies and owns lifecycle; it must not contain business runtime logic.
- `access` only adapts protocols and transports into usecase commands.
- `usecase` owns protocol-independent business orchestration.
- `runtime` owns node-local state machines, indexes, actors, allocators, and reusable runtime primitives.
- `infra` adapts concrete `pkg/*` implementations to internal ports.
- committed channel log entries are the durable message facts; delivery and conversation projection are driven from committed facts.
- all lifecycle rollback, resource cleanup, tests, and docs remain aligned with cluster semantics.

## 2. Current Problems To Fix

当前 `internal/` 的方向基本正确，但存在几个结构性问题：

- `internal/app/build.go` 构建逻辑过长，数据库、cluster、channel runtime、presence、delivery、access server 等资源交织在一个函数中，失败清理容易遗漏。
- `internal/app/lifecycle.go` 手写启动/停止顺序，局部失败回滚容易漏掉已启动组件。
- channel runtime meta、leader repair、liveness、delivery routing 等运行时逻辑散在 `internal/app`，使组合根变厚。
- `runtime/online` 直接依赖 gateway session 抽象，不利于保持 runtime 原语独立。
- 部分 usecase 之间通过另一个 usecase 的 command 类型耦合，边界不够干净。
- `FLOW.md`、测试分层、静态检查和 exported type 注释需要成为架构约束的一部分，而不是事后补丁。
- 部分三节点真实 harness 测试默认落在普通单测中，拉长 `go test ./internal/...` 时间。

## 3. Architecture Choice

采用“模块化单体 + 端口适配 + 内部事件”的架构。

不推荐把当前系统拆成微服务。IM send path、presence、delivery、channel log、slot metadata 对延迟和一致性都敏感。过早拆服务会把本地调用变成网络一致性问题，增加运维和排障成本。

推荐保留 Go 单仓和单进程部署优势，通过包边界和接口契约实现模块隔离：

```text
access -> usecase/contracts
usecase -> runtime/contracts/pkg abstractions
runtime -> pkg abstractions; no access dependency
infra -> concrete pkg adapters
app -> all modules as the only composition root
```

## 4. Target Package Layout

目标结构可以按以下方向演进。具体目录名可在实施时微调，但依赖方向必须保持稳定。

```text
internal/
  app/
    compose/              dependency graph construction
    lifecycle/            ordered lifecycle manager and resource stack
  config/                 file/env loading, defaults, validation, examples
  access/
    api/                  external HTTP API adapter
    gateway/              gateway frame adapter
    manager/              manager HTTP adapter
    node/                 node-to-node RPC adapter
  usecase/
    message/              durable send, recv ack, sync query orchestration
    presence/             authoritative route, lease, activation orchestration
    delivery/             delivery orchestration over runtime actors
    conversation/         sync query and projection orchestration
    management/           manager-facing aggregate queries/actions
    user/                 token and user/device operations
  runtime/
    channelmeta/          runtime meta resolver, bootstrapper, repairer, liveness
    delivery/             actors, retry wheel, ack index
    online/               online route registry over minimal session writer
    messageid/
    sequence/
  infra/
    cluster/              pkg/cluster adapters
    channellog/           pkg/channel adapters
    metastore/            pkg/slot/meta and proxy adapters
    observability/        logger, metrics, trace adapters
  contracts/
    messageevents/        committed message event contracts
    deliveryevents/       delivery route event contracts
```

The layout is successful only if package dependencies enforce the architecture. Moving files without changing dependency direction does not count as the refactor.

## 5. Core Boundaries

### 5.1 App

`app` is the composition root.

Allowed responsibilities:

- build concrete dependencies from validated config;
- wire ports to adapters;
- own resource stack and component lifecycle;
- expose limited accessors used by tests, API health, and command entrypoints;
- register observer hooks that delegate into runtime modules.

Forbidden responsibilities:

- channel leader repair decisions;
- message routing business logic;
- delivery actor behavior;
- presence route state transitions;
- conversation projection rules;
- direct request/response mapping for HTTP, gateway, or RPC.

### 5.2 Access

`access/*` packages adapt external protocols to usecase commands and map usecase results back to protocol responses.

Allowed responsibilities:

- decode and validate protocol shape;
- map HTTP/gateway/RPC payloads into command/query objects;
- map domain errors to HTTP status, gateway reason code, or RPC error;
- handle transport-specific timeouts and response writes.

Forbidden responsibilities:

- durable append policy;
- presence authority decisions;
- channel meta repair;
- delivery retry policy;
- conversation hot/cold projection rules.

### 5.3 Usecase

`usecase/*` packages orchestrate domain operations through small ports.

Example `message` ports:

```go
type ChannelAppender interface {
    Append(ctx context.Context, req AppendRequest) (AppendResult, error)
}

type ChannelMetaResolver interface {
    EnsureWritable(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

type CommittedPublisher interface {
    PublishCommitted(ctx context.Context, event MessageCommitted) error
}
```

The message usecase should make a message durable and publish a committed fact. It should not know whether delivery, conversation projection, or manager read models consume that fact.

### 5.4 Runtime

`runtime/*` packages own node-local primitives and state machines.

Examples:

- `runtime/channelmeta` owns metadata refresh, bootstrap, authoritative apply, liveness cache, and leader repair orchestration.
- `runtime/delivery` owns per-channel actors, retry wheel, in-flight routes, and ack index.
- `runtime/online` owns local online indexes, but depends only on a minimal writer interface rather than gateway session internals.

Suggested online boundary:

```go
type SessionWriter interface {
    WriteFrame(frame.Frame) error
    Close() error
}
```

Gateway session implements this interface, but runtime does not import gateway packages.

### 5.5 Infra

`infra/*` packages adapt concrete `pkg/*` components to usecase/runtime ports.

Examples:

- cluster adapter wraps `pkg/cluster` RPC, slot leader lookup, and node liveness source;
- channel log adapter wraps `pkg/channel` append/fetch/status;
- meta store adapter wraps slot proxy and metadata storage;
- observability adapter injects logger, metrics, and trace recorder.

## 6. Message Send Flow

Gateway path:

```text
Gateway Transport
  -> protocol decode
  -> access/gateway maps SendPacket to message.SendCommand
  -> usecase/message.Send
       1. validate sender and channel type
       2. normalize person channel
       3. resolve writable channel meta
       4. append to channel log through ChannelAppender
       5. receive durable MessageID and MessageSeq
       6. publish MessageCommitted event
  -> access/gateway writes Sendack
```

Committed side effects:

```text
MessageCommitted
  -> delivery subscriber
       -> runtime/delivery actor
       -> local or remote push
       -> ack/offline/retry handling
  -> conversation projector
       -> hot overlay update
       -> async durable flush
       -> cold conversation wakeup when needed
```

The sendack should represent durable commit success. Real-time delivery and conversation projection should be asynchronous unless a product requirement explicitly makes them blocking.

## 7. Channel Meta Runtime

Move current `app/channelmeta*.go` responsibilities into `runtime/channelmeta`.

Target subcomponents:

```text
Resolver
  EnsureWritable(ctx, channelID)
  Refresh(ctx, channelID)
  ApplyAuthoritative(ctx, meta)

Bootstrapper
  EnsureRuntimeMeta(ctx, channelID)

LeaderRepairer
  RepairAuthoritative(ctx, request)

LivenessCache
  UpdateNodeStatus(nodeID, status)
  IsAlive(nodeID)

Watcher
  WatchSlotLeaderChanges
  WatchLocalReplicaStateChanges
```

External modules should depend on narrow interfaces:

```go
type Resolver interface {
    EnsureWritable(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
    Refresh(ctx context.Context, id channel.ChannelID) (channel.Meta, error)
}

type AuthoritativeApplier interface {
    ApplyAuthoritative(ctx context.Context, meta channel.Meta) error
}
```

`runtime/channelmeta` may internally use singleflight, slot leader lookup, liveness cache, repair probes, and authoritative store writes. Callers should not know those details.

## 8. Lifecycle And Resource Management

Replace manual lifecycle sequencing with a small ordered lifecycle manager.

```go
type Component interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

Default startup order:

```text
cluster
  -> managed_slots_ready
  -> channelmeta
  -> presence
  -> conversation_projector
  -> gateway
  -> api
  -> manager
```

Requirements:

- startup follows dependency order;
- startup failure stops already-started components in reverse order;
- stop is idempotent;
- stop aggregates errors;
- each component has a bounded stop timeout where applicable;
- tests assert start and rollback order through the lifecycle manager, not through reflection hacks.

Resource construction should use a resource stack:

```text
open metadb                 -> push close
open raftdb                 -> push close
open channel log db         -> push close
create data-plane pool      -> push close
create data-plane client    -> push stop
create channel transport    -> push close if needed
create channel runtime      -> push stop/close if needed
build app successfully      -> transfer stack ownership to App.Stop
build fails                 -> close stack in reverse order
```

This ensures build failure cleanup is complete and testable.

## 9. Config Architecture

Split config into three layers:

```text
RawConfig      // file/env string representation
TypedConfig    // parsed, strongly typed config
RuntimeConfig  // derived concrete config for cluster, gateway, channel runtime
```

Flow:

```text
load file/env
  -> decode raw config
  -> apply defaults
  -> validate
  -> derive runtime configs
```

Requirements:

- config fields and environment keys use `WK_` consistently;
- environment variables override config file values;
- JSON list overrides remain whole-field overrides;
- every config field has an English comment;
- `wukongim.conf.example` is validated by test whenever config schema changes;
- deprecated fields such as static `Cluster.Slots` fail with clear migration messages.

## 10. Error Contract

Introduce stable internal error categories and adapter-level mappings.

Domain categories:

```text
unauthenticated
permission_denied
invalid_argument
not_found
stale_meta
not_leader
not_ready
timeout
unavailable
internal
```

Mapping responsibilities:

- access/api maps categories to HTTP status and stable JSON error code;
- access/gateway maps categories to WuKong reason code;
- access/node maps categories to RPC error payload;
- logs keep the original wrapped error;
- external clients do not receive raw internal error strings.

Example API error:

```json
{
  "code": "channel_not_ready",
  "message": "channel is temporarily unavailable"
}
```

## 11. Observability

Replace ad-hoc global observability hooks with injected ports where possible.

```go
type TraceRecorder interface {
    RecordSendStage(ctx context.Context, event SendStageEvent)
}
```

The app composition root builds one observability bundle and injects it into modules:

```text
logger
metrics registry
trace recorder
health reporters
debug snapshot providers
```

Send path trace stages should form one coherent chain:

```text
gateway.decode
gateway.map_command
message.ensure_meta
channel.append.local or channel.append.forward
replica.leader.queue_wait
replica.leader.local_durable
replica.leader.quorum_wait
message.committed
gateway.write_sendack
delivery.submit
conversation.project
```

## 12. Testing Strategy

Target test pyramid:

```text
large number of fast unit tests
  usecase/message
  usecase/presence
  runtime/delivery
  runtime/channelmeta
  config validation

component tests
  access/api with fake usecases
  access/gateway with fake message/presence
  access/node RPC codec contracts
  lifecycle manager and resource stack

integration tests
  single-node cluster
  three-node cluster
  leader failover
  pending checkpoint
  send stress
```

Rules:

- `go test ./internal/...` should stay fast enough for regular development.
- Multi-node real harness tests belong behind `integration` unless they are tightly bounded and explicitly justified.
- Avoid long `time.Sleep`; prefer fake clocks, explicit channels, or short eventually loops.
- Race tests should focus on runtime actors, online registry, gateway session/core, and channelmeta watchers.
- Static checks (`gofmt`, `go vet`, `staticcheck`) should be part of the quality gate.

## 13. Migration Plan

### Phase 1: Quality Gate And Documentation Alignment

- Run and fix `gofmt`, `go vet`, and `staticcheck` for `internal/...`.
- Update `internal/FLOW.md` to match manager lifecycle and current send path names.
- Move or tag slow multi-node tests that violate unit-test time constraints.
- Add missing English comments for key config structs and fields.

### Phase 2: Lifecycle And Resource Stack

- Add lifecycle manager and resource stack packages under `internal/app` or `internal/app/lifecycle`.
- Convert `App.Start` and `App.Stop` to component registration.
- Convert build cleanup to resource stack ownership transfer.
- Add rollback tests for every component failure point.

### Phase 3: Extract Channel Meta Runtime

- Create `internal/runtime/channelmeta`.
- Move resolver, bootstrapper, repairer, liveness cache, and watcher code out of `app`.
- Keep public behavior unchanged through adapter interfaces.
- Update `FLOW.md` and tests.

### Phase 4: Introduce Committed Event Contracts

- Add `contracts/messageevents` with `MessageCommitted`.
- Make message usecase publish committed facts instead of directly knowing concrete delivery/conversation routing.
- Add in-process event hub or explicit fanout publisher in app composition.
- Preserve current delivery and conversation behavior through subscribers.

### Phase 5: Boundary Cleanup

- Remove runtime dependency on gateway session package.
- Remove usecase-to-usecase command coupling where it is only DTO reuse.
- Move concrete `pkg/*` adapters into `infra` packages.
- Add dependency-direction checks or package import tests.

## 14. Success Criteria

The architecture refactor is successful when:

- `internal/app` is mostly composition and lifecycle; complex runtime logic lives in runtime/usecase/infra modules.
- `go test ./internal/...` is consistently fast for normal development.
- `go test -tags=integration ./internal/...` owns the expensive multi-node tests.
- `gofmt`, `go vet`, and `staticcheck` pass for `internal/...`.
- `internal/FLOW.md` matches the code and is updated with package moves.
- config changes are documented in English comments and reflected in `wukongim.conf.example`.
- message send semantics remain cluster-first and unchanged externally.
- delivery and conversation projection consume committed facts through explicit contracts.

## 15. Risks And Mitigations

- **Risk: Too much movement at once.** Mitigate by migrating one boundary at a time and preserving public behavior through adapters.
- **Risk: Event fanout changes send semantics.** Mitigate by first implementing the event publisher as a synchronous fanout wrapper that preserves current ordering, then make subscribers async only where tests prove it is safe.
- **Risk: Channel meta extraction breaks leader repair.** Mitigate with black-box tests around refresh, bootstrap, dead leader repair, draining repair, and remote append forwarding before moving files.
- **Risk: Package churn slows feature work.** Mitigate by landing lifecycle/resource stack first, then extracting channelmeta, then event contracts.
- **Risk: Integration tests hide regressions if over-tagged.** Mitigate by adding fast component tests for every behavior moved behind integration tests.

## 16. Non-Goals

- Do not split `internal` into separate deployable services.
- Do not introduce a separate non-cluster deployment mode.
- Do not change external gateway or HTTP protocol behavior as part of the architecture refactor.
- Do not rewrite `pkg/cluster` or `pkg/channel` unless a specific adapter boundary requires a small interface adjustment.
- Do not introduce a generic global service container.
