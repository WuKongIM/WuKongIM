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
API.SlotIDs() / PeersForSlot(slotID)  // SlotIDs 优先返回 assignment/hash-slot table 中的动态物理 Slot，最后才回退 InitialSlotCount
API.ListNodes(ctx) / ListNodesStrict(ctx)
API.ListTasks(ctx) / ListTasksStrict(ctx)
API.WaitForManagedSlotsReady(ctx)

// 运维操作 — operator.go
API.ListSlotAssignments(ctx) / ListSlotAssignmentsStrict(ctx) / ListActiveMigrationsStrict(ctx)
API.ListObservedRuntimeViews(ctx) / ListObservedRuntimeViewsStrict(ctx)
API.SlotLogStatusOnNode(ctx, nodeID, slotID)  // 读取某节点某 Slot 的 Raft commit/applied watermark；本地走 runtime.Status，远程走 managed-slot status RPC
API.GetReconcileTask(ctx, slotID) / GetReconcileTaskStrict(ctx, slotID) / ForceReconcile(ctx, slotID)
API.MarkNodeDraining(ctx, nodeID) / ResumeNode(ctx, nodeID)
API.TransferSlotLeader(ctx, slotID, nodeID) / RecoverSlot(ctx, slotID, strategy) / RecoverSlotStrict(ctx, slotID, strategy)
API.TransportPoolStats()
Cluster.AddSlot(ctx) / RemoveSlot(ctx, slotID) / Rebalance(ctx)
API.ListNodeOnboardingCandidates(ctx)
API.CreateNodeOnboardingPlan(ctx, targetNodeID, retryOfJobID)
API.StartNodeOnboardingJob(ctx, jobID)
API.ListNodeOnboardingJobs(ctx, limit, cursor) / GetNodeOnboardingJob(ctx, jobID) / RetryNodeOnboardingJob(ctx, jobID)

// 传输层 — 共享给业务层注册额外 Handler
API.Server() / RPCMux() / Discovery() / RPCService(ctx, nodeID, slotID, serviceID, payload)
```

`ListActiveMigrationsStrict` 先执行严格的 Controller leader assignment/hash-slot table 刷新，再从刷新后的路由表读取 active migrations。
`ListObservedRuntimeViewsStrict` 在本地或远端 Controller leader observation warmup 未完成时会 fail closed 返回 `ErrObservationNotReady`，调用方不能回退到本地非严格缓存作为安全判断依据。

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `Cluster` | cluster.go:59 | 核心结构体，聚合传输/Controller/Agent/ManagedSlot/迁移等全部资源 |
| `Config` | config.go:32 | 配置容器：NodeID, ListenAddr, InitialSlotCount/SlotCount(仅初始 bootstrap seed), HashSlotCount, EnableHashSlotMigration(默认关闭的实验性 hash-slot 迁移开关), 工厂函数, 超时参数, Observer / TransportObserver 等；其中 TransportObserver 用于汇聚传输层 bytes / dial / enqueue / RPC client 可观测信号 |
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
| `NodeOnboardingCandidate` | onboarding.go | 管理后台扩容候选节点：节点状态、当前 Slot replica 数、leader 数、是否推荐 |

## 5. 核心流程

### 5.1 启动

入口: `cluster.go:128 Start`

```
NewCluster(cfg):
  ① cfg.applyDefaults() → cfg.validate()
  ② 创建 Router(默认 HashSlotTable), runtimeState, assignmentCache, slotManager, slotExecutor

Start():
  ③ startTransportLayer():
     使用 Cluster-owned DynamicDiscovery → Server → 注册 4 个 Handler:
       msgTypeRaft       → handleRaftMessage
       rpcServiceForward → handleForwardRPC
       rpcServiceController → handleControllerRPC
       rpcServiceManagedSlot → handleManagedSlotRPC
     创建 raftPool + rpcPool + controllerPool → raftClient + fwdClient + controllerRPCClient
     Server / Pool 通过 Config.TransportObserver 上报 transport send/receive bytes、dial / enqueue 结果，以及 RPC client 调用结果 / 时延 / inflight
     Cluster.TransportPoolStats() 在观测刷新时聚合 raftPool/rpcPool/controllerPool 的 active/idle 连接数
     controller RPC 使用独立 controllerPool，避免被业务 forward / managed-slot RPC 队列阻塞
  ④ startControllerRaftIfLocalPeer():
     条件: ControllerEnabled() && HasLocalControllerPeer()
     → newControllerHost(cfg, transport)
     → ensureControllerHashSlotTable → 加载或创建默认 HashSlotTable
     → host.storeHashSlotTableSnapshot(table) 预热 leader-local HashSlot snapshot
     → router.UpdateHashSlotTable
     → host.Start()
       Controller Raft 使用独立的 transport message type，必须避免与 Slot Raft / observation hint 冲突；
       入站 Controller Raft frame 只接受 To=local 且 From!=local 的消息，避免 stale discovery / loopback 帧进入 RawNode。
  ⑤ startMultiraftRuntime():
     → multiraft.New(nodeID, tickInterval, workers, raftTransport)
     → 绑定 Router.runtime
  ⑥ startControllerClient():
     条件: ControllerEnabled()
     → newHashSlotMigrationWorker()
     → newControllerClient(static controller peers 或 join seeds, cache)
     → join mode 且本节点不在静态 Nodes 中时，先 JoinCluster（携带 NodeID / Node.Name / AdvertiseAddr / token / version），校验响应包含本节点且地址匹配后，作为 data worker 更新 discovery / HashSlotTable / controller client peers（不变更 Controller voter 集合）
     → onLeaderChange 时通知 runtimeObservationReporter.requestFullSync()
     → 创建 slotAgent{cluster, client, cache}
     Controller client 的只读 RPC（list_nodes / list_assignments / list_runtime_views / list_tasks）遇到 not leader /
     no leader / deadline exceeded 时按 DEBUG 记录，避免启动选主或 failover 期间把可重试读抬成 ERROR。
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
     → client.Report(nodeStatus)  // 仅上报节点心跳；地址优先使用 AdvertiseAddr，其次使用本节点 WK_CLUSTER_NODES 静态地址，避免把监听绑定地址写回 membership
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
        - 若本次包含 node 变化或 full sync，用合并后的完整 nodes cache 刷新 DynamicDiscovery
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
  ③ advanceNodeOnboardingOnce(state)
     → 若存在 running onboarding job，每轮最多推进一个状态转移：
       - pending move: propose NodeOnboardingJobUpdate，同时写入目标 Assignment + TaskKindRebalance
       - running move: 等待 Assignment/Runtime 收敛；必要时调用 TransferSlotLeader 后再完成 move
       - 全部 move 终态成功: 标记 job completed；异常: 标记 failed
     → 如果本轮已推进 onboarding，直接返回，避免同 tick 再做普通 planner 决策
  ④ 若仍有 running onboarding job:
     → state.PauseRebalance=true，暂停普通自动 Rebalance
     → state.LockedSlots=currentOnboardingLockedSlots，锁住当前 onboarding move 的 Slot
     → Bootstrap/Repair 仍允许 planner 处理
  ⑤ planner.NextDecision(state)
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
          - Bootstrap: 若 Task.TargetNode 已设置，则由 TargetNode 执行，用于初始化 Leader 均衡；旧任务无 TargetNode 时回退到 DesiredPeers 中最小 alive NodeID
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
    → waitForLeader(slotID)  // 轮询 runtime.Status 直到 LeaderID != 0 (超时 ManagedSlotLeaderWait)
    → ensureLeaderOnTarget(slotID, TargetNode)  // 初始 leader 不在目标节点时主动 transfer，尽量均衡初始化 Leader 分布

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

对外创建入口 `StartHashSlotMigration`、`AddSlot`、`RemoveSlot` 受 `Config.EnableHashSlotMigration` 保护；默认关闭时返回 `ErrInvalidConfig`，避免在 durable delta forwarding、source fencing、recoverable cutover 语义明确接受前启用实验性迁移。已存在迁移的内部观测、推进、完成和中止流程不受该开关影响。

```
迁移阶段: Snapshot → Delta → Switching → Done

observeHashSlotMigrations(ctx):
  ① 从 Router.hashSlotTable 加载所有活跃迁移
  ② 先重试 pending source cleanup，并扫描已注册 source 状态机的持久 migration state:
     → 如果持久 state 对应的 hashSlot/source/target 已不在 Controller 活跃迁移中，
       向 source Slot 复制 bounded cleanup 维护命令；这样 finalize/abort 成功后即使节点重启丢失内存 pending map，
       也能通过持久 state 重新发现并清理 source fence/outbox。
     → 如果 source Slot 已因 RemoveSlot finalize 被关闭、但同一节点仍有其他已注册状态机可访问共享 metadb，
       observer 可使用该状态机执行 bounded local cleanup 作为退休 source Slot 的恢复路径。
  ③ 中止不再需要的活跃迁移:
     → migrationWorker.AbortMigration(hashSlot)
       仅在 Controller 迁移已消失或 source/target 被重新规划时清理 source durable delta outbox；
       本节点只是暂时不再是 source Leader 时只停止本地 worker，不能删除恢复用 outbox。
  ④ 启动新迁移:
     条件: shouldExecuteHashSlotMigration (本节点是 source Leader)
     → migrationWorker.StartMigration(hashSlot, source, target)
  ⑤ 完成 Snapshot 阶段:
     Phase == PhaseSnapshot:
       → 先确认 target Slot 已有 Leader / 可导入快照，避免在 target 尚不可用时提前 fence source
       → 再向 source Slot 提案 EnterFence，确保 FenceIndex 已在 source apply 路径持久化，
         从而阻止 snapshot apply index 之后的新 source 写入落在 snapshot/outbox 之外
       → exportHashSlotSnapshot(source, hashSlot)
         状态机导出指定 hashSlot 的数据快照 + 记录 sourceApplyIndex
       → importHashSlotSnapshot(target, snap)
         Leader 本地: 直接 import / 远程: RPC(import_snapshot)
       → migrationWorker.MarkSnapshotComplete(hashSlot, sourceApplyIndex, bytes)
  ⑥ replay durable delta outbox:
     PhaseDelta / PhaseSwitching 且本节点是 source Leader 时，从 source 状态机批量读取持久 outbox，
     封装 apply_delta 后提案到 target；只有目标提案成功才向 source Slot 复制 ack 维护命令。
     如果 source outbox 不可检查，或本轮读取达到 replay limit，视为未 drained。
  ⑦ source fence / switch readiness:
     若准备进入 Switching 或 Controller 已处于 Switching，必须确认 source Slot 已持久化 EnterFence；
     source fsm 持久 fence marker 到 durable outbox，之后普通 source 写入返回 fenced 结果且不让 Raft apply 失败，
     仅允许 apply_delta / fence 等迁移维护命令；
     fence marker 被 target apply_delta 并 ack 后，才认为 fence ready。
  ⑧ 标记切换完成:
     Phase == PhaseSwitching 且 durable delta outbox 已确认 drained 且 source fence ready → migrationWorker.MarkSwitchComplete(hashSlot)
  ⑨ migrationWorker.Tick() → 产生 Transition:
     → PhaseDelta: advanceHashSlotMigration (Propose 到 Controller，payload 携带 hashSlot/source/target/phase)
       同时 fsm 层的 DeltaForwarder 将 live write 转发到 target Slot
     → PhaseSwitching: 仅 durable delta outbox 已确认 drained 且 source fence ready 时 advanceHashSlotMigration（同样携带 source/target identity）
     → PhaseDone: 仅 durable delta outbox 已确认 drained 且 source fence ready 时 finalizeHashSlotMigration → 更新 HashSlotTable
       → finalize 成功后向 source Slot 复制 cleanup 维护命令清理 durable delta outbox，cleanup payload 携带 throughIndex，
         source fsm 只删除 <= throughIndex 的 outbox row，且只有当前 state.LastOutboxIndex 非 0 且不大于 throughIndex 时删除 migration state，
         防止旧 cleanup 命令擦除同方向的新迁移状态。
       → 成功后通过 OnHashSlotMigration 上报 `result=ok`
     → TimedOut: AbortHashSlotMigration + 记录 pendingAbort
       → abort 成功后向 source Slot 复制 bounded cleanup 维护命令清理 durable delta outbox，清理失败会登记 pending retry
       → 成功后通过 OnHashSlotMigration 上报 `result=abort`

Delta 转发 (运行时):
  Controller 表处于 Snapshot 时 target runtime 已发布 incoming delta 标记，保证新增物理 Slot 可用被迁移 hashSlot bootstrap/import；
  target fsm 对 apply_delta 维护命令做幂等接收，即使 follower 的 runtime incoming 标记落后于 Raft 数据面也不能把 Slot 置为 fatal；
  Controller 表处于 Delta 或 Switching 时，source runtime 发布 outgoing 标记，target runtime 继续保留 incoming 标记；
  target import snapshot 后不会删除 hashSlot 的 source migration state/outbox；这些记录是 source fence 和 cleanup recovery 的一部分，
  后续由 finalize/abort cleanup 或 orphaned-state reconciler 清理。
  普通 Raft snapshot restore 仍使用 exact import，会替换 state/index/meta span，避免本地旧 migration meta 污染恢复后的状态；
  只有 live hash-slot 迁移导入路径保留本地 migration meta。
  源 Slot fsm 收到被迁移 hashSlot 的 apply → makeHashSlotDeltaForwarder:
    → 同一 apply batch 中先把原始 source command 写入 durable delta outbox
    → 封装 EncodeApplyDeltaCommand → ProposeWithHashSlot(target, hashSlot, payload)
    → live forwarding 最佳努力重试直到成功或 Cluster 停止；失败不阻塞 source apply，后续 observer 通过 durable outbox replay 恢复
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
        NotLeader + LeaderID → 更新缓存，插入 leader 为首重试；join_cluster 可携带 LeaderAddr 作为临时 seed
        NotLeader + 无 LeaderID → 清除缓存，尝试下一个
        正常 → 缓存该 target 为 Leader，返回

服务端 Handle(ctx, body):
  → 解码 req → 校验 Controller Leader:
     非 Leader → marshalRedirect(LeaderID, LeaderAddr)
     是 Leader → 分发处理:
       heartbeat         → 更新 leader-local observation / 刷新健康 deadline
                           → 优先读 leader-local HashSlot snapshot 返回版本/表
                           → snapshot miss 时 fallback store 并回填 snapshot
                           → dynamic join mode 下拒绝未知节点绕过 JoinCluster
       runtime_report    → 更新 leader-local runtime observation；FullSync 可激活 Joining 节点
       join_cluster      → 校验 token/version/conflict → Propose(NodeJoin)
                           → 刷新 leader DynamicDiscovery，返回显式 JoinErrorCode 或 nodes + HashSlot table
       list_assignments  → 优先读 leader-local metadata snapshot.Assignments
                           + leader-local HashSlot snapshot（miss 时 fallback store）
                           → metadata snapshot dirty / cold 时 fallback `controllerMeta.ListAssignments`
       list_nodes        → 优先读 leader-local metadata snapshot.Nodes
                           → snapshot dirty / cold 时 fallback `controllerMeta.ListNodes`
                           → client 侧用返回的完整 nodes snapshot 刷新 DynamicDiscovery
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
      list_onboarding_candidates → 读取严格 leader 视图，返回 Active Data 节点当前 Slot/leader 负载和推荐状态
       create_onboarding_plan → 基于当前 leader 状态生成并持久化 planned job；blocked plan 也会保存，供管理后台解释原因
       start_onboarding_job → 校验 planned job 指纹和可执行性，提案 running 状态；若其它 job 已 running 返回 onboarding error code
       list/get_onboarding_job → 读取持久化 job，列表按 manager-facing created_at desc 分页
       retry_onboarding_job → 仅允许 failed job，基于原 target 创建新的 planned job，并保留 retry_of_job_id
```

### 5.9 动态物理 Slot

```
AddSlot(ctx):
  ① 若 `EnableHashSlotMigration=false` → ErrInvalidConfig（该入口会创建 hash-slot 迁移）
  ② 读取 Controller SlotAssignment 作为当前物理 Slot 基准
  ③ 若 HashSlotTable 存在活跃迁移 → ErrInvalidConfig（管理层映射为迁移冲突）
  ④ 选择 max(assignment.SlotID)+1 作为新物理 Slot，复用当前最小 Slot 的 DesiredPeers
  ⑤ 提案 AddSlot；Controller 持久化新 Assignment + Bootstrap task，并生成迁入新 Slot 的 hash-slot migration

RemoveSlot(ctx, slotID):
  ① 若 `EnableHashSlotMigration=false` → ErrInvalidConfig（该入口会创建 hash-slot 迁移）
  ② 若 HashSlotTable 缺失 / slotID 不存在 / 存在任意活跃迁移 / 将移除最后一个物理 Slot → 拒绝
  ③ 提案 RemoveSlot；Controller 生成迁出该 Slot 的 hash-slot migration
  ④ 被移除 Slot 的 Assignment 暂时保留；最后一个迁移 Finalize 后由 Controller 删除 Assignment/Task

SlotIDs()/planner/readiness:
  - 当前物理 Slot 集合以 Controller Assignment / HashSlotTable 为准。
  - InitialSlotCount 只用于初始 HashSlotTable 和 bootstrap fallback，不再作为长期固定 Slot universe。
  - managedSlotsReady 使用 Controller Assignment 集合作为权威输入，不要求 assignment 数量等于 InitialSlotCount。
```

### 5.10 新节点资源分配（Node Onboarding）

```
管理后台 / internal/usecase/management:
  ① GET candidates：查看 Active Data 节点当前 Slot replica / Leader 数，识别新加入但尚未承载资源的节点
  ② POST plan：选择 target_node_id，Controller Leader 生成 durable planned job（含 moves、blocked reasons、plan fingerprint）
  ③ POST jobs/:job_id/start：只启动已审核的 planned job，不重新生成计划
  ④ GET jobs / jobs/:job_id：轮询进度和每个 move 的 running/completed/failed/skipped 状态
  ⑤ POST jobs/:job_id/retry：failed job 才能重试，创建新的 planned job

执行语义:
  - 新节点 Join + Activate 只进入 membership/discovery，不会自动获得 Slot replica/Leader。
  - Onboarding job 通过 Rebalance 任务把选定 Slot replica 串行迁入 target；需要时在 move 完成前把 Slot Leader 转到 target。
  - 同一时间只允许一个 running onboarding job；running 期间普通自动 Rebalance 暂停，避免扩容计划被其它均衡任务打乱。
  - 每个 controller tick 最多提案一个 onboarding command，确保 assignment/task/job 三者状态可追踪、可恢复。
```

## 6. RPC Service IDs

| Service ID | 常量 | 用途 | 文件 |
|---|---|---|---|
| 1 | `rpcServiceForward` | 提案转发到 Leader | forward.go |
| 14 | `rpcServiceController` | Controller 控制面 RPC | codec_control.go |
| 20 | `rpcServiceManagedSlot` | 受管 Slot 操作 RPC | managed_slots.go |

**Controller RPC 操作** (23 种): `heartbeat` / `runtime_report` / `join_cluster` / `list_assignments` / `list_nodes` / `list_runtime_views` / `fetch_observation_delta` / `operator` / `get_task` / `force_reconcile` / `task_result` / `start_migration` / `advance_migration` / `finalize_migration` / `abort_migration` / `add_slot` / `remove_slot` / `list_onboarding_candidates` / `create_onboarding_plan` / `start_onboarding_job` / `list_onboarding_jobs` / `get_onboarding_job` / `retry_onboarding_job`

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
| `ErrOnboardingRunningJobExists` | 已有其它新节点资源分配作业在运行 | onboarding.go |
| `ErrOnboardingPlanNotExecutable` | 审核计划当前不可执行或含 blocked reason | onboarding.go |
| `ErrOnboardingPlanStale` | 审核计划指纹与当前 Controller 状态不一致 | onboarding.go |
| `ErrOnboardingInvalidJobState` | 对 job 当前状态执行了非法操作（如非 failed 重试） | onboarding.go |

## 8. 避坑清单

- **Propose 必须带 HashSlot**: `Propose()` 是兼容旧路径的快捷方式，仅适用于"一个物理 Slot 只有一个 Hash Slot"的场景。一旦 Slot 拥有多个 Hash Slot（AddSlot/Rebalance 后），必须使用 `ProposeWithHashSlot`，否则返回 `ErrHashSlotRequired`。
- **Forward 重试预算有限**: `ProposeWithHashSlot` 内置 Retry 循环，`ForwardRetryBudget`(默认 300ms) 只重试 `ErrNotLeader`。网络分区或全部 peer 不可达时不会无限重试。
- **Controller 观测读语义**: `ListObservedRuntimeViews` 在 leader 上优先读本地 `observationCache`；只有 leader 不可达时才允许降级到本地 `controllerMeta`，且结果可能滞后。
- **Manager 严格一致读语义**: `ListNodesStrict`、`ListSlotAssignmentsStrict`、`ListObservedRuntimeViewsStrict`、`ListTasksStrict`、`GetReconcileTaskStrict` 只接受 controller leader 结果；本地节点若自身就是 leader 可直接读 leader 本地数据，否则必须经 controller client 读取，禁止降级到本地 `controllerMeta`。
- **Manager Slot 日志水位读语义**: `SlotLogStatusOnNode` 只读取目标节点当前 Slot Raft 运行时的 commit/applied watermark，用于运维健康摘要；读取失败应由上层按 Slot 样本降级为 unavailable，不能替代 controller leader strict-read 拓扑来源。
- **Manager recover 必须走 strict assignments**: `RecoverSlotStrict` 使用 `ListSlotAssignmentsStrict` 作为唯一 assignment 来源，避免 manager 写接口因为 fallback 到本地 assignment 状态而在不同节点上看到不同恢复结论。
- **Controller HashSlot 读快路径**: leader 处理 `heartbeat` / `list_assignments` 时优先读 `controllerHost` 持有的 HashSlot snapshot；只有 snapshot cold miss 才会回落到 `controllerMeta.LoadHashSlotTable()`，回填后再继续返回。
- **Controller metadata 读快路径**: leader-local `controllerMetadataSnapshot` 缓存 Nodes / Assignments / Tasks。planner、调和器本地 leader helper、以及 leader 侧 `list_assignments` / `list_nodes` / `list_tasks` / `get_task` 都优先读 clean snapshot；只要 snapshot dirty / cold 就必须回落到 Pebble-backed `controllerMeta`。
- **Membership snapshot 同步到 discovery**: Controller 返回或加载的完整 Nodes snapshot 会写入现有 `DynamicDiscovery` 实例；observation 增量必须先合并 follower 本地 cache，再用完整 nodes 刷新，不能用 `delta.Nodes` 覆盖，否则会误删未变化节点并触发错误连接驱逐。写入 discovery 时会跳过 `0.0.0.0` / `[::]` 等监听绑定地址，保留静态 `WK_CLUSTER_NODES` 作为可路由基线。
- **Observation hint peers 动态更新**: controller leader 的 metadata snapshot reload 会用 Data 且非 Rejected 的节点刷新 observation hint 目标，并保留静态配置 peers；Joining 节点因此能收到 full-sync hint 并完成激活。
- **节点健康改为 deadline 驱动**: steady-state 不再由 `controllerTickOnce()` 提案 `EvaluateTimeouts`；leader 本地 `nodeHealthScheduler` 只在 Alive/Suspect/Dead 边沿变化时提案 `NodeStatusUpdate`。
- **节点健康 mirror 只反映 committed state**: `nodeHealthScheduler` 对 repeated Alive observation 优先读本地 durable node mirror；mirror miss 才 `GetNode()`。mirror 通过 leader change 全量 reload 和 committed command 增量 refresh 维护，不直接信任 proposal payload。
- **NodeStatus 观察链路有且仅有两条**: controller leader 通过 committed `NodeStatusUpdate` / operator command 触发 `OnNodeStatusChange`；其他节点则只通过 `SyncObservationDelta()` 里的 `delta.Nodes` diff 触发同一个 hook，避免 app 层维护两套分支逻辑。
- **新 leader 先 warmup 再规划**: leader change 会清空旧 observation，等待 fresh observation 后再恢复 Repair/Rebalance 规划，避免把“暂时未观测到”误判为节点故障。
- **controller leader warmup 会重挂 node-health deadline**: 新 controller leader 读取 metadata snapshot / node mirror 时，不只是恢复 `nodeMirror`，还会基于持久化的 `LastHeartbeatAt` 重新挂回 suspect/dead timer。这样即使故障节点正好是旧 controller leader，dead 检测也不会因为 leader failover 而永久停在 `Alive`。
- **调和器任务执行权**: 并非所有节点都执行任务。`shouldExecuteTask` 逻辑: Bootstrap 优先 Task.TargetNode 执行以均衡初始 Leader；Repair/Rebalance 优先 SourceNode 执行，SourceNode 不可用时由 Leader 执行；其它任务由 DesiredPeers 中最小 alive NodeID 执行。错配会导致任务不执行。
- **源 Slot 保护**: 当 Repair/Rebalance 任务的 SourceNode == 本节点时，即使该 Slot 不在 `desiredLocalSlots` 中，调和器也会保护它不被关闭（`protectedSourceSlots`），否则 changeConfig/RemoveVoter 发送不出去。
- **ensureLocal 三条路径**: 有 HardState → Open；无 HardState+bootstrapAuthorized → Bootstrap；无 HardState+hasRuntimeView → Open 等 Leader 添加。混淆条件会导致 Slot 无法加入集群或重复 Bootstrap。
- **Bootstrap 只在任务执行节点授权时**: `bootstrapAuthorized=true` 仅在 `reconciler.Tick` 中确认 `TaskKindBootstrap` 可运行且本节点是执行节点后才传入。防止非目标节点提前 Bootstrap 同一 Slot。
- **Hash Slot 迁移仅 Source Leader 执行**: `shouldExecuteHashSlotMigration` 检查本节点是否是 source Slot 的 Leader。非 Leader 节点会跳过迁移操作。Leader 切换后迁移自然转移到新 Leader。
- **Delta 转发由 durable outbox 兜底**: `forwardHashSlotDelta` 是 live best-effort 重试并会在 Cluster 停止时退出；迁移期间的恢复语义依赖 source fsm 同批写入的 durable outbox，以及 observer 后续 replay/ack，而不是等待后台 goroutine 全部发送完毕。
- **Controller 迁移命令必须带完整 identity**: Advance / Finalize / Abort 必须携带当前 migration 的 source 和 target；source/target 缺失或不匹配按 stale no-op 处理，避免旧命令修改同一 hashSlot 的新迁移。
- **HashSlotTable 不覆盖活跃迁移**: 同一 hashSlot 已有 active migration 时，新的 StartMigration 不会替换原 migration；需要先 finalize / abort 旧迁移。
- **pendingTaskReport 防重复上报**: 任务执行完成后如果 `reportTaskResult` RPC 失败，会暂存为 `pendingTaskReport`，下一轮 Tick 重试上报。如果 Controller 侧任务已变更（identity 不匹配），旧结果会被丢弃。
- **ControllerClient Leader 探测有个体超时**: `call()` 对每个 peer 设置独立的 `controllerRequestTimeout`，避免一个慢 peer 耗尽整个重试预算。
- **observeOnce 容忍 SyncAssignments 失败**: 即使 `SyncAssignments` 返回错误，只要本地有缓存的 assignments 且错误是可降级的，仍会触发 `ApplyAssignments`。保证网络抖动时调和不停滞。
- **运行时状态机注册**: `newStateMachine` 创建状态机后立即调用 `registerRuntimeStateMachine`，使后续 `updateRuntimeHashSlotTable` 能推送最新 hash slot 集合。漏注册会导致迁移后状态机不知道自己拥有哪些 hash slot。
- **Config 校验**: `HashSlotCount >= InitialSlotCount` 是硬性约束；`HashSlotCount > 1` 时必须提供 `NewStateMachineWithHashSlots` 工厂函数；`ControllerReplicaN` 和 `SlotReplicaN` 不能超过节点数。
- **新节点加入不等于资源分配**: dynamic join + activation 只让节点进入 Active Data membership；要承载 Slot replica/Leader，必须通过 manager onboarding job 显式 plan/start。
- **Onboarding API 不走本地降级**: 候选、计划、启动、列表、详情、重试都必须由 controller leader 的严格视图处理，避免不同节点基于滞后 metadata 创建冲突计划。
- **Onboarding 执行与普通 Rebalance 隔离**: running job 存在时 `controllerTickOnce()` 先推进 onboarding；未推进时才运行 planner，并设置 PauseRebalance/LockedSlots，防止普通 Rebalance 抢占 onboarding Slot。
