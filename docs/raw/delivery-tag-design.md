# Delivery Tag 投递方案

## 背景

当前实时投递链路已经避免了“群消息按订阅者写多份消息”的落盘问题，但所有频道在实时投递时仍然存在两个热路径成本：

- 每条消息都需要分页读取订阅者，并按 UID 查询 presence 路由。
- 如果尝试维护 `channel -> online routes` 索引，又会把在线/离线高频变化扩散到每个频道或每个节点，节点状态和实时更新成本都很高。

因此，本方案参考旧 WuKongIM 的 tag 机制，把频道成员集合统一做成由频道 leader 管理的投递快照。tag 只描述频道订阅者按目标节点的分片，不描述实时在线连接；在线状态仍由投递时的 presence/本地会话状态判断。该机制适用于所有频道，10 万人频道只是最需要它降低重复扫描成本的高压场景。

## 核心结论

Delivery Tag 应定义为：**频道 leader 创建和更新的、按节点分片的订阅者快照，其他节点只能按 tagKey 拉取自己的分片并缓存。**

关键约束：

- 只有频道 leader 节点可以创建、重建、更新频道 tag。
- 非 leader 节点不能本地创建频道 tag；只能向频道 leader 请求当前节点的 tag 分片，然后缓存。
- tag 是缓存快照，不是强一致元数据，不进入 Raft，也不作为消息可靠性的唯一依据。
- tag 只缓存订阅者分片，不缓存在线连接，不维护 `channel -> online subscribers` 索引。
- 成员变化不生成新的 tagKey，而是在频道 leader 上递增 `tagVersion` 来失效旧缓存，避免向所有节点广播逐 UID 的实时变更。
- `tagKey` 标识 tag incarnation，普通成员变化保持稳定；leader incarnation 变化、TTL 后冷重建等非普通成员变更场景可以生成新的 tagKey。
- 频道 leader 分发消息给目标节点时必须带上 `tagKey + tagVersion`；目标节点本地缓存只有在 key 和 version 都一致时才可复用。

## 旧 WuKongIM 参考

旧实现中有几个关键点可以保留：

- `event_distribute.go` 中，频道 leader 调用 `getOrMakeTagForLeader` / `makeChannelTag` 创建 tag，然后把 `TagKey` 写到事件里；非 leader 收到事件后只根据 `TagKey` 获取 tag。
- `common.GetOrRequestAndMakeTag` 先查本地缓存，miss 后向频道 leader 请求；请求带 `NodeId` 时，leader 只返回该节点对应的 UIDs，因此非 leader 拿到的 tag 不是频道全量 tag。
- `TagManager.Get` 会检查旧实现里的 `NodeVersion`，集群节点版本变化后旧 tag 失效；当前方案扩展为 `PartitionTopologyVersion`。
- `bucket.go` 用 `LastGetTime + Expire` 定期清理 tag，并清理失效的 `channel -> tagKey` 映射。
- 旧实现中成员变化会 `RenameTag` 生成新 key；当前方案不沿用这一点，改为保持 tagKey 稳定并递增 tagVersion，目标节点根据 version mismatch 重新拉取。

参考代码：

- `learn_project/WuKongIM/internal/channel/handler/event_distribute.go`
- `learn_project/WuKongIM/internal/common/common.go`
- `learn_project/WuKongIM/internal/manager/manager_tag.go`
- `learn_project/WuKongIM/internal/manager/bucket.go`
- `learn_project/WuKongIM/internal/ingress/server.go`

## 适用范围

本方案适用于所有频道类型的实时投递成员解析路径，包括小频道、普通群、临时频道、cmd 频道和大频道。不同频道可以在实现上采用相同语义，但允许在性能上做特化：

- 小频道：主要获得一致的 leader-authoritative tag 语义，性能收益不是重点。
- 普通群：复用成员快照，减少重复 subscriber 读取和节点分片规划。
- 大频道：显著降低重复扫描订阅者存储、跨节点规划和 follower 全量缓存成本。

10 万人频道不是该方案的唯一适用范围，只是验证方案上限和压力保护的代表场景。

## 目标

- 降低所有频道投递时重复解析订阅者和规划目标节点的成本，尤其降低大频道高频消息下的订阅者存储扫描成本。
- 避免每个节点维护庞大的 `channel -> online member` 实时索引。
- 保持频道 leader 对频道投递快照的唯一创建权，避免多节点并发生成不一致 tag。
- 让 follower 只缓存本节点需要处理的 UID 分片，降低跨节点内存放大。
- 保留现有分页 subscriber store 和 presence 权威查询作为 fallback / 在线判断能力。

## 非目标

- 不解决大量在线成员的最终 fanout 成本；tag 只能减少成员解析和分发规划成本，不能消除向大量连接写包的成本。
- 不把 tag 的 UID 分片做成持久化强一致状态；节点重启、TTL 过期、leader 切换后允许重建。
- 不做任何频道的在线订阅者实时索引。
- 不在非 leader 节点上扫描订阅者全量并创建频道 tag。
- 不替代后续可能需要的持久 fanout job、离线补偿或大频道限流策略。

## 数据模型

建议的逻辑结构如下，具体命名可在实现阶段调整：

```go
// DeliveryTag is a leader-owned subscriber snapshot for one channel.
type DeliveryTag struct {
    Key                       string
    ChannelID                 string
    ChannelType               uint8
    Nodes                     []DeliveryTagNode
    CreatedAt                 time.Time
    LastAccess                time.Time
    TagVersion                uint64
    SubscriberMutationVersion uint64
    PartitionTopologyVersion  PartitionTopologyVersion
}

// DeliveryTagNode stores the subscriber partition that one node should process.
type DeliveryTagNode struct {
    NodeID  uint64
    UIDs    []string
    SlotIDs []uint32
}

// PartitionTopologyVersion describes the cluster-controlled partitioning state
// that affects UID ownership.
type PartitionTopologyVersion struct {
    HashSlotTableVersion uint64
    SlotAuthorityRefs    []SlotAuthorityRef
}

// SlotAuthorityRef records the authoritative owner/version for one UID slot
// represented by this tag.
type SlotAuthorityRef struct {
    SlotID         uint64
    LeaderNodeID   uint64
    ConfigEpoch    uint64
    BalanceVersion uint64
}
```

### tagKey 与 tagVersion

- `tagKey` 是 tag incarnation ID，不是 channel ID。首次创建 tag 时由频道 leader 生成；普通成员 Add/Remove/Reset/RemoveAll 不改变 tagKey。
- `tagVersion` 是同一个 tag incarnation 的公开防护版本。普通成员变化、`SubscriberMutationVersion` 前进、或 `PartitionTopologyVersion` 变化导致的重建，都必须递增 tagVersion；只有当前 `tagVersion` 才能代表当前完整 tag materialization。
- leader incarnation 变化、leader 重启导致内存 tag 丢失、TTL 后冷重建等场景可以生成新的 tagKey，避免新 leader 用相同 key 和较小 version 命中 follower 旧缓存。
- `SubscriberMutationVersion` 是成员存储的权威变更版本；tag 复用前必须先比较它是否落后于存储侧的最新版本。
- follower 缓存命中必须同时匹配 `tagKey`、`tagVersion` 和 `PartitionTopologyVersion`；`SubscriberMutationVersion` 只用于判断快照是否已经过时。

### SubscriberSource

Delivery Tag 适用于所有频道，但不同频道的订阅者来源不同，构建 tag 时不能把所有频道都简单地读 subscriber store：

- 个人频道：从 channelID 派生双方 UID，必要时先去掉 cmd 包装，再解析原频道的两个 UID。
- Agent 频道：从 channelID 派生用户 UID 和 Agent UID，和个人频道一样属于 ID 派生型。
- 群 / 社区 / 话题 / 数据 / 直播 / AgentGroup：分页读取 ordinary subscriber store。
- Info：分页读取 ordinary subscriber store，并合并当前生效的临时订阅者 overlay。
- Temp：读取临时频道订阅者来源；如果是在线命令类临时投递，则使用请求中明确给出的 UID 集合作为一次性、消息作用域的输入，不写入 channel-level tag cache，也不跨请求复用。
- Visitors：分页读取 customer-service 订阅者来源，并把 visitor UID 从 channelID 中补入结果。
- CustomerService：分页读取 customer-service 订阅者来源，并把 visitor UID 从 channelID 中补入结果；它是旧兼容路径。
- cmd / derived 频道：先解析原频道，再用原频道的 SubscriberSource 生成 tag；cmd 频道本身不是独立成员集合。

实现时建议把上述逻辑抽象成 `SubscriberSource` 小接口，由 usecase/runtime 注入，避免在 access 层或 tag manager 中写死入口协议逻辑。对于当前实现尚未显式覆盖的 overlay，必须在这里补齐，而不是在投递路径末尾临时拼接。

### Leader 缓存

频道 leader 节点缓存完整 tag：

- `tagKey -> DeliveryTag`
- `channelKey -> currentTagRef`

`currentTagRef` 至少包含 `{tagKey, tagVersion, SubscriberMutationVersion, PartitionTopologyVersion}`，用于判断一次投递请求是否比本地缓存更新或更旧。

leader 需要完整 tag，因为它要知道一次投递应该分发到哪些目标节点，也要响应 follower 的 `GetTag(tagKey, tagVersion, targetNodeID)` 请求。

### Follower 缓存

非 leader 节点只缓存本节点分片，语义上这是 local partition tag，不是频道全量 tag：

- `tagKey -> {tagVersion, SubscriberMutationVersion, PartitionTopologyVersion, local UIDs}`
- `channelKey -> currentTagRef`

follower 拿到的 `local UIDs` 只包含当前节点负责处理的订阅者，不包含频道所有订阅者，也不包含其他节点的分片。任何非 leader 节点都不能把本地 tag 用作全量成员快照。这样任何频道都不会在每个节点复制全量 UID 快照，大频道的收益更明显。

### 分片维度

分片节点建议沿用旧 WuKongIM 思路：按 UID 所属的权威节点分片，例如 UID slot leader / presence authority owner。

原因：

- tag 是成员集合缓存，而不是在线路由缓存。
- 用户连接实际在哪个接入节点仍由 presence 判断。
- 按 UID 权威节点分片后，每个节点只处理自己负责的 UID 子集，避免所有节点遍历全量成员。

## 投递流程

### 1. 频道 leader 获取或创建 tag

消息提交后进入实时投递，频道 leader 执行：

1. 根据 `(channelID, channelType)` 查 `channel -> currentTagRef`。
2. 如果存在 tagKey，读取 `tagKey -> DeliveryTag` 并校验 `tagVersion`、`SubscriberMutationVersion` 和 `PartitionTopologyVersion`。
3. 如果 tag 不存在、过期、`SubscriberMutationVersion` 落后，或 `PartitionTopologyVersion` 的任一分量落后，则 leader 通过 SubscriberSource 解析订阅者并重建 tag。
4. 重建时按 UID 权威节点聚合，得到 `DeliveryTag.Nodes`。
5. leader 写入本地缓存；首次创建时生成 tagKey，后续成员变更只递增 tagVersion。
6. leader 把 `tagKey` 和 `tagVersion` 附到本次投递事件或节点间投递请求中。

只有这一步允许创建频道 tag。

### 2. leader 按 tag 分发到目标节点

leader 不再为远端节点回显所有 routes，也不需要先把所有在线路由算出来再跨节点发送。leader 只需要根据 tag 的节点分片把消息、tagKey 和 tagVersion 分发到对应节点。

`tagKey + tagVersion` 是 leader -> target 的公开 fence；`SubscriberMutationVersion` 和 `PartitionTopologyVersion` 只存在于 tag 的内部 currentTagRef 中，并且任何会改变它们的重建都必须同时推进 `tagVersion`，避免外部 wire contract 变成四字段协议。

每个目标节点收到的数据应尽量是：

- `channelID`
- `channelType`
- `tagKey`
- `tagVersion`
- 消息 frame / message metadata
- 必要的 trace / retry metadata

如果目标节点就是 leader 本地节点，则直接进入本地分片投递。

### 2.1 ACK / retry 所有权

Tag 分发后，ACK/retry 的所有权必须从频道 leader 下沉到目标分片节点：

- 频道 leader 只负责把消息、tagKey、tagVersion 和目标分片任务交给对应节点；它不维护该分片内每条 route 的 ACK/retry 状态。
- 目标分片节点获取本地 UID 分片后，解析在线 routes，并作为这些 routes 的 delivery owner 维护 ACK、retry、session close 和过期清理。
- 如果实际连接在其他 gateway 节点，目标分片节点继续使用现有 route-based `delivery_push` RPC 转发；该 RPC 仍使用 `accepted count + retryable/dropped 精确 routes`，用于目标分片节点还原本地 route 状态。
- leader -> 目标分片节点这一层只返回分片任务状态，例如 `accepted`、`retryable`、`tag_mismatch`、`not_leader`；不要要求目标节点向频道 leader 回传所有 accepted routes。
- 分片任务需要按 `(channelID, channelType, messageID, targetNodeID, tagKey, tagVersion)` 做幂等处理，避免 leader 重试或 committed replay 时产生不可控重复投递。

这样可以保留当前 route-based ACK/retry runtime，又避免频道 leader 重新持有全频道在线 route 状态。

### 3. follower 获取本地分片并缓存

目标节点收到带 tagKey 和 tagVersion 的投递请求后：

1. 查本地 `channel -> currentTagRef`，其中 `currentTagRef` 包含 `tagKey`、`tagVersion`、`SubscriberMutationVersion` 和 `PartitionTopologyVersion`。
2. 如果请求与本地 current ref 完全一致，且本地 tag body 存在且未过期，可以直接复用。
3. 如果 tagKey 相同但请求 tagVersion 小于本地 tagVersion，直接把它当成 stale event 处理，不允许它删除或降级本地新缓存。
4. 如果 tagKey 相同但请求 tagVersion 更新，或 `SubscriberMutationVersion` / `PartitionTopologyVersion` 不一致，说明该 local partition 已失效，需要向当前频道 leader 拉取。
5. 如果 tagKey 不一致，follower 不能根据随机 key 本地判断新旧，也不能先删除 current ref；必须向当前频道 leader 确认当前 ref。
6. 拉取请求为：`GetTag(channelID, channelType, tagKey, tagVersion, targetNodeID=localNodeID)`；leader 必须返回当前 channel ref 或明确返回 `tag_not_current` / `not_leader`。
7. leader 只返回当前请求节点分片的 UIDs 和当前 version；如果该节点没有分片，返回空集合也可以缓存。返回结果不能被解释为频道全量订阅者。
8. follower 只在 leader 确认返回 ref 是当前 channel ref 时才更新本地 cache；旧响应必须丢弃，不能覆盖新缓存。

follower 在任何情况下都不能通过本地订阅者 store 自行创建频道 tag，也不能基于本地 tag 对外提供“频道全量订阅者”语义。

### 4. 目标节点执行本地实时投递

目标节点拿到本地 UIDs 后，再执行在线判断和连接投递：

1. 对本地 UIDs 按 UID slot / presence authority 分组后批量查询 presence / 本地会话；不能假设一个本地分片只包含单个 slot。
2. 过滤离线用户、发送者、黑名单等规则。
3. 对在线 endpoint 写连接或转发到实际接入节点。
4. 目标分片节点对 accepted routes 建立 ACK / retry 状态。

这一步仍然可能面对大量在线连接，因此仍需保留已有的 inflight、ACK、retry、RPC chunk 等保护。

## 成员变更流程

成员变更必须收敛到频道 leader 更新 tag。

### Add / Remove

1. API / usecase 把成员变更提交给权威 subscriber store；subscriber rows 写入和 `SubscriberMutationVersion` 递增必须在同一个 slot Raft command / 同一权威存储事务中完成。
2. store 返回本次逻辑变更后的 `SubscriberMutationVersion`；tag manager 只能消费这个版本，不能自己生成成员版本。
3. 找到频道 leader。
4. 如果请求在 leader 本地，直接用返回的 mutation version 更新 leader tag；否则通过节点 RPC 请求 leader 更新。
5. 如果 leader 上不存在当前 channel tag，记录本次成员变更已影响 tag；下一条消息会按最新 SubscriberSource 重建。
6. 如果 leader 上存在当前 tag：
   - add：把新增 UID 合并到对应节点分片。
   - remove：从对应节点分片删除 UID。
   - 使用 store 返回的 `SubscriberMutationVersion`，并递增一次 tagVersion。
   - 更新 `channel -> currentTagRef`。
   - 旧 version 的本地分片缓存等待目标节点收到新 version 后失效，或由 TTL 清理。

### Reset / RemoveAll

Reset / RemoveAll 不建议在 tag 中做逐 UID 删除；频道越大，这种逐项删除越容易形成 CPU 和锁持有尖峰。

推荐策略：

- 如果 leader 上存在当前 tag，直接重置/清空 tag 的节点分片并递增 tagVersion，不生成新的 tagKey。
- 如果 leader 上不存在当前 tag，则不为了 Reset / RemoveAll 单独创建 tag；下一条消息首次构建 tag 时再生成 tagKey。
- Reset + Add 这类复合操作应按一个逻辑成员操作发布一个 `SubscriberMutationVersion` 和一个 tagVersion：存储层分 chunk 写入时不应每个 chunk 暴露一个新 version，避免投递看到中间态。

### 成员变更失败补偿

成员存储变更成功后，必须保证频道 leader 的 tag 被更新、标记 dirty 或后续重建，不能只删除发起节点的本地缓存：

- `SubscriberMutationVersion` 必须持久化在权威 slot store 的频道成员状态中，或持久化在同一个 slot 权威范围内的相邻成员状态行中；leader 重启或切换后仍能读取它。
- 如果成员变更请求已经到达频道 leader，leader 应先标记该 channel tag dirty，再提交权威 subscriber store mutation；subscriber rows 和 `SubscriberMutationVersion` 成功提交后，leader 用返回版本更新 tag，更新完成后清除 dirty。
- 如果成员变更发生在非 leader 节点，RPC 到 leader 更新 tag 失败时，发起节点可以记录 pending invalidate / retry 任务，但它只是加速信号，不能作为唯一一致性保障。
- leader 在处理下一条消息前必须先比较权威 `SubscriberMutationVersion`；如果当前 tag 版本落后于 store，则必须从 SubscriberSource 重建 tag，并把新的 mutation version 写回缓存。
- 如果成员变更被分 chunk 写入，所有 chunk 必须共享同一个逻辑 `SubscriberMutationVersion`，不能让投递看到中间态版本。
- 不允许仅依赖 TTL 来修复成员变更后的旧完整 tag；TTL 只能作为兜底资源回收。

### CMD / 临时频道

如果存在 cmd 频道或临时频道的 tag，也必须遵循同一原则：

- 其 tag 创建权属于对应频道 leader。
- 其他节点只能请求和缓存。
- cmd / derived tag 必须记录 `SourceChannelKey` 和 `SourceSubscriberMutationVersion`。
- derived 频道 leader 每次使用 tag 前都要读取源频道当前 `SubscriberMutationVersion`；如果源版本不同，derived tag 直接判定为 stale 并重建。
- 因为正确性由源版本比较保证，初版不依赖持久反向索引。可以维护本地 `sourceChannelKey -> derivedTagKey set` 作为主动清理优化，但 leader 切换或索引丢失后仍必须靠源版本比较兜底。
- 如果源频道 leader 和 derived 频道 leader 不在同一节点，源成员变更只需要保证权威源版本已递增；主动 invalidate RPC 是加速路径，不是正确性前提。

## 缓存一致性策略

### tagKey + tagVersion 是主要一致性信号

tagKey 标识 tag incarnation，tagVersion 标识该 incarnation 下的成员快照版本。目标节点收到 leader 分发的消息后，必须用请求里的 `tagKey + tagVersion` 与本地缓存做双校验。

- 如果请求比本地 current ref 旧，直接判定为 stale event；不能因为旧请求触发本地 current ref 的删除或回退。
- tagKey 不一致时，只能按当前请求对应的 key 单独拉取或等待 leader 指示，不能无条件覆盖 channel 维度的 current ref；这通常只发生在首次创建、leader incarnation 变化、TTL 清理后冷重建等非普通成员变更场景。
- tagKey 一致但 tagVersion 不一致：说明同一个 tag incarnation 下的本地缓存已经过期，只有当请求比本地 current ref 更新时才允许替换；旧请求不能驱逐新缓存。
- tagKey、tagVersion、`SubscriberMutationVersion` 和 `PartitionTopologyVersion` 都一致：才允许直接使用本地缓存的 UID 分片。
- 旧投递事件携带旧 tagVersion，可以继续尽力投递；如果 leader 已无法返回该版本，则按 retry/fallback 处理。
- 新消息必须使用 leader 当前 `channel -> currentTagRef`。

普通成员变化必须保持 tagKey 稳定，只递增 tagVersion，确保目标节点通过 version mismatch 识别缓存失效并重新拉取。

### PartitionTopologyVersion 处理分片拓扑变化

tag 中记录创建时的 `PartitionTopologyVersion`。它不是单一的本地计数，而是由控制面/集群运行时暴露的权威引用集合：

- `HashSlotTableVersion`：全局 hash-slot table 版本，来源是当前项目已有的 `cluster.HashSlotTableVersion()`。
- `SlotAuthorityRefs`：本 tag 中所有 UID 所属 slot 的有序列表；每项记录 `{SlotID, LeaderNodeID, ConfigEpoch, BalanceVersion}`。
- `LeaderNodeID` 来源是 `cluster.LeaderOf(slotID)`；当前 presence authority RPC 也是按 UID slot leader 路由，因此 presence authority owner 被包含在这个字段里。
- `ConfigEpoch` / `BalanceVersion` 来源是 controller 的 `SlotAssignment`；如果某 slot 暂时取不到 assignment 版本，不能把 0 当作命中，只能保守重建或返回 retryable。

tag 覆盖多个 UID slot 时，不能只取 max version。必须记录排序后的 `SlotAuthorityRefs`，比较时要求：

- `HashSlotTableVersion` 相等。
- `SlotAuthorityRefs` 的 slot 集合相等。
- 每个 slot 的 `LeaderNodeID`、`ConfigEpoch`、`BalanceVersion` 都相等。

`PartitionTopologyVersion` 不能只理解为旧 WuKongIM 的普通 `NodeVersion`。它必须由权威控制面提供；各节点只读取，不在本地推导。任何一个 slot ref 变化都必须重建或失效。未来如果 presence authority 从 slot leader 中独立出来，应在 `SlotAuthorityRef` 中增加显式 presence authority epoch，而不是恢复成模糊的全局版本。

tag 构建必须从单个权威控制面快照读取这些字段，不能混用“旧 hash-slot table version + 新 assignments”这类分段读取结果；如果当次读不到自洽快照，就必须重试或返回 retryable。

- leader 使用 tag 前发现 `tag.PartitionTopologyVersion` 与当前权威拓扑引用集合不同，必须重建。
- follower 本地缓存也按同样规则失效，不能继续使用旧分片。
- 这解决的是 UID 分片归属变化，不替代成员变更的 tagVersion 递增。

### TTL 处理冷频道和漏清理

每个 tag 维护 `LastAccess`：

- 后台 bucket 定期扫描。
- 超过 `WK_DELIVERY_TAG_EXPIRE` 后删除 tag。
- 如果 `channel -> tagKey` 指向的 tag 已不存在，也删除该 channel 映射。

TTL 只负责资源回收，不保证业务实时一致性。

### leader 切换

频道 leader 切换后，旧 leader 上的 tag 不再权威。

推荐处理：

- 新 leader 首次处理该频道消息时重新创建 tag incarnation，并生成新的 tagKey；如果权威 subscriber store 上的 `SubscriberMutationVersion` 已经更高，则直接以 store 版本重建，不沿用旧缓存。
- 节点请求 tag 时如果发现目标不是当前 leader，应返回 `not_leader` 或由调用方重新定位 leader。
- follower 缓存中的旧 tag 可因 tagKey 不匹配、tagVersion 不匹配、SubscriberMutationVersion 落后、PartitionTopologyVersion 变化、TTL 或请求失败而失效。

## 失败处理

### follower 获取 tag 失败

如果目标节点向 leader 获取 tag 分片失败：

- 对实时投递返回 retryable，而不是本地扫描订阅者创建 tag。
- delivery runtime 按已有重试机制退避重试。
- 多次失败后可以降级为本次实时投递 dropped，但消息本身仍已在 channel log 中持久化。

### leader 上 tagKey 或 version 不存在

可能原因：

- leader TTL 清理了旧 tag。
- leader 切换后旧 tagKey 不存在，或新 leader 只保留同一 tagKey 的更新版本。
- follower 收到的是旧事件。
- tagKey 存在，但请求 version 与 leader 当前 version 不一致。

处理策略：

- 如果请求带的是旧 tagKey 或旧 version，leader 不应强行用旧 key/version 创建旧快照。
- 可以返回 `tag_not_found` 或 `tag_version_mismatch`，由调用方重试并重新获取当前 channel tag。
- 对实时投递而言，最终可以依赖后续 committed replay / 客户端同步补偿，不应为了单次旧 tag miss/version mismatch 让 follower 本地创建 tag。
- 如果 leader 上的权威 `SubscriberMutationVersion` 已经前进，而请求携带的是旧版本，leader 应优先返回当前版本信息，而不是把旧请求写回 current ref。

### 成员变更时 leader 不可用

成员存储变更成功但 leader tag 更新失败时：

- 不回滚订阅者存储。
- 不允许只删除本地可见 tag 映射后结束；这不能保证频道 leader 上的完整 tag 失效。
- 必须依赖权威存储里已经递增的 `SubscriberMutationVersion` 作为最终失效凭据；pending invalidate / retry 只负责尽快把这个新版本送到 leader。
- leader 恢复后，下一条消息使用 tag 前必须先比较权威 `SubscriberMutationVersion`：能增量更新则递增 tagVersion，无法确认增量完整性则从 SubscriberSource 重建。

## 与在线索引方案的对比

### 相比 `channel -> online subscribers` 索引的优点

- 不需要在线/离线时更新大量频道索引。
- 不需要每个节点维护全频道在线成员集合。
- 不把连接 churn 放大成频道维度的状态 churn。
- 关注点更清晰：tag 管成员快照，presence 管在线状态。

### 相比在线索引方案的缺点

- 每条消息仍需要对 tag 分片内 UID 做在线判断。
- 当频道成员全部离线时，仍可能遍历该频道的 UID 内存分片，只是避免了订阅者存储扫描和跨节点全量 presence RPC。
- tag 创建和 miss 请求会集中到频道 leader，热点频道或大频道的 leader 压力更高。
- 成员频繁变化时，tagVersion 会频繁递增，导致 follower cache 命中率下降。
- 如果一个频道成员极多且消息很少，首次投递会有一次明显的 tag 构建成本。

## 优点

- leader-only 创建，语义清晰，避免多节点并发创建不一致快照。
- follower 只缓存本地分片，非 leader tag 只包含当前节点用户，内存不会按“节点数 * 全量成员”放大。
- 成员快照可跨多条消息复用，显著减少频道重复扫描订阅者存储；热门频道收益更明显。
- 不维护在线成员索引，避免高频上线/下线触发大规模频道状态更新。
- 使用 tagVersion 递增做缓存失效，tagKey 保持稳定，目标节点只需按 version mismatch 重新拉取。
- 可以与当前 ACK / retry / RPC chunk / subscriber 分页能力兼容演进，但 ACK/retry owner 必须明确下沉到目标分片节点。

## 缺点与风险

- 频道 leader 会承担 tag 构建、完整 tag 缓存、分片查询响应，热点频道或大频道可能进一步集中压力。
- tag 是最终一致缓存，不保证成员变化后所有旧事件立刻停止使用旧成员快照。
- tag 只优化成员解析，不解决大量在线连接的写放大、ACK 索引和慢连接问题。
- 如果 UID 分片节点与实际连接节点不同，目标分片节点仍需要按 presence authority 分组查询并二次转发。
- 成员高频变化会导致 tagVersion 高频递增，cache miss 和 leader 请求增多。
- leader 切换、TTL 过期、PartitionTopologyVersion 变化都会触发重建，需要明确 retry/fallback 行为。

## 建议落点

按照当前项目分层，建议拆成以下职责：

- `internal/runtime/deliverytag`：节点内 tag cache、bucket、TTL、tag incarnation、PartitionTopologyVersion 失效、channelTag/version 映射。
- `internal/usecase/delivery`：在 delivery resolver 中接入 tag 分片解析，但不直接依赖入口协议。
- `internal/access/node`：新增或复用节点 RPC，提供 `GetDeliveryTag` / `UpdateDeliveryTag` 之类的入口适配。
- `internal/app`：组合根装配 tag manager、RPC handler、delivery resolver 依赖。

实现时仍要避免新的大而全 service；leader 判断、RPC 转发、cache 操作应通过小接口注入。

## 推荐推进顺序

1. 先实现节点内 `DeliveryTagManager`，只覆盖 cache、TTL、tag incarnation、tagKey/channelKey/version 映射、PartitionTopologyVersion 失效。
2. 给权威 subscriber store 增加 `SubscriberMutationVersion` 持久化和读取能力，再接入 tag 复用判断。
3. 增加 SubscriberSource 抽象，覆盖个人频道、普通群、临时频道、cmd/派生频道和特殊频道。
4. 增加 leader-only 的 tag build / fetch RPC，并用单元测试证明 follower 不能创建 tag。
5. 增加 leader -> 目标分片节点的 tag 分片投递 RPC，明确分片任务状态和幂等键。
6. 在目标分片节点接入现有 route-based delivery runtime，由目标节点维护本分片 ACK/retry。
7. 把成员 Add / Remove / Reset / RemoveAll 接到权威 `SubscriberMutationVersion`、leader tagVersion 递增、dirty 标记和 pending invalidate retry。
8. 增加指标：tag build 次数、cache hit/miss、leader fetch latency、tag size、tag expired、tag not found、dirty rebuild、version mismatch、partition handoff retry。
9. 压测小频道、普通群、大频道和 10 万成员场景，对比无 tag、tag warm、tag cold、成员频繁变更、leader 切换等情况。

## 仍需保留的保护

即使引入 tag，也必须继续保留或加强：

- 单频道 inflight route 上限。
- ACK binding 上限和超时清理。
- 远端 push chunk。
- 目标分片节点到实际连接节点的 retryable / dropped 精确路由反馈。
- committed dispatcher 积压观测。
- 大频道 fanout 限流或降级策略。
- leader -> 目标分片节点的分片任务幂等和重试上限。

## 结论

Delivery Tag 方案适合作为所有频道实时投递的统一成员快照机制：它不追求维护精确在线索引，而是把频道成员集合变成 leader 权威、可复用、可失效的分片快照。

该方案可以统一小频道、普通群和大频道的投递成员解析路径，并显著减少热门频道重复读取订阅者存储和重复做跨节点成员规划的成本，同时避免每个节点维护大量在线订阅者索引。但它不能消除最终连接写入、ACK、慢客户端和 leader 热点问题，因此应与现有投递背压、RPC chunk、ACK 清理以及后续 fanout job 方案配合使用。
