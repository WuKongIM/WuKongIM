# internal 流程文档

## 1. 职责定位

IM 业务应用层。负责客户端连接管理（Gateway）、消息收发、在线状态（Presence）、投递（Delivery）、会话同步（Conversation）、HTTP API 接入、节点间 RPC 协调。
**不负责**: 分布式共识与 Slot 编排（由 `pkg/cluster` 负责）、Channel Log 复制（由 `pkg/channel` 负责）、元数据状态机与存储（由 `pkg/slot/fsm` + `pkg/slot/meta` 负责）。

## 2. 核心组件分工

| 组件 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `app.App` | `app/app.go:30` | 主入口：聚合所有子系统、构建依赖图、管理启停生命周期 |
| `gateway.Gateway` | `gateway/gateway.go:14` | 网关层：管理 Transport/Protocol/Session，双向实时通信 |
| `access/gateway.Handler` | `access/gateway/handler.go:43` | 网关桥接：将 Frame 路由到 Usecase 层 |
| `access/api.Server` | `access/api/server.go:57` | HTTP API：服务端消息发送、Token 管理、会话同步、健康检查 |
| `access/node.Client` | `access/node/options.go:111` | 节点间 RPC 客户端：投递提交/推送/确认、Presence、会话事实、channel leader repair/evaluate |
| `access/node.Adapter` | `access/node/options.go:64` | 节点间 RPC 服务端：注册到集群 RPCMux，处理入站 RPC（含 channel leader repair/evaluate） |
| `usecase/message.App` | `usecase/message/app.go:36` | 消息用例：发送、接收确认、会话关闭处理 |
| `usecase/delivery.App` | `usecase/delivery/app.go:10` | 投递用例：提交投递、确认、离线处理 |
| `usecase/presence.App` | `usecase/presence/app.go:31` | 在线状态用例：激活/去激活、权威路由注册、心跳 |
| `usecase/conversation.App` | `usecase/conversation/app.go:32` | 会话用例：增量/全量同步、冷热分离 |
| `runtime/delivery.Manager` | `runtime/delivery/manager.go:14` | 投递运行时：分片 Actor 模型、重试轮、Ack 索引 |
| `runtime/online.MemoryRegistry` | `runtime/online/registry.go:12` | 在线注册表：按 SessionID/UID/SlotID 索引连接 |
| `runtime/messageid` | `runtime/messageid/` | Snowflake 分布式 ID 生成 |
| `runtime/sequence` | `runtime/sequence/` | 序列号分配 |

## 3. 对外接口

```go
// app — 应用层唯一入口
app.New(Config) (*App, error)
App.Start() / Stop()
App.Cluster() / App.ChannelLog() / App.Store()
App.Message() / App.Conversation() / App.ConversationProjector()
App.Gateway() / App.GatewayHandler() / App.API()
App.DB() / App.RaftDB() / App.ChannelLogDB() / App.ISRRuntime()

// Gateway Handler — 网关事件回调
Handler.OnSessionActivate(ctx) / OnSessionOpen(ctx) / OnSessionClose(ctx)
Handler.OnFrame(ctx, frame) / OnListenerError(listener, err) / OnSessionError(ctx, err)

// Message Usecase — 消息操作
message.App.Send(ctx, SendCommand) (SendResult, error)
message.App.RecvAck(RecvAckCommand) error
message.App.SessionClosed(SessionClosedCommand) error

// Presence Usecase — 在线状态操作
presence.App.Activate(ctx, ActivateCommand) error
presence.App.Deactivate(ctx, DeactivateCommand) error
presence.App.HeartbeatAuthoritative(ctx) error
presence.App.ApplyRouteAction(ctx, RouteAction) error

// Conversation Usecase — 会话操作
conversation.App.Sync(ctx, SyncQuery) (SyncResult, error)

// Delivery Runtime — 投递操作
delivery.Manager.Submit(ctx, CommittedEnvelope) error
delivery.Manager.AckRoute(ctx, RouteAck) error
delivery.Manager.SessionClosed(ctx, SessionClosed) error
delivery.Manager.ProcessRetryTicks(ctx) error
delivery.Manager.SweepIdle()

// Online Registry — 在线注册表
online.Registry.Register(conn) / Unregister(sessionID)
online.Registry.Connection(sessionID) / ConnectionsByUID(uid)
online.Registry.ActiveConnectionsBySlot(slotID)
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `App` | app/app.go:30 | 核心结构体，聚合 Cluster/ChannelLog/Gateway/所有 Usecase/Runtime |
| `Config` | app/config.go:15 | 配置容器：Node, Storage, Cluster, API, Gateway, Conversation, Observability, Log |
| `Gateway` | gateway/gateway.go:14 | 网关：管理 Listener/Session/Protocol 注册 |
| `Server` | gateway/core/server.go:27 | 网关核心服务器：管理监听器、协议分发 |
| `Session` | gateway/session/session.go:17 | 客户端会话：写队列、状态管理、Frame 读写 |
| `Handler` | access/gateway/handler.go:43 | 网关桥接层：持有 Online/Messages/Presence 引用 |
| `Server` | access/api/server.go:57 | HTTP API 服务器：消息发送、Token、会话同步、健康检查 |
| `Client` | access/node/options.go:111 | 节点间 RPC 客户端：投递/Presence/会话事实/channel leader repair |
| `Adapter` | access/node/options.go:64 | 节点间 RPC 服务端适配器：含 channel leader repair/evaluate handler |
| `message.App` | usecase/message/app.go:36 | 消息用例：持有 Cluster/Online/CommittedDispatcher/DeliveryAck |
| `delivery.App` | usecase/delivery/app.go:10 | 投递用例：封装 delivery.Manager |
| `presence.App` | usecase/presence/app.go:31 | 在线状态用例：持有 Online/Router/AuthorityClient/ActionDispatcher |
| `conversation.App` | usecase/conversation/app.go:32 | 会话用例：持有 States/ChannelUpdate/Facts |
| `Manager` | runtime/delivery/manager.go:14 | 投递管理器：分片 → Actor → Mailbox → RetryWheel |
| `shard` | runtime/delivery/shard.go:9 | 投递分片：按 ChannelKey hash 分配 |
| `actor` | runtime/delivery/actor.go:11 | 投递 Actor：单 Channel 的投递状态机 |
| `AckIndex` | runtime/delivery/ | Ack 索引：SessionID+MessageID → AckBinding 映射 |
| `MemoryRegistry` | runtime/online/registry.go:12 | 在线注册表：按 SessionID/UID/SlotID 三维索引 |
| `OnlineConn` | runtime/online/ | 连接元数据：UID/DeviceID/SlotID/Session/State |

## 5. 核心流程

### 5.1 启动

入口: `app/app.go:91 New` → `app/build.go:39 build` → `app/lifecycle.go:15 Start`

```
New(Config):
  → build(cfg):
    ① cfg.ApplyDefaultsAndValidate()
    ② 创建 Logger
    ③ 打开数据库: metadb.Open → raftstorage.Open → channelstore.Open
    ④ 创建 Cluster: raftcluster.NewCluster(runtimeConfig)
       → observer hook:
         OnLeaderChange: 调度热 slot 的 authoritative reread
         OnNodeStatusChange: 更新 channelMetaSync.nodeLiveness cache
    ⑤ 创建 Channel Log 基础设施:
       messageid.NewSnowflakeGenerator → dataPlanePool → dataPlaneClient
       → channeltransport.New → channelruntime.New → appChannelCluster
       → channeltransport.NewProbeClient（供权威 leader repair 候选评估复用 `ReconcileProbe` RPC）
       → runtime 会读取 FollowerReplicationRetryInterval；leader replica 会读取 AppendGroupCommitMaxWait/Records/Bytes；
         三节点压测场景可额外放大 data-plane pool / fetch inflight / pending fetch 并发
    ⑥ 创建 Store: metastore.New(cluster, db)
    ⑦ 创建 Conversation:
       conversationusecase.NewProjector → store.RegisterChannelUpdateOverlay
       → conversationusecase.New
    ⑧ 创建 ChannelMetaSync（含 node liveness cache / leader repair coordinator）
    ⑨ 创建 Presence:
       online.NewRegistry → presenceAuthorityClient → presence.New
       → presenceWorker
    ⑩ 创建 Delivery:
       deliveryruntime.NewAckIndex → SubscriberResolver
       → deliveryruntime.NewManager(shards, resolver, push)
       → deliveryusecase.New
    ⑪ 创建 CommittedDispatcher: asyncCommittedDispatcher
    ⑫ 创建 Node Access: accessnode.New (注册节点间 RPC Handler，包括 channel leader repair/evaluate)
    ⑬ 创建 Message: message.New(cluster, online, dispatcher, deliveryAck)
    ⑭ 创建 User: userusecase.New
    ⑮ 创建 API: accessapi.New (HTTP 服务器)
    ⑯ 创建 Gateway Handler: accessgateway.New
    ⑰ 创建 Gateway: gateway.New(handler, authenticator, listeners)

Start():
  ① startCluster → cluster.Start()
  ② waitForManagedSlotsReady (超时 10s)
  ③ startChannelMetaSync → channelMetaSync.Start()（启动轻量 active-slot leader watcher；不再做全量 channel runtime meta scan；slot leader 变化只触发权威 reread / 本地 apply；refresh worker 绑定 ChannelMetaSync 生命周期，Stop 会先取消并等待这些后台 refresh 退出；若刷新发现权威 leader 缺失/已死/不在副本集，则由当前 slot leader 走权威修复并持久化新的 channel leader）
  ④ startPresence → presenceWorker.Start()
  ⑤ startConversationProjector → conversationProjector.Start()
  ⑥ startGateway → gateway.Start()
  ⑦ startAPI → api.Start()
  任一步骤失败 → 反向回滚已启动组件
```

### 5.2 停止

入口: `app/lifecycle.go:87 Stop`

```
Stop():
  stopOnce.Do:
    ① stopAPI (5s 超时)
    ② stopGateway
    ③ stopConversationProjector
    ④ stopPresence
    ⑤ stopChannelMetaSync
    ⑥ stopCluster
    ⑦ closeChannelLogDB + dataPlaneClient.Stop + dataPlanePool.Close
    ⑧ closeRaftDB
    ⑨ closeWKDB (metadb)
    ⑩ syncLogger
  所有错误通过 errors.Join 聚合返回
```

### 5.3 消息发送（Gateway 路径）

入口: `access/gateway/frame_router.go:12 OnFrame`

```
客户端 SendPacket 到达 Gateway
  ↓
Handler.OnFrame(ctx, frame):
  ① 类型分发: SendPacket → handleSend / RecvackPacket → handleRecvAck / PingPacket → handlePing
     → handlePing(ctx): 直接 writePong(ctx)，不进入 usecase
  ↓
handleSend(ctx, pkt):
  ② decryptSendPacketIfNeeded: 解密（如需要），失败 → writeSendack(ReasonNotSupportChannelType)
  ③ mapSendCommand(ctx, pkt): 从 Session 提取 UID/SessionID/ProtocolVersion，构建 SendCommand
  ④ context.WithTimeout(sendTimeout=10s)
  ⑤ messages.Send(ctx, cmd):
     ↓
message.App.Send(ctx, cmd):
  ⑥ 校验 FromUID 非空（拒绝未认证发送者）
  ⑦ 校验 ChannelType: 仅支持 Person / Group
  ⑧ Person 频道: NormalizePersonChannel(fromUID, channelID) 规范化频道 ID
  ⑨ sendDurable(ctx, cmd):
     a. buildDurableMessage(cmd, now): 构建持久化消息
     b. sendWithMetaRefreshRetry: 带元数据刷新重试的 Append
        → cluster.Append(channelID, message)
        → 只有当 Channel Leader 把日志写入提交到跨频道 Pebble durable batching，
          且 MinISR 对应的 quorum 已推进运行时 CommitHW 后，Append 才返回给应用层；
          leader 本地 Checkpoint 持久化改为后台 coalescing，不再阻塞 sendack
        → 若当前 leader 已完成选主但副本仍处于 `CommitReady=false` 的 provisional 状态，
          Append 会快速返回 `ErrNotReady`；这表示副本还在做 quorum-safe tail reconcile，
          不是 meta 过期
        → 失败且 ErrStaleMeta / ErrNotLeader / ErrRerouted → refresher.Refresh
        → 权威运行时元数据缺失(ErrNotFound) 时，按 Slot 拓扑 bootstrap RuntimeMeta
        → 若权威 RuntimeMeta 的 leader lease 已过期/即将过期，只有当前 Channel Leader 所在节点会先做一次权威续租；Refresh 不再因为 Slot leader 变化去重写 channel leader / replicas / ISR
        → 重新读取权威 RuntimeMeta → apply 到本地 ISR runtime → 返回已应用视图给发送路径 → 重试一次 Append
        → 若重试后的本地状态表明当前节点不是 Channel Leader，或刷新后本地 runtime 已被移除但路由 meta 指向远端 leader，
          appChannelCluster 会按 meta 中的 Leader 通过 `access/node/channel_append_rpc.go` 转发追加请求；
          但即使已经转发到 leader，leader 仍可能因 `CommitReady=false` 临时拒绝写入
     c. Append 成功 → 获得 MessageID + MessageSeq
     d. dispatcher.SubmitCommitted(ctx, committedMessage):
        → asyncCommittedDispatcher 异步分发已提交消息
        → 同时携带 SenderSessionID（仅用于实时投递过滤当前发起发送的连接，不写入 durable log）
        → 触发 Delivery + Conversation 投影
  ⑩ 返回 SendResult{MessageID, MessageSeq, ReasonSuccess}
  ↓
writeSendack(ctx, pkt, result): 写回 SendackPacket 到客户端
```

### 5.4 消息发送（API 路径）

入口: `access/api/message_send.go`

```
HTTP POST /api/message/send
  ↓
API Server 解析请求体
  ↓
messages.Send(ctx, cmd):
  → 同 5.3 步骤 ⑥-⑩
  ↓
返回 HTTP 响应 {message_id, message_seq}
```

### 5.5 已提交消息分发

入口: `app/build.go asyncCommittedDispatcher`

```
asyncCommittedDispatcher.SubmitCommitted(ctx, message):
  ① 判断 preferLocal:
     本地节点是 Channel Leader → 本地处理
     否则 → nodeClient RPC 转发到 Leader 节点
  ② 本地处理:
     a. delivery.Submit(ctx, committedEnvelope):
        → deliveryRuntime.Manager.Submit [见 5.6]
        → envelope 内保留 SenderSessionID，用于过滤当前发送连接
     b. conversation.SubmitCommitted(ctx, message):
        → conversationProjector 更新会话投影
```

### 5.6 投递运行时

入口: `runtime/delivery/manager.go:76 Submit`

```
Manager.Submit(ctx, env):
  ① shardFor(channelKey): FNV32a hash → 选择 shard
  ② shard.submit(ctx, env):
     a. actorFor(key): 获取或创建 Channel 对应的 Actor
     b. actor.handleStartDispatch(ctx, env):
        ↓
Actor 状态机:
  ③ Resolve 阶段:
     → Resolver.ResolveEndpoints(channelID, channelType)
     → 获取所有订阅者的在线端点 (UID → NodeID/SessionID)
     → 分页解析 (resolvePageSize=256)
  ④ Push 阶段:
     → 遍历每个端点:
        本地端点: localDeliveryPush → online.Registry 查找 Session → Session.WriteFrame
        远程端点: nodeClient RPC → 目标节点 localDeliveryPush
     → 若 route.SessionID == SenderSessionID：跳过当前发起发送的连接
     → 同 UID 的其他在线 Session 仍正常收到 RecvPacket
     → AckIndex.Bind(sessionID, messageID, channelKey, route)
  ⑤ 等待 Ack:
     → 客户端收到消息后发送 RecvackPacket
     → Manager.AckRoute → actor.handleRouteAck → 清除 binding
  ⑥ 重试 (失败时):
     → shard.nextRetryDelay(attempt): 指数退避 [500ms, 1s, 2s]
     → RetryWheel.Schedule(entry)
     → processRetryTicks: 周期扫描到期条目 → 重新 Push
  ⑦ 离线处理:
     → SessionClosed → actor.handleRouteOffline
     → 触发离线消息存储
  ⑧ 空闲清理:
     → SweepIdle: 超过 idleTimeout(1min) 的 Actor 被回收
```

### 5.7 在线状态（Presence）

入口: `usecase/presence/gateway.go:34 Activate`

```
客户端连接认证成功:
  ↓
Handler.OnSessionActivate(ctx):
  ① activateCommandFromContext: 从 Context 提取 UID/DeviceID/DeviceFlag 等
  ② presence.Activate(ctx, cmd):
     a. localConnFromActivate(cmd): 构建 OnlineConn
        → router.SlotForKey(uid) 计算 SlotID
     b. online.Register(conn): 注册到本地在线表
     c. authority.RegisterAuthoritative(ctx, cmd):
        → 计算 SlotID → RPC 到 Slot Leader
        → Leader 侧: Raft Propose 写入权威路由
        → 返回 RouteAction 列表 (踢旧设备等)
     d. dispatchActions(ctx, actions):
        → 遍历 actions → ApplyRouteAction:
           本地 action: MarkClosing → kick → 延迟 Close
           远程 action: nodeClient RPC 转发
     e. 失败回滚: Unregister + bestEffortUnregister

客户端断开连接:
  ↓
Handler.OnSessionClose(ctx):
  ① messages.SessionClosed: 通知消息层清理
  ② presence.Deactivate(ctx, cmd):
     a. online.Connection(sessionID) → 查找连接
     b. online.Unregister(sessionID)
     c. bestEffortUnregister: RPC 到 Slot Leader 注销权威路由

Presence 心跳 (后台):
  presenceWorker 周期执行:
    → presenceApp.HeartbeatAuthoritative(ctx)
    → 续租权威路由 lease
  active slot 的 Slot Leader 发生变化时:
    → presenceWorker 立即补一次 HeartbeatOnce
    → 若新 leader 返回 route digest mismatch
       → ReplayAuthoritative 重新灌入当前节点该 slot 的 active routes
```

### 5.8 会话同步

入口: `usecase/conversation/sync.go:48 Sync`

```
API: GET /api/conversation/sync?uid=X&version=V&limit=N
  ↓
conversation.App.Sync(ctx, query):
  ① 加载活跃会话:
     states.ListUserConversationActive(uid, activeScanLimit=2000)
     → addActiveCandidates: 关联 ChannelUpdateLog
     → 冷会话 (lastMsgAt > coldThreshold=30天): 异步降级 ClearActiveAt
  ② 加载客户端 overlay 候选:
     addOverlayCandidates: query.LastMsgSeqs 中的频道
     → 有 state → 合并; 无 state → 标记 overlay
  ③ 增量收集 (version > 0):
     collectIncrementalViews:
     → ScanUserConversationStatePage 分页扫描
     → BatchGetChannelUpdateLogs 批量查更新
     → buildIncrementalCandidate: state.UpdatedAt > version 或 update.UpdatedAt > version
     → incrementalViewRetainer: 小顶堆保留 top-N
  ④ 加载最新消息:
     facts.LoadLatestMessages(ctx, keys):
     → 本地 Channel: cluster.Status → cluster.Fetch(lastSeq, limit=1)
     → 远程 Channel (ErrStaleMeta): 查 ChannelRuntimeMeta → nodeClient RPC
     → `conversation_facts` 目标节点若本地 `Status/Fetch` 命中 `ErrStaleMeta`，会先 `RefreshChannelMeta` 再重试一次本地读取
     → 支持 batch 优化: LoadLatestConversationMessages
  ⑤ 构建视图:
     buildSyncConversationView:
     → 计算 unread = lastMsgSeq - max(readSeq, deletedToSeq)
     → 自己发的消息: unread=0, readedTo=lastMsgSeq
     → onlyUnread 过滤
  ⑥ 排序 + 截断:
     按 displayUpdatedAt 降序 → 取 top limit
  ⑦ 加载最近消息 (msgCount > 0):
     LoadRecentMessagesBatch → filterVisibleRecents(deletedToSeq)
  ⑧ 返回 SyncResult{Conversations}
```

### 5.9 接收确认（RecvAck）

入口: `access/gateway/frame_router.go:72 handleRecvAck`

```
客户端 RecvackPacket 到达:
  ↓
handleRecvAck(ctx, pkt):
  ① mapRecvAckCommand(ctx, pkt): 提取 MessageID/SessionID
  ② messages.RecvAck(cmd):
     → deliveryAck 路由:
        本地: deliveryApp.AckRoute → Manager.AckRoute
          → AckIndex.Lookup(sessionID, messageID) → AckBinding
          → shard.routeAcked → actor.handleRouteAck
        远程: nodeClient RPC 转发到目标节点
```

## 6. RPC Service（节点间通信）

| 操作 | 方向 | 文件 | 说明 |
|------|------|------|------|
| `delivery_submit` | → remote | access/node/delivery_submit_rpc.go | 提交已提交消息到 Leader 节点投递 |
| `delivery_push` | → remote | access/node/delivery_push_rpc.go | 推送消息到目标节点的在线 Session |
| `delivery_ack` | → remote | access/node/delivery_ack_rpc.go | 转发 RecvAck 到投递所在节点 |
| `delivery_offline` | → remote | access/node/delivery_offline_rpc.go | 通知离线消息处理 |
| `presence` | → slot leader | access/node/presence_rpc.go | 权威路由注册/注销/心跳 |
| `conversation_facts` | → channel leader | access/node/conversation_facts_rpc.go | 远程加载会话最新/最近消息；命中 `ErrStaleMeta` 时先刷新 channel meta 再重试一次本地读取 |
| `channel_append` | → channel leader | access/node/channel_append_rpc.go | 非 Leader 副本将 Durable Append 转发到 Channel Leader；leader 若仍在 replica reconcile（`CommitReady=false`）会返回 `ErrNotReady` |
| `channel_leader_repair` | → slot leader | access/node/channel_leader_repair_rpc.go | 非 slot leader 节点请求当前 slot leader 权威修复 ChannelRuntimeMeta.Leader |
| `channel_leader_evaluate` | → candidate replica | access/node/channel_leader_evaluate_rpc.go | 请求某个 ISR 副本基于本地 durable state + probe proof 评估自己是否可安全接任 channel leader |

## 7. 错误处理

| 常量 | 含义 | 位置 |
|------|------|------|
| `ErrNotBuilt` | App 未完成构建 | app/ |
| `ErrStopped` | App 已停止 | app/ |
| `ErrAlreadyStarted` | App 已启动 | app/ |
| `ErrInvalidConfig` | 配置校验失败 | app/ |
| `ErrUnauthenticatedSender` | 发送者未认证 (FromUID 为空) | usecase/message/ |
| `ErrClusterRequired` | 消息发送需要 Cluster | usecase/message/ |
| `ErrUnsupportedFrame` | 不支持的 Frame 类型 | access/gateway/ |
| `ErrUnauthenticatedSession` | 未认证的 Session | access/gateway/ |
| `ErrPresenceRequired` | Presence 用例未注入 | access/gateway/ |
| `ErrRouterRequired` | Presence 路由器未注入 | usecase/presence/ |
| `ErrSessionRequired` | Activate 缺少 Session | usecase/presence/ |
| `ErrRemoteActionDispatchRequired` | RouteAction 需要远程分发 | usecase/presence/ |

## 8. 避坑清单

- **启动顺序严格**: `Start()` 按 Cluster → WaitReady → ChannelMetaSync → Presence → Conversation → Gateway → API 顺序启动，任一步骤失败会反向回滚已启动组件。不要尝试跳过或重排。
- **停止顺序相反**: `Stop()` 先停 API/Gateway（停止接入新请求），再停业务层，最后停 Cluster 和关闭数据库。`stopOnce` 保证幂等。
- **Person 频道 ID 规范化**: 发送到 Person 频道时，`NormalizePersonChannel(fromUID, channelID)` 会将两个 UID 排序拼接为统一的 channelID。直接使用原始 channelID 会导致同一对话产生两个不同的 Channel。
- **sendWithMetaRefreshRetry 只刷新一次权威结果，但可能顺带触发权威 leader repair**: 消息 Append 遇到 `ErrStaleMeta`、`ErrNotLeader` 或 `ErrRerouted` 时会触发一次刷新重试。若权威 `ChannelRuntimeMeta` 读到 `ErrNotFound`，刷新路径会先按 Slot 拓扑 bootstrap 缺失的运行时元数据；若读到的权威元数据 lease 已过期/即将过期，则当前 Channel Leader 所在节点会先尝试权威续租；若 lease 已经失效且本次刷新不在 leader 本机，刷新路径会把它当作 `leader_lease_expired` 交给当前 slot leader 重新选择/确认可写 leader，避免继续把写流量转发到一个已经失去租约却尚未被 controller 标 dead 的旧 leader。刷新后的权威 meta 若命中 `leader_missing` / `leader_not_replica` / `leader_dead` / `leader_draining` / `leader_lease_expired`，则会由当前 slot leader 选举并持久化新的 channel leader；持久化步骤只允许当前本地 slot leader 提案，若 repair 过程中 slot leadership 翻转，则会重新路由到新的 slot leader 重新执行，然后重新读取权威结果、应用到本地 ISR runtime，并把这份已应用视图返回给发送路径。重试后的本地 `Append` 若发现当前节点只是 Follower，或刷新后本地 runtime 已被移除但最新路由 meta 指向远端 leader，会按最新 meta 中的 Leader 走 node RPC 转发。
- **权威 runtime-meta reconcile ≠ replica reconcile**: `channelMetaSync` / `sendWithMetaRefreshRetry` 里的 reconcile 现在只负责两件事：当前 Channel Leader 的 lease 续租，以及在权威 leader 缺失/失效（包括 lease 已失效但 leader 尚未被 controller 判 dead）时由 slot leader 持久化新的 `ChannelRuntimeMeta.Leader` / `LeaderEpoch` / `LeaseUntilMS`；它不再把权威 `ChannelRuntimeMeta` 的 leader / replicas / ISR 跟着 Slot 拓扑漂移。`pkg/channel` 里的 replica reconcile probe 则负责在启动或 leader transfer 后重建 quorum-safe `CommitHW`，把副本从 `CommitReady=false` 拉到可服务状态。前者解决“路由是否仍然有效”，后者解决“现在是否安全可写”。
- **channelMetaSync 不再做每秒全量 scan**: steady-state 不再 `ListChannelRuntimeMeta()` 预热本地所有副本频道；业务路径按 `ChannelID` 激活，复制路径按 `ChannelKey` 激活，只保留热 runtime。
- **active-slot leader watcher 只盯热 slot**: `channelMetaSync.Start()` 现在只轮询当前热本地 runtime 所在的 physical slot leader；检测到 slot leader 变化后，仅刷新该 slot 下已激活 channel 的权威 `ChannelRuntimeMeta` 并重新 apply。除非这次 reread 发现权威 leader 已缺失/失效，否则不会改写 channel leader。
- **runtime-meta bootstrap 与续租职责已拆开**: runtime-meta 的 bootstrap 仍依赖 `SlotForKey`、Slot peers 和当前 Slot leader；后续 refresh/reconcile 只负责当前 Channel Leader 的 lease 续租，不再把 leader / replicas / ISR 重新投影成 Slot 拓扑。通用读路径仍保持纯读取语义，写路径和主动同步路径负责把权威运行时元数据补齐并续租。
- **Refresh 热路径只读 node liveness cache**: `RefreshChannelMeta()` 不会每次发消息都直接查 controller；它先看本地 `nodeLiveness` cache。cache 由 `ObserverHooks.OnNodeStatusChange` 驱动更新：controller leader 走 committed `NodeStatusUpdate`，其他节点走 `SyncObservationDelta()` 的 `delta.Nodes` diff，但 app 层统一只消费这一个 hook。
- **asyncCommittedDispatcher 的 preferLocal**: 已提交消息直接进入本地 delivery runtime；后续由 `distributedDeliveryPush` 按在线 route 把远端会话转成 node RPC push，不再先把整条已提交消息转发到 Channel Leader。这避免所有实时投递都绕经 Leader。
- **投递 Actor 按 Channel 隔离**: 每个 Channel 有独立的 Actor，通过 shard 分片减少锁竞争。Actor 空闲超过 1 分钟会被回收。
- **投递 Actor 不等待全局连续 MessageSeq**: `preferLocal` 下每个节点只能看到该 Channel 全局序列的一个稀疏子集（例如只看到 1、3、5…）。Actor 现在按“本节点已观察到的提交流”推进；更高的本地已观察 seq 会立即进入解析/推送，之后若更低 seq 才到达，则按 late delivery best-effort 补发，而不是为了等待永远不会在本节点出现的全局 gap 一直卡住实时投递。
- **投递重试有上限**: 默认重试延迟 [500ms, 1s, 2s]，最大重试次数 = len(retryDelays)+1 = 4 次。超过后消息进入离线处理。
- **AckIndex 是投递确认的关键**: `AckIndex.Bind` 在 Push 时建立 SessionID+MessageID → Channel+Route 的映射，RecvAck 通过此映射找到对应的投递 Actor。Session 关闭时 `LookupSession` 批量处理所有未确认消息。
- **Presence 权威路由通过 Raft**: `RegisterAuthoritative` 是一次 Raft Propose，写入 Slot Leader 的状态机。这保证了多节点场景下设备踢出的一致性。
- **Presence 失败回滚**: `Activate` 中如果 `dispatchActions` 失败，会先 `bestEffortUnregister`（注销权威路由）再 `online.Unregister`（清理本地）。顺序不能反。
- **会话冷热分离**: `coldThreshold`(默认 30 天) 之前无消息的会话被标记为冷会话，异步降级 `ClearActiveAt`。冷会话不参与活跃候选，但仍可通过增量扫描或 overlay 返回。
- **会话增量同步**: `version > 0` 时走增量路径，通过 `ScanUserConversationStatePage` 分页扫描 + `incrementalViewRetainer` 小顶堆保留 top-N。大量会话时避免全量加载。
- **Gateway 空闲断开只看客户端入站**: `gateway/core/server.go` 的 idle monitor 只根据最近一次客户端入站数据刷新 deadline；客户端发送的任意 frame（包括 `PingPacket`）都会续期，服务端持续下发消息不会延长 `IdleTimeout`。默认超时为 3 分钟。
- **Gateway TokenAuth 当前不可用**: `Config.Gateway.TokenAuthOn = true` 会直接报错 `gateway token auth requires verifier hooks`，需要先注册验证钩子。
- **Cluster.Slots 静态配置已废弃**: 配置中设置 `Cluster.Slots` 会直接报错，必须使用 `Cluster.InitialSlotCount` 走 Controller 管理的动态 Slot 分配。
