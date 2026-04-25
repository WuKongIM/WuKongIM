# pkg/channel 重构方案

> 目标：高性能 + 设计优雅。项目未上线，无需兼容性。

## 术语约定

本文档统一使用以下术语，避免与旧文档混淆：

| 术语 | 含义 |
|------|------|
| **Channel** | 一个 ISR 复制单元（即原 "Group" 概念）。每个 Channel 对应一个 Leader + ISR Followers 维护的独立 log。 |
| **ChannelKey** (`string`) | Channel 的运行时唯一标识，格式 `"channel/{type}/{base64_id}"`。consensus/runtime 层使用。 |
| **ChannelID** (`struct`) | Channel 的业务标识 `{ID string, Type uint8}`。handler 层使用。 |
| **Replica** | Channel 在某个节点上的本地状态机（Leader / Follower / Tombstoned）。 |

> 注意：旧代码中 `log.ChannelKey` 是 `struct{ChannelID, ChannelType}`，而 `isr.ChannelKey` 是 `string`。两者同名不同义正是需要统一的问题之一。重构后 `ChannelKey` 专指 `string` 类型的运行时键；业务层使用 `ChannelID` 结构体。

---

## 一、现状问题分析

### 1.1 包命名问题

| 当前包名 | 问题 |
|---------|------|
| `channel/log` | 与 Go 标准库 `log` 冲突，IDE 中自动补全混淆 |
| `channel/node` | 语义模糊，`node` 可以指网络节点、DOM 节点等 |
| `channel/isr` | 缩写不够直观，新读者需要先理解 ISR 概念 |

### 1.2 类型重复与适配器膨胀

- **`ChannelKey` 同名不同义**：`isr.ChannelKey`（`string`）= 运行时唯一键，`log.ChannelKey`（`struct`）= 业务元组——两个不同概念却同名
- **`ChannelMeta` 两份**：`isr.ChannelMeta` 和 `log.ChannelMeta` 字段有交叉但不相同
- **`ChannelHandle` 两份**：`node/types.go:62` 和 `log/types.go:157`
- **`Runtime` 接口两份**：`node/types.go:119` 和 `log/types.go:153`
- **`log/isr_bridge.go` 171 行**：包含 4 种桥接类型，仅仅是为了适配 `isr.LogStore`/`CheckpointStore`/`EpochHistoryStore`/`SnapshotApplier` 与 `log.Store` 之间的接口差异
- **`log/channelkey.go`**：一个只干类型转换的文件，将 `log.ChannelKey`（struct）格式化为 `isr.ChannelKey`（string）

### 1.3 性能问题

#### A. 锁竞争热点

| 位置 | 问题 |
|------|------|
| `isr/replica.go` `r.mu` | Append 热路径上每次 Lock/Unlock，与 Status() 的 RLock 竞争 |
| `node/channel.go` `g.mu`（`group` 结构） | `metaSnapshot()` 每次 Append 前调用，与 `setMeta()` / `markTask()` 竞争 |
| `node/runtime.go` `r.mu` | 全局 RWMutex 保护 `groups` map，每次 `Channel()` 查询时取写锁（非读锁） |
| `log/send.go` `c.mu` | `metaForKey()` 每次 Append 取 RLock 读取 metas map |

**`runtime.Channel()` 为何用写锁而非读锁**：因为内部调用了 `dropExpiredTombstonesLocked()` 会修改 tombstones map。这使得每次 Channel 查询都需要写锁。

#### B. 内存分配热点

| 位置 | 问题 |
|------|------|
| `isr/append.go:44` | 每次 Append 创建 `appendRequest` + `appendWaiter` + `chan appendCompletion` |
| `isr/append.go:125` | `collectAppendBatch()` 每次 `make([]*appendRequest, count)` |
| `isr/append.go:219` | `buildActiveAppendBatch()` 每次 `make([]Record, 0, recordCount)` 合并 records |
| `isr/progress.go:60` | `nextHWCheckpointLocked()` 每次 `make([]uint64, 0, len(ISR))` + `slices.Sort` |
| `log/log_store.go:55` | 每条 record `append([]byte(nil), payload...)` 拷贝 |
| `log/log_store.go:263` | `readGroupOffsets` 每次 `make([]LogRecord, 0, ...)` |
| `node/runtime.go:100` | `time.NewTimer` / `time.AfterFunc` 频繁创建定时器 |

#### C. Group Commit 可优化

当前 `collectAppendBatch()` 的信号通知使用带缓冲的 channel (`appendSignal chan struct{}, 1`)。当 collector 正在处理前一个 batch 时，新到达的 request 会 enqueue 到 `appendPending` 但 collector 已退出循环——必须等到下一个 request 到达重新触发 `runAppendCollector`。这会导致单 record batch 频率偏高，吞吐下降。

#### D. Scheduler 无优先级

当前 `scheduler` 按 FIFO 处理所有 Channel 的所有任务类型。没有区分紧急任务（如 replication ack）和低优先级任务（如 snapshot），可能导致关键路径延迟。

### 1.4 设计问题

#### A. `runtime.go` 职责过重（578 行）

`node/runtime.go` 同时处理：
- Channel CRUD（EnsureChannel/RemoveChannel/ApplyMeta/Channel）
- 复制协调（processReplication, retryReplication, 7 个 retry 管理方法）
- 调度器驱动（runScheduler, enqueueScheduler, processChannel）
- Peer 请求管理（queuedPeerRequests, releasePeerInflight, drainPeerQueue）
- Follower 重试定时器（scheduleFollowerReplication, fireFollowerReplicationRetry）

#### B. `group` 结构过度使用回调

`node/channel.go:24-28` 定义了 5 个回调函数（`onControl`, `onReplication`, `onCommit`, `onLease`, `onSnapshot`），这些回调闭包捕获了 runtime 的方法，形成隐式的双向依赖。同时 `group` 这个命名也不再准确——它其实就是一个 Channel 的本地表示。

#### C. Tombstone 管理与 Channel 查询耦合

`Channel()` 方法内调用 `dropExpiredTombstonesLocked()` 做清理，迫使查询路径使用写锁。

---

## 二、重构后包结构

```
pkg/channel/
├── channel.go              # 对外 API：Cluster interface + New()
├── types.go                # 统一的共享类型（ChannelKey, ChannelID, Meta, NodeID, ...）
├── errors.go               # 统一错误定义
│
├── replica/                # ISR 共识层（原 isr/）
│   ├── replica.go          # 状态机核心
│   ├── append.go           # Group Commit（优化版）
│   ├── fetch.go            # Leader Fetch 服务
│   ├── progress.go         # HW 推进（优化版）
│   ├── replication.go      # Follower 复制应用
│   ├── recovery.go         # 状态恢复
│   ├── meta.go             # 元数据变更
│   ├── history.go          # Epoch 历史
│   └── types.go            # replica 包内部类型
│
├── store/                  # 存储引擎层（从 log/ 提取）
│   ├── engine.go           # Pebble DB 管理（原 db.go）
│   ├── logstore.go         # 消息日志读写（原 log_store.go）
│   ├── checkpoint.go       # Checkpoint 持久化
│   ├── history.go          # Epoch 历史持久化
│   ├── idempotency.go      # 幂等性存储（原 state_store.go）
│   ├── commit.go           # CommitCoordinator（原 commit_coordinator.go + commit_batch.go）
│   ├── snapshot.go         # Snapshot 存储（原 snapshot_store.go）
│   ├── keys.go             # Key 编码（原 store_keys.go）
│   └── codec.go            # Value 编解码（原 store_codec.go）
│
├── runtime/                # 运行时协调层（原 node/，职责拆分）
│   ├── runtime.go          # 核心：Channel CRUD + 调度驱动（精简版）
│   ├── channel.go          # 单 Channel 本地状态（原 node/channel.go 的 group 结构，去回调）
│   ├── replicator.go       # 复制协调（从 runtime.go 提取）
│   ├── scheduler.go        # 优先级调度器（重写）
│   ├── backpressure.go     # 背压 + 流量控制（原 limits.go + batching.go）
│   ├── session.go          # Peer Session 缓存
│   ├── tombstone.go        # Tombstone 管理（独立 goroutine 清理）
│   ├── snapshot.go         # Snapshot 发送限流
│   └── types.go            # 运行时类型定义
│
├── handler/                # 业务逻辑层（从 log/ 提取非存储部分）
│   ├── append.go           # 消息追加（原 log/send.go）
│   ├── fetch.go            # 消息查询（原 log/fetch.go）
│   ├── meta.go             # 元数据管理（原 log/meta.go）
│   └── codec.go            # 消息编解码（原 log/codec.go）
│
└── transport/              # 网络传输层（保持）
    ├── transport.go        # Transport 实现（原 adapter.go）
    ├── session.go          # 连接会话管理
    └── codec.go            # 线路编解码
```

### 2.1 包命名改进说明

| 原名 | 新名 | 原因 |
|------|------|------|
| `isr/` | `replica/` | 更 Go 风格，`replica.Replica` 直白，不需要了解 ISR 概念才能理解 |
| `log/` | 拆分为 `store/` + `handler/` | 消除 stdlib 冲突；分离存储引擎和业务逻辑 |
| `node/` | `runtime/` | 语义精确——协调多个 Channel 本地状态的运行时 |

### 2.2 类型统一方案

**根包 `types.go` 定义统一类型**：

```go
package channel

import "time"

// NodeID 标识集群中的一个节点。
type NodeID uint64

// ChannelKey 是 Channel 的运行时唯一标识。
// 格式："channel/{type}/{base64_id}"
// 由 handler 层从 ChannelID 生成后注入到 replica/runtime 层。
type ChannelKey string

// ChannelID 是 Channel 的业务标识。仅 handler 层使用。
type ChannelID struct {
    ID   string
    Type uint8
}

// Role 是 Replica 在某个节点上的角色。
type Role uint8

const (
    RoleFollower Role = iota + 1
    RoleLeader
    RoleFencedLeader
    RoleTombstoned
)

// Status 是 Channel 的生命周期状态。仅 handler 层使用。
type Status uint8

const (
    StatusCreating Status = iota + 1
    StatusActive
    StatusDeleting
    StatusDeleted
)

// Features 是 Channel 的协议特性开关。
type Features struct {
    MessageSeqFormat MessageSeqFormat
}

// Meta 是 Channel 的权威元数据。
// 由控制面生产，应用到 replica/runtime/handler 各层。
// replica 层只读 Key/Epoch/Leader/Replicas/ISR/MinISR/LeaseUntil；
// handler 层额外使用 ID/Status/Features。
type Meta struct {
    Key         ChannelKey
    ID          ChannelID
    Epoch       uint64
    LeaderEpoch uint64
    Leader      NodeID
    Replicas    []NodeID
    ISR         []NodeID
    MinISR      int
    LeaseUntil  time.Time
    Status      Status
    Features    Features
}

// ReplicaState 是 Replica 在本地的运行时状态快照。
type ReplicaState struct {
    Key            ChannelKey
    Role           Role
    Epoch          uint64
    OffsetEpoch    uint64
    Leader         NodeID
    LogStartOffset uint64
    HW             uint64
    LEO            uint64
}

// Record 是一条日志记录。
type Record struct {
    Payload   []byte
    SizeBytes int
}

// Checkpoint 描述一个持久化的 HW 快照。
type Checkpoint struct {
    Epoch          uint64
    LogStartOffset uint64
    HW             uint64
}

// EpochPoint 描述一次 epoch 切换在日志中的位置。
type EpochPoint struct {
    Epoch       uint64
    StartOffset uint64
}

// CommitResult 是一次成功 Append 的结果。
type CommitResult struct {
    BaseOffset   uint64
    NextCommitHW uint64
    RecordCount  int
}
```

**消除重复**：
- 删除 `isr.ChannelKey` / `isr.ChannelMeta` / `isr.Role` / `isr.Record` / `isr.Checkpoint` / `isr.EpochPoint` / `isr.ReplicaState` / `isr.CommitResult`
- 删除 `log.ChannelKey`（struct）/ `log.ChannelMeta` / `log.ChannelStatus` / `log.ChannelFeatures` / `log.MessageSeqFormat`
- 删除 `node.ChannelHandle`（与 `log.ChannelHandle` 合并为根包或 runtime 的同一个接口）
- 删除 `log/channelkey.go`——`ChannelKey` 生成逻辑下放到 `handler` 包，由 `handler.keyFromID(ChannelID) ChannelKey` 提供
- 删除 `log/isr_bridge.go`——`store.ChannelStore` 直接实现 `replica.LogStore` 等接口

---

## 三、性能优化方案

### 3.1 无锁元数据读取（atomic.Pointer）

**目标**：消除 Append/Fetch 热路径上的元数据读锁。

```go
// runtime/channel.go
type channel struct {
    key     channel.ChannelKey
    gen     uint64
    replica replica.Replica
    meta    atomic.Pointer[channel.Meta]  // 替代 sync.Mutex + meta 字段
    // ...
}

func (c *channel) Meta() channel.Meta {
    return *c.meta.Load()  // 无锁读取
}

func (c *channel) setMeta(m channel.Meta) {
    c.meta.Store(&m)  // 原子写入（写者单线程保证）
}
```

**同样适用于**：
- `replica/replica.go` 中的 `state` 字段用 `atomic.Pointer[ReplicaState]` 替代 `r.mu.RLock()` 读取
- `handler` 层的 metas map 用 `sync.Map` 或 COW (Copy-on-Write) map 替代 RWMutex

### 3.2 sync.Pool 对象池化

**目标**：消除 Append 热路径上的高频分配。

```go
// replica/pool.go
var appendWaiterPool = sync.Pool{
    New: func() any {
        return &appendWaiter{
            ch: make(chan appendCompletion, 1),
        }
    },
}

func acquireWaiter() *appendWaiter {
    w := appendWaiterPool.Get().(*appendWaiter)
    w.target = 0
    w.result = channel.CommitResult{}
    return w
}

func releaseWaiter(w *appendWaiter) {
    // drain channel
    select {
    case <-w.ch:
    default:
    }
    appendWaiterPool.Put(w)
}
```

**池化对象清单**：

| 对象 | 当前分配路径 | 预计 GC 减少 |
|------|------------|------------|
| `appendWaiter` | 每次 Append | 高 |
| `appendRequest` | 每次 Append | 高 |
| `[]Record` 合并 slice | 每次 batch flush | 中 |
| `[]uint64` HW 计算 | 每次 advanceHW | 中 |
| `pebble.Batch` | 每次 DB 写入 | 低（Pebble 内部已池化） |

### 3.3 HW 计算优化

**目标**：消除每次 advanceHW 的排序分配。

当前实现每次创建 `[]uint64` slice + `slices.Sort`。ISR 通常 3-5 个节点，优化空间有限但可消除分配：

```go
// replica/progress.go
const maxISRSize = 8  // 足够覆盖所有实际场景

type progressTracker struct {
    matches [maxISRSize]uint64  // 栈分配
    count   int
}

func (t *progressTracker) candidateHW(minISR int) (uint64, bool) {
    if t.count < minISR {
        return 0, false
    }
    // 对 count <= 8 的小数组，插入排序比 slices.Sort 更快且零分配
    buf := t.matches[:t.count]
    insertionSort(buf)
    return buf[t.count-minISR], true
}
```

### 3.4 Group Commit 优化

**问题**：当前 `runAppendCollector()` 在 caller goroutine 同步运行。当 collector 完成一个 batch 后循环回到 `collectAppendBatch()`，如果此时 `appendPending` 为空则退出。下一个到达的 request 需要重新创建 collector 循环。

**方案：持久化 Collector goroutine**

```go
// replica/append.go
func (r *replica) startAppendCollector() {
    go func() {
        for {
            select {
            case <-r.appendSignal:
                for {
                    batch := r.collectAppendBatch()
                    if len(batch) == 0 {
                        break
                    }
                    r.flushAppendBatch(batch)
                }
            case <-r.stopCh:
                return
            }
        }
    }()
}
```

**优势**：
- 消除频繁创建/销毁 collector 循环的开销
- 可以在 batch 间持续等待，batch 利用率更高
- 支持优雅关闭（`stopCh`）

### 3.5 Tombstone 异步清理

**目标**：将 `Channel()` 查询路径从写锁降级为读锁。

```go
// runtime/tombstone.go
type tombstoneManager struct {
    mu         sync.Mutex
    tombstones map[channel.ChannelKey]map[uint64]tombstone
    cleanupCh  chan struct{}
}

// 独立 goroutine 定期清理，而非在查询路径上清理
func (m *tombstoneManager) startCleanup(interval time.Duration, now func() time.Time) {
    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                m.dropExpired(now())
            case <-m.cleanupCh:
                return
            }
        }
    }()
}
```

**效果**：`runtime.Channel()` 改为 `RLock()` 仅查询 channels map。

### 3.6 分片锁（Sharded Lock）替代全局 RWMutex

**目标**：当 Channel 数量很大（百万级）时，减少全局锁争用。

```go
// runtime/runtime.go
const shardCount = 64  // 必须是 2 的幂

type runtime struct {
    shards [shardCount]shard
    // ...
}

type shard struct {
    mu       sync.RWMutex
    channels map[channel.ChannelKey]*channel
}

func (r *runtime) shardFor(key channel.ChannelKey) *shard {
    h := fnv1a(string(key))
    return &r.shards[h&(shardCount-1)]
}

func (r *runtime) Channel(key channel.ChannelKey) (ChannelHandle, bool) {
    s := r.shardFor(key)
    s.mu.RLock()  // 分片读锁
    c, ok := s.channels[key]
    s.mu.RUnlock()
    if !ok {
        return nil, false
    }
    return c, true
}
```

### 3.7 零拷贝读路径

**目标**：Fetch 路径减少内存拷贝。

当前 `log_store.go:306` 每条 record 执行 `append([]byte(nil), iter.Value()...)`。
Pebble 的 `iter.Value()` 在 `iter.Next()` 后失效，所以必须拷贝。

**方案：使用 Arena 分配器**

```go
// store/arena.go
type arenaAllocator struct {
    buf []byte
    off int
}

func (a *arenaAllocator) alloc(n int) []byte {
    if a.off+n > len(a.buf) {
        size := max(len(a.buf)*2, n)
        a.buf = make([]byte, size)
        a.off = 0
    }
    s := a.buf[a.off : a.off+n]
    a.off += n
    return s
}

// scan 时使用 arena，减少 GC 对象数
func (e *Engine) scanOffsets(key channel.ChannelKey, from uint64, limit, maxBytes int, arena *arenaAllocator, visit func(uint64, []byte) error) (int, error) {
    // ...
    for valid := iter.First(); valid && count < limit; valid = iter.Next() {
        val := iter.Value()
        buf := arena.alloc(len(val))
        copy(buf, val)
        if err := visit(offset, buf); err != nil {
            return count, err
        }
        // ...
    }
}
```

**效果**：单次 Fetch 的所有 record 共享一块连续内存，GC 只看到 1 个对象而非 N 个。

### 3.8 Timer 池化

**目标**：减少 replication retry 定时器的频繁创建。

对于简单场景，复用 `time.Timer`：

```go
var timerPool = sync.Pool{
    New: func() any { return time.NewTimer(time.Hour) },
}

func acquireTimer(d time.Duration) *time.Timer {
    t := timerPool.Get().(*time.Timer)
    t.Reset(d)
    return t
}

func releaseTimer(t *time.Timer) {
    if !t.Stop() {
        select {
        case <-t.C:
        default:
        }
    }
    timerPool.Put(t)
}
```

更进一步，对于大量重试 peer 场景可以使用时间轮：

```go
// runtime/timer_wheel.go
type timerWheel struct {
    interval time.Duration
    buckets  [][]timerEntry
    current  int
    mu       sync.Mutex
}

type timerEntry struct {
    key      channel.ChannelKey
    peer     channel.NodeID
    version  uint64
    callback func()
}
```

---

## 四、设计优化方案

### 4.1 消除回调闭包，改用 delegate 接口

**当前**：`node/channel.go` 的 `group` 结构通过 5 个函数字段回调 runtime 方法，闭包捕获了 runtime 引用。

**改为**：定义 `ChannelDelegate` 接口，runtime 实现此接口。

```go
// runtime/channel.go
type ChannelDelegate interface {
    OnReplication(key channel.ChannelKey)
    OnSnapshot(key channel.ChannelKey)
}

type channel struct {
    key      channel.ChannelKey
    gen      uint64
    replica  replica.Replica
    delegate ChannelDelegate

    meta    atomic.Pointer[channel.Meta]
    pending atomic.Uint32  // taskMask，atomic 操作替代 mutex
    // ...
}

func (c *channel) markReplication() {
    for {
        old := c.pending.Load()
        next := old | uint32(taskReplication)
        if c.pending.CompareAndSwap(old, next) {
            break
        }
    }
}
```

### 4.2 优先级调度器

**当前**：所有 Channel 的所有任务走同一个 FIFO channel。

**改为**：使用优先级队列，区分紧急任务和普通任务。

```go
// runtime/scheduler.go
type Priority uint8

const (
    PriorityHigh   Priority = iota  // ProgressAck, 复制完成
    PriorityNormal                  // 常规复制请求
    PriorityLow                     // Snapshot, 清理
)

type schedulerEntry struct {
    key      channel.ChannelKey
    priority Priority
}

type scheduler struct {
    queues [3]schedulerQueue  // 按优先级分离
    ch     chan channel.ChannelKey
    // ...
}

func (s *scheduler) enqueue(key channel.ChannelKey, priority Priority) {
    s.mu.Lock()
    defer s.mu.Unlock()
    // 高优先级任务先出队
}
```

### 4.3 统一错误体系

**当前**：错误分散在 `isr/errors.go`、`log/errors.go`、`node/errors.go`，前缀不统一（`isr:` / `isrnode:` / 无前缀）。

**改为**：根包定义统一的错误类型，前缀统一为 `channel:`。

```go
// errors.go
package channel

import "errors"

// 共识层错误（原 isr/errors.go）
var (
    ErrNotLeader       = errors.New("channel: not leader")
    ErrLeaseExpired    = errors.New("channel: lease expired")
    ErrTombstoned      = errors.New("channel: tombstoned")
    ErrInsufficientISR = errors.New("channel: insufficient ISR")
    ErrCorruptState    = errors.New("channel: corrupt state")
    ErrInvalidMeta     = errors.New("channel: invalid meta")
    ErrInvalidConfig   = errors.New("channel: invalid config")
)

// 运行时错误（原 node/errors.go）
var (
    ErrChannelExists   = errors.New("channel: already exists")
    ErrChannelNotFound = errors.New("channel: not found")
    ErrTooManyChannels = errors.New("channel: limit exceeded")
    ErrBackpressured   = errors.New("channel: backpressured")
)

// 业务层错误（原 log/errors.go）
var (
    ErrStaleMeta               = errors.New("channel: stale metadata")
    ErrIdempotencyConflict     = errors.New("channel: idempotency conflict")
    ErrChannelDeleting         = errors.New("channel: deleting")
    ErrMessageSeqExhausted     = errors.New("channel: message seq exhausted")
    ErrProtocolUpgradeRequired = errors.New("channel: protocol upgrade required")
    ErrInvalidArgument         = errors.New("channel: invalid argument")
)
```

### 4.4 Store 直接实现 Replica 接口

**当前**：`log/isr_bridge.go` 有 4 个桥接类型适配 `isr.LogStore` / `isr.CheckpointStore` / `isr.EpochHistoryStore` / `isr.SnapshotApplier`。

**改为**：`store.ChannelStore` 直接实现所有 replica 包需要的接口。

```go
// store/channel_store.go
type ChannelStore struct {
    engine *Engine
    key    channel.ChannelKey
    id     channel.ChannelID
    // ...缓存字段
}

// 直接实现 replica.LogStore
func (s *ChannelStore) LEO() uint64 { /* ... */ }
func (s *ChannelStore) Append(records []channel.Record) (uint64, error) { /* ... */ }
func (s *ChannelStore) Read(from uint64, maxBytes int) ([]channel.Record, error) { /* ... */ }
func (s *ChannelStore) Truncate(to uint64) error { /* ... */ }
func (s *ChannelStore) Sync() error { /* ... */ }

// 直接实现 replica.CheckpointStore
func (s *ChannelStore) LoadCheckpoint() (channel.Checkpoint, error) { /* ... */ }
func (s *ChannelStore) StoreCheckpoint(cp channel.Checkpoint) error { /* ... */ }

// 直接实现 replica.EpochHistoryStore
func (s *ChannelStore) LoadHistory() ([]channel.EpochPoint, error) { /* ... */ }
func (s *ChannelStore) AppendHistory(point channel.EpochPoint) error { /* ... */ }
func (s *ChannelStore) TruncateHistory(leo uint64) error { /* ... */ }

// 直接实现 replica.ApplyFetchStore（带 CommitCoordinator）
func (s *ChannelStore) StoreApplyFetch(req replica.ApplyFetchStoreRequest) (uint64, error) { /* ... */ }
```

**效果**：删除 `isr_bridge.go`（171 行）和 `channelkey.go`（20 行），`ReplicaFactory.New()` 简化为直接传入 `ChannelStore`。

### 4.5 `runtime.go` 职责拆分

将 `runtime.go`（578 行）按职责拆分：

| 新文件 | 职责 | 来源方法 |
|-------|------|---------|
| `runtime.go` | Channel CRUD, 调度驱动 | `New`, `EnsureChannel`, `RemoveChannel`, `ApplyMeta`, `Channel`, `enqueueScheduler`, `runScheduler`, `processChannel` |
| `replicator.go` | 复制协调, 重试管理 | `processReplication`, `retryReplication`, `markReplicationRetry`, `clearReplicationRetry*`, `scheduleFollowerReplication`, `fireFollowerReplicationRetry`, `isReplicationPeerValid`, `drainPeerQueue` |
| `backpressure.go` | 背压管理, Peer 请求限流 | `sendEnvelope`, `queuedPeerRequests`, `releasePeerInflight`, `releaseChannelInflight` + 原 `limits.go` + 原 `batching.go` |

---

## 五、实施计划

### 阶段 1：基础结构（无行为变更）

**目标**：建立新的包结构，迁移代码，保证所有测试通过。

| 步骤 | 操作 | 验证 |
|------|------|------|
| 1.1 | 创建 `channel/types.go`、`channel/errors.go`，定义统一类型（`ChannelKey`、`ChannelID`、`Meta`、`ReplicaState`、`Role`、`Record`、`Checkpoint`、`CommitResult` 等） | 编译通过 |
| 1.2 | 创建 `channel/store/` 包，将 `log/db.go`、`log/log_store.go`、`log/store_keys.go`、`log/store_codec.go`、`log/checkpoint_store.go`、`log/history_store.go`、`log/state_store.go`、`log/snapshot_store.go`、`log/commit_coordinator.go`、`log/commit_batch.go` 迁入，调整 import | 所有 store 测试通过 |
| 1.3 | 创建 `channel/replica/` 包，将 `isr/` 全部迁入，所有 `ChannelKey`/`ChannelMeta`/... 改为引用根包类型 | 所有 replica 测试通过 |
| 1.4 | 创建 `channel/handler/` 包，将 `log/send.go`、`log/fetch.go`、`log/meta.go`、`log/codec.go`、`log/seq_read.go`、`log/apply.go` 迁入，把 `log.ChannelKey`（struct）改为根包 `channel.ChannelID` | 所有 handler 测试通过 |
| 1.5 | 创建 `channel/runtime/` 包，将 `node/` 全部迁入；把 `group` struct 重命名为 `channel`，文件 `channel.go` | 所有 runtime 测试通过 |
| 1.6 | 删除 `log/isr_bridge.go` 和 `log/channelkey.go`，让 `store.ChannelStore` 直接实现 replica 接口，`ChannelKey` 生成逻辑下放到 handler 包 | 删除桥接后测试通过 |
| 1.7 | 删除旧 `isr/`、`log/`、`node/` 包 | 全量测试通过 |
| 1.8 | 更新 `internal/app/build.go`、`internal/app/channelmeta.go`、`internal/usecase/message/`、`internal/usecase/conversation/`、`internal/access/node/` 等外部引用 | 集成测试通过 |

### 阶段 2：性能优化

| 步骤 | 操作 | 验证 |
|------|------|------|
| 2.1 | `runtime/channel.go` 的 `meta` 改为 `atomic.Pointer[channel.Meta]` | benchmark 对比 |
| 2.2 | `runtime.Channel()` 中移除 tombstone 清理，改为独立 goroutine | 查询路径降为 RLock |
| 2.3 | runtime 全局锁改为分片锁 | 高并发 benchmark |
| 2.4 | `appendWaiter`/`appendRequest` 池化 | Append benchmark |
| 2.5 | HW 计算改为栈分配固定数组 + 插入排序 | 无 alloc 验证 |
| 2.6 | Group Commit 改为持久化 collector goroutine | 吞吐量 benchmark |
| 2.7 | Fetch 读路径引入 Arena 分配器 | Fetch benchmark |
| 2.8 | Timer 池化（或时间轮） | 高 Channel 数测试 |

### 阶段 3：设计优化

| 步骤 | 操作 | 验证 |
|------|------|------|
| 3.1 | `runtime/channel.go` 回调改为 `ChannelDelegate` 接口 | 单测 |
| 3.2 | `taskMask` 改为 `atomic.Uint32` CAS 操作 | 无锁验证 |
| 3.3 | `runtime.go` 职责拆分为 `runtime.go` + `replicator.go` + `backpressure.go` | 代码审查 |
| 3.4 | scheduler 改为优先级调度 | 延迟测试 |

---

## 六、风险与注意事项

1. **阶段 1 的 import 变更范围大**：涉及 `internal/app/build.go`、`internal/app/channelmeta.go`、`internal/usecase/message/`、`internal/usecase/conversation/`、`internal/access/node/` 等。建议每步完成后运行全量测试。

2. **`ChannelKey` 语义迁移**：旧 `log.ChannelKey`（struct）被替换为根包 `channel.ChannelID`（struct），旧 `isr.ChannelKey`（string）被替换为根包 `channel.ChannelKey`（string）。外部调用者需要做相应改名。建议用 IDE 批量重命名先处理 import，再逐个解决类型不匹配。

3. **`runtime/channel.go` 中的 struct 命名冲突**：Go 允许 `type channel struct`，但同文件/同包中引用根包类型 `channel.Meta` 时会和 struct 名冲突。解决方案：在 `runtime` 包内给根包起别名，如 `import core "github.com/WuKongIM/WuKongIM/pkg/channel"`，struct 用 `channel`，类型用 `core.Meta`、`core.ChannelKey`。

4. **`atomic.Pointer` 的 ABA 问题**：`Meta` 是值类型结构体，每次 `Store` 都是新指针，不存在 ABA 问题。但需确保 `Meta` 中没有引用共享可变状态（当前没有）。

5. **`sync.Pool` 的回收时机**：`appendWaiter.ch` 是 buffered channel，Pool 回收后 channel 不会被 GC，需确保 `releaseWaiter` 清空 channel。

6. **持久化 collector goroutine 的关闭**：需要在 `Tombstone()` 时通知 collector 退出，否则 goroutine 泄漏。

7. **分片锁的遍历场景**：如果有需要遍历所有 Channel 的操作（目前没有），分片锁会增加复杂度。当前所有操作都是按 key 查询，适合分片。

8. **Arena 分配器的生命周期**：arena 在单次 Fetch 请求内使用，请求完成后随 GC 回收。如果 Fetch 返回的 `[]byte` 被上层长期持有，需要在上层做拷贝。当前架构中消息解码发生在 `handler/fetch.go` 的 `decodeMessage` 中会拷贝到 `Message` 结构体，所以 arena 的生命周期是安全的。

---

## 七、预期效果

| 指标 | 当前 | 预期 |
|------|------|------|
| Append 热路径锁竞争 | 3 次 Mutex（replica.mu + group.mu + runtime.mu） | 1 次 Mutex（replica.appendMu），其余 atomic |
| Append GC 压力 | 每次 3+ 对象分配 | sync.Pool 复用，接近零分配 |
| HW 计算分配 | 每次 make + sort | 栈分配 + 插入排序 |
| Channel 查询锁类型 | 全局写锁 | 分片读锁 |
| `runtime.go` 行数 | 578 行 | ~200 行 |
| 桥接适配器代码 | 171 行（4 个类型） | 0（直接实现） |
| 类型转换胶水 | `log/channelkey.go` 20 行 | 0（合并到 handler） |
| 共享类型定义 | 3 处重复（`isr` / `log` / `node`） | 1 处（根包） |
| 错误定义 | 3 个文件分散，前缀不统一 | 1 个文件，前缀统一为 `channel:` |
| `ChannelKey` 语义 | 2 种（string 和 struct 同名） | 1 种（string，struct 改名 `ChannelID`） |
