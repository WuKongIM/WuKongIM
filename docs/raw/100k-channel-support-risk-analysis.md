# 10 万人频道支持风险分析

## 背景

本文分析当前项目在支持“10 万人频道”（主要指 10 万普通订阅者的群频道）时可能出现的性能、资源与语义问题。

当前架构的一个优势是：频道消息通过 `pkg/channel` 的 channel log 持久化，一条群消息只写一份频道日志，不会按订阅者写 10 万份消息。因此，风险不主要在消息落盘本身，而集中在订阅者列表读取、实时投递 fanout、presence 路由查询、ACK 状态维护、会话活跃提示和成员管理操作。

本文只讨论当前代码路径下的问题，不代表最终设计目标。

## 当前关键链路

### 发送与提交

- `internal/usecase/message.Send` 先完成 channel log append。
- append 成功后，通过 committed dispatcher 异步触发：
  - realtime delivery：`internal/runtime/delivery`
  - conversation active hint：`internal/usecase/conversation`
- `internal/app/build.go` 当前把 committed dispatcher 配成 `PreferLocal: true`，即提交后的本地副作用优先在当前节点执行。

相关代码：

- `internal/usecase/message/send.go`
- `pkg/channel/handler/append.go`
- `pkg/channel/replica/append_pipeline.go`
- `internal/app/deliveryrouting.go`
- `internal/app/build.go`

### 订阅者读取

- 订阅者保存在 slot meta 的 `Subscriber` 表里，主键维度是 `(channel_id, channel_type, uid)`。
- 存储层支持分页读取：`ListSubscribersPage`。
- 但实时投递当前对群频道使用 `SnapshotChannelSubscribers`，会一次性拉全量订阅者到内存。

相关代码：

- `internal/usecase/delivery/subscriber.go`
- `pkg/slot/meta/subscriber.go`
- `pkg/slot/proxy/subscriber_rpc.go`

### 实时投递

- 每个频道进入一个 delivery actor。
- actor 按页解析订阅者，查询 presence 权威路由，再按本地/远端节点推送。
- accepted route 会绑定 ACK 索引，后续收到 recvack 或 session close 后清理。

相关代码：

- `internal/runtime/delivery/actor.go`
- `internal/runtime/delivery/manager.go`
- `internal/app/deliveryrouting.go`
- `internal/access/node/delivery_push_rpc.go`

## 主要问题

### 1. 每条群消息会全量拉取订阅者快照

`subscriberResolver.BeginSnapshot` 对非个人频道调用 `SnapshotChannelSubscribers`，这会把一个频道的全部订阅者读成 `[]string`。

对 10 万人频道而言，每条消息都会触发：

- 一次完整 Pebble range scan；
- 一次可能跨节点的 subscriber RPC；
- 一个包含 10 万 UID 的响应；
- 一个 10 万元素的内存 slice。

这会导致明显的 CPU、IO、网络和 GC 压力。若频道消息频率较高，这个成本会按消息数线性放大。

相关代码：

- `internal/usecase/delivery/subscriber.go`
- `pkg/slot/proxy/subscriber_rpc.go`
- `pkg/slot/meta/subscriber.go`

### 2. “快照分页”在内存中线性查 cursor

全量订阅者被拉到内存后，`NextPage` 通过 `nextSubscriberSnapshotPage` 分页。该函数每次根据 cursor 从头扫描 UID slice 找起点。

当默认 delivery resolve page size 为 256 时，10 万成员大约需要 391 页。每页都线性找 cursor，会产生额外 O(N * page_count) 的 CPU 开销。

相关代码：

- `internal/usecase/delivery/subscriber.go`
- `internal/runtime/delivery/manager.go`

### 3. 成员全离线时仍然会扫完整成员表

`localDeliveryResolver.ResolvePage` 会先读订阅者页，再调用 `EndpointsByUIDs` 查询在线路由。若当前页没有任何在线 route，它会继续读下一页，直到读完整个频道或凑到 route。

因此，10 万人频道即使全部离线，每条消息仍可能扫描 10 万订阅者并查询 presence，最终实时投递为 0。

这是大频道最危险的低收益高成本路径。

相关代码：

- `internal/app/deliveryrouting.go`
- `internal/app/presenceauthority.go`
- `internal/usecase/presence/directory.go`

### 4. Presence 查询被 fanout 放大

群频道每页订阅者会批量查询 presence 权威路由。`presenceAuthorityClient.EndpointsByUIDs` 会按 UID 所属 slot 分组，再逐组查询本地或远端 presence。

10 万成员频道会把一次消息放大为大量 presence 查询。热点频道会给多个 slot leader 的 `directory` 带来持续读锁/互斥锁压力。

相关代码：

- `internal/app/presenceauthority.go`
- `internal/usecase/presence/directory.go`
- `internal/access/node/presence_rpc.go`

### 5. 单频道 actor 的 inflight route 默认上限只有 4096

delivery actor 使用 `MaxInflightRoutesPerActor` 限制单 actor 未完成 route 数，默认值是 4096。

10 万在线成员时，一条消息最多先推进约 4096 条 route，后续订阅者要等 ACK、session close、drop 或 retry 释放预算。若客户端 ACK 慢，后续成员和后续消息都会被同一个频道 actor 阻塞。

这意味着当前默认配置下，10 万在线成员不具备“快速全量实时投递”的能力。

相关代码：

- `internal/runtime/delivery/manager.go`
- `internal/runtime/delivery/actor.go`

### 6. ACK 索引内存随在线 fanout 放大

每个 accepted route 会绑定 `AckBinding`。10 万在线成员收到同一条消息时，理论上会产生接近 10 万个 ACK binding。

如果频道消息持续产生、客户端 ACK 慢或网络抖动，ACK binding 数量会按 `在线成员数 * 未 ACK 消息数` 增长，造成内存压力和 retry wheel 压力。

相关代码：

- `internal/runtime/delivery/ackindex.go`
- `internal/runtime/delivery/actor.go`
- `internal/access/node/delivery_push_rpc.go`

### 7. 远端实时投递可能形成巨型 RPC

跨节点投递时，`distributedDeliveryPush` 会按目标 node 聚合 route。对群频道，一个目标 node 会收到一个 `DeliveryPushItem`，其中包含该节点所有 route 和一份 frame。

如果 10 万在线连接集中在少数节点，单个 RPC 可能包含数万 route。响应里还会按 route 回传 accepted / retryable / dropped。

虽然底层 transport 单帧上限是 64MB，但巨型 RPC 会带来：

- 大 slice 分配；
- 编解码耗时；
- 网络尖峰；
- 响应回显开销；
- GC 压力；
- 单 RPC 超时风险。

相关代码：

- `internal/app/deliveryrouting.go`
- `internal/access/node/delivery_rpc.go`
- `internal/access/node/delivery_push_codec.go`
- `pkg/transport/errors.go`

### 8. 远端节点推送并发固定为最多 4 个 worker

`pushRemoteNodes` 对远端节点使用 `min(4, len(nodeIDs))` 个 worker。

如果集群节点较多，或者某些节点处理慢，一个热门 10 万人频道的单条消息 fanout 会被少量 worker 串住。对多节点大群，这个上限可能过低。

相关代码：

- `internal/app/deliveryrouting.go`

### 9. 远端接收节点逐 route 写连接

远端 `handleDeliveryPushItem` 收到 batch 后，遍历所有 route，逐个找在线连接、绑定 ACK、调用 `WriteFrame`。

这条路径没有进一步按连接分片调度，也没有将慢连接隔离成独立队列。大 batch 到达时，单个 RPC handler 会承担大量连接写操作。

相关代码：

- `internal/access/node/delivery_push_rpc.go`

### 10. committed dispatcher 对热门频道容易积压或溢出

committed dispatcher 按 channel hash 分片，每个 shard 默认队列深度 512。热门频道会固定落到同一个 shard。

如果该 shard 的 delivery fanout 很慢，新提交消息会持续排队；队列满时，当前逻辑只尝试 conversation fallback，不保证 realtime delivery 立即执行。

相关代码：

- `internal/app/deliveryrouting.go`
- `internal/app/committed_replay.go`

### 11. 订阅者批量写入和删除是大 Raft proposal

`AddChannelSubscribers` / `RemoveChannelSubscribers` 把 UID 列表编码进一个 Raft command。10 万 UID 一次导入或删除会形成：

- 巨型 command；
- 巨型 Raft entry；
- 大量 TLV 编解码；
- 一次大 Pebble batch；
- 较长时间的 DB 写锁持有。

这会影响 slot meta 的稳定性，并增加超时和快照压力。

相关代码：

- `pkg/slot/proxy/store.go`
- `pkg/slot/fsm/command.go`
- `pkg/slot/meta/subscriber.go`

### 12. Reset / RemoveAll 会聚合全量 UID 后一次删除

`removeAllSubscribersFor` 会分页读取订阅者，但把所有 UID append 到一个 slice 后，再调用一次 `RemoveChannelSubscribers`。

对 10 万人频道，这会带来内存峰值和一次巨型删除 proposal。

相关代码：

- `internal/usecase/channel/app.go`

### 13. 大群会话 active hint 默认不 fanout

配置示例中：

```text
WK_CONVERSATION_GROUP_ACTIVE_FANOUT_MAX_SUBSCRIBERS=0
```

零值表示禁用群订阅者 active hint fanout。也就是说，当前默认语义下，大群消息不会给 10 万成员都写会话活跃提示。

如果业务要求“所有成员收到消息后会话列表立刻出现/置顶”，当前默认配置不满足；如果把该值调到 10 万，又会引入大量 subscriber scan 和 active hint 写入压力。

相关代码：

- `wukongim.conf.example`
- `internal/usecase/conversation/projector.go`
- `internal/app/config.go`

### 14. 单频道写入 leader 是天然热点

消息落盘虽然不是按成员写多份，但同一频道仍由单个 channel replica leader 串行协调 append 和复制。

当前 group commit 默认参数是：

- max wait：1ms；
- max records：64；
- max bytes：64KB。

对“热门 10 万人频道 + 高发送 QPS”场景，单 channel leader 会成为写入和提交顺序的天然瓶颈。

相关代码：

- `pkg/channel/handler/append.go`
- `pkg/channel/replica/replica.go`
- `pkg/channel/replica/append_pipeline.go`

## 高风险场景

### 场景 A：10 万成员，绝大多数离线

表现：

- 每条消息扫描 10 万订阅者；
- presence 查询大量 UID；
- 实际 realtime route 很少甚至为 0。

风险：

- CPU / IO 被无效 fanout 消耗；
- 热门频道会拖慢 slot meta 和 presence；
- committed dispatcher 队列积压。

### 场景 B：10 万成员，大量在线

表现：

- 需要维护大量 route state 和 ACK binding；
- 默认 4096 inflight route 上限导致分批推进；
- 慢 ACK 或慢连接会阻塞后续 fanout。

风险：

- 延迟长尾严重；
- 内存随未 ACK 消息数放大；
- retry wheel 压力变大。

### 场景 C：跨节点大群，在线连接分布不均

表现：

- 少数目标节点收到巨型 delivery push RPC；
- 单 RPC 内逐 route 写连接；
- 响应回传大量 route。

风险：

- RPC 超时；
- 节点间网络尖峰；
- 远端 handler 被大 batch 占用；
- GC 抖动。

### 场景 D：热门频道高 QPS

表现：

- 单 channel leader append；
- 单 committed dispatcher shard；
- 单 delivery actor；
- 同一订阅者表和 presence 查询被反复访问。

风险：

- 单热点扩散到多层；
- 高 QPS 下积压迅速放大；
- replay 追赶成本高。

### 场景 E：一次性导入、重置或删除 10 万成员

表现：

- 大 HTTP 请求；
- 大 Raft proposal；
- 大 Pebble batch；
- Reset / RemoveAll 聚合全量 UID 后一次删除。

风险：

- 操作超时；
- slot apply 卡顿；
- snapshot / compaction 压力增大。

## 优先改造建议

### 当前已落地的缓解

- 群频道实时投递的订阅者解析已改为直接调用存储分页，不再在每条消息开始时全量 snapshot 订阅者。
- 远端 group delivery push 已按目标节点 route chunk 拆分，当前单 RPC route 上限为 1000。
- delivery push v2 响应已改为 `accepted_count + retryable/dropped 精确 route`，避免把所有成功 route 在响应中回显；legacy 请求/响应仍保留 accepted route 列表用于滚动升级兼容。
- 频道订阅者 Add / Remove / Reset / RemoveAll 已按 `SubscriberPageLimit` 分块提交；Reset / RemoveAll 边分页读取边删除，不再先把全量 UID 聚合到内存后一次提交删除。
- Slot subscriber Raft command 已增加底层硬限制：单条 Add / Remove subscriber command 最多 1000 个 UID，编码后 UID 字节最多 64KB；proxy 提交前校验，FSM decode 再兜底校验。

### P0：实时投递改为存储分页，不要每条消息全量 snapshot

建议：

- 个人频道仍可使用内存小 snapshot；
- 群频道 `BeginSnapshot` 不应调用 `SnapshotChannelSubscribers`；
- `NextPage` 应直接调用 `ListChannelSubscribers`；
- cursor 应由存储层推进，避免内存中反复线性扫描。

预期收益：

- 避免每条消息读取 10 万 UID；
- 降低内存峰值；
- 降低跨节点 subscriber RPC 大响应风险。

### P0：delivery push RPC 按 route chunk 拆分

建议：

- 单个目标节点单次 RPC 限制 route 数，例如 500 或 1000；
- 响应不要默认回显所有 accepted route，可只回传异常 route 和 accepted count；
- 对 retryable / dropped 保留精确 route。

预期收益：

- 控制单 RPC 大小；
- 降低编解码和 GC 峰值；
- 降低超时风险。

### P0：大频道 fanout 状态持久化或可恢复

建议：

- 对超过阈值的频道，fanout 变成 durable job；
- 记录 message seq、subscriber cursor、目标 node cursor、重试状态；
- committed replay 不应反复从头触发大规模实时投递。

预期收益：

- 可恢复；
- 可限速；
- 可观测；
- 避免内存 actor 承担完整大群投递生命周期。

### P1：维护在线订阅者索引

建议：

- 对大频道维护 `channel -> online route` 或 `channel -> online UID` 索引；
- 发送时优先 fanout 在线订阅者；
- 离线成员通过 channel log 拉取消息，不走实时 fanout。

预期收益：

- 解决“10 万离线成员仍全量扫描”的问题；
- realtime delivery 成本接近在线人数，而不是总订阅人数。

### P1：订阅者变更拆小 proposal

建议：

- 管理 API 对大批量成员操作强制分批；
- Raft command 限制 UID 数和字节大小；
- Reset / RemoveAll 使用分页删除，边读边提交小批删除；
- 维护 member count 和 membership version。

当前进展：

- 用例层已把订阅者 Add / Remove / Reset / RemoveAll 按 `SubscriberPageLimit` 拆分，避免单次提交 10 万 UID。
- 底层 Slot subscriber Raft command 已限制单条 UID 数和编码字节数。
- member count 和 membership version 仍待补齐。

预期收益：

- 降低 slot apply 卡顿；
- 减少大 Raft entry 和大 Pebble batch；
- 提升运维操作可控性。

### P1：大群 ACK 和慢连接隔离

建议：

- 单频道 actor 不应被慢 ACK 全局阻塞；
- 可按目标 node、route bucket 或 subscriber bucket 拆子 actor；
- 对大群可以降低 ACK 跟踪粒度，或仅对需要强确认的消息跟踪 route ACK。

预期收益：

- 降低慢连接对全频道的影响；
- 控制 ACK binding 内存。

### P2：明确大群会话语义

当前默认禁用群 active hint 全量 fanout，这是合理的保护策略，但需要产品语义配合。

建议明确：

- 大群是否需要给所有成员立即更新会话列表；
- 若不需要，客户端通过已知会话、频道列表、同步消息、read-repair 等方式发现；
- 若需要，只能通过限速、分页、后台任务实现，不能在消息热路径上同步 fanout 10 万 active hints。

## 建议压测项

至少补以下压测或集成基准：

1. 10 万订阅者，0 在线，单条消息和连续消息。
2. 10 万订阅者，1% 在线，在线 UID 均匀分布。
3. 10 万订阅者，50% 在线，跨 3/5/10 节点分布。
4. 10 万订阅者，全在线，慢 ACK 比例 1% / 10%。
5. 热门频道高 QPS，验证 channel leader、committed dispatcher、delivery actor 积压。
6. 一次性导入 10 万成员。
7. Reset / RemoveAll 10 万成员。
8. 节点故障后 committed replay 追赶大频道消息。
9. 远端 delivery push RPC 大 batch 编解码和超时。
10. conversation group active hint 开启后，10 万成员 fanout 的写入压力。

## 结论

当前实现可以作为中小群频道的基础，但直接承载 10 万人频道会出现明显的热点和资源放大。

最核心的问题不是“消息写入 10 万份”，而是：

- 每条消息按全量订阅者做实时 fanout 准备；
- 大量 presence 查询；
- 单 actor 和 ACK 状态承载大规模在线投递；
- 跨节点 delivery push 缺少 route chunk；
- 成员管理操作缺少大批量分片。

若要支持 10 万人频道，应优先把大群投递从“单消息热路径全量扫描 + 内存 actor 推进”改为“分页、分片、限速、可恢复的 fanout job”，并让 realtime 成本尽量按在线成员数计费，而不是按频道总成员数计费。
