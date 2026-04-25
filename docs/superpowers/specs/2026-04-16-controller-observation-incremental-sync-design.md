# Controller Observation Incremental Sync Design

**Date:** 2026-04-16
**Status:** Draft for review

## Background

当前 controller 侧 steady-state CPU 的主要来源，已经从 Raft 提案路径收敛到了 observation 路径：

- `observeOnce` 默认每 `200ms` 运行一次
- `slotAgent.HeartbeatOnce()` 每轮先发送 1 条节点心跳
- 然后为本地每个已打开 slot 单独发送 1 条 `RuntimeView`
- controller leader 对每条请求都要进入 `controllerHandler.Handle`

这意味着即使集群完全空闲，只要节点上有 slot，就会持续产生：

- controller RPC 次数
- 编解码开销
- leader handler 进入次数
- observation cache 更新与读路径负担

当前设计在功能上正确，但它把 steady-state 成本绑定到了“observe tick × 本地 slot 数量”，不适合长期扩展。

## Goals

- 把 observation steady-state 成本从“周期性全量散射上报”改成“增量同步 + 低频保活”
- 明显降低空闲期 controller RPC 次数与 leader handler CPU
- 保持节点健康检测与 controller warmup 的 fail-closed 语义
- 保持 repair / rebalance 正确性，允许 `RuntimeView` 存在约 `2~6s` 的陈旧窗口
- 尽量复用现有 `Cluster` / `controllerHost` / `observationCache` 结构，避免重写 planner

## Non-goals

- 本轮不重写 planner / reconciler 的决策规则
- 本轮不引入 mixed-version 滚动升级兼容层
- 本轮不把 `RuntimeView` 重新做回 durable replicated metadata
- 本轮不要求把本地运行时观测变成 intrusive 的 multiraft 新事件总线；优先在现有 runtime 能力上落地

## Proposed Architecture

### 1. Observation split into two channels

把当前单一 `controllerRPCHeartbeat` 语义拆成两个独立通道：

- `heartbeat`
  - 只承载 node keepalive / node metadata
  - 固定低频发送，默认 `1~2s`
  - 继续承载 `HashSlotTableVersion` 对账与 leader 返回的 hash slot table snapshot
  - 继续驱动 `nodeHealthScheduler`

- `runtime_report`
  - 只承载 slot runtime observation
  - steady-state 只发送增量变化
  - 支持批量发送多个 slot 的 view
  - 支持显式 tombstone 删除已关闭 slot
  - 不与 node heartbeat 大包耦合，避免互相拖慢

### 2. Runtime report becomes incremental, not per-slot periodic RPC

新增独立 runtime report 请求，建议结构如下：

```go
type RuntimeObservationReport struct {
    NodeID     uint64
    ObservedAt time.Time
    FullSync   bool
    Views      []controllermeta.SlotRuntimeView
    ClosedSlots []uint32
}
```

语义：

- `FullSync=false`
  - 表示增量更新
  - `Views` 中只包含发生业务变化的 slot
  - `ClosedSlots` 显式删除 leader observation 中对应 slot

- `FullSync=true`
  - 表示该节点当前 runtime observation 的完整快照
  - 用于新 leader warmup、节点重连恢复、低频自愈校准

### 3. Local runtime observation mirror and dirty set

每个节点维护本地 `runtimeObservationReporter`：

- `mirror`
  - 保存“最近一次成功发送给 controller leader 的 slot runtime 视图”
- `dirtyViews`
  - 保存待发送的 slot delta
- `closedSlots`
  - 保存待发送的 slot tombstone
- `needFullSync`
  - 标记是否需要下一次发送完整快照

steady-state 不再对每个 slot 逐个调用 `client.Report(...)`。

### 4. Pragmatic event source for this codebase

理想状态下，`RuntimeView` 应该由 managed slot runtime 的状态变化事件直接驱动。

但基于当前代码结构，本轮优先采用“低侵入增量探测”：

- 保留本地 reporter 的轻量级扫描能力
- 扫描的数据源使用 `multiraft.Runtime.Status(slotID)` 的 cached snapshot
- `Runtime.Status` 读取的是 slot 内部已缓存的状态，不会重新触发 raft ready 处理
- 同时复用现有 cluster 层事件点做显式标脏：
  - `setRuntimePeers`
  - `deleteRuntimePeers`
  - managed slot open / close
  - local source slot protected / released

也就是说，**协议与 controller 视角是事件驱动增量同步；本地第一版实现以“低频 delta scan + 显式 close/open 标记”合成这些事件。**

这比引入新的 multiraft 事件回调更稳，也能先拿到绝大部分 CPU 收益。

### 5. Runtime detection cadence

新增独立 runtime observation cadence，而不是复用 `ControllerObservation=200ms`：

- `ObservationHeartbeatInterval`
  - 节点心跳发送间隔
  - 默认建议：`2s`

- `ObservationRuntimeScanInterval`
  - 本地 runtime delta scan 间隔
  - 默认建议：`1s`

- `ObservationRuntimeFlushDebounce`
  - dirty views 合并后发送的最小去抖窗口
  - 默认建议：`100~200ms`

- `ObservationRuntimeFullSyncInterval`
  - 极低频完整快照自愈周期
  - 默认建议：`60s`
  - 不是 steady-state 主机制，只是兜底纠偏

这样 steady-state 成本从：

- 每 `200ms` × 本地 slot 数量 × 每 slot 一次 RPC

变成：

- 每 `2s` 一次 node heartbeat
- 每 `1s` 一次本地 cached status delta scan
- 只有脏 slot 才触发 `runtime_report`
- 每 `60s` 一次 full sync 自愈

## Data Flow

### Steady-state idle

1. `heartbeatLoop` 每 `2s` 发送一次 node heartbeat
2. `runtimeObservationReporter` 每 `1s` 扫描本地 slot cached status
3. 如果发现无业务变化，不发送 `runtime_report`
4. 仅在 `60s` 自愈窗口到达时发送一次 `FullSync=true` 的全量 runtime snapshot

### Slot runtime changed

1. reporter 扫描到某 slot 的以下字段变化：
   - `LeaderID`
   - `CurrentPeers`
   - `HealthyVoters`
   - `HasQuorum`
   - `ObservedConfigEpoch`
2. 该 slot 被写入 `dirtyViews`
3. debounce 窗口到达后，批量发送一次 `runtime_report`
4. leader 成功应用 observation snapshot 后，本地清理对应 dirty 状态

### Slot closed locally

1. `deleteRuntimePeers(slotID)` 或等价 close 路径触发 tombstone
2. `slotID` 写入 `closedSlots`
3. 下一个 flush 将该 slot 从 leader observation snapshot 中显式删除

### Controller leader changed

1. node 侧 controller client 检测到 redirect / leader 变化
2. `runtimeObservationReporter.needFullSync = true`
3. 下次 flush 发送该节点当前全部 runtime views，`FullSync=true`
4. leader 侧只有在收到至少一轮 fresh runtime full sync 后，才允许 warmup 完成并开放 planner

## Leader-side changes

### 1. controllerHandler

新增 `controllerRPCRuntimeReport`：

- follower 收到时继续返回 redirect
- leader 收到时不走 Raft proposal
- 直接写入 leader-local `observationCache`

`controllerRPCHeartbeat` 继续存在，但只处理：

- 节点 keepalive
- `HashSlotTableVersion` 对账
- `nodeHealthScheduler.observe`

### 2. observationCache

`observationCache` 需要从“单条 apply”扩成“批量 upsert/delete”：

- `applyRuntimeReport(report)`
  - `FullSync=true` 时，按 `NodeID` 替换该节点贡献的全部 runtime views
  - `FullSync=false` 时，只增量 upsert `Views`，并删除 `ClosedSlots`

为支持按节点 full sync 替换，cache 需要补充 slot->owner node 的索引，或直接按 node 维护二级结构：

- `runtimeViewsByNode map[uint64]map[uint32]SlotRuntimeView`
- `snapshot()` 时再聚合为 planner 需要的 `[]SlotRuntimeView`

### 3. Warmup semantics

当前 warmup 语义是“leader 收到 fresh observation 后 ready”。

新设计下应收紧为：

- node keepalive 到达 != runtime observation ready
- 新 leader 至少收到每个已知节点一轮 fresh runtime full sync，或达到可配置最小覆盖条件后，才允许 planner 开始工作

默认建议：

- 单节点集群：收到本地 full sync 即 ready
- 多节点集群：收到全部 alive 节点 full sync 才 ready
- 若部分节点在 warmup 期间没有 heartbeat，则仍按现有 node health fail-closed 语义处理

## Correctness and Failure Handling

### Lost runtime report

- dirty state 在成功 flush 前不能清空
- redirect / timeout / transient error 下保留 dirty，等待下轮重试
- full sync 失败时 `needFullSync` 继续保留

### Zombie runtime views

避免旧 slot runtime view 长时间滞留：

- close 路径优先发 `ClosedSlots`
- leader 侧增加 coarse TTL eviction 作为最后兜底
- TTL 不参与主一致性，只在 tombstone 丢失或节点异常退出时清理陈旧视图

建议 TTL：

- `3 x ObservationRuntimeFullSyncInterval`

### Draining / dead node semantics

- node liveness 仍由 heartbeat 通道驱动
- runtime report 丢失不能自动把节点判 dead
- `NodeStatusUpdate` / `nodeHealthScheduler` 逻辑保持不变

## Trade-offs

### Benefits

- controller leader steady-state RPC 次数大幅下降
- slot 越多，收益越明显
- heartbeat 与 runtime payload 解耦，避免大包拖慢健康检测
- planner / reconciler 不需要重写即可受益

### Costs

- `RuntimeView` 实时性下降到秒级
- observation 协议与 cache 结构变复杂
- warmup 判定逻辑比“收到任意 observation 就 ready”更严格
- 第一版仍保留低频本地 delta scan，不是完全零扫描

## Testing Strategy

### Unit tests

- reporter 在无变化时不发送 runtime report
- reporter 在 leader 变化 / peers 变化 / quorum 变化时合并发送 dirty views
- close slot 触发 `ClosedSlots`
- redirect / timeout 后 dirty 状态保留，成功后清空
- `FullSync=true` 会替换该节点旧 runtime snapshot
- heartbeat 与 runtime report 通道互不阻塞

### Integration tests

- 三节点空闲运行时，runtime report 次数显著低于旧模型
- 新 leader 选举后，planner 在 full sync 到达前保持 warmup
- full sync 到达后 planner 正常恢复
- slot close / reopen 后 leader observation snapshot 正确删除并重建
- controller leader 切换期间 runtime dirty delta 不丢失

### Performance verification

使用当前已有的验证方法回归：

- `docker stats` 采样三节点 idle CPU
- `5001/5002/5003 /metrics` 对比 controller RPC request bytes / count
- `go tool pprof` 抓 controller leader 与 follower CPU profile

预期结果：

- idle 下 `controllerHandler.Handle` 样本下降
- `runtime_report` 次数接近“变化次数 + 低频 full sync”，而不是“tick × slot 数量”
- controller leader 与 follower CPU 更接近，且整体低于当前基线

## Rollout Notes

- 本轮为同版本集群内升级设计，不保证 mixed-version 兼容
- `pkg/cluster/FLOW.md` 与相关测试文档需要在实现完成后同步更新
- 如果第一版收益已满足目标，不再继续把 multiraft 内部状态变化做成专门回调总线
- 若第一版本地 delta scan 仍成为热点，再考虑第二阶段把 dirty 生成下沉到 runtime 事件源
