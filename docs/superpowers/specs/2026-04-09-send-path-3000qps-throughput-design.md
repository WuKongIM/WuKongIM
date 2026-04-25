# Send Path 3000 QPS Throughput Design

## 概述

本设计面向当前三节点真实发送链路的下一轮吞吐优化，目标是在不改变 durable send 成功条件、不降低 `MinISR`、不引入共享日志分片架构、也不放宽 ack 语义的前提下，把现有真实发送压测提升到 `>= 3000 QPS`。

本轮设计建立在当前已经落地的两类基础优化之上：

- data-plane 并发限制已从 `Cluster.PoolSize` 解耦
- leader 侧已具备保守的 append group commit 能力

但当前链路仍然明显慢于 Kafka 的同类吞吐路径，主因已经从“单点锁竞争”转向“确认链路过长、跨 group 复用不足、节点级 durable 批量化不足、压测驱动模型过于串行”。

## 背景与差异归因

### 当前 WuKongIM 发送链路为什么 QPS 低

当前 durable send 主链路仍然是：

1. gateway 收到发送帧
2. `internal/usecase/message` 进入 durable append
3. `pkg/storage/channellog` 通过每 channel 一个 ISR group 写入日志
4. follower 通过 fetch 拉取并本地 durable apply
5. leader 等待 ISR 进度推进到可提交位置后返回 sendack

从现有代码和压测结果看，QPS 低主要不是 gateway 编解码问题，而是以下几个结构性原因：

1. **ack 关键路径多一个 fetch 往返**
   - leader 目前主要在 `Fetch` 请求到来时更新 follower progress
   - follower 即使已经 durable apply 完本批数据，leader 也往往要等“下一次 fetch”才能感知到该 follower 的新 LEO
   - 结果是每条消息从“写到 follower”到“leader 可提交”之间多了一段额外 RTT

2. **复制 RPC 仍以单 group 为主，节点间不能充分合批**
   - 当前 `pkg/replication/isrnodetransport` 基本按 group 发送 fetch RPC
   - 在 `send_stress` 这种“多 sender + 多个人频道 + 小消息”的模型下，每个 channel group 都比较热，但单个 group 不足以形成很大的批次
   - 于是系统会产生大量“小而散”的 fetch 请求和响应

3. **durable 批量化主要停留在 group 内，还没有提升到 node / DB 级**
   - 当前 append group commit 只能合并“同一个 group 的并发 append”
   - 但 `send_stress` 的热点分布是“很多 group 同时各来一点流量”
   - 对共享 Pebble DB 来说，真正应该摊薄的是“同一节点上多个 group 的 durable commit 成本”

4. **leader / follower 热路径仍存在额外重复工作**
   - committed 后的 checkpoint / idempotency 写入仍会触发额外的记录解码或 reread
   - send 成功返回前仍有一些状态确认和存储往返没有被批量结果复用

5. **压测驱动模型本身过于串行**
   - 当前 `internal/app/send_stress_test.go` 基本是“一个 worker 一条连接，一次发送，一次等 ack”
   - 它更像 latency mode，而不是 Kafka `ProducerPerformance` 那种多 inflight 吞吐驱动
   - 即使后端已经具备一定批量能力，也很难被当前压测形态充分打满

### Kafka 为什么 QPS 高

对比 `learn_project/kafka` 中的发送链路，可以看到 Kafka 的高 QPS 来自一整套“多 inflight + 跨消息批量 + 节点级 durable 摊薄”的组合：

1. **生产者默认是异步多 inflight**
   - 发送线程把消息写入 `RecordAccumulator`
   - 独立 sender 线程按 `batch.size` / `linger.ms` / `acks` 组织请求
   - 单个连接和单个 broker 上可以同时存在多个 inflight produce

2. **broker 侧按 partition / request 批量落盘**
   - produce 请求天然携带成批 records
   - broker append 的单位不是“单消息同步一轮”
   - durable 成本会被一批 records 摊薄

3. **副本确认走 progress 驱动，不额外依赖下一轮业务请求**
   - follower fetch 会成批返回数据
   - leader 端的 high watermark 推进与 delayed produce / purgatory 机制结合
   - producer 成功返回依赖的是副本进度达到条件，而不是“再来一次新的 produce 才知道”

4. **跨 partition / broker 的网络与磁盘复用度高**
   - 多个 partition 的热流量可以同时被 producer / broker / replica fetch 聚合
   - node 级 I/O 和网络开销能够被更多业务消息共享

当前 WuKongIM 和 Kafka 的核心差距，不在“是否也有 append batching”，而在：

- Kafka 的批量化覆盖了 producer、broker、replica fetch、durable commit 全链路
- 当前 WuKongIM 还主要停留在“单 group 内 append 合批”，还没有把确认链路和 node 级 durable commit 一起拉平

## 目标

- 在三节点真实发送压测下达到 `>= 3000 QPS`
- 保持当前 durable send 成功条件不变
- 保持当前 `MinISR` 语义不变
- 保持当前 channel -> ISR group 的路由模型，不引入共享日志分片架构
- 保持当前 sendack 成功定义，不引入新的“弱确认”等级
- 允许为 `send_stress` 新增多 inflight 的 throughput mode，用更真实的方式驱动后端吞吐能力

## 非目标

- 本轮不做“layer 4”类改造：不引入多个 channel 共享一个新日志分片 / 分区架构
- 本轮不做“layer 5”类改造：不新增更弱的 ack level，不允许 leader-only 成功返回
- 本轮不重做 gateway 协议层
- 本轮不引入绕过集群语义的“单机特殊路径”
- 本轮不改变消息顺序、offset 分配和 idempotency 语义

## 方案对比

### 方案 A：继续只做单 group 内优化

继续打磨 `pkg/replication/isr` 内部 append / fetch / apply 的局部实现，不改确认路径，不改 transport 合批，不改 node 级 durable batching。

优点：

- 风险最小
- 改动局部

缺点：

- 难以消除“下一轮 fetch 才能提交”的确认延迟
- 难以利用多 group 并发流量
- 很难把目标从百级 QPS 拉到 3000 级

### 方案 B：throughput mode + progress ack + batched fetch + node-level durable batching

在保持发送语义不变的前提下，同时补齐：

- benchmark 侧多 inflight 吞吐驱动
- follower durable apply 后的显式 progress ack
- 节点间 multi-group batched fetch RPC
- channellog 节点级 durable commit coordinator
- 发送热路径上的额外 reread / 重复写入清理

优点：

- 直接命中当前瓶颈
- 兼容现有 channel/group 模型
- 不放宽 durable 语义
- 最有机会在当前架构下达到 `3000 QPS`

缺点：

- 改动跨 `internal/app`、`pkg/replication/isr*`、`pkg/storage/channellog`
- 需要严密回归，避免批量化破坏 per-message 正确性

### 方案 C：只增加 throughput mode 和 transport batching

先让 benchmark 更激进，再做 batched fetch，暂不处理 progress ack 和 node-level durable batching。

优点：

- 见效快
- 改动比方案 B 小

缺点：

- 仍然保留确认链路额外 RTT
- follower / leader 的 durable sync 仍按 group 分散发生
- 更像“压测提速”，不是从根上补齐链路

## 推荐方案

选择方案 B。

原因：

1. 目标是 `3000 QPS`，只做局部优化大概率不够。
2. 用户已明确允许新增多 inflight throughput mode，但没有允许放宽 durable 语义，因此必须从“确认路径缩短 + 跨 group 合批 + node 级 durable 摊薄”这三个方向同时推进。
3. 该方案仍然保留当前 channel/group 模型和 sendack 语义，风险可控，且收益路径清晰。

## 设计

### 1. `send_stress` 增加多 inflight throughput mode

`internal/app/send_stress_test.go` 当前更像 latency harness，需要增加一个显式 throughput mode 来更真实地驱动后端吞吐。

建议新增：

- `Mode = latency | throughput`
- `MaxInflightPerWorker`

语义：

- `latency`：保持当前“一发一等 ack”的串行模式，继续作为保守基线
- `throughput`：每个 worker 在单连接上维持最多 `N` 个未完成发送，writer 和 ack-reader 解耦

实现原则：

1. 每条消息仍然有独立的 `ClientSeq` / `ClientMsgNo`
2. ack reader 按 `ClientSeq` 或 `ClientMsgNo` 做结果归并
3. 单条消息只有在自己的 sendack 成功后才计入 success
4. 压测结束后仍然逐条校验 owner / follower 存储内容

这一步不是为了“做出更高数字”，而是为了：

- 让后端真实吞吐能力能被打满
- 保留 latency / throughput 两种模式，避免只剩单一压测口径

### 2. follower durable apply 后增加显式 progress ack

当前 leader 主要在 follower 的下一次 `Fetch` 请求到来时感知该 follower 的新进度。这个确认路径太长。

本轮增加显式 progress ack：

1. follower 收到 fetch response
2. `ApplyFetch` 完成 durable apply
3. follower 立即向 leader 发送一个轻量 progress ack
4. leader 基于 ack 中的 match offset 推进该 follower 的 progress
5. 若达到提交条件，则 leader 立即推进 HW 并唤醒等待中的 append waiter

建议新增：

- `pkg/replication/isr/progress_ack.go`
- `pkg/replication/isrnode` 中对应的 envelope / handler

ack 至少包含：

- `GroupKey`
- `Epoch`
- `Generation`
- `ReplicaID`
- `MatchOffset`

正确性要求：

- ack 必须只在 follower durable apply 成功后发送
- leader 只接受当前 epoch / generation 的 ack
- ack 处理必须幂等、单调，不允许旧 ack 回退 progress
- ack 只缩短确认路径，不改变最终成功条件

### 3. 节点间改为 multi-group batched fetch RPC

当前节点间复制 transport 还是以单 group fetch 为主，无法充分利用“同一 follower 正在追多个 group”的天然并发。

本轮在 `pkg/replication/isrnode` 和 `pkg/replication/isrnodetransport` 引入 per-peer batched fetch：

1. 同一 follower 发往同一 leader 的多个 fetch request 先进入 peer session 聚合队列
2. 由 session 在极短窗口内打包成一个 batched fetch RPC
3. leader 在一个 RPC 内处理多个 group 的 fetch
4. 响应按 item 返回，每个 group 独立携带结果或错误
5. follower 收到 batched response 后逐 item 分发到对应 group 的 `ApplyFetch`

实现要点：

- `PeerSession.TryBatch` 不再恒为 `false`
- `Flush` 负责发送 batched fetch
- codec 增加 batch request / response 编解码
- 每个 item 仍然保持独立的 groupKey / epoch / generation / payload

正确性要求：

- batched transport 只合并网络包，不合并 group 语义
- 一个 group 的失败不能污染同批其它 group
- 发生解码错误或单 item stale 时，要按 group 级保守降级重试

### 4. `channellog` 增加 node-level durable commit coordinator

当前共享 Pebble DB 的真正瓶颈已经不是“能不能同 group append 合批”，而是“多个 group 的 committed / apply durable 成本没有在 DB 级合批”。

本轮在 `pkg/storage/channellog` 增加 node-level durable commit coordinator，统一调度同一 DB 上多个 group 的 durable commit。

核心思路：

1. **leader 本地 append 继续保留当前 group 内 append 合批**
   - log records 仍先写入本 group 的本地 log
   - 但 committed 后的 checkpoint / idempotency durable 提交不再每 group 立刻单独 sync

2. **follower apply 也统一走 coordinator**
   - 多个 group 的 fetched records、checkpoint、idempotency mutation 统一进入 coordinator
   - coordinator 在极短窗口内构建一个跨 group 的 Pebble batch，一次 `Sync` 持久化

3. **batch 成功后，再按请求粒度分别回填结果**
   - leader 侧再推进 HW、唤醒 waiters
   - follower 侧再发布新的 LEO/HW，并发送 progress ack

这样可以把：

- N 个 group 的 checkpoint commit
- N 个 group 的 apply fetch durable commit

压缩成更少的 Pebble `Batch.Commit(pebble.Sync)` 次数。

建议新增：

- `pkg/storage/channellog/commit_coordinator.go`
- `pkg/storage/channellog/commit_batch.go`

并调整：

- `pkg/storage/channellog/db.go`
- `pkg/storage/channellog/log_store.go`
- `pkg/storage/channellog/checkpoint_store.go`
- `pkg/storage/channellog/state_store.go`
- `pkg/storage/channellog/apply.go`
- `pkg/storage/channellog/isr_bridge.go`

关键语义约束：

- 只有 shared batch `Sync` 成功后，相关 group 的 committed 状态才可见
- coordinator 不能打乱单个 group 内的 offset / checkpoint 顺序
- 某个 batch 失败时，整批请求都返回失败，不允许局部成功后继续对外可见

### 5. 清理发送热路径上的额外 reread / 重复工作

仅靠“合批”还不够，本轮还要把 commit 前后的重复工作收紧。

优先处理两类热点：

1. **避免 committed 阶段为 idempotency 重复 reread log**
   - 当前 checkpoint / apply 过程中仍有根据 committed offset 再读取 / 再解码记录的路径
   - 新方案中，append / apply 阶段直接产出 `appliedMessage` 元数据，随 commit batch 一起传入 coordinator
   - coordinator 直接用这些元数据构造 idempotency mutation，避免重复扫描

2. **send 成功返回尽量复用 commit 结果**
   - `pkg/storage/channellog/send.go` 中 post-commit 的状态确认尽量复用 commit batch 已知结果
   - 仅在 truly duplicate / stale 需要时再回退到 reread

目标不是去掉所有校验，而是让：

- 正常成功路径尽量不重复读
- 重复写路径尽量一次 commit 解决更多状态落盘

## 模块边界

本轮涉及的主要文件边界如下。

### benchmark / harness

- `internal/app/send_stress_test.go`
- `internal/app/multinode_integration_test.go`

### replication runtime / transport

- `pkg/replication/isr/progress_ack.go`
- `pkg/replication/isr/progress.go`
- `pkg/replication/isr/replication.go`
- `pkg/replication/isrnode/types.go`
- `pkg/replication/isrnode/batching.go`
- `pkg/replication/isrnode/transport.go`
- `pkg/replication/isrnodetransport/codec.go`
- `pkg/replication/isrnodetransport/session.go`

### storage / durable batching

- `pkg/storage/channellog/commit_coordinator.go`
- `pkg/storage/channellog/commit_batch.go`
- `pkg/storage/channellog/db.go`
- `pkg/storage/channellog/log_store.go`
- `pkg/storage/channellog/checkpoint_store.go`
- `pkg/storage/channellog/state_store.go`
- `pkg/storage/channellog/apply.go`
- `pkg/storage/channellog/isr_bridge.go`
- `pkg/storage/channellog/send.go`

## 正确性约束

### sendack 成功条件不变

sendack 成功仍然要求：

- leader 侧 append 已进入本地日志
- 满足当前 `MinISR` 语义
- 对应 commit 已完成 durable 可见

本轮任何优化都只能缩短达到这个条件的时间，不能改变条件本身。

### 每条消息结果独立

即使通过 throughput mode、batched fetch、node-level durable batching 把很多消息并在一起处理：

- 每条消息仍然有自己的成功/失败结果
- 单条消息的 context cancel 或 duplicate 不能污染同批其它消息
- offset / messageSeq 必须保持单调且与最终日志一致

### progress ack 必须幂等且带时代校验

- 旧 epoch / generation 的 ack 必须被忽略
- ack 不能回退 progress
- ack 丢失时系统仍可退回“下一轮 fetch 推进 progress”的保守路径

### batched fetch 只共享 transport，不共享 group 状态

- 一个 RPC 内可包含多个 group
- 但每个 group 的 fetch / apply / retry / stale 处理仍然独立
- batch 只是网络层复用，不改变 ISR 语义边界

### node-level durable batching 不能破坏可见性

- shared Pebble batch 返回前，不得提前对外发布 HW / LEO / idempotency 成功结果
- shared Pebble batch 成功后，才允许逐 group 发布状态
- batch 失败时必须整批保守失败

## 测试策略

### 单元测试

至少覆盖：

1. throughput mode 可以在单连接上维持多 inflight，并且 ack 结果能按消息正确归并
2. progress ack 只会在 durable apply 后发送，且 leader 端按单调规则更新 progress
3. stale / duplicate / old generation ack 不会推进 HW
4. batched fetch codec 可以正确承载多 group 请求与响应
5. batched fetch 中单 item 失败不会影响其它 item
6. commit coordinator 可以把多个 group 的 durable commit 合成一次 Pebble sync
7. commit coordinator 失败时不会提前发布任何 group 的 committed 结果
8. committed 元数据可直接生成 idempotency mutation，避免 reread 仍保持正确

### 集成测试

至少增加三类集成验证：

1. 三节点下 follower apply 后，leader 可在 progress ack 到达后立即唤醒 append waiter，而不是必须等下一轮 fetch
2. batched fetch + progress ack 组合下，多 channel 并发发送仍保持消息顺序、messageSeq、idempotency 正确
3. throughput mode 下的真实发送验证仍可逐条校验 owner / follower 的消息内容

### 性能验收

保留两种压测口径：

- `latency mode`：确认单 inflight 基线没有明显退化
- `throughput mode`：目标达到 `>= 3000 QPS`

验收时至少记录：

- QPS
- p50 / p95 / p99 ack latency
- success / failed 数量
- leader / follower 是否出现异常超时、重复提交、错误截断

## 分阶段落地

### Phase 1：throughput mode 和稳定压测基线

- 给 `send_stress` 增加 `throughput` 模式和 `MaxInflightPerWorker`
- 固化 3 节点、多 sender、多个人频道、小 payload 的验收脚本
- 确保后续每一轮优化都能在相同口径下对比

### Phase 2：显式 progress ack

- 加入 `progress ack` envelope、处理逻辑和回归测试
- 验证 append waiter 不再依赖“下一轮 fetch 才被唤醒”

### Phase 3：multi-group batched fetch transport

- 完成 codec、session、runtime batching 改造
- 让同一 peer 的多个热 group 能共享更少的 fetch RPC

### Phase 4：node-level durable commit coordinator + 热路径清理

- 引入 coordinator
- 把 leader committed 和 follower apply 的 durable commit 汇聚到 DB 级批量提交
- 清理 reread / 重复 decode / 重复状态提交

## 风险与回退

主要风险：

- batched transport 把网络层问题扩大到多个 group
- progress ack 如果时代校验不严，可能错误推进 HW
- node-level durable batching 如果可见性边界处理不当，可能出现“已返回成功但状态未 durable”的严重错误

回退策略：

- progress ack 可以保守退回“仅依赖 fetch 推进 progress”
- batched fetch 可以按 peer 或按 feature flag 退回单 group RPC
- commit coordinator 可以退回每 group 独立 durable commit

所有回退都必须保持当前 durable 语义不变。
