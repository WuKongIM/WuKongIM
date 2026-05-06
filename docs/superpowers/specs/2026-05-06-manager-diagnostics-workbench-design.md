# 管理后台消息诊断工作台设计

- 日期：2026-05-06
- 范围：`web` 管理后台“诊断 / 消息链路”页面、manager diagnostics 只读 API、节点内 diagnostics 查询聚合、节点间 diagnostics RPC
- 关联目录：
  - `web/src/pages/diagnostics`
  - `web/src/lib/manager-api.ts`
  - `web/src/lib/manager-api.types.ts`
  - `web/src/lib/navigation.ts`
  - `web/src/i18n/messages`
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/access/node`
  - `internal/app/diagnostics.go`
  - `internal/observability/diagnostics`

## 1. 背景

当前管理后台已经具备 `Dashboard`、`Nodes`、`Slots`、`Channels`、`Messages`、`Connections`、`Network`、`Controller`、`Slot Logs`、`Onboarding` 等页面，但还缺少一个面向“消息发送链路”的诊断入口。`web/src/pages/topology/page.tsx` 仍是占位页，现有 `Messages` 页面也更偏向频道消息查询，不负责解释消息为什么丢失、超时、重复或卡在复制/投递阶段。

后端已经有节点内 diagnostics 能力：

- `internal/observability/diagnostics` 定义了 `Event`、`Query`、`QueryResult`、bounded in-memory store、索引和摘要。
- `internal/app/diagnostics.go` 暴露 `QueryDiagnostics(ctx, query)`，返回当前节点的 diagnostics 结果。
- `internal/access/api/diagnostics.go` 暴露 `/debug/diagnostics/...` 节点内 debug API。
- sendtrace 事件已经覆盖网关、message usecase、channel append、replica、runtime replication 等关键路径。

但 `/debug/diagnostics/...` 是节点内 debug API，不适合直接给浏览器作为后台入口：它没有 manager 权限模型，也不能提供统一的跨节点聚合视角。因此第一期应新增 manager 级只读诊断 API 和 `web` 诊断工作台。

## 2. 目标与非目标

## 2.1 目标

1. 在管理后台新增 `/diagnostics` 页面，作为消息链路故障定位入口。
2. 支持按 `trace_id`、`client_msg_no`、`channel_key + message_seq` 和最近异常事件查询 diagnostics 数据。
3. 新增 manager 只读 API，由 manager 负责鉴权、参数校验、节点聚合、partial 结果和稳定 JSON DTO。
4. 新增节点间 diagnostics RPC，让 manager 能查询本节点和远端节点的 node-local diagnostics store，而不是让浏览器直连 debug API。
5. 页面展示链路摘要、阶段时间线、节点泳道、事件明细和关联跳转，帮助运维定位故障发生在哪个阶段、哪个节点、哪个 slot/channel。
6. 单节点集群应正常展示为 `local_node` 或 `cluster` scope 下的一个节点结果，不引入独立的单机语义。
7. 诊断数据必须保持安全：不返回 payload，不返回未脱敏 UID，不暴露 debug API 给后台页面。

## 2.2 非目标

当前阶段明确不做：

- 不接入 OpenTelemetry、Jaeger、Grafana Loki 或其他外部观测系统。
- 不把 diagnostics 事件持久化；第一期只查询 bounded in-memory store 中最近保留的事件。
- 不做实时流式订阅或 websocket 推送。
- 不在诊断页面提供自动修复、重试、leader transfer、node drain 等写操作。
- 不让 `web` 直接访问 `/debug/diagnostics/...`。
- 不承诺全历史搜索；事件可能因 ring buffer、索引上限或采样策略而过期。
- 不新增绕过集群语义的业务分支；即使只有一个节点，也按单节点集群处理。

## 3. 方案比较

## 3.1 方案 A：Manager 聚合 API + Web 诊断工作台（推荐）

浏览器只调用 `/manager/diagnostics/...`。manager 使用现有权限体系鉴权，并通过本地 `App.QueryDiagnostics` 和新增节点 RPC 查询远端节点 diagnostics store，然后聚合为页面 DTO。

优点：

- 安全边界清晰，复用 manager JWT 和权限。
- 页面入口稳定，不依赖 debug API 是否暴露。
- 支持跨节点排查，能返回部分节点失败的 `partial` 状态。
- 符合现有分层：`access -> usecase/runtime`，`app -> all`。

代价：

- 需要新增 manager usecase、access handler、node RPC codec/client/adapter 和前端页面。

## 3.2 方案 B：Web 直接访问节点内 debug API

页面直接请求 `/debug/diagnostics/...`。

优点：实现最少。

缺点：绕过 manager 权限，跨节点体验差，CORS/部署复杂，也会把 debug API 暴露给管理后台。该方案不推荐进入生产后台。

## 3.3 方案 C：外部 trace/log 平台优先

将 diagnostics 事件输出到外部观测平台，由后台嵌入或跳转。

优点：适合更大规模生产查询和长期留存。

缺点：依赖外部系统，不能快速补齐当前 manager 闭环。可以作为后续增强，不作为第一期。

## 4. 推荐设计

采用方案 A。第一期将 diagnostics 作为 manager 只读能力落地，页面重点回答五个问题：

1. 这次查询有没有找到事件？
2. 链路是否完整，最终状态是 `ok`、`error`、`timeout`、`partial` 还是 `not_found`？
3. 首个失败阶段和最慢阶段是什么？
4. 涉及哪些节点、slot、channel、message_seq 和 peer node？
5. 下一步应该打开哪个现有页面继续排查？

## 5. 后端设计

## 5.1 Manager API

新增只读接口：

```text
GET /manager/diagnostics/trace/:trace_id
GET /manager/diagnostics/message
GET /manager/diagnostics/events
```

查询参数：

```text
node_id        可选；指定节点，不传则查询可见集群节点
limit          可选；默认 100，最大 500
client_msg_no  /message 查询用
channel_key    /message 查询用
message_seq    /message 查询用，必须为正整数
stage          /events 查询用
result         /events 查询用；第一期需要进入 node-local diagnostics.Query，在节点内过滤后再按 limit 截断
```

语义：

- `trace/:trace_id`：按 trace 精确查询。
- `message?client_msg_no=...`：按客户端消息号查询。
- `message?channel_key=...&message_seq=...`：按 channel key 和消息序号查询。
- `/message` 同时传 `client_msg_no` 和 `channel_key + message_seq` 时返回 `400`，避免隐式优先级导致查错链路。
- `events?stage=...&result=error`：查询最近阶段异常；不传 stage 时表示最近 diagnostics 事件。
- `node_id` 存在时只查指定节点；不存在时查集群节点列表中的 alive/suspect/draining 节点，dead 节点记录为 skipped note，不阻塞整体返回。

`result` 过滤必须在节点内 diagnostics store 的 query 阶段完成，不能由 manager 在已截断的 node result 上做二次过滤，否则最近异常会被普通事件挤掉。实现时应扩展 `diagnostics.Query` 增加 `Result diagnostics.Result`，并在 store 过滤后再排序和截断；manager 聚合层只做跨节点合并后的最终排序与截断。

权限：

```text
cluster.diagnostics:r
```

若 manager auth 开启，三个接口都要求 `cluster.diagnostics:r`。已有 `*:*` 继续拥有全部权限。第一期不提供 diagnostics 写权限。

## 5.2 Manager 响应 DTO

新增 manager-facing DTO，避免直接把 node-local `diagnostics.QueryResult` 原样暴露给前端。建议 JSON：

```json
{
  "scope": "cluster",
  "status": "timeout",
  "generated_at": "2026-05-06T12:00:00Z",
  "query": {
    "trace_id": "tr-1",
    "limit": 100
  },
  "summary": {
    "first_failure_stage": "channel_append",
    "first_failure_result": "timeout",
    "first_failure_error_code": "context_deadline_exceeded",
    "slowest_stage": "replica_quorum",
    "slowest_duration_ms": 812,
    "involved_nodes": [1, 2, 3],
    "peer_nodes": [2],
    "slot_id": 12,
    "channel_key": "2:g1",
    "client_msg_no": "c-1",
    "message_seq": 88,
    "event_count": 14
  },
  "nodes": [
    {
      "node_id": 1,
      "status": "ok",
      "duration_ms": 3,
      "event_count": 8,
      "notes": []
    },
    {
      "node_id": 2,
      "status": "unavailable",
      "duration_ms": 0,
      "event_count": 0,
      "notes": ["diagnostics RPC timeout"]
    }
  ],
  "events": [],
  "notes": ["node 2 unavailable; result is incomplete"]
}
```

字段规则：

- `scope` 默认返回 `cluster`；只有显式 `node_id` 查询或 controller snapshot 不可用而降级为本节点查询时，才返回 `local_node`。
- `status` 由聚合层按固定算法计算：先看事件失败，再看结果完整性，再看是否命中事件。
- `error` 表示至少一个已返回事件 `result=error`。
- `timeout` 表示没有 `result=error`，但至少一个已返回事件 `result=timeout`。
- `partial` 表示没有 error/timeout 事件，但存在 `result=partial`、`result=dropped`、`result=canceled`，或至少一个目标节点不可达、被跳过、controller snapshot 不完整。
- `not_found` 表示所有目标节点均成功返回且合并后的事件数为 0。
- `ok` 表示所有目标节点均成功返回、合并后的事件数大于 0，且没有 error/timeout/partial/dropped/canceled 事件。
- 如果同时存在失败事件和不可达节点，顶层 `status` 仍按 `error` 或 `timeout` 返回，节点级 `nodes` 和 `notes` 继续展示 partial 事实。
- `result=skipped` 是事件级信息，不单独抬升顶层 status；但节点级 `status=skipped` 表示未查询某个目标节点，会抬升顶层 `partial`。
- 示例：所有节点可达但没有事件命中时返回 `status=not_found`；node 1 命中事件、node 2 返回 `not_found`、node 3 可达但无事件时按事件结果返回 `ok/error/timeout`，不因 node-local `not_found` 变 partial；没有失败事件但 node 3 RPC 不可达时返回 `status=partial`；同时存在 `result=error` 和 node 3 不可达时返回 `status=error`，并在 notes 说明结果不完整。
- `events` 是所有成功节点返回事件的合并结果，按 `at` 升序排序，超过 `limit` 时保留最近 `limit` 条。
- manager DTO 中事件耗时统一输出为 `duration_ms`，不得直接透传 Go `time.Duration` 的纳秒整数。
- `nodes` 保留每个节点的状态摘要，便于页面展示 partial 和 unavailable。
- `summary` 从合并事件计算，不信任单个节点 summary 作为全局结论。
- `events[].from_uid` 必须为空；现有 store 已 redaction，聚合层仍应避免重新填充敏感字段。
- 不返回消息 payload、用户 token、客户端远端地址等非必要敏感信息。

## 5.3 Management usecase

在 `internal/usecase/management` 新增 diagnostics 用例：

```go
// DiagnosticsReader queries diagnostics events from local or remote cluster nodes.
type DiagnosticsReader interface {
    QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

type DiagnosticsQueryRequest struct {
    NodeID uint64
    Query  diagnostics.Query
}

func (a *App) QueryDiagnostics(ctx context.Context, req DiagnosticsQueryRequest) (DiagnosticsQueryResponse, error)
```

职责：

- 选择查询节点：指定 `node_id` 时只查该节点；未指定时使用 `ClusterReader.ListNodesStrict(ctx)` 获取节点列表。
- 限制并标准化 `limit`。
- 并发查询每个目标节点，但设置小的并发上限，避免管理查询压垮集群。
- 对远端失败做 partial，不因单个节点失败丢弃其他节点结果。
- 合并、排序、最终截断和计算 summary；`result` 过滤必须通过 `diagnostics.Query.Result` 下推到节点内 store。
- 单节点集群只查询本节点，返回正常结果。

节点选择建议：

- 第一版默认查询 `alive`、`suspect` 和 `draining` 节点；draining 节点仍可能持有连接、runtime 状态或尚未完成的投递链路，不能默认跳过。
- `dead` 节点不发 RPC，写入 node summary 和 notes，节点级状态为 `skipped`。
- 如果 `ListNodesStrict` 不可用，则降级为只查询本节点，并将整体状态标记为 `partial`，note 写明 controller snapshot unavailable。

## 5.4 App 层 diagnostics reader

在 `internal/app` 增加一个窄适配器，注入 management usecase：

```go
type managementDiagnosticsReader struct {
    localNodeID uint64
    local       interface {
        QueryDiagnostics(context.Context, diagnostics.Query) diagnostics.QueryResult
    }
    remote *accessnode.Client
}

func (r managementDiagnosticsReader) QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
    if nodeID == r.localNodeID {
        return r.local.QueryDiagnostics(ctx, query), nil
    }
    return r.remote.QueryDiagnostics(ctx, nodeID, query)
}
```

该适配器属于组合根附近的 glue，不把 access/node 依赖引入 usecase 包。

## 5.5 节点间 diagnostics RPC

在 `internal/access/node` 新增 diagnostics RPC，而不是复用 HTTP debug API：

- 新增 service id：`diagnosticsRPCServiceID uint8 = 42`。
- `Options` 增加 `Diagnostics DiagnosticsProvider`。
- `Adapter.New` 注册 `handleDiagnosticsRPC`。
- `Client` 增加 `QueryDiagnostics(ctx, nodeID, query)`。
- codec 采用项目内 node RPC 常用的二进制 codec；测试中拒绝 JSON payload，保持和现有高性能 RPC 风格一致。

RPC DTO：

```go
type DiagnosticsProvider interface {
    QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
}

type diagnosticsRequest struct {
    Query diagnostics.Query
}

type diagnosticsResponse struct {
    Status string
    Result diagnostics.QueryResult
}
```

错误处理：

- 本地 diagnostics store 关闭时不返回 RPC error，而返回 `StatusOK` + `QueryResult{Status:not_found, Notes:[...]}`，与 `internal/app/diagnostics.go` 当前语义一致。
- RPC 编码错误、远端不可达、超时返回 error，由 management 聚合为 node-level `unavailable`。

## 5.6 Access manager handler

在 `internal/access/manager/diagnostics.go` 新增 handler。职责仅限：

- 解析 path/query 参数。
- 校验 `limit`、`node_id`、`message_seq`。
- 构造 `management.DiagnosticsQueryRequest`。
- 调用 `s.management.QueryDiagnostics(...)`。
- 将 usecase DTO 转成 JSON。
- 按现有 manager 错误风格返回 `400`、`403`、`503`。

路由注册：

```go
diagnosticsRoutes := s.engine.Group("/manager")
if s.auth.enabled() {
    diagnosticsRoutes.Use(s.requirePermission("cluster.diagnostics", "r"))
}
diagnosticsRoutes.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
diagnosticsRoutes.GET("/diagnostics/message", s.handleDiagnosticsMessage)
diagnosticsRoutes.GET("/diagnostics/events", s.handleDiagnosticsEvents)
```

## 6. 前端设计

## 6.1 导航

在 `web/src/lib/navigation.ts` 的 observability 分组新增：

```text
/diagnostics
```

建议位于 `Network` 和 `Controller` 之前，因为它是故障定位入口，不是底层日志页。

页面标题：

- 中文：`消息诊断`
- 英文：`Message Diagnostics`

## 6.2 页面布局

新增 `web/src/pages/diagnostics/page.tsx`。

页面结构：

```text
PageHeader
  标题、说明、scope badge、数据保留/采样提示

QueryPanel
  Tab 1: Trace ID
  Tab 2: Client Msg No
  Tab 3: Channel + Seq
  Tab 4: Recent Errors
  可选 node_id、limit

SummaryStrip
  status、slowest stage、first failure、involved nodes、slot/channel、event count

TimelineSection
  按时间排序的 stage timeline
  error/timeout/partial 高亮

NodeLanesSection
  按 node_id 分组，展示每个节点的事件和 unavailable 状态

EventTable
  stage、result、duration_ms、node、peer_node、slot、channel_key、message_seq、error_code、service、attempt、queue_depth

RelatedLinks
  Messages / Channels / Slot Logs / Connections / Network

ExportPanel
  复制或下载当前诊断 JSON
```

第一版可以先实现 QueryPanel、SummaryStrip、TimelineSection、EventTable 和 JSON 导出；NodeLanes 可以用轻量分组列表，不必做复杂图形。

## 6.3 查询表单

表单状态：

```ts
type DiagnosticsQueryMode = "trace" | "client_msg_no" | "channel_seq" | "recent_errors"

type DiagnosticsQueryForm = {
  mode: DiagnosticsQueryMode
  traceId: string
  clientMsgNo: string
  channelKey: string
  messageSeq: string
  stage: string
  result: "" | "error" | "timeout" | "partial" | "dropped" | "canceled" | "skipped"
  nodeId: string
  limit: string
}
```

校验：

- `trace` 要求 `traceId` 非空。
- `client_msg_no` 要求 `clientMsgNo` 非空。
- `channel_seq` 要求 `channelKey` 非空且 `messageSeq` 为正整数。
- `recent_errors` 默认 `result=error`，允许 stage 为空。
- `nodeId` 可空；非空时必须为正整数。
- `limit` 默认 100，范围 1 到 500。

## 6.4 API client/types

在 `web/src/lib/manager-api.types.ts` 新增 diagnostics 类型：

```ts
export type ManagerDiagnosticsStatus = "ok" | "error" | "timeout" | "partial" | "not_found"

export type ManagerDiagnosticsEvent = {
  trace_id?: string
  span_id?: string
  parent_span_id?: string
  stage: string
  at: string
  duration_ms?: number
  node_id?: number
  peer_node_id?: number
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  range_start?: number
  range_end?: number
  service?: string
  result: string
  error_code?: string
  error?: string
  attempt?: number
  queue_depth?: number
  replica_role?: string
  sample_reason?: string
}

export type ManagerDiagnosticsSummary = {
  first_failure_stage?: string
  first_failure_result?: string
  first_failure_error_code?: string
  slowest_stage?: string
  slowest_duration_ms?: number
  involved_nodes: number[]
  peer_nodes: number[]
  slot_id?: number
  channel_key?: string
  client_msg_no?: string
  message_seq?: number
  event_count: number
}

export type ManagerDiagnosticsNodeResult = {
  node_id: number
  status: "ok" | "not_found" | "unavailable" | "skipped"
  duration_ms: number
  event_count: number
  notes: string[]
}

export type ManagerDiagnosticsResponse = {
  scope: "cluster" | "local_node"
  status: ManagerDiagnosticsStatus
  generated_at: string
  query: Record<string, unknown>
  summary: ManagerDiagnosticsSummary
  nodes: ManagerDiagnosticsNodeResult[]
  events: ManagerDiagnosticsEvent[]
  notes: string[]
}
```

在 `web/src/lib/manager-api.ts` 新增：

```ts
getDiagnosticsTrace(traceId, params)
getDiagnosticsMessage(params)
getDiagnosticsEvents(params)
```

## 6.5 状态与空态

页面状态映射：

- `ok`：绿色，链路查询成功且没有失败事件。
- `error`：红色，突出首个 `result=error` 事件。
- `timeout`：橙色；仅当没有 `result=error`，但存在 `result=timeout` 事件时使用，突出超时阶段和 peer node。
- `partial`：黄色；仅当没有 error/timeout 事件但存在 partial/dropped/canceled 事件，或结果不完整时使用，展示不可达或 skipped 节点和已返回节点，不能提示“全部正常”。
- `not_found`：空态，提示可能原因：diagnostics 未启用、采样未命中、事件已被 ring buffer 覆盖、查询字段不正确、查询节点不对。
- `forbidden`：说明当前 manager 用户缺少 `cluster.diagnostics:r`。
- `unavailable`：说明 manager 或 diagnostics 聚合不可用。

## 6.6 关联跳转

从事件和 summary 生成跳转：

- 有 `channel_key` 且能解析出 `channel_type/channel_id` 时，跳 `/messages?channel_id=...&channel_type=...`。
- 有 `slot_id` 和 `node_id` 时，跳 `/slot-logs?slot_id=...&node_id=...`。
- 有 `node_id` 时，跳 `/nodes` 或 `/connections?node_id=...`。
- 有 `channel_key` 但无法安全解析时，只展示 channel key，不生成错误链接。

注意：当前 diagnostics 使用的是 `channel_key`，不保证一定可反解为 `channel_id/channel_type`。第一期可以仅在格式明确时生成链接，后续可在事件中补充独立的 `channel_id` 和 `channel_type` 字段。

## 7. 数据流

```text
Operator opens /diagnostics
  -> Web validates query form
  -> Web calls /manager/diagnostics/...
  -> access/manager parses params and checks cluster.diagnostics:r
  -> management.QueryDiagnostics selects target nodes
  -> local node uses App.QueryDiagnostics
  -> remote nodes use access/node diagnostics RPC
  -> each node reads local diagnostics Store
  -> management merges node results, sorts events, computes summary
  -> Web renders summary, timeline, node lanes, event table, related links
```

## 8. 错误处理

后端：

- 参数错误返回 `400`，并使用现有 manager error envelope。
- 权限不足返回 `403`。
- 全部节点不可达且没有本地结果时返回 `200` + `status=partial`，并在 `nodes` 和 `notes` 中列出每个不可达节点；只有 management dependency 未配置等服务自身不可用场景返回 `503`。
- management dependency 未配置时返回 `503`。
- diagnostics store disabled 返回 `status=not_found`，notes 说明 store disabled。
- context canceled/timeout 时尽量返回已完成节点结果，并标记 partial。

前端：

- 维持现有 `ManagerApiError` 映射风格。
- 查询中禁用提交按钮，刷新时保留旧结果并显示 refreshing 状态。
- partial 不能使用成功 toast；必须明确显示不可达节点。
- JSON 导出使用当前响应对象，不重新请求。

## 9. 性能与安全

性能：

- 默认 limit 100，最大 500。
- 聚合查询并发限制建议为 8 或 `min(8, node_count)`。
- 每个节点 query 使用短超时，例如 2s 到 3s，避免后台查询长期挂起。
- 只返回必要字段，不返回 payload。
- 不做全量扫描型历史查询；store 本身已有 bounded indexes。

安全：

- 只读权限 `cluster.diagnostics:r`。
- 不暴露 `/debug/diagnostics/...` 给 web。
- 不返回 `from_uid`、payload、token、客户端请求 body。
- error 文本继续受 `MaxErrorBytes` 限制。
- 如果未来增加按 UID 查询，必须先设计脱敏和权限边界，不能直接复用用户标识搜索。

## 10. 测试计划

后端单元测试：

```text
go test ./internal/observability/diagnostics ./internal/access/node ./internal/usecase/management ./internal/access/manager -count=1
```

重点覆盖：

- `internal/access/node` diagnostics RPC codec roundtrip。
- diagnostics RPC 拒绝 JSON payload。
- local diagnostics disabled 返回 `not_found` result，而不是 RPC error。
- management 按 trace/client/channel_seq 查询成功。
- management 多节点结果按时间排序。
- management 所有目标节点可达但无事件时返回 `not_found`。
- management 默认查询 alive/suspect/draining 节点，并将 dead 节点标记为 skipped。
- management 对单节点失败返回 partial，并保留其他节点事件。
- management `result` filter 正确。
- manager handler 校验 invalid limit/node_id/message_seq。
- manager handler 权限不足返回 403。
- manager handler 绑定 `cluster.diagnostics:r`。

前端测试：

```text
cd web && yarn test
```

重点覆盖：

- `/diagnostics` 路由和导航渲染。
- trace/client/channel/recent error 四种查询表单校验。
- API client URL 拼接和参数编码。
- `ok/error/timeout/partial/not_found/forbidden/unavailable` 状态渲染。
- timeline 按 `at` 排序。
- partial 节点摘要展示。
- 关联跳转 URL 生成。
- JSON 导出包含当前响应。

集成测试：

第一期可增加一个定向集成测试，复用现有 diagnostics/sendtrace 覆盖路径：

```text
go test -tags=integration ./internal/app -run TestManagerDiagnosticsAggregatesSendTrace -count=1
```

该测试不应成为常规开发必跑项；日常提交前优先跑相关单元测试。

## 11. 分阶段落地

## 11.1 Phase 1：只读诊断闭环

- 新增 access/node diagnostics RPC。
- 新增 management diagnostics 聚合用例。
- 新增 access/manager diagnostics routes。
- 新增 web `/diagnostics` 页面、API client、types、i18n、导航。
- 页面支持查询、summary、timeline、event table、partial 状态和 JSON 导出。

## 11.2 Phase 2：关联体验增强

- 事件中补充独立 `channel_id`、`channel_type` 字段，避免从 `channel_key` 猜测。
- `/channels` 支持通过 URL 打开指定 channel detail。
- `/slot-logs` 支持从 URL 自动填充 slot/node 查询。
- `/connections` 支持 node_id URL filter。
- `/topology` 引入 diagnostics 关联高亮。

## 11.3 Phase 3：生产观测增强

- 可选持久化或外部 trace exporter。
- 支持按事故窗口聚合查询。
- 支持导出更完整的 diagnostics bundle。
- 与 runbook/incident console 连接。

## 12. 实现期固定决策与检查项

1. 第一版只在 `channel_key` 能安全反解时生成 `/messages` 和 `/channels` 链接；无法反解时只展示原始 `channel_key`。
2. 跨节点默认查询 `alive`、`suspect` 和 `draining` 节点；`dead` 节点不发 RPC，只在 node summary 和 notes 中说明，节点状态标记为 `skipped`。
3. 全部目标节点不可达时返回 `200 + status=partial`，让页面能展示节点级失败明细；服务自身依赖未配置才返回 `503`。
4. 实现时检查 `wukongim.conf.example` 是否存在非 wildcard manager 权限示例；如果存在，需要补充 `cluster.diagnostics:r`。
