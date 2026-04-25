# Send Path 3000 QPS MinISR2 Design

## 概述

本设计面向 `internal/app/send_stress_test.go` 当前三节点真实发送压测，目标是在保持 durable `sendack` 语义不变、保持三节点集群模型不变的前提下，把发送吞吐提升到 `>= 3000 QPS`。

本轮用户明确允许把 channel 运行时的 `MinISR` 从 `3` 下调到 `2`，但不允许把 `sendack` 放宽为 leader-only 成功，也不允许引入绕过集群语义的单机特例。因此，本轮优化的核心不是“改 ack 定义”，而是在 `MinISR=2` 的语义边界内，缩短 leader 等待 quorum commit 的路径，并显著降低 leader 本地 durable append 的同步成本。

## 背景与现状证据

### 压测语义

`TestSendStressThreeNode` 当前压测链路具备以下关键特征：

- 三节点 harness，客户端连接到 leader
- 每个 sender 预热独立 person channel
- channel 运行时元数据显式预热为 `Replicas=[1,2,3]`、`ISR=[1,2,3]`
- `sendack` 在 `messages.Send()` 返回后才写回客户端
- durable send 最终依赖 `channel.Append()` 完成 quorum commit

关键代码点：

- `internal/app/send_stress_test.go:822`
- `internal/app/send_stress_test.go:920`
- `internal/access/gateway/frame_router.go:51`
- `internal/usecase/message/send.go:53`
- `pkg/channel/handler/append.go:79`
- `pkg/channel/replica/append.go:56`
- `pkg/channel/replica/progress.go:81`

### pprof 与压测结论

在原始代码下，使用 throughput 模式、`workers=16`、`senders=32`、`max_inflight=64` 的一组代表性压测，得到约 `1208 QPS`，`p99` 约 `3.07s`。

配套 `cpu/block/mutex` profile 表明：

1. **主阻塞不在 gateway 回包，而在 append 等待 quorum commit**
   - block profile 的主要累计延迟落在 `pkg/channel/replica.(*replica).Append` 的 waiter 等待处
   - 说明绝大部分 send 路径时间在等 HW 推进，而不是在做 frame encode / socket write

2. **最大的有效 CPU 热点是 Pebble flush / sync**
   - CPU profile 明显落在 `record.(*LogWriter).flushPending`、`syncWithLatency`、`Write`、`Fsync` 相关 syscall
   - leader 本地 append 当前仍是每个 channel 各自 `pebble.Sync`

3. **复制重试节拍和 hard backpressure 会放大尾延迟**
   - 当前 app 构建路径把 `FollowerReplicationRetryInterval` 固定为 `1s`
   - harness 的 `DataPlanePoolSize / MaxFetchInflight / MaxPendingFetch` 也偏保守
   - follower 空 fetch 响应与 hard backpressure 共享同一重试节拍，容易形成周期性 tail

4. **参数优化能提升吞吐，但无法单独达到目标**
   - 临时把复制重试改到毫秒级、放宽 fetch 限额后，吞吐仅从约 `1208` 提升到约 `1360 QPS`
   - 再叠加更激进的 append group commit，吞吐提升到约 `1795 QPS`
   - 说明复制节拍优化和组提交优化都有效，但仅靠这两类保守调整仍不足以稳定达到 `3000 QPS`

## 目标

- 在三节点 send stress 场景下达到 `>= 3000 QPS`
- 保持 durable `sendack` 语义：成功 ack 仍代表消息已满足 durable commit 条件
- 保持三节点集群模型，不引入绕过集群的本地快速路径
- 允许将 `MinISR` 从 `3` 调整为 `2`
- 保持 durable verification 全通过，`error_rate=0`

## 非目标

- 不引入 leader-only ack / weak ack
- 不重构为共享日志分片或完全不同的存储架构
- 不重写 gateway 协议层
- 不牺牲顺序性、offset 分配语义或 durable 语义来换吞吐

## Benchmark 与验收口径

### 官方验收线

统一以 `internal/app/send_stress_test.go` 的三节点压测为准，固定以下 benchmark 口径：

- `mode=throughput`
- `duration=15s`
- `workers=16`
- `senders=32`
- `max_inflight_per_worker=64`
- `ack_timeout=20s`
- channel 运行时 `MinISR=2`

### 验收标准

- `QPS >= 3000`
- `success == total`
- `error_rate == 0`
- `verification_failures == 0`
- 建议先以 `p99 <= 2s` 作为护栏目标

### 观察线

额外保留一条更高压力的观察线，例如：

- `workers=32`
- `senders=64`
- `max_inflight_per_worker=128`

该观察线不作为本轮必须达标的唯一标准，只用于观察 headroom 和 tail 变化。

## 方案对比

### 方案 A：只做参数暴露和保守调优

仅把复制重试、fetch 并发和 append group commit 变成显式配置，再通过 benchmark 搜索最优参数。

优点：

- 改动小
- 风险低
- 可以快速建立 benchmark 与 profile 回路

缺点：

- 按现有证据，单靠此方案大概率无法稳定达到 `3000 QPS`

### 方案 B：直接改 leader append durable 路径

在保持上层语义不变的前提下，把 leader 本地 append 从“每 channel 单独 sync”改为“跨 channel 合批 sync”。

优点：

- 直接命中当前写盘瓶颈
- 最有可能带来决定性吞吐提升

缺点：

- 涉及 store / replica durable 路径，正确性要求高
- 如果没有稳定 benchmark，很难量化收益

### 方案 C：分阶段混合方案

先建立 benchmark 和参数化调优，再在此基础上改 leader append durable 路径，必要时再收拾后台提交噪音。

优点：

- 风险和收益最均衡
- 每个阶段都能单独验证效果
- 便于定位收益来源和回归问题

缺点：

- 路线比单点改动更长

## 推荐方案

选择 **方案 C**。

原因：

1. 当前用户允许 `MinISR=2`，为降低 quorum 等待创造了现实空间。
2. 现有 evidence 已经说明：复制节拍优化有效，但不够；写盘批量化才是更大的天花板。
3. 分阶段推进能先把 benchmark 固定住，再做 store 层核心改造，避免把收益和风险混在一起。

## 设计

### 第 1 阶段：参数显式化与稳定 benchmark

目标：先把当前隐藏在代码中的吞吐关键参数变成配置项，并建立稳定 benchmark。

#### 需要暴露的参数

- `FollowerReplicationRetryInterval`
- append group commit:
  - `AppendGroupCommitMaxWait`
  - `AppendGroupCommitMaxRecords`
  - `AppendGroupCommitMaxBytes`
- data plane:
  - `DataPlanePoolSize`
  - `DataPlaneMaxFetchInflight`
  - `DataPlaneMaxPendingFetch`

#### 改动边界

- `internal/app/config.go`
- `internal/app/build.go`
- `internal/app/send_stress_test.go`
- 如需透传 replica 参数，补到对应 config / builder 链路

#### 预期收益

- benchmark 口径固定
- profile 和回归更稳定
- 为第 2 阶段提供清晰对照组

### 第 2 阶段：leader append 跨 channel 合批 durable commit

目标：在不改变 durable ack 语义的前提下，把多个活跃 channel 的 leader append 合并到更少的 Pebble sync。

#### 现状问题

当前 leader append 路径：

- `pkg/channel/store/logstore.go:25` 的 `ChannelStore.Append()`
- 最终 `batch.Commit(pebble.Sync)`

这意味着：

- 同一时刻多个 channel 的 append 无法共享 sync
- 对 `send_stress` 这种“多 sender、多 channel、小消息”的 workload 非常不友好

#### 建议改法

复用或扩展现有 `pkg/channel/store/commit.go` 的 commit coordinator：

1. `ChannelStore.Append()` 不再直接自己执行 `pebble.Sync`
2. 而是向 coordinator 提交一个 `commitRequest`
3. coordinator 在短 flush window 内收集多个 channel 的 append
4. 合并为一个 Pebble write batch 并执行一次 sync
5. sync 成功后，再对每个 request 执行自己的 publish 回调

#### 必须保证的语义

- `ChannelStore.Append()` 仍然同步等待 durable commit 完成后才返回
- 同一 channel 继续用现有 `writeMu` 串行分配 base offset，保证 offset 顺序不乱
- sync 完成前不得 publish 新的 durable 状态
- batch commit 失败时，本批涉及的 append 请求必须全部失败
- 上层 `replica.Append()` / `handler.Append()` / `sendack` 语义保持不变

#### 不做的扩展

第一版不做“同一 channel 内更激进的 pipeline offset 分配”，避免复杂度过高。首要收益来自跨 channel 合批 sync，而不是单 channel 深流水。

#### 主要文件

- `pkg/channel/store/logstore.go`
- `pkg/channel/store/commit.go`
- `pkg/channel/store/engine.go`
- 必要时少量触及 `pkg/channel/replica/append.go`

### 第 3 阶段：后台提交链路降噪（可选）

如果前两阶段之后仍未达到 `3000 QPS`，再处理后台提交噪音。

#### 候选点

- `internal/app/deliveryrouting.go`
  - 当前 `asyncCommittedDispatcher.SubmitCommitted()` 每条消息起一个 goroutine
  - 可考虑改为有界 worker / shard queue

- `internal/usecase/conversation/projector.go`
  - 调整 flush / wakeup 策略
  - 降低高吞吐发送时与前台路径的锁竞争

#### 为什么排在后面

现有 block profile 的主阻塞仍在 append 等 HW；后台优化更像“最后一公里”，不应该先于第 2 阶段。

## 测试设计

### 1. store 层合批语义测试

新增 store 层单元测试，验证：

- 多个 channel 的 leader append 可以共享一次 sync
- sync 完成前 publish 不得发生
- batch 失败时所有相关 append 都失败

建议复用现有 `pkg/channel/store/commit_test.go` 中的 counting / blocking 测试基建。

### 2. durable 语义测试

用可阻塞 sync 的 fake file / batch，验证：

- sync 未完成时 `ChannelStore.Append()` 不能返回成功
- 上层 waiter 不能提前放行
- durable 状态不能提前可见

### 3. replica / handler 回归测试

重点确保：

- append 仍然要等到 quorum commit 才返回
- `MinISR=2` 时提交语义正确
- progress ack / apply fetch / follower catch-up 不受 leader append 合批破坏
- 幂等语义不回退

### 4. 端到端 benchmark 验收

在 `internal/app/send_stress_test.go` 固化 benchmark preset，并用同一组参数验证：

- `QPS >= 3000`
- durable verification 全通过
- `error_rate=0`
- tail 延迟在护栏内

## 风险与缓解

### 风险 1：leader append 合批后 durable 边界被意外放松

缓解：

- 先写 failing test 验证“sync 前不得 publish / 返回”
- store 层和 replica 层都覆盖语义测试

### 风险 2：同一 channel offset / LEO 发布顺序错乱

缓解：

- 第一版保持每 channel `writeMu` 串行
- 只在 sync 方式上做批量化，不改每 channel 的顺序约束

### 风险 3：吞吐提升有限，仍未到 `3000`

缓解：

- 第 1 阶段先建立 benchmark 与 profile 基线
- 第 2 阶段后重新 profile
- 仅在必要时进入后台链路降噪阶段

## 里程碑

### Milestone 1

- 固化 benchmark 口径
- `MinISR=2`
- 关键吞吐参数可配置
- 能稳定跑出 baseline 与 profile

### Milestone 2

- leader append 合批 durable commit 落地
- store / replica / app tests 全通过
- 官方验收线接近或达到 `3000 QPS`

### Milestone 3（如需要）

- committed dispatcher / conversation projector 降噪
- 进一步压低 tail，并补足剩余吞吐缺口

## 推荐执行顺序

1. 固化 benchmark 和 `MinISR=2` 语义
2. 用 TDD 先写 store 层 failing tests
3. 暴露并接通吞吐关键配置项
4. 实现 leader append 合批 sync
5. 跑 store / replica / runtime / app 相关测试
6. 跑官方验收线 benchmark
7. 若仍未过线，再处理后台提交降噪
