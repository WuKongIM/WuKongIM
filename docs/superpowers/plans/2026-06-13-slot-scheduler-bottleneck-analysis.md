# Slot Scheduler 瓶颈分析与优化方案

## 问题描述

在 10000 QPS 三节点基准测试中失败，实际吞吐量仅达到目标的 29.9%（2989.7 QPS），P99 延迟高达 5156.8ms。

## 根因分析

### 1. 单线程瓶颈

**位置**: `pkg/clusterv2/default_slots.go:25`

```go
const (
    defaultSlotRuntimeWorkerCount = 1  // ← 单线程瓶颈
)
```

**影响**:
- Slot Scheduler 使用单个 worker goroutine 处理所有 slot 的 raft 状态机推进
- 在高负载下（10000 QPS），单线程无法及时处理任务队列
- 导致大量任务积压在 dirty/requeued 状态

### 2. 压力证据

从 `runtime_pool_pressure_summary.tsv` 可见所有三个节点的 slot scheduler 都出现：

**node1 (127_0_0_1_5011)**:
```
component=slot pool=scheduler queue=scheduler
- inflight_max=1 workers=1 inflight_util_max=1.000  ← 100% 饱和
- admission_dirty_delta=4623                         ← 4623 个任务标记为 dirty
- admission_requeued_delta=696                       ← 696 个任务重新入队
- reason=worker_over90,worker_saturated              ← 工作线程饱和
```

**node2 (127_0_0_1_5012)**:
```
- admission_dirty_delta=9200    ← 最严重：9200 个 dirty
- admission_requeued_delta=798
```

**node3 (127_0_0_1_5013)**:
```
- admission_dirty_delta=5486
- admission_requeued_delta=1242  ← 最多重入队
```

### 3. Scheduler 工作流程

**架构** (`pkg/slot/multiraft/scheduler.go`):
```
Channel (cap=1024) → Single Worker → processSlot()
                    ↓
                Processing Map (正在处��的 slot)
                    ↓
                Dirty Map (需要重新处理的 slot)
```

**问题流程**:
1. 任务从 `scheduler.ch` (容量 1024) 被单个 worker 消费
2. Worker 调用 `processSlot()` 处理一个 slot 的所有待处理事件
3. 处理期间，如果同一个 slot 有新事件到达 → 标记为 dirty
4. 处理完成后，如果该 slot 被标记为 dirty → 重新入队
5. **单线程处理速度 < 任务到达速度 → 积压**

### 4. processSlot() 耗时分析

从 `runtime.go:106` 可见单次 `processSlot()` 包含：
```go
func (r *Runtime) processSlot(slotID SlotID) bool {
    worked := g.processRequests()         // 处理请求队列
    worked = g.processControls(...) || worked  // 处理控制消息
    worked = g.processTick() || worked          // 处理 raft tick
    readyProcessed, requeue := g.processReady(...)  // 处理 raft ready
    ...
}
```

这是一个复合操作，涉及：
- 请求队列处理
- Raft 状态机推进
- 消息发送/接收
- 日志写入

**单线程无法并行处理多个 slot**

### 5. 其他资源未饱和

**Ants 线程池利用率**:
- channelv2-rpc: 最高 83.6% (node2)
- channelappend/effect: 最高 64.8%
- channelappend/advance: 最高 45.8%
- 其他池都有余量

**系统资源**:
- CPU: 平均 81-137%，峰值 310-378% (4 核有余量)
- 内存: 2-3.6 GB (9-11%)
- 协程数: 8600-9200

**结论**: 瓶颈不在业务逻辑处理能力，而在 scheduler 调度层

## 优化方案

### 方案 1: 增加 Worker 数量 (推荐)

**修改**: `pkg/clusterv2/default_slots.go`

```go
const (
    defaultSlotRuntimeWorkerCount = 4  // 改为 4，利用多核
)
```

**优势**:
- 最小改动
- 多个 worker 并行处理不同 slot
- 单个 slot 的顺序性由 scheduler 保证（同一 slot 同时只被一个 worker 处理）

**预期效果**:
- 吞吐量提升 3-4 倍
- 10000 QPS 测试应该能通过

### 方案 2: 动态 Worker 配置 (长期)

**目标**: 根据 slot 数量和负载动态调整

```go
// Config 添加字段
type SlotsConfig struct {
    Workers int  // 0 = 自动（= min(slot_count, cpu_count)）
    ...
}

// 初始化时
workers := cfg.Slots.Workers
if workers == 0 {
    workers = runtime.NumCPU()  // 或基于 slot 数量
}
```

**优势**:
- 灵活配置
- 适应不同规模集群

### 方案 3: 优化 Scheduler 算法 (深度优化)

**当前问题**:
- 每次 `enqueue()` 都加锁
- `dispatchLocked()` 使用 `select` 非阻塞写，队列满时直接返回
- 大量 dirty/requeued 导致重复调度

**可能优化**:
1. 批量调度：一次处理多个 slot
2. 优先级队列：dirty slot 优先处理
3. 减少锁竞争：使用 lock-free 队列

**复杂度**: 高，需要仔细验证正确性

## 立即行动建议

### Step 1: 快速验证

```bash
# 修改 defaultSlotRuntimeWorkerCount
sed -i '' 's/defaultSlotRuntimeWorkerCount = 1/defaultSlotRuntimeWorkerCount = 4/' \
    pkg/clusterv2/default_slots.go

# 重新编译测试
make build
./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000
```

### Step 2: 观察指标

测试后检查：
1. `runtime_pool_pressure_summary.tsv` 中 slot/scheduler 的 `inflight_util_max` 是否降低
2. `admission_dirty_delta` 和 `admission_requeued_delta` 是否减少
3. 实际 QPS 和延迟是否改善

### Step 3: 调优

如果 4 个 worker 仍不够：
- 尝试 8 个 worker（通常不超过 CPU 核心数）
- 检查是否有其他瓶颈出现（如网络、磁盘 IO）

## 预期结果

**Workers = 4 时**:
- 实际 QPS: 8000-10000+
- P99 延迟: < 400ms
- Scheduler 利用率: 60-80%
- dirty/requeued: 大幅减少

## 风险评估

**改动风险**: 低
- 仅修改常量配置
- multiraft 本身设计支持多 worker
- 单个 slot 的顺序性由 scheduler 内部保证

**验证建议**:
- 运行完整的功能测试套件
- 运行压力测试观察稳定性
- 检查日志中是否有异常

## 参考

- 测试报告: `docs/development/perf-runs/20260613-122615-three-node-real-qps/`
- Scheduler 实现: `pkg/slot/multiraft/scheduler.go`
- Runtime 实现: `pkg/slot/multiraft/runtime.go`
- 配置位置: `pkg/clusterv2/default_slots.go:25`
