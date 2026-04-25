# MultiISR Library Design

## 目标

设计一个节点级多组 ISR 管理库:

```text
pkg/multiisr
```

它建立在 `pkg/isr` 之上,负责:

- 在单节点内管理大量 ISR 组
- 统一调度复制、fetch、lease 检查、snapshot 等后台任务
- 复用节点间连接与批量 IO
- 将“单组 correctness”与“多组规模管理”分层

它的定位类似:

```text
pkg/isr      ~= 单组复制引擎
pkg/multiisr ~= 节点级 group runtime / scheduler
```

## 为什么单独拆库

如果把“单组复制正确性”和“成千上万组的调度/资源管理”揉在一起,会同时放大两类复杂度:

- correctness
- scalability

拆分后可以得到更清晰的边界:

- `pkg/isr` 专注单组语义与安全性
- `pkg/multiisr` 专注规模管理

## 前置依赖

`pkg/multiisr` 是建立在 `pkg/isr` 之上的第二层库。

planning 时必须把它视为:

- 前置 spec: [2026-04-02-isr-design.md](/Users/tt/Desktop/work/go/WuKongIM-v3.1/docs/superpowers/specs/2026-04-02-isr-design.md)
- 当前仓库迁移关系:
  - `pkg/multiraft` 继续服务于 controller/control-plane
  - `pkg/isr` / `pkg/multiisr` 是为 channel data-plane 新增的并行栈

也就是说:

- `pkg/multiisr` 不会在实现上“复用或替换 `pkg/multiraft` 单组语义”
- 它直接依赖新的 `pkg/isr` 抽象

## 设计边界

### In Scope

- group registry
- scheduler
- peer 连接复用
- fetch/append IO batching
- lease / timeout 扫描
- snapshot throttle
- per-group 生命周期管理
- 指标与限流

### Out of Scope

- 单组 ISR 复制算法
- control-plane 一致性
- 业务语义
- 客户端协议

## 核心组件

### GroupRegistry

负责管理本节点承载的所有 group:

```go
type GroupRegistry interface {
    Ensure(groupID uint64, meta isr.GroupMeta) error
    Remove(groupID uint64) error
    Get(groupID uint64) (GroupHandle, bool)
    List() []GroupHandle
}
```

### Scheduler

统一调度多组任务,避免每组自带一堆 goroutine。

建议工作类型:

- `ControlTask`
  处理外部元数据变更
- `ReplicationTask`
  处理 fetch / ack / progress
- `CommitTask`
  推进 HW 与完成提交回调
- `LeaseTask`
  检查 leader lease
- `SnapshotTask`
  执行 snapshot / restore

推荐调度顺序:

```text
control -> replication -> commit -> lease -> snapshot
```

原因:

- 先收敛 control-plane 视图
- 再处理复制事实
- 再推进提交与回调
- 最后做后台维护

这条顺序对 scheduler 是硬约束,不是 best-effort 建议。

其中 `LeaseTask` 属于 safety-critical preemption:

- 一旦检测到 lease 过期,必须在下一轮 append 前生效
- 不能把 lease 检查当作“随缘后台清理”

此外必须再固定一条 last-mile 规则:

> 每次进入 group append 临界区前,runtime 都必须同步检查一次 lease

也就是说:

- `LeaseTask` 负责尽快把过期 leader 切到 fenced
- append 快路径仍必须做一次同步 lease 校验

因此不会出现“lease 已过期,但因为 scheduler 还没轮到 LeaseTask 而继续 append 一轮”的空窗。

### PeerSessionManager

同一对节点之间不能为每个 group 单独建连接。

`pkg/multiisr` 应维护 node-pair 级别连接复用:

```go
type PeerSessionManager interface {
    Session(peer NodeID) PeerSession
}
```

一个 `PeerSession` 上可复用:

- fetch 请求
- fetch 响应
- truncate 指令
- snapshot 传输

建议明确 `PeerSession`:

```go
type PeerSession interface {
    Send(env Envelope) error
    TryBatch(env Envelope) bool
    Flush() error
    Backpressure() BackpressureState
    Close() error
}
```

配套类型也应在第一版定死:

```go
type MessageKind uint8

const (
    MessageKindFetchRequest MessageKind = iota + 1
    MessageKindFetchResponse
    MessageKindTruncate
    MessageKindSnapshotChunk
    MessageKindAck
)
```

```go
type BackpressureLevel uint8

const (
    BackpressureNone BackpressureLevel = iota
    BackpressureSoft
    BackpressureHard
)

type BackpressureState struct {
    Level           BackpressureLevel
    PendingRequests int
    PendingBytes    int64
}
```

解释:

- `BackpressureNone`: 正常收发
- `BackpressureSoft`: 允许继续排队,但 scheduler 应主动降速
- `BackpressureHard`: 当前 peer session 已饱和,新流量必须延后

其中 envelope 至少包含:

```go
type Envelope struct {
    Peer       NodeID
    GroupID    uint64
    Epoch      uint64
    Generation uint64
    RequestID  uint64
    Kind       MessageKind
    Payload    []byte
}
```

字段用途:

- `GroupID`: 多组复用同一连接时做 demux
- `Epoch`: 丢弃旧 leader / 旧视图包
- `Generation`: 区分 tombstone 前后的同一 `groupID` 实例
- `RequestID`: request/response correlation

batching 放在 `PeerSession` 之上,由 `pkg/multiisr` runtime 控制,`Transport` 只负责 envelope 级收发。

### Batching

多个 group 发往同一 peer 的小请求应允许批量发送。

目标:

- 降低 syscalls
- 降低连接数量
- 降低高 group 数量时的包头放大

但 batching 不能改变 `pkg/isr` 的单组顺序语义。

## 生命周期

### EnsureGroup

当 control-plane 指定本节点承载某组时:

1. registry 中查找是否已存在
2. 不存在则创建 `isr.Replica`
3. 注入持久化和 transport 适配
4. 将 group 注册进 scheduler
5. 应用首份 `GroupMeta`

### RemoveGroup

移除必须是两阶段:

1. 从 scheduler 下线
2. 进入 tombstone
3. 延迟释放本地资源

不能直接把运行时对象硬删掉,否则 in-flight 回调和网络包会污染新实例。

### Tombstone 规则

必须引入 `Generation`,否则同一 `groupID` 的旧包会污染新实例。

规则:

1. 每次 `EnsureGroup` 创建新实例时:
   - `Generation++`
2. `RemoveGroup` 后:
   - 旧实例进入 tombstone
   - 保留 `TombstoneTTL`
3. tombstone 期间:
   - 拒绝 append
   - 继续接收并丢弃旧 generation 包
4. 同一 `groupID` 可以立即再次 `Ensure`
   - 但新实例必须使用新的 `Generation`
5. 收到旧 generation 的晚到包时:
   - 直接丢弃

建议:

```go
type TombstonePolicy struct {
    TombstoneTTL time.Duration
}
```

`TombstoneTTL` 必须作为 runtime 显式配置项提供,不能依赖隐含公式。

第一版建议默认值:

```text
TombstoneTTL = 30s
```

`Generation` 的所有权也必须定死:

- `Generation` 是 node-local、per-group 的持久化单调计数
- 它不等于 control-plane 的 `Epoch`
- 它必须保存在本地 registry metadata store 中并跨重启恢复
- `EnsureGroup` 必须先 durably 分配新 `Generation`,再注册新实例

校验规则:

- peer demux 的主键是 `(GroupID, Generation)`
- freshness 判定仍由 `Epoch` 负责
- transport/runtime 只有在 `(GroupID, Generation)` 命中当前实例或 tombstone 实例时才处理包
- 其余晚到包一律丢弃

## Transport 抽象

`pkg/multiisr` 需要一层节点级 transport,但不能绑死某个 wire protocol:

```go
type Transport interface {
    Send(peer NodeID, env Envelope) error
    RegisterHandler(fn func(Envelope))
}
```

`Transport` 不负责:

- group demux
- batching 策略
- request correlation
- tombstone generation 判定

这些都由 `pkg/multiisr` runtime 处理。

fetch 的边界也必须定死:

- follower fetch 是 peer-to-peer 内部 RPC
- 它不是 `pkg/multiisr.Runtime` 对业务暴露的 public API
- `Runtime` / `GroupHandle` 只对上层暴露 append 和状态查询
- fetch 请求的发起、批量化、重试和 demux 全部属于 `pkg/multiisr` 内部职责

### GenerationStore

`Generation` 需要持久化来源,不能只靠进程内自增。

建议抽象:

```go
type GenerationStore interface {
    Load(groupID uint64) (uint64, error)
    Store(groupID uint64, generation uint64) error
}
```

`Load` 语义必须固定:

- 若 `groupID` 从未分配过 generation,返回 `(0, nil)`
- 若持久化记录损坏、无法解析或违背单调约束,返回非空 error
- 调用方不得把 error 当作“缺省为 0”继续启动实例
- `Store` 成功返回后,该 generation 必须在重启后可见

`EnsureGroup` 的分配顺序必须固定为:

1. 读取当前持久化 generation
2. `next = current + 1`
3. durably `Store(groupID, next)`
4. 用 `next` 创建并注册新实例

这样才能在重启后继续屏蔽晚到包。

消息类型至少包括:

- fetch request
- fetch response
- truncate command
- snapshot chunk
- ack / progress

## 资源与限流

### 必须集中控制的资源

- 活跃 group 数
- 每 peer 并发 fetch 数
- 每 group 未提交日志内存
- snapshot 并发数
- 单节点总恢复带宽

### 建议引入的配额

```go
type Limits struct {
    MaxGroups              int
    MaxFetchInflightPeer   int
    MaxSnapshotInflight    int
    MaxRecoveryBytesPerSecond int64
}
```

超过限额时的行为也必须明确:

- `MaxGroups`: 拒绝新的 `EnsureGroup`,返回 `ErrTooManyGroups`
- `MaxFetchInflightPeer`: 新 fetch 进入 peer 局部队列,必要时返回 backpressure
- `MaxSnapshotInflight`: 新 snapshot 任务排队
- `MaxRecoveryBytesPerSecond`: throttle,不允许绕过

## 多组调度而非多组共识

`pkg/multiisr` 不是“一个 giant multi-group 共识协议”。

它只是:

> 多个 `pkg/isr` 实例的节点级运行时容器

所以它不重新定义:

- committed 规则
- leader/follower 语义
- lease 语义

这些仍全部继承自 `pkg/isr`。

## 与业务适配层的关系

`pkg/multiisr` 不知道:

- 哪些 group 是 channel
- 哪些 group 是别的业务对象
- 上层 offset 映射成什么业务序号

它只对上暴露:

- group status
- append/fetch result
- metric/event hooks

更精确地说:

- 对业务适配层暴露:
  - append
  - status
  - event hooks
- 对 peer runtime 内部暴露:
  - fetch
  - snapshot transport
  - truncate / ack

## 第一阶段与第二阶段

建议实现顺序:

### 第一阶段

- 做最小 `pkg/multiisr`
- group registry
- scheduler
- peer session 复用
- 基础限流

### 第二阶段

- 更激进的 batching
- snapshot queue
- 热组优先级调度
- 细粒度资源隔离

不要在第一阶段就把所有“大规模组管理”优化一次性做完。

## 抽象接口

```go
type Runtime interface {
    EnsureGroup(meta isr.GroupMeta) error
    RemoveGroup(groupID uint64) error
    ApplyMeta(meta isr.GroupMeta) error
    Group(groupID uint64) (GroupHandle, bool)
}
```

```go
type GroupHandle interface {
    ID() uint64
    Status() isr.ReplicaState
    Append(ctx context.Context, records []isr.Record) (isr.CommitResult, error)
}
```

## Reconcile 流程

control-plane 到 runtime 的顺序必须固定:

1. 首次下发某组 metadata:
   - `EnsureGroup(meta)`
2. 已存在组收到更新:
   - `ApplyMeta(meta)`
3. control-plane 判定本节点不再承载该组:
   - `RemoveGroup(groupID)`
4. 同一 `groupID` 被重新分配回来:
   - 再次 `EnsureGroup(meta)` 创建新 generation 实例

职责边界:

- `EnsureGroup`
  负责创建实例、分配新 generation、注册 scheduler
- `ApplyMeta`
  负责对现存实例做元数据收敛
- `RemoveGroup`
  负责驱动 tombstone 和延迟释放

## 测试重点

至少覆盖:

1. 大量 group 注册/移除时无资源泄漏
2. 同 peer 多 group 连接复用正确
3. scheduler 不会打破单组顺序
4. snapshot / recovery 有限流
5. group tombstone 不会被旧网络包污染
