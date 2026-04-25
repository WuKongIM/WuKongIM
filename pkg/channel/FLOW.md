# pkg/channel 流程文档

## 1. 职责定位

基于 ISR 复制状态机的分布式频道模型。负责消息的追加、获取、多副本复制、持久化存储和故障恢复。
**不负责**: 频道的 Slot 分配（由 controller 负责）、元数据的分布式存储（由 slot 负责）。

## 2. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `handler/` | `handler.New()` → `Service` | 请求校验、幂等去重、兼容 DurableMessage ingress/egress 编解码、基于结构化消息表的读取与查询 |
| `replica/` | `replica.NewReplica()` → `Replica` | 副本状态机：Group Commit 追加、HW 推进、分歧检测、角色转换、恢复；并提供 dry-run leader promotion evaluator |
| `runtime/` | `runtime.Build()` → `Runtime` | 频道生命周期管理、复制调度（三优先级）、leader/follower lane 状态、 多级背压、墓碑清理 |
| `store/` | `store.NewEngine()` → `Engine` | Pebble 结构化 `message` 表：主记录、二级索引、Checkpoint/EpochHistory/Snapshot 等系统状态持久化 |
| `transport/` | `transport.Build()` | steady-state `LongPollFetch`、辅助 `Fetch`，始终独立保留的 `ReconcileProbe` RPC，以及供控制面 leader-repair 复用的同步 `ProbeClient` |

## 3. 对外接口

```go
// channel.go — 对外唯一入口
type Cluster interface {
    ApplyMeta(meta Meta) error                                         // 控制面下发元数据变更
    Append(ctx context.Context, req AppendRequest) (AppendResult, error) // 追加消息
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
| `Meta` | types.go | 频道元数据：Key, Epoch, Leader, Replicas, ISR, MinISR, LeaseUntil, Status |
| `Message` | types.go | 消息：MessageID, MessageSeq, FromUID, ClientMsgNo, Payload |
| `ReplicaState` | types.go | 副本状态：Role、运行时 CommitHW(`HW`)、持久化 CheckpointHW、`CommitReady`、LEO、Epoch |
| `AppendRequest` | types.go | 追加请求：ChannelID, Message, ExpectedChannelEpoch, ExpectedLeaderEpoch |
| `Checkpoint` | types.go | 检查点：Epoch + LogStartOffset + HW；用于冷恢复下界与 reconcile 后的持久化提交水位 |

## 5. 核心流程

### 5.1 消息追加（Append）

入口: `channel.go` → `handler/append.go service.Append`

```
Handler 层:
  ① 解码 ChannelKey → handler/key.go:KeyFromChannelID
  ② 加载 Meta，校验 Epoch → handler/meta.go:metaForKey
  ③ 若 `FromUID` 和 `ClientMsgNo` 同时存在，则通过 `LookupIdempotency()` 命中
     `uidx_from_uid_client_msg_no`，再用 `GetMessageBySeq()` 取回已落盘消息
     命中且 PayloadHash 匹配 → 返回旧结果；Hash 不匹配 → ErrIdempotencyConflict
  ④ 分配 MessageID → MessageIDGenerator.Next()
  ⑤ 编码兼容层 DurableMessage → handler/codec.go:encodeMessage
     这份 `[]byte` 只作为 replica/transport 仍在使用的 ingress 表示；磁盘真相已是结构化 `message` 行

Replica 层 (replica/append.go):
  ⑥ 先检查 leader 是否 `CommitReady=true`；若刚启动/刚完成 leader transfer 但尚未完成 reconcile，则直接返回 ErrNotReady
  ⑦ Group Commit 收集 (1ms窗口/64条/64KB) → collectAppendBatch
  ⑧ Leader LogStore 在单 channel `writeMu` 下先 prepare 记录，再把 synced append 提交给 `store/commit.go`
     的跨频道 coordinator，用 200µs 窗口合并 Pebble Sync；sync 完成前不发布新的 LEO
  ⑨ `ChannelStore.prepareAppendLocked()` 会把兼容层 payload 解成 `messageRow`，并由 `messageTable.append()`
     写入 `message` 表的 primary/payload families，同时维护 `message_id` / `client_msg_no`
     / `uidx_from_uid_client_msg_no` 三类索引
  ⑩ sync 成功后执行 publish：更新 durable commit 计数、发布 LEO，并注册 Waiter (目标 CommitHW = LEO + recordCount)
  ⑪ `progress.go:advanceHW` 取 ISR 中第 MinISR 高的 MatchOffset，满足 quorum 后先推进运行时 `HW(CommitHW)`、完成 Waiter / sendack
  ⑫ Checkpoint 持久化由后台 publisher 异步 coalescing；写盘成功后才推进 `CheckpointHW`
```

### 5.2 消息获取（Fetch）

入口: `handler/fetch.go service.Fetch`

```
  ① 校验 Limit > 0, MaxBytes > 0
  ② 加载 Meta，检查频道状态非 Deleting/Deleted
  ③ 从 Runtime 获取 HandlerChannel → runtime.Channel(key)
  ④ 若 `CommitReady=false`，直接返回 ErrNotReady，不再从 Checkpoint 猜 committed frontier
  ⑤ handler/fetch.go 直接调用 `store.ListMessagesBySeq(startSeq, ...)` 扫描结构化 `message` 表
  ⑥ 读取结果按运行时 CommitHW 裁剪，不再通过 `store.Read()` + DurableMessage 解码重建消息
  ⑦ 返回 Messages + NextSeq + CommittedSeq
```

### 5.3 副本复制（Replication）

发起: `runtime/replicator.go` | 接收: `replica/replication.go:ApplyFetch`

```
steady-state `long_poll`（当前唯一复制主路径）:
  ① follower 侧 Runtime 为每个 peer 维护固定 `N lane` 的 `PeerLaneManager`，并通过 runtime 内部 `lane dispatcher` 统一调度 `(peer,lane)` 的实际发送
  ② channel startup / membership change / response reissue 只负责标记 lane dirty 或 pending，并把 lane 交给 dispatcher；dispatcher 保证同一 `(peer,lane)` 只会 single-flight 发送一条 poll，但真正会阻塞到 RPC 返回的 send 会异步发车，避免被单个 long-poll park 串住其他 lane
  ③ lane 首次发送 `open(full membership)`，后续 steady-state 只发 `poll(membershipVersionHint + cursorDelta[])`
  ④ leader 侧 Runtime 为每个 `(peer,lane)` 维护 `LeaderLaneSession`；频道把复制事件通过 `replicationTargets` 直达对应 lane
  ⑤ leader 处理 `ServeLanePoll` 时，先应用 `cursorDelta` 推进 follower progress / HW，再从 lane ready queue 挑选可返回的 channel item
  ⑥ 若 lane 当前无 ready item，则 park 到 `maxWait` 或新事件；空响应是正常 timeout，follower 收到后立即续下一条 poll
  ⑦ follower 收到 item 后仍走 `ApplyFetch` 落盘；`StoreApplyFetch()` 会按 leader 给出的顺序把兼容 payload
     解成 `messageRow` 并写入同一个结构化 `message` 表；steady-state ACK 通过下一条 lane poll 的 `cursorDelta` 回传
  ⑧ lane poll 发送失败 / backpressure / RPC timeout 的恢复退避按 `(peer,lane)` 维度计时；steady-state 不再依赖 legacy short-poll / batch-fetch 路径
  ⑨ 对冷 channel 的 `Fetch` / `ReconcileProbe` ingress 不再要求 runtime 预热：runtime miss 时会通过注入的 activator 按 `ChannelKey` 做一次权威激活，再重试一次本地处理
```

```
Reconcile Probe（启动 / leader transfer 后的 provisional 收敛）:
  ① leader promotion 或 recover 后若 `CommitReady=false`，runtime 触发 peer probe；leader 本地 append 也会给未建立 steady-state lane 视图的冷 follower 排一个轻量 wake-up probe
  ② transport/session.go 发送 ReconcileProbe RPC，收集 follower 的 {OffsetEpoch, LogEndOffset, CheckpointHW}
  ③ replica/reconcile.go 基于 epoch lineage + quorum proofs 计算 quorum-safe prefix
  ④ 保留 quorum-safe tail，必要时截断 minority-only suffix
  ⑤ 持久化新的 Checkpoint，推进 `CheckpointHW`，最后把 `CommitReady` 置为 true
```

```
Promotion Dry-run（供 app 侧权威 leader repair 选主）:
  ① 当 app 侧判断权威 `ChannelRuntimeMeta.Leader` 缺失 / 已死 / 不在副本集时，
     slot leader 会向 ISR 候选副本发 `channel_leader_evaluate` RPC
  ② 候选副本从 `channel/store` 加载本地 durable view：
     `EpochHistory` / `LEO` / `CheckpointHW` / `OffsetEpoch`
  ③ 若需要 peer proof，则通过 `transport/probe_client.go` 复用 `ReconcileProbe` RPC
     向其他 ISR 副本直连拉取 proof；这条外部 probe 会带 `Generation=0`
  ④ `replica/promotion_evaluator.go` 基于 durable view + proof 计算：
     `ProjectedSafeHW` / `ProjectedTruncateTo` / `CommitReadyNow` / `CanLead`
     - 候选副本必须先拥有本地 durable state；冷空副本不会因为 peer proof 存在就被提升
     - 只有真实收到的 quorum proof 才计入 `MinISR`；缺失 proof 不会再被本地 `HW` 隐式补票
  ⑤ app 层再根据报告排序并持久化新的 `ChannelRuntimeMeta.Leader`；
     这一步只决定“谁最安全”，真正切主后的 runtime reconcile 仍按正常 leader promotion 流程执行
```

### 5.4 角色转换

入口: `replica/replica.go`

```
BecomeLeader (replica.go:153):
  ① 验证 Meta.Leader 是本节点
  ② 追加 EpochPoint → history.go:appendEpochPointLocked
  ③ 初始化 Progress: 本节点=LEO, 其他ISR=当前安全 committed frontier → progress.go:seedLeaderProgressLocked
  ④ 若本地恢复结果仍是 provisional，则允许完成 leader promotion，但保持 `CommitReady=false`
  ⑤ Runtime 触发 reconcile；只有 `CommitReady=true` 后 leader 才接受新的 Append
  ⑥ 检查 Lease → 过期则 FencedLeader

BecomeFollower (replica.go:209):
  ① 应用新 Meta（支持同 channel epoch 下、更高 LeaderEpoch 的 leader transfer）
  ② 失败所有待处理 Append (failOutstandingAppendWorkLocked)

Tombstone (replica.go:230):
  ① 标记 Tombstoned → 拒绝所有后续操作
```

### 5.5 故障恢复

入口: `replica/recovery.go recoverFromStores`

```
  ① 加载 Checkpoint{Epoch, LogStartOffset, HW} → store/checkpoint.go
  ② 加载 Epoch History → store/history.go
  ③ 校验一致性 (CheckpointHW ≤ LEO，其中 `LEO` 由 `message` 表最大 `message_seq` 恢复；Epoch 匹配)
  ④ 启动时不再立即把 `LEO > CheckpointHW` 的尾巴截断掉；先保留 local tail，发布 `CommitReady=false`
  ⑤ 若后续 probe 证明尾巴是 quorum-safe，就保留并提升 CommitHW；若只存在于 minority/stale leader，则在 reconcile 中截断
  ⑥ reconcile 成功后持久化 fresh checkpoint，推进 `CheckpointHW`，再标记 recovered / `CommitReady=true`
  → Runtime.EnsureChannel(meta) → ApplyMeta → BecomeLeader/BecomeFollower
```

## 6. 运行时架构要点

- **64 路分片**: Runtime 按 FNV(ChannelKey) 分 64 个 shard，每个 shard 独立 RWMutex → `runtime/runtime.go`
- **三优先级调度**: High(Leader变更) / Normal(周期复制) / Low(快照) → `runtime/scheduler.go`
- **lane dispatcher**: `long_poll` steady-state 不再让 channel 直接发下一条 poll；startup / membership / response 事件只调度 `(peer,lane)`，由 dispatcher 统一做 single-flight、异步 send 发车与 dirty requeue，避免阻塞中的 long-poll 把其他 lane 一起串行化 → `runtime/lane_dispatcher.go`
- **三级背压**: 无背压(立即发送) / 软(批量合并) / 硬(排队+重试调度) → `runtime/backpressure.go`
- **墓碑管理**: 频道删除后加入墓碑(带TTL)，防止过期响应生效 → `runtime/tombstone.go`

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
```

`channel.Record.Payload` 仍作为复制协议兼容表示存在，但不再是 Pebble 存储真相。详见 `store/keys.go`、`store/table_codec.go`

## 8. 避坑清单

- **per-channel 互斥锁**: `channel.go:lockApplyMeta` 使用引用计数的 per-key 锁，保证同一频道的 ApplyMeta 串行执行。Handler → Runtime 失败时会 RestoreMeta 回滚。
- **结构化 `message` 表才是持久化真相**: `channel.Record.Payload` 现在只是 ingress/egress 兼容层；排查落盘问题时优先看 `store/message_table.go`、`store/table_codec.go`，不要再把 raw payload 当作唯一来源。
- **幂等命中依赖唯一索引 + PayloadHash**: 幂等检查走 `uidx_from_uid_client_msg_no`，并且必须校验 FNV-64a PayloadHash 一致，否则返回 `ErrIdempotencyConflict`；不再通过扫描原始日志后解码比对。
- **按 MessageID / ClientMsgNo 查询已经是索引直达**: `MessageID` 命中唯一索引，`ClientMsgNo` 命中 `(client_msg_no, message_seq)` 索引分页；不要再写“全表扫描后过滤”的调用路径。
- **Group Commit 窗口**: 默认 1ms/64条/64KB，配置在 `replica/replica.go:effectiveAppendGroupCommit*`。调大会增加延迟但提高吞吐。
- **双水位不要混用**: `HW` 是运行时 CommitHW，驱动 sendack / committed reads / fetch；`CheckpointHW` 是冷恢复安全下界；`CommitReady` 决定 leader 是否已经完成 quorum-safe 收敛。live read path 不能再把 `LoadCheckpoint()` 当作 committed source of truth。
- **LEO 由最大 `message_seq` 恢复**: 重启后的 `LEO()` 不再依赖旧 offset 日志键，而是扫描 `message` 表最大主键；如果主记录 family 缺失、payload family 孤儿或索引指到不存在行，会按 `ErrCorruptState` 处理。
- **HW 推进不可回退**: 运行时 `HW(CommitHW)` 只能前进不能后退。`progress.go:advanceHW` 会检查 newHW ≥ currentHW。
- **Lease 过期自动降级**: Leader Lease 过期后自动变为 FencedLeader，拒绝所有写入但不影响读取。见 `replica/append.go:appendableLocked`。
- **Cross-channel durable batching**: `store/commit.go` 使用 200µs 窗口跨频道合并 Pebble durable 写入；Leader 的 synced Append 和 Follower 的 ApplyFetch 都走同一个 coordinator。单频道的 `writeMu` 仍然串行，且 sync 完成前不会发布新的 LEO。
- **手工 `PutIdempotency()` 只应服务恢复/快照路径**: 追加消息时 `Append()` / `StoreApplyFetch()` 已经维护唯一索引；测试或业务代码再额外覆盖同一索引值，可能把 append 已写入的 `payload_hash` 覆盖掉并制造假性损坏。
- **Checkpoint 不再阻塞 sendack**: leader 在 quorum commit 后先完成 Append waiter，Checkpoint 持久化走后台 coalescing；若 checkpoint 写盘长期失败，当前实现还缺少显式 health / metrics 暴露。
- **leader reconcile 先区分“需要 peer 证明”与“只差本地 checkpoint”**: 若 leader transfer 后只是 `CheckpointHW < HW`、本地没有 `LEO > HW` 的 provisional tail，则会直接做本地 reconcile，不等待 peer probe；若已经拿到足以证明本地 tail 全量 quorum-safe 的 proof，也不会继续卡在离线 ISR peer 上。
- **Transport RPC 分片**: steady-state lane poll 按 `laneID` 路由，保证同一 `(peer,lane)` 有序；辅助 `Fetch` / `ReconcileProbe` 继续按 FNV-64a(ChannelKey) 路由；app 侧同步 `ProbeClient` 也复用同一个 `ReconcileProbe` service。见 `transport/session.go` / `transport/probe_client.go`。
- **Leader lane session 是固定规模资源**: ready queue / parked waiter 的规模与 `peer * laneCount` 成正比，不会退化成 per-channel timer / goroutine。
- **复制 ingress 允许一次按 key 激活重试**: `ServeFetch` / `ServeReconcileProbe` 遇到 runtime miss 时会先走 activator 按 `ChannelKey` 拉权威 meta、确保本地 runtime，再重试一次；它不是后台全量预热。
- **`ServeReconcileProbe` 允许外部 `Generation=0` 探测**: runtime 内部 steady-state / reconcile session 仍会携带具体 generation 做匹配；而 app 侧 leader-repair dry-run 走同步 `ProbeClient` 时允许 `Generation=0`，服务端返回当前 generation，避免因为本地 runtime 代次未知而拿不到 proof。
- **lane retry 是唯一 steady-state 恢复节奏**: steady-state lane poll 的恢复 backoff 由 `(peer,lane)` timer 驱动；`ReconcileProbe` 仍是独立恢复协议。
- **leader append 会主动叫醒冷 follower**: long-poll 打开后，leader 本地 append 会把尚未被 lane session 跟踪的复制目标补一个 `ReconcileProbe`，避免 follower 必须依赖启动全量预热才能开始拉取。
- **`cursorDelta` 是唯一 steady-state ACK**: follower 不再单独发送 `ProgressAck`；复制进度通过下一条 lane poll 回传给 leader。
- **promotion evaluator 看的是 quorum-safe prefix，不是“谁的 LEO 最大”**: dry-run 评估优先依据 quorum proofs 计算 `ProjectedSafeHW` / `ProjectedTruncateTo`，必要时会拒绝拥有更长但不安全尾巴的副本；app 层选主只能从持久化 ISR 中挑候选，不能把 stale replica 选成新 leader。
- **promotion evaluator 不给缺失 proof 补票**: 只有本地 durable view + 实际收到的 peer proof 才能组成 `MinISR`；缺 proof 时会直接返回 `insufficient_quorum`，避免把本地 `HW` 当成隐式多数派证明。
- **promotion evaluator 不会提升冷空副本**: 若候选本地没有任何 durable state（没有 epoch lineage / offset / checkpoint），即使其他 ISR 有 proof 也会返回 `candidate_missing_state`。
