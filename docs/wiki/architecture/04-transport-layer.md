# Transport · 集群网络层

> 横向贯穿三层的统一集群通信基础设施
> 所有节点间通信（Controller / Slot / Channel）共享同一套 Transport

## 1. 概述

Transport 层是 WuKongIM 分布式架构的"血管系统"。它提供了一套简单高效的 TCP 通信框架：

- **二进制帧协议**：5 字节头 + payload，零拷贝友好
- **双模式通信**：单向消息（Fire-and-Forget） + RPC（Request/Response）
- **Service 多路复用**：一个 TCP 连接上承载多种 RPC 服务
- **连接池 + 分片**：固定大小连接池，按 shardKey 选择连接

## 2. 代码结构

```
pkg/transport/
├── types.go       # 核心接口定义（26 行）
├── errors.go      # 错误类型与常量（27 行）
├── codec.go       # 帧编解码（69 行）
├── server.go      # TCP 服务端（208 行）
├── client.go      # 客户端 + RPC（197 行）
├── pool.go        # 连接池（137 行）
└── rpcmux.go      # RPC 服务路由（61 行）
```

## 3. 协议格式

### 3.1 消息帧

```
┌───────────────┬──────────────────┬──────────────────────┐
│  msgType (1B) │  bodyLen (4B BE) │       body (N B)     │
└───────────────┴──────────────────┴──────────────────────┘
     总头部：5 字节         最大 body 大小：64 MB
```

### 3.2 消息类型分配

| msgType | 用途                   | 方向         |
| ------- | ---------------------- | ------------ |
| 0x00    | 保留（无效）           | -            |
| 0x01    | MultiRaft 消息         | 单向         |
| 0x02    | Controller Raft 消息   | 单向         |
| 0x03-0xFD | 用户自定义           | 单向         |
| 0xFE    | RPC Request            | 请求         |
| 0xFF    | RPC Response           | 响应         |

### 3.3 RPC 帧格式

**RPC Request body:**
```
┌──────────────────┬──────────────────────┐
│  requestID (8B)  │     payload (N B)    │
└──────────────────┴──────────────────────┘
```

**RPC Response body:**
```
┌──────────────────┬──────────────┬──────────────────┐
│  requestID (8B)  │ errCode (1B) │     data (N B)   │
└──────────────────┴──────────────┴──────────────────┘
```

**Service 多路复用（payload 内部）:**
```
┌───────────────┬───────────────────────┐
│ serviceID (1B)│   handler_payload (N) │
└───────────────┴───────────────────────┘
```

## 4. Server

### 4.1 结构

```go
// server.go
type Server struct {
    listener   net.Listener
    handlers   map[uint8]MessageHandler    // msgType → handler
    rpcHandler RPCHandler                  // 统一 RPC 处理
    accepted   map[net.Conn]struct{}       // 跟踪所有连接
    stopCh     chan struct{}
    wg         sync.WaitGroup
}
```

### 4.2 注册与启动

```go
// 注册消息处理器（不能注册 0x00, 0xFE, 0xFF）
server.Handle(msgType, handler)

// 注册 RPC 服务多路复用
server.HandleRPCMux(rpcMux)

// 启动 TCP 监听
server.Start(listenAddr)
```

### 4.3 连接处理流程

```
listener.Accept()
    │
    │ 设置 TCP Keep-Alive（30s）
    │ 加入 accepted 集合
    │ 启动 goroutine
    ▼
handleConn(conn)
    │
    │ for { ReadMessage(conn) → msgType, body }
    ▼
    ├── msgType == RPC Request (0xFE)
    │       → 提取 requestID
    │       → goroutine: rpcHandler(body)
    │       → 写回 RPC Response（带 writeMu 保护）
    │
    └── msgType == 普通消息
            → 调用 handlers[msgType](conn, body)
```

### 4.4 并发安全

- 每个连接一个读 goroutine（`handleConn`）
- RPC 响应写回使用 `writeMu` 互斥锁保证帧完整性
- RPC handler 在独立 goroutine 中执行，不阻塞消息读取

## 5. Client

### 5.1 结构

```go
// client.go
type Client struct {
    pool      *Pool                 // 连接池
    nextReqID atomic.Uint64         // RPC 请求 ID 生成器
    pending   sync.Map              // requestID → responseChan
    readLoops sync.Map              // (nodeID, poolIdx) → 读循环
    stopCh    chan struct{}
    wg        sync.WaitGroup
}
```

### 5.2 单向发送

```go
// client.go:40-52
func (c *Client) Send(nodeID, shardKey, msgType, body) error {
    conn, idx := c.pool.Get(nodeID, shardKey)   // 获取连接
    defer c.pool.Release(nodeID, idx)            // 释放锁
    err := WriteMessage(conn, msgType, body)
    if err != nil {
        c.pool.Reset(nodeID, idx)                // 连接异常，重置
    }
    return err
}
```

### 5.3 RPC 调用

```go
// client.go:54-94
func (c *Client) RPC(ctx, nodeID, shardKey, payload) ([]byte, error) {
    reqID := c.nextReqID.Add(1)           // 生成唯一请求 ID
    ch := make(chan rpcResponse, 1)
    c.pending.Store(reqID, ch)            // 注册等待
    defer c.pending.Delete(reqID)

    c.ensureReadLoop(nodeID, idx, conn)   // 确保读循环已启动
    WriteMessage(conn, MsgTypeRPCRequest, encode(reqID, payload))

    select {
    case resp := <-ch:                    // 等待响应
        return resp.data, resp.err
    case <-ctx.Done():                    // 超时/取消
        return nil, ctx.Err()
    }
}
```

### 5.4 Service RPC

```go
// client.go:92-94
func (c *Client) RPCService(ctx, nodeID, shardKey, serviceID, payload) ([]byte, error) {
    // 在 payload 前面加上 serviceID 字节
    return c.RPC(ctx, nodeID, shardKey, [serviceID | payload])
}
```

### 5.5 读循环

每个 (nodeID, poolIndex) 对维护一个读循环 goroutine：

```go
// client.go:140-179
func (c *Client) readLoop(nodeID, idx, conn) {
    for {
        msgType, body := ReadMessage(conn)
        if msgType == MsgTypeRPCResponse {
            reqID, errCode, data := decode(body)
            ch, ok := c.pending.Load(reqID)
            if ok {
                ch <- rpcResponse{data, errCode}
            }
        }
    }
}
```

使用 CAS（Compare-And-Swap）防止重复启动（`client.go:116-138`）。

## 6. 连接池（Pool）

### 6.1 结构

```go
// pool.go
type Pool struct {
    discovery   Discovery             // NodeID → 地址解析
    size        int                    // 每节点连接数（默认 4）
    dialTimeout time.Duration          // 拨号超时（默认 5s）
    nodes       map[NodeID]*nodeConns  // 每节点连接数组
    mu          sync.RWMutex
}

type nodeConns struct {
    addr  string
    conns []net.Conn                   // 固定大小的连接数组
    mu    []sync.Mutex                 // 每槽位独立锁
}
```

### 6.2 连接选择算法

```
Get(nodeID, shardKey)
    │
    │ idx = shardKey % poolSize
    │ lock(nodes[nodeID].mu[idx])
    │
    ├─ nodes[nodeID].conns[idx] != nil → 返回现有连接
    │
    └─ nodes[nodeID].conns[idx] == nil
         → Discovery.Resolve(nodeID)
         → net.DialTimeout(addr, dialTimeout)
         → 设置 TCP Keep-Alive（30s）
         → 存入 conns[idx]
         → 返回新连接
```

**关键设计**：
- **确定性路由**：同一个 shardKey 始终选择同一个连接槽位
- **按槽位加锁**：不同 shardKey 的请求可以并行使用不同连接
- **惰性连接**：首次使用时才建立连接

### 6.3 连接重置

```go
// pool.go:71-81
func (p *Pool) Reset(nodeID, idx) {
    // 关闭连接，清空槽位
    // 下次 Get() 会重新拨号
    // 注意：不释放锁，由调用方 Release()
}
```

## 7. RPC 多路复用器（RPCMux）

```go
// rpcmux.go
type RPCMux struct {
    handlers map[uint8]RPCHandler    // serviceID → handler
}

func (m *RPCMux) Handle(serviceID, handler)   // 注册服务
func (m *RPCMux) HandleRPC(ctx, body) ([]byte, error) {
    serviceID := body[0]
    handler := m.handlers[serviceID]
    return handler(ctx, body[1:])
}
```

## 8. 三层使用 Transport 的方式

### 8.1 注册总览

```go
// raftcluster/cluster.go:87-93
server := nodetransport.NewServer()

// 单向消息
server.Handle(1, handleRaftMessage)           // MultiRaft 消息
server.Handle(2, handleControllerRaftMessage) // Controller Raft 消息

// RPC 服务
rpcMux := nodetransport.NewRPCMux()
rpcMux.Handle(1, handleForwardRPC)            // 请求转发
rpcMux.Handle(14, handleControllerRPC)        // Controller 操作
rpcMux.Handle(20, handleManagedSlotRPC)       // Slot 管理
server.HandleRPCMux(rpcMux)
```

### 8.2 各层消息类型汇总

| 层级         | 通信方式     | 类型/ID | 用途                      | 编码格式    |
| ------------ | ------------ | ------- | ------------------------- | ----------- |
| Controller   | 单向消息     | msgType=2 | Raft 协议消息           | Protobuf    |
| Controller   | RPC          | service=14 | 心跳/任务/查询         | JSON        |
| Slot         | 单向消息     | msgType=1 | MultiRaft 协议消息       | Protobuf    |
| Slot         | RPC          | service=1 | 请求转发到 Leader        | 自定义二进制 |
| Slot         | RPC          | service=20 | Slot 配置/状态查询      | JSON        |
| Channel      | （通过 isrnode transport） | ISR 复制消息 | FetchReq/Resp/Ack | 自定义      |

### 8.3 连接池使用

```go
// 所有层共享同一个连接池实例
raftPool := nodetransport.NewPool(discovery, poolSize, dialTimeout)
raftClient := nodetransport.NewClient(raftPool)   // Raft 消息客户端
fwdClient := nodetransport.NewClient(raftPool)    // RPC 客户端
```

两个 Client 共享 Pool，但各自维护独立的 pending RPC 和读循环。

## 9. 消息流转图

### 9.1 MultiRaft 消息

```
Node A (Slot S1 Leader)                    Node B (Slot S1 Follower)
    │                                          │
    │ rawNode.Ready() → messages               │
    │                                          │
    │ raftTransport.Send(batch)                │
    │   body = [slotID:8][raftMsg:N]           │
    │   client.Send(nodeB, slotID, 1, body)    │
    │ ──────────[msgType=1]──────────────────▸ │
    │                                          │
    │                            handleRaftMessage(conn, body)
    │                              decode(body) → slotID, msg
    │                              runtime.Step(slotID, msg)
```

### 9.2 Controller RPC

```
Agent Node                                 Controller Leader
    │                                          │
    │ controllerClient.Report(heartbeat)       │
    │   RPCService(leaderID, ^uint32, 14, json)│
    │ ──────────[RPC, service=14]────────────▸ │
    │                                          │
    │                            handleControllerRPC(ctx, body)
    │                              decode JSON → request
    │                              Propose(CommandKindNodeHeartbeat)
    │                                          │
    │ ◀────────[RPC Response]──────────────── │
    │   json response                          │
```

### 9.3 Forward RPC

```
Node A (Non-Leader for G5)                 Node B (Leader for G5)
    │                                          │
    │ client.Propose(G5, data)                 │
    │   → not leader, find leaderID            │
    │   forwardToLeader(leaderB, G5, cmd)      │
    │   RPCService(nodeB, G5, 1, [G5|cmd])     │
    │ ──────────[RPC, service=1]─────────────▸ │
    │                                          │
    │                            handleForwardRPC(ctx, body)
    │                              decode → slotID, cmd
    │                              runtime.Propose(G5, cmd)
    │                              ... Raft 共识 ...
    │                                          │
    │ ◀────────[RPC Response, errCode=0]───── │
```

## 10. 错误处理

### 10.1 传输层错误

| 错误            | 场景                          | 处理方式                      |
| --------------- | ----------------------------- | ----------------------------- |
| ErrStopped      | Transport 已关闭              | 上层感知并停止                |
| ErrTimeout      | RPC 请求超时                  | 客户端 ctx.Done() 返回       |
| ErrNodeNotFound | Discovery 找不到节点地址      | 返回错误给调用方              |
| ErrMsgTooLarge  | 消息超过 64MB                 | 拒绝发送                      |
| 连接断开        | 网络故障 / 对端关闭           | Pool.Reset() → 下次重新拨号  |

### 10.2 Raft 消息的特殊处理

Raft 消息（msgType=1,2）使用单向发送，失败时**静默忽略**：

```go
// raftcluster/transport.go:25-27
// Raft 协议自身通过心跳机制保证消息最终送达
// 瞬时发送失败不需要重试，Raft 会在下一轮心跳重传
_ = t.client.Send(nodeID, shardKey, msgTypeRaft, body)
```

### 10.3 RPC 错误码

| errCode | 含义        | 上层处理              |
| ------- | ----------- | --------------------- |
| 0       | 成功        | 解析 data 返回       |
| 1       | NotLeader   | 重新发现 Leader      |
| 2       | Timeout     | 重试或返回错误       |
| 3       | NoSlot      | 返回 SlotNotFound    |

## 11. 性能特性

| 维度             | 值                                     |
| ---------------- | -------------------------------------- |
| 连接池大小       | 每节点 4 连接（可配）                  |
| 帧头开销         | 5 字节                                 |
| 最大消息         | 64 MB                                  |
| 拨号超时         | 5 秒                                   |
| Keep-Alive       | 30 秒                                  |
| RPC 响应关联     | O(1)（原子 requestID + sync.Map）      |
| 缓冲区复用       | sync.Pool，初始 512B，最大保留 64KB    |
| 写序列化         | Per-connection mutex（保证帧完整性）   |

## 12. 服务发现

### 12.1 Discovery 接口

```go
// types.go
type Discovery interface {
    Resolve(nodeID NodeID) (addr string, err error)
}
```

### 12.2 静态发现

```go
// raftcluster/discovery.go
type StaticDiscovery struct {
    nodes map[NodeID]string   // nodeID → "host:port"
}
```

从集群配置中的 `Nodes` 列表构建。适用于节点地址固定的部署场景。

## 13. 配置参考

| 参数           | 类型           | 默认值 | 说明                   | 代码位置           |
| -------------- | -------------- | ------ | ---------------------- | ------------------ |
| ListenAddr     | string         | 必填   | TCP 监听地址           | `raftcluster/config.go` |
| PoolSize       | int            | 4      | 每节点连接池大小       | `raftcluster/config.go` |
| DialTimeout    | time.Duration  | 5s     | TCP 拨号超时           | `pool.go`          |
| TCP Keep-Alive | -              | 30s    | TCP 心跳周期           | `pool.go:9`        |
| MaxMsgSize     | -              | 64MB   | 单帧最大消息大小       | `codec.go`         |
| BufferPool     | -              | 512B   | 缓冲区初始大小         | `codec.go`         |
| BufferPoolMax  | -              | 64KB   | 缓冲区最大保留大小     | `codec.go`         |
