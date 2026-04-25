# Distributed Cluster Testing Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `WuKongIM v3.1` 建立分布式节点选举、日志复制、容灾恢复、转发路径和长稳压测的分层测试体系，并明确 `PR / nightly / 发布前 soak` 的执行矩阵。

**Architecture:** 测试按 `multiraft / isr / channellog / raftcluster / internal/app` 五层分开落地。`PR` 只跑确定性故障场景，`nightly` 跑环境变量门控的压力测试，`发布前 soak` 跑 `-count` 重复和长时间 leader churn，避免把时序波动直接塞进每次提交回归。

**Tech Stack:** Go 1.23、`testing`、`testify`、`pkg/replication/multiraft`、`pkg/replication/isrnode`、`pkg/storage/channellog`、`pkg/cluster/raftcluster`、`internal/app`、现有 stress env vars。

---

## 执行约束

- 所有新增场景都优先复用现有 harness，不新造第二套测试框架。
- 术语和命名遵守仓库约束，统一使用“单节点集群”或“三节点集群”，不引入绕过集群语义的单机分支。
- `PR` 阶段不跑长稳压测；压力测试统一保留为 env-gated。
- 对“客户端成功 ack 后是否 durable”这类高价值语义，必须同时验证返回值和持久化读回，不能只看一个断言。
- 对负向场景，优先断言“不可 committed / 不可读”，不要误把本地未提交日志写入当成正确提交。

## 文件映射

| 路径 | 责任 |
|------|------|
| `pkg/replication/multiraft/proposal_test.go` | stale leader、分区恢复、旧提案不应 apply |
| `pkg/replication/multiraft/e2e_test.go` | 复用 async cluster harness、网络分区与恢复 helper |
| `pkg/replication/isr/append_test.go` | `MinISR`、lease fencing、快失败规则 |
| `pkg/storage/channellog/multinode_integration_test.go` | 三节点 channel log 提交、follower 停机恢复、`MinISR` 阻塞提交 |
| `pkg/cluster/raftcluster/cluster_test.go` | 三节点真实 stop/restart helper、重选主和恢复 |
| `pkg/cluster/raftcluster/stress_test.go` | mixed workload、forwarding contention、leader restart stress |
| `internal/app/multinode_integration_test.go` | gateway/API 到 durable commit 的端到端容灾验证 |
| `docs/superpowers/plans/2026-04-08-distributed-cluster-testing.md` | 固化实施顺序、执行矩阵与通过标准 |

## Task 1: 加固多节点测试 harness，支持真实 stop/restart

**Files:**
- Modify: `pkg/storage/channellog/multinode_integration_test.go`
- Modify: `pkg/cluster/raftcluster/cluster_test.go`
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: 先写最小 harness 自测，锁定 restart 语义**

新增或补充以下测试骨架：

- `TestThreeNodeClusterHarnessRestartNodeReopensData`
- `TestThreeNodeAppHarnessRestartNodePreservesDataDir`
- `TestTestNodeRestartReopensClusterWithSameListenAddr`

测试要求：

- `stopNode()` 后旧对象不可再被使用。
- `restartNode()` 必须基于同一 `DataDir / raft dir / listen addr` 重建，而不是新临时目录。
- `orderedApps()` / `runningNodes()` 之类 helper 必须跳过 `nil` 节点。

- [ ] **Step 2: 跑聚焦测试，确认当前 helper 不满足需求**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestThreeNodeClusterHarnessRestartNodeReopensData' -count=1
go test ./pkg/cluster/raftcluster -run 'TestTestNodeRestartReopensClusterWithSameListenAddr' -count=1
go test ./internal/app -run 'TestThreeNodeAppHarnessRestartNodePreservesDataDir' -count=1
```

Expected: FAIL，因为当前 `channellog` restart 只 reopen DB，`raftcluster` `testNode` 没保存目录和配置，`app` harness 也没有 `stopNode/restartNode`。

- [ ] **Step 3: 写最小实现**

实现要求：

- `pkg/storage/channellog/multinode_integration_test.go`
  - `threeNodeChannelHarness` 增加 `addrs/specs`
  - 新增 `runningNodes()`、`stopNode()`、`restartNode()`
  - `close()` 额外清空 `runtime/cluster`
- `pkg/cluster/raftcluster/cluster_test.go`
  - `testNode` 增加 `dir/listenAddr/nodes/groups/groupCount`
  - 提取 `newStartedTestNode(...)`
  - 新增 `restartNode(t, nodes, idx)`
- `internal/app/multinode_integration_test.go`
  - `threeNodeAppHarness` 增加 `specs`
  - 新增 `runningApps()`、`stopNode()`、`restartNode()`、`waitForLeaderChange()`
  - `orderedApps()` 改成只返回存活节点

- [ ] **Step 4: 重新跑 helper 测试，确认通过**

Run:

```bash
go test ./pkg/storage/channellog -run 'TestThreeNodeClusterHarnessRestartNodeReopensData' -count=1
go test ./pkg/cluster/raftcluster -run 'TestTestNodeRestartReopensClusterWithSameListenAddr' -count=1
go test ./internal/app -run 'TestThreeNodeAppHarnessRestartNodePreservesDataDir' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/multinode_integration_test.go pkg/cluster/raftcluster/cluster_test.go internal/app/multinode_integration_test.go
git commit -m "test: add multinode restart helpers"
```

## Task 2: 补齐 deterministic 复制与恢复场景

**Files:**
- Modify: `pkg/replication/multiraft/proposal_test.go`
- Modify: `pkg/replication/isr/append_test.go`
- Modify: `pkg/storage/channellog/multinode_integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定高价值故障语义**

新增测试：

- `TestRemoteCommitDoesNotApplyStaleCommandAfterHeal`
- `TestAppendFailsFastWhenMetaISRBelowMinISR`
- `TestThreeNodeClusterBlocksCommitWhenMinISRUnavailable`
- `TestFollowerRestartCatchesUpAfterLeaderProgress`

关键断言：

- 旧 leader 的 `stale` future 不应成功完成，heal 后也不能被 apply 到 FSM。
- `ISR < MinISR` 的副本元数据必须快失败 `ErrInsufficientISR`。
- 三节点实际停掉 follower 时，`Append()` 预期黑盒表现是 `context.DeadlineExceeded`，并且 `CommittedSeq` 不增长。
- follower 恢复后必须追平 `HW/LEO` 并能读回停机期间的全部 committed 消息。

- [ ] **Step 2: 跑聚焦测试，确认当前实现未覆盖**

Run:

```bash
go test ./pkg/replication/multiraft -run 'TestRemoteCommitDoesNotApplyStaleCommandAfterHeal' -count=1
go test ./pkg/replication/isr -run 'TestAppendFailsFastWhenMetaISRBelowMinISR' -count=1
go test ./pkg/storage/channellog -run 'TestThreeNodeCluster(BlocksCommitWhenMinISRUnavailable|FollowerRestartCatchesUpAfterLeaderProgress)' -count=1
```

Expected: FAIL

- [ ] **Step 3: 写最小实现**

实现要求：

- `proposal_test.go` 复用 `partitionNode/healNode/waitForNodeCommitIndex`
- `append_test.go` 直接在 `isr` 层构造 `ISR < MinISR` 元数据
- `multinode_integration_test.go`
  - 用新 `stopNode/restartNode` helper
  - 负向测试使用 `LoadMsg()` 断言“消息不可 committed”
  - 恢复测试在 follower 重启后校验历史消息全部补齐

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run:

```bash
go test ./pkg/replication/multiraft -run 'TestRemoteCommitDoesNot(ResolveLocalFuture|ApplyStaleCommandAfterHeal)' -count=1
go test ./pkg/replication/isr -run 'TestAppend(FailsFastWhenMetaISRBelowMinISR|LeaderLeaseExpiryFencesAppend)' -count=1
go test ./pkg/storage/channellog -run 'TestThreeNodeCluster(AppendCommitsBeforeAckAndSurvivesFollowerRestart|BlocksCommitWhenMinISRUnavailable|FollowerRestartCatchesUpAfterLeaderProgress)' -count=1
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/replication/multiraft/proposal_test.go pkg/replication/isr/append_test.go pkg/storage/channellog/multinode_integration_test.go
git commit -m "test: cover quorum loss and follower recovery"
```

## Task 3: 补齐 app 层 durable ack 与滚动重启场景

**Files:**
- Modify: `internal/app/multinode_integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定 app 层最终语义**

新增测试：

- `TestThreeNodeAppSendAckSurvivesLeaderCrash`
- `TestThreeNodeAppRollingRestartPreservesWriteAvailability`

关键断言：

- sender 收到 `Sendack` 后立刻杀 leader，消息仍必须能在存活节点和恢复后的旧 leader 上读到。
- 滚动重启期间允许短暂重试，但每条成功 `Sendack` 的消息都必须在三节点 `ChannelLogDB` 中出现。
- 测试里每轮连接当前 leader，不把“客户端长连接迁移”混进 durable 语义测试。

- [ ] **Step 2: 跑聚焦测试，确认当前实现未覆盖**

Run:

```bash
go test ./internal/app -run 'TestThreeNodeApp(SendAckSurvivesLeaderCrash|RollingRestartPreservesWriteAvailability)' -count=1 -timeout 10m
```

Expected: FAIL

- [ ] **Step 3: 写最小实现**

实现要求：

- 提供 `mustReadSendAck()`、`sendMultinodeMessageAndReadAck()`、`seedMultinodePersonChannelMeta()`、`requireCommittedOnRunningApps()`
- `SendAckSurvivesLeaderCrash` 必须包含：
  - seed runtime meta
  - sender 收 `Sendack`
  - leader stop
  - `waitForLeaderChange`
  - 存活节点读回
  - 旧 leader restart 后读回
- `RollingRestartPreservesWriteAvailability` 必须包含：
  - 节点顺序重启 `follower -> follower -> leader`
  - 每轮重启前后都做一次成功发送
  - 最终统一校验全部 `ackedSeqs`

- [ ] **Step 4: 重新跑 app 聚焦测试，确认通过**

Run:

```bash
go test ./internal/app -run 'TestThreeNodeApp(GatewaySendUsesDurableCommit|SendAckSurvivesLeaderCrash|RollingRestartPreservesWriteAvailability)' -count=1 -timeout 10m
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/app/multinode_integration_test.go
git commit -m "test(app): cover durable ack under restart"
```

## Task 4: 补齐 raftcluster 重选主与 restart stress

**Files:**
- Modify: `pkg/cluster/raftcluster/cluster_test.go`
- Modify: `pkg/cluster/raftcluster/stress_test.go`

- [ ] **Step 1: 先写失败测试，锁定 cluster 层 leader churn 语义**

新增测试：

- `TestThreeNodeClusterReelectsAfterLeaderRestart`
- `TestStressThreeNodeMixedWorkloadWithRestarts`
- `TestStressForwardingContentionWithLeaderRestarts`

关键断言：

- 旧 leader stop 后，多数派能重新选主。
- 旧 leader restart 后可以重新加入并继续服务。
- `mixed workload + restarts` 最终所有成功创建的 channel 都可读。
- `forwarding contention + leader restarts` 持续向 follower 写入时，允许 transient error，但最终数据完整性必须成立。

- [ ] **Step 2: 跑聚焦测试，确认当前实现未覆盖**

Run:

```bash
go test ./pkg/cluster/raftcluster -run 'TestThreeNodeClusterReelectsAfterLeaderRestart' -count=1
WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=10s go test ./pkg/cluster/raftcluster -run 'TestStress(ThreeNodeMixedWorkloadWithRestarts|ForwardingContentionWithLeaderRestarts)' -count=1 -timeout 20m
```

Expected: FAIL

- [ ] **Step 3: 写最小实现**

实现要求：

- `cluster_test.go`
  - 用 `restartNode()` 重启旧 leader
  - 重启后验证 `CreateChannel()` 可继续成功
- `stress_test.go`
  - 新增 `nodesMu` 保护 worker 选节点和主线程重启
  - `mixed workload` 每秒随机重启一个节点
  - `forwarding contention` 必须按 `channelID -> SlotForKey -> 当前 group leader` 选 follower，不能偷懒只看 `group 1`
  - stress report 输出 `Restarts`、`Transient Errors`、`Channels Verified`

- [ ] **Step 4: 重新跑 cluster 测试，确认通过**

Run:

```bash
go test ./pkg/cluster/raftcluster -run 'TestThreeNodeClusterReelectsAfterLeaderRestart' -count=1
WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=10s WKCLUSTER_STRESS_WORKERS=4 go test ./pkg/cluster/raftcluster -run 'TestStress(ThreeNodeMixedWorkloadWithRestarts|ForwardingContentionWithLeaderRestarts)' -count=1 -timeout 20m
```

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/cluster/raftcluster/cluster_test.go pkg/cluster/raftcluster/stress_test.go
git commit -m "test(raftcluster): add restart and forwarding stress coverage"
```

## Task 5: 固化执行矩阵与验收标准

**Files:**
- Modify: `docs/superpowers/plans/2026-04-08-distributed-cluster-testing.md`

- [ ] **Step 1: 把最终执行矩阵写入本计划**

矩阵要求：

- `PR 必跑`
  - `multiraft` stale leader
  - `channellog` quorum loss / follower recovery
  - `app` durable ack / rolling restart
  - `raftcluster` leader restart
- `nightly`
  - `raftcluster` restart stress
  - `isrnode` pressure stress
  - `metafsm` / `raftstorage` stress
- `发布前 soak`
  - `-race`
  - `-count=20`
  - 20 分钟以上 restart stress

- [ ] **Step 2: 提供推荐命令**

`PR`：

```bash
go test ./pkg/replication/multiraft -run 'TestRemoteCommitDoesNot(ResolveLocalFuture|ApplyStaleCommandAfterHeal)' -count=1
go test ./pkg/storage/channellog -run 'TestThreeNodeCluster(AppendCommitsBeforeAckAndSurvivesFollowerRestart|BlocksCommitWhenMinISRUnavailable|FollowerRestartCatchesUpAfterLeaderProgress)' -count=1
go test ./internal/app -run 'TestThreeNodeApp(GatewaySendUsesDurableCommit|SendAckSurvivesLeaderCrash|RollingRestartPreservesWriteAvailability)' -count=1 -timeout 10m
go test ./pkg/cluster/raftcluster -run 'TestThreeNodeClusterReelectsAfterLeaderRestart' -count=1
```

`nightly`：

```bash
WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=3m WKCLUSTER_STRESS_WORKERS=8 WKCLUSTER_STRESS_SEED=42 go test ./pkg/cluster/raftcluster -run 'TestStress(ThreeNodeMixedWorkload|ThreeNodeMixedWorkloadWithRestarts|ForwardingContention|ForwardingContentionWithLeaderRestarts)' -count=1 -timeout 30m
MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3m MULTIISR_STRESS_GROUPS=256 MULTIISR_STRESS_PEERS=8 MULTIISR_STRESS_SEED=42 go test ./pkg/replication/isrnode -run 'TestRuntimeStress' -count=1 -timeout 30m
WKFSM_STRESS=1 WKFSM_STRESS_DURATION=3m WKFSM_STRESS_WORKERS=8 go test ./pkg/storage/metafsm -run Stress -count=1 -timeout 30m
WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3m go test ./pkg/storage/raftstorage -run Stress -count=1 -timeout 30m
```

`发布前 soak`：

```bash
go test -race ./pkg/replication/multiraft ./pkg/storage/channellog ./pkg/cluster/raftcluster ./internal/app -count=1
go test ./pkg/replication/multiraft -run 'TestRemoteCommitDoesNot(ResolveLocalFuture|ApplyStaleCommandAfterHeal)' -count=20
go test ./pkg/storage/channellog -run 'TestThreeNodeCluster(BlocksCommitWhenMinISRUnavailable|FollowerRestartCatchesUpAfterLeaderProgress)' -count=20
go test ./internal/app -run 'TestThreeNodeApp(SendAckSurvivesLeaderCrash|RollingRestartPreservesWriteAvailability)' -count=20 -timeout 20m
WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=20m WKCLUSTER_STRESS_WORKERS=16 WKCLUSTER_STRESS_SEED=20260408 go test ./pkg/cluster/raftcluster -run 'TestStress(ThreeNodeMixedWorkloadWithRestarts|ForwardingContentionWithLeaderRestarts)' -count=1 -timeout 40m
```

- [ ] **Step 3: 固化通过标准**

必须明确写入：

- 不允许出现“成功 ack 但消息未 committed”
- 不允许 stale leader 恢复后 apply 旧提案
- stress 测试允许 transient error，但最终 `Data Integrity` 必须 `OK`
- `-count=20` 不允许偶发失败

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/plans/2026-04-08-distributed-cluster-testing.md
git commit -m "docs: add distributed cluster testing plan"
```

## 最终验证

- [ ] **Step 1: 跑 PR 必跑矩阵**

Run:

```bash
go test ./pkg/replication/multiraft -run 'TestRemoteCommitDoesNot(ResolveLocalFuture|ApplyStaleCommandAfterHeal)' -count=1
go test ./pkg/storage/channellog -run 'TestThreeNodeCluster(AppendCommitsBeforeAckAndSurvivesFollowerRestart|BlocksCommitWhenMinISRUnavailable|FollowerRestartCatchesUpAfterLeaderProgress)' -count=1
go test ./internal/app -run 'TestThreeNodeApp(GatewaySendUsesDurableCommit|SendAckSurvivesLeaderCrash|RollingRestartPreservesWriteAvailability)' -count=1 -timeout 10m
go test ./pkg/cluster/raftcluster -run 'TestThreeNodeClusterReelectsAfterLeaderRestart' -count=1
```

Expected: PASS

- [ ] **Step 2: 跑短时 nightly smoke**

Run:

```bash
WKCLUSTER_STRESS=1 WKCLUSTER_STRESS_DURATION=10s WKCLUSTER_STRESS_WORKERS=4 go test ./pkg/cluster/raftcluster -run 'TestStress(ThreeNodeMixedWorkloadWithRestarts|ForwardingContentionWithLeaderRestarts)' -count=1 -timeout 20m
MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=10s MULTIISR_STRESS_GROUPS=64 MULTIISR_STRESS_PEERS=4 go test ./pkg/replication/isrnode -run 'TestRuntimeStress' -count=1 -timeout 20m
```

Expected: PASS

- [ ] **Step 3: 汇总结果并再决定是否进入长 soak**

输出内容至少包括：

- 哪些测试新增成功
- 哪些 stress 项仍然 flaky
- 是否可以进入 `-count=20` 和 20 分钟以上 soak
