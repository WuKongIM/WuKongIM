# Issue #957：单频道消息修改增量方案

状态：服务端已按本文实现，接口细节见 [API 契约](../../specs/message-update-api.md)。官方 SDK 适配和生产容量验收需在各自仓库及部署环境完成。

来源：https://github.com/WuKongIM/WuKongIM/issues/957

代码调查基线：`0160c1f7d068b555c872df79ab6ab57f2cd4bc76`。

实现位于 `codex/message-updates` 分支的 `.worktrees/message-updates` 工作副本。本文替代此前的全频道同步、页面周期整页重读和合并接口草案。

## 1. 范围与保证

- 只整体替换仍保留的普通持久化消息 payload。业务解释内容，不提供 JSON Patch。
- CMD、非持久化、流式消息禁止编辑。CMD 判断同时检查原记录 SyncOnce 和部署实例 CommandCodec，不能硬编码频道后缀。
- 消息 ID、原 message_seq、发送人、频道、发送时间、Expire、setting/header 不变；不增加未读、不修改已读、不改变会话排序、不重新激活隐藏会话。
- 业务后端决定编辑权限和时限；IM 负责目标校验、CAS、幂等、集群持久化和读取可见性。
- 成功表示最新内容、频道更新序号、索引、幂等结果和待通知状态持久提交，不等待所有客户端接收。删除/retention/频道销毁优先，覆盖内容不能复活原消息。
- 只同步最新状态，不提供编辑历史或全设备逐次重放。未打开频道不后台追平；未校准的历史缓存、离线搜索和导出可能包含旧内容。
- 新版官方 SDK 承担提示处理、增量合并和页面校准。旧 SDK 读取到的消息可以是最新内容，但原有缓存不会自动获得新同步能力。

## 2. 接口分工

| 接口 | 职责 | 变化 |
| --- | --- | --- |
| `POST /message/update` | 业务后端修改一条消息 | 新增 |
| `POST /channel/messageupdates` | 获取一个频道的消息修改增量 | 新增 |
| `POST /channel/messagesync` | 按消息序号拉取新消息、历史消息 | 保持分页参数和职责，读取内容应用最新版本 |
| `POST /messages` | 精确查询消息 | 保持查询用途，读取内容应用最新版本 |
| `POST /conversation/list` | 查询最近会话及最新 last_message | 摘要内容应用最新版本 |
| `POST /conversation/sync` | 同步最近会话及 recents | 返回内容应用最新版本，并支持发现消息序号不变的最后消息修改 |

不新增 `/channel/messages/sync`，不向 `/channel/messagesync` 加入编辑游标。既有 `/channel/messagesyncbatch` 保留兼容，新流程不依赖它。不增加 `/message/updateheads` 或全频道发现接口。

`message_seq` 是原消息顺序，`version` 是单条消息的编辑版本，`update_seq` 是一个频道的编辑顺序，`update_cursor` 是服务端签发的同步位置；四者不能混用。SDK 不解析或自行生成游标。

## 3. 业务后端修改消息

Alice 将频道 group-A 的第 100 条消息从“明天 10 点开会”改为“明天 11 点开会”。业务后端检查权限和编辑时限后调用 `/message/update`：

```json
{
  "channel_id": "group-A",
  "channel_type": 2,
  "message_id": "12345",
  "expected_version": "0",
<<<<<<< HEAD
  "expected_content_epoch": "0",
=======
>>>>>>> 1e0a52c43 (docs: update)
  "request_id": "f2fbe748-eefd-4c6a-b31a-5b2a657136fa",
  "payload": "aGVsbG8gd29ybGQ="
}
```

<<<<<<< HEAD
payload 示例仅演示 Base64 编码，不对应上述中文。本版修改 payload 解码后上限为 1 MiB，更新 JSON 请求体上限为 2 MiB；新接口的 message_id、version 等 64 位值用十进制字符串避免 JavaScript 精度丢失。未编辑版本为 0，首次编辑为 1。单聊沿用既有 login_uid/频道规范化约定；UID 不是业务编辑授权声明。

修改请求还必须携带读取正文时的 `X-WK-Content-Epoch`，作为 `expected_content_epoch`。服务端与接收转发的 Slot 节点均校验它，后者在恢复维护准入锁内检查并入队；代数不符返回 409 `content_epoch_conflict`，须重新读取并决策。

服务端先检查原消息的权威 committed 状态，再由频道所属 Slot 在 apply 中校验 expected_version、生命周期和 retention。通过一次 metadata batch 原子完成：
=======
payload 示例仅演示 Base64 编码，不对应上述中文。沿用消息解码后大小上限；新接口的 message_id、version 等 64 位值用十进制字符串避免 JavaScript 精度丢失。未编辑版本为 0，首次编辑为 1。单聊沿用既有 login_uid/频道规范化约定；UID 不是业务编辑授权声明。

服务端先检查幂等和原消息的权威 committed 状态，再由频道所属 Slot 在 apply 中校验 expected_version、生命周期和 retention。通过一次 metadata batch 原子完成：
>>>>>>> 1e0a52c43 (docs: update)

1. 更新最新 payload 和 version。
2. 递增频道 update_seq，更新该消息 last_update_seq。
3. 删除该消息旧更新索引项，插入新索引项。
4. 保存 request_id 对应的请求摘要及首次成功结果。
5. 更新可合并的持久待通知标记。

提交成功返回 message_id、原 message_seq、新 version 和 updated_at_ms。超时或结果未知使用原 request_id 和原参数重试；相同 ID 改参数报幂等冲突。版本冲突须重新读取并由业务重新决策，换新 request_id，不自动覆盖别人修改。

原始 Channel log 不直接覆盖，因为现有日志条目标识包含 payload。覆盖表与原消息不在同一跨引擎事务，必须使用权威生命周期/retention 校验并在读取时确认原消息仍存在且可见。

## 4. 在线提示与增量读取

持久 worker 在提交后发送 `message_updated` EVENT 提示，逻辑字段包括频道、message_id、version，不携带正文。提示不进入聊天历史、不占 message_seq、不增加未读，也不修改 CMD。

待通知标记可合并连续编辑；派发版本 v 后只能条件清理 v，不能清掉随后提交的 v+1。允许提示重复或丢失，不建设逐设备永久 ACK 队列。通知只发给支持该类型的在线连接，沿用源频道在线接收者路由，不授予读取权限。

Bob 正在查看 group-A，保存有上次修改游标。SDK 合并重复提示后调用 `/channel/messageupdates`：

```json
{
  "channel_id": "group-A",
  "channel_type": 2,
<<<<<<< HEAD
  "login_uid": "bob",
=======
>>>>>>> 1e0a52c43 (docs: update)
  "update_cursor": "opaque-cursor",
  "limit": 100
}
```

响应数据包含 `updates`、`next_update_cursor`、`more`、`reset_required`。正常增量的 updates 每项包含消息身份、原 message_seq、最新 payload、version、updated_at_ms；只返回当前调用者仍有权读取且原消息仍存在的记录。

SDK 在同一本地事务中合并消息内容、仍指向该消息的会话摘要，并保存 next_update_cursor。旧版本响应不能覆盖高版本内容；more 为 true 时继续同频道分页。即使 updates 为空，也须按服务端返回的游标及 more 处理，不能自行判断已扫描完毕。

未缓存的旧消息不必因此创建聊天气泡或下载其前后历史；以后展示该历史页时从服务端读取最新内容。普通新消息仍由原收发和 `/channel/messagesync` 路径处理。

## 5. 进入频道、离线恢复与历史翻页

### 已有有效修改游标

用户进入 group-A 后，SDK 按既有消息进度补新消息，同时从保存的 update_cursor 补该频道的修改增量。两类响应按 message_id/version 合并；消息修改不推进普通消息接收或已读进度。

停留期间，提示、切回前台和网络重连触发当前频道增量查询。同频道最多一个在途修改查询，合并触发、失败退避。离开频道停止主动同步；不得在登录或重连时遍历用户全部频道。

为补偿用户持续停留时丢失的提示，可配置仅针对当前可见频道的低频增量校准，周期须容量验证。它查询修改增量，不反复拉整页正文；不承诺弱网或后台硬时效。

### 首次进入、游标丢失或失效

<<<<<<< HEAD
初始化模式：`update_cursor` 为空时，服务端取得当前频道更新位置 H，返回 `reset_required: true`、空 updates 和初始化游标，不默认补发全部历史修改。
=======
建议明确初始化模式：`update_cursor` 为空时，服务端取得当前频道更新位置 H，返回 `reset_required: true`、空 updates 和初始化游标，不默认补发全部历史修改。
>>>>>>> 1e0a52c43 (docs: update)

SDK 必须先取得这个初始化位置，再按已有历史接口读取当前要展示的页面。成功后在本地事务保存页面和初始化游标。读取失败不完成初始化。这样 H 之后并发发生的编辑会由下一轮增量补齐，不能先读页面、再直接跳到后来取得的最新游标。

失效时旧缓存标记未校准；重新展示旧历史页必须向服务端读取。不能把初始化到 H 描述为设备全部历史已同步。服务端返回恢复代际变化时，SDK 重置跨代版本比较，防止旧高版本压制恢复后的内容。

### 翻到旧历史

Carol 翻到第 90–110 条时，仍调用 `/channel/messagesync` 的原有范围分页能力：

```json
{
  "login_uid": "carol",
  "channel_id": "group-A",
  "channel_type": 2,
  "start_message_seq": 90,
  "end_message_seq": 111,
  "limit": 21,
  "pull_mode": 1
}
```

起点包含、终点排除，响应按原有可见性及预算返回最新内容。成功后合并当前页；不能以本地已有缓存为由跳过未校准页面。不得把未完成分页中的缺失当作删除。编辑增量不替代现有权限、retention 或删除校准语义。

## 6. 最近会话列表

用户明确要求 `/conversation/list` 和 `/conversation/sync` 都支持消息修改，不能只改前者。用户只查看会话列表时，沿用 SDK 对应的会话接口读取当前页，不为每行额外调用 `/channel/messageupdates`。两入口复用同一最新内容读取能力。

`/conversation/list` 在 last_message 中返回最新 payload 和消息 version。`/conversation/sync` 返回的 recents 同样合并最新 payload 和消息 version；此外必须能返回当前页最后消息的修改，即使该频道没有新消息、message_seq 未前进。不能只在已有 recents 上替换 payload 后就宣称支持完成。

<<<<<<< HEAD
实现保留 SyncLegacy 的原响应数组、会话 Version 和 last_msg_seqs 规则。当前尾消息的消息 version 大于 0，且客户端序号已追到尾部时，recents 从尾消息前一序号开始读取，确保这条已编辑尾消息仍返回。它可能在后续会话刷新中重复返回；SDK 必须按 message_id/version 合并，不能累加未读或当作新消息。msg_count=0 仍不请求消息正文，only_unread 等原筛选不扩大。
=======
当前 SyncLegacy 按 last_msg_seqs 的排他序号读取 recents，并跳过 recents 为空的会话；会话 Version 又来自原消息时间戳。这些值不会因编辑变化，因此需要补充独立于新消息筛选的摘要刷新路径，且不得修改原消息序号、时间或会话 Version 的既有含义来伪装新消息。具体兼容字段/启用方式应在实现契约中明确，保留旧响应数组形状，并验证旧 SDK；本设计尚不宣称已有透明兼容实现。
>>>>>>> 1e0a52c43 (docs: update)

若第 100 条仍是 group-A 的最后消息，其编辑更新该行摘要；若第 101 条已成为最后消息，第 100 条的编辑不能覆盖第 101 条摘要。SDK 应用异步响应时核对最新消息身份和版本。

可见会话的最后消息收到提示后，合并刷新当前会话页；打开列表、回前台、重连以及有容量预算的低频可见页校准补偿漏提示。重读使用该页原始分页条件，不能从上一页末尾 cursor 往后翻来代替当前页刷新。

不修改 active_at、发送时间、排序、未读、read_seq 或隐藏状态。服务端在读取时拼装摘要，不向十万群成员的会话记录逐人写入新 payload。两个会话接口都只承担当前请求范围的会话/摘要刷新，不承担频道全部历史修改同步；后者仍属于 `/channel/messageupdates`。

两入口均须验证：客户端已收到第 100 条、last_msg_seqs 已到 100，此时只编辑第 100 条且无新消息，仍能通过所使用的会话接口刷新摘要；随后第 101 条到达时，旧编辑结果不能覆盖它。`/conversation/sync` 的请求 version、分页、only_unread、排除类型与旧响应解码需覆盖兼容回归，不能通过强制未读或扩大查询范围绕过问题。

## 7. 有序索引、分页与一致性

权威状态位于同一 Channel-owned Hash Slot，默认 256 个 Hash Slot，单节点集群与多节点集群使用相同语义。最新覆盖表只保存每条消息的最新编辑；索引为 `(channel_id, channel_type, last_update_seq, message_id)`。幂等记录仍随成功请求数增长，不能把总存储描述为每条消息恒定一行。

举例：客户端已到 30；消息 A 修改为 31，B 修改为 32，A 再修改为 33。在没有分页读取介入时，后续查询返回 B 的最新状态和 A 的最新状态，不返回 A 的中间版本 31。

每轮固定上界 H，在 `(after, H]` 范围执行有界索引扫描。分页游标编码该上界和实际扫描位置；过滤不可见/已删除记录后仍须正确推进，不能只按返回记录数推断进度。扫描完毕才推进到 H。

这是最新状态索引，不是历史快照。消息再次修改后索引可能移到 H 之后，该消息由下一轮同步获取；不能声称所有轮内记录都代表 H 时刻的历史内容。索引与覆盖读取必须有一致的读取视图或等效校验，避免拼装互不对应的序号与 payload。

游标需要校验频道、调用者可见性上下文、频道生命周期和恢复代际。权限/生命周期变化导致进度不可继续时返回 reset_required，禁止静默跳过缺口。普通重启、leader 切换不改变恢复代际。

所有内容查询先执行原消息存在性、权限和 Message Visibility Floor/retention 约束，再批量合并覆盖内容；覆盖读取失败不能把旧 payload 标成最新。保留原 committed-history 和 persisted-preview 的前沿语义，不能为了会话预览激活 Channel runtime。

<<<<<<< HEAD
本版覆盖读取按物理 Slot 合并，每组通过本节点 leader 的 ReadIndex 确认 quorum，等待 durable apply 追平，再读取固定引擎快照并复核路由。最大四个受管理的并行读任务；累积结果前检查总字节数。每个 Slot 最多保留 256 个尚未确认的读请求，取消或暂时失败不能释放 Raft 内仍在排队的请求计数。新 leader 尚未持久提交本任期记录时拒绝读取。生产路径不为读请求追加 noop；仅未提供 ReadIndex 的自定义嵌入端口保留保守 noop 回退。不能用 readiness 缓存代替目标读屏障。
=======
当前 Slot leader 路由后直接 DB Get 尚未证明为线性一致读。实现需要每个目标 Slot 的提交/应用/authority 一致读屏障；可先验证目标 Slot noop proposal 基线，再评估 ReadIndex。不能用少数代表 Slot 的 readiness 缓存代替目标读屏障，也不能每条消息单独写 noop。
>>>>>>> 1e0a52c43 (docs: update)

## 8. 性能与边界

- 修改增量按频道更新索引 seek/range scan，禁止扫描整个频道历史或逐条检查更新时间。工作量取决于本页有界扫描、可见性检查及返回 payload，而非频道总消息数。
- 无变化只返回空增量和游标，减少正文读取与传输，但鉴权、路由、索引定位、读屏障和网络请求仍有成本。
- 用户有 1000 个频道时，只同步当前打开的频道；积压仍可能需要该频道多次分页，不能承诺进入频道永远只发一次请求。
- 最新内容只写共享状态，不按十万群成员放大写入。在线提示仍有 fanout，大量用户同时查看频道仍会触发大量读取，必须合并提示、加入抖动、有界并发与背压。
- 历史和会话查询也需读取最新覆盖。按 Slot 合并读取，但一次会话页可能跨多个 Slot，读屏障及 RTT 无法全部变成一次内部操作。
- 现有 `/messages` 虽支持多个 selectors，内部仍逐项调用 reader；若实现需要批量精确读取，必须做真实内部批量，不能只降低 HTTP 次数就宣称性能完成。
- 页数、索引扫描、响应字节、payload 解码、队列长度、并发和内存分配均须有界。更新后的 payload 重新计入既有字节预算，不得只按原消息大小分页。
- 内容覆盖、更新索引和待通知随原消息清理；幂等结果随目标保留并允许目标删除后回收。采用最新状态索引，不引入固定 7 天编辑事件日志。

增量校准示例：10 万个前台可见频道页面按 30 秒间隔校准，约 3333 次请求/秒，即使没有修改仍需服务这些请求。可见会话列表刷新另计；不能把聊天增量变轻等同于整套系统负载消失。30 秒仅为估算示例，尚非经压测确认的默认值。

验收覆盖未编辑频道、稀疏/密集修改、同消息连续编辑、冷节点、跨 Slot 会话页、大 payload、突发重连和十万成员群；同时评估 QPS、p95/p99、CPU、分配、网络、Raft/磁盘延迟、队列稳定性和拒绝率。沿用现有会话查询预算及零 runtime-load/无 membership 写入约束，禁止放大上限掩盖退化。

## 9. 实现和验证边界

仅新增两个 HTTP 接口及 EVENT 类型，但仍需完成存储、强读、生命周期和 SDK 工作。access 只做入口，usecase 编排业务，infra 实现窄 ports，Slot/FSM 原子持久化，app 装配，不引入全局聚合服务。

优先验证：CAS 与幂等、无 quorum 不成功、提交后崩溃恢复、通知条件清理、分页期间连续编辑不永久漏同步、初始化并发窗口、数据与游标本地原子提交、旧响应不回退内容、最后消息与新消息竞态、权限/retention/频道重建、备份恢复代际，以及只同步当前频道。

元数据 schema、命令兼容、迁移、snapshot、backup/restore 和分批清理须覆盖所有新表和索引；混合版本节点不可静默忽略新内容。分别验收 JavaScript、Android、iOS、Flutter、HarmonyOS 官方 SDK 的实际维护版本。

<<<<<<< HEAD
实施前读取目标包适用 AGENTS/FLOW，按仓库规则冻结上下文，完成相关 unit/integration/E2E 和性能检查，更新 CHANGELOG、受影响 FLOW 及用户文档。服务端已增加定向 unit/integration 和索引 benchmark；测试结果及未覆盖的生产负载边界记录在 API 契约中。未修改任何外部官方 SDK 仓库，不能将服务端验证描述为全端交付。
=======
实施前读取目标包适用 AGENTS/FLOW，按仓库规则冻结上下文，完成相关 unit/integration/E2E 和性能检查，更新 CHANGELOG、受影响 FLOW 及用户文档。本次仅整理设计，未执行功能或性能测试，不代表功能已交付。
>>>>>>> 1e0a52c43 (docs: update)

## 附录：调查规则快照

以下文件 SHA-256 固定于上文代码基线，实施时重新发现适用规则。

| 文件 | SHA-256 |
| --- | --- |
| `AGENTS.md` | `c6eae7244b1c660be4b2a62cbdc1efa5c26c35e34972e994df7e6ae073fc9d0b` |
| `internal/access/api/FLOW.md` | `67c41156aadaad7c893e5cd4abda480fd3154288d686cf8e5838f537957cc6c7` |
| `internal/usecase/message/FLOW.md` | `e9c96aeaf0779d1a51823ad083bf6a1767e40528ab8f8e62be68b730b6417798` |
| `internal/usecase/conversation/FLOW.md` | `e63c206f385ecabdfc069719e45cca3fb2911fb0e46083429b4aa4c4bf4a0b50` |
| `internal/infra/cluster/FLOW.md` | `a8aa20c3ba298cb9fdc1d0c400601b61d2407ce9d6ff26ee441e5647d4672ab0` |
| `pkg/channel/FLOW.md` | `5a41bd6725a6b357b525e47f588c0b910a9d8f47ad6e2249a61b87e8717c36a4` |
| `pkg/cluster/FLOW.md` | `04ac5e1883f3d99ff2e91d42971a696cf98eb066ecd1e1a113161ab22f6b3cd0` |
| `pkg/db/FLOW.md` | `9a3546de6bcf19cc0534eba7f67955a8b5d41d435619e49b1a733b6a4c1050da` |
| `pkg/db/message/FLOW.md` | `11596aea7e30db3aa2dc6c976b636c1a8c3781456c93be1ab70af8762716fdb0` |
| `pkg/db/meta/FLOW.md` | `ccb5864426a041dedcfd879178def845eeef27f0c9083c0e91e231bc371f3704` |
