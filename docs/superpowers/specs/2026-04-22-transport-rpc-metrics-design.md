# Transport RPC Metrics Design

## 概述

本设计面向节点间 transport / RPC 排障场景，目标是在不引入高基数业务标签、不改变现有传输语义的前提下，为 WuKongIM 增加能够定位节点间拥塞、RPC 超时、连接建立失败与队列背压的 Prometheus 指标。

当前已有指标覆盖 `transport sent/received bytes`、controller RPC 延迟和 pool active/idle 连接数，但对于如下问题仍缺少直接观测：

- 某个目标节点的 RPC 是否持续超时。
- 某个 service 是否显著变慢，例如 `channel_append`。
- transport 是卡在 dial、enqueue、还是等待 response。
- `nodetransport: queue full` 是 raft 还是 rpc 队列压力。

## 目标

- 为 transport client path 增加低基数指标，直接回答 `target_node + service` 维度上的超时、错误和耗时问题。
- 为 transport enqueue / dial path 增加拥塞指标，定位 `queue full`、dial 失败、stopped 等基础设施异常。
- 复用现有 `pkg/metrics` 和 `internal/app/observability.go` 机制，不引入新的 metrics 注册入口。
- 保持现有 transport API 和运行语义兼容，已有 bytes 指标继续保留。

## 非目标

- 不引入 `channel_id`、`uid`、`slot_id` 等高基数字段。
- 不增加 server handler 端 per-service latency 指标。
- 不修改 raft / rpc 的发送、重试、排队或超时语义。
- 不把业务层 message/channel 语义下沉到 transport 包中。

## 方案

### 方案 A：只在业务层补 metrics

在 `internal/access/node`、`internal/app/channelcluster.go`、`internal/usecase/message` 等调用边界补 counters / histograms。

优点：

- 改动面小。
- 可以快速覆盖已知超时路径。

缺点：

- 看不到 `queue full`、dial 失败、发送前背压等 transport 级问题。
- 无法统一复用到 cluster controller、managed slot、forward、node RPC 等其他 service。

### 方案 B：在 transport 层增加通用观测事件，并接到现有 metrics（推荐）

在 `pkg/transport` 的 `Pool`、`Client` 中增加 observer hook：

- dial 事件：结果 + duration + target node
- enqueue 事件：kind + result + target node
- RPC client 事件：service + result + duration + inflight + target node

再由 `internal/app/observability.go` 将 `serviceID` 映射为稳定 service name，并写入 `pkg/metrics/transport.go`。

优点：

- 能统一覆盖 cluster transport 和 node direct RPC。
- 能同时解释 `queue full` 与 `channel_append` 20s timeout 这两类问题。
- 标签维度统一，便于 dashboard / alert 聚合。

缺点：

- 需要扩展 transport observer 接口，并更新 wiring 与测试。

### 方案 C：只在 cluster transport 层补 metrics

只围绕 `pkg/cluster` 的 raft / controller / managed slot / forward 补充指标。

优点：

- 聚焦 cluster，改动局部。

缺点：

- 无法覆盖 `internal/access/node` 走共享 transport 的 direct RPC，例如 `channel_append`。
- 仍然无法统一回答 target node / service 级问题。

## 推荐方案

采用方案 B。

## 设计细节

### 1. 新增 transport observer 事件

在 `pkg/transport/types.go` 的 `ObserverHooks` 中保留现有：

- `OnSend(msgType, bytes)`
- `OnReceive(msgType, bytes)`

并新增事件化 hook：

- `OnDial(event)`
- `OnEnqueue(event)`
- `OnRPCClient(event)`

事件字段只保留低基数观测维度：

- `TargetNode uint64`
- `ServiceID uint8`（仅 RPC）
- `Kind string`（`raft|rpc|bulk`）
- `Result string`
- `Duration time.Duration`
- `Bytes int`（如需要）

这些事件不携带业务维度，因此 transport 包仍然保持基础设施职责。

### 2. Dial 指标

在 `pkg/transport/pool.go` 的 `acquire()` 中增加 dial 成功/失败观测：

- dial 前记录开始时间。
- dial 成功后上报 `target_node + result=ok + duration`。
- dial 失败时上报 `target_node + result=dial_error + duration`。

对应 metrics：

- `wukongim_transport_dial_total{target_node,result}`
- `wukongim_transport_dial_duration_seconds{target_node}`

### 3. Enqueue / 背压指标

在 `pkg/transport/pool.go` 的 `Send()` / `RPC()` 路径中，根据 enqueue 结果上报：

- `ok`
- `queue_full`
- `stopped`
- 其他错误归类为 `other`

标签只包含：

- `target_node`
- `kind=raft|rpc|bulk`
- `result`

对应 metrics：

- `wukongim_transport_enqueue_total{target_node,kind,result}`

该指标用于直接解释当前日志中的 `cluster.transport.raft_send.skipped`。

### 4. RPC client 指标

在 `pkg/transport/client.go` 的 `RPCService()` 中统一记录 RPC 调用：

- 调用开始时增加 inflight gauge。
- 返回时根据 error / ctx 分类结果。
- 记录 target node、service、duration。

结果分类控制为低枚举值：

- `ok`
- `timeout`
- `canceled`
- `remote_error`
- `dial_error`
- `queue_full`
- `stopped`
- `other`

对应 metrics：

- `wukongim_transport_rpc_client_total{target_node,service,result}`
- `wukongim_transport_rpc_client_duration_seconds{target_node,service}`
- `wukongim_transport_rpc_inflight{target_node,service}`

### 5. Service name 映射

在 `internal/app/observability.go` 中增加 `serviceID -> service name` 映射，覆盖现有 transport 共享 service：

- cluster:
  - `1 -> forward`
  - `14 -> controller`
  - `20 -> managed_slot`
- node:
  - `5 -> presence`
  - `6 -> delivery_submit`
  - `7 -> delivery_push`
  - `8 -> delivery_ack`
  - `9 -> delivery_offline`
  - `13 -> conversation_facts`
  - `33 -> channel_append`

未知 service 回退为 `service_<id>`，保证未知路径仍然可观测。

### 6. 指标标签约束

为避免高基数，本设计固定使用以下维度：

- RPC：`target_node + service (+ result)`
- enqueue：`target_node + kind + result`
- dial：`target_node + result`

明确不使用：

- `channel_id`
- `uid`
- `slot_id`
- `client_msg_no`
- `session_id`

### 7. 与现有指标关系

保留现有指标：

- `wukongim_transport_sent_bytes_total`
- `wukongim_transport_received_bytes_total`
- `wukongim_transport_connections_pool_active`
- `wukongim_transport_connections_pool_idle`
- controller call latency -> `ObserveRPC(...)`

新增指标只补足 transport client path 可见性，不替换现有 bytes / pool 指标。

## 测试设计

### 1. metrics registry 测试

在 `pkg/metrics/registry_test.go` 中增加 gather 断言，验证：

- 新 family 已注册。
- label 包含 `target_node`、`service`、`kind`、`result` 等预期维度。
- counter / gauge / histogram 值符合观测输入。

### 2. transport pool 测试

在 `pkg/transport/pool_test.go` 中覆盖：

- dial success / failure
- enqueue success / queue full / stopped

验证 observer 事件分类正确。

### 3. transport client 测试

在 `pkg/transport/client_test.go` 中覆盖：

- `RPCService()` success
- `context deadline exceeded`
- `context canceled`
- remote error

验证 RPC client metrics 的 `service/target/result/duration/inflight`。

### 4. app observability 测试

在 `internal/app/observability_test.go` 中验证：

- service id 能映射到稳定字符串。
- 未知 service id 映射到 `service_<id>`。
- 新 observer hook 能正确写入 metrics registry。

## 运维收益

新增指标上线后，可以直接用如下查询定位问题：

- 某个目标节点的 `channel_append` 是否大量 timeout：
  - `wukongim_transport_rpc_client_total{target_node="3",service="channel_append",result="timeout"}`
- 某个目标节点的 RPC 是否整体变慢：
  - `rate(wukongim_transport_rpc_client_duration_seconds_sum{target_node="3"}[5m]) / rate(wukongim_transport_rpc_client_duration_seconds_count{target_node="3"}[5m])`
- 某个目标节点的 raft 队列是否经常满：
  - `increase(wukongim_transport_enqueue_total{target_node="1",kind="raft",result="queue_full"}[5m])`
- 某个目标节点是否经常 dial 失败：
  - `increase(wukongim_transport_dial_total{target_node="3",result!="ok"}[5m])`
