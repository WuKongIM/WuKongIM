# Conversation Sync Design

## Overview

为当前 `WuKongIM v3` 主仓设计一套高性能、可扩展的最近会话同步方案，并实现 `/conversation/sync`。方案需要满足以下约束：

- 保持消息发送热路径极简，不在每条消息发送时同步写 per-user conversation 状态
- 会话同步 correctness 不能依赖内存态或单一加速索引
- 兼容旧版会话项字段语义，尤其是 `channel_id`、`channel_type`、`unread`、`last_msg_seq`、`last_client_msg_no`、`readed_to_msg_seq`、`recents`
- 放弃旧版 `page/page_size`，也不引入 `cursor/sync_version`；`sync` 保持单次请求语义，由 `limit` 控制返回上限
- 支持“冷会话突然有新消息”的情况，不允许因此丢失本接口语义内应返回的工作集会话

该设计遵循当前仓库分层约定：

- `internal/access/api` 只做 HTTP 兼容和协议映射
- `internal/usecase/conversation` 负责同步编排
- `internal/app` 负责装配 projector、cache、usecase、API
- 持久化状态落在 `pkg/storage/metadb` / `pkg/storage/metastore`
- 消息事实源继续由 `pkg/storage/channellog` 提供

## Goals

- 提供正式的 `conversation` usecase，而不是继续把逻辑堆在 API handler
- 为 `/conversation/sync` 建立可靠的数据边界和性能边界
- 默认同步只关注用户当前工作集，而不是历史全量会话全集
- 在不污染消息热路径的前提下，解决冷会话重新活跃后的发现问题
- 让未来的 `clearUnread`、`delete`、会话搜索、归档同步都能复用同一数据模型

接口语义：

- `/conversation/sync` 返回“当前工作集快照 + 自上次 `version` 以来的用户态/频道态增量补集”
- 它不是严格 delta stream，也不是历史全量目录分页接口

### Product Acceptance

`/conversation/sync` 的产品定位明确为“最近工作集同步接口”，不是“历史全量会话枚举接口”。

接受的产品限制：

- 当 `version == 0` 且 `last_msg_seqs` 为空时，服务端默认只返回 working set
- 长期冷历史会话如果既不在 working set，也不在客户端显式已知集合中，本次可以不返回
- 当候选会话数超过 `limit` 时，本次只返回排序靠前的窗口；未返回的较旧会话不保证能通过下一次 `version` 增量自动补回
- 这不视为 correctness 错误，而是接口边界定义

如果未来需要首登设备拉全历史目录，必须通过单独的 archive/bootstrap 接口解决，而不是扩展本接口语义。

## Non-Goals

- 本次不实现会话搜索、归档会话浏览、置顶、免打扰、草稿等扩展能力
- 本次不实现旧版 `page/page_size` 兼容
- 本次不实现游标翻页；单次 `sync` 只返回当前窗口内前 `limit` 条会话
- 本次不在消息提交热路径同步写 `metastore` conversation 索引
- 本次不把 `/conversation/sync` 设计成“历史全量会话目录枚举”接口；它是工作集同步接口
- 本次不提供首登设备的冷历史会话补全能力

## Existing Constraints

- 当前主仓还没有正式的 `conversation` usecase
- 当前 `message` 用例在消息提交后已经有异步 committed dispatcher 挂点，可用于 conversation projector
- `channellog` 已能提供 committed seq、最后消息、最近消息窗口
- `metastore` 已具备 authoritative slot 路由、RPC 透传、subscriber 快照能力
- 用户角度的会话同步必须以“用户 owner 节点”为聚合入口
- 单节点部署统一视为“单节点集群”

## Rollout Contract

对于已有历史会话数据的迁移部署，本次要求先完成一次 backfill，再对外开启 `/conversation/sync`：

- `UserConversationState`
  需要从既有会话目录/订阅关系回填基础行、`read_seq`、`deleted_to_seq`
- `active_at`
  对已有“最近会话”可按旧目录最近活跃时间回填；无法判定的历史冷会话允许保持 `0`
- `ChannelUpdateLog`
  不要求回填全历史，只要求在 cutover 后开始正常累积新消息的更新索引

backfill 数据源优先级：

- 既有会话目录
  是 `read_seq`、`deleted_to_seq`、`active_at`、`updated_at` 的优先来源
- 订阅关系 / 单聊关系
  只用于补齐缺失的 `UserConversationState` 行，不覆盖既有会话目录中的进度字段

backfill 冲突与幂等规则：

- 主键冲突按 `(uid, channel_type, channel_id)` upsert
- 若既有会话目录存在，则以它为准写入：
  - `read_seq`
  - `deleted_to_seq`
  - `active_at`
  - `updated_at`
- 若只有订阅关系 / 单聊关系而无既有会话目录，则创建空白 state 行，默认：
  - `read_seq = 0`
  - `deleted_to_seq = 0`
  - `active_at = 0`
  - `updated_at = 0`
- backfill 必须可重复执行；重复执行同一批数据不应放大字段值或生成重复行

迁移期约束：

- 未完成 backfill 的用户，`/conversation/sync` 结果可能不完整，因此不应提前切流
- 新部署且不存在历史数据时，不需要 backfill

## Problem Framing

最近会话同步同时要解决三个不同问题：

1. `catalog correctness`
   用户有哪些会话候选频道，不能丢会话
2. `message facts`
   某个频道当前的最后消息、`last_msg_seq`、`recents`、未读如何计算
3. `working set performance`
   默认 sync 不应每次都扫描用户全部历史目录，也不应扫描全局全部频道更新

如果只依赖 `channellog`，会丢“用户有哪些频道”的目录信息。  
如果只依赖用户目录全量扫描，大用户 sync 成本会失控。  
如果每条消息都写 per-user conversation index，又会破坏消息热路径吞吐。

因此需要把 correctness 和 acceleration 分离：

- 真相层：`UserConversationState + channellog`
- 加速层：`UserConversationState.active_at + ChannelUpdateLog`

## Recommended Approach

采用两类核心数据：

1. `UserConversationState`
   用户全量目录和低频状态真相，同时承载最近会话候选索引列 `active_at`
2. `ChannelUpdateLog`
   频道级更新索引，分为 owner 节点内存 hot index 和 metastore cold index

消息发送热路径只做：

- `channellog.Append`
- 异步提交 committed message 给 projector

不在发送同步链路中写 `metastore` 会话状态。

## Architecture

### 1. API Adapter

`internal/access/api/conversation_sync.go`

职责：

- 解析 `/conversation/sync` 请求
- 兼容旧版会话项字段语义
- 把兼容请求映射为 usecase query
- 输出旧版兼容的数组响应

### 2. Conversation Usecase

`internal/usecase/conversation`

职责：

- 聚合用户目录、最近激活状态、更新索引、消息事实源
- 完成候选集合裁剪
- 计算 unread、recent messages
- 对单聊 internal channel ID 与外部 `channel_id` 做双向映射

### 3. Async Projector

projector 运行在 committed message 异步路径上。

职责：

- 更新 owner 节点内存 hot `ChannelUpdateLog`
- 周期性 flush hot index 到 cold `ChannelUpdateLog`
- 判断频道是否发生 `cold -> hot` 切换
- 对冷会话触发一次异步 `UserConversationState.active_at` 激活 fanout

它是加速器和冷唤醒器，不是 correctness 真相源。

### 4. Storage

#### UserConversationState

按 `uid` authoritative 存储：

- `uid`
- `channel_id`
- `channel_type`
- `read_seq`
- `deleted_to_seq`
- `active_at`
- `updated_at`

时间单位：

- `active_at: int64`
  UnixNano
- `updated_at: int64`
  UnixNano

职责：

- 记录这个用户“知道哪些会话”
- 记录低频状态：已读位置、逻辑删除位置
- 为默认 sync 提供“最近激活会话”候选索引

状态字段更新时机：

- 单聊首次建档
- 群订阅加入 / 移除
- clear unread / set unread / read
- delete conversation

`active_at` 推进时机：

- 用户打开会话
- 用户显式已读 / clear unread
- 冷会话重新活跃
- 单聊首次建档
- 可选的显式兴趣动作（本次不做）

`active_at` 维护边界：

- 仅修改 `active_at` 的维护写，不自动推进 `updated_at`
- `active_at` 只负责 working set 候选维护
- `updated_at` 只反映真实用户状态变更，例如 `read_seq`、`deleted_to_seq`、删除标记

`updated_at` 保持原义：

- 表示这条 `UserConversationState` 记录最近一次状态写入时间
- 由建档、`read_seq` 变更、`deleted_to_seq` 变更、删除标记等真实状态更新推进
- 不参与默认 sync working set 候选读取和排序

`delete conversation` 语义：

- 本次采用逻辑删除，不删除 `UserConversationState` 行
- 删除动作把 `deleted_to_seq` 推进到删除时该会话的最新消息序号
- 删除动作可选地把 `active_at` 清零，使其移出默认 working set
- 后续若有新消息满足 `last_msg_seq > deleted_to_seq`，该会话可以重新出现在 `/conversation/sync`

本次不做“sync 命中后自动续期”。

读取方式：

- 按 `(uid, active_at desc)` 读取最近激活会话
- 默认只取前若干条作为候选，不扫描用户全部历史目录
- `active_at` 只表示“最近一次进入默认 sync 候选集的业务时间”，不承担最终会话排序语义

注意：

- `UserConversationState.updated_at` 保持状态记录本身的更新时间语义，不承担 working set 语义
- 可选的后台冷降级任务只允许把 `active_at` 置为 `0` 或降到更早的业务时间，用于移出默认 working set；不得删除 `UserConversationState` 行
- 冷降级必须异步执行，不得放在 `/conversation/sync` 主查询链路里逐条 join 后同步回写
- `UserConversationState` 仍是 correctness 主表，不能为了 working set 裁剪而删除旧会话行
- 性能边界依赖索引 seek 读取，而不是独立 working set 表的垃圾回收

#### ChannelUpdateLog

频道 owner 侧的更新索引，分两层：

- hot index：内存覆盖表
- cold index：持久化 metastore 表

字段：

- `channel_id`
- `channel_type`
- `updated_at`
- `last_msg_seq`
- `last_client_msg_no`
- `last_msg_at`

时间单位：

- `updated_at: int64`
  UnixNano
- `last_msg_at: int64`
  UnixNano

`updated_at` 语义：

- 表示 owner projector 处理该条 committed message 时生成的频道业务更新时间
- hot index 与 cold flush 必须保存同一个 `updated_at`
- 不能使用 flush 落盘时间覆盖该值

职责：

- 为 `version` 驱动的增量 sync 提供 per-channel 变更判断
- 为默认排序提供轻量索引

不是消息事实源，也不是 correctness 必需唯一来源。

## Data Ownership

- `UserConversationState`
  由 `uid` 所属 owner 节点 authoritative
- `ChannelUpdateLog`
  由 `channel_id` 所属 owner 节点 authoritative
- `channellog`
  仍由频道 owner 提供消息事实

`/conversation/sync` 必须先路由到 `uid owner`，再由 usecase 按需向不同频道 owner 拉取 hot/cold update info 和消息事实。

推荐索引：

- `UserConversationState` 主键：`(uid, channel_type, channel_id)`
- `UserConversationState` 二级索引：`(uid, active_at desc, channel_type, channel_id)`
- `ChannelUpdateLog` 主键：`(channel_type, channel_id)`

## Working Set Strategy

默认 sync 不面向“用户全部历史目录”，而面向工作集。

工作集候选由两类数据组成：

1. `recently activated working set`
   来自 `UserConversationState.active_at`，按 `active_at desc` 读取前若干条
2. `client known channels`
   来自客户端 `last_msg_seqs`

再叠加一类增量候选：

3. `recent channel updates`
   先获取用户 `UserConversationState` 中的 channel 集，再按这些 channel 批量查询 `ChannelUpdateLog`

最终候选集：

`candidate = recently activated working set ∪ client known ∪ recent updates for this user`

工作集读取边界：

- `UserConversationState` 必须基于 `(uid, active_at desc)` 索引读取该用户全部 `active_at > 0` 记录
- 为防止单个用户数据过大拖垮系统，读取必须受 `active_scan_limit` 硬上限保护
- 当 `active_at > 0` 的记录数超过上限时，只保留 `active_at` 最新的前 `active_scan_limit` 条
- `active_scan_limit` 默认值：`2000`
- 这一步是“全量活跃窗口读取”，不是按 `limit` 截断的分页读取

active working set 冷检查：

- 对本次读取到的全部 active rows，必须批量查询对应 `ChannelUpdateLog.last_msg_at`
- 若 `last_msg_at <= now - cold_threshold`，则该会话视为冷会话，不进入本次 active working set 候选
- 对这些冷会话，异步把 `UserConversationState.active_at` 置为 `0`
- 该回写不要求阻塞当前 `sync` 响应，但必须最终完成

增量探测边界：

- 当 `version > 0` 时，不允许直接按时间范围全局扫描 `ChannelUpdateLog`
- 必须先从 `UserConversationState` 获取该用户的 channel 目录
- 再按批次用这些 channel key 查询 `ChannelUpdateLog`
- 批量探测建议使用 `channel_probe_batch_size`
- `channel_probe_batch_size` 默认值：`512`
- 对大用户采用分页扫描 `UserConversationState` 目录 + TopN/heap 保留，避免一次性把全集加载到内存

增量发现规则：

- `version > 0` 的先验候选发现认两类增量：
  - 用户态增量：`UserConversationState.updated_at > version`
  - 频道态增量：`ChannelUpdateLog.updated_at > version`
- `channellog` 不参与增量候选发现，只参与已入选候选的消息事实加载
- 如果某个频道既不在 working set、也不在 client known channels、且其 `ChannelUpdateLog` 已缺失或过冷、同时该会话也没有新的用户态变更，则它不会通过 `version` 路径被发现
- 这种情况下的恢复路径依赖冷唤醒 fanout 把该会话重新推入 `UserConversationState.active_at`

client known overlay 规则：

- `last_msg_seqs` 命中的频道如果不存在对应 `UserConversationState` 行，服务端把它视为“本次请求的一次性 overlay candidate”
- `/conversation/sync` 查询链路本身不因为 `last_msg_seqs` 自动创建或补写 `UserConversationState`
- 缺失 state 行时，默认：
  - `read_seq = 0`
  - `deleted_to_seq = 0`
  - `active_at = 0`
- 该 overlay candidate 仍需经过消息事实读取、删除可见性规则和最终排序
- 如果最终无可见消息，或客户端 `last_msg_seq >= 服务端 last_msg_seq`，则该 overlay candidate 不返回

这样：

- 几个月 / 几年无消息、用户也没显式兴趣的会话，不默认进入 sync
- 冷会话突然活跃，会通过异步更新 `UserConversationState.active_at` 被重新纳入下一次 sync
- 首次新设备或客户端重装后，客户端显式已知频道仍可补集回来

### Bootstrap Contract

当客户端是首次同步，或本地状态丢失时，可能出现：

- `version == 0`
- `last_msg_seqs` 为空

本接口在这种情况下的契约是：

- `/conversation/sync` 默认只返回“服务端工作集”视角下的会话；若客户端提供 `last_msg_seqs`，再补入客户端显式已知频道
- 它不会退化成用户全量历史目录扫描接口
- 冷历史会话如果既不在 working set、也不在最近变化窗口内，则本次不会返回

这不是错误，而是设计选择。原因是：

- 该接口优先服务最近会话首页和增量同步
- 全量历史目录枚举会显著破坏默认 sync 的查询边界
- 这一限制已作为本次产品约束接受

为了保证 brand-new device 仍能拿到有意义的数据，设计依赖三点：

- `UserConversationState.active_at` 是持久化的跨设备最近会话提示，而不是客户端本地缓存
- brand-new direct chat 通过首次建档推进 `active_at`
- 冷会话重新活跃时，通过异步 bump `UserConversationState.active_at` 重新进入候选集合

后续如果需要“浏览全部历史会话目录”，应通过单独的 archive/search 接口解决，而不是扩展本接口语义。

## Cold Wakeup Strategy

当频道 owner projector 处理 committed message 时：

1. 更新 hot `ChannelUpdateLog`
2. 判断该频道是否从冷状态切到热状态

建议冷状态判定：

- `ChannelUpdateLog` 不存在，或其 `last_msg_at <= now - cold_threshold`
- 这条消息是重新活跃后的第一条 committed message

默认值：

- `cold_threshold = 30d`

如果只是普通热频道：

- 不做任何 per-user fanout

如果是 `cold -> hot`：

- 单聊：异步为双方 ensure `UserConversationState` 存在，并把 `active_at` 推进到 `message_time`
- 群聊：异步按 subscriber 快照分页展开成员，再批量推进 `UserConversationState.active_at=message_time`

该 fanout 是 rare path，且只在冷转热发生一次，不在后续连续热消息中重复。

fanout 语义：

- `active_at` upsert 必须是幂等操作，语义为 `active_at = max(existing_active_at, message_time)`
- projector 对该 fanout 采用至少一次投递语义
- 瞬时失败必须异步重试，不得因为一次失败就永久放弃
- 该 fanout 是冷会话重新进入 working set 的正确性补偿路径，不是纯观察性优化

## ChannelUpdateLog Update Strategy

消息发送同步热路径不写 `ChannelUpdateLog` 持久层。

projector 的更新策略：

- committed message 到达 owner 节点后，hot index 覆盖最新 entry
- flush 触发条件：
  - 周期定时，例如 `100ms ~ 500ms`
  - 脏键数量阈值，例如 `1024`
  - 优雅停机 best-effort flush

同一频道连续 100 条消息，只在 flush 时落一次 cold `ChannelUpdateLog`。

查询顺序：

1. 先查 owner 内存 hot index
2. hot 未命中再查 cold `ChannelUpdateLog`
3. 最终消息事实仍以 `channellog` 为准

说明：

- `ChannelUpdateLog` 用于增量候选发现
- `channellog` 用于已入选候选的事实加载与最终响应组装
- `channellog` 不承担大范围增量候选发现职责

### Hot Window Cleanup

`ChannelUpdateLog` 只保留 hot channel，不承担历史全量索引职责。

规则：

- 当 `last_msg_at <= now - cold_threshold` 时，该 entry 视为冷数据
- 冷数据在读取时按未命中处理
- 冷数据由后台 janitor 或 flush 附带清理异步删除，不在消息热路径和 sync 读路径同步删除

因此：

- 当 `version > 0` 时，只对该用户 `UserConversationState` 目录中的 channel 做 `ChannelUpdateLog` 批量探测，并保留两类增量：
  - `UserConversationState.updated_at > version` 的用户态变更
  - `ChannelUpdateLog.updated_at > version` 且仍处于 hot window 的频道态变更
- 当 `version == 0` 时，不做全量 `ChannelUpdateLog` 探测；brand-new sync 默认只依赖 working set 与客户端显式 `last_msg_seqs`

超过该窗口的冷历史会话不会通过 `ChannelUpdateLog` 命中，只能依赖：

- `UserConversationState.active_at`
- 客户端显式 `last_msg_seqs`

## Sync API

### Request

保留旧版主要语义字段，去掉 `page/page_size`、`cursor`、`sync_version`：

- `uid`
- `version`
- `last_msg_seqs`
- `msg_count`
- `only_unread`
- `exclude_channel_types`
- `limit`

说明：

- `version`
  表示客户端已持久化的上一次 sync 下界，建议保存上次响应中所有会话 `version` 的最大值
- `last_msg_seqs`
  保持旧版字符串协议：`channel_id:channel_type:last_msg_seq|channel_id:channel_type:last_msg_seq`
- `last_msg_seqs`
  用于补客户端已知频道集合，并帮助快速跳过未变化频道；空字符串表示客户端无已知频道
- `msg_count`
  表示每个会话 `recents` 的窗口大小；`msg_count <= 0` 时不加载 `recents`
- `limit`
  单次同步返回的最大会话数；推荐默认值 `200`，最大值 `500`

`limit` 截断语义：

- `limit` 只控制本次返回窗口，不提供 `has_more` 或 cursor 补页能力
- 客户端必须把它视为“最近会话窗口大小”，而不是“可完整遍历全部候选”的分页参数
- 若需要更大的最近会话窗口，应显式增大 `limit`
- 若需要完整历史目录浏览，必须走单独的 archive/bootstrap 接口

### Response

响应体直接返回旧版兼容数组：`[]Conversation`

会话项字段保持旧版语义：

- `channel_id`
- `channel_type`
- `unread`
- `timestamp`
- `last_msg_seq`
- `last_client_msg_no`
- `offset_msg_seq`
- `readed_to_msg_seq`
- `version`
- `recents`

`offset_msg_seq` 本次固定返回 `0`。

会话项中的 `version` 定义为：

- 当前会话的 `sync_updated_at`
- 单位为 UnixNano
- 客户端后续请求中的 `version` 应保存上次响应里所有会话 `version` 的最大值

比较契约：

- 请求 `version`、`UserConversationState.active_at`、`UserConversationState.updated_at`、`ChannelUpdateLog.updated_at`、`ChannelUpdateLog.last_msg_at` 全部使用 `UnixNano`
- `MessageResp.timestamp` 与会话项 `timestamp` 保持旧版秒级语义，不直接参与增量比较
- 当需要用 latest message timestamp 参与 `display_updated_at` 计算时，必须执行 `time.Unix(message_timestamp_seconds, 0).UnixNano()`

会话项字段的精确定义：

- `channel_id: string`
  对外频道 ID；单聊场景返回对端 UID，而不是内部 person channel ID
- `channel_type: uint8`
  频道类型，保持旧版枚举语义
- `unread: int`
  基于 `last_msg_seq` 与 `read_seq` 计算后的未读数，并应用“最后一条是自己发送则未读归零”的兼容规则
- `timestamp: int64`
  当前会话最新一条消息的服务器时间戳，单位秒；取自该会话 latest message metadata；若当前会话无可见消息则返回 `0`
- `last_msg_seq: uint32`
  当前会话最新一条消息的序号；与 `timestamp`、`last_client_msg_no` 指向同一条 latest message
- `last_client_msg_no: string`
  当前会话最新一条消息的 `client_msg_no`；若 latest message 不存在则为空字符串
- `offset_msg_seq: int64`
  本次固定返回 `0`
- `readed_to_msg_seq: uint32`
  当前用户在该会话中的已读位置；基础值为 `max(read_seq, deleted_to_seq)`；若 latest message 为本人发送，则兼容返回 `last_msg_seq`
- `version: int64`
  当前会话的 sync 变化标记，等于 `sync_updated_at`，单位 UnixNano；用于客户端汇总出下一次请求的全局 `version`
- `recents: []MessageResp`
  最近消息窗口，最多 `msg_count` 条；按消息新到旧排列，`recents[0]` 必须是 latest message

`recents` 的兼容契约：

- `recents` 中的每个元素必须与旧版 `MessageResp` JSON 结构兼容
- 兼容源文件：`learn_project/WuKongIM/internal/types/message.go`
- v1 实现至少必须保持以下字段名、类型和 JSON 形状不变：
  - `header: {no_persist:int, red_dot:int, sync_once:int}`
  - `setting: uint8`
  - `message_id: int64`
  - `message_idstr: string`
  - `client_msg_no: string`
  - `end?: uint8`
  - `end_reason?: uint8`
  - `error?: string`
  - `stream_data?: []byte`
  - `event_meta?: object`
  - `event_sync_hint?: object`
  - `message_seq: uint64`
  - `from_uid: string`
  - `channel_id: string`
  - `channel_type: uint8`
  - `topic?: string`
  - `expire: uint32`
  - `timestamp: int32`
  - `payload: []byte`
- `event_meta`、`event_sync_hint` 若返回，必须保持与旧版相同的 JSON 字段命名；若当前消息无对应数据，可返回 `null` 或省略

字段关系约束：

- 当 `recents` 非空时，`recents[0]` 必须与 `last_msg_seq`、`last_client_msg_no`、`timestamp` 指向同一条消息
- `timestamp` 用于客户端展示最新消息时间
- `version` 用于客户端增量同步判断，不要求与 `time.Unix(timestamp, 0).UnixNano()` 完全相等
- 纯用户态变更，例如 `read_seq`、`deleted_to_seq`、逻辑删除，会通过 `UserConversationState.updated_at` 推进会话项 `version`

删除可见性规则：

- `deleted_to_seq` 是当前用户在该会话上的逻辑删除截断线
- 若会话 `last_msg_seq <= deleted_to_seq`，该会话在本次 `/conversation/sync` 中视为不可见，应直接过滤
- 若会话 `last_msg_seq > deleted_to_seq`，该会话可见，但 `recents` 只能返回 `message_seq > deleted_to_seq` 的消息
- 未读计算的下界为 `max(read_seq, deleted_to_seq)`
- `only_unread` 过滤基于上述删除裁剪后的未读数执行
- brand-new sync、working set 命中、client known channels 命中时，都必须遵守同一删除可见性规则

`readed_to_msg_seq` 计算规则：

- 基础值：`base_readed_to = max(read_seq, deleted_to_seq)`
- 若 latest visible message 不存在，返回 `base_readed_to`
- 若 latest visible message 的 `from_uid != uid`，返回 `base_readed_to`
- 若 latest visible message 的 `from_uid == uid`，返回 `last_msg_seq`

client known overlay 示例：

- 客户端上报：`last_msg_seqs = "u2:1:88"`
- 服务端不存在 `(uid=u1, channel_id=u2, channel_type=1)` 的 `UserConversationState`
- 服务端查询该频道消息事实，得到：
  - `server_last_msg_seq = 90`
  - latest message timestamp = `1710000000`
- 本次按 overlay 计算：
  - `read_seq = 0`
  - `deleted_to_seq = 0`
  - `unread = 90`
  - `timestamp = 1710000000`
- 若该频道最终进入返回结果，仍不要求 sync 链路立即补写 `UserConversationState`

### Error Contract

延续当前 API 的简单错误风格，参数状态非法时返回：

- HTTP `400`
- 响应体：`{"error":"..."}`

固定错误语义：

- `{"error":"invalid request"}`
  请求体缺字段、字段格式错误或参数组合非法
- `{"error":"invalid last_msg_seqs"}`
  `last_msg_seqs` 不符合旧版字符串协议

## Sync Algorithm

本接口采用单次请求语义，不支持 cursor 续页。

1. 路由到 `uid owner`
2. 解析 `last_msg_seqs`
3. 拉取：
   - `UserConversationState(uid=?, active_at > 0 order by active_at desc limit active_scan_limit)`
   - client known channels
4. 对 active rows 批量查询对应 `ChannelUpdateLog.last_msg_at`
5. 若 `last_msg_at <= now - cold_threshold`：
   - 本次从 active working set 候选中剔除
   - 异步回写 `UserConversationState.active_at = 0`
6. 若 `version > 0`：
   - 分页扫描该用户的 `UserConversationState` channel 目录
   - 按 `channel_probe_batch_size` 批量查询这些 channel 的 hot/cold `ChannelUpdateLog`
   - 若 `UserConversationState.updated_at > version`，直接纳入增量候选
   - 若 `ChannelUpdateLog.updated_at > version` 且仍处于 hot window，纳入增量候选
   - 用 TopN/heap 保留高优先级增量候选，避免全量载入内存
7. 若 `version == 0` 且 `last_msg_seqs` 为空：
   - 不做用户全量目录扫描
   - 仅以 `working set` 作为默认候选来源
8. 合并候选集合并去重
9. 先按 `exclude_channel_types` 过滤候选集合
10. 对过滤后的候选集合读取最小消息事实，至少包括：
   - `read_seq`
   - `last_msg_seq`
   - last message metadata
11. 基于消息事实计算 `unread`
12. 在完成 unread 计算后应用 `only_unread`
13. 对剩余候选按显示排序键排序
14. 截取前 `limit` 个频道
15. 只对最终返回的频道读取：
   - `channellog.Status`
   - last message
   - recent messages
   - 用户目录状态
16. `msg_count` 仅决定每个会话 `recents` 的窗口大小，不影响候选选择和排序
17. 返回旧版兼容数组响应

### Cold Demotion

主路径要求在 `sync` 中对 active working set 做一次 lazy cold demotion。

此外可以实现后台补充 janitor：

- 输入：按批扫描 `active_at > 0` 的用户会话
- 判断：若关联 `ChannelUpdateLog.last_msg_at <= now - cold_threshold`
- 动作：只把 `UserConversationState.active_at` 清零或降级
- 禁止：删除 `UserConversationState` 行，或在 `sync` 主链路里同步执行降级

## Ordering

`UserConversationState.active_at` 仅用于：

- 高性能读取最近激活会话候选集
- 控制默认 sync 的候选读取边界

它不直接决定 `/conversation/sync` 返回结果的最终顺序。

显示排序键固定为：

- `display_updated_at desc`
- `channel_type asc`
- `channel_id asc`

`display_updated_at` 优先级：

1. hot `ChannelUpdateLog.updated_at`
2. cold `ChannelUpdateLog.updated_at`
3. `time.Unix(last_message_timestamp_seconds, 0).UnixNano()`

`sync_updated_at` 定义：

- `sync_updated_at = max(display_updated_at, UserConversationState.updated_at)`
- 它用于会话项 `version` 计算和客户端增量水位推进
- 它不改变最终显示排序

排序与版本边界：

- `display_updated_at` 用于最终显示排序
- `sync_updated_at` 用于会话项 `version`
- `version > 0` 的先验候选发现使用：
  - `UserConversationState.updated_at > version`
  - `ChannelUpdateLog.updated_at > version`
- 当多个会话 `display_updated_at` 相同时，按 `channel_type asc`、`channel_id asc` 打破并列

这能保证单次 sync 的排序稳定和 deterministic truncation。

## Unread Calculation

基础计算：

- `base_visible_read_seq = max(read_seq, deleted_to_seq)`
- `unread = max(0, last_msg_seq - base_visible_read_seq)`

其中：

- `read_seq` 来自 `UserConversationState.read_seq`
- `deleted_to_seq` 会抬高 recent message 窗口的下界

单聊兼容语义：

- 返回 `channel_id` 时使用对端 uid，而不是内部归一化 person channel ID

“自己发送的最新消息不算未读”的兼容规则在查询侧处理：

- 如果最后一条 recent message 的 `from_uid == uid`，则把
  - `readed_to_msg_seq = last_msg_seq`
  - `unread = 0`

本次不在消息热路径持久化 sender ack position。

## Filter and Window Order

为避免 planning 和实现时出现歧义，过滤与窗口顺序固定为：

1. 构造原始候选集合
2. 应用 `exclude_channel_types`
3. 读取最小消息事实并计算 `unread`
4. 应用 `only_unread`
5. 按排序键排序
6. 截取 `limit`
7. 仅对最终结果加载 `recents`

`msg_count` 的语义固定为：

- 每个会话返回的 `recents` 条数上限
- 不参与候选集合裁剪
- 不参与排序
- 不参与 `version` 计算

## Failure and Fallback Behavior

- hot `ChannelUpdateLog` 查询失败：回退 cold index
- cold index 未命中：仅对已经入选的候选回退到 `channellog` 读取事实
- working set 缺失：回退 client known + 用户目录精确读取
- 冷会话激活 fanout 延迟：最多导致冷会话重新出现稍慢，但异步重试必须最终把会话推回 working set

整体原则：

- correctness 总能回退到 `UserConversationState + channellog`
- `ChannelUpdateLog` 是增量发现索引，不是最终消息事实源
- `channellog` 的 fallback 仅限已入选候选，不承担大范围候选发现

## Proposed Package Layout

- `internal/access/api/conversation_sync.go`
- `internal/access/api/conversation_legacy_model.go`
- `internal/usecase/conversation/app.go`
- `internal/usecase/conversation/deps.go`
- `internal/usecase/conversation/types.go`
- `internal/usecase/conversation/sync.go`
- `internal/usecase/conversation/projector.go`
- `pkg/storage/metadb/user_conversation_state.go`
- `pkg/storage/metadb/channel_update_log.go`
- `pkg/storage/metastore/user_conversation_state_rpc.go`
- `pkg/storage/metastore/channel_update_log_rpc.go`

## Integration Points

### Message Send

在 committed dispatcher 路径上接 conversation projector。

### Subscriber Changes

群订阅加入/移除时，维护：

- `UserConversationState`
- 加入时可按需要推进 `active_at`

### Direct Message Bootstrap

单聊首次出现时，为双方确保：

- `UserConversationState`（包含初始 `active_at`）

该操作是 pair 首次建档，不是每条消息写。

## Testing Strategy

### Storage Tests

- `UserConversationState` 幂等建档、读写、逻辑删除、read_seq 更新、`active_at` 排序读取、`updated_at` 原义保持、authoritative remote slot 读取
- `ChannelUpdateLog` hot 覆盖、flush 合并、cold 读取

### Usecase Tests

- working set 命中时不扫描全量目录
- cold wakeup 会通过更新 `UserConversationState.active_at` 把冷会话带回下次 sync
- `version > 0` 时先扫描用户 `UserConversationState` channel 目录，再按批查询 `ChannelUpdateLog`
- 客户端 `last_msg_seqs` 能补齐 server 目录之外的已知频道
- `only_unread` 过滤正确
- 单聊 `channel_id` 映射正确
- hot/cold update index 缺失时能回退

### API Tests

- 请求参数兼容旧版主要字段
- 响应体为旧版兼容数组，单会话字段语义与旧版对齐
- 单节点集群和多节点集群都能路由到 `uid owner`

## Open Decisions Resolved

- 不在消息热路径同步写 per-user conversation 索引
- `page/page_size`、`cursor`、`sync_version` 都不兼容
- 不在 `UserConversationState` 主表中加入 `last_interest_at`
- 不单独引入 `UserConversationWorkingSet` 表，最近会话索引直接落在 `UserConversationState.active_at`
- 不做“sync 命中自动续期 working set”
- `ChannelUpdateLog` 采用 hot memory + cold persistence 结构
- `UserConversationState.active_at` 承担最近会话业务时间语义
- `UserConversationState.updated_at` 保持状态记录自身的更新时间原义
- `version > 0` 时，先取用户 `UserConversationState` channel 集，再按这些 channel 查询 `ChannelUpdateLog`
- 冷会话重新活跃通过异步更新 `UserConversationState.active_at` 进入候选集合

## Implementation Guidance

实现顺序建议：

1. 扩展 `UserConversationState` 并落 `ChannelUpdateLog`
2. 落 `internal/usecase/conversation` 的 sync 编排
3. 接入 API `/conversation/sync`
4. 接入 message committed projector 和 hot/cold `ChannelUpdateLog`
5. 接入冷会话激活 fanout
6. 补 integration tests

先实现 correctness 路径，再叠加 hot/cold index 优化；任何阶段都必须保留回退到 `UserConversationState + channellog` 的能力。
