# Channel Structured Message Table Storage Design

## 概述

本设计将 `pkg/channel/store` 从“按 offset 把 durable message 整段 `[]byte` 直接写入 Pebble”的日志键值模型，升级为“按 channel 分区管理的 catalog-driven message table”模型。消息将以结构化行的形式落盘，并由表级二级索引直接支持 `MessageSeq` 范围读、`MessageID` 精确查、`ClientMsgNo` 精确查，以及 `(FromUID, ClientMsgNo)` 幂等命中。

目标不是仅仅把原始 `[]byte` 换一种编码方式，而是让 `message` 成为真正可扩展的存储表：后续新增字段、拆列族、补索引或扩展其他消息相关表时，不需要继续修改脆弱的手写 durable payload 布局。

本项目尚未上线，因此本次改造不考虑旧磁盘格式兼容。新的结构化消息表将直接替换当前 `logstore` 的原始日志值模型。

## 目标

- 用结构化 `message` 表替换当前 raw durable payload 主存储。
- 让 `MessageID`、`ClientMsgNo`、`(FromUID, ClientMsgNo)` 查询直接命中索引，不再依赖日志扫描后过滤。
- 保持现有 channel runtime / replica 的核心语义不变，包括 `LEO`、`HW`、`CheckpointHW`、`Truncate(to)`、`StoreApplyFetch(...)`。
- 让消息存储具备 catalog 化描述，便于后续扩列、扩索引、扩消息相关表。
- 统一本地 append 与 follower apply fetch 的消息持久化逻辑，避免双轨编码。

## 非目标

- 不兼容当前已经存在的旧 Pebble 消息日志格式。
- 不在本次设计中重写 `pkg/channel/replica`、`pkg/channel/transport` 的网络复制协议。
- 不把 `pkg/slot/meta` 的实现直接抽成全局通用表引擎；这里只复用 catalog 思想，不复用整套元数据库实现。
- 不在本次设计中新增超出当前需求的全文检索、按时间范围检索、按 topic 检索等额外索引。

## 方案

### 方案 A：结构化主表 + 兼容层保留 `channel.Record` 接口（推荐）

磁盘主存储升级为结构化 `message` 表；`Append([]channel.Record)`、`Read(...)`、`ReadOffsets(...)`、`StoreApplyFetch(...)` 等现有接口继续保留，但只作为 ingress/egress 兼容层存在。写入时先把 `Record.Payload` 解码为结构化 `messageRow`，读取时再把 `messageRow` 临时编码回 `Record.Payload`。

优点：

- 磁盘 source of truth 已经完全切到结构化消息表。
- `pkg/channel/store`、`pkg/channel/handler` 可以先完成主要价值改造，不需要同步重写 transport / replica RPC 编码。
- 改造边界清晰，适合当前仓库状态。

缺点：

- `channel.Record` 仍会在兼容层存在一段时间，读写有一次行对象与 payload 之间的转换成本。

### 方案 B：端到端把 `channel.Record` 也升级为结构化消息对象

消息不仅在磁盘上结构化持久化，复制层、transport 层、handler 层也全部改成直接传递结构化消息对象，不再使用 durable payload bytes。

优点：

- 模型最彻底，系统内没有“结构化对象 / durable bytes”双重表示。

缺点：

- 会扩大改动面到 `pkg/channel/replica`、`pkg/channel/transport`、相关 RPC codec 和测试桩，显著增加风险与工期。

### 方案 C：结构化主表并进一步拆成多张消息子表

除 `message` 主表外，再预先拆出如 `message_payload`、`message_ext`、`message_delivery` 等多表模型。

优点：

- 在理论上更适合远期复杂检索和冷热分离。

缺点：

- 当前需求还没有逼出多表拆分的必要性，容易过度设计。
- 会增加写放大、删除和一致性维护复杂度。

## 推荐方案

采用方案 A。

这满足“消息必须是表”的设计目标，同时将改动集中在 `pkg/channel/store` 与直接依赖它的 `pkg/channel/handler`。磁盘主模型将彻底放弃 raw durable payload；`channel.Record` 仅作为过渡兼容接口保留，避免同步重写复制协议带来的额外风险。

## 设计细节

### 1. 总体架构

`pkg/channel/store` 将新增自己的 catalog/runtime 层，但只实现 message store 需要的最小子集。设计上复用 `pkg/slot/meta/catalog.go` 的声明式 schema 思路，而不直接复用其实现。

核心分层如下：

- `catalog.go`：定义 `TableDesc`、`ColumnDesc`、`ColumnFamilyDesc`、`IndexDesc` 以及 `message` 表描述。
- `message_row.go`：定义持久化行模型 `messageRow` 以及与 `channel.Message` / `channel.Record` 的转换。
- `table_codec.go`：负责编码/解码表主键、列族值、索引键和值。
- `message_table.go`：提供 message 表级别的 append/get/scan/truncate/index maintenance 能力。
- `message_log.go`、`commit.go`：改为调用 message table，不再直接写 raw durable payload。
- `handler` 侧读取和查询逻辑直接走 message table / secondary indexes。

磁盘上的 source of truth 是 `message` 表行，不再是原始 `[]byte` 日志值。

### 2. 键空间布局

当前 `pkg/channel/store` 的 Pebble 键空间分散为 `Log / Checkpoint / History / Snapshot / Idempotency`。改造后将重排为 3 类键空间：

- `State`：表主记录。
- `Index`：表二级索引。
- `System`：checkpoint、epoch history、snapshot 等非 message row 系统状态。

推荐键布局：

```text
State  : [0x10][channelKey][tableID][primaryKey...][familyID]
Index  : [0x11][channelKey][tableID][indexID][indexColumns...][primaryKey...]
System : [0x12][channelKey][systemKind...]
```

其中：

- `channelKey` 继续作为 channel store 的逻辑分区边界。
- `message` 表是 `State/Index` 中的第一张表。
- `Checkpoint`、`EpochHistory`、`SnapshotPayload` 继续保留为 `System` 区对象，而不是塞进 `message` 表。

### 3. `message` 表 schema

`message` 表是本次改造的唯一业务表，按 channel 分区存储。表内主键使用 `message_seq`，而不是旧的内部 `offset`。

#### 3.1 主键

- Primary key: `message_seq`

虽然 runtime / replica 仍以 offset 语义驱动，但 store 层的业务主键以 `message_seq` 暴露更合理。内部转换规则固定为：

- `message_seq = offset + 1`
- `offset = message_seq - 1`

这允许上层仍维持现有 `LEO`、`Truncate(to)`、`FetchOffset` 语义，而持久化模型围绕消息序号组织。

#### 3.2 列定义

首批列如下：

- `message_seq`
- `message_id`
- `framer_flags`
- `setting`
- `stream_flag`
- `msg_key`
- `expire`
- `client_seq`
- `client_msg_no`
- `stream_no`
- `stream_id`
- `timestamp`
- `channel_id`
- `channel_type`
- `topic`
- `from_uid`
- `payload`
- `payload_hash`

说明：

- `payload_hash` 作为明确列持久化，不再临时从 durable bytes header 中解析。
- `channel_id` 虽然与 store 的 `channelKey` 可相互推导，但仍保留成列，便于将来做导出、离线校验或跨系统复制。

#### 3.3 列族

首版使用 2 个列族：

- `primary`：除 `payload` 外的全部元数据列。
- `payload`：只包含 `payload`。

原因：

- 常见查询如按 `MessageID`、`ClientMsgNo`、幂等查重，不必先读取大 payload。
- 后续新增字段时，可以根据访问频率把字段放入合适 family，而不是不断膨胀单个 binary blob。

#### 3.4 二级索引

首批索引如下：

- `uidx_message_id`
  - Unique
  - Columns: `message_id`
  - `message_id` 必须为非零；若解码结果为零则视为损坏或无效输入，直接拒绝入库
  - Value: `message_seq`（必要时附带少量校验字段）

- `idx_client_msg_no`
  - Non-unique
  - Columns: `client_msg_no`, `message_seq`
  - 仅当 `client_msg_no != ""` 时写入
  - 用于按 client message number 的精确匹配、按 `message_seq` 排序分页查询

- `uidx_from_uid_client_msg_no`
  - Unique
  - Columns: `from_uid`, `client_msg_no`
  - 仅当 `from_uid != "" && client_msg_no != ""` 时写入
  - Value: `message_seq`, `message_id`, `payload_hash`
  - 直接承载幂等约束

当前独立的 `idempotency` keyspace 将被移除；幂等能力不再是消息存储之外的一张额外表，而是 `message` 表上的唯一索引约束。

### 4. 行编码与 catalog runtime

本设计不再为 durable message 定义一整块固定 header + 变长字段的单体 binary layout。相反，每个列族值使用“带列 ID 的列块编码”：

```text
[version][columnID][valueLength][value]...
```

编码要求：

- 每个 family 的值都带版本号，便于后续扩展。
- 每列用 `columnID` 标识，而不是依赖固定字段顺序。
- 未识别列在读取时允许跳过，支持向前兼容的字段扩展。

这样后续新增列时，主要变更集中在：

- `catalog.go` 更新表 schema
- `message_row.go` 补持久化字段
- `table_codec.go` 补列编解码

不需要再整体重写一段容易出错的手写 durable payload 布局。

### 5. 持久化行模型与 API

store 内部新增持久化行模型 `messageRow`。它与 `channel.Message` 接近，但职责不同：

- `channel.Message` 是业务消息对象。
- `messageRow` 是存储行对象，带明确的持久化元信息。

`messageRow` 至少包含：

- `MessageSeq`
- 全部 message 持久化字段
- `PayloadHash`

#### 5.1 主要内部 API

`ChannelStore` 需要逐步演进出以表语义为中心的接口，例如：

- `AppendMessages(rows []messageRow) (baseSeq uint64, error)`
- `GetMessageBySeq(seq uint64) (messageRow, bool, error)`
- `GetMessageByMessageID(messageID uint64) (messageRow, bool, error)`
- `ListMessagesBySeq(...) ([]messageRow, error)`
- `ListMessagesByClientMsgNo(...) ([]messageRow, nextCursor, error)`，其中 `nextCursor` 继续使用与当前 handler 查询一致的 `BeforeSeq` 语义，即“下一页的独占 message_seq 上界”
- `LookupIdempotency(fromUID, clientMsgNo string) (hit, bool, error)`

#### 5.2 兼容 API

现有这些方法先保留，但内部只作为兼容层：

- `Append([]channel.Record)`
- `Read(...)`
- `ReadOffsets(...)`
- `StoreApplyFetch(...)`

语义变化：

- ingress 时：`Record.Payload -> decode -> messageRow`
- egress 时：`messageRow -> encode -> Record.Payload`

因此 `channel.Record.Payload` 只保留为兼容表示，不再是 Pebble 存储真相。

### 6. 写路径

#### 6.1 本地 append

`ChannelStore.Append(records []channel.Record)` 改为：

1. 校验 store 状态。
2. 读取当前最大 `message_seq`，计算本批 `baseSeq`。
3. 将每个 `Record.Payload` 解码为 `messageRow`。
4. 为每个 row 填充本批分配的 `MessageSeq`。
5. 在一个 Pebble batch 中写入：
   - `message.primary` family
   - `message.payload` family
   - `uidx_message_id`
   - `idx_client_msg_no`
   - `uidx_from_uid_client_msg_no`
6. 经 commit coordinator durable sync 后发布新的 `LEO`。

这一步不再写原始 log key。

#### 6.2 Follower apply fetch

`StoreApplyFetch(...)` 与本地 append 使用同一套 message row 持久化原语：

1. `req.Records[*].Payload` 解码为 `messageRow`。
2. `MessageSeq` 不能重新分配，必须严格使用“当前本地 post-truncate `LEO` + 1 + i”规则推导，其中 `i` 是本批记录的顺序下标。
3. 上述 `post-truncate LEO` 由 replica 在进入 `StoreApplyFetch(...)` 前校准，因此它必须与 leader 发送区间的起始 offset/seq 一致；store 侧不得再引入独立的 seq 计数器。
4. 和本地 append 一样写入主表与索引。
5. 同一批次内按需要写入 checkpoint。

这替代当前 `commit.go` 中“先写 raw log，再从 raw payload 中补解析 idempotency 字段”的双轨逻辑。

#### 6.3 幂等写入

当前 Append 热路径是：

- `GetIdempotency(...)`
- 命中后再 `loadMessageViewAtOffset(...)`
- 再比对 payload hash

改造后应变为：

1. 直接查询 `uidx_from_uid_client_msg_no`。
2. 索引值返回 `message_seq`、`message_id`、`payload_hash`。
3. 若 hash 不同，立即返回 `ErrIdempotencyConflict`。
4. 若 hash 相同，再按 `message_seq` 回表读取消息并返回已有结果。

这样幂等命中只需要“一次索引查 + 一次主表查”，而且不再依赖解析旧 raw log value。

### 7. 读路径与查询

#### 7.1 顺序读取

`handler/seq_read.go` 相关逻辑改为直接走 message table：

- `LoadMsg` -> `GetMessageBySeq`
- `LoadNextRangeMsgs` -> `ListMessagesBySeq`
- `LoadPrevRangeMsgs` -> `ListMessagesBySeq` 的逆序扫描版本

不再先 `ReadOffsets(...)` 再 `decodeMessageRecord(...)`。

#### 7.2 消息查询

`pkg/channel/handler/message_query.go` 改造目标：

- 无过滤条件：按 `message_seq` 倒序扫主表。
- `MessageID != 0`：直接命中 `uidx_message_id`。
- `ClientMsgNo != ""`：直接扫描 `idx_client_msg_no`。

因此 `QueryMessages(...)` 不再需要“分页读日志 -> decode -> in-memory 过滤”的路径。

#### 7.3 `ClientMsgNo` 分页语义

为保持现有 `BeforeSeq` 行为，`idx_client_msg_no` 应在索引键中同时带上 `client_msg_no` 与 `message_seq`。这样可以：

- 在同一 `client_msg_no` 下按 `message_seq` 倒序分页。
- 保留 `BeforeSeq` 的游标语义，而不需要在 handler 层手工扫过不匹配数据。

### 8. 截断与索引清理

`Truncate(to)` 仍保持现有语义：删除 `offset >= to` 的日志。映射到新模型中即删除 `message_seq >= to+1` 的消息行。

实现方式：

1. 扫描主表尾部，读取要删除的 `message.primary` family。
2. 从 row 中取出参与索引删除的列：
   - `message_id`
   - `client_msg_no`
   - `from_uid`
3. 删除：
   - `message.primary`
   - `message.payload`
   - `uidx_message_id`
   - `idx_client_msg_no`
   - `uidx_from_uid_client_msg_no`
4. 更新 `LEO`。

不能只删主表而不删索引，否则会留下悬挂索引，破坏幂等和查询正确性。

### 9. 恢复与系统状态

`Checkpoint`、`EpochHistory`、`SnapshotPayload` 继续保留在 `System` 区，不并入 `message` 表。

恢复语义：

- `LEO` 不再从 raw log key 最大 offset 推导，而是从 `message` 表最大 `message_seq` 推导。
- replica/store 对外仍以 offset 驱动；内部统一转换为 `message_seq`。
- `TruncateHistoryTo(...)`、`LoadHistory()`、`LoadCheckpoint()`、`StoreCheckpoint()`、`LoadSnapshotPayload()`、`StoreSnapshotPayload()` 仍保留，但底层 keyspace 切换到新的 `System` 布局。

### 10. 一致性与错误语义

所有 message row 持久化都必须在单个 Pebble batch 中原子完成：

- 主表 family
- 全部二级索引
- 同批次需要的 checkpoint/system 写入

约束语义：

- `message_id` 冲突：视为严重状态错误，应返回存储错误或损坏错误。
- `(from_uid, client_msg_no)` 冲突：
  - `payload_hash` 相同 -> 幂等命中
  - `payload_hash` 不同 -> `ErrIdempotencyConflict`
- `idx_client_msg_no` 是查询索引，不提供唯一约束。
- 当 `client_msg_no == ""` 时，不写入 `idx_client_msg_no`。
- 当 `from_uid == ""` 或 `client_msg_no == ""` 时，不写入 `uidx_from_uid_client_msg_no`；这类消息不参与幂等约束。

### 11. 测试策略

本次改造需要至少覆盖 4 层测试：

#### 11.1 catalog / codec 层

新增测试覆盖：

- `message` 表 schema 描述正确。
- family 编码/解码。
- index 键值编码/解码。
- 新增列时旧值仍可解码。

#### 11.2 store 层

新增或改写测试覆盖：

- append 后按 seq 读取。
- 按 `MessageID` 精确读取。
- 按 `ClientMsgNo` 分页读取。
- 幂等命中与冲突。
- truncate 后主表与索引都被清理。
- restart 后 `LEO`、checkpoint、history 恢复正确。
- `StoreApplyFetch(...)` 与 checkpoint 原子更新。

#### 11.3 handler 层

改写测试覆盖：

- `Append` 幂等命中直接走唯一索引。
- `seq_read` 直接走表，不再依赖 raw log decode。
- `message_query` 对 `MessageID` / `ClientMsgNo` 走索引分页，而不是全量扫描过滤。

#### 11.4 replica / runtime 关联层

回归现有关键测试，确保 store 重构不破坏：

- `HW`
- `CheckpointHW`
- `LEO`
- `ApplyFetch`
- `Truncate`
- 启动恢复与 reconcile 边界

### 12. 代码组织与影响面

预计主要落点：

- `pkg/channel/store/catalog.go`
- `pkg/channel/store/message_row.go`
- `pkg/channel/store/table_codec.go`
- `pkg/channel/store/message_table.go`
- `pkg/channel/store/message_log.go`
- `pkg/channel/store/commit.go`
- `pkg/channel/store/keys.go`
- `pkg/channel/store/idempotency.go`
- `pkg/channel/handler/append.go`
- `pkg/channel/handler/seq_read.go`
- `pkg/channel/handler/message_query.go`
- `pkg/channel/FLOW.md`

其中 `FLOW.md` 需要更新以下事实：

- channel store 不再把消息作为 raw log payload 主存储。
- 幂等表不再是独立 keyspace，而是 message 表的唯一索引。
- 查询路径支持 `MessageID` / `ClientMsgNo` 索引直达。

实现计划必须把测试迁移与 `pkg/channel/FLOW.md` 更新列为显式任务，避免存储实现、查询路径和文档描述脱节。

## 实施边界

本设计只覆盖 `pkg/channel/store` 与直接依赖它的 handler 查询/读取逻辑，不扩展到以下范围：

- transport 层消息复制协议重构
- channel 之外的消息业务表设计
- 面向外部 API 的新查询接口设计

## 结论

本次改造的核心不是把 durable payload 从一个 blob 变成另一个 blob，而是把 `pkg/channel/store` 提升为基于 catalog 的结构化消息表存储：

- `message` 是真正的表。
- `MessageID` / `ClientMsgNo` / 幂等命中是表索引能力。
- raw durable payload 只保留为兼容 ingress/egress 表示。
- checkpoint/history/snapshot 继续作为 system state 管理。

这样既解决当前 raw `[]byte` 不利于检索与扩展的问题，也为后续新增消息字段、消息相关子表和更多索引留出清晰演进路径。
