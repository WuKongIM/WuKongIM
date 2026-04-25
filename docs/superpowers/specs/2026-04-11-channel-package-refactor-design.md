# pkg/channel 重构设计

## 背景

`pkg/channel` 当前同时承载了单 Channel ISR 状态机、消息日志存储、节点内多 Channel 运行时协调以及业务入口编排，但边界已经被历史演进打散：

- `isr`、`log`、`node` 三个包重复定义同一领域的核心类型，出现了同名不同义的 `ChannelKey`
- `log` 包内混杂了 store、handler、类型转换与 bridge 代码，导致 `log/isr_bridge.go` 和 `log/channelkey.go` 这类胶水长期存在
- `node/runtime.go` 同时管理 CRUD、复制重试、调度、背压、peer 请求、snapshot 限流与 tombstone，已经演变成职责过重的运行时大对象
- Append/Fetch/Channel 查询热路径有不必要的锁竞争与分配开销，且现有 FIFO scheduler 无法保障关键路径优先级

本次重构以“高性能 + 设计优雅”为唯一目标，不保留旧结构兼容层。项目未上线，因此允许直接删除旧语义与过渡代码。

## 目标

1. 以根包 `pkg/channel` 统一定义所有公共语义，彻底消除重复类型与桥接层。
2. 将现有 `isr/log/node` 重建为 `replica/store/runtime/handler` 四层职责清晰的结构。
3. 明确业务标识与运行时标识的边界：业务层使用 `ChannelID`，运行时层使用 `ChannelKey`。
4. 让 Append/Fetch/Channel 查询等热路径落在正确的数据结构上，减少锁争用、无效拷贝和短生命周期对象分配。
5. 让 `pkg/channel` 对外仍保持单一组合入口，供 `internal/app` 与 usecase 层稳定接入。

## 非目标

- 不保留 `pkg/channel/isr`、`pkg/channel/log`、`pkg/channel/node` 旧 import 路径兼容层
- 不为了迁移平滑保留 `isr_bridge`、struct 版 `ChannelKey` 或双 `Runtime`/`ChannelHandle` 接口
- 不引入“每个 Channel 一个 goroutine”的重型运行时模型
- 不在本次内改变控制面元数据来源与集群语义；单节点部署仍然视作“单节点集群”

## 最终包结构

```text
pkg/channel/
├── channel.go
├── types.go
├── errors.go
├── doc.go
│
├── replica/
│   ├── replica.go
│   ├── append.go
│   ├── fetch.go
│   ├── progress.go
│   ├── replication.go
│   ├── recovery.go
│   ├── meta.go
│   ├── history.go
│   ├── pool.go
│   └── types.go
│
├── store/
│   ├── engine.go
│   ├── channel_store.go
│   ├── logstore.go
│   ├── checkpoint.go
│   ├── history.go
│   ├── idempotency.go
│   ├── commit.go
│   ├── snapshot.go
│   ├── keys.go
│   └── codec.go
│
├── runtime/
│   ├── runtime.go
│   ├── channel.go
│   ├── replicator.go
│   ├── scheduler.go
│   ├── backpressure.go
│   ├── session.go
│   ├── tombstone.go
│   ├── snapshot.go
│   └── types.go
│
├── handler/
│   ├── append.go
│   ├── fetch.go
│   ├── meta.go
│   ├── codec.go
│   ├── seq_read.go
│   ├── apply.go
│   └── key.go
│
└── transport/
    ├── transport.go
    ├── session.go
    └── codec.go
```

## 分层职责

### 根包 `pkg/channel`

根包只保留稳定公共语义和组合入口：

- 对外 `Cluster` 接口与 `New()` 构造函数
- 唯一公共类型：`NodeID`、`ChannelKey`、`ChannelID`、`Meta`、`ReplicaState`、`Record`、`Checkpoint`、`EpochPoint`、`CommitResult`
- 唯一公共错误：统一前缀 `channel:`

根包不承载任何具体存储实现、复制细节、消息编解码逻辑或调度策略。

### `handler`

`handler` 是业务入口层，负责：

- 将上层 `ChannelID` 转换为运行时 `ChannelKey`
- Append/Fetch 参数校验与错误翻译
- 消息编解码、序号兼容、元数据状态检查
- 调用 `runtime` 与 `store` 完成业务编排

`handler` 可以理解 `Status`、`Features`、`MessageSeqFormat` 等业务语义，但不直接参与 Replica 内部状态机。

### `replica`

`replica` 只负责单个 Channel 的 ISR 复制状态机：

- Leader Append / Follower Fetch / ApplyFetch / ProgressAck
- HW 推进、epoch history、状态恢复
- 租约、角色、offset/epoch 状态维护

`replica` 只接受统一的根包类型，不再定义自己的 `ChannelMeta`、`ChannelKey`、`Record` 等公共领域类型。

### `store`

`store` 只负责本地持久化能力：

- Pebble engine 生命周期
- Channel log 读写与 checkpoint/history/snapshot 持久化
- 幂等性状态存储
- Commit coordinator

`store.ChannelStore` 直接实现 `replica` 需要的存储接口，不再保留 bridge 层。

### `runtime`

`runtime` 是本节点多 Channel 的协调层，负责：

- Channel CRUD 与 generation 管理
- 本地 Channel 注册与查询
- scheduler、replicator、peer session、背压与 snapshot 并发控制
- tombstone 生命周期管理

`runtime` 不理解业务消息字段，只围绕 `ChannelKey`、`Meta`、`Record`、Envelope 等运行时对象工作。

### `transport`

`transport` 只做节点间传输与 envelope 编解码，不承载业务编排逻辑。

## 统一类型设计

### 标识模型

根包提供两类标识：

- `ChannelID struct{ ID string; Type uint8 }`
  - 业务侧标识
  - 由 handler 和上层 usecase 使用
- `ChannelKey string`
  - 运行时唯一标识
  - 格式固定为 `channel/{type}/{base64_id}`
  - 由 `handler/key.go` 从 `ChannelID` 派生
  - 供 `replica/runtime/store/transport` 使用

这样可以彻底消除旧 `log.ChannelKey`（struct）与 `isr.ChannelKey`（string）同名不同义的问题。

### 元数据模型

统一的 `Meta` 是唯一权威元数据：

- 复制层依赖：`Key`、`Epoch`、`LeaderEpoch`、`Leader`、`Replicas`、`ISR`、`MinISR`、`LeaseUntil`
- 业务层额外依赖：`ID`、`Status`、`Features`

复制层不再持有自己的 `ChannelMeta` 副本定义；业务层不再定义平行的 `ChannelMeta`。

### 统一错误体系

所有错误放到根包 `errors.go`，统一使用 `channel:` 前缀，覆盖三类错误域：

- 共识/Replica 错误，如 `ErrNotLeader`、`ErrLeaseExpired`、`ErrInsufficientISR`
- Runtime 错误，如 `ErrChannelExists`、`ErrChannelNotFound`、`ErrBackpressured`
- Handler/业务错误，如 `ErrStaleMeta`、`ErrChannelDeleting`、`ErrInvalidArgument`

## 接口边界设计

### 顶层组合入口

根包定义顶层接口：

- `type Cluster interface`
  - `ApplyMeta(meta Meta) error`
  - `Append(ctx context.Context, req AppendRequest) (AppendResult, error)`
  - `Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)`
  - `Status(id ChannelID) (ChannelRuntimeStatus, error)` 或等价面向业务的状态查询

`channel.New()` 负责组装 `handler + runtime + store + transport`，是唯一组合根。

### `runtime` 暴露的最小能力

`runtime` 对 `handler` 暴露最小 Channel 运行时接口：

- `Channel(key ChannelKey) (ChannelHandle, bool)`
- `EnsureChannel(meta Meta) error`
- `RemoveChannel(key ChannelKey) error`
- `ApplyMeta(meta Meta) error`

统一 `ChannelHandle`：

- `ID() ChannelKey`
- `Meta() Meta`
- `Status() ReplicaState`
- `Append(ctx context.Context, records []Record) (CommitResult, error)`

### `store` 直接满足 `replica`

`store.ChannelStore` 直接实现 `replica` 所需接口：

- `LogStore`
- `CheckpointStore`
- `EpochHistoryStore`
- `ApplyFetchStore`
- `SnapshotApplier` 或等价 snapshot 接口

因此删除：

- `pkg/channel/log/isr_bridge.go`
- `pkg/channel/log/channelkey.go`

## 并发模型

### 总体模型

采用“单 Channel 局部串行 + 全局分片并发 + 热路径原子快照”：

- `runtime` 用固定分片 map 管理 Channel，按 key 查询只走 shard `RLock`
- tombstone 清理由独立组件异步完成，不再污染查询路径
- 单个 Channel 的局部状态与任务由 `runtime/channel.go` 维护
- `replica` 内部通过常驻 append collector 与局部互斥保持顺序性
- `Status()`、`Meta()` 等高频读通过 atomic 快照完成

### `runtime` 设计

`runtime` 不再维持单个全局 `RWMutex + groups map`，而改为分片结构：

- 固定数量 shard，例如 64
- 每个 shard 独立 `RWMutex + map[ChannelKey]*channel`
- `Channel(key)` 无副作用，仅查找并返回 handle

遍历型操作不是当前主要场景，因此分片锁的复杂度是可接受的，换来高并发下更稳定的查询性能。

### `runtime/channel.go`

单 Channel 本地结构由旧 `group` 重命名为 `channel`，不再使用 5 个闭包回调：

- 使用显式 `ChannelDelegate` 接口连接 `runtime` 与 `channel`
- `Meta` 通过 `atomic.Pointer[Meta]` 提供无锁读取
- taskMask 迁移为 `atomic.Uint32` 或极轻量互斥，仅保护任务位图与局部队列
- `replicationPeers` 与 snapshot 字节统计保留在局部对象，不再散落在 runtime 大对象中

### tombstone

tombstone 从查询路径中拆出，形成独立管理器：

- 独立 goroutine 周期性清理过期 tombstone
- `Channel()` 和 `EnsureChannel()` 不再因为 tombstone 清理被迫使用写锁
- `RemoveChannel()` 只负责插入 tombstone 并触发状态变更

## 性能设计

### Append 热路径

`replica.Append()` 的目标是显著降低锁竞争和分配：

- `ReplicaState` 改为 `atomic.Pointer[ReplicaState]`，`Status()` 零锁读取
- Append request/waiter 通过 `sync.Pool` 复用
- 常驻 append collector goroutine 持续 drain 队列，消除“collector 退出后等下一次 append 再拉起”的小 batch 问题
- 合并批次的 `[]Record` buffer 复用，减少高频临时切片分配

### HW 推进

ISR 通常规模较小，使用“小数组 + 插入排序”替代 `make + slices.Sort`：

- 固定数组承载 match offsets
- 对 `count <= 8` 的场景使用插入排序
- 避免每次 `advanceHW` 都产生新 slice 和排序分配

### Fetch 读路径

Pebble iterator 的 value 必须在 `Next()` 前拷贝，因此零拷贝不可行，但可以减少 GC 对象数：

- 为单次 Fetch 引入 arena allocator
- 一次扫描共享一块连续内存
- handler 解码后再拷贝到消息结构体，保证 arena 生命周期安全

### Timer 与重试

Follower replication retry 采用可复用 timer 或时间轮：

- 常规场景优先 `sync.Pool + time.Timer`
- 大量 peer 重试时可替换为时间轮实现

### 调度优先级

Scheduler 从单 FIFO 队列改为多优先级队列：

- `high`：ack、truncate、复制完成通知
- `normal`：常规 replication、commit 推进
- `low`：snapshot、cleanup

保证关键路径不会被背景任务拖慢，同时保持单 Channel 在任一时刻最多一个处理单元在执行。

## 需要删除的旧结构

本次重构完成后，以下结构必须删除而不是保留兼容：

- `pkg/channel/isr`
- `pkg/channel/log`
- `pkg/channel/node`
- `pkg/channel/log/isr_bridge.go`
- `pkg/channel/log/channelkey.go`
- 旧 `log.ChannelKey` struct 语义
- 重复的 `ChannelMeta`、`ChannelHandle`、`Runtime` 定义

## 外部影响面

包结构切换后，下列引用方需要一起调整 import 和类型名：

- `internal/app/build.go`
- `internal/app/channelmeta.go`
- `internal/usecase/message/...`
- `internal/usecase/conversation/...`
- `internal/access/node/...`
- 任何直接引用 `pkg/channel/isr`、`pkg/channel/log`、`pkg/channel/node` 的代码

所有外部调用方应遵守新的语义边界：

- 业务侧使用 `ChannelID`
- 运行时/复制侧使用 `ChannelKey`

## 测试策略

### 测试迁移原则

- 保留原有测试资产，但按新包边界归类
- 删除只验证 bridge/adapter 的过渡性测试
- 为新的关键并发与性能结构补充测试

### 测试重归类

- `isr/*_test.go` 迁入 `replica/*_test.go`
- 存储相关 `log/*store*_test.go`、`commit_coordinator_test.go` 迁入 `store/*_test.go`
- 业务入口相关 `send/fetch/meta/apply/seq_read` 测试迁入 `handler/*_test.go`
- `node/*_test.go` 迁入 `runtime/*_test.go`
- `transport/*_test.go` 保持在 `transport`

### 直接删除的旧测试

- `log/channelkey_test.go`
- `log/isr_bridge_test.go`
- 其他只验证旧桥接/旧命名适配的测试

### 新增测试

- `pkg/channel/types_test.go`：`ChannelID -> ChannelKey` 稳定性
- `pkg/channel/replica/append_collector_test.go`：collector 持续 drain、取消与关闭语义
- `pkg/channel/replica/progress_test.go`：HW 推进正确性与小 ISR 行为
- `pkg/channel/runtime/tombstone_test.go`：异步清理与查询路径无副作用
- `pkg/channel/runtime/scheduler_test.go`：优先级顺序与同 key 单执行语义
- `pkg/channel/store/channel_store_test.go`：store 直接实现 replica 接口
- `pkg/channel/api_test.go`：顶层装配和基础用法

### 验证命令

开发阶段优先运行与改动相关的单元测试：

- `go test ./pkg/channel/...`
- `go test ./internal/... ./pkg/...`

跨层装配改动完成后，再补充运行受影响模块的更大范围测试。集成测试保持按项目约定单独执行，不在日常重构迭代中默认运行。

## 实施顺序

### 阶段 1：结构与语义重建

1. 建立根包统一类型与错误
2. 创建 `store/replica/runtime/handler` 新目录
3. 将旧代码迁入新目录并立刻切到统一类型
4. 删除 `isr_bridge` 与 `channelkey` 胶水
5. 调整外部引用，确保 `go test ./pkg/channel/...` 通过

阶段 1 的目标不是零改动搬迁，而是在新目录下直接得到正确边界。

### 阶段 2：性能重构

1. 为 `runtime/channel` 和 `replica/state` 接入 atomic 快照
2. 将 append collector 改为常驻 goroutine
3. 引入 waiter/request/buffer 池
4. 把 tombstone 清理从查询路径拆出
5. 将 runtime map 改为分片锁结构
6. 优化 HW 推进、Fetch arena 和 timer 复用

### 阶段 3：设计收口

1. 去掉闭包回调，改为 delegate 接口
2. 重写 scheduler 为优先级模型
3. 拆分 `runtime.go` 为 runtime/replicator/backpressure 等文件
4. 清理历史命名、测试与文档引用

## 风险与约束

### 大范围 import 变更

这是一次多包重构，编译错误会在多个层面同时暴露。应当坚持边迁边编译、边迁边跑局部测试，而不是所有文件改完再统一收敛。

### 命名冲突

`runtime` 包内使用 `type channel struct` 时，应给根包起别名，例如：

```go
import core "github.com/WuKongIM/WuKongIM/pkg/channel"
```

避免 `channel.Meta` 与局部 `channel` struct 名称冲突。

### atomic 快照前提

`Meta` 和 `ReplicaState` 作为值对象写入新指针，不能包含共享可变状态。切片字段如果需要长期只读，应在写入前拷贝，避免并发共享底层数组。

### goroutine 生命周期

append collector、tombstone cleaner、retry timer/timer wheel 都必须有明确的关闭路径，`RemoveChannel()` 和 runtime shutdown 时必须停止后台任务，避免泄漏。

## 预期结果

重构完成后，`pkg/channel` 应具备以下特征：

- 共享领域语义只定义一次
- 没有 `bridge` 或类型转换胶水层
- `runtime.go` 不再承担多类职责
- Append/Fetch/Channel 查询热路径的锁与分配显著减少
- scheduler 能区分关键路径和背景任务
- 外部调用方的接入点更清晰：业务侧经 `ChannelID`，运行时侧经 `ChannelKey`

最终交付不是“旧包换新目录”，而是一个边界清晰、性能友好、能继续承载后续演进的全新 `pkg/channel`。
