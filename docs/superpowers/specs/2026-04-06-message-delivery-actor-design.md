# 2026-04-06 消息发送与异步投递 Actor 设计

## 概述

本设计将消息处理明确拆成两条链路：

- 发送链路：只负责校验、持久化写入、返回 `SendAck`
- 投递链路：异步负责订阅者解析、在线投递、`recvack` 跟踪、重试与退出实时投递

本设计选择“逻辑上每频道一个 actor，物理上由固定分片执行器承载的虚拟 actor”模型。频道 actor 的 owner 跟随该频道当前 `channellog leader`，从而保证同一频道内投递状态机是单写者。

## 背景与问题

当前 `internal/usecase/message.Send` 在 durable write 成功后立即执行 best-effort 本地/远端投递。这样有几个问题：

- 发送成功语义与投递成功语义耦合在同一个用例里
- 投递是同步后半段，链路边界不清楚
- `recvack` 还没有进入真正的投递状态机
- 群频道与个人频道缺少统一的订阅者解析模型
- 大规模频道下缺少明确的 actor 生命周期、热点隔离与内存上限

## 已确认约束

- 发送成功只以消息持久化成功为准
- 投递链路必须异步
- 实时投递完成条件是目标在线 route 返回 `recvack`
- route 断线后不继续绑定到新 session 进行实时重试，后续交给离线补消息
- 节点重启后不恢复内存中的投递状态，依赖客户端重连后的离线补消息
- 个人频道与群频道都统一视为频道，只是订阅者解析方式不同

## 目标

- 清晰拆分发送链路与投递链路
- 为每个频道建立串行投递状态机，避免同频道内并发修改投递状态
- 让 `recvack` 真正驱动投递完成
- 支持群频道的大规模订阅者分页解析与在线投递
- 明确集群 owner、远端投递、ack 回流、route 离线回报的职责
- 在大规模频道场景下保持有界内存与有界 goroutine

## 非目标

- 不在本轮设计中持久化投递任务与 ack 进度
- 不在本轮设计中实现离线补消息本身
- 不在本轮设计中做热点频道跨节点迁移
- 不在本轮设计中引入通用事件总线

## 核心设计

### 1. 两条链路

#### 发送链路

入口仍然落在 `internal/usecase/message`。

职责：

- 校验发送者与频道参数
- 执行 `channellog.Send`
- 返回 `SendResult`
- 在 durable write 成功后异步提交 `CommittedMessageEnvelope`

成功语义：

- 只要消息写入成功并获得 `message_id/message_seq`，发送链路即成功

不再负责：

- 直接做本地/远端在线投递
- 等待 `recvack`
- 重试

#### 投递链路

新增独立 `delivery` 用例与 `runtime/delivery` 运行时。

职责：

- 接收 `CommittedMessageEnvelope`
- 根据频道解析订阅者快照
- 批量查询在线路由
- 对在线 route 下发 `RECV`
- 跟踪 `recvack`
- 对未 ack 且仍在线的 route 执行退避重试
- 对离线 route 退出实时投递

### 2. 逻辑上每频道一个 actor

`ChannelActor` 的 key 为 `ChannelKey{channel_id, channel_type}`。

- 个人频道：也是频道 actor
- 群频道：也是频道 actor

区别只体现在订阅者解析：

- 个人频道：从兼容旧实现的 canonical personal `channel_id` 解析出两个 uid
- 群频道：从数据库分页查询订阅者

### 3. actor owner

每个频道 actor 的 owner 绑定到该频道当前的 `channellog leader`。

原因：

- 发送写入已经围绕频道 leader 线性化
- 投递 actor 跟随同一个 owner，频道内形成单写者
- 避免多个入口节点各自维护同一频道投递状态

actor 身份建议由以下字段组成：

- `channel_key`
- `leader_epoch`
- `actor_epoch`

所有跨节点提交、ack、offline 通知都必须带上这些 epoch。旧 owner 的迟到消息直接丢弃。

## 关键数据模型

### `CommittedMessageEnvelope`

发送链路在 durable write 成功后提交给 owner actor 的轻量消息信封。

建议字段：

```go
type CommittedMessageEnvelope struct {
    ChannelID   string
    ChannelType uint8
    MessageID   uint64
    MessageSeq  uint64
    SenderUID   string
    ClientMsgNo string
    Topic       string
    Payload     []byte
    Framer      wkframe.Framer
    Setting     wkframe.Setting
    MsgKey      string
    Expire      uint32
    StreamNo    string
    ClientSeq   uint64
    LeaderEpoch uint64
}
```

正常热路径下 actor 直接使用该 envelope 构造 `RECV`，不再为了每条消息回查 `channellog`。

### `InflightMessage`

actor 内部用于跟踪单条消息的实时投递状态。

建议字段：

```go
type InflightMessage struct {
    MessageID        uint64
    MessageSeq       uint64
    ResolveDone      bool
    NextCursor       string
    Routes           map[RouteKey]*RouteDeliveryState
    PendingRouteCnt  int
}
```

其中 `Routes` 只跟踪“发送时在线的 route”，不跟踪全量订阅者。

### `RouteKey`

route 必须绑定具体在线连接，而不是只绑定 uid。

```go
type RouteKey struct {
    UID       string
    NodeID    uint64
    BootID    uint64
    SessionID uint64
}
```

### `AckBinding`

目标网关节点用它把 `recvack` 回路由到 owner actor。

```go
type AckBinding struct {
    SessionID   uint64
    MessageID   uint64
    ChannelID   string
    ChannelType uint8
    OwnerNodeID uint64
    LeaderEpoch uint64
    ActorEpoch  uint64
    Route       RouteKey
}
```

建议本地索引：

- 主索引：`(session_id, message_id) -> AckBinding`
- 反向索引：`session_id -> []AckBinding`

## 订阅者解析

新增独立边界 `SubscriberResolver`，不混入 `presence`。

```go
type SubscriberResolver interface {
    BeginSnapshot(ctx context.Context, key channellog.ChannelKey) (SnapshotToken, error)
    NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) (uids []string, nextCursor string, done bool, err error)
}
```

### 个人频道

个人频道不走数据库成员表，而是使用 `PersonChannelCodec` 从 canonical `channel_id` 中解析出两个 uid。

canonical 规则必须兼容 [learn_project/WuKongIM/internal/options/common.go](/Users/tt/Desktop/work/go/WuKongIM-v3.1/learn_project/WuKongIM/internal/options/common.go) 里的：

- `GetFakeChannelIDWith(fromUID, toUID)`
- `GetFromUIDAndToUIDWith(channelId)`

设计假定：

- personal `channel_id` 使用 `uidA@uidB` 形式
- 两端 uid 的前后顺序按 `crc32(uid)` 比较决定，而不是按字典序
- `PersonChannelCodec.Encode(a, b)` 必须与 `GetFakeChannelIDWith(a, b)` 保持一致
- `PersonChannelCodec.Decode(channelID)` 必须与 `GetFromUIDAndToUIDWith(channelID)` 保持一致
- `@` 作为个人频道分隔符时，uid 本身不能再包含 `@`

如果现有入口层仍有“把对端 uid 直接塞进 `channel_id`”的兼容行为，则需要在进入 `message.Send` 前先归一化成 canonical personal `channel_id`。这是从当前代码现状推导出的兼容要求。

关于 `crc32` 冲突：

- 旧实现中，如果两个不同 uid 的 `crc32` 恰好相同，会打印 warning，然后退回 `toUID + "@" + fromUID`
- 新实现首版应保持同样行为，优先保证兼容，不要擅自引入新的 tie-break 规则

### 群频道

群频道 `BeginSnapshot` 必须拿到稳定读视图，避免翻页过程中成员变动导致单条消息的投递目标漂移。

actor 不保存全量成员，只保存：

- 当前分页 cursor
- 当前消息已展开出的在线 route

## 在线路由展开

建议在 `presence` 上新增批量查询接口，而不是对 uid 逐个查路由：

```go
type BatchRecipientDirectory interface {
    EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Endpoint, error)
}
```

单页处理流程：

1. actor 拉一页订阅者 uid
2. 批量查在线路由
3. 只保留 active route
4. 按 `(node_id, boot_id)` 分桶
5. 发送 `PushBatchRPC`
6. 为成功写出的 route 建立 `AckBinding`
7. 把下一页包装成新的 mailbox 事件

这保证了大群不会一次性把所有订阅者与路由都载入内存。

## actor 运行模型

### 虚拟 actor

不采用“每频道一个 goroutine”的字面模型，而采用：

- 逻辑上每频道一个 actor
- 物理上固定数量 `delivery shard workers`
- `channel_key` 一致性 hash 到 shard

这样可以保证：

- 同频道严格串行
- 不同频道并行
- goroutine 数量与分片数有关，而不是与频道总数线性相关

### actor 生命周期

- 没有未完成投递时 actor 不存在
- 首次收到该频道 envelope 时 materialize actor
- actor 空闲超时后驱逐
- driver 只保留活跃投递频道

### mailbox 事件

建议保留有限几类事件：

- `StartDispatch`
- `ContinueResolve`
- `RouteAcked`
- `RetryTick`
- `RouteOffline`
- `ActorIdleSweep`

每次只推进一小步状态，避免大群长时间霸占 shard。

## 顺序与补洞

owner actor 正常热路径直接消费 `CommittedMessageEnvelope`，避免每条消息都回查 `channellog`。

但 actor 仍需维护一个小型 `reorder buffer`：

- 收到的 `message_seq == nextDispatchSeq`：直接投递
- 收到的 `message_seq > nextDispatchSeq`：暂存，说明前面有 gap
- 只有出现 gap、actor 冷启动、leader 切换时，才调用 `channellog.Fetch` 做补洞或 catch-up

也就是说：

- 正常热路径：写后不读
- 异常/恢复路径：按需回查

## 集群协作

### 1. 提交链路

发送节点在 `message.Send` durable success 后：

- 若本节点是频道 owner，则本地调用 `delivery.Submit`
- 若不是 owner，则调用 `SubmitCommittedMessageRPC(ownerNode, envelope)`

owner actor 对 `message_seq` 幂等处理。

### 2. 下发链路

owner actor 按节点分桶发送 `PushBatchRPC`：

```go
type PushBatchRequest struct {
    ChannelID   string
    ChannelType uint8
    OwnerNodeID uint64
    LeaderEpoch uint64
    ActorEpoch  uint64
    MessageID   uint64
    MessageSeq  uint64
    Frame       []byte
    Routes      []RouteKey
}
```

目标节点收到请求后：

- 先校验 `BootID`
- 校验 `session_id -> connection`
- 校验 `uid/session/route state`
- 先注册 `AckBinding`
- 再写 `RECV`
- 写失败则撤销 binding

返回结果分三类：

- `acked-to-runtime`：已成功写到 session，等待客户端 `recvack`
- `retryable`：本次写失败，可重试
- `dropped`：route 已失效，不应再重试

### 3. ack 链路

`RecvAckCommand` 需要至少包含：

- `UID`
- `SessionID`
- `MessageID`
- `MessageSeq`

收到 `recvack` 后：

1. 目标网关节点使用 `(session_id, message_id)` 查 `AckBindingIndex`
2. 命中则转成 `AckNotifyRPC`
3. owner actor 把对应 route 标记为 `acked`
4. 清除该 route 的重试计划与 binding

无命中的 ack 直接忽略。

### 4. route 离线链路

目标节点本地 session close 时，通过反向索引查出所有未完成 binding，并发送 `RouteOfflineBatchRPC` 给 owner。

owner actor 收到后把 route 直接标为 `dropped`，停止实时重试。

这与“断线后后续交给离线补消息”的约束一致。

### 5. leader 切换

leader 切换后：

- 旧 owner actor `seal`
- 旧 epoch 的迟到 ack / offline / retry 事件全部丢弃
- 新 owner 收到新的 envelope 或新的提交后创建新 actor

本轮不恢复旧 owner 内存中的未完成实时投递，这符合“不持久化投递状态”的约束。

## 大规模频道治理

### 必须有界

建议首版就设置硬限制：

- `SubscriberPageSize`
- `PresenceBatchSize`
- `MaxInflightMessagesPerChannel`
- `MaxInflightRoutesPerChannel`
- `MaxMailboxDepthPerChannel`
- `RealtimeRetryMaxAge`
- `MaxRetryAttempts`

超过上限后：

- 消息 durable send 仍然成功
- 超限的实时 route 或旧 inflight message 直接降级退出实时投递
- 后续依赖离线补消息

### 热点频道独占 lane

首版不做跨节点迁移，但允许 owner 节点内将热点频道从普通 shard 提升到独占执行 lane：

- 不改变频道 owner
- 不破坏频道单写者
- 减少超级群拖垮普通频道的风险

## 重试调度

不允许：

- 每个 route 一个 goroutine
- 每个 route 一个 timer

建议 shard 级统一时间轮或最小堆：

- actor 为 route 记录 `attempt` 与 `nextRetryAt`
- 调度器到点后向 actor 投递 `RetryTick`

建议退避：

- `500ms`
- `1s`
- `2s`
- `5s`
- `10s` 封顶

再叠加少量 jitter。

停止条件：

- 收到 `recvack`
- route 离线
- `boot_id` 不匹配
- 超出最大实时重试时长
- 超出最大尝试次数

停止不等于发送失败，只表示退出实时链路。

## 完成语义

### 发送完成

以下条件成立即发送成功：

- `channellog.Send` 成功
- 获得 `message_id` 与 `message_seq`

### 单条消息的实时投递完成

以下条件同时成立：

- 该消息的订阅者分页解析完成
- 所有已进入实时投递的 route 都进入 `acked` 或 `dropped`

其中：

- `acked`：已收到客户端 `recvack`
- `dropped`：route 离线、boot 变化或超出实时重试预算

## 包结构建议

```text
internal/
  usecase/
    message/
      send.go                 durable send + submit envelope
      recvack.go              recvack -> ack binding lookup / forward
    delivery/
      app.go                  delivery usecase entry
      submit.go
      ack.go
      offline.go
  runtime/
    delivery/
      manager.go              shard 管理与 actor materialize/evict
      shard.go                固定 worker 分片
      actor.go                ChannelActor 状态机
      mailbox.go              actor mailbox
      retrywheel.go           shard 级统一重试调度
      ackindex.go             session/message -> actor binding
      types.go
  access/
    node/
      delivery_submit_rpc.go
      delivery_push_rpc.go
      delivery_ack_rpc.go
      delivery_offline_rpc.go
```

## 失败语义

### 必须返回给发送者的错误

- durable write 失败
- 频道元数据错误
- leader / stale meta 且刷新后仍失败

### 不应影响发送成功的错误

- owner actor mailbox 满但已明确降级为离线补消息
- 某个 route 的在线写失败
- 某个 route 未 ack
- route 断线

如果系统希望保守处理，也可以把“owner actor 不可达”视为内部错误；但那会把发送链路重新与实时投递耦合。基于本设计目标，建议 owner 提交采用异步失败记录，不反压 durable send 成功语义。

## 测试建议

至少覆盖以下场景：

- 发送成功仅依赖 durable write
- 同频道连续消息按 `message_seq` 串行投递
- 乱序 envelope 通过 reorder buffer 补洞后顺序投递
- 个人频道通过 personal channel codec 解析两个订阅者
- personal channel codec 与 `learn_project` 的 `GetFakeChannelIDWith/GetFromUIDAndToUIDWith` 保持兼容
- 群频道分页解析订阅者并批量查 presence
- 只跟踪在线 route，离线 uid 不进入 inflight
- `recvack` 通过 `AckBindingIndex` 命中正确 actor
- session close 导致 route 退出实时重试
- `boot_id` 变化导致旧 route 被丢弃
- shard 级统一重试调度正确退避
- 热点频道独占 lane 不影响普通频道吞吐
- leader 切换后旧 epoch 事件被丢弃

## 推荐落地顺序

1. 先把 `message.Send` 改成 durable success 后只提交 envelope，不再直接投递
2. 引入单节点版 `delivery runtime`，先跑通本地 actor、mailbox、retry、ack
3. 把 `RecvAckCommand` 补上 `SessionID`，接入 `AckBindingIndex`
4. 引入远端 `Submit/Push/Ack/Offline` RPC
5. 引入 `SubscriberResolver` 与个人频道 codec
6. 引入群频道分页解析与批量 presence 查询
7. 增加热点频道独占 lane、上限与降级策略

## 结论

本设计把消息语义明确拆成“durable send”和“async realtime delivery”两条链路，并以“频道 owner actor”作为唯一串行状态机来承载订阅者解析、在线投递、`recvack` 跟踪与重试。

相比把所有逻辑继续堆在 `message.Send` 里，这种设计的优势是：

- 成功语义清晰
- 个人频道与群频道统一
- `recvack` 成为真正的投递完成信号
- 大规模频道可通过分页、虚拟 actor、统一调度与热点隔离保持可控

首版刻意不持久化投递状态，把节点重启与 route 断线后的恢复交给离线补消息，从而把设计复杂度控制在当前仓库可以稳妥承载的范围内。
