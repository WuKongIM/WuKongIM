# ISR Core Library Design

## 目标

设计一个可独立复用的单组 ISR 复制库:

```text
pkg/isr
```

它的定位不是“完整共识库”,而是:

> 一个由外部 control-plane 驱动的单组 ISR 数据复制引擎

`pkg/isr` 只解决单个复制组的数据面问题:

- leader / follower 角色切换
- append / fetch / ack
- `LEO / HW`
- leader lease fencing
- divergence truncate
- snapshot / restore

它不解决:

- leader 选举策略
- replica placement
- 业务语义
- 协议兼容
- 多组调度

这些由上层或旁路组件负责。

## 设计边界

### In Scope

- 单组 ISR 复制运行时
- 外部下发 `GroupMeta` 后的状态收敛
- leader append 与 follower fetch
- committed frontier 推进
- 旧 leader fencing
- 重启恢复与未提交尾部清理
- snapshot / catch-up

### Out of Scope

- control-plane quorum
- 自动 leader election
- membership 规划算法
- 多组共享调度
- 业务级幂等和业务消息格式
- 客户端协议兼容

## 核心设计原则

1. `pkg/isr` 是 data-plane,不是 controller
2. 外部 control-plane 是唯一 authority
3. 单组只允许一个可写 leader
4. committed frontier 语义必须精确定义
5. 空日志、恢复、截断都必须满足统一不变量

## 核心模型

### GroupMeta

`pkg/isr` 不自行决定谁是 leader,而是消费外部下发的元数据视图:

```go
type GroupMeta struct {
    GroupID      uint64
    Epoch        uint64
    Leader       NodeID
    Replicas     []NodeID
    ISR          []NodeID
    MinISR       int
    LeaseUntil   time.Time
}
```

语义:

- `Epoch`
  当前 leader 身份版本。每次切主都递增。
- `Leader`
  当前唯一可写 leader。
- `Replicas`
  当前目标副本集。
- `ISR`
  当前同步副本集。
- `LeaseUntil`
  由外部 control-plane 授予 leader 的 lease 截止时间。

### GroupMeta 归一化规则

为避免不同实现对同一份元数据做出不同解释,`GroupMeta` 必须满足:

1. `Replicas` 是有序、去重列表
2. `ISR` 是有序、去重列表
3. `ISR` 必须是 `Replicas` 的子集
4. 当 `Leader != 0` 时:
   - `Leader ∈ Replicas`
   - `Leader ∈ ISR`
5. `MinISR` 必须满足:
   - `1 <= MinISR <= len(Replicas)`
6. 不允许空 `Replicas`

`pkg/isr` 在接收元数据时必须做归一化和校验:

- 非法元数据直接返回 `ErrInvalidMeta`
- 不允许内部静默修正 leader/ISR 关系

`ISR` 的列表顺序不参与 committed 计算,它的语义是集合;保留顺序只是为了保持外部视图稳定和便于 diff。

### 同 epoch 更新规则

`ApplyMeta(meta)` 在 `meta.Epoch == local.Epoch` 时只允许更新:

- `LeaseUntil`
- `ISR`
- `Replicas`
- `MinISR`

不允许更新:

- `Leader`
- `GroupID`

如果同 epoch 下本地节点已不在 `Replicas` 中:

- `ApplyMeta` 只更新本地视图
- 由上层 runtime 随后显式调用 `BecomeFollower` 或 `Remove/Tombstone`
- `ApplyMeta` 自身不做 destructive 迁移

### ReplicaState

```go
type ReplicaState struct {
    GroupID uint64
    Role    Role

    Epoch  uint64
    Leader NodeID

    LogStartOffset uint64
    HW             uint64
    LEO            uint64
}
```

### committed frontier 采用 exclusive 语义

为避免空日志与边界状态歧义,`HW` 不定义为“最大已提交 offset”,而定义为:

> `HW = next uncommitted offset`

因此:

- 已提交记录满足 `offset < HW`
- 未提交记录满足 `offset >= HW`
- 空日志时 `HW = 0, LEO = 0`

统一不变量为:

```text
LogStartOffset <= HW <= LEO
```

这条不变量在以下状态都自然成立:

- 空副本: `0 <= 0 <= 0`
- 首条日志已写未提交: `0 <= 0 <= 1`
- 首条日志已提交: `0 <= 1 <= 1`
- 截断后无脏尾: `LogStartOffset <= HW == LEO`

## Checkpoint 语义

每个副本都必须持久化 committed frontier checkpoint:

```go
type Checkpoint struct {
    Epoch          uint64
    LogStartOffset uint64
    HW             uint64
}
```

### 关键约束

`Checkpoint` 不是 best-effort cache,而是 commit safety boundary 的一部分。

当某个 committed frontier `HW = O` 对上层可见时,所有被算进这次提交的副本都必须已经 durably 持久化:

- 日志记录 `< O`
- `Checkpoint{HW: O}`

实现可以做批量 fsync,但不能出现:

- 客户端已被告知 committed
- 副本重启后 checkpoint 仍落后
- 然后错误截断 committed 记录

重启恢复与 `BecomeLeader` 都必须以本地 checkpoint 为 committed 真值来源,不能从 `LEO` 反推。

### Load 语义

为避免恢复分支不一致,`CheckpointStore.Load()` 和 `EpochHistoryStore.Load()` 必须统一以下约定:

| 场景 | 返回 |
|------|------|
| 首次启动 / 文件不存在 / 空存储 | `(zeroValue, ErrEmptyState)` |
| 数据损坏 | `ErrCorruptState` |
| I/O 错误 | 原始 I/O 错误 |

其中:

- `Checkpoint` 的 zero value 为 `{Epoch: 0, LogStartOffset: 0, HW: 0}`
- `EpochHistory` 的 zero value 为 `nil`

调用方恢复逻辑:

- `ErrEmptyState` 视为“空副本首次启动”
- 其余错误视为恢复失败

恢复后必须再次校验:

```text
Checkpoint.LogStartOffset <= Checkpoint.HW
```

若不满足,视为 `ErrCorruptState`。

## 构造与依赖注入

为避免把恢复责任散落到调用方,`pkg/isr` 的运行时依赖必须明确注入。

建议最小构造依赖:

```go
type SnapshotApplier interface {
    InstallSnapshot(ctx context.Context, snap Snapshot) error
}

type ReplicaConfig struct {
    LocalNode         NodeID
    LogStore          LogStore
    CheckpointStore   CheckpointStore
    EpochHistoryStore EpochHistoryStore
    SnapshotApplier   SnapshotApplier
}
```

边界必须固定:

- `pkg/isr` 负责:
  - `HW/LEO/LogStartOffset` 语义
  - truncate 顺序
  - epoch history 持久化与恢复
  - snapshot 安装时的 offset/checkpoint 协调
- 注入的 `SnapshotApplier` 负责:
  - 持久化 snapshot payload
  - 恢复上层状态机或业务索引
  - 保证 `InstallSnapshot` 成功返回后,重新打开本地 `LogStore` 仍满足 `LEO() >= snap.EndOffset`

也就是说:

- `pkg/isr` 不负责生成 snapshot payload
- `pkg/isr` 也不解释 payload 的业务内容
- 它只协调 snapshot 对复制偏移状态的影响

## 启动与重启恢复顺序

副本在进程启动或重启后,必须先完成一次统一恢复,之后才能参与 `BecomeLeader` / `BecomeFollower`。

恢复顺序固定为:

1. 加载 `CheckpointStore.Load()`
2. 加载 `EpochHistoryStore.Load()`
3. 用加载结果重建内存态:
   - `LogStartOffset = checkpoint.LogStartOffset`
   - `HW = checkpoint.HW`
   - `epochHistory = loadedHistory`
4. 读取本地 `LEO = LogStore.LEO()`
5. 校验:
   - `LogStartOffset <= HW`
   - `HW <= LEO`
   - 若不满足,返回 `ErrCorruptState`
6. 若 `LEO > HW`,执行 `LogStore.Truncate(HW)` 删除未提交尾部
7. `Truncate(HW)` 成功后必须 `Sync()`,确保脏尾删除 durable
8. 重新读取 `LEO`,并要求 `LEO == HW`
9. 只有在步骤 1-8 全部成功后,副本才进入可服务的非写状态

恢复结果:

- `epochHistory` 必须在步骤 3 就进入内存,供后续 divergence 判断使用
- `LogStartOffset` 的恢复真值来自 `Checkpoint`,不是从日志文件长度猜测
- 若本地已经安装过 snapshot,则 `Checkpoint.LogStartOffset` 必须反映该 snapshot 的 `EndOffset`
- 若在步骤 6-7 之间崩溃,重启后必须重复该恢复流程,直到 `LEO == HW`

## 角色与状态机

### 角色

`pkg/isr` 至少维护以下角色:

- `Follower`
- `Leader`
- `FencedLeader`
- `Tombstoned`

### 方法级状态迁移

| 调用 | 前置条件 | 成功结果 | 失败结果 |
|------|----------|----------|----------|
| `ApplyMeta(meta)` | `meta.GroupID` 匹配, `meta.Epoch >= local.Epoch` | 更新非破坏性元数据视图,不做截断 | `ErrInvalidMeta`, `ErrStaleMeta` |
| `BecomeLeader(meta)` | `meta.Leader == localNode`, `meta.Epoch >= local.Epoch` | 执行恢复、截断未提交尾部、进入 `Leader` | `ErrInvalidMeta`, `ErrStaleMeta` |
| `BecomeFollower(meta)` | `meta.Leader != localNode`, `meta.Epoch >= local.Epoch` | 停止 append,进入 `Follower` | `ErrInvalidMeta`, `ErrStaleMeta` |
| lease 过期 | 本地当前为 `Leader` | 进入 `FencedLeader` | 无 |
| `Tombstone()` | 外部 runtime 下线该组 | 进入 `Tombstoned` | 无 |

约束:

- `ApplyMeta` 不执行 destructive truncate,也不隐式完成角色转换
- 角色转换必须通过 `BecomeLeader` / `BecomeFollower`
- `FencedLeader` 可以继续响应只读状态查询和被动复制,但不能 append
- `Tombstoned` 由外部 runtime 驱动,`pkg/isr` 只提供显式 API,不自行进入该状态

## 写入流程

### 角色约束

只有 leader 能接受 append。

follower 只做:

- fetch
- 落盘
- ack / progress 上报

### Record 模型

`pkg/isr` 的日志记录对库本身是业务无关的 opaque payload,但为了计划和实现必须明确其最低形状:

```go
type Record struct {
    Payload    []byte
    SizeBytes  int
}
```

规则:

- `Payload` 由上层定义编码格式
- `SizeBytes` 用于 `MaxBytes` 预算,等于记录落盘后的实际编码大小
- `LogStore.Append(records)` 必须是 all-or-nothing 原子追加
- `LogStore.Read(from, maxBytes)` 只能返回完整记录,不能返回半条记录

因此 fetch 的 `MaxBytes` 语义是:

- 尽量返回不超过 `MaxBytes` 的完整记录前缀
- 至少允许返回 1 条完整记录
- 若 `MaxBytes <= 0`,直接返回 `ErrInvalidFetchBudget`
- 若首条候选记录本身大于 `MaxBytes`,允许返回这 1 条完整记录

### Append

leader 处理写入时:

1. 校验本地仍为 leader
2. 校验本地 epoch 与 `GroupMeta.Epoch` 一致
3. 校验 lease 未过期
4. 校验 `len(ISR) >= MinISR`
5. 在单串行 append 临界区内:
   - 将记录追加到 `LEO`
   - `LEO++`
6. 等待足够多 ISR 副本复制
7. 推进 `HW`
8. 仅在 committed 后对上层返回成功

`pkg/isr` 只返回 committed offset,不解释其业务含义。

### Append 成功/失败契约

`Append` 至少应返回以下错误:

- `ErrNotLeader`
- `ErrStaleMeta`
- `ErrLeaseExpired`
- `ErrInsufficientISR`
- `ErrTombstoned`

并返回:

```go
type CommitResult struct {
    BaseOffset    uint64 // 本次 append 的起始 offset
    NextCommitHW  uint64 // committed frontier, exclusive
    RecordCount   int
}
```

语义:

- `BaseOffset`
  本次 append 的第一条记录 offset
- `NextCommitHW`
  本次提交完成后的 `HW`
- `RecordCount`
  成功提交的记录数

## Fetch 复制协议

建议固定为 follower 主动拉取:

```go
type FetchRequest struct {
    GroupID      uint64
    Epoch        uint64
    FetchOffset  uint64
    MaxBytes     int
}
```

leader 返回从 `FetchOffset` 开始的连续记录。

### Fetch 返回契约

```go
type FetchResult struct {
    Epoch         uint64
    HW            uint64
    Records       []Record
    TruncateTo    *uint64
}
```

语义:

- 正常追赶:
  - `TruncateTo == nil`
  - `Records` 为从 `FetchOffset` 开始的连续记录
- 发生分叉:
  - `TruncateTo != nil`
  - follower 必须先截断到该位置再继续 fetch

`Fetch` 至少应返回以下错误:

- `ErrStaleMeta`
- `ErrTombstoned`
- `ErrNotLeader`
- `ErrSnapshotRequired`

优点:

- follower 自带背压
- 重启恢复可从本地 `LEO` 继续
- leader 不维护复杂 push 队列

## HW 推进

leader 维护 ISR 内每个 follower 的 `MatchOffsetExclusive`,定义为:

> 该副本下一条未复制 offset

leader 自己的 `MatchOffsetExclusive = LEO`。

建议提交规则:

```text
HW = ISR 中第 MinISR 大的 MatchOffsetExclusive
```

当 `MinISR == len(ISR)` 时,等价于 ISR 最小 `MatchOffsetExclusive`。

## Lease 与 fencing

因为 `pkg/isr` 不做共识,所以必须支持外部授予的 leader lease。

规则:

1. leader 只有在 `now < LeaseUntil` 时可继续接写
2. 一旦 lease 过期,必须立即自我 fence
3. 被 fence 的旧 leader:
   - 停止 append
   - 可继续被拉取已持有日志
   - 不得再向上层报告可写
4. follower 永远不能自发升主

## BecomeLeader 恢复顺序

`BecomeLeader(meta)` 不能等价于“把角色切到 leader 后立刻接写”。

必须固定为:

1. 确保“启动与重启恢复顺序”已经成功执行完成
2. 校验 `meta.Leader == localNode` 且 `meta.Epoch >= local.Epoch`
3. 以当前恢复后的本地状态为基准:
   - `recoveryCutoff = HW`
4. 若 `LEO > recoveryCutoff`,则再次执行 `LogStore.Truncate(recoveryCutoff)` 并 `Sync()`
5. 重新读取 `LEO`,要求 `LEO == recoveryCutoff`
6. 以当前 `LEO` durably 追加:
   - `EpochPoint{Epoch: meta.Epoch, StartOffset: LEO}`
7. 将新 `epochHistory` 发布到内存态
8. 清空旧 leader 时代遗留的 peer progress
9. 仅当以下条件都满足时才转为可写:
   - 本地角色是 leader
   - epoch 已切到新值
   - lease 已生效
   - 步骤 4 的 truncate 已 durable 完成
   - 步骤 6 的 epoch history append 已 durable 完成

因此:

- committed 记录 `< HW` 永不回退
- 未提交脏尾全部被丢弃
- 下一条新记录总是从当前 `LEO` 继续

durable 顺序是硬约束:

```text
truncate dirty tail -> sync truncate -> append epoch history -> first writable append
```

不允许:

- 先写新 epoch history,再回头 truncate 旧脏尾
- 在 epoch history 尚未 durable 前接收第一条新写

崩溃语义:

- 若在 truncate durable 后、epoch history append 前崩溃:
  - 重启后仍按 follower/non-writable 恢复
  - 再次执行 `BecomeLeader`
- 若在 epoch history append 后、首条新写前崩溃:
  - 重新执行 `BecomeLeader` 时,相同 `EpochPoint` 追加必须幂等成功

## Divergence 与 truncate

落后 follower 或旧 leader 重新加入时,leader 必须支持返回 truncate 指令。

规则:

1. 先比较本地 epoch/offset 历史
2. 如果 follower 尾部与 leader 分叉:
   - 先返回 `truncateTo`
3. follower 只能截断 `offset >= HW` 的未提交尾部
4. 截断完成后再继续 fetch

如果 follower 的 `FetchOffset < LogStartOffset`,说明它已经落后到必须装 snapshot。

此时:

- `Fetch` 返回 `ErrSnapshotRequired`
- follower 必须通过 snapshot 路径恢复到 `LogStartOffset` 附近

`pkg/isr` 必须提供 epoch history:

```go
type EpochPoint struct {
    Epoch       uint64
    StartOffset uint64
}
```

用于定位分叉点。

epoch history 不能只存在内存中,必须持久化并参与恢复。

建议抽象:

```go
type EpochHistoryStore interface {
    Load() ([]EpochPoint, error)
    Append(point EpochPoint) error
}
```

`Append(point)` 的语义必须固定:

- 若 `point.Epoch` 大于当前最后一个 epoch,正常追加
- 若最后一个点与 `point` 完全相同,视为幂等成功
- 其余情况视为 `ErrCorruptState` 或等价持久化错误

这样 `BecomeLeader` 在崩溃重试时才能安全重放最后一个 epoch point。

归属规则:

- `BecomeLeader(meta)` 成功切主后,在接受新写前必须追加:
  - `EpochPoint{Epoch: meta.Epoch, StartOffset: LEO}`
- 该追加必须 durable 持久化
- 重启恢复时必须先加载 epoch history,再进行 divergence 定位与 leader 恢复

也就是说:

- `EpochHistoryStore` 由 `pkg/isr` runtime 负责写入
- `LogStore` 和 `CheckpointStore` 负责数据与 committed frontier
- 三者共同定义恢复真值

## Snapshot

单组 ISR 库必须支持 snapshot,但 snapshot 内容与 payload 来源由上层定义。

`pkg/isr` 只要求:

```go
type Snapshot struct {
    GroupID      uint64
    Epoch        uint64
    EndOffset    uint64
    Payload      []byte
}
```

语义:

- `EndOffset` 采用 exclusive 语义
- 加载 snapshot 后:
  - `LogStartOffset = EndOffset`
  - `HW = EndOffset`
  - `LEO >= EndOffset`

边界:

- snapshot payload 的生成、拉取、业务含义解释不属于 `pkg/isr`
- 这些由注入的 `SnapshotApplier` 与上层 transport/runtime 负责
- `pkg/isr` 只负责协调 snapshot 安装后本地 offset 状态必须如何变化

`pkg/isr` 必须暴露显式 snapshot 安装能力:

```go
InstallSnapshot(ctx context.Context, snap Snapshot) error
```

要求:

- 安装顺序必须固定为:
  1. 调用 `SnapshotApplier.InstallSnapshot(ctx, snap)` 使 snapshot payload durable
  2. 持久化 `Checkpoint{Epoch: snap.Epoch, LogStartOffset: snap.EndOffset, HW: snap.EndOffset}`
  3. 将内存态更新为:
     - `LogStartOffset = snap.EndOffset`
     - `HW = snap.EndOffset`
     - `LEO = LogStore.LEO()`
  4. 校验 `LEO >= snap.EndOffset`,否则视为 `ErrCorruptState`
- 安装 snapshot 后,本地 committed frontier 至少推进到 `snap.EndOffset`
- follower 若因 `ErrSnapshotRequired` 进入恢复路径,必须先 `InstallSnapshot`,再继续 fetch 增量日志
- 一旦步骤 2 成功,重启恢复必须能从 `Checkpoint.LogStartOffset` 重新得到正确 `LogStartOffset`

## 抽象接口

```go
type LogStore interface {
    LEO() uint64
    Append(records []Record) (base uint64, err error)
    Read(from uint64, maxBytes int) ([]Record, error)
    Truncate(to uint64) error
    Sync() error
}
```

`Truncate(to)` 采用 exclusive 语义:

- 保留所有 `offset < to` 的记录
- 删除所有 `offset >= to` 的记录
- 截断后 `LEO = to`

运行时约束:

- `pkg/isr` 只能对 `to >= HW` 执行 truncate

```go
type CheckpointStore interface {
    Load() (Checkpoint, error)
    Store(Checkpoint) error
}
```

```go
type EpochHistoryStore interface {
    Load() ([]EpochPoint, error)
    Append(point EpochPoint) error
}
```

```go
type Replica interface {
    ApplyMeta(meta GroupMeta) error
    BecomeLeader(meta GroupMeta) error
    BecomeFollower(meta GroupMeta) error
    Tombstone() error
    InstallSnapshot(ctx context.Context, snap Snapshot) error
    Append(ctx context.Context, batch []Record) (CommitResult, error)
    Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
    Status() ReplicaState
}
```

## 与上层的关系

`pkg/isr` 不知道:

- 这是不是 `channel`
- offset 对应的业务序号是什么
- 业务幂等键是什么
- 删除态应该返回什么错误

这些都由上层适配层负责。

## 测试重点

至少覆盖:

1. 空日志 / 首条提交边界
2. `HW` exclusive frontier 正确性
3. lease 过期后旧 leader 不可写
4. `BecomeLeader` 会丢弃未提交尾部
5. divergence truncate 只影响未提交记录
6. checkpoint 落盘与 committed 可见性的顺序约束
7. snapshot 恢复后不变量成立
