# `pkg/cluster` 包重构方案

> **目标**：以"高性能 + 设计优雅性"为核心，重构 `pkg/cluster` 集群胶水层，将当前 ~3,400 LOC 的"上帝对象"拆解为职责单一、可独立测试、低开销的组件集合。

> **范围**：仅重构 `pkg/cluster/` 包内部实现与对外接口语义；不触及 `controller/`、`slot/`、`channel/`、`transport/` 等下层包；不变更现有持久化格式。

> **不破坏的承诺**：包对外的功能集（路由、转发、托管 Slot、Controller 操作）行为完全等价；上层 `internal/app/build.go` 仅需更新少量类型/方法签名即可继续工作；持久化数据可平滑升级。

---

## 1. 现状评估

### 1.1 文件构成（实现层 16 文件 / 3,420 LOC）

| 文件 | LOC | 职责（当前） |
|------|-----|--------------|
| `cluster.go` | 909 | **上帝对象**：状态、生命周期、Server/Pool 装配、Controller Raft 启动、observation loop、Controller RPC handler、Propose、各类 Getter |
| `managed_slots.go` | 555 | 托管 Slot RPC 协议 + 编解码 + 本地/远程操作 + Catch-up / Leader 迁移 + 全局测试 hook |
| `agent.go` | 440 | Heartbeat、SyncAssignments、ApplyAssignments、reconcile 任务执行编排、pendingTaskReports |
| `controller_client.go` | 278 | Controller RPC client（JSON 编码 + Leader 缓存 + 重试） |
| `operator.go` | 251 | Drain / Resume / Recover / ForceReconcile / retryControllerCommand |
| `config.go` | 181 | Config + 校验 + 默认值 |
| `assignment_cache.go` | 82 | 分配快照缓存（RWMutex） |
| `codec.go` | 71 | Forward RPC 二进制 codec |
| `forward.go` | 62 | Forward to leader 调用 + handler |
| `router.go` | 47 | Key -> Slot 哈希 + LeaderOf |
| `readiness.go` | 40 | observation 常量 + buildRuntimeView |
| `static_discovery.go` | 33 | 静态 Discovery |
| `transport.go` | 30 | raftTransport 适配器 |
| `discovery.go` | 14 | Discovery interface |
| `errors.go` | 13 | 包错误 |

### 1.2 核心痛点

| # | 痛点 | 影响 | 严重度 |
|---|------|------|--------|
| **P1** | `Cluster` struct 是上帝对象，单结构 49 字段 + ~60 个方法，把 transport/server/pool/runtime/controller/agent/router/cache/peers/lifecycle 全部塞在一起 | 难以单元测试、字段共享导致并发风险、修改一处影响全局、新人理解成本高 | 高 |
| **P2** | Agent / ControllerClient / ManagedSlot 都通过 `*Cluster` 反向引用回主对象，形成循环耦合 | 任意子组件无法独立 mock；测试必须构造完整 Cluster；改造一处 ripple effect | 高 |
| **P3** | Controller RPC 与 Managed Slot RPC 全部使用 `encoding/json`（hot path 包含 Heartbeat、ListAssignments、Status、ChangeConfig） | JSON 编解码 CPU 与 GC 开销显著（vs 已有的二进制 forward codec） | 高 |
| **P4** | `ApplyAssignments` 串行处理：list-nodes → list-views → 串行 ensureLocal → 串行 getTask → 串行执行；任意一个 RPC 慢则全队伍卡住 | Reconcile 延迟高、单点慢会拖垮整个 200ms tick | 中 |
| **P5** | 重试逻辑重复实现：`retryControllerCommand`、`retryControllerCall`、`changeSlotConfig`、`transferSlotLeadership`、`waitForManagedSlotLeader` 各写一份 | 重试策略不一致（间隔、退避、可重试错误集合都不同），bug 易藏匿 | 中 |
| **P6** | 关键超时硬编码：`controllerObservationInterval=200ms`、`controllerRequestTimeout=2s`、`managedSlotLeaderWaitTimeout=5s`、`managedSlotCatchUpTimeout=5s`、`managedSlotLeaderMoveTimeout=5s`、内部 `time.Sleep(50ms)` | 不同部署形态（嵌入式 / 大集群）需要不同节奏，目前无法调；测试无法加速 | 中 |
| **P7** | 测试 hook 是包级 `var` + 三套独立 mu/hook：`managedSlotExecutionTestHook`、`managedSlotStatusHooks`、`managedSlotLeaderHooks` | 全局可变状态阻碍并行测试；setter 返回 reset 函数容易遗漏 | 中 |
| **P8** | 包对外暴露的全是具体类型 `*Cluster`，没有 interface 边界；`internal/app` 直接持有 `*Cluster` | 上层无法 mock，集成测试只能跑全量 cluster；与"组合根"原则有冲突 | 中 |
| **P9** | observation loop 把 Heartbeat / SyncAssignments / ApplyAssignments / controllerTickOnce 串成单 goroutine 单 ticker | 任意一步阻塞整 loop；无法独立调速；无观测 | 中 |
| **P10** | `runtimePeers` 是另一个 `map+RWMutex`，与 `assignmentCache` 维护同类信息但不共享 | 双源数据可能漂移；并发读写两层锁 | 低 |
| **P11** | 缺少运行时观测：无 metric / event 钩子记录 Controller RPC 延迟、reconcile 耗时、forward 重试次数 | 生产排障困难 | 低 |
| **P12** | `Cluster` 没有 `String()` / `Snapshot()` 类调试入口，遇到状态异常只能从 log 推断 | 排障效率低 | 低 |

### 1.3 不需要动的部分（已经做得不错）

- `router.go`：CRC32 路由 + LeaderOf 委托 runtime，逻辑干净。
- `forward.go` + `codec.go`：二进制 forward 协议是包内最佳实践范本，本次重构会向它看齐。
- `static_discovery.go` + `discovery.go`：接口清晰、实现简单。
- `transport.go`：只做 raftpb 与 transport.Client 之间的适配，单一职责。
- `config.go` 校验逻辑：完整、错误信息有上下文。

---

## 2. 设计目标与原则

| 目标 | 落地方式 |
|------|----------|
| **G1 · 单一职责** | `Cluster` 退化为"门面 + 编排"，每条职责线落到独立组件 |
| **G2 · 接口边界** | 对外通过 `cluster.API` 接口提供；上层 `internal/app` 持有接口而非具体类型 |
| **G3 · 高性能 RPC** | 移除 hot-path JSON，所有内部 RPC（Controller / ManagedSlot）改用与 forward 同源的二进制 codec |
| **G4 · 可注入超时** | `Config.Timeouts` 子结构体集中所有时间常量，默认值保持现状以零回归启动 |
| **G5 · 并发结构正交** | observation loop 拆为独立 component；reconcile 路径由 `Reconciler` 拥有完整状态机 |
| **G6 · 无全局测试 hook** | 测试通过构造小组件直接验证；保留对外的 `Hook` 接口仅限 integration test 注入 |
| **G7 · 可观测性钩子** | 暴露 `Observer` 接口（onProposeForward / onControllerCall / onReconcileTask 等），默认 noop，便于 metric/log 接入 |
| **G8 · 零数据迁移** | 不变更任何 Pebble / RaftLog / 持久化字段，纯结构层重构 |

---

## 3. 目标包结构

> **设计取舍**：不引入 sub-package（保留扁平 `pkg/cluster/`），避免 import 路径膨胀；通过文件 + 类型边界实现组件分离。文件命名遵循"组件名 = 文件名 = 主类型名"。

```
pkg/cluster/
├── api.go                  # NEW · 对外接口 cluster.API（被 internal/app 持有）
├── cluster.go              # 门面：组合根 + Start/Stop + 调用转发；目标 < 300 LOC
├── config.go               # 既有 + 新增 Timeouts/Observer 子结构
├── errors.go               # 既有
│
├── router.go               # 既有（基本不变）
├── discovery.go            # 既有
├── static_discovery.go     # 既有
│
├── transport_glue.go       # 原 transport.go + raftTransport 适配
├── codec.go                # 既有 forward codec（保留）
├── codec_control.go        # NEW · Controller RPC 二进制 codec（替代 JSON）
├── codec_managed.go        # NEW · ManagedSlot RPC 二进制 codec（替代 JSON）
│
├── controller_client.go    # 瘦身：仅 Leader 缓存 + 多 peer fan-out + 二进制 codec
├── controller_handler.go   # NEW · Controller RPC server-side handler（从 cluster.go 抽出）
│
├── slot_manager.go         # NEW · 托管 Slot 生命周期（ensure/open/bootstrap/close）
├── slot_handler.go         # NEW · 托管 Slot RPC handler（从 managed_slots.go 抽出）
├── slot_executor.go        # NEW · reconcile 任务的步骤执行器（AddLearner→Promote→Transfer→Remove）
│
├── agent.go                # 瘦身：Heartbeat / SyncAssignments / ApplyAssignments 编排
├── observer.go             # NEW · observation loop（独立 goroutine + 可控 ticker）
├── reconciler.go           # NEW · reconcile 决策 + 任务调度（从 agent.go 抽出）
│
├── retry.go                # NEW · 统一可重试错误集合 + 退避策略
├── assignment_cache.go     # 既有 + 增加 PeersForLocalSlots() 等便捷方法
├── runtime_state.go        # NEW · 整合 runtimePeers + slot snapshot，单一真相源
│
├── operator.go             # 瘦身：Drain / Resume / Recover / ForceReconcile（统一走 retry.go）
├── readiness.go            # 既有 + buildRuntimeView 移入 reconciler.go 时保留 helper
│
└── *_test.go               # 全部既有测试 + 新增组件级单元测试
```

**LOC 预期**：实现层维持在 3,400 LOC 上下（拆分不会增加总量；JSON→binary 反而省百行）。

---

## 4. 组件拆解与职责契约

### 4.1 `cluster.API`（新增 · 对外接口）

```go
// API 是 cluster 包对外暴露的稳定接口。internal/app 与所有上层调用方
// 应当持有 API 而非 *Cluster，便于 mock 与依赖反转。
type API interface {
    // 基础识别
    NodeID() multiraft.NodeID
    IsLocal(nodeID multiraft.NodeID) bool

    // 路由 & 共识入口
    SlotForKey(key string) multiraft.SlotID
    LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
    Propose(ctx context.Context, slotID multiraft.SlotID, cmd []byte) error

    // Slot 元信息
    SlotIDs() []multiraft.SlotID
    PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID
    WaitForManagedSlotsReady(ctx context.Context) error

    // Controller 操作
    ListSlotAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error)
    ListObservedRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, error)
    GetReconcileTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error)
    ForceReconcile(ctx context.Context, slotID uint32) error
    MarkNodeDraining(ctx context.Context, nodeID uint64) error
    ResumeNode(ctx context.Context, nodeID uint64) error
    TransferSlotLeader(ctx context.Context, slotID uint32, nodeID multiraft.NodeID) error
    RecoverSlot(ctx context.Context, slotID uint32, strategy RecoverStrategy) error

    // 共享传输（保留 escape hatch；后续考虑收敛到具名注册接口）
    Server() *transport.Server
    RPCMux() *transport.RPCMux
    Discovery() Discovery
    RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error)
}

// 编译期断言
var _ API = (*Cluster)(nil)
```

### 4.2 `Cluster`（瘦身 · 门面）

**职责**：
1. 持有所有组件实例（依赖注入容器）
2. `Start()` / `Stop()` 顺序编排
3. 把 API 调用转发给对应组件（绝大多数方法 1~3 行）

**字段**（目标 ≤ 15）：
```go
type Cluster struct {
    cfg     Config
    logger  wklog.Logger
    obs     ObserverHooks  // 默认 noop

    // 网络层
    transport *transportLayer    // server/pool/clients/discovery 聚合
    runtime   *multiraft.Runtime
    router    *Router

    // 控制面
    controller *controllerHost     // controllerMeta + controllerSM + controller raft service（仅本地是 controller peer 时）
    cclient    *controllerClient   // 远端 controller RPC 客户端

    // 数据面
    slotMgr    *slotManager        // 托管 Slot 生命周期
    runState   *runtimeState       // runtimePeers 单一真相源
    assignCache *assignmentCache

    // 编排
    agent      *agent
    reconciler *reconciler
    observer   *observerLoop

    stopped    atomic.Bool
}
```

**Start 顺序**（语义不变）：
1. `transport.Start()` → server + pool 就绪
2. `controllerHost.Start()`（仅本地 controller peer）
3. `multiraft runtime` 创建
4. `cclient.Start()`（远端 client）
5. 注册 RPC handler：forward / controller / managedSlot
6. `observer.Start()`（goroutine）
7. seed legacy slots（无 controller 模式）

### 4.3 `transportLayer`（新增 · 网络聚合）

```go
type transportLayer struct {
    server      *transport.Server
    rpcMux      *transport.RPCMux
    raftPool    *transport.Pool
    rpcPool     *transport.Pool
    raftClient  *transport.Client
    fwdClient   *transport.Client
    discovery   *StaticDiscovery
}

func newTransportLayer(cfg Config) *transportLayer
func (t *transportLayer) Start(addr string) error
func (t *transportLayer) Stop()
```

**收益**：把 cluster.go 中分散的 7 个网络字段 + 3 个启动方法合并；后续替换 transport 实现只需改这一处。

### 4.4 `controllerHost`（新增 · 本地 Controller Raft 容器）

```go
type controllerHost struct {
    meta    *controllermeta.Store
    raftDB  *raftstorage.DB
    sm      *slotcontroller.StateMachine
    service *controllerraft.Service
}

func newControllerHost(cfg Config, t *transportLayer) (*controllerHost, error)
func (h *controllerHost) Start(ctx context.Context) error
func (h *controllerHost) Stop()
func (h *controllerHost) IsLeader(local multiraft.NodeID) bool
func (h *controllerHost) LeaderID() multiraft.NodeID
func (h *controllerHost) Propose(ctx context.Context, cmd slotcontroller.Command) error
func (h *controllerHost) SnapshotPlannerState(ctx context.Context) (slotcontroller.PlannerState, error)
```

**收益**：把 `startControllerRaftIfLocalPeer` + `controllerTickOnce` + `snapshotPlannerState` + Stop 时的 4 个 close 全部聚合；`Cluster` 只需 `if h := cluster.controller; h != nil { ... }`。

### 4.5 `controllerClient`（瘦身）

**改动**：
- 保留：Leader 缓存、多 peer fan-out、tried 集合
- 移除：JSON marshal/unmarshal，改用 `codec_control.go` 二进制编解码
- 移除：内联 retry（统一走 `retry.go`）
- 新增：构造时接收 `*transportLayer.fwdClient`（不再依赖 `*Cluster.RPCService`）

### 4.6 `controller_handler.go`（新增 · server 端）

把 `cluster.handleControllerRPC` (909-line cluster.go 中 ~150 行) 迁出：

```go
type controllerHandler struct {
    host    *controllerHost
    localID multiraft.NodeID
    timeout time.Duration
}

func (h *controllerHandler) Handle(ctx context.Context, body []byte) ([]byte, error)
```

handler 不再依赖 `*Cluster`，只依赖 controllerHost。每个 case 用二进制 codec，不再 `json.Marshal`。

### 4.7 `slotManager`（新增 · 替代 managed_slots.go 的本地侧）

```go
type slotManager struct {
    runtime    *multiraft.Runtime
    runState   *runtimeState
    storage    func(slotID multiraft.SlotID) (multiraft.Storage, error)
    sm         func(slotID multiraft.SlotID) (multiraft.StateMachine, error)
    timeouts   Timeouts
}

// 本地操作
func (m *slotManager) EnsureLocal(ctx context.Context, slotID multiraft.SlotID, desiredPeers []uint64, hasView, bootstrap bool) error
func (m *slotManager) ChangeConfigLocal(ctx context.Context, slotID multiraft.SlotID, ch multiraft.ConfigChange) error
func (m *slotManager) TransferLeaderLocal(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error
func (m *slotManager) StatusLocal(slotID multiraft.SlotID) (managedSlotStatus, error)
func (m *slotManager) CloseLocal(ctx context.Context, slotID multiraft.SlotID) error
```

### 4.8 `slot_handler.go`（新增 · server 端）

把 `handleManagedSlotRPC` 迁出：

```go
type slotHandler struct {
    mgr *slotManager
}
func (h *slotHandler) Handle(ctx context.Context, body []byte) ([]byte, error)
```

使用 `codec_managed.go` 二进制 codec 代替 JSON。

### 4.9 `slotExecutor`（新增 · reconcile 步骤执行）

把 `executeReconcileTask` + 相关 helper（`changeSlotConfig` / `transferSlotLeadership` / `waitForManagedSlotCatchUp` / `ensureLeaderMovedOffSource` / `currentManagedSlotLeader` / `managedSlotStatusOnNode`）汇集到一个 struct：

```go
type slotExecutor struct {
    mgr      *slotManager
    rpc      *transportLayer
    router   *Router
    timeouts Timeouts
    obs      ObserverHooks
    retry    Retry
}

// 五步法实现：AddLearner → CatchUp → PromoteLearner → CatchUp → MoveLeaderOffSource → RemoveVoter
func (e *slotExecutor) Execute(ctx context.Context, task assignmentTaskState) error
```

测试 hook 通过 `ObserverHooks.OnReconcileStep` 与构造时注入的可替换函数实现，**消除 3 个全局 mutex hook**。

### 4.10 `agent`（瘦身）

只保留三个动作的"编排者"：

```go
type agent struct {
    cclient    controllerAPI
    cache      *assignmentCache
    runState   *runtimeState
    reconciler *reconciler
    nodeID     multiraft.NodeID
    obs        ObserverHooks
}

func (a *agent) HeartbeatOnce(ctx context.Context) error
func (a *agent) SyncAssignments(ctx context.Context) error
func (a *agent) ApplyAssignments(ctx context.Context) error  // 委派给 reconciler.Plan + Execute
```

### 4.11 `reconciler`（新增 · 状态机）

```go
type reconciler struct {
    mgr      *slotManager
    executor *slotExecutor
    cclient  controllerAPI
    cache    *assignmentCache
    runState *runtimeState
    nodeID   multiraft.NodeID
    obs      ObserverHooks
    pending  pendingTaskReports
}

// 一个 Tick 内的完整决策 + 执行
func (r *reconciler) Tick(ctx context.Context) error
```

`reconciler.Tick` 内部把 `ApplyAssignments` 中的 6 个串行阶段拆为命名清晰的私有方法：

1. `loadAssignments` / `loadNodes` / `loadViews` / `loadTasks` —— 可受控并行（errgroup，超时受 Timeouts.ControllerRequest 约束）
2. `ensureLocalSlots(...)` —— 读 cache，调 `slotManager.EnsureLocal`
3. `closeOrphanSlots(...)` —— 关掉不再属于本地的 slot
4. `flushPendingReports(...)` —— 把上一轮失败的 ReportTaskResult 重发
5. `selectExecutableTasks(...)` —— 决定哪些 task 由本节点执行（即原 `shouldExecuteTask`）
6. `executeAndReport(...)` —— 调 `slotExecutor.Execute` + Report

### 4.12 `observerLoop`（新增 · 独立 goroutine）

```go
type observerLoop struct {
    agent       *agent
    controller  *controllerHost  // 可空
    nodeID      multiraft.NodeID
    interval    time.Duration
    obs         ObserverHooks
    stop        chan struct{}
    done        chan struct{}
}

func (l *observerLoop) Start(ctx context.Context)
func (l *observerLoop) Stop()
```

阶段拆分：
- `tickHeartbeat()` —— Heartbeat（自身节奏：interval）
- `tickReconcile()` —— Sync + Apply（自身节奏：interval，可与 heartbeat 错开）
- `tickPlanner()` —— controllerTickOnce（仅本地是 controller leader 时）

未来可演进为三个独立 ticker；本次重构先保持单 goroutine 但代码已就绪。

### 4.13 `runtimeState`（新增 · 单一真相源）

替代散落的 `runtimePeers map + RWMutex`：

```go
type runtimeState struct {
    mu    sync.RWMutex
    peers map[multiraft.SlotID][]multiraft.NodeID
}

func (s *runtimeState) Set(slotID multiraft.SlotID, peers []multiraft.NodeID)
func (s *runtimeState) Get(slotID multiraft.SlotID) ([]multiraft.NodeID, bool)
func (s *runtimeState) Delete(slotID multiraft.SlotID)
func (s *runtimeState) Snapshot() map[multiraft.SlotID][]multiraft.NodeID
```

并发写入与 `assignmentCache` 解耦：assignmentCache 是"期望状态"，runtimeState 是"实际本地拓扑"。

### 4.14 `retry.go`（新增 · 统一重试）

```go
type Retryable func(error) bool

type Retry struct {
    Interval time.Duration
    MaxWait  time.Duration  // 总预算
    IsRetryable Retryable
}

func (r Retry) Do(ctx context.Context, fn func(context.Context) error) error
```

**默认策略**：

| 场景 | Interval | MaxWait | IsRetryable |
|------|---------|---------|-------------|
| Controller RPC | 100ms | `Timeouts.ControllerLeaderWait` (10s) | NotLeader / NoLeader / DeadlineExceeded |
| Forward Propose | 50ms | `Timeouts.ForwardRetryBudget` (300ms) | NotLeader |
| ChangeConfig | 50ms | `Timeouts.ConfigChangeRetryBudget` (300ms) | NotLeader |
| Transfer Leader | 50ms | `Timeouts.LeaderTransferRetryBudget` (300ms) | NotLeader |

把 `retryControllerCommand` / `retryControllerCall` / 内联循环全部替换为 `retry.Do(ctx, fn)`。

### 4.15 二进制 RPC codec（替代 JSON）

#### Controller RPC 协议（codec_control.go）

```
Request:
+--------+--------+--------+--------+
| ver(1) | kind(1)| slotID(4)      |
+--------+--------+--------+--------+
| payload(varint length-prefixed)  |
+----------------------------------+

Kind enum:
  0x01 Heartbeat
  0x02 ListAssignments
  0x03 ListNodes
  0x04 ListRuntimeViews
  0x05 Operator
  0x06 GetTask
  0x07 ForceReconcile
  0x08 TaskResult

Response:
+--------+--------+--------+--------+
| ver(1) | flags  | leaderID(8)    |
+--------+--------+--------+--------+
| payload(varint length-prefixed)  |
+----------------------------------+

flags bit0 = NotLeader
flags bit1 = NotFound
```

每种 payload 用 protobuf-style varint encoding（与 raftpb 复用 marshaler 风格），或者直接用 `gogoproto`/`vtprotobuf` 生成的代码。

**取舍**：第一版使用手写 binary codec（与 codec.go 风格一致，无新依赖）；如未来需要跨语言再升级到 protobuf。

#### ManagedSlot RPC 协议（codec_managed.go）

```
Request:
+--------+--------+--------+--------+--------+
| ver(1) | kind(1)| slotID(4)      | nodeID(8) |
+--------+--------+--------+--------+--------+
| changeType(1)                              |
+--------------------------------------------+

Kind enum:
  0x01 Status
  0x02 ChangeConfig
  0x03 TransferLeader

Response:
+--------+--------+--------+--------+
| ver(1) | flags  | leaderID(8)    |
+--------+--------+--------+--------+
| commitIndex(8)                   |
| appliedIndex(8)                  |
| messageLen(varint)+message       |
+----------------------------------+

flags bit0 = NotLeader
flags bit1 = NotFound
flags bit2 = Timeout
```

### 4.16 `Timeouts`（新增 · 集中超时）

```go
type Timeouts struct {
    ControllerObservation     time.Duration  // 200ms（observation loop 节拍）
    ControllerRequest         time.Duration  // 2s（单次 controller RPC 超时）
    ControllerLeaderWait      time.Duration  // 10s（重试预算）
    ForwardRetryBudget        time.Duration  // 300ms
    ManagedSlotLeaderWait     time.Duration  // 5s
    ManagedSlotCatchUp        time.Duration  // 5s
    ManagedSlotLeaderMove     time.Duration  // 5s
    ConfigChangeRetryBudget   time.Duration  // 300ms
    LeaderTransferRetryBudget time.Duration  // 300ms
}

func (t *Timeouts) applyDefaults()
```

`Config.Timeouts` 字段；`Config.applyDefaults` 调 `Timeouts.applyDefaults`。所有原硬编码常量替换为 `c.cfg.Timeouts.X`。

### 4.17 `ObserverHooks`（新增 · 可观测性）

```go
type ObserverHooks struct {
    OnControllerCall    func(kind string, dur time.Duration, err error)
    OnReconcileStep     func(slotID uint32, step string, dur time.Duration, err error)
    OnForwardPropose    func(slotID uint32, attempts int, dur time.Duration, err error)
    OnSlotEnsure        func(slotID uint32, action string, err error)  // open/bootstrap/close
    OnLeaderChange      func(slotID uint32, from, to multiraft.NodeID)
}
```

默认全 nil；零成本（每次调用前 `if h.OnX != nil` 判断）。`internal/log` 或后续 metric 包可挂载。

---

## 5. 数据流对比

### 5.1 当前 Propose 路径

```
client → Cluster.Propose
       ├─ router.LeaderOf
       ├─ if local: runtime.Propose → future.Wait
       └─ else: cluster.forwardToLeader → fwdClient.RPCService → 远端 handleForwardRPC
```

### 5.2 重构后 Propose 路径

```
client → API.Propose (cluster facade, 4 lines)
       └─ forwarder.Propose (新独立组件，可单测)
          ├─ retry.Do (统一重试策略)
          │  └─ router.LeaderOf
          │  └─ if local: runtime.Propose
          │  └─ else: transport.fwdClient.RPCService (forward codec, 现成)
          └─ obs.OnForwardPropose(slotID, attempts, dur, err)
```

### 5.3 当前 reconcile tick

```
observer (goroutine, 200ms ticker)
  → cluster.observeOnce
     → agent.HeartbeatOnce        ← JSON RPC
     → agent.SyncAssignments      ← JSON RPC + retry
     → agent.ApplyAssignments     ← 6 个串行阶段, 7 处 JSON RPC, 2 处嵌套 retry
  → cluster.controllerTickOnce
     → controller.Propose (本地)
     → snapshotPlannerState (4 个串行 store call)
     → planner.NextDecision
     → controller.Propose (本地)
```

### 5.4 重构后 reconcile tick

```
observerLoop (goroutine, 200ms ticker, Timeouts.ControllerObservation)
  → agent.HeartbeatOnce          ← binary RPC
  → reconciler.Tick
     → loadAssignments|loadNodes|loadViews|loadTasks (errgroup, 并行, 受 ControllerRequest 约束)
     → ensureLocalSlots
     → closeOrphanSlots
     → flushPendingReports
     → selectExecutableTasks
     → executeAndReport
        → slotExecutor.Execute (5-step state machine)
        → cclient.ReportTaskResult (binary RPC)
  → controllerHost.Tick (仅本地 leader)
     → host.Propose(EvaluateTimeouts)
     → host.SnapshotPlannerState (errgroup 并行 4 store calls)
     → planner.NextDecision
     → host.Propose(AssignmentTaskUpdate)
```

每一步可独立 mock + 单测；通过 `Timeouts.ControllerRequest` 调速；任意一步耗时通过 `obs.OnControllerCall` 输出。

---

## 6. 并发模型与锁分析

| 资源 | 当前 | 重构后 | 说明 |
|------|------|--------|------|
| `runtimePeers map` | `runtimePeersMu sync.RWMutex` | `runtimeState.mu sync.RWMutex` | 仅命名变化，迁出到独立 struct |
| `assignmentCache` | `mu sync.RWMutex` | 不变 | 已经是 RWMutex；读多写少 OK |
| `controllerClient.leader` | `mu sync.RWMutex` | 改为 `atomic.Uint64` | 单 uint64 字段，atomic 比 mutex 更快 |
| `pendingTaskReports.m` | `mu sync.Mutex` | 不变 | 写多读少，Mutex 合理 |
| `managedSlot*Hooks` | 3 套全局 RWMutex | **删除** | 改为构造时注入 |
| `Cluster.stopped` | `atomic.Bool` | 不变 | 已经是 atomic |
| 新增 `Cluster.observerStop` | chan struct{} | 移入 `observerLoop` | 隔离 |

**总计**：从 5 个公共可变状态点 + 3 个全局 hook，减为 4 个公共可变状态点 + 0 个全局 hook，且 controllerClient leader 字段从 RWMutex 降到 atomic.Uint64（hot path 收益明显，因为每个 RPC call 都要读 leader）。

---

## 7. 性能预估

| 路径 | 当前 | 重构后 | 收益估算 |
|------|------|--------|----------|
| Heartbeat（200ms 一次） | JSON marshal AgentReport + RPC + JSON unmarshal | binary codec | 单次省 ~5μs CPU + ~800B 临时分配 |
| ListAssignments（200ms 一次或更频繁） | JSON marshal + RPC + JSON unmarshal `[]SlotAssignment` | binary codec + 复用 buffer | 1024 slots 场景下省 ~50μs + ~50KB 分配 |
| ManagedSlot Status RPC（catch-up 期间高频） | JSON | binary | 单次省 ~3μs |
| Propose（hot path） | 1 次 mu RLock 取 leader + retry sleep | 1 次 atomic.Load + retry.Do（同节拍） | 取 leader 的 RWMutex 改 atomic 后省 ~30ns/call；高 QPS 场景累计可观 |
| reconcile Tick load 阶段 | 4 个 store call 串行（共 ~8ms 典型值） | errgroup 并行（~2ms） | 单 tick 节省 ~6ms |
| ApplyAssignments 串行 RPC | 串行 7 RPC 最坏 ~14ms | 内部 fan-out + retry 并行最差 ~5ms | 在 reconcile 高峰可降 60% |

> 数值为基于当前代码静态分析的合理估算；落地后由 `pkg/cluster/stress_test.go` 实际验证。

---

## 8. 接口兼容性 & 上层影响

### 8.1 `internal/app/build.go` 改动

```go
// 当前
app.cluster, err = raftcluster.NewCluster(...)
// 之后所有地方持有 *raftcluster.Cluster

// 重构后
var clusterAPI raftcluster.API
clusterAPI, err = raftcluster.NewCluster(...)
// 上层尽量持有 raftcluster.API（少数需要 Server() / RPCMux() 注册的地方仍可用）
```

需要修改的点（grep `raftcluster.Cluster` ≈ 10 处，`*raftcluster.Cluster` ≈ 6 处）：
- `internal/app/build.go`：声明改为 `raftcluster.API`
- `internal/access/node`：构造函数参数改为 `raftcluster.API`
- `presenceRouter`：内含 `*raftcluster.Cluster` → `raftcluster.API`
- `multinode_integration_test.go`：保留对 `*Cluster` 的引用（测试场景下持有具体类型 OK）

### 8.2 持久化兼容

- Controller RPC / ManagedSlot RPC 是**点对点同步消息**，无持久化记录。
- 升级路径：
  - 阶段 A：双协议并存（旧 JSON handler 仍注册，新 binary handler 注册到不同 service ID）。
  - 阶段 B：所有节点滚动重启完毕后切换 client 默认 codec 为 binary。
  - 阶段 C：删除旧 JSON handler。
- **简化方案**：因为 cluster 包内部通信都是同步 RPC、且每次部署集群整体替换，第一版可直接换 codec，不做双协议过渡。

### 8.3 测试兼容

- 既有 `cluster_test.go` / `controller_client_internal_test.go` / 等需要按类型签名调整。
- 既有 `cluster_integration_test.go`（`//go:build integration`）保持行为不变；等价性由它兜底。
- 新增 5 个组件级单元测试文件（见第 9 节）。

---

## 9. 实施批次（10 个独立可编译/可测的小步）

> **黄金法则**：每批次结束 `go build ./... && go test ./pkg/cluster/...` 全部通过；每批次一个 commit。

### Batch 0 · 基线（0.5 天）
- 创建 `docs/superpowers/plans/2026-04-XX-cluster-package-refactor-plan.md`（本文件副本，写明 owner / status）。
- 运行 `go test ./pkg/cluster/... -count=3` 确认基线绿。
- 用 `pprof` / `bench` 跑 `stress_test.go` 留一份基线数据。

### Batch 1 · `Timeouts` 与 `Config` 拓展（0.5 天）
- 新增 `Timeouts` 子结构 + `applyDefaults`。
- 把 6 处硬编码常量改为 `c.cfg.Timeouts.X` 引用，默认值保持原值。
- 风险：低；测试：`config_test.go` 增加默认值 case。

### Batch 2 · `retry.go` 统一重试器（0.5 天）
- 实现 `Retry.Do`。
- 替换 `retryControllerCommand` / `retryControllerCall` 内部实现，对外签名保持。
- 替换 `changeSlotConfig` / `transferSlotLeadership` / `Propose` 中 3 段内联 for-retry。
- 风险：中（行为等价性）；测试：单元测试验证重试次数 / 退出条件。

### Batch 3 · `runtimeState` 提取（0.5 天）
- 把 `runtimePeersMu` + `runtimePeers` 抽到 `runtimeState`。
- `Cluster` 仅持有 `*runtimeState` 引用；所有 `setRuntimePeers` / `getRuntimePeers` / `deleteRuntimePeers` 改为调用 runtimeState。
- 风险：低；测试：`cluster_test.go` 既有 case 即可。

### Batch 4 · `transportLayer` 聚合（0.5 天）
- 新增 `transportLayer` 包内 struct，把 `server / rpcMux / raftPool / rpcPool / raftClient / fwdClient / discovery` 7 个字段移入。
- `Cluster` 持有 `*transportLayer`；`Server()` / `RPCMux()` / `Discovery()` 等 API 改为转发。
- 风险：低；纯字段迁移。

### Batch 5 · `controllerHost` 提取（1 天）
- 新增 `controllerHost`，迁出 `startControllerRaftIfLocalPeer` / `controllerTickOnce` / `snapshotPlannerState` / Stop 时的 4 个 close。
- `Cluster.controller*` 字段折叠为 `controllerHost *controllerHost`。
- 风险：中；测试：`cluster_test.go` 单节点 controller 流程。

### Batch 6 · `observerLoop` 提取（0.5 天）
- 新增 `observerLoop`，迁出 `startObservationLoop` / `observeOnce`。
- `Cluster.observation*` 三个字段移入 observerLoop。
- 风险：低。

### Batch 7 · `slotManager` + `slot_handler.go` + `codec_managed.go`（1.5 天）
- 把 `managed_slots.go` 拆为：
  - `slot_manager.go`：本地侧 ensure/change/transfer/status/close
  - `slot_handler.go`：RPC handler
  - `codec_managed.go`：binary codec
- 删除全局 `managedSlotStatusHooks` / `managedSlotLeaderHooks`，改为 `slotExecutor` 构造时注入。
- 保留 `SetManagedSlotExecutionTestHook` 接口（兼容 integration test）但内部转发到注入式实现。
- **此 batch 同时引入 binary codec for managed slot RPC**。
- 风险：高；测试：`managed_slots` 相关 unit test + integration test 全跑。

### Batch 8 · `controllerClient` + `controller_handler.go` + `codec_control.go`（1.5 天）
- 把 `cluster.handleControllerRPC` 迁到 `controller_handler.go`。
- 重写 `controllerClient` 为依赖 `transportLayer.fwdClient` + `codec_control`，移除 JSON。
- `controllerClient.leader` 改 `atomic.Uint64`。
- 风险：高；测试：`controller_client_internal_test.go` 全跑 + 新增 codec 单元测试 + integration test。

### Batch 9 · `agent` + `reconciler` + `slotExecutor` 重构（2 天）
- 把 `agent.ApplyAssignments` 拆解为 `reconciler.Tick` 的 6 个有名阶段。
- 把 reconcile 任务执行步骤抽到 `slotExecutor`。
- `loadAssignments|Nodes|Views|Tasks` 用 `errgroup` 并行（受 `Timeouts.ControllerRequest` 约束）。
- 风险：高（这是核心业务路径）；测试：所有现有 reconcile / failover / drain 测试 + integration test。

### Batch 10 · `cluster.API` 接口 + 上层切换（1 天）
- 新增 `api.go` 定义 `cluster.API`。
- `Cluster` 实现该接口（`var _ API = (*Cluster)(nil)`）。
- 修改 `internal/app/build.go` 将持有类型从 `*Cluster` 改为 `API`（保留 `Server()` / `RPCMux()` 等需要具体类型的少数地方）。
- 修改 `presenceRouter` / `accessnode` 等持有点。
- 风险：低（接口本身和实现 1:1 对应）；测试：`internal/app` 全部测试 + `multinode_integration_test.go`。

### Batch 11 · `ObserverHooks` 接入（0.5 天）
- 在 `Config` 增加 `Observer ObserverHooks` 字段，默认零值。
- 在 5 个埋点处插入 `if h := c.obs.OnX; h != nil { h(...) }`。
- 风险：低；不接入任何 metric 系统，仅留 hook。

### Batch 12 · 文档与基准对比（0.5 天）
- 更新 `AGENTS.md` 中 cluster 包目录注释。
- 更新 `docs/wiki/architecture/02-slot-layer.md` 中提到 cluster 内部组件的段落。
- 跑 batch0 同样的 `stress_test.go`，对比基线，附数据进 commit message。

**总耗时估算**：~10 工作日（含测试 + 集成测试），分布在 12 个 commit / PR。

---

## 10. 风险登记

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| **二进制 codec 未覆盖某个 controller RPC 字段** | 中 | 高（线上集群无法通信） | Batch 8 强制对 `slotcontroller.AgentReport`、`SlotAssignment`、`SlotRuntimeView`、`ReconcileTask`、`OperatorRequest` 5 类对象编写 round-trip 单测；用 fuzz / property test 加固 |
| **reconciler 拆分后并发顺序变化导致竞态** | 中 | 高（slot 进入坏状态） | Batch 9 完成后强制运行 `cluster_integration_test.go` 全部用例 + 跑 1 小时压力测试 |
| **删除全局 hook 后 integration test 失败** | 中 | 中 | Batch 7 保留 `SetManagedSlotExecutionTestHook` 兼容入口；内部转发到注入式 hook 列表 |
| **API 接口新增后被反射 / 类型断言滥用** | 低 | 中 | 在 `cluster.API` 注释中明确"接口稳定性约定"，并加 `// Deprecated` 标记任何短期保留的具体类型方法 |
| **重构期间 main 分支有其他变更冲突** | 中 | 中 | 每个 Batch 独立 PR；rebase main；本计划完成前不允许改 cluster 内部布局 |
| **超时改动影响 production 默认表现** | 低 | 高 | Batch 1 默认值与现状完全一致；只在 `*_test.go` 中按需 override |

---

## 11. 开放问题（执行前需澄清）

1. **是否引入 protobuf 依赖？**
   - 倾向：不引入。手写 binary codec 与现有 forward codec 风格统一。
   - 替代：如未来需要跨语言客户端再升级。

2. **`controllerClient.leader` 改为 `atomic.Uint64` 是否需要 `sync/atomic.Pointer[T]`？**
   - 当前 NodeID 是 `uint64`，直接 `atomic.Uint64` 即可。

3. **`Cluster.API` 是否要拆为多个小接口（按职责）？**
   - 倾向：第一版用单一大接口，避免上层多次类型组合；后续若发现某些消费方只需子集再拆。

4. **observerLoop 是否拆为多 goroutine（heartbeat / reconcile / planner 各一个）？**
   - 第一版保持单 goroutine，预留 hook；性能数据出来后决定是否进一步拆分。

5. **是否在本次重构中删除 `seedLegacySlotsIfConfigured`（无 controller 模式）？**
   - 不删；仍有少量测试使用。但在 `Config.applyDefaults` 中明确该路径仅供测试。

---

## 12. 验收标准

1. **行为等价**：所有现有 unit test + integration test (`go test -tags=integration ./pkg/cluster/...` + `./internal/app/...`) 通过，无新增 flake。
2. **结构清晰**：`Cluster` 主结构体字段数 ≤ 15，对外 API 全部走 `cluster.API`。
3. **性能不退化**：`stress_test.go` benchmark 数据相对基线 ≥ 持平；reconcile tick load 阶段实测 ≥ 50% 提速。
4. **零数据迁移**：升级前后任意节点数据可直接切换二进制运行。
5. **无全局可变状态**：`grep "^var " pkg/cluster/*.go` 仅剩错误声明 + `_ API = (*Cluster)(nil)` 类断言。
6. **超时全可配**：`grep -E "(time\\.Second|time\\.Millisecond)" pkg/cluster/*.go` 仅出现在 `config.go` 的默认值与 codec 测试。
7. **可观测性就位**：`ObserverHooks` 5 个 callback 均有埋点，且默认 noop 时无额外开销（benchmark 验证）。
8. **文档同步**：`AGENTS.md` 与 `docs/wiki/architecture/02-slot-layer.md` 已更新；本 plan 移入 `docs/superpowers/plans/` 标 done。

---

## 13. 与既有重构计划的关系

- **不冲突**：本计划仅涉及 `pkg/cluster/` 内部，与 `pkg-restructure-proposal.md` 中"提升 raftcluster/ → cluster/"那一批已经完成的目录调整无重叠。
- **互补**：本计划与 `transport-pipeline-priority-refactor.md` 互不依赖；后者改 `pkg/transport`，前者改 `pkg/cluster`。两者可并行推进。
- **铺路**：本计划完成后，`cluster.API` 接口的引入会让未来"将 channel/log 从 store-proxy 切换到直接调 cluster" 等需求更容易实现。

---

> **状态**：草案 v1，待评审。
> **作者**：Claude (Opus 4.6) on 2026-04-11
> **关联文件**：`pkg/cluster/*.go`、`internal/app/build.go`、`docs/raw/pkg-restructure-proposal.md`
