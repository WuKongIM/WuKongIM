# Cluster Idle CPU Optimization 设计

- 日期：2026-04-22
- 范围：`pkg/cluster` controller 读路径与 reconciliation steady-state 行为
- 关联目录：
  - `pkg/cluster`
  - `pkg/transport`
  - `pkg/controller`
  - `docs/superpowers/plans/2026-04-22-cluster-idle-cpu-optimization.md`

## 1. 背景

本地三节点开发集群在几乎没有业务消息时，`wk-node1/2/3` 仍长期保持约 7%~10% CPU。现场 profiling 与 metrics 表明，主要空耗并不来自消息收发，而来自 steady-state 控制面：

- `reconciler.Tick()` 每轮都会拉 assignments / nodes / runtime views
- 每轮还会对每个 slot 单独执行一次 `get_task`
- 非 leader 节点的大量 controller 读请求会通过 transport RPC 发往 controller leader
- controller leader 自己的 controller 读路径也会经过 self-RPC，而不是直接走本地 handler / snapshot

在当前 10 个 managed slots、200ms controller observation、3 节点部署下，这些周期性读会稳定地产生高频 RPC，即使业务面近乎空闲也无法把 CPU 压下去。

## 2. 目标与非目标

### 2.1 目标

1. 消除 reconciliation 每轮按 slot `get_task` 的 steady-state 风暴
2. 消除 controller leader 对自身 controller service 的 self-RPC
3. 保持现有 controller 读语义不变
4. 保持现有对外 API、配置项、部署方式不变
5. 通过单元/集成测试锁定新的低空耗行为

### 2.2 非目标

- 不调整 observation / tick 默认频率
- 不修改 slot / controller raft 共识参数
- 不引入新的缓存失效协议或订阅式同步机制
- 不改 transport 协议格式
- 不处理 channel/data-plane 的 idle CPU

## 3. 设计概览

### 3.1 `getTask` 优化

当前 `reconciler.loadTasks()` 会并发对每个 assignment 执行一次 `agent.getTask(slotID)`。在 steady-state 无 task 时，这相当于每轮做一次“按 slot 全量 miss 查询”，成本与 slot 数量线性相关。

优化后分两层：

- 批量层：`loadTasks()` 先尝试一次性读取全部 tasks
  - local controller leader：优先 metadata snapshot / local meta
  - 其他节点：优先 `ListTasks()` controller read
- 精确认层：仅当某个 task 真正 runnable 且本轮准备执行时，再对这个 slot 做一次 fresh confirmation

这样 steady-state 无 task 时，请求量从“每轮 N 次 `get_task`”收敛为“每轮 1 次 `list_tasks`”。

### 3.2 controller local fast path

当前 controller client 的所有请求都统一走 `Cluster.RPCService()`，即使目标 node 就是本地 controller leader，也会编码请求、经过 transport client/server/mux，再由 controller handler 解码处理。

优化后，当 controller client 选中的 target 是本地节点时：

- 不再走 transport self-RPC
- 直接调用本地 `controllerHandler.Handle(ctx, body)`
- 继续复用同一套 request/response codec 与 handler 逻辑，避免读路径语义分叉

这样可以去掉 self-RPC 的 socket、queue、mux、goroutine 调度成本，同时保持 controller handler 仍是唯一业务入口。

## 4. 详细设计

### 4.1 Reconciler task loading

- `reconciler.loadTasks()` 改为优先走 `slotAgent.listTasks(ctx)`
- `slotAgent` 新增 `listTasks(ctx)` helper，行为与现有 `listControllerNodes` / `listRuntimeViews` 一致：
  - local leader 且 snapshot warm：直接返回 snapshot tasks
  - 否则尝试 controller client `ListTasks`
  - 若允许 fallback，再回退到 local meta
- `loadTasks()` 只把结果整理为 `map[slotID]task`
- `pendingTaskReport` 仍然优先，避免重拉已经待上报的 task
- 执行前的 fresh confirmation 保留，以保证 runnable task 在真正执行前仍做一次 slot 级确认

### 4.2 Local controller read fast path

- 在 `controllerClient.call()` 中加入 target-local 分支
- 当 `target == cluster.cfg.NodeID` 时：
  - 直接执行 `cluster.handleControllerRPC(ctx, body)`
  - 走与远程一致的编码/解码流程
  - 保持 redirect / not leader / metrics 行为不变
- 只有 target 为远程节点时，才继续走 `cluster.RPCService()` / `transport.Client`

## 5. 风险与控制

### 5.1 风险

- 批量 `ListTasks` 若处理不当，可能改变当前 pending-task replay 优先级
- local fast path 若绕过过多层，可能导致与远程 handler 语义不一致

### 5.2 控制措施

- 批量 task 读取只替换“无 task 的 steady-state miss 路径”，保留执行前 fresh confirmation
- local fast path 仍复用 `encodeControllerRequest/decodeControllerResponse` + `controllerHandler.Handle`
- 为两类优化分别补测试：
  - `reconciler.Tick()` 在无 task steady-state 不再按 slot `getTask`
  - controller client 访问本地 leader 时不会触发 transport RPC，但结果与原路径一致

## 6. 测试策略

1. `pkg/cluster/reconciler_test.go`
   - 新增 test：无 pending report、无 tasks 时，`Tick()` 走一次 `ListTasks()`，不走逐 slot `GetTask()`
   - 保留现有 fresh confirmation 测试，确保 runnable task 仍会二次确认
2. `pkg/cluster/cluster_test.go`
   - 新增 test：local controller leader 的 `ListTasks()` / `ListSlotAssignments()` 等读路径不会触发 transport self-RPC
   - 断言结果仍来自本地 controller handler / snapshot
3. 运行 focused 包测试：`go test ./pkg/cluster -count=1`

## 7. 预期效果

在 10-slot 三节点开发集群下：

- follower 节点 controller `get_task` RPC 频率应从“每 200ms * slot 数”下降到接近 0
- controller `list_tasks` 频率维持每 observation 一次
- leader 节点应明显减少 self-RPC 相关 transport 开销
- idle CPU 应显著低于当前 7%~10% 基线
