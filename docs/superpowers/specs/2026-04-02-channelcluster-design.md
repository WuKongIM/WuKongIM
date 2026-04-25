# ChannelCluster Library Design

> Note (2026-04-03): The runtime identity model in this historical design is superseded by
> `docs/superpowers/specs/2026-04-03-channel-keyed-isr-design.md`.
> Current code uses a strong string `isr.GroupKey`, and the only business-to-runtime translation
> is the local `ChannelKey -> GroupKey` derivation in `pkg/storage/channellog`.
> Do not reintroduce controller-assigned numeric `GroupID`.

## 目标

设计一个面向 `channel` 业务语义的集群库:

```text
pkg/channelcluster
```

它建立在:

- `pkg/isr`
- `pkg/multiisr`

之上,负责把通用 ISR 复制能力适配成 `WuKongIM` 的 `channel` 数据面。

核心语义固定为:

1. `一个 channel = 一个 ISR group`
2. `messageSeq = committed offset + 1`

## 依赖关系

```text
internal/usecase/channelmeta   -> control-plane metadata
pkg/isr                        -> 单组复制语义
pkg/multiisr                   -> 多组运行时管理
pkg/channelcluster             -> channel-specific data plane
```

`pkg/channelcluster` 不负责 control-plane quorum,但消费其元数据视图。

## 设计边界

### In Scope

- `channel -> ISR group` 绑定
- `messageSeq` 语义
- channel 消息写入/读取
- `clientMsgNo` 幂等
- `Deleting` 语义
- `messageSeq` 协议升级与兼容
- 将业务错误映射给 gateway/API

### Out of Scope

- channel metadata quorum
- replica placement
- 通用 ISR 调度

## metadata 契约

`pkg/channelcluster` 依赖外部 control-plane 提供:

```go
type ChannelMeta struct {
    ChannelID   string
    ChannelType uint8

    ChannelEpoch uint64
    LeaderEpoch  uint64

    Replicas []NodeID
    ISR      []NodeID
    Leader   NodeID

    MinISR int
    Status ChannelStatus

    Features ChannelFeatures
}
```

epoch 关系固定为:

- `ChannelEpoch`
  结构性元数据版本,在以下变化时递增:
  - `Status`
  - `Replicas`
  - `ISR`
  - `Features`
- `LeaderEpoch`
  leader 身份版本,切主时递增

因此:

- 允许 `ChannelEpoch` 不变而 `LeaderEpoch` 单独递增
- 不允许 `LeaderEpoch` 倒退

当前实现不再把运行时复制组身份存入 `ChannelMeta`。

映射规则固定为:

1. `channel` 仍是唯一业务复制单元
2. 节点侧通过稳定的 `ChannelKey -> isr.GroupKey` 规则本地推导运行时 key
3. 该推导规则必须在所有节点上一致且可重复
4. 所有数据路径都必须先查询 `ChannelMeta`,再通过本地推导出的 `GroupKey` 定位到底层 ISR group

这样才能避免不同节点对同一 channel 落到不同复制组,同时保持 ISR 运行时对业务无感知。

### metadata 获取与失效模型

`pkg/channelcluster` 的 metadata 输入必须通过:

```go
ApplyMeta(meta ChannelMeta)
```

统一进入本地缓存。

规则:

1. `ApplyMeta` 是 runtime metadata 的唯一写入口
2. `Send` / `Fetch` / `Status` 只读取本地缓存,不在请求路径同步访问 controller
3. 本地缓存命中状态必须区分:
   - `Missing`: 本地完全没有该 channel 的 runtime metadata
   - `Present`: 本地存在 `ChannelMeta`
4. 若 `Send` / `Fetch` / `Status` 遇到 `Missing`,统一返回 `ErrStaleMeta`
   - 这包括:
     - 首次冷启动尚未 preload
     - 本地 cache eviction 后再次访问
     - tombstone/GC 后再次访问旧 channel
   - runtime 不得在 `Missing` 场景直接猜测 `ErrChannelNotFound` 或 `ErrNotLeader`
5. `ApplyMeta` 的 compare/update 规则必须机械化:
   - 若 `meta.ChannelEpoch < local.ChannelEpoch`,返回 `ErrStaleMeta`
   - 若 `meta.ChannelEpoch == local.ChannelEpoch` 且 `meta.LeaderEpoch < local.LeaderEpoch`,返回 `ErrStaleMeta`
   - 若 `(meta.ChannelEpoch, meta.LeaderEpoch)` 与本地完全相等:
     - 若关键字段 `Status/Replicas/ISR/Leader/MinISR/Features` 全相等,视为幂等重放
     - 否则返回 `ErrConflictingMeta`
   - 若 `meta.ChannelEpoch > local.ChannelEpoch`,整体替换本地缓存
   - 若 `meta.ChannelEpoch == local.ChannelEpoch` 且 `meta.LeaderEpoch > local.LeaderEpoch`,按新 leader 视图替换缓存
6. access/usecase 侧应尽量把请求观察到的版本作为 request-scoped token 透传给 runtime
7. 若请求路径发现本地 metadata 过期、cache miss 或 role 不匹配:
   - 返回 `ErrStaleMeta` / `ErrNotLeader`
   - 由上层 access/usecase 触发 metadata refresh 后重试

这样可以保持:

- runtime 边界稳定
- 请求路径不被 controller 依赖拖慢
- stale-meta 处理责任清晰

因此 access/usecase 的 refresh 规则也必须固定:

1. runtime 返回 `ErrStaleMeta` 时,先向 control-plane 拉取最新 metadata
2. 若 control-plane 返回 channel 不存在或已删除,由 access/usecase 向调用方返回 `ErrChannelNotFound`
3. 若 control-plane 返回有效 `ChannelMeta`,先 `ApplyMeta(meta)` 再决定是否重试原请求

`Status` 至少包括:

- `Creating`
- `Active`
- `Deleting`
- `Deleted`

`Features` 至少包括:

```go
type ChannelFeatures struct {
    MessageSeqFormat MessageSeqFormat
}
```

## messageSeq 语义

### 规范化定义

`pkg/channelcluster` 将 `pkg/isr` 暴露的 committed offset 解释为:

```text
messageSeq = committed offset + 1
```

这里 committed offset 使用 `pkg/isr` 的 exclusive frontier:

- 记录 committed 当且仅当 `offset < HW`
- 最后一条 committed 记录序号为 `messageSeq = HW`

因此:

- 空 channel: `HW = 0`,没有可见消息
- 第一条消息 committed 后: `HW = 1`,其 `messageSeq = 1`

### 位宽选择

`pkg/channelcluster` 的规范化 `messageSeq` 类型固定为:

```go
type MessageSeq uint64
```

不允许:

- 回绕
- reset 每 channel 序号
- 对外无提示地截断成 `uint32`

## 幂等语义

### 必须复制,不能只放 leader 内存

幂等索引不是 leader 本地缓存。

它必须作为 channel 复制状态的一部分,在所有副本上持久化并参与恢复。

建议幂等键:

```text
(channelID, channelType, senderUID, clientMsgNo)
```

建议值:

```go
type IdempotencyEntry struct {
    MessageID  uint64
    MessageSeq uint64
    Offset     uint64
}
```

### 落盘语义

每条消息写入时,leader 必须在同一业务提交边界内完成:

1. 追加消息记录到日志
2. 以这条日志记录为唯一真值来源更新幂等索引
3. 等待 committed
4. 对外返回同一条消息对应的 `messageSeq`

这里的原子性必须定死:

> “消息记录 + 幂等索引”不是两个彼此独立的复制提交,而是同一条 committed log record 的两个 apply 副作用

因此不变量为:

- 若某条 `IdempotencyEntry` 对外可见,则对应消息一定 committed
- 若某条消息 committed,则所有副本在 apply 该 log record 后都必须得到同一条 `IdempotencyEntry`

若命中相同 `(channelID, channelType, senderUID, clientMsgNo)` 但 payload 不同:

- 必须返回 `ErrIdempotencyConflict`
- 不允许复用旧 `MessageID` / `MessageSeq`
- 也不允许偷偷当成一条新消息继续写入

恢复时:

- 副本必须从本地存储恢复幂等索引
- snapshot 必须包含幂等索引状态

否则切主后无法保证重试仍返回原始 `messageSeq`。

## ChannelStore 语义

`pkg/channelcluster` 需要两类持久化:

### 1. MessageLog

真正的消息复制日志,由 `pkg/isr` 驱动 offset / HW。

每条消息记录至少包含:

- `MessageID`
- `SenderUID`
- `ClientMsgNo`
- `Payload`

### 2. ChannelStateStore

保存:

- 幂等索引
- 可选的按 `messageSeq` 或 offset 的二级索引
- snapshot payload

推荐抽象:

```go
type ChannelStateStore interface {
    PutIdempotency(key IdempotencyKey, entry IdempotencyEntry) error
    GetIdempotency(key IdempotencyKey) (IdempotencyEntry, bool, error)
    Snapshot(offset uint64) ([]byte, error)
    Restore(snapshot []byte) error
}
```

## 写入路径

1. gateway/API 路由写请求到当前 channel leader
2. `pkg/channelcluster` 校验:
   - `Status == Active`
   - `LeaderEpoch` 匹配
   - `ChannelEpoch` 不过期
   - 协议版本可接受
   - 若请求携带 `ExpectedChannelEpoch/ExpectedLeaderEpoch`,则必须与本地缓存兼容,否则返回 `ErrStaleMeta`
3. 先查幂等索引:
   - 命中则直接返回历史 `messageSeq`
4. 若幂等未命中,由当前 leader 生成全局唯一 `MessageID`
   - `MessageID` 由 `pkg/channelcluster` 负责生成
   - 生成器由上层注入,但必须保证 cluster-wide uniqueness
   - 推荐实现为 `nodeID` 前缀 + 本地单调序列
5. 调用 `pkg/multiisr` 对应 group append
6. append committed 后取得 committed offset
7. 转换:
   - `messageSeq = offset + 1`
8. 更新业务索引并返回

因此:

- 首次写入时同时确定 `MessageID` 和 `MessageSeq`
- 幂等重试命中时必须返回同一个 `MessageID` 和 `MessageSeq`

## 读取路径

默认只读 committed 消息:

- 从 `messageSeq = N` 读取,转换为 `offset = N - 1`
- 仅返回 `offset < HW` 的记录

`pkg/channelcluster` 不暴露 follower stale read 语义。

## 删除语义

`Deleting` 阶段固定为:

> 对外既不可写,也不可读

行为:

- 新写请求: `ErrChannelDeleting`
- 新读请求: `ErrChannelDeleting`
- `Deleted` 后: `ErrChannelNotFound`

### in-flight 写请求

删除栅栏生效前已经拿到 offset 的写请求:

- 若最终 committed,返回成功
- 若在 committed 前 channel 已完成 fence / step-down / 回收,返回 `ErrChannelDeleting`

因此 `Deleting` fence 的是新请求,而不是强制回滚已经进入日志的旧请求。

## 协议升级

当前仓库现状:

- `pkg/wkpacket` / `pkg/wkproto` / `pkg/jsonrpc` 的 `messageSeq` 仍为 `uint32`

而本设计要求:

- 规范化 `messageSeq = uint64`

这里必须明确边界:

- `pkg/channelcluster` 核心库的公共 API 以 `uint64 messageSeq` 为准
- wire protocol 升级不是这个库本体的一部分,而是它的 follow-up integration track

也就是说,后续 planning 必须拆成两个实现轨道:

1. `pkg/channelcluster` 核心库与内部调用方
2. `pkg/wkpacket` / `pkg/wkproto` / `pkg/jsonrpc` / access layer 的 wire 升级

这两条轨道有依赖关系,但不是同一个库实现步骤。

### 升级阶段

#### Stage 1: 服务端双栈

所有节点先支持:

- legacy `uint32 messageSeq`
- new `uint64 messageSeq`

但 cluster 默认仍运行在 `LegacyU32`。

#### Stage 2: 能力协商

节点和客户端都声明能力:

```go
type ProtocolCapabilities struct {
    MessageSeqU64 bool
}
```

control-plane 必须能观测:

- 哪些 replica 节点支持 `u64`
- 哪些 gateway 节点支持 `u64`

#### Stage 3: 混合版本集群行为

只要仍存在不支持 `u64` 的承载节点或入口节点:

- `ChannelFeatures.MessageSeqFormat` 必须保持 `LegacyU32`
- 对应 channel 不得启用 `u64`

若 legacy channel 的 committed `messageSeq` 即将超过 `MaxUint32`:

- 必须拒绝继续写入
- 返回 `ErrMessageSeqExhausted`

不能偷偷回绕或截断。

#### Stage 4: 启用 u64

当所有承载节点和目标入口都具备能力后,controller 才能将某个 channel 或整个 cluster 的 `MessageSeqFormat` 提升为 `U64`。

一旦 channel 已切到 `U64`:

- legacy 客户端访问该 channel 必须收到 `ErrProtocolUpgradeRequired`
- 不再提供 `uint32` 降级视图

### wire 升级受影响包

- `pkg/wkpacket`
- `pkg/wkproto`
- `pkg/jsonrpc`
- `internal/usecase/message`
- `internal/access/api`
- `internal/access/gateway`

这些包不属于 `pkg/channelcluster` 核心库实现范围,但属于其后续 integration plan 的依赖集合。

## 错误契约

`pkg/channelcluster` 必须固定一组稳定错误,由 access/gateway/API 做协议层映射。

第一版至少包括:

| Error | 触发条件 | access/gateway/API 动作 |
| --- | --- | --- |
| `ErrNotLeader` | 本地不是当前 leader 或 leader lease 已失效 | 触发 metadata refresh,按入口协议返回 redirect/retry 信号 |
| `ErrStaleMeta` | 请求携带的 `(ChannelEpoch, LeaderEpoch)` 与本地缓存不兼容 | 刷新 metadata 后有限重试,避免无限重试环 |
| `ErrChannelDeleting` | channel 处于 `Deleting` | 直接向调用方返回“channel deleting”,不自动重试 |
| `ErrChannelNotFound` | channel 已 `Deleted` 或不存在 | 直接返回 not found |
| `ErrIdempotencyConflict` | 相同 `clientMsgNo` 命中不同 payload | 直接返回 conflict,不重试 |
| `ErrProtocolUpgradeRequired` | channel 已启用 `U64`,但入口或客户端仍是 legacy | 直接返回 upgrade required |
| `ErrMessageSeqExhausted` | legacy `uint32` channel 即将越界 | 拒绝写入并上报告警 |
| `ErrInvalidFetchArgument` | `Limit <= 0` | 视为 bad request |
| `ErrInvalidFetchBudget` | `MaxBytes <= 0` | 视为 bad request |

约束:

- `pkg/channelcluster` 只定义语义错误,不直接绑定 HTTP status 或某个 RPC code
- 具体协议码映射属于 Workstream B,但不得改写上述错误语义
- access/gateway 的自动重试只能针对 `ErrNotLeader` / `ErrStaleMeta`,其余错误默认不可重试

## Public API

```go
type ChannelKey struct {
    ChannelID   string
    ChannelType uint8
}
```

```go
type Cluster interface {
    ApplyMeta(meta ChannelMeta) error
    Send(ctx context.Context, req SendRequest) (SendResult, error)
    Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
    Status(key ChannelKey) (ChannelRuntimeStatus, error)
}
```

```go
type SendRequest struct {
    ChannelID   string
    ChannelType uint8
    SenderUID   string
    ClientMsgNo string
    Payload     []byte
    ExpectedChannelEpoch uint64
    ExpectedLeaderEpoch  uint64
}
```

```go
type SendResult struct {
    MessageID  uint64
    MessageSeq uint64
}
```

```go
type FetchRequest struct {
    Key         ChannelKey
    FromSeq     uint64
    Limit       int
    MaxBytes    int
    ExpectedChannelEpoch uint64
    ExpectedLeaderEpoch  uint64
}
```

```go
type ChannelMessage struct {
    MessageID   uint64
    MessageSeq  uint64
    SenderUID   string
    ClientMsgNo string
    Payload     []byte
}
```

```go
type FetchResult struct {
    Messages      []ChannelMessage
    NextSeq       uint64
    CommittedSeq  uint64
}
```

读路径契约固定为:

- `FromSeq` 为包含式下界
- `FromSeq == 0` 表示“从当前可读起点开始读”
- `Limit <= 0` 返回 `ErrInvalidFetchArgument`
- `MaxBytes <= 0` 返回 `ErrInvalidFetchBudget`
- 返回按 `MessageSeq` 严格递增排序的 committed 消息
- `CommittedSeq` 为当前最大 committed `messageSeq`
- `NextSeq` 为下一次顺序拉取应携带的起点
- 若 `FromSeq > CommittedSeq`,返回空结果,并保持 `NextSeq = FromSeq`
- 若 channel 处于 `Deleting`,返回 `ErrChannelDeleting`
- 不返回 tombstone 记录给普通读者

若后续引入保留策略导致历史被裁剪,则“当前可读起点”由最早 retained message 决定。

```go
type ChannelRuntimeStatus struct {
    Key          ChannelKey
    Status       ChannelStatus
    Leader       NodeID
    LeaderEpoch  uint64
    HW           uint64
    CommittedSeq uint64
}
```

## 与 `internal/usecase/channelmeta` 的关系

`pkg/channelcluster` 只消费 metadata,不产生命令裁决。

例如:

- create/delete channel
- elect leader
- update ISR
- reassignment

这些仍属于:

```text
internal/usecase/channelmeta
```

## 实现拆分顺序

为避免把“核心库”和“接入层/协议升级”混成一个大任务,planning 时必须拆成至少两个 workstream:

### Workstream A: `pkg/channelcluster` 核心库

包含:

- `channel -> ISR group` 绑定
- `messageSeq = committed offset + 1`
- 幂等索引
- 删除态
- `Send`/append 路径:
  - request token 校验
  - `MessageID` 分配
  - leader append
  - commit 回调转 `messageSeq`
  - 幂等命中/冲突处理
- `Fetch/Status` 运行时 API
- 稳定错误集合与内部 `uint64` API

不包含:

- `pkg/wkpacket`
- `pkg/wkproto`
- `pkg/jsonrpc`
- HTTP / RPC 返回格式改造

进入条件:

- `pkg/isr` 与 `pkg/multiisr` 已提供可用的 append/fetch/status 基础能力
- `ChannelMeta` / `ChannelKey -> GroupKey` 推导 / epoch 契约已固定

完成条件:

- `Send` / `Fetch` / `Status` 三个核心 API 全部可运行
- `messageSeq = committed offset + 1` 在写路径与读路径都被测试锁定
- 幂等、删除态、stale-meta、not-leader 错误语义固定
- 对上只暴露 `uint64 messageSeq`

### Workstream B: 接入层与协议升级

包含:

- `uint32 -> uint64 messageSeq` wire 升级
- gateway / API / JSON-RPC 适配
- legacy compatibility 与 `ErrProtocolUpgradeRequired`

依赖:

- Workstream A 已提供稳定的内部 `uint64` API

进入条件:

- Workstream A 完成
- 受影响的 wire/types 包清单已冻结
- mixed-version capability 协商方案已确定

完成条件:

- `pkg/wkpacket` / `pkg/wkproto` / `pkg/jsonrpc` 完成 wire 升级
- gateway/API 能正确映射 `ErrProtocolUpgradeRequired` 等错误
- mixed cluster 与 legacy client 行为有验证用例
- `LegacyU32 -> U64` 切换具备明确入口与回滚边界

这样 planning 时才能既保持“一个库一个 spec”,又不低估联调与协议升级成本。

## 测试重点

至少覆盖:

1. `messageSeq = committed offset + 1`
2. 幂等重试返回同一 `messageSeq`
3. 切主后幂等索引仍正确恢复
4. `Deleting` 阶段新读写统一被 fence
5. in-flight 写请求在删除阶段的完成/失败语义
6. `uint32 -> uint64` 升级顺序与 mixed cluster 行为
