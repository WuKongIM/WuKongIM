# Node Onboarding Resource Allocation 设计

- 日期：2026-04-26
- 范围：新节点加入并激活后，通过 manager 后台生成资源接管计划、确认执行并跟踪进度
- 关联目录：
  - `web`
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app`
  - `pkg/cluster`
  - `pkg/controller/meta`
  - `pkg/controller/plane`
  - `docs/superpowers/specs/2026-04-26-dynamic-node-join-design.md`

## 1. 背景

动态节点加入已经让新节点可以通过 seed 和 join token 加入已有集群。当前流程解决的是 membership 和 discovery 问题：

1. 新节点调用 `JoinCluster`。
2. Controller 元数据写入 `Role=Data`、`JoinState=Joining` 的节点。
3. 节点完成 runtime full sync 后激活为 `JoinState=Active`。
4. Planner 后续只把 `Active + Alive + Data` 节点纳入调度候选。

但新节点激活后，并没有一个专门的资源接管流程。它只是进入通用 Planner 候选池，是否承载 Slot 副本取决于 Bootstrap、Repair 或自然 Rebalance 是否触发。对于扩容场景，管理员更需要一个明确、可审计、可重试的流程：

```text
新节点 Active -> 自动生成接管计划 -> 管理员确认 -> 串行迁移 Slot 副本与 Leader -> 展示结果
```

当前 `/manager/slots/rebalance` 是 hash-slot 在物理 Slot 之间的重平衡，不是“新节点接管 Slot 副本”的专用流程。因此本设计新增独立的 Node Onboarding 能力。

## 2. 目标与非目标

### 2.1 目标

1. 在管理后台提供专门的“扩容 / 接管向导”。
2. 新增后端 Node Onboarding API，而不是复用 hash-slot rebalance 语义。
3. 支持半自动流程：自动生成计划，管理员确认后执行。
4. 第一版资源范围包含：
   - Slot 副本迁移到新节点。
   - 尽量让新节点承担部分 Slot Leader。
5. 迁移强度采用目标均衡 + 串行执行，第一版不做并发迁移。
6. 持久化 Onboarding Job，支持重启或 Controller Leader 切换后查询进度。
7. 失败即停，不自动回滚，支持重新生成计划后重试。
8. 保持已有 Slot 安全迁移链路：
   `AddLearner -> CatchUp -> Promote -> TransferLeader -> RemoveOld`。

### 2.2 非目标

当前阶段不做以下内容：

- 不实现节点 Join 本身；Join 已由动态节点加入流程负责。
- 不自动把新节点加入 Controller Raft voter 集合。
- 不实现 AddSlot / RemoveSlot。
- 不做 hash-slot 总量扩容或 hash-slot 重新分布。
- 不实现多 Slot 并发迁移。
- 不实现中途取消正在执行的底层 Slot Rebalance。
- 不实现自动回滚已完成的迁移。
- 不做容量权重的复杂调度策略，第一版只保留权重字段的未来扩展空间。

## 3. 用户确认的产品语义

本次设计基于以下已确认选择：

- 采用 Web + 后端专用 onboarding API。
- 采用半自动流程：自动生成计划，管理员确认执行。
- 第一版迁移 Slot 副本，并做 Leader 平衡。
- 采用目标均衡 + 串行执行。
- UI 入口采用专门的扩容 / 接管向导。
- 执行状态采用持久化 Onboarding Job。
- 失败即停，不自动回滚，允许重新生成并重试。

## 4. 架构设计

### 4.1 组件

新增能力由四层组成：

1. **Web 扩容向导**
   - 页面路径：`/onboarding`
   - 展示候选节点、接管计划、执行进度和结果。
   - 通过轮询 job detail 展示进度。

2. **Manager API / Management Usecase**
   - 负责 HTTP 鉴权、权限校验、参数校验和 DTO 转换。
   - 保持 manager 层的 controller leader 一致读语义。

3. **Targeted Onboarding Planner**
   - 输入 `target_node_id` 和 Controller leader 快照。
   - 输出明确的 Slot 副本迁移计划。
   - 不复用 hash-slot rebalance。

4. **Controller Metadata + Job Executor**
   - 持久化 `NodeOnboardingJob`。
   - Controller leader 串行推进 job。
   - 每个 move 复用现有 `TaskKindRebalance` 执行链路。

### 4.2 数据流

```text
Web /onboarding
  -> GET /manager/node-onboarding/candidates
  -> POST /manager/node-onboarding/plan              # 创建 planned job
  -> POST /manager/node-onboarding/jobs/:job_id/start
  -> GET /manager/node-onboarding/jobs/:job_id

Manager usecase
  -> Cluster strict reads: nodes / assignments / runtime views / tasks
  -> Targeted Onboarding Planner
  -> Controller command: create planned job / start job / retry-plan

Controller leader
  -> Load running job
  -> Lock current move slot
  -> Submit Slot Rebalance task
  -> Optionally transfer leader to target
  -> Persist move status and job counters
  -> Advance next move
```

## 5. Targeted Onboarding Planner

### 5.1 输入

Planner 输入：

```text
target_node_id
nodes
slot_assignments
runtime_views
reconcile_tasks
slot_replica_n
now
```

所有输入必须来自 Controller leader 视图，不允许使用本地 fallback。

### 5.2 校验

计划生成前必须校验：

- target node 存在。
- target node 是 `Role=Data`。
- target node 是 `JoinState=Active`。
- target node 是 `Status=Alive`。
- target node 不是 `Draining`。由于 `Draining` 是 `Status` 的一种，blocked reason 使用 `target_draining`，不归类为 `target_not_alive`。
- target node 没有正在运行的 onboarding job。
- 当前没有会影响候选 Slot 的 failed task。
- 候选 Slot 必须有 runtime view 且 `HasQuorum=true`。

计划生成接口始终返回成功 DTO：如果无法生成安全计划，`moves` 为空，并带 `blocked_reasons`，便于 Web 明确展示原因。执行接口不得启动带 blocked reasons 的计划。

### 5.3 目标负载

Planner 计算每个 active data node 当前承载的 Slot 副本数：

```text
node_load[node_id] = count(slot_assignment.DesiredPeers contains node_id)
```

目标是让 target node 接近加入后的平均副本负载：

```text
total_replicas = sum(len(assignment.DesiredPeers))
active_nodes = count(Active + Alive + Data)
target_min = floor(total_replicas / active_nodes)
target_max = ceil(total_replicas / active_nodes)
```

第一版忽略 `CapacityWeight` 的加权计算，后续可扩展为 weighted target load。

选择循环必须更新模拟负载，避免实现差异：

1. 复制当前 assignments 和 `node_load` 作为 simulated state。
2. 设置 `desired_target_load = target_max`。即使 `target_min=0`，只要存在安全候选，也允许新节点接管 1 个 Slot 副本。
3. 当 `simulated_load[target_node_id] >= desired_target_load` 时停止。
4. 每轮按第 5.5 节排序选出第一个仍满足安全条件的候选。
5. 选中后更新 simulated assignment：把 source 替换为 target。
6. 更新 simulated load：`source--`、`target++`。
7. 同一个 Slot 在同一计划中只能出现一次。
8. 如果没有候选但 target 仍未达到 `desired_target_load`，返回当前已生成 moves；若 moves 为空，返回 `no_safe_candidate`。

### 5.4 候选 Slot 条件

每个 move 表示把某个 Slot 的一个副本从 source node 替换为 target node。候选必须满足：

- target node 当前不在该 Slot 的 `DesiredPeers` 中。
- source node 当前在该 Slot 的 `DesiredPeers` 中。
- source node 是 `Active + Alive + Data`。
- source node 当前模拟负载高于目标下限；每选中一个 move 后都必须用更新后的 simulated load 重新判断。
- Slot 有 runtime view。
- Slot `HasQuorum=true`。
- Slot 没有进行中的 reconcile task。
- Slot 不在 hash-slot migration 的 source 或 target 保护范围内。

### 5.5 排序

候选排序必须确定性：

1. source 当前是 Slot leader 的候选优先，方便顺带做 Leader 平衡。
2. source 节点负载越高越优先。
3. `BalanceVersion` 越低越优先。
4. `SlotID` 越小越优先。

### 5.6 Leader 平衡

第一版 Leader 平衡只绑定到副本迁移 move，不额外生成 leader-only move：

- 计算 `desired_target_leader_count = ceil(total_physical_slot_count / active_nodes)`，其中 `total_physical_slot_count` 是当前 Controller assignments 中的物理 Slot 数。
- 计算 `leader_need = max(0, desired_target_leader_count - current_target_leader_count)`。
- 对于已选中的副本迁移 move，如果 `current_leader_id == source_node_id` 且 `leader_need > 0`，标记 `leader_transfer_required=true`，并把 `leader_need--`。
- `planned_leader_gain` 等于 `leader_transfer_required=true` 的 move 数量。
- 对于 `leader_transfer_required=true` 的 move，executor 必须在副本迁移完成后显式请求 Leader 转移到 target node，并验证 `leader_after == target_node_id`。
- 对于 `leader_transfer_required=false` 的 move，如果 source 是当前 leader，仍允许底层 Rebalance 按既有逻辑把 leader 移出 source，但不要求落到 target。

这样第一版可以让新节点承担一部分 Leader，同时避免引入单独的 leader-only 调度任务。

### 5.7 输出

计划 DTO：

```json
{
  "target_node_id": 4,
  "summary": {
    "current_target_slot_count": 0,
    "planned_target_slot_count": 3,
    "current_target_leader_count": 0,
    "planned_leader_gain": 1
  },
  "moves": [
    {
      "slot_id": 2,
      "source_node_id": 1,
      "target_node_id": 4,
      "reason": "replica_balance",
      "desired_peers_before": [1, 2, 3],
      "desired_peers_after": [2, 3, 4],
      "current_leader_id": 1,
      "leader_transfer_required": true
    }
  ],
  "blocked_reasons": []
}
```

`planned_target_slot_count` 必须等于 `current_target_slot_count + len(moves)`，表示执行完本计划返回的 moves 后 target 的预计承载数。即使候选不足导致无法达到 `target_max`，该字段也只反映当前可执行计划，不反映理想目标。

## 6. 持久化 Job

### 6.1 Job 状态

新增 `NodeOnboardingJob`：

```text
job_id
target_node_id
retry_of_job_id
status: planned | running | failed | completed | cancelled
created_at
updated_at
started_at
completed_at
plan_version
plan_fingerprint
moves[]
current_move_index
result_counts
current_task
last_error
```

`planned` 是第一版必需状态：管理员先审核持久化计划，再显式 start。第一版不暴露 cancel API；`cancelled` 只保留给未来扩展或人工修复，不要求 Web 展示取消入口。

### 6.2 Move 状态

每个 move：

```text
slot_id
source_node_id
target_node_id
status: pending | running | completed | failed | skipped
task_kind: rebalance
task_slot_id
started_at
completed_at
last_error
desired_peers_before
desired_peers_after
leader_before
leader_after
leader_transfer_required
```

### 6.3 状态转移

Job 状态转移：

| 当前状态 | 事件 | 下一个状态 |
|---|---|---|
| planned | start 成功 | running |
| planned | start 时计划已过期 | planned |
| running | 所有 move 完成或跳过 | completed |
| running | 任一 move 失败 | failed |
| failed | retry | 新建 planned job |
| completed | retry | 不允许 |
| cancelled | 任意自动动作 | 不允许 |

Move 状态转移：

| 当前状态 | 事件 | 下一个状态 |
|---|---|---|
| pending | 开始执行 | running |
| pending | 目标状态已满足 | skipped |
| running | Slot Rebalance task 成功且 Leader 校验通过 | completed |
| running | Slot task 失败、Leader 转移失败或前置校验失效 | failed |

### 6.4 执行语义

Job executor 必须串行推进：

1. 只处理 `running` job。
2. 找到当前 `pending` 或 `running` move。
3. 如果 move 是 `pending`，先检查目标状态是否已经满足；若 current assignment 已等于 `desired_peers_after`，标记为 `skipped`。
4. 如果 move 是 `pending` 且目标状态未满足，按第 6.5 节的 start compatibility 校验当前状态是否仍可启动。
5. 如果 move 是 `running`，按第 6.5 节的 running compatibility 校验当前状态是否仍可继续或完成。
6. 为通过校验的 pending move 创建或复用该 Slot 的 `TaskKindRebalance`，并把 move 标记为 `running`。
7. 等待现有 reconciler 执行：
   `AddLearner -> CatchUp -> Promote -> TransferLeader -> RemoveOld`
8. 若 move 的 `leader_transfer_required=true`，在 Rebalance task 完成后显式调用 Slot Leader transfer 到 target node，并等待 `leader_after == target_node_id`。
9. 观测 task 和可选 Leader transfer 完成后，把 move 标记为 `completed`。
10. 进入下一个 move。
11. 所有 move 完成或跳过后，job 标记为 `completed`。

### 6.5 Start 与 Move 兼容性校验

`plan_fingerprint` 是 planned job 创建时对以下输入的稳定摘要。编码采用 canonical JSON：字段名按字典序排列，数组按 plan move 顺序保留，整数使用十进制 JSON number，时间统一使用 RFC3339Nano 字符串；最终取 SHA-256 hex。

- target node id、target node status、role、join state。
- 每个 move 的 `slot_id`、`source_node_id`、`target_node_id`、`desired_peers_before`、`desired_peers_after`、`current_leader_id`、`leader_transfer_required`。
- 每个 move 对应 assignment 的 `config_epoch` 和 `balance_version`。
- 每个 move 对应 runtime view 的 `current_peers`、`leader_id`、`has_quorum`、`observed_config_epoch`。
- 创建计划时相关 Slot 没有 running / retrying / failed reconcile task。
- 创建计划时相关 Slot 不在 hash-slot migration 的 source 或 target 保护范围内。

`POST /manager/node-onboarding/jobs/:job_id/start` 必须重新读取 Controller leader strict view，并做兼容性校验：

- target node 仍存在，仍是 `Role=Data`、`JoinState=Active`、`Status=Alive`，且不是 Draining。
- 每个 pending move 的 source node 仍存在，仍是 `Role=Data`、`JoinState=Active`、`Status=Alive`，且不是 Draining；否则返回 `409 plan_stale`。
- 每个 pending move 的 current assignment 仍等于 `desired_peers_before`，且 `config_epoch` / `balance_version` 未变化。
- source 仍在 current assignment 中，target 仍不在 current assignment 中。
- 每个 move 的 Slot 仍有 runtime view 且 `HasQuorum=true`。
- 每个 move 的 Slot 没有 running / retrying / failed reconcile task。若普通 Planner 在 plan 和 start 之间创建了 Rebalance task，start 返回 `409 plan_stale`。
- 每个 move 的 Slot 没有 active hash-slot migration，也不是 migration source / target。

如果任一 start 校验失败，start 返回 `409 plan_stale`，job 保持 `planned`。

running move 使用更宽松的 compatibility：

- 若 current assignment 仍等于 `desired_peers_before`，说明 Rebalance task 尚未完成，继续等待 task。
- 若 current assignment 等于 `desired_peers_after`，说明副本替换已完成；继续校验可选 Leader transfer，成功后标记 `completed`。
- 若 current assignment 既不是 before 也不是 after，说明有外部配置变更，job 进入 `failed`。
- 若 source node 在 running 期间变为 Dead 或 Draining，job 进入 `failed`，由 Repair 流程优先处理。
- 若 target node 在 running 期间不再 Active / Alive / Data 或进入 Draining，job 进入 `failed`。

### 6.6 失败语义

- 任意 move 失败后，job 进入 `failed`。
- 不自动回滚已完成 move。
- 重试时不直接重放旧 move，而是基于当前集群状态重新生成 planned job。
- failed job 保留用于审计，新 retry 创建新 job，并通过 `retry_of_job_id` 关联。

### 6.7 与普通 Planner 的协调

Onboarding 复用 `TaskKindRebalance`，但必须避免和普通自动 Rebalance 竞争：

- Controller leader 同一时间只允许一个 `running` onboarding job。
- 有 `running` onboarding job 时，普通 Planner 暂停自动 `TaskKindRebalance` 生成。
- Bootstrap / Repair 仍可运行；如果它们影响当前 onboarding move 的 source、target 或 slot，onboarding job 必须失败并让 Repair 优先。
- 当前 move 的 slot 被 onboarding job 锁定；普通 Planner 不得为该 slot 生成另一个任务。
- planned job 不加锁，只有 start 后进入 running 才加锁。
- Controller leader 切换后，新 leader 从持久化 job 和 task 表恢复推进状态；旧 leader 不再推进。

## 7. Manager API

### 7.1 候选节点

```text
GET /manager/node-onboarding/candidates
```

权限：

- `cluster.node:r`
- `cluster.slot:r`

响应：

```json
{
  "items": [
    {
      "node_id": 4,
      "addr": "wk-node4:7000",
      "status": "alive",
      "join_state": "active",
      "slot_count": 0,
      "leader_count": 0,
      "recommended": true,
      "recommendation_reason": "active node has no slot replicas"
    }
  ]
}
```

### 7.2 生成计划并创建 planned job

```text
POST /manager/node-onboarding/plan
```

权限：

- `cluster.slot:w`

请求：

```json
{
  "target_node_id": 4
}
```

响应中的 `plan` 使用第 5.7 节的 plan DTO。

该接口会持久化一个 `status=planned` 的 job，并返回 job detail。管理员确认执行时启动这个 job，而不是让后端重新生成另一个可能不同的计划。

响应：

```json
{
  "job": {
    "job_id": "onboard-20260426-0001",
    "target_node_id": 4,
    "retry_of_job_id": "",
    "status": "planned",
    "created_at": "2026-04-26T12:00:00Z",
    "updated_at": "2026-04-26T12:00:00Z",
    "started_at": null,
    "completed_at": null,
    "current_move_index": -1,
    "result_counts": {
      "pending": 3,
      "running": 0,
      "completed": 0,
      "skipped": 0,
      "failed": 0
    },
    "current_task": null,
    "last_error": "",
    "plan": {
      "target_node_id": 4,
      "summary": {
        "current_target_slot_count": 0,
        "planned_target_slot_count": 3,
        "current_target_leader_count": 0,
        "planned_leader_gain": 1
      },
      "moves": [
        {
          "slot_id": 2,
          "source_node_id": 1,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [2, 3, 4],
          "current_leader_id": 1,
          "leader_transfer_required": true
        },
        {
          "slot_id": 3,
          "source_node_id": 2,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [1, 3, 4],
          "current_leader_id": 3,
          "leader_transfer_required": false
        },
        {
          "slot_id": 4,
          "source_node_id": 3,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [1, 2, 4],
          "current_leader_id": 1,
          "leader_transfer_required": false
        }
      ],
      "blocked_reasons": []
    },
    "moves": [
      {
        "slot_id": 2,
        "source_node_id": 1,
        "target_node_id": 4,
        "status": "pending",
        "task_kind": "rebalance",
        "task_slot_id": 2,
        "started_at": null,
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [2, 3, 4],
        "leader_before": 0,
        "leader_after": 0,
        "leader_transfer_required": true
      },
      {
        "slot_id": 3,
        "source_node_id": 2,
        "target_node_id": 4,
        "status": "pending",
        "task_kind": "rebalance",
        "task_slot_id": 3,
        "started_at": null,
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [1, 3, 4],
        "leader_before": 0,
        "leader_after": 0,
        "leader_transfer_required": false
      },
      {
        "slot_id": 4,
        "source_node_id": 3,
        "target_node_id": 4,
        "status": "pending",
        "task_kind": "rebalance",
        "task_slot_id": 4,
        "started_at": null,
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [1, 2, 4],
        "leader_before": 0,
        "leader_after": 0,
        "leader_transfer_required": false
      }
    ]
  }
}
```

如果计划为空或被阻塞，仍创建 `planned` job，但不能 start。这样 Web 可以展示“为什么不能接管”，同时保留审计记录。

### 7.3 启动 Job

```text
POST /manager/node-onboarding/jobs/:job_id/start
```

权限：

- `cluster.slot:w`

语义：

- 只允许启动 `planned` job。
- start 前必须重新校验 planned job 的 `plan_fingerprint` 与当前 leader 视图是否兼容。
- 若计划已过期，返回 `409 plan_stale`，job 保持 `planned`，Web 需要重新生成计划。
- 若计划有 `blocked_reasons` 或 `moves` 为空，返回 `409 plan_not_executable`。
- 若存在其他 `running` onboarding job，返回 `409 running_job_exists`。
- 成功后 job 进入 `running`，由 Controller leader 串行推进。

响应返回最新 job detail。

### 7.4 查询 Job

```text
GET /manager/node-onboarding/jobs
GET /manager/node-onboarding/jobs/:job_id
```

权限：

- `cluster.slot:r`

列表默认返回最近 20 个 job，按 `created_at desc` 排序，可通过 `limit` 和 `cursor` 分页。

列表响应：

```json
{
  "items": [
    {
      "job_id": "onboard-20260426-0001",
      "target_node_id": 4,
      "retry_of_job_id": "",
      "status": "running",
      "created_at": "2026-04-26T12:00:00Z",
      "started_at": "2026-04-26T12:01:00Z",
      "completed_at": null,
      "current_move_index": 0,
      "result_counts": {
        "pending": 2,
        "running": 1,
        "completed": 0,
        "skipped": 0,
        "failed": 0
      },
      "last_error": ""
    }
  ],
  "has_more": false,
  "next_cursor": ""
}
```

`current_move_index` 为 0-based；没有当前 move 时为 `-1`。`result_counts` 必须从 job moves 的当前状态实时汇总。

详情响应使用第 7.2 节 job detail，并额外包含完整 moves 和 `current_task`：

```json
{
  "job": {
    "job_id": "onboard-20260426-0001",
    "target_node_id": 4,
    "retry_of_job_id": "",
    "status": "running",
    "created_at": "2026-04-26T12:00:00Z",
    "updated_at": "2026-04-26T12:01:05Z",
    "started_at": "2026-04-26T12:01:00Z",
    "completed_at": null,
    "current_move_index": 0,
    "result_counts": {
      "pending": 2,
      "running": 1,
      "completed": 0,
      "skipped": 0,
      "failed": 0
    },
    "current_task": {
      "slot_id": 2,
      "kind": "rebalance",
      "step": "catch_up",
      "status": "pending",
      "attempt": 1,
      "last_error": ""
    },
    "plan": {
      "target_node_id": 4,
      "summary": {
        "current_target_slot_count": 0,
        "planned_target_slot_count": 3,
        "current_target_leader_count": 0,
        "planned_leader_gain": 1
      },
      "moves": [
        {
          "slot_id": 2,
          "source_node_id": 1,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [2, 3, 4],
          "current_leader_id": 1,
          "leader_transfer_required": true
        },
        {
          "slot_id": 3,
          "source_node_id": 2,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [1, 3, 4],
          "current_leader_id": 3,
          "leader_transfer_required": false
        },
        {
          "slot_id": 4,
          "source_node_id": 3,
          "target_node_id": 4,
          "reason": "replica_balance",
          "desired_peers_before": [1, 2, 3],
          "desired_peers_after": [1, 2, 4],
          "current_leader_id": 1,
          "leader_transfer_required": false
        }
      ],
      "blocked_reasons": []
    },
    "moves": [
      {
        "slot_id": 2,
        "source_node_id": 1,
        "target_node_id": 4,
        "status": "running",
        "task_kind": "rebalance",
        "task_slot_id": 2,
        "started_at": "2026-04-26T12:01:01Z",
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [2, 3, 4],
        "leader_before": 1,
        "leader_after": 0,
        "leader_transfer_required": true
      },
      {
        "slot_id": 3,
        "source_node_id": 2,
        "target_node_id": 4,
        "status": "pending",
        "task_kind": "rebalance",
        "task_slot_id": 3,
        "started_at": null,
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [1, 3, 4],
        "leader_before": 0,
        "leader_after": 0,
        "leader_transfer_required": false
      },
      {
        "slot_id": 4,
        "source_node_id": 3,
        "target_node_id": 4,
        "status": "pending",
        "task_kind": "rebalance",
        "task_slot_id": 4,
        "started_at": null,
        "completed_at": null,
        "last_error": "",
        "desired_peers_before": [1, 2, 3],
        "desired_peers_after": [1, 2, 4],
        "leader_before": 0,
        "leader_after": 0,
        "leader_transfer_required": false
      }
    ]
  }
}
```

字段枚举：

- job status: `planned | running | failed | completed | cancelled`
- move status: `pending | running | completed | failed | skipped`
- task kind: `bootstrap | repair | rebalance`
- task step: `add_learner | catch_up | promote | transfer_leader | remove_old`
- task status: `pending | retrying | failed`

DTO 字段约束：

| DTO | 字段 | 语义 |
|---|---|---|
| job detail | `plan` | planned 时必须返回；running/completed/failed 也保留原始 plan 用于审计 |
| job detail | `moves` | 完整 move 状态列表；每个 plan move 都有一个对应 move 状态 |
| job detail | `current_move_index` | 0-based；没有当前 move 时为 `-1` |
| job detail | `current_move_index` on failed | 指向 failed move 的 0-based index |
| job detail | `result_counts` | 从 `moves[].status` 实时汇总 |
| job detail | `current_task` | 当前 move 对应的 reconcile task；没有任务时为 `null` |
| move detail | `desired_peers_before/after` | 必须与 plan move 一致 |
| move detail | `leader_before` | move 开始执行时观测到的 leader；未知时为 `0` |
| move detail | `leader_after` | move 完成后观测到的 leader；未完成或未知时为 `0` |

### 7.5 重试 Job

```text
POST /manager/node-onboarding/jobs/:job_id/retry
```

权限：

- `cluster.slot:w`

语义：

- 只允许 retry `failed` job。
- 使用 failed job 的 `target_node_id`。
- 基于当前集群状态重新生成计划并创建新的 `planned` job。
- 新 job 的 `retry_of_job_id` 指向 failed job。
- retry 不自动 start，Web 必须展示新计划并让管理员再次确认。
- 响应返回新 planned job detail。

## 8. Web 扩容向导

### 8.1 导航

新增导航项：

```text
运行时 -> 扩容
path: /onboarding
```

也可以在节点页对推荐节点显示“接管”快捷入口，但第一版主入口是 `/onboarding`。

### 8.2 步骤

#### 步骤 1：选择节点

展示候选节点：

- node id
- addr
- status
- join state
- slot count
- leader count
- recommendation reason

推荐条件第一版：

- `Active + Alive + Data`
- slot count 明显低于平均值，尤其是 0

#### 步骤 2：审核计划

展示：

- 将迁移的 Slot 数量。
- 每个 Slot 的 source -> target。
- 当前 leader。
- 预期 DesiredPeers 变化。
- 风险提示：
  - 串行执行。
  - 不自动回滚。
  - 失败后需重新生成计划再重试。

管理员点击“生成计划”后创建 `planned` job；点击“确认执行”后调用 start 接口启动同一个 planned job。若 start 返回 `plan_stale`，页面提示重新生成计划，不能静默执行另一个计划。

#### 步骤 3：执行进度

展示：

- job status
- current move index
- move 列表状态
- last error
- 当前 Slot task 状态

前端轮询：

```text
GET /manager/node-onboarding/jobs/:job_id
```

轮询间隔建议 2 秒。页面离开后停止轮询。

#### 步骤 4：结果

完成后展示：

- target node 最终 slot count
- target node 最终 leader count
- completed / skipped / failed move 数量

失败后展示：

- failed move
- last error
- “重新生成计划并重试”按钮；按钮调用 retry 接口创建新的 planned job，仍需管理员再次确认 start。

## 9. 错误处理

HTTP 错误建议：

- `400 invalid_request`：target node id 非法、请求体非法。
- `403 forbidden`：权限不足。
- `404 not_found`：target node 或 job 不存在。
- `409 running_job_exists`：已有运行中 job。
- `409 plan_not_executable`：planned job 没有 moves 或包含 blocked reasons。
- `409 plan_stale`：start 时当前 leader 视图已不再匹配 planned job 的 `plan_fingerprint`。
- `409 invalid_job_state`：对非 planned job 调 start，或对非 failed job 调 retry。
- `503 service_unavailable`：Controller leader 一致读不可用。

`POST /manager/node-onboarding/plan` 本身不因 blocked reasons 返回 `409`；它返回 planned job 和 blocked reasons。只有 start 阶段才拒绝不可执行计划。

Planner blocked reason 建议使用稳定 code：

```text
target_not_active
target_not_alive
target_draining
target_not_data
running_job_exists
no_runtime_view
slot_quorum_lost
slot_task_running
slot_task_failed
slot_hash_migration_active
already_balanced
no_safe_candidate
```

`blocked_reasons` payload 采用结构化对象，便于 Web 展示全局或 Slot 级原因：

```json
{
  "code": "slot_task_running",
  "scope": "slot",
  "slot_id": 2,
  "node_id": 0,
  "message": "slot has running reconcile task"
}
```

Web 应直接展示 code 对应的中文说明，避免只显示后端错误字符串。

请求层错误和 blocked planned job 必须分开：

- target node id 非法或请求体非法：直接返回 `400 invalid_request`，不创建 planned job。
- target node 不存在：直接返回 `404 not_found`，不创建 planned job。
- target node 存在但不满足 Active / Alive / Data / non-draining：创建 planned job，`moves=[]`，`blocked_reasons` 使用对应 target code。
- Slot 级不安全条件：创建 planned job，`moves=[]` 或部分 moves，并在 `blocked_reasons` 中记录 Slot 级 code。

## 10. 一致性与安全约束

- 所有 plan 和 job 创建必须基于 Controller leader strict view。
- 不允许 manager API 使用本地 fallback 生成资源计划。
- 同一时间只允许一个 running onboarding job。
- 第一版每个 job 内只串行执行一个 move。
- 不和 hash-slot migration 叠加执行同一个物理 Slot 的副本迁移。
- target node 必须已经 `Active`，不能在 `Joining` 阶段提前接管资源。
- Draining 节点不能作为 target。
- 若 source 在执行前变为 Dead 或 Draining，本 move 停止并让 job failed，由 Repair 流程优先处理。

## 11. 测试计划

### 11.1 单元测试

`pkg/controller/plane` 或新 planner 包：

- target inactive / joining / dead 时拒绝。
- target slot count 为 0 时生成计划。
- 已经均衡时返回空计划和 `already_balanced`。
- 选择循环使用 simulated load，target 达到 `target_max` 后停止。
- 有 running task 的 Slot 不进入候选。
- quorum lost 的 Slot 不进入候选。
- source leader 优先排序。
- leader need 大于 0 时只把 source leader move 标记为 `leader_transfer_required`。
- BalanceVersion 和 SlotID 排序稳定。

`pkg/controller/meta`：

- onboarding job 编解码。
- job CRUD。
- job snapshot / restore。

`internal/usecase/management`：

- candidates DTO。
- planned job DTO。
- start stale plan 返回 `plan_stale`，不会执行不同计划。
- start blocked plan 返回 `plan_not_executable`。
- retry failed job 生成新 planned job。

`internal/access/manager`：

- routes。
- auth permission。
- 400 / 403 / 404 / 409 / 503 错误响应。

`web`：

- `/onboarding` 路由。
- candidate loading / empty / error。
- plan review。
- create planned job。
- start planned job。
- polling completed / failed。
- permission disabled state。

### 11.2 集成测试

`pkg/cluster`：

- 4 节点环境中 node4 Active 后，创建 planned onboarding job。
- start planned job 后进入 running。
- job 串行生成 Slot Rebalance task。
- node4 最终进入若干 Slot DesiredPeers。
- 若 move 标记 `leader_transfer_required`，迁移后 leader 必须转移到 target。
- running job 期间再次 start 另一个 job 返回 conflict。
- running onboarding job 期间普通自动 Rebalance 不生成竞争任务。

### 11.3 E2E 测试

`test/e2e/cluster/dynamic_node_join` 可扩展或新增场景：

1. 启动 3 节点集群。
2. 动态加入 node4。
3. 等待 node4 `Active`。
4. 调 manager onboarding plan，断言返回 planned job 且 moves 非空。
5. start planned job。
6. 等待 job completed。
7. 查询 `/manager/nodes/4`，断言 hosted slot count > 0。
8. 发送消息验证集群仍可读写。

## 12. 风险与后续扩展

### 12.1 风险

- 若 Slot 数太少，无法让新节点获得明显负载，计划可能为空。
- Leader 转移依赖现有 runtime 稳定性，失败时 job 会停在 failed。
- 持久化 job 与现有 task 状态需要保持一致，避免 job 显示 running 但 task 已失败。
- Controller leader 切换期间要避免两个 leader 同时推进同一 job。

### 12.2 后续扩展

- 支持容量权重调度。
- 支持并发度配置。
- 支持自动模式：新节点 Active 后自动创建 planned job，管理员只确认执行。
- 支持 cancel / pause / resume。
- 支持 AddSlot + hash-slot 扩容的完整容量扩展流程。
- 支持 Controller voter onboarding 的独立高风险流程。
- 支持拓扑图展示迁移前后副本分布。
