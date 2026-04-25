# Send Path 3000 QPS Throughput Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保持当前 durable send 成功语义、`MinISR` 语义和 channel -> ISR group 架构不变的前提下，把三节点真实发送链路提升到 `>= 3000 QPS`。

**Architecture:** 本轮按四条主线推进：先给 `send_stress` 增加多 inflight 的 throughput mode，让压测能够真正打满后端；再通过 follower durable apply 后的显式 progress ack 缩短 leader 提交确认路径；随后在 `isrnode` / `isrnodetransport` 做 per-peer multi-group batched fetch，减少小 RPC 风暴；最后在 `pkg/storage/channellog` 引入 node-level durable commit coordinator，把多个 group 的 durable commit 摊薄到同一 Pebble batch/sync 上，并顺手清掉成功路径上的 reread / 重复写入。

**Tech Stack:** Go 1.23、`testing`、`testify`、`internal/app`、`pkg/replication/isr`、`pkg/replication/isrnode`、`pkg/replication/isrnodetransport`、`pkg/storage/channellog`、Pebble、`pprof`。

**Spec:** `docs/superpowers/specs/2026-04-09-send-path-3000qps-throughput-design.md`

---

## 执行约束

- 每个行为修改都先按 `@superpowers/test-driven-development` 写失败测试，再写最小实现。
- 不改变 durable send 成功定义，不降低 `MinISR`，不增加新的“弱确认”等级。
- 不引入新的共享日志分片 / 分区架构；仍保持 channel -> ISR group 模型。
- 吞吐验收必须沿用现有 `send_stress` 形状：三节点、多 sender、多个人频道、小 payload，只允许新增 `throughput mode` 和 `max inflight` 驱动方式。
- 所有性能结论都必须来自 fresh 压测结果；最终交付前执行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `internal/app/send_stress_test.go` | 新增 throughput mode、`MaxInflightPerWorker`、worker writer/ack-reader 解耦、吞吐验收入口 |
| `internal/app/multinode_integration_test.go` | 保持三节点 harness 与 throughput 验收配置一致 |
| `pkg/replication/isr/types.go` | 增加 progress ack 请求/接口定义 |
| `pkg/replication/isr/progress.go` | leader progress 单调推进与 HW 推进逻辑复用 |
| `pkg/replication/isr/progress_ack.go` | progress ack 核心实现 |
| `pkg/replication/isrnode/types.go` | 新增 progress ack envelope / batched fetch 所需类型 |
| `pkg/replication/isrnode/batching.go` | peer session batching 驱动与 flush 触发 |
| `pkg/replication/isrnode/transport.go` | fetch response apply 后发送 progress ack，接收 progress ack 后推进 leader |
| `pkg/replication/isrnodetransport/codec.go` | progress ack、batched fetch request/response 编解码 |
| `pkg/replication/isrnodetransport/adapter.go` | batched fetch / progress ack RPC service 路由 |
| `pkg/replication/isrnodetransport/session.go` | per-peer 请求聚合、flush、响应 fanout |
| `pkg/storage/channellog/commit_coordinator.go` | node-level durable commit coordinator |
| `pkg/storage/channellog/commit_batch.go` | shared Pebble batch 构建与结果 fanout |
| `pkg/storage/channellog/db.go` | coordinator 生命周期与 DB 级共享入口 |
| `pkg/storage/channellog/store.go` | store 级 coordinator 访问、publish 边界 |
| `pkg/storage/channellog/log_store.go` | leader / follower durable 提交改走 shared batch |
| `pkg/storage/channellog/checkpoint_store.go` | checkpoint mutation 改成 coordinator 可合并的写入单元 |
| `pkg/storage/channellog/state_store.go` | idempotency mutation 改成 coordinator 可合并的写入单元 |
| `pkg/storage/channellog/apply.go` | committed 元数据直出、避免 reread、桥接 coordinator |
| `pkg/storage/channellog/isr_bridge.go` | ISR log/checkpoint bridge 与 coordinator 对接 |
| `pkg/storage/channellog/send.go` | 成功路径减少 post-commit reread / 重复写入 |

## Task 1: 给 `send_stress` 增加 throughput mode 配置和 inflight 跟踪器

**Files:**
- Modify: `internal/app/send_stress_test.go`
- Test: `internal/app/send_stress_test.go`

- [ ] **Step 1: 写失败测试，锁定 throughput mode 配置解析和 inflight 约束**

在 `internal/app/send_stress_test.go` 增加 focused 测试，至少覆盖：

```go
func TestSendStressConfigDefaultsToLatencyMode(t *testing.T)
func TestSendStressConfigParsesThroughputModeAndInflightOverride(t *testing.T)
func TestValidateSendStressConfigRejectsInvalidThroughputInflight(t *testing.T)
```

要求：

- 默认仍是 `latency` mode
- `throughput` mode 必须要求 `MaxInflightPerWorker > 0`
- `latency` mode 下 `MaxInflightPerWorker` 自动视为 `1`

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./internal/app -run 'TestSendStressConfig(DefaultsToLatencyMode|ParsesThroughputModeAndInflightOverride)|TestValidateSendStressConfigRejectsInvalidThroughputInflight' -count=1
```

Expected: FAIL，因为当前配置还没有 `Mode` / `MaxInflightPerWorker`。

- [ ] **Step 3: 写最小实现**

在 `internal/app/send_stress_test.go` 中新增：

```go
type sendStressMode string

const (
	sendStressModeLatency    sendStressMode = "latency"
	sendStressModeThroughput sendStressMode = "throughput"
)

type sendStressConfig struct {
	Mode                 sendStressMode
	MaxInflightPerWorker int
	// keep existing fields...
}
```

同时补充：

- `WK_SEND_STRESS_MODE`
- `WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER`
- `parseSendStressMode`
- throughput mode 的配置校验

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./internal/app -run 'TestSendStressConfig(DefaultsToLatencyMode|ParsesThroughputModeAndInflightOverride)|TestValidateSendStressConfigRejectsInvalidThroughputInflight' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/app/send_stress_test.go
git commit -m "test(app): add send stress throughput mode config"
```

## Task 2: 实现 throughput mode 的多 inflight worker 驱动

**Files:**
- Modify: `internal/app/send_stress_test.go`
- Test: `internal/app/send_stress_test.go`

- [ ] **Step 1: 写失败测试，锁定单连接多 inflight 的 ack 归并语义**

在 `internal/app/send_stress_test.go` 增加 focused 测试，抽出一个小型 inflight tracker / scripted connection helper，覆盖：

```go
func TestSendStressThroughputTrackerCompletesOutOfOrderAcks(t *testing.T)
func TestRunSendStressWorkersThroughputModeCapsInflightPerWorker(t *testing.T)
```

要求：

- out-of-order ack 仍能按 `ClientSeq` / `ClientMsgNo` 匹配到原消息
- 单个 worker 同一时刻的未完成请求数不超过 `MaxInflightPerWorker`
- `latency` mode 继续保持“一发一等 ack”

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./internal/app -run 'TestSendStressThroughputTrackerCompletesOutOfOrderAcks|TestRunSendStressWorkersThroughputModeCapsInflightPerWorker' -count=1
```

Expected: FAIL，因为当前 `runSendStressWorkers()` 仍是串行一发一等。

- [ ] **Step 3: 写最小实现**

在 `internal/app/send_stress_test.go` 中把 worker 执行拆成两条路径：

- `runSendStressWorkersLatency(...)`
- `runSendStressWorkersThroughput(...)`

throughput 路径要求：

- 每个 worker 维持一个 writer loop
- 独立 ack reader 持续消费 ack
- 用 `MaxInflightPerWorker` 限流
- 每条消息仍然生成独立 `sendStressRecord`
- 压测结束后继续复用现有 `verifySendStressCommittedRecords(...)`

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./internal/app -run 'TestSendStressThroughputTrackerCompletesOutOfOrderAcks|TestRunSendStressWorkersThroughputModeCapsInflightPerWorker' -count=1
```

Expected: PASS

- [ ] **Step 5: 跑 smoke 验证并提交**

Run:

```bash
WK_SEND_STRESS=1 \
WK_SEND_STRESS_MODE=throughput \
WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER=4 \
WK_SEND_STRESS_WORKERS=4 \
WK_SEND_STRESS_SENDERS=8 \
WK_SEND_STRESS_MESSAGES_PER_WORKER=20 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v
```

Expected: PASS，日志里出现 throughput mode 的 metrics 输出。

```bash
git add internal/app/send_stress_test.go
git commit -m "test(app): add send stress throughput runner"
```

## Task 3: 在 `pkg/replication/isr` 增加 progress ack 核心能力

**Files:**
- Create: `pkg/replication/isr/progress_ack.go`
- Modify: `pkg/replication/isr/types.go`
- Modify: `pkg/replication/isr/progress.go`
- Test: `pkg/replication/isr/progress_ack_test.go`

- [ ] **Step 1: 写失败测试，锁定 progress ack 的单调推进和即时提交语义**

新增 `pkg/replication/isr/progress_ack_test.go`，覆盖：

```go
func TestApplyProgressAckAdvancesHWWithoutFollowUpFetch(t *testing.T)
func TestApplyProgressAckIgnoresStaleEpoch(t *testing.T)
func TestApplyProgressAckPreservesMonotonicReplicaProgress(t *testing.T)
```

要求：

- follower durable apply 后只靠 progress ack 就能推进 leader HW
- stale epoch / stale match offset 不能推进 progress
- 现有 fetch 驱动的 progress 路径继续可用

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/replication/isr -run 'TestApplyProgressAck(AdvancesHWWithoutFollowUpFetch|IgnoresStaleEpoch|PreservesMonotonicReplicaProgress)$' -count=1
```

Expected: FAIL，因为当前 replica 还没有 progress ack API。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isr/types.go` 中新增：

```go
type ProgressAckRequest struct {
	GroupKey    GroupKey
	Epoch       uint64
	ReplicaID   NodeID
	MatchOffset uint64
}
```

并为 `Replica` 增加：

```go
ApplyProgressAck(ctx context.Context, req ProgressAckRequest) error
```

`progress_ack.go` 中实现：

- role / epoch / groupKey 校验
- `progress[ReplicaID]` 的单调推进
- 复用 `advanceHW()`
- 对旧 ack 幂等忽略

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/replication/isr -run 'TestApplyProgressAck(AdvancesHWWithoutFollowUpFetch|IgnoresStaleEpoch|PreservesMonotonicReplicaProgress)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isr/types.go pkg/replication/isr/progress.go pkg/replication/isr/progress_ack.go pkg/replication/isr/progress_ack_test.go
git commit -m "feat(isr): add progress ack path"
```

## Task 4: 在 `pkg/replication/isrnode` 中发送并接收 progress ack

**Files:**
- Modify: `pkg/replication/isrnode/types.go`
- Modify: `pkg/replication/isrnode/transport.go`
- Test: `pkg/replication/isrnode/session_test.go`
- Test: `pkg/replication/isrnode/limits_test.go`

- [ ] **Step 1: 写失败测试，锁定 fetch apply 后必须立即发送 progress ack**

在 `pkg/replication/isrnode/session_test.go` / `pkg/replication/isrnode/limits_test.go` 增加 focused 测试：

```go
func TestFetchResponseSendsProgressAckAfterApplyFetch(t *testing.T)
func TestProgressAckEnvelopeRequiresMatchingGeneration(t *testing.T)
func TestLeaderAppliesProgressAckWithoutWaitingForNextFetch(t *testing.T)
```

要求：

- follower 成功 `ApplyFetch` 后立刻发出 progress ack envelope
- generation 不匹配的 ack 必须被丢弃
- leader 收到 ack 后无需等下一轮 fetch 就能推进等待中的 append

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/replication/isrnode -run 'Test(FetchResponseSendsProgressAckAfterApplyFetch|ProgressAckEnvelopeRequiresMatchingGeneration|LeaderAppliesProgressAckWithoutWaitingForNextFetch)$' -count=1
```

Expected: FAIL，因为当前 runtime 只处理 fetch response。

- [ ] **Step 3: 写最小实现**

在 `pkg/replication/isrnode/types.go` 中新增：

```go
const (
	MessageKindProgressAck MessageKind = ...
)

type ProgressAckEnvelope struct {
	GroupKey    isr.GroupKey
	Epoch       uint64
	Generation  uint64
	ReplicaID   isr.NodeID
	MatchOffset uint64
}
```

在 `transport.go` 中：

- `applyFetchResponseEnvelope()` 成功后发送 progress ack
- `deliverEnvelope()` / `handleEnvelope()` 增加 progress ack 分支
- 调用 `replica.ApplyProgressAck(...)`

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/replication/isrnode -run 'Test(FetchResponseSendsProgressAckAfterApplyFetch|ProgressAckEnvelopeRequiresMatchingGeneration|LeaderAppliesProgressAckWithoutWaitingForNextFetch)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isrnode/types.go pkg/replication/isrnode/transport.go pkg/replication/isrnode/session_test.go pkg/replication/isrnode/limits_test.go
git commit -m "feat(isrnode): wire progress ack envelopes"
```

## Task 5: 在 `isrnodetransport` 中增加 progress ack RPC

**Files:**
- Modify: `pkg/replication/isrnodetransport/codec.go`
- Modify: `pkg/replication/isrnodetransport/adapter.go`
- Modify: `pkg/replication/isrnodetransport/session.go`
- Test: `pkg/replication/isrnodetransport/codec_test.go`
- Test: `pkg/replication/isrnodetransport/adapter_test.go`

- [ ] **Step 1: 写失败测试，锁定 progress ack 的编解码与 RPC 路由**

新增 focused 测试：

```go
func TestProgressAckCodecRoundTrip(t *testing.T)
func TestPeerSessionSendDispatchesProgressAckRPC(t *testing.T)
func TestAdapterHandleProgressAckInvokesRegisteredHandler(t *testing.T)
```

要求：

- progress ack 可以独立编解码
- peer session 发送 progress ack 时不会走 fetch codec
- adapter 收到 progress ack RPC 后会转成 inbound envelope

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/replication/isrnodetransport -run 'Test(ProgressAckCodecRoundTrip|PeerSessionSendDispatchesProgressAckRPC|AdapterHandleProgressAckInvokesRegisteredHandler)$' -count=1
```

Expected: FAIL，因为当前 adapter 只认识 `RPCServiceFetch`。

- [ ] **Step 3: 写最小实现**

在 `codec.go` 中增加：

```go
const RPCServiceProgressAck uint8 = ...

func encodeProgressAck(...)
func decodeProgressAck(...)
```

在 `adapter.go` 中：

- 注册 `RPCServiceProgressAck`
- 收到 RPC 后调用 `deliver(...)`

在 `session.go` 中：

- `Send(env)` 支持 `MessageKindProgressAck`
- 使用 `client.RPCService(..., RPCServiceProgressAck, ...)`
- 远端只返回空响应

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/replication/isrnodetransport -run 'Test(ProgressAckCodecRoundTrip|PeerSessionSendDispatchesProgressAckRPC|AdapterHandleProgressAckInvokesRegisteredHandler)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isrnodetransport/codec.go pkg/replication/isrnodetransport/adapter.go pkg/replication/isrnodetransport/session.go pkg/replication/isrnodetransport/codec_test.go pkg/replication/isrnodetransport/adapter_test.go
git commit -m "feat(isrnodetransport): add progress ack rpc"
```

## Task 6: 实现 per-peer multi-group batched fetch transport

**Files:**
- Modify: `pkg/replication/isrnode/batching.go`
- Modify: `pkg/replication/isrnode/types.go`
- Modify: `pkg/replication/isrnodetransport/codec.go`
- Modify: `pkg/replication/isrnodetransport/adapter.go`
- Modify: `pkg/replication/isrnodetransport/session.go`
- Test: `pkg/replication/isrnode/session_test.go`
- Test: `pkg/replication/isrnodetransport/codec_test.go`
- Test: `pkg/replication/isrnodetransport/adapter_test.go`

- [ ] **Step 1: 写失败测试，锁定 batched fetch 的聚合、flush 和逐 group fanout**

新增 focused 测试：

```go
func TestPeerSessionTryBatchQueuesMultipleFetchRequests(t *testing.T)
func TestPeerSessionFlushSendsSingleFetchBatchRPC(t *testing.T)
func TestBatchedFetchResponseFansOutResultsPerGroup(t *testing.T)
```

要求：

- 同一 peer 的多个 fetch request 能被同一个 session 收集
- flush 只发一个 batch RPC
- 响应可按 item 重新 fanout 为独立 fetch response envelope

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -run 'Test(PeerSessionTryBatchQueuesMultipleFetchRequests|PeerSessionFlushSendsSingleFetchBatchRPC|BatchedFetchResponseFansOutResultsPerGroup)$' -count=1
```

Expected: FAIL，因为当前 `TryBatch()` 恒为 `false`。

- [ ] **Step 3: 写最小实现**

在 `session.go` 中新增 per-peer batch 队列：

- `TryBatch()` 对 fetch request 返回 `true`
- session 自己负责启动极短的 flush 窗口
- `Flush()` 发送一个 batched fetch RPC

在 `codec.go` / `adapter.go` 中增加：

- `encodeFetchBatchRequest`
- `decodeFetchBatchRequest`
- `encodeFetchBatchResponse`
- `decodeFetchBatchResponse`
- `RPCServiceFetchBatch`

leader 端 batch handler 要求：

- 逐 item 调 `ServeFetch`
- 单 item 错误单独回传，不污染其它 group

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -run 'Test(PeerSessionTryBatchQueuesMultipleFetchRequests|PeerSessionFlushSendsSingleFetchBatchRPC|BatchedFetchResponseFansOutResultsPerGroup)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/isrnode/batching.go pkg/replication/isrnode/types.go pkg/replication/isrnode/session_test.go pkg/replication/isrnodetransport/codec.go pkg/replication/isrnodetransport/adapter.go pkg/replication/isrnodetransport/session.go pkg/replication/isrnodetransport/codec_test.go pkg/replication/isrnodetransport/adapter_test.go
git commit -m "feat(isrnodetransport): batch fetch requests per peer"
```

## Task 7: 在 `channellog` 中引入 node-level durable commit coordinator

**Files:**
- Create: `pkg/storage/channellog/commit_coordinator.go`
- Create: `pkg/storage/channellog/commit_batch.go`
- Modify: `pkg/storage/channellog/db.go`
- Modify: `pkg/storage/channellog/store.go`
- Test: `pkg/storage/channellog/commit_coordinator_test.go`

- [ ] **Step 1: 写失败测试，锁定跨 group shared sync 的边界**

新增 `pkg/storage/channellog/commit_coordinator_test.go`，覆盖：

```go
func TestCommitCoordinatorBatchesMultipleGroupsIntoSinglePebbleSync(t *testing.T)
func TestCommitCoordinatorDoesNotPublishBeforeSyncCompletes(t *testing.T)
func TestCommitCoordinatorFanoutsBatchFailureToAllWaiters(t *testing.T)
```

要求：

- 两个 group 的 durable request 可以共享一次 Pebble sync
- sync 未完成前任何 group 的 publish callback 都不能被调用
- sync 失败时所有等待请求都收到失败

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestCommitCoordinator(BatchesMultipleGroupsIntoSinglePebbleSync|DoesNotPublishBeforeSyncCompletes|FanoutsBatchFailureToAllWaiters)$' -count=1
```

Expected: FAIL，因为当前还没有 coordinator。

- [ ] **Step 3: 写最小实现**

在 `db.go` / `store.go` 上挂一个 DB 级 coordinator，核心接口可类似：

```go
type commitRequest struct {
	groupKey  isr.GroupKey
	build     func(*pebble.Batch) error
	publish   func() error
	done      chan error
}
```

实现要求：

- DB 级队列聚合多个 group 请求
- 一个 batch 内统一 `Commit(pebble.Sync)`
- commit 成功后再逐请求 `publish`

- [ ] **Step 4: 重跑 focused 测试，确认通过**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestCommitCoordinator(BatchesMultipleGroupsIntoSinglePebbleSync|DoesNotPublishBeforeSyncCompletes|FanoutsBatchFailureToAllWaiters)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/commit_coordinator.go pkg/storage/channellog/commit_batch.go pkg/storage/channellog/db.go pkg/storage/channellog/store.go pkg/storage/channellog/commit_coordinator_test.go
git commit -m "feat(channellog): add durable commit coordinator"
```

## Task 8: 把 leader committed 和 follower apply durable 路径接入 coordinator

**Files:**
- Modify: `pkg/storage/channellog/log_store.go`
- Modify: `pkg/storage/channellog/checkpoint_store.go`
- Modify: `pkg/storage/channellog/state_store.go`
- Modify: `pkg/storage/channellog/apply.go`
- Modify: `pkg/storage/channellog/isr_bridge.go`
- Test: `pkg/storage/channellog/apply_test.go`
- Test: `pkg/storage/channellog/log_store_blocking_test.go`
- Test: `pkg/storage/channellog/storage_integration_test.go`

- [ ] **Step 1: 写失败测试，锁定 coordinator 接入后的原子可见性**

增加 focused 测试：

```go
func TestCheckpointBridgeUsesCoordinatorForCommittedState(t *testing.T)
func TestStoreApplyFetchPublishesLEOOnlyAfterCoordinatorCommit(t *testing.T)
func TestISRBridgeLEODoesNotBlockWhileCoordinatorSyncIsInFlight(t *testing.T)
```

要求：

- leader committed checkpoint + idempotency 更新通过 coordinator 原子发布
- follower apply 的 `LEO/HW` 只在 shared sync 成功后可见
- `LEO()` 读取在 sync 阻塞时仍不被长时间卡死

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/storage/channellog -run 'Test(CheckpointBridgeUsesCoordinatorForCommittedState|StoreApplyFetchPublishesLEOOnlyAfterCoordinatorCommit|ISRBridgeLEODoesNotBlockWhileCoordinatorSyncIsInFlight)$' -count=1
```

Expected: FAIL，因为当前 leader/follower durable 路径各自单独 sync。

- [ ] **Step 3: 写最小实现**

接线方式：

- `checkpointBridge.Store(...)` 把 committed idempotency mutation + checkpoint mutation 提交给 coordinator
- `StoreApplyFetch(...)` / `applyFetchedRecords(...)` 把 fetched records、idempotency mutation、checkpoint mutation 放进同一个 shared batch
- `state_store.go` / `checkpoint_store.go` 暴露 no-sync 的 batch writer helper，供 coordinator 复用

- [ ] **Step 4: 重跑 focused 测试与现有存储集成测试**

Run:

```bash
go test ./pkg/storage/channellog -run 'Test(CheckpointBridgeUsesCoordinatorForCommittedState|StoreApplyFetchPublishesLEOOnlyAfterCoordinatorCommit|ISRBridgeLEODoesNotBlockWhileCoordinatorSyncIsInFlight|ClusterAppend.*|StoreStateFactory.*)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/log_store.go pkg/storage/channellog/checkpoint_store.go pkg/storage/channellog/state_store.go pkg/storage/channellog/apply.go pkg/storage/channellog/isr_bridge.go pkg/storage/channellog/apply_test.go pkg/storage/channellog/log_store_blocking_test.go pkg/storage/channellog/storage_integration_test.go
git commit -m "feat(channellog): route durable apply and checkpoint through coordinator"
```

## Task 9: 清理 `send.go` 成功路径的 reread / 重复 idempotency 写入

**Files:**
- Modify: `pkg/storage/channellog/send.go`
- Test: `pkg/storage/channellog/send_test.go`
- Test: `pkg/storage/channellog/storage_integration_test.go`

- [ ] **Step 1: 写失败测试，锁定 post-commit 成功路径不再重复写状态**

增加 focused 测试：

```go
func TestAppendReturnsCommittedResultWithoutPostCommitIdempotencyRewrite(t *testing.T)
func TestAppendDuplicateStillReturnsStoredMessageAfterCoordinatorCommit(t *testing.T)
```

要求：

- 正常成功路径不再走第二次 `PutIdempotency`
- duplicate 路径仍然能返回已提交消息
- payload 冲突时仍然返回 `ErrIdempotencyConflict`

- [ ] **Step 2: 跑 focused 测试，确认当前实现失败**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestAppend(ReturnsCommittedResultWithoutPostCommitIdempotencyRewrite|DuplicateStillReturnsStoredMessageAfterCoordinatorCommit|ReturnsErrIdempotencyConflictWhenPayloadChanges)$' -count=1
```

Expected: FAIL，因为当前 `send.go` 成功路径仍有 post-commit `GetIdempotency` / `PutIdempotency`。

- [ ] **Step 3: 写最小实现**

在 `pkg/storage/channellog/send.go` 中：

- 保留 pre-append duplicate fast path
- 普通成功路径直接信任 coordinator 已完成 durable state publish
- 仅在 truly duplicate / stale fallback 时回退到 reread

- [ ] **Step 4: 重跑 focused 测试与现有 send 测试**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestAppend(ReturnsCommittedResultWithoutPostCommitIdempotencyRewrite|DuplicateStillReturnsStoredMessageAfterCoordinatorCommit|ReturnsErrIdempotencyConflictWhenPayloadChanges|ReturnsStoredResultForCommittedDuplicate)$' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/send.go pkg/storage/channellog/send_test.go pkg/storage/channellog/storage_integration_test.go
git commit -m "perf(channellog): remove redundant post-commit rereads"
```

## Task 10: 跑端到端回归和 3000 QPS 验收

**Files:**
- Verify: `internal/app/send_stress_test.go`
- Verify: `pkg/replication/isr/...`
- Verify: `pkg/replication/isrnode/...`
- Verify: `pkg/replication/isrnodetransport/...`
- Verify: `pkg/storage/channellog/...`

- [ ] **Step 1: 跑关键包测试，确保语义回归通过**

Run:

```bash
go test ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/replication/isrnodetransport ./pkg/storage/channellog ./internal/app -count=1
```

Expected: PASS

- [ ] **Step 2: 跑真实三节点 throughput 压测，沿用现有 `send_stress` 形状**

Run:

```bash
WK_SEND_STRESS=1 \
WK_SEND_STRESS_MODE=throughput \
WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER=8 \
WK_SEND_STRESS_WORKERS=32 \
WK_SEND_STRESS_SENDERS=128 \
WK_SEND_STRESS_MESSAGES_PER_WORKER=200 \
WK_SEND_STRESS_ACK_TIMEOUT=10s \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v
```

Expected: PASS，并且日志里的 `qps` 达到 `>= 3000`。

- [ ] **Step 3: 复跑存储基准，确认 durable batching 的方向正确**

Run:

```bash
go test ./pkg/storage/channellog -run '^$' -bench '^BenchmarkClusterAppend$' -benchmem -count=1
```

Expected: `ns/op` 明显低于当前约 `13.4ms/op` 基线。

- [ ] **Step 4: 如吞吐未达标，补 fresh profile 再定位**

Run:

```bash
WK_SEND_STRESS=1 \
WK_SEND_STRESS_MODE=throughput \
WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER=8 \
WK_SEND_STRESS_WORKERS=32 \
WK_SEND_STRESS_SENDERS=128 \
WK_SEND_STRESS_MESSAGES_PER_WORKER=200 \
go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -cpuprofile /tmp/send_stress.cpu.out -blockprofile /tmp/send_stress.block.out -mutexprofile /tmp/send_stress.mutex.out -v
```

Expected: 生成 fresh profile，热点不再集中在“下一轮 fetch 才能提交”和“每 group 单独 sync”。

- [ ] **Step 5: 执行最终校验并提交收尾**

执行 `@superpowers/verification-before-completion`，然后：

```bash
git add internal/app/send_stress_test.go pkg/replication/isr pkg/replication/isrnode pkg/replication/isrnodetransport pkg/storage/channellog
git commit -m "perf: lift durable send throughput toward 3000 qps"
```
