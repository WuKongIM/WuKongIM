# Transport 包重构：Pipeline + 优先级调度

> 目标：Raft 流量永不被业务 RPC 阻塞；降低 GC 压力；提升连接利用率

## 1. 背景

`pkg/transport` 是集群节点间通信的基础层，承载三类流量：

| 流量类型 | msgType | 模式 | 频率 |
|----------|---------|------|------|
| Slot Raft | 1 | 单向 fire-and-forget | heartbeat 100ms / slot |
| Controller Raft | 2 | 单向 fire-and-forget | heartbeat 100ms |
| RPC (Forward / Controller / ISR) | 0xFE/0xFF | 请求-响应 | 随业务负载 |

**关键时序**：election timeout = 1s（= 10 × heartbeat tick），即连续 10 个 heartbeat 丢失或延迟就触发选举。

## 2. 现状问题

### 2.1 连接独占锁

`pool.go` 的 `Get()` 对 slot 连接加 Mutex，直到 `Release()` 才释放。
整个写操作期间同一 shard 的连接被独占，其他 goroutine 必须排队：

```go
func (p *Pool) Get(nodeID NodeID, shardKey uint64) (net.Conn, int, error) {
    idx := int(shardKey % uint64(p.size))
    nc.mu[idx].Lock()  // 独占直到 Release()
    ...
}
```

当 `Client.RPC()` 调用时：持锁写入 → 等 readLoop 读到响应 → 才返回释放锁。
期间同一 shard 的 raft `Send()` 全部排队。

### 2.2 ReadMessage 每次分配

```go
// codec.go:63
body = make([]byte, bodyLen)  // 每次读都分配
```

以 100ms × N slot × M node 频率，每秒产生数千次 alloc，GC 压力显著。

### 2.3 Raft 与 RPC 共享 Pool

```go
// cluster.go:100-103
c.raftPool = transport.NewPool(c.discovery, c.cfg.PoolSize, c.cfg.DialTimeout)
c.raftClient = transport.NewClient(c.raftPool)
c.fwdClient = transport.NewClient(c.raftPool)  // ← 共享同一 Pool
```

慢 Forward RPC 占用的连接正是 raft heartbeat 需要的连接。

### 2.4 sync.Map 在高写入场景不理想

`client.go` 的 `pending sync.Map` / `readLoops sync.Map` 在高写入频率下比 sharded mutex 慢。

## 3. 整体架构

### 3.1 核心思路

**旧模型**：独占连接 → 写数据 → 释放连接

**新模型**：入队到连接的优先级缓冲 → 独立 writer goroutine 按优先级批量发送

```
                     ┌──────────────────────────┐
  Raft Send ────────►│  MuxConn                  │
                     │  ┌─ P0 queue (raft) ─────┐│
  RPC Request ──────►│  ├─ P1 queue (rpc)  ─────┤│──► writerLoop ──► TCP
                     │  └─ P2 queue (bulk) ─────┘│    (net.Buffers / writev)
  Forward ──────────►│                            │
                     │  readerLoop ◄──────── TCP  │
                     │       │                    │
                     │       ▼                    │
                     │  dispatch by msgType       │
                     └──────────────────────────┘
```

### 3.2 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 线协议 | **保持不变** `[msgType:1][bodyLen:4][body:N]` | 优先级是发送端本地调度决策，不需要上线 |
| 隔离策略 | raft Pool + rpc Pool 各自独立连接集 | 逻辑隔离，物理上是独立 TCP 连接但共享 Server listener |
| 写入模型 | 无锁入队 + 独立 writer goroutine | 解除连接独占 |
| 读取模型 | 每连接独立 reader goroutine + dispatch | 与当前架构一致 |
| API 兼容 | 允许破坏性变更 | 一次到位，消除历史包袱 |

### 3.3 Pool 隔离方式

`cluster.go` 创建两个 Pool 实例，各自持有独立的 TCP 连接集：

```go
// 高优先级：raft 专用
raftPool := transport.NewPool(transport.PoolConfig{
    Discovery:   discovery,
    Size:        4,
    DefaultPri:  transport.PriorityRaft,
    QueueSizes:  [3]int{2048, 0, 0},
    DialTimeout: 3 * time.Second,
})

// 普通优先级：业务 RPC 专用
rpcPool := transport.NewPool(transport.PoolConfig{
    Discovery:   discovery,
    Size:        8,
    DefaultPri:  transport.PriorityRPC,
    QueueSizes:  [3]int{0, 1024, 256},
    DialTimeout: 5 * time.Second,
})
```

## 4. 关键组件

### 4.1 Frame / Slab Pool (`frame.go`)

分级对象池，替代 `codec.go` 中每次 `make([]byte, bodyLen)` 的热点分配：

```go
const (
    HeaderSize   = 5
    MaxFrameSize = 64 << 20
)

// slabPool 按 512 / 4K / 64K / 大于 64K 四档池化
var slabPool = newSlabPool([]int{512, 4096, 65536})

func readFrame(r io.Reader) (msgType uint8, body []byte, release func(), err error) {
    var hdr [HeaderSize]byte
    if _, err = io.ReadFull(r, hdr[:]); err != nil {
        return 0, nil, nil, err
    }
    msgType = hdr[0]
    if msgType == 0 {
        return 0, nil, nil, ErrInvalidMsgType
    }
    bodyLen := binary.BigEndian.Uint32(hdr[1:5])
    if bodyLen > MaxFrameSize {
        return 0, nil, nil, ErrMsgTooLarge
    }
    if bodyLen == 0 {
        return msgType, nil, func() {}, nil
    }
    buf, rel := slabPool.get(int(bodyLen))
    body = buf[:bodyLen]
    if _, err = io.ReadFull(r, body); err != nil {
        rel()
        return 0, nil, nil, err
    }
    return msgType, body, rel, nil
}

// writeFrame 编码帧到 net.Buffers（零拷贝，不合并 header+body）
func writeFrame(bufs *net.Buffers, msgType uint8, body []byte) {
    hdr := encodeHeader(msgType, len(body))  // 5 bytes, 栈分配
    *bufs = append(*bufs, hdr[:], body)
}
```

**要点**：
- 小于 512B 的 raft heartbeat 复用 slab，单次读取降至 0-1 alloc
- `release` 回调由消费方（handler）处理完后调用；Server 端在 dispatch 后自动 release

### 4.2 Priority Writer (`writer.go`)

每个 MuxConn 的独立写 goroutine，核心调度器：

```go
type Priority uint8

const (
    PriorityRaft Priority = 0  // P0 - 最高
    PriorityRPC  Priority = 1  // P1
    PriorityBulk Priority = 2  // P2 - 最低
    numPriorities          = 3
)

type writeItem struct {
    msgType uint8
    body    []byte
    done    chan error  // nil = fire-and-forget
}

type priorityWriter struct {
    conn     net.Conn
    queues   [numPriorities]chan writeItem
    stopCh   chan struct{}
    doneCh   chan struct{}
}

func newPriorityWriter(conn net.Conn, queueSizes [numPriorities]int) *priorityWriter {
    pw := &priorityWriter{
        conn:   conn,
        stopCh: make(chan struct{}),
        doneCh: make(chan struct{}),
    }
    for i := range pw.queues {
        size := queueSizes[i]
        if size == 0 {
            size = 64 // 最低保底
        }
        pw.queues[i] = make(chan writeItem, size)
    }
    go pw.loop()
    return pw
}
```

**调度逻辑**：

```go
func (pw *priorityWriter) loop() {
    defer close(pw.doneCh)

    batch := make([]writeItem, 0, 64)
    var bufs net.Buffers

    for {
        // 1. 阻塞等待任意队列有数据（优先 P0）
        item, ok := pw.waitAny()
        if !ok {
            return
        }
        batch = append(batch[:0], item)

        // 2. 贪心收集：优先排空 P0 → P1 → P2，批次上限 64
        batch = pw.drain(batch, PriorityRaft, 64)
        if len(batch) < 64 {
            batch = pw.drain(batch, PriorityRPC, 64-len(batch))
        }
        if len(batch) < 64 {
            batch = pw.drain(batch, PriorityBulk, 64-len(batch))
        }

        // 3. 编码为 net.Buffers 并 writev（单次 syscall 发送多帧）
        bufs = bufs[:0]
        for i := range batch {
            writeFrame(&bufs, batch[i].msgType, batch[i].body)
        }
        _, err := bufs.WriteTo(pw.conn)

        // 4. 通知等待方（RPC 的 done channel）
        for i := range batch {
            if batch[i].done != nil {
                batch[i].done <- err
            }
        }
        if err != nil {
            return // 连接断开，退出 → 上层触发重建
        }
    }
}

// waitAny 阻塞等待任意队列的数据，优先 P0
func (pw *priorityWriter) waitAny() (writeItem, bool) {
    // 先尝试非阻塞取 P0
    select {
    case item := <-pw.queues[0]:
        return item, true
    default:
    }
    // 阻塞等待任意
    select {
    case item := <-pw.queues[0]:
        return item, true
    case item := <-pw.queues[1]:
        return item, true
    case item := <-pw.queues[2]:
        return item, true
    case <-pw.stopCh:
        return writeItem{}, false
    }
}

// drain 非阻塞地从指定优先级收集最多 max 个 item
func (pw *priorityWriter) drain(batch []writeItem, p Priority, max int) []writeItem {
    for len(batch) < cap(batch) && max > 0 {
        select {
        case item := <-pw.queues[p]:
            batch = append(batch, item)
            max--
        default:
            return batch
        }
    }
    return batch
}
```

**优先级保证**：
- `waitAny()` 先尝试非阻塞取 P0，有 P0 数据就绝不等 P1/P2
- drain 顺序 P0 → P1 → P2，P0 优先填满批次
- 批次上限 64 帧，避免一次 writev 发送过大

**背压策略**：

| 优先级 | 队满行为 | 理由 |
|--------|---------|------|
| P0 (raft) | 阻塞等待，10ms 超时后丢弃 | raft 库有内置重传，短暂丢弃安全 |
| P1 (rpc) | 返回 `ErrQueueFull` | 调用方决定重试或超时 |
| P2 (bulk) | 返回 `ErrQueueFull` | 调用方决定 |

```go
func (pw *priorityWriter) enqueue(p Priority, item writeItem) error {
    if p == PriorityRaft {
        // P0 短超时
        select {
        case pw.queues[p] <- item:
            return nil
        case <-pw.stopCh:
            return ErrStopped
        case <-time.After(10 * time.Millisecond):
            return ErrQueueFull
        }
    }
    // P1/P2 非阻塞
    select {
    case pw.queues[p] <- item:
        return nil
    case <-pw.stopCh:
        return ErrStopped
    default:
        return ErrQueueFull
    }
}
```

### 4.3 MuxConn (`conn.go`)

封装一条 TCP 连接的全双工读写：

```go
type MuxConn struct {
    raw     net.Conn
    writer  *priorityWriter
    pending *pendingMap       // 分片 map，替代 sync.Map

    dispatch func(msgType uint8, body []byte, release func())

    readerDone chan struct{}
    closeOnce  sync.Once
    closed     atomic.Bool
}

func newMuxConn(raw net.Conn, dispatch func(uint8, []byte, func()), cfg ConnConfig) *MuxConn {
    mc := &MuxConn{
        raw:        raw,
        writer:     newPriorityWriter(raw, cfg.QueueSizes),
        pending:    newPendingMap(16),
        dispatch:   dispatch,
        readerDone: make(chan struct{}),
    }
    go mc.readLoop()
    return mc
}

// Send 入队一条 fire-and-forget 消息（非阻塞）
func (mc *MuxConn) Send(p Priority, msgType uint8, body []byte) error {
    return mc.writer.enqueue(p, writeItem{msgType: msgType, body: body})
}

// RPC 发送请求并等待响应
func (mc *MuxConn) RPC(ctx context.Context, p Priority, reqID uint64, body []byte) ([]byte, error) {
    ch := make(chan rpcResponse, 1)
    mc.pending.Store(reqID, ch)
    defer mc.pending.Delete(reqID)

    done := make(chan error, 1)
    if err := mc.writer.enqueue(p, writeItem{
        msgType: MsgTypeRPCRequest,
        body:    body,
        done:    done,
    }); err != nil {
        return nil, err
    }

    // 等写完成
    select {
    case err := <-done:
        if err != nil {
            return nil, err
        }
    case <-ctx.Done():
        return nil, ctx.Err()
    }

    // 等响应
    select {
    case resp := <-ch:
        return resp.body, resp.err
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-mc.readerDone:
        return nil, ErrStopped
    }
}

func (mc *MuxConn) readLoop() {
    defer close(mc.readerDone)
    defer mc.failAllPending(io.EOF)

    for {
        msgType, body, release, err := readFrame(mc.raw)
        if err != nil {
            return
        }
        if msgType == MsgTypeRPCResponse {
            mc.handleRPCResponse(body)
            release()
        } else {
            mc.dispatch(msgType, body, release)
        }
    }
}

func (mc *MuxConn) handleRPCResponse(body []byte) {
    if len(body) < 9 {
        return
    }
    reqID := binary.BigEndian.Uint64(body[0:8])
    errCode := body[8]
    data := body[9:]

    ch, ok := mc.pending.LoadAndDelete(reqID)
    if !ok {
        return
    }
    var resp rpcResponse
    if errCode != 0 {
        resp.err = fmt.Errorf("nodetransport: remote error: %s", data)
        resp.body = data
    } else {
        resp.body = data
    }
    ch <- resp
}

func (mc *MuxConn) Close() {
    mc.closeOnce.Do(func() {
        mc.closed.Store(true)
        mc.writer.stop()
        _ = mc.raw.Close()
    })
}

func (mc *MuxConn) failAllPending(err error) {
    mc.pending.Range(func(id uint64, ch chan rpcResponse) {
        select {
        case ch <- rpcResponse{err: err}:
        default:
        }
    })
}
```

### 4.4 Pending Map (`pending.go`)

分片互斥替代 `sync.Map`，减少高并发下的竞争：

```go
type pendingMap struct {
    shards []pendingShard
    mask   uint64
}

type pendingShard struct {
    mu sync.Mutex
    m  map[uint64]chan rpcResponse
}

func newPendingMap(numShards int) *pendingMap {
    shards := make([]pendingShard, numShards)
    for i := range shards {
        shards[i].m = make(map[uint64]chan rpcResponse)
    }
    return &pendingMap{shards: shards, mask: uint64(numShards - 1)}
}

func (p *pendingMap) Store(id uint64, ch chan rpcResponse)         { ... }
func (p *pendingMap) LoadAndDelete(id uint64) (chan rpcResponse, bool) { ... }
func (p *pendingMap) Range(fn func(uint64, chan rpcResponse))      { ... }
```

### 4.5 Pool (`pool.go`)

```go
type PoolConfig struct {
    Discovery   Discovery
    Size        int               // 每 peer 连接数
    DialTimeout time.Duration
    QueueSizes  [numPriorities]int
    DefaultPri  Priority
}

type Pool struct {
    cfg      PoolConfig
    nodes    sync.Map         // NodeID → *nodeConnSet
    dispatch func(uint8, []byte, func())
    nextID   atomic.Uint64    // RPC request ID 生成器
}

type nodeConnSet struct {
    addr string
    conns []atomic.Pointer[MuxConn]  // 无锁快路径读取
    dialMu sync.Mutex                // 串行 dial，避免重复建连
}

// Send 非阻塞发送，入队到对应连接的 priorityWriter
func (p *Pool) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error {
    mc, err := p.acquire(nodeID, shardKey)
    if err != nil {
        return err
    }
    return mc.Send(p.cfg.DefaultPri, msgType, body)
}

// RPC 发送请求并同步等待响应
func (p *Pool) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, payload []byte) ([]byte, error) {
    mc, err := p.acquire(nodeID, shardKey)
    if err != nil {
        return nil, err
    }
    reqID := p.nextID.Add(1)
    wire := encodeRPCRequest(reqID, payload)
    return mc.RPC(ctx, p.cfg.DefaultPri, reqID, wire)
}

// acquire 获取连接（无锁快路径 + 慢路径 dial）
func (p *Pool) acquire(nodeID NodeID, shardKey uint64) (*MuxConn, error) {
    set := p.getOrCreateNodeSet(nodeID)
    idx := int(shardKey % uint64(len(set.conns)))

    // 快路径：无锁
    if mc := set.conns[idx].Load(); mc != nil && !mc.closed.Load() {
        return mc, nil
    }

    // 慢路径：加锁 dial
    set.dialMu.Lock()
    defer set.dialMu.Unlock()
    if mc := set.conns[idx].Load(); mc != nil && !mc.closed.Load() {
        return mc, nil
    }

    raw, err := net.DialTimeout("tcp", set.addr, p.cfg.DialTimeout)
    if err != nil {
        return nil, err
    }
    setTCPKeepAlive(raw)
    mc := newMuxConn(raw, p.dispatch, ConnConfig{QueueSizes: p.cfg.QueueSizes})
    set.conns[idx].Store(mc)
    return mc, nil
}

func (p *Pool) Close() {
    p.nodes.Range(func(_, val any) bool {
        set := val.(*nodeConnSet)
        for i := range set.conns {
            if mc := set.conns[i].Load(); mc != nil {
                mc.Close()
            }
        }
        return true
    })
}
```

**与旧 Pool 关键区别**：
- **无 Get/Release/Reset** — `Send`/`RPC` 不再返回 `net.Conn`，不暴露底层连接
- **无锁快路径** — `atomic.Pointer[MuxConn]` 替代 `sync.RWMutex` + map lookup
- **自动重连** — MuxConn 的 readLoop 退出会标记 `closed`，下次 `acquire` 自动重 dial

### 4.6 Client (`client.go`)

Client 变为 Pool 的薄封装：

```go
type Client struct {
    pool *Pool
}

func NewClient(pool *Pool) *Client {
    return &Client{pool: pool}
}

func (c *Client) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error {
    return c.pool.Send(nodeID, shardKey, msgType, body)
}

func (c *Client) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, payload []byte) ([]byte, error) {
    return c.pool.RPC(ctx, nodeID, shardKey, payload)
}

func (c *Client) RPCService(ctx context.Context, nodeID NodeID, shardKey uint64, serviceID uint8, payload []byte) ([]byte, error) {
    return c.RPC(ctx, nodeID, shardKey, encodeRPCServicePayload(serviceID, payload))
}

func (c *Client) Stop() {
    // Pool 生命周期由外层管理，Client 不再持有 stopCh
}
```

### 4.7 Server (`server.go`)

Server 端 accept 的连接也封装为 MuxConn，RPC 响应走 priorityWriter：

```go
type Server struct {
    listener net.Listener
    handlers atomic.Pointer[handlerTable]  // copy-on-write，热路径无锁
    rpcHandler atomic.Value                // *RPCHandler

    conns  sync.Map
    stopCh chan struct{}
    wg     sync.WaitGroup
    cfg    ServerConfig
}

func (s *Server) acceptLoop() {
    defer s.wg.Done()
    for {
        raw, err := s.listener.Accept()
        if err != nil {
            select {
            case <-s.stopCh:
                return
            default:
                continue
            }
        }
        setTCPKeepAlive(raw)
        s.wg.Add(1)
        go s.serveConn(raw)
    }
}

func (s *Server) serveConn(raw net.Conn) {
    defer s.wg.Done()

    mc := newMuxConn(raw, s.makeDispatcher(), s.cfg.ConnConfig)
    s.conns.Store(mc, struct{}{})
    defer func() {
        s.conns.Delete(mc)
        mc.Close()
    }()

    <-mc.readerDone // 阻塞直到连接断开
}

func (s *Server) makeDispatcher() func(uint8, []byte, func()) {
    return func(msgType uint8, body []byte, release func()) {
        defer release()
        switch msgType {
        case MsgTypeRPCRequest:
            // RPC 请求需要写回响应 → 需要 MuxConn 引用
            // 通过闭包传递
            s.handleRPCRequest(body, /* mc 引用 */)
        default:
            if h := s.handlers.Load().get(msgType); h != nil {
                h(body)
            }
        }
    }
}
```

**Server 端 RPC 响应路径**：
Server accept 的 MuxConn 也有 priorityWriter，RPC handler 执行完后通过该 MuxConn 的 writer 写回响应：

```go
func (s *Server) handleRPCRequest(mc *MuxConn, body []byte) {
    if len(body) < 8 {
        return
    }
    reqID := binary.BigEndian.Uint64(body[0:8])
    payload := body[8:]

    h := s.rpcHandler.Load()
    if h == nil {
        return
    }
    handler := *h.(*RPCHandler)

    // 并发处理（和当前 Server 行为一致）
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        respData, err := handler(context.Background(), payload)
        var errCode uint8
        if err != nil {
            errCode = 1
            respData = []byte(err.Error())
        }
        respBody := encodeRPCResponse(reqID, errCode, respData)
        // 通过 MuxConn 的 priorityWriter 写回
        _ = mc.Send(PriorityRPC, MsgTypeRPCResponse, respBody)
    }()
}
```

**改进**：去掉了旧代码中 `handleConn` 里的 `writeMu sync.Mutex`，因为所有写操作都通过 priorityWriter 排队，天然串行。

## 5. API 变更清单

| 接口 | 旧签名 | 新签名 | 备注 |
|------|--------|--------|------|
| `NewPool` | `NewPool(d, size, timeout)` | `NewPool(PoolConfig{...})` | Config 结构体 |
| `Pool.Get/Release/Reset` | 三段式锁管理 | **删除** | 内部 acquire 替代 |
| `Pool.Send` | 不存在 | `Send(nodeID, shardKey, msgType, body) error` | Pool 直接发送 |
| `Pool.RPC` | 不存在 | `RPC(ctx, nodeID, shardKey, payload) ([]byte, error)` | Pool 直接 RPC |
| `Client.Send` | `Send(nodeID, shardKey, msgType, body)` | 签名不变 | 内部委托 Pool |
| `Client.RPC` | `RPC(ctx, nodeID, shardKey, payload)` | 签名不变 | 内部委托 Pool |
| `MessageHandler` | `func(net.Conn, []byte)` | `func([]byte)` | **去掉 conn 参数** |
| `RPCHandler` | `func(ctx, body) ([]byte, error)` | 不变 | — |
| `WriteMessage/ReadMessage` | 公开函数 | 改为内部 `writeFrame/readFrame` | 外部不直接操作 |

## 6. 数据流图

### 6.1 Raft Send 路径（单向）

```
raftTransport.Send(batch)
    │
    ▼  (for each envelope)
client.Send(nodeID, slotID, msgTypeRaft, data)
    │
    ▼
pool.Send(nodeID, slotID, msgTypeRaft, data)
    │
    ▼
pool.acquire(nodeID, slotID)  →  MuxConn (atomic.Pointer, 无锁)
    │
    ▼
mc.Send(PriorityRaft, msgTypeRaft, data)
    │
    ▼
priorityWriter.enqueue(P0, item)  →  channel 入队，立即返回
    │
    ▼  (writer goroutine)
waitAny() → drain(P0, 64) → drain(P1) → drain(P2)
    │
    ▼
net.Buffers.WriteTo(conn)  →  writev syscall (批量)
```

### 6.2 RPC 路径（请求-响应）

```
client.RPC(ctx, nodeID, shardKey, payload)
    │
    ▼
pool.RPC(ctx, ...) → acquire → MuxConn
    │
    ▼
mc.RPC(ctx, P1, reqID, body)
    │
    ├─► pending.Store(reqID, ch)       // 注册响应通道
    ├─► writer.enqueue(P1, item)       // 入队请求
    │       │
    │       ▼ (writer goroutine)
    │   writev → TCP → 对端 Server
    │
    └─► select { <-ch, <-ctx.Done() }  // 等响应
              ▲
              │ (reader goroutine)
        readFrame() → msgType=0xFF → handleRPCResponse()
            → pending.LoadAndDelete(reqID)
            → ch <- response
```

## 7. 性能预期

| 指标 | 当前 | 预期 | 改善 |
|------|------|------|------|
| Raft Send p99（RPC 空载） | ~50μs | ~30μs | ~1.6x |
| Raft Send p99（RPC 饱和） | 5-50ms | ~50μs | **100x** |
| ReadMessage alloc/op | 2 | 0-1 | slab 复用 |
| Write syscall/帧 | 1 | 最多 1/64 | writev 批量 |
| 连接 acquire | RWMutex + map | atomic.Pointer | 无锁快路径 |

## 8. 风险与缓解

### 8.1 优先级饥饿

**风险**：P0 raft 流量爆发（如大批 append entries）时饿死 P1/P2。

**缓解**：批次上限 64 帧。每轮 loop 一定走完 drain(P0) → drain(P1) → drain(P2)，P1/P2 在每轮都有机会发送。极端情况下 P1 延迟升高但不会完全饿死。

### 8.2 Slab 池错误使用

**风险**：handler 持有 frame body 超过 `release()` 调用，导致 slab 复用后数据竞争。

**缓解**：
- Server 端 dispatch 自动在 handler 返回后 `release()`
- handler 如需异步保留数据，**必须 copy**（文档+注释明确标注）
- CI 配置 `-race` 检测

### 8.3 TCP HOL Blocking

**风险**：TCP 丢包重传时所有优先级同时阻塞。

**现实**：这是 TCP 的根本限制，仅 QUIC 能解决，但 QUIC 在内网开销过大。通过 Pool 物理隔离（raft 和 rpc 各自独立连接），至少保证 raft 连接的丢包不会被 RPC 流量加剧。接受此风险。

### 8.4 迁移编译影响面

**风险**：`MessageHandler` 签名变更涉及多处调用点。

**缓解**：Phase 6 集中一步完成，编译器直接报错不会漏改。涉及文件清单见第 10 节。

### 8.5 P0 队满丢弃

**风险**：P0 队满超时丢弃消息，raft 观察到 MsgUnreachable 可能触发不必要的 snapshot。

**缓解**：
- P0 队列容量 2048，正常负载不会满
- 超时 10ms 足够排空几轮 writev
- 记录 metric + 日志，便于排查

## 9. 不在本次范围

- TLS / mTLS
- 消息压缩（lz4/snappy）
- Heartbeat 合并（多 slot heartbeat 合并为一帧） — 需 multiraft 层配合
- QUIC
- 动态调整 Pool 大小
- Metric / tracing 埋点（保留扩展接口，实现另开 PR）
- 自动降级（mux → 直连 fallback）

## 10. 分阶段实施

### Phase 1: 基础设施

| 文件 | 操作 | 内容 |
|------|------|------|
| `frame.go` | 新增 | slabPool + readFrame + writeFrame |
| `pending.go` | 新增 | 分片 pendingMap |
| `frame_test.go` | 新增 | 编解码 + slab 命中率 + 边界 case |
| `pending_test.go` | 新增 | 并发 Store/Delete/Range |

**验收**：`go test -bench BenchmarkReadFrame` alloc/op ≤ 1

### Phase 2: Priority Writer

| 文件 | 操作 | 内容 |
|------|------|------|
| `writer.go` | 新增 | priorityWriter + 调度逻辑 |
| `writer_test.go` | 新增 | 优先级验证 + 背压 + 并发 |

**验收**：P0 消息在 P2 饱和时 p99 延迟 < 5ms

### Phase 3: MuxConn

| 文件 | 操作 | 内容 |
|------|------|------|
| `conn.go` | 新增 | MuxConn = writer + reader + pending |
| `conn_test.go` | 新增 | 生命周期 + RPC 配对 + 连接断开 |

**验收**：连接断开时所有 pending RPC 在 10ms 内返回错误

### Phase 4: Pool 重写

| 文件 | 操作 | 内容 |
|------|------|------|
| `pool.go` | 重写 | PoolConfig + acquire + Send + RPC |
| `pool_test.go` | 重写 | 适配新 API |

**验收**：`TestPool_Concurrent` 通过，无锁快路径 acquire < 100ns

### Phase 5: Client / Server 适配

| 文件 | 操作 | 内容 |
|------|------|------|
| `client.go` | 重写 | 薄封装委托 Pool |
| `server.go` | 重写 | MuxConn 封装 + dispatcher |
| `types.go` | 修改 | MessageHandler 签名去掉 net.Conn |
| `errors.go` | 修改 | 新增 ErrQueueFull |
| `codec.go` | 删除 | 合并进 frame.go |
| `client_test.go` | 重写 | 适配新 API |
| `server_test.go` | 重写 | 适配新 API |
| `codec_test.go` | 删除 | 合并进 frame_test.go |

**验收**：`go test ./pkg/transport/...` 全通过

### Phase 6: 上游迁移

需要修改的文件：

| 文件 | 变更 |
|------|------|
| `pkg/cluster/cluster.go` | 两个 Pool + PoolConfig + 去掉 Get/Release |
| `pkg/cluster/transport.go` | raftTransport 适配新 Client |
| `pkg/cluster/forward.go` | fwdClient 适配 |
| `pkg/controller/raft/transport.go` | 适配新 Client |
| `pkg/controller/raft/service.go` | NewPool → PoolConfig |
| `pkg/channel/transport/adapter.go` | 适配新 Client |
| 所有 `MessageHandler` 使用处 | 去掉 `net.Conn` 参数 |

**验收**：`go test ./...` 全通过

### Phase 7: 压力测试 + Benchmark

| 文件 | 内容 |
|------|------|
| `transport/stress_test.go` | Raft 流量 + RPC 流量并发，验证 P0 延迟不受影响 |
| `transport/benchmark_test.go` | 新旧对比：Send QPS、RPC RTT、alloc/op |
| `cluster/election_stability_test.go` | 故意让 Forward RPC 慢，验证 raft 不抖 |

**验收**：RaftSendUnderRPCLoad 的 p99 < 5ms

### Phase 8: 清理

- 删除旧 `Pool.Get/Release/Reset` 残留引用
- 更新 `pkg/transport` 的 package doc
- 清理未使用的导出符号

## 11. 文件变更汇总

```
pkg/transport/
├── frame.go           ← 新增（替代 codec.go）
├── frame_test.go      ← 新增（替代 codec_test.go）
├── writer.go          ← 新增
├── writer_test.go     ← 新增
├── conn.go            ← 新增
├── conn_test.go       ← 新增
├── pending.go         ← 新增
├── pending_test.go    ← 新增
├── pool.go            ← 重写
├── pool_test.go       ← 重写
├── client.go          ← 重写
├── client_test.go     ← 重写
├── server.go          ← 重写
├── server_test.go     ← 重写
├── rpcmux.go          ← 不变
├── rpcmux_test.go     ← 不变
├── types.go           ← MessageHandler 签名变更
├── errors.go          ← 新增 ErrQueueFull
├── codec.go           ← 删除
└── codec_test.go      ← 删除
```
