# internal 流程文档

## 📋 目录
1. [职责定位](#1-职责定位)
2. [核心组件](#2-核心组件)
3. [对外接口](#3-对外接口)
4. [核心流程](#4-核心流程)
5. [RPC 服务](#5-rpc-服务)
6. [错误处理](#6-错误处理)
7. [避坑清单](#7-避坑清单)

---

## 1. 职责定位

### ✅ 负责范围
- 客户端连接管理（Gateway）
- 消息收发
- 在线状态（Presence）
- 投递（Delivery）
- 会话同步（Conversation）
- HTTP API 接入
- 节点间 RPC 协��

### ❌ 不负责范围
- 分布式共识与 Slot 编排 → `pkg/cluster`
- Channel Log 复制 → `pkg/channel`
- 元数据状态机与存储 → `pkg/slot/fsm` + `pkg/slot/meta`

---

## 2. 核心组件

### 2.1 架构分层

```
┌─────────────────────────────────────────────────────────┐
│                        app.App                          │
│                    (应用层入口)                          │
└─────────────────────────────────────────────────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│   Gateway    │    │   API/Node   │    │   Usecase    │
│   (接入层)    │    │   (接入层)    │    │   (用例层)    │
└──────────────┘    └──────────────┘    └──────────────┘
        │                   │                   │
        └───────────────────┼───────────────────┘
                            ▼
                    ┌──────────────┐
                    │   Runtime    │
                    │  (运行时层)   │
                    └──────────────┘
                            │
                            ▼
                    ┌──────────────┐
                    │   pkg/*      │
                    │  (基础设施)   │
                    └──────────────┘
```

### 2.2 组件清单

#### 应用层
| 组件 | 文件 | 职责 |
|------|------|------|
| `app.App` | `app/app.go` | 主入口：聚合所有子系统、构建依赖图、管理启停生命周期 |

#### 接入层（Access）
| 组件 | 文件 | 职责 |
|------|------|------|
| `gateway.Gateway` | `gateway/gateway.go` | 网关：管理 Transport/Protocol/Session，双向实时通信 |
| `gateway.Handler` | `access/gateway/handler.go` | 网关桥接：将 Frame 路由到 Usecase 层 |
| `api.Server` | `access/api/server.go` | HTTP API：消息发送、频道同步、Token、会话同步 |
| `node.Client` | `access/node/options.go` | 节点间 RPC 客户端 |
| `node.Adapter` | `access/node/options.go` | 节点间 RPC 服务端 |

#### 用例层（Usecase）
| 组件 | 文件 | 职责 |
|------|------|------|
| `message.App` | `usecase/message/app.go` | 消息用例：发送、接收确认、会话关闭 |
| `delivery.App` | `usecase/delivery/app.go` | 投递用例：提交投递、确认、离线处理 |
| `presence.App` | `usecase/presence/app.go` | 在线状态用例：激活/去激活、权威路由、心跳 |
| `conversation.App` | `usecase/conversation/app.go` | 会话用例：增量/全量同步、冷热分离 |

#### 运行时层（Runtime）
| 组件 | 文件 | 职责 |
|------|------|------|
| `delivery.Manager` | `runtime/delivery/manager.go` | 投递运行时：分片 Actor 模型、重试轮、Ack 索引 |
| `online.MemoryRegistry` | `runtime/online/registry.go` | 在线注册表：按 SessionID/UID/SlotID 索引 |
| `channelmeta` | `runtime/channelmeta/` | Channel 元数据：resolver、缓存、lease、repair |
| `messageid` | `runtime/messageid/` | Snowflake 分布式 ID 生成 |

#### 事件合约（Contracts）
| 组件 | 文件 | 职责 |
|------|------|------|
| `messageevents` | `contracts/messageevents/` | 已提交消息事件合约 |
| `deliveryevents` | `contracts/deliveryevents/` | 投递回执与会话关闭事件合约 |

### 2.3 依赖边界

**规则**：
- `runtime/*` 可依赖 `pkg/*`，禁止依赖 `access/*`、`gateway/*`、`usecase/*`、`app`
- `usecase/*` 可依赖 `runtime/*`、`pkg/*`，禁止依赖 `access/*`、`app`
- `access/*` 作为适配器层，转换传输 DTO 到合约/用例/运行时 DTO

**边界检查**：
```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

---

## 3. 对外接口

### 3.1 应用层
```go
app.New(Config) (*App, error)
App.Start() / Stop()
App.Cluster() / ChannelLog() / Store()
App.Message() / Conversation() / Gateway()
```

### 3.2 网关层
```go
Handler.OnSessionActivate(ctx)
Handler.OnSessionOpen(ctx) / OnSessionClose(ctx)
Handler.OnFrame(ctx, frame)
```

### 3.3 用例层
```go
// 消息
message.App.Send(ctx, SendCommand) (SendResult, error)
message.App.RecvAck(RecvAckCommand) error

// 在线状态
presence.App.Activate(ctx, ActivateCommand) error
presence.App.Deactivate(ctx, DeactivateCommand) error

// 会话
conversation.App.Sync(ctx, SyncQuery) (SyncResult, error)
conversation.App.ClearUnread(ctx, ClearUnreadCommand) error
```

### 3.4 运行时层
```go
// 投递
delivery.Manager.Submit(ctx, CommittedEnvelope) error
delivery.Manager.AckRoute(ctx, RouteAck) error

// 在线注册表
online.Registry.Register(conn) / Unregister(sessionID)
online.Registry.Connection(sessionID)
```

---

## 4. 核心流程

### 4.1 启动流程

**入口**: `app.New()` → `build()` → `Start()`

#### 构建阶段（build）
```
1. 配置校验
   └─ ApplyDefaultsAndValidate()

2. 创建 ResourceStack（失败时自动清理）

3. 初始化基础设施
   ├─ Logger
   ├─ 数据库（metadb, raftstorage, channelstore）
   └─ Cluster（含 observer hooks）

4. 创建 Channel Log 基础设施
   ├─ MessageID 生成器（Snowflake）
   ├─ DataPlane 连接池
   └─ Channel Transport/Runtime

5. 创建业务组件
   ├─ Store（元数据存储）
   ├─ Conversation（会话投影器）
   ├─ ChannelMetaSync（元数据同步）
   ├─ Presence（在线状态 + Worker）
   ├─ Delivery（投递管理器）
   ├─ CommittedDispatcher（有界分片队列，异步分发已提交事件）
   ├─ CommittedReplay（从 Channel Log 补偿已提交事件）
   ├─ Message（消息用例）
   └─ Node Access（RPC Handler）

6. 创建接入层
   ├─ API Server（可选）
   ├─ Manager（可选）
   ├─ Gateway Handler
   └─ Gateway
```

#### 启动阶段（Start）
```
按顺序启动（失败则逆序停止）:
1. cluster
2. managed_slots_ready（等待 Slot 就绪，超时 10s）
3. channelmeta（启动 active-slot leader watcher）
4. presence（启动 Worker）
5. conversation_projector
6. delivery_runtime
7. committed_dispatcher
8. committed_replay
9. gateway
10. api
11. manager
```

### 4.2 停止流程

**入口**: `Stop()`

```
按启动逆序停止（5s 超时）:
1. manager
2. api
3. gateway
4. committed_replay
5. committed_dispatcher
6. delivery_runtime
7. conversation_projector
8. presence
9. channelmeta（StopWithoutCleanup）
10. managed_slots_ready（no-op）
11. cluster

然后关闭资源:
12. channelLog.Close
13. dataPlaneClient.Stop
14. dataPlanePool.Close
15. channelLogDB.Close
16. raftDB.Close
17. metadb.Close
18. syncLogger
```

### 4.3 消息发送流程

#### Gateway 路径
```
客户端 SendPacket
  ↓
Handler.OnFrame(ctx, frame)
  ├─ SendPacket → handleSend
  ├─ RecvackPacket → handleRecvAck
  └─ PingPacket → handlePing（直接返回 Pong）
  ↓
handleSend(ctx, pkt)
  ├─ 解密（如需要）
  ├─ 构建 SendCommand
  └─ messages.Send(ctx, cmd)
      ↓
message.App.Send(ctx, cmd)
  ├─ 校验 FromUID、ChannelType
  ├─ Person 频道规范化
  └─ sendDurable(ctx, cmd)
      ├─ buildDurableMessage
      └─ sendWithEnsuredMeta
          ├─ 先通过 ChannelMetaSync 获取健康元数据（可命中业务 fast path cache）
          ├─ local/remote Append 到当前 Channel Leader
          ├─ 权威元数据缺失时 bootstrap
          ├─ lease 过期时续租或 repair
          └─ stale / not-leader / lease-expired / reroute 错误时强制刷新并仅重试一次 Append
      ↓
dispatcher.SubmitCommitted(ctx, messageevents.MessageCommitted)
  ├─ committedFanout（fanout 给订阅者）
  └─ asyncCommittedDispatcher
      ├─ 按 ChannelID + ChannelType 选择固定分片队列
      ├─ 队列满时不失败 Send，改走 conversation fallback + flush
      ├─ delivery.Submit（投递）
      └─ conversation.SubmitCommitted（会话投影）
committed_replay 后台从已提交 Channel Log 补偿未分发事件
  ├─ 按 committed cursor 扫描 message_seq
  ├─ 重新提交 delivery / conversation
  ├─ conversation.Flush 成功后批量推进 cursor
  └─ 记录 replay lag / pass duration 指标
  ↓
返回 SendResult{MessageID, MessageSeq}
  ↓
writeSendack(ctx, pkt, result)
```

#### API 路径
```
HTTP POST /api/message/send
  ↓
API Server 解析请求体
  ↓
messages.Send(ctx, cmd)
  └─ 同 Gateway 路径
  ↓
返回 HTTP 响应 {message_id, message_seq}
```

### 4.4 投递流程

```
Manager.Submit(ctx, env)
  ↓
shardFor(channelKey)（FNV32a hash）
  ↓
shard.submit(ctx, env)
  ↓
actorFor(key)（获取或创建 Actor）
  ↓
Actor 状态机:
  ├─ Resolve 阶段
  │   └─ Resolver.ResolvePage（分页获取订阅者在线端点并记录 resolve 指标）
  │
  ├─ Push 阶段
  │   ├─ 本地端点: localDeliveryPush → Session.WriteFrame
  │   ├─ 远程端点: delivery_push RPC 按目标节点批量发送
  │   │   ├─ Group 频道: 每个目标节点一个 frame item，包含该节点多条 route
  │   │   ├─ Person 频道: 按 recipient channel view 拆 item，避免收件视图串用
  │   │   └─ 滚动升级时若对端未返回 route 结果，回退 legacy DeliveryPushCommand
  │   ├─ 过滤 SenderSessionID（跳过发送者连接）
  │   └─ AckIndex.Bind（建立 Ack 映射）
  │
  ├─ 等待 Ack
  │   └─ Manager.AckRoute → actor.handleRouteAck
  │
  ├─ 重试（失败时）
  │   ├─ 指数退避 [500ms, 1s, 2s]
  │   ├─ RetryWheel.Schedule
  │   └─ delivery_runtime lifecycle 周期处理 retry tick 并 sweep idle actor
  │
  ├─ 路由过期
  │   ├─ 最终重试后从 AckIndex 移除绑定
  │   ├─ 释放节点内 retry state，避免无限 pin route
  │   └─ 依赖 Channel Log / committed_replay / 客户端同步做 durable catch-up
  │
  └─ 离线处理
      └─ SessionClosed → actor.handleRouteOffline
```

### 4.5 在线状态流程

#### 激活（Activate）
```
客户端连接认证成功
  ↓
Handler.OnSessionActivate(ctx)
  ↓
presence.Activate(ctx, cmd)
  ├─ localConnFromActivate（构建 OnlineConn）
  ├─ online.Register(conn)（注册到本地在线表）
  ├─ authority.RegisterAuthoritative(ctx, cmd)
  │   ├─ 计算 SlotID
  │   ├─ RPC 到 Slot Leader
  │   ├─ Leader 侧: Raft Propose 写入权威路由
  │   └─ 返回 RouteAction 列表（踢旧设备等）
  └─ dispatchActions(ctx, actions)
      ├─ 本地 action: MarkClosing → kick → 延迟 Close
      └─ 远程 action: nodeClient RPC 转发
```

#### 去激活（Deactivate）
```
客户端断开连接
  ↓
Handler.OnSessionClose(ctx)
  ├─ messages.SessionClosed（通知消息层清理）
  └─ presence.Deactivate(ctx, cmd)
      ├─ online.Connection(sessionID)
      ├─ online.Unregister(sessionID)
      └─ bestEffortUnregister（RPC 到 Slot Leader 注销）
```

#### 心跳（Heartbeat）
```
presenceWorker 周期执行:
  └─ presenceApp.HeartbeatAuthoritative(ctx)
      └─ 续租权威路由 lease

Slot Leader 变化时:
  └─ presenceWorker.HeartbeatOnce
      └─ digest mismatch 时 ReplayAuthoritative
```

### 4.6 会话同步流程

```
API: GET /api/conversation/sync?uid=X&version=V&limit=N
  ↓
conversation.App.Sync(ctx, query)
  ├─ 1. 加载活跃会话
  │   ├─ ListUserConversationActive（limit=2000）
  │   ├─ addActiveCandidates（关联 ChannelUpdateLog）
  │   └─ 冷会话降级（lastMsgAt > 30天）
  │
  ├─ 2. 加载客户端 overlay 候选
  │   └─ addOverlayCandidates（query.LastMsgSeqs）
  │
  ├─ 3. 增量收集（version > 0）
  │   ├─ ScanUserConversationStatePage（分页扫描）
  │   ├─ BatchGetChannelUpdateLogs（批量查更新）
  │   └─ incrementalViewRetainer（小顶堆保留 top-N）
  │
  ├─ 4. 加载最新消息
  │   ├─ 本地 Channel: cluster.Status → cluster.Fetch
  │   └─ 远程 Channel: nodeClient RPC
  │
  ├─ 5. 构建视图
  │   ├─ 计算 unread = lastMsgSeq - max(readSeq, deletedToSeq)
  │   ├─ 自己发的消息: unread=0
  │   └─ onlyUnread 过滤
  │
  ├─ 6. 排序 + 截断
  │   └─ 按 displayUpdatedAt 降序 → 取 top limit
  │
  └─ 7. 加载最近消息（msgCount > 0）
      └─ LoadRecentMessagesBatch → filterVisibleRecents
  ↓
返回 SyncResult{Conversations}
```

### 4.7 接收确认流程

```
客户端 RecvackPacket
  ↓
handleRecvAck(ctx, pkt)
  ├─ mapRecvAckCommand（提取 MessageID/SessionID）
  └─ messages.RecvAck(cmd)
      └─ deliveryAck 路由:
          ├─ 本地: deliveryApp.AckRoute
          │   └─ Manager.AckRoute
          │       ├─ AckIndex.Lookup（查找 AckBinding）
          │       └─ actor.handleRouteAck
          └─ 远程: nodeClient RPC 转发
```

---

## 5. RPC 服务

### 节点间通信

| 操作 | 方向 | 文件 | 说明 |
|------|------|------|------|
| `delivery_submit` | → remote | `access/node/delivery_submit_rpc.go` | 提交已提交消息到 Leader 节点投递 |
| `delivery_push` | → remote | `access/node/delivery_push_rpc.go` | 批量推送一个 committed message 的多条 route 到目标节点在线 Session，兼容 legacy 单 item |
| `delivery_ack` | → remote | `access/node/delivery_ack_rpc.go` | 转发 RecvAck 到投递所在节点 |
| `delivery_offline` | → remote | `access/node/delivery_offline_rpc.go` | 通知离线消息处理 |
| `presence` | → slot leader | `access/node/presence_rpc.go` | 权威路由注册/注销/心跳 |
| `conversation_facts` | → channel leader | `access/node/conversation_facts_rpc.go` | 远程加载会话最新/最近消息 |
| `channel_append` | → channel leader | `access/node/channel_append_rpc.go` | 非 Leader 副本转发 Durable Append |
| `channel_messages` | → channel leader | `access/node/channel_messages_rpc.go` | 管理端消息查询与频道消息同步 |
| `channel_leader_repair` | → slot leader | `access/node/channel_leader_repair_rpc.go` | 请求权威修复 ChannelRuntimeMeta.Leader |
| `channel_leader_evaluate` | → candidate replica | `access/node/channel_leader_evaluate_rpc.go` | 评估副本是否可安全接任 channel leader |

---

## 6. 错误处理

### 应用层错误
| 错误 | 含义 |
|------|------|
| `ErrNotBuilt` | App 未完成构建 |
| `ErrStopped` | App 已停止 |
| `ErrAlreadyStarted` | App 已启动 |
| `ErrInvalidConfig` | 配置校验失败 |

### 用例层错误
| 错误 | 含义 |
|------|------|
| `ErrUnauthenticatedSender` | 发送者未认证（FromUID 为空） |
| `ErrClusterRequired` | 消息发送需要 Cluster |
| `ErrRouterRequired` | Presence 路由器未注入 |
| `ErrSessionRequired` | Activate 缺少 Session |

### 接入层错误
| 错误 | 含义 |
|------|------|
| `ErrUnsupportedFrame` | 不支持的 Frame 类型 |
| `ErrUnauthenticatedSession` | 未认证的 Session |
| `ErrPresenceRequired` | Presence 用例未注入 |

---

## 7. 避坑清单

### 🔴 启动与停止
- **启动顺序严格**: 按 Cluster → WaitReady → ChannelMetaSync → Presence → Conversation → DeliveryRuntime → CommittedDispatcher → CommittedReplay → Gateway → API → Manager 顺序启动，任一步骤失败会按启动逆序停止
- **停止顺序相反**: 先停 Manager/API/Gateway（停止接入新请求），再停业务层，最后停 Cluster 和关闭数据库
- **stopOnce 保证幂等**: 多次调用 Stop() 只执行一次

### 🔴 消息发送
- **Person 频道 ID 规范化**: 必须使用 `NormalizePersonChannel(fromUID, channelID)` 将两个 UID 排序拼接，否则同一对话会产生两个不同的 Channel
- **sendWithEnsuredMeta 先走健康元数据缓存**: 业务路径只复用 active、epoch/leader epoch 非零、leader lease 足够、leader 非 dead/draining 且 repair policy 健康的 cache entry
- **sendWithEnsuredMeta 只强制刷新一次**: 遇到 `ErrStaleMeta`、`ErrNotLeader`、`ErrLeaseExpired` 或 `ErrRerouted` 时 invalid cache 并触发一次刷新重试
- **权威元数据缺失时 bootstrap**: 读到 `ErrNotFound` 时会按 Slot 拓扑 bootstrap 缺失的运行时元数据
- **lease 过期时续租**: 当前 Channel Leader 所在节点会先尝试权威续租
- **权威 leader 修复**: 命中 `leader_missing`/`leader_not_replica`/`leader_dead`/`leader_draining`/`leader_lease_expired` 时，由当前 slot leader 选举并持久化新的 channel leader

### 🔴 投递运行时
- **Actor 按 Channel 隔离**: 每个 Channel 有独立的 Actor，通过 shard 分片减少锁竞争
- **Actor 空闲回收**: 超过 1 分钟空闲会被回收
- **不等待全局连续 MessageSeq**: `preferLocal` 下每个节点只能看到该 Channel 全局序列的稀疏子集，按本节点已观察到的提交流推进
- **投递重试有上限**: 默认重试延迟 [500ms, 1s, 2s]，最大重试次数 = 4 次，超过后移除 Ack 绑定并依赖 Channel Log catch-up
- **AckIndex 是关键**: `AckIndex.Bind` 在 Push 时建立 SessionID+MessageID → Channel+Route 的映射
- **远程实时投递只在单条 committed message 内批量**: 不跨消息聚合；按目标节点合并 RPC，Person 频道按 recipient view 拆分 frame item

### 🔴 在线状态
- **权威路由通过 Raft**: `RegisterAuthoritative` 是一次 Raft Propose，写入 Slot Leader 的状态机
- **失败回滚顺序**: `Activate` 失败时先 `bestEffortUnregister`（注销权威路由）再 `online.Unregister`（清理本地）

### 🔴 会话同步
- **冷热分离**: `coldThreshold`（默认 30 天）之前无消息的会话被标记为冷会话，异步降级 `ClearActiveAt`
- **增量同步**: `version > 0` 时走增量路径，通过分页扫描 + 小顶堆保留 top-N

### 🔴 网关
- **空闲断开只看客户端入站**: idle monitor 只根据最近一次客户端入站数据刷新 deadline，服务端持续下发消息不会延长 `IdleTimeout`（默认 3 分钟）
- **TokenAuth 当前不可用**: `Config.Gateway.TokenAuthOn = true` 会直接报错，需要先注册验证钩子

### 🔴 配置
- **Cluster.Slots 静态配置已废弃**: 必须使用 `Cluster.InitialSlotCount` 走 Controller 管理的动态 Slot 分配

### 🔴 元数据同步
- **channelMetaSync 不再做全量 scan**: steady-state 不再 `ListChannelRuntimeMeta()` 预热本地所有副本频道，业务路径按需激活
- **active-slot leader watcher 只盯热 slot**: 只轮询当前热本地 runtime 所在的 physical slot leader
- **Refresh 热路径只读 node liveness cache**: `RefreshChannelMeta()` 不会每次都直接查 controller，先看本地 `nodeLiveness` cache

### 🔴 已提交消息分发
- **committedFanout + asyncCommittedDispatcher 的 preferLocal**: message 用例只发布 `messageevents.MessageCommitted`，`asyncCommittedDispatcher` 用有界分片队列分发，默认把已提交消息进入本地 delivery runtime，避免所有实时投递都绕经 Leader。
- **committed dispatcher 溢出不影响 durable send**: 队列满只记录指标并触发 conversation fallback/flush，Sendack 仍只由 Channel Log quorum commit 决定。
- **committed dispatcher 停止依赖 replay 补偿**: 停止时不再接收新队列任务，未启动/停止中的提交返回内部错误供调用方记录；队列内未处理实时投递可被丢弃并由 committed_replay 兜底补发。
- **committed_replay 是补偿路径，不阻塞 Sendack**: Sendack 仍只等待 Channel Log quorum commit；后台 replayer 以 Channel Log 为真相，用批量 cursor 兜底补发 delivery / conversation，cursor 只在 conversation flush 成功后推进。
- **性能指标走窄接口注入**: message append、meta refresh、dispatch queue、delivery resolve/push、runtime gauge 和 replay lag 由 `internal/app` 组合根接入 `pkg/metrics`，低层包不直接依赖具体 metrics registry。

---

## 8. 测试说明

### 快速测试（默认）
```bash
GOWORK=off go test ./internal/app
GOWORK=off go test ./internal/...
GOWORK=off go test ./internal/access/gateway ./internal/gateway/transport/gnet
```

### 集成测试（慢测试）
```bash
GOWORK=off go test -tags=integration ./internal/app
GOWORK=off go test -tags=integration ./internal/access/gateway ./internal/gateway/transport/gnet
```

### 边界检查
```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

---

**文档版本**: v2.0
**最后更新**: 2026-04-30
