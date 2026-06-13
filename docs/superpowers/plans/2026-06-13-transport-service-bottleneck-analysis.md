# Transport Service 线程池瓶颈分析

## 背景

在修复 Slot Scheduler 单线程瓶颈后（Workers: 1→4），重新进行 10000 QPS 测试，发现新的瓶颈：**transportv2 service 线程池饱和**。

## 修复前后对比

### Slot Scheduler 状态

**修复前** (Workers=1):
```
node1: inflight_util=1.000 (100%饱和), dirty=4623, requeued=696, worker_saturated ✗
node2: inflight_util=1.000 (100%饱和), dirty=9200, requeued=798, worker_saturated ✗
node3: inflight_util=1.000 (100%饱和), dirty=5486, requeued=1242, worker_saturated ✗
```

**修复后** (Workers=4):
```
node1: inflight_util=0.000, dirty=17473, requeued=1309, 无 worker_saturated ✓
node2: inflight_util=0.000, dirty=16723, requeued=1349, 无 worker_saturated ✓
node3: inflight_util=0.000, dirty=16225, requeued=1095, 无 worker_saturated ✓
```

**结论**: Slot scheduler 修复成功，worker 不再100%饱和。

### 新瓶颈：Transportv2 Service 线程池

修复后暴露出新瓶颈 - transportv2 service worker 饱和：

**所有三个节点的 service_20 都100%饱和**:
```
node1: service_20: 512/512 workers (100% 饱和) ✗
node2: service_20: 512/512 workers (100% 饱和) ✗
node3: service_20: 512/512 workers (100% 饱和) ✗
```

**其他 service 也接近饱和**:
```
node2: service_2:  128/128 workers (100% 饱和)
node2: service_17: 126/128 workers (98.4%)
node2: service_16: 126/128 workers (98.4%)
node3: service_19: 128/128 workers (100% 饱和)
```

### 测试结果对比

| 指标 | 修复前 (Workers=1) | 修复后 (Workers=4) | 变化 |
|------|-------------------|-------------------|------|
| 实际 QPS | 2989.7 (29.9%) | 2898.3 (29.0%) | ⬇️ 略降 |
| P99 延迟 | 5156.8ms | 5775.1ms | ⬆️ 略升 |
| P95 延迟 | 3454.8ms | 3137.4ms | ⬇️ 降低 |
| 主要瓶颈 | Slot Scheduler | Transport Service | 转移 |

**观察**:
- 吞吐量和延迟几乎没有改善
- 瓶颈从 slot scheduler 转移到 transport service
- 说明系统有多个串联瓶颈

## 问题分析

### 1. Service Worker 饱和

从 `runtime_pool_pressure_summary.tsv`:
```
component=transportv2 pool=service_20
- workers=512
- inflight_util_max=1.000 (100% 饱和)
- worker_over90,worker_saturated

对应的队列:
component=transportv2 pool=service queue=service_20
- queue_depth_max=255/4096 (6.2%)
- queue_bytes_max=141754/67108864 (0.2%)
```

**问题**:
- Worker 线程池 100% 饱和
- 队列有积压但未满（说明worker处理速度跟不上）

### 2. Dirty/Requeued 仍然很高

尽管 slot scheduler worker 不再饱和，但 dirty/requeued 反而增加了：

**修复前**:
```
node1: dirty=4623, requeued=696
node2: dirty=9200, requeued=798
node3: dirty=5486, requeued=1242
```

**修复后**:
```
node1: dirty=17473, requeued=1309
node2: dirty=16723, requeued=1349
node3: dirty=16225, requeued=1095
```

**原因**:
- Slot scheduler 不再是瓶颈，能更快地处理和重入队
- 但下游 transport service 处理不过来
- 导致更多 slot 被标记为 dirty 并重新入队

### 3. 系统资源仍有余量

**CPU**: 平均 88-121%，峰值 261-286% (4核有余量)
**内存**: 2.6-3.4GB (8-10%)
**Ants 线程池**: 最高 83.6%

**结论**: 瓶颈不在系统资源，而在特定线程池配置

## 根本原因推测

### 串联瓶颈链

```
客户端请求
  → Gateway
  → ChannelAppend (effect/advance 未饱和)
  → Slot Scheduler (已修复，不再饱和)
  → Transport Service (新瓶颈，100%饱和) ← 当前卡点
  → Raft 状态机推进
  → 响应客户端
```

### Transport Service 的作用

Transport Service 负责：
- 节点间 RPC 消息处理
- Raft 消息传输（append、vote、snapshot等）
- Slot 状态同步

在高 QPS 下：
- Raft 需要频繁的心跳和日志复制
- 每条消息都需要通过 transport service 处理
- 512 个 worker 可能不足以处理 10000 QPS 的 raft 消息量

## 下一步行动

### 选项 1: 增加 Transport Service Worker 数量（需要找到配置位置）

**目标**: 找到并修改 service worker 数量配置

**挑战**:
- 代码中未找到明确的 512/128 常量定义
- 可能是动态计算或默认值
- 需要深入 transportv2 源码分析

### 选项 2: 优化 Raft 消息频率

**可能方向**:
- 增加 batch 大小，减少消息数量
- 调整心跳间隔
- 优化日志复制策略

### 选项 3: 使用性能分析工具

**建议**:
```bash
# 查看 CPU profile，找出热点函数
go tool pprof docs/development/perf-runs/20260613-123537-three-node-real-qps/010000-qps/pprof/after/127_0_0_1_5011-cpu.pb.gz

# 查看 goroutine 分布
grep -c "goroutine" docs/development/perf-runs/20260613-123537-three-node-real-qps/010000-qps/pprof/after/127_0_0_1_5011-goroutine.txt
```

### 选项 4: 渐进式压测

在找到配置前，先测试较低 QPS 找到系统容量：

```bash
# 测试 5000 QPS
./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 5000

# 测试 7500 QPS
./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 7500
```

找到系统的实际容量上限。

## 待确认问题

1. **Transport service worker 数量在哪里配置？**
   - 512/128 这些数字的来源
   - 是否可以配置或需要修改代码

2. **为什么有多个 service (service_2, service_17, service_20)？**
   - 它们的作用分别是什么
   - 为什么 service_20 有 512 workers，其他只有 128？

3. **Dirty/requeued 增加是否正常？**
   - 是否说明 slot scheduler 修复有效
   - 还是说明配置不当

## 参考

- 测试报告: `docs/development/perf-runs/20260613-123537-three-node-real-qps/`
- Slot Scheduler 修复: `pkg/clusterv2/default_slots.go:25` (1→4 workers)
- Transport Service 代码: `pkg/transportv2/internal/rpc/service.go`
- 压力数据: `runtime_pool_pressure_summary.tsv`
