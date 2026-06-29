# ControllerV2 Task Audit History Design

## 背景

Web 任务中心要面向新架构重新设计。新架构里当前任务的权威接口是
`internalv2/access/manager` 暴露的 ControllerV2 active task 接口：

```text
GET /manager/controller/tasks
GET /manager/controller/tasks/:task_id
```

这些接口读取 `internalv2/usecase/management` 从 clusterv2 control snapshot
投影出的 active Controller task。ControllerV2 的 completed task 会从
`cluster-state.json` 的 active `tasks` 集合移除，因此 active task 接口天然不保留历史。

本设计只考虑新版本：

- `pkg/controllerv2`
- `pkg/clusterv2/control`
- `internalv2`
- Web 任务中心的新 ControllerV2 任务视图

旧版本 `pkg/controller`、旧 `internal/access/manager/distributed-tasks*`、旧
`internal/usecase/management/distributed_tasks.go`、旧 `pkg/db/meta` channel
migration task 都不在第一版任务审计范围内。

## 目标

- 为 ControllerV2 task 提供完整的有界审计历史和单任务事件时间线。
- 当前任务继续读取 `/manager/controller/tasks*`，历史任务读取新增审计接口。
- 审计不改变 ControllerV2 Raft、FSM、planner、task executor 和 active task 语义。
- 使用 JSONL append-only 文件，不引入 Pebble 表，不落到 `pkg/db/meta`。
- 默认只保留最近 200 个任务，每个任务最多保留 50 条事件。
- 审计写失败不阻塞 ControllerV2 command apply 或 manager 操作。

## 非目标

- 不把 completed task 留在 ControllerV2 active `tasks` 集合里。
- 不为旧 Controller 或旧 distributed task 聚合接口补历史。
- 不把 node onboarding、scale-in 设计成单独的 durable job 历史。它们在 v2 中通过
  ControllerV2 `slot_replica_move` task 表达，任务中心按 Controller task 展示。
- 不支持无限历史、分页游标、复杂全文搜索或导出。
- 不新增 retry、cancel、advance 等任务写操作。

## 存储位置

新增 v2 专属包：

```text
internalv2/observability/taskaudit/
  FLOW.md
  model.go
  store.go
  jsonl.go
  index.go
  retention.go
```

审计是 internalv2 manager 可观测能力，放在 `internalv2/observability`，与
`internalv2/observability/diagnostics` 同层。它不进入 `pkg/db/meta`、不进入
ControllerV2 `cluster-state.json`、不进入 Controller Raft snapshot。

物理文件默认放在节点数据目录：

```text
${WK_NODE_DATA_DIR}/task-audit/controller-v2-events.jsonl
```

第一版不新增路径配置项。后续如果需要独立磁盘或迁移路径，再增加
`WK_TASK_AUDIT_DIR` 并同步 `wukongim.conf.example`。

## 边界与依赖方向

- `pkg/controllerv2` 可以定义 task transition 事件结构和 observer 接口。
- `pkg/controllerv2` 不能 import `internalv2/observability/taskaudit`。
- `internalv2/app` 负责把 `pkg/controllerv2` 的 transition observer 适配到
  `internalv2/observability/taskaudit.Store`。
- `internalv2/usecase/management` 定义自己需要的 task audit 查询端口和 DTO，只依赖该端口。
- `internalv2/access/manager` 只做 HTTP 参数校验、权限、DTO 映射和错误映射。

Observer 调用必须发生在 ControllerV2 state file 成功持久化之后，且不在 FSM mutex、
Raft ready 处理、WAL append 或 statefile save 的关键路径里做文件 fsync。推荐做法是：

```text
ControllerV2 ApplyBatch
  -> 计算 task transitions
  -> 保存 cluster-state.json 成功
  -> 返回 ApplyResult/BatchApplyResult
  -> runtime/app adapter 把 transitions 投递到 taskaudit 单 writer 队列
```

## 集群可见性

ControllerV2 task 是集群级任务，审计事件来自 committed Controller Raft command：

- Controller voter 节点在 apply committed command 后生成相同的逻辑 transition。
- 非 Controller voter 的 mirror 节点只同步 state file，不依赖 mirror diff 生成完整审计。
- Manager 审计接口优先读取当前 Controller leader/voter 的审计投影；如果请求落在非
  Controller voter 节点，通过 internalv2 manager/node RPC 路由到当前 Controller
  leader 或任一可用 voter。
- 如果路由失败，接口返回 `503 service_unavailable`，而不是返回一个可能不完整的 mirror
  diff 历史。
- 单节点集群自然读取本节点本地审计。

这样避免多 voter fan-out 后的重复合并，也避免非 voter mirror 因 state sync 粒度太粗而漏掉
中间任务事件。

第一版需要显式补齐远程读取通道：

- `internalv2/usecase/management` 定义 `TaskAuditReader` 和 `RemoteTaskAuditReader` 端口。
- `internalv2/access/node` 增加 manager task audit 查询 RPC codec/handler/client。
- `pkg/clusterv2/net` 分配新的 task audit RPC service id。
- `internalv2/app` 把本地 taskaudit store 和远程 reader 注入 management usecase。
- 如果这些远程读取通道没有在第一版实现，则 manager audit API 必须只在 Controller voter 节点可用，非 voter 返回 `503 service_unavailable`。

## 数据模型

### TaskAuditEvent

每行 JSONL 存一条事件：

```go
type TaskAuditEvent struct {
    EventID         string
    SourceRef       string
    AuditNodeID     uint64
    AppliedRaftIndex uint64
    AppliedRaftTerm  uint64
    TaskID          string
    EventType       string
    Status          string
    Kind            string
    Step            string
    SlotID          uint32
    SourceNode      uint64
    TargetNode      uint64
    TargetPeers     []uint64
    ConfigEpoch     uint64
    Attempt         uint32
    PhaseIndex      uint32
    ParticipantNode uint64
    ParticipantStatus string
    OccurredAt      time.Time
    LastError       string
    Summary         string
    DetailSnapshot  json.RawMessage
    DetailTruncated bool
    Terminal        bool
}
```

`EventType` 固定集合：

```text
created
running
participant_progress
failed
completed
snapshot
```

`snapshot` 只用于两类场景：

- 审计启用后首次从 active Controller task snapshot backfill 已存在任务。
- compaction 后标记某个任务的早期事件已被裁剪。

`DetailSnapshot` 保存有界排障上下文，不归档完整业务对象。第一版上限为 16KB；超过上限截断并设置
`detail_truncated=true`。

### TaskAuditSnapshot

`TaskAuditSnapshot` 是内存读模型，不单独持久化。启动时从 JSONL 重放，运行时由 append 更新：

```go
type TaskAuditSnapshot struct {
    TaskID             string
    Kind               string
    Step               string
    Status             string
    SlotID             uint32
    SourceNode         uint64
    TargetNode         uint64
    TargetPeers        []uint64
    ConfigEpoch        uint64
    Attempt            uint32
    PhaseIndex         uint32
    FirstSeenAt        time.Time
    LastEventAt        time.Time
    LastAppliedRaftIndex uint64
    TerminalAt         time.Time
    Terminal           bool
    ObservedEventCount int
    RetainedEventCount int
    EventsTruncated    bool
    LastError          string
    Summary            string
}
```

内存索引：

```go
latestByTask map[string]TaskAuditSnapshot
eventsByTask map[string][]TaskAuditEvent
seenEvents   map[string]struct{}
```

查询历史任务时扫描内存 snapshots，按 `last_applied_raft_index desc` 排序后返回有界结果，
`last_event_at` 只用于显示。查询事件时间线时读取 `eventsByTask[task_id]`，按
`applied_raft_index asc` 排序展示。

## 保留策略

默认值：

```text
MaxTasks = 200
MaxEventsPerTask = 50
```

保留策略按任务数量裁剪，而不是按全局事件数量裁剪：

- 按 `last_applied_raft_index desc` 严格保留最近 200 个任务的审计快照和事件。
- 每个任务最多保留 50 条事件。
- 当任务数超过 200 时，直接删除 `last_applied_raft_index` 最旧的任务，不因为 terminal / non-terminal 改变顺序。
- 如果旧的 non-terminal 任务被裁剪，当前 active 状态仍由 `/manager/controller/tasks*` 表达。
- 历史接口只承诺“最近 200 个任务”的审计窗口。

`failed` 不是 terminal。ControllerV2 failed task 仍保留在 active `tasks` 集合里，后续可能重新推进。
只有以下事件视为 terminal：

- `completed` from `KindCompleteTask`
- `completed` from `KindCommitSlotReplicaMove`

## JSONL 写入与压缩

写入流程：

1. 构造 `TaskAuditEvent`。
2. `Append` 进入单 writer 队列。
3. writer 持有 store mutex，向 `controller-v2-events.jsonl` 追加一行 JSON，flush + fsync 文件。
4. 在同一 mutex 内更新 `seenEvents`、`eventsByTask` 和 `latestByTask`。
5. 应用保留策略。
6. 必要时异步触发 compaction；compaction 仍必须获取同一个 store mutex。

compaction 使用临时文件：

```text
controller-v2-events.jsonl
controller-v2-events.compacting
```

压缩算法：

1. compaction 获取和 append 相同的 store mutex；持锁期间 append 等待，避免 rename 窗口丢事件。
2. 从内存投影复制当前 retained tasks/events。
3. 如果某任务早期事件被丢弃，写入一条 `snapshot` truncation marker。
4. 写入临时文件，flush + fsync 临时文件后 close。
5. 原子 rename 覆盖原 JSONL。
6. fsync 父目录，确保 rename 持久化。
7. rename 成功后，新文件即为 authoritative JSONL；即使后续父目录 fsync 或 append 句柄 reopen
   失败，也不能再声称旧文件仍然存在。
8. rename 后重新打开 append 句柄，或 append 时短生命周期 open `O_APPEND`。
9. rename 前任一步失败都保留旧文件继续追加；rename 后失败时标记 store unavailable，后续启动从
   已 rename 的新文件恢复。

truncation marker 的 `SourceRef` 使用：

```text
audit-compaction/<task_id>/<last_dropped_event_id>
```

`DetailSnapshot` 至少包含 `events_dropped`、`last_dropped_at` 和
`observed_event_count`。启动重放看到 marker 时设置 `events_truncated=true`。

启动加载：

- 文件不存在时按空审计启动。
- 单行 JSON 损坏时跳过该行，计数 `corrupt_lines` 并继续加载后续行。
- 目录不可写时审计能力标记 unavailable，但主程序继续运行。

幂等：

- `AppliedRaftIndex` 是排序、保留、幂等和重放的主顺序字段；`OccurredAt` 只用于 UI 展示，不能参与裁剪决策。
- `SourceRef` 必须来自 ControllerV2 committed Raft position，格式：

```text
controller-v2/<applied_raft_index>/<command_kind>
```

- `EventID` 由 `task_id/event_type/status/step/attempt/phase_index/participant_node/source_ref`
  组成。
- `OccurredAt` 使用 ControllerV2 command `IssuedAt`；如果为空，使用 apply 后的 UTC 时间。它不参与幂等去重。
- append 前先检查 `seenEvents`；同一 `EventID` 已存在时直接计为 duplicate，不再写入 JSONL，避免重复事件无限增长。
- 启动重放完成后立即应用同一套保留策略；即使 compaction 尚未执行，重启后内存投影也不能超过 200 个任务或每任务 50 条事件。

## ControllerV2 写入点

第一版只在 ControllerV2 command 产生 `ApplyResult.Changed=true` 后写审计。`Noop` 和
`Rejected` 不写事件，避免过期 task result、stale revision 或重复上报污染历史。

建议在 `pkg/controllerv2/fsm` 内计算轻量 task transition，并随 `ApplyResult` 返回：

```go
type TaskTransition struct {
    AppliedRaftIndex uint64
    AppliedRaftTerm  uint64
    CommandKind      command.Kind
    IssuedAt         time.Time
    Before           state.ReconcileTask
    BeforeValid      bool
    After            state.ReconcileTask
    AfterValid       bool
    ParticipantNode  uint64
}
```

`BeforeValid=false, AfterValid=true` 表示创建；`BeforeValid=true, AfterValid=false` 表示完成移除；
两者都为 true 表示状态、step、participant 或 attempt 变化。`Before` 和 `After` 必须是
deep-copied value，不能指向 `ApplyBatch` 内部可变 `next` state 的 slice 或 task。
`internalv2/app` adapter 再把它映射为 `TaskAuditEvent`。

公开 wiring：

- `pkg/controllerv2` 根包定义 `TaskTransitionObserver` 或 `TaskTransitionSink`，不要只停留在
  `fsm` 子包内部。
- `pkg/clusterv2/control.RuntimeConfig` 增加透传字段，把 observer 交给 ControllerV2 runtime。
- ControllerV2 apply scheduler 在 `ApplyBatch` 成功并持久化 applied metadata
  (`MarkAppliedBatch`) 之后，把 transitions 投递到 bounded nonblocking queue。
- audit queue 满时丢弃该次 audit append 并记 metric/log，不阻塞 Raft apply、WAL、statefile
  或 applied metadata 持久化。

| ControllerV2 command | Change condition | EventType | Status | Terminal | DetailSnapshot |
| --- | --- | --- | --- | --- | --- |
| `KindUpsertSlotAssignmentAndTask` + `bootstrap` task | 新任务被写入 | `created` | `pending` 或 task status | false | assignment、target peers、completion policy |
| `KindUpsertSlotAssignmentAndTask` + `leader_transfer` task | 新任务被写入 | `created` | `pending` | false | source leader、target leader、target peers、config epoch |
| `KindUpsertSlotReplicaMoveTask` | 新 `slot_replica_move` task 被写入 | `created` | `pending` | false | source node、target node、target peers、config epoch |
| `KindAdvanceSlotReplicaMovePhase` | move task step/phase index 改变 | `running` | task status | false | previous step、next step、observed voters/learners/config index |
| `KindReportTaskProgress` | participant progress 改变 | `participant_progress` | task status | false | participant node、participant attempt、participant status、participant error |
| `KindFailTask` | task status 变为 failed、attempt 增加 | `failed` | `failed` | false | old attempt、new attempt、last error |
| `KindCompleteTask` | task 被移除 | `completed` | `completed` | true | final task snapshot |
| `KindCommitSlotReplicaMove` | assignment commit 成功且 move task 被移除 | `completed` | `completed` | true | final task snapshot、new desired peers、new config epoch |

字段映射：

- `TaskID`: ControllerV2 `ReconcileTask.TaskID`，与 `/manager/controller/tasks/:task_id` 完全一致。
- `Kind`: `bootstrap`、`leader_transfer`、`slot_replica_move`。
- `Status`: active 状态为 `pending`、`running`、`failed`；完成事件使用审计专属 `completed`。
- `Step`: ControllerV2 `TaskStep`。
- `SlotID`、`SourceNode`、`TargetNode`、`TargetPeers`、`ConfigEpoch`、`Attempt`、`LastError`
  从 `ReconcileTask` 复制。
- `PhaseIndex` 用于 `slot_replica_move` phase 进度。

实现注意：当前 `internalv2/access/manager/controller_tasks.go` 的
`validControllerTaskKind` 需要补充 `slot_replica_move`，否则现有 active task 列表无法按 v2
onboarding/scale-in 产生的 task kind 过滤，Web 当前任务和历史任务筛选会不一致。

启动 backfill：

- taskaudit store 启动后读取当前 ControllerV2 local control snapshot。
- 对当前 active tasks 中 `latestByTask` 不存在的任务写 `snapshot` 事件。
- `SourceRef` 使用 `controller-v2-backfill/<state_revision>/<task_id>`。
- `AppliedRaftIndex` 使用 snapshot 的 `AppliedRaftIndex`。
- backfill 只补“审计启用时已经存在”的 active task，不伪造历史 created/progress。

## API

新增 manager 只读接口：

```text
GET /manager/controller/task-audits?kind=&status=&slot_id=&node_id=&keyword=&limit=
GET /manager/controller/task-audits/:task_id/events
```

权限沿用 Controller task：

```text
cluster.controller:r
```

列表响应：

```json
{
  "total": 37,
  "limit": 200,
  "truncated": false,
  "items": []
}
```

`limit` 只控制返回数量，不是分页游标。默认 200，最大 200；负数、非数字、超过最大值返回
`400 bad_request`。`total` 是当前保留集合中筛选后的数量；当 `total > limit` 时返回
`truncated=true`。

列表按 `last_applied_raft_index desc` 排序；响应可同时包含 `last_event_at` 供 UI 展示时间。

支持筛选：

- `kind`: `bootstrap`、`leader_transfer`、`slot_replica_move`
- `status`: `pending`、`running`、`failed`、`completed`
- `slot_id`: 正整数
- `node_id`: 正整数，匹配 source、target、target peers 和 participant node
- `keyword`: 轻量 contains 匹配 task id、kind、status、step、summary、last error

事件响应：

```json
{
  "task": {},
  "events": [],
  "truncated": false
}
```

`truncated=true` 表示该任务时间线早期事件已被保留策略裁剪。任务已被 retention 清理时返回 404。

## Web 设计

`/cluster/tasks` 改为 ControllerV2 任务中心，增加视图切换：

```text
当前任务 | 历史任务
```

当前任务使用现有 v2 接口，并替换当前 Web 页面里旧的 `/manager/distributed-tasks*`
调用和 `cluster.task` 权限判断：

```text
GET /manager/controller/tasks
GET /manager/controller/tasks/:task_id
```

历史任务使用新增审计接口：

```text
GET /manager/controller/task-audits
GET /manager/controller/task-audits/:task_id/events
```

历史视图不分页，默认最多展示 200 个保留任务。筛选项与当前 Controller task 对齐：

- kind
- status
- slot
- node
- keyword

详情抽屉展示：

- 任务最新审计快照。
- 事件时间线。
- participant progress。
- detail snapshot。
- detail truncated / events truncated 提示。

默认页面仍优先展示当前任务，保持排障入口聚焦 active failed/running/pending task。

Web 迁移范围：

- `web/src/lib/manager-api.ts` 增加 `/manager/controller/tasks*` 与
  `/manager/controller/task-audits*` client，旧 distributed task client 不再被任务中心使用。
- `web/src/lib/manager-api.types.ts` 增加 Controller task 和 audit task 类型。
- `web/src/pages/tasks/page.tsx` 改成 ControllerV2 task shape。
- `web/src/pages/tasks/page.test.tsx` 权限 fixture 改为 `cluster.controller:r`，并覆盖
  `slot_replica_move` kind、当前/历史切换和 audit events drawer。

## 错误处理

- 审计写失败不阻塞 ControllerV2 command apply，只记录 warn 日志并增加 metric。
- JSONL 单行损坏跳过，其余行继续加载。
- 文件不存在按空审计启动。
- 目录不可写时审计能力 unavailable，接口返回 service unavailable，主程序继续运行。
- 非 Controller voter 节点无法路由到 Controller voter audit 时返回 service unavailable。
- compaction 失败不影响追加写。
- detail snapshot 超限截断并标记 `detail_truncated=true`。

## 观测指标

新增轻量 metrics：

```text
wukongim_task_audit_append_total{result="ok|duplicate|error"}
wukongim_task_audit_events_retained
wukongim_task_audit_tasks_retained
wukongim_task_audit_compactions_total{result="ok|error"}
wukongim_task_audit_corrupt_lines_total
wukongim_task_audit_unavailable
```

指标类型：

- `append_total`、`compactions_total`、`corrupt_lines_total` 是 counter。
- `events_retained`、`tasks_retained` 是 gauge。
- `unavailable` 是 0/1 gauge。

这些指标只用于判断审计是否可用，不作为任务调度依据。

## 测试计划

单元测试：

- `pkg/controllerv2/fsm`：created、running phase、participant progress、failed、completed transition；
  noop/reject 不产生 transition；`AppliedRaftIndex` 进入 `SourceRef`。
- `pkg/controllerv2/raft` / `pkg/clusterv2/control`：observer 通过公开 runtime config 透传，并在
  applied metadata 持久化后非阻塞投递。
- `internalv2/observability/taskaudit`：append、重放、保留最近 200 个任务、每任务 50 条事件、
  compaction、坏行跳过、重复 EventID 幂等、append-during-compaction 串行化、truncation marker 重启恢复。
- `internalv2/access/node`：远程 task audit 查询 RPC codec/handler/client；若第一版选择不做远程读取，
  测试非 Controller voter 返回 service unavailable。
- `internalv2/usecase/management`：kind/status/slot/node/keyword 筛选、limit/truncated、事件详情 404、
  unavailable 映射。
- `internalv2/access/manager`：权限、查询参数校验、列表和事件响应结构；active task 和 audit task
  都接受 `slot_replica_move` kind。
- Web：当前/历史视图切换、Controller task 筛选、事件时间线抽屉、events truncated 提示。

涉及以下包的行为或路由时同步更新对应 `FLOW.md`：

- `pkg/controllerv2/FLOW.md`
- `pkg/clusterv2/control`
- `internalv2/app/FLOW.md`
- `internalv2/usecase/management/FLOW.md`
- `internalv2/access/manager/FLOW.md`
- `internalv2/access/node/FLOW.md`

验证命令按改动范围选择：

```text
go test ./pkg/controllerv2/... ./internalv2/observability/taskaudit ./internalv2/usecase/management ./internalv2/access/manager
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx
cd web && bunx tsc -b
git diff --check
```

## 后续扩展

- 如果需要高层操作历史，可新增独立 operator audit，把 node onboarding、scale-in、leader-transfer batch
  这类 manager action 单独记录；不要混进 Controller task audit。
- 如果用户需要更长历史，再把 JSONL 保留数量配置化。
- 如果 Web 需要大量历史分页，再评估专门索引或外部归档方案，而不是提前引入复杂存储。
