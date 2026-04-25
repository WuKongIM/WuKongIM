# WuKongIM 可观测性方案设计

> 状态：Design Proposal / 待评审
> 范围：全系统（Gateway、Channel 层、Slot 层、Controller 层、Transport 层、存储层）
> 目标：为 WuKongIM v3.1 建立指标 + 健康检查的可观测性体系，使系统从黑盒变为白盒。

## 1. 背景与问题

### 1.1 现状

1. **日志**：基于 Zap 的结构化日志已就位（`pkg/wklog/` + `internal/log/zap.go`），支持 JSON/console 格式、日志分级、日志轮转。最近已设计标准化日志规则（module → event → object fields → outcome）。
2. **指标**：`go.mod` 中已引入 `prometheus/client_golang v1.16.0`，但**未实际使用**——无 Counter、Histogram、Gauge，无 `/metrics` 端点。
3. **追踪**：无分布式追踪基础设施，无 Trace/Span 上下文传播，无跨节点请求关联。
4. **健康检查**：仅 `GET /healthz` 返回 200，无组件级健康状态、无就绪探针。

### 1.2 痛点

| 问题 | 影响 |
|------|------|
| 消息延迟飙升无法定位瓶颈 | 不知道是 Gateway 排队、ISR 复制、Pebble 写入还是网络抖动 |
| 集群故障修复过程不透明 | Controller 的 Repair/Rebalance 决策过程无法观测 |
| 热点频道无法发现 | 没有 per-channel 维度的吞吐/延迟指标 |
| 连接泄漏难以诊断 | 连接数变化趋势、连接生命周期无数据 |
| 容量规划缺乏数据支撑 | 无 QPS、字节吞吐量、存储增长等历史趋势 |
| 跨节点消息投递失败无法追踪 | 缺少 TraceID 将一条消息的完整生命周期串联 |

### 1.3 设计原则

1. **低侵入**：在现有模块接口上"插桩"，不改变核心数据流。
2. **低开销**：热路径上只做原子计数和直方图采样，避免分配和锁。
3. **渐进实施**：按优先级分阶段落地，每阶段可独立上线。
4. **标准生态**：Prometheus + Grafana，不造轮子。

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        WuKongIM 节点                                     │
│                                                                         │
│  ┌─────────┐  ┌──────────┐  ┌─────────┐  ┌───────────┐  ┌───────────┐ │
│  │ Gateway  │  │ Channel  │  │  Slot   │  │Controller │  │ Transport │ │
│  │  指标    │  │  指标    │  │  指标   │  │   指标    │  │   指标    │ │
│  └────┬─────┘  └────┬─────┘  └────┬────┘  └─────┬─────┘  └─────┬─────┘ │
│       │             │             │              │              │       │
│       └──────┬──────┴──────┬──────┴──────┬───────┴──────────────┘       │
│              │             │             │                               │
│         ┌────▼────┐        │        ┌────▼─────┐                        │
│         │Prometheus│        │        │ Zap     │                        │
│         │Registry │        │        │ Logger  │                        │
│         └────┬────┘        │        └────┬─────┘                        │
│              │             │             │                               │
│         ┌────▼────┐        │        ┌────▼─────┐                        │
│         │ :5001   │        │        │ app.log  │                        │
│         │/metrics │        │        │error.log │                        │
│         └─────────┘        │        └──────────┘                        │
└────────────────────────────┼────────────────────────────────────────────┘
                             │
                    ┌────────▼─────────┐
                    │    Prometheus     │
                    │    (指标存储)      │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │     Grafana      │
                    │    (统一看板)     │
                    └──────────────────┘
```

> 分布式追踪（OpenTelemetry）作为独立方案设计，详见 [distributed-tracing-design.md](./distributed-tracing-design.md)。

## 3. 指标体系（Prometheus）

### 3.1 指标命名规范

所有指标使用 `wukongim_` 前缀，遵循 Prometheus 命名约定：

```
wukongim_<subsystem>_<name>_<unit>

subsystem: gateway | channel | slot | controller | transport | storage
unit:      total | seconds | bytes | ratio
```

通用标签（每个指标都会携带）：

| 标签 | 含义 | 示例 |
|------|------|------|
| `node_id` | 节点 ID | `1` |
| `node_name` | 节点名称 | `node-1` |

### 3.2 Gateway 层指标

接入层是面向客户端的第一线，直接反映用户体验。

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_gateway_connections_active` | Gauge | `protocol`(tcp/ws) | 当前活跃连接数 |
| `wukongim_gateway_connections_total` | Counter | `protocol`, `event`(open/close) | 连接累计数 |
| `wukongim_gateway_auth_total` | Counter | `status`(ok/fail) | 认证请求次数 |
| `wukongim_gateway_auth_duration_seconds` | Histogram | — | 认证延迟 |
| `wukongim_gateway_messages_received_total` | Counter | `protocol` | 收到的客户端消息数 |
| `wukongim_gateway_messages_received_bytes` | Counter | `protocol` | 收到的字节数 |
| `wukongim_gateway_messages_delivered_total` | Counter | `protocol` | 推送给客户端的消息数 |
| `wukongim_gateway_messages_delivered_bytes` | Counter | `protocol` | 推送的字节数 |
| `wukongim_gateway_frame_handle_duration_seconds` | Histogram | `frame_type`(SEND/SUB/...) | 帧处理延迟 |

**直方图桶**：消息处理延迟使用 `{.0005, .001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5}` 秒。

### 3.3 Channel 消息层指标

消息层是吞吐和延迟的核心，需要最精细的观测。

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_channel_append_total` | Counter | `result`(ok/err) | 消息追加次数 |
| `wukongim_channel_append_duration_seconds` | Histogram | — | 单次 Append 延迟（从接收到 HW 确认） |
| `wukongim_channel_append_batch_size` | Histogram | — | Group Commit 的批次大小 |
| `wukongim_channel_fetch_total` | Counter | — | Fetch 请求次数 |
| `wukongim_channel_fetch_duration_seconds` | Histogram | — | Fetch 延迟 |
| `wukongim_channel_hw_lag` | Gauge | `channel_key` | Leader 的最新 offset 与 HW 的差值 |
| `wukongim_channel_isr_size` | Gauge | `channel_key` | 当前 ISR 副本数 |
| `wukongim_channel_epoch_changes_total` | Counter | — | Epoch 切换（Leader 变更）次数 |
| `wukongim_channel_replication_duration_seconds` | Histogram | — | 复制确认延迟（Leader → Follower ack） |
| `wukongim_channel_active_channels` | Gauge | — | 当前活跃的 Channel 运行时数量 |

> **注意**：`channel_key` 标签仅在 ISR 异常（`isr_size < MinISR`）时才附带，避免高基数标签导致 Prometheus 内存膨胀。常规聚合指标不含此标签。

### 3.4 Slot 元数据层指标

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_slot_proposals_total` | Counter | `slot_id` | Raft 提案次数 |
| `wukongim_slot_apply_duration_seconds` | Histogram | `slot_id` | 命令应用延迟 |
| `wukongim_slot_apply_batch_size` | Histogram | — | 批量应用的条数 |
| `wukongim_slot_leader_elections_total` | Counter | — | Leader 选举次数 |
| `wukongim_slot_snapshot_duration_seconds` | Histogram | — | 快照生成耗时 |
| `wukongim_slot_log_entries` | Gauge | `slot_id` | 未压缩的日志条目数 |
| `wukongim_slot_meta_cache_hits_total` | Counter | — | 元数据缓存命中 |
| `wukongim_slot_meta_cache_misses_total` | Counter | — | 元数据缓存未命中 |
| `wukongim_slot_meta_refresh_total` | Counter | `reason`(stale/not_leader) | 元数据刷新次数 |

### 3.5 Controller 控制层指标

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_controller_decisions_total` | Counter | `type`(repair/rebalance/migration) | 调度决策次数 |
| `wukongim_controller_decision_duration_seconds` | Histogram | — | 单次调度周期耗时 |
| `wukongim_controller_tasks_active` | Gauge | `type` | 正在执行的任务数 |
| `wukongim_controller_tasks_completed_total` | Counter | `type`, `result`(ok/fail/timeout) | 已完成任务数 |
| `wukongim_controller_nodes_alive` | Gauge | — | 存活节点数 |
| `wukongim_controller_nodes_suspect` | Gauge | — | 疑似故障节点数 |
| `wukongim_controller_nodes_dead` | Gauge | — | 已标记死亡节点数 |
| `wukongim_controller_hashslot_migrations_active` | Gauge | — | 正在进行的 hash slot 迁移数 |
| `wukongim_controller_hashslot_migrations_total` | Counter | `result`(ok/fail/abort) | 迁移次数 |

### 3.6 Transport 网络层指标

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_transport_rpc_duration_seconds` | Histogram | `service` | RPC 调用延迟 |
| `wukongim_transport_rpc_total` | Counter | `service`, `result`(ok/err) | RPC 调用次数 |
| `wukongim_transport_sent_bytes_total` | Counter | `msg_type` | 发送字节数 |
| `wukongim_transport_received_bytes_total` | Counter | `msg_type` | 接收字节数 |
| `wukongim_transport_connections_pool_active` | Gauge | `peer_node` | 连接池活跃连接数 |
| `wukongim_transport_connections_pool_idle` | Gauge | `peer_node` | 连接池空闲连接数 |
| `wukongim_transport_dial_duration_seconds` | Histogram | — | 拨号建连延迟 |
| `wukongim_transport_dial_errors_total` | Counter | — | 拨号失败次数 |

### 3.7 存储层指标

| 指标名 | 类型 | 标签 | 含义 |
|--------|------|------|------|
| `wukongim_storage_write_duration_seconds` | Histogram | `store`(channel/slot) | 写入延迟 |
| `wukongim_storage_read_duration_seconds` | Histogram | `store` | 读取延迟 |
| `wukongim_storage_write_bytes_total` | Counter | `store` | 写入字节数 |
| `wukongim_storage_compaction_total` | Counter | — | Pebble Compaction 次数 |
| `wukongim_storage_compaction_duration_seconds` | Histogram | — | Compaction 耗时 |
| `wukongim_storage_disk_usage_bytes` | Gauge | `store` | 磁盘占用 |
| `wukongim_storage_cache_hit_ratio` | Gauge | — | Pebble Block Cache 命中率 |

### 3.8 Go Runtime 指标

直接使用 `prometheus/client_golang` 的 `collectors.NewGoCollector()` 和 `collectors.NewProcessCollector()`，无需自定义：

- `go_goroutines`：goroutine 数量
- `go_memstats_*`：内存使用
- `go_gc_*`：GC 指标
- `process_cpu_seconds_total`：CPU 使用
- `process_open_fds`：打开文件描述符数

### 3.9 指标暴露

在现有 API Server（Gin）上注册 `/metrics` 端点：

```go
// internal/access/api/server.go

import (
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) setupRoutes() {
    // ...existing routes...
    s.router.GET("/metrics", gin.WrapH(promhttp.Handler()))
}
```

## 4. 健康检查与诊断

### 4.1 健康检查端点

将现有 `/healthz` 扩展为组件级健康检查：

```
GET /healthz           → 200 OK / 503 Service Unavailable（简要）
GET /healthz/details   → 各组件详细状态（JSON）
GET /readyz            → 就绪探针（Kubernetes 用）
```

**详细健康检查响应**：

```json
{
  "status": "healthy",
  "node_id": 1,
  "node_name": "node-1",
  "uptime_seconds": 86400,
  "components": {
    "gateway": {
      "status": "healthy",
      "active_connections": 15230,
      "protocols": {
        "tcp": { "status": "healthy", "connections": 12000 },
        "ws":  { "status": "healthy", "connections": 3230 }
      }
    },
    "controller": {
      "status": "healthy",
      "is_leader": true,
      "alive_nodes": 3,
      "dead_nodes": 0,
      "active_tasks": 0
    },
    "slot_layer": {
      "status": "healthy",
      "slots": [
        { "id": 1, "role": "leader", "term": 42, "applied_index": 189320 },
        { "id": 2, "role": "follower", "term": 38, "applied_index": 156780 }
      ]
    },
    "channel_layer": {
      "status": "healthy",
      "active_channels": 8523,
      "under_replicated_channels": 0
    },
    "storage": {
      "status": "healthy",
      "disk_usage_bytes": 5368709120,
      "disk_free_bytes": 107374182400
    },
    "transport": {
      "status": "healthy",
      "peers": {
        "node-2": { "status": "connected", "latency_ms": 1.2 },
        "node-3": { "status": "connected", "latency_ms": 0.8 }
      }
    }
  }
}
```

### 4.2 就绪探针

`/readyz` 返回 200 的条件：
1. 至少一个 Gateway Listener 就绪。
2. 本节点持有的所有 Slot 已完成 Raft 选举。
3. Controller Raft 已选出 Leader（如果本节点是 Controller 副本）。
4. HashSlotTable 已从 Controller 同步。

### 4.3 运行时诊断端点

```
GET /debug/pprof/*       → Go pprof（已有 net/http/pprof，需注册到 Gin）
GET /debug/goroutines    → 当前 goroutine 堆栈摘要
GET /debug/config        → 当前运行配置（脱敏）
GET /debug/cluster       → 集群视图（节点列表、Slot 分布、Leader 分布）
GET /debug/channels      → 活跃 Channel 列表（分页，含 ISR 状态）
```

## 5. 告警规则

### 5.1 黄金信号（Golden Signals）

| 信号 | 指标 | 告警条件 | 严重度 |
|------|------|----------|--------|
| **延迟** | `wukongim_channel_append_duration_seconds` | P99 > 100ms 持续 5min | Warning |
| **延迟** | `wukongim_channel_append_duration_seconds` | P99 > 500ms 持续 2min | Critical |
| **流量** | `rate(wukongim_gateway_messages_received_total[5m])` | 突降 50% 持续 5min | Warning |
| **错误** | `rate(wukongim_channel_append_total{result="err"}[5m])` | 错误率 > 1% 持续 3min | Critical |
| **饱和度** | `go_goroutines` | > 10000 持续 5min | Warning |

### 5.2 集群告警

| 告警 | 条件 | 严重度 |
|------|------|--------|
| 节点失联 | `wukongim_controller_nodes_dead > 0` 持续 1min | Critical |
| ISR 不足 | `wukongim_channel_isr_size < MinISR` | Critical |
| 迁移停滞 | 迁移耗时 > 10min | Warning |
| 磁盘使用率 | `wukongim_storage_disk_usage_bytes / total > 0.85` | Warning |
| 连接数异常 | `wukongim_gateway_connections_active` 5min 内增长 > 200% | Warning |
| Raft 日志积压 | `wukongim_slot_log_entries > 100000` 持续 10min | Warning |

### 5.3 示例 Prometheus 告警规则

```yaml
groups:
  - name: wukongim_critical
    rules:
      - alert: MessageAppendLatencyHigh
        expr: histogram_quantile(0.99, rate(wukongim_channel_append_duration_seconds_bucket[5m])) > 0.5
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "消息追加 P99 延迟超过 500ms"
          description: "节点 {{ $labels.node_name }} 消息追加 P99 延迟为 {{ $value }}s"

      - alert: NodeDown
        expr: wukongim_controller_nodes_dead > 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "集群存在宕机节点"
          description: "当前 {{ $value }} 个节点标记为 Dead"

      - alert: ISRUnderReplicated
        expr: wukongim_channel_isr_size < 2
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "Channel ISR 副本不足"
          description: "频道 {{ $labels.channel_key }} ISR 副本数为 {{ $value }}"
```

## 6. Grafana 看板设计

### 6.1 看板分层

| 看板 | 目标用户 | 核心内容 |
|------|----------|----------|
| **总览** | 运维/SRE | 集群状态、消息 QPS、P99 延迟、错误率、节点健康 |
| **Gateway** | 运维/开发 | 连接数趋势、协议分布、认证成功率、帧处理延迟 |
| **消息通道** | 开发 | Append/Fetch 延迟、ISR 状态、HW 追赶、Epoch 变更 |
| **集群** | 运维 | Slot 分布、Leader 分布、Controller 决策、迁移进度 |
| **存储** | 运维 | Pebble 写放大、Compaction、磁盘增长、缓存命中率 |
| **网络** | 运维/SRE | RPC 延迟分布、连接池利用率、跨节点吞吐 |

### 6.2 总览看板布局

```
┌─────────────────────────────────────────────────────────────────┐
│ WuKongIM 集群总览                                                │
├─────────┬──────────┬──────────┬──────────┬──────────────────────┤
│ 节点数   │ 连接数    │ 消息 QPS  │ P99 延迟  │ 错误率              │
│ 3/3 ✓   │ 15.2K    │ 28.5K    │ 12ms     │ 0.01%              │
├─────────┴──────────┴──────────┴──────────┴──────────────────────┤
│                                                                  │
│  ┌─ 消息 QPS 趋势 ──────────────┐  ┌─ P99 延迟趋势 ──────────┐ │
│  │  ▁▂▃▄▅▆▇█▇▆▅▄▃▂▁▁▂▃▄▅▆▇█   │  │  ▁▁▁▁▂▁▁▁▁▃▁▁▁▁▁▁▁▁▂  │ │
│  │  --- 收到  --- 推送            │  │  --- Append  --- Fetch   │ │
│  └──────────────────────────────┘  └──────────────────────────┘ │
│                                                                  │
│  ┌─ 节点状态 ────────────┐  ┌─ Slot Leader 分布 ──────────────┐ │
│  │ node-1  ● Alive  L:3  │  │  node-1: ████████ 4             │ │
│  │ node-2  ● Alive  L:4  │  │  node-2: ██████████ 5           │ │
│  │ node-3  ● Alive  L:3  │  │  node-3: ████████ 4 (偏差 ≤1)  │ │
│  └────────────────────────┘  └──────────────────────────────────┘ │
│                                                                  │
│  ┌─ 连接数趋势 ─────────────────────────────────────────────────┐ │
│  │  TCP:  ▁▂▃▄▅▆▇▇▇▇▇▇▇▇▇▇▇▇▇▇  12K                        │ │
│  │  WS:   ▁▁▁▂▂▃▃▃▃▃▃▃▃▃▃▃▃▃▃▃  3.2K                        │ │
│  └──────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

## 7. 代码组织

### 7.1 新增包结构

```
pkg/
└── metrics/
    ├── registry.go        [新增] 全局 Prometheus Registry + 初始化
    ├── gateway.go         [新增] Gateway 层指标定义
    ├── channel.go         [新增] Channel 层指标定义
    ├── slot.go            [新增] Slot 层指标定义
    ├── controller.go      [新增] Controller 层指标定义
    ├── transport.go       [新增] Transport 层指标定义
    └── storage.go         [新增] Storage 层指标定义

internal/access/api/
    └── server.go          [改动] 注册 /metrics、/healthz/details、/readyz
```

### 7.2 对现有模块的改动

| 文件 | 改动 | 说明 |
|------|------|------|
| `internal/app/app.go` | 改动 | 初始化 Metrics Registry |
| `internal/gateway/gateway.go` | 改动 | 插入连接计数、消息计数、帧延迟直方图 |
| `internal/usecase/message/send.go` | 改动 | 插入 Append 延迟 |
| `pkg/channel/handler/append.go` | 改动 | 插入 Channel Append 指标 |
| `pkg/channel/replica/replica.go` | 改动 | 插入 ISR/HW/Epoch 指标 |
| `pkg/slot/multiraft/worker.go` | 改动 | 插入 Proposal/Apply 指标 |
| `pkg/controller/plane/planner.go` | 改动 | 插入决策/任务指标 |
| `pkg/transport/transport.go` | 改动 | 插入 RPC 延迟/字节计数 |

### 7.3 指标注册模式

采用依赖注入而非全局变量，便于测试和隔离：

```go
// pkg/metrics/registry.go

type Metrics struct {
    Registry *prometheus.Registry

    // Gateway
    GatewayConnectionsActive *prometheus.GaugeVec
    GatewayMessagesReceived  *prometheus.CounterVec
    // ...

    // Channel
    ChannelAppendDuration *prometheus.Histogram
    ChannelISRSize        *prometheus.GaugeVec
    // ...
}

func New(nodeID string, nodeName string) *Metrics {
    m := &Metrics{
        Registry: prometheus.NewRegistry(),
    }
    // 注册 Go Runtime 指标
    m.Registry.MustRegister(collectors.NewGoCollector())
    m.Registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

    constLabels := prometheus.Labels{
        "node_id":   nodeID,
        "node_name": nodeName,
    }

    m.GatewayConnectionsActive = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name:        "wukongim_gateway_connections_active",
            Help:        "Number of active gateway connections",
            ConstLabels: constLabels,
        },
        []string{"protocol"},
    )
    m.Registry.MustRegister(m.GatewayConnectionsActive)

    // ...其他指标注册...

    return m
}
```

## 8. 配置

### 8.1 新增配置项

```yaml
# wukongim.yaml

# 指标配置
metrics:
  enable: true                    # 是否启用 Prometheus 指标

# 健康检查配置
health:
  detail_enable: true             # 是否暴露详细健康状态
  debug_enable: false             # 是否暴露 pprof/debug 端点（生产环境建议关闭）
```

### 8.2 对应环境变量

```
WK_METRICS_ENABLE=true
WK_HEALTH_DETAIL_ENABLE=true
WK_HEALTH_DEBUG_ENABLE=false
```

## 9. 部署架构

### 9.1 最小部署（开发/测试）

```
┌────────────────────────────────┐
│ WuKongIM (单节点)               │
│ :5001/metrics ──────────────┐  │
│ :5001/healthz               │  │
└─────────────────────────────┼──┘
                              │
                    ┌─────────▼──────────┐
                    │ Prometheus (单机)   │
                    │ + Grafana           │
                    └────────────────────┘
```

只需 Prometheus + Grafana 即可获得完整指标观测。

### 9.2 生产部署

```
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│ WuKongIM node-1  │  │ WuKongIM node-2  │  │ WuKongIM node-3  │
│ :5001/metrics    │  │ :5002/metrics    │  │ :5003/metrics    │
└────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
         │                     │                      │
         └──────────┬──────────┴──────────────────────┘
                    │
           ┌────────▼─────────┐
           │    Prometheus     │
           └────────┬─────────┘
                    │
           ┌────────▼─────────┐
           │     Grafana      │
           └──────────────────┘
```

## 10. 实施计划

### Phase 1：指标基础 + /metrics 端点（优先级最高）

**目标**：暴露核心指标，接入 Prometheus + Grafana。

**范围**：
1. 实现 `pkg/metrics/` 包，定义所有指标。
2. 在 `internal/app/app.go` 初始化 Metrics。
3. 在 API Server 注册 `/metrics` 端点。
4. 在 Gateway 插入连接数和消息计数。
5. 在 Channel 层插入 Append 延迟和 ISR 指标。
6. Go Runtime 指标自动注册。
7. 提供 Prometheus scrape 配置和 Grafana 总览看板 JSON。

**验收标准**：`curl :5001/metrics` 返回 Prometheus 格式指标。

### Phase 2：全层指标覆盖 + 健康检查

**目标**：完成所有层指标插桩，完善健康检查。

**范围**：
1. Slot 层、Controller 层、Transport 层、Storage 层指标插桩。
2. 实现 `/healthz/details` 和 `/readyz`。
3. 注册 pprof 到 Gin（`/debug/pprof/*`）。
4. 告警规则文件和 Grafana 分层看板。

### Phase 3：看板 + 告警 + 运维文档

**目标**：完善运维工具链。

**范围**：
1. 完善所有 Grafana 看板。
2. 告警规则调优。
3. 运维 Runbook（常见问题排查手册）。
4. Docker Compose 增加 Prometheus + Grafana 一键启动。

> 分布式追踪的实施计划详见 [distributed-tracing-design.md](./distributed-tracing-design.md)。

## 11. 新增依赖

**无需新增依赖**，`prometheus/client_golang` 已在 go.mod 中。

## 12. 性能影响评估

| 操作 | 额外开销 | 说明 |
|------|----------|------|
| Counter.Inc() | ~5ns | 原子操作，无锁 |
| Histogram.Observe() | ~20ns | 原子 CAS + 桶定位 |
| Gauge.Set() | ~5ns | 原子写 |
| /metrics 采集 | ~2ms / 次 | 15s 采集间隔，可忽略 |

**结论**：对消息热路径的影响 < 50ns / op，不影响系统吞吐。
