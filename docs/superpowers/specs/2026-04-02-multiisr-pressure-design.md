# MultiISR Pressure Testing Design

## 目标

为 `pkg/multiisr` 增加第一版调度与队列稳定性压力测试，重点回答:

> 在多 group、多 peer、带 backpressure 与 snapshot 干扰的场景下，`pkg/multiisr` 是否能够持续推进而不出现队列失控、group 饥饿或恢复失败？

本轮设计固定范围:

- 只覆盖 `pkg/multiisr`
- 优先验证调度与队列稳定性
- 同时提供轻量 benchmark 和显式 gated stress test
- 默认压测规模固定为 `groups=256, peers=8`

本轮不扩展到:

- 真实网络或真实 wire protocol 压测
- CI 性能阈值门禁
- 跨包系统级压测
- 为压测修改 `pkg/multiisr` 生产语义

## 背景与约束

当前仓库中:

- `pkg/isr` 已经具备一组 benchmark
- `pkg/multiisr` 已完成第一版 runtime、scheduler、peer queue、backpressure 与 snapshot waiting 语义
- `pkg/multiisr` 现有测试以 correctness 单测为主，还没有成型的压力测试入口

用户已确认本轮边界:

1. 先做 `pkg/multiisr`
2. 重点看“调度与队列稳定性”
3. 压测路径采用混合场景:
   - replication/fetch 调度
   - backpressure + peer queue
   - snapshot queue 干扰
4. 输出形态采用:
   - `Benchmark...`
   - 显式环境变量开启的 stress test
5. 默认规模采用:
   - `groups=256`
   - `peers=8`

因此，本轮目标不是单纯追求吞吐数字，而是建立一套:

- 默认可回归
- 显式可重压
- 能给出稳定性结论

的 `pkg/multiisr` 压测体系。

## 方案对比

考虑三个方向:

- `A`: 只做 benchmark
- `B`: benchmark + gated stress
- `C`: 只做长时间 stress harness

最终选择 `B`。

选择原因:

1. **兼顾回归与稳定性**
   benchmark 适合频繁运行，stress 适合验证“不会卡死/不会失控”。`pkg/multiisr` 两者都需要。

2. **与目标最匹配**
   本轮的关注点不是极限吞吐，而是混合场景下的稳定推进。只做 benchmark 无法覆盖长时间队列恢复与饥饿问题。

3. **默认成本可控**
   通过环境变量 gate，默认 `go test ./...` 仍保持轻量，不会把重压场景强塞进日常测试。

4. **后续扩展空间充足**
   第一版先建立标准入口，后续再扩更重规模、更多矩阵或更真实 transport，不需要推翻结构。

## 设计范围

本轮只新增以下内容:

- `pkg/multiisr/benchmark_test.go`
- `pkg/multiisr/stress_test.go`
- `pkg/multiisr/pressure_testutil_test.go`
- `docs/multiisr-stress.md`

本轮明确不做:

- 修改 `pkg/multiisr` 公开 API
- 引入真实网络连接
- 自动 profile 导出
- benchmark/stress 结果写入 CI 阈值门禁
- 为测试便利性修改生产语义

## 测试结构

### 1. `pkg/multiisr/benchmark_test.go`

职责:

- 放轻量 benchmark
- 用标准 `go test -bench` 风格输出
- 关注单轮调度、peer queue drain 与混合场景运行成本

### 2. `pkg/multiisr/stress_test.go`

职责:

- 放长时间稳定性测试
- 必须通过环境变量显式开启
- 默认 `go test ./...` 不应跑重压场景

### 3. `pkg/multiisr/pressure_testutil_test.go`

职责:

- 提供 pressure harness
- 提供 fake peer session / transport 统计器
- 提供 env 配置解析
- 封装 group、peer、snapshot 驱动逻辑

### 4. `docs/multiisr-stress.md`

职责:

- 记录 benchmark/stress 命令
- 记录环境变量及默认值
- 记录如何解读输出
- 说明默认规模与重压规模

## 场景矩阵

本轮覆盖两层场景。

### Benchmark 场景

#### 1. `BenchmarkRuntimeReplicationScheduling`

目标:

- 高频 enqueue replication
- 观察多 group 多 peer 下单轮调度成本

重点指标:

- `ns/op`
- `B/op`
- `allocs/op`

#### 2. `BenchmarkRuntimePeerQueueDrain`

目标:

- 人为制造 peer inflight 限制
- 观察 queued request 被 fetch response 带动 drain 的成本

重点指标:

- drain 单轮成本
- 队列恢复路径分配情况

#### 3. `BenchmarkRuntimeMixedReplicationAndSnapshot`

目标:

- replication 为主路径
- 周期性插入 snapshot task
- 观察混合场景的调度成本与分配特征

### Stress 场景

#### 1. `TestRuntimeStressMixedScheduling`

场景:

- `groups=256`
- `peers=8`
- 持续注入 replication 请求
- 周期性切换 peer backpressure
- 低频插入 snapshot

验证:

- 所有 group 都有推进
- peer queue 不永久增长
- snapshot waiting queue 最终清空

#### 2. `TestRuntimeStressPeerQueueRecovery`

场景:

- 持续压迫 1-N 个热点 peer
- 交替进入 / 解除 hard backpressure

验证:

- hard backpressure 期间请求被排队
- backpressure 解除后队列能回落
- 不出现“只增不减”的长期卡死

#### 3. `TestRuntimeStressSnapshotInterference`

场景:

- replication 持续进行
- snapshot 周期性插入
- 观察 waiting queue 与恢复顺序

验证:

- waiting queue 最终清空
- snapshot 恢复不打断整体推进
- 不出现 group 长期饥饿

## 默认规模与环境变量

### 默认规模

第一版默认目标固定为:

- `groups=256`
- `peers=8`

这是本轮用户已确认的主规模。

### 环境变量

建议引入以下前缀:

```text
MULTIISR_STRESS
MULTIISR_STRESS_DURATION
MULTIISR_STRESS_GROUPS
MULTIISR_STRESS_PEERS
MULTIISR_STRESS_SEED
MULTIISR_STRESS_SNAPSHOT_INTERVAL
MULTIISR_STRESS_BACKPRESSURE_INTERVAL
```

语义:

- `MULTIISR_STRESS=1`
  显式开启长时间 stress test
- `MULTIISR_STRESS_DURATION`
  stress 运行时长，默认建议 `10s`
- `MULTIISR_STRESS_GROUPS`
  覆盖默认 group 数
- `MULTIISR_STRESS_PEERS`
  覆盖默认 peer 数
- `MULTIISR_STRESS_SEED`
  固定伪随机输入，便于复现
- `MULTIISR_STRESS_SNAPSHOT_INTERVAL`
  控制 snapshot 插入频率
- `MULTIISR_STRESS_BACKPRESSURE_INTERVAL`
  控制 backpressure 切换频率

第一版仍应保持:

- 默认 benchmark 轻量
- stress 只有显式开启时才运行

## Harness 设计

pressure harness 只放在测试文件内，不进入生产代码。

建议核心组件:

- `pressureHarness`
  持有 runtime、group 集合、peer 集合、统计器
- `pressureConfig`
  统一收敛默认规模与环境变量覆盖
- `pressureMetrics`
  收集每个 group 的调度次数、每个 peer 的 queue 高水位、snapshot waiting 高水位等
- `pressurePeerSession`
  支持：
  - 正常收发
  - soft/hard backpressure 切换
  - send/batch/queue 统计
- `pressureDriver`
  周期性驱动：
  - replication enqueue
  - response delivery
  - backpressure toggle
  - snapshot injection

设计原则:

1. helper 只服务 benchmark/stress，不侵入生产 API
2. 结果依赖 final state，而不是脆弱的时间片细节
3. 所有规模参数集中管理，避免在 benchmark/stress 循环内硬编码数字

## 断言策略

### Benchmark

benchmark 不设性能阈值断言。

理由:

- 不同机器波动较大
- 第一版目标是建立基线，不是卡门槛

只要求:

- 命令稳定可运行
- 输出 `-benchmem`
- 结果可重复收集

### Stress

stress 只做稳定性断言，不做机器相关性能断言。

至少要验证:

1. 所有 group 都至少被调度过
2. peer queue 不会永久增长
3. hard backpressure 解除后 queued request 能 drain
4. snapshot waiting queue 最终清空
5. 不存在长期没有推进的 group

不做:

- “必须在 X 毫秒内完成” 这种机器敏感断言
- 吞吐阈值断言

## 命令形态

建议命令:

```bash
go test ./pkg/multiisr
go test ./pkg/multiisr -run '^$' -bench 'BenchmarkRuntime(ReplicationScheduling|PeerQueueDrain|MixedReplicationAndSnapshot)$' -benchmem
MULTIISR_STRESS=1 go test ./pkg/multiisr -run 'TestRuntimeStress(MixedScheduling|PeerQueueRecovery|SnapshotInterference)$' -count=1
```

文档中可再附加重压示例:

```bash
MULTIISR_STRESS=1 \
MULTIISR_STRESS_DURATION=30s \
MULTIISR_STRESS_GROUPS=512 \
MULTIISR_STRESS_PEERS=12 \
go test ./pkg/multiisr -run 'TestRuntimeStress(MixedScheduling|PeerQueueRecovery|SnapshotInterference)$' -count=1
```

## 输出物

本轮需要产出:

1. `pkg/multiisr` benchmark 套件
2. gated stress test 套件
3. 压测 helper/harness
4. 一份操作文档 `docs/multiisr-stress.md`

## 验证方式

本轮完成前至少需要确认:

1. `go test ./pkg/multiisr` 默认仍然轻量通过
2. benchmark 命令稳定运行
3. stress test 在 gate 打开时可以稳定运行
4. stress 结果能输出明确稳定性结论，而不是只打印噪声日志

## 后续扩展点

本轮完成后，后续可以再进入下一轮设计与实现:

- 更重规模:
  - `groups=512`
  - `groups=1024`
- 更真实的 transport / response 模型
- profile 采样与热点归因
- 多节点集成层压测

但这些都不属于第一版 `pkg/multiisr` 压力测试范围。
