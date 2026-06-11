# channelwrite 重构设计：以 channel 为串行单元

- 日期：2026-06-11
- 范围：`internalv2/runtime/channelwrite` 包内部重写
- 目标：高性能（降低 SEND 写入延迟、提升并发吞吐）+ 高可读性
- 公共契约：`Group.SubmitLocal / Start / Stop / ApplySubscriberMutation` 签名与语义保持不变；`Router`、`message.App`、`internalv2/app/wiring.go` 不需要改

## 背景与问题

SEND 热路径：`gateway.Handler.SendBatch` → `message.App`(薄 facade) → `channelwrite.Router` →
`reactor.enqueue` → `effectScheduler`(ants pool) → `append.AppendBatch` → SENDACK。

当前 `channelwrite` 的并发模型是 `Group` 持有 N 个 `reactor`，channel 按 hash 分到
reactor。每个 reactor 是一个 goroutine 事件循环（`reactor.run()`）加一把全局锁
`r.mu`，串行处理所有 hash 到它的 channel 的 prepare/append/commit 三阶段。

### 已确认的性能瓶颈（按影响排序）

1. **Reactor 单 goroutine 事件循环 + 全局 `r.mu` 是核心串行点。** 所有 mailbox 事件、
   completion 事件、effect 调度都经过单个 goroutine；`r.mu` 保护 `states map`，并在
   `recordPrepareCompletion`、`recordCommitCompletion`、`applyPreparedCompletion`、
   `hasAppendInflight`、`hasCommitInflight` 中反复加锁。`hasAppendInflight` /
   `hasCommitInflight` 还会遍历整个 states map（O(N) 扫描）。串行粒度 = reactor，是吞吐天花板。

2. **`effectWake` 用 close-and-recreate channel 做全局唤醒。** `effectWake.notify()` 每次
   `close(ch)` 再 `make` 新 channel 并持有 `mu`；每个 effect 完成都触发一次，高并发下是全局
   竞争点且反复分配增加 GC 压力。

3. **SEND 热路径逐条内存分配。** `reactorForTarget` 每次 `fnv.New64a()` + `[]byte(key)`；
   `SubmitLocal` 每次 `cloneSendBatchItems` + `newFuture`（2 slice + 1 channel）+
   `ack := make(chan error, 1)`；`prepareBatch` 分配 3 个 slice。`BenchmarkRouterAllItemsContextPlainBatch`
   实测单批 4 allocs / 57KB。全包几乎无 `sync.Pool` 复用。

4. **三阶段 ants pool 的 token + completion 往返。** 一次 SEND 至少 prepare + append 两次跨
   goroutine 往返 + 两次 channel 通信，对低延迟单条发送不利。

### 当前状态的已知问题

`internalv2/runtime/channelwrite/benchmark_test.go` 的 `BenchmarkSubmitLocalHotChannel` 与
`BenchmarkSubmitLocalManyChannelsParallel` 当前失败（"want one successful result"）：bench
fixture（`newBenchmarkChannelWriteGroup`）只注入 `MessageID` 和 `Appender`，prepare 阶段校验拒
绝了 bench send command。这意味着团队目前无法用这两个基准量化 SEND 热路径性能。已记录到
`docs/development/CODE_QUALITY.md`。

## 核心架构：channel 为串行单元

把串行边界从 "reactor" 下放到 "channel"。

```text
Group
  └── shards []*shard              (按 hash 分片，仅用于查找 + 锁条带，数量 = GOMAXPROCS 量级)
        └── channels map[key]*channelWriter

channelWriter (一个 channel 的完整状态机，自包含)
  ├── 串行不变式：同一 channel 的 append 严格有序
  ├── 状态：pending → appending → committing  (取代交织的多套 map)
  ├── 单写者：通过 per-writer 的串行执行保证，不需要跨 channel 锁
  └── 复用共享 worker pool 执行阻塞的 append / post-commit
```

设计要点：

1. **串行边界下放到 channel。** 不同 channel 的 `channelWriter` 完全独立运行，互不持锁。这直接
   消除 reactor 级锁竞争（吞吐），也消除 `hasAppendInflight` / `hasCommitInflight` 的 O(N)
   全 map 扫描（尾延迟）。

2. **每个 `channelWriter` 是一个三阶段小状态机**（pending → appending → committing），用一个清晰
   的状态字段表达，取代当前 `completedPrepare` / `nextDrainSeq` / `pendingAppend` /
   `pendingCommit` / `readyAppendCompletion` 等多套并行 map。这是可读性收益的核心。

3. **Group 的 shard map 仅做两件事**：channel 查找、channel 创建/回收的锁条带。shard 数量远小于
   channel 数量，shard 锁只在"查找/创建 writer"时短暂持有，不覆盖 append/commit 执行。

4. **公共契约不变**：重写范围严格限制在 channelwrite 包内部。

## 并发与执行模型

不给每个 channel 一个常驻 goroutine（百万 channel 会爆）。改用 **per-writer 串行队列 + 共享
worker pool** 的"激活式"模型：

```text
channelWriter 状态机 (单写者不变式靠"同一时刻至多一个 goroutine 在推进它"保证)
  ├── inbox      轻锁的提交入口 (新 SEND batch 入队)
  ├── scheduled  atomic bool: 该 writer 是否已在某个 worker 上排队推进
  └── advance()  单次推进：在持有 writer 自己的锁时，把状态机往前走一步
                 (pending→提交append任务 / append完成→提交commit任务 / ...)
```

执行流：

1. **提交**：`SubmitLocal` 找到/创建 `channelWriter`，把 batch 放进它的 inbox，然后
   `tryActivate()`——用一个 `atomic.CompareAndSwap` 把 writer 标记为 scheduled，若成功就把"推进
   这个 writer"作为一个任务投到共享 worker pool。已 scheduled 则直接返回（说明已有 worker 会处理）。

2. **推进**：worker 取出 writer，调用 `advance()`。`advance` 在 writer 自己的锁内：消费 inbox →
   prepare → 若可 append 则发起 append（阻塞调用走 worker，完成后回调再次 `tryActivate`）→ append
   完成则推进 commit。一次 `advance` 处理完当前可推进的工作后，清除 scheduled 标记；清除后若发现
   inbox 又有新活，重新 `tryActivate`（关闭"丢唤醒"窗口）。

3. **串行保证**：scheduled 标记确保**同一 writer 同一时刻只有一个 worker 在 `advance`**。这就是
   单写者不变式，无需跨 channel 锁，append 顺序天然有序。

相对当前实现的改变：

| 维度 | 当前 | 新模型 |
|------|------|--------|
| 串行单元 | reactor (共享 goroutine + `r.mu`) | channel (独立 writer + CAS 激活) |
| 唤醒机制 | `effectWake` close-and-recreate channel（全局竞争 + 分配） | per-writer `atomic.CompareAndSwap`，无分配 |
| append 顺序 | reactor 内 `nextDrainSeq` map 重排 | writer 内单写者天然有序，删除重排 map |
| 跨 channel 隔离 | 共享锁，互相阻塞 | 完全独立 |
| worker pool | 三个 ants pool (prepare/append/commit) | 保留共享 pool 执行阻塞 IO，调度入口统一为 advance |

**worker pool 取舍**：保留共享 pool（append 是阻塞的 quorum 写，必须从 advance 调用栈解耦，否则一个
慢 channel 占住 worker）。但 prepare 这种纯 CPU、无 IO 的阶段直接在 `advance` 内联执行，省掉一次
worker 往返和一次 completion channel 通信——对单条 SEND 延迟是直接收益。

## 数据结构与内存优化

目标：单条 SEND 热路径堆分配从 4-5 次降到接近 0。

| 分配点 | 当前 | 新模型 |
|--------|------|--------|
| `hashString64` | `fnv.New64a()` + `[]byte(key)` 每次两次分配 | 无分配 FNV-1a（直接按 string 字节迭代，不转 slice） |
| `cloneSendBatchItems` | 每次 `make([]SendBatchItem, n)` + 逐项 Clone | 单条快路径免拷贝；批量用 `sync.Pool` 复用 backing array |
| `newFuture` | 每次 2 个 slice + 1 个 channel | `sync.Pool` 复用 Future，完成后归还 |
| `ack := make(chan error,1)` | 每次 SubmitLocal 分配 | 复用 Future 上的同步原语，删除独立 ack channel |
| effect 结构体 | prepare/append/commit Effect 多次构造 | 合并为 writer 内的就地状态字段，不为每阶段分配独立 effect |

新的核心数据结构：

```go
// channelWriter 是单个 channel 的写状态机（取代分散在 reactor + channelState 的多套 map）
type channelWriter struct {
    target    AuthorityTarget
    key       string

    mu        sync.Mutex      // 仅保护本 writer，无跨 channel 竞争
    scheduled atomic.Bool     // 单写者激活标记
    phase     writerPhase     // pending | appending | committing（显式状态，取代隐式多 map）

    pending   batchQueue      // 待 append 的已 prepare 项（环形 buffer，pool 复用）
    appending *appendInflight // 当前在途 append（至多 appendInflightLimit）
    committed commitQueue     // 待 post-commit 的已提交 envelope

    subscriberCache subscriberCache  // 保留现有语义
}
```

**保留不动的部分**（语义敏感，不在本次重写范围）：

- `subscriberCache` 的版本失效逻辑（`state.go` 现有实现，直接复用）
- `committed` 队列的前缀压缩（`pruneCommittedPrefixIfNeeded`）
- commit 重试 / drop 语义、post-commit 失败分类
- `Recipient` / `RecipientBatch` / delivery worker 入队接口

**删除的复杂度**：`completedPrepare map[string]map[uint64]`、`nextPrepareSeq` / `nextDrainSeq`
双序列号 map、`readyAppendCompletion` + `hasReadyAppendCompletion` 特例、reactor 级的
`pendingPrepare` / `pendingAppend` / `pendingCommit` 三个全局 slice 及其 effect 队列计数器。这些
都是为"reactor 服务多 channel 还要保持 per-channel 有序"而存在的；下放到 channel 后全部消失。

**Future 池化权衡**：`sync.Pool` 复用 Future 要求严格归还时机——Future 必须在 `Wait` 返回且调用方
不再持有结果引用后才能归还。采用 **Future 池化 + Wait 返回拷贝**：`Wait` 返回结果的拷贝（现有
`snapshot()` 已这么做），然后立即归还 Future 本体。净收益为正，因为 Future 本体含 channel 比结果
slice 重得多。

## 可观测性保持

现有 reactor 暴露的压力指标（`ReactorPressureObservation`、`EffectWorkerPressureObservation`）被
三节点压测脚本汇总进 `channelwrite_metrics_summary.tsv`，是性能排查的关键证据。重写保留这些信号，但
语义从 "per-reactor" 迁到新模型：

| 现有指标 | 新模型映射 |
|----------|-----------|
| `MailboxDepth/Capacity` (per reactor) | per-shard 提交队列深度（activate 失败/排队计数） |
| `PendingAppendItems` (per reactor) | 全局聚合 + per-shard：活跃 writer 的 pending 总量（atomic 累加，不扫 map） |
| `AppendInflightItems` | 全局 atomic 计数器，writer 发起/完成 append 时增减 |
| `PostCommitBacklog` | 全局 atomic 计数器 |
| `EffectWorkerPressure` (prepare/append/commit) | 共享 worker pool 的 submit/full/inflight（append/commit 仍走 pool） |
| `LocalAdmissionObservation` | SubmitLocal 入口，语义不变 |
| `AppendObserver` / `PostCommitFailureObservation` | 完全不变 |

**关键原则**：所有压力计数用全局/分片 atomic 增减，绝不为出指标去遍历 channel map（当前
`hasAppendInflight` 扫全 map 的反模式被彻底删除）。`internalv2/app/observability.go` 里的
`deliveryMessageObserver` 适配层签名保持，只改 channelwrite 侧产生数据的方式。

## 可读性整理（限本包内）

1. **文件重组**，按"概念"而非"阶段"组织：
   - `writer.go` — `channelWriter` 状态机（核心，一个文件读懂单 channel 生命周期）
   - `group.go` — Group + shard 查找/创建/回收 + 公共 API
   - `pool.go` — 共享 worker pool + Future/batch 的 sync.Pool
   - `prepare.go` / `append.go` / `commit.go` — 各阶段的纯逻辑函数（无状态，输入→输出，易测）
   - `state.go` — subscriberCache、commit 队列等保留的数据结构
   - `observer.go` / `options.go` / `router.go` / `delivery.go` — 基本不动

2. **评估删除 `group.go` 开头约 90 行类型别名转发**：如果 contract 类型只在包内用，直接用
   `contract.Xxx`；对外导出的少数类型保留别名。目标是减少"这个类型到底定义在哪"的跳转成本。

3. **`advance()` 是唯一的状态推进入口**：读者只需理解一个函数就能掌握 prepare→append→commit 的全部
   流转，取代当前散落在 `run()` / `dispatchPendingEffects()` / `recordXxxCompletion()` /
   `drainCompletedPrepare()` 多处的状态变更。

4. **FLOW.md 更新**：重写完成后更新 `internalv2/FLOW.md` 和 `internalv2/app/FLOW.md` 中描述
   channelwrite reactor 的段落（"multiple channel-hashed authority reactors" → 新的
   channel-writer 模型），保持文档与代码一致（AGENTS.md 要求）。

## 基准建立、迁移策略与测试

### 前置：先修复并建立可信基准

第 0 阶段，在任何重写之前：

1. **修复失败的 benchmark**：`BenchmarkSubmitLocalHotChannel` /
   `BenchmarkSubmitLocalManyChannelsParallel` 当前因 bench fixture 缺字段导致 prepare 校验拒绝。
   先让它们跑通（补全 send command 或 fixture），不改生产代码。
2. **补齐热路径基准**：单条 SEND（hot channel）、多 channel 并行、带 post-commit 的端到端，全部
   `-benchmem`。记录 baseline 到 `docs/development/perf-runs/`。
3. **三节点压测 baseline**：跑 `scripts/bench-wukongimv2-three-nodes-10kch.sh`，保存 before 快照。
4. 这套基准成为重写的 before/after 客观标尺——没有它，"高性能"无法验证。

### 迁移策略

公共契约不变，因此采用包内并行实现 + 测试守护，避免大爆炸式替换：

- 新模型在同包内以新文件实现（`writer.go` 等），`Group` 内部从持有 `[]*reactor` 切换到持有
  `[]*shard`。
- 现有 channelwrite 的全部测试（`*_test.go` 约 5000 行）是回归守护网——append 顺序、背压、commit
  重试、subscriber mutation、stale route 等语义都有覆盖。新模型必须让这些测试全绿且不修改断言（除非
  断言本身耦合了 reactor 内部结构，那种测试改写为针对公共行为）。
- reactor 相关的纯内部测试（直接构造 `newReactor`、断言 `pendingPrepare` 等私有字段）随实现删除/改写。

### 测试策略

1. **TDD 推进**：每个 `channelWriter` 状态转换先写测试（pending→append、append 完成→commit、背压拒
   绝、append 顺序在并发提交下严格有序）。
2. **竞态检测**：`go test -race ./internalv2/runtime/channelwrite/...` 必须干净——单写者不变式正是
   race detector 要验证的核心。
3. **顺序不变式压测**：并发向同一 channel 提交大量 batch，断言 append 到 appender 的顺序与提交序一
   致（新模型最关键的正确性保证）。
4. **回归**：`go test ./internalv2/... ./pkg/...` + 三节点压测 after 快照对比 before。

### 实施分期（每期独立可验证、可提交）

| 期 | 内容 | 验证 |
|----|------|------|
| 0 | 修复 benchmark + 建立 baseline | benchmark 全绿，baseline 快照存档 |
| 1 | 无分配 hash + Future/batch 池化（不改并发模型） | benchmem 显示分配下降，现有测试全绿 |
| 2 | `channelWriter` 状态机 + shard + CAS 激活，替换 reactor | race 干净，顺序不变式测试通过，现有测试全绿 |
| 3 | prepare 内联 + 删除 effectWake/多套 map | benchmark 对比 baseline |
| 4 | 可观测性指标适配 + FLOW.md 更新 | 三节点压测 after vs before，指标语义一致 |

**回滚保障**：每期是独立 commit，任何一期压测显示回退（吞吐/延迟劣于 baseline）就停在该期定位，不盲目
继续。

## 非目标

- 不改 channelwrite 包之外的代码（`Router` / `message.App` / `app/wiring.go` 等的调用方式保持）。
- 不改 append 的 quorum/durable 语义、commit 重试与 drop 语义、subscriber 版本失效语义。
- 不引入 Disruptor / ring-buffer 式单写者（与高可读性目标冲突，已在方案选型中排除）。
- 不做 channelwrite 之外的无关重构。
