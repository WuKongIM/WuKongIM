# internal/runtime/channelplane Flow

## 1. 职责定位

`internal/runtime/channelplane` owns durable send routing for channel-keyed append workloads. It shards work by channel across multiple reactors, serializes same-channel append effects, resolves authoritative route identity, and batches remote leader appends by peer lane. It returns synchronous append results to callers, but it does not own message auth, permission checks, plugin hooks, committed side effects, or node RPC transport encoding.

## 2. 依赖边界

### ✅ 允许依赖
- `pkg/channel`：append request/result、channel meta、route validation DTO
- `internal/runtime/channelmeta`：authoritative route refresh、lease renewal、repair inputs
- `pkg/metrics`：低基数计数/延迟观测
- `pkg/observability/sendtrace`：durable append / peer append 链路事件
- `pkg/wklog`：结构化日志

### ❌ 不允许依赖
- `internal/access/*`
- `internal/usecase/*`
- `internal/app`
- `internal/gateway/*`

Node RPC transport adapters live in `internal/access/node`. This package only speaks neutral DTOs and narrow ports.

## 3. 核心组件

| 组件 | 说明 |
|------|------|
| `Plane` | 对外门面：生命周期、`AppendBatch` 入口、reactor 组装 |
| `Reactor` | Channel hash shard：事件 inbox、active-cell 调度、单 shard 公平推进 |
| `ChannelCell` | 单 channel 状态机：pending 队列、route resolve、effect 执行、future 完成 |
| `RouteResolver` | 权威路由读取/缓存/失效：singleflight、route generation fencing、metadata refresh |
| `PeerReactor` | 远端 append 批处理器：按目标节点与 lane 聚合 `AppendBatches` RPC |
| `Future` | 请求完成句柄：上下文取消、关闭完成、结果对齐 |

## 4. 核心流程

### 4.1 Durable append

```
message.App / 其他 durable sender
  ↓
Plane.AppendBatch(ctx, req)
  ↓
按 ChannelID 哈希到固定 reactor shard
  ↓
ChannelCell 将请求排入单 channel pending 队列
  ↓
若本地 route 缺失或过期：通过 RouteResolver 读取/刷新 authoritative meta
  ↓
RouteGeneration / ChannelEpoch / LeaderEpoch 通过后进入 effect 执行
  ↓
本地 leader：调用 LocalOwner.AppendLocalBatch
  ↓
远端 leader：交给 PeerReactor 发送 AppendBatches RPC
  ↓
按输入顺序回填每个 future 的结果
```

### 4.2 Route resolution

`RouteResolver` 维护 channelplane 需要的 authoritative route view。它把 `channelmeta` 的原始元数据投影为 `ChannelRoute`，并用 `RouteGeneration`、`ChannelEpoch`、`LeaderEpoch`、lease 与 fence 共同做 RPC epoch fencing。

- 旧的或不健康的 route view 会被失效。
- `InvalidateRoute` 使用 `RouteGeneration` 做代际保护，旧 append effect 不能误删更新的 cached route。
- stale route、not-leader、lease-expired、write-fenced 结果会触发一次权威刷新。
- 刷新后仍不匹配时，结果会以 typed status 回到调用方，不在 message 层再做 second-hop redirect。

### 4.3 Peer batching

`PeerReactor` 按目标节点再按 lane 聚合 remote append。

- 同一 target node 的多个 channel append 可共用一次 RPC flush。
- flush 条件由最大等待时间、最大记录数、最大字节数决定。
- 队列满时返回 typed backpressure，避免无界积压。
- RPC 响应会按原始 batch 顺序拆回 owning reactor，保证同 channel 的 future 顺序不乱。

### 4.4 同频道串行语义

- 同一 channel 只允许一个 inflight effect。
- 后续 append 进入 pending queue，直到前一个 effect 完成。
- 这保证同频道 durable append 的结果顺序与请求顺序一致。
- 不同 channel 仍可在不同 reactor shard 上并行推进。

## 5. Backpressure 模型

- reactor inbox 有界，防止 channel 热点把调度器打爆。
- channel cell pending 队列有界，避免单 channel 无限排队。
- peer lane 有界，避免远端 leader 慢响应把本地 reactor 阻塞。
- typed backpressure 会通过 `ErrOverloaded` / `ErrPeerBackpressured` 归一化给上层，message usecase 只看到最终 append 失败，不感知内部 lane 细节。

## 6. Shutdown

`Stop` 必须：
- 停止新的 append 接入
- 关闭 reactor/peer lane 背景循环
- 以 terminal error 完成所有未决 future
- 避免把停机过程误判成 route 失败或 leader 失败

## 7. Tests

```bash
GOWORK=off go test ./internal/runtime/channelplane -count=1
GOWORK=off go test ./internal/runtime/channelplane -run 'Test(ChannelPlane|ChannelCell|RouteResolver|PeerReactor)' -count=1
```
