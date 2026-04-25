# Send Stress Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `WuKongIM v3.1` 增加一条 env-gated 的三节点真实发送链路压测，覆盖 `WKProto/TCP -> Gateway -> durable commit -> SendAck`，并在压测结束后对全部成功发送做持久化提交校验。

**Architecture:** 实现集中落在 `internal/app/send_stress_test.go`，复用现有 `threeNodeAppHarness`、WKProto 编解码 helper 和三节点 channel meta 刷新逻辑，不再引入第二套压力测试基座。测试先用纯 helper/config 测试锁定 env 配置与统计逻辑，再落真实三节点压力场景；若压力测试暴露生产缺陷，先在缺陷所属包补最小回归测试，再修生产代码，最后回跑真实压力测试。

**Tech Stack:** Go 1.23、`testing`、`testify`、`net`、`internal/app` 三节点 harness、`internal/usecase/message`、`pkg/protocol/wkframe`、`pkg/storage/channellog`、`pkg/storage/metadb`。

**Spec:** `docs/superpowers/specs/2026-04-08-send-stress-design.md`

---

## 执行约束

- 每个任务都使用 `@superpowers/test-driven-development`：先写失败测试，再写最小实现，再跑测试确认通过。
- 新测试默认必须跳过；只有 `WK_SEND_STRESS=1` 时才运行真实长压。
- 发送链路只覆盖 `person` 单聊，不把 `RecvPacket`/`RecvAck` 纳入成功条件。
- 每条成功发送都必须同时验证 `SendAck` 成功和最终 durable 可读；禁止只看其中一边。
- 如果压力测试暴露的是真实程序 bug，先在缺陷所属包补 focused failing regression，再修生产代码；不要降低压力测试标准。
- 最终交付前运行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `internal/app/send_stress_test.go` | 新增发送压力测试入口、env 配置、统计、预热、发送 worker、SendAck 校验、持久化回读校验 |
| `internal/app/multinode_integration_test.go` | 复用现有 `threeNodeAppHarness`、`readAppWKProtoFrameWithin`、`waitForAppCommittedMessage` 等三节点真实 helper；仅在 send stress 实现确实缺少必要 helper 时才补最小抽取 |
| `internal/app/integration_test.go` | 复用 `sendAppWKProtoFrame` 编码写帧 helper；除非测试实现缺 helper，否则不改 |
| `internal/usecase/message/send_test.go` | 如果 stress red phase 暴露 send usecase 缺陷，在这里补最小回归测试 |
| `internal/usecase/message/send.go` | 仅当 stress 暴露 durable send 缺陷时修生产代码 |
| `internal/access/gateway/handler_test.go` | 如果 stress 暴露 gateway send/ack 协议缺陷，在这里补最小回归测试 |
| `internal/access/gateway/handler.go` | 仅当 stress 暴露 gateway 缺陷时修生产代码 |

## Task 1: 落发送压力测试的配置、统计与纯 helper 骨架

**Files:**
- Create: `internal/app/send_stress_test.go`

- [ ] **Step 1: 先写失败测试，锁定配置与统计语义**

在 `internal/app/send_stress_test.go` 先增加以下测试：

- `TestSendStressConfigDefaultsAndOverrides`
- `TestSendStressLatencySummaryPercentiles`
- `TestSendStressOutcomeErrorRate`

测试骨架：

```go
func TestSendStressConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WK_SEND_STRESS", "1")
	t.Setenv("WK_SEND_STRESS_DURATION", "1500ms")
	t.Setenv("WK_SEND_STRESS_WORKERS", "7")
	t.Setenv("WK_SEND_STRESS_SENDERS", "11")
	t.Setenv("WK_SEND_STRESS_MESSAGES_PER_WORKER", "13")
	t.Setenv("WK_SEND_STRESS_DIAL_TIMEOUT", "2s")
	t.Setenv("WK_SEND_STRESS_ACK_TIMEOUT", "1800ms")
	t.Setenv("WK_SEND_STRESS_SEED", "42")

	cfg := loadSendStressConfig(t)
	require.True(t, cfg.Enabled)
	require.Equal(t, 1500*time.Millisecond, cfg.Duration)
	require.Equal(t, 7, cfg.Workers)
	require.Equal(t, 11, cfg.Senders)
	require.Equal(t, 13, cfg.MessagesPerWorker)
	require.Equal(t, 2*time.Second, cfg.DialTimeout)
	require.Equal(t, 1800*time.Millisecond, cfg.AckTimeout)
	require.EqualValues(t, 42, cfg.Seed)
}
```

```go
func TestSendStressLatencySummaryPercentiles(t *testing.T) {
	summary := summarizeSendStressLatencies([]time.Duration{
		90 * time.Millisecond,
		10 * time.Millisecond,
		70 * time.Millisecond,
		30 * time.Millisecond,
		50 * time.Millisecond,
	})

	require.Equal(t, 5, summary.Count)
	require.Equal(t, 50*time.Millisecond, summary.P50)
	require.Equal(t, 90*time.Millisecond, summary.P95)
	require.Equal(t, 90*time.Millisecond, summary.P99)
	require.Equal(t, 90*time.Millisecond, summary.Max)
}
```

```go
func TestSendStressOutcomeErrorRate(t *testing.T) {
	outcome := sendStressOutcome{Total: 10, Success: 8, Failed: 2}
	require.InDelta(t, 20.0, outcome.ErrorRate(), 0.001)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run:

```bash
go test ./internal/app -run 'TestSendStress(ConfigDefaultsAndOverrides|LatencySummaryPercentiles|OutcomeErrorRate)' -count=1
```

Expected: FAIL，因为 `send_stress_test.go` 里的配置、统计类型和 helper 还不存在。

- [ ] **Step 3: 写最小实现**

在 `internal/app/send_stress_test.go` 中增加最小骨架：

```go
type sendStressConfig struct {
	Enabled           bool
	Duration          time.Duration
	Workers           int
	Senders           int
	MessagesPerWorker int
	DialTimeout       time.Duration
	AckTimeout        time.Duration
	Seed              int64
}

type sendStressLatencySummary struct {
	Count int
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

type sendStressOutcome struct {
	Total   uint64
	Success uint64
	Failed  uint64
}
```

实现：

```go
func (o sendStressOutcome) ErrorRate() float64
func loadSendStressConfig(t *testing.T) sendStressConfig
func requireSendStressEnabled(t *testing.T, cfg sendStressConfig)
func summarizeSendStressLatencies(latencies []time.Duration) sendStressLatencySummary
```

要求：

- 默认 `Enabled=false`
- `Duration` 默认 `5s`
- `Workers` 默认 `max(4, runtime.GOMAXPROCS(0))`
- `Senders` 默认 `max(8, Workers)`
- `MessagesPerWorker` 默认 `50`
- `DialTimeout` 默认 `3s`
- `AckTimeout` 默认 `5s`
- 约束 `Senders >= Workers`；若显式配置不满足，`loadSendStressConfig` 直接 `t.Fatalf`
- worker 到 sender 的映射固定为 `worker i -> sender i`，一个 worker 只持有一个 sender 和一条长连接，不做 sender 复用或连接共享
- 如果 `Senders > Workers`，多出的 sender 只作为预热数据集保留，不参与本次活跃 worker 发流
- 非法参数直接 `t.Fatalf`

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run:

```bash
go test ./internal/app -run 'TestSendStress(ConfigDefaultsAndOverrides|LatencySummaryPercentiles|OutcomeErrorRate)' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/app/send_stress_test.go
git commit -m "test(app): scaffold send stress config"
```

## Task 2: 落真实三节点发送压力测试与全量 durable 校验

**Files:**
- Modify: `internal/app/send_stress_test.go`
- Modify: `internal/app/multinode_integration_test.go` (only if a missing reusable helper blocks the stress test)

- [ ] **Step 1: 先写失败测试，锁定真实发送压力闭环**

在 `internal/app/send_stress_test.go` 增加：

- `TestSendStressThreeNode`

并在同文件先定义测试所需记录和 helper 骨架：

```go
type sendStressTarget struct {
	SenderUID     string
	RecipientUID  string
	ChannelID     string
	ChannelType   uint8
	OwnerNodeID   uint64
	ConnectNodeID uint64
}

type sendStressRecord struct {
	Worker       int
	SenderUID    string
	RecipientUID string
	ChannelID    string
	ChannelType  uint8
	ClientSeq    uint64
	ClientMsgNo  string
	Payload      []byte
	MessageID    int64
	MessageSeq   uint64
	AckLatency   time.Duration
	OwnerNodeID  uint64
	ConnectNodeID uint64
}
```

主测试骨架：

```go
func TestSendStressThreeNode(t *testing.T) {
	cfg := loadSendStressConfig(t)
	requireSendStressEnabled(t, cfg)

	harness := newThreeNodeAppHarness(t)
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	targets := preloadSendStressChannels(t, harness, leader, cfg)
	outcome, records, failures := runSendStressWorkers(t, harness, targets, cfg)
	verifySendStressCommittedRecords(t, harness, records)

	t.Logf("send stress results: total=%d success=%d failed=%d error_rate=%.2f%%", outcome.Total, outcome.Success, outcome.Failed, outcome.ErrorRate())
	if len(failures) > 0 {
		t.Logf("send stress failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}
```

需要的最小 helper：

```go
func preloadSendStressChannels(t *testing.T, harness *threeNodeAppHarness, leader *App, cfg sendStressConfig) []sendStressTarget
func runSendStressWorkers(t *testing.T, harness *threeNodeAppHarness, targets []sendStressTarget, cfg sendStressConfig) (sendStressOutcome, []sendStressRecord, []string)
func verifySendStressCommittedRecords(t *testing.T, harness *threeNodeAppHarness, records []sendStressRecord)
func runSendStressClient(t *testing.T, app *App, senderUID string, cfg sendStressConfig) net.Conn
```

- [ ] **Step 2: 用显式 env 跑真实压力测试，确认当前实现失败**

Run:

```bash
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=2s WK_SEND_STRESS_WORKERS=3 WK_SEND_STRESS_SENDERS=6 WK_SEND_STRESS_MESSAGES_PER_WORKER=8 go test ./internal/app -run 'TestSendStressThreeNode' -count=1 -v -timeout 10m
```

Expected: FAIL，因为真实发送压力测试、meta 预热、worker 并发发送和 durable 校验逻辑都还不存在。

- [ ] **Step 3: 写最小实现**

实现要求：

- `preloadSendStressChannels(...)`
  - 生成 `stress-sender-%03d` / `stress-recipient-%03d`
  - 为每对 sender/recipient 写入 `metadb.ChannelRuntimeMeta`
  - `ChannelRuntimeMeta` 必须完整镜像现有 `internal/app/multinode_integration_test.go` 的 person send seed 形状，至少包含：
    - `ChannelEpoch`
    - `LeaderEpoch`
    - `Replicas`
    - `ISR`
    - `Leader`
    - `MinISR`
    - `Status`
    - `Features`
    - `LeaseUntilMS`
  - `Replicas` / `ISR` 固定 `[1,2,3]`
  - `Leader` 使用当前稳定 leader
  - `MinISR` 固定 `3`
  - `Status` 固定 `channellog.ChannelStatusActive`
  - `Features` 固定 `channellog.MessageSeqFormatLegacyU32`
  - `LeaseUntilMS` 使用 `time.Now().Add(time.Minute).UnixMilli()`
  - 返回完整 `sendStressTarget`，显式携带：
    - `OwnerNodeID`: 该单聊 channel 的 durable owner
    - `ConnectNodeID`: 该 sender 实际连入的 gateway 节点
  - 对 `harness.appsWithLeaderFirst(leaderID)` 全量 `RefreshChannelMeta`
- `runSendStressClient(...)`
  - 真实 `net.Dial` 到 `tcp-wkproto`
  - 发 `ConnectPacket`
  - 等待成功 `Connack`
- `runSendStressWorkers(...)`
  - 每个 worker 绑定一个 sender/client connection
  - 每个 worker 的发送循环语义固定为：
    - 最多发送 `MessagesPerWorker`
    - 若 `Duration` 先到，则提前停止并记录已完成的部分
    - 总目标发送量上限是 `Workers * MessagesPerWorker`
  - 每条消息发送一个真实 `wkframe.SendPacket`
  - 每发一条立即读取一个 `SendackPacket`
  - 校验 `ReasonSuccess`、`MessageID != 0`、`MessageSeq != 0`
  - `ClientMsgNo` 必须使用确定性唯一格式，例如 `send-stress-%02d-%s-%03d-%d`，至少包含 `worker`、`senderUID`、`iteration`、`seed`
  - 记录 `ClientMsgNo/Payload/MessageSeq/MessageID/AckLatency`
  - 在 `sendStressRecord` 中同时写入 `OwnerNodeID` 和 `ConnectNodeID`
  - 统计 `Total/Success/Failed`
  - 收集有限数量失败样本
- `verifySendStressCommittedRecords(...)`
  - 遍历全部成功记录
  - 使用 `record.OwnerNodeID` 先定位 owner 节点，再用 `waitForAppCommittedMessage(...)` 读回
  - 核对 `Payload`、`FromUID`、`ClientMsgNo`、`ChannelID`、`ChannelType`
  - 再在 `harness.orderedApps()` 上确认同一 `MessageSeq` 最终可读
- 测试日志输出至少包含：
  - `duration`
  - `workers`
  - `senders`
  - `total`
  - `success`
  - `failed`
  - `qps`
  - `p50/p95/p99/max`
  - `verification_count`
  - `verification_failures`

- [ ] **Step 4: 重新跑 focused 测试，确认默认关闭和显式开启都符合预期**

Run:

```bash
go test ./internal/app -run 'TestSendStressThreeNode' -count=1
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=2s WK_SEND_STRESS_WORKERS=3 WK_SEND_STRESS_SENDERS=6 WK_SEND_STRESS_MESSAGES_PER_WORKER=8 go test ./internal/app -run 'TestSendStressThreeNode' -count=1 -v -timeout 10m
```

Expected:

- 第一条 PASS 且输出 `set WK_SEND_STRESS=1 to enable send stress test`
- 第二条 PASS，日志中 `failed=0`，并完成全量 durable 校验

- [ ] **Step 5: 如果 Task 2 暴露生产缺陷，先补 focused regression 再修生产代码**

执行规则：

- 若失败点在 `message` durable send：
  - 先在 `internal/usecase/message/send_test.go` 写 focused failing test
  - 再改 `internal/usecase/message/send.go`
- 若失败点在 gateway send/ack：
  - 先在 `internal/access/gateway/handler_test.go` 写 focused failing test
  - 再改 `internal/access/gateway/handler.go`
- 若失败点落在其他所有权包，例如 `pkg/storage/channellog`、`internal/app/channelmeta.go` 或其他发送链路依赖：
  - 先在该缺陷所属包写最小 focused failing regression
  - 再改对应生产文件
- 修复后必须先跑该 focused regression；如果缺陷不在 `message`/`gateway`，把下面第一条替换成对应 owning package 的 focused `go test` 命令。随后再回跑：

```bash
go test ./internal/usecase/message ./internal/access/gateway -run 'Test.*Send.*' -count=1
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=2s WK_SEND_STRESS_WORKERS=3 WK_SEND_STRESS_SENDERS=6 WK_SEND_STRESS_MESSAGES_PER_WORKER=8 go test ./internal/app -run 'TestSendStressThreeNode' -count=1 -v -timeout 10m
```

Expected: PASS；不得通过改弱压力测试断言来“修复”失败。

- [ ] **Step 6: 提交**

如果没有生产修复：

```bash
git add internal/app/send_stress_test.go internal/app/multinode_integration_test.go
git commit -m "test(app): add real send stress coverage"
```

如果包含生产修复：

```bash
git add internal/app/send_stress_test.go internal/app/multinode_integration_test.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/access/gateway/handler.go internal/access/gateway/handler_test.go
git commit -m "test(app): add real send stress coverage"
```

## Task 3: 文档化新的发送压力测试入口

**Files:**
- Modify: `docs/distributed-cluster-test-matrix.md`

- [ ] **Step 1: 先更新矩阵文档，补充新的 env-gated 发送长压项**

在“压力与稳定性”区域增加：

- `internal/app` 发送压力测试
- 推荐命令
- 关键 env 变量
- 适用场景：手动验证 / nightly / 发布前 soak

建议文案：

```md
- app 真实发送链路压测：
  `WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=10s WK_SEND_STRESS_WORKERS=8 WK_SEND_STRESS_SENDERS=16 WK_SEND_STRESS_MESSAGES_PER_WORKER=100 go test ./internal/app -run 'TestSendStressThreeNode' -count=1 -v -timeout 15m`
```

- [ ] **Step 2: 运行最小回归，确保文档中的命令仍然可执行**

Run:

```bash
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=1s WK_SEND_STRESS_WORKERS=2 WK_SEND_STRESS_SENDERS=4 WK_SEND_STRESS_MESSAGES_PER_WORKER=4 go test ./internal/app -run 'TestSendStressThreeNode' -count=1 -timeout 10m
```

Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add docs/distributed-cluster-test-matrix.md
git commit -m "docs: document send stress test entrypoint"
```
