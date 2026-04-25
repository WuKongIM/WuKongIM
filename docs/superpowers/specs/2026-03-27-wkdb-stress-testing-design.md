# wkdb Stress Testing Design

## Goal

为 `wkdb` 增加一套可重复执行的双层压力测试体系：

- 轻量层提供稳定、可比较的 benchmark 基线
- 深压层提供长时间并发混合负载下的正确性验证
- 本次落地不仅要补测试入口，还要实际跑一轮并产出第一版人工基线结果

本设计只覆盖 `wkdb` 当前业务存储职责，不重新引入任何已经拆出的 Raft 语义。

约束：

- `wkdb` 继续只负责 slot-scoped business KV 和 business snapshot
- 所有业务数据仍必须严格按 `slot` 前缀隔离
- 普通 `go test ./...` 不应被长时间压力测试拖慢
- 第一版不设置硬性能阈值，只记录结果和基线

## Scope

### In Scope

- 为 `wkdb` 新增可重复执行的 benchmark 测试文件
- 为 `wkdb` 新增手动开启的深压 stress test 文件
- 为 benchmark 和 stress test 抽取共享 test helper
- 覆盖 `user`、`channel`、slot 删除、slot snapshot 导入导出四类路径
- 在压力测试中校验 slot 隔离、索引一致性、snapshot 可恢复性
- 定义手动压测命令、环境变量参数和输出约定
- 实际执行一轮 benchmark 和 stress test，记录结果

### Out of Scope

- 把压力测试接入默认 CI gate
- 引入固定的性能失败阈值
- 覆盖 `wkfsm`、`raftstore` 或 `multiraft` 的性能测试
- 引入外部压测框架或基准结果持久化系统
- 模拟多进程或多机分布式负载
- 增加业务模型或修改 `wkdb` 对外 API

## Current State

当前 `wkdb` 已有的测试全部是功能正确性测试，分布在：

- [`wkdb/user_test.go`](../../../wkdb/user_test.go)
- [`wkdb/channel_test.go`](../../../wkdb/channel_test.go)
- [`wkdb/snapshot_test.go`](../../../wkdb/snapshot_test.go)
- [`wkdb/codec_test.go`](../../../wkdb/codec_test.go)

这些测试验证了：

- `user/channel` CRUD 的 slot 隔离
- channel 二级索引扫描行为
- `DeleteSlotData` 只影响目标 slot
- `ExportSlotSnapshot/ImportSlotSnapshot` 的 round-trip 和错误处理

但当前缺少两类信号：

- 缺少标准化 benchmark，无法稳定比较 `ns/op`、`B/op`、`allocs/op`
- 缺少长时间并发混合负载，无法系统发现高压下的索引残留、slot 串写、snapshot 恢复偏差等问题

因此现阶段“全面压力测试”不能只靠一次手工跑，而是要先把压力测试入口补齐。

## Recommended Test Architecture

推荐采用双层结构：

### Layer 1: Benchmarks

新增：

- [`wkdb/benchmark_test.go`](../../../wkdb/benchmark_test.go)

职责：

- 提供轻量、稳定、可重复的 micro/mid benchmarks
- 使用 Go 原生 benchmark 输出性能指标
- 不做长时间 soak，不承担复杂最终一致性校验

执行方式：

```bash
go test ./wkdb -bench 'Benchmark(User|Channel|DeleteSlotData|SlotSnapshot)' -benchmem -count=3
```

### Layer 2: Stress Tests

新增：

- [`wkdb/stress_test.go`](../../../wkdb/stress_test.go)

职责：

- 提供显式开启的深压测试
- 在多 goroutine、多 slot、混合读写删和 snapshot 场景下校验正确性
- 输出吞吐、操作计数、随机种子和主要规模参数

执行方式：

```bash
WKDB_STRESS=1 go test ./wkdb -run 'TestStress' -count=1 -v
```

stress test 必须显式门控，避免普通 `go test ./...` 误跑长时间负载。

### Shared Test Helpers

新增：

- [`wkdb/testutil_test.go`](../../../wkdb/testutil_test.go)

职责：

- 收拢 `openTestDB`、数据构造、随机 ID 生成、reference model 校验等 helper
- 为 benchmark 和 stress test 复用同一套辅助逻辑
- 避免各测试文件重复维护准备和校验代码

## Benchmark Coverage

benchmark 只测单一职责场景，准备阶段必须放在 `b.ResetTimer()` 之前。

### `BenchmarkUserCreate`

场景：

- 单 slot
- 使用唯一 UID 批量插入用户

观察指标：

- 单次创建开销
- 写路径分配情况

### `BenchmarkUserGet`

场景：

- 预灌固定数量用户
- 基准阶段循环读取已存在 UID

观察指标：

- 热路径读取开销
- `getValue + decode` 的稳定分配量

### `BenchmarkUserUpdate`

场景：

- 预先创建用户
- 基准阶段重复更新 token、device 字段

观察指标：

- 覆盖写的成本
- value 重编码的分配行为

### `BenchmarkChannelCreateAndList`

场景：

- 围绕同一个 `ChannelID` 管理多个 `ChannelType`
- 测创建和 `ListChannelsByChannelID`

观察指标：

- 主记录写入成本
- 二级索引维护成本
- 索引扫描成本

### `BenchmarkDeleteSlotData`

场景：

- 先构造中等规模 slot 数据集
- 基准阶段重复清理目标 slot 并重建

观察指标：

- `DeleteRange + Commit(pebble.Sync)` 的成本
- slot 级清理与数据规模的关系

### `BenchmarkSlotSnapshotExportImport`

场景：

- 构造同时包含 `user/channel/index` 的 slot
- 分别 benchmark export 和 import 子场景

观察指标：

- snapshot 遍历与 payload 编码开销
- snapshot 重导入的写入和同步成本
- payload 大小与数据规模的对应关系

## Stress Coverage

stress test 需要在压负载的同时做最终一致性校验，不接受“只要不 panic 就算通过”。

### `TestStressConcurrentUsersAndChannels`

场景：

- 多 worker
- 多 slot
- 混合执行 `Create/Get/Update/Delete`

设计要求：

- 每个 worker 只对指定 slot 集合写入
- 测试结束后逐 slot 校验实际结果和 reference model 一致

必须发现的问题：

- 跨 slot 污染
- 已删除记录复活
- 成功写入但最终不可读

### `TestStressChannelIndexConsistency`

场景：

- 高并发创建、更新、删除同一批 `ChannelID + ChannelType`
- 周期性执行 `ListChannelsByChannelID`

设计要求：

- `ListChannelsByChannelID` 的返回集合必须与逐条 `GetChannel` 一致
- 删除后索引不残留
- 返回顺序保持当前 `ChannelType` 有序语义

必须发现的问题：

- 索引残留
- 索引缺项
- 列表结果与主记录不一致

### `TestStressSnapshotRestoreUnderLoad`

场景：

- 并发灌入多个 slot 的业务数据
- 对目标 slot 执行 `ExportSlotSnapshot`
- 将 snapshot 导入新 DB

设计要求：

- 导入后 user/channel 数据与 source reference model 一致
- channel index 在 restore 后仍可正常扫描
- 只恢复目标 slot，不能把其他 slot 数据带入目标库

必须发现的问题：

- snapshot 数据遗漏
- snapshot 恢复后索引失真
- slot 范围错误

## Correctness Model

所有深压测试共享一个测试内 reference model：

- 按 `slot` 维护用户映射
- 按 `slot + channelID + channelType` 维护 channel 映射
- 只在操作真正成功后更新 reference model

最终校验通过两类方式完成：

- 点查：`GetUser/GetChannel`
- 聚合查：`ListChannelsByChannelID`

这样可以把压力测试从“高并发执行一些 API”提升为“高并发下验证数据库状态和期望模型一致”。

## Runtime Controls

stress test 通过环境变量控制运行强度：

- `WKDB_STRESS=1`：启用深压测试
- `WKDB_STRESS_DURATION`：运行时长
- `WKDB_STRESS_WORKERS`：并发 worker 数
- `WKDB_STRESS_SLOTS`：参与压测的 slot 数
- `WKDB_STRESS_SEED`：随机种子，便于复现

默认值策略：

- 默认值偏保守，适合开发机手动执行
- 环境变量可手动放大，满足更深 soak 需求
- 未显式开启 `WKDB_STRESS=1` 时，相关测试使用 `t.Skip`

## Reporting

第一版不设置硬阈值，但需要统一输出以下结果：

### Benchmarks

保留 Go 原生 benchmark 输出：

- `ns/op`
- `B/op`
- `allocs/op`

### Stress Tests

通过 `t.Logf` 输出：

- 实际 `duration/workers/slots/seed`
- 总操作数和各类操作计数
- 粗粒度吞吐
- snapshot payload 大小

### Manual Baseline

本次实施完成后，额外执行一轮：

- benchmark 命令
- stress 命令

并把结果作为第一版人工基线反馈给用户。后续如果需要引入性能阈值，应基于这轮基线再决定。

## File Plan

预计新增或调整：

```text
wkdb/
  benchmark_test.go
  stress_test.go
  testutil_test.go
  user_test.go            # only if helper migration is needed
  channel_test.go         # only if helper migration is needed
  snapshot_test.go        # only if helper migration is needed
  codec_test.go           # only if helper migration is needed
```

原则：

- 尽量把新增逻辑放到新的 `_test.go` 文件中
- 不为压测改动生产 API
- 只在必要时迁移现有 helper，避免无关重排

## Verification

实施完成后至少需要验证：

```bash
go test ./wkdb -count=1
go test ./wkdb -bench 'Benchmark(User|Channel|DeleteSlotData|SlotSnapshot)' -benchmem -count=3
WKDB_STRESS=1 go test ./wkdb -run 'TestStress' -count=1 -v
```

验收标准：

- `wkdb` 现有功能测试保持通过
- benchmark 能稳定运行并输出指标
- stress test 在默认深压参数下通过最终一致性校验
- 输出足够构成第一版人工基线

## Risks And Mitigations

### Risk: 压测不稳定、结果噪音大

缓解：

- benchmark 只保留单一职责场景
- 使用固定初始规模和固定随机种子默认值
- 多次 `-count` 运行 benchmark，而不是只看单次结果

### Risk: stress test 过慢，影响日常开发

缓解：

- 用 `WKDB_STRESS=1` 显式门控
- 默认参数保守
- 长时间 soak 只作为手动命令，不纳入普通测试路径

### Risk: 测试只压 API，没验证最终状态

缓解：

- 引入 reference model
- 结束后做逐 slot、逐对象、一致性校验
- 对 channel 额外校验索引扫描结果

## Acceptance Criteria

- `wkdb` 新增 benchmark 入口，覆盖 user/channel/delete-slot/snapshot 四类路径
- `wkdb` 新增显式门控的 stress 入口，覆盖混合 CRUD、索引一致性、snapshot restore
- stress test 使用 reference model 做最终一致性校验
- 普通 `go test ./...` 不会误跑深压测试
- 本次实现后完成一轮实际 benchmark 和 stress 执行，并把结果反馈给用户
