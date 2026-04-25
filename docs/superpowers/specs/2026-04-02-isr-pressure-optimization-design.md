# ISR Pressure And Optimization Design

## 目标

对 `pkg/isr` 做第一轮压测与热点分析，判断当前实现是否存在明确且值得实施的优化空间，并在有充分证据时给出后续优化方向。

本轮设计只覆盖:

- `pkg/isr` 现有 benchmark 的基线测量
- 热点定位与结果对比
- 是否值得优化的决策标准

本轮设计不直接包含生产实现优化。

## 背景与约束

当前 `pkg/isr` 已经具备一组 benchmark:

- `BenchmarkReplicaAppend`
- `BenchmarkReplicaFetch`
- `BenchmarkReplicaApplyFetch`
- `BenchmarkNewReplicaRecovery`
- `BenchmarkThreeReplicaReplicationRoundTrip`

用户要求本轮按以下边界推进:

1. 范围只限 `pkg/isr`
2. 先做压测和热点分析，再决定是否修改生产实现
3. 优先关注吞吐和延迟，同时也观察内存分配
4. 需要在有无优化前后给出可对比结果

因此，本轮目标不是“为了优化而优化”，而是先回答:

> 当前 `pkg/isr` 的主要热点在哪里，这些热点是否值得进入实现优化阶段？

## 方案对比

考虑三个方向:

- `A`: 仅扩展现有 benchmark，收集更稳定的基线结果
- `B`: 现有 benchmark + `pprof` 热点分析
- `C`: 新增长时间 stress harness 做持续压测

最终选择 `B`。

选择原因:

1. **定位效率最高**
   单纯 benchmark 只能告诉我们“慢”，但不能解释“为什么慢”。`pprof` 可以直接给出热点函数和分配热点。

2. **改动最小**
   当前已经有可运行 benchmark，第一轮无需新增复杂 stress harness。

3. **对比更干净**
   后续如果决定优化，可以在同一批 benchmark 和同一套 profile 维度上做前后对比。

4. **避免噪声驱动决策**
   长时间压测更容易受调度、环境抖动和 benchmark 自身噪声影响。第一轮应先做可重复、易解释的测量。

## 设计范围

本轮只新增与压测分析相关的文档与可能的基准运行产物，不新增新的生产功能。

本轮明确不做:

- 新增长期运行的 stress harness
- 修改 `pkg/isr` 生产实现
- 接入 CI 性能门禁
- 跨包压测与系统级集成压测

如果分析后确认值得优化，再进入下一轮设计与实现。

## 压测对象

### 主观察路径

第一轮主观察对象固定为:

- `BenchmarkReplicaAppend`
- `BenchmarkThreeReplicaReplicationRoundTrip`

理由:

- `Append` 代表 leader 写路径的核心成本
- `ThreeReplicaReplicationRoundTrip` 代表三副本 committed 闭环的端到端成本

### 辅助观察路径

以下 benchmark 作为旁证使用:

- `BenchmarkReplicaFetch`
- `BenchmarkReplicaApplyFetch`
- `BenchmarkNewReplicaRecovery`

这些结果用于判断主热点是更偏本地写入、复制拉取、复制应用，还是恢复路径。

## 执行方法

本轮执行分三层。

### 1. 基线 benchmark

对关键 benchmark 单独运行，避免整套 benchmark 相互干扰。

执行原则:

- 使用 `-benchmem`
- 使用固定 `-benchtime`
- 对关键基准单独执行，而不是一开始就跑全套

建议的主命令形态:

```bash
go test ./pkg/isr -run '^$' -bench '^BenchmarkReplicaAppend$' -benchmem -benchtime=3s
go test ./pkg/isr -run '^$' -bench '^BenchmarkThreeReplicaReplicationRoundTrip$' -benchmem -benchtime=3s
```

需要记录的指标:

- `ns/op`
- `B/op`
- `allocs/op`

### 2. 热点 profile

只对最有价值的 benchmark 采样:

- `cpu profile`
- `memory profile`

目标是回答:

- 哪些函数占据主要 CPU 时间
- 哪些路径产生主要内存分配
- 热点是业务逻辑、数据复制逻辑，还是 benchmark 输入构造/测试夹具噪声

第一轮 profile 重点仍然放在:

- `BenchmarkReplicaAppend`
- `BenchmarkThreeReplicaReplicationRoundTrip`

### 3. 结果归因

将 benchmark 结果与 profile 热点结合，形成可执行结论，而不是只罗列数字。

输出时需要区分:

- 真实 `pkg/isr` 热点
- benchmark harness 或输入构造带来的噪声
- Go runtime 常规开销

## 是否值得优化的判断标准

只有在满足以下条件之一时，才建议进入生产实现优化阶段:

1. 同一条路径上存在明显热点，且热点集中在少数 `pkg/isr` 函数中
2. `allocs/op` 或 `B/op` 存在明显可消除的分配
3. 端到端路径的主要成本来自 `pkg/isr` 真实逻辑，而不是 benchmark 夹具或输入构造
4. 优化方向能够在不改变现有语义和测试边界的前提下落地

如果热点主要来自以下来源，则本轮结论应为“暂不建议优化生产实现”:

- benchmark 噪声
- profile 抽样不稳定
- Go runtime 常规成本
- 用户未授权修改的实现边界之外

## 输出物

本轮需要产出三类结果。

### 1. 基线数据

至少包含:

- `BenchmarkReplicaAppend`
- `BenchmarkThreeReplicaReplicationRoundTrip`

并可附带:

- `BenchmarkReplicaFetch`
- `BenchmarkReplicaApplyFetch`
- `BenchmarkNewReplicaRecovery`

### 2. 热点分析

需要说明:

- CPU 主要热点
- 内存分配主要热点
- 这些热点对应的具体代码路径

### 3. 结论

结论只能是以下两类之一:

- `值得优化`
  - 给出 1-2 个具体优化点
  - 说明为什么这些点值得进入实现阶段
- `暂不建议优化`
  - 说明为什么当前热点不足以支持修改生产实现

## 后续决策点

如果本轮结论是 `值得优化`，下一步不直接编码，而是进入新的设计与计划流程，明确:

- 允许修改的 `pkg/isr` 生产代码范围
- 目标优化点
- 预期收益
- 前后对比方式

如果本轮结论是 `暂不建议优化`，则本轮工作在结果汇总处结束，不再为“必须有动作”而引入不必要的实现变更。

## 验证方式

本轮完成前，至少需要确认以下事实:

1. benchmark 命令可以稳定运行并产出结果
2. profile 文件成功生成并能定位到热点函数
3. 结果分析能够明确区分真实热点与 benchmark 噪声

本轮不要求先实现优化，因此不存在“优化后必须变快”的硬性门槛；本轮只要求形成可信的基线和决策依据。
