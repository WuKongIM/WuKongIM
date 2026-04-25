# Send Path Group Commit Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a conservative leader-side group-commit path so concurrent durable sends can share one durable append while preserving existing committed-before-success semantics.

**Architecture:** Keep the public `isr.Replica` API unchanged. Implement opportunistic owner-driven batching inside `pkg/replication/isr` with default-on replica-local limits (`1ms / 64 records / 64KB`) and reuse the existing HW advancement and waiter notification flow after a merged durable append completes.

**Tech Stack:** Go 1.23, `testing`, `pkg/replication/isr`, `pkg/storage/channellog`, `internal/app` send stress, `pprof`.

**Spec:** `docs/superpowers/specs/2026-04-09-send-path-group-commit-design.md`

---

## 执行约束

- 每个行为修改都先按 `@superpowers/test-driven-development` 写失败测试，再写最小实现。
- 本轮不改变 durable send 成功语义，不降低 `MinISR`，不引入绕过集群语义的单机分支。
- 本轮只做 leader append 侧 group commit，不同时落 follower apply batching。
- 最终交付前必须执行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `pkg/replication/isr/types.go` | 增加 replica 内部 group-commit 配置字段 |
| `pkg/replication/isr/replica.go` | 扩展 replica 内部批量状态 |
| `pkg/replication/isr/append.go` | 实现 owner-driven pending queue、batch collect、flush、waiter 安装 |
| `pkg/replication/isr/progress.go` | 如有必要，适配批量 waiter 通知 |
| `pkg/replication/isr/recovery.go` | 如有必要，为 snapshot/install 与 append flush 加 barrier |
| `pkg/replication/isr/testenv_test.go` | 增加 fake log 计数/阻塞控制，支撑 group-commit 回归测试 |
| `pkg/replication/isr/append_test.go` | batched append 语义、取消、durable sharing 回归 |
| `pkg/replication/isr/fetch_test.go` | 如需补充 append publish 与 fetch 可见性回归 |
| `internal/app/send_stress_test.go` | 仅用于验证，不预期改源码，除非为更稳定的统计补充 helper |

## Task 1: 为 group commit 行为写失败测试

**Files:**
- Modify: `pkg/replication/isr/testenv_test.go`
- Modify: `pkg/replication/isr/append_test.go`

- [ ] **Step 1: 写失败测试，锁定多个并发 append 共享一次 durable append**

在 `pkg/replication/isr/append_test.go` 增加 focused 测试，建议至少包含：

```go
func TestConcurrentAppendsShareSingleDurableAppend(t *testing.T)
```

测试要点：

- 构造 `MinISR=1` 的 leader env，让 commit 可本地完成
- 使用显式 group-commit test config（例如 `maxWait=20ms`, `maxRecords=2`）
- 启动两个并发 `Append()`
- 断言：
  - 两个请求都成功
  - `BaseOffset` 分别递增
  - fake log 的 durable append / sync 只发生一次

- [ ] **Step 2: 写失败测试，锁定 batched append 仍要等 committed 才返回**

增加测试：

```go
func TestBatchedAppendRetainsCommitWaitSemantics(t *testing.T)
```

测试要点：

- 使用三副本 cluster、`MinISR=3`
- 两个并发 append 被聚到一个 batch
- follower ack 之前两个 append 都不能提前返回
- follower ack 完成后两个 append 都成功返回，且 `NextCommitHW` 正确

- [ ] **Step 3: 写失败测试，锁定 pre-flush cancel 不进入 durable batch**

增加测试：

```go
func TestCanceledPendingAppendIsRemovedBeforeBatchFlush(t *testing.T)
```

测试要点：

- 第一个 append 进入 pending queue 后取消
- 第二个 append 后续 flush
- 断言取消请求返回 `context.Canceled`
- 断言 durable append 只包含未取消请求的数据

- [ ] **Step 4: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(ConcurrentAppendsShareSingleDurableAppend|BatchedAppendRetainsCommitWaitSemantics|CanceledPendingAppendIsRemovedBeforeBatchFlush)$' -count=1
```

Expected: FAIL，因为当前 `Append()` 仍是每请求单独 durable append，没有 pending batch 行为。

## Task 2: 实现 replica 内部 group-commit 批量状态与默认配置

**Files:**
- Modify: `pkg/replication/isr/types.go`
- Modify: `pkg/replication/isr/replica.go`

- [ ] **Step 1: 写失败测试，锁定默认 group-commit 配置会被应用**

在 `pkg/replication/isr/append_test.go` 或新测试文件中增加：

```go
func TestNewReplicaAppliesDefaultGroupCommitConfig(t *testing.T)
```

断言：`ReplicaConfig` 零值时，内部 group-commit 参数被填成 `1ms / 64 / 64KB`。

- [ ] **Step 2: 跑 focused 测试，确认失败**

Run:

```bash
go test ./pkg/replication/isr -run '^TestNewReplicaAppliesDefaultGroupCommitConfig$' -count=1
```

Expected: FAIL，因为当前 replica 没有这些内部状态。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/types.go` 与 `pkg/replication/isr/replica.go` 中：

- 为 `ReplicaConfig` 增加 group-commit 配置字段
- 定义内部默认值常量
- 在 `NewReplica()` 中应用默认值
- 在 `replica` 中增加 pending queue、owner 状态、signal channel 等批量状态

要求：

- 不修改公开 `Replica` 接口
- 不向 `internal/app` 新增公开配置面
- 零值配置必须得到保守默认值

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/replication/isr -run '^TestNewReplicaAppliesDefaultGroupCommitConfig$' -count=1
```

Expected: PASS

## Task 3: 用 TDD 重写 `replica.Append` 为 owner-driven group commit

**Files:**
- Modify: `pkg/replication/isr/append.go`
- Modify: `pkg/replication/isr/progress.go`
- Modify: `pkg/replication/isr/recovery.go` (only if barrier wiring is needed)
- Modify: `pkg/replication/isr/append_test.go`
- Modify: `pkg/replication/isr/testenv_test.go`

- [ ] **Step 1: 先补失败测试，锁定 flush 期间不会错误暴露未 committed 记录**

如果现有测试不足，新增：

```go
func TestFetchSkipsUnpublishedRecordsDuringGroupedAppendFlush(t *testing.T)
```

要求：

- flush 进行中时 follower fetch 不应读到尚未 publish 的 leader records
- committed 语义不变

- [ ] **Step 2: 跑 focused 测试，确认失败或保护不足**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(FetchSkipsUnpublishedRecordsDuringGroupedAppendFlush|ConcurrentAppendsShareSingleDurableAppend|BatchedAppendRetainsCommitWaitSemantics|CanceledPendingAppendIsRemovedBeforeBatchFlush)$' -count=1
```

Expected: FAIL 或缺乏保护。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/append.go` 实现：

- pending queue enqueue
- 首个请求成为 owner
- owner 依据 `maxWait/maxRecords/maxBytes` collect
- flush 时合并请求 records 为单个 batch
- 单次 durable `log.Append()`，必要时继续调用 `log.Sync()` 兼容接口
- 按请求顺序回填各自 `BaseOffset` / `RecordCount`
- 使用现有 `advanceHWLocked()` / waiter 机制按 committed 结果分别返回
- 处理 pending cancel 和 post-flush cancel
- 如设计所需，为控制面状态切换补充 barrier

要求：

- 低流量单请求最多额外等待 `1ms` 量级
- 高流量下优先由 `maxRecords/maxBytes` 触发立刻 flush
- 不改变 `CommitResult` 语义
- 不破坏 `MinISR`、lease、role 校验

- [ ] **Step 4: 重跑 append focused tests**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(ConcurrentAppendsShareSingleDurableAppend|BatchedAppendRetainsCommitWaitSemantics|CanceledPendingAppendIsRemovedBeforeBatchFlush|Append.*|FetchSkipsUnpublishedRecordsDuringGroupedAppendFlush)$' -count=1
```

Expected: PASS

## Task 4: 回归第一轮 ISR 语义与锁范围测试

**Files:**
- Verify only unless regressions appear

- [ ] **Step 1: 跑 ISR focused regression**

Run:

```bash
go test ./pkg/replication/isr -run 'Test(FetchDoesNotHoldReplicaLockAcrossLogRead|ApplyFetchDoesNotHoldReplicaLockAcrossLogSync|AppendDoesNotHoldReplicaLockAcrossLogSync|Fetch.*|ApplyFetch.*|Append.*)$' -count=1
```

Expected: PASS

- [ ] **Step 2: 如果失败，补最小修复并重跑**

只修 group-commit 改动引入的回归，不做额外优化。

## Task 5: 跑 broader regression 与真实压力测试

**Files:**
- Verify only

- [ ] **Step 1: 跑 broader regression**

Run:

```bash
go test ./internal/app ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -count=1
```

Expected: PASS

- [ ] **Step 2: 跑真实 send stress，记录第二轮基线**

Run:

```bash
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v
```

Expected: PASS，且 QPS 高于本轮之前的 `48.00` 基线。

- [ ] **Step 3: 抓 fresh profile**

Run:

```bash
rm -rf /tmp/wukongim-send-profile-group-commit
mkdir -p /tmp/wukongim-send-profile-group-commit
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 \
  -cpuprofile /tmp/wukongim-send-profile-group-commit/send.cpu.pprof \
  -memprofile /tmp/wukongim-send-profile-group-commit/send.mem.pprof \
  -blockprofile /tmp/wukongim-send-profile-group-commit/send.block.pprof \
  -mutexprofile /tmp/wukongim-send-profile-group-commit/send.mutex.pprof
```

Expected: PASS，并成功生成 profile 文件。

- [ ] **Step 4: 对比 after-after profile**

Run:

```bash
go tool pprof -top -sample_index=delay /tmp/wukongim-send-profile-group-commit/send.block.pprof
go tool pprof -top -sample_index=delay /tmp/wukongim-send-profile-group-commit/send.mutex.pprof
go tool pprof -top /tmp/wukongim-send-profile-group-commit/send.cpu.pprof
```

Expected:

- `appendPayloads` / Pebble commit 的摊薄效果可见
- `replica.Append` 与 send path 的 wait cost 继续下降
- 第二轮基线优于 `48.00 QPS`
