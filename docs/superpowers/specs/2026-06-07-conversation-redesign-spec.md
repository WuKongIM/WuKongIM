# Conversation Redesign Spec

## 背景

最近会话的核心矛盾是写放大和读放大不能同时用一套模型解决。

个人会话一条消息最多影响两个人，适合写扩散。小群消息影响人数可控，也可以按阈值写扩散。大群消息可能影响 10 万人，如果每条消息都更新所有成员的最近会话，会拖慢 Slot Raft 和后台投影；如果完全读时扫描用户所有 membership，又会在用户加入大量频道时漏数据或读放大过高。

本设计复用已有 `pkg/db/meta` 的 `conversation` 表作为唯一用户维度最近会话索引。`conversation` 只存用户会话状态和排序锚点，不存最后一条消息内容。最后一条消息在 `/conversation/list` 返回时从 `pkg/db/message` 的 channel log 查询。

## 目标

1. 复用现有 `conversation` 表，避免新增 `user_person_conversation` 或 `user_group_conversation` 重复表。
2. 删除新方案中的 `channel_latest` 依赖，message log 是最后消息事实来源。
3. 个人消息写扩散更新双方 `conversation.active_at`。
4. 小群消息在成员数阈值内写扩散更新成员 `conversation.active_at`。
5. 大群消息不全量更新成员 `conversation.active_at`，避免一条消息触发 10 万用户会话写入。
6. 支持 `/conversation/list` 按 `conversation.active_at` 稳定分页。
7. 支持 `SparseActive=true` 会话低频更新排序锚点，最后消息只用于展示，不反向影响排序。
8. 读取最近会话时只补齐当前页的最后消息，避免扫描用户全部 membership 或大量频道。

## 非目标

1. 不在 `conversation` 表中存储 payload、from_uid、client_msg_no、last_message_id 等最后消息展示字段。
2. 不通过扫描 `user_channel_membership` 后再批量读取最新消息来组装全量最近会话。
3. 不让大群每条消息都把所有成员的会话顶到列表最前。
4. 不把 conversation projector 写入放进 message append 的同步主路径。
5. 不为单节点部署引入绕过集群语义的分支；单节点仍是单节点集群。

## 核心语义

`conversation.active_at` 是最近会话列表的权威排序锚点。

`SparseActive=false` 表示该会话的 `active_at` 会随新消息频繁更新。个人会话和小群会话默认使用这个模式。

`SparseActive=true` 表示该会话的 `active_at` 是低频排序锚点。普通新消息不会频繁更新它。读取列表时可以展示 message log 中的最后一条消息，但该最后消息的时间不参与本次列表排序。

因此，大群即使有新消息，也不会因为每条消息都移动到每个成员最近会话列表的第一位。只有用户主动发送、打开、恢复、加入，或后台低频修正任务触发时，才会更新该用户该会话的 `active_at`。

## 数据模型

### conversation

复用已有 `pkg/db/meta` 的 `conversation` 表。

字段：

```text
uid
channel_id
channel_type
read_seq
deleted_to_seq
active_at
updated_at
sparse_active
```

新增字段：

```go
SparseActive bool
```

字段注释建议：

```go
// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
// Message appends do not have to advance ActiveAt for every user in this conversation.
```

主键保持：

```text
(uid, channel_id, channel_type)
```

active 索引保持或升级为：

```text
(uid, active_at desc, channel_id, channel_type)
```

如果后续需要同一 `active_at` 下按更稳定的消息顺序排序，可以追加 `last_active_seq`。本阶段不引入，因为当前设计不要求 conversation 存最后消息 seq。

### message log

`pkg/db/message` 继续作为每个 channel 消息事实来源。

读取最近会话时，针对当前页 conversation rows 查询每个 channel 的最后一条可见消息。

需要补一个高效接口，不能使用当前正向扫描再反转的 `ReadReverse` 实现：

```go
GetLastVisibleMessage(ctx, channelID, channelType, deletedToSeq)
```

行为：

1. 从 channel log 尾部直接反向 seek。
2. 跳过 `seq <= deleted_to_seq` 的消息。
3. 找到一条可见消息就返回。
4. 没有可见消息时返回 not found，而不是清除 conversation row。

### user_channel_membership

`user_channel_membership` 继续用于成员关系、权限校验和频道成员反查。

它不再用于 `/conversation/list` 的主读取路径。最近会话主路径只扫 `conversation` active index。

## 写入流程

### 个人消息

```text
message append committed
  -> projector receives committed message event
  -> upsert conversation(sender, peer)
  -> upsert conversation(receiver, sender)
```

写入语义：

```text
sparse_active=false
active_at=message timestamp
updated_at=projection time
```

个人 channel id 可以继续复用现有个人频道 id 派生逻辑。对外返回时仍可转换成 peer uid。

### 小群消息

```text
message append committed
  -> projector resolves channel member count
  -> if member_count <= small_group_fanout_limit:
       fanout upsert conversation for members
     else:
       treat as sparse group
```

写入语义：

```text
sparse_active=false
active_at=message timestamp
updated_at=projection time
```

fanout 必须异步批量执行。不能放入 message append 同步路径，也不能让单条消息直接阻塞 Slot Raft 提交。

### 大群消息

```text
message append committed
  -> no full member fanout
  -> only message log has the new message
```

对于已存在的用户 conversation row：

```text
sparse_active=true
active_at 不随普通消息更新
```

对于尚不存在 conversation row 的成员，不因普通大群消息创建用户 conversation。创建时机包括：

1. 用户加入频道。
2. 用户主动发送消息。
3. 用户主动打开该会话。
4. 用户显式订阅或 pin 该会话。
5. 后台低频任务为活跃用户补齐。

### sparse active 更新时机

`SparseActive=true` 的 `active_at` 可以在以下场景更新：

```text
user joins channel
user sends a message in channel
user opens or resumes channel
user pins or explicitly activates channel
low-frequency projector repair
```

这些更新不是每条消息级别的，目标是让大群列表位置低频变化。

## 读取流程

### /conversation/list

```text
request(uid, cursor, limit)
  -> scan conversation active index by uid
  -> get limit rows plus one
  -> batch/concurrent fetch last visible message for returned rows
  -> map person channel id to peer uid where needed
  -> return rows with last_message
```

排序只基于 `conversation.active_at`：

```text
active_at desc
channel_id asc
channel_type asc
```

最后消息只用于展示：

```text
last_message_id
last_message_seq
from_uid
timestamp
payload/preview
client_msg_no
```

如果某条 conversation 没有可见最后消息：

返回 conversation 且 `last_message=null`。读接口不删除、不隐藏、不跳过该 conversation，避免读取路径产生副作用。后台修正可以作为独立任务处理异常状态。

### 分页 cursor

active list 必须使用 active index cursor：

```text
active_at
channel_id
channel_type
```

接口可继续对外使用结构化 cursor，或编码成 opaque cursor。第一版建议结构化，便于测试和排查。

示例：

```json
{
  "active_at": 1710000000,
  "channel_id": "group-1",
  "channel_type": 2
}
```

下一页扫描从该 active index key 之后开始。

### read_seq 与 deleted_to_seq

`read_seq` 用于未读计算：

```text
unread = max(0, last_visible_seq - read_seq)
```

`deleted_to_seq` 用于最后消息可见性：

```text
last visible message must have seq > deleted_to_seq
```

如果 channel 尾部消息都被该用户删除，`GetLastVisibleMessage` 继续向前找，直到找到可见消息或到达边界。

## API 形状

`POST /conversation/list`

请求：

```json
{
  "uid": "u1",
  "limit": 50,
  "cursor": {
    "active_at": 1710000000,
    "channel_id": "group-1",
    "channel_type": 2
  }
}
```

响应：

```json
{
  "conversations": [
    {
      "channel_id": "group-1",
      "channel_type": 2,
      "active_at": 1710000000,
      "sparse_active": true,
      "read_seq": 10,
      "deleted_to_seq": 0,
      "last_message": {
        "message_id": 1001,
        "message_idstr": "1001",
        "message_seq": 12,
        "from_uid": "u2",
        "timestamp": 1710000100,
        "client_msg_no": "c1",
        "payload": "..."
      }
    }
  ],
  "next_cursor": {
    "active_at": 1709999999,
    "channel_id": "group-0",
    "channel_type": 2
  },
  "more": 1
}
```

## internalv2 分层

### usecase

`internalv2/usecase/conversation` 负责入口无关编排：

```text
List(uid, cursor, limit)
  -> scan user conversation active page
  -> load last visible messages for page rows
  -> produce response DTO
```

usecase 通过端口依赖存储：

```go
type ConversationStore interface {
    ListUserConversationActivePage(...)
}

type MessageStore interface {
    GetLastVisibleMessage(...)
}
```

### infra

`internalv2/infra/cluster` 适配 clusterv2 和 message db：

```text
conversation store -> clusterv2/meta facade
message store -> message runtime or message db facade
```

### app

`internalv2/app` 装配 conversation usecase、projector、HTTP API 和 metrics observer。

### access

`internalv2/access/api` 只做 HTTP DTO 映射、错误映射和观测事件记录。排序、游标、最后消息补齐都不放在 access 层。

## 集群与存储路由

`conversation` 是 UID-owned 表，读写按 `uid` 路由到对应 hash slot。

个人和小群 fanout 时，应按 UID hash slot 分组批量提交，避免每个用户单独提交一个命令。

message log 是 channel-owned，最后消息读取按 channel route 访问对应 channel log。

单节点部署仍按单节点集群处理，不引入绕过集群语义。

## projector 设计

conversation projector 消费 message committed events。

职责：

1. 个人消息写扩散两条 conversation。
2. 小群消息按阈值 fanout。
3. 大群消息不全量 fanout。
4. 对需要创建 sparse conversation 的场景执行低频 upsert。
5. 按 UID hash slot 聚合批量命令。
6. 提供 flush/drain 能力用于测试。

批处理原则：

```text
按 UID hash slot 分组
每组批量 upsert conversation
限制每批 rows、bytes、等待时间
失败后重试，upsert 必须幂等
```

## 幂等与并发

`conversation.active_at` 只能单调前进，除非明确执行 hide/clear。

普通 upsert 规则：

```text
if incoming.active_at < existing.active_at:
    keep existing.active_at
else:
    update active_at
```

`SparseActive` 合并规则：

```text
explicit sparse=true can set row sparse
explicit sparse=false can clear sparse only when channel policy allows fanout
```

第一版建议由调用方传明确写模式：

```text
dense touch: sparse_active=false, active_at advances
sparse ensure: sparse_active=true, active_at advances only for explicit activation
```

## 删除、隐藏和清理

`HideUserConversation` 继续推进 `deleted_to_seq` 并清除或降低 active 状态，现有语义可保留。

删除后：

```text
deleted_to_seq advances
active_at can be cleared to 0
last visible message query uses deleted_to_seq
```

如果用户再次发送、打开或加入，conversation 可以重新激活。

## 性能预期

个人消息：

```text
message append: O(1)
conversation projector: O(2)
list read: O(limit) conversation scan + O(limit) last message reads
```

小群消息：

```text
message append: O(1)
conversation projector: O(member_count) only below threshold
list read: O(limit)
```

大群消息：

```text
message append: O(1)
conversation projector: O(0) for ordinary message fanout
list read: O(limit) for conversation rows + O(limit) last message reads
```

最重要的改进是：大群消息不会触发 10 万用户的会话更新。

## 观测指标

保留或调整 `/conversation/list` 低基数指标：

```text
conversation_list_total{result}
conversation_list_duration_seconds{result}
conversation_list_returned_items{result}
conversation_list_last_message_loads{result}
conversation_list_sparse_items{result}
```

projector 指标：

```text
conversation_projector_events_total{kind,result}
conversation_projector_batches_total{kind,result}
conversation_projector_rows_total{kind,result}
conversation_projector_flush_duration_seconds{kind,result}
```

不要把 `uid`、`channel_id` 放进 metrics label。

## 测试计划

### pkg/db/meta

1. `UserConversationState` 编解码包含 `SparseActive`。
2. active index 按 `active_at desc, channel_id, channel_type` 返回。
3. active page cursor 能稳定翻页。
4. upsert 保持 `active_at` 单调。
5. sparse ensure 不会错误降低 `active_at`。
6. hide/delete 后 `deleted_to_seq` 和 active 状态符合预期。

### pkg/db/message

1. `GetLastVisibleMessage` 从尾部直接读取最后一条消息。
2. `deleted_to_seq` 能跳过不可见尾部消息。
3. 空 channel 返回 not found。
4. 大 channel 取最后消息不正向扫描全量。

### internalv2/usecase/conversation

1. list 按 conversation active index 分页。
2. last message 只补齐当前页 rows。
3. `SparseActive=true` 的最后消息不改变返回排序。
4. person channel 返回 peer uid。
5. last message missing 时行为稳定。

### projector

1. 个人消息只写 sender/receiver 两条 conversation。
2. 小群在阈值内 fanout。
3. 大群超过阈值不 fanout。
4. projector 按 UID hash slot 聚合批量命令。
5. retry 后 upsert 幂等。

### HTTP/API

1. `/conversation/list` 映射 cursor、items、last_message。
2. invalid request 返回兼容错误。
3. metrics 记录 returned、sparse、last message loads、latency。

### smoke

1. 单节点集群个人消息后双方 list 可见。
2. 单节点集群小群消息后成员 list 可见。
3. 单节点集群大群消息不导致所有成员 active_at 更新。

## 迁移与收敛

当前工作区已有 `channel_latest` 相关临时实现时，本 spec 取代该方向。

迁移步骤建议：

1. 在 `conversation` 表增加 `SparseActive` 字段。
2. 增加 active page scan API。
3. 增加高效 `GetLastVisibleMessage`。
4. 重写 `internalv2/usecase/conversation`，改为扫 conversation active index。
5. 重写 HTTP `/conversation/list` 响应 last_message 补齐。
6. 重写 projector：个人/小群 fanout，大群 sparse。
7. 移除或停止接入 `channel_latest` 表、slot command、clusterv2 facade、metrics 中的 latest 投影逻辑。
8. 更新 FLOW.md 和 AGENTS.md 中与 conversation 相关描述。

## 开放决策

已确认：

1. `SparseActive=true` 时，排序看 `conversation.active_at`。
2. 最后一条消息只用于展示，不参与排序。
3. `conversation` 不存最后一条消息字段。
4. 新方案不需要 `channel_latest`。

需要实现前选择默认值：

1. `small_group_fanout_limit` 默认值。建议第一版使用 500 或 1000。
2. 大群 sparse conversation 创建时机。建议第一版支持用户加入、用户发送、用户打开。

## 验收标准

1. 个人消息提交后，双方 `/conversation/list` 稳定出现该会话。
2. 小群消息在阈值内能更新成员最近会话。
3. 大群消息不会按成员数写扩散 conversation。
4. `/conversation/list` 不扫描用户全部 membership。
5. `/conversation/list` 只按 `conversation.active_at` 排序。
6. `SparseActive=true` 会话的新消息不会自动改变其列表位置。
7. 最后一条消息来自 message log，且遵守 `deleted_to_seq` 可见性。
8. 相关单元测试和单节点集群 smoke 测试通过。
