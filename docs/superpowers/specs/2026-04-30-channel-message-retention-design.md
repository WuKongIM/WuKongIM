# Channel Message Retention Design

## 概述

本设计为频道消息增加自动清理能力，例如配置 `7d` 后自动清理 7 天以前的消息。清理语义不是节点本地独立 TTL，而是基于频道 ISR 复制语义的日志前缀 retention：当前 channel leader 计算一个单调递增的逻辑清理边界，先把边界提交为权威集群状态，再由各节点按该边界异步执行本地物理删除。

本项目没有独立的单机语义；单节点部署也按单节点集群处理。因此消息 retention 必须遵守 channel leader、ISR、slot metadata、committed replay 和本地存储恢复约束，不能绕过集群语义直接删除 Pebble 数据。

## 目标

- 支持按保留时长自动清理频道消息，例如保留最近 7 天消息。
- 只清理每个频道的连续过期前缀，不在 message sequence 中间挖洞。
- 确保 leader 和 follower 对消息可见边界一致，leader 切换后边界不回退。
- 将逻辑不可读边界和本地物理删除进度分离，允许删除滞后但不允许旧消息重新可见。
- 清理时同步维护 message 主记录、payload、二级索引、幂等索引和本地 retention bounds。
- 避免删除或隐藏尚未完成提交副作用重放的消息。
- 默认关闭，启用后可通过配置控制扫描周期、批量大小和单轮删除上限。

## 非目标

- 不在首版提供冷归档或历史消息恢复能力。
- 不在首版支持按 channel 单独配置 retention policy。
- 不让 follower 基于本地时间独立计算清理边界。
- 不清理非连续前缀的过期消息。
- 不保证 retention 窗口之外的发送幂等长期有效；首版中消息清理窗口也是消息幂等索引保留窗口。
- 不把 retention 物理删除建模为 snapshot install，也不复用 checkpoint `LogStartOffset` 表达 retention 边界。

## 方案比较

### 方案 A：本地定时 DeleteRange

每个节点自己扫描本地 Pebble message 表，并删除过期消息。

优点：实现最快。

缺点：会绕过 channel ISR 复制语义，容易造成 leader/follower 边界不一致、message sequence 空洞、索引悬挂、committed replay 丢来源，以及 leader 切换后旧消息重新可见。

结论：不采用。

### 方案 B：Leader 驱动的 retention boundary（推荐）

当前 channel leader 扫描本地消息，计算一个安全清理边界 `RetentionThroughSeq`，将该边界写入权威 channel metadata。各节点只应用权威边界，不独立做 TTL 判断。本地物理删除另有 `PhysicalRetentionThroughSeq` 进度，删除失败不会回退逻辑读边界。

优点：符合集群语义；边界单调、可恢复、可观测；能与现有 committed replay 和结构化 message 表对齐；不破坏 snapshot/checkpoint 语义。

缺点：需要新增 metadata 字段、store 前缀压缩能力、runtime 读边界能力、committed replay durable cursor gating 和后台 worker。

结论：采用。

### 方案 C：归档后再清理

先将过期前缀写入对象存储或冷库，确认归档成功后再推进清理边界。

优点：可以释放本地热存储，同时保留历史恢复能力。

缺点：引入归档一致性、回查 API、失败补偿和运维成本，超出首版目标。

结论：作为后续扩展，不进入首版。

## 水位定义

| 水位 | 所属层 | 含义 |
| --- | --- | --- |
| `RetentionThroughSeq` | 权威 slot metadata | 集群确认不可再对外读取的最高消息序号，单调递增。 |
| `MinAvailableSeq` | 运行时派生值 | 第一个可读消息序号；等于 `max(RetentionThroughSeq + 1, LogStartOffset + 1, 1)`，其中 `LogStartOffset` 只来自 snapshot。 |
| `LocalRetentionThroughSeq` | 本地 channel store / replica | 本节点已经持久采用的 retention floor；可在本地缺少旧日志时前进，用于重启后保持 LEO/read floor 不回退。 |
| `PhysicalRetentionThroughSeq` | 本地 channel store | 本节点已经物理删除到的最高消息序号，允许滞后于 `RetentionThroughSeq`，也允许低于 `LocalRetentionThroughSeq`。 |
| `LogStartOffset` | replica checkpoint/snapshot | snapshot-backed 复制日志下界；不是 retention 边界。首版 retention 不推进它。 |
| `HW` / `CommittedSeq` | replica runtime | 当前已提交消息水位。 |
| `CheckpointHW` | replica durable checkpoint | 已持久化的安全恢复水位。 |
| `LEO` | channel store / replica | 当前本地日志末端消息序号。前缀删除不能让它回退。 |
| `committedReplayCursor` | channel store system key | 已完成 delivery/conversation 补偿重放的消息序号；retention 依赖它时必须确保持久化。 |

## 核心语义

每个频道维护一个权威单调边界：

```text
RetentionThroughSeq = N
MinAvailableSeq = max(N + 1, snapshot lower bound, 1)
```

含义：

- `<= N` 的消息已经被集群确认不可再读。
- 服务端不再对外返回 `<= N` 的消息，即使本地物理删除尚未完成。
- 未来 leader 必须继承该边界，不能回退。
- 本地物理删除只负责释放空间和移除索引，不能决定逻辑可见性。

首版只清理连续过期前缀。leader 从当前逻辑可读起点开始顺序扫描 message 表，直到第一条未过期消息或达到单轮扫描上限。只有这一段连续前缀可以成为候选清理范围。TTL 的时间依据为 message row 的 server `Timestamp` 秒级时间；`Timestamp <= 0` 或无法解析的行不视为过期，扫描在该行停止。

## 一致性设计

### 单点决策

只有当前 channel leader 可以计算和提交 `RetentionThroughSeq`。Follower 不基于本地时间或本地数据独立判断过期消息。

Leader 提交边界时必须携带 expected channel epoch、leader epoch 和 lease fence，避免旧 leader 或 stale metadata 覆盖新状态。

### 权威提交

`RetentionThroughSeq` 写入 `pkg/slot/meta.ChannelRuntimeMeta`，由 slot Raft 持久化和复制。推荐新增字段：

```go
RetentionThroughSeq uint64
RetentionUpdatedAtMS int64
```

`resolveMonotonicChannelRuntimeMeta` 必须保证 `RetentionThroughSeq` 只能前进不能回退。对于 stale channel epoch、stale leader epoch 或 leader 不匹配的写入，不能覆盖当前权威状态。对于更高 channel epoch 的 metadata，也必须继承现有 `RetentionThroughSeq`，不能因拓扑变化把读边界重置为 0。

### 安全边界计算

Leader 先计算不含 replay cursor 的候选边界，再把当前 leader replay cursor 做 durable confirm，最后得到最终边界：

```text
candidateThroughSeq = min(
  expiredThroughSeq,
  leaderCheckpointHW,
  leaderCommittedHW,
  minISRMatchOffset,
)

durableCommittedReplayCursor = durableConfirmLeaderReplayCursor()

safeThroughSeq = min(
  candidateThroughSeq,
  durableCommittedReplayCursor,
)
```

该两阶段顺序避免用未持久化的 leader-local cursor 计算 retention，也允许 cursor 暂时落后时只把 boundary 推进到已重放并确认的部分。

约束含义：

- `expiredThroughSeq`：只清理连续过期前缀。
- `leaderCheckpointHW`：不能在 durable checkpoint 尚未覆盖前隐藏或删除消息。
- `leaderCommittedHW`：不能处理未提交消息。
- `durableCommittedReplayCursor`：不能处理尚未完成并持久确认 delivery/conversation 重放的消息。首版 committed replay 是 channel leader 权威执行，故这里使用当前 leader 的 durable cursor；如果未来允许 follower 独立执行 committed replay，该 gate 必须升级为当前 ISR 的 durable cursor 最小值。
- `minISRMatchOffset`：首版只清理当前 ISR 都已经追上的前缀，避免 follower 落后于新的读边界。

如果 `candidateThroughSeq <= current RetentionThroughSeq`，本轮无需 durable confirm cursor，也不提交新边界。如果 durable confirm 失败，或最终 `safeThroughSeq <= current RetentionThroughSeq`，本轮不提交新边界。

`minISRMatchOffset` 必须来自当前 leader epoch 内已观测到的 ISR fetch/cursor progress，不能直接复用 quorum HW，也不能把缺失 follower progress 默认当作 HW。未知或未观测到的 ISR 成员只能证明到当前 `RetentionThroughSeq`，从而阻止 retention 在该成员真正追上前继续前进。

### Committed Replay Cursor Durability

当前 committed replay cursor 是 `NoSync` duplicate-suppression hint。Retention 一旦依赖它，就必须把它升级为 durable safety gate。当前代码的 committed replay 只在 channel leader 上执行，followers 不作为提交副作用的独立权威。因此首版语义为：leader 在推进 retention 前必须 durable confirm 自己的 committed replay cursor；其他副本在应用权威 boundary 时必须 durable fast-forward 本地 committed replay cursor 到 `RetentionThroughSeq`，避免未来成为 leader 后尝试重放已不可读的前缀。

- 在提交新的 `RetentionThroughSeq` 前，worker 必须确认当前 leader 的 replay cursor 已经 `>= safeThroughSeq`。
- 当前 leader cursor 必须在提交 slot metadata `RetentionThroughSeq` 之前用 Sync durable confirm；本地/ follower 采用 boundary 时的 cursor fast-forward 发生在 metadata commit 之后，不能替代 leader 的 pre-commit confirm。
- 如果 leader cursor 缺失、无法 durable confirm，或确认后仍 `<= current RetentionThroughSeq`，本轮不得推进 retention boundary。
- 如果 leader cursor 低于 `candidateThroughSeq` 但高于当前 boundary，只能把 `safeThroughSeq` 限制到该 cursor，不能跳过未重放部分。
- 应用权威 boundary 的每个节点必须 durable advance 本地 cursor 至少到 `RetentionThroughSeq`，再允许 committed replay 从该 channel 继续运行。
- 重启后 committed replay 从 `max(cursor + 1, MinAvailableSeq)` 开始；若 cursor 丢失但权威 boundary 已推进，必须先 durable fast-forward cursor 到 `RetentionThroughSeq`，不能尝试读取 boundary 以下的消息。
- 如果未来改成 follower 也执行 committed replay side effects，则 `safeThroughSeq` 必须改为不超过当前 ISR 所有 replaying replicas 的 durable cursor 最小值。

### 逻辑读边界和物理删除分离

权威 `RetentionThroughSeq` 一旦通过 slot metadata 提交并被应用到 runtime，就立即成为读路径 fence。即使本地物理删除失败或滞后，所有读路径都必须按 `MinAvailableSeq` 裁剪。

本地持久采用 boundary 后推进 `LocalRetentionThroughSeq` 和本地 log bounds。物理删除成功后只推进 `PhysicalRetentionThroughSeq`，并删除 message rows 和 indexes。物理删除进度不得影响 `RetentionThroughSeq`，也不得让 `LEO` 回退。

### 本地物理删除

各节点通过 channel replica/runtime 的受控接口应用权威边界，例如：

```text
ApplyRetentionBoundary(boundary)
```

该接口分三步：

1. 在 replica loop 内单调发布 `RetentionThroughSeq` / `MinAvailableSeq`，使读路径立即裁剪旧消息。
2. 在 durable lock 下持久采用本地 retention floor：更新 `LocalRetentionThroughSeq`、本地 log bounds / LEO floor，并 durable fast-forward committed replay cursor 到 boundary。该步骤即使本地 `LEO < boundary` 也必须可执行。
3. 在 durable lock 下按安全条件尝试物理删除 `<= boundary` 的本地消息前缀。

逻辑发布遵循权威 slot metadata：只要 `boundary >= current RetentionThroughSeq`，本地 runtime 就必须先发布读 fence。持久采用 boundary 保证重启后 read floor、LEO floor 和 replay cursor 不回退。物理删除可以因为本地副本落后或 checkpoint 未追上而延迟重试，但不能阻止读 fence 生效。

物理删除安全条件：

- `boundary <= CheckpointHW`。
- `boundary <= HW`。
- `boundary <= LEO`；如果本地 `LEO < boundary`，执行 retention reset/catch-up floor，不执行物理删除。
- `boundary > PhysicalRetentionThroughSeq`，否则 no-op。
- 物理删除已完成后，重复应用同一 boundary 是 no-op；如果逻辑边界已应用但物理删除滞后，则允许重试物理删除。

本地物理删除必须在一个 Pebble batch 中完成：

- 删除 message primary family。
- 删除 message payload family。
- 删除 `message_id` index。
- 删除 `client_msg_no` index。
- 删除 `(from_uid, client_msg_no)` idempotency index。
- 更新本地 retention bounds，例如 `LocalRetentionThroughSeq`、`PhysicalRetentionThroughSeq` 和 `MaxSeq/LEO floor`。

Retention 不更新 checkpoint `LogStartOffset`。现有 recovery validation 把非零 `LogStartOffset` 视为 snapshot-backed 下界；首版 retention 不创建 snapshot payload，因此不能复用该字段。为避免前缀删除所有 row 后 `LEO` 恢复为 0，或旧副本本地 `LEO` 落后于权威 boundary 后无法从 `boundary + 1` 继续追赶，channel store 需要新增本地 log bounds / retention state system key，并在 `LEO()` 恢复时取 `max(messageTable.maxSeq(), retainedMaxSeq)`。

当副本本地 `LEO < RetentionThroughSeq` 时，必须执行 retention reset/catch-up：持久设置 `LocalRetentionThroughSeq` 和 `retainedMaxSeq` 至 boundary，将运行时 `LEO` floor 提升到 boundary，读取仍从 `MinAvailableSeq` 开始，后续复制从 boundary 之后继续。该副本在完成 reset 并追上当前 leader 之前不得加入 ISR，也不得被提升为 leader。

### Retention Catch-Up Protocol

Retention catch-up 不是 snapshot install。复制请求或 long-poll 的 proof/fetch offset 如果低于 `RetainedThroughOffset = max(RetentionThroughSeq, LogStartOffset)`，处理规则为：

- 如果 `LogStartOffset > RetentionThroughSeq` 且需要 snapshot payload，继续使用现有 snapshot-required 语义。
- 如果 retention boundary 是主导原因，leader 返回明确的 retention reset 结果，例如 `ErrRetentionResetRequired`，并携带 `RetentionThroughSeq`、`RetainedThroughOffset` 和 `MinAvailableSeq`。
- Follower 收到 retention reset 后，先从权威 metadata 或响应中的 fence 校验 boundary，再执行本地 `AdoptRetentionBoundary(boundary)`，持久推进 `LocalRetentionThroughSeq`、`retainedMaxSeq` 和本地 committed replay cursor。
- Adoption 成功后，follower 将本地 `LEO` floor 至少提升到 boundary，下次复制从 `boundary + 1` 继续；追上当前 leader 前保持非 ISR 且不可成为 leader。
- Client/manager message reads 不返回 retention reset 错误，而是直接按 `MinAvailableSeq` clamp 或过滤。

## 读取语义

所有 committed message 读取路径都必须感知最小可用序列。

- `Fetch`：`FromSeq == 0` 或低于 `MinAvailableSeq` 时，从 `MinAvailableSeq` 开始。
- 旧版 `messagesync`：up/down/latest 都不得返回 `MinAvailableSeq` 以下的消息。
- 按 sequence 读取：`LoadMsg`、next/prev range helper 必须 clamp 或过滤到 `MinAvailableSeq`。
- 按 `MessageID` / `ClientMsgNo` 查询：即使命中本地未删除索引，若 row 的 `MessageSeq < MinAvailableSeq`，必须表现为未找到或被过滤。
- Manager/node 消息查询和远程读取接口必须复用同一裁剪逻辑。
- Committed replay 读取从 `max(cursor + 1, MinAvailableSeq)` 开始，不能读取 boundary 以下的消息。

`FetchResult` 和管理查询结果可扩展返回 `MinAvailableSeq`，方便客户端修正本地游标或展示“历史已清理”。

## 配置

新增应用配置，默认关闭：

```text
WK_CHANNEL_MESSAGE_RETENTION_TTL=0
WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL=1h
WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE=128
WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES=10000
```

字段语义：

- `WK_CHANNEL_MESSAGE_RETENTION_TTL`：消息保留时长。`0` 表示关闭自动清理。
- `WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL`：后台 worker 扫描周期。
- `WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE`：每轮最多处理的频道数。
- `WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES`：单频道单轮最多清理的消息数，限制单次 batch 大小和写放大。

配置字段需要加入 `internal/app.Config`、`cmd/wukongim/config.go` 和 `wukongim.conf.example`，并保留详细英文注释。

## 模块落点

### `pkg/slot/meta`、`pkg/slot/fsm`、`pkg/slot/proxy`

新增权威 retention boundary 字段和窄写命令：

- `ChannelRuntimeMeta.RetentionThroughSeq`。
- `ChannelRuntimeMeta.RetentionUpdatedAtMS`。
- `AdvanceChannelRetentionThroughSeq` 或等价窄命令，只推进 retention 字段，不重写 leader lease/topology。
- 该窄命令必须携带 `ChannelID`、`ChannelType`、`ExpectedChannelEpoch`、`ExpectedLeaderEpoch`、`ExpectedLeader`、`ExpectedLeaseUntilMS`、`RetentionThroughSeq` 和 `RetentionUpdatedAtMS`；任一 expected fence 不匹配时拒绝写入。
- 读 RPC 自动携带字段；写入必须走 slot Raft propose。

### `pkg/channel/store`

新增 message retention store 原语：

- 扫描连续过期前缀。
- durable confirm/advance committed replay cursor。
- 持久采用本地 retention floor / catch-up reset。
- 原子删除前缀消息和索引。
- 更新本地 retention/log-bounds system key，保证 LEO 不因全量前缀删除而回退。

这些原语只处理本地持久化，不负责判断谁是 leader，也不独立计算集群安全边界。

### `pkg/channel/replica`、`pkg/channel/runtime` 和 `pkg/channel/transport`

新增 retention boundary 应用路径：

- Leader 在 replica loop 内暴露 `CheckpointHW`、`HW`、`LEO`、`MinISRMatchOffset` 和当前 `MinAvailableSeq` 给 worker 计算安全边界。
- Replica 接收已提交的权威 boundary，先发布逻辑读 fence，再持久采用本地 retention floor。
- 本地 `LEO < RetentionThroughSeq` 时执行 retention reset/catch-up，并阻止该副本在追上前进入 ISR 或成为 leader。
- Fetch/long-poll 低于 retention floor 时返回 retention reset 结果，不返回稀疏记录；只有真正需要 snapshot payload 时才走 snapshot-required。
- Fetch response envelope、lane poll item 和节点间 transport codec 必须携带 retention reset 字段，确保远程 follower 能 durable adopt boundary 并从 `boundary + 1` 重试。

### `internal/runtime/channelretention`

新增后台 worker：

- 周期扫描频道。
- 只在当前节点是 channel leader 时尝试计算边界。
- 读取 ISR progress 并计算 `candidateThroughSeq`。
- durable confirm 当前 leader replay cursor，将最终 `safeThroughSeq` 限制到已持久确认的重放进度。
- 通过 slot metadata 提交权威 boundary。
- 记录 blocked reason，不直接绕过 runtime 删除数据。

### `internal/app`

在组合根中装配 retention worker。生命周期建议在 committed replay 之后启动，在 channel cluster 关闭之前停止，避免清理 worker 删除 committed replay 仍需读取的消息。

## 故障场景

- Leader 提交 boundary 后崩溃，但本地尚未删除：新 leader 从 slot metadata 读到 boundary 后继续应用，读语义不回退。
- Follower 删除成功但 leader 崩溃：boundary 已经是权威状态，未来 leader 不应再服务旧消息。
- Follower 暂时未删除：读取路径按权威 boundary 裁剪，本地多留数据只影响空间，不影响语义。
- 旧 leader 网络分区后继续清理：提交 boundary 时会被 leader epoch 或 lease fence 拒绝。
- 非 ISR 老副本重新加入：先读取权威 boundary，执行 retention reset/catch-up floor，再从新边界之后追赶；完成追赶前不能加入 ISR，也不能凭旧数据成为 leader。
- Cursor durable confirm 成功但 metadata commit 失败：只多持久化 replay cursor，不隐藏消息。
- Metadata commit 成功但物理删除失败：旧消息仍占空间，但所有读路径按 boundary 裁剪。

## 观测

建议新增日志和指标：

- retention pass 次数、耗时和结果。
- 每轮扫描频道数。
- 删除消息数和删除字节估算。
- 当前 `RetentionThroughSeq`、`MinAvailableSeq`、`LocalRetentionThroughSeq`、`PhysicalRetentionThroughSeq`。
- blocked reason：not leader、no expired prefix、checkpoint lag、committed replay lag、ISR lag、lease expired、store error。

Manager 后续可展示每个频道的最小可用序列、最近清理时间和最近 blocked reason。

## 测试计划

单元测试：

- `pkg/slot/meta` retention 字段持久化、解码缺省为 0、单调不回退。
- `pkg/slot/fsm` retention 窄命令和 runtime meta command round-trip。
- `pkg/channel/store` 前缀删除会同时删除主记录、payload 和所有索引。
- 本地 retention/log-bounds state 保证全量前缀删除后 LEO 不回退。
- leader durable committed replay cursor confirm 在 metadata boundary 前生效，followers 应用 boundary 时 durable fast-forward 本地 cursor。
- 重复应用同一 boundary 是 no-op。
- 扫描连续过期前缀遇到第一条未过期或 zero timestamp 消息停止。

Replica/runtime 测试：

- leader 和 follower 应用同一 boundary 后读取结果一致。
- boundary 大于 `CheckpointHW` 或 `HW` 时延迟物理删除但逻辑读 fence 生效；本地 `LEO` 低于 boundary 时执行 retention reset/catch-up。
- leader 切换后 `RetentionThroughSeq` 不回退，新 leader 从 `max(cursor + 1, MinAvailableSeq)` replay 且不会读取 retained prefix。
- runtime status 暴露正确 `MinAvailableSeq`。
- replication fetch 低于 retention floor 返回 retention reset 结果并携带 boundary，不返回稀疏记录。
- 本地 `LEO < RetentionThroughSeq` 的老副本通过 retention reset 从 boundary 后继续追赶，并在追上前保持非 ISR。

App 级测试：

- retention worker 不推进超过 durable committed replay cursor 的 boundary。
- 单节点集群启用 TTL 后能推进边界并清理消息。
- 多节点下 follower 未及时物理删除时，读取仍按权威边界裁剪。

集成测试：

- 多节点发送消息、等待过期、触发 retention、切换 leader，再验证旧消息不可见且新消息可继续复制。

## 后续扩展

- 引入冷归档后再推进 retention boundary。
- 增加幂等 tombstone 表，让发送幂等窗口独立于消息保留窗口。
- 支持按 channel type 或单 channel 配置 retention policy。
- 当 snapshot install 完整后，放宽 `minISRMatchOffset` 限制，允许慢 follower 通过 snapshot 或 boundary catch-up 恢复。
