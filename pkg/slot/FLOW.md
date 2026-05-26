# pkg/slot 流程文档

## 1. 职责定位

基于 Multi-Raft 的分布式元数据存储层。集群元数据按 Slot 分片到多个独立 Raft Group 中管理，每个 Slot 负责一部分键空间（用户、频道、订阅者、会话状态等）。
**不负责**: 消息日志存储（由 channel 负责）、Slot 的副本分配决策（由 controller 负责）、跨节点 durable send 寻址 / 重路由（由 `internal/runtime/channelplane` 负责）。

## 2. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `multiraft/` | `multiraft.New()` → `Runtime` | Multi-Raft 运行时：Worker 池 + Ticker + Scheduler，管理多个独立 Raft Group |
| `fsm/` | `fsm.NewStateMachine()` | 状态机：TLV 命令解码 + BatchStateMachine 批量应用（均摊 fsync） |
| 元数据存储 | `pkg/db/meta.Open()` → `DB` | 统一 Pebble 元数据库：hash-slot 分区表、WriteBatch、快照；`pkg/slot/fsm` 与 `pkg/slot/proxy` 继续使用 `metadb` 别名 |
| `proxy/` | `proxy.New()` → `Store` | 分布式代理：写入路由到 Propose，读取路由到 Leader 的权威 RPC |

## 3. 对外接口

```go
// proxy/store.go — 业务层唯一入口
Store.CreateChannel / UpdateChannel / UpsertChannel / DeleteChannel / GetChannelForPermission / ScanChannelsSlotPage
Store.AddChannelSubscribers / RemoveChannelSubscribers / ListChannelSubscribers
Store.UpsertChannelRuntimeMeta / AdvanceChannelRetentionThroughSeq / GetChannelRuntimeMeta / ListChannelRuntimeMeta / ScanChannelRuntimeMetaSlotPage
Store.CreateUser / UpsertUser / GetUser
Store.UpsertDevice / GetDevice
Store.GetUserConversationState / UpsertUserConversationStates / ListUserConversationActive / ScanUserConversationStatePage
Store.TouchUserConversationActiveAt / ClearUserConversationActiveAt / HideUserConversations
Store.RegisterUserConversationActiveOverlay(overlay)  // 注册 UID-owner active_at 热提示覆盖层
Store.SubmitUserConversationActiveHints / RemoveUserConversationActiveHints
Store.CreateChannelMigrationTask / CreateChannelMigrationTaskWithRuntimeGuard / GetActiveChannelMigrationTask / ListRunnableChannelMigrationTasksForLocalLeaderSlots / ListActiveChannelMigrationTasksForNode
Store.ClaimChannelMigrationTask / AdvanceChannelMigrationTask / SetChannelWriteFence / ResetChannelWriteFenceToPreCutover
Store.CommitChannelLeaderTransfer / AddChannelLearner / PromoteLearnerAndRemoveReplica / ClearChannelWriteFence / AbortChannelMigration
Store.GarbageCollectTerminalChannelMigrationTasks
Store.GetCMDConversationState / ListCMDConversationActive
Store.UpsertCMDConversationStates / AdvanceCMDConversationReadSeq
Store.BindPluginUser / UnbindPluginUser / ListPluginBindingsByUID / ListPluginBindingsByPluginNo / ExistPluginBindingByUID

// pkg/db/meta — 本地 ShardStore / WriteBatch helper
ShardStore.CreateChannelMigrationTask / CreateChannelMigrationTaskWithRuntimeGuard / ClaimChannelMigrationTask / AdvanceChannelMigrationTask / GetChannelMigrationTask / GetActiveChannelMigrationTask / ListChannelMigrationTasks / DeleteTerminalChannelMigrationTasksBefore
WriteBatch.CreateChannelMigrationTask / CreateChannelMigrationTaskWithRuntimeGuard / ClaimChannelMigrationTask / AdvanceChannelMigrationTask / SetChannelWriteFence / ResetChannelWriteFenceToPreCutover / CommitChannelLeaderTransfer / AddChannelLearner / PromoteLearnerAndRemoveReplica / ClearChannelWriteFence / AbortChannelMigration / DeleteTerminalChannelMigrationTasksBefore
ShardStore.BindPluginUser / UnbindPluginUser / ListPluginBindingsByUID / ScanPluginBindingsByPluginNo / ExistPluginBindingByUID
WriteBatch.BindPluginUser / UnbindPluginUser

// multiraft/api.go — Raft Runtime 底层 API
Runtime.OpenSlot / BootstrapSlot / CloseSlot
Runtime.Step(ctx, Envelope)                  // 接收远端 Raft 消息
Runtime.Propose(ctx, slotID, data) → Future  // 提交提案
Runtime.ChangeConfig / TransferLeadership / CompactLog / Status
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `SlotID` / `NodeID` | multiraft/types.go | Slot / 节点标识 |
| `Command` | multiraft/types.go | 状态机命令：SlotID(物理 Raft 组), HashSlot(逻辑哈希分片), Index, Term, Data(TLV编码) |
| `Future` / `Result` | multiraft/types.go | 异步提案结果，Wait(ctx) 阻塞到提交 |
| `ConfigChange` | multiraft/types.go | 成员变更：AddVoter / RemoveVoter / AddLearner / PromoteLearner |
| `Status` | multiraft/types.go | Runtime 观测：Leader、Peers、CurrentVoters、Term/Index 等；CurrentVoters 来自当前 Raft conf state |
| `Storage` interface | multiraft/types.go | Raft 日志存储抽象：InitialState / Entries / Save / MarkApplied |
| `User` / `Channel` / `Device` | pkg/db/meta | 业务数据模型；`Channel` 现在持久化 `Ban` / `Disband` / `SendBan` / `AllowStranger` / `SubscriberMutationVersion` |
| `ChannelRuntimeMeta` | pkg/db/meta | Leader/ISR/Epoch、RouteGeneration、write-fence 与权威保留边界运行时元数据 |
| `ChannelMigrationTask` | pkg/db/meta | Channel leader transfer / replica replace 的权威任务、owner lease、进度与 terminal retention 索引 |
| `CMDConversationState` | pkg/db/meta | UID-owned CMD 离线同步工作集与 read cursor，独立于普通会话状态 |
| `Raft Logger` | multiraft/logging.go | `wklog` 结构化日志，模块 `slot.raft`，附带 `raftScope=slot` / `nodeID` / `slotID` / `raftEvent`；heartbeat/read-index/probe 类噪声按 Debug 输出 |

## 5. 核心流程

### 5.1 写入（以 CreateChannel 为例）

入口: `proxy/store.go:44 CreateChannel`

```
  ① hashSlot := cluster.HashSlotForKey(channelID)       // CRC32(key) % HashSlotCount
  ② slotID := cluster.SlotForKey(channelID)             // hashSlot 查表定位物理 Slot
  ③ cmd := fsm.EncodeUpsertChannelCommand(channel)      // TLV 编码，包含 channel status flags
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

`Store.TouchUserConversationActiveAt` 是 active hint 的 best-effort 落盘路径。它仍按 UID hash slot
拆分命令以保持迁移围栏语义；因此上游 cache 的后台 flush batch 需要保持较小，避免一次 best-effort
flush 触发大量 hash-slot proposal 并抢占前台发送链路。

### 5.2 读取（本地 vs 权威 RPC）

入口: `proxy/store.go` 各 Get 方法

```
读取路径分两种:

本地读取（proxy/store.go:69 GetChannel）:
  hashSlot = HashSlotForKey(channelID)
  → db.ForHashSlot(hashSlot).GetChannel(ctx, ...)  // 直接读本地 Pebble
  → meta.DB 会对热 Channel 元数据维护节点内有界缓存；Create/Update/Upsert/Delete/AddSubscribers/RemoveSubscribers
    以及 WriteBatch commit、快照导入会同步更新或清理缓存，保证订阅版本栅栏不绕过本地已应用状态。

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

Channel migration active task scan:
  `Store.ListActiveChannelMigrationTasksForNode` 按 Slot 权威 leader 扫描 active task，仅以 `SourceNode` / `TargetNode` 判断节点参与关系；`OwnerNodeID` 只表示当前 executor，不算节点参与。NodeScaleIn 会把该结果与当前 `ChannelRuntimeMeta` 扫描结果再 join，用于统计 channel metadata 仍引用目标节点的 active task。
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
       - 若 applied 增量达到 Slot log compaction 阈值，导出 Slot meta snapshot，写入 Raft snapshot，并裁剪旧本地 entries
       - 通过 Future 通知提案者
    ⑥ refreshStatus → 刷新 Leader / CurrentVoters 观测，检测 Leader 丢失并清理 pending 提案
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
生成: fsm/statemachine.go:Snapshot → pkg/db/meta snapshot export
  遍历 State/Index/Meta 三个键空间 → 编码为 binary

传输: Raft InstallSnapshot RPC
  - 小快照仍作为普通 `msgTypeRaft` / `MsgSnap` 发送
  - 超过 transport 单帧预算的 Slot `MsgSnap` 会在 `pkg/cluster` 的 raftTransport 层拆成 `msgTypeRaftSnapshotChunk`
  - 接收端按 snapshot chunk 重组成原始 `MsgSnap` 后再调用 Runtime.Step，状态机 Restore 语义不变

恢复: fsm/statemachine.go:Restore → pkg/db/meta snapshot import
  删除旧键空间范围 → 原子写入所有新 KV → 单次 Commit

本地日志压缩:
  ① processReady apply + MarkApplied + RawNode.Advance 之后检查 applied delta
  ② 达到阈值时调用 StateMachine.Snapshot 导出当前物理 Slot 拥有的 hash slot 数据
  ③ 用当前 applied index/term/conf state 写入 Raft snapshot，并通过 raftlog 裁剪 snapshot 覆盖的 entries
  ④ 发生 Raft membership change 且已有 snapshot 时，会刷新 snapshot 到最新 conf state，保证后续新 learner 可以安装 snapshot 追赶
  ⑤ Runtime.CompactLog 可由运维入口手动触发同一节点本地压缩，忽略自动阈值但仍遵守 compaction enabled 配置
  ⑥ 启动时先 Restore snapshot，再从 snapshot index 之后 replay committed entries
```

### 5.6 Channel Migration Task Flow

```
创建:
  ① 管理入口通过 dry-run 读取 `ChannelRuntimeMeta`，再用 `CreateChannelMigrationTaskWithRuntimeGuard` 把 epoch/leader/fence guard 带入 Slot Raft
  ② FSM 在同一个 `WriteBatch` 中创建 `ChannelMigrationTask`，并在 commit-time 复核 pre-batch runtime guard

Replica replace:
  ① `AddChannelLearner` 原子更新 task 阶段与 `ChannelRuntimeMeta.Replicas`，只把 target 加进 `Replicas`
  ② `learner = Replicas - ISR`；learner 可以接收 channel 复制和 catch-up probe，但不参与 HW quorum、MinISR 写可用性或 leader promotion 候选
  ③ catch-up 必须证明 target 在当前 leader/epoch 下追到阈值；V1 遇到 snapshot-required gate 时不能 promote，必须等待 snapshot 支持或人工恢复
  ④ `SetChannelWriteFence` 原子推进 task 与权威 `WriteFenceVersion`，channel leader drain 期间写入 fail-closed
  ⑤ `PromoteLearnerAndRemoveReplica` 在同一 Slot Raft `WriteBatch` 中验证 drain proof、把 target 加入 `ISR`、移除 source，并推进同 leader `ChannelEpoch`

Leader transfer:
  ① `SetChannelWriteFence` 后由旧 leader `FenceAndDrain` 生成 cutover proof
  ② `CommitChannelLeaderTransfer` 原子验证 task/fence/proof，写入新 leader、递增 `LeaderEpoch`/`ChannelEpoch`，并保留后续 verify/clear 所需阶段

清理/回滚:
  ① `ClearChannelWriteFence` 只能在 verify 阶段用更高 `WriteFenceVersion` 清除权威 fence
  ② `ResetChannelWriteFenceToPreCutover` / `AbortChannelMigration` 仍通过 Slot Raft 命令更新 task 与 runtime meta；不能靠 channel 节点本地 TTL 重新开写
```

## 6. Meta DB 键空间与表

**3 个键空间:**
```
State (0x10): [0x10][hashSlot:2][tableID:4][主键列...]            主记录，值带 CRC32 校验
Index (0x11): [0x11][hashSlot:2][tableID:4][indexID:2][索引列...]  二级索引
Meta  (0x12): [0x12][hashSlot:2][...]                             元信息
```

**业务表 catalog** (见 `pkg/db/meta/schema.go`):

| Table ID | 表 | 主键 | 二级索引 |
|----|----|----|----|
| 1 | User | (uid) | - |
| 2 | Channel | (channel_id, channel_type) | idx_channel_id |
| 3 | ChannelRuntimeMeta | (channel_id, channel_type) | - |
| 4 | Device | (uid, device_flag) | - |
| 5 | Subscriber | (channel_id, channel_type, uid) | - |
| 6 | UserConversationState | (uid, channel_type, channel_id) | idx_user_conversation_active |
| 7 | ReservedConversationProjection | 已移除，不再读写 | - |
| 8 | ChannelMigrationTask | (channel_id, channel_type, task_id) | idx_channel_migration_active, idx_channel_migration_terminal |
| 9 | CMDConversationState | (uid, channel_type, channel_id) | idx_cmd_conversation_active |
| 10 | PluginUserBinding | (uid, plugin_no) | idx_plugin_no_uid |

## 7. FSM 命令类型（34 种 + 2 个保留 ID）

TLV 格式: `[Version:1][CmdType:1][Tag:1 + Length:4 + Value:N]...`
未知 Tag 自动跳过（前向兼容）。详见 `fsm/command.go`。

```
1: UpsertUser         2: UpsertChannel         3: DeleteChannel
4: UpsertChannelRuntimeMeta                     5: DeleteChannelRuntimeMeta
6: CreateUser         7: UpsertDevice
8: AddSubscribers     9: RemoveSubscribers
10: UpsertUserConversationStates
11: TouchUserConversationActiveAt               12: ClearUserConversationActiveAt
13: ReservedConversationProjectionUpsert        14: ReservedConversationProjectionDelete
15: AdvanceChannelRetentionThroughSeq
16: HideUserConversations
17: UpsertCMDConversationStates
18: AdvanceCMDConversationReadSeq
20: ApplyDelta                         21: EnterFence
22: AckMigrationOutbox                 23: CleanupMigrationOutbox
30: CreateChannelMigrationTask         31: ClaimChannelMigrationTask
32: AdvanceChannelMigrationTask        33: SetChannelWriteFence
34: ResetChannelWriteFenceToPreCutover 35: CommitChannelLeaderTransfer
36: AddChannelLearner                  37: PromoteLearnerAndRemoveReplica
38: ClearChannelWriteFence             39: AbortChannelMigration
40: GarbageCollectTerminalChannelMigrationTasks
41: CreateChannelMigrationTaskWithRuntimeGuard
42: BindPluginUser                    43: UnbindPluginUser
```

## 8. RPC Service IDs（proxy 层）

| Service ID 常量 | 值 | 用途 | 文件 |
|---|---:|---|---|
| `runtimeMetaRPCServiceID` | 3 | ChannelRuntimeMeta 查询 | proxy/runtime_meta_rpc.go |
| `identityRPCServiceID` | 4 | User / Device 查询 | proxy/identity_rpc.go |
| `subscriberRPCServiceID` | 10 | 订阅者列表（分页/快照） | proxy/subscriber_rpc.go |
| `userConversationStateRPCServiceID` | 11 | 会话状态查询、active_at 热提示提交/删除 | proxy/user_conversation_state_rpc.go |
| `channelRPCServiceID` | 12 | Channel 权限元数据查询与物理 Slot 权威分页扫描（Ban / Disband / SendBan / AllowStranger / SubscriberMutationVersion） | proxy/channel_rpc.go |
| `channelMigrationRPCServiceID` | 47 | Channel migration active-task 查询与远端 slot-leader 提案转发，避免与 conversation facts service ID 13 冲突 | proxy/channel_migration_rpc.go |
| `cmdConversationStateRPCServiceID` | 49 | CMD 会话状态查询、upsert 与 read cursor 推进 | proxy/cmd_conversation_state_rpc.go |
| `pluginBindingRPCServiceID` | 53 | 插件绑定查询、UID-owned 远端提案与 plugin_no 扫描 | proxy/plugin_binding_rpc.go |

**RPC 状态码** (authoritative_rpc.go): `ok` / `not_found` / `not_leader` / `no_leader` / `no_slot` / `stale_meta`

## 9. 避坑清单

- **归属校验**: `fsm/statemachine.go:ApplyBatch` 必须同时校验 `cmd.SlotID == m.slot` 和 `cmd.HashSlot` 属于当前状态机拥有的 hash slot 集合；兼容旧路径时会退化为“单物理 slot 仅拥有同编号 hash slot”的默认行为。
- **归属集合会热更新**: 节点收到新的 `HashSlotTable` 后，`cluster` 会把最新的 hash slot 集合推送给已打开的 `fsm.stateMachine`；迁移完成后的新路由能立即生效，Snapshot/Restore 也会按最新集合导出/导入。
- **迁移期 Delta 是受限例外**: Controller 把迁移推进到 `PhaseDelta` 后，源 Slot 的 `fsm.stateMachine` 会由 `cluster` 注入 delta forwarder，把 live write 包装成 `apply_delta` 转发到目标 Slot；目标 Slot 只对这类 `apply_delta` 放开迁移中的 hash slot，普通命令仍按最终归属校验拒绝。
- **CreateUser 幂等**: `Store.CreateUser` 先权威 RPC 查询避免重复，但 Raft Apply 层的 `CreateUser` 仍需是幂等的（并发场景下已存在时跳过，不能 fail Slot）。见 `pkg/db/meta` compatibility `WriteBatch.CreateUser`。
- **ListChannelRuntimeMeta 扇出**: `store.go:102` 遍历所有 SlotID 发 RPC，N 个 Slot 就是 N 次 RPC，慎用。
- **ChannelRuntimeMeta 写入单调保护**: `UpsertChannelRuntimeMeta` 会拒绝更旧的 `ChannelEpoch` / `LeaderEpoch` / `RouteGeneration`，同一 epoch 下也不会切换到不同 leader 或缩短 leader lease；已接受的写入不会降低 `RetentionThroughSeq`，相同边界下不会回退 `RetentionUpdatedAtMS`；write-fence 字段是同一通道的权威 fence 状态，`set/renew/reset/clear` 必须通过更高的 `WriteFenceVersion` 表达有效更新，单调写入不能清空或回退已有 fence；repair / bootstrap 必须通过更高 epoch、RouteGeneration 或更长 lease 表达有效更新。
- **RuntimeMeta RPC 版本兼容**: `runtime_meta` v2 response 携带 `RouteGeneration` 和 `WriteFence*` 字段；v1 request/response 必须继续可解码，new caller 可先尝试 v2，遇到旧节点不支持 request codec 时回退 v1，responder 必须按 request codec 版本回包。
- **Channel 迁移 cutover proof**: `CommitChannelLeaderTransfer` 和 `PromoteLearnerAndRemoveReplica` 必须同时验证活跃 fence、`DrainedFenceVersion == meta.WriteFenceVersion`、`DrainedChannelEpoch == meta.ChannelEpoch`、`DrainedLeaderEpoch == meta.LeaderEpoch`、`DrainedLeaderNode == meta.Leader`，否则按 `stale_meta` 处理，避免过期 drain proof 继续切换。
- **Channel 迁移恢复/回滚边界**: `ResetChannelWriteFenceToPreCutover` 只能作为已过期 fence 的恢复命令；`PromoteLearnerAndRemoveReplica` 要求 target 仍是 learner（在 `Replicas` 但不在 `ISR`）；`AbortChannelMigration` 只在 replica replace 且 target 仍未进入 `ISR` 时移除 unpromoted learner，leader transfer abort 不删除非 ISR 副本。
- **Channel 迁移不可逆边界**: drain 后续租 fence 会提升 `WriteFenceVersion` 并清空旧 `Cutover*` / `Drained*` proof，后续 cutover 必须重新 drain；clear 只能从 leader transfer 的 `VerifyNewLeader` 或 replica replace 的 `VerifyMembership` 完成（embedded leader transfer 的 clear 只能回到 `AddLearner`）；abort 只允许在 leader commit / promote 之前的可回滚阶段，且只有已经越过 `AddLearner` 的 replica replace 才会移除 unpromoted learner，不能把已提交 leader transfer 或已 promote 的任务标成 aborted。
- **Replica replace 嵌入式 leader transfer**: 当 source 是当前 channel leader 时，executor 通过 `AdvanceChannelMigrationTask.EmbeddedDesiredLeader` 在同一任务中持久化 embedded transfer 决策；后续 leader-transfer 子阶段不能创建第二个 active task，embedded clear 只清 fence/proof 并回到 `AddLearner`；`AddLearner` 会重新读取权威 meta，若 source 又成为 leader 则重新回到 embedded transfer。
- **Channel 迁移 fence/task 一致性**: set/renew/reset/commit/promote/clear/abort 不允许覆盖或清理 foreign fence；涉及已存在 fence 的命令必须让 task fence 与 runtime meta fence 的 token/version 指向同一 task。source/target 角色校验采用 fail-closed 语义，例如 add learner 时 source 必须仍在 `Replicas` 和 `ISR` 且不能是当前 leader、target 必须尚未进入 `Replicas`/`ISR`。
- **Channel 迁移 clear marker**: token 为空但 `WriteFenceVersion > 0` 是合法的已清 fence generation marker，不算 active/foreign fence；同任务 reset 后重设 fence 或后续新任务 set fence 必须基于该 marker 继续递增版本。
- **Channel 迁移任务创建防竞态**: 管理入口创建任务应使用 `CreateChannelMigrationTaskWithRuntimeGuard`，把 dry-run 读取到的 `ChannelRuntimeMeta` epoch/leader/fence 状态带进 Raft apply；如果创建前 runtime meta 已变化，FSM 返回确定性的 `stale_meta`，避免创建出必然失败的陈旧任务；若同批前序命令已 staged runtime meta，guarded create 会记录 pre-batch DB guard 并在 commit-time 复核，防止同批 staging 期间 DB 被更新后仍创建陈旧任务。
- **Channel 迁移同批写保护**: `UpsertChannelRuntimeMeta` / `CreateChannelMigrationTask` 如果已经在同一个 `ApplyBatch` 里写入，后续迁移命令会复用同批 staged state，不再按 DB 旧值做 commit-time 重新校验；这样 `runtime meta -> create task -> add learner` 这类合法同批命令不会被误判为 stale。`CreateChannelMigrationTaskWithRuntimeGuard` 是例外：它可读取同批 staged runtime meta 做语义校验，但仍会记录 pre-batch runtime guard 用于 commit-time 防竞态。
- **Channel 迁移提交期竞态**: commit-time guard 如果发现 `ErrStaleMeta` / `ErrNotFound` / `ErrAlreadyExists`，FSM 会把该 Raft entry 归一化为确定性的 `stale_meta` 结果；批量提交遇到这类提交期竞态会拆成单条重放，避免正常迁移竞态让 Slot fatal。
- **Channel 迁移编码健壮性**: 迁移命令只接受单个 JSON payload TLV，重复 payload、重复 JSON key 或未知 JSON 字段视为 `ErrCorruptValue`，避免歧义编码。
- **Retention 窄更新是可失败提案**: `AdvanceChannelRetentionThroughSeq` 直接调用 meta store 时会返回 `ErrStaleMeta` / `ErrNotFound`，但 FSM apply 会把 stale 或缺失 runtime meta 转成确定性的 `stale_meta` 结果，避免正常竞态让 Slot fatal；proxy/cluster 再把该结果归一化为调用方可见的 `ErrStaleMeta`。
- **ChannelRuntimeMeta 分页边界**: `meta.ShardStore.ListChannelRuntimeMetaPage` 只扫描当前 hash slot 的主键范围，按 `(channel_id, channel_type)` 升序读取并用 `limit+1` 判定是否还有下一页；更高层如果需要物理 Slot / 全局分页，必须基于这个分片原语做增量合并，不能先全量拉取再截页。
- **ChannelRuntimeMeta 权威分页**: `Store.ScanChannelRuntimeMetaSlotPage` 通过 `runtime_meta scan_page` 在物理 Slot leader 上把多个 hash slot 做增量 k-way merge；任一节点对同一 Slot 发起分页都会路由到同一个权威来源，不允许回退本地全量扫描。
- **Channel 元数据权威分页**: `Store.ScanChannelsSlotPage` 通过 `channel scan_channels_page` 在物理 Slot leader 上扫描 channel 主记录；后台业务频道清单必须基于该权威分页聚合，不能绕过 Slot leader 或全量本地扫描。
- **ListUserConversationActive 热覆盖层**: `Store.ListUserConversationActive` 在 UID 所属 Slot leader 合并持久化 active index 与 `UserConversationActiveOverlay` 中的 UID-local 热提示；覆盖层只作为工作集提示，合并时会 point-read 未出现在 active index 的会话状态，用 `DeletedToSeq` 过滤 stale hint，且对覆盖层请求完整的 UID-local 有界热集合，避免已删除 hint 前缀遮挡后续有效 hint。
- **HideUserConversations 删除语义**: 删除会话必须走独立命令 16；只有新 `DeletedToSeq` 前进时才持久化屏障并在同一批写中清空 `ActiveAt`/删除 active index，避免旧 delete 重试覆盖后续新消息激活；随后通过 `RemoveUserConversationActiveHints` 删除 UID-owner hot hint 并安装 stale hint barrier。
- **命令 16 升级约束**: 混合版本 Slot 副本不能安全接收 `HideUserConversations`；发布时需要 stop-the-world 升级或后续 capability gate。
- **CMDConversationState 是独立表**: CMD 离线同步只读写 Table ID 9，不能混用 `UserConversationState`；active index 仍按 UID owner 有界扫描。
- **CMD read cursor 单调推进**: `AdvanceCMDConversationReadSeq` 只在新 `ReadSeq` 更大时推进，旧 syncack 重试不能回退 cursor。
- **PluginUserBinding UID 路由**: 插件绑定表使用 `(uid, plugin_no)` 主键和 `idx_plugin_no_uid(plugin_no, uid)` 二级索引；写入、解绑、按 UID 查询必须以 UID 作为 hash slot 路由 key，按 plugin_no 扫描是诊断/管理查询，需要按 Slot 权威分页聚合。
- **PluginUserBinding plugin_no 分页**: plugin_no 维度扫描的公开 cursor 以 `(plugin_no, uid, slot_id, hash_slot)` 做总序断点，避免不同 hash slot 中出现相同 `(plugin_no, uid)` 时翻页跳项；远端扫描请求必须校验 `hash_slot` 属于目标物理 Slot。
- **PluginUserBinding 只表达集群绑定**: 表内只保存 UID 到 plugin_no 的权威关联，不保存节点本地插件配置、启停状态或进程状态；这些状态属于 `internal/runtime/plugin` 的 node-local desired/observed state。
- **PluginUserBinding Receive 选择边界**: 写入绑定不要求目标插件在所有节点都存在；Receive hook 执行时由 usecase 结合本节点 observed plugins、desired enabled 和方法列表选择最高优先级 running Receive 插件。
- **ApplyBatch 原子性**: 一个 ApplyBatch 内所有命令要么全部成功，要么全部失败（WriteBatch 未 Commit 就丢弃）。任何一条失败会导致整个 Raft Slot fail。
- **Subscriber 命令硬上限**: `AddSubscribers` / `RemoveSubscribers` 单条 Raft command 最多 1000 个 UID，编码后的 UID 字节最多 64KB；proxy 在提案前校验，FSM decode 仍兜底拒绝超限命令。
- **Leader 变更自动失败 pending**: `slot.go:refreshStatus` 检测到从 Leader 降级时，立即 fail 所有 submitted/pending 的 proposal/config Future 返回 ErrNotLeader。
- **Batch Apply 与 ConfChange 穿插**: `slot.go:applyCommittedEntries` 遇到 ConfChange 必须先 flush 累积的 Normal Entry 批次。不能把 ConfChange 塞进批次里。
- **Slot log compaction 恢复边界**: 启动时只把持久化 snapshot index 作为 RawNode applied point，然后 replay snapshot 之后仍存在的 entries；不能用更靠后的 persisted applied index 跳过 replay。
- **RPC Handler 注册**: `proxy.New` 在构造时调用 `cluster.RPCMux().Handle(...)` 注册 7 个 handler，漏任一个该类远端查询会全部失败。
- **写入 Key 路由**: `HashSlotForKey(key)` 先算逻辑 hash slot，再通过 `SlotForKey(key)` 查表定位物理 Slot；**同一实体必须使用同一 Key**（User 用 uid，Channel 用 channelID，Device 用 uid 而非 deviceFlag）。用错 Key 会写到不同 hash slot / Slot，读不到。
- **值 CRC 校验失败**: Pebble 存储值带 CRC32，校验失败返回 `ErrCorruptValue`。表明磁盘损坏或编解码器版本不兼容。
