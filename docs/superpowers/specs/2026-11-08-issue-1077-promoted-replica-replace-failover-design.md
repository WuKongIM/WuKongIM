# Issue #1077：已提升的副本替换任务阻塞 leader 故障转移

状态：设计已获批准并按本文实现。E2E 与本文 7.1 节的差异：历史校验改为通过路由的 `/channel/messagesync` 在每个运行节点上进行，因为 manager 消息读取是节点本地读，不承载该 Slot 的节点会返回 404。

来源：https://github.com/WuKongIM/WuKongIM/issues/1077 ，根因分析见 [issue 评论](https://github.com/WuKongIM/WuKongIM/issues/1077#issuecomment-6605537336)。

代码调查基线：`399e269b1`（v3.0.0-beta.27）。

## 1. 问题

副本替换任务（`ReplicaReplace`）处于 `VerifyMembership`（26）阶段时，如果频道 leader 宕机，这个频道会一直拒绝写入（`503 retry required`），直到旧 leader 进程恢复。旧 leader 永远不恢复时，频道就永远不可写。

形成死锁的两条路径：

1. `RepairScanner.scanMeta` 发现 leader 疑似宕机，同时频道有进行中的迁移任务。它调用 `AbortReplicaReplacementForFailover`，然后直接返回，不选主。
2. 这个抢占只会中止尚未提升的任务（第 1、20–25 阶段，并且目标不在 ISR 里）。到了第 26 阶段，目标已经进入 ISR，所以抢占不生效，任务保持进行中，第 1 步每轮扫描都重复。
3. 执行器的 `runReplicaReplaceVerifyMembership` 会先对 `meta.Leader` 调用 `ApplyChannelMeta`。旧 leader 已宕机，这一步一直失败，执行不到最后清除写入栅栏那一步。
4. 写入栅栏从 `CutoverFence`（23）阶段开始持有，只有第 26 阶段结束时才清除。在此之前，所有写入都返回 503。

## 2. 关键事实：存储层已经允许这次转换

已有的清除栅栏命令（Slot 命令 38，`ClearChannelWriteFence`）在存储层的转换规则如下（`requireChannelMigrationClearFenceTransition`）：

- 允许 `ReplicaReplace` 任务从 `VerifyMembership` 转为 `Completed` / `ClearFence`。
- 只校验任务守卫、运行时元数据守卫，以及栅栏令牌和版本是否与任务匹配。
- 不要求 leader 存活，也不要求运行时探测结果。

运行时校验（让 leader 和目标应用元数据、探测目标角色、确认回放覆盖）全部在执行器里，存储层不检查。

**结论：方案 B 只需要改执行器侧的调度逻辑，不新增命令、不改状态机转换规则、不改持久化格式。**

## 3. 方案

在 `RepairScanner.scanMeta` 现有的抢占入口里，增加一种已提升任务的处理：发现 leader 疑似宕机，且任务处于第 26 阶段时，用已有的命令 38 直接把任务标为完成，同时清除这个任务持有的写入栅栏，然后照常返回。下一轮扫描时频道已经没有进行中的任务，现有的 `scanLeaderFailover` 照常选主。

新增 `MigrationStore.CompletePromotedReplicaReplacementForFailover(ctx, id, leader, leaderEpoch) (bool, error)`，和现有的 `AbortReplicaReplacementForFailover` 并列，放在 `repair_preempt.go`。它从 Slot 重新读取最新的运行时元数据和进行中的任务，下列条件全部满足时才提交：

| 条件 | 目的 |
| --- | --- |
| `meta.Leader == leader` 且 `meta.LeaderEpoch == leaderEpoch` | 只处理扫描器观察到疑似宕机的那一任 leader |
| 任务未结束、`Kind == ReplicaReplace`、`!EmbeddedLeaderTransfer`、`Phase == VerifyMembership` | 只处理已提升的任务；其他阶段仍走现有的中止路径或保持不动 |
| 源节点不在 `Replicas` 和 `ISR` 中，目标节点同时在 `Replicas` 和 `ISR` 中，`len(ISR) >= MinISR` | 与 `runReplicaReplaceVerifyMembership` 自己的成员校验相同，确认成员变更已经提交 |
| `meta.WriteFenceToken == task.TaskID` 且 `meta.WriteFenceVersion == task.FenceVersion` | 只清除这个任务自己持有的栅栏 |

提交时复用 `ClearWriteFence` 的请求构造方式：`RuntimeGuard` 取自同一次读到的元数据，状态和阶段为 `Completed` / `ClearFence`。如果元数据在读取之后发生了变化，守卫不匹配，Slot 拒绝这次提交，函数返回 `false`。执行器同时把任务清掉时也一样，函数返回 `false`。

`scanMeta` 的调整：先尝试已有的中止，不生效时再尝试完成；两者任一成功都计数，然后返回，下一轮再选主。`RepairScannerResult` 新增 `TasksCompletedForFailover`，计入 `MaxTasksPerTick` 上限，并输出到迁移 worker 的日志字段 `tasks_completed_for_failover`。

## 4. 安全性

- **数据不丢**：第 25 阶段在写入栅栏保护下，原子地提升目标并移除源节点。目标在 `FinalTargetCatchUp` 之后才进入 ISR，所以持有栅栏之前已提交的全部数据。栅栏期间没有新的提交。
- **跳过的只是运行时同步**：第 26 阶段还剩的工作，是把已提交的成员关系推送给 leader 和目标运行时、探测目标角色，以及确认回放覆盖。leader 已宕机时，接下来的故障转移任务会选出原生 leader，并带着新的 leader epoch 把元数据重新应用到新 leader 和各个副本，自身也要求回放覆盖。所以跳过的这一步由故障转移重新完成。
- **清除栅栏后到选主之前**：这段时间频道没有可用的 leader，写入照样被拒。故障转移任务自己会设置新的栅栏。
- **误判宕机**：如果 leader 其实还活着，只是 Controller 认为它疑似宕机，这和没有迁移任务时的普通故障转移完全一样：旧 leader 运行时仍持有第 23 阶段的栅栏，在拿到新元数据前不接受写入，新的 leader epoch 会把它隔离。不会引入新的脑裂情形。
- **规则不变**：FLOW 中“dead leaders preempt unpromoted replacement through guarded abort”保持不变；“promoted tasks stay protected”调整为“已提升的任务只能被完成、不能被中止，也不会回滚成员”。

## 5. 兼容性与升级（AGENTS.md「Breaking Changes and Upgrades」）

| 项目 | 结论 |
| --- | --- |
| 受影响版本 | 修复前的所有 v3 版本都有这个死锁 |
| 持久化数据 / 格式 | 不变：没有新命令，没有新字段，第 26 阶段完成后的任务记录形状与正常完成时相同 |
| 复制协议 / RPC / API / 配置 | 不变 |
| 滚动升级 | 安全：新节点提议的是已有的命令 38，旧副本用完全相同的规则应用它，不会出现新旧节点判定不一致导致的数据分叉。扫描器运行在旧节点上时，行为和现在一样（仍会死锁），不会更差 |
| 回滚 | 可以直接回滚到上一个版本，不需要迁移数据 |
| 停机 / 数据丢失风险 | 无 |
| 行为差异 | 这类任务会被记为 `Completed`，但没有执行最后的运行时校验。只有日志和新增的计数能区分出来 |

据此判断这**不是破坏性变更**。实现阶段会核对 PR diff 里不包含 `pkg/db/meta` 和 `pkg/slot` 的转换规则改动；一旦需要改到，就停下来重新请你批准。

## 6. 可能失败的方式（先列出，再写代码）

| 编号 | 失败方式 | 由哪项测试覆盖 |
| --- | --- | --- |
| F1 | 完成了不在第 26 阶段的任务（未提升的任务应该被中止，而不是完成） | 单元测试 |
| F2 | 读取后元数据已变化（例如新的 leader epoch），仍然提交成功 | 单元测试（Slot 守卫拒绝） |
| F3 | 清除了不属于这个任务的栅栏 | 单元测试 |
| F4 | leader 健康时也提前完成任务 | 单元测试 |
| F5 | 执行器先一步清除栅栏，扫描器报错刷屏或重复计数 | 单元测试 |
| F6 | 任务完成后，下一轮扫描没有创建故障转移任务 | 单元测试 + E2E |
| F7 | 选出新 leader 后，运行时仍带旧栅栏，写入继续 503 | E2E |
| F8 | leader 停机前已提交的消息在新 leader 上读不到 | E2E |
| F9 | 旧 leader 恢复后，日志与新 leader 分叉 | E2E |
| F10 | 混合版本下旧副本拒绝命令 | 设计约束：命令和规则不变，审阅时核对 diff |

## 7. 测试计划

按仓库的测试规定，E2E 是主要验收手段；单元测试只覆盖扫描器和存储之间的判定分支，并且保持很快。

### 7.1 E2E（可复现，修复前必须失败）

新场景 `test/e2e/cluster/channel_failover_during_replica_replace`，沿用 `dynamic_node_faults` 的约定：

- 使用通过 `scripts/build-gofail-binary.sh --package pkg/cluster/channels` 构建的 gofail 二进制，并且只在 `WK_E2E_GOFAIL_CHANNEL_MIGRATION=1` 时运行。
- 新增 gofail 点 `wkChannelReplicaReplaceVerifyMembershipError`，放在 `runReplicaReplaceVerifyMembership` 的开头，返回错误。用返回错误而不是暂停，是因为扫描器和执行器在同一个 worker tick 里串行运行，暂停会把扫描器一起卡住。

步骤：

1. 启动一个 4 节点的多节点集群，创建一个 3 副本的群组频道，写入若干条消息。
2. 在所有节点上打开 gofail 点。通过 `POST /manager/channel-migrations/replica-replace` 把一个非 leader 副本替换为第 4 个节点。
3. 通过 manager 迁移任务列表，等任务进入 `verify_membership` 阶段。
4. 停止频道 leader 进程，然后关闭 gofail 点。此后执行器对已宕机 leader 的真实拨号失败接手，忠实还原 issue 的场景。
5. 要求在规定时间内（例如 30 秒）通过幸存节点的 `/message/send` 写入成功，按 e2e 约定只重试精确的 `503 retry required`（F6、F7）。
6. 在所有幸存节点上校验历史消息完整（F8）。
7. 重启旧 leader，校验它以 follower 身份追上，各节点历史一致（F9）。
8. 输出 JSON 报告作为可复现的产物，包含各阶段耗时、任务的最终状态和 leader 切换记录。

先在当前 main 上运行这个场景，确认它在第 5 步失败，再提交修复。

### 7.2 单元测试（`pkg/cluster/channels`）

在 `repair_preempt_test.go` 中用真实的元数据存储覆盖 F1–F5，在扫描器测试中覆盖 F6：第一轮扫描完成任务，第二轮扫描创建故障转移任务。

## 8. 需要同步更新的文档

- `pkg/cluster/FLOW.md`：更新第 3 条主流程中关于抢占的描述，并通过 `flow-doc-contracts` 命名检查。
- 根目录 `CHANGELOG.md` 的 `Unreleased`：新增一条修复记录（频道在副本替换收尾时 leader 宕机后可以恢复写入）。
- `docs/development/PROJECT_KNOWLEDGE.md`：记录“已提升的副本替换只能完成、不能中止”这条规则。

## 9. 不在范围内

- e2e 诊断信息里 app-log 路径错误：issue 评论中已经记录，单独修复。
- 第 26 阶段之外的类似窗口：已核实不存在。命令 37（`PromoteLearnerAndRemoveReplica`）在同一个原子批次里完成三件事：替换 `Replicas` 和 `ISR` 成员、把阶段从 25 推进到 26，并且拒绝目标已经在 ISR 中的请求（`requireChannelMigrationPromoteLearnerTransition`）。所以不会出现“第 25 阶段且目标已在 ISR”的状态。第 25 阶段及更早的阶段由现有的中止路径覆盖。
