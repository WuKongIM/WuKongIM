# 管理后台网络观测页面设计

- 日期：2026-04-29
- 范围：`web` 管理后台“观测 / 网络”页面、manager network 只读 API、节点间 transport / RPC / Channel 复制数据面的观测快照
- 关联目录：
  - `web/src/pages/network`
  - `web/src/lib/manager-api.ts`
  - `web/src/lib/manager-api.types.ts`
  - `internal/access/manager`
  - `internal/usecase/management`
  - `internal/app/observability.go`
  - `pkg/transport`
  - `pkg/cluster`
  - `pkg/channel/transport`

## 1. 背景

当前管理后台已有 `Nodes`、`Connections`、`Slots`、`Topology` 等页面，但 `web/src/pages/network/page.tsx` 仍是占位页，只说明 manager API 尚未暴露 transport 或 throughput 数据。

分布式网络相关代码已经具备较明确的分层：

- `pkg/transport` 提供节点间 TCP frame、MuxConn、priority writer、连接池和 RPCMux。
- `pkg/cluster` 基于同一 transport 运行 Raft 消息、Controller RPC、Managed Slot RPC、Forward RPC 和 observation hint。
- `internal/app` 额外创建 Channel data-plane pool，用于 `pkg/channel/transport` 的 fetch、reconcile probe、long-poll fetch。
- `internal/access/node` 在 cluster RPCMux 上注册 presence、delivery、conversation、channel append、runtime summary 等节点间业务 RPC。
- `internal/app/observability.go` 已通过 `TransportObserver` 和 `ObserverHooks` 记录 Prometheus 指标，包括 bytes、dial、enqueue、RPC inflight、RPC duration、pool active/idle。

但这些信息目前只进入 `/metrics`，manager API 没有稳定 JSON DTO；前端也没有节点间网络视角。新页面应成为“节点间通信观测入口”，而不是复用客户端连接清单。

## 2. 目标与非目标

## 2.1 目标

1. 重新开发 `web` 管理后台“观测 / 网络”页面，展示节点间 transport、RPC、Channel data-plane 和 discovery/config 状态。
2. 新增 manager network 只读 DTO，避免前端直接解析 Prometheus 文本。
3. 第一版以当前节点视角为准，明确标注 `local_node` scope；后续可扩展为 Controller leader 聚合的全局网络视角。
4. 对单节点集群给出正常空态：无远端 peer 是正常状态，不展示为故障。
5. 将客户端 Gateway connections 与节点间 transport 明确区分，避免与 `/manager/connections` 页面职责重叠。
6. 网络页面和拓扑页面分工明确：网络看通信链路，拓扑看副本、Slot、Channel 关系。
7. 支持运维快速定位 dial error、queue full、RPC timeout、observation stale、Channel long-poll reset 等问题。

## 2.2 非目标

当前阶段明确不做：

- 不实现完整全局 peer-to-peer matrix；没有远端节点视角的数据不得假装完整。
- 不让浏览器直接抓取和解析 `/metrics`。
- 不在网络页面实现节点 drain、scale-in、Slot leader transfer 等写操作。
- 不把客户端连接列表迁移到网络页面；客户端连接继续由 `/connections` 页面负责。
- 不替代 Grafana dashboard；页面只提供 manager 交互入口所需的摘要和 drilldown。
- 不引入绕过集群语义的“单机模式”分支；单节点部署统一表述为“单节点集群”。

## 3. 设计原则

## 3.1 Scope 先准确，再扩展

第一版只承诺当前节点视角：

- 当前节点到其他 peer 的 pool、RPC、dial、enqueue 数据；bytes 第一版只能作为本节点总量或按 msgType 汇总，除非后续扩展 transport hook 携带 peer identity。
- Controller leader 观测到的节点健康、Slot runtime view freshness。
- Channel data-plane 的本地 outbound pool 与 RPC service 状态；lane / reset / backpressure 明细放到后续 runtime 观测扩展。

页面必须在 header 中明确显示 `Scope: local-node view`。后续当每个节点都能上报 network snapshot 时，再扩展为 `cluster aggregate view`。

## 3.2 事件语义优先于裸指标

网络页需要把底层指标解释成可操作的状态：

- `long_poll_fetch` timeout 是正常长轮询返回，不应作为错误突出展示。
- `queue_full` 表示本地 priority writer 或 RPC 队列压力，应高亮；当前 transport-level enqueue 只能定位到 peer + queue kind，RPC client lifecycle 才能定位到 peer + service。
- `dial_error` 表示本地到 peer 的连接建立失败，应高亮；当前只能定位到 peer，不能可靠归因到具体 RPC service。
- pool active 为 0 不一定异常，空闲集群可以没有已建立 outbound 连接。
- node dead/suspect 来自 Controller heartbeat，不等同于 pool 连接状态。

## 3.3 API DTO 面向页面稳定

新增 manager network DTO，字段应为页面直接消费的稳定结构，而不是 Prometheus label 或内部 struct 直出。指标名称和 Prometheus label 可以变化，但 manager DTO 尽量保持兼容。

## 3.4 网络与拓扑分离

网络页面回答：

- 当前节点能否连到 peer？
- 哪些 RPC 服务慢、失败、积压？
- Channel 复制长轮询是否健康？
- discovery 地址、pool、队列是否异常？

拓扑页面回答：

- Slot / Channel 副本如何分布？
- 哪些节点是 leader / follower？
- 哪些关系存在 quorum、ISR 或 placement 异常？

## 4. 后端设计

## 4.1 新增 manager API

新增只读接口：

```text
GET /manager/network/summary
GET /manager/network/peers
GET /manager/network/peers/:node_id
GET /manager/network/events?limit=50
```

第一版也可以只落一个聚合接口：

```text
GET /manager/network/summary
```

`summary` 返回页面首屏所需的全部数据；后续再拆分 peers / events 以支持分页和局部刷新。

权限建议：

- 新增资源：`cluster.network`
- 读权限：`cluster.network:r`
- 如果为了减少第一版配置扩散，也可临时要求 `cluster.overview:r` + `cluster.node:r`，但最终应独立成 `cluster.network:r`。

## 4.2 usecase 位置

在 `internal/usecase/management` 增加 network 聚合用例，例如：

```go
ListNetworkSummary(ctx context.Context) (NetworkSummary, error)
GetNetworkPeer(ctx context.Context, nodeID uint64) (NetworkPeerDetail, error)
ListNetworkEvents(ctx context.Context, limit int) (NetworkEvents, error)
```

`internal/access/manager` 只负责 HTTP 参数解析、权限校验和 JSON 输出，不做指标聚合。

## 4.3 数据来源

第一版数据来源：

- 节点健康与 membership：`ClusterReader.ListNodesStrict(ctx)`。
- Controller leader：`ClusterReader.ControllerLeaderID()`。
- Slot runtime observation freshness：`ClusterReader.ListObservedRuntimeViewsStrict(ctx)`。
- Cluster pools：`ClusterReader.TransportPoolStats()`；当前会合并 raft / rpc / controller pools。
- Channel data-plane pool：`internal/app` 当前持有 `dataPlanePool.Stats()`，需要通过 management dependency 注入或 app-level collector 暴露。
- Transport events：来自 `transport.ObserverHooks` 的本地内存窗口，其中 send / receive 第一版只有 msgType + bytes，没有 peer identity。
- RPC service latency / result / inflight：来自 `transport.ObserverHooks.OnRPCClient` 和 `cluster.ObserverHooks.OnControllerCall` 的本地内存窗口；service 级结果只对 RPC client 生命周期可靠。

现有 Prometheus Registry 可以继续保留，不作为前端 JSON 的直接数据源。

## 4.4 app 级 network collector

在 `internal/app` 增加轻量 collector，和现有 metrics observer 并行：

- 订阅 `transport.ObserverHooks`：send、receive、dial、enqueue、RPC client lifecycle。
- 订阅 `cluster.ObserverHooks`：controller call、node status change、leader change、task result 中与网络相关的状态。
- 提供 bounded ring buffer 保存最近网络事件。
- 提供按 peer 聚合的 dial / enqueue / pool 滚动窗口计数，以及按 peer + service 聚合的 RPC client 结果。
- 提供当前 pool snapshot refresh：cluster pools + data-plane pool。
- 第一版不承诺 per-peer bytes；若需要 peer traffic，需要先扩展 `transport.ObserverHooks.OnSend/OnReceive` 事件携带 peer identity。

collector 不应替代 Prometheus，只为 manager JSON 页面提供实时摘要。

## 4.5 DTO 草案

```go
// NetworkSummary is the manager-facing local node network observation snapshot.
type NetworkSummary struct {
    GeneratedAt        time.Time
    Scope              NetworkScope
    Headline           NetworkHeadline
    Traffic            NetworkTraffic
    Peers              []NetworkPeer
    Services           []NetworkRPCService
    ChannelReplication NetworkChannelReplication
    Discovery          NetworkDiscovery
    Events             []NetworkEvent
    SourceStatus       NetworkSourceStatus
}
```

核心 JSON 形态：

```json
{
  "generated_at": "2026-04-29T12:00:00Z",
  "scope": {
    "view": "local_node",
    "local_node_id": 1,
    "controller_leader_id": 1
  },
  "source_status": {
    "local_collector": "ok",
    "controller_context": "ok",
    "runtime_views": "ok",
    "errors": {}
  },
  "headline": {
    "remote_peers": 2,
    "alive_nodes": 3,
    "suspect_nodes": 0,
    "dead_nodes": 0,
    "pool_active": 8,
    "pool_idle": 4,
    "rpc_inflight": 3,
    "dial_errors_1m": 0,
    "queue_full_1m": 0,
    "timeouts_1m": 1,
    "stale_observations": 0
  },
  "traffic": {
    "scope": "local_total_by_msg_type",
    "tx_bps": 8192,
    "rx_bps": 4096,
    "peer_breakdown_available": false
  },
  "peers": [
    {
      "node_id": 2,
      "name": "node-2",
      "addr": "127.0.0.1:7002",
      "health": "alive",
      "last_heartbeat_at": "2026-04-29T11:59:58Z",
      "pools": {
        "cluster": { "active": 4, "idle": 8 },
        "data_plane": { "active": 1, "idle": 3 }
      },
      "rpc": {
        "inflight": 2,
        "p95_ms": 12,
        "success_rate": 0.998
      },
      "errors": {
        "dial_error_1m": 0,
        "queue_full_1m": 0,
        "timeout_1m": 1,
        "remote_error_1m": 0
      }
    }
  ]
}
```

字段说明：

- `scope.view` 第一版固定为 `local_node`。
- `pool.cluster` 第一版可以是 raft/rpc/controller 合并值；如果能拆出来源，再扩展为 `raft`、`rpc`、`controller`。
- `traffic.scope` 第一版为 `local_total_by_msg_type`，放在 summary 顶层；不能展示为真实 per-peer traffic，除非 transport send / receive hook 增加 peer identity。
- `source_status` 用于区分 local collector 可读、controller context 不可用、runtime views 不可用，避免把 unavailable 误显示为 0。
- `errors.dial_error_1m` 是 peer 级 transport 错误；`errors.queue_full_1m` 可以来自 peer 级 enqueue 或 peer+service 的 RPC client result，UI 需要分开标注来源。
- `success_rate` 缺少样本时为空或 `unknown`，不要显示 100%。
- `events` 必须限制数量，默认 50 条以内。

## 4.6 RPC service 映射

页面使用稳定 service name，而不是裸 id：

| Service | ID | 分组 |
|---|---:|---|
| `forward` | 1 | cluster |
| `presence` | 5 | usecase |
| `delivery_submit` | 6 | usecase |
| `delivery_push` | 7 | usecase |
| `delivery_ack` | 8 | usecase |
| `delivery_offline` | 9 | usecase |
| `conversation_facts` | 13 | usecase |
| `controller` | 14 | controller |
| `managed_slot` | 20 | slot |
| `channel_fetch` | 30 | channel_data_plane |
| `channel_append` | 33 | usecase |
| `channel_reconcile_probe` | 34 | channel_data_plane |
| `channel_long_poll_fetch` | 35 | channel_data_plane |
| `channel_messages` | 36 | usecase |
| `channel_leader_repair` | 37 | usecase |
| `channel_leader_evaluate` | 38 | usecase |
| `runtime_summary` | 39 | usecase |

当前 `internal/app/observability.go` 的 `transportRPCServiceName` 缺少部分 channel/data-plane service name，应在实现时补齐，避免页面显示 `service_30`、`service_34`、`service_35`、`service_36` 等。

## 5. 前端页面设计

## 5.1 页面结构

`NetworkPage` 建议结构：

```text
NetworkPage
├─ PageHeader
│  ├─ title / description
│  ├─ refresh / auto refresh
│  └─ badges: scope, generated_at, local_node, controller_leader
├─ SummaryCards
│  ├─ remote peers
│  ├─ node health
│  ├─ pool connections
│  ├─ RPC inflight
│  ├─ network errors
│  └─ observation freshness
├─ MainGrid
│  ├─ LocalNodePeerMap / OutboundPeers
│  └─ NetworkEvents
└─ Tabs
   ├─ Peers
   ├─ RPC Services
   ├─ Channel Replication
   └─ Discovery & Config
```

第一版如果没有 tabs 组件，可用多个 `SectionCard` 纵向排列，后续再抽象 tabs。

## 5.2 Header

Header 展示：

- `Scope: local-node view`
- `Local Node: <id>`
- `Controller Leader: <id>`
- `Generated: <time>`
- 单节点集群时展示 `Single-node cluster` badge

刷新策略：

- 提供手动 refresh。
- 可选 5s auto-refresh toggle；默认关闭，避免 manager API 负载不可控。

## 5.3 Summary cards

卡片：

1. `Remote Peers`：远端节点数。
2. `Node Health`：alive / suspect / dead / draining。
3. `Pool Connections`：active / idle。
4. `RPC Inflight`：当前 inflight 总数。
5. `Network Errors`：1m 内 dial_error、queue_full、timeout。
6. `Observation Freshness`：stale runtime views 数量。

状态色规则：

- dead > 0：red。
- suspect > 0：yellow。
- queue_full / dial_error > 0：yellow 或 red，取决于连续窗口。
- 单节点集群 remote peers = 0：neutral。

## 5.4 LocalNodePeerMap / Outbound Peers

第一版展示本节点行：

```text
local node -> peer node
```

每个 peer cell 内容：

- health badge。
- pool active/idle。
- RPC inflight。
- 最近错误摘要。

未采集的方向显示 `not collected`，不展示为失败。第一版避免使用“完整 matrix”文案，`matrix` 只留给 Phase 4 的全局聚合视图。

点击 cell 打开 detail sheet：

- peer identity：node id/name/address/role/join state。
- heartbeat age。
- cluster pool / data-plane pool。
- service breakdown。
- recent events。
- affected slots count（如果后端提供）。

## 5.5 RPC Services

表格列：

| 列 | 内容 |
|---|---|
| Service | 稳定 service name |
| Group | controller / slot / channel data-plane / usecase |
| Target | peer node |
| Inflight | 当前值 |
| Rate | 近窗口调用量 |
| Latency | p50 / p95 / p99 |
| Errors | RPC timeout / RPC queue_full / remote_error；dial_error 单独作为 peer-level transport error 展示 |
| Last Seen | 最近一次调用时间 |

低样本时 latency 显示 `insufficient samples`。

## 5.6 Channel Replication

第一版只展示当前已经能可靠获得的 Channel data-plane 信息：

- data-plane pool active / idle。
- data-plane RPC services：`channel_fetch`、`channel_reconcile_probe`、`channel_long_poll_fetch` 的 inflight、latency、result。
- long-poll 配置摘要：lane count、max wait、max bytes、max channels。

以下运行时 lane 明细作为 Phase 3 扩展，不在 Phase 1 DTO 中承诺：

- per peer active lanes。
- reset count 和 reset reason。
- stale meta / not leader 次数。
- reconcile probe 次数和失败数。
- backpressure hard 次数。
- queued replication count。

实现这些字段前，必须先在 `pkg/channel/runtime` 或 `pkg/channel/transport` 增加明确的 runtime snapshot；页面不得从现有 pool/RPC 指标推断 lane 状态。

## 5.7 Discovery & Config

展示：

- local cluster listen addr。
- advertise addr。
- seeds。
- effective discovery nodes。
- pool size / data-plane pool size。
- dial timeout。
- Controller observation intervals。
- data-plane RPC timeout。
- long-poll parameters。

校验提示：

- `0.0.0.0` / `[::]` 只能作为 listen bind，不应作为 peer advertise address。
- static `WK_CLUSTER_NODES` 地址必须唯一。
- dynamic discovery address change 会关闭对应 peer 的旧 pools。

## 5.8 空态与错误态

单节点集群空态：

> 当前是单节点集群，没有远端节点间 transport 链路。Gateway 客户端连接请查看“连接”页面。

权限不足：

- 若缺少 `cluster.network:r`，显示 forbidden 状态。
- 如果沿用组合权限，提示需要的具体权限。

Controller 不可用：

- 节点本地 network collector 仍可返回本地 transport 数据。
- 节点健康、Slot observation freshness 标记为 unavailable。
- 页面区分“本地 transport 可读”和“controller-backed cluster context 不可读”。

## 6. 测试策略

## 6.1 后端测试

新增或扩展：

- `internal/usecase/management/network_test.go`
  - 单节点集群返回正常空态。
  - 多节点聚合 peers、health、pool stats。
  - controller 读失败时保留本地 collector 数据并标记 context unavailable。
  - rolling window 正确统计 dial_error、queue_full、timeout。
- `internal/access/manager/network_test.go`
  - 权限校验。
  - JSON 字段命名。
  - peer detail 参数校验。
- `internal/app/observability_test.go`
  - network collector 从 transport hooks 接收事件。
  - data-plane pool 与 cluster pool 都纳入 snapshot。
  - service id 映射包含 channel fetch / reconcile probe / long poll / messages / repair / evaluate / runtime summary。

## 6.2 前端测试

新增：

- `web/src/pages/network/page.test.tsx`
  - loading / forbidden / unavailable / empty single-node cluster。
  - summary cards 渲染。
  - outbound peers / LocalNodePeerMap 渲染 local-node scope。
  - RPC service 表格渲染 service name 而非裸 id。
  - refresh 调用 API。
- i18n 测试：英文、中文 message id 完整。

## 6.3 验证命令

常规验证：

```bash
go test ./internal/usecase/management ./internal/access/manager ./internal/app ./pkg/transport ./pkg/cluster
cd web && bun test --run
```

如果只改前端页面，可先跑：

```bash
cd web && bun test --run network
```

## 7. 分阶段落地

## 7.1 Phase 1：Network Summary API + 页面骨架

- 新增 network DTO 和 `/manager/network/summary`。
- 接入节点健康、controller leader、pool stats、基础 transport event counters。
- 重做 `web/src/pages/network/page.tsx`，实现 header、summary cards、peer list、单节点集群空态。

## 7.2 Phase 2：RPC service breakdown + events

- 补 service 聚合和 recent events ring buffer。
- 前端增加 RPC Services 和 Events section。
- 补齐 service id 到 name 的映射。

## 7.3 Phase 3：Channel replication drilldown

- 暴露 Channel data-plane long-poll / reconcile probe 状态。
- 前端增加 Channel Replication section。
- 区分正常 long-poll timeout 和异常 timeout。

## 7.4 Phase 4：Cluster aggregate view

- 每个节点上报或可远程读取本地 network snapshot。
- Controller leader 聚合全局 matrix，并在此阶段才启用完整 peer-to-peer matrix 文案。
- 页面 scope 从 `local_node` 扩展为 `cluster_aggregate`。

## 8. 开放问题

1. network collector 的窗口长度第一版使用 1m / 5m，还是只做 1m？建议先做 1m，页面文案明确。
2. `cluster.network:r` 是否需要立即加入默认 `wukongim.conf.example` 管理员权限？建议加入。
3. Channel runtime 是否需要直接暴露 lane dispatcher queue 长度？建议 Phase 3 再做，Phase 1 不阻塞。
4. manager API 是否允许 controller context 失败时返回 partial data？建议允许，但必须用 `source_status.controller_context` 字段明确标注。

## 9. 验收标准

- 网络页面不再显示“not exposed”占位，而是能展示真实 manager network snapshot 或明确单节点集群空态。
- 页面不会把客户端 connections 当作节点间 network 展示。
- 页面明确标注第一版为 local-node view。
- 后端 DTO 不直接透传 Prometheus 文本或内部 transport struct。
- `dial_error` 能定位到 peer；RPC timeout / RPC queue_full / remote_error 能定位到 peer 和 service；transport-level enqueue queue_full 至少能定位到 peer 和 queue kind。
- 单节点集群下无远端 peer 被视为正常状态。
