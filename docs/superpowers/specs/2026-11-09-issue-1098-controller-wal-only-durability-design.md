# Issue #1098：Controller 每条日志只保留一次 WAL fsync

状态：已按本文实现。实现中的补充：Raft 服务的 `Stop`/`Start` 现在会等待自行失败退出的运行协程完成持久化收尾，以免关闭检查点在 `Stop` 返回后才写入，或与重新打开的存储竞争（回归测试 `TestStopAfterRunFailureJoinsDurableShutdown`）。实测普通磁盘上 256 哈希槽单节点集群就绪由 25–27 秒降至 4.6–6.6 秒。

来源：https://github.com/WuKongIM/WuKongIM/issues/1098 ，根因证据见 [issue 评论](https://github.com/WuKongIM/WuKongIM/issues/1098#issuecomment-6723684532)。

代码调查基线：`9a4c4dca8`。原型位于本地分支 `proto/controller-meta-checkpoint`（未推送，只用于测量）。

## 1. 问题

新启动的集群偶尔会有一个节点迟迟不就绪：`/readyz` 和 WKProto 连接一直等不到成功。CI 和 macOS 本机都出现过。

根因链（已用对照实验确认因果）：

1. 启动时 presence 要为 **256 个哈希槽各申领一次代次**（`ClaimPresenceGeneration`）。每次申领之前都要做一次一致性读（`GetPresenceGeneration` → `ReadBarrier`）。
2. 这次读要等到之前所有已提交的日志都 apply 完，所以 256 次申领实际上是串行的，每次都要经过一轮完整的"提交 + apply"。
3. 每一轮的持久化成本是 **8 次 fsync**，全部在锁内：

   | 步骤 | 位置 | fsync |
   | --- | --- | --- |
   | Raft Ready 落盘 | `raftstore.SaveReady`：WAL、`meta.json` 临时文件、目录 | 3 |
   | FSM 保存状态 | `statefile.Save`：`cluster-state.json` 临时文件、目录 | 2 |
   | 推进 applied 标记 | `raftstore.MarkAppliedBatch`：WAL、`meta.json` 临时文件、目录 | 3 |

4. macOS 本机一次 fsync 约 19 ms（APFS 上 `F_FULLFSYNC`，50 次取样的 p50），Linux 慢盘 runner 上也很可观。于是 256 轮串行申领超出了单节点 10 秒、集群 30 秒的就绪时限。

实测（同一用例：256 个哈希槽的单节点集群、开启 token 认证，统计启动到 `/readyz` 就绪的时间，普通磁盘，每种各跑 2 次）：

| 版本 | 就绪耗时 |
| --- | --- |
| 当前 main | 25.6 s / 26.8 s |
| 原型：只把 `meta.json` 改为检查点（每轮 4 次 fsync） | 11.0 s / 16.2 s |
| 原型 + 批量接收读请求 | 13.5 s / 14.0 s（无明显收益，不采纳） |
| 当前 main，数据放内存盘（fsync 约 0.09 ms） | 约 3 s |

## 2. 目标

每条 Controller 日志在热路径上**只做一次 fsync**，也就是 Raft 必需的那次 WAL 落盘。`meta.json` 和 `cluster-state.json` 改成检查点，只在需要时写入。磁盘格式、复制协议、配置都不变，可以回滚到任何 v3 版本。

预期：每轮 fsync 从 8 次降到 1 次。按串行 256 轮估算，本机就绪时间约 5–6 秒。所有 Controller 写入（任务、健康上报、presence 申领）都会受益。

## 3. 改动

### 3.1 `raftstore`

- `SaveReady`：WAL 写入和 fsync 不变（`raft.MustSync` 决定）。只有带快照时才同步写 `meta.json`。内存里的 `s.meta` 仍然每次都更新。
- `MarkAppliedBatch`：照常向 WAL 追加 applied 记录，但**不单独 fsync**（由下一次 Ready 的 fsync 或检查点一起落盘），也不再重写 `meta.json`。
- `Compact`：在 `releaseBefore` 删除任何 WAL 段之前，先写 `meta.json` 检查点，再对 WAL 做一次 fsync。
- `Close`：正常关闭时写一次 `meta.json` 检查点。

### 3.2 FSM 状态文件

- `fsm.StateMachine.ApplyBatch` 不再每批调用 `statefile.Save`，只更新内存状态并照常发布（watch 唤醒、任务转换通知不变）。
- 新增 `Checkpoint(ctx)`：把当前内存状态写成 `cluster-state.json`，它的 `AppliedRaftIndex` 就是已 apply 的索引。触发时机：
  1. 每次日志压缩 / 快照之前（硬性要求，见 4.3）；
  2. 有界的周期：apply 每推进 4096 条，或距上次检查点超过 30 秒，二者先到者为准。这样可以限制崩溃后的重放长度；
  3. 正常关闭。
- 镜像节点（非投票节点）从 leader 拉取并保存完整状态文件的流程不变。leader 发送的是内存快照，不是磁盘文件。

### 3.3 Raft 服务

- `onApplied` 之后的 `durableApplied` 水位改为表示"已在内存中 apply，并且对应日志已持久化提交"。ReadBarrier 仍然等这个水位追上读索引。
- 检查点由 apply 协程在批与批之间执行，不阻塞 Raft 主循环。

## 4. 正确性论证

### 4.1 已提交日志的持久性不变

Raft 安全性依赖的 HardState（term、vote、commit）和日志条目，仍然在 Ready 落盘时写入 WAL 并 fsync（`raft.MustSync`），这一点不受影响。被拿掉的只是**派生状态**的同步写入。

### 4.2 启动恢复本来就以 WAL 为准

- `raftstore.Open`：从 WAL 回放出来的 HardState、applied index、快照元数据、ConfState 会覆盖 `meta.json`。从 v3.0.0-beta.1 起每个版本都是这样。
- `recoverStartup`：从 `cluster-state.json` 自己的 `AppliedRaftIndex + 1` 开始重放已提交日志，直到 commit；状态文件领先于 commit 时会直接报错。这同样从 beta.1 起就有。beta.20 起，状态文件比快照旧时还会先从快照恢复。

这说明"派生文件落后于 WAL"本来就是现有恢复逻辑必须处理的情况：在 WAL fsync 之后、写 `meta.json` 之前崩溃，就会出现。新设计只是让这个窗口在正常运行时也存在，恢复逻辑本身不改。

### 4.3 压缩前必须写检查点（硬规则）

WAL 换段时不会把当前状态带进新段。压缩删除旧段之后，回放就依赖检查点。所以：

> 删除任何 WAL 前缀之前，`meta.json` 和 `cluster-state.json` 两个检查点都要写好并 fsync，而且索引都不早于压缩点。

满足这条规则后，恢复永远不需要已删除的日志；beta.20 之前不会"从快照补齐旧状态文件"的版本也能正确恢复。

### 4.4 apply 结果的可见性

presence 读请求和申领回执，都是在内存中 apply 之后才返回。如果返回之后崩溃，重启时会从检查点开始确定性地重放同一段已提交日志，得到同样的代次 `G`（`G` 本身就是提交索引）。所以对外暴露的结果不会因为崩溃而改变。

### 4.5 未 fsync 的 applied 记录

applied 记录只是让恢复少重放一些日志。崩溃时丢了最后几条，结果只是 applied 水位偏低、多重放几条，不影响正确性。

## 5. 兼容性与升级（AGENTS.md「Breaking Changes and Upgrades」）

| 项目 | 结论 |
| --- | --- |
| 持久化格式 | 不变：文件、记录类型、JSON 结构都一样，只是写入频率变了 |
| 复制协议 / RPC / API / 配置 | 不变 |
| 滚动升级 | 安全：这只是节点本地的持久化策略，不影响节点之间的交互 |
| 回滚 | 任何 v3 版本都能读新版本写的数据，依据见 4.2 和 4.3 |
| 停机 / 数据丢失 | 无 |
| 行为差异 | 磁盘上的 `cluster-state.json` 最多落后约 4096 条日志或 30 秒；按约定，读者应读内存状态 |

据此判断这**不是破坏性变更**。实现阶段只要发现任何地方需要改格式或协议，就停下来重新请你批准。

## 6. 风险与待核实项

1. **晋升路径**：`runtime_controller_voter_promotion.go` 会从磁盘读 `cluster-state.json`，用于镜像节点晋升为投票节点。镜像节点的文件由同步客户端整份写入，理论上不受影响。实现前要逐行核实，晋升后的第一次检查点也要覆盖到。
2. **离线工具**：`wkcli` 等离线检查如果读 `cluster-state.json`，看到的是检查点时刻的状态。需要在输出里标出检查点索引，避免误读。
3. **WAL 保留量**：两次压缩之间保留的 WAL 不变，所以检查点周期不会让磁盘占用增加。
4. **单测缺口**：现有 raftstore 测试没有一条断言"每次 Ready 后 `meta.json` 都是最新的"，原型改动也全部通过了。这次要补上崩溃恢复相关的测试。

## 7. 可能失败的方式（先列出，再写代码）

| 编号 | 失败方式 | 覆盖方式 |
| --- | --- | --- |
| F1 | 压缩删除了检查点之后还需要的 WAL 段，恢复时缺日志 | raftstore 测试：检查点落后，压缩后重新打开 |
| F2 | 重启后丢失 Raft 投票或 term，违反 Raft 安全性 | raftstore 测试：`meta.json` 落后，重新打开，HardState 以 WAL 为准 |
| F3 | 状态文件落后时，恢复少重放或重复 apply | raft 恢复测试：状态文件落后于 commit，重启后状态与未崩溃时逐字节相同 |
| F4 | 压缩没有触发检查点，旧版本（beta.20 之前的逻辑）无法恢复 | 测试：压缩后用不从快照补齐的恢复逻辑重放，必须成功 |
| F5 | 崩溃后申领回执或读结果和恢复后的状态不一致 | E2E：申领之后 kill -9 重启，同一 Slot 的代次不变 |
| F6 | 检查点阻塞 Raft 主循环，或在热路径上做了 fsync | 计数测试：每轮 fsync 次数为 1（用可注入的 sync 计数器） |
| F7 | 启动仍然超时 | E2E：256 个哈希槽的单节点集群，在普通磁盘上 10 秒内就绪（macOS 本机实测作为验收产物） |
| F8 | 镜像节点晋升读到旧状态 | 现有晋升 E2E 必须通过；按第 6 节第 1 项逐行核实 |

## 8. 测试计划

- 单元测试：`raftstore`（F1、F2、F6）和 `controller/raft` 恢复（F3、F4）。
- E2E：在 `test/e2e/cluster/startup_recovery` 中加入 F5 和 F7。F7 输出 JSON 报告，记录就绪耗时、提交索引和检查点索引。
- 回归：Controller 相关包的全部单元测试、`dynamic_node_*` 和 `controller_voter_promotion` E2E、夜间 gofail 套件。

## 9. 需要同步的文档

- `pkg/controller/FLOW.md`：改写两条约定。
  - "JSON 文件在发布和推进 applied index 之前保存"改为"JSON 文件是检查点，压缩前必须写入"；
  - "等待实际的 FSM 保存和持久化 applied 标记"改为"等待 apply 水位，已提交日志的持久性由 WAL 保证"。
- `docs/development/PROJECT_KNOWLEDGE.md`：记录"派生文件是检查点，压缩前必须写检查点"这条规则。
- `CHANGELOG.md`：记一条用户可见的改进，fsync 慢的磁盘上 Controller 写入和集群启动明显加快。
