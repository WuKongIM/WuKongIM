# Task Audit History Design

## 背景

当前 Web 任务中心 `/cluster/tasks` 只展示当前可见的分布式任务，数据来自
`GET /manager/distributed-tasks*`。这个接口聚合了 Slot reconcile、节点扩容、节点缩容和频道迁移任务，适合查看当前异常与执行状态，但不等同于完整历史。

主要缺口是：部分 active task 在成功后会从调度状态中删除。例如 Slot reconcile 在 `TaskResult` 成功后删除 active task，ControllerV2 active tasks 也会在完成后从 control snapshot 消失。为了查看历史而保留 active task 会污染 planner、scale-in blocker 和重试语义，因此历史审计必须是独立的旁路记录。

本设计把任务历史定义为管理侧审计能力：记录任务状态变化事件，进程启动时从 JSONL 重放出有界内存投影，Web 历史视图读取该投影。它不参与调度、不改变任务状态机、不提供重试/取消等写操作。

## 目标

- 为任务中心提供可查看的任务历史和单任务事件时间线。
- 第一版覆盖现有任务中心四个 domain：`slot_reconcile`、`node_onboarding`、`node_scale_in`、`channel_migration`。
- 使用轻量 JSONL append-only 文件，不引入新的 Pebble/metadata 表。
- 默认只保留最近 200 个任务，每个任务最多保留 50 条事件。
- 当前任务接口继续表达 active/current task，历史任务接口表达审计投影。
- 审计写失败不阻塞核心任务状态变更。
- 设计字段预留后续接入 ControllerV2 task audit 的空间，但第一版不实现 ControllerV2 写入。

## 非目标

- 不把 active task 改成永不删除。
- 不支持无限历史、分页游标、复杂全文搜索或审计导出。
- 不在第一版接入 `internalv2 /manager/controller/tasks` 的 ControllerV2 active task 历史。
- 不把任务审计落到 `pkg/db/meta`，避免与 hash-slot/业务元数据混淆。
- 不新增 retry、cancel、advance 等任务写操作。

## 存储位置

新增独立包：

```text
internal/observability/taskaudit/
  FLOW.md
  model.go
  store.go
  jsonl.go
  index.go
  retention.go
```

审计属于观测/排障数据，放在 `internal/observability` 比放在 `pkg/db/meta` 更清晰。它不暴露 Pebble，也不进入业务元数据、controller metadata 或 hash-slot snapshot。

物理文件默认放在节点数据目录：

```text
${WK_NODE_DATA_DIR}/task-audit/events.jsonl
```

第一版不新增路径配置项。后续如果需要独立磁盘或迁移路径，再增加 `WK_TASK_AUDIT_DIR` 并同步 `wukongim.conf.example`。

## 数据模型

### TaskAuditEvent

`TaskAuditEvent` 是 append-only 事件。每行 JSONL 存一条事件。

```go
type TaskAuditEvent struct {
    EventID         string
    Domain          string
    TaskID          string
    EventType       string
    Status          string
    Phase           string
    Kind            string
    ScopeType       string
    ScopeID         string
    SlotID          uint32
    ChannelID       string
    ChannelType     int64
    SourceNode      uint64
    TargetNode      uint64
    OwnerNode       uint64
    Attempt         uint32
    OccurredAt      time.Time
    LastError       string
    Summary         string
    DetailSnapshot  json.RawMessage
    DetailTruncated bool
}
```

`EventType` 使用固定集合：

```text
created
claimed
running
retry_scheduled
failed
completed
cancelled
snapshot
```

`snapshot` 用于兼容上线审计功能后首次发现的已有 durable 状态。例如已有 node onboarding job 在审计启用前就存在，首次扫描可写一条 snapshot event，而不是伪造 created。

`DetailSnapshot` 是有界 JSON，保存排障关键上下文，不作为完整业务对象归档。第一版上限为 16KB；超过上限截断，并设置 `detail_truncated=true`。

### TaskAuditSnapshot

`TaskAuditSnapshot` 是内存读模型，不单独持久化。启动时从 JSONL 重放，运行时由 append 更新。

```go
type TaskAuditSnapshot struct {
    Domain      string
    TaskID      string
    Kind        string
    Status      string
    Phase       string
    ScopeType   string
    ScopeID     string
    SlotID      uint32
    ChannelID   string
    ChannelType int64
    SourceNode  uint64
    TargetNode  uint64
    OwnerNode   uint64
    Attempt     uint32
    FirstSeenAt time.Time
    LastEventAt time.Time
    TerminalAt  time.Time
    Terminal    bool
    EventCount  int
    LastError   string
    Summary     string
    Links       map[string]string
}
```

内存索引：

```go
latestByTask map[TaskKey]TaskAuditSnapshot
eventsByTask map[TaskKey][]TaskAuditEvent
```

`TaskKey` 由 `domain + task_id` 组成。查询历史任务时扫描内存 snapshots，按 `last_event_at desc` 排序后返回有界结果。查询事件时间线时读取 `eventsByTask[key]`。

## 保留策略

默认值：

```text
MaxTasks = 200
MaxEventsPerTask = 50
```

保留策略按任务数量裁剪，而不是按全局事件数量裁剪：

- 保留最近 200 个任务的审计快照和事件。
- 每个任务最多保留 50 条事件。
- 当任务数超过 200 时，优先删除最旧的 terminal 任务。
- 非 terminal 任务不优先删除，避免未处理的 failed、blocked、retrying、running 任务丢失审计上下文。
- 如果非 terminal 任务数量本身超过上限，保留最近 200 个非 terminal 任务，并记录一次审计裁剪日志。

`failed` 不总是 terminal。是否 terminal 按来源语义决定：

- Slot reconcile 普通 failed 仍是 active 异常任务，不视为 terminal。
- Leader transfer 失败并删除 active task 进入 cooldown 时视为 terminal failed。
- Channel migration 的 completed、failed、aborted/cancelled 视为 terminal。
- Node onboarding 的 completed、cancelled 视为 terminal；failed 保留为非 terminal，便于后续人工处理或重试。
- Node scale-in 的 remove 成功视为 terminal completed；start/drain/advance 后的状态不是 terminal。

## JSONL 写入与压缩

写入流程：

1. 构造 `TaskAuditEvent`。
2. `Append` 加锁向 `events.jsonl` 追加一行 JSON，并 flush。
3. 更新内存 `eventsByTask` 和 `latestByTask`。
4. 应用保留策略。
5. 必要时异步触发 compaction。

compaction 使用临时文件：

```text
events.jsonl
events.compacting
```

压缩时将当前内存中仍保留的任务事件重新写入 `events.compacting`，fsync 后原子 rename 替换 `events.jsonl`。压缩失败不影响后续追加，旧文件继续使用，下次再试。

启动加载：

- 文件不存在时按空审计启动。
- 单行 JSON 损坏时跳过该行，计数 `corrupt_lines` 并继续加载后续行。
- 目录不可写时审计能力标记 unavailable，但主程序继续运行。

幂等：

- `EventID` 由稳定字段组成：`domain/task_id/event_type/status/phase/attempt/occurred_at_unix_nano`。
- 同一 `EventID` 重复 append 时内存投影 no-op。
- 如果旧事件重放顺序导致 `OccurredAt < snapshot.LastEventAt`，不覆盖 snapshot 最新状态，但仍可保留在该任务事件时间线中。

## 写入点

第一版只在真实状态变化边界写审计，不在普通 status 查询中写。

### Slot reconcile

在 controller state machine 的 `TaskResult` 应用路径写审计：

- 创建任务时写 `created`。
- 失败且还能重试时写 `retry_scheduled`。
- 超过重试上限时写 `failed`，非 terminal。
- 成功删除 active task 前写 `completed`，terminal。
- leader transfer 失败并删除 task、写入 cooldown 时写 `failed`，terminal。

### Node onboarding

在 job 状态更新路径写审计：

- planned、running、failed、completed、cancelled 都写事件。
- move 级别变化放入 `detail_snapshot`，第一版不把每个 move 展开成独立任务。

### Node scale-in

节点缩容当前是 manager 派生状态，不是 durable task。第一版只在显式 operator action 后写审计：

- start
- drain
- advance
- remove
- cancel/resume

纯 `GET status` 不写审计，避免刷新页面产生噪声。

### Channel migration

在 channel migration task 创建、claim、advance、terminal transition、abort 路径写审计：

- 创建写 `created`。
- claim/renew owner 写 `claimed`。
- phase/progress 变化写 `running` 或 `retry_scheduled`。
- completed、failed、aborted/cancelled 写 terminal 事件。

## API

新增 manager 只读接口：

```text
GET /manager/distributed-task-audits?domain=&status=&node_id=&scope=&keyword=&limit=
GET /manager/distributed-task-audits/:domain/:id/events
```

权限沿用任务中心：

```text
cluster.task:r
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

`limit` 只控制返回数量，不是分页游标。最大值不超过 `MaxTasks`。`total` 是当前保留集合中筛选后的数量；当 `total > limit` 时返回 `truncated=true`。

支持筛选：

- `domain`
- `status`
- `node_id`
- `scope`
- `keyword`

`keyword` 只在内存快照字段中做轻量 contains 匹配，匹配字段包括 task id、domain、kind、status、phase、scope id、summary、last error。不做倒排索引。

事件响应：

```json
{
  "task": {},
  "events": [],
  "truncated": false
}
```

如果任务已被 retention 清理，返回 404。

## Web 设计

`/cluster/tasks` 增加视图切换：

```text
当前任务 | 历史任务
```

当前任务继续使用：

```text
GET /manager/distributed-tasks/summary
GET /manager/distributed-tasks
GET /manager/distributed-tasks/:domain/:id
```

历史任务使用：

```text
GET /manager/distributed-task-audits
GET /manager/distributed-task-audits/:domain/:id/events
```

历史视图不分页，默认展示最多 200 个保留任务。它保留 domain、status、scope、node、keyword 筛选，列表字段复用当前任务的 operator-facing 字段。详情抽屉展示：

- 任务最新快照。
- 事件时间线。
- 每条事件的 detail snapshot。
- detail truncated 提示。

默认页面仍优先展示当前任务，保持排障入口聚焦异常、重试、阻塞和运行中的任务。

## 错误处理

- 审计写失败不阻塞任务状态变更，只记录 warn 日志并增加 metric。
- JSONL 单行损坏跳过，其余行继续加载。
- 文件不存在按空审计启动。
- 目录不可写时审计能力 unavailable，接口返回 service unavailable，主程序继续运行。
- compaction 失败不影响追加写。
- detail snapshot 超限截断并标记 `detail_truncated=true`。

## 观测指标

新增轻量 metrics：

```text
wukongim_task_audit_append_total{result}
wukongim_task_audit_events_retained
wukongim_task_audit_tasks_retained
wukongim_task_audit_compactions_total{result}
wukongim_task_audit_corrupt_lines_total
wukongim_task_audit_unavailable
```

这些指标用于判断审计是否可用，不作为任务调度依据。

## 测试计划

单元测试：

- `internal/observability/taskaudit`：append、重放、保留 200 个任务、每任务 50 条事件、compaction、坏行跳过、重复 EventID 幂等、并发 append。
- management usecase：domain/status/node/scope/keyword 筛选、limit/truncated、事件详情 404、unavailable 映射。
- 写入点：Slot reconcile 成功删除前写 completed；失败重试写 retry_scheduled；terminal failed/cancelled 进入历史；node scale-in status 查询不写审计。
- manager HTTP：权限、查询参数校验、列表和事件响应结构。
- Web：当前/历史视图切换、历史筛选、事件时间线抽屉、truncated 提示。

验证命令按改动范围选择：

```text
go test ./internal/observability/taskaudit ./internal/usecase/management ./internal/access/manager
cd web && bun run test -- src/lib/manager-api.test.ts src/pages/tasks/page.test.tsx
cd web && bunx tsc -b
git diff --check
```

## 后续扩展

- 接入 ControllerV2 task audit，在 ControllerV2 command apply 或 task writer 边界追加事件。
- 如果用户需要更长历史，再把 JSONL 保留数量配置化。
- 如果 Web 需要大量历史分页，再评估专门索引或外部归档方案，而不是提前引入复杂存储。
