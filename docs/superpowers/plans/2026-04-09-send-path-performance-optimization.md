# Send Path Performance Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提升三节点真实发送链路的 durable send 吞吐，先消除 data-plane 并发限制和 ISR 锁竞争带来的异常排队与 timeout，同时保持现有集群语义与成功条件不变。

**Architecture:** 本轮先做两类保守优化：一是把 data-plane connection pool、fetch inflight、pending fetch RPC 从同一个 `PoolSize` 里解耦，避免测试和运行时被隐式背压提前卡死；二是缩小 `pkg/replication/isr` 的 `Append`、`Fetch`、`ApplyFetch` 锁范围，把日志 I/O 与状态更新拆开，减少 leader/follower 间的串行化。所有改动先由 focused 测试锁定，再用真实 `send_stress_test` 和 fresh `pprof` 做回归验证。

**Tech Stack:** Go 1.23、`testing`、`testify`、`internal/app`、`pkg/replication/isr`、`pkg/replication/isrnode`、`pkg/replication/isrnodetransport`、`pkg/transport/nodetransport`、`pprof`。

**Spec:** `docs/superpowers/specs/2026-04-09-send-path-performance-optimization-design.md`

---

## 执行约束

- 每个行为修改都先按 `@superpowers/test-driven-development` 写失败测试，再写最小实现。
- 本轮不改变 durable send 成功语义，不降低 `MinISR`，不引入绕过集群语义的单机分支。
- 如果在拆锁过程中发现必须引入批量提交才能保持正确性，暂停并回到设计阶段；本轮不偷偷落 group commit。
- 所有性能结论都必须以 fresh 压测输出和 fresh profile 为依据，最终交付前执行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `internal/app/config.go` | 新增/扩展 data-plane 并发相关配置项与默认值 |
| `internal/app/build.go` | 将 isr runtime limit 与 data-plane 参数解耦 |
| `internal/app/lifecycle.go` | 组装 data-plane pool、transport adapter，并使用新的独立并发配置 |
| `internal/app/multinode_integration_test.go` | 为三节点 harness 增加显式 data-plane 并发覆盖，避免默认 `PoolSize=1` 锁死压力测试 |
| `internal/app/lifecycle_test.go` | 覆盖新的 data-plane 默认值和装配行为 |
| `pkg/replication/isr/fetch.go` | 收缩 `Fetch` 锁范围 |
| `pkg/replication/isr/replication.go` | 收缩 `ApplyFetch` 锁范围 |
| `pkg/replication/isr/append.go` | 收缩 `Append` 锁范围中的非必要锁内工作 |
| `pkg/replication/isr/*_test.go` | 补回归测试，锁定拆锁后的语义不变 |
| `internal/app/send_stress_test.go` | 使用显式并发参数复跑真实发送压力测试 |

## Task 1: 解耦 data-plane 并发配置

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Test: `internal/app/lifecycle_test.go`

- [ ] **Step 1: 写失败测试，锁定新的 data-plane 配置默认值与装配行为**

在 `internal/app/lifecycle_test.go` 新增 focused 测试，覆盖：

- 当 `Cluster.PoolSize=1` 且未显式配置时，data-plane 仍使用保守默认 fetch inflight / pending RPC
- 当显式配置 data-plane 并发参数时，`build` 和 `startChannelMetaSync` 会把这些值传到 isr runtime 与 transport adapter

建议测试名：

```go
func TestNewConfiguresIndependentDataPlaneLimits(t *testing.T)
func TestStartChannelMetaSyncUsesExplicitDataPlaneSettings(t *testing.T)
```

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./internal/app -run 'Test(NewConfiguresIndependentDataPlaneLimits|StartChannelMetaSyncUsesExplicitDataPlaneSettings)$' -count=1
```

Expected: FAIL，因为当前配置模型还没有独立 data-plane 参数。

- [ ] **Step 3: 写最小实现**

在 `internal/app/config.go` 的 `ClusterConfig` 中新增独立字段，例如：

```go
DataPlanePoolSize         int
DataPlaneMaxFetchInflight int
DataPlaneMaxPendingFetch  int
```

要求：

- `ApplyDefaultsAndValidate` 为这些字段提供保守默认值
- 默认值不能依赖“单节点语义”，而应保持单节点集群/多节点集群一致逻辑
- `internal/app/build.go` 中 isr runtime 使用独立的 `DataPlaneMaxFetchInflight`
- `internal/app/lifecycle.go` 中 `nodetransport.NewPool` 使用 `DataPlanePoolSize`
- `internal/app/lifecycle.go` 中 `isrnodetransport.New` 使用 `DataPlaneMaxPendingFetch`

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./internal/app -run 'Test(NewConfiguresIndependentDataPlaneLimits|StartChannelMetaSyncUsesExplicitDataPlaneSettings)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/app/config.go internal/app/build.go internal/app/lifecycle.go internal/app/lifecycle_test.go
git commit -m "refactor(app): decouple data plane concurrency settings"
```

## Task 2: 更新三节点 harness，让压力测试能显式放大 data-plane 并发

**Files:**
- Modify: `internal/app/multinode_integration_test.go`
- Test: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: 写失败测试，锁定三节点 harness 的 data-plane 配置**

在 `internal/app/multinode_integration_test.go` 增加 focused 测试，校验 `newThreeNodeAppHarness` 生成的配置不会再把 data-plane 完全锁死在 `PoolSize=1` 上。

建议测试名：

```go
func TestThreeNodeAppHarnessUsesExplicitDataPlaneConcurrency(t *testing.T)
```

- [ ] **Step 2: 跑 focused 测试，确认失败**

Run:

```bash
go test ./internal/app -run '^TestThreeNodeAppHarnessUsesExplicitDataPlaneConcurrency$' -count=1
```

Expected: FAIL，因为 harness 目前只设置 `Cluster.PoolSize = 1`。

- [ ] **Step 3: 写最小实现**

在 `newThreeNodeAppHarness` 中为测试显式设置更合理的 data-plane 参数，例如：

```go
cfg.Cluster.PoolSize = 1
cfg.Cluster.DataPlanePoolSize = 8
cfg.Cluster.DataPlaneMaxFetchInflight = 16
cfg.Cluster.DataPlaneMaxPendingFetch = 16
```

要求：

- 保留控制平面测试所需的较小 `Cluster.PoolSize`
- 只放大发送链路和 ISR 复制相关的数据面并发

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./internal/app -run '^TestThreeNodeAppHarnessUsesExplicitDataPlaneConcurrency$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/app/multinode_integration_test.go
git commit -m "test(app): configure data plane concurrency in harness"
```

## Task 3: 为 `replica.Fetch` 拆锁写回归测试

**Files:**
- Test: `pkg/replication/isr/benchmark_test.go`
- Test: `pkg/replication/isr/fetch_test.go`

- [ ] **Step 1: 写失败测试，证明 `Fetch` 不应在 `log.Read` 期间长期持有 replica 锁**

新增一个受控 fake log 或包装器，在 `Read` 阶段阻塞，并从并发 goroutine 触发一个只需要短暂拿锁的副本状态操作，断言该操作不再被 `Read` 整段阻塞。

建议测试名：

```go
func TestFetchDoesNotHoldReplicaLockAcrossLogRead(t *testing.T)
```

- [ ] **Step 2: 跑 focused 测试，确认失败**

Run:

```bash
go test ./pkg/replication/isr -run '^TestFetchDoesNotHoldReplicaLockAcrossLogRead$' -count=1
```

Expected: FAIL，因为当前 `Fetch` 在 `log.Read` 之前就持有 `r.mu` 并延续到函数返回。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/fetch.go` 中重排逻辑：

- 锁内读取 `state/meta` 快照并完成合法性检查
- 计算 divergence / truncate / HW 推进所需的状态
- 如果需要读日志，先释放锁，再执行 `log.Read`
- 仅在必要时回锁提交状态

要求：

- 不能改变 `FetchResult` 语义
- 不能改变 `advanceHWLocked` 的时机约束
- 不能放松 epoch / leader / snapshot 校验

- [ ] **Step 4: 重跑 focused 测试与现有 fetch 测试**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(FetchDoesNotHoldReplicaLockAcrossLogRead|Fetch.*)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isr/fetch.go pkg/replication/isr/fetch_test.go
git commit -m "perf(isr): reduce lock scope in fetch"
```

## Task 4: 为 `replica.ApplyFetch` 拆锁写回归测试

**Files:**
- Modify: `pkg/replication/isr/replication.go`
- Test: `pkg/replication/isr/append_test.go`
- Test: `pkg/replication/isr/benchmark_test.go`

- [ ] **Step 1: 写失败测试，证明 `ApplyFetch` 不应在 `log.Append/Sync` 期间长期持有 replica 锁**

新增受控 fake log，在 `Append` 或 `Sync` 阶段阻塞，同时并发触发需要副本状态锁的轻量操作，断言该操作不会被整段日志 I/O 拖住。

建议测试名：

```go
func TestApplyFetchDoesNotHoldReplicaLockAcrossLogSync(t *testing.T)
```

- [ ] **Step 2: 跑 focused 测试，确认失败**

Run:

```bash
go test ./pkg/replication/isr -run '^TestApplyFetchDoesNotHoldReplicaLockAcrossLogSync$' -count=1
```

Expected: FAIL，因为当前 `ApplyFetch` 在 `Append/Sync` 期间一直持有 `r.mu`。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/replication.go` 中重排流程：

- 锁内完成 epoch / leader / truncate 参数校验和必要状态快照
- 锁外执行日志 append / sync
- 锁内提交 `LEO/HW/checkpoint` 更新

要求：

- 继续保证 truncate 语义
- 继续保证 checkpoint 与 HW 一致
- 不新增新的 public API

- [ ] **Step 4: 重跑 focused 测试与现有 apply 测试**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(ApplyFetchDoesNotHoldReplicaLockAcrossLogSync|Apply.*)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isr/replication.go pkg/replication/isr/*test.go
git commit -m "perf(isr): reduce lock scope in apply fetch"
```

## Task 5: 对 `replica.Append` 做保守锁范围收缩

**Files:**
- Modify: `pkg/replication/isr/append.go`
- Test: `pkg/replication/isr/append_test.go`

- [ ] **Step 1: 写失败测试，锁定 append 路径的关键语义**

重点锁定：

- leader append 仍然只在 committed 后返回成功
- context cancel 时 waiter 正确移除
- 拆锁后不会破坏已有 HW 推进和 error 语义

如果现有测试未覆盖拆锁风险，补最小 focused 测试，例如：

```go
func TestAppendRetainsCommitWaitSemanticsAfterLockRestructuring(t *testing.T)
```

- [ ] **Step 2: 跑 focused 测试，确认当前实现不满足新约束或缺乏保护**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(AppendRetainsCommitWaitSemanticsAfterLockRestructuring|Append.*)$' -count=1
```

Expected: FAIL 或缺失保护行为。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/append.go` 中只做保守优化：

- 能提前完成的合法性校验继续锁内做
- 尽量减少 append 前后的非必要锁内工作
- 不改变 `select { case <-waiter.ch ... }` 的提交等待语义

- [ ] **Step 4: 重跑 append 测试**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(AppendRetainsCommitWaitSemanticsAfterLockRestructuring|Append.*)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isr/append.go pkg/replication/isr/append_test.go
git commit -m "perf(isr): reduce nonessential append lock work"
```

## Task 6: 跑真实发送压力测试并抓 fresh profile

**Files:**
- Verify only: no new source files expected unless regression appears

- [ ] **Step 1: 跑 focused app 测试，确认功能面无回归**

Run:

```bash
go test ./internal/app ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -count=1
```

Expected: PASS

- [ ] **Step 2: 跑真实 send stress，记录优化后基线**

Run:

```bash
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v
```

Expected: PASS，且 QPS 高于优化前基线，timeout 明显减少或消失。

- [ ] **Step 3: 抓 fresh profile**

Run:

```bash
mkdir -p /tmp/wukongim-send-profile-after
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 \
  -cpuprofile /tmp/wukongim-send-profile-after/send.cpu.pprof \
  -memprofile /tmp/wukongim-send-profile-after/send.mem.pprof \
  -blockprofile /tmp/wukongim-send-profile-after/send.block.pprof \
  -mutexprofile /tmp/wukongim-send-profile-after/send.mutex.pprof
```

Expected: PASS，并成功生成 profile 文件。

- [ ] **Step 4: 对比 before/after profile**

Run:

```bash
go tool pprof -top -sample_index=delay /tmp/wukongim-send-profile-after/send.block.pprof
go tool pprof -top -sample_index=delay /tmp/wukongim-send-profile-after/send.mutex.pprof
go tool pprof -top /tmp/wukongim-send-profile-after/send.cpu.pprof
```

Expected:

- `replica.Fetch` / `ApplyFetch` 的锁竞争占比下降
- `sendEnvelope` 背压占比下降
- 发送链路不再主要卡在隐式 data-plane 排队

- [ ] **Step 5: 记录结果并决定是否进入下一轮 batch/group-commit 设计**

把以下结果整理到最终交付说明：

- 优化后 QPS、p50、p95、p99
- 关键热点变化
- 是否还需要下一轮 batch/group-commit

- [ ] **Step 6: 提交**

```bash
git add <only modified source files>
git commit -m "perf(send): improve durable send replication baseline"
```

