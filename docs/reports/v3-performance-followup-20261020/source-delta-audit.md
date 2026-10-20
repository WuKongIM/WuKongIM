# beta.24 → b469：conversation persisted 读取性能差异审计

## 范围与结论

- 基线：`4d44b39dda6fdaa7dafb637ca6cfb718e9530f77`（beta.24）。
- 候选：`b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。
- 仅静态审计这两个固定版本的差异及调用链；未修改仓库，未运行测试、负载或工作流。下列代码行号均以 **b469** 为准，不将后续迁移修复归入本次差异。
- 已先广搜差异，再读取适用的 `FLOW.md`（cluster/slot/db 子树与 conversation、infra/cluster、app 等 package scope）；FLOW 仅作导航。可执行代码为调用链依据。
- 测量前提由主任务证据提供：`/conversation/list` 与 `/conversation/sync` 的纯 persisted 窗口，runtime loads 与 membership writes 增量为 0。此审计不独立重复验证测量结果。

**最直接的候选是通用 Slot ReadIndex 合并。** Persisted 读取不会激活频道 runtime，但仍通过消息编辑 overlay 获取权威 Slot 读取屏障，因此确实能执行被修改的代码。SEND 权限 cohort、首次频道元数据创建均不在这一读取调用链上。存储 commit batching 是有条件的共享资源影响；commit 指标快照主要属于当前夹具的窗口边界开销。代码审计只能定位候选，不能确定此次性能差异的根因、方向或幅度。

## 能进入纯 persisted 请求调用链的差异

### 1. 通用 Slot fresh ReadIndex 合并（`81c93d726`）

差异：

- `pkg/slot/multiraft/slot.go:531` 的 `processControls` 在 `:538`–`:544` 合并本次 worker 已取出的连续 read-barrier controls。
- `pkg/slot/multiraft/read_barrier.go:115` 新增 `issueReadBarrierBatch`；`:120` 分配 batch key 切片，`:123` 逐请求 admission，`:132` 保存同批 keys，`:134` 只发一次 `rawNode.ReadIndex`。
- 同文件 `:188`–`:202` 将返回的 ReadState index 分配给本批各请求并完成屏障。
- 单请求仍走原路径（`:116`–`:118`）。没有新增等待窗口；后续到达的请求需要新的 quorum round。逐请求取消、leader/current-term、256 pending 上限及 durable applied 条件仍保留（`:139`–`:185`、`:204` 起）。

可达调用链：

1. `internal/usecase/conversation/app.go:182` 调用 persisted heads hydration；`internal/infra/cluster/conversation.go:108` 选择 `ReadChannelPersistedConversationHeads`。
2. `pkg/cluster/node.go:1281` 读取 persisted heads，`:1295`–`:1302` 对找到的消息执行 `overlayMessageContent`。
3. `pkg/cluster/node_message_update.go:44`–`:63` 构造消息编辑读取并调用 `ReadMessageUpdatesBatch`，`:39` 转交 Slot proxy。
4. `pkg/slot/proxy/message_update.go:234`–`:237` 在生产端口调用 `ReadSlotBarrier`，屏障成功后才在 `:257` 读取数据库快照。
5. `pkg/slot/multiraft/read_barrier.go:66` 起的屏障入口最终将 control 放入 worker 队列，进入上述合并逻辑。

Legacy sync 的 recent messages 也经过 overlay：`pkg/cluster/node.go:936`–`:940`。即使 fixture 没有编辑过消息，跳过 apply 的判断仍发生在页面读回之后（`pkg/cluster/node_message_update.go:77`–`:90`），因此“无编辑”和 runtime loads 为 0 都不能排除 ReadIndex。

影响推断：同一 Slot 的并发读取可能改变 quorum 请求数、临时分配、锁内 bookkeeping、完成突发与调度形态。原意应是减少重复 quorum 工作，但仅凭代码不能断定 P99 或 CPU 必然改善。建议主任务优先对照已有 `edit_overlay` 与 message-update `barrier` 阶段证据，以及对应 Slot/raft/调度的 profile；该建议不构成已确认归因。

## 共享资源与测量边界候选

### 2. Commit 窗口到期后吸收有限队列快照（`07b8dcd76`）

- `pkg/db/internal/commit/coordinator.go:481`–`:482`、`:504`–`:505` 将到期立即返回改为 `collectQueuedSnapshot`。
- 新函数在 `:521` 固定 intake 长度，`:526` 固定待处理数量，`:528` 起有界处理，并保留 batch size/bytes 上限（`:537`、`:543`）。
- 该 coordinator 同时用于 metadata（`pkg/db/meta/db.go:41`）和 message store（`pkg/db/message/compat.go:336`、`:350`），不是 SEND 专用设施。

它不由普通数据库读取直接调用。可能影响准备阶段的写 batch、flush/compaction 后续状态，或与测量窗口并行的其他真实提交所争用的 CPU、内存、磁盘。runtime loads 和 membership writes 为 0 不能推出所有 metadata 写入、所有后台存储工作都为 0。若窗口内无提交且后台存储已平稳，本项没有已证实的直接请求开销。相关 options/compat 中 500µs 描述的差异为注释，不是默认时长改动。

### 3. Commit histogram 原子快照及复制（`1592f9025`）

- `pkg/metrics/storage.go:455`–`:465` 构造新快照 collector；`:622`–`:631` 在同一互斥锁内更新 commit histogram。
- `pkg/metrics/storage_commit_snapshot.go:41`–`:47` 在 Collect 中持锁执行内部 registry Gather，`:54`–`:56` 交付快照，`:70`–`:71` 再通过 `proto.Clone` 写入 DTO。

每次 metrics 抓取的分配/CPU 路径有实际变化，若同时存在提交还可能产生锁争用。但本次 `mixed_attribution` 夹具的测量边界是：

- warmup 完成后，`test/e2e/message/conversation_qps/stress_test.go:97`–`:103` 先 `snapshot` 再 `diagnosticMetrics`；
- 中间 `:146`–`:172` 只运行两种 endpoint 请求；
- 请求结束后 `:106`–`:112` 再 `snapshot` / `diagnosticMetrics`，以两次 snapshot 的累计值计算 CPU/allocated bytes。

因此该差异可能进入 CPU/alloc 差值的边界开销，**不能直接作为请求窗口内 P99 差异的解释**。开始前抓取对后续 GC 的残留影响仍需证据；此审计不把它当作已发生事实，也不假定存在未观测到的并发外部 scraper。

## SEND、冷启动或其他入口上的差异

| 差异 | 证据（b469） | 对本窗口的判断 |
| --- | --- | --- |
| SEND permission cohort（`82d0ac8e5`）与执行门限（`bd98885f4`） | `pkg/slot/proxy/send_permission_cohort.go:13`–`:20` 的 1ms/有界配额；`:55` 入口、`:182` 按需启动 cohort、`:232` 执行；`send_permission_rpc.go:27` execution 上限从 64 调到 128 | SEND 调用入口为 `pkg/cluster/node_send_ban.go:41`、`:56` 及 `internal/infra/cluster/channel_metadata.go:330`。Conversation metadata 在 `pkg/cluster/node.go:1329` 调用原 `ReadPermissionMetadataBatch`，不会调用新 SEND cohort。此窗口无 SEND 时没有直接 cohort 等待/去重开销；准备阶段或另有并发 SEND 才可能有共享影响。 |
| 初次频道元数据建批等待 500ms→20ms（`a16218b08`） | `pkg/cluster/channels/meta_create_batcher.go:25`–`:30` | 影响冷频道初次 SEND 建立元数据及 fixture 准备时间。既有频道 persisted heads 读取不创建元数据，不能据此解释当前只读窗口的直接耗时。 |
| Manager runtime observation（`ea0672388`） | `internal/usecase/management/channel_runtime_meta.go:214`–`:230`；`channel_observation.go:39`–`:47`；`pkg/cluster/channels/runtime_observation.go:33`、`:61`–`:103` | 新 Manager 观察会读 metadata、探测现有 runtime，并可读取尾序号，且不激活 runtime。它属于另一入口；当前窗口只有两个 conversation endpoint，不走该观察逻辑。若其他场景同时轮询 Manager，不能仅以 runtime loads 为 0 排除其工作量。 |
| Chat-lifecycle pacing/preflight（`93031a3bf`、`9f3d4a192`） | `internal/bench/chatlifecycle/engine.go` 的 `admitGrantedTraffic`，以及同包 worker/preflight 变更 | 由 chat-lifecycle workload 驱动，conversation QPS fixture 不执行该流量生成器；不能直接归入此次 endpoint 请求成本。 |
| Demo/UI/文档/注释及管理观察 wiring | 差异清单中的相应路径 | 未发现其进入当前两条 endpoint 调用链的证据。此分类不声称二进制、运行环境或准备状态完全等价。 |

## 已核对的不变路径及限制

两版本对以下路径的 `git diff --name-only` 为空：`internal/usecase/conversation/`、`internal/infra/cluster/conversation.go`、`internal/infra/cluster/persisted_message_reader.go`、`pkg/cluster/node.go`、`pkg/cluster/node_message_update.go`、`pkg/cluster/channels/service.go`、`pkg/cluster/channels/persisted_messages.go`、`pkg/slot/proxy/message_update.go`、`pkg/slot/proxy/membership_rpc.go`、`test/e2e/message/conversation_qps/`、`go.mod`、`go.sum`。

这只证明这些源文件无直接差异，**不证明性能等价**：它们会调用发生变化的通用 Slot 实现，且同一进程仍共享存储、调度和 GC。静态证据目前支持把 ReadIndex 合并列为首要直接候选，把 commit batching 列为条件性资源候选，把 metrics 快照区分为边界统计影响；尚不能把任何一个定性为回归原因。
