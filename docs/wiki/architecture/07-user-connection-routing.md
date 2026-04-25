# 用户连接与跨节点消息路由

> 本文档描述用户连接到集群节点后，系统如何注册在线状态、如何定位目标用户所在节点，以及消息如何跨节点投递到接收者。

## 1. 核心问题

在分布式部署中，用户 A 连接到节点 1，用户 B 连接到节点 2。当用户 A 给用户 B 发消息时，节点 1 需要知道用户 B 在哪个节点上。

WuKongIM 通过 **Slot 哈希 + Presence Authority（在线权威目录）** 机制解决这个问题：

```
用户 B 的 UID
    │
    ▼  CRC32 哈希
SlotID = HashSlotTable.Lookup(CRC32("userB") % HashSlotCount)
    │
    ▼  找到该 Slot 的 Raft Leader
Presence Authority（该 Slot Leader 上的内存目录）
    │
    ▼  查询 directory.endpointsByUID("userB")
返回 [{NodeID: 2, BootID: X, SessionID: Y}]
    │
    ▼  按 NodeID 分组投递
RPC → 节点 2 → 本地 Session → 用户 B 收到消息
```

## 2. 用户连接注册

### 2.1 连接激活流程

**代码位置**：`internal/usecase/presence/gateway.go:34-78`

用户通过 TCP/WebSocket 连接到某个节点后，Gateway 完成协议握手，调用 `presence.App.Activate()` 注册在线状态：

```
用户 B 连接到节点 2
  │
  ▼
presence.App.Activate(ActivateCommand{
    UID, DeviceID, DeviceFlag, DeviceLevel, Session
})
  │
  ├─ 1. localConnFromActivate(cmd)
  │     构造 OnlineConn，计算 SlotID = router.SlotForKey(uid)
  │
  ├─ 2. online.Register(conn)
  │     注册到本地内存 Registry（MemoryRegistry）
  │
  ├─ 3. authority.RegisterAuthoritative(ctx, {SlotID, Route})
  │     向 Slot Raft Leader 上的 Presence Authority 注册路由
  │     Route = {UID, NodeID, BootID, SessionID, DeviceID, ...}
  │
  └─ 4. dispatchActions(ctx, result.Actions)
        处理设备冲突（如踢掉同一用户的旧设备连接）
```

### 2.2 两层注册

用户连接信息存储在两个层级：

| 层级 | 组件 | 位置 | 作用 |
| ---- | ---- | ---- | ---- |
| 本地 | `MemoryRegistry` | 每个节点内存 | 管理本节点的 TCP Session，快速查找本地连接 |
| 权威 | `directory`（Presence Authority） | Slot Raft Leader 节点内存 | 全局在线状态的权威来源，跨节点查询用户位置 |

**本地 Registry**（`internal/runtime/online/registry.go`）：

```
MemoryRegistry
  ├─ bySession: SessionID → OnlineConn     // 按 Session 查连接
  ├─ byUID:     UID → map[SessionID]Conn   // 按用户查所有连接
  └─ byGroup:   SlotID → []Conn            // 按 Slot 分组
```

**权威 Directory**（`internal/usecase/presence/directory.go:26-31`）：

```
directory
  ├─ byUID:    UID → map[routeKey]Route    // 全局用户路由表
  ├─ leases:   leaseKey → GatewayLease     // 网关租约（TTL 30s）
  └─ ownerSet: leaseKey → map[routeKey]Route // 按网关分组的路由
```

### 2.3 Route 数据结构

**代码位置**：`internal/usecase/presence/types.go:10-19`

```go
type Route struct {
    UID         string   // 用户 ID
    NodeID      uint64   // 连接所在节点
    BootID      uint64   // 节点启动 ID（区分重启前后）
    SessionID   uint64   // TCP Session ID
    DeviceID    string   // 设备标识
    DeviceFlag  uint8    // 设备类型（手机/PC/Web）
    DeviceLevel uint8    // 设备级别（Master/Slave）
    Listener    string   // 监听路径（WebSocket/TCP）
}
```

### 2.4 连接断开

**代码位置**：`internal/usecase/presence/gateway.go:80-109`

用户断开连接时，调用 `Deactivate()`：

```
presence.App.Deactivate(DeactivateCommand{UID, SessionID})
  │
  ├─ online.Unregister(sessionID)           // 从本地 Registry 移除
  └─ authority.UnregisterAuthoritative(...)  // 从权威 Directory 移除
```

## 3. Presence Authority：用户在哪个节点

### 3.1 权威目录的归属

每个用户的在线状态由**一个特定的 Slot Raft Leader** 管理。哪个 Slot 负责哪个用户，由 UID 的哈希决定：

```go
// internal/usecase/presence/gateway.go:47
slotID := a.router.SlotForKey(conn.UID)
```

这意味着：
- 用户 B 的 UID 哈希到 Slot 5
- Slot 5 的 Raft Leader 在节点 3
- 那么节点 3 上的 `directory` 就是用户 B 在线状态的权威来源

### 3.2 注册过程

**代码位置**：`internal/usecase/presence/directory.go:43-88`

```
directory.register(slotID, route, nowUnix)
  │
  ├─ sweepExpiredLocked(nowUnix)    // 清理过期租约
  │
  ├─ 检查设备冲突：
  │   遍历 byUID[route.UID] 中的已有路由
  │   if conflicts(incoming, existing):
  │     ├─ Master 级别 → 踢掉所有同类设备
  │     └─ Slave 级别 → 仅踢掉同 DeviceID 的连接
  │   生成 RouteAction（close / kick_then_close）
  │
  ├─ byUID[route.UID][routeKey] = route   // 写入用户路由表
  ├─ ownerSet[leaseKey][routeKey] = route  // 写入网关分组
  └─ 更新 GatewayLease（续期 30s TTL）
```

### 3.3 查询过程

**代码位置**：`internal/usecase/presence/directory.go:168-193`

```go
func (d *directory) endpointsByUID(uid string, nowUnix int64) []Route {
    d.sweepExpiredLocked(nowUnix)
    routes := d.byUID[uid]
    // 返回该用户的所有在线路由（可能多设备）
    // 按 SessionID → NodeID → BootID 排序，保证确定性
}
```

### 3.4 租约与过期清理

网关节点通过心跳续期租约（默认 TTL 30 秒）。如果网关节点宕机，心跳停止，租约过期后 `sweepExpiredLocked()` 会自动清理该节点的所有路由，避免消息投递到已死节点。

## 4. 跨节点查询：presenceAuthorityClient

### 4.1 路由到正确的 Slot Leader

**代码位置**：`internal/app/presenceauthority.go:47-97`

查询用户在线端点时，需要路由到该用户 UID 对应 Slot 的 Raft Leader：

```
presenceAuthorityClient.EndpointsByUID(ctx, "userB")
  │
  ├─ slotID = cluster.SlotForKey("userB")
  ├─ leaderID = cluster.LeaderOf(slotID)
  │
  ├─ if cluster.IsLocal(leaderID):
  │   └─ local.EndpointsByUID(ctx, "userB")   // 本地直接查
  │
  └─ else:
      └─ remote.EndpointsByUID(ctx, "userB")   // RPC 到远端 Leader
```

### 4.2 批量查询优化

**代码位置**：`internal/app/presenceauthority.go:61-97`

投递消息时通常需要查询多个用户的在线端点。`EndpointsByUIDs` 按 SlotID 分组，减少 RPC 次数：

```
EndpointsByUIDs(ctx, ["userB", "userC", "userD"])
  │
  ├─ 按 SlotID 分组：
  │   Slot 5 → ["userB", "userD"]
  │   Slot 8 → ["userC"]
  │
  ├─ Slot 5 Leader 在本地 → local.EndpointsByUIDs(ctx, ["userB", "userD"])
  └─ Slot 8 Leader 在远端 → remote.EndpointsByUIDs(ctx, ["userC"])
```

## 5. 消息投递：从发送到接收

### 5.1 投递触发

消息写入 Channel ISR 并提交后，`asyncCommittedDispatcher` 异步触发投递（详见 `05-message-sending-flow.md`）。投递在 Channel ISR Leader 节点上执行。

### 5.2 解析订阅者与在线端点

**代码位置**：`internal/app/deliveryrouting.go:109-220`

```
localDeliveryResolver.ResolvePage()
  │
  ├─ subscribers.NextPage(ctx, snapshot, cursor, pageSize)
  │   从 Slot 层分页获取频道订阅者 UID 列表
  │
  ├─ authority.EndpointsByUIDs(ctx, uids)
  │   查询每个 UID 的在线端点（Route 列表）
  │
  └─ 展开为 RouteKey 列表：
     [{UID: "userB", NodeID: 2, BootID: X, SessionID: Y}, ...]
```

### 5.3 按节点分组投递

**代码位置**：`internal/app/deliveryrouting.go:266-329`

```
distributedDeliveryPush.Push(ctx, cmd)
  │
  ├─ 按 NodeID 分组：
  │   本地路由 → localRoutes
  │   远端路由 → remoteRoutes[nodeID][uid]
  │
  ├─ 本地投递：
  │   localDeliveryPush.pushEnvelope(env, localRoutes)
  │     → online.Connection(sessionID)
  │     → conn.Session.WriteFrame(recvPacket)   // TCP 直写
  │
  └─ 远端投递：
      for nodeID, routesByUID := range remoteRoutes:
        f = buildRealtimeRecvPacket(msg, uid)
        frameBytes = codec.EncodeFrame(f)
        client.PushBatch(ctx, nodeID, DeliveryPushCommand{
            Routes: routes, Frame: frameBytes,
        })
        → 远端节点收到后本地 WriteFrame 到接收者 Session
```

## 6. 完整流程示例

用户 A（节点 1）给用户 B（节点 2）发消息的完整链路：

```
┌─────────────────────────────────────────────────────────────────┐
│ 阶段一：用户 B 连接注册（发生在用户 B 上线时）                 │
│                                                                  │
│  用户 B → 节点 2 (TCP 连接)                                     │
│    → presence.Activate({UID: "userB", Session: ...})            │
│    → online.Register(conn)              // 节点 2 本地 Registry │
│    → SlotForKey("userB") = Slot 5                               │
│    → Slot 5 Leader (节点 3) 的 directory:                       │
│       byUID["userB"] = {NodeID:2, SessionID:Y, ...}            │
└─────────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────────┐
│ 阶段二：消息写入（发生在用户 A 发送时）                        │
│                                                                  │
│  用户 A → 节点 1 (SendPacket)                                   │
│    → 频道寻址 → Channel ISR Leader (假设节点 1)                 │
│    → ISR 写入 → HW 推进 → 回包 SendackPacket                   │
│    → asyncCommittedDispatcher.SubmitCommitted(msg)              │
└─────────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────────┐
│ 阶段三：投递路由（异步，在 ISR Leader 节点执行）               │
│                                                                  │
│  localDeliveryResolver.ResolvePage()                            │
│    → subscribers.NextPage() → ["userB"]                         │
│    → authority.EndpointsByUIDs(["userB"])                        │
│       → SlotForKey("userB") = Slot 5                            │
│       → Slot 5 Leader (节点 3):                                 │
│          directory.endpointsByUID("userB")                      │
│          → [{NodeID:2, BootID:X, SessionID:Y}]                  │
└─────────────────────────────────────────────────────────────────┘
                              │
┌─────────────────────────────────────────────────────────────────┐
│ 阶段四：跨节点推送                                              │
│                                                                  │
│  distributedDeliveryPush.Push()                                 │
│    → NodeID=2 ≠ 本地 → 远端投递                                │
│    → buildRealtimeRecvPacket(msg, "userB")                      │
│    → client.PushBatch(ctx, nodeID=2, {Frame, Routes})           │
│    → 节点 2 收到 RPC:                                           │
│       → online.Connection(sessionID=Y)                          │
│       → conn.Session.WriteFrame(recvPacket)                     │
│    → 用户 B 的 TCP 连接收到 RecvPacket                          │
└─────────────────────────────────────────────────────────────────┘
```

## 7. 设备冲突处理

### 7.1 冲突规则

**代码位置**：`internal/usecase/presence/directory.go:252-264`

```go
func conflicts(incoming, existing Route) bool {
    if incoming.UID != existing.UID || incoming.DeviceFlag != existing.DeviceFlag {
        return false  // 不同用户或不同设备类型，不冲突
    }
    switch incoming.DeviceLevel {
    case Master:
        return true   // Master 级别踢掉所有同类设备
    case Slave:
        return incoming.DeviceID == existing.DeviceID  // Slave 仅踢同设备
    }
}
```

### 7.2 踢设备流程

当用户 B 在新手机上登录，旧手机连接会被踢掉：

```
新连接注册 → directory.register() 检测到冲突
  → 生成 RouteAction{Kind: "kick_then_close", SessionID: 旧Session}
  → dispatchActions() → ApplyRouteAction()
    → 向旧 Session 发送 DisconnectPacket{ReasonCode: ConnectKick}
    → 延迟 2s 后关闭旧连接
```

## 8. 容错机制

| 场景 | 处理方式 |
| ---- | -------- |
| Slot Leader 不可达 | `presenceAuthorityClient` 通过 `callAuthoritativeRPC` Leader 追踪循环重定向 |
| 网关节点宕机 | 租约 30s 过期后，`sweepExpiredLocked()` 自动清理该节点所有路由 |
| 用户多设备在线 | `directory.byUID` 支持同一 UID 多个 Route，投递时全部推送 |
| 注册权威失败 | `Activate()` 回滚：`online.Unregister()` 清理本地状态 |
| 投递远端节点失败 | 标记为 Retryable，由投递运行时调度重试 |

## 9. 关键设计决策

### 9.1 为什么用 Slot 哈希而不是全局广播

- **O(1) 定位**：通过 UID 哈希直接算出负责的 Slot，再查 Raft Leader，无需广播。
- **强一致**：Presence 数据由 Slot Raft Leader 管理，不会出现两个节点对同一用户在线状态的不一致视图。
- **水平扩展**：用户分散到不同 Slot，Slot 可以分布在不同节点上，避免单点瓶颈。

### 9.2 为什么 Presence 数据存内存而不是 Raft 日志

- **高频变更**：用户上下线频繁，写入 Raft 日志开销过大。
- **可重建**：网关节点通过心跳续期租约，宕机后租约过期自动清理；重启后用户重连自动重新注册。
- **低延迟**：内存查询 O(1)，投递热路径不需要磁盘 IO。

### 9.3 为什么投递在 ISR Leader 节点执行

- **避免重复投递**：集中在一个节点做投递决策，不会出现多节点同时推送同一消息。
- **统一 ACK 管理**：投递状态和确认跟踪在同一节点，简化一致性。
- 详见 `05-message-sending-flow.md` 第 5.2 节。
