# transportv2 高并发热路径外科手术重构 — 设计

- 日期：2026-06-12
- 范围：`pkg/transportv2/`（及其唯一消费者 `pkg/clusterv2/net/`、指标消费端 `internalv2/app/observability.go`）
- 驱动：性能优先（继续压低传输层 CPU / 延迟），API 可改，外科手术式分多个小 PR
- 风格：每个 PR 独立可验证、可回滚，附 benchmark 前后对比

## 背景

`pkg/transportv2` 是分布式内网网络库，承载 raft / 控制面 / RPC / 批量等多优先级内部流量。
当前分层已成熟清晰：`core`（类型 / `OwnedBuffer` / `ObserverDrain`）→ `wire`（编解码）→
`sched`（带权重 DRR 优先级调度器）→ `rpc`（service / pending / executor）→
`conn`（单连接 actor，1 读 + 1 写 goroutine）→ `peer`（按 node 分槽连接池）→
`server` / `client` 公共 API。

本次重构**不做结构性重写**，只针对高并发热路径上的 CPU / 分配 / 锁开销做精准手术。

### 实测基线（2026-06-12，10 线程）

| Benchmark | ns/op | allocs/op | B/op |
|---|---|---|---|
| `BenchmarkTransportV2RPC` | 29933 | 42 | 2611 |
| `BenchmarkTransportV2RPCParallel` | 7901 | 41 | 3412 |
| `BenchmarkSchedulerMixedPriorityBatch`（队列深 4096，64 帧/批） | 245807 | 15 | 58136 |
| `BenchmarkSchedulerEnqueueNextBatch`（空队列） | 180 | 4 | 512 |
| `BenchmarkWriteFrame1KiB` | 69 | 4 | 120 |
| `BenchmarkReadFrame1KiB` | 98 | 5 | 192 |

压测文档 `docs/development/WKSIM_STRESS_FINDINGS.md`（2026-06-12 条目）独立佐证：
约 1/3 采样 CPU 在 `conn.writeLoop -> writeOutbound -> wire.WriteFrame -> net.Buffers.WriteTo -> writev`。

## 关键事实（决定第一刀不破坏指标）

指标消费端 `internalv2/app/observability.go::transportV2MetricsObserver.ObserveTransport`：

- `scheduler_queue` → `RuntimePressure.SetQueue`（**gauge 赋值**）。调度器现在每出队一帧发一个该事件，
  对 gauge 只有每批最后状态有意义。这是 O(n²) 的根。
- `scheduler_wait` → 只消费 `Duration` 和 `Result`，**忽略** `Items/Bytes/Capacity`。
  当前 `waitEventLocked` 计算的快照字段是纯浪费。
- `snapshotLaneQueueLocked`（`internal/sched/scheduler.go`）遍历整条 lane 累加字节，每次调用 O(n)。
  lane 未维护 per-lane 计数器，补上即可降为 O(1)。

结论：第一刀可同时去掉 O(n²) 与浪费字段，且不丢任何被消费的指标。

## 总原则

1. 每个 PR 独立可验证、可回滚，提交前跑相关 benchmark 给出前后 alloc/ns 对比。
2. 不丢任何**被消费**的观测指标；可以改"如何产生"指标，不能改消费端看到的语义。
3. 公共 API 优先不破坏；需要同步改 `clusterv2/net` 的地方在每个 PR 明确列出。
4. **PR3b 与 PR5 无条件执行**（不设数据门槛），但仍各自单独成 PR、单独验证、单独可回滚。

## PR 拆分

顺序固定：埋点 → 写复用 → RPC 去拷贝/去 goroutine → 读/OwnedBuffer → 锁模型。

### PR 1 — 调度器埋点去 O(n²)

**问题**：`nextBatchLocked` 每出队一帧调用 `queueEventLocked → snapshotLaneQueueLocked`
（遍历整条 lane），且每帧生成 `scheduler_queue` + `scheduler_wait` 事件 append 进 slice。
批大小 B、队列深 N 时是 O(B·N) + 大量事件分配。`SchedulerMixedPriorityBatch` 在 N=4096
时达 245807 ns/批，对比空队列 180 ns，证实该扫描是主因。

**改动**：
1. `lane` 结构体增加 `items int`、`bytes int64`，在 enqueue / dequeue / drain 处增量维护。
   `snapshotLaneQueueLocked` 改为 O(1) 查表。
2. `nextBatchLocked` 不再 per-item 生成 `scheduler_queue`。改为每批、每个被触及的 priority
   各生成一个最终状态事件（gauge 语义下只有最后状态有意义，消费端是 `SetQueue`）。
3. `scheduler_wait` 保留 per-item（带 `Duration`），但只填消费端实际使用的
   `Duration / Result / Priority / SourceID`，去掉被忽略的快照字段计算。

**验证**：`BenchmarkSchedulerMixedPriorityBatch`（预期 ~246µs/批 → 个位数 µs），
`BenchmarkSchedulerEnqueueNextBatch` 不退化；`scheduler_test.go` 全绿。
新增 per-lane 计数器与全局 `queuedItems/queuedBytes` 的一致性不变量断言。

**影响面**：纯 `internal/sched`。公共 API 不变，`clusterv2/net` 不动。
**风险**：低。唯一注意点是计数器在 drain / stop 路径保持一致。

### PR 2 — 写路径每批分配复用

**问题**：`writeOutboundBatch` 每批 `make([]Outbound)` + `make([]wire.Frame)`；
`WriteFrames` 每次 `make(net.Buffers)`。writeLoop 是单 goroutine，这些都可跨批复用。

**改动**：
1. 把 `outbounds`、`frames`、`net.Buffers` 提升为 writeLoop 私有 scratch（`Conn` 字段或
   writeLoop 栈上持有），每批 `[:0]` 重置复用。
2. `wire` 增加借用调用者 buffer 的 `WriteFramesInto(w, buffers *net.Buffers, frames, max)`，
   避免内部再分配；`WriteFrames` 保留为薄封装兼容现有测试。

**验证**：`BenchmarkWriteFrame1KiB` 及新增批量写 benchmark 的 alloc 下降；
`BenchmarkTransportV2RPCParallel` alloc/op 下降；conn 单测全绿（背压、关闭、超时下复用安全）。
**影响面**：`internal/conn` + `wire`。公共 API 不变。
**风险**：低-中。复用 buffer 必须在 `net.Buffers.WriteTo` 返回后才重置（写路径同步，已满足）；
`WriteTo` 消费切片，每次使用前重建 —— 测试覆盖。

### PR 3 — RPC 响应路径去拷贝 + 去 per-request goroutine（无条件执行）

拆为 3a / 3b 两个小 PR，分别验证、分别可回滚。

**问题**：
- `server.dispatchRPCRequest` 每请求 `make(chan rpc.Response, 1)` + `go sendRPCResponse`。
- `handleRPCResponse`、`service.handle`、`EncodeRPCResponse`、`Client.Call` 各有一次 payload `append` 拷贝。

**PR 3a 去拷贝**：响应体在能确定生命周期处改为转移 `OwnedBuffer` 所有权而非 `append` 拷贝；
`EncodeRPCResponse` 改用 slab 池获取 buffer 写 status+payload，避免裸 `make`。
保持 `Handler` 的 `[]byte` 返回签名不变（公共 API），只优化 handler 返回值到 wire 的内部搬运。

**PR 3b 去 goroutine**：用 server 级小响应分发池（或复用 `rpc.Executor`）替代 per-request `go`，
`reply chan` 改为池化复用。**无条件执行**，但单独成 PR。

**验证**：`BenchmarkTransportV2RPC` / `RPCParallel` 的 alloc/op（目标砍到 ~20 以下）与 ns/op；
`client_server_test.go` 端到端全绿；race detector 跑通。
**影响面**：`server.go` + `internal/conn` + `internal/rpc`。公共 API 不变。
**风险**：中。所有权转移最易 use-after-release / double-release，靠 `OwnedBuffer` once-release
语义 + race + 现有测试守。3b 改并发模型，故独立 PR。

### PR 4 — 读路径与 OwnedBuffer 分配优化

**问题**：`ReadFrame` 5 allocs；`OwnedBuffer` 每次 `NewOwnedBuffer` 都堆分配一个含
`sync.Once` 的 `ownedState`。RPC 往返中 OwnedBuffer 创建是 alloc 大头之一。

**改动**：
1. `ownedState` 池化或值内联：去掉 `sync.Once`，release 改用 atomic CAS，减少每帧一次的 state 分配。
   `OwnedBuffer` 内部表示改动，**对外行为不变**。
2. 复查 `ReadFrame` 是否还有可去的中间分配。

**验证**：`BenchmarkSlabPoolGetRelease`、`BenchmarkReadFrame1KiB`、端到端 alloc/op；
`core/types_test.go` 的 release 语义测试（double-release、并发 release）在 race 下全绿。
**影响面**：`core`（`OwnedBuffer`）+ `wire`。这是贯穿全库的所有权原语，影响面最大。
**风险**：中。必须保证 double-release 仍幂等、并发 release 安全。

### PR 5 — 调度器锁模型（无条件执行）

**问题**：每连接 `Send`（入队）与 `writeLoop`（出队）共用一把 `sched.Scheduler.mu` + `Cond`。
group fanout 下大量 goroutine 向同一连接 `Send`，该锁是争用点。

**改动**：per-lane MPSC 无锁入队 + 单消费者出队，或 enqueue / dequeue 分离锁。
PR1-4 之后重新压测 + pprof 作为**前后对比依据**（不作为是否执行的门槛）。

**验证**：`mutex` / `block` profile 中 `Scheduler.mu` 争用下降；`BenchmarkTransportV2SendParallelWithBackpressure`
吞吐提升；`scheduler_test.go` 全绿 + 长稳压测验证 DRR 权重、Cond 唤醒、批次子组顺序不回归。
**影响面**：`internal/sched`（可能触及 `internal/conn` 的 enqueue 调用）。
**风险**：高。DRR 正确性、Cond 唤醒、批次顺序（近期刚修过子组顺序）都是雷区。
故放最后、单独 PR、最严格并发测试 + 长稳压测。

## 不做的事（YAGNI）

- 不重新划分包边界、不重写连接 actor 模型。
- 不引入新协议特性（多路复用 / 压缩 / 流式 / 新帧类型）—— 属"扩展性"驱动，与本次"性能优先"目标不符。
- 不动 `peer.Manager` 分槽 / 拨号逻辑 —— 不在热路径分配上。

## 验证策略汇总

- 单元：各 internal 包现有测试 + 新增不变量断言，全程 `-race`。
- 微基准：每个 PR 跑对应 benchmark，记录 alloc/op 与 ns/op 前后对比写入 PR 描述。
- 端到端：`client_server_test.go`、`stress_test.go`。
- 系统级：关键 PR（尤其 PR2 写路径、PR5 锁）后跑三节点压测
  `scripts/bench-wukongimv2-three-nodes-real-qps.sh`，对照 `WKSIM_STRESS_FINDINGS.md` 基线。

## 预期累计效果

- 端到端 RPC 往返 allocs/op：42 → 目标 ~15 以下。
- `SchedulerMixedPriorityBatch`：245807 ns/批 → 目标个位数 µs。
- 三节点压测中 `conn.writeLoop` 系列 CPU 占比下降；调度器锁争用从争用 top 移除。
