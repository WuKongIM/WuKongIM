# wkdb Slot Shard Snapshot Design

## Goal

为 `wkdb` 增加按固定 `slot/shard` 边界导出和恢复业务状态的能力，并将该能力设计为可供 `multiraft` 的 `StateMachine.Snapshot/Restore` 直接调用。

本设计的目标不是复刻 CockroachDB 的完整 snapshot 协议，而是引入一套对当前仓库规模足够简单、边界清晰、后续可演进的 shard snapshot 原语。

约束：

- `multiraft.GroupID == wkdb slotID`
- 不考虑现有 `wkdb` 数据格式兼容性
- 单个 Pebble DB 承载多个 slot
- Raft 持久化状态与业务状态机快照严格分离
- 第一版 snapshot 允许在内存中构建完整 payload，不要求流式协议

## Scope

### In Scope

- 重做 `wkdb` key layout，使所有业务 key 显式带 `slot` 前缀
- 新增 `ShardStore` 或等价 API，使所有业务 CRUD 绑定到一个 slot
- 为单个 slot 计算稳定的业务 key spans
- 导出单个 slot 的业务 snapshot
- 删除单个 slot 的全部业务数据
- 导入单个 slot 的业务 snapshot
- 为 `multiraft` 提供可复用的 `StateMachine` 集成设计
- 为 `multiraft` 提供独立的 `Storage` 持久化布局设计
- 单元测试与 snapshot round-trip 测试

### Out of Scope

- 多版本 snapshot 协议协商
- 跨 slot 事务
- 在线迁移旧 `wkdb` 数据格式
- 压缩、加密、增量 snapshot
- Cockroach 风格 shared SST / external SST snapshot 协议
- 自动 rebalance、迁移调度器
- 将业务 snapshot 与 raft log 合并为单一导出物

## Current State

当前 `wkdb` 只是整库 Pebble 封装，没有 shard 概念：

- [`wkdb/db.go`](../../../wkdb/db.go) 管理单个 Pebble DB 生命周期
- [`wkdb/user.go`](../../../wkdb/user.go) 和 [`wkdb/channel.go`](../../../wkdb/channel.go) 直接在全局 keyspace 上编码和读写
- 现有 key 编码不携带 `slot` 维度，因此无法通过连续 span 精确导出单个 shard 数据

当前 `multiraft` 已经将 Raft 持久化与状态机分离：

- [`multiraft/types.go`](../../../multiraft/types.go) 中 `Storage` 负责 `HardState/Entries/Snapshot`
- `StateMachine` 负责 `Apply/Snapshot/Restore`

因此最合适的落点是：

- `wkdb` 负责 shard 业务状态机数据
- `multiraft.Storage` 负责 Raft 自身持久化

## Recommended Architecture

采用单个 Pebble DB、单 keyspace、多 slot 前缀布局。

推荐顶层 keyspace：

```text
/wk/state/<slot>/<table>/<primary-key...>
/wk/index/<slot>/<table>/<index>/<index-key...>/<primary-key...>
/wk/meta/<slot>/<name...>

/wk/raft/<group>/hardstate
/wk/raft/<group>/log/<index>
/wk/raft/<group>/snapshot
```

其中：

- `/wk/state`、`/wk/index`、`/wk/meta` 属于业务状态机快照范围
- `/wk/raft` 属于 Raft 存储范围，不进入业务 snapshot payload

这个划分保证：

- 单个 slot 的业务数据在 Pebble 中形成连续 spans
- snapshot/export/restore、slot 删除、slot 迁移都可复用同一组 span 计算函数
- `multiraft` 上层不会意外把 raft log 混进业务快照

## Key Layout

### Business Keys

业务主记录：

```text
[wk-state-prefix][slot:u64][tableID:u32][primary-key...][familyID:u16]
```

业务索引记录：

```text
[wk-index-prefix][slot:u64][tableID:u32][indexID:u16][index-key...][primary-key...]
```

slot 内业务元数据：

```text
[wk-meta-prefix][slot:u64][name...]
```

要求：

- 同一 slot 内所有业务 key 的编码必须保持稳定且全序
- 不允许任何业务 API 绕过 slot 前缀直接写全局 key
- 未来新增表、索引、meta 项时，必须继续落在对应 slot 前缀下

### Raft Keys

Raft 持久化状态单独编码：

```text
[wk-raft-prefix][group:u64][kind]
[wk-raft-prefix][group:u64][log-prefix][index:u64]
```

建议最少覆盖：

- `hardstate`
- `applied-index`
- `conf-state` 或由 snapshot metadata 还原的必要字段
- raft log entries
- raft snapshot metadata

## Public API Changes

建议新增：

```go
type ShardStore struct {
    db   *DB
    slot uint64
}

func (db *DB) ForSlot(slot uint64) *ShardStore
```

推荐将现有类型化 API 挂到 `ShardStore`：

```go
func (s *ShardStore) CreateUser(ctx context.Context, u User) error
func (s *ShardStore) GetUser(ctx context.Context, uid string) (User, error)
func (s *ShardStore) UpdateUser(ctx context.Context, u User) error
func (s *ShardStore) DeleteUser(ctx context.Context, uid string) error

func (s *ShardStore) CreateChannel(ctx context.Context, ch Channel) error
func (s *ShardStore) GetChannel(ctx context.Context, channelID string, channelType int64) (Channel, error)
func (s *ShardStore) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error)
func (s *ShardStore) UpdateChannel(ctx context.Context, ch Channel) error
func (s *ShardStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error
```

不推荐继续在 `DB` 上暴露不带 slot 的业务写接口，因为这会让“漏传 shard”成为运行时错误，而不是类型层面的错误。

## Slot Span Model

新增专门模块统一计算 slot spans：

```go
func slotStateSpan(slot uint64) Span
func slotIndexSpan(slot uint64) Span
func slotMetaSpan(slot uint64) Span
func slotAllDataSpans(slot uint64) []Span
```

要求：

- 所有 snapshot/export/restore/delete 逻辑只能依赖这些函数
- 不允许在不同调用点手工拼接前缀
- span 函数必须覆盖该 slot 的全部业务 key，且不与其他 slot 重叠

## Snapshot Export Design

新增底层原语：

```go
type SlotSnapshot struct {
    SlotID uint64
    Data   []byte
    Stats  SnapshotStats
}

func (db *DB) ExportSlotSnapshot(ctx context.Context, slotID uint64) (SlotSnapshot, error)
```

导出流程：

1. 获取 Pebble snapshot
2. 遍历 `slotAllDataSpans(slotID)`
3. 按 key 顺序将每条 `key/value` 编码进入 snapshot payload
4. 返回完整 payload 和统计信息

第一版 payload 采用简单顺序格式：

```text
magic
version
slotID
entryCount
repeated {
  keyLen
  valueLen
  key
  value
}
checksum
```

选择该格式的原因：

- 适合 `multiraft.StateMachine.Snapshot()` 直接返回 `[]byte`
- 容易实现和调试
- 后续可以平滑升级为 SST + manifest 格式，而不影响高层接口

## Snapshot Import Design

新增：

```go
func (db *DB) ImportSlotSnapshot(ctx context.Context, snap SlotSnapshot) error
func (db *DB) DeleteSlotData(ctx context.Context, slotID uint64) error
```

恢复流程：

1. 校验 magic、version、slotID、checksum
2. 删除目标 slot 的现有业务 spans
3. 将 snapshot payload 中的 key/value 重新写入 Pebble
4. 确认写入成功后返回

第一版恢复建议使用单个或分批 Pebble batch `Set` 完成，不要求一开始就生成 SST 并 ingest。原因：

- 当前仓库规模较小，先验证正确性比优化恢复速度更重要
- 如果 snapshot 体积后续增大，可在不改公共 API 的前提下，把导入实现替换成 “生成 SST 后 ingest”

恢复实现必须满足幂等重试语义：

- 任意一次失败的 `ImportSlotSnapshot` 可以被同一个 snapshot 再次重试
- 重试前必须重新清理目标 slot 业务 spans
- `DeleteSlotData + ImportSlotSnapshot` 的组合必须在逻辑上表现为“旧 slot 数据被新 snapshot 完整替换”

## Error Handling

新增或复用错误语义：

- `ErrInvalidArgument`: slot 为 0、payload 非法、slot 不匹配
- `ErrCorruptValue`: snapshot payload 解码失败
- `ErrChecksumMismatch`: snapshot checksum 不匹配
- `ErrNotFound`: 对空 slot 的业务读取

额外要求：

- `ImportSlotSnapshot` 必须拒绝将 `slot=A` 的 snapshot 恢复到 `slot=B`
- `DeleteSlotData` 必须只删除业务 spans，不触碰 `/wk/raft/<group>/...`
- snapshot 导出期间不得暴露半写状态；必须基于 Pebble snapshot 读取

## multiraft Integration

### StateMachine

提供 `wkdbStateMachine`：

- `Apply(ctx, cmd)`：将业务命令应用到 `ShardStore`
- `Snapshot(ctx)`：调用 `ExportSlotSnapshot(slotID)`，返回 `multiraft.Snapshot`
- `Restore(ctx, snap)`：调用 `ImportSlotSnapshot(...)`

其中：

- `Snapshot.Index/Term` 由 `multiraft` 调用方填充
- `Snapshot.Data` 直接承载 `SlotSnapshot.Data`

### Storage

提供单独的 `wkdbRaftStorage` 实现 `multiraft.Storage`：

- `InitialState`
- `Entries`
- `Term`
- `FirstIndex`
- `LastIndex`
- `Snapshot`
- `Save`
- `MarkApplied`

要求：

- Raft 持久化 key 只放在 `/wk/raft/<group>/...`
- 业务 snapshot 与 raft storage 的持久化边界不可混淆
- `Storage.Snapshot()` 返回的是 Raft snapshot metadata 与 payload，不是“扫业务表”的替代实现

## Package Layout Changes

建议新增文件：

```text
wkdb/
  shard.go
  shard_spans.go
  snapshot.go
  snapshot_codec.go
  raft_storage.go
  raft_state_machine.go
```

现有文件的职责调整：

- `db.go`: Pebble 生命周期、基础读写辅助、`ForSlot`
- `codec.go`: 业务 key/value 编码，增加 slot-aware 编码入口
- `user.go`: 改到 `ShardStore`
- `channel.go`: 改到 `ShardStore`
- `catalog.go`: 保持静态表描述，不承担 slot 逻辑

## Implementation Sequence

推荐顺序：

1. 引入 slot-aware key layout
2. 新增 `ShardStore`，将现有 `user/channel` CRUD 迁移到按 slot 操作
3. 新增 slot span 计算模块
4. 实现 `ExportSlotSnapshot`
5. 实现 `DeleteSlotData` 与 `ImportSlotSnapshot`
6. 增加 snapshot round-trip 测试
7. 实现 `wkdbStateMachine`
8. 实现 `wkdbRaftStorage`
9. 将 `multiraft` demo 或测试接到新实现

## Testing Strategy

必须覆盖：

- 同一 slot 下 `user/channel/index/meta` 可被完整导出和恢复
- 不同 slot 的数据互不污染
- `DeleteSlotData(slotA)` 不影响 `slotB`
- snapshot round-trip 后业务查询结果完全一致
- `ImportSlotSnapshot` 对错误 slotID、错误 checksum、截断 payload 返回明确错误
- `multiraft.StateMachine.Restore` 后状态机读取结果与 snapshot 导出前一致
- `wkdbRaftStorage` 的 `Save/InitialState/Entries/Snapshot` 满足 `multiraft` 启动与恢复路径

## Open Decisions

本设计中唯一允许在实现时再细化、但不影响总体方案的点：

- snapshot payload 内部字段使用固定宽度还是 uvarint 编码
- restore 第一版的 batch 大小阈值
- `ShardStore` 是否暴露 `SlotID()` 这类辅助方法

这些不改变整体架构，不阻塞 implementation planning。
