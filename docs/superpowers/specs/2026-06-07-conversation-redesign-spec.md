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
9. 明确最后消息展示字段必须真实持久化到 message log，不能依赖易丢失的内存事件。
10. 保留成员可见下界，避免新成员看到入群前消息。

## 非目标

1. 不在 `conversation` 表中存储 payload、from_uid、client_msg_no、last_message_id 等最后消息展示字段。
2. 不通过扫描 `user_channel_membership` 后再批量读取最新消息来组装全量最近会话。
3. 不让大群每条消息都把所有成员的会话顶到列表最前。
4. 不把 conversation projector 写入放进 message append 的同步主路径。
5. 不为单节点部署引入绕过集群语义的分支；单节点仍是单节点集群。

## 核心语义

`conversation.active_at` 是最近会话列表的权威排序锚点。

`active_at` 使用服务端 durable append/commit 侧时间，单位为 Unix milliseconds。客户端时间不参与排序锚点，避免客户端时钟或异步投影导致列表乱序。

`SparseActive=false` 表示该会话的 `active_at` 会随新消息频繁更新。个人会话和小群会话默认使用这个模式。

`SparseActive=true` 表示该会话的 `active_at` 是低频排序锚点。普通新消息不会频繁更新它。读取列表时可以展示 message log 中的最后一条消息，但该最后消息的时间不参与本次列表排序。

因此，大群即使有新消息，也不会因为每条消息都移动到每个成员最近会话列表的第一位。只有用户主动发送、打开、恢复、加入，或后台低频修正任务触发时，才会更新该用户该会话的 `active_at`。

分页不是快照一致读。请求期间如果有会话被新的 `active_at` 推到更靠前的位置，后续页面可能看不到该移动后的新位置；这是最近会话列表可接受的实时读语义。索引 cursor 只保证单次扫描在当前索引顺序中可继续前进。

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

同一 `active_at` 下按 `(channel_id, channel_type)` 做确定性排序。第一版不新增消息序列类排序字段，保持 conversation 不存最后消息信息。

conversation row 创建时必须保留成员可见下界。第一版不新增 `visible_from_seq` 字段，而是在创建用户会话行时把：

```text
read_seq = max(read_seq, join_seq - 1)
deleted_to_seq = max(deleted_to_seq, join_seq - 1)
```

其中 `join_seq` 来自 `user_channel_membership.JoinSeq` 或创建会话时的等价可见起点。这样 `/conversation/list` 不需要每次扫描 membership，也不会让新成员看到入群前消息。无法确定 `join_seq` 的 legacy row 按 `0` 处理。

### message log

`pkg/db/message` 继续作为每个 channel 消息事实来源。

读取最近会话时，针对当前页 conversation rows 查询每个 channel 的最后一条可见消息。

需要补高效 tail-read 接口，不能使用当前正向扫描再反转的 `ReadReverse` 实现：

```go
GetLastVisibleMessage(ctx, channelID, channelType, visibleAfterSeq)
GetLastVisibleMessageBatch(ctx, requests)
```

行为：

1. 从 channel log 尾部直接反向 seek。
2. `visibleAfterSeq` 是不可见前缀边界，通常为 `max(join_seq - 1, deleted_to_seq)`；如果 row 创建时已把 `join_seq - 1` 合入 `deleted_to_seq`，则直接使用 `deleted_to_seq`。
3. 找到一条可见消息就返回。
4. 没有可见消息时返回 not found，而不是清除 conversation row。
5. Batch 接口按 channel route 分组，并限制并发、rows 和 bytes。

message log 必须持久化最近会话展示需要的 durable 字段：

```text
message_id
message_seq
from_uid
client_msg_no
payload
server_timestamp_ms
```

`MessageCommitted`、channelv2 append DTO、channelv2 store adapter、`pkg/db/message.Record` 和 `pkg/db/message.Message` 必须能端到端保留这些字段。`active_at` 使用同一个 `server_timestamp_ms`，projector 不能使用客户端时间或本地重试时间覆盖排序锚点。

实现 `GetLastVisibleMessage` 前需要为 `pkg/db/internal/engine` 暴露受控反向迭代能力，例如 `Last/Prev/SeekLT` 或等价 wrapper。tail read 必须从 channel keyspace 尾部 seek，不能先正向读取全 channel 再反转。

### user_channel_membership

`user_channel_membership` 继续用于成员关系、权限校验和频道成员反查。

它不再用于 `/conversation/list` 的主读取路径。最近会话主路径只扫 `conversation` active index。

用户加入频道、主动打开频道或后台补齐 sparse conversation 时，创建 conversation row 的组件必须读取或携带 `JoinSeq`，并按上面的初始化规则写入 `read_seq/deleted_to_seq`。

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
active_at=event.server_timestamp_ms
updated_at=projection time
```

个人 channel id 可以继续复用现有个人频道 id 派生逻辑。对外返回时仍可转换成 peer uid。

### 小群消息

```text
message append committed
  -> projector resolves channel fanout class
  -> if member_count <= small_group_fanout_limit:
       fanout upsert conversation for members
     else:
       treat as sparse group
```

写入语义：

```text
sparse_active=false
active_at=event.server_timestamp_ms
updated_at=projection time
```

fanout 必须异步批量执行。不能放入 message append 同步路径，也不能让单条消息直接阻塞 Slot Raft 提交。

成员数判断不能通过每条消息全量扫描 subscriber 得出。第一版采用以下任一方式：

1. channel metadata 维护 durable/cached `subscriber_count`，projector 直接读取。
2. 没有 count 时，只读取 `small_group_fanout_limit + 1` 个 subscribers 做分类，读到超过阈值立即停止。

`small_group_fanout_limit` 作为配置项进入 internalv2 app 配置，建议默认值为 `1000`。配置变更时同步更新 `wukongim.conf.example`。

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

大群普通消息可以 touch 发送者自己的 conversation row，因为这是用户主动发送行为；不能批量 touch 非发送者成员。

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

`SparseActive` 的值来自 channel fanout policy，而不是只依赖 row 中的旧值。若频道从小群变为大群，后续 projector 事件按 sparse policy 写入；已有 dense row 可在下一次用户打开、用户发送或低频修正时转为 sparse。第一版不要求一次性扫描所有历史成员 backfill。

## 读取流程

### /conversation/list

```text
request(uid, cursor, limit)
  -> scan conversation active index by uid
  -> get limit rows plus one
  -> batch/concurrent fetch last visible message for returned rows through channel-owned route
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
server_timestamp_ms
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

`more` 使用整数 `0/1`，与现有兼容 API 风格保持一致。旧的 `truncated` 和 `scanned_memberships` 字段来自 membership-scan 临时实现，新方案不再返回。

### read_seq 与 deleted_to_seq

`read_seq` 用于未读计算：

```text
unread = max(0, last_visible_seq - max(read_seq, deleted_to_seq))
```

`deleted_to_seq` 用于最后消息可见性：

```text
last visible message must have seq > max(join floor, deleted_to_seq)
```

如果 channel 尾部消息都被该用户删除，`GetLastVisibleMessage` 继续向前找，直到找到可见消息或到达边界。

如果 conversation row 是基于 membership 创建的，`deleted_to_seq` 初始值已经包含 `join_seq - 1`，因此 `GetLastVisibleMessage` 不需要每次再查 membership。若未来引入单条消息撤回或 per-user message delete，需要为 tail read 增加 `max_backscan`、指标和 degraded 行为。

## API 形状

`POST /conversation/list`

这是 internalv2 conversation API 的目标形态，会替换当前 membership-scan 临时实现。新响应不再包含 `truncated` 和 `scanned_memberships`；cursor 从 latest-message cursor 改为 active-index cursor。

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
      "unread": 2,
      "last_message": {
        "message_id": 1001,
        "message_idstr": "1001",
        "message_seq": 12,
        "from_uid": "u2",
        "server_timestamp_ms": 1710000100,
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
  -> produce usecase result/domain DTO
```

usecase 通过端口依赖存储：

```go
type ConversationStore interface {
    ListUserConversationActivePage(...)
}

type MessageStore interface {
    GetLastVisibleMessageBatch(...)
}
```

### infra

`internalv2/infra/cluster` 适配 clusterv2 和 message db：

```text
conversation store -> clusterv2/meta facade
message store -> channel-owned committed log facade
```

usecase 只返回入口无关 result/domain DTO。HTTP JSON DTO 留在 access 层。

last-message 读取必须走 channel-owned authoritative route。多节点下不能从 UID-owned conversation 所在节点直接读本地 message DB。route not ready、owner unavailable、corrupt message 等基础设施错误默认让 `/conversation/list` 失败；只有单条消息 not found 返回 `last_message=null`。

`GetLastVisibleMessageBatch` 需要限制单页并发，例如：

```text
max_last_message_concurrency
max_last_message_batch_items = limit
```

### app

`internalv2/app` 装配 conversation usecase、projector、HTTP API 和 metrics observer。

业务策略不放在 app。app 只负责配置、依赖装配和生命周期；个人/小群/大群 fanout、SparseActive 合并和 cursor 规则属于 `internalv2/usecase/conversation` 或其内部 projector 组件。

### access

`internalv2/access/api` 只做 HTTP DTO 映射、错误映射和观测事件记录。排序、游标、最后消息补齐都不放在 access 层。

### Meta/Slot/Clusterv2 work items

实现本 spec 需要补齐 UID-owned conversation 的集群读写 surface：

1. `pkg/db/meta.UserConversationState` 增加 `SparseActive`，旧 value decode 默认 `false`。
2. 更新 meta schema、inspect、snapshot、compat `ShardStore` 和 `WriteBatch` surface。
3. 增加 `ListUserConversationActivePage(uid, cursor, limit)`，按 active index 返回 limit+1 稳定页。
4. active index scan 需要观测 stale index skip 数，并保留后续修复任务入口。
5. Slot FSM 增加 conversation upsert/touch batch command，TLV 编码包含 `SparseActive`、`ActiveAt`、`MessageSeq`、`ReadSeq`、`DeletedToSeq`。
6. FSM apply 必须按 UID hash slot 写入，拒绝不属于当前 Slot/HashSlot 的 batch item。
7. clusterv2 增加 `UpsertUserConversationStatesBatch` 和 `ListUserConversationActivePage` facade，按 UID route，并按 physical Slot/hashSlot 分组批量提交。
8. Batch 命令需要 rows/bytes 上限，避免大批 fanout command 拖慢 Slot apply。

## 集群与存储路由

`conversation` 是 UID-owned 表，读写按 `uid` 路由到对应 hash slot。

个人和小群 fanout 时，应按 UID hash slot 分组批量提交，避免每个用户单独提交一个命令。

message log 是 channel-owned，最后消息读取按 channel route 访问对应 channel log。

单节点部署仍按单节点集群处理，不引入绕过集群语义。

`/conversation/list` 是一个跨所有权读：先通过 UID-owned route 读取 conversation page，再通过 channel-owned route 批量读取最后消息。任何实现都不能因为当前节点本地有 message DB 句柄就绕过 channel route。

## projector 设计

conversation projector 消费 message committed events。

职责：

1. 个人消息写扩散两条 conversation。
2. 小群消息按阈值 fanout。
3. 大群消息不全量 fanout。
4. 对需要创建 sparse conversation 的场景执行低频 upsert。
5. 按 UID hash slot 聚合批量命令。
6. 提供 flush/drain 能力用于测试。

projector 输入：

```text
MessageCommitted event:
  channel_id
  channel_type
  message_id
  message_seq
  from_uid
  client_msg_no
  payload
  server_timestamp_ms

Channel policy/member source:
  person peer derivation
  subscriber_count or bounded subscriber page
  join_seq for row creation
```

分类规则：

```text
person:
  dense touch sender and peer

group where member_count <= small_group_fanout_limit:
  dense touch all listed members

group where member_count > small_group_fanout_limit:
  sparse policy
  ordinary message does not touch non-sender members
  sender may be sparse-touch because sending is explicit user activity
```

批处理原则：

```text
按 UID hash slot 分组
每组批量 upsert conversation
限制每批 rows、bytes、等待时间
失败后重试，upsert 必须幂等
```

## SparseActive transition table

```text
row absent + dense touch:
  create row
  sparse_active=false
  active_at=event.server_timestamp_ms
  read_seq/deleted_to_seq initialized from join_seq-1 when available

row absent + sparse ensure:
  create row
  sparse_active=true
  active_at=explicit activation time
  read_seq/deleted_to_seq initialized from join_seq-1 when available

dense row + dense touch:
  keep sparse_active=false
  advance active_at if event passes deleted_to_seq fence

sparse row + ordinary large-group message from non-sender:
  no write

sparse row + user send/open/resume/pin:
  keep sparse_active=true
  advance active_at for explicit activity if event passes deleted_to_seq fence

dense row + channel reclassified as large:
  future ordinary non-sender events do not touch the row
  next explicit sparse ensure may set sparse_active=true

sparse row + channel reclassified as small:
  dense touch may clear sparse_active=false after channel policy confirms fanout is allowed

hide/clear:
  advance deleted_to_seq as requested
  clear active_at when hide semantics require it
  keep sparse_active unchanged unless an explicit channel policy update changes it
```

## 幂等与并发

`conversation.active_at` 只能单调前进，除非明确执行 hide/clear。

普通 upsert 规则：

```text
if incoming.message_seq > 0 and incoming.message_seq <= existing.deleted_to_seq:
    ignore touch
else if incoming.active_at < existing.active_at:
    keep existing.active_at
else:
    update active_at
```

这个 `message_seq` fence 防止延迟 projector 事件在用户 hide/delete 后重新激活已删除会话。

read/hide 规则：

```text
hide deleted_to_seq must be monotonic
unread uses max(read_seq, deleted_to_seq)
last visible message uses seq > max(join floor, deleted_to_seq)
```

active index 同一 `active_at` 下使用 `(channel_id, channel_type)` 做确定性 tie-breaker。第一版不引入额外排序序列字段，也不承诺跨页快照一致；若产品后续要求同毫秒内严格消息顺序，可再引入不含消息内容的 `active_event_seq`。

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

重新激活仍必须遵守 `deleted_to_seq` fence：旧消息对应的延迟 projector 事件不能重新激活；用户打开、加入或发送产生的新显式活动可以重新设置 `active_at`。

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

`/conversation/list` 的 `O(limit)` 成立条件：

1. conversation active index 支持 cursor page scan。
2. active index stale entries 数量受控并有指标。
3. last-message tail read 是反向 seek，不是正向全量扫描。
4. Batch last-message 读取有并发和数量上限。

## 观测指标

保留或调整 `/conversation/list` 低基数指标：

```text
conversation_list_total{result}
conversation_list_duration_seconds{result}
conversation_list_returned_items{result}
conversation_list_last_message_loads{result}
conversation_list_sparse_items{result}
conversation_list_active_index_stale_skips{result}
conversation_list_last_message_errors{result}
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
2. 旧 value decode 时 `SparseActive=false`。
3. active index 按 `active_at desc, channel_id, channel_type` 返回。
4. active page cursor 能稳定翻页。
5. active index stale skip 计数可观测。
6. upsert 保持 `active_at` 单调。
7. sparse ensure 不会错误降低 `active_at`。
8. hide/delete 后 `deleted_to_seq` 和 active 状态符合预期。
9. `incoming.message_seq <= deleted_to_seq` 的 touch 被忽略。
10. join 创建时 `read_seq/deleted_to_seq` 初始化为 `join_seq-1`。

### pkg/db/message

1. `GetLastVisibleMessage` 从尾部直接读取最后一条消息。
2. `deleted_to_seq` 能跳过不可见尾部消息。
3. 空 channel 返回 not found。
4. 大 channel 取最后消息不正向扫描全量。
5. `from_uid`、`client_msg_no`、payload、`server_timestamp_ms` 通过 channelv2/message DB 真实写读链路端到端保留。
6. `visibleAfterSeq` 下界能阻止返回入群前消息。

### pkg/slot/fsm

1. conversation batch command encode/decode 包含 `SparseActive`、`MessageSeq`、`ReadSeq`、`DeletedToSeq`。
2. FSM apply 拒绝不属于当前 hash slot 的 UID-owned conversation row。
3. stale projector event 不能越过 `deleted_to_seq` fence。

### pkg/clusterv2

1. `UpsertUserConversationStatesBatch` 按 UID hash slot/physical Slot 分组提交。
2. `ListUserConversationActivePage` 按 UID route 读取正确 hash slot。
3. batch rows/bytes 上限生效。
4. routed read 不绕过集群语义。

### internalv2/infra/cluster

1. conversation store 适配 UID-owned clusterv2 facade。
2. message store 适配 channel-owned last-message route。
3. route not ready、not found、corrupt state 的错误映射符合 spec。

### internalv2/usecase/conversation

1. list 按 conversation active index 分页。
2. last message 只补齐当前页 rows。
3. `SparseActive=true` 的最后消息不改变返回排序。
4. person channel 返回 peer uid。
5. last message missing 时行为稳定。
6. unread 使用 `max(read_seq, deleted_to_seq)`。
7. API result 不包含 `truncated/scanned_memberships`。

### projector

1. 个人消息只写 sender/receiver 两条 conversation。
2. 小群在阈值内 fanout。
3. 大群超过阈值不 fanout。
4. projector 按 UID hash slot 聚合批量命令。
5. retry 后 upsert 幂等。
6. 大群发送者可 touch，非发送者成员不批量 touch。
7. member_count 分类不全量扫描大群 subscriber。

### HTTP/API

1. `/conversation/list` 映射 cursor、items、last_message。
2. invalid request 返回兼容错误。
3. metrics 记录 returned、sparse、last message loads、latency。
4. `more` 为 `0/1`，cursor exclusive 语义正确。
5. `last_message=null` 时仍返回 conversation。

### internalv2/app

1. app wiring 使用新的 conversation usecase、projector 和 message tail reader。
2. app 不再装配 `channel_latest` projector 路径。
3. metrics observer 保持低基数。

### smoke

1. 单节点集群个人消息后双方 list 可见。
2. 单节点集群小群消息后成员 list 可见。
3. 单节点集群大群消息不导致所有成员 active_at 更新。
4. 单节点集群 SEND -> projector flush -> `/conversation/list` 返回 message log 中的 last_message。
5. routed/multi-node smoke 覆盖 UID-owned conversation read 和 channel-owned last-message read。

## 迁移与收敛

当前工作区已有 `channel_latest` 相关临时实现时，本 spec 取代该方向。

迁移步骤建议：

1. 在 `conversation` 表增加 `SparseActive` 字段。
2. 增加 active page scan API。
3. 增加高效 `GetLastVisibleMessage`。
4. 补齐 message durable fields，保证 last-message API 能返回展示字段和 `server_timestamp_ms`。
5. 增加 UID-owned conversation Slot FSM command 和 clusterv2 facade。
6. 重写 `internalv2/usecase/conversation`，改为扫 conversation active index。
7. 重写 HTTP `/conversation/list` 响应 last_message 补齐。
8. 重写 projector：个人/小群 fanout，大群 sparse。
9. 停止接入 `channel_latest` 表、slot command、clusterv2 facade、metrics 中的 latest 投影逻辑。
10. 更新 FLOW.md 和 AGENTS.md 中与 conversation 相关描述。

`channel_latest` 收敛分两阶段：

```text
Phase 1:
  stop writing channel_latest
  stop reading channel_latest from conversation usecase
  remove app projector wiring for latest
  remove latest-specific metrics from active paths
  keep durable table ID, command ID, decoder, snapshot/inspect compatibility if they may exist in local data or Raft logs

Phase 2:
  after the durable compatibility window is explicitly closed,
  remove unused latest table/facade/commands if no released data can reference them
  never reuse old table IDs or command IDs for a different meaning
```

Acceptance check:

```bash
rg "channel_latest|ChannelLatest|UpsertChannelLatest|GetChannelLatest"
```

Allowed leftovers after Phase 1 must be documented as durability compatibility code only.

## 开放决策

已确认：

1. `SparseActive=true` 时，排序看 `conversation.active_at`。
2. 最后一条消息只用于展示，不参与排序。
3. `conversation` 不存最后一条消息字段。
4. 新方案不需要 `channel_latest`。
5. 第一版 `small_group_fanout_limit` 默认值为 `1000`。
6. 大群 sparse conversation 创建时机为用户加入、用户发送、用户打开/恢复。

仍可在 implementation plan 中细化：

1. `max_last_message_concurrency` 默认值。
2. conversation projector batch rows/bytes 上限。

## 验收标准

1. 个人消息提交后，双方 `/conversation/list` 稳定出现该会话。
2. 小群消息在阈值内能更新成员最近会话。
3. 大群消息不会按成员数写扩散 conversation。
4. `/conversation/list` 不扫描用户全部 membership。
5. `/conversation/list` 只按 `conversation.active_at` 排序。
6. `SparseActive=true` 会话的新消息不会自动改变其列表位置。
7. 最后一条消息来自 message log，且遵守 `deleted_to_seq` 和 join visibility 下界。
8. `/conversation/list` 不从 UID-owned 节点绕过 channel route 读取 message log。
9. Phase 1 后 active conversation path 不读写 `channel_latest`。
10. 相关单元测试、单节点集群 smoke 测试和 routed/multi-node smoke 测试通过。
