# Channel Message Retention Design

## 概述

本设计为频道消息增加自动清理能力，例如配置 `7d` 后自动清理 7 天以前的消息。清理语义不是节点本地独立 TTL，而是基于频道 ISR 复制语义的日志前缀压缩：当前 channel leader 计算一个单调递增的清理边界，先把边界提交为权威集群状态，再由各节点按该边界执行本地物理删除。

本项目没有独立的单机语义；单节点部署也按单节点集群处理。因此消息 retention 必须遵守 channel leader、ISR、slot metadata、checkpoint、committed replay 的一致性约束，不能绕过集群语义直接删除 Pebble 数据。

## 目标

- 支持按保留时长自动清理频道消息，例如保留最近 7 天消息。
- 只清理每个频道的连续过期前缀，不在 message sequence 中间挖洞。
- 确保 leader 和 follower 对消息可见边界一致，leader 切换后边界不回退。
- 清理时同步维护 message 主记录、payload、二级索引、幂等索引和 checkpoint 边界。
- 避免删除尚未完成提交副作用重放的消息。
- 默认关闭，启用后可通过配置控制扫描周期、批量大小和单轮删除上限。

## 非目标

- 不在首版提供冷归档或历史消息恢复能力。
- 不在首版支持按 channel 单独配置 retention policy。
- 不让 follower 基于本地时间独立计算清理边界。
- 不清理非连续前缀的过期消息。
- 不保证 retention 窗口之外的发送幂等长期有效；首版中消息清理窗口也是消息幂等索引保留窗口。

## 方案比较

### 方案 A：本地定时 DeleteRange

每个节点自己扫描本地 Pebble message 表，并删除过期消息。

优点：实现最快。

缺点：会绕过 channel ISR 复制语义，容易造成 leader/follower 边界不一致、message sequence 空洞、索引悬挂、committed replay 丢来源，以及 leader 切换后旧消息重新可见。

结论：不采用。

### 方案 B：Leader 驱动的 retention boundary（推荐）

当前 channel leader 扫描本地消息，计算一个安全清理边界 `RetentionThroughSeq`，将该边界写入权威 channel metadata。各节点只应用权威边界，不独立做 TTL 判断。

优点：符合集群语义；边界单调、可恢复、可观测；能与现有 `LogStartOffset`、checkpoint、committed replay 模型对齐。

缺点：需要新增 metadata 字段、store 前缀压缩能力、runtime 应用边界能力和后台 worker。

结论：采用。

### 方案 C：归档后再清理

先将过期前缀写入对象存储或冷库，确认归档成功后再推进清理边界。

优点：可以释放本地热存储，同时保留历史恢复能力。

缺点：引入归档一致性、回查 API、失败补偿和运维成本，超出首版目标。

结论：作为后续扩展，不进入首版。

## 核心语义

每个频道维护一个单调边界：

```text
RetentionThroughSeq = N
MinAvailableSeq = N + 1
```

含义：

- `<= N` 的消息已经被集群确认可以清理。
- 服务端不再对外返回 `<= N` 的消息。
- 本地物理删除可以滞后，但读取路径必须按该边界裁剪。
- 未来 leader 必须继承该边界，不能回退。

首版只清理连续过期前缀。leader 从 `LogStartOffset + 1` 开始顺序扫描 message 表，直到第一条未过期消息或达到单轮扫描上限。只有这一段连续前缀可以成为候选清理范围。

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

`resolveMonotonicChannelRuntimeMeta` 必须保证 `RetentionThroughSeq` 只能前进不能回退。对于 stale channel epoch、stale leader epoch 或 leader 不匹配的写入，不能覆盖当前权威状态。

### 安全边界计算

Leader 计算的最终边界为：

```text
safeThroughSeq = min(
  expiredThroughSeq,
  leaderCommittedHW,
  committedReplayCursor,
  minISRMatchOffset,
)
```

约束含义：

- `expiredThroughSeq`：只清理连续过期前缀。
- `leaderCommittedHW`：不能删除未提交消息。
- `committedReplayCursor`：不能删除尚未完成 delivery/conversation 重放的消息。
- `minISRMatchOffset`：首版只清理当前 ISR 都已经追上的前缀，避免 follower 落后于新的 `LogStartOffset`。

如果 `safeThroughSeq <= current RetentionThroughSeq`，本轮不做任何修改。

### 受控应用

各节点通过 channel replica/runtime 的受控接口应用权威边界，例如：

```text
ApplyRetentionBoundary(boundary)
```

该接口必须在 replica loop 或 durable lock 下串行执行，并满足：

- boundary 小于等于本地 `HW` 或本地 `LEO`。
- boundary 大于等于当前 `LogStartOffset`。
- 重复应用同一 boundary 是 no-op。
- 写入成功后再发布新的 runtime state。

本地物理删除必须在一个 Pebble batch 中完成：

- 删除 message primary family。
- 删除 message payload family。
- 删除 `message_id` index。
- 删除 `client_msg_no` index。
- 删除 `(from_uid, client_msg_no)` idempotency index。
- 更新 checkpoint 的 `LogStartOffset`。

## 读取语义

`Fetch` 和旧版 `messagesync` 都必须感知最小可用序列。

- `FromSeq == 0` 时，从 `max(LogStartOffset + 1, RetentionThroughSeq + 1, 1)` 开始。
- `FromSeq <= RetentionThroughSeq` 时，clamp 到 `RetentionThroughSeq + 1`。
- 返回结果应包含当前 committed frontier；后续可扩展返回 `MinAvailableSeq`，方便客户端修正本地游标或展示“历史已清理”。

首版可以保持兼容返回空页或从新边界返回数据，但内部必须明确区分“没有消息”和“请求低于 retention 边界”。

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

### `pkg/channel/store`

新增 message retention store 原语：

- 扫描连续过期前缀。
- 查询或加载当前 `LogStartOffset`。
- 原子删除前缀消息和索引。
- 单调更新 checkpoint `LogStartOffset`。

这些原语只处理本地持久化，不负责判断谁是 leader，也不独立计算集群安全边界。

### `pkg/channel/replica` 和 `pkg/channel/runtime`

新增 retention boundary 应用路径：

- Leader 提供当前 ISR match offset 视图给上层计算安全边界。
- Replica 接收已提交的权威 boundary，并在本地有序应用。
- Runtime 状态暴露新的 `LogStartOffset` / `RetentionThroughSeq`，供读取路径和管理接口使用。

### `internal/runtime/channelretention`

新增后台 worker：

- 周期扫描频道。
- 只在当前节点是 channel leader 时尝试计算边界。
- 读取 committed replay cursor 和 ISR progress，计算 `safeThroughSeq`。
- 通过 slot metadata 提交权威 boundary。
- 记录 blocked reason，不直接绕过 runtime 删除数据。

### `internal/app`

在组合根中装配 retention worker。生命周期建议在 committed replay 之后启动，在 channel cluster 关闭之前停止，避免清理 worker 删除 committed replay 仍需读取的消息。

## 故障场景

- Leader 提交 boundary 后崩溃，但本地尚未删除：新 leader 从 slot metadata 读到 boundary 后继续应用，语义不回退。
- Follower 删除成功但 leader 崩溃：boundary 已经是权威状态，未来 leader 不应再服务旧消息。
- Follower 暂时未删除：读取路径按权威 boundary 裁剪，本地多留数据只影响空间，不影响语义。
- 旧 leader 网络分区后继续清理：提交 boundary 时会被 leader epoch 或 lease fence 拒绝。
- 非 ISR 老副本重新加入：先读取权威 boundary 并应用，再从新边界之后追赶；不能凭旧数据成为 leader。

## 观测

建议新增日志和指标：

- retention pass 次数、耗时和结果。
- 每轮扫描频道数。
- 删除消息数和删除字节估算。
- 当前 `RetentionThroughSeq` / `MinAvailableSeq`。
- blocked reason：not leader、no expired prefix、committed replay lag、ISR lag、lease expired、store error。

Manager 后续可展示每个频道的最小可用序列、最近清理时间和最近 blocked reason。

## 测试计划

单元测试：

- `pkg/channel/store` 前缀删除会同时删除主记录、payload 和所有索引。
- checkpoint `LogStartOffset` 只能前进，不能回退。
- 重复应用同一 boundary 是 no-op。
- 扫描连续过期前缀遇到第一条未过期消息停止。

Replica/runtime 测试：

- leader 和 follower 应用同一 boundary 后读取结果一致。
- boundary 大于本地 HW 或 LEO 时拒绝或延迟应用。
- leader 切换后 `RetentionThroughSeq` 不回退。
- runtime status 暴露正确 `LogStartOffset`。

App 级测试：

- retention worker 不删除 committed replay cursor 之后的消息。
- 单节点集群启用 TTL 后能推进边界并清理消息。
- 多节点下 follower 未及时物理删除时，读取仍按权威边界裁剪。

集成测试：

- 多节点发送消息、等待过期、触发 retention、切换 leader，再验证旧消息不可见且新消息可继续复制。

## 后续扩展

- 引入冷归档后再推进 retention boundary。
- 增加幂等 tombstone 表，让发送幂等窗口独立于消息保留窗口。
- 支持按 channel type 或单 channel 配置 retention policy。
- 当 snapshot install 完整后，放宽 `minISRMatchOffset` 限制，允许慢 follower 通过 snapshot 或 boundary catch-up 恢复。
