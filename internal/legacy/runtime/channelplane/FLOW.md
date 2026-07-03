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
- `pkg/gateway/*`

Node RPC transport adapters live in `internal/access/node`. This package only speaks neutral DTOs and narrow ports.

## 3. 核心组件

| 组件 | 说明 |
|------|------|
| `Plane` | 对外门面：生命周期、`AppendBatch` 入口、reactor 组装 |
| `Reactor` | Channel hash shard：事件 inbox、ready scheduler、单 shard 公平推进 |
| `Scheduler` | reactor-local ready channel queue：以 `channel.ChannelID` 去重、FIFO/round-robin 推进 active cells |
| `ChannelCell` | 单 channel 状态机：pending 队列、route resolve、local/remote append 启动、future 完成、idle tracking |
| `EffectExecutor` | 有界执行 route resolve / local append 等可能阻塞的 effect，避免请求数驱动 goroutine 无界增长 |
| `RouteResolver` | 权威路由读取/缓存/失效：singleflight、route generation fencing、metadata refresh |
| `PeerReactor` | 远端 append 批处理器：按目标节点与 lane 聚合 `AppendBatches` RPC，固定 RPC workers 异步执行并受 timeout 约束 |
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
ChannelCell 将请求排入单 channel pending 队列并标记 ready
  ↓
Reactor scheduler 按 ready channel 顺序推进，每个 channel 一次最多启动一个 effect
  ↓
若本地 route 缺失或过期：通过 EffectExecutor 调用 RouteResolver 读取/刷新 authoritative meta
  ↓
RouteGeneration / ChannelEpoch / LeaderEpoch 通过后按 leader 位置启动 append
  ↓
本地 leader：通过 EffectExecutor 调用 LocalOwner.AppendLocalBatch
  ↓
远端 leader：直接交给 PeerReactor 异步排队；不占用 EffectExecutor worker 等 RPC
  ↓
按输入顺序回填每个 future 的结果
```

### 4.2 Route resolution

`RouteResolver` 维护 channelplane 需要的 authoritative route view。它把 `channelmeta` 的原始元数据投影为 `ChannelRoute`，并用 `RouteGeneration`、`ChannelEpoch`、`LeaderEpoch`、lease 与 fence 共同做 RPC epoch fencing。

- 旧的或不健康的 route view 会被失效。
- `InvalidateRoute` 使用 `RouteGeneration` 做代际保护，旧 append effect 不能误删更新的 cached route。
- resolver 维护 invalidation serial；失效后新的 resolve 不会 join 更早的 in-flight lookup，旧 lookup 返回后也不能重新污染 cache。
- stale route、not-leader、lease-expired、write-fenced 结果会触发一次权威刷新。
- 刷新后仍不匹配时，结果会以 typed status 回到调用方，不在 message 层再做 second-hop redirect。

### 4.3 Peer batching

`PeerReactor` 按目标节点再按 lane 聚合 remote append。

- 同一 target node 的多个 channel append 可共用一次 RPC flush。
- flush 条件由最大等待时间、最大记录数、最大字节数决定。
- lane event loop 只负责收集与切 batch；`AppendBatches` RPC 由固定数量 worker 执行，慢 RPC 不阻塞 lane 继续处理取消、超时或后续任务。
- flush queue 有界；当 RPC worker 与等待队列都满时，batch 内任务以 `ErrPeerBackpressured` 完成，避免为每个 flush 创建等待 goroutine。
- RPC 使用 `PeerRPCTimeout` 约束；同步 wrapper 的调用方 ctx 取消会释放 pending 预算，后续 lane/RPC completion 通过 once 保证不会重复完成。
- 队列满时返回 typed backpressure，避免无界积压。
- RPC 响应会按原始 batch 顺序拆回 owning reactor，保证同 channel 的 future 顺序不乱。

### 4.4 同频道串行语义

- 同一 channel 只允许一个 inflight effect。
- 后续 append 进入 pending queue，直到前一个 effect 完成。
- pending queue 在入队容量检查和 completion 重新调度前会 compact 已取消命令，避免超时请求长期占用单频道队列预算。
- reactor scheduler 对 ready channel 去重并公平推进；completion 后如果 channel 仍有 pending，再重新入队。
- 这保证同频道 durable append 的结果顺序与请求顺序一致。
- 不同 channel 仍可在不同 reactor shard 上并行推进。

### 4.5 Idle cell eviction

- reactor 为已完成且无 pending/inflight 的 channel cell 记录最后活跃时间。
- `CellIdleTTL` 到期后，reactor 周期性 sweep 并删除 idle cell，释放 cached route、pending slice backing array 与 per-channel state。
- sweep 只在 reactor goroutine 内执行，不跨 goroutine 修改 cell map。

## 5. Backpressure 模型

- reactor inbox 有界，防止 channel 热点把调度器打爆。
- channel cell pending 队列有界，避免单 channel 无限排队。
- effect executor 有界，避免 route/local effect 按请求数无界派生 goroutine。
- peer lane pending、flush queue 与 RPC worker 数均有界，避免远端 leader 慢响应把本地 reactor 或 goroutine 调度器拖垮。
- typed backpressure 会通过 `ErrOverloaded` / `ErrPeerBackpressured` 归一化给上层，message usecase 只看到最终 append 失败，不感知内部 lane 细节。

## 6. Shutdown

`Stop` 必须：
- 停止新的 append 接入，submit gate 在 stop 后稳定返回 `ErrClosed`
- 关闭 reactor/peer lane/effect executor 背景循环
- peer lane 停止时关闭 flush queue，并等待固定 RPC workers 退出或 stop context 超时
- drain reactor inbox，并以 terminal error 完成所有已接收但未处理的 future
- 避免把停机过程误判成 route 失败或 leader 失败

## 7. Tests

```bash
GOWORK=off go test ./internal/runtime/channelplane -count=1
GOWORK=off go test ./internal/runtime/channelplane -run 'Test(ChannelPlane|ChannelCell|RouteResolver|PeerReactor)' -count=1
```
