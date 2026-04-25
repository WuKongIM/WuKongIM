# UI 复制可观测性改造设计

- 日期：2026-04-21
- 范围：`ui/` 静态管理界面的节点页、槽页、频道页
- 背景文档：
  - `docs/wiki/architecture/README.md`
  - `docs/wiki/architecture/01-controller-layer.md`
  - `docs/wiki/architecture/02-slot-layer.md`
  - `docs/wiki/architecture/03-channel-layer.md`
  - `docs/wiki/architecture/05-message-sending-flow.md`
  - `docs/wiki/architecture/06-slot-data-and-leader-routing.md`
  - `docs/wiki/architecture/07-user-connection-routing.md`

## 1. 目标

当前 UI 已具备节点、槽、频道三类对象的基础管理页，但复制相关内容还停留在较浅层的 `lag / throughput / leader` 展示，无法体现 WuKongIM v3.1 的两套核心复制语义：

- L2 `Slot` 层的 `MultiRaft`
- L3 `Channel` 层的 `ISR`

本次设计目标是让 UI 能支持以下排障路径：

1. 在节点页快速判断问题主要出在 `Slot MultiRaft` 还是 `Channel ISR`
2. 在槽页查看单个 `Slot` 的 Raft 复制进度、成员变更和异常事件
3. 在频道页查看单个 `Channel` 的 ISR 复制状态、提交进度和确认延迟
4. 在三页之间自然下钻，但不混淆 `Raft` 与 `ISR` 两套协议语义

## 2. 设计原则

### 2.1 主视角明确，跨层下钻自然

- 节点页是排障入口，看“节点参与了哪些复制活动，哪里最危险”
- 槽页是 `MultiRaft` 深挖页，看“单个 Slot 的 Raft 日志复制状态”
- 频道页是 `ISR` 深挖页，看“单个 Channel 的消息复制和提交状态”
- 各页之间通过异常对象列表和上下游对象字段形成跳转关系

### 2.2 协议语义分开呈现

不能把以下信息混为一个“统一日志复制面板”：

- Controller 控制面的 Raft 调度
- Slot 层的 `MultiRaft`
- Channel 层的 `ISR`

本次 UI 中：

- `Slot` 页面只主讲 `MultiRaft`
- `Channel` 页面只主讲 `ISR`
- `Node` 页面同时展示两类复制画像，但明确拆成两个区块
- Controller 任务只作为辅助上下文，不作为主复制视图

### 2.3 列表负责比较，抽屉负责解释

- 列表只放适合排序、筛选、对比的字段
- 抽屉才展示复制进度明细、成员状态、最近事件
- 避免把调试信息一次性塞进表格，造成阅读负担

## 3. 页面设计

## 3.1 节点页：复制排障起点

### 页面目标

节点页回答两个问题：

- 这个节点在 `Slot MultiRaft` 里是否存在复制风险
- 这个节点在 `Channel ISR` 里是否存在复制风险

### 页面结构

#### 顶部指标卡

拆成两组语义：

- `Slot 复制画像`
  - Leader Slots
  - Follower Slots
  - 平均 Commit Lag
  - 配置变更中的 Slot 数
- `Channel ISR 画像`
  - Leader Channels
  - Follower Channels
  - 平均 HW Lag
  - MinISR 风险频道数

#### 主表字段

保留节点主表，但增加复制感知字段：

- 节点 ID
- 地址
- 角色
- 状态
- Slot 数
- `Slot Leader / Follower`
- `Slot Max Commit Lag`
- `Channel ISR Risk`
- 连接数
- RPC Latency
- `最近复制异常`
- 操作

说明：

- `Slot Max Commit Lag` 用于快速找出“该节点承载的最危险 Slot”
- `Channel ISR Risk` 是聚合风险标识，例如 `2 shrink · 1 lease risk`
- `最近复制异常` 使用短文本，避免表格过宽

#### 节点抽屉结构

1. `基础信息`
   - 节点摘要、角色、状态、地址
2. `Slot 复制画像`
   - Leader Slots
   - Follower Slots
   - 平均 Commit Lag
   - 最大 Commit Lag
   - Config Change 中的 Slot 数
   - 最慢 Slot 列表
3. `Channel ISR 画像`
   - Leader Channels
   - Follower Channels
   - 平均 HW Lag
   - MinISR 风险频道数
   - Lease 风险频道数
   - Ack 延迟最高频道列表
4. `异常对象`
   - 异常 Slot 列表
   - 异常 Channel 列表
   - 每个对象保留可跳转信息（页面内先显示为链接样式）
5. `最近事件`
   - 复制相关异常或调度动作摘要

### 节点页边界

- 节点页不展示单个副本的 `match / next` 明细
- 节点页不直接展示完整 `ISR members` 明细
- 节点页负责判断方向，不负责做最终复制诊断

## 3.2 槽页：MultiRaft 深挖页

### 页面目标

槽页专门刻画单个 `Slot` 的 `MultiRaft` 复制状态，回答：

- 当前 Leader 是否稳定
- 日志推进是否卡在某个副本
- 是否存在成员变更、Learner 追平、Leader Transfer 等过程

### 顶部指标卡

建议替换或增强为：

- Active Slots
- Degraded Slots
- Leader Transfer Pending
- Learner Catch-up Pending

### 主表字段

- Slot ID
- Leader 节点
- Replica
- 状态
- `Term`
- `Epoch`
- `CommitIndex`
- `AppliedIndex`
- `Max Replica Lag`
- `Config State`
- 操作

说明：

- `Term` 与 `Epoch` 分开显示，避免把 Raft 任期和配置版本混为一个字段
- `Config State` 用于表达 `stable / add-learner / promoting / transfer-leader / removing-old`
- `Max Replica Lag` 用于主表排序和风险定位

### 槽抽屉结构

1. `Raft 概览`
   - Slot ID
   - Leader
   - Term
   - Epoch
   - Voters
   - Learners
   - Config State
2. `日志进度`
   - CommitIndex
   - AppliedIndex
   - LastIndex
   - Snapshot / Checkpoint 点位（采用简化字段）
3. `副本进度`
   - 每个副本一行
   - 角色（Leader / Follower / Learner）
   - Match
   - Next
   - Replay Lag
   - 状态（healthy / catching-up / stalled）
4. `关联热点 Channel`
   - 列出当前 Slot 下受影响或最活跃的频道
5. `最近事件`
   - AddLearner
   - CatchUp
   - PromoteLearner
   - TransferLeader
   - RemoveOld
   - lag spike / no quorum risk 等摘要

### 槽页边界

- 槽页不展示 Channel ISR 的副本确认表
- 槽页只展示与当前 Slot 元数据复制直接相关的内容
- 若出现 Channel 异常，只以“关联热点 Channel”形式引导跳转

## 3.3 频道页：ISR 深挖页

### 页面目标

频道页专门刻画 `Channel ISR`，回答：

- 当前 ISR 是否稳定
- HW 推进是否受某个 follower 拖慢
- 是否因 MinISR、Lease、Ack 延迟产生风险

### 顶部指标卡

增强为：

- Leader Channels
- ISR Shrink Count
- MinISR Risk
- Ack Delay Hotspots

### 主表字段

- Channel ID
- 类型
- Leader 节点
- 所属 Slot
- `Replicas / ISR`
- 订阅数
- `HW`
- `LEO`
- `Ack Lag`
- `MinISR`
- Backlog
- 状态
- 最近活跃
- 操作

说明：

- `Replicas / ISR` 用于一眼看出 ISR 是否收缩
- `HW` / `LEO` 体现提交进度与日志尾部位置
- `Ack Lag` 作为主要排序字段之一

### 频道抽屉结构

1. `ISR 概览`
   - Leader
   - Replicas
   - ISR
   - MinISR
   - LeaderEpoch
   - Lease 状态
2. `日志进度`
   - HW
   - LEO
   - CommittedSeq
   - Backlog
3. `副本确认进度`
   - 每个副本的 Ack / Match / HW Lag
   - 状态（in-sync / delayed / out-of-isr risk）
4. `路由上下文`
   - 所属 Slot
   - 当前 Leader Node
   - 可作为跳转线索
5. `最近事件`
   - ISR shrink / expand
   - leader handoff
   - lease risk
   - follower ack delay
   - retry spike

### 频道页边界

- 频道页不展示 Slot Raft 的 `next` / `learner` / `config change` 细节
- 频道页用 `ISR` 术语，不强行套用 `Raft` 术语

## 4. 跨页关系设计

为了匹配实际排障路径，需要增加轻量的跨页关联：

- 节点抽屉中的异常 Slot → 跳槽页
- 节点抽屉中的异常 Channel → 跳频道页
- 槽抽屉中的关联热点 Channel → 跳频道页
- 频道抽屉中的所属 Slot / Leader Node → 跳槽页 / 节点页

注意：

- 本次可以先使用“链接样式”或文本锚点表达，不要求实现真正跨页面查询参数跳转
- 如果实现成本允许，可沿用当前 `?status=` 一类简单 query 参数模式

## 5. Mock 数据设计要求

当前 UI 基于 `ui/assets/data.js` 的静态数据驱动，本次设计需要同步扩展数据模型，但保持现有页面 key 和文件名稳定。

### 5.1 节点数据新增方向

在 `nodes[].detail` 下补充：

- `slotReplication`
  - leaderSlots
  - followerSlots
  - avgCommitLag
  - maxCommitLag
  - configChanging
  - hotSlots[]
- `channelReplication`
  - leaderChannels
  - followerChannels
  - avgHwLag
  - minIsrRisk
  - leaseRisk
  - hotChannels[]

### 5.2 槽数据新增方向

在 `groups.rows[]`（保留内部 key）下补充：

- term
- epoch
- commitIndex
- appliedIndex
- lastIndex
- configState
- replicasProgress[]
  - node
  - role
  - match
  - next
  - lag
  - status
- hotChannels[]

### 5.3 频道数据新增方向

在 `channels.rows[]` 下补充：

- replicasSummary
- isrSummary
- hw
- leo
- ackLag
- minIsr
- leaseState
- replicaAcks[]
  - node
  - role
  - ack
  - hwLag
  - status
- slot

## 6. 交互约束

- 延续当前 UI 的“主表 + 抽屉”模式，不新增大型二级页面
- 保持 `ui/groups.html` 文件名与 `window.ADMIN_UI_DATA.groups` 等内部命名不变
- 保持当前深色运维面板风格与卡片、表格、抽屉结构一致
- 允许增加新的 section / table / badge，但不要引入全新框架或复杂状态管理

## 7. 实施建议

建议按以下顺序改造：

1. 先扩展 `ui/assets/data.js` 的节点、槽、频道 mock 数据
2. 再调整 `ui/assets/app.js` 的三页渲染结构
3. 最后补充抽屉中的复制细节区块和异常列表
4. 保持其他页面（dashboard / network / topology / connections）只做必要的引用同步，不做额外扩展

## 8. 验收标准

满足以下条件即视为本轮设计落地正确：

1. 节点页能区分 `Slot MultiRaft` 与 `Channel ISR` 两类复制风险
2. 槽页能看到单个 Slot 的 `term / epoch / commit / applied / replica progress / config state`
3. 频道页能看到单个 Channel 的 `ISR / HW / LEO / Ack Lag / MinISR / Lease`
4. 三页之间存在明确的异常对象和关联对象线索
5. UI 文案中不混淆 `Raft` 与 `ISR` 语义
6. 内部实现标识保持稳定，不把本次 UI 设计做成结构性重构

## 9. 非目标

本次不包含：

- 新增独立“复制中心”页面
- 实时拉流或 WebSocket 动态刷新
- 接入真实后端 API
- 改造 Controller 页或单独展示 Controller Raft 明细
- 重命名现有 `groups` 内部代码标识
