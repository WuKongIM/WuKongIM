# Send Path 5000 QPS Transport Design

## 概述

本设计面向 `internal/app/send_stress_test.go` 当前三节点、`MinISR=2`、durable `sendack` 语义下的官方 throughput preset，目标是在不放宽 durable commit 语义、不破坏当前 dual-watermark 正确性的前提下，把 send path 从当前约 `4210 QPS` 推进到 `>= 5000 QPS`，同时把 `p95` 维持在当前基线附近，不接受用明显更差的尾延迟去换吞吐数字。

本轮设计聚焦 transport 热路径，而不是再次扩大会写盘 / Pebble 参数面的改动。原因是最新 `pprof` 已经显示 checkpoint 已退出 hot path，剩余热点集中在：

- `pkg/channel/transport.(*peerSession).Flush`
- `pkg/channel/transport.(*peerSession).sendFetchRequest`
- `pkg/transport.(*Pool).acquire`
- Pebble WAL / flush / compaction

在这些热点里，transport 侧仍然存在更低风险、更容易拿到确定收益的空间：连接获取、连接复用、batch flush/fallback 放大，以及 fetch/probe RPC 的退化路径。

## 背景与现状证据

### 当前官方验收口径

继续使用当前已经验证通过的官方压测口径：

- `internal/app/send_stress_test.go`
- 三节点 app harness
- `MinISR=2`
- `mode=throughput`
- `duration=15s`
- `workers=16`
- `senders=32`
- `max_inflight_per_worker=64`
- `ack_timeout=20s`

### 当前基线

最新一轮完整验证结果：

- `total=67198`
- `success=67198`
- `failed=0`
- `error_rate=0.00%`
- `qps=4210.29`
- `p50=476.435459ms`
- `p95=962.106917ms`
- `p99=1.109378583s`
- `verification_count=1600`
- `verification_failures=0`

### pprof 结论

最新 `tmp/profiles/send-stress-postfix.cpu.out` / `tmp/profiles/send-stress-postfix.block.out` 表明：

1. **sendack 已不再被 leader-local checkpoint 持久化主导**
   - `startCheckpointPublisher` 在 block profile 中仅约 `3.99%`
   - checkpoint path 不再是决定性瓶颈

2. **transport 热点已上升为第一优先级优化对象之一**
   - CPU profile 中 `pkg/channel/transport.(*peerSession).Flush` 约 `11.13%`
   - `pkg/transport.(*Pool).acquire` 约 `10.32%`
   - 说明 transport 仍存在明显的连接获取 / flush / RPC 路径开销

3. **append path 仍然是端到端等待主路径**
   - block profile 中 `pkg/channel/replica.(*replica).Append` 累计约 `65.73%`
   - 但这已不意味着要先改 replica 语义；更可能是 transport 与 storage 仍决定 commit 推进速度

4. **Pebble 仍然很热，但不应作为本轮第一枪**
   - `pebble/...timeDiskOp`
   - `pebble/...LogWriter.flushLoop`
   - `compaction`
   - 这些热点说明写盘仍贵，但 transport 侧还没榨干，先做更低风险的 transport 优化更合适

## 目标

- 在当前官方 throughput preset 下达到 `QPS >= 5000`
- 保持 `success == total`
- 保持 `verification_failures == 0`
- 保持 durable `sendack` 语义不变
- 将 `p95` 控制在当前基线附近，不允许相对当前基线恶化超过约 `10%~15%`

## 非目标

- 不修改 `sendack` 成功定义
- 不引入 leader-only ack
- 不降低 `MinISR`
- 不回退 dual-watermark / reconcile 正确性修复
- 不把本轮主战场扩展成 Pebble / storage 的大规模调参或重构
- 不改动 gateway 对外协议语义

## 方案对比

### 方案 A：先做 transport 连接池热路径优化

核心是减少 `pkg/transport/pool.go` 中 `Pool.acquire` 的现拨 / 争锁成本，让热点路径尽量命中已建连接。

优点：

- 风险最低
- 直接命中当前 CPU 热点
- 对吞吐和尾延迟通常都有正收益

缺点：

- 单独实施不一定足以把 `4210` 推到 `5000+`

### 方案 B：先做 channel transport batching / fallback 优化

核心是减少 `pkg/channel/transport/session.go` 中 `Flush`、`sendFetchBatch`、batch miss 后逐条 fallback 的放大开销。

优点：

- 直接命中 `peerSession.Flush` 热点
- 对吞吐和 `p95` 都有改善空间

缺点：

- 如果 flush window 或 fallback 策略过激，可能伤到 tail latency

### 方案 C：组合方案（推荐）

先做连接池热路径优化，再做 channel transport batching / fallback 优化；在不动 durable 语义和 storage 主结构的前提下，先把 transport 剩余低垂果子摘干净。如果组合方案仍未达到 `5000 QPS`，再进入单独的 Pebble follow-up 设计，而不是把 storage 调优混在本轮里。

优点：

- 风险与收益比最好
- 更容易守住 `p95` 护栏
- 容易归因，便于看清每个改动的收益

缺点：

- 需要同时覆盖 pool 和 channel transport 两个子模块

## 推荐方案

选择 **方案 C**。

原因：

1. 当前 `4210 QPS` 已经说明结构性大坑基本填平，不再需要先改语义；现在更像是在榨 transport 路径的工程效率。
2. `Pool.acquire` 和 `peerSession.Flush` 都已经进入 CPU 热点前列，且改动边界相对清晰。
3. 先做 transport 优化，更容易守住用户明确要求的 `p95` 护栏；直接推 Pebble 参数更容易拿到吞吐、但更可能放大 jitter 或把问题掩盖掉。

## 设计

### 1. `pkg/transport/pool.go`：把 acquire 从热点拨号点变成连接命中点

#### 当前问题

`Pool.acquire()` 当前行为是：

1. 先按 `nodeID` 找 `nodeConnSet`
2. 按 `shardKey % len(conns)` 选一个连接槽位
3. 若槽位无活连接，就在热点请求 goroutine 内拿 `dialMu[idx]` 并现场拨号

这会带来几个问题：

- 热点请求直接承担拨号成本
- 多个 goroutine 会在单个槽位的 `dialMu` 上排队
- 新 leader / 新 peer / 连接抖动时，热点路径容易退化成“现拨 + 等锁”
- 即使 `Pool.Size > 1`，也不保证高频 shard 能在压测开始前已经 warm up

#### 目标行为

- 热点路径以“读现有连接”为主，不以“现拨连接”为主
- 连接失效后的重建尽量异步，不让首个业务请求承担全部重建开销
- 保持 shard 到连接槽位的稳定映射，不改变现有分布语义

#### 设计约束

- 不修改上层 `wktransport.Client` / `Pool` 的公开语义
- 不破坏现有按 `nodeID + shardKey` 的连接分配行为
- 不引入后台 goroutine 风暴
- 不为了预热而无界地连接所有 peer / 所有槽位

#### 建议改法

引入“按需预热 + 失败后异步重建”的保守模型：

1. 为 `nodeConnSet` 增加轻量级槽位状态
   - 是否正在后台预热
   - 最近一次拨号失败时间 / 短暂冷却窗口

2. `acquire()` 热点路径改成三段式
   - 若槽位已有活连接，直接返回
   - 若槽位无活连接但尚未进入后台预热，则触发一次异步预热，并根据场景决定是否同步等待极短时间
   - 若已有后台预热线程在跑，则热点路径避免重复拨号，只做有限等待或快速失败重试

3. 压测 / runtime 启动阶段允许显式预热必要 peer 的连接槽位
   - 只预热当前已知活跃 peer
   - 不要求全量铺满所有槽位
   - 目标是降低 throughput test 刚进入稳态时的大量现拨抖动

4. 连接失效后，下一个请求触发异步重建
   - 避免所有请求一起卡在同步拨号上

#### 不做的事

- 不引入复杂的 LRU / dynamic rebalance
- 不让 pool 脱离当前 shard-sticky 语义
- 不做大规模接口改造

### 2. `pkg/channel/transport/session.go`：稳定 batch flush，减少 fallback 放大

#### 当前问题

`peerSession` 当前路径中，fetch batching 有几个潜在放大点：

- `TryBatch()` 只按固定 `200us` flush window 聚合，窗口策略较死
- `Flush()` / `flushBatch()` 失败后，会逐条 `sendFetchRequest()` fallback
- batch 响应里单个 item 缺失或返回 error，也会退回逐条单发
- reconcile probe 与 fetch 都走同一 client / pool 热路径，连接 miss 时容易互相放大

这意味着：

- 一次 batch miss 可能变成 N 次串行单发 RPC
- transport 热点不仅是“正常 flush”，也是“flush 后的退化路径”
- 在 throughput 高峰时，这类 fallback 容易把 `p95` 和连接获取开销一起放大

#### 目标行为

- 能 batch 时稳定 batch
- batch 失败时退化路径要保守，但不能过度放大
- fetch / reconcile 的 flush 要尽量压在已建连接上执行
- 不因为更激进的 batching 让 `p95` 明显恶化

#### 建议改法

1. 将 batch flush 从“纯定时器驱动”改为“定时器 + 阈值驱动”
   - 保留短 flush window，避免过久排队
   - 当 batch 条数或字节数达到阈值时立即 flush，不再死等 timer

2. 限制单次 batch miss 后的 fallback 放大
   - 优先对整个 batch 进行一次保守退化，而不是每个 item 都独立重新走最重路径
   - 对 item-level miss，优先按小批次补发，而不是完全逐条补发

3. 将 reconcile probe 也纳入相同的“优先命中已建连接”路径
   - 避免 probe 在 provisional leader 时频繁制造现拨
   - 保持 probe 与 fetch 的队列隔离，不让低频 probe 干扰高频 fetch batch

4. 强化 `Backpressure()` 与 queue 上限的护栏
   - 避免因为扩 batch 或延后 flush 让 pending queue 悄悄膨胀
   - 保持 hard backpressure 语义不变

#### 不做的事

- 不把 fetch / probe / progressAck 统一成复杂的大总线 batch
- 不改 runtime envelope 语义
- 不为了省几次 RPC 而显著增加队列等待时间

### 3. `internal/app/send_stress_test.go`：把 5000+ 验收护栏固化为可比较输出

本轮不改 benchmark preset，只增强观测：

- 明确输出当前 official preset 的主指标
- 明确输出与前一轮基线的对照值
- 保留 durable verification 输出
- 保留 profile 生成方式，保证 A/B 可比

如果需要新增断言，只增加“软护栏检查”而不是把 `p95` 写死成容易误伤环境波动的绝对值。

## 文件边界

### 允许修改

- `pkg/transport/pool.go`
- `pkg/transport/pool_test.go`
- `pkg/channel/transport/session.go`
- `pkg/channel/transport/transport.go`
- `pkg/channel/transport/adapter_test.go`
- `pkg/channel/transport/progress_ack_rpc_test.go`
- `internal/app/send_stress_test.go`
- 必要时少量补充 `pkg/channel/runtime/session_test.go` 或 transport 相关测试基建

### 不应修改

- `pkg/channel/replica` 的 durable 语义
- `pkg/channel/store` 的 commit/checkpoint 语义
- gateway 外部协议定义
- `MinISR` 语义和官方 throughput preset 本身

## 验收标准

### 主验收

- 官方 preset 下 `QPS >= 5000`
- `success == total`
- `error_rate == 0.00%`
- `verification_failures == 0`

### 延迟护栏

以当前基线作为 guardrail：

- `p95` 不应相对当前基线恶化超过约 `10%~15%`
- 若 QPS 超过 `5000`，但 `p95` 明显恶化，则本轮视为未达成目标

### profile 护栏

- `pkg/transport.(*Pool).acquire` 热度应明显下降
- `pkg/channel/transport.(*peerSession).Flush` 或其 fallback 路径不应继续恶化
- checkpoint path 仍不应重新回到主瓶颈

## 测试策略

### 单元测试

1. `pool.go`
   - 连接已建立时 `acquire` 不重复拨号
   - 同一槽位并发 miss 时不发生重复同步拨号风暴
   - 连接关闭后可触发重建，但不会无限重建

2. `session.go`
   - batch 达到阈值时可提前 flush
   - batch miss 后退化路径不会无界逐条放大
   - backpressure 统计与上限仍然正确

### 回归测试

- transport 相关现有测试保持通过
- runtime / channel transport 的 fetch/probe/progressAck 行为保持兼容
- `send_stress` 官方 preset 重新验证

### 性能验证

- 复跑 `TestSendStressThreeNode`
- 重新抓 CPU / block profile
- 与本轮基线对比：
  - `QPS`
  - `p50 / p95 / p99`
  - `Pool.acquire`
  - `peerSession.Flush`

## 风险与回退策略

### 风险 1：连接预热引入多余连接或 goroutine

控制方式：

- 只对活跃 peer / 活跃槽位按需预热
- 背景预热必须有限状态、有限并发

### 风险 2：更激进 batching 伤到尾延迟

控制方式：

- 保留短 flush window
- 只增加“达阈值提前 flush”，不延长窗口
- 把 `p95` 作为第一等公民护栏

### 风险 3：fallback 逻辑变复杂后回归隐藏语义

控制方式：

- item-level 成功 / 失败语义保持不变
- 先补测试，再改实现
- 若复杂度超过预期，优先保守实现，不在本轮上复杂批量协议

## Deferred Follow-up

如果 transport 组合优化完成后，官方 preset 仍未达到 `5000 QPS`，下一轮再单独出 spec，针对以下方向继续推进：

- Pebble WAL / flush / compaction 参数与写放大
- channel transport 与 transport pool 更深层的连接生命周期管理
- send stress workload 下的更激进 fetch/progress 批处理

本 spec 不把这些 follow-up 混入本轮实现范围。
