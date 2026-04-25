# Channel Long Poll Replication Design

## 概述

本设计面向 `pkg/channel` 当前 steady-state 复制路径，目标是在不放宽默认 quorum `sendack` 语义、不破坏当前 `MinISR` quorum commit 与 `reconcile` 正确性的前提下，去掉独立 `ProgressAck` 通路，改造成面向大量频道、少量热点频道高频发送场景的 `peer` 级批量长轮询复制架构。

本轮设计约束来自已确认的需求：

- 默认 `sendack` 仍然等 quorum commit，再返回给客户端
- 允许少数频道或消息类型选择 leader-local ack 快路径
- steady-state 复制采用 `peer` 级批量长轮询，而不是 `channel` 级单独 waiter
- 设计必须考虑大量频道，但热点主要集中在少量频道上
- 本轮不考虑 mixed-version / mixed-protocol 兼容性

本设计推荐将当前：

- `Fetch`
- `FetchBatch`
- `ProgressAck`

三段式 steady-state 复制协议，收敛为一个新的 `LongPollFetch` 主协议，并引入固定 `N lane/peer`、`session + delta`、`ready queue` 事件驱动模型；`ReconcileProbe` 继续保留为独立恢复路径，不并入 steady-state 复制。

## 背景与现状

### 当前复制与提交路径

当前 `sendack` 路径仍然是：

1. gateway 收到发送请求后调用 `messages.Send(...)`，只有该调用返回后才回 `sendack`
2. `handler.Append(...)` 做元数据校验、幂等检查和编码
3. leader `replica.Append(...)` 做 group commit、本地 durable append、注册 waiter
4. follower 收到 fetch response 后本地 durable apply
5. follower 再通过独立 `ProgressAck` RPC 向 leader 回报 `MatchOffset`
6. leader 更新 progress，推进 `HW`，waiter 完成，`sendack` 返回

相关代码：

- `internal/access/gateway/frame_router.go`
- `internal/usecase/message/send.go`
- `pkg/channel/handler/append.go`
- `pkg/channel/replica/append.go`
- `pkg/channel/replica/progress.go`
- `pkg/channel/runtime/backpressure.go`
- `pkg/channel/transport/session.go`

### 当前架构的主要问题

1. **独立 `ProgressAck` 增加了一跳主路径**
   - follower 拿到数据并 durable apply 后，还要额外发一次 ACK RPC，leader 才能推进 quorum commit
   - 这使 `sendack` 临界路径额外依赖一条单独的网络往返

2. **steady-state 主复制节奏仍由 retry 驱动**
   - 当前空闲后 follower 复制回路主要依赖 `FollowerReplicationRetryInterval`
   - 默认值为 `10ms`，导致空闲后首条消息天然容易吃到一个 retry gap

3. **当前 transport 更偏短批量 RPC，不是真正的长轮询**
   - `peerSession` 目前有 `200us` batching，但 fetch/batch/ack 仍是短请求语义
   - steady-state 下没有一条常驻挂起的复制请求

4. **单 peer 路径在热点场景容易队头阻塞**
   - 当前文档明确写到 V1 每个 peer 最多一个 in-flight fetch RPC
   - 在“大量频道 + 少量热点频道”场景下，热点频道容易拖住同一 peer 下其他频道

### 与 Kafka 的对比结论

Kafka 在 steady-state 复制上采用的是：

- follower 持续 `Fetch`
- 下一次 `Fetch` 本身携带新的 `fetchOffset`，等价于 ACK 回传
- `num.replica.fetchers` 提供固定数量的 fetch lane
- 每条 lane 批量携带多个 partition
- `FetchSession` 缓存上下文，只在 steady-state 发送增量

相关参考：

- `learn_project/kafka/server/src/main/java/org/apache/kafka/server/config/ReplicationConfigs.java`
- `learn_project/kafka/core/src/main/scala/kafka/server/ReplicaFetcherManager.scala`
- `learn_project/kafka/core/src/main/scala/kafka/server/AbstractFetcherManager.scala`
- `learn_project/kafka/core/src/main/scala/kafka/server/RemoteLeaderEndPoint.scala`
- `learn_project/kafka/server/src/main/java/org/apache/kafka/server/FetchSession.java`
- `learn_project/kafka/server/src/main/java/org/apache/kafka/server/FetchContext.java`

本设计并不照搬 Kafka 的 ISR 语义；`pkg/channel` 当前的提交语义仍保留为 `Meta.ISR + MinISR` 下的 quorum commit。但在复制通道组织方式上，应明确向 Kafka 的 `fixed fetch lanes + long poll + session/delta` 靠拢。

## 目标

- 去掉 steady-state 独立 `ProgressAck` 协议
- 默认 `sendack` 仍然等 quorum commit 完成
- 引入 `peer` 级批量长轮询，空闲后首条消息不再由 `10ms retry` 驱动
- 在大量频道、少量热点频道高频发送场景下避免全量扫描和单 peer 队头阻塞
- 保留当前 `MinISR` quorum commit、`CommitReady`、`reconcile` 的正确性模型
- 使 steady-state 复制资源规模与 `peer * lane` 成正比，而不是与 `channel` 数量成正比

## 非目标

- 不改变默认 quorum `sendack` 语义
- 不移除 `ReconcileProbe` 或把恢复路径并入 steady-state 协议
- 不实现 mixed-version / mixed-protocol 兼容
- 不实现动态 lane 扩缩或热点频道迁移
- 不在第一版引入 per-channel waiter / per-channel timer / per-channel goroutine
- 不改变 gateway / usecase / handler 对外语义

## 方案对比

### 方案 A：`1 lane/peer + batch long poll`

将当前 `peer` 下的 steady-state fetch、batch、ack 路径收敛为单条批量长轮询管道。follower 在 apply 完响应后立刻重发下一条 long poll，请求里带 cursor delta，作为 ACK 回传。

优点：

- 改动最小
- 最容易替换当前 `ProgressAck` 路径
- 能立刻去掉 `10ms retry` 的空闲主时钟

缺点：

- 单条 lane 仍然承载同一 peer 下所有热点与普通频道
- 热点频道导致的队头阻塞问题依然严重
- 不适合“大量频道 + 少量热点很热”的场景

### 方案 B：`fixed N lane/peer + batch long poll + session/delta`（推荐）

每个 `leader <-> follower` 对固定维护 `N` 条 lane。频道按 `hash(channelKey) % N` 映射到某条 lane。每条 lane 常驻一条 long poll，请求中带 `cursorDelta` 作为 ACK 回传；steady-state 下仅发送 membership/version hint 与 cursor 增量。

优点：

- 与 Kafka 的复制组织方式一致
- 能同时解决独立 ACK、多频道长轮询、热点频道队头阻塞问题
- 资源规模与 `peer * lane` 成正比，适合大量频道
- 能复用现有 `MinISR` / `advanceHW` / waiter 完成逻辑

缺点：

- 需要引入 lane/session 状态机
- `runtime` 与 `transport` 改动较大

### 方案 C：动态 lane / 热点 lane 迁移

在方案 B 基础上，根据 lane 热度自动扩缩 lane 数，或将超热点频道迁到专用 lane。

优点：

- 理论资源利用率更高
- 可进一步降低热点场景下的局部阻塞

缺点：

- lane 迁移、session 重建、顺序一致性、排障复杂度都很高
- 第一版风险明显过大

## 推荐方案

选择 **方案 B**。

推荐参数：

- 第一版固定 `8 lane/peer`
- `laneCount` 配置化，便于压测后提升到 `16`
- steady-state `maxWait` 推荐从 `1ms~2ms` 起步，而不是沿用 Kafka 默认 `500ms`
- `FollowerReplicationRetryInterval` 保留，但仅作为网络异常恢复退避，不再作为 steady-state 主复制时钟

## 设计

### 1. 端到端时序

默认 quorum `sendack` 模式下：

1. gateway 收到消息并调用 `messages.Send(...)`
2. `handler.Append(...)` 进入 leader `replica.Append(...)`
3. leader 做 group commit、本地 durable append，注册 waiter，目标为某个 `target offset`
4. leader 将该频道标记为对应 follower lane 的 `ready`
5. follower 对 leader 的该 lane 常驻一条 long poll；leader 发现有 ready item，立即返回
6. follower durable apply 响应中的 records / truncate / HW 变化
7. follower 不再单独发送 `ProgressAck`，而是立刻重发同一 lane 的下一条 long poll
8. 新请求头部携带 `cursorDelta`，表明 follower 在该 lane 下哪些频道已经 durable 到哪里
9. leader 收到该请求后，先应用 `cursorDelta`，更新 progress，尝试推进 `HW`
10. 一旦 `HW >= waiter.target`，waiter 完成，`messages.Send(...)` 返回，gateway 写 `sendack`

即：

- 现在：`ApplyFetch -> ProgressAck -> leader advanceHW -> sendack`
- 新方案：`ApplyFetch -> next LongPollFetchRequest(cursorDelta) -> leader advanceHW -> sendack`

在 `MinISR=2`、三节点场景下，这保留了当前“leader + 任意一个 follower durable 即可 quorum commit”的语义。

#### leader-local ack 快路径

对允许快速回 ACK 的频道或消息类型，新增显式 `completion mode = local`：

- leader 本地 durable append 成功后即可完成 waiter 并返回 `sendack`
- 后台复制仍走同一套 lane long poll
- 此模式不得隐式由 steady-state 异常触发；必须显式 opt-in

### 2. `fixed N lane/peer` 与状态模型

#### lane 定义

- `lane` 是复制并发隔离单元，不是业务单元
- `laneID = hash(channelKey) % laneCount`
- 同一频道永远走同一条 lane
- 这样既保证单频道顺序稳定，又能把热点频道散列到不同 lane

#### follower 侧状态：`PeerLaneManager`

每个 leader peer 下维护固定 `N` 条 `LaneState`，每条 lane 至少包含：

- 身份：`peerID`、`laneID`、`sessionID`、`sessionEpoch`、`protocolVersion`
- 成员视图：当前该 lane 负责的频道集合
- 复制游标：`appliedCursor[channelKey] = {channelEpoch, matchOffset, offsetEpoch}`
- 请求状态：当前是否有挂起中的 long poll、最近一次 request/response 时间、是否需要 reset
- 脏集合：`dirtyCursorChannels`

steady-state 下 follower 只发送脏频道的 cursor delta，而不是发送全量 lane 游标表。

#### leader 侧状态：`LeaderLaneSession`

每个 `(peer, laneID)` 维护一个 `LeaderLaneSession`，至少包含：

- 会话身份：`sessionID`、`sessionEpoch`、`lastUsedAt`
- 成员快照：`sessionChannels`
- follower progress：`followerProgress[channelKey]`
- `readyFlags[channelKey]`
- 去重 FIFO `readyQueue`
- 当前挂起的 `parkedRequest`
- 当前 lane 的预算：`maxWait`、`maxBytes`、`maxChannels`

#### session + delta 的必要性

大量频道场景下，不允许每轮 long poll 都发送：

- 全量频道列表
- 全量频道游标

steady-state 协议必须以：

- `membershipVersionHint`
- `cursorDelta[]`

为主，仅在 `open/reset` 时允许发送 full snapshot。

### 3. 唤醒机制：按事件直达 lane，不做全量扫描

#### 频道事件直达 `replicationTargets`

每个 leader channel 预先缓存一份轻量复制目标表：

- `replicationTargets = []PeerLaneKey`

频道一旦出现：

- `DataReady`
- `HWReady`
- `TruncateReady`
- `SessionReset`

等复制相关事件，只需逐个向其 `replicationTargets` 投递事件，不需要扫描所有 session 或所有频道。

#### lane 只维护 ready 集，不扫描成员全集

每条 `LeaderLaneSession` 维护：

- `readyFlags[channelKey] -> bitmask`
- `readyQueue`（去重 FIFO）

热点频道如果短时间内连续 append 多次，只做 ready 原因位合并，不重复入队。leader 收到请求后，只从 `readyQueue` 中 drain 可返回的频道，不扫描 lane 下全部频道。

#### parked request 唤醒

每条 lane 同时最多只有一条挂起的 long poll。

- 若请求到达时 lane 没有 ready item，则存入 `parkedRequest`
- 任一频道事件投递到该 lane 并使其变成 ready 时，若存在 `parkedRequest`，立即唤醒该 lane 返回

资源规模由此固定在：

- `peer * lane`

而不是：

- `channel`

### 4. 协议设计：`LongPollFetch`

建议将 steady-state 复制收敛为一个新 RPC 服务：`LongPollFetch`。

#### 请求结构

- 会话头：
  - `peerID`
  - `laneID`
  - `sessionID`
  - `sessionEpoch`
  - `op = open | poll | close`
  - `protocolVersion`
- 预算：
  - `maxWaitMs`
  - `maxBytes`
  - `maxChannels`
- 成员视图：
  - steady-state 只带 `membershipVersionHint`
  - `open/reset` 才带 `fullMembershipSnapshot`
- `cursorDelta[]`：
  - `channelKey`
  - `channelEpoch`
  - `matchOffset`
  - `offsetEpoch`
- 能力位：
  - `supportsQuorumAck`
  - `supportsLocalAck`

#### 响应结构

- 会话结果：
  - `status = ok | need_reset | stale_meta | not_leader | closed`
  - `sessionID`
  - `sessionEpoch`
- lane 结果位：
  - `timedOut`
  - `moreReady`
  - `resetRequired`
- `items[]`：
  - `channelKey`
  - `channelEpoch`
  - `leaderEpoch`
  - `flags = data | truncate | hw_only | reset`
  - `records`
  - `leaderHW`
  - `truncateTo`

#### 请求处理顺序

leader 收到请求后的处理顺序必须固定为：

1. 校验 session / lane / protocol
2. 吸收 membership / open state
3. 应用 `cursorDelta`
4. 更新 follower progress，推进 `HW`
5. 再从 `readyQueue` 中选 item 返回
6. 若无 item，则 parked 直到 `maxWait` 或新事件到达

换言之：

- `LongPollFetch` 先当 ACK 用，再当 Fetch 用

#### 空响应语义

空响应是正常 steady-state 结果，不代表错误。

- `timedOut=true` 只说明本次 `maxWait` 到了且没有可返回项
- follower 收到后应立刻续下一条 poll
- 即使空响应没有 records，也可能通过下一次请求中的 `cursorDelta` 起到 ACK 回传作用

### 5. 与现有代码的对接方式

#### 保留的核心模块

以下模块建议保留核心语义不变：

- `internal/access/gateway/frame_router.go`
- `internal/usecase/message/send.go`
- `pkg/channel/handler/append.go`
- `pkg/channel/replica/append.go`
- `pkg/channel/replica/progress.go`
- `pkg/channel/replica/reconcile.go`

commit 核心仍然是：

- leader durable append
- waiter 注册
- `advanceHW`
- `notifyReadyWaitersLocked()`

本轮改造只改变“谁来驱动 progress / HW 前进”，不改变 quorum commit 算法本身。

#### 需要替换式重构的模块

- `pkg/channel/runtime/replicator.go`
  - 从“短 fetch + retry”模型重构为 `PeerLaneManager`
- `pkg/channel/runtime/backpressure.go`
  - 去掉 steady-state `ProgressAck` 主路径
  - inflight / backpressure 单位改为 `lane`
- `pkg/channel/transport/session.go`
  - steady-state 主路径改为 `LongPollFetch`
- `pkg/channel/transport/transport.go`
  - `ProgressAck` 服务整体退役
- `pkg/channel/transport/codec.go`
  - 新增 long poll 协议编解码

#### 不建议强复用当前单频道 `Envelope`

当前 `runtime.Envelope` 心智模型偏单频道消息，而新 steady-state 协议是 `peer-lane` 批量 RPC。

不建议将 lane 协议强行塞入现有单频道 `Envelope`，否则会把：

- 单频道 generation 语义
- 批量 lane/session 语义

混成一个难以维护的抽象。

建议新增 lane 专用协议类型：

- `LanePollRequest`
- `LanePollResponse`
- `LaneCursorDelta`
- `LaneResponseItem`

#### `ApplyMeta` 作为 lane membership 驱动源

由于 `Replicas/ISR` 由控制面下发，lane membership 也必须由 `ApplyMeta` 驱动。

- follower 侧按最新 `Meta.Replicas/ISR` 决定自己对哪些 peer-lane 建立或移除 membership
- leader 侧根据最新 meta 更新 `replicationTargets`
- 频道删除、重建、leader 变化时，都通过 meta 变更触发 lane clean-up 或 reset

steady-state 协议因此只携带 version / delta，而不承担 authoritative membership 来源。

### 6. 异常与恢复路径

#### leader 切换

- 旧 leader 上所有 `(peer, laneID)` session 立即失效
- 新 leader 保持当前 `CommitReady=false -> reconcile -> CommitReady=true` 恢复语义
- 未 `CommitReady` 的频道不应混入 steady-state records 返回
- `ReconcileProbe` 继续作为独立恢复协议

#### session 失效 / reset

以下情况统一返回 `need_reset`：

- leader 重启
- lane 数配置变更
- `sessionEpoch` 不匹配
- leader 主动淘汰 session
- follower 检测到 membership 版本与 leader 不一致

follower 收到后：

- 清理 lane inflight 状态
- 保留本地 `appliedCursor`
- 发送 `open(full snapshot)` 重建 session

#### 热点 lane 拥堵

第一版不做动态 lane 迁移，而是依赖：

- `maxBytes`
- `maxChannels`
- `moreReady`
- item 预算耗尽后回队尾

来实现 lane 内切片返回和基本公平性。

#### 删除 / 重建 / stale item

必须依赖 `channelEpoch` 做 item 级隔离：

- 旧 session 中的频道项若 `channelEpoch` 不匹配，一律丢弃
- follower 带来的旧 `cursorDelta` 若 `channelEpoch` 落后，一律忽略

#### 网络错误与退避

- `maxWait` timeout 属于正常空响应，不进入退避
- 只有 RPC timeout / 连接错误才进入 `FollowerReplicationRetryInterval` 退避
- 该配置从 steady-state 主时钟降级为异常恢复时钟

### 7. 测试、指标与 rollout

#### 核心指标

建议在现有 `sendtrace` 基础上补充：

- gateway / sendack
  - `sendack_total_latency`
  - `sendack_quorum_wait_latency`
  - `sendack_local_durable_latency`
- leader lane
  - `lane_poll_inflight`
  - `lane_poll_parked`
  - `lane_poll_timeout_total`
  - `lane_ready_queue_depth`
  - `lane_more_ready_total`
- follower lane
  - `lane_cursor_delta_items`
  - `lane_poll_reissue_latency`
  - `lane_session_reset_total`
- replica commit
  - `hw_advance_latency`
  - `waiter_completed_total`
  - `waiter_timeout_total`

#### 第一版必须覆盖的 6 类测试

1. 三节点、单频道、quorum ack
2. 三节点、单频道、leader-local ack
3. 大量冷频道 + 少量热点频道
4. leader 切换
5. session reset / full open
6. 网络抖动 / 空响应 / timeout

#### 推荐 rollout 路径

- 先补当前 `ProgressAck` 路径的基线指标
- 新协议仅在测试环境启用，固定 `8 lane/peer`
- 做长时间 soak，重点看：
  - `session reset rate`
  - `lane timeout rate`
  - `ready queue depth`
  - `sendack p99`
- 生产按集群维度切换，不做 mixed protocol 混跑
- 保留进程级配置开关，例如：
  - `replication.mode = progress_ack | long_poll`

## 模块建议

建议新增：

- `pkg/channel/runtime/lanes.go`
- `pkg/channel/runtime/lane_session.go`
- `pkg/channel/runtime/lane_directory.go`
- `pkg/channel/transport/longpoll.go`
- `pkg/channel/transport/longpoll_codec.go`

建议逐步退役 steady-state 主路径中的：

- `pkg/channel/runtime/replicator.go`
- `pkg/channel/transport/session.go` 内的旧 fetch / fetchbatch 主路径
- `pkg/channel/transport/transport.go` 中的 `ProgressAck`
- `pkg/channel/transport/codec.go` 中的旧 `ProgressAck` / steady fetch batch 编码

## 第一版明确不做

- 动态 lane 扩缩
- 热点频道 lane 迁移
- mixed-version / mixed-protocol 兼容
- 将 `ReconcileProbe` 并入 steady-state 协议
- 复杂 lane 优先级调度
- per-channel parked request / per-channel timer / per-channel goroutine

## 结论

本设计保留：

- 当前 `Meta.ISR + MinISR` 下的 quorum commit 语义
- `CommitReady` / `reconcile` 的安全恢复模型
- `leader durable -> waiter -> HW advance -> sendack` 的提交主干

本设计替换：

- steady-state 独立 `ProgressAck`
- `10ms retry` 驱动的空闲复制主时钟
- `1 peer` 下单通道复制域的短 fetch 组织方式

最终 steady-state 架构将变成：

- `peer` 级批量长轮询
- fixed `N lane/peer`
- `cursorDelta` 替代独立 ACK
- lane session + ready queue 事件驱动
- `sendack` 默认仍在 quorum commit 完成后返回

该方案与 Kafka 的 steady-state 复制组织方式一致，但保留 `pkg/channel` 当前的 quorum commit 与恢复语义，适合大量频道、少量热点频道高频发送场景。
