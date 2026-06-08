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

成功返回的权威 List 不能明知遗漏 cursor 范围内的 eligible cache row。cache 压力、路由切换或 authority ownership 不确定时，List 必须返回 typed error，而不是退化成 DB-only 或 partial-cache 成功响应。

节点重启后 cache 可以丢失。已 flush 的 conversation 从 DB 读回；未 flush 的短窗口数据丢失属于现有异步投影语义，不扩大为跨节点不一致。后续消息或后台修正可以再次推进 row。节点仍持有某个 UID hash-slot 的 unflushed cache 时，不能在失去该 hash-slot authority 后继续对外提供成功 List。

authority cache ownership 由完整 route target fencing：

```text
hash_slot
slot_id
leader_node_id
route_revision
authority_epoch
```

authority 写入和读取都携带这个 target。接收节点发现 target 不是自己当前持有的 authority 时返回 `stale_route`、`not_leader` 或 `route_not_ready`，调用方重新 resolve 后重试。

authority handoff 是显式状态机：

1. 节点收到某 hash-slot 不再由自己负责的 authority event 后，把该 target 标记为 `draining`，停止对该 target 返回 successful List，并尝试 flush 或 transfer dirty cache。
2. 节点收到某 hash-slot 新归自己负责的 authority event 后，把该 target 标记为 `warming`。在 warming 完成前，List 返回 `route_not_ready`，不能直接 DB-only 成功。
3. warming 完成条件是：旧 leader 通过 drain RPC 返回 drained/transferred，或者本节点本地没有该 target 的历史 dirty cache 且旧 leader 明确返回 no-dirty。
4. 如果旧 leader 不可达，第一版允许在 `AuthorityHandoffTimeout` 后进入 explicit abandon，并记录低基数 abandon metric/log；这是 crash/lost-cache degraded state，不是普通 successful handoff。

## 架构

### app

`internalv2/app` 继续作为唯一组合根。

新增一个 UID authority conversation runtime，替代当前“本节点收 committed event、本节点 dirty、本节点 flush”的 projector 运行形态。它包含：

1. projection coordinator：复用 `conversation.Projector` 的 fanout policy，把 committed event 转成带 source `MessageSeq` 的 active patches。
2. immediate admission path：`CommittedSink.Submit` 在返回前完成 projection 和 authority cache admission，DB flush 仍异步。
3. authority router：按 row.UID 解析 clusterv2 UID hash-slot leader。
4. authority cache：本节点作为 UID leader 时保存未 flush rows。
5. flush worker：按 UID-owned rows 批量调用 conversation active patch / batch write facade。
6. route authority watcher：监听 `WatchRouteAuthorities`，失去 authority 时阻止本地成功 List，并尝试先 flush owned dirty rows。
7. authoritative active reader：在本节点合并 DB active page 与 authority cache，返回 active rows。

authority cache 至少包含这些索引：

1. key index：`(uid, channel_id, channel_type) -> cache entry`，用于 coalesce 和 DB 同 key overlay。
2. per-UID ordered index：`(active_at desc, channel_id, channel_type)`，用于 cursor paging，不允许 List 扫描时因为 per-UID row 数超过窗口而静默截断。
3. dirty flush queue：按 route target / UID row key 去重，供 DB flush worker drain。

cache entry 需要保留 source `MessageSeq`，这样 merge 和 flush 都能遵守 `DeletedToSeq` delete barrier。

### access/node

`internalv2/access/node` 新增 conversation RPC 适配，风格与 presence/delivery RPC 保持一致。

建议新增 service：

```text
RPCConversationAuthority
```

支持两类内部调用：

```text
admit_conversation_active_patches
list_conversations
drain_conversation_authority
```

RPC payload 使用稳定二进制 codec，request/response magic 单独定义版本。响应 status 复用低基数字符串：

```text
ok
not_leader
stale_route
route_not_ready
cache_pressure
rejected
```

`access/node` 只做编码、解码、状态映射和本地 port 调用，不承载 conversation 投影、缓存、分页或成员分类规则。

`admit_conversation_active_patches` 的一个 RPC request 只承载同一个 exact route target 的 patches。该 request 在 target 内按 all-or-nothing status 返回；不同 target 由 infra client 拆成多个 request，因此一个 target 的 stale/not-leader 不影响其他 target 的 admission。

`drain_conversation_authority` response 使用明确 result enum：

```text
drained      old leader flushed all dirty rows for the target
transferred  old leader transferred dirty cache entries to the new target owner
no_dirty     old leader has no dirty cache for the target
busy         old leader is still draining; caller should retry until handoff timeout
```

实现计划中需要在 `pkg/clusterv2/net/ids.go` 追加稳定 service id：

```text
RPCConversationAuthority
```

### infra/cluster

`internalv2/infra/cluster` 新增 conversation authority client。

它负责：

1. `RouteKey(uid)` 解析 UID 权威 leader。
2. leader 是本节点时调用本地 authority port。
3. leader 是远端时通过 `access/node` conversation RPC 调用远端。
4. 按完整 route target 分组写入，而不是只按 leader node 分组。
5. 对 route-not-ready、stale-route、not-leader 做与 presence 类似的短时间 fresh-route retry。

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

node RPC 只返回 authoritative active rows 和 cursor/done 状态。`internalv2/usecase/conversation.App` 继续负责 cursor 语义、last-message hydration、unread 计算和 public `ListResult` 组装，避免远端 RPC handler 复制 usecase 逻辑。

## 写入流程

```text
message durable commit
  -> committed sink projects metadata promptly
  -> projection coordinator builds candidate active patches
  -> group rows by exact UID authority route target
  -> local leader rows enter authority cache
  -> remote leader rows go through ConversationAuthority RPC
  -> authority cache coalesces by (uid, channel_id, channel_type)
  -> flush worker asynchronously writes batches to DB
```

这里有两个独立阶段：

1. **cache admission stage**：committed sink 在返回前完成 projection、route target resolve、本地 cache admission 或远端 authority RPC admission。这个阶段不能等 idle flush cadence。失败时返回 sink error 并记录 metrics/logging；现有 message usecase 仍把 committed sink error 视为 non-fatal send observation。
2. **durable flush stage**：authority cache 的 dirty rows 按 flush interval、idle delay、max lag、row budget 批量写 DB。

cache admission 不允许逐 row 远端 RPC。它必须按 exact route target 合并为 batch，并用 `RPC_BATCH_ROWS` 与 `RPC_CONCURRENCY` 控制并发。`ADMISSION_TIMEOUT` 是 foreground admission budget；超过预算时返回 sink error，SENDACK 仍按现有 non-fatal sink error 语义继续，但该事件不属于 no-gap success guarantee。

coalesce 规则：

1. 同一个 `(uid, channel_id, channel_type)` 只保留最新 `ActiveAt` 的 row。
2. `ReadSeq` 和 `DeletedToSeq` 不能被更小的 projection floor 覆盖。
3. cache entry 保存 source `MessageSeq`；如果 DB 或 cache 中 `DeletedToSeq >= MessageSeq`，该 projection 不能重新激活 conversation。
4. `SparseActive` 按现有 projector policy 保留。
5. cache row 写入不等待 DB flush，不等待 Slot proposal。

flush 写 DB 时应使用等价于 `TouchUserConversationActiveAt` 的 patch 语义，而不是 blind upsert：patch 必须携带 `MessageSeq`，让 DB 层继续用 `MessageSeq <= DeletedToSeq` 阻止已删除会话被旧消息复活。

如果远端 RPC 失败，事件不能静默丢弃。可重试错误重新入队或返回 sink error；队列满、cache full 或持续失败通过低基数指标和日志暴露。由于 message usecase 当前不因 committed sink error 改变 SENDACK，conversation no-gap guarantee 以 cache admission success 为边界；admission failure 是明确的 degraded state，不能伪装成权威成功。

## 读取流程

```text
HTTP /conversation/list on any node
  -> conversation usecase List(uid, cursor, limit)
  -> authority client resolves UID leader
  -> local leader:
       page authority cache ordered index for uid
       read DB active page window large enough to merge the cache page
       merge authority cache rows for uid
       sort by active_at desc, channel_id, channel_type
       apply cursor and limit
       return authoritative active rows
  -> remote leader:
       ConversationAuthority RPC list_conversations
       remote leader executes the same local merge path
       return authoritative active rows
  -> conversation usecase reads last visible messages for returned rows
  -> conversation usecase returns public ListResult
```

为了避免 cache row 被 DB page 边界截断漏掉，权威读不能只读取 DB 的 `limit` 行再简单 append cache。第一版采用 ordered-cache merge：

1. 从 per-UID ordered cache index 读取 cursor 后最多 `limit + 1` 个 candidate rows，不按任意窗口截断。
2. 从 DB active index 读取 `limit + cacheCandidateCount + 1` 行；如果 DB window 达到配置上限仍无法证明完整合并，返回 `cache_pressure` typed error。
3. 对 cache candidate 中未出现在 DB active page 的 keys，批量读取 DB primary conversation rows。隐藏会话的 `ActiveAt=0` 不会出现在 active index，因此必须用 primary rows 获取 `DeletedToSeq` 和 tombstone/delete barrier。
4. 用 `(uid, channel_id, channel_type)` 去重；同 key 合并时 cache row 只推进 `ActiveAt`、`UpdatedAt`、`SparseActive` 等投影字段，`ReadSeq` 和 `DeletedToSeq` 取 cache 与 DB 的较大值。
5. 如果 DB primary row 或 cache tombstone/delete barrier 说明 `DeletedToSeq >= cache.MessageSeq`，忽略该 cache activation，避免删除后的旧投影复活会话。
6. 统一排序后截取 `limit`。

如果某个 UID cache candidate 数超过可服务上限，List 返回 `cache_pressure`，不返回 partial-cache 成功页。后续 flush 会把 dirty rows 固化到 DB，压力解除后恢复成功 List。

## 错误处理

1. route not ready：List 返回 typed route-not-ready 错误，由 HTTP 层按现有错误映射处理。
2. not leader 或 stale route：authority client 重新 resolve UID route 并短时间重试。
3. cache pressure：List 返回 typed cache-pressure 错误，不退化为 partial-cache 成功页。
4. remote RPC rejected：保留为请求错误，不回退到入口节点本地 DB-only 读，避免返回看似成功但漏数据的结果。
5. last message 读不到：沿用现有语义，只表示该 conversation 当前没有可展示消息，不删除 row。
6. flush 失败：cache row 保留或 requeue，继续对权威 List 可见。
7. lose authority：旧 leader 停止服务对应 target 的 successful List，并先尝试 flush dirty rows；无法确认 ownership 或 flush 完成时返回 route-not-ready/stale-route。

## 配置与观测

新增配置应保持 `WK_` 前缀，并同步 `wukongim.conf.example`：

```text
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS
WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX
WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT
WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT
WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS
WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY
```

建议默认值：

```text
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=4096
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=100000
WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=1000
WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=3s
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS=512
WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY=16
```

配置字段需要英文注释和上下限校验：

```text
CACHE_MAX_ROWS_PER_UID: 0 uses default, valid 1..100000
CACHE_MAX_ROWS: 0 uses default, valid 1..1000000
LIST_DB_WINDOW_MAX: 0 uses default, valid 1..10000 and must be >= maxListLimit + 1
ADMISSION_TIMEOUT: 0 uses default, valid 10ms..5s
HANDOFF_TIMEOUT: 0 uses default, valid 100ms..30s
RPC_TIMEOUT: 0 uses default, valid 10ms..5s
RPC_BATCH_ROWS: 0 uses default, valid 1..5000
RPC_CONCURRENCY: 0 uses default, valid 1..128
```

per-UID 与全局 cache 达到上限时，admission 返回 typed cache-pressure error 并进入 metrics/logging；List 不使用被截断的 cache 伪装成功。

指标保持低基数，不带 UID 或 channel label：

1. authority cache rows、dirty rows、per-UID pressure bucket。
2. authority RPC request/result/duration。
3. list merge DB rows、cache rows、returned rows、has_more。
4. flush rows/result/duration/requeued rows。
5. handoff state transitions and explicit abandon counts.

## 测试

单元测试：

1. authority cache coalesce 保留最新 `ActiveAt`，不降低 `ReadSeq/DeletedToSeq`。
2. local List 合并 DB row 和 cache row，同 key 时 cache 推进投影字段且 read/delete state 取较大值。
3. cursor 后的 cache-only row 能出现在第一页。
4. remote List 通过 node RPC 到 UID leader。
5. not-leader route retry 后改打新 leader。
6. RPC rejected 不回退到 DB-only 成功响应。
7. cache pressure 不返回 partial-cache 成功页。
8. multi-page cursor 在 DB rows 与 cache rows 交错时稳定前进。
9. `DeletedToSeq >= MessageSeq` 时 cache activation 不能 resurrect cleared conversation。
10. batch RPC 按 exact route target 分组，并能处理部分 target stale/not-leader。
11. `SEND -> List before DB flush` 在 cache admission 成功时能看到 conversation。
12. cache-only candidate 会读取 DB primary row，并被 hidden row 的 `DeletedToSeq` fence 拦截。
13. batch RPC result 是 per exact route-target all-or-nothing；单个 target 失败不影响其他 target 的 admission/retry。
14. `ADMISSION_TIMEOUT` 会中止 admission，并作为 non-fatal committed sink error 观测；batch row 和 RPC concurrency 限制被遵守。

装配测试：

1. `internalv2/app` 在 cluster 暴露 conversation authority surface 时注册 RPC handler。
2. single-node cluster List 走本地 authority merge。
3. static multi-node cluster 中，非权威 API 节点查询 UID conversation 时转发到权威节点。
4. leader transfer 后旧 leader 不再返回 stale successful List，新 leader route-not-ready 或权威成功。
5. remote active-row List 失败时，last-message hydration 不在 RPC handler 中重复执行。
6. 新 leader handoff warming 期间不会返回 DB-only stale success；drain/transfer 完成后才成功。

性能测试只做轻量单元或定向测试；三节点 Docker Compose 和 wkbench 类场景按 `docs/development/PERF_TRIAGE.md` 采证后再跑。

## 实施边界

第一版只处理 conversation 生成可见性和 List 权威转发，不扩大到 read ack、delete conversation、pin、mute 等后续会话状态能力。

`internalv2/app/FLOW.md`、`internalv2/access/node/FLOW.md`、`internalv2/infra/cluster/FLOW.md` 需要随实现更新，描述新的 authority cache、RPC 和 List flow。
