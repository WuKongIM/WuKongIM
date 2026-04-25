# ISR Benchmarks Design

## 目标

为 `pkg/isr` 增加第一版性能测试，目标是提供一组可稳定运行的 Go benchmark，用于观察 ISR 核心路径的吞吐、延迟和内存分配特征。

本设计只覆盖 `pkg/isr`，不扩展到其他包，也不引入性能阈值断言或 CI 门禁。

第一版固定目标:

- 使用标准 `go test -bench` 基准风格
- 输出 `-benchmem` 指标
- 同时覆盖微基准和端到端复制吞吐
- 复用现有测试夹具，避免为 benchmark 改生产接口

## 背景与约束

`pkg/isr` 当前已经具备:

- `Append` 写入与提交等待
- `Fetch` 复制拉取
- `ApplyFetch` follower 复制应用
- `NewReplica` / `BecomeLeader` 恢复与切主
- `testenv_test.go` 中的 fake store 与三副本测试夹具

本设计需要满足以下约束:

1. 范围只限 `pkg/isr`
2. 第一版 benchmark 只要求稳定输出结果，不做性能阈值校验
3. benchmark 不应依赖随机行为或外部环境
4. benchmark helper 不应侵入生产代码边界
5. benchmark 结果应尽量避免被状态累积污染

## 方案对比

考虑三个方向:

- `A`: 单文件参数化 benchmark，复用现有测试夹具
- `B`: 按路径拆分多个 benchmark 文件，各自维护 helper
- `C`: 先造一套自定义压测 harness，再包到 benchmark 中

最终选择 `A`。

选择原因:

1. **与仓库现有 benchmark 风格一致**
   仓库现有性能测试主要采用标准 `Benchmark...` + `b.Run(...)` 子用例模式，`A` 与现有风格最一致。

2. **改动最小**
   `pkg/isr` 已经有完善的 fake store 和三副本夹具，直接复用即可，不需要为了 benchmark 额外引入一层复杂基础设施。

3. **维护成本最低**
   第一版 benchmark 规模有限，集中在一个文件里更容易控制命名、参数和 helper 复用，避免拆散后出现重复代码和隐式硬编码。

4. **符合 YAGNI**
   当前目标只是构建第一版性能观察手段，不需要并发压测框架、profile 自动采集或性能门禁。

## 设计范围

第一版只新增以下内容:

- `pkg/isr/benchmark_test.go`
- 少量用于校验 benchmark harness 的普通测试

第一版明确不做:

- 跨包 benchmark
- 并发压测框架
- 自动 profile 导出
- CI 性能门禁
- 为 benchmark 额外暴露生产接口

## Benchmark 布局

benchmark 集中放在:

```text
pkg/isr/benchmark_test.go
```

统一命名采用:

```text
Benchmark<Subject><Action>
```

子用例采用参数化命名，显式体现关键维度，例如:

```text
batch=1/payload=128
max_bytes=4096/backlog=32
mode=truncate_append
state=dirty_tail
```

这样可以避免模糊命名，也便于后续扩展更多场景。

## Benchmark 夹具配方

为了让实现计划可以直接映射到具体 setup，本设计固定几种基准夹具配方。

### Recovery 夹具

`state=empty`

- `fakeCheckpointStore.Load()` 返回 `ErrEmptyState`
- `fakeEpochHistoryStore.Load()` 返回 `ErrEmptyState`
- `fakeLogStore.leo = 0`
- 预期 `NewReplica` 直接恢复为空 follower 状态

`state=clean_checkpoint`

- `fakeCheckpointStore.Load()` 返回 `{Epoch: 7, LogStartOffset: 0, HW: 64}`
- `fakeEpochHistoryStore.Load()` 返回 `[{Epoch: 7, StartOffset: 0}]`
- `fakeLogStore.leo = 64`
- 预期 `NewReplica` 不触发截断

`state=dirty_tail`

- `fakeCheckpointStore.Load()` 返回 `{Epoch: 7, LogStartOffset: 0, HW: 64}`
- `fakeEpochHistoryStore.Load()` 返回 `[{Epoch: 7, StartOffset: 0}]`
- `fakeLogStore.leo = 96`
- 预期 `NewReplica` 截断到 `HW=64`

### Fetch / ApplyFetch 夹具

`Fetch`

- 使用预填充 leader 日志
- 预填充记录数由 `backlogRecords` 参数控制
- `FetchOffset` 默认从 `0` 开始，保证 benchmark 主要测量读取与结果构造成本

`ApplyFetch mode=append_only`

- follower 初始 `LEO=0, HW=0`
- leader 返回无 `TruncateTo` 的 records
- 每轮只测追加与 checkpoint 推进

`ApplyFetch mode=truncate_append`

- follower 初始存在未提交脏尾
- 请求携带 `TruncateTo` 和追加 records
- 每轮覆盖“先截断，再追加，再 checkpoint”完整路径

## Benchmark 矩阵

第一版包含五组 benchmark。

### 1. `BenchmarkReplicaAppend`

测量 leader 本地 `Append` 的完整提交路径。

特点:

- 使用 leader 场景，而不是 follower 或未提交场景
- 计时区间包含 helper 驱动的 follower ack，直到 `Append` 返回提交结果
- 该 benchmark 测量的是 committed append 路径，而不是“只写 leader 本地日志、不等待提交”的局部路径
- 使用参数表驱动不同 `batchSize` 与 `payloadBytes`

建议的初始参数:

- `batch=1/payload=128`
- `batch=16/payload=128`
- `batch=16/payload=1024`

### 2. `BenchmarkReplicaFetch`

测量 leader 从已有日志中执行 `Fetch` 的性能。

特点:

- 日志数据预填充在计时开始前完成
- 子用例以 `MaxBytes` 和 backlog 大小为主要维度
- 只测稳定读取路径，不把日志扩容成本混入结果

建议的初始参数:

- `max_bytes=4096/backlog=32`
- `max_bytes=65536/backlog=256`

### 3. `BenchmarkReplicaApplyFetch`

测量 follower 执行 `ApplyFetch` 的性能。

包含两个稳定子路径:

- `mode=append_only`
- `mode=truncate_append`

其中:

- `append_only` 用于测量常规复制写入路径
- `truncate_append` 用于覆盖 follower 需要截断未提交尾部后再追加的路径

若单次循环会导致状态单向增长，则通过计时外重建夹具保证输入条件稳定。

### 4. `BenchmarkNewReplicaRecovery`

测量 `NewReplica` 的恢复开销。

至少覆盖以下状态:

- `state=empty`
- `state=clean_checkpoint`
- `state=dirty_tail`

这样既能覆盖空状态冷启动，也能覆盖 checkpoint 驱动的恢复与截断路径。

### 5. `BenchmarkThreeReplicaReplicationRoundTrip`

测量三副本串行复制闭环的吞吐。

单轮路径固定为:

1. leader `Append`
2. follower2 `Fetch + ApplyFetch + ack Fetch`
3. follower3 `Fetch + ApplyFetch + ack Fetch`
4. leader `HW` 推进并返回提交结果

第一版只做串行稳定版，不先引入并发 worker，以减少调度噪声并保持结果可解释。

## Helper 设计

benchmark helper 只放在测试文件内，不进入生产代码。

计划引入以下私有 helper:

- `makeBenchmarkRecords(batchSize, payloadBytes int) []Record`
- `newBenchmarkLeaderEnv(b *testing.B, ...)`
- `newBenchmarkFollowerEnv(b *testing.B, ...)`
- `newBenchmarkCluster(b *testing.B, ...)`
- `rebuildOutsideTimer(b *testing.B, fn func())`
- `replicateRoundTripOnce(b *testing.B, cluster *threeReplicaCluster, records []Record)`

设计原则:

1. helper 只负责准备 benchmark 状态和重复动作封装
2. helper 不隐藏核心语义，benchmark 主体仍能清楚看出被测路径
3. 所有输入参数集中到结构体表里，避免把数字硬编码在 benchmark 循环中

## 状态重置策略

为了保证 benchmark 稳定性，需要显式控制测试状态，而不是依赖累积状态自然演化。

### Append

- 每轮使用固定 records 输入
- 在 helper 内完成 follower ack，保证 waiter 不残留
- 必要时在计时外重建 leader/follower 状态

### Fetch

- leader 日志在计时前预填充
- `FetchOffset`、`MaxBytes`、backlog 全部由参数表控制
- benchmark 循环内不做日志扩容

### ApplyFetch

- 如果循环导致 follower 日志单向增长，则按 `resetAfterOps` 参数在计时外重建 follower
- `truncate_append` 模式在每次重建时显式构造脏尾状态

### Recovery

- 每轮在计时区间内执行 `NewReplica`
- store 内容构造在计时前完成
- 不把测试对象初始化逻辑和恢复逻辑混淆

### Round Trip

- 每轮都执行完整复制闭环
- 如状态累计会影响下一轮，就在计时外重建三副本集群
- 不引入后台 goroutine 或随机 sleep 控制提交

### `resetAfterOps` 固定规则

第一版明确以下默认值:

- `BenchmarkReplicaAppend`: `resetAfterOps=256`
- `BenchmarkReplicaFetch`: `resetAfterOps=0`
- `BenchmarkReplicaApplyFetch mode=append_only`: `resetAfterOps=256`
- `BenchmarkReplicaApplyFetch mode=truncate_append`: `resetAfterOps=1`
- `BenchmarkNewReplicaRecovery`: `resetAfterOps=0`
- `BenchmarkThreeReplicaReplicationRoundTrip`: `resetAfterOps=128`

其中:

- `0` 表示该 benchmark 不需要周期性重建
- `1` 表示每轮都在计时外重建夹具
- 非零值必须通过参数表显式声明，不能把重置周期硬编码在循环体中

## 可扩展性与可维护性

本设计优先保证 benchmark 可以平滑扩展，而不会在后续演进中变成一组难以维护的脚本。

具体约束:

1. **参数集中定义**
   每组 benchmark 使用结构体参数表定义 `batchSize`、`payloadBytes`、`maxBytes`、`backlogRecords`、`resetAfterOps` 等关键参数。

2. **命名显式**
   benchmark 名称和子用例名称必须反映对象、动作和场景，避免出现 `BenchmarkX1`、`small`、`normal` 之类无信息命名。

3. **不污染生产接口**
   所有 benchmark 专用逻辑保持在 `_test.go` 文件内，避免为测试便利性而扭曲 `pkg/isr` 公开 API。

4. **复用现有夹具**
   尽量站在 `testenv_test.go` 现有 fake store 和 cluster helper 之上扩展，而不是重写第二套测试环境。

5. **新增场景成本低**
   后续如果要增加更大 payload、更多 backlog、并发 round trip 或 snapshot 相关 benchmark，应只需要增加参数项或新增单个 helper，而不是重写 benchmark 骨架。

## 可测试性设计

benchmark harness 本身也需要被普通测试覆盖，避免出现“benchmark 在跑，但测的不是预期路径”。

计划增加的普通测试聚焦两类问题:

1. **闭环正确性**
   验证 round trip helper 确实能推进 leader `HW`、follower `LEO`，而不是停留在未提交状态。

2. **重置正确性**
   验证 rebuild helper 后不会残留上一次的 progress、waiter、日志尾部等状态污染。

这些测试不追求穷举，只验证 benchmark helper 的关键不变量。

## 错误处理与失败信号

benchmark 中遇到以下情况必须立即 `b.Fatal(...)`:

- `Append` / `Fetch` / `ApplyFetch` / `NewReplica` 返回非预期错误
- `HW`、`LEO`、`progress` 没有推进到预期
- 重置后的夹具状态不满足 benchmark 前置条件

这类失败应被视为 benchmark harness 错误，而不是“可以忽略的波动”。

## TDD 落地顺序

实现阶段遵循 test-first 流程:

1. 先为 benchmark helper 增加普通测试
2. 运行这些测试，确认红灯且失败原因正确
3. 实现 helper 与 benchmark 本体
4. 重新运行普通测试直到通过
5. 运行 benchmark 命令确认结果可稳定输出

这样可以先证明 benchmark harness 正确，再把 benchmark 指标作为可信数据输出。

## 验证命令

第一版验证只包含以下命令:

```bash
go test ./pkg/isr
go test ./pkg/isr -run '^TestBenchmark'
go test ./pkg/isr -run '^$' -bench . -benchmem
```

说明:

- 第一条用于回归 `pkg/isr` 现有行为
- 第二条用于单独验证 benchmark helper 的普通测试
- 第三条用于输出 benchmark 指标

## 实施结果预期

完成后，`pkg/isr` 将具备:

- 一组覆盖 ISR 核心路径的 benchmark
- 一套可复用、可扩展的 benchmark helper
- 对 benchmark harness 自身的最小正确性保护

这为后续进一步分析 `pkg/isr` 的吞吐、分配和状态恢复成本提供基线，但不会在第一版中引入额外的性能门禁和复杂压测体系。
