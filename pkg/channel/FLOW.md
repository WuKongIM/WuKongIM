# pkg/channel 流程文档

## 1. 职责定位

基于 ISR 复制状态机的分布式频道模型。负责消息的追加、获取、多副本复制、持久化存储和故障恢复。
**不负责**: 频道的 Slot 分配（由 controller 负责）、元数据的分布式存储（由 slot 负责）、跨节点 durable send 寻址 / 重路由（由 `internal/runtime/channelplane` 负责）。

## 2. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `handler/` | `handler.New()` → `Service` | 请求校验、幂等去重、兼容 DurableMessage ingress/egress 编解码、基于结构化消息表的读取与查询 |
| `replica/` | `replica.NewReplica()` → `Replica` | 单 channel ISR 副本状态机：loop-owned Group Commit 追加、HW/Checkpoint 推进、retention boundary 应用/采用/修剪、epoch 分歧检测、角色转换、恢复、快照和 leader reconcile；并提供 dry-run leader promotion evaluator |
| `runtime/` | `runtime.Build()` → `Runtime` | 频道生命周期管理、复制调度（三优先级）、leader/follower lane 状态、retention boundary 委托、多级背压、墓碑清理 |
| `pkg/db/message` | `message.Open()` → `Engine` / `ChannelStore` | 共享 DB 结构化 `message` 表：主记录、二级索引、Checkpoint/EpochHistory/Snapshot/RetentionState/CommittedCursor 等系统状态持久化 |
| `transport/` | `transport.Build()` | steady-state `LongPollFetch`、辅助 `Fetch`，始终独立保留的 `ReconcileProbe` RPC，以及供控制面 leader-repair 复用的同步 `ProbeClient` |

## 3. 对外接口

```go
// channel.go — 对外唯一入口
type Cluster interface {
    ApplyMeta(meta Meta) error                                         // 控制面下发元数据变更
    Append(ctx context.Context, req AppendRequest) (AppendResult, error) // 追加消息
    AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) // 同一频道批量追加消息
    Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)    // 获取已提交消息
    Status(id ChannelID) (ChannelRuntimeStatus, error)                 // 查询频道状态
    Close() error
}

// 组装: channel.New(Config) → Cluster
// 构建顺序: Transport → Runtime → BindFetchService → Handler → cluster
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `Meta` | types.go | 频道元数据：Key, Epoch, RouteGeneration, Leader, Replicas, ISR, MinISR, LeaseUntil, Status, RetentionThroughSeq, WriteFence |
| `Message` | types.go | 消息：MessageID, MessageSeq, FromUID, ClientMsgNo, Payload |
| `ReplicaState` | types.go | 副本状态：Role、运行时 CommitHW(`HW`)、持久化 CheckpointHW、`CommitReady`、LEO、Epoch、retention floor |
| `AppendRequest` | types.go | 追加请求：ChannelID, Message, ExpectedChannelEpoch, ExpectedLeaderEpoch；`TraceID` / `Attempt` 仅用于节点内 diagnostics |
| `AppendBatchRequest` | types.go | 同一 ChannelID 内按请求顺序追加多条消息；逐项结果与输入下标对齐，单条 `Append` 是其兼容包装 |
| `Checkpoint` | types.go | 检查点：Epoch + LogStartOffset + HW；用于冷恢复下界与 reconcile 后的持久化提交水位 |
| `RetentionView` / `RetentionReset` | types.go | retention 规划视图与 follower 低于逻辑 floor 时的 typed reset |
| `FenceAndDrainRequest` / `DrainResult` | types.go | migration cutover 写栅栏下的 leader drain 请求与证明结果 |

## 5. 核心流程

### 5.1 消息追加（Append / AppendBatch）

入口: `channel.go` → `handler/append.go service.Append` / `service.AppendBatch`

`channelplane` 负责 durable send 的寻址与重路由；`pkg/channel` 只在本地 leader 上执行 append、校验 epoch / fence / 归属并返回结构化结果。

```
Handler 层:
  ① 解码 ChannelKey → handler/key.go:KeyFromChannelID
  ② 加载 Meta，校验 Epoch → handler/meta.go:metaForKey；Meta 缓存会保留 slot 投影下来的 WriteFence
     若 WriteFence 仍带 token，则本地写入准入 fail-closed 返回 ErrWriteFenced；即使 wall-clock TTL 已过，也必须等权威 meta 清除/升级 fence 后才重新放行
  ③ 若 `FromUID` 和 `ClientMsgNo` 同时存在，则通过 `LookupIdempotency()` 命中
     `uidx_from_uid_client_msg_no`，再用 `GetMessageBySeq()` 取回已落盘消息
     命中且 PayloadHash 匹配 → 返回旧结果；Hash 不匹配 → ErrIdempotencyConflict
     AppendBatch 会先逐项解析已落盘幂等结果，再在批内按 `(ChannelID, FromUID, ClientMsgNo)` 合并重复项；
     相同 key 且 PayloadHash 不同的后续项返回 `ErrIdempotencyConflict`，不会进入 replica append
     为了支撑 store 的 trusted append 快路径，Handler 会在同一进程内按幂等 key 对“尚未解析落盘结果”的 append
     加短生命周期互斥锁，覆盖幂等查询、MessageID 分配和 replica append；同 key 并发重试会等第一条提交后再命中旧结果，
     不依赖 store 写路径再次读取二级索引兜底
  ③.1 `AppendRequest.TraceID` / `Attempt` 仅透传给节点内 sendtrace/diagnostics，用于排查当前 append 尝试
      它们不参与 message codec、存储、幂等索引或 Phase 1 节点 RPC payload
  ④ 检查 WriteFence 与 legacy U32 剩余 seq 容量；批量请求中不可追加的项写入逐项错误，已落盘幂等命中不受新写 fence 影响
  ⑤ 对可追加的新消息分配 MessageID → MessageIDGenerator.Next()
  ⑥ 编码兼容层 DurableMessage → handler/codec.go:encodeMessage
     这份 `[]byte` 只作为 replica/transport 仍在使用的 ingress 表示；磁盘真相已是结构化 `message` 行

Replica 层 (replica/append.go + replica/append_pipeline.go):
  ⑦ 先检查 leader 是否 `CommitReady=true` 且本地 meta 不带 WriteFence token；若刚启动/刚完成 leader transfer 但尚未完成 reconcile，则直接返回 ErrNotReady；若 migration fence 尚未被权威清除/升级，则返回 ErrWriteFenced
  ⑧ runtime 对本进程新建的记录批次使用 replica owned append fast path，避免重复深拷贝；facade 将 append request 提交到 replica loop；loop-owned append pipeline 按 1ms/64条/64KB 做 Group Commit
     AppendBatch 对同一频道一次调用 `group.Append(ctx, []Record)`，让 leader local durable append、quorum waiter 和 follower fetch 天然看到多条连续 record
  ⑨ append effect worker 在 `durableMu` 下执行 Leader LogStore append/sync；底层 store 对同一 channel 的并发 synced Append 先按
     `pkg/db/message` compatibility commit path 的 FlushWindow 聚合成一个 channel-local append，再在单 channel `writeMu` 下 prepare 记录并提交给跨频道 coordinator；
     coordinator 用可配置窗口（默认 200µs）合并 Pebble Sync，并可按请求数/记录数/字节数限制单批大小；sync 完成前不发布新的 LEO
  ⑩ `ChannelStore.prepareAppendLocked()` 会把兼容层 payload 解成 `messageRow`，Replica durable 写入生产路径使用
     `AppendTrusted` / `StoreApplyFetchTrusted*`，在调用方已经证明记录是下一段连续 log entry 后跳过逐行 Pebble
     existing-index 读取；严格 `Append` / `StoreApplyFetch` API 仍保留旧的跨落盘索引冲突检查。
     两种模式都会写入 `message` 表的 primary/payload families，同时维护 `message_id` / `client_msg_no`
     / `uidx_from_uid_client_msg_no` 三类索引，并继续拒绝同一个批次内重复的 message_id 或幂等 key
  ⑪ durable result 带 EffectID/Channel/Epoch/LeaderEpoch/RoleGeneration 回到 loop；fence 通过后才发布 LEO，并注册 Quorum Waiter (目标 CommitHW = LEO + recordCount)
      对同 leader 的 lease-only 续租，append effect 入写前和 durable result 回环时都以当前已续租 lease 做最终 fence；
      不能因 effect 创建时捕获的旧 lease 已过期而丢弃已经安全落盘的 append，否则 durable LEO 会领先运行时 LEO
      若 lease 恢复/leader reconcile 已经把这批 in-flight append 的 durable LEO 采纳到运行时，effect 回环按幂等完成原 waiter，不再把较低 BaseOffset 当成损坏
  ⑫ `progress_pipeline.go:advanceHWLocked` 取 ISR 中第 MinISR 高的 MatchOffset，满足 quorum 后先推进运行时 `HW(CommitHW)`、完成 Waiter / sendack
  ⑬ Checkpoint 持久化由 checkpoint effect worker 异步 coalescing；写盘成功后才推进 `CheckpointHW`
```

### 5.2 消息获取（Fetch）

入口: `handler/fetch.go service.Fetch`

```
  ① 校验 Limit > 0, MaxBytes > 0
  ② 加载 Meta，检查频道状态非 Deleting/Deleted
  ③ 从 Runtime 获取 HandlerChannel → runtime.Channel(key)
  ④ 若 `CommitReady=false`，直接返回 ErrNotReady，不再从 Checkpoint 猜 committed frontier
  ⑤ handler/fetch.go 将 `FromSeq=0` 或低于 retention/snapshot floor 的请求夹到 `MinAvailableSeq`
  ⑥ 直接调用 `message.ChannelStore.ListMessagesBySeq(startSeq, ...)` 扫描结构化 `message` 表
  ⑦ 读取结果按运行时 CommitHW 和 `MinAvailableSeq` 裁剪，不再通过 compat `ChannelStore.Read()` + DurableMessage 解码重建消息
  ⑧ 返回 Messages + NextSeq + CommittedSeq + RetentionThroughSeq + MinAvailableSeq
```

### 5.3 副本复制（Replication）

发起: `runtime/replicator.go` | 接收: `replica/follower_apply.go:ApplyFetch`

```
steady-state `long_poll`（当前唯一复制主路径）:
  ① follower 侧 Runtime 为每个 peer 维护固定 `N lane` 的 `PeerLaneManager`，并通过 runtime 内部 `lane dispatcher` 统一调度 `(peer,lane)` 的实际发送
  ② channel startup / membership change / response reissue 只负责标记 lane dirty 或 pending，并把 lane 交给 dispatcher；dispatcher 保证同一 `(peer,lane)` 只会 single-flight 发送一条 poll，但真正会阻塞到 RPC 返回的 send 会异步发车，避免被单个 long-poll park 串住其他 lane
  ③ lane 首次发送 `open(full membership + 当前 follower cursorDelta)`，后续 steady-state 只发 `poll(membershipVersionHint + cursorDelta[])`
  ④ leader 侧 Runtime 为每个 `(peer,lane)` 维护 `LeaderLaneSession`；频道把复制事件通过 `replicationTargets` 直达对应 lane
     复制目标来自 `Replicas`，因此 learner 会收到复制；HW / append quorum / reconcile 安全判断只来自 `ISR`，learner 在提升进 `ISR` 前不影响写可用性
  ⑤ leader 处理 `ServeLanePoll` 时，先应用 `cursorDelta` 推进 follower progress / HW，再从 lane ready queue 挑选可返回的 channel item
  ⑥ replica 层在 follower cursor 低于 `max(RetentionThroughSeq, LogStartOffset)` 且 retention 主导 floor 时返回 typed `RetentionReset`；若 `LogStartOffset` 主导则仍走 snapshot-required
  ⑦ `RetentionReset` 会通过普通 Fetch 与 long-poll item 传播；follower 先 `ApplyRetentionBoundary` 采用本地 floor，再从 `RetainedThroughOffset` / `MinAvailableSeq` 继续拉取，不把空/HW-only 响应当作已追平
  ⑧ 若 lane 当前无 ready item，则 park 到 `maxWait` 或新事件；空响应是正常 timeout，follower 收到后立即续下一条 poll
  ⑨ follower 收到 data item 后仍走 `ApplyFetch` 落盘；重复送达的已应用前缀会被当作幂等 ACK 场景跳过，剩余新记录仍必须连续；
     `StoreApplyFetch()` 会按 leader 给出的顺序把兼容 payload
     解成 `messageRow` 并写入同一个结构化 `message` 表；steady-state ACK 通过下一条 lane poll 的 `cursorDelta` 回传
  ⑩ lane poll 发送失败 / backpressure / RPC timeout 的恢复退避按 `(peer,lane)` 维度计时；steady-state 不再依赖 legacy short-poll / batch-fetch 路径
  ⑪ Leader 只有在 follower 的 lane `FullMembership` 已显式带上该 channel（含本地 generation）后，才把它视为已建立 steady-state lane；
     仅因为 leader 本地 ready 标记写入 lane session 的 channel 不能抑制冷 follower 的 wake-up `ReconcileProbe`
  ⑫ 对冷 channel 的 `Fetch` / `ReconcileProbe` ingress 以及 follower 收到的 `lane response item`
     不再要求 runtime 预热：runtime miss 时会通过注入的 activator 按 `ChannelKey` 做一次权威激活，再重试一次本地处理；
     这样首条 long-poll 复制响应不会因为 follower 本地 runtime 尚未落地而被直接丢弃
```

```
Reconcile Probe（启动 / leader transfer 后的 provisional 收敛）:
  ① leader promotion 或 recover 后若 `CommitReady=false`，runtime 触发 peer probe；leader 本地 append 也会给未建立 steady-state lane 视图的冷 follower 排一个轻量 wake-up probe
  ② transport/session.go 发送 ReconcileProbe RPC；普通复制/leader-repair 继续使用 v2 响应收集
     {OffsetEpoch, LogEndOffset, CheckpointHW}，channel migration 可通过 v3 request 显式要求
     {Leader, Role, LogStartOffset, CommitReady} 等扩展 readiness 字段
  ③ replica/reconcile_coordinator.go 基于 epoch lineage + quorum proofs 计算 quorum-safe prefix
  ④ 保留 quorum-safe tail，必要时截断 minority-only suffix
  ⑤ 持久化新的 Checkpoint，推进 `CheckpointHW`，最后把 `CommitReady` 置为 true
```

```
Promotion Dry-run（供 app 侧权威 leader repair 选主）:
  ① 当 app 侧判断权威 `ChannelRuntimeMeta.Leader` 缺失 / 已死 / 不在副本集时，
     slot leader 会向 ISR 候选副本发 `channel_leader_evaluate` RPC
  ② 候选副本从 `pkg/db/message` compatibility store 加载本地 durable view：
     `EpochHistory` / `LEO` / `CheckpointHW` / `OffsetEpoch`
  ③ 若需要 peer proof，则通过 `transport/probe_client.go` 复用 `ReconcileProbe` RPC
     向其他 ISR 副本直连拉取 proof；这条外部 probe 会带 `Generation=0`
  ④ `replica/promotion_evaluator.go` 基于 durable view + proof 计算：
     `ProjectedSafeHW` / `ProjectedTruncateTo` / `CommitReadyNow` / `CanLead`
     - 候选副本必须先拥有本地 durable state；冷空副本不会因为 peer proof 存在就被提升
     - 缺失 ISR proof 只按候选本地 `HW` 计入 quorum-safe prefix，不会证明 `HW` 以上的本地 tail
  ⑤ app 层再根据报告排序并持久化新的 `ChannelRuntimeMeta.Leader`；
     这一步只决定“谁最安全”，真正切主后的 runtime reconcile 仍按正常 leader promotion 流程执行
```

### 5.4 角色转换

入口: `replica/replica.go`

```
BecomeLeader (replica.go:219 → lifecycle_pipeline.go:87):
  ① 验证 Meta.Leader 是本节点
  ② 如需新 epoch boundary，先发 `beginLeaderEpochEffect`，由 durable adapter 以 expected LEO fence 写入 EpochPoint
  ③ 初始化 Progress: 本节点=LEO, 其他ISR=当前安全 committed frontier；retention progress 本节点=LEO, 其他ISR=`RetentionThroughSeq` → progress_pipeline.go:seedLeaderProgressLocked
  ④ 若本地恢复结果仍是 provisional，则允许完成 leader promotion，但保持 `CommitReady=false`
  ⑤ Runtime 触发 reconcile；只有 `CommitReady=true` 后 leader 才接受新的 Append
  ⑥ BecomeLeader 入场 Lease 已过期会直接返回 ErrLeaseExpired；已发布 leader 在 append/reconcile 中发现 lease 过期才会变为 FencedLeader

ApplyMeta same-leader ChannelEpoch bump:
  ① 本节点已经是 leader 且权威 Meta 提升 ChannelEpoch 但 leader 不变时，先保持旧 epoch 对外可见并把 `CommitReady=false`
  ② 通过 durable `BeginEpoch` 写入 `EpochPoint{Epoch:newEpoch, StartOffset:currentLEO}`
  ③ durable 成功后才发布新 epoch 并按原有 readiness/reconcile 规则重新打开 append；durable 失败则保持 not-ready 等待后续权威重试
  ④ 同 leader/epoch/ISR 的 lease-only 续租只刷新本地 Meta，不推进 `roleGeneration`，不触发 leader reconcile，也不因为 `CheckpointHW < HW` 临时关闭 `CommitReady`

BecomeFollower (replica.go:227 → lifecycle_pipeline.go:270):
  ① 应用新 Meta（支持同 channel epoch 下、更高 LeaderEpoch 的 leader transfer）
  ② 失败所有待处理 Append (failOutstandingAppendWorkLocked)

Tombstone (replica.go:231 → lifecycle_pipeline.go:293):
  ① 标记 Tombstoned → 拒绝所有后续操作
```

### 5.4.1 Migration Fence And Drain

入口: `runtime.FenceAndDrain` → `replica.FenceAndDrain`

```
  ① 调用方必须先应用带相同 WriteFence token/version 的权威 Meta；本地 wall-clock TTL 不能放开写入
  ② runtime 按 ChannelKey 找到本地 channel，调用 replica loop 并在结果里补充 RuntimeGeneration
  ③ replica 仅允许当前 leader 或已被 lease fence 的旧 leader，在匹配 ChannelEpoch / LeaderEpoch / Leader 后生成 drain proof；请求携带的权威迁移 fence 可早于本地 Meta 传播到达，同 task token 的旧 fence 也可被更高权威版本推进，但本地已有其他 active/newer fence 时会拒绝
  ④ drain 会先 fail-closed 后续 append，并等待 durable append、quorum waiter、本地未提交 tail、reconcile/checkpoint settle 后再返回
  ⑤ DrainResult 返回 LEO、HW、CheckpointHW、ChannelEpoch、LeaderEpoch、WriteFenceVersion 和 RuntimeGeneration，供 migration task 持久化 proof
  ⑥ drain 后即使同版本 fence TTL 过期或同版本空 fence 被误应用，append 仍返回 ErrWriteFenced；只有更高 WriteFenceVersion 的 clear/reset/superseding meta 可退出 drained fail-closed 状态
```

### 5.4.2 Channel Migration Control Plane Contract

```
  ① Slot `ChannelMigrationTask` 是迁移阶段、owner lease、fence token 和 cutover proof 的权威来源；channel runtime 只应用 slot 投影下来的 `Meta`
  ② Replica replace 的 `AddChannelLearner` 阶段只把 target 加入 `Replicas`，不加入 `ISR`；因此 `learner = Replicas - ISR`
  ③ learner 会接收 steady-state 复制和 catch-up probe，但在 `PromoteLearnerAndRemoveReplica` 把它写入 `ISR` 前，不参与 HW quorum、MinISR 写可用性或 leader promotion 候选
  ④ 同 leader 的迁移成员变更必须跨一个 durable same-leader `ChannelEpoch` boundary：先持久化 `EpochPoint{Epoch:newEpoch, StartOffset:currentLEO}`，再发布新 epoch 和 readiness
  ⑤ cutover 必须先通过权威 `WriteFence` fail-closed 停写，再用 `FenceAndDrain` 证明同一 leader/epoch/fence 下 LEO、HW、CheckpointHW 已收敛
  ⑥ Slot 迁移命令通过同一个 Slot Raft `WriteBatch` 原子更新 task 与 `ChannelRuntimeMeta`，不能由 channel 节点本地推断迁移阶段
  ⑦ V1 不自动安装 channel snapshot；最终 proof 若发现 target `LogStartOffset > CutoverHW`，executor 会按 `ErrSnapshotRequired` 把目标视为未满足 promotion gate，等待后续 snapshot 支持或人工恢复
```

### 5.5 故障恢复

入口: `replica/recovery.go recoverFromStores`

```
  ① 加载 Checkpoint{Epoch, LogStartOffset, HW} → pkg/db/message/checkpoint.go
  ② 加载 Epoch History → pkg/db/message/history.go
  ③ 加载 RetentionState；`LocalRetentionThroughSeq` / `PhysicalRetentionThroughSeq` 只表示本地持久化进度，不会初始化逻辑 `RetentionThroughSeq`
  ④ 校验一致性 (CheckpointHW ≤ LEO，其中 `LEO` 由 `message` 表最大 `message_seq` 与 RetentionState.RetainedMaxSeq 的较大值恢复；Epoch 匹配)
  ⑤ 启动时不再立即把 `LEO > CheckpointHW` 的尾巴截断掉；先保留 local tail，发布 `CommitReady=false`
  ⑥ 若后续 probe 证明尾巴是 quorum-safe，就保留并提升 CommitHW；若只存在于 minority/stale leader，则在 reconcile 中截断
  ⑦ `recovered=true` 在启动恢复成功后设置；reconcile 成功后持久化 fresh checkpoint，推进 `CheckpointHW` 并恢复 `CommitReady=true`
  → Runtime.EnsureChannel(meta) → ApplyMeta → BecomeLeader/BecomeFollower
```

## 6. 运行时架构要点

- **64 路分片**: Runtime 按 FNV(ChannelKey) 分 64 个 shard，每个 shard 独立 RWMutex → `runtime/runtime.go`
- **三优先级调度**: High(Leader变更) / Normal(周期复制) / Low(快照) → `runtime/scheduler.go`
- **lane dispatcher**: `long_poll` steady-state 不再让 channel 直接发下一条 poll；startup / membership / response 事件只调度 `(peer,lane)`，由 dispatcher 统一做 single-flight、异步 send 发车与 dirty requeue，避免阻塞中的 long-poll 把其他 lane 一起串行化 → `runtime/lane_dispatcher.go`
- **三级背压**: 无背压(立即发送) / 软(批量合并) / 硬(排队+重试调度) → `runtime/backpressure.go`
- **墓碑管理**: 频道删除后加入墓碑(带TTL)，防止过期响应生效 → `runtime/tombstone.go`
- **Idle Eviction**: 可通过 `IdleEvictionPolicy` 为冷 channel runtime 设置空闲卸载；卸载前会避开正在使用、调度、复制或 snapshot 等运行时工作，随后复用 `RemoveChannel` 的 tombstone/cleanup 路径释放 replica goroutine 与 `MaxChannels` 计数 → `runtime/runtime.go` / `runtime/channel.go`
- **Replica 执行模式**: 默认 `WK_CLUSTER_CHANNEL_EXECUTION_MODE=pooled`，把本地 replica loop、append/checkpoint effect 和 timer 调度放入共享 worker pool；`dedicated` 保留为回滚模式。它只改变节点内执行缓存/调度方式，仍保留每 channel single-writer、ISR 复制和单节点集群语义 → `replica/execution_pool.go`

## 7. 存储键空间

```
State  (0x15): prefix + key + tableID + messageSeq + familyID
               - 当前唯一业务表是 `message`
               - family 1 = primary metadata
               - family 2 = payload

Index  (0x16): prefix + key + tableID + indexID + indexColumns...
               - `uidx_message_id(message_id)` → message_seq
               - `idx_client_msg_no(client_msg_no, message_seq)` → message_seq
               - `uidx_from_uid_client_msg_no(from_uid, client_msg_no)` → message_seq + message_id + payload_hash

System (0x17): prefix + key + tableID + systemID + ...
               - Checkpoint
               - Epoch History
               - Snapshot Payload
               - Committed Dispatch Cursor
               - Retention State
```

`channel.Record.Payload` 仍作为复制协议兼容表示存在，但不再是 Pebble 存储真相。详见 `pkg/db/message/keys.go`、`pkg/db/message/codec.go`

## 8. 避坑清单

- **per-channel 互斥锁**: `channel.go:lockApplyMeta` 使用引用计数的 per-key 锁，保证同一频道的 ApplyMeta 串行执行。Handler → Runtime 失败时会 RestoreMeta 回滚。
- **结构化 `message` 表才是持久化真相**: `channel.Record.Payload` 现在只是 ingress/egress 兼容层；排查落盘问题时优先看 `pkg/db/message/read.go`、`pkg/db/message/codec.go`，不要再把 raw payload 当作唯一来源。
- **幂等命中依赖唯一索引 + PayloadHash**: 幂等检查走 `uidx_from_uid_client_msg_no`，并且必须校验 FNV-64a PayloadHash 一致，否则返回 `ErrIdempotencyConflict`；不再通过扫描原始日志后解码比对。
- **按 MessageID / ClientMsgNo 查询已经是索引直达**: `MessageID` 命中唯一索引，`ClientMsgNo` 命中 `(client_msg_no, message_seq)` 索引分页；不要再写“全表扫描后过滤”的调用路径。
- **旧版 messagesync 只读 committed message 表**: `handler/message_sync.go:SyncMessages` 复用 `ListMessagesBySeq()`，按旧版 `start_message_seq` / `end_message_seq` / `pull_mode` 语义裁剪结果，并始终按 `message_seq` 升序返回给 HTTP API。
- **Group Commit 窗口**: 默认 1ms/64条/64KB，配置在 `replica/replica.go:effectiveAppendGroupCommit*`。调大会增加延迟但提高吞吐。
- **双水位不要混用**: `HW` 是运行时 CommitHW，驱动 sendack / committed reads / fetch；`CheckpointHW` 是冷恢复安全下界；`CommitReady` 决定 leader 是否已经完成 quorum-safe 收敛。live read path 不能再把 `LoadCheckpoint()` 当作 committed source of truth。
- **LEO 由消息表与 RetentionState 共同恢复**: 重启后的 `LEO()` 不再依赖旧 offset 日志键，而是取 `message` 表最大主键与 `RetentionState.RetainedMaxSeq` 的较大值；如果主记录 family 缺失、payload family 孤儿或索引指到不存在行，会按 `ErrCorruptState` 处理。
- **RetentionState 与尾部截断同批维护**: `LocalRetentionThroughSeq` 是不可回退的本地采用下界，尾部截断不能低于它；当 reconcile 截断本地 tail 时，`RetainedMaxSeq` 必须在同一个 durable batch 内下调到不超过新的 LEO，避免重启后恢复出已删除的尾巴。
- **RetentionThroughSeq 是权威逻辑 floor**: 只来自 slot metadata / `ApplyRetentionBoundary`，本地恢复不会从 RetentionState 反推它；应用后立即更新 `MinAvailableSeq`，但物理删除必须等 `CommitReady`、`CheckpointHW`、`HW`、`LEO` 四个门禁都覆盖 boundary。
- **所有客户端读取都必须尊重 `MinAvailableSeq`**: Fetch、旧版 messagesync、seq read、message query 都要夹到/过滤掉 floor 以下的消息；`FromSeq=0` 也从有效 floor 开始，不再回落到 1。
- **Retention 不修改 checkpoint LogStartOffset**: retention reset/trim 只维护 `RetentionThroughSeq`、`LocalRetentionThroughSeq`、`PhysicalRetentionThroughSeq` 和 retained LEO floor；只有 snapshot install 推进 `LogStartOffset`。
- **HW 推进不可回退**: 运行时 `HW(CommitHW)` 只能前进不能后退。`progress_pipeline.go:advanceHWLocked` 会检查 newHW ≥ currentHW。
- **Lease 过期自动降级**: Leader Lease 过期后自动变为 FencedLeader，拒绝所有写入但不影响读取。见 `replica/append_pipeline.go:appendableLocked` 和 `replica/reconcile_coordinator.go:ensureReconcileLeaseLocked`。
- **Channel-local + cross-channel durable batching**: `ChannelStore.Append()` 对同一 channel 的并发 synced append 先用 commit coordinator 的 FlushWindow 聚合并预留连续 seq，再提交给 `pkg/db/message` 的 compatibility 写入路径；coordinator 支持请求数/记录数/字节数批量上限。单频道的 `writeMu` 仍然串行，且 sync 完成前不会发布新的 LEO。
- **Committed Dispatch Cursor 不是消息真相**: `pkg/db/message` committed cursor compatibility API 的热路径 cursor 只记录异步已提交事件补偿进度，默认使用 `NoSync` 降低写放大且不能覆盖更高 cursor；retention 安全门禁必须走 durable confirm/advance 路径，cursor 丢失时仍只能从结构化 `message` 表或已采用的 retention floor 重复回放。
- **手工 `PutIdempotency()` 只应服务恢复/快照路径**: 追加消息时 `Append()` / `StoreApplyFetch()` 已经维护唯一索引；测试或业务代码再额外覆盖同一索引值，可能把 append 已写入的 `payload_hash` 覆盖掉并制造假性损坏。
- **Checkpoint 不再阻塞 sendack**: leader 在 quorum commit 后先完成 Append waiter，Checkpoint 持久化走 checkpoint effect worker coalescing；若 checkpoint 写盘失败会临时置 `CommitReady=false` 并重试。health / metrics 暴露应通过上层观测性窄接口接入，不改变 sendack 语义。
- **Learner 语义**: `learner = Replicas - ISR`。learner 会接收 steady-state 复制和 catch-up probe，但在进入 `ISR` 前不参与 HW quorum、MinISR 写可用性或 leader promotion 候选。
- **Append diagnostics 不改变协议语义**: `AppendRequest.TraceID` 和 `Attempt` 是 Phase 1 节点内诊断字段，只服务 sendtrace/diagnostics 查询；不要把它们写入 message payload/store codec/idempotency key，也不要编码进 `channel_append` 节点 RPC payload。
- **AppendBatch 是同频道优化，不是跨频道事务**: 批内只保证一个 ChannelID 的请求顺序和逐项结果对齐；跨频道分组必须在上层完成。已落盘幂等命中必须先于 WriteFence 返回，新的可写项才参与一次 `group.Append([]Record)`。
- **leader reconcile 先区分“需要 peer 证明”与“只差本地 checkpoint”**: 若 leader transfer 后只是 `CheckpointHW < HW`、本地没有 `LEO > HW` 的 provisional tail，则会直接做本地 reconcile，不等待 peer probe；若已经拿到足以证明本地 tail 全量 quorum-safe 的 proof，也不会继续卡在离线 ISR peer 上。
- **Transport RPC 分片**: steady-state lane poll 按 `laneID` 路由，保证同一 `(peer,lane)` 有序；辅助 `Fetch` / `ReconcileProbe` 继续按 FNV-64a(ChannelKey) 路由；app 侧同步 `ProbeClient` 也复用同一个 `ReconcileProbe` service。`ReconcileProbe` request 默认保持 v2 以兼容滚动升级，只有 channel migration 显式请求 v3 时服务端才返回扩展 readiness 字段。见 `transport/session.go` / `transport/probe_client.go`。
- **Leader lane session 是固定规模资源**: ready queue / parked waiter 的规模与 `peer * laneCount` 成正比，不会退化成 per-channel timer / goroutine。
- **复制 ingress 允许一次按 key 激活重试**: `ServeFetch` / `ServeReconcileProbe` 遇到 runtime miss 时会先走 activator 按 `ChannelKey` 拉权威 meta、确保本地 runtime，再重试一次；它不是后台全量预热。
- **`ServeReconcileProbe` 允许外部 `Generation=0` 探测**: runtime 内部 steady-state / reconcile session 仍会携带具体 generation 做匹配；而 app 侧 leader-repair dry-run 走同步 `ProbeClient` 时允许 `Generation=0`，服务端返回当前 generation，避免因为本地 runtime 代次未知而拿不到 proof。
- **lane retry 是唯一 steady-state 恢复节奏**: steady-state lane poll 的恢复 backoff 由 `(peer,lane)` timer 驱动；`ReconcileProbe` 仍是独立恢复协议。
- **leader append 会主动叫醒冷 follower**: long-poll 打开后，leader 本地 append 会把尚未被 lane session 跟踪的复制目标补一个 `ReconcileProbe`，避免 follower 必须依赖启动全量预热才能开始拉取。
- **`cursorDelta` 是唯一 steady-state ACK**: follower 不再单独发送 `ProgressAck`；复制进度通过下一条 lane poll 回传给 leader。
- **promotion evaluator 看的是 quorum-safe prefix，不是“谁的 LEO 最大”**: dry-run 评估优先依据 quorum proofs 计算 `ProjectedSafeHW` / `ProjectedTruncateTo`，必要时会拒绝拥有更长但不安全尾巴的副本；app 层选主只能从持久化 ISR 中挑候选，不能把 stale replica 选成新 leader。
- **promotion evaluator 不用缺失 proof 证明 tail**: 缺失 ISR proof 会按候选本地 `HW` 参与 quorum-safe prefix 计算；它可以保留已提交前缀，但不会把 `HW` 以上的本地 tail 当成多数派已证明。
- **promotion evaluator 不会提升冷空副本**: 若候选本地没有任何 durable state（没有 epoch lineage / offset / checkpoint），即使其他 ISR 有 proof 也会返回 `candidate_missing_state`。
