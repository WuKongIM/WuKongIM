# L2 · Slot 元数据层

> 一致性算法：**MultiRaft**（基于 etcd/raft v3，N 个 Raft Slot 复用同一进程）
> 职责：存储和管理系统元数据——频道信息、ISR 分布、订阅关系、用户数据等

## 1. 概述

Slot 层是系统的"元数据中枢"。它先把任意 Key 稳定映射到固定数量的 **HashSlot**，再通过 **HashSlotTable** 查到当前承载该逻辑分片的物理 Raft Slot，在保证强一致的同时支持物理 Slot 的动态扩缩容。

**核心价值**：
- 将元数据按 Key 哈希到固定数量的 HashSlot 中，保证逻辑寻址稳定
- 通过 HashSlotTable 将 HashSlot 映射到动态物理 Slot，实现在线迁移与再均衡
- 所有 Slot 在同一个 MultiRaft Runtime 中运行，共享 Tick 和调度，降低资源消耗
- Controller 层负责管理 Slot 的副本分布，Slot 层本身只关注数据一致性

## 2. 代码结构

```text
pkg/
├── slot/
│   ├── multiraft/             # MultiRaft 核心引擎
│   │   ├── slot.go            # 单个物理 Slot 的 Raft 实现
│   │   ├── runtime.go         # 多 Slot 调度器
│   │   ├── api.go             # 对外 API
│   │   ├── types.go           # 类型定义
│   │   └── scheduler.go       # Worker 调度
│   ├── meta/                  # 元数据 KV / snapshot / codec
│   ├── fsm/                   # Slot 状态机与迁移命令
│   └── proxy/                 # 面向业务层的元数据访问代理
│
└── cluster/
    ├── api.go                 # 对上层暴露的 cluster.API
    ├── cluster.go             # 组合根与运行时生命周期
    ├── hashslottable.go       # HashSlotTable 与迁移状态
    ├── router.go              # Key → HashSlot → SlotID 路由
    ├── hashslot_migration.go  # HashSlot 迁移观察与调度
    ├── managed_slots.go       # Controller 管理的 Slot 生命周期
    ├── slot_handler.go        # managed-slot RPC 服务端
    ├── slot_manager.go        # managed-slot 本地协调
    ├── slot_executor.go       # reconcile 执行器
    ├── controller_host.go     # 本地 controller raft/meta 启动
    ├── controller_client.go   # controller RPC 客户端
    ├── controller_handler.go  # controller RPC 服务端
    ├── transport_glue.go      # 共享 transport/server/pool 装配
    ├── transport.go           # Raft transport 适配
    ├── forward.go             # 非 Leader 节点请求转发
    ├── codec.go               # 通用消息编解码
    ├── codec_control.go       # controller RPC 二进制编解码
    ├── codec_managed.go       # managed-slot RPC 二进制编解码
    ├── observer.go            # observation loop
    ├── retry.go               # 统一重试策略
    └── runtime_state.go       # 本地 slot 运行态快照
```

## 3. MultiRaft Runtime

### 3.1 核心结构

```go
// runtime.go
type Runtime struct {
    opts      Options
    mu        sync.RWMutex
    slots     map[SlotID]*slot      // 所有活跃的 Slot
    scheduler *scheduler           // 工作调度器
    stopCh    chan struct{}
}
```

Runtime 是 MultiRaft 的入口，管理当前节点上的所有 Raft Slot。一个集群进程中只有一个 Runtime 实例。

### 3.2 配置参数

| 参数            | 类型           | 默认值 | 说明                       | 代码位置     |
| --------------- | -------------- | ------ | -------------------------- | ------------ |
| NodeID          | uint64         | 必填   | 当前节点 ID                | `types.go`   |
| TickInterval    | time.Duration  | 100ms  | Raft Tick 周期             | `types.go`   |
| Workers         | int            | 2      | Worker 协程数              | `types.go`   |
| ElectionTick    | int            | 10     | 选举超时（Tick 倍数）      | `types.go`   |
| HeartbeatTick   | int            | 1      | 心跳间隔（Tick 倍数）      | `types.go`   |
| PreVote         | bool           | true   | 防止分区扰动               | `types.go`   |
| CheckQuorum     | bool           | -      | Leader 在心跳中验证多数    | `types.go`   |
| MaxSizePerMsg   | uint64         | -      | 单条消息最大大小           | `types.go`   |
| MaxInflight     | int            | -      | 最大 inflight 消息数       | `types.go`   |

### 3.3 公开 API

```go
// api.go
func (r *Runtime) OpenSlot(slotID, storage, stateMachine) error        // 打开已有 Slot
func (r *Runtime) BootstrapSlot(slotID, voters, storage, sm) error     // 初始化新 Slot
func (r *Runtime) CloseSlot(slotID) error                               // 关闭 Slot
func (r *Runtime) Step(slotID, message) error                           // 处理 Raft 消息
func (r *Runtime) Propose(ctx, slotID, data) (Result, error)            // 提交 Proposal
func (r *Runtime) ChangeConfig(ctx, slotID, change) (Result, error)     // 修改成员配置
func (r *Runtime) TransferLeadership(slotID, targetID) error            // 转移 Leadership
func (r *Runtime) Status(slotID) (Status, error)                        // 查询 Slot 状态
func (r *Runtime) Slots() []SlotID                                       // 列出所有 Slot
```

## 4. 单个 Raft Slot 实现

### 4.1 Slot 结构

```go
// slot.go
type slot struct {
    id                 SlotID
    storage            Storage           // 日志持久化
    stateMachine       StateMachine      // 业务状态机
    rawNode            *raft.RawNode     // etcd raft 引擎
    submittedProposals []*future         // 待响应的 Proposal
    submittedConfigs   []*future         // 待响应的配置变更
    pendingProposals   map[uint64]trackedFuture
    pendingConfigs     map[uint64]trackedFuture
    transportBuf       []Envelope        // 待发送的 Raft 消息
    tickPending        bool              // 是否有未处理的 Tick
}
```

### 4.2 请求处理流程（Proposal）

```
1. Propose(data)
     ↓
2. group.enqueueControl(controlPropose)
     ↓
3. processControls()                      // slot.go:160-193
     → rawNode.Propose(data)              // 提交给 etcd/raft
     ↓
4. 等待 Raft 复制到多数节点
     ↓
5. processReady()                         // slot.go:273-327
     → storage.Save(entries, hardState)   // 持久化
     → 应用 committed entries → StateMachine.Apply()
     → 发送 Raft messages 给 peers
     ↓
6. future.resolve(result)                 // 返回给调用方
```

### 4.3 配置变更

支持的变更类型：

| 操作            | 说明                                  |
| --------------- | ------------------------------------- |
| AddVoter        | 添加投票成员                          |
| RemoveVoter     | 移除投票成员                          |
| AddLearner      | 添加 Learner（只同步日志，不投票）    |
| PromoteLearner  | 将 Learner 提升为投票成员             |

配置变更同样通过 Raft 共识，保证所有节点看到一致的成员视图。

### 4.4 批量应用

Slot 支持批量应用连续的 normal entries 以提升吞吐：

```go
// slot.go:329-457
// 连续的普通 entry 会被打包成一批
// 调用 StateMachine.ApplyBatch() 一次性应用
// 配置变更 entry 单独处理
```

如果 StateMachine 实现了 `BatchStateMachine` 接口，则使用 `ApplyBatch()`；否则逐条 `Apply()`。

## 5. Key → HashSlot → Slot 路由

### 5.1 路由算法

```go
func HashSlotForKey(key string, hashSlotCount uint16) uint16 {
    return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}

func (r *Router) SlotForKey(key string) SlotID {
    hashSlot := r.HashSlotForKey(key)
    return r.hashSlotTable.Load().Lookup(hashSlot)
}
```

- 第一步使用 CRC32 将任意 Key 映射到固定范围 `[0, HashSlotCount)` 的 HashSlot。
- 第二步通过 Controller 持久化并下发的 `HashSlotTable` 查到当前 physical Slot。
- `HashSlotCount` 保持稳定，physical Slot 的数量与承载关系可以动态变化。

### 5.2 Leader 查询

```go
// router.go
func (r *Router) LeaderOf(slotID SlotID) (NodeID, error) {
    status, err := r.runtime.Status(slotID)
    if status.LeaderID == 0 {
        return 0, ErrNoLeader
    }
    return status.LeaderID, nil
}
```

如果当前节点不是目标 physical Slot 的 Leader，请求会通过 Forward RPC 转发到 Leader 节点。Controller 更新 `HashSlotTable` 后，所有节点会在 assignment 刷新或 heartbeat 下发时同步新表并刷新本地路由。

## 6. 元数据管理

Slot 层存储的系统元数据包括但不限于：

### 6.1 频道元数据（ChannelMeta）

```go
// channellog/types.go:39-50
type ChannelMeta struct {
    ChannelID    string           // 频道唯一标识
    ChannelType  uint8            // 频道类型（群聊/单聊/...）
    ChannelEpoch uint64           // 频道配置版本号
    LeaderEpoch  uint64           // Leader 版本号
    Replicas     []NodeID         // 所有副本节点
    ISR          []NodeID         // 同步副本集合
    Leader       NodeID           // 当前 Leader
    MinISR       int              // 最小同步副本数
    Status       ChannelStatus    // Creating / Active / Deleting / Deleted
    Features     ChannelFeatures  // 特性标记
}
```

### 6.2 频道运行时状态

```go
// channellog/types.go
type ChannelRuntimeStatus struct {
    Key          ChannelKey
    Status       ChannelStatus
    Leader       NodeID
    LeaderEpoch  uint64
    HW           uint64           // 已提交的 offset
    CommittedSeq uint64           // 已提交的消息序号
}
```

### 6.3 其他元数据

- **订阅者关系**：频道的成员列表和订阅状态
- **用户信息**：用户基本信息、在线状态、设备信息
- **路由表**：消息投递路由映射

> 所有元数据通过 Slot 层的 StateMachine 实现持久化和一致性保证。具体的 StateMachine 由业务层注入（`NewStateMachine` 工厂函数）。

## 7. Managed Slots 生命周期

Controller 通过 Assignment 驱动 physical Slot 的打开与关闭，同时通过 HashSlotTable 驱动逻辑分片迁移：

```
Controller 分配 Assignment（DesiredPeers = [1, 2, 3]）
    ↓
Node Agent.ApplyAssignments()                 // agent.go
    ↓
对比本地 Runtime.Slots() 与 Assignment
    ├─ 本地缺少该 Slot → Runtime.OpenSlot() 或 Bootstrap
    └─ 本地多出该 Slot → Runtime.CloseSlot()
```

### 7.1 HashSlot 迁移生命周期

```
Controller StartMigration / AddSlot / RemoveSlot / Rebalance
    ↓
持久化 HashSlotTable（migration phase = Snapshot / Delta / Switching）
    ↓
Node observeHashSlotMigrations()
    ├─ Snapshot：源 Slot 导出单个 hash slot 快照，目标 Slot 导入
    ├─ Delta：源 Slot 对该 hash slot 开启增量双写
    ├─ Switching：Controller 原子切表，路由开始指向目标 Slot
    └─ Done / Abort：清理 worker 状态与双写运行时
```

### 7.2 修复任务执行（managed_slots.go）

```
AddLearner(targetNode)
    ↓
waitForCatchUp(targetNode)
    → 轮询直到 target.AppliedIndex >= leader.CommitIndex
    ↓
Promote(targetNode → Voter)
    ↓
waitForCatchUp(again)
    ↓
TransferLeader(如果源节点是 Leader)
    ↓
RemoveVoter(sourceNode)
    ↓
ReportTaskResult(success)
```

### 7.2 配置变更重试

配置变更通过 `changeSlotConfig()` 执行，但重试逻辑已经收敛到共享 `Retry` helper：由 `Interval`、`MaxWait` 和 `IsRetryable` 控制重试节拍、总预算和可重试错误，而不是固定 3 次指数退避。如果目标节点不是 Leader，则仍会通过 RPC 转发到 Leader 节点执行。

## 8. 跨层通信

### 8.1 Transport 注册

```go
// transport_glue.go
server.Handle(msgTypeRaft, handleRaftMessage)                // msgType=1, Raft 消息
rpcMux.Handle(rpcServiceForward, handleForwardRPC)           // serviceID=1, 请求转发
rpcMux.Handle(rpcServiceController, handleControllerRPC)     // serviceID=10, controller RPC
rpcMux.Handle(rpcServiceManagedSlot, handleManagedSlotRPC)   // serviceID=20, Slot 管理
```

### 8.2 Raft 消息传输

```go
// transport.go
type raftTransport struct {
    client *nodetransport.Client
}

// 消息格式：[slotID:8 bytes][protobuf raft message:N bytes]
func (t *raftTransport) Send(ctx context.Context, batch []Envelope) error {
    for _, env := range batch {
        body := encodeRaftBody(env.SlotID, env.Message.Marshal())
        t.client.Send(env.Message.To, env.SlotID, msgTypeRaft, body)
    }
    return nil
}
```

以 SlotID 为 shard key，确保同一 Slot 的消息走固定连接，保持有序。

### 8.3 请求转发

当客户端请求到达非 Leader 节点时：

```
Non-Leader 节点
    ↓
forwardToLeader(leaderID, slotID, cmd)       // forward.go
    ↓
RPCService(leaderID, slotID, rpcServiceForward, payload)
    ↓
Leader 节点
    ↓
handleForwardRPC → runtime.Propose(slotID, cmd)
    ↓
Raft 共识 → 返回结果
    ↓
原始节点返回给客户端
```

## 9. Interface 合约

### 9.1 Storage 接口

每个 Slot 需要实现以下存储接口：

```go
// multiraft/types.go:70-80
type Storage interface {
    InitialState(ctx) (BootstrapState, error)  // 启动时恢复状态
    Entries(ctx, lo, hi, maxSize) ([]Entry, error)  // 读取日志条目
    Term(ctx, index) (uint64, error)           // 查询某条日志的 Term
    FirstIndex(ctx) (uint64, error)            // 第一条日志索引
    LastIndex(ctx) (uint64, error)             // 最后一条日志索引
    Snapshot(ctx) (Snapshot, error)            // 获取快照
    Save(ctx, PersistentState) error           // 持久化
    MarkApplied(ctx, index) error              // 标记已应用
}
```

### 9.2 StateMachine 接口

```go
// multiraft/types.go:94-106
type StateMachine interface {
    Apply(ctx, Command) ([]byte, error)        // 应用单条命令
    Restore(ctx, Snapshot) error               // 从快照恢复
    Snapshot(ctx) (Snapshot, error)            // 创建快照
}

// 可选的批量优化
type BatchStateMachine interface {
    StateMachine
    ApplyBatch(ctx, []Command) ([][]byte, error)  // 批量应用
}
```

### 9.3 Transport 接口

```go
// multiraft/types.go:66-68
type Transport interface {
    Send(ctx, []Envelope) error  // 批量发送 Raft 消息
}
```

## 10. 工作原理图

```
                          ┌─────────────────────────────────┐
                          │          Runtime                 │
                          │   ┌─────────────────────────┐   │
 Propose / ChangeConfig   │   │     scheduler            │   │
 ─────────────────────▸   │   │   ┌─────┬─────┬─────┐   │   │
                          │   │   │ W1  │ W2  │ ... │   │   │  Workers 并行处理
                          │   │   └──┬──┴──┬──┴─────┘   │   │  不同 Slot
                          │   └─────┼─────┼─────────────┘   │
                          │         ▼     ▼                  │
                          │   ┌─────────────────────────┐   │
                          │   │   slots map             │   │
                          │   │  ┌───────┐ ┌───────┐    │   │
                          │   │  │ G-1   │ │ G-2   │ ...│   │  每个 Slot 独立
                          │   │  │rawNode│ │rawNode│    │   │  的 Raft 状态机
                          │   │  └───────┘ └───────┘    │   │
                          │   └─────────────────────────┘   │
                          └─────────────────────────────────┘
                                        │
                                        │ Raft Messages
                                        ▼
                          ┌─────────────────────────────────┐
                          │      Transport (nodetransport)  │
                          │   msgType=1, shard by SlotID   │
                          └─────────────────────────────────┘
```

## 11. 性能特性

| 维度         | 特征                                                          |
| ------------ | ------------------------------------------------------------- |
| 扩展方式     | 保持 HashSlotCount 稳定，通过增减 physical Slot 和迁移做扩缩容 |
| 吞吐瓶颈     | 单 Slot 受限于 Raft 的写入 TPS（通常数千/秒）               |
| 内存占用     | 每个 Slot 一个 rawNode，内存随 Slot 数线性增长             |
| 日志压缩     | 支持 Snapshot 机制，定期快照后截断旧日志                     |
| 批量优化     | 连续 entry 可批量应用（BatchStateMachine）                   |
| 连接复用     | 所有 Slot 共享 Transport 连接池                              |
