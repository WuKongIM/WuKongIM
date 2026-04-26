# pkg/cluster 流程文档

## 1. 职责定位

分布式集群编排层。负责 Multi-Raft Slot 的生命周期管理、请求路由与 Leader 转发、Controller 协调（选举 / 任务分配 / 调和）、Hash Slot 迁移调度。
**不负责**: 单个 Raft Group 内部共识（由 `slot/multiraft` 负责）、元数据状态机与存储（由 `slot/fsm` + `slot/meta` 负责）、Controller 决策逻辑（由 `controller/plane` 负责）。

## 2. 核心组件分工

| 组件 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `Cluster` | `cluster.go:59` | 主入口：聚合所有资源、启动/停止生命周期 |
| `Router` | `router.go:9` | 请求路由：CRC32 → HashSlot → 物理 SlotID → Leader 查询 |
| `slotAgent` | `agent.go:21` | 节点代理：心跳上报、同步分配、触发调和 |
| `reconciler` | `reconciler.go:13` | 分配调和器：确保本地 Slot、加载/执行任务、关闭多余 Slot |
| `slotManager` | `slot_manager.go:12` | Slot 管理：ensureLocal / changeConfig / transferLeadership / waitForCatchUp |
| `slotExecutor` | `slot_executor.go:11` | 任务执行器：Bootstrap / Repair / Rebalance 三种任务的分步执行 |
| `controllerClient` | `controller_client.go:31` | Controller RPC 客户端：Leader 发现 + 重试 + 读写操作 |
| `controllerHandler` | `controller_handler.go:12` | Controller RPC 服务端：请求分发到 Propose / Meta 查询 |
| `controllerHost` | `controller_host.go` | Controller 本地宿主：管理 Controller Raft + 状态机 + 元数据库 + leader-local observation cache / delta snapshot / planner dirty wake |
| `runtimeObservationReporter` | `runtime_observation_reporter.go` | 本地 runtime 镜像：扫描 cached status、生成 dirty delta / tombstone / full sync |
| `observerLoop` / `signalLoop` | `observer.go` | 周期 / 事件驱动循环基础设施；当前拆成 heartbeat / runtime scan / slow sync / planner safety / migration progress / wake loops |

## 3. 对外接口

```go
// api.go — 业务层唯一入口
API.Start() / Stop()
API.NodeID() / IsLocal(nodeID)
API.SlotForKey(key) / HashSlotForKey(key) / HashSlotsOf(slotID) / HashSlotTableVersion() / ControllerLeaderID()
API.LeaderOf(slotID) / Propose(ctx, slotID, cmd)
API.SlotIDs() / PeersForSlot(slotID)
API.ListNodes(ctx) / ListNodesStrict(ctx)
API.ListTasks(ctx) / ListTasksStrict(ctx)
API.WaitForManagedSlotsReady(ctx)

// 运维操作 — operator.go
API.ListSlotAssignments(ctx) / ListSlotAssignmentsStrict(ctx)
API.ListObservedRuntimeViews(ctx) / ListObservedRuntimeViewsStrict(ctx)
API.GetReconcileTask(ctx, slotID) / GetReconcileTaskStrict(ctx, slotID) / ForceReconcile(ctx, slotID)
API.MarkNodeDraining(ctx, nodeID) / ResumeNode(ctx, nodeID)
API.TransferSlotLeader(ctx, slotID, nodeID) / RecoverSlot(ctx, slotID, strategy) / RecoverSlotStrict(ctx, slotID, strategy)
API.TransportPoolStats()
Cluster.AddSlot(ctx) / RemoveSlot(ctx, slotID) / Rebalance(ctx)

// 传输层 — 共享给业务层注册额外 Handler
API.Server() / RPCMux() / Discovery() / RPCService(ctx, nodeID, slotID, serviceID, payload)
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `Cluster` | cluster.go:59 | 核心结构体，聚合传输/Controller/Agent/ManagedSlot/迁移等全部资源 |
| `Config` | config.go:32 | 配置容器：NodeID, ListenAddr, SlotCount, HashSlotCount, 工厂函数, 超时参数, Observer / TransportObserver 等；其中 TransportObserver 用于汇聚传输层 bytes / dial / enqueue / RPC client 可观测信号 |
| `Timeouts` | config.go:59 | 控制器请求 / 重试预算 + observation cadence：heartbeat、runtime scan、slow sync、planner safety、planner wake debounce 等 |
| `Router` | router.go:9 | 路由器：持有 HashSlotTable(atomic), 负责 key→slot→leader 映射 |
| `HashSlotTable` | hashslottable.go | Hash Slot 路由表：hashSlot→物理SlotID 映射 + 迁移状态 |
| `slotAgent` | agent.go:21 | 节点代理：持有 Cluster + controllerAPI + assignmentCache |
| `reconciler` | reconciler.go:13 | 调和器：驱动 ensureLocal + loadTasks + executeTask + reportResult |
| `slotManager` | slot_manager.go:12 | Slot 管理器：ensureLocal/changeConfig/transferLeadership/waitForCatchUp/statusOnNode |
| `slotExecutor` | slot_executor.go:11 | 任务执行器：根据 TaskKind 分步执行 Bootstrap/Repair/Rebalance |
| `controllerAPI` | controller_client.go:14 | Controller 客户端接口：Report/ListNodes/RefreshAssignments/迁移操作等 14 个方法 |
| `controllerClient` | controller_client.go:31 | controllerAPI 实现：Leader 缓存 + 逐 peer 探测 + 重定向跟随 |
| `assignmentCache` | assignment_cache.go | 分配缓存：slotID → desiredPeers 映射，原子快照 |
| `runtimeState` | runtime_state.go | 运行时状态：slotID → 当前 peers 映射（线程安全） |
| `runtimeObservationReporter` | runtime_observation_reporter.go | 节点侧 runtime 增量上报器：mirror / dirtyViews / closedSlots / needFullSync |
| `observationCache` | observation_cache.go | leader-local 观测缓存：节点心跳 + `runtimeViewsByNode` 聚合视图 + TTL 淘汰 |
| `ObserverHooks` | config.go:71 | 可观测钩子：OnControllerCall / OnControllerDecision / OnReconcileStep / OnForwardPropose / OnSlotEnsure / OnTaskResult / OnHashSlotMigration / OnLeaderChange / OnNodeStatusChange |

## 5. 核心流程

### 5.1 启动

入口: `cluster.go:128 Start`

```
NewCluster(cfg):
  ① cfg.applyDefaults() → cfg.validate()
  ② 创建 Router(默认 HashSlotTable), runtimeState, assignmentCache, slotManager, slotExecutor

Start():
  ③ startTransportLayer():
     创建 StaticDiscovery → Server → 注册 4 个 Handler:
       msgTypeRaft       → handleRaftMessage
       rpcServiceForward → handleForwardRPC
       rpcServiceController → handleControllerRPC
       rpcServiceManagedSlot → handleManagedSlotRPC
     创建 raftPool + rpcPool → raftClient + fwdClient
     Server / Pool 通过 Config.TransportObserver 上报 transport send/receive bytes、dial / enqueue 结果，以及 RPC client 调用结果 / 时延 / inflight
     Cluster.TransportPoolStats() 在观测刷新时聚合 raftPool/rpcPool 的 active/idle 连接数
  ④ startControllerRaftIfLocalPeer():
     条件: ControllerEnabled() && HasLocalControllerPeer()
     → newControllerHost(cfg, transport)
     → ensureControllerHashSlotTable → 加载或创建默认 HashSlotTable
     → host.storeHashSlotTableSnapshot(table) 预热 leader-local HashSlot snapshot
     → router.UpdateHashSlotTable
     → host.Start()
  ⑤ startMultiraftRuntime():
     → multiraft.New(nodeID, tickInterval, workers, raftTransport)
     → 绑定 Router.runtime
  ⑥ startControllerClient():
     条件: ControllerEnabled()
     → newHashSlotMigrationWorker()
     → newControllerClient(peers, cache)
     → onLeaderChange 时通知 runtimeObservationReporter.requestFullSync()
     → 创建 slotAgent{cluster, client, cache}
  ⑦ startObservationLoop():
     条件: controllerClient != nil
     → 创建 runtimeObservationReporter
     → 周期 ObservationHeartbeatInterval 启动 heartbeatLoop（仅 node heartbeat）
     → 周期 ObservationRuntimeScanInterval 启动 runtimeObservationLoop（delta scan + runtime_report flush）
     → signalLoop 监听 observationHint → wakeReconcileLoop（按 hint 触发 delta sync + reconcile）
     → 周期 ObservationSlowSyncInterval 启动 slowSyncLoop（hint 丢失时的全量/宽范围自愈）
     → signalLoop 监听 controllerHost planner dirty wake → plannerWakeLoop
     → 周期 PlannerSafetyInterval 启动 plannerSafetyLoop（无 wake 时也会兜底评估）
     → 周期 ControllerObservation 启动 migrationProgressLoop（仅在有 active migration / pending abort 时推进）
  ⑧ seedLegacySlotsIfConfigured():
     条件: !ControllerEnabled()（静态部署模式）
     → 遍历 cfg.Slots → openOrBootstrapSlot (根据持久化状态决定 Open 还是 Bootstrap)
```

### 5.2 写入提案（Propose）

入口: `cluster.go:510 Propose` / `cluster.go:519 ProposeWithHashSlot`

```
调用者: Propose(ctx, slotID, cmd)
  ① legacyProposeHashSlot(slotID):
     → router.HashSlotsOf(slotID)
     → 单 hashSlot 场景直接返回，多 hashSlot 报 ErrHashSlotRequired
  ② ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
     ↓
ProposeWithHashSlot:
  ③ encodeProposalPayload(hashSlot, cmd)  // 在 cmd 前附加 2 字节 hashSlot
  ④ Retry 循环 (ForwardRetryBudget 预算内):
     a. router.LeaderOf(slotID) → 查询本地 Runtime 的 Leader
     b. 本地 Leader:
        runtime.Propose(ctx, slotID, payload) → future.Wait(ctx)
     c. 远程 Leader:
        forwardToLeader(ctx, leaderID, slotID, payload)
          → fwdClient.RPCService(leaderID, rpcServiceForward, payload)
          → 远端 handleForwardRPC:
             decodeForwardPayload → runtime.Status(验证 Slot 存在)
             → runtime.Propose → future.Wait
             → encodeForwardResp(errCode, data)
          → 解码响应: OK / NotLeader(重试) / Timeout / NoSlot
  ⑤ 返回结果 (通过 ObserverHooks.OnForwardPropose 上报)
```

### 5.3 观测循环（Observation Loop）

入口: `cluster.go:startObservationLoop`

```
heartbeatLoop 每 ObservationHeartbeatInterval (默认2s) 执行:

heartbeatOnce(ctx):
  ① agent.HeartbeatOnce(ctx):
     → client.Report(nodeStatus)  // 仅上报节点心跳
     → 响应中携带 HashSlotTable → applyHashSlotTablePayload 更新 Router + 状态机
     → Controller Leader 本地只更新 `observationCache.nodes` / `nodeHealthScheduler`
       steady-state heartbeat 不再夹带 per-slot RuntimeView
       heartbeat 返回的 HashSlotTable 优先读 `controllerHost` 的 leader-local snapshot
       snapshot miss 时才 fallback `controllerMeta.LoadHashSlotTable()`
     → 当 `nodeHealthScheduler` 触发 `NodeStatusUpdate` 并被 controller leader 提交后，
       `controllerHost.handleCommittedCommand()` 会通过 `ObserverHooks.OnNodeStatusChange`
       向上游发布 node status 变更

runtimeObservationLoop 每 ObservationRuntimeScanInterval (默认1s) 执行:

runtimeObservationOnce(ctx):
  ① runtimeObservationReporter.tick(ctx)
     → snapshotRuntimeObservationViews():
        遍历 runtime.Slots() → runtime.Status(slotID) → buildRuntimeView
     → 与 mirror 对比：
        - LeaderID / CurrentPeers / HealthyVoters / HasQuorum / ObservedConfigEpoch 变化 → dirtyViews
        - deleteRuntimePeers(slotID) → closedSlots tombstone
     → 若无 dirty / tombstone 且未到 full sync 周期 → 不发 controller RPC
     → flush 成功后更新本地 mirror，失败则保留 dirty 状态
     → leader redirect / leader change / 定期自愈 时发送 `FullSync=true`

wakeReconcileLoop（signalLoop，收到 hint 时立即执行）:
  ① follower 收到 `msgTypeObservationHint`
     → decodeObservationHint → `wakeState.observeHint(...)`
     → cluster.signalObservationWake() 唤醒 wake loop
  ② wakeReconcileOnce(ctx):
     → takePending() 取出 coalesced hint
     → `agent.SyncObservationDelta(ctx, hint)`
        - 调用 controller RPC `fetch_observation_delta`
        - 带上 follower 已应用的 revisions / leader generation / affected slots
        - leader 按 revision 返回增量，必要时 fallback full sync
     → applyObservationDelta:
        - 更新 follower 本地 assignments / tasks / nodes / runtime views cache
        - 记录本次 delta 的 scoped reconcile slots（若 delta 不含 nodes 变化）
        - 对 `delta.Nodes` 做状态 diff；除 controller leader 外的节点都通过
          `ObserverHooks.OnNodeStatusChange` 感知 node status 变更
     → `agent.ApplyAssignments(ctx)` → `reconciler.Tick(ctx)` [见 5.4]
     → `observeHashSlotMigrations(ctx)` [见 5.7]

slowSyncLoop（周期兜底）:
  ① slowSyncOnce(ctx):
     → 即使没有 hint，也调用 `agent.SyncObservationDelta(ctx, observationHint{})`
     → 以空 scope 请求 leader 当前 revision，对 dropped hint / missed wake 做低频修复
     → 成功后继续 `ApplyAssignments + observeHashSlotMigrations`

plannerWakeLoop（signalLoop，controller leader dirty wake）:
  ① `controllerHost.markPlannerDirty()` 在以下场景置 dirty:
     - runtime observation report
     - committed controller command（assignment/task/task-result/migration/operator 等）
     - leader ownership change
  ② dirty wake 经过 PlannerWakeDebounce 去抖后发到 plannerWakeLoop
  ③ plannerWakeOnce(ctx):
     → `controllerHost.consumePlannerDirty()`
     → dirty=true 时立即执行 `controllerTickOnce(ctx)`

plannerSafetyLoop（周期兜底）:
  ① plannerSafetyOnce(ctx):
     → 无论是否有 dirty wake，都执行一次 `controllerTickOnce(ctx)`
     → 避免 wake 丢失后 planner 永久不评估

controllerTickOnce(ctx):
  条件: 本节点是 Controller Leader
  ① 若 leader 仍处于 warmup（尚未收到当前 leader term 下、覆盖全部 alive 节点的 runtime `FullSync=true`）→ 直接跳过
  ② snapshotPlannerState:
     → 优先读 leader-local metadata snapshot (Nodes / Assignments / Tasks)
       - snapshot `Ready && !Dirty` → 直接使用内存快照
       - snapshot cold / dirty → fallback `controllerMeta.ListNodes/ListAssignments/ListTasks`
     → + leader-local observation RuntimeViews
     → runtime views 来自 `observationCache.runtimeViewsByNode`
        - `FullSync=true`：替换单个 reporting node 的完整快照
        - `FullSync=false`：增量 upsert + `ClosedSlots` 删除
        - snapshot 时按 slot 聚合为 planner 需要的视图
        - 长期未更新节点通过 coarse TTL 淘汰 zombie runtime view
  ③ planner.NextDecision(state)
     → 如果有新决策且对应 Slot 无现有任务 → Propose(AssignmentTaskUpdate)
     → 成功后通过 OnControllerDecision 上报任务类型与决策耗时
```

### 5.4 分配调和（Reconciliation）

入口: `reconciler.go:21 Tick`

```
Tick(ctx):
  ① 快照 assignments
     → 若上一个 observation delta 提供了 safe scoped slots（仅 slot-scoped 变化、无 node 变化）
       则只保留受影响 slots；否则走全量 assignments
  ② 过滤出本节点参与的 desiredLocalSlots
  ③ listControllerNodes → 获取所有节点状态 (alive/draining/dead)
     → 若本节点是 Controller Leader 且 metadata snapshot clean，则优先读本地 metadata snapshot
     → follower steady-state 优先读本地已应用的 observation delta cache
     → 否则走 controller client / fallback store 原逻辑
  ④ listRuntimeViews → 获取 leader 观测到的 Slot 运行时视图（leader-local snapshot / follower applied cache）
  ⑤ 确保本地 Slot:
     遍历本节点分配:
       ensureManagedSlotLocal(slotID, desiredPeers, hasView, false)
       → slotManager.ensureLocal [见 5.5]
  ⑥ loadTasks:
     → 先批量读取 Controller Tasks 快照（leader-local metadata snapshot / follower applied cache / controller client `list_tasks` / fallback `controllerMeta.ListTasks`）
     → 与 pendingTaskReport 合并成 slotID → task map，steady-state 无 task 时不再对每个 Slot 单独 `get_task`
  ⑦ 保护迁移源 Slot:
     如果 task 的 SourceNode==本节点 且 kind 为 Repair/Rebalance
     → 源 Slot 即使不在 desiredLocalSlots 中也需保持打开
  ⑧ 关闭多余 Slot:
     遍历 runtime.Slots():
       scoped reconcile 时仅处理 scoped slots；否则处理全部本地 runtime slots
       不在 desiredLocalSlots 且不在 protectedSourceSlots → runtime.CloseSlot
       → deleteRuntimePeers + unregisterRuntimeStateMachine
  ⑨ 执行任务:
     遍历 assignments → 取出对应 task:
       a. reconcileTaskRunnable(now, task): 检查 Pending 或 Retrying+到时间
       b. shouldExecuteTask: 确定由哪个节点执行
          - Repair/Rebalance: SourceNode 优先，否则 Leader 执行
          - 其他: DesiredPeers 中最小 NodeID(alive) 执行
       c. getTask(fresh read) 确认任务仍有效
       d. executeReconcileTask → slotExecutor.Execute [见 5.6]
       e. reportTaskResult → Controller 反馈执行结果
          → 成功后通过 OnTaskResult 上报任务类型与结果
          失败时存 pendingTaskReport，下轮重试上报
```

### 5.5 Slot 本地保障（ensureLocal）

入口: `slot_manager.go:20 ensureLocal`

```
ensureLocal(ctx, slotID, desiredPeers, hasRuntimeView, bootstrapAuthorized):
  ① runtime.Status(slotID) → 已存在: 更新 peers，返回
  ② 不存在: cfg.NewStorage(slotID) + newStateMachine(slotID)
  ③ storage.InitialState:
     有 HardState (已有持久化):
       → 校验本节点仍在 peers 或 desiredPeers 中
       → runtime.OpenSlot(opts)
     空 HardState + bootstrapAuthorized:
       → runtime.BootstrapSlot(opts, voters=desiredPeers)
     空 HardState + hasRuntimeView + !bootstrapAuthorized:
       → runtime.OpenSlot(opts)  // 等 Leader 通过 Raft 添加自己
     空 HardState + !hasRuntimeView:
       → 跳过（无法安全 Open 或 Bootstrap）
```

### 5.6 任务执行（Task Execution）

入口: `slot_executor.go:72 Execute`

```
Execute(ctx, assignment):
  根据 task.Kind 分支:

  Bootstrap:
    → waitForLeader(slotID)
    → 轮询 runtime.Status 直到 LeaderID != 0 (超时 ManagedSlotLeaderWait)

  Repair / Rebalance:
    ① changeConfig(AddLearner, targetNode)
       → LeaderOf(slotID) → 本地: runtime.ChangeConfig / 远程: RPC(change_config)
       → Retry (ConfigChangeRetryBudget)
    ② waitForCatchUp(targetNode)
       → 轮询 target.AppliedIndex >= leader.CommitIndex (超时 ManagedSlotCatchUp)
       → statusOnNode: 本地读 runtime.Status / 远程 RPC(status)
    ③ changeConfig(PromoteLearner, targetNode)
    ④ waitForCatchUp(targetNode)  // promote 后再等一轮
    ⑤ ensureLeaderMovedOffSource(sourceNode, targetNode)
       → 如果当前 Leader == sourceNode → transferLeadership(slotID, targetNode)
       → 轮询直到 Leader != sourceNode (超时 ManagedSlotLeaderMove)
    ⑥ sourceNode != 0 时: changeConfig(RemoveVoter, sourceNode)
```

### 5.7 Hash Slot 迁移

入口: `hashslot_migration.go:105 observeHashSlotMigrations`

```
迁移阶段: Snapshot → Delta → Switching → Done

observeHashSlotMigrations(ctx):
  ① 从 Router.hashSlotTable 加载所有活跃迁移
  ② 中止不再需要的活跃迁移:
     → migrationWorker.AbortMigration(hashSlot)
  ③ 启动新迁移:
     条件: shouldExecuteHashSlotMigration (本节点是 source Leader)
     → migrationWorker.StartMigration(hashSlot, source, target)
  ④ 标记切换完成:
     Phase == PhaseSwitching → migrationWorker.MarkSwitchComplete(hashSlot)
  ⑤ 完成 Snapshot 阶段:
     Phase == PhaseSnapshot:
       → exportHashSlotSnapshot(source, hashSlot)
         状态机导出指定 hashSlot 的数据快照 + 记录 sourceApplyIndex
       → importHashSlotSnapshot(target, snap)
         Leader 本地: 直接 import / 远程: RPC(import_snapshot)
       → migrationWorker.MarkSnapshotComplete(hashSlot, sourceApplyIndex, bytes)
  ⑥ migrationWorker.Tick() → 产生 Transition:
     → PhaseDelta: advanceHashSlotMigration (Propose 到 Controller)
       同时 fsm 层的 DeltaForwarder 将 live write 转发到 target Slot
     → PhaseSwitching: advanceHashSlotMigration
     → PhaseDone: finalizeHashSlotMigration → 更新 HashSlotTable
       → 成功后通过 OnHashSlotMigration 上报 `result=ok`
     → TimedOut: AbortHashSlotMigration + 记录 pendingAbort
       → 成功后通过 OnHashSlotMigration 上报 `result=abort`

Delta 转发 (运行时):
  源 Slot fsm 收到被迁移 hashSlot 的 apply → makeHashSlotDeltaForwarder:
    → 封装 EncodeApplyDeltaCommand → ProposeWithHashSlot(target, hashSlot, payload)
    → 重试直到成功或 Cluster 停止
```

### 5.8 Controller RPC

入口: `controller_client.go:187 call` (客户端) / `controller_handler.go:16 Handle` (服务端)

```
客户端 call(ctx, req):
  ① targets(): 缓存的 Leader 优先 → localLeaderHint → 所有 peers
  ② 逐 peer 探测:
     → 若 target 是本地节点：直接走 `handleControllerRPC(ctx, body)`，避免 controller self-RPC
     → 否则走 `RPCService(target, rpcServiceController=14, body)`
     → decodeControllerResponse:
        NotLeader + LeaderID → 更新缓存，插入 leader 为首重试
        NotLeader + 无 LeaderID → 清除缓存，尝试下一个
        正常 → 缓存该 target 为 Leader，返回

服务端 Handle(ctx, body):
  → 解码 req → 校验 Controller Leader:
     非 Leader → marshalRedirect(LeaderID)
     是 Leader → 分发处理:
       heartbeat         → 更新 leader-local observation / 刷新健康 deadline
                           → 优先读 leader-local HashSlot snapshot 返回版本/表
                           → snapshot miss 时 fallback store 并回填 snapshot
       list_assignments  → 优先读 leader-local metadata snapshot.Assignments
                           + leader-local HashSlot snapshot（miss 时 fallback store）
                           → metadata snapshot dirty / cold 时 fallback `controllerMeta.ListAssignments`
       list_nodes        → 优先读 leader-local metadata snapshot.Nodes
                           → snapshot dirty / cold 时 fallback `controllerMeta.ListNodes`
       list_runtime_views→ controllerHost.snapshotObservations().RuntimeViews
       operator          → Propose(OperatorRequest)
       list_tasks        → 优先读 leader-local metadata snapshot.Tasks
                           → snapshot dirty / cold 时 fallback `controllerMeta.ListTasks`
       get_task          → 优先读 leader-local metadata snapshot.TasksBySlot
                           → snapshot dirty / cold 时 fallback `controllerMeta.GetTask`
       force_reconcile   → forceReconcileOnLeader
       task_result       → Propose(TaskResult)
       start/advance/finalize/abort_migration → Propose(Migration)
       add_slot/remove_slot → Propose(AddSlot/RemoveSlot)
```

## 6. RPC Service IDs

| Service ID | 常量 | 用途 | 文件 |
|---|---|---|---|
| 1 | `rpcServiceForward` | 提案转发到 Leader | forward.go |
| 14 | `rpcServiceController` | Controller 控制面 RPC | codec_control.go |
| 20 | `rpcServiceManagedSlot` | 受管 Slot 操作 RPC | managed_slots.go |

**Controller RPC 操作** (14 种): `heartbeat` / `list_assignments` / `list_nodes` / `list_runtime_views` / `operator` / `get_task` / `force_reconcile` / `task_result` / `start_migration` / `advance_migration` / `finalize_migration` / `abort_migration` / `add_slot` / `remove_slot`

**Managed Slot RPC 操作** (4 种): `status` / `change_config` / `import_snapshot` / `transfer_leader`

**Forward 响应码**: `OK(0)` / `NotLeader(1)` / `Timeout(2)` / `NoSlot(3)`

## 7. 错误码

| 常量 | 含义 | 文件 |
|------|------|------|
| `ErrNoLeader` | Slot 无 Leader | errors.go |
| `ErrNotLeader` | 当前节点非该 Slot Leader | errors.go |
| `ErrNotStarted` | Cluster 未启动或组件为 nil | errors.go |
| `ErrLeaderNotStable` | Leader 迁移超时后仍不稳定 | errors.go |
| `ErrSlotNotFound` | Slot 不存在于 Runtime 中 | errors.go |
| `ErrHashSlotRequired` | 多 hashSlot 场景需显式传入 | errors.go |
| `ErrRerouted` | 请求被重路由 | errors.go |
| `ErrInvalidConfig` | 配置校验失败 | errors.go |
| `ErrManualRecoveryRequired` | 可达副本不足法定人数 | errors.go |

## 8. 避坑清单

- **Propose 必须带 HashSlot**: `Propose()` 是兼容旧路径的快捷方式，仅适用于"一个物理 Slot 只有一个 Hash Slot"的场景。一旦 Slot 拥有多个 Hash Slot（AddSlot/Rebalance 后），必须使用 `ProposeWithHashSlot`，否则返回 `ErrHashSlotRequired`。
- **Forward 重试预算有限**: `ProposeWithHashSlot` 内置 Retry 循环，`ForwardRetryBudget`(默认 300ms) 只重试 `ErrNotLeader`。网络分区或全部 peer 不可达时不会无限重试。
- **Controller 观测读语义**: `ListObservedRuntimeViews` 在 leader 上优先读本地 `observationCache`；只有 leader 不可达时才允许降级到本地 `controllerMeta`，且结果可能滞后。
- **Manager 严格一致读语义**: `ListNodesStrict`、`ListSlotAssignmentsStrict`、`ListObservedRuntimeViewsStrict`、`ListTasksStrict`、`GetReconcileTaskStrict` 只接受 controller leader 结果；本地节点若自身就是 leader 可直接读 leader 本地数据，否则必须经 controller client 读取，禁止降级到本地 `controllerMeta`。
- **Manager recover 必须走 strict assignments**: `RecoverSlotStrict` 使用 `ListSlotAssignmentsStrict` 作为唯一 assignment 来源，避免 manager 写接口因为 fallback 到本地 assignment 状态而在不同节点上看到不同恢复结论。
- **Controller HashSlot 读快路径**: leader 处理 `heartbeat` / `list_assignments` 时优先读 `controllerHost` 持有的 HashSlot snapshot；只有 snapshot cold miss 才会回落到 `controllerMeta.LoadHashSlotTable()`，回填后再继续返回。
- **Controller metadata 读快路径**: leader-local `controllerMetadataSnapshot` 缓存 Nodes / Assignments / Tasks。planner、调和器本地 leader helper、以及 leader 侧 `list_assignments` / `list_nodes` / `list_tasks` / `get_task` 都优先读 clean snapshot；只要 snapshot dirty / cold 就必须回落到 Pebble-backed `controllerMeta`。
- **节点健康改为 deadline 驱动**: steady-state 不再由 `controllerTickOnce()` 提案 `EvaluateTimeouts`；leader 本地 `nodeHealthScheduler` 只在 Alive/Suspect/Dead 边沿变化时提案 `NodeStatusUpdate`。
- **节点健康 mirror 只反映 committed state**: `nodeHealthScheduler` 对 repeated Alive observation 优先读本地 durable node mirror；mirror miss 才 `GetNode()`。mirror 通过 leader change 全量 reload 和 committed command 增量 refresh 维护，不直接信任 proposal payload。
- **NodeStatus 观察链路有且仅有两条**: controller leader 通过 committed `NodeStatusUpdate` / operator command 触发 `OnNodeStatusChange`；其他节点则只通过 `SyncObservationDelta()` 里的 `delta.Nodes` diff 触发同一个 hook，避免 app 层维护两套分支逻辑。
- **新 leader 先 warmup 再规划**: leader change 会清空旧 observation，等待 fresh observation 后再恢复 Repair/Rebalance 规划，避免把“暂时未观测到”误判为节点故障。
- **controller leader warmup 会重挂 node-health deadline**: 新 controller leader 读取 metadata snapshot / node mirror 时，不只是恢复 `nodeMirror`，还会基于持久化的 `LastHeartbeatAt` 重新挂回 suspect/dead timer。这样即使故障节点正好是旧 controller leader，dead 检测也不会因为 leader failover 而永久停在 `Alive`。
- **调和器任务执行权**: 并非所有节点都执行任务。`shouldExecuteTask` 逻辑: Repair/Rebalance 优先 SourceNode 执行，SourceNode 不可用时由 Leader 执行；其他任务由 DesiredPeers 中最小 alive NodeID 执行。错配会导致任务不执行。
- **源 Slot 保护**: 当 Repair/Rebalance 任务的 SourceNode == 本节点时，即使该 Slot 不在 `desiredLocalSlots` 中，调和器也会保护它不被关闭（`protectedSourceSlots`），否则 changeConfig/RemoveVoter 发送不出去。
- **ensureLocal 三条路径**: 有 HardState → Open；无 HardState+bootstrapAuthorized → Bootstrap；无 HardState+hasRuntimeView → Open 等 Leader 添加。混淆条件会导致 Slot 无法加入集群或重复 Bootstrap。
- **Bootstrap 只在任务授权时**: `bootstrapAuthorized=true` 仅在 `reconciler.Tick` 中检测到 `TaskKindBootstrap` 且 `reconcileTaskRunnable` 时才传入。防止脑裂场景下多个节点同时 Bootstrap 同一 Slot。
- **Hash Slot 迁移仅 Source Leader 执行**: `shouldExecuteHashSlotMigration` 检查本节点是否是 source Slot 的 Leader。非 Leader 节点会跳过迁移操作。Leader 切换后迁移自然转移到新 Leader。
- **Delta 转发无限重试**: `forwardHashSlotDelta` 在后台 goroutine 中无限重试直到成功或 Cluster 停止。这保证了迁移期间 live write 不会丢失，但也意味着 Cluster.Stop 前需等待所有 pending delta 发送完毕。
- **pendingTaskReport 防重复上报**: 任务执行完成后如果 `reportTaskResult` RPC 失败，会暂存为 `pendingTaskReport`，下一轮 Tick 重试上报。如果 Controller 侧任务已变更（identity 不匹配），旧结果会被丢弃。
- **ControllerClient Leader 探测有个体超时**: `call()` 对每个 peer 设置独立的 `controllerRequestTimeout`，避免一个慢 peer 耗尽整个重试预算。
- **observeOnce 容忍 SyncAssignments 失败**: 即使 `SyncAssignments` 返回错误，只要本地有缓存的 assignments 且错误是可降级的，仍会触发 `ApplyAssignments`。保证网络抖动时调和不停滞。
- **运行时状态机注册**: `newStateMachine` 创建状态机后立即调用 `registerRuntimeStateMachine`，使后续 `updateRuntimeHashSlotTable` 能推送最新 hash slot 集合。漏注册会导致迁移后状态机不知道自己拥有哪些 hash slot。
- **Config 校验**: `HashSlotCount >= InitialSlotCount` 是硬性约束；`HashSlotCount > 1` 时必须提供 `NewStateMachineWithHashSlots` 工厂函数；`ControllerReplicaN` 和 `SlotReplicaN` 不能超过节点数。
