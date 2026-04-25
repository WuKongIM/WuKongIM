# pkg/slot 流程文档

## 1. 职责定位

基于 Multi-Raft 的分布式元数据存储层。集群元数据按 Slot 分片到多个独立 Raft Group 中管理，每个 Slot 负责一部分键空间（用户、频道、订阅者、会话状态等）。
**不负责**: 消息日志存储（由 channel 负责）、Slot 的副本分配决策（由 controller 负责）。

## 2. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `multiraft/` | `multiraft.New()` → `Runtime` | Multi-Raft 运行时：Worker 池 + Ticker + Scheduler，管理多个独立 Raft Group |
| `fsm/` | `fsm.NewStateMachine()` | 状态机：TLV 命令解码 + BatchStateMachine 批量应用（均摊 fsync） |
| `meta/` | `meta.Open()` → `DB` | Pebble 元数据库：7 张业务表 + 3 个键空间 + WriteBatch + 快照 |
| `proxy/` | `proxy.New()` → `Store` | 分布式代理：写入路由到 Propose，读取路由到 Leader 的权威 RPC |

## 3. 对外接口

```go
// proxy/store.go — 业务层唯一入口
Store.CreateChannel / UpdateChannel / DeleteChannel
Store.AddChannelSubscribers / RemoveChannelSubscribers / ListChannelSubscribers
Store.UpsertChannelRuntimeMeta / GetChannelRuntimeMeta / ListChannelRuntimeMeta / ScanChannelRuntimeMetaSlotPage
Store.CreateUser / UpsertUser / GetUser
Store.UpsertDevice / GetDevice
Store.RegisterChannelUpdateOverlay(overlay)  // 注册热路径覆盖层

// multiraft/api.go — Raft Runtime 底层 API
Runtime.OpenSlot / BootstrapSlot / CloseSlot
Runtime.Step(ctx, Envelope)                  // 接收远端 Raft 消息
Runtime.Propose(ctx, slotID, data) → Future  // 提交提案
Runtime.ChangeConfig / TransferLeadership / Status
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `SlotID` / `NodeID` | multiraft/types.go | Slot / 节点标识 |
| `Command` | multiraft/types.go | 状态机命令：SlotID(物理 Raft 组), HashSlot(逻辑哈希分片), Index, Term, Data(TLV编码) |
| `Future` / `Result` | multiraft/types.go | 异步提案结果，Wait(ctx) 阻塞到提交 |
| `ConfigChange` | multiraft/types.go | 成员变更：AddVoter / RemoveVoter / AddLearner / PromoteLearner |
| `Storage` interface | multiraft/types.go | Raft 日志存储抽象：InitialState / Entries / Save / MarkApplied |
| `User` / `Channel` / `Device` | meta/*.go | 业务数据模型 |
| `ChannelRuntimeMeta` | meta/channel_runtime_meta.go | Leader/ISR/Epoch 运行时元数据 |
| `Raft Logger` | multiraft/logging.go | `wklog` 结构化日志，模块 `slot.raft`，附带 `raftScope=slot` / `nodeID` / `slotID` / `raftEvent`；heartbeat/read-index/probe 类噪声按 Debug 输出 |

## 5. 核心流程

### 5.1 写入（以 CreateChannel 为例）

入口: `proxy/store.go:44 CreateChannel`

```
  ① hashSlot := cluster.HashSlotForKey(channelID)       // CRC32(key) % HashSlotCount
  ② slotID := cluster.SlotForKey(channelID)             // hashSlot 查表定位物理 Slot
  ③ cmd := fsm.EncodeUpsertChannelCommand(channel)      // TLV 编码
  ④ cluster.ProposeWithHashSlot(ctx, slotID, hashSlot, cmd) // 提交带 hashSlot 信封的提案
     ↓
multiraft/slot.go:
  ⑤ enqueueControl(controlPropose, data, future) → scheduler.enqueue(slotID)
  ⑥ Worker 拾取 → processControls → rawNode.Propose(data)
  ⑦ applyCommittedEntries 先从 entry.Data 头部解出 HashSlot，再把 TLV Data 传给状态机
  ⑧ processReady → persistReady → transport.Send → 多数确认
     ↓
fsm/statemachine.go:43 ApplyBatch:
  ⑨ 创建 WriteBatch → 遍历 cmds → 校验物理 Slot + HashSlot 归属 → decodeCommand → cmd.apply(wb, hashSlot)
  ⑩ wb.Commit() — 单次 fsync 原子提交
     ↓
Pebble:
  写入 State 键(0x10)主记录 + Index 键(0x11)二级索引
```

### 5.2 读取（本地 vs 权威 RPC）

入口: `proxy/store.go` 各 Get 方法

```
读取路径分两种:

本地读取（proxy/store.go:69 GetChannel）:
  hashSlot = HashSlotForKey(channelID)
  → db.ForHashSlot(hashSlot).GetChannel(ctx, ...)  // 直接读本地 Pebble

权威读取（proxy/store.go:138 GetUser，需线性一致性）:
  slotID = SlotForKey(uid)
  → callAuthoritativeRPC(proxy/authoritative_rpc.go:35):
    ① 获取 peers := cluster.PeersForSlot(slotID)
    ② 遍历 peers（去重），发送 RPC
    ③ 响应状态处理:
       ok / not_found          → 返回结果
       not_leader + leaderID   → 优先重试 leaderID
       no_leader / no_slot     → 记录 lastErr，尝试下一个
    ④ 所有 peers 用尽 → 返回 lastErr
```

### 5.3 Raft Ready 处理循环

入口: `multiraft/runtime.go:runWorker` → `slot.go:processReady`

```
Worker 循环:
  processSlot (runtime.go:93):
    ① beginProcessing → 抢占锁，防并发处理
    ② processRequests → 入站消息 Step 到 rawNode
    ③ processControls → Propose / ConfigChange / Campaign / TransferLeader
    ④ processTick    → tickPending 时 rawNode.Tick()
    ⑤ processReady   → 核心处理:
       - storageView.persistReady(HardState + Entries + Snapshot)
       - trackReadyEntries(future 追踪)
       - transport.Send(ready.Messages)
       - applyCommittedEntries: 连续 Normal Entry 批量 ApplyBatch (均摊 fsync)
                                遇到 ConfChange 先 flush 再 ApplyConfChange
       - when `lastApplied` advances, `storage.MarkApplied(lastApplied)`
       - rawNode.Advance(ready)
       - 通过 Future 通知提案者
    ⑥ refreshStatus → 检测 Leader 丢失，清理 pending 提案
    ⑦ finishProcessing
```

### 5.4 Slot 生命周期

```
创建: BootstrapSlot (runtime.go:58)
  ① newSlot → 加载持久化状态 → 创建 rawNode
  ② rawNode.Bootstrap(voters) → 初始化成员
  ③ slots[slotID] = slot
  ④ scheduler.enqueue → 首次处理

运行: Ticker 周期(TickInterval)扫描所有 Slot → markTickPending → scheduler.enqueue

关闭: CloseSlot (runtime.go:102)
  ① 等待 processing=false
  ② slot.closed=true → 失败所有 pending futures
  ③ 从 slots map 删除
```

### 5.5 快照

```
生成: fsm/statemachine.go:Snapshot → meta/snapshot.go:ExportSlotSnapshot
  遍历 State/Index/Meta 三个键空间 → 编码为 binary

传输: Raft InstallSnapshot RPC

恢复: fsm/statemachine.go:Restore → meta/snapshot.go:ImportSlotSnapshot
  删除旧键空间范围 → 原子写入所有新 KV → 单次 Commit
```

## 6. Meta DB 键空间与表

**3 个键空间:**
```
State (0x10): [0x10][hashSlot:2][tableID:4][主键列...]            主记录，值带 CRC32 校验
Index (0x11): [0x11][hashSlot:2][tableID:4][indexID:2][索引列...]  二级索引
Meta  (0x12): [0x12][hashSlot:2][...]                             元信息
```

**7 张业务表** (见 `meta/catalog.go`):

| Table ID | 表 | 主键 | 二级索引 |
|----|----|----|----|
| 1 | User | (uid) | - |
| 2 | Channel | (channel_id, channel_type) | idx_channel_id |
| 3 | ChannelRuntimeMeta | (channel_id, channel_type) | - |
| 4 | Device | (uid, device_flag) | - |
| 5 | Subscriber | (channel_id, channel_type, uid) | - |
| 6 | UserConversationState | (uid, channel_type, channel_id) | idx_user_conversation_active |
| 7 | ChannelUpdateLog | (channel_type, channel_id) | - |

## 7. FSM 命令类型（14 种）

TLV 格式: `[Version:1][CmdType:1][Tag:1 + Length:4 + Value:N]...`
未知 Tag 自动跳过（前向兼容）。详见 `fsm/command.go`。

```
1: UpsertUser         2: UpsertChannel         3: DeleteChannel
4: UpsertChannelRuntimeMeta                     5: DeleteChannelRuntimeMeta
6: CreateUser         7: UpsertDevice
8: AddSubscribers     9: RemoveSubscribers
10: UpsertUserConversationStates
11: TouchUserConversationActiveAt               12: ClearUserConversationActiveAt
13: UpsertChannelUpdateLogs                     14: DeleteChannelUpdateLogs
```

## 8. RPC Service IDs（proxy 层）

| Service ID 常量 | 用途 | 文件 |
|---|---|---|
| `runtimeMetaRPCServiceID` | ChannelRuntimeMeta 查询 | proxy/runtime_meta_rpc.go |
| `identityRPCServiceID` | User / Device 查询 | proxy/identity_rpc.go |
| `subscriberRPCServiceID` | 订阅者列表（分页/快照） | proxy/subscriber_rpc.go |
| `userConversationStateRPCServiceID` | 会话状态查询 | proxy/user_conversation_state_rpc.go |
| `channelUpdateLogRPCServiceID` | 频道更新日志查询 | proxy/channel_update_log_rpc.go |

**RPC 状态码** (authoritative_rpc.go): `ok` / `not_found` / `not_leader` / `no_leader` / `no_slot`

## 9. 避坑清单

- **归属校验**: `fsm/statemachine.go:ApplyBatch` 必须同时校验 `cmd.SlotID == m.slot` 和 `cmd.HashSlot` 属于当前状态机拥有的 hash slot 集合；兼容旧路径时会退化为“单物理 slot 仅拥有同编号 hash slot”的默认行为。
- **归属集合会热更新**: 节点收到新的 `HashSlotTable` 后，`cluster` 会把最新的 hash slot 集合推送给已打开的 `fsm.stateMachine`；迁移完成后的新路由能立即生效，Snapshot/Restore 也会按最新集合导出/导入。
- **迁移期 Delta 是受限例外**: Controller 把迁移推进到 `PhaseDelta` 后，源 Slot 的 `fsm.stateMachine` 会由 `cluster` 注入 delta forwarder，把 live write 包装成 `apply_delta` 转发到目标 Slot；目标 Slot 只对这类 `apply_delta` 放开迁移中的 hash slot，普通命令仍按最终归属校验拒绝。
- **CreateUser 幂等**: `Store.CreateUser` 先权威 RPC 查询避免重复，但 Raft Apply 层的 `CreateUser` 仍需是幂等的（并发场景下已存在时跳过，不能 fail Slot）。见 `meta/batch.go:CreateUser`。
- **ListChannelRuntimeMeta 扇出**: `store.go:102` 遍历所有 SlotID 发 RPC，N 个 Slot 就是 N 次 RPC，慎用。
- **ChannelRuntimeMeta 分页边界**: `meta.ShardStore.ListChannelRuntimeMetaPage` 只扫描当前 hash slot 的主键范围，按 `(channel_id, channel_type)` 升序读取并用 `limit+1` 判定是否还有下一页；更高层如果需要物理 Slot / 全局分页，必须基于这个分片原语做增量合并，不能先全量拉取再截页。
- **ChannelRuntimeMeta 权威分页**: `Store.ScanChannelRuntimeMetaSlotPage` 通过 `runtime_meta scan_page` 在物理 Slot leader 上把多个 hash slot 做增量 k-way merge；任一节点对同一 Slot 发起分页都会路由到同一个权威来源，不允许回退本地全量扫描。
- **ApplyBatch 原子性**: 一个 ApplyBatch 内所有命令要么全部成功，要么全部失败（WriteBatch 未 Commit 就丢弃）。任何一条失败会导致整个 Raft Slot fail。
- **Leader 变更自动失败 pending**: `slot.go:refreshStatus` 检测到从 Leader 降级时，立即 fail 所有 submitted/pending 的 proposal/config Future 返回 ErrNotLeader。
- **Batch Apply 与 ConfChange 穿插**: `slot.go:applyCommittedEntries` 遇到 ConfChange 必须先 flush 累积的 Normal Entry 批次。不能把 ConfChange 塞进批次里。
- **RPC Handler 注册**: `proxy.New` 在构造时调用 `cluster.RPCMux().Handle(...)` 注册 5 个 handler，漏任一个该类远端查询会全部失败。
- **写入 Key 路由**: `HashSlotForKey(key)` 先算逻辑 hash slot，再通过 `SlotForKey(key)` 查表定位物理 Slot；**同一实体必须使用同一 Key**（User 用 uid，Channel 用 channelID，Device 用 uid 而非 deviceFlag）。用错 Key 会写到不同 hash slot / Slot，读不到。
- **值 CRC 校验失败**: Pebble 存储值带 CRC32，校验失败返回 `ErrCorruptValue`。表明磁盘损坏或编解码器版本不兼容。
