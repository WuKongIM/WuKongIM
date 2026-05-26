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
- 客户端网关业务适配（Access Gateway）
- 消息收发
- 在线状态（Presence）
- 投递（Delivery）
- 会话同步（Conversation）
- CMD 离线同步（CMD Sync）
- 插件运行时与 PDK host RPC 适配
- HTTP API 接入
- 节点间 RPC 协议

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
│ Gateway      │    │   API/Node   │    │   Usecase    │
│ Adapter      │    │   (接入层)    │    │   (用例层)    │
│ (接入适配)    │    │              │    │              │
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
| `access/gateway.Handler` | `access/gateway/handler.go` | 网关业务适配：将 `pkg/gateway` 的 Frame / lifecycle 路由到 Usecase 层 |
| `api.Server` | `access/api/server.go` | HTTP API：消息发送、频道同步、Token、会话同步、CMD 离线同步、插件公开路由 |
| `manager.Server` | `access/manager/server.go` | 后台管理 HTTP API：JWT、权限适配、集群/业务/插件管理 DTO |
| `node.Client` | `access/node/options.go` | 节点间 RPC 客户端 |
| `node.Adapter` | `access/node/options.go` | 节点间 RPC 服务端，包含插件 HTTP route / 管理 / committed side-effect 转发 |
| `plugin.Server` | `access/plugin/server.go` | PDK host RPC：将插件进程 wkrpc 调用适配到插件用例 |

`pkg/gateway.Gateway` 位于 `../pkg/gateway/gateway.go`，是可复用客户端网关基础设施；`internal/app` 负责组合它并注入 `internal/access/gateway.Handler`，不要把消息、在线状态或频道业务规则放入 `pkg/gateway`。

#### 用例层（Usecase）
| 组件 | 文件 | 职责 |
|------|------|------|
| `message.App` | `usecase/message/app.go` | 消息用例：发送、接收确认、会话关闭 |
| `delivery.App` | `usecase/delivery/app.go` | 投递用例：提交投递、确认、离线处理 |
| `presence.App` | `usecase/presence/app.go` | 在线状态用例：激活/去激活、权威路由、心跳 |
| `conversation.App` | `usecase/conversation/app.go` | 会话用例：增量/全量同步、冷热分离 |
| `benchdata.App` | `usecase/benchdata/app.go` | Benchmark 数据准备用例：能力描述、用户 token、群频道、订阅者批量准备 |
| `channel.App` | `usecase/channel/app.go` | 频道业务用例：资料、订阅者、白名单、黑名单 |
| `cmdsync.App` | `usecase/cmdsync/app.go` | CMD 同步用例：合并 pending overlay、读取 command-channel log、生成 sync records、处理 syncack |
| `cmdsync.ConversationUpdater` | `usecase/cmdsync/pending.go` | UID-owner pending CMD conversation intent 缓冲、flush 与 graceful-stop 恢复 |
| `plugin.App` | `usecase/plugin/app.go` | 插件用例：生命周期登记、配置、绑定、host RPC 和 Send/PersistAfter/Receive hook 编排 |
| `management.App` | `usecase/management/app.go` | 后台管理聚合用例：节点、用户、系统 UID、频道业务、诊断 DTO |

#### 运行时层（Runtime）
| 组件 | 文件 | 职责 |
|------|------|------|
| `delivery.Manager` | `runtime/delivery/manager.go` | 投递运行时：分片 Actor 模型、重试轮、Ack 索引 |
| `deliverytag.Manager` | `runtime/deliverytag/manager.go` | 投递标签缓存：按频道保存 leader 构建的订阅者分区快照 |
| `online.MemoryRegistry` | `runtime/online/registry.go` | 在线注册表：按 SessionID/UID/SlotID 索引 |
| `channelmeta` | `runtime/channelmeta/` | Channel 元数据：resolver、缓存、lease、repair |
| `channelplane` | `runtime/channelplane/` | Channel key reactor 平面：durable append 寻址、同频道串行、peer batching 与 typed backpressure |
| `messageid` | `runtime/messageid/` | Snowflake 分布式 ID 生成 |
| `plugin.Runtime` | `runtime/plugin/lifecycle.go` | 节点内插件进程、Unix socket、热重载和期望状态运行时 |
| `userlimit` | `runtime/userlimit/` | 节点内 UID 发送令牌桶限流，保护 message send 热路径 |

#### 事件合约（Contracts）
| 组件 | 文件 | 职责 |
|------|------|------|
| `messageevents` | `contracts/messageevents/` | 已提交消息事件合约 |
| `deliveryevents` | `contracts/deliveryevents/` | 投递回执与会话关闭事件合约 |

#### Benchmark 黑盒客户端（Bench）
| 组件 | 文件 | 职责 |
|------|------|------|
| `bench/config` | `bench/config/` | wkbench target / worker / scenario YAML 严格加载 |
| `bench/model` | `bench/model/` | wkbench 配置、速率与确定性计划模型 |
| `bench/planner` | `bench/planner/` | 按 worker 权重规划 identity pool、channel、member 与 traffic 分片 |
| `bench/target` | `bench/target/` | wkbench 黑盒 target HTTP client，调用 bench API、health、ready 与 snapshot |
| `bench/coordinator` | `bench/coordinator/` | wkbench coordinator preflight，校验 target capabilities、worker control 与 gateway placeholder；编排阶段并汇总 metrics/report |
| `bench/worker` | `bench/worker/` | wkbench worker 控制 HTTP API、运行分配状态、阶段推进、WKProto workload metrics 与 worker report |
| `bench/metrics` | `bench/metrics/` | wkbench 低基数 counters/gauges/histograms/errors 快照与聚合 |
| `bench/report` | `bench/report/` | wkbench report.json、summary.md、worker metrics 与 target snapshot artifacts |
| `bench/wkproto` | `bench/wkproto/` | wkbench 黑盒 WKProto TCP client，用于 worker 连接、SendAck 与 Recv 验证 |
| `bench/workload` | `bench/workload/` | wkbench person/group 数据准备和 WKProto 发送/接收 workload |

### 2.3 依赖边界

**规则**：
- `runtime/*` 可依赖 `pkg/*`，禁止依赖 `access/*`、`usecase/*`、`app`
- `usecase/*` 可依赖 `runtime/*`、`pkg/*`，禁止依赖 `access/*`、`app`
- `runtime/*` / `usecase/*` 不应依赖 `pkg/gateway/*`；客户端网关入口协议由 `internal/access/gateway` 适配
- `access/*` 作为适配器层，转换传输 DTO 到合约/用例/运行时 DTO
- `bench/*` 是 wkbench 黑盒客户端代码，禁止依赖 server internal 包和集群运行时包；target 数据准备只能通过 `/bench/v1/*` bench API，不能使用 Manager API

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
conversation.App.SetUnread(ctx, SetUnreadCommand) error
conversation.App.DeleteConversation(ctx, DeleteConversationCommand) error

// 频道业务
channel.App.UpdateInfo(ctx, UpdateInfoCommand) (UpdateInfoResult, error)
channel.App.ListSubscribersPage(ctx, MemberListPageRequest) (MemberListPageResult, error)
channel.App.ListAllowlistPage(ctx, MemberListPageRequest) (MemberListPageResult, error)
channel.App.ListDenylistPage(ctx, MemberListPageRequest) (MemberListPageResult, error)

// CMD 离线同步
cmdsync.App.Sync(ctx, SyncQuery) (SyncResult, error)
cmdsync.App.SyncAck(ctx, SyncAckCommand) error

// 后台管理
management.App.ListChannels(ctx, ChannelBusinessListQuery) (ChannelBusinessListResult, error)
management.App.GetChannel(ctx, ChannelBusinessDetailQuery) (ChannelBusinessDetail, error)
management.App.UpsertChannel(ctx, ChannelBusinessUpsertCommand) (ChannelBusinessDetail, error)
management.App.ListChannelMembers(ctx, ChannelMemberListQuery) (ChannelMemberListResult, error)
management.App.AddChannelMembers(ctx, ChannelMemberMutationCommand) (ChannelMemberMutationResult, error)
management.App.RemoveChannelMembers(ctx, ChannelMemberMutationCommand) (ChannelMemberMutationResult, error)
management.App.ListSystemUsers(ctx) (ListSystemUsersResponse, error)
management.App.AddSystemUsers(ctx, MutateSystemUsersRequest) (MutateSystemUsersResponse, error)
management.App.RemoveSystemUsers(ctx, MutateSystemUsersRequest) (MutateSystemUsersResponse, error)
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
   ├─ Plugin（可选：节点内 runtime/usecase/host RPC；默认关闭；此时先构建能力，后续注入 Send/PersistAfter/Receive hook）
   ├─ ConversationActiveHintCache（UID-owner active_at 热提示覆盖层）
   ├─ Conversation（会话投影器）
   ├─ CMDSync（CMD 离线同步用例 + pending conversation updater / intent router）
   ├─ ChannelMetaSync（元数据同步）
   ├─ ChannelPlane（channel key reactor 平面；durable append 寻址、route refresh、peer batching）
   ├─ Presence（在线状态 + Worker）
   ├─ Delivery（投递管理器）
   ├─ CommittedDispatcher（有界分片队列，异步分发已提交事件）
   ├─ CommittedReplay（从 Channel Log 补偿已提交事件）
   ├─ ChannelMigration（执行权威迁移任务）
   ├─ ChannelRetention（按权威元数据推进频道消息保留边界）
   ├─ Message（消息用例）
   └─ Node Access（RPC Handler，含 CMD sync、CMD intent UID-owner 转发与 plugin HTTP/management/committed 转发）

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
4. channelplane（启动 channel reactor shards）
5. presence（启动 Worker）
6. conversation_active_hints
7. conversation_projector
8. cmd_conversation_updater
9. plugin_runtime（可选；先于 delivery/committed 相关组件，保证 hook 运行期可用）
10. delivery_runtime
11. committed_dispatcher
12. committed_replay
13. channel_migration
14. channel_retention
15. gateway
16. api
17. manager
```

### 4.2 停止流程

**入口**: `Stop()`

```
按启动逆序停止（5s 超时）:
1. manager
2. api
3. gateway
4. channel_retention
5. channel_migration
6. committed_replay
7. committed_dispatcher
8. delivery_runtime
9. plugin_runtime（先停 Receive observer，再停插件进程运行时）
10. cmd_conversation_updater
11. conversation_projector
12. conversation_active_hints
13. presence
14. channelplane
15. channelmeta（StopWithoutCleanup）
16. managed_slots_ready（no-op）
17. cluster

然后关闭资源:
18. channelLog.Close
19. dataPlaneClient.Stop
20. dataPlanePool.Close
21. channelLogDB.Close
22. raftDB.Close
23. metadb.Close
24. syncLogger
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
  ├─ 确保 diagnostics trace context
  ├─ 构建 SendCommand（携带 TraceID）
  ├─ pkg/observability/sendtrace 记录 gateway send / sendack（携带 client_msg_no 与 channel_key）
  └─ messages.Send(ctx, cmd)
      ↓
message.App.Send(ctx, cmd)
  ├─ 校验 FromUID、ChannelType 与 request-scoped subscribers 参数
  ├─ CMD 输入先还原 source channel；已寻址到 `source____cmd` 的输入不重复追加后缀
  ├─ Person / Agent 频道规范化
  ├─ 发送前权限与频道状态校验（业务权限按 source channel；Visitors 非本人使用 CustomerService 维度）
  ├─ 插件启用时调用 SendHook（按优先级运行本地 Send 插件，可变更 payload/headers 或返回 reason 拒绝）
  ├─ NoPersist 分支
  │   ├─ command-style（SyncOnce 或已寻址 CMD）分配 transient message ID，MessageSeq=0，通过 SubmitRealtime 投递，不写 Channel Log
  │   └─ 非 command-style 返回 ReasonSuccess，不写 Channel Log，也不做 realtime 投递
  ├─ durable CMD 分支（SyncOnce 或已寻址 CMD）写入 `source____cmd`
  └─ sendDurable(ctx, appendCmd)
      ├─ buildDurableMessage
      └─ ChannelPlane.AppendBatch / ChannelAppender
          ├─ 在 channel reactor 内按 ChannelID 寻址并串行化同频道 append effect
          ├─ route refresh、lease repair、local owner append 与 peer batching 都在 channelplane 内完成
          └─ message.usecase 只接收最终 append 结果，不再持有 slot leader / channel leader / remote redirect 细节
      ↓
request-scoped durable CMD：用 RequestSubscribers 构建 CMD conversation intent，并按 UID owner 路由到 pending updater
      ↓
dispatcher.SubmitCommitted(ctx, messageevents.MessageCommitted)
  ├─ committedFanout（提交到 asyncCommittedDispatcher；插件启用时额外提交到 pluginCommittedRouter）
  └─ asyncCommittedDispatcher
      ├─ 按 ChannelID + ChannelType 选择固定分片队列
      ├─ 队列满时不失败 Send，改走 best-effort conversation fallback
      ├─ delivery.Submit（投递）
      └─ conversation.SubmitCommitted（异步 active hint 投影）
realtime delivery 直接提交 transient MessageRealtime
  ├─ 不依赖 committed replay 补偿
  └─ delivery 使用 source-channel 订阅者解析和在线路由 fanout
committed_replay 后台从已提交 Channel Log 补偿未分发事件
  ├─ 按 committed cursor 扫描 message_seq
  ├─ 重新提交 delivery / conversation；不再运行独立 CMD subscriber-scan projector
  ├─ 普通 CMD intent 由 delivery resolver 的 UID page observer 重新产生
  ├─ delivery 接受后批量推进 cursor；active hint 失败只记录告警
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
  ├─ 读取/校验 X-WK-Trace-ID 或生成节点内 TraceID
  └─ 构建 SendCommand（携带 TraceID）
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
  │   ├─ Channel Leader 使用 deliverytag.Manager 构建/刷新订阅者分区 tag
  │   ├─ 非 Leader 通过 delivery_tag RPC 获取本节点分区并本地缓存
  │   ├─ Resolver.ResolvePage 只展开当前 tag 分区内的在线端点并记录 resolve 指标
  │   └─ 插件启用时，离线 UID 在 presence 分类后分页通知 Receive observer，避免大群全量离线 UID 一次性物化
  │
  ├─ Push 阶段
  │   ├─ 本地端点: localDeliveryPush → Session.WriteFrame
  │   ├─ 远程端点: delivery_push RPC 按目标节点批量发送
  │   │   ├─ Group 频道: 按目标节点聚合 route，超过单 RPC 上限时按 route chunk 拆分
  │   │   ├─ Person 频道: 按 recipient channel view 拆 item，避免收件视图串用
  │   │   └─ v2 响应只返回 accepted count + retryable/dropped 精确 route，legacy 响应仍回显 accepted route
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
      ├─ SessionClosed → actor.handleRouteOffline
      └─ plugin Receive observer 通过有界 worker/queue 调用绑定 UID 的最高优先级本地 Receive 插件；队列满时反压 delivery，避免静默丢 hook 或创建无界 goroutine
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
API: POST /conversation/sync
  ↓
conversation.App.Sync(ctx, query)
  ├─ 1. 加载活跃会话
  │   ├─ ListUserConversationActive（limit=activeScanLimit）
  │   └─ Store 在 UID-owner 合并持久化 active_at 与 active hint cache
  │
  ├─ 2. 加载客户端 overlay 候选
  │   └─ addOverlayCandidates（query.LastMsgSeqs）
  │
  ├─ 3. 兼容 legacy version
  │   └─ request version 不再作为完整历史增量发现游标
  │
  ├─ 4. 加载最新消息
  │   ├─ 本地 Channel Log: cluster.Status → cluster.Fetch
  │   └─ 远程 Channel Log: conversation_facts RPC
  │
  ├─ 5. 构建视图
  │   ├─ 计算 unread = lastMsgSeq - max(readSeq, deletedToSeq)
  │   ├─ latest.MessageSeq <= deletedToSeq 时隐藏会话
  │   ├─ 自己发的消息: unread=0
  │   └─ onlyUnread 过滤
  │
  ├─ 6. 排序 + 截断
  │   └─ 按 displayUpdatedAt 降序 → 取 top limit
  │
  └─ 7. 加载最近消息（msgCount > 0）
      └─ LoadRecentMessagesBatch → filterVisibleRecents（message_seq > deleted_to_seq）
  ↓
返回 SyncResult{Conversations}
```

普通 `/conversation/sync` 只读取 `UserConversationState` 和 Channel Log 最新消息，不依赖 `CMDConversationState`；`source____cmd` 和 `SyncOnce` 消息不会进入普通会话列表。

### 4.7 CMD 离线同步流程

```
API: POST /message/sync
  ↓
access/api 解析 uid/message_seq/limit，保持 legacy bare array 响应
  ↓
clusterCMDSyncUsecase
  ├─ trim/validate UID，避免空 UID 做 slot lookup
  ├─ SlotForKey(uid) + LeaderOf(slot) 寻址 UID owner
  ├─ 本地 UID owner: cmdsync.App.Sync
  └─ 远程 UID owner: nodeClient.SyncCMD RPC
      ↓
cmdsync.App.Sync
  ├─ ListCMDConversationActive(uid) 读取独立 CMD conversation state
  ├─ 合并 owner-local pending CMD conversation overlay，避免等待 flush 才可见
  ├─ 对每个 state/pending view 从 max(ReadSeq, DeletedToSeq)+1 开始读取 command-channel log
  │   ├─ command channel key 始终是 `source____cmd`
  │   ├─ 本地 command owner: Channel Log committed read
  │   └─ 远端 command owner: channel_messages RPC（SyncMode=true）
  ├─ 按 Timestamp / commandChannelID / channelType / messageSeq / messageID 稳定排序并按 limit 截断
  ├─ 返回前剥离一层 `____cmd`，展示 source-channel
  └─ Replace 最新 sync records（供 syncack 推进 read cursor）

API: POST /message/syncack
  ↓
clusterCMDSyncUsecase 按 UID owner 本地/远端路由
  ↓
cmdsync.App.SyncAck
  ├─ Pop 最近一次 sync records
  ├─ 只保留 command-channel record 且 LastReturnedMsgSeq > 0
  ├─ AdvanceCMDConversationReadSeq 推进独立 CMD read cursor
  └─ pending-only record 先 upsert durable read progress，再按 UID/channel/throughSeq 清理 pending
```

说明：

- `NoPersist` CMD 不写 Channel Log，也不创建 CMD conversation state，因此不会被 `/message/sync` 返回。
- CMD conversation 更新来自 request-scoped recipients 或 delivery 已解析 UID page；不保存每条消息的 subscriber snapshot。
- request-scoped replay 没有 `MessageScopedUIDs` 时仍是 best effort；普通 CMD replay 依赖 delivery UID observer 重新生成 intent。
- 当前项目没有绕过集群的独立部署语义；单节点集群也走同一 UID owner / command owner 路由模型。

### 4.8 接收确认流程

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
| `delivery_tag` | → channel leader | `access/node/delivery_tag_rpc.go` | 获取/刷新 Leader 权威 delivery tag，本地节点只接收自己的订阅者分区 |
| `delivery_push` | → remote | `access/node/delivery_push_rpc.go` | 批量推送一个 committed message 的多条 route 到目标节点在线 Session，兼容 legacy 单 item |
| `delivery_ack` | → remote | `access/node/delivery_ack_rpc.go` | 转发 RecvAck 到投递所在节点 |
| `delivery_offline` | → remote | `access/node/delivery_offline_rpc.go` | 通知离线消息处理 |
| `plugin_http_forward` | → plugin owner node | `access/node/plugin_http_forward_rpc.go` | 转发公开 `/plugins/:plugin/*path` 请求到插件所在节点 |
| `plugin_management` | → target node | `access/node/plugin_management_rpc.go` | 管理端跨节点查询/更新/重启/卸载节点本地插件 |
| `plugin_committed` | → channel owner | `access/node/plugin_committed_rpc.go` | 转发已提交消息的插件 PersistAfter 副作用到 Channel owner 节点 |
| `presence` | → slot leader | `access/node/presence_rpc.go` | 权威路由注册/注销/心跳 |
| `conversation_facts` | → channel leader | `access/node/conversation_facts_rpc.go` | 远程加载会话最新/最近消息 |
| `channel_plane_append` | → channel leader | `access/node/channel_plane_rpc.go` | Typed `AppendBatches` 批量适配器，供 channelplane peer reactor 调用；只校验 route epoch 并本地 owner append，不做 refresh/redirect |
| `channel_append` | → channel leader | `access/node/channel_append_rpc.go` | 兼容旧调用的 legacy single/batch bridge；不再作为 durable send 主路径 |
| `channel_messages` | → channel leader | `access/node/channel_messages_rpc.go` | 管理端消息查询与频道消息同步 |
| `channel_leader_repair` | → slot leader | `access/node/channel_leader_repair_rpc.go` | 请求权威修复 ChannelRuntimeMeta.Leader |
| `channel_leader_evaluate` | → candidate replica | `access/node/channel_leader_evaluate_rpc.go` | 评估副本是否可安全接任 channel leader |
| `diagnostics` | → target node | `access/node/diagnostics_rpc.go` | 管理端跨节点查询目标节点的 node-local diagnostics store |
| `cmd_sync` | → UID slot leader | `access/node/cmdsync_rpc.go` | 转发 legacy CMD 离线消息同步与 syncack 到 UID 权威节点 |

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
| `ErrChannelAppenderRequired` | 持久化消息发送需要 ChannelAppender |
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
- **启动顺序严格**: 按 Cluster → WaitReady → ChannelMetaSync → Presence → Conversation → CMDConversationUpdater → PluginRuntime（可选）→ DeliveryRuntime → CommittedDispatcher → CommittedReplay → ChannelMigration → ChannelRetention → Gateway → API → Manager 顺序启动，任一步骤失败会按启动逆序停止；PluginRuntime 早于 DeliveryRuntime/CommittedReplay 启动且晚于它们停止，保证 Send/PersistAfter/Receive hook 的运行期边界清晰；CMDConversationUpdater 早于 DeliveryRuntime 启动，因此停止时会在 DeliveryRuntime drain 之后保存 pending
- **停止顺序相反**: 先停 Manager/API/Gateway（停止接入新请求），再停业务层，最后停 Cluster 和关闭数据库
- **stopOnce 保证幂等**: 多次调用 Stop() 只执行一次

### 🔴 消息发送
- **Person 频道 ID 规范化**: 必须使用 `NormalizePersonChannel(fromUID, channelID)` 将两个 UID 排序拼接，否则同一对话会产生两个不同的 Channel
- **Durable append 只依赖 ChannelAppender**: message.usecase 只构建 batch，不做 slot leader / channel leader 寻址、metadata refresh、remote redirect 或 lease repair；这些都属于 `internal/runtime/channelplane`
- **channelplane owns route refresh**: stale route、not-leader、lease-expired、write-fenced 与 peer backpressure 的处理都在 channelplane 内闭环；它通过 `internal/runtime/channelmeta` 做一次权威刷新后再重试，不把刷新细节泄漏到 message usecase
- **权威元数据是路由输入，不是 message 责任**: `channelMetaSync` 只提供 channelplane 所需的 authoritative view / repair port，不再承担 durable send 的业务重试编排

### 🔴 投递运行时
- **Actor 按 Channel 隔离**: 每个 Channel 有独立的 Actor，通过 shard 分片减少锁竞争
- **Actor 空闲回收**: 超过 1 分钟空闲会被回收
- **不等待全局连续 MessageSeq**: owner routing 与 replay 会让节点只看到该 Channel 全局序列的稀疏子集，按本节点已观察到的提交流推进
- **投递重试有上限**: 默认重试延迟 [500ms, 1s, 2s]，最大重试次数 = 4 次，超过后移除 Ack 绑定并依赖 Channel Log catch-up
- **AckIndex 是关键**: `AckIndex.Bind` 在 Push 时建立 SessionID+MessageID → Channel+Route 的映射
- **DeliveryTag 是 Leader 权威分区快照**: Leader 构建全频道订阅者分区，Follower 仅缓存本节点分区；普通订阅者变更保持 `tagKey`、递增 `tagVersion`，陈旧 tag 响应只触发重试/刷新，不能覆盖新缓存
- **远程实时投递只在单条 committed message 内批量**: 不跨消息聚合；按目标节点合并 RPC，Group 频道大 route 集会按 chunk 拆分，Person 频道按 recipient view 拆分 frame item；delivery push v2 响应用 accepted count 避免回显所有成功 route，滚动升级时可回退 legacy 请求/响应

### 🔴 在线状态
- **权威路由通过 Raft**: `RegisterAuthoritative` 是一次 Raft Propose，写入 Slot Leader 的状态机
- **失败回滚顺序**: `Activate` 失败时先 `bestEffortUnregister`（注销权威路由）再 `online.Unregister`（清理本地）

### 🔴 会话同步
- **工作集同步**: 默认候选来自 `ListUserConversationActive`（持久化 `active_at` + UID-owner hot active hint overlay）和客户端 `last_msg_seqs`，再以 Channel Log 作为最新消息事实源。
- **Legacy version 兼容**: 请求 `version` 只保留兼容语义，不再触发完整历史增量发现；返回项 `version` 是展示时间与用户状态更新时间的兼容水位。
- **删除屏障**: DeleteConversation 持久化 `deleted_to_seq`、清空 `active_at` 并删除 hot hint；`message_seq <= deleted_to_seq` 的 hint/recents 不可见，更新消息可重新激活会话。
- **普通会话不读 CMD 状态**: `/conversation/sync` 只依赖 `UserConversationState`，不能把 `CMDConversationState` 混入普通会话工作集。

### 🔴 CMD 离线同步
- **UID owner 与 command owner 分离**: `/message/sync` 和 `/message/syncack` 按 UID slot owner 路由；UID owner 再按 `source____cmd` 的 ChannelRuntimeMeta leader 读取 command-channel log。
- **CMD state 独立**: `CMDConversationState` 只服务 legacy CMD 离线同步，不能写入或读取普通 `UserConversationState`。
- **syncack 只推进最近 generation**: `/message/syncack` 只消费 `SyncRecordCache` 中最近一次 `/message/sync` 返回的 record，不按客户端 `last_message_seq` 重新扫描频道。
- **不持久化 subscriber snapshot**: CMD offline sync 写入/恢复的是 conversation intent pending update；graceful stop 可恢复未 flush pending，kill -9 不保证内存 intent 持久化。

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
- **committedFanout + owner routing**: message 用例只发布 `messageevents.MessageCommitted`；`asyncCommittedDispatcher` 用有界分片队列分发 delivery / conversation 副作用，`pluginCommittedRouter`（插件启用时）通过独立 node RPC 将 PersistAfter 副作用路由到 Channel owner 节点；committed replay 不触发插件副作用。
- **committed dispatcher 溢出不影响 durable send**: 队列满只记录指标并触发 best-effort conversation fallback，Sendack 仍只由 Channel Log quorum commit 决定。
- **committed dispatcher 停止依赖 replay 补偿**: 停止时不再接收新队列任务，未启动/停止中的提交返回内部错误供调用方记录；队列内未处理实时投递可被丢弃并由 committed_replay 兜底补发。
- **committed_replay 是补偿路径，不阻塞 Sendack**: Sendack 仍只等待 Channel Log quorum commit；后台 replayer 以 Channel Log 为真相，用批量 cursor 兜底补发 delivery / conversation，active hint 是可丢弃的 best-effort 路径。
- **性能指标走窄接口注入**: message append、meta refresh、dispatch queue、delivery resolve/push、runtime gauge 和 replay lag 由 `internal/app` 组合根接入 `pkg/metrics`，低层包不直接依赖具体 metrics registry。
- **诊断 trace 只走窄链路**: access/gateway 与 access/api 负责创建或校验节点内 diagnostics trace context；`message.SendCommand.TraceID` 把它传到 message 用例和 channel append；gateway send/sendack 与 durable send 的 `pkg/observability/sendtrace` 事件携带 diagnostics-safe `channel_key`，支持按频道 debug match 采样；`internal/app` 组合根负责安装 diagnostics store 的 sendtrace sink 并在 Stop 时恢复。

### 🔴 插件子系统
- **默认关闭**: `WK_PLUGIN_ENABLE=false` 时不构建 plugin runtime/usecase/access，也不收集 offline Receive UID；单节点集群也不绕过节点间 owner routing 语义。
- **本地运行时边界**: Runtime 只扫描节点本地 `*.wkp` 可执行文件，启动时传入 `--socket` 与 `--sandbox`；`StateDir` 只保存节点本地 desired config/enabled，UID 绑定不放在本地文件。
- **Phase 1 PDK 兼容面**: 支持 `.wkp`/go-pdk 的 `Send`、`PersistAfter`、`Receive`、`Route`、`ConfigUpdate` 和 `/stop`；host RPC 支持 `/plugin/start`、`/close`、`/message/send`、`/channel/messages`、`/plugin/httpForward`、`/cluster/config`、`/cluster/channels/belongNode`、`/conversation/channels`；`/stream/open|write|close` 稳定返回 unimplemented。
- **hook 注入点明确**: SendHook 注入 message usecase，按本地 running plugin 的 priority 降序执行；PersistAfter 只在 committed owner 上执行，远端通过 `plugin_committed` RPC 路由；Receive 只对 durable、非 request-scoped、非 SyncOnce、非 NoPersist 的离线收件人执行。
- **插件发送不绕过消息用例**: 插件发起 `/message/send` 会设置 `SendOriginPlugin` 并进入 `message.App.Send`，仍走权限、hook recursion guard、NoPersist/SyncOnce 和集群 append 分支。
- **Receive 有界反压**: offline UID 按 resolver page 通知，observer 使用有界 worker/queue；满载时反压 delivery，不静默丢 eligible hook，也不创建无界 goroutine。
- **绑定是 Slot 权威元数据**: manager 的 plugin binding API 经 `pkg/slot` 按 UID hash slot 写入 Raft；按 plugin_no 列表是管理查询，需要跨 Slot 权威分页聚合。
- **管理面语义分离**: `/manager/nodes/:node_id/plugins...` 是节点本地运行时/desired state 管理；`/manager/plugin-bindings` 是集群 UID 绑定管理；二者均要求 `cluster.plugin` 权限。公开 `ANY /plugins/:plugin/*path` Phase 1 保持开放且节点本地。
- **Send hook 失败策略**: `WK_PLUGIN_FAIL_OPEN=false` 时 Send hook 读状态或调用失败会 fail-closed；开启后返回原始命令并继续发送，插件返回非 success reason 仍按业务拒绝处理。

---

## 8. 测试说明

### 快速测试（默认）
```bash
GOWORK=off go test ./internal/app
GOWORK=off go test ./internal/...
GOWORK=off go test ./internal/access/gateway ./pkg/gateway/transport/gnet
```

### 集成测试（慢测试）
```bash
GOWORK=off go test -tags=integration ./internal/app
GOWORK=off go test -tags=integration ./internal/access/gateway ./pkg/gateway/transport/gnet
```

### 边界检查
```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

---

**文档版本**: v2.1
**最后更新**: 2026-05-16
