# Channel Lifecycle

## 相关文档

- `send-activation-sequence.md`：发送消息如何激活 channel 的时序图版
- `leader-switch-reconcile.md`：leader 切换后如何切主与收敛

## 一句话说明

WuKongIM 里的 channel 不是启动时全量预热的，而是按需激活的。

当发送消息、复制抓取、reconcile probe 或 long-poll lane open 首次访问某个冷 channel 时，系统会先加载权威 `ChannelRuntimeMeta`，再决定当前节点是否需要创建本地 channel runtime。

## 先分清两个对象

### 1. 权威元数据

权威元数据存放在 `ChannelRuntimeMeta` 中，描述：

- 这个 channel 的 leader 是谁
- replicas / ISR 是谁
- 当前 epoch 是多少
- lease 是否有效
- 当前状态是不是 active / deleting / deleted

它回答的是：这个 channel **应该怎么运行**。

### 2. 本地运行时

本地运行时在 `pkg/channel/runtime` 里，内部包含：

- 本地 channel 对象
- replica 状态机
- 复制调度
- lane / fetch / probe 等运行逻辑

它回答的是：这个节点上的 channel **现在怎么运行**。

所以“激活 channel”本质上是：

1. 先拿到权威元数据
2. 再根据元数据决定当前节点要不要创建本地 runtime

## 生命周期总览

```text
冷状态
  -> 按需激活
  -> 加载 / bootstrap / reconcile 权威 meta
  -> 当前节点需要承载副本
  -> 创建本地 runtime
  -> 进入 leader 或 follower
  -> 复制 / 收敛 / 正常工作
  -> 元数据变化时刷新
  -> 删除或退出时移除
```

## 1. 冷状态

应用启动时不会全量创建所有 channel runtime。

当前实现里，启动阶段只会启动 `channelMetaSync` 的 watcher，用来观察已经激活 channel 所在 slot 的 leader 变化；不会扫描所有 channel 并提前加载本地 runtime。

这意味着大多数 channel 在第一次真正被访问前，都是冷的。

## 2. 哪些入口会激活 channel

当前主要有四类激活来源：

- `business`：业务发送消息
- `fetch`：复制抓取
- `probe`：reconcile probe
- `lane_open`：long-poll lane 打开

也就是说，发送消息会激活 channel，但不是唯一入口。

## 3. 发送消息时的激活流程

发送消息的主路径可以简化成：

```text
message.App.Send
  -> cluster.Append
  -> 如果本地 meta 或 runtime 不可用，返回 ErrStaleMeta / ErrNotLeader / ErrRerouted
  -> RefreshChannelMeta
  -> ActivateByID(business)
  -> 加载或创建权威 ChannelRuntimeMeta
  -> reconcile 当前 meta
  -> ApplyRoutingMeta
  -> 如当前节点属于 replicas，则 EnsureLocalRuntime
  -> 重试 Append
```

这里最重要的一点是：

- 发送消息不是直接 `EnsureChannel`
- 而是先尝试写
- 失败后再触发一次按需刷新和激活

## 4. 首次没有元数据时会发生什么

如果业务发送时发现权威 `ChannelRuntimeMeta` 不存在，系统会走 bootstrap：

- 根据 slot 拓扑计算 replicas
- 计算当前 slot leader
- 设置 ISR 和 MinISR
- 直接写入一条 `StatusActive` 的运行时元数据

所以首次发送一个冷 channel 时，常见情况是：

- 先补一条权威 runtime meta
- 再按这条 meta 创建本地 runtime

## 5. 本地 runtime 什么时候会创建

不是所有节点都会为一个 channel 创建本地 runtime。

只有当当前节点出现在该 channel 的 `replicas` 中时，才会创建本地 runtime；否则只更新 routing 信息，不创建本地副本。

可以简单理解为：

- 在副本集合里：创建或更新本地 runtime
- 不在副本集合里：如果本地之前有 runtime，就移除

## 6. 本地 runtime 创建后会进入什么状态

本地 runtime 创建完成后，会根据元数据进入两种角色之一：

### Leader

- 本地负责 append
- 本地负责推进提交水位
- 本地负责驱动 follower 复制

但 leader 刚切上来时不一定立刻可写。

如果本地还有 provisional tail 没完成 quorum-safe 收敛，`CommitReady` 会是 `false`，这时 append 会返回 `ErrNotReady`。

### Follower

- 本地不接受业务 append
- 本地通过 fetch / long-poll / probe 跟 leader 同步数据

所以一个节点即使因为发送消息“激活”了 channel，也不代表它一定会本地写入；它也可能只是激活出一个 follower runtime，然后把 append 转发给真正的 leader。

## 7. 正常运行阶段

进入正常运行后，channel runtime 会持续处理这些事情：

- leader append
- follower replication
- HW 推进
- checkpoint 持久化
- long-poll lane ready / wakeup
- reconcile probe

其中有一个很关键的点：

leader 本地 append 成功后，会主动唤醒复制路径；对冷 follower，还可能补一个轻量 probe，帮助对端尽快进入 steady-state 复制。

## 8. 冷却流程

这里要先说清一个容易误解的点：

**当前实现里，没有“channel 空闲一段时间后自动冷却并回收本地 runtime”的后台机制。**

所以这里说的“冷却”，更准确地说是：

- 本地 runtime 被卸载
- 当前节点重新回到“这条 channel 在本地是冷的”状态

也就是说，channel 不是因为“最近没人发消息了”就自动变冷，而是因为**当前节点不再需要继续承载这条 channel**，或者**进程退出**，才会退回冷状态。

### 当前代码里，进入“本地冷状态”主要有 3 条路径

#### 1. 副本拓扑变化，当前节点不再属于 replicas

这是最常见的“退冷”路径。

`channelMetaSync` 刷新权威 meta 后，如果发现当前节点已经不在这条 channel 的 `replicas` 里，就不会继续保留本地 runtime，而是走：

```text
applyAuthoritativeMeta
  -> removeLocalRuntime
  -> appChannelCluster.RemoveLocalRuntime
  -> runtime.RemoveChannel
```

这表示：

- routing 信息可以继续更新
- 但本地 replica runtime 会被卸载

卸载后，这条 channel 对当前节点来说就重新变成“冷的”。

#### 2. channel 进入 deleted

当权威状态进入 `deleted` 时，本地 runtime 也会被移除。

这条路径和上面不同的地方是：

- 上面是“当前节点不再承载它”
- 这里是“这条 channel 整体已经被删除”

结果都是一样的：本地 runtime 退场。

#### 3. 进程停止

应用退出时，不会保留本地 runtime。

这时会统一关闭 `channelLog` / `runtime` / `replica`，所以所有本地 channel 最终都会退回冷状态。

### 本地 runtime 被移除时，具体做了什么

本地 runtime 被卸载时，不只是从一个 map 里删掉，还会做一整套清理动作：

- 先把 replica 标记成 `Tombstone`
- 把 channel 从 runtime 的 shard map 中删除
- 清理 follower/leader lane 关联
- 清理 snapshot waiter
- 清理复制重试与 peer queue
- 必要时关闭无效 peer session
- 最后关闭 replica

同时，runtime 还会加一段短期 tombstone 记录。

这段 tombstone 的作用不是“保活”，而是：

- 拦截删除前已经在路上的旧响应
- 防止旧 generation 的包又落回新状态里

### 冷却后还能再激活吗

可以，但要看当前权威 meta。

分两种情况：

- **只是当前节点被卸载了，但以后又重新进入 replicas**  
  那么后续再次访问这条 channel 时，仍然可以重新激活并重建本地 runtime。

- **channel 已经 deleted**  
  那就不会再按原状态重新激活。

### 所以“冷却”要怎么理解

建议把它理解成：

```text
不是空闲自动降温，
而是本地副本 runtime 被明确卸载，
当前节点重新回到 cold miss 状态。
```

## 9. 元数据变化时怎么演化

channel 不是创建完就不变了。

当下面这些信息变化时，channel lifecycle 会继续往前走：

- slot leader 变化
- replicas / ISR 变化
- MinISR 变化
- lease 需要续租

这时系统会刷新权威 meta，再把变化 apply 到本地 runtime。

本地 runtime 在 apply 新 meta 后，可能发生：

- follower -> leader
- leader -> follower
- 保持角色不变，但复制目标变化
- 清理已经失效的 peer session / lane / retry 状态

## 10. 删除阶段

当 channel 状态进入 `deleting` 或 `deleted` 后：

- 业务 append 会被拒绝
- 本地 runtime 会被移除

移除时不会只是简单从 map 里删掉，还会做几件事：

- 把 replica 标记成 tombstone
- 从 runtime 中删除 channel
- 清理复制重试、snapshot waiter、peer queue
- 记录一段短期 tombstone 信息

这段 tombstone 的作用是拦截晚到的旧响应，避免删除后的旧包再次污染新状态。

## 11. 退出阶段

应用停止时：

- `channelMetaSync` 会先停止 watcher
- `channelLog.Close()` 会关闭 channel runtime
- runtime 会关闭所有 replica 和 peer session

所以进程退出时，所有本地 channel runtime 都会被统一释放。

## 12. 当前实现里一个很重要的现实

当前代码里，我没有看到“channel 空闲一段时间后自动回收本地 runtime”的逻辑。

也就是说，一个 channel 一旦被激活并且当前节点仍然属于它的副本集合，它通常会继续留在本地，直到：

- channel 被删除
- 副本集变化导致当前节点不再承载它
- 应用停止

所以它更像“按需加载 + 长驻”，而不是“请求级临时对象”。

## 常见误解

### 误解 1：发送消息会临时创建一个 channel，用完马上销毁

不是。

发送消息触发的是按需激活。激活成功后，本地 runtime 通常会继续保留，而不是这次发送结束就销毁。

### 误解 2：启动时已经把所有 channel 都准备好了

不是。

当前实现是冷启动、按需激活，不是全量预热。

### 误解 3：只有 leader 节点才会有本地 channel runtime

不是。

只要当前节点在该 channel 的 replicas 里，就可能创建本地 runtime；只是 follower 不负责业务写入。

## 推荐记忆方式

可以把 channel 生命周期记成下面这句话：

```text
channel 先有权威 meta，再按需创建本地 runtime；
创建后按 leader / follower 运行；
拓扑变化时持续刷新；
删除或退出时移除。
```

## 关键代码

- 发送入口：`internal/usecase/message/send.go`
- 失败后刷新重试：`internal/usecase/message/retry.go`
- 激活入口：`internal/app/channelmeta_activate.go`
- 首次 bootstrap：`internal/app/channelmeta_bootstrap.go`
- 权威 meta reconcile：`internal/app/channelmeta_lifecycle.go`
- 本地 runtime 创建与移除：`internal/app/channelcluster.go`
- runtime 主体：`pkg/channel/runtime/runtime.go`
- channel 本地状态：`pkg/channel/runtime/channel.go`
- 复制与唤醒：`pkg/channel/runtime/replicator.go`
- long-poll 激活：`pkg/channel/runtime/longpoll.go`
- fetch / probe 激活：`pkg/channel/runtime/backpressure.go`
- replica 角色切换与 reconcile：`pkg/channel/replica/replica.go`、`pkg/channel/replica/reconcile.go`
