# Send Path Group Commit Design

## 概述

第一轮优化已经完成两类保守改动：

- data-plane 并发限制从 `Cluster.PoolSize` 解耦
- `pkg/replication/isr` 中 `Fetch` / `ApplyFetch` / `Append` 的部分锁范围收缩

在同样的三节点 durable send 压测参数下：

- 优化前：约 `30.30 QPS`
- 优化后：约 `48.00 QPS`
- `p50` 从约 `311ms` 降到 `196.618ms`
- `p99` 从约 `916ms` 降到 `398.198ms`

这说明第一轮已经消除了“异常低并发 + 明显锁串行化”的一部分工程性问题，但 durable send 仍远低于目标数量级。fresh pprof 也表明主瓶颈已经进一步集中到 durable append / checkpoint / transport 往返，而不是 gateway 编解码或 fetch 锁竞争。

本设计定义第二轮优化：在不改变 durable send 成功语义、不降低 `MinISR`、不引入绕过集群语义的前提下，为 leader append 路径引入保守的 `group commit`。

## 现状与关键证据

基于 fresh 压测与 fresh pprof，可以确认以下事实：

1. `pkg/storage/channellog.(*Store).appendPayloads`、`(*stateStore).CommitCommittedWithCheckpoint)`、Pebble `Batch.Commit` / `flushPending` / `sync` 已成为主要成本。
2. `pkg/replication/isrnode.(*runtime).sendEnvelope` 与 `pkg/replication/isrnodetransport.(*peerSession).Send` 的占比上升，说明复制 transport 开始成为第二梯队瓶颈。
3. 当前 `pkg/replication/isr.(*replica).Append` 仍然是 durable send 的同步入口，而 `pkg/storage/channellog` 的实际 durable 成本主要发生在 `LogStore.Append()` 内部，而不是 `LogStore.Sync()`：
   - `pkg/storage/channellog.(*Store).appendPayloads` 直接 `batch.Commit(pebble.Sync)`
   - `pkg/storage/channellog.(*Store).sync()` 当前为 no-op
4. 因此，想继续提升吞吐，核心不是继续打磨 `Sync()` 调用点，而是把多个并发 append 请求合并为更少的 durable `Append()` 次数。

## 目标

- 在 leader 侧把多个并发 `Append()` 请求聚合成一次 durable log append
- 继续保持现有 durable send 成功条件：只有 committed 后才返回成功
- 保持现有 offset 分配顺序与 `CommitResult` 语义
- 让低流量场景的额外等待保持很小，可控在 `1ms` 级别
- 为下一轮 batch replication / checkpoint batching 提供更干净的对比基线

## 非目标

- 本轮不修改 `SendAck` 语义
- 本轮不降低 `MinISR`
- 本轮不修改 fetch RPC 协议
- 本轮不实现 follower 侧 batch apply
- 本轮不实现 checkpoint store 的独立批量刷盘器
- 本轮不增加新的 app 级公开配置项；第一版先在 `pkg/replication/isr` 内使用保守默认值

## 方案对比

### 方案 A：只扩大 data-plane / fetch 并发

继续增加 pool、fetch inflight、pending fetch RPC。

优点：

- 改动小
- 风险低

缺点：

- 无法减少 durable `Append()` 次数
- 不能摊薄 Pebble sync / checkpoint 成本
- 继续优化收益会快速递减

### 方案 B：leader 侧 opportunistic group commit

把多个并发 `Append()` 请求在很短窗口内聚合，一次写入，一次 durable commit，再按原有 committed 语义分别返回。

优点：

- 直接命中当前主瓶颈
- 不需要改 fetch / apply 协议
- 风险可控，容易做 focused regression

缺点：

- 仍然保留复制往返和 follower apply 的单批推进模型
- 不能单独保证达到最终目标数量级

### 方案 C：Append + replication + apply 全链路批量

优点：

- 理论上最接近目标吞吐

缺点：

- 语义与实现改动过大
- 很难隔离收益归因
- 第一版回归风险过高

## 推荐方案

选择方案 B。

第一版使用保守参数：

- `max_wait = 1ms`
- `max_records = 64`
- `max_bytes = 64KB`

只在 leader append 路径引入 opportunistic batching，不改复制协议与 follower 语义。

## 设计

### 1. 仅在 `pkg/replication/isr` 内增加 leader append 聚合

`replica.Append(ctx, batch)` 不再总是“每个请求独立写一次”。

改为：

1. 调用先进入 replica 本地 pending queue
2. 第一个进入队列的调用成为当前 batch owner
3. owner 在很短窗口内收集更多 append 请求
4. 满足以下任一条件即 flush：
   - 等待达到 `1ms`
   - 累积记录数达到 `64`
   - 累积字节数达到 `64KB`
5. flush 时将多个请求的记录拼成一个大的 `[]Record`
6. 执行一次 durable `log.Append(merged)`
7. 按请求顺序为每个请求分配自己的 `BaseOffset` / `RecordCount`
8. 复用现有 `advanceHWLocked()` / waiter 通知机制，在 committed 后分别返回

### 2. 不引入常驻 goroutine，每个 batch 使用 owner 驱动

第一版不为每个 replica 增加常驻 batch worker goroutine。

原因：

- channel group 数量可能很多
- 一 goroutine 一 replica 的 steady-state 成本不划算
- owner 驱动模型更容易保持最小改动

具体方式：

- pending queue 由 replica 内部状态维护
- 当前没有 owner 时，新的 `Append()` 调用成为 owner
- owner 负责 collect / flush / 继续接管下一批
- 队列清空后 owner 退出

这样只在真正有 append 流量时做聚合，不增加长期后台负担。

### 3. append barrier 只包住 flush，不包住 collect 窗口

批量收集窗口需要允许其它并发 `Append()` 加入当前 pending queue，所以 collect 期间不能长期占住队列锁。

但真正开始 flush 后，需要防止下面这些控制面状态变更与 durable append 交叉：

- `ApplyMeta`
- `BecomeLeader`
- `BecomeFollower`
- `Tombstone`
- `InstallSnapshot`

因此第一版约束为：

- collect 窗口允许控制面继续运行
- flush 开始后，append barrier 持有到 durable append 与 post-append state publish 完成
- 控制面变更路径也通过同一个 barrier 串行化

如果控制面在 collect 窗口内先发生变化，那么 queued append 会在 flush 前重新校验并失败，而不是错误地落到新角色/新 epoch 上。

### 4. durable 成本落在 `log.Append()`，不是 `log.Sync()`

这轮实现必须按真实 store 行为设计：

- `pkg/storage/channellog.(*Store).appendPayloads` 已经在 `Append()` 内执行 `pebble.Sync`
- `LogStore.Sync()` 目前只是兼容接口，不是主成本来源

因此 flush 的核心收益来自：

- 把 N 个并发请求合成 1 次 `log.Append()`
- 把 N 次 Pebble sync 压缩成 1 次
- 把 N 次 leader 本地 LEO publish / waiter 安装过程压缩成 1 次批次处理

### 5. 语义约束

第一版必须保持这些语义：

- committed 前不能返回成功
- 每个请求得到的 `BaseOffset` 必须与合并后日志顺序一致
- `CommitResult.RecordCount` 仍然是单请求自己的记录数，不是整批大小
- `NextCommitHW` 仍表示该请求成功返回时看到的 committed HW
- `MinISR`、lease、leader/follower 校验保持不放松
- 空 batch 行为保持不变

### 6. context cancel 语义

取消要分三段处理：

1. **进入 pending queue 之前 / 期间取消**
   - 直接从 pending queue 移除
   - 返回 `context.Canceled`

2. **已经进入 flush，但 durable append 尚未发布结果**
   - 请求可能已经被写入日志
   - 仍允许调用方返回 `context.Canceled`
   - 但不能破坏整批其它请求的 durable commit

3. **已经注册 commit waiter 后取消**
   - 与当前语义一致：从 waiter 集合中移除后返回 `context.Canceled`

重点是：取消不能破坏批次里其它请求的 durable 结果，也不能留下泄漏 waiter。

### 7. 默认值放在 `ReplicaConfig` 层，app 暂不开放配置

为了让单元测试可控，同时避免先把 app 配置面做大，第一版方案是：

- 在 `pkg/replication/isr.ReplicaConfig` 增加 group-commit 相关字段
- `NewReplica()` 在零值时使用默认值：`1ms / 64 / 64KB`
- `pkg/storage/channellog.NewReplica()` 暂时不显式传值，直接吃默认值

这样：

- 测试可以精确覆盖阈值与等待窗口
- app / config 层不需要先引入新的公开参数
- 后续如果需要调优，再把这些值向上层配置暴露

## 影响边界

本轮允许修改：

- `pkg/replication/isr/types.go`
- `pkg/replication/isr/replica.go`
- `pkg/replication/isr/append.go`
- `pkg/replication/isr/progress.go`
- `pkg/replication/isr/recovery.go`
- `pkg/replication/isr/*_test.go`
- 如有必要，少量 `pkg/storage/channellog/*test.go` 或 `internal/app/send_stress_test.go` 验证代码

本轮不应修改：

- `internal/usecase/message` 的 durable send 定义
- `internal/access/gateway` 的协议语义
- fetch / apply RPC schema
- follower apply 行为语义

## 测试策略

### focused 单元测试

至少覆盖：

1. 多个并发 append 可以共享一次 durable append / sync
2. batched append 的 `BaseOffset` 顺序正确
3. batched append 仍然只在 committed 后返回成功
4. pre-flush cancel 不会进入 durable batch
5. post-flush cancel 不会破坏其它请求返回
6. lease / role 变化发生在 collect 窗口时，queued append 会在 flush 前失败
7. flush 期间不再把 replica 状态锁压在真实 durable append 兼容路径上

### 现有语义回归

继续要求这些已有语义测试保持通过：

- `AppendWaitsUntilMinISRReplicasAcknowledgeViaFetch`
- `AppendCommitsUsingReturnedBaseOffsetWhenLEOReadLags`
- 第一轮锁范围回归测试

### 集成与性能验证

继续使用：

- `go test ./internal/app ./pkg/replication/isr ./pkg/replication/isrnode ./pkg/replication/isrnodetransport -count=1`
- `WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=30s WK_SEND_STRESS_MESSAGES_PER_WORKER=100000 go test ./internal/app -run '^TestSendStressThreeNode$' -count=1 -v`
- fresh CPU / block / mutex profile

验证重点：

- QPS 是否继续明显高于 `48 QPS`
- `appendPayloads` / `Batch.Commit` 占比是否下降或被更高层批量摊薄
- `replica.Append` 的 block/mutex 热点是否继续下降

## 风险

- collect 窗口中的控制面变更可能让 queued append 在 flush 前批量失败
- 批量 offset 计算出错会破坏 committed 可见性语义
- cancel 竞态容易造成 waiter 泄漏或重复通知
- 如果 batch 边界选择不当，低流量尾延迟可能被不必要拉高

对应策略：

- 所有关键路径先写 focused failing tests
- 第一版窗口严格控制在 `1ms`
- 默认值保守，不先暴露为 app 公共配置
- 不在本轮同时改 replication / apply / checkpoint batching

## 后续决策点

如果这一轮完成后仍然显示：

- Pebble checkpoint / committed-state 写入仍是显著热点
- durable send 仍远低于目标数量级

则下一轮直接进入：

1. checkpoint / committed state batching
2. batch replication / envelope coalescing
3. follower apply batching
