# Send Path Performance Optimization Design

## 概述

本设计针对当前三节点真实发送链路在压力测试下吞吐极低的问题，聚焦发送成功前的 durable commit 路径：

- `internal/access/gateway`
- `internal/usecase/message`
- `pkg/storage/channellog`
- `pkg/replication/isr`
- `pkg/replication/isrnode`
- `pkg/replication/isrnodetransport`

本轮目标不是改变“单节点集群”语义，也不是放宽 durable send 的成功条件，而是在保持现有语义的前提下，先移除当前最明显的工程性瓶颈，为后续更大幅度的批量化优化建立正确基线。

## 现状与证据

基于 `internal/app/send_stress_test.go` 的真实压测与 `pprof` 结果，当前发送链路存在以下事实：

1. 默认压力模型下吞吐只有约 `30 QPS`，`p50` 在 `300ms` 量级。
2. 提高 worker 数量后，吞吐没有上升，反而出现大量 `read i/o timeout`。
3. `pprof` 显示热点主要集中在：
   - `pkg/replication/isr.(*replica).Append`
   - `pkg/replication/isr.(*replica).Fetch`
   - `pkg/replication/isr.(*replica).ApplyFetch`
   - `pkg/replication/isrnode.(*runtime).sendEnvelope`
   - Pebble `flushPending / sync`
4. 阻塞热点说明发送主路径大量时间消耗在“等待 ISR 提交完成”，而不是 gateway 编解码或 WKProto 读写。

因此，本轮优化的根因判断为：

- data-plane 并发配置被测试和运行时装配绑死，导致 fetch RPC 并发过低
- `replica` 级别锁范围过大，日志读写与状态更新互相阻塞
- 每条消息都走单条同步写盘和单条复制推进，导致尾延迟极高

## 目标

- 解除 data-plane 连接池大小与 ISR fetch 并发限制之间的隐式耦合
- 缩小 `pkg/replication/isr` 中 `Append`、`Fetch`、`ApplyFetch` 的锁持有范围
- 保持当前 durable send 成功条件不变
- 让 `send_stress_test` 的结果能反映更真实的复制链路能力，而不是被单个硬编码限制提前卡死
- 为下一轮 batch/group-commit 优化提供新的 baseline 和 profile

## 非目标

- 本轮不修改 `SendAck` 语义
- 本轮不降低 `MinISR`
- 本轮不改为 leader-only ack
- 本轮不直接实现 group commit 或 batch replication
- 本轮不重做 gateway 协议层
- 本轮不引入绕过集群语义的“单机特殊路径”

## 方案对比

### 方案 A：只调压测参数

只修改 stress test 的 worker、sender、timeout、pool size，使压测不那么容易超时。

优点：

- 改动最小
- 风险最低

缺点：

- 无法解决 `replica` 锁竞争
- 不能解释高并发下为什么会排队和 timeout
- 只能暂时掩盖瓶颈，不能提升真实上限

### 方案 B：先做并发限制解耦 + ISR 拆锁

新增独立 data-plane 调优参数，拆掉 `PoolSize -> MaxFetchInflightPeer / MaxPendingFetchRPC` 的隐式绑定，同时收缩 `replica` 锁范围。

优点：

- 风险相对可控
- 直接命中当前 `pprof` 最明显的热点
- 不改变 durable 语义
- 能显著改善当前“低吞吐 + 高 timeout”的异常状态

缺点：

- 仍然保留单条同步写盘模型
- 预计无法单独达到 `10000 QPS`

### 方案 C：直接上 batch/group-commit

在 leader 与 follower 侧都增加批量 append、批量 sync、批量复制、批量提交。

优点：

- 最有机会逼近目标吞吐

缺点：

- 语义与实现改动面最大
- 在当前并发与锁问题未清理前，profile 归因会混杂
- 回归风险高，不适合直接作为第一轮

## 推荐方案

选择方案 B。

原因：

1. 当前 profile 已经证明最紧迫的问题是并发限制和锁范围，而不是批量模型本身。
2. 在 durable 语义不变的前提下，B 方案的收益和风险比最好。
3. 做完 B 方案之后，再决定是否进入 batch/group-commit，会有更干净的对比数据。

## 设计

### 1. data-plane 并发配置解耦

当前 `PoolSize` 同时影响：

- `nodetransport.Pool` 连接数
- `isrnode.Limits.MaxFetchInflightPeer`
- `isrnodetransport.Options.MaxPendingFetchRPC`

这会让一个“连接池大小”配置，隐式决定复制链路的并发和背压行为，语义过于混杂。

本轮改为将以下能力独立配置：

- data-plane connection pool size
- max fetch inflight per peer
- max pending fetch RPC per peer

配置原则：

- 默认值保持与现有生产配置接近，不因升级破坏行为
- 测试 harness 可单独覆盖这些值，避免继续被 `PoolSize=1` 锁死
- 默认仍应保守，但必须允许真实发送压测显式放大 data-plane 并发

### 2. `replica.Fetch` 拆锁

当前 `Fetch` 在持有 `r.mu` 的情况下读取日志，这会让：

- follower 拉取记录时阻塞 leader 上的其它状态更新
- append 与 fetch 更容易形成串行化

本轮目标：

- 在锁内读取必要的状态快照、校验 meta、一致性计算
- 在锁外执行 `log.Read`
- 仅在必要时回到锁内提交可变状态

要求：

- 不改变 fetch 返回值语义
- 不改变 HW 推进语义
- 不放松任何 epoch / leader / truncate 校验

### 3. `replica.ApplyFetch` 拆锁

当前 `ApplyFetch` 在持锁状态下执行：

- `log.Append`
- `log.Sync`
- checkpoint 更新

这会放大 follower 写盘对其它请求的阻塞。

本轮目标：

- 将“参数合法性校验 / 当前状态读取”和“状态提交”分离
- 将实际日志写入和同步放到锁外
- 保证锁外写入期间不会破坏当前 epoch / leader / truncation 约束

本轮只接受在不改变外部语义的前提下拆锁；若发现需要大幅重构日志原语，则保留现有语义，留待下一轮设计。

### 4. `replica.Append` 的保守优化

`Append` 当前最大的阻塞来源仍是“等提交完成”本身，本轮不改变它的提交语义。

但可以做两类保守优化：

- 避免在长时间 I/O 操作上持有 `r.mu`
- 尽量减少等待提交前必须完成的锁内工作

如果实现中发现 `Append` 需要批量化才能取得明显收益，则只做最小安全重排，不在本轮引入 group commit。

### 5. 压测与回归验证

本轮必须同时保留两类验证：

- focused 单元测试 / 集成测试：验证新配置与 ISR 语义
- 真实压力测试：验证吞吐、延迟、timeout 情况

验证结果至少要回答：

- timeout 是否明显减少或消失
- QPS 是否明显提升
- mutex/block profile 是否不再以 `replica.Fetch` 和 `replica.ApplyFetch` 为主要热点

## 影响的文件边界

本轮允许修改：

- `internal/app/config.go`
- `internal/app/build.go`
- `internal/app/lifecycle.go`
- `internal/app/multinode_integration_test.go`
- `pkg/replication/isr/append.go`
- `pkg/replication/isr/fetch.go`
- `pkg/replication/isr/replication.go`
- `pkg/replication/isrnode/types.go`
- `pkg/replication/isrnodetransport/adapter.go`
- 相关测试文件

本轮不应修改：

- `internal/access/gateway` 的 send/ack 协议语义
- `internal/usecase/message` 的 durable send 成功定义
- `pkg/storage/channellog` 的 durable append 成功条件

## 测试策略

### 单元测试

- data-plane 新配置默认值与覆盖行为
- `replica.Fetch` 在拆锁后仍然保持正确的记录返回、truncate、HW 推进
- `replica.ApplyFetch` 在拆锁后仍然保持正确的 append、checkpoint、epoch 行为

### 集成测试

- 三节点 harness 下显式放大 data-plane 并发后，真实 send stress 不再被 `PoolSize=1` 卡死
- durable commit 校验仍全部通过
- leader 稳定条件下无额外语义回退

### 性能验证

- 复跑 `internal/app/send_stress_test.go`
- 重新抓 CPU / block / mutex profile
- 以同一套参数对比优化前后：
  - QPS
  - p50 / p95 / p99
  - timeout 数量
  - `replica.Fetch` / `ApplyFetch` / `sendEnvelope` 热点占比

## 风险

- 拆锁后如果状态快照和日志状态不一致，可能引入新的竞态
- 配置解耦后如果默认值选择不当，可能改变已有运行时背压行为
- follower apply 路径如果拆锁不完整，可能出现更隐蔽的 HW 推进问题

对应策略：

- 所有语义变更都必须先写失败测试，再做最小实现
- 保留默认值的保守行为，测试场景使用显式覆盖
- 任何不容易证明正确的批量化思路都不在本轮落地

## 后续决策点

如果本轮完成后仍然显示：

- Pebble sync 仍是主瓶颈
- QPS 仍明显低于目标数量级

则下一轮设计直接进入：

- group commit
- batch replication
- 显式复制确认优化

也就是说，本轮是“把错误的串行化和隐式背压先拿掉”，下一轮才是“把 durable send 做成真正高吞吐模型”。

