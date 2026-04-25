# WuKongIM 分布式追踪方案设计

> 状态：Design Proposal / 待评审
> 范围：全系统（Gateway、Channel 层、Slot 层、Controller 层、Transport 层）
> 前置依赖：[observability-design.md](./observability-design.md)（指标体系需先落地）
> 目标：基于 OpenTelemetry 实现跨节点消息全链路追踪，支持从 TraceID 定位一条消息的完整生命周期。

## 1. 背景

### 1.1 为什么需要分布式追踪

指标体系（Prometheus）能回答"系统整体怎么样"，但无法回答"这条消息为什么慢"。

| 场景 | 指标能做到 | 指标做不到 |
|------|-----------|-----------|
| 消息 P99 延迟飙升 | 知道延迟升高了 | 不知道卡在哪个节点、哪个阶段 |
| 跨节点投递失败 | 知道错误率上升 | 不知道请求经过了哪些节点、在哪一跳失败 |
| ISR 复制异常慢 | 知道复制延迟高 | 不知道是网络、磁盘还是 Follower 自身问题 |

分布式追踪通过 TraceID 将一条消息在多个节点上的处理过程串联成完整的调用链，精确定位瓶颈。

### 1.2 为什么选 OpenTelemetry

| 选项 | 优劣 |
|------|------|
| 纯 Prometheus | 无法做请求级追踪和跨节点关联 |
| 纯 OpenTracing（Jaeger SDK） | 已被 OpenTelemetry 取代，不推荐新项目使用 |
| **OpenTelemetry** | 统一 Traces + Metrics + Logs 标准，生态完善，支持 Jaeger/Tempo/Zipkin 后端 |

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        WuKongIM 节点                                     │
│                                                                         │
│  ┌─────────┐  ┌──────────┐  ┌─────────┐  ┌───────────┐  ┌───────────┐ │
│  │ Gateway  │  │ Channel  │  │  Slot   │  │Controller │  │ Transport │ │
│  │  Span    │  │  Span    │  │  Span   │  │   Span    │  │   Span    │ │
│  └────┬─────┘  └────┬─────┘  └────┬────┘  └─────┬─────┘  └─────┬─────┘ │
│       │             │             │              │              │       │
│       └──────┬──────┴──────┬──────┴──────┬───────┴──────────────┘       │
│              │             │             │                               │
│         ┌────▼─────────────▼─────────────▼────┐                         │
│         │     OpenTelemetry SDK (Traces)       │                         │
│         └────────────────┬────────────────────┘                         │
│                          │                                               │
│                     ┌────▼────┐                                          │
│                     │  OTLP   │                                          │
│                     │Exporter │                                          │
│                     └────┬────┘                                          │
└──────────────────────────┼──────────────────────────────────────────────┘
                           │
              ┌────────────▼────────────┐
              │ OpenTelemetry Collector  │ （可选，聚合/过滤/转发）
              └────────────┬────────────┘
                           │
                 ┌─────────▼─────────┐
                 │  Jaeger / Tempo   │
                 │   (追踪存储)       │
                 └─────────┬─────────┘
                           │
                 ┌─────────▼─────────┐
                 │     Grafana       │
                 │   (追踪查询)       │
                 └───────────────────┘
```

## 3. TraceID 生成与传播

### 3.1 Trace 生命周期

```
客户端发送消息
    │
    ▼ Gateway 生成 TraceID（或使用客户端携带的 TraceID）
    │
    ├─── Span: gateway.handle_send
    │       │
    │       ▼ 通过 RPC 元数据传播 TraceID + SpanID
    │
    ├─── Span: channel.append
    │       │
    │       ├── Span: channel.replicate（→ Follower 节点）
    │       │
    │       └── Span: storage.write
    │
    ├─── Span: delivery.dispatch
    │       │
    │       └── Span: gateway.push_recv
    │
    └── 完成
```

### 3.2 一条消息的完整 Trace 示例

```
TraceID: abc123def456

gateway.handle_send      [node-1]  ────────────────────────────── 12ms
  ├─ message.send         [node-1]  ──────────────────────────── 10ms
  │   ├─ slot.lookup       [node-1]  ──── 0.5ms
  │   ├─ channel.append    [node-2]  ────────────────────────── 8ms
  │   │   ├─ storage.write  [node-2]  ──── 1ms
  │   │   └─ channel.replicate [node-3]  ─────────────── 5ms
  │   │       └─ storage.write [node-3]  ──── 0.8ms
  │   └─ (HW confirmed)
  └─ delivery.dispatch    [node-2]  ─────── 2ms
      └─ gateway.push_recv [node-1]  ──── 0.3ms
```

## 4. 跨节点上下文传播

### 4.1 传播机制

跨节点追踪需要在现有 Transport 层的 RPC 中注入追踪上下文。

**方案：在 RPC Payload 头部追加 TraceContext**

```go
// pkg/trace/propagation.go

// TraceContext 追踪上下文，附加在 RPC payload 头部
type TraceContext struct {
    TraceID  [16]byte  // W3C Trace ID
    SpanID   [8]byte   // 父 Span ID
    Flags    byte      // 采样标记
}

const TraceContextSize = 25 // 16 + 8 + 1

// EncodeTraceContext 编码追踪上下文到 payload 头部
func EncodeTraceContext(ctx context.Context, buf []byte) int {
    sc := trace.SpanContextFromContext(ctx)
    if !sc.IsValid() {
        buf[0] = 0 // 无追踪上下文标记
        return 1
    }
    buf[0] = 1 // 有追踪上下文标记
    copy(buf[1:17], sc.TraceID[:])
    copy(buf[17:25], sc.SpanID[:])
    buf[25] = byte(sc.TraceFlags())
    return 26
}

// DecodeTraceContext 从 payload 头部解码追踪上下文
func DecodeTraceContext(buf []byte) (context.Context, int) {
    if buf[0] == 0 {
        return context.Background(), 1
    }
    var tid trace.TraceID
    var sid trace.SpanID
    copy(tid[:], buf[1:17])
    copy(sid[:], buf[17:25])
    flags := trace.TraceFlags(buf[25])
    sc := trace.NewSpanContext(trace.SpanContextConfig{
        TraceID:    tid,
        SpanID:     sid,
        TraceFlags: flags,
        Remote:     true,
    })
    return trace.ContextWithRemoteSpanContext(context.Background(), sc), 26
}
```

### 4.2 对 RPC 帧格式的改动

当前帧格式：`[serviceID:1][payload]`

新帧格式：`[serviceID:1][traceFlag:1][traceContext:0|25][payload]`

- `traceFlag=0` 时无 traceContext，兼容旧版本。
- 额外开销：未采样请求 +1 字节，采样请求 +26 字节。

### 4.3 兼容性

- 追踪关闭时（`trace.enable=false`），所有 RPC 帧 `traceFlag=0`，开销仅 +1 字节。
- 滚动升级时新旧节点混合运行，旧节点收到带 TraceContext 的帧会忽略未知前缀——需要在 Transport 层做版本协商或在 traceFlag 位上做前向兼容处理。

## 5. 关键追踪 Span

| Span 名称 | 位置 | 触发条件 | 关键属性 |
|-----------|------|----------|----------|
| `gateway.handle_frame` | Gateway | 每个客户端帧 | `frame_type`, `uid`, `protocol` |
| `gateway.auth` | Gateway | CONNECT 帧 | `uid`, `result` |
| `message.send` | UseCase | 消息发送 | `channel_id`, `client_msg_no`, `from_uid` |
| `channel.append` | Channel Handler | 消息追加 | `channel_key`, `message_seq`, `isr_size` |
| `channel.replicate` | Channel Replica | ISR 复制 | `channel_key`, `target_node`, `lag` |
| `channel.fetch` | Channel Handler | 消息拉取 | `channel_key`, `start_seq`, `count` |
| `slot.propose` | Slot MultiRaft | Raft 提案 | `slot_id`, `cmd_type` |
| `slot.apply` | Slot FSM | 命令应用 | `slot_id`, `batch_size` |
| `controller.decide` | Controller Planner | 调度决策 | `decision_type`, `affected_slots` |
| `transport.rpc` | Transport | 跨节点 RPC | `service`, `peer_node`, `payload_bytes` |
| `delivery.dispatch` | Delivery | 消息投递 | `channel_key`, `subscriber_count` |
| `delivery.push` | Delivery | 推送到客户端 | `uid`, `protocol`, `result` |

## 6. 采样策略

生产环境不能对每条消息都做追踪，需要合理的采样：

### 6.1 采样模式

| 策略 | 配置 | 适用场景 |
|------|------|----------|
| **概率采样** | 默认 1%（可配置） | 常规生产环境 |
| **尾部采样** | 延迟 > P99 时采样 100% | 捕获异常慢请求 |
| **错误采样** | 发生错误时采样 100% | 错误诊断 |
| **调试采样** | 通过 Header 强制采样 | 开发调试 |

### 6.2 采样器实现

```go
// pkg/trace/sampler.go

// CompositeSampler 组合采样器：概率 + 错误强制 + 慢请求强制
type CompositeSampler struct {
    baseSampler  sdktrace.Sampler  // 概率采样
    slowThreshold time.Duration    // 慢请求阈值
}

func NewCompositeSampler(rate float64, slowThreshold time.Duration) *CompositeSampler {
    return &CompositeSampler{
        baseSampler:   sdktrace.TraceIDRatioBased(rate),
        slowThreshold: slowThreshold,
    }
}

func (s *CompositeSampler) ShouldSample(params sdktrace.SamplingParameters) sdktrace.SamplingResult {
    // 1. 检查是否有强制采样标记（调试模式）
    if hasForceFlag(params) {
        return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
    }
    // 2. 走概率采样（头部采样）
    return s.baseSampler.ShouldSample(params)
}

// 尾部采样需要在 Span 结束时判断：
// - 如果 Span 有错误状态 → 标记为采样
// - 如果 Span 延迟 > slowThreshold → 标记为采样
// 尾部采样通常在 OTel Collector 中实现，不在 SDK 中
```

### 6.3 尾部采样（OTel Collector 侧）

```yaml
# otel-collector-config.yaml
processors:
  tail_sampling:
    decision_wait: 10s
    policies:
      - name: error-policy
        type: status_code
        status_code:
          status_codes: [ERROR]
      - name: latency-policy
        type: latency
        latency:
          threshold_ms: 100
      - name: probabilistic-policy
        type: probabilistic
        probabilistic:
          sampling_percentage: 1
```

## 7. TraceID 与日志关联

将 TraceID 注入 Zap Logger，实现日志与追踪的关联：

```go
// pkg/trace/logger.go

func WithTraceLogger(ctx context.Context, logger *zap.Logger) *zap.Logger {
    sc := trace.SpanContextFromContext(ctx)
    if sc.IsValid() {
        return logger.With(
            zap.String("traceID", sc.TraceID().String()),
            zap.String("spanID", sc.SpanID().String()),
        )
    }
    return logger
}
```

使用方式——在请求入口处创建带 TraceID 的 Logger，后续日志自动携带：

```go
func (g *Gateway) handleSend(ctx context.Context, session *Session, pkt *SendPacket) {
    ctx, span := tracer.Start(ctx, "gateway.handle_send")
    defer span.End()

    log := trace.WithTraceLogger(ctx, g.Log)
    log.Info("send.received", zap.String("channelID", pkt.ChannelID))
    // ...
}
```

在 Grafana 中可以从指标面板钻取到追踪视图，再从追踪关联到对应日志行。

## 8. 代码组织

### 8.1 新增包结构

```
pkg/trace/
    ├── provider.go        [新增] OpenTelemetry TracerProvider 初始化与关闭
    ├── propagation.go     [新增] 跨节点追踪上下文编解码（TraceContext）
    ├── sampler.go         [新增] 组合采样器（概率 + 错误 + 慢请求）
    └── logger.go          [新增] WithTraceLogger 日志关联工具
```

### 8.2 对现有模块的改动

| 文件 | 改动 | 说明 |
|------|------|------|
| `internal/app/app.go` | 改动 | 初始化 Trace Provider |
| `internal/gateway/gateway.go` | 改动 | 创建 gateway.* Span |
| `internal/usecase/message/send.go` | 改动 | 创建 message.send Span |
| `pkg/channel/handler/append.go` | 改动 | 创建 channel.append Span |
| `pkg/channel/replica/replica.go` | 改动 | 创建 channel.replicate Span |
| `pkg/transport/transport.go` | 改动 | RPC 帧追加 TraceContext 编解码 |
| `internal/runtime/delivery/dispatcher.go` | 改动 | 创建 delivery.* Span |
| `cmd/wukongim/config.go` | 改动 | 新增 Trace 配置项 |

## 9. 配置

### 9.1 新增配置项

```yaml
# wukongim.yaml

trace:
  enable: false                   # 是否启用分布式追踪
  endpoint: "localhost:4317"      # OTLP gRPC 端点
  sample_rate: 0.01               # 概率采样率（0.0~1.0，默认 1%）
  slow_threshold: "100ms"         # 慢请求阈值（超过则在 Collector 侧 100% 采样）
```

### 9.2 对应环境变量

```
WK_TRACE_ENABLE=false
WK_TRACE_ENDPOINT=localhost:4317
WK_TRACE_SAMPLE_RATE=0.01
WK_TRACE_SLOW_THRESHOLD=100ms
```

### 9.3 TracerProvider 初始化

```go
// pkg/trace/provider.go

func NewProvider(cfg TraceConfig, serviceName string, nodeID string) (*sdktrace.TracerProvider, error) {
    if !cfg.Enable {
        return nil, nil // 返回 nil，各模块检查后跳过 Span 创建
    }

    exporter, err := otlptracegrpc.New(context.Background(),
        otlptracegrpc.WithEndpoint(cfg.Endpoint),
        otlptracegrpc.WithInsecure(),
    )
    if err != nil {
        return nil, err
    }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithSampler(NewCompositeSampler(cfg.SampleRate, cfg.SlowThreshold)),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceNameKey.String(serviceName),
            attribute.String("node.id", nodeID),
        )),
    )

    otel.SetTracerProvider(tp)
    return tp, nil
}

func Shutdown(tp *sdktrace.TracerProvider) {
    if tp != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        tp.Shutdown(ctx)
    }
}
```

## 10. 部署架构

### 10.1 最小部署（开发/测试）

```
┌────────────────────────────────┐
│ WuKongIM (单节点)               │
│ OTLP → localhost:4317          │
└──────────────┬─────────────────┘
               │
     ┌─────────▼──────────┐
     │  Jaeger All-in-One │
     │  :16686 (UI)        │
     └────────────────────┘
```

一个 Docker 容器即可运行 Jaeger All-in-One。

### 10.2 生产部署

```
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│ WuKongIM node-1  │  │ WuKongIM node-2  │  │ WuKongIM node-3  │
│ OTLP → :4317     │  │ OTLP → :4317     │  │ OTLP → :4317     │
└────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
         │                     │                      │
         └──────────┬──────────┴──────────────────────┘
                    │
        ┌───────────▼───────────┐
        │ OpenTelemetry Collector│  （尾部采样、过滤、转发）
        └───────────┬───────────┘
                    │
          ┌─────────▼─────────┐
          │  Grafana Tempo /   │
          │  Jaeger            │
          └─────────┬─────────┘
                    │
          ┌─────────▼─────────┐
          │     Grafana       │
          └───────────────────┘
```

建议使用 OTel Collector 做尾部采样，避免在 WuKongIM 节点上做复杂的采样决策。

## 11. 新增依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| `go.opentelemetry.io/otel` | v1.x | OTel API |
| `go.opentelemetry.io/otel/sdk` | v1.x | OTel SDK（TracerProvider、Sampler） |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` | v1.x | OTLP gRPC Exporter |

## 12. 性能影响评估

| 操作 | 额外开销 | 说明 |
|------|----------|------|
| Span 创建（采样时） | ~200ns | 仅被采样的请求有此开销（默认 1%） |
| Span 创建（未采样时） | ~20ns | 只做采样判断，不记录 |
| TraceContext 编解码 | ~10ns | 简单内存拷贝 |
| TraceContext 帧额外字节 | 1~26B / RPC | 相对于消息 payload 可忽略 |
| Span 批量导出（后台） | ~1ms / batch | BatchSpanProcessor 异步批量发送，不阻塞热路径 |

**结论**：

- 追踪关闭时：每个 RPC +1 字节，性能影响为零。
- 追踪开启、1% 采样率：热路径影响 < 50ns / op（99% 请求只做采样判断）。
- 追踪开启、100% 采样率（调试场景）：热路径影响 ~200ns / op，Span 导出在后台异步完成。

## 13. 实施步骤

1. **`pkg/trace/` 基础包**：实现 TracerProvider 初始化、CompositeSampler、TraceContext 编解码。
2. **Transport 层改动**：RPC 帧追加 TraceContext，兼容性处理。
3. **Gateway Span**：`gateway.handle_frame`、`gateway.auth`。
4. **消息链路 Span**：`message.send` → `channel.append` → `channel.replicate` → `storage.write`。
5. **投递链路 Span**：`delivery.dispatch` → `delivery.push`。
6. **日志关联**：`WithTraceLogger` 集成到请求处理路径。
7. **Docker Compose**：集成 Jaeger/Tempo + OTel Collector 一键启动。
8. **Grafana 追踪看板**：Trace 搜索 + Trace → Metrics 钻取。
