# Channel Runtime Capacity Runbook

## Goal

当大量频道同时活跃时，先用节点级 active channel runtime 上限和空闲淘汰把工作集控制在可观测范围内，避免每个频道的 replica runtime、调度状态和 goroutine 无界增长。

本 runbook 面向“单节点集群”和多节点集群统一适用；不要通过绕过集群语义的独立分支处理容量问题。

## Risk Model

- 一个 active local channel runtime 会持有 channel replica、调度状态、peer queue、snapshot waiter 等节点内运行时资源
- 10 万频道同时激活时，即使每个频道业务量很小，也会放大 goroutine、heap、调度扫描和清理成本
- 没有上限时，突发创建频道可能把进程推到内存或 goroutine 压力区后才暴露问题
- 空闲频道不淘汰时，历史活跃频道会长期占用 active runtime budget

## Capacity Controls

### `WK_CLUSTER_MAX_CHANNELS`

限制单个节点上 active local channel runtime 数量。

- `0`：保持旧行为，不限制
- `>0`：达到上限后，新 channel runtime 激活失败，运行时返回 `ErrTooManyChannels`
- 多节点集群需要按节点容量设置，不是全局频道总数

建议先保守启用，例如：

```conf
WK_CLUSTER_MAX_CHANNELS=50000
```

### `WK_CLUSTER_CHANNEL_IDLE_TIMEOUT`

空闲 channel runtime 的淘汰时间。

- `0`：关闭空闲淘汰
- `>0`：超过该时间没有被使用且没有调度、peer inflight、snapshot waiter 等运行中工作时，可被安全移除
- 淘汰只释放节点内 runtime，不改变 channel log、slot metadata 或集群语义

建议初始值：

```conf
WK_CLUSTER_CHANNEL_IDLE_TIMEOUT=30m
```

### `WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL`

空闲淘汰扫描间隔。

- `0`：运行时从 idle timeout 自动派生一个有界扫描间隔
- `>0`：使用显式扫描间隔
- 间隔越短，回收越及时，但扫描成本越高

建议初始值：

```conf
WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL=1m
```

## Suggested Starting Profile

```conf
WK_CLUSTER_MAX_CHANNELS=50000
WK_CLUSTER_CHANNEL_IDLE_TIMEOUT=30m
WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL=1m
```

上线后再根据节点规格、真实频道活跃分布和指标观测调整。

## Pressure Test

默认单元测试不会跑 10 万频道压测；需要显式开启。

### Measure 100k activation footprint

```bash
MULTIISR_ACTIVATION_STRESS=1 \
MULTIISR_ACTIVATION_CHANNELS=100000 \
MULTIISR_ACTIVATION_REPORT_EVERY=10000 \
go test ./pkg/channel/runtime -run TestRuntimeStressChannelActivationFootprint -v
```

关注日志中的：

- `activated`：实际激活频道数
- `goroutines_delta`：激活期间 goroutine 增量
- `heap_alloc_delta`：当前 heap alloc 增量
- `heap_sys_delta`：runtime 向系统申请的 heap 增量
- `elapsed`：激活耗时

### Verify the cap behavior

```bash
MULTIISR_ACTIVATION_STRESS=1 \
MULTIISR_ACTIVATION_CHANNELS=100000 \
MULTIISR_ACTIVATION_MAX_CHANNELS=5000 \
MULTIISR_ACTIVATION_REPORT_EVERY=1000 \
go test ./pkg/channel/runtime -run TestRuntimeStressChannelActivationFootprint -v
```

预期：

- `activated=5000`
- 测试遇到 `ErrTooManyChannels` 后停止继续激活
- goroutine 和 heap 增量不再跟随 requested channel count 线性放大

## Rollout Steps

1. 在测试环境跑 activation footprint，记录当前节点规格下的 goroutine 和 heap 增量
2. 先配置 `WK_CLUSTER_MAX_CHANNELS`，确保突发激活会 fail closed
3. 再配置 `WK_CLUSTER_CHANNEL_IDLE_TIMEOUT` 和 `WK_CLUSTER_CHANNEL_IDLE_SCAN_INTERVAL`
4. 上线后观察 active channel、进程 goroutine、heap、GC、发送失败率和延迟
5. 如果出现 `ErrTooManyChannels` 对应的业务错误，优先判断是容量阈值过低、热点频道过多还是频道工作集无法及时淘汰
6. 稳定后按节点规格和业务峰值调整上限与 idle timeout

## Monitoring Checklist

- `wukongim_channel_active_channels`
- `wukongim_channel_max_channels`
- `wukongim_channel_activation_rejected_total{reason="too_many_channels"}`
- `wukongim_channel_idle_evictions_total`
- Go runtime goroutine 数
- Go heap alloc / heap sys / GC pause
- message send / channel activation 错误率
- channel replication long-poll 延迟和超时
- 节点 CPU，尤其是调度和清理扫描期间的尖峰

## Failure Handling

若出现 active channel runtime 继续增长或节点资源快速恶化：

- 立即确认 `WK_CLUSTER_MAX_CHANNELS` 是否为正数且在当前进程生效
- 降低 `WK_CLUSTER_CHANNEL_IDLE_TIMEOUT`，让冷频道更快释放 runtime budget
- 对热点入口限流，避免继续放大新频道激活
- 扩容节点或迁移热点频道，避免单节点 active runtime 预算长期打满
- 保留 pressure test 输出、指标截图和错误样本，用于重新评估 per-node budget

若 `ErrTooManyChannels` 明显增多：

- 不要直接关闭上限；先确认是否为真实容量保护触发
- 如果节点资源仍健康，可以逐步提高 `WK_CLUSTER_MAX_CHANNELS`
- 如果节点资源接近上限，应先扩容、迁移或降低 idle timeout
