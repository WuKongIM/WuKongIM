# Conversation Authority Cache Design

## 背景

当前 internalv2 最近会话由 `MessageCommitted` 进入 app 层 conversation projector，再由后台 worker 按 idle、flush interval、max lag 和批量预算写入 UID-owned `conversation` rows。

这个模型保护了 SEND/SENDACK 主路径性能，但会产生可见性窗口：消息已经 durable commit，`/conversation/list` 仍然只读 DB active rows，因此 flush 前可能查不到刚生成的 conversation。

需要保持批量写性能，同时让任意 API 节点收到 `List(uid)` 时都能读到该 UID 权威节点上的未 flush conversation cache。

## 目标

1. conversation row 的生成、缓存和 flush 归属 UID 权威节点。
2. 任意节点收到 `List(uid)` 都通过 UID 路由读权威结果。
3. 权威节点 List 合并 `cache + db`，消除 flush 前查不到的窗口。
4. 继续异步批量 flush DB，避免把 conversation durable write 放回 SEND/SENDACK 同步主路径。
5. 复用现有 `internalv2/usecase/conversation.Projector` 投影规则，避免重新定义个人、小群、大群语义。
6. 保持单节点集群和多节点集群同一套语义，不增加绕过集群的业务分支。

## 非目标

1. 不在 conversation row 中存储最后消息 payload、from_uid、client_msg_no 等展示字段。
2. 不通过调小 flush interval 或 idle delay 作为主要解决方式。
3. 不让普通大群消息全量写扩散到所有成员。
4. 不引入新的泛化 service 层。
5. 不改变 channel message log 作为最后消息事实来源的规则。

## 核心语义

UID 权威节点是某个 UID 最近会话读写的唯一即时权威。

`conversation` DB rows 是持久化事实来源。权威节点内存 cache 是 DB flush 前的即时可见层。List 的结果必须来自同一个 UID 权威节点合并后的视图，不能由入口节点各自拼本地 cache。

节点重启后 cache 可以丢失。已 flush 的 conversation 从 DB 读回；未 flush 的短窗口数据丢失属于现有异步投影语义，不扩大为跨节点不一致。后续消息或后台修正可以再次推进 row。

## 架构

### app

`internalv2/app` 继续作为唯一组合根。

新增一个 UID authority conversation runtime，替代当前“本节点收 committed event、本节点 dirty、本节点 flush”的 projector 运行形态。它包含：

1. projection coordinator：复用 `conversation.Projector` 把 committed event 转成 `metadb.UserConversationState`。
2. authority router：按 row.UID 解析 clusterv2 UID hash-slot leader。
3. authority cache：本节点作为 UID leader 时保存未 flush rows。
4. flush worker：按 UID-owned rows 批量调用 `UpsertUserConversationStatesBatch`。
5. authoritative list reader：在本节点合并 DB active page 与 authority cache，再补 last visible messages。

### access/node

`internalv2/access/node` 新增 conversation RPC 适配，风格与 presence/delivery RPC 保持一致。

建议新增 service：

```text
RPCConversationAuthority
```

支持两类内部调用：

```text
upsert_conversation_states
list_conversations
```

RPC payload 使用稳定二进制 codec，request/response magic 单独定义版本。响应 status 复用低基数字符串：

```text
ok
not_leader
route_not_ready
rejected
```

`access/node` 只做编码、解码、状态映射和本地 port 调用，不承载 conversation 投影、缓存、分页或成员分类规则。

### infra/cluster

`internalv2/infra/cluster` 新增 conversation authority client。

它负责：

1. `RouteKey(uid)` 解析 UID 权威 leader。
2. leader 是本节点时调用本地 authority port。
3. leader 是远端时通过 `access/node` conversation RPC 调用远端。
4. 对 route-not-ready、not-leader 做与 presence 类似的短时间 fresh-route retry。

这个 adapter 不修改 conversation 业务语义，只负责 clusterv2 路由与 RPC。

### usecase/conversation

保留现有 `List` 的入口无关语义，但需要把 store port 从“只读 DB active rows”扩展为“读权威 active view”。

一种轻量方式是增加新的 port：

```go
// ActiveViewStore pages UID-owned active rows from DB plus unflushed authority cache.
type ActiveViewStore interface {
    ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
}
```

`App.List` 使用 active view port；last message 仍通过 `MessageStore.GetLastVisibleMessages` 读取当前页 channel tail。

## 写入流程

```text
message durable commit
  -> committed sink accepts MessageCommitted metadata into a bounded queue
  -> background projection worker drains queued events
  -> projection coordinator builds candidate UserConversationState rows
  -> group rows by UID authority leader
  -> local leader rows enter authority cache
  -> remote leader rows go through ConversationAuthority RPC
  -> authority cache coalesces by (uid, channel_id, channel_type)
  -> flush worker asynchronously writes batches to DB
```

coalesce 规则：

1. 同一个 `(uid, channel_id, channel_type)` 只保留最新 `ActiveAt` 的 row。
2. `ReadSeq` 和 `DeletedToSeq` 不能被更小的 projection floor 覆盖。
3. `SparseActive` 按现有 projector policy 保留。
4. cache row 写入不等待 DB flush，不阻塞 SENDACK。

如果远端 RPC 失败，事件不能静默丢弃。第一版按现有 projector requeue 语义处理：可重试错误重新入队，队列满或持续失败通过低基数指标和日志暴露。

## 读取流程

```text
HTTP /conversation/list on any node
  -> conversation usecase List(uid, cursor, limit)
  -> authority client resolves UID leader
  -> local leader:
       read DB active page window
       merge authority cache rows for uid
       sort by active_at desc, channel_id, channel_type
       apply cursor and limit
       read last visible messages for returned rows
       return page
  -> remote leader:
       ConversationAuthority RPC list_conversations
       remote leader executes the same local merge path
       return encoded ListResult
```

为了避免 cache row 被 DB page 边界截断漏掉，权威读不能只读取 DB 的 `limit` 行再简单 append cache。第一版采用 bounded merge window：

1. 从 DB 读取 `limit + cacheCandidateCount + 1` 行，上限受配置保护。
2. 取该 UID cache 中 cursor 之后的 candidate rows。
3. 用 `(uid, channel_id, channel_type)` 去重；同 key 合并时 cache row 只推进 `ActiveAt`、`UpdatedAt`、`SparseActive` 等投影字段，`ReadSeq` 和 `DeletedToSeq` 取 cache 与 DB 的较大值。
4. 统一排序后截取 `limit`。

如果某个 UID cache candidate 数超过上限，List 返回时仍以 DB + bounded cache 为准，并记录 cache pressure 指标。后续 flush 会把超出部分固化到 DB。

## 错误处理

1. route not ready：List 返回 typed route-not-ready 错误，由 HTTP 层按现有错误映射处理。
2. not leader 或 stale route：authority client 重新 resolve UID route 并短时间重试。
3. remote RPC rejected：保留为请求错误，不回退到入口节点本地 DB-only 读，避免返回看似成功但漏数据的结果。
4. last message 读不到：沿用现有语义，只表示该 conversation 当前没有可展示消息，不删除 row。
5. flush 失败：cache row 保留或 requeue，继续对权威 List 可见。

## 配置与观测

新增配置应保持 `WK_` 前缀，并同步 `wukongim.conf.example`：

```text
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID
WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT
```

配置字段需要英文注释。

指标保持低基数，不带 UID 或 channel label：

1. authority cache rows、dirty rows、per-UID pressure bucket。
2. authority RPC request/result/duration。
3. list merge DB rows、cache rows、returned rows、has_more。
4. flush rows/result/duration/requeued rows。

## 测试

单元测试：

1. authority cache coalesce 保留最新 `ActiveAt`，不降低 `ReadSeq/DeletedToSeq`。
2. local List 合并 DB row 和 cache row，同 key 时 cache 推进投影字段且 read/delete state 取较大值。
3. cursor 后的 cache-only row 能出现在第一页。
4. remote List 通过 node RPC 到 UID leader。
5. not-leader route retry 后改打新 leader。
6. RPC rejected 不回退到 DB-only 成功响应。

装配测试：

1. `internalv2/app` 在 cluster 暴露 conversation authority surface 时注册 RPC handler。
2. single-node cluster List 走本地 authority merge。
3. static multi-node cluster 中，非权威 API 节点查询 UID conversation 时转发到权威节点。

性能测试只做轻量单元或定向测试；三节点 Docker Compose 和 wkbench 类场景按 `docs/development/PERF_TRIAGE.md` 采证后再跑。

## 实施边界

第一版只处理 conversation 生成可见性和 List 权威转发，不扩大到 read ack、delete conversation、pin、mute 等后续会话状态能力。

`internalv2/app/FLOW.md`、`internalv2/access/node/FLOW.md`、`internalv2/infra/cluster/FLOW.md` 需要随实现更新，描述新的 authority cache、RPC 和 List flow。
