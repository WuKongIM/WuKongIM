# Raftstore Pebble Stress Suite Design

## Goal

为 `raftstore` 的 Pebble 持久化后端增加一套可重复执行的 benchmark 和 opt-in stress test 套件，用于长期验证：

- 写入吞吐与分配变化
- 多 group 并发访问下的稳定性
- clean reopen 后的恢复正确性
- snapshot、entry、applied index 相关持久化语义

目标是把这套能力沉淀到仓库内，作为日常开发和显式长压都可复用的测试基础设施，而不是一次性的临时压测脚本。

## Scope

### In Scope

- 仅覆盖 `raftstore` 的 Pebble 后端
- 在 `raftstore` 包内新增 benchmark
- 在 `raftstore` 包内新增默认跳过的长压测试
- 为 benchmark 和 stress test 提供共享 helper
- 通过环境变量控制规模和执行时长
- 补充运行说明文档

### Out of Scope

- 不覆盖 `raftstore.NewMemory()`
- 不修改 `multiraft.Storage` 接口或运行时语义
- 不引入独立的 `cmd/` 压测程序
- 不设置机器相关的绝对性能阈值断言
- 不做 crash-only、kill -9、磁盘故障注入等超出当前仓库测试基础设施的场景

## Constraints

- 默认 `go test ./...` 必须保持轻量，不能自动进入重压测试
- 默认 `go test ./raftstore` 只跑单元测试和普通测试，不跑长压
- 重压测试必须显式开启，例如 `WRAFT_RAFTSTORE_STRESS=1`
- 日常性能回归通过 `go test ./raftstore -bench . -run '^$'` 完成
- 单次完整长压可以接受 5 到 10 分钟
- 断言必须以语义正确性为主，不依赖特定机器上的固定时延阈值

## Current State

当前仓库已经具备：

- `raftstore/pebble.go` 的 Pebble 持久化实现
- `raftstore/pebble_test.go` 的功能正确性单测
- `multiraft/benchmark_test.go` 的 benchmark 组织风格，可作为命名和子 benchmark 参考

当前缺口是：

- 没有专门覆盖 `raftstore` 的 benchmark
- 没有针对多 group、高并发、长时间读写和反复 reopen 的仓库内压测套件
- 没有统一的压力测试 helper 和运行文档

## Design Principles

### Keep Default Test Runs Cheap

轻量测试与重压测试必须严格分层：

- 功能单测继续留在现有 `*_test.go`
- benchmark 只在 `go test -bench` 时执行
- 长压测试默认 `t.Skip`

这样可以保证开发者日常执行 `go test ./...` 时不会无意中承担 Pebble 长压成本。

### Validate Correctness Under Load, Not Just Speed

这套设计不是“只看吞吐数字”的性能展示，而是要在负载下持续校验：

- group 隔离
- 日志窗口读取正确性
- `MarkApplied` 持久化正确性
- clean close/reopen 后的状态一致性
- snapshot 后索引和 term 语义

### Reuse a Small Internal Harness

benchmark 和 stress test 都会用到相同的数据构造、模型记录、最终校验和 reopen 辅助逻辑。应当抽出轻量 helper，避免每个 case 自己手写一套状态机和断言。

## File Layout

推荐新增和修改的文件：

- `raftstore/pebble_benchmark_test.go`
  - 放日常可执行 benchmark
- `raftstore/pebble_stress_test.go`
  - 放默认跳过、显式开启的长压测试
- `raftstore/pebble_test_helpers_test.go`
  - 放 benchmark 和 stress test 共用 helper
- `docs/raftstore-stress.md`
  - 放运行方式、环境变量、结果解读

如果实现时 helper 数量很少，也可以把 helper 放回 benchmark/stress 文件中，但前提是文件仍然保持清晰可维护。

## Benchmark Layer

### Purpose

benchmark 层用于日常性能回归跟踪。它必须满足两个条件：

1. 可以稳定重复执行
2. 每个 benchmark 都带有最基本的正确性校验，避免跑出“吞吐很高但结果是错的”的假象

### Benchmark Cases

#### `BenchmarkPebbleSaveEntries`

覆盖：

- 单 group 顺序追加写
- 单 group 从某个 index 开始覆盖尾部日志
- 多 group 交错写
- 不同 payload 大小

每个 case 在 benchmark 运行后至少校验：

- `Save()` 无错误
- `LastIndex()` 与预期一致
- 必要时抽样读取最近一段 `Entries()`，确认写入结果与最后一次状态一致

#### `BenchmarkPebbleEntries`

覆盖：

- 小窗口读取
- 大窗口顺序扫描
- `maxSize` 限制下的截断读取
- reopen 后读取

每个 case 在 benchmark 运行后至少校验：

- 返回 entry 的 index 连续
- `maxSize` 语义与当前 `memoryStore` 保持一致
- 读取结果不跨 group 污染

#### `BenchmarkPebbleInitialStateAndReopen`

覆盖：

- 单 group reopen
- 多 group reopen
- 含 snapshot 的状态恢复
- 含 applied index 的状态恢复

每个 case 在 benchmark 运行后至少校验：

- `InitialState()` 中的 `HardState`、`ConfState`、`AppliedIndex` 正确
- `FirstIndex()`、`LastIndex()` 与预期一致
- 多 group 间状态互不串扰

#### `BenchmarkPebbleMarkApplied`

覆盖：

- 单 group 高频 `MarkApplied`
- 多 group 并发 `MarkApplied`

每个 case 在 benchmark 运行后至少校验：

- 最终持久化的 `AppliedIndex` 正确
- reopen 后 `AppliedIndex` 保持一致

### Benchmark Execution Notes

除专门测 reopen 的 case 外，benchmark 应将数据准备、目录创建、预装载样本等 setup 成本放在计时区间外，并通过 `b.ResetTimer()`、`b.StopTimer()` 控制测量边界。

### Benchmark Scaling

benchmark 默认只跑中小规模 case，保证开发者能在合理时间内得到结果。

额外提供可选环境变量控制更大规模，例如：

- `WRAFT_RAFTSTORE_BENCH_SCALE=default`
- `WRAFT_RAFTSTORE_BENCH_SCALE=heavy`

`heavy` 模式可以放大 group 数、entry 数和 payload 大小，但仍然通过 `go test -bench` 显式触发，不应影响普通测试。

## Stress Test Layer

### Purpose

stress test 层用于显式长压，重点验证 Pebble 后端在多 goroutine、多 group、长时间运行、周期性 reopen 下是否仍保持正确语义。

### Execution Gate

stress test 必须通过环境变量显式启用，例如：

```bash
WRAFT_RAFTSTORE_STRESS=1 go test ./raftstore -run '^TestPebbleStress' -count=1 -v
```

未设置环境变量时，这些测试应 `t.Skip`，并给出明确提示。

### Stress Cases

#### `TestPebbleStressConcurrentWriters`

场景：

- 固定数量的 group
- 多个 writer goroutine 持续调用 `Save()` 和 `MarkApplied()`
- 日志 index 按 group 单调推进
- 周期性构造 tail replacement，验证覆盖写路径

最终校验：

- 每个 group 的最终日志状态与模型一致
- `AppliedIndex <= LastIndex`
- clean reopen 后 `InitialState()`、`Entries()`、`FirstIndex()`、`LastIndex()` 与模型一致

#### `TestPebbleStressMixedReadWriteReopen`

场景：

- writers 持续写入
- readers 持续调用 `Entries()`、`Term()`、`FirstIndex()`、`LastIndex()`、`InitialState()`
- 测试执行期间按轮次进入 barrier，使读写 goroutine 先 quiesce，再执行 clean close/reopen，随后恢复下一轮

最终校验：

- 读路径全程无 error、无 panic
- reopen 后各 group 的状态延续正确
- 最终摘要校验与模型一致

#### `TestPebbleStressSnapshotAndRecovery`

场景：

- 周期性写 snapshot
- snapshot 后继续写入更高 index 的 entries
- 反复 close/reopen

最终校验：

- `FirstIndex()` 反映 snapshot 覆盖后的语义
- `Term(snapshotIndex)` 返回 snapshot term
- `ConfState` 能从 snapshot 以及已提交的 post-snapshot conf change 正确恢复

## Test Harness

### Shared Model

测试内应维护一个轻量模型，例如 `stressGroupModel`，记录每个 group 的预期状态：

- 最近一次写入后的 `lastIndex`
- 最近一次 `MarkApplied` 的值
- 当前 `firstIndex`
- snapshot 的 `index`、`term`、`ConfState`
- 必要的 entry 摘要或 checksum

模型只服务于测试校验，不应试图复刻真实存储实现。

### Helper Responsibilities

推荐 helper：

- `benchEntry(index, term, payloadSize)`：构造可控 payload 的 entry
- `benchPersistentState(startIndex, count, term, payloadSize)`：批量构造写入状态
- `openBenchDB(b)` / `openStressDB(t)`：统一打开、关闭和 reopen Pebble DB
- `verifyPebbleGroupState(...)`：统一完成最终状态断言
- 环境变量解析 helper：统一读取 duration、group 数、payload 大小、writer 数

### Correctness Contract

并发压力测试中的断言应聚焦存储契约，而不是不稳定的中间时序细节。例如：

- 可以断言最终状态和 reopen 后状态
- 可以断言 `Entries()` 返回的 index 连续
- 可以断言 `Term()`、`FirstIndex()`、`LastIndex()` 与最终模型一致
- 不应断言某个并发瞬间必须看到哪一批刚写入但尚未被下一轮覆盖的数据
- 不应把测试主动 `Close()` 期间产生的预期中断当作存储语义错误，因此 close/reopen 场景需要先让 worker quiesce

## Environment Variables

推荐支持以下环境变量：

- `WRAFT_RAFTSTORE_STRESS`
  - 是否启用长压测试
- `WRAFT_RAFTSTORE_STRESS_DURATION`
  - 长压时长，默认可设为 `5m`
- `WRAFT_RAFTSTORE_STRESS_GROUPS`
  - group 数
- `WRAFT_RAFTSTORE_STRESS_WRITERS`
  - writer goroutine 数
- `WRAFT_RAFTSTORE_STRESS_PAYLOAD`
  - entry payload 大小
- `WRAFT_RAFTSTORE_BENCH_SCALE`
  - benchmark 规模档位

实现时应为所有变量提供默认值，并在非法输入时给出清晰失败信息。

## Success Criteria

设计完成后的仓库应满足：

1. `go test ./...` 不会自动进入 Pebble 重压
2. `go test ./raftstore -bench . -run '^$'` 能稳定跑出 `raftstore` benchmark
3. `WRAFT_RAFTSTORE_STRESS=1 ...` 能显式触发 5 到 10 分钟的长压
4. benchmark 和 stress test 都会在结束时做必要的正确性校验
5. 新套件只覆盖 Pebble 后端，不改变现有生产语义
6. 有文档说明如何运行、如何调规模、如何理解结果

## Risks and Mitigations

### Risk: Test Code Becomes a Second Storage Implementation

如果测试 helper 过度复杂，容易把测试模型写成另一个难维护的存储实现。

Mitigation：

- 模型只保留最终断言所需的最小状态
- 不复刻 Pebble 内部逻辑
- 每个 helper 保持单一职责

### Risk: Benchmarks Measure Setup Noise Instead of Storage Cost

如果每轮 benchmark 都包含大量数据准备或 reopen 成本，结果会失真。

Mitigation：

- 使用 `b.ResetTimer()`、`b.StopTimer()` 明确隔离 setup
- 把 reopen 型 benchmark 单独建 case，不和纯写入 benchmark 混在一起

### Risk: Stress Tests Flake Because of Timing Assumptions

并发长压最容易因过度依赖瞬时时序而产生不稳定失败。

Mitigation：

- 只对最终状态和可稳定观察的契约做断言
- 统一使用 clean close/reopen 做最终一致性验证
- 避免把吞吐、调度顺序、goroutine 先后作为测试正确性的前提

## Recommended Commands

日常 benchmark：

```bash
go test ./raftstore -bench . -run '^$' -count=1
```

放大 benchmark 规模：

```bash
WRAFT_RAFTSTORE_BENCH_SCALE=heavy go test ./raftstore -bench . -run '^$' -count=1
```

显式长压：

```bash
WRAFT_RAFTSTORE_STRESS=1 \
WRAFT_RAFTSTORE_STRESS_DURATION=5m \
go test ./raftstore -run '^TestPebbleStress' -count=1 -v
```

## Implementation Notes for Planning

后续 implementation plan 应明确：

- 先写失败的 benchmark/stress shell 或辅助测试，再补 helper 和实现
- benchmark 与 stress test 共享 helper，但避免耦合过深
- 文档与测试代码一并交付
- 最终验证至少包含：
  - `go test ./raftstore`
  - `go test ./raftstore -bench . -run '^$' -count=1`
  - 一轮显式开启的 `TestPebbleStress...`
