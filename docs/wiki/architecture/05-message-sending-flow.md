# 发送消息完整流程

> 本文档描述一条消息从客户端发送到所有接收者的完整链路，涉及三层分布式架构的协作方式，并说明频道寻址和脑裂防护机制。

## 1. 流程总览

```
客户端 SendPacket
  │
  ▼
┌─────────────────────────────────────────────────────────────┐
│  接入层（Gateway）                                          │
│  OnFrame → mapSendCommand → message.App.Send                │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│  业务层（UseCase · Message）                                │
│  Send → NormalizePersonChannel → sendDurable                │
│    → sendWithMetaRefreshRetry → handler.Append              │
└─────────────────────┬───────────────────────────────────────┘
                      │ 频道寻址（三步定位）
                      ▼
┌─────────────────────────────────────────────────────────────┐
│  L2 Slot 层 — 元数据查询                                    │
│  HashSlotForKey(channelID, count) → CRC32 % HashSlotCount  │
│  → HashSlotTable.Lookup(hashSlot) → physical SlotID        │
│  → 从对应 Slot Raft Leader 获取 ChannelRuntimeMeta         │
│  → 得到 ISR Leader / Replicas / Epoch 等信息               │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│  L3 Channel 层 — ISR 消息写入                               │
│  replica.Append → Group Commit → 本地日志写入              │
│  → Follower 拉取（FetchRequest / FetchResponse）           │
│  → ProgressAck → HW 推进（MinISR 副本确认）                │
│  → 返回 MessageSeq                                         │
└─────────────────────┬───────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│  回包 + 异步投递                                            │
│  SendackPacket → 客户端                                     │
│  asyncCommittedDispatcher → 路由到 ISR Leader 节点          │
│  → 解析订阅者 → 解析在线端点 → Push RecvPacket 到接收者    │
└─────────────────────────────────────────────────────────────┘
```

## 2. 详细步骤

### 2.1 客户端发送（Gateway 接入）

**代码位置**：`internal/access/gateway/frame_router.go` · `internal/access/gateway/mapper.go`

1. 客户端通过 TCP 长连接发送 `SendPacket`（WuKong 二进制协议帧）。
2. Gateway 的 `OnFrame` 分发器根据帧类型路由到 `handleSend`。
3. `mapSendCommand()` 从 Session 中提取发送者 UID，并对**个人频道**做规范化处理——将双方 UID 按 CRC32 哈希值排序拼接为固定的 `channelID`（格式为 `UID_A@UID_B`），确保 A→B 和 B→A 对话使用同一个频道。
4. 创建带超时的 `context`（默认 10s），调用 `message.App.Send()`。

```
SendPacket
  → OnFrame(ctx, frame)
  → handleSend(ctx, pkt)
  → mapSendCommand(ctx, pkt)       // 提取 FromUID、规范化 channelID
  → message.App.Send(ctx, cmd)
```

### 2.2 业务校验与持久化写入（UseCase · Message）

**代码位置**：`internal/usecase/message/send.go` · `internal/usecase/message/retry.go`

1. **校验发送者**：`FromUID` 不能为空，频道类型必须是 Person 或 Group。
2. **个人频道规范化**：如果是 Person 类型，调用 `NormalizePersonChannel()` 确保 channelID 唯一且确定。
3. **构造持久消息**：填充时间戳、消息帧元数据、载荷等字段。
4. **带元数据刷新的追加**：调用 `sendWithMetaRefreshRetry()` 尝试写入频道日志。
   - 首次尝试直接 `cluster.Append()`
   - 如果返回 `ErrStaleMeta`、`ErrNotLeader` 或 `ErrRerouted`，进入 refresh/retry 路径
   - refresh 先从 Slot 层读取权威 `ChannelRuntimeMeta`
   - 若权威读取返回 `ErrNotFound`，则按当前 Slot 拓扑 bootstrap 一份运行时元数据；该 bootstrap 只依赖 slot peers 和 slot leader，不依赖业务 `channel-info` 是否存在
   - bootstrap 后会重新读取权威元数据，并以重读结果为准 `ApplyMeta` 到本地 ISR runtime，然后只重试一次 `Append`
   - 若重试时本地副本不是 Channel Leader，`appChannelCluster.Append()` 会按 meta 中的 Leader 通过 `internal/access/node/channel_append_rpc.go` 转发到真正的 Channel Leader
5. **异步投递**：写入成功后，调用 `dispatcher.SubmitCommitted()` 触发异步消息投递。
6. **返回结果**：将 `MessageID` 和 `MessageSeq` 封装为 `SendResult`。

```go
// retry.go — 元数据刷新重试核心逻辑
result, err := cluster.Append(ctx, req)
if shouldRefreshAndRetry(err) {   // ErrStaleMeta || ErrNotLeader || ErrRerouted
    meta, err := refresher.RefreshChannelMeta(ctx, req.ChannelID)
    cluster.ApplyMeta(meta)
    req.ExpectedChannelEpoch = meta.Epoch
    req.ExpectedLeaderEpoch = meta.LeaderEpoch
    result, err = cluster.Append(ctx, req)  // 重试
}
```

### 2.3 频道寻址（三步定位）

#### 第一步：Key → HashSlot（稳定哈希）

**代码位置**：`pkg/cluster/router.go` · `pkg/cluster/hashslot/hashslottable.go`

```go
func HashSlotForKey(key string, hashSlotCount uint16) uint16 {
    if hashSlotCount == 0 {
        return 0
    }
    return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}
```

- 将 `channelID` 字符串经 CRC32 哈希后，映射到固定范围 `[0, HashSlotCount)` 的逻辑分片。
- `HashSlotCount` 是 `HashSlotTable` 创建时的配置常量，控制"逻辑分片"数量；物理 Slot 数可以通过控制面动态增减。
- 所有节点使用相同算法，保证同一个 Key 永远落到同一个 hash slot。

#### 第二步：HashSlot → Physical Slot（查表）

**代码位置**：`pkg/cluster/hashslottable.go` · `pkg/cluster/router.go`

```go
func (r *Router) SlotForKey(key string) multiraft.SlotID {
    table := r.hashSlotTable.Load()
    if table == nil {
        return 0
    }
    return table.Lookup(r.HashSlotForKey(key))
}
```

- `HashSlotTable` 是 Controller 持久化的权威映射：`hashSlot -> physical SlotID`。
- 当执行 `AddSlot` / `RemoveSlot` / `Rebalance` 时，Controller 会更新 `HashSlotTable`，节点通过 `UpdateHashSlotTable` 刷新本地表。
- 这样"Key 的逻辑哈希"与"物理 Slot 的副本分布"被解耦：路由稳定，物理承载可动态调整。

#### 第三步：SlotID → ChannelRuntimeMeta → ISR Leader

**代码位置**：`pkg/slot/proxy/store.go` · `pkg/slot/proxy/authoritative_rpc.go` · `internal/app/channelmeta.go`

1. 根据 SlotID 找到该 Slot 的 Raft Leader 节点。
2. 从 Raft Leader 读取该频道的 `ChannelRuntimeMeta`，包含：

| 字段         | 说明                               |
| ------------ | ---------------------------------- |
| Leader       | ISR 组的 Leader 节点 ID            |
| Replicas     | 所有副本节点列表                   |
| ISR          | 同步副本集合                       |
| LeaderEpoch  | Leader 版本号                      |
| ChannelEpoch | 频道配置版本号                     |
| MinISR       | 最小同步副本数                     |
| LeaseUntilMS | Leader 租约到期时间（毫秒时间戳）  |
| Status       | 频道状态（Creating/Active/...）    |

3. **元数据缓存**：`channelMetaSync` 组件（`internal/app/channelmeta.go`）周期性（默认 1s）从 Slot 层拉取 `ChannelRuntimeMeta` 列表，通过 `ApplyMeta` 同步到本地 ISR 运行时，避免每次写入都远程查询。

4. **Leader 追踪**：如果 Slot RPC 请求到达非 Leader 节点，通过 `callAuthoritativeRPC`（`pkg/slot/proxy/authoritative_rpc.go`）的 Leader 追踪循环——遍历 Peers，根据 `rpcStatusNotLeader` 响应中的 `leaderID` 重定向到正确的 Leader。

```
寻址流程：
channelID = "group_abc"
    → CRC32("group_abc") % 256 = HashSlot(136)
    → HashSlotTable.Lookup(136) = SlotID(12)
    → SlotID(12) 的 Raft Leader 在 Node-2
    → Node-2 返回 ChannelRuntimeMeta{Leader: Node-3, ISR: [3,1,2]}
    → 消息写入路由到 Node-3
```

### 2.4 ISR 消息写入（Channel 层）

**代码位置**：`pkg/channel/handler/append.go` · `pkg/channel/replica/append.go` · `pkg/channel/replica/progress.go`

#### 2.4.1 入口校验

```
handler.Append(req)
  → Epoch 和 LeaderEpoch 是否匹配？（防止 stale 写入）
  → 频道是否正在删除或已删除？
  → MessageSeqFormat 兼容性检查（U64 格式需要客户端支持）
  → 当前节点是否为 Leader？
  → 幂等检查：(FromUID, ClientMsgNo) 是否已写入？
      ├─ PayloadHash 一致 → 返回缓存结果（真重复）
      └─ PayloadHash 不一致 → 返回冲突错误
```

#### 2.4.2 Leader Append（Group Commit）

```
replica.Append(records)
  → 角色校验：必须是 Leader（非 FencedLeader / Follower）
  → 租约校验：now < LeaseUntil（否则降级为 FencedLeader）
  → ISR 数量校验：len(ISR) >= MinISR
  → 进入 Group Commit 收集器：
      等待 1ms / 凑满 64 条 / 凑满 64KB（先到先触发）
  → 合并多个并发请求为一批
  → 写入本地日志 + fsync
  → LEO（Log End Offset）前进
```

#### 2.4.3 ISR 复制（拉模型）

```
Follower                                Leader
  │ FetchRequest ──────────────────────▸ │
  │  (fetchOffset, offsetEpoch,          │
  │   replicaID, maxBytes)               │
  │                                      │ Fetch: 读取日志 + 分歧检测
  │ ◀─────────── FetchResponse ─────────│
  │  (records, leaderHW, epoch,          │
  │   truncateTo?)                       │
  │                                      │
  │ ApplyFetch: 写入本地日志             │
  │                                      │
  │ ProgressAck ───────────────────────▸ │
  │  (matchOffset)                       │
```

- **Follower 发起拉取**：当 Follower 需要同步数据时，通过 `FetchRequest` RPC 向 Leader 拉取新记录，请求中携带自己的 `fetchOffset`、`offsetEpoch` 等信息。
- **Leader 处理 Fetch**：Leader 收到请求后执行分歧检测（`divergenceStateLocked`），如果发现日志分歧则返回 `truncateTo` 指令；否则返回新记录和当前 `leaderHW`。
- **Follower 应用数据**：Follower 通过 `ApplyFetch` 将收到的记录写入本地日志，如有截断指令则先截断。
- **进度确认**：Follower 发送 `ProgressAck(matchOffset)` 确认写入进度。
- **批量优化**：Transport 层支持 `FetchBatch`，将多个频道的 `FetchRequest` 合并为一次 RPC 调用。

#### 2.4.4 HW 推进

```go
// progress.go — HW 推进算法
matches := collect ISR members' matchOffset
slices.Sort(matches)  // 升序排列: [80, 90, 100]
candidate = matches[len(matches) - MinISR]
// 例：3 副本 ISR, MinISR=2 → candidate = matches[3-2] = matches[1] = 90
// 含义：至少 2 个副本确认了 offset ≤ 90 的数据
```

当候选 HW > 当前 HW 时：
1. 推进 HW
2. 写入 Checkpoint（HW + Epoch + LogStartOffset）到持久化存储
3. 唤醒等待中的 Append waiter
4. 返回 `MessageSeq = commit.BaseOffset + 1`（用户可见的 1-indexed 序号）

### 2.5 回包给发送者

**代码位置**：`internal/access/gateway/mapper.go`

写入成功后，Gateway 构造 `SendackPacket` 回传给客户端：

```go
ctx.WriteFrame(&frame.SendackPacket{
    MessageID:   result.MessageID,
    MessageSeq:  result.MessageSeq,
    ClientSeq:   pkt.ClientSeq,
    ClientMsgNo: pkt.ClientMsgNo,
    ReasonCode:  result.Reason,
})
```

### 2.6 异步消息投递

**代码位置**：`internal/app/deliveryrouting.go`

回包和投递是**解耦的**——回包后立即在新 goroutine 中执行投递。

#### 2.6.1 路由到 ISR Leader 节点

```
asyncCommittedDispatcher.SubmitCommitted(msg)
  → go routeCommitted(ctx, msg)
      → 单节点模式（preferLocal=true）→ 直接本地提交
      → 多节点模式：
          → channelLog.Status(channelKey) → 获取 ISR Leader
          → Leader 在本地 → submitLocal()（含 delivery + conversation）
          → Leader 在远端 → nodeClient.SubmitCommitted(nodeID, msg) via RPC
          → 获取 Leader 失败 → 重试 3 次（20ms × (attempt+1) 退避）
          → 全部失败 → 降级为仅更新会话列表（conversation fallback）
```

#### 2.6.2 解析订阅者与在线端点

```
localDeliveryResolver.ResolvePage()
  → subscribers.NextPage()           // 从 Slot 层分页获取频道订阅者列表
  → authority.EndpointsByUIDs(uids)  // 查询每个 UID 的在线端点
  → 返回 RouteKey 列表：{UID, NodeID, BootID, SessionID}
```

#### 2.6.3 推送 RecvPacket

```
distributedDeliveryPush.Push(cmd)
  → buildRealtimeRecvPacket(msg, recipientUID)
  → 个人频道特殊处理：解码 channelID 为 left@right，
    替换 RecvPacket.ChannelID 为对方的 UID
  → 按 NodeID 分组路由：
      ├─ 本地路由 → localDeliveryPush.pushEnvelope()
      │              → online.Connection(sessionID)
      │              → conn.Session.WriteFrame(recvPacket)  // TCP 直写
      │
      └─ 远端路由 → client.PushBatch(nodeID, pushCmd)
                     → RPC 发送预编码帧到目标节点
                     → 目标节点 WriteFrame 到接收者连接
```

### 2.7 接收者收到消息

接收者的 TCP 连接收到 `RecvPacket`，包含：
- MessageID / MessageSeq
- FromUID、ChannelID、ChannelType
- Payload（消息正文）
- Timestamp、MsgKey 等元数据

对于**个人频道**，接收者看到的 `ChannelID` 是对方的 UID（通过解码 `left@right` 格式的内部频道 ID 后取对方），便于客户端直接展示对话界面。

### 2.8 接收确认（RecvAck）

**代码位置**：`internal/usecase/message/recvack.go` · `internal/app/deliveryrouting.go`

接收者收到 `RecvPacket` 后发送 `RecvackPacket` 确认：

```
RecvackPacket(MessageID, MessageSeq)
  → message.App.RecvAck(cmd)
  → ackRouting.AckRoute(ctx, RouteAckCommand{UID, SessionID, MessageID, MessageSeq})
      → 查询 AckIndex 获取 OwnerNodeID
      → OwnerNodeID == 本地 → 直接 local.AckRoute()
      → OwnerNodeID != 本地 → notifier.NotifyAck(ownerNodeID, cmd) via RPC
      → 清除 AckIndex 绑定
```

## 3. 频道寻址机制详解

### 3.1 三层路由设计

WuKongIM 的频道寻址采用**三层路由**设计，把"稳定哈希""物理承载""频道 ISR 元数据"分离开：

```
                    ┌─────────────────┐
  channelID ──────▸ │   CRC32 Hash    │ ──▸ HashSlot
                    └─────────────────┘
                           │
                           ▼
                    ┌─────────────────┐
   HashSlot ───────▸│  HashSlotTable  │ ──▸ SlotID
                    └─────────────────┘
                           │
                           ▼
                    ┌─────────────────┐
      SlotID ──────▸│  Slot Raft      │ ──▸ ChannelRuntimeMeta
                    │  Leader         │      {Leader, ISR, Epoch, ...}
                    └─────────────────┘
                           │
                           ▼
                    ┌─────────────────┐
    ISR Leader ────▸│  Channel ISR    │ ──▸ 消息读写
                    │  Leader Node    │
                    └─────────────────┘
```

### 3.2 为什么不用一步直接定位

- **解耦逻辑哈希与物理承载**：频道的 `channelID → HashSlot` 是稳定的，而 `HashSlotTable` 可以把同一个 hash slot 在线迁移到新的物理 Slot。
- **元数据强一致**：通过 Raft 保证频道元数据的一致性，避免出现两个节点同时认为自己是 Leader 的情况。
- **支持动态扩缩容**：新增或移除 physical slot 时，只需要调整 `HashSlotTable` 并完成迁移，不必重算所有 Key 的逻辑哈希。

### 3.3 元数据缓存与刷新

每个节点本地缓存频道的 `ChannelRuntimeMeta`：

- **主动同步**：`channelMetaSync`（`internal/app/channelmeta.go`）周期性（默认 1s）从 Slot 层拉取 `ListChannelRuntimeMeta`，筛选本节点参与的频道，通过 `ApplyMeta` 同步到本地 ISR 运行时。当 `HashSlotTable` 版本变化时自动清空缓存重新同步。
- **被动刷新**：当写入返回 `ErrStaleMeta`、`ErrNotLeader` 或 `ErrRerouted` 时，`sendWithMetaRefreshRetry` 会走 `ErrNotFound -> bootstrap -> re-read -> apply -> retry` 的链路。从 Slot 权威源读不到运行时元数据时，refresh path 会先按 Slot 拓扑补齐 `ChannelRuntimeMeta`，再以权威重读结果为准完成本地应用和一次重试。
- **读路径保持纯读取**：目前生产路径中的发送链路 refresh path 会触发 runtime-meta bootstrap；普通元数据读路径仍然只返回权威存储中的现状，不主动创建业务或运行时元数据。
- **Epoch 保护**：每次写入携带 `ExpectedChannelEpoch` 和 `ExpectedLeaderEpoch`，Channel 层校验后才接受写入，防止过时的元数据导致数据写入错误的 Leader。

## 4. 脑裂防护机制

三层架构在每一层都有针对性的脑裂防护措施。

### 4.1 Controller 层：Raft 共识

- 使用标准 Raft 协议（etcd/raft v3），Leader 选举需要多数派同意。
- **PreVote**（`pkg/controller/raft/service.go:155`）：候选人在正式选举前先发 PreVote，只有得到多数同意才开始真正选举，防止分区后的节点用高 Term 干扰集群。
- **两阶段超时**（`pkg/controller/plane/statemachine.go`）：节点状态 Alive → Suspect（默认 3s）→ Dead（默认 10s），避免短暂网络抖动触发不必要的副本迁移。

### 4.2 Slot 层：PreVote + CheckQuorum

**代码位置**：`pkg/slot/multiraft/slot.go` · `pkg/slot/multiraft/types.go`

```go
rawNode, err := raft.NewRawNode(&raft.Config{
    CheckQuorum: raftOpts.CheckQuorum,
    PreVote:     raftOpts.PreVote,
    // ...
})
```

| 机制         | 作用                                                                 |
| ------------ | -------------------------------------------------------------------- |
| PreVote      | 分区节点无法获得多数派 PreVote 支持，不会发起真正选举，不会扰乱集群 |
| CheckQuorum  | Leader 周期性检查是否仍能与多数派通信，否则主动让出 Leadership       |

**请求转发**：Slot 层的 `callAuthoritativeRPC`（`pkg/slot/proxy/authoritative_rpc.go`）实现 Slot Leader 追踪循环；Channel Durable Append 则由 `internal/access/node/channel_append_rpc.go` 按 `ChannelRuntimeMeta.Leader` 做 channel-leader 转发，两者分别服务不同层级的 leader 路由。

### 4.3 Channel 层：Leader 租约 + FencedLeader + Epoch

这是三层中最关键的脑裂防护，因为 Channel 层使用的是 ISR 协议（非 Raft），Leader 选举由外部（Slot 层）指定，需要额外机制防止双写。

#### 4.3.1 Leader 租约（Lease）

**代码位置**：`pkg/channel/replica/append.go`

```go
if !r.now().Before(r.meta.LeaseUntil) {
    r.state.Role = channel.ReplicaRoleFencedLeader
    r.publishStateLocked()
    return channel.CommitResult{}, channel.ErrLeaseExpired
}
```

- 每个 Channel Leader 持有一个租约（`LeaseUntil` 时间戳）。
- 租约由 Slot 层在下发 `ChannelRuntimeMeta` 时设定和续期。
- **写入前必须校验租约有效**，过期则立即降级为 `FencedLeader`。
- FencedLeader 拒绝所有 Append 写入，但仍可响应 Fetch 请求（供 Follower 追数据）。

#### 4.3.2 FencedLeader 状态

```
               正常写入
  Leader ──────────────▸ 接受 Append
    │
    │ 租约过期
    ▼
  FencedLeader ────────▸ 拒绝 Append（ErrLeaseExpired）
    │                    仍可响应 Fetch
    │ 新元数据下发
    ▼
  Follower ────────────▸ 通过 FetchRequest 从新 Leader 拉取数据
```

#### 4.3.3 Epoch 保护

- **LeaderEpoch**：每次 Leader 切换时递增，写入请求必须携带匹配的 LeaderEpoch。
- **ChannelEpoch**：每次 ISR 成员变化时递增，防止过时的元数据导致错误路由。
- 新 Leader 就任时，**截断 LEO 到 HW**（`replica.go:BecomeLeader`），丢弃上一任 Leader 未提交的数据，确保不会出现幽灵写入。

#### 4.3.4 Epoch History 分歧检测

**代码位置**：`pkg/channel/replica/history.go` · `pkg/channel/replica/progress.go`

当发生 Leader 切换后，新旧 Leader 的日志可能出现分歧：

```
旧 Leader：[1, 2, 3, 4(epoch=1), 5(epoch=1)]
新 Leader：[1, 2, 3, 4(epoch=2), 5(epoch=2), 6(epoch=2)]

Follower(旧 Leader) FetchRequest(fetchOffset=6, offsetEpoch=1)
  → 新 Leader 查 EpochHistory：epoch=1 的 range = [startOffset, nextEpochStart)
  → divergenceStateLocked() 返回 truncateTo
  → Follower 截断到分歧点，重新从该点拉取新数据
```

### 4.4 脑裂防护总结

```
┌──────────────────────────────────────────────────────────────────┐
│                    脑裂防护全景                                  │
├──────────┬───────────────────────────────────────────────────────┤
│ L1 层    │ Raft 多数派选举 + PreVote 防扰动                     │
│ Controller│ 两阶段超时（Suspect 3s → Dead 10s）防抖动           │
├──────────┼───────────────────────────────────────────────────────┤
│ L2 层    │ MultiRaft PreVote + CheckQuorum                      │
│ Slot     │ Leader 主动让位 + callAuthoritativeRPC Leader 追踪   │
├──────────┼───────────────────────────────────────────────────────┤
│ L3 层    │ Leader 租约（Lease）→ 过期降级为 FencedLeader        │
│ Channel  │ Epoch 保护 → 旧 Leader 写入被拒绝                   │
│          │ 新 Leader 截断 LEO 到 HW → 丢弃未提交数据           │
│          │ EpochHistory → divergenceStateLocked 精确定位分歧点  │
└──────────┴───────────────────────────────────────────────────────┘
```

## 5. 关键设计决策

### 5.1 为什么回包和投递解耦

- 消息一旦写入 ISR 并达到 HW 即视为**已提交**，不会丢失。
- 回包延迟只取决于 ISR 写入延迟（通常 < 10ms），不受投递解析影响。
- 投递失败（如接收者离线）不影响消息持久化，后续可通过拉取接口获取。

### 5.2 为什么投递要路由到 ISR Leader

- ISR Leader 是频道的"所有者"，集中在 Leader 节点做投递可以：
  - 避免重复投递（多节点同时投递同一消息）
  - 统一管理投递状态（ACK 跟踪、离线通知）
  - 简化会话更新的一致性

### 5.3 个人频道的特殊处理

**代码位置**：`internal/runtime/channelid/person.go` · `internal/app/deliveryrouting.go`

- **发送端**：`EncodePersonChannel` 将双方 UID 按 CRC32 哈希值排序，哈希值大的在前，拼接为 `UID_A@UID_B` 的确定性形式（哈希相等时按字符串比较），确保双向对话共用同一个频道。
- **接收端**：`recipientChannelView` 解码 `left@right` 格式的内部频道 ID，将 RecvPacket 的 channelID 替换为对方的 UID，方便客户端直接展示"来自谁的消息"。

```go
// person.go — 个人频道编码
func EncodePersonChannel(leftUID, rightUID string) string {
    leftHash := crc32.ChecksumIEEE([]byte(leftUID))
    rightHash := crc32.ChecksumIEEE([]byte(rightUID))
    if leftHash > rightHash {
        return leftUID + "@" + rightUID
    }
    if leftHash == rightHash && leftUID > rightUID {
        return leftUID + "@" + rightUID
    }
    return rightUID + "@" + leftUID
}
```

### 5.4 ISR 复制为什么用拉模型

- **Follower 自主调度**：每个 Follower 根据自己的 LEO 和状态发起 FetchRequest，避免 Leader 需要维护所有 Follower 的推送状态。
- **天然背压**：Follower 处理完一批数据后再发起下一次 Fetch，不会被 Leader 推送淹没。
- **批量优化**：Transport 层的 `FetchBatch` 将多个频道的 Fetch 请求合并为一次 RPC，减少网络开销。
- **分歧处理简洁**：Leader 在处理 Fetch 时通过 `divergenceStateLocked` 检测日志分歧，在同一个请求-响应周期内完成截断指令的下发。

## 6. 错误处理与容错

| 错误场景                | 处理方式                                                |
| ----------------------- | ------------------------------------------------------- |
| Slot Leader 不可达      | `callAuthoritativeRPC` Leader 追踪循环，尝试所有 Peers  |
| Channel 元数据过期      | `sendWithMetaRefreshRetry` 正常 refresh，按权威结果 re-read -> apply -> retry |
| Channel 元数据缺失      | 权威读取返回 `ErrNotFound` 时执行 `bootstrap -> re-read -> apply -> retry` |
| ISR Leader 租约过期     | 降级为 FencedLeader，返回 `ErrLeaseExpired`，触发重试   |
| ISR 副本不足            | `len(ISR) < MinISR` 时拒绝写入，返回错误                |
| 投递 Leader 查找失败    | 重试 3 次后降级为仅更新会话列表（conversation fallback）|
| 远端节点推送失败        | 标记为 Retryable，由投递运行时调度重试                  |
| 消息重复发送            | 幂等检查 (FromUID, ClientMsgNo) + PayloadHash，返回缓存 |
| 协议版本不兼容          | MessageSeqFormat 检查，返回 `ErrProtocolUpgradeRequired` |
