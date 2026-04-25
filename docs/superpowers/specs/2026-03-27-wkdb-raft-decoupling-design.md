# wkdb Raft Decoupling Design

## Goal

将当前混在 `wkdb` 包内的 Raft 存储与状态机职责拆开，明确形成三个独立边界：

- `wkdb`：只负责业务数据存储
- `raftstore`：负责 `multiraft.Storage`
- `wkfsm`：负责基于 `wkdb` 的 `multiraft.StateMachine`

本次设计的目标不是一次性补齐持久化 Raft 后端，而是先完成职责解耦，确保包边界纯粹、调用关系清晰、后续实现可以独立演进。

约束：

- `multiraft.GroupID == wkdb slotID`
- `wkdb` 不应再暴露任何 Raft 类型、Raft key 编码或 Raft 存储实现
- 第一版 `raftstore` 只提供内置内存实现
- 第一版允许丢失“重启恢复 Raft 状态”的能力
- `wkfsm` 继续复用 `wkdb` 的业务 snapshot 导入导出能力

## Scope

### In Scope

- 从 `wkdb` 中移除所有 Raft 相关类型和实现
- 新增 `raftstore` 包，并提供内置内存实现
- 将原 `wkdbraft` 的状态机能力迁移并改名为 `wkfsm`
- 将命令编解码从 `wkdb` 挪到 `wkfsm`
- 调整仓库内所有调用方与测试到新包边界
- 删除依赖 `wkdb` 持久化 Raft 状态的测试

### Out of Scope

- `raftstore` 的持久化实现
- Raft 日志压缩、外部 WAL、SST 导入导出
- `wkdb` 业务模型扩展
- 变更 `multiraft` 的 `Storage` / `StateMachine` 抽象
- 修改当前业务 snapshot payload 格式

## Current State

当前边界存在两个问题：

1. `wkdb` 同时承担业务存储和 Raft 存储职责
2. `wkdbraft` 主要只是适配层，语义不够稳定

当前具体表现：

- `wkdb/raft_storage.go` 在 `wkdb` 内部实现了 `RaftStorage`
- `wkdb/raft_state_machine.go` 在 `wkdb` 内部实现了 `RaftStateMachine`
- `wkdb/raft_types.go` 在 `wkdb` 暴露了一套仅为适配 `multiraft` 存在的类型
- `wkdbraft/adapter.go` 只是把 `multiraft` 调用转发到 `wkdb`

这导致：

- `wkdb` 依赖了不属于其核心职责的 Raft 语义
- 包名与职责不匹配，阅读成本高
- 后续想增加新的 Raft 存储后端时，边界不干净
- 业务库测试和 Raft 测试容易缠绕

## Recommended Architecture

推荐拆成以下三个包：

### `wkdb`

职责：

- `DB` 生命周期
- `ShardStore` / slot-scoped 业务访问
- `User` / `Channel` CRUD
- 业务 key 编码与 slot spans
- 业务 snapshot 导出导入

明确不再包含：

- `HardState`
- `raftpb.Entry`
- Raft log key 编码
- `multiraft.Storage` 适配
- 状态机命令协议

### `raftstore`

职责：

- 直接实现 `multiraft.Storage`
- 管理 `HardState`、`Entries`、`Snapshot`、`AppliedIndex`
- 先提供内置 `memory` 实现

设计原则：

- 不额外定义一层平行的 store 抽象
- `multiraft.Storage` 就是对外契约
- 后续持久化实现也放在此包中扩展

### `wkfsm`

职责：

- 直接实现 `multiraft.StateMachine`
- 内部基于 `wkdb.DB` 和 `slot`
- 负责业务命令编解码
- 负责业务 snapshot 的 `Snapshot/Restore`

明确不承担：

- Raft log 持久化
- `HardState` 存储
- `AppliedIndex` 管理

## Package Dependencies

目标依赖方向：

```text
multiraft
  ^        ^
  |        |
raftstore  wkfsm
             ^
             |
            wkdb
```

要求：

- `wkdb` 不依赖 `multiraft`
- `wkdb` 不依赖 `raftpb`
- `raftstore` 可依赖 `multiraft` 和 `raftpb`
- `wkfsm` 可依赖 `multiraft` 和 `wkdb`
- 三个包之间不得形成循环依赖

## Public API

### `raftstore`

第一版公开 API：

```go
package raftstore

func NewMemory() multiraft.Storage
```

实现语义：

- 返回一个线程安全的内存存储实现
- 支持 `InitialState`
- 支持 `Save`
- 支持 `Entries`
- 支持 `Term`
- 支持 `FirstIndex`
- 支持 `LastIndex`
- 支持 `Snapshot`
- 支持 `MarkApplied`

第一版不要求：

- 跨进程恢复
- 文件落盘
- 可插拔 backend 注册机制

### `wkfsm`

第一版公开 API：

```go
package wkfsm

func New(db *wkdb.DB, slot uint64) multiraft.StateMachine

func EncodeUpsertUserCommand(user wkdb.User) []byte
func EncodeUpsertChannelCommand(channel wkdb.Channel) []byte
```

设计要求：

- `New` 直接返回 `multiraft.StateMachine`
- 命令编码逻辑留在 `wkfsm`，不再留在 `wkdb`
- `Apply` 必须 fail fast 校验 `cmd.GroupID` 与构造时 `slot` 一致

### `wkdb`

保留现有业务 API：

```go
func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) ForSlot(slot uint64) *ShardStore

func (db *DB) ExportSlotSnapshot(ctx context.Context, slotID uint64) (SlotSnapshot, error)
func (db *DB) ImportSlotSnapshot(ctx context.Context, snap SlotSnapshot) error
func (db *DB) DeleteSlotData(ctx context.Context, slotID uint64) error
```

删除：

- `NewRaftStorage`
- `NewStateMachine`
- `RaftStorage`
- `RaftStateMachine`
- `RaftCommand`
- `RaftSnapshot`
- `RaftBootstrapState`
- `RaftPersistentState`
- 一切 Raft key 编码函数

## Memory Storage Design

`raftstore.NewMemory()` 的内部模型建议与 `multiraft` 现有 fake storage 语义保持一致：

- `BootstrapState`
- `[]raftpb.Entry`
- `raftpb.Snapshot`
- `AppliedIndex`

行为要求：

- `Save` 写入 `HardState` 时覆盖现有 `HardState`
- `Save` 写入 `Entries` 时，如果新 entries 的第一条 index 为 `first`，则保留所有 `index < first` 的旧 entries，删除所有 `index >= first` 的旧 entries，再追加新 entries
- `Save` 写入 `Snapshot` 时更新当前 snapshot
- `Save` 写入 `Snapshot` 时删除所有 `index <= snapshot.Metadata.Index` 的 entries
- `MarkApplied` 更新 `AppliedIndex`
- `FirstIndex` / `LastIndex` / `Term` 行为与 `multiraft` 对现有 fake storage 的假设一致

并发要求：

- 内部必须加锁
- 对外返回的 `Entry`、`Snapshot.Data`、`ConfState` 切片必须做拷贝，避免调用方别名污染

## State Machine Design

`wkfsm` 保留当前业务语义：

- `upsert_user`
- `upsert_channel`

第一版继续使用 JSON 命令格式：

```json
{
  "type": "upsert_user",
  "user": {
    "uid": "u1",
    "token": "t1"
  }
}
```

原因：

- 当前仓库已有测试基础
- 本次核心目标是职责解耦，不是更换命令协议
- 协议改动会扩大测试和迁移范围

`Snapshot/Restore` 设计：

- `Snapshot` 调用 `wkdb.ExportSlotSnapshot`
- `Restore` 调用 `wkdb.ImportSlotSnapshot`
- `wkfsm` 只处理业务数据 payload
- Raft snapshot 的 `Index` / `Term` / `ConfState` 由 `multiraft` 与 `raftstore` 管理

## File Migration Plan

### Delete from `wkdb`

- `wkdb/raft_storage.go`
- `wkdb/raft_state_machine.go`
- `wkdb/raft_types.go`

### Delete from `wkdbraft`

- `wkdbraft/adapter.go`
- `wkdbraft/storage_test.go`
- `wkdbraft/state_machine_test.go`
- `wkdbraft/integration_test.go`

### Add

- `raftstore/memory.go`
- `raftstore/memory_test.go`
- `wkfsm/state_machine.go`
- `wkfsm/command.go`
- `wkfsm/fsm_test.go`

## Testing Strategy

### `wkdb`

`wkdb` 测试只保留业务存储相关内容：

- slot-scoped CRUD
- key 编码
- snapshot 导入导出
- slot spans

要求：

- `wkdb` 测试中不再出现 `raftpb`
- `wkdb` 测试中不再验证 `HardState`、`Entries`、`MarkApplied`

### `raftstore`

新增内存存储测试，至少覆盖：

- 空存储 `InitialState`
- `Save(HardState)`
- `Save(Entries)`
- `Save(Snapshot)`
- `Entries(lo, hi, maxSize)`
- `Term(index)`
- `FirstIndex()`
- `LastIndex()`
- `MarkApplied(index)`
- 并发安全和数据拷贝语义

### `wkfsm`

迁移并保留状态机测试，至少覆盖：

- `Apply(upsert_user)`
- `Apply(upsert_channel)`
- `Snapshot/Restore` round-trip
- slot 隔离
- `cmd.GroupID != slot` 时返回错误
- 非法 JSON / 非法命令类型返回错误

### Integration

调整现有集成测试预期：

- 保留“运行期可正确提案并写入业务状态”的验证
- 删除“依赖内存 store 重启恢复”的验证

## Behavior Changes

本次重构后，需要明确接受一个短期退化：

- `raftstore.NewMemory()` 不提供重启恢复能力

这意味着：

- Runtime 运行期间行为正确
- 进程退出后，Raft log、`HardState`、`Snapshot metadata` 不保留
- 若要恢复重启能力，后续应在 `raftstore` 内增加持久化实现

这不是 bug，而是阶段性能力收缩，用来换取边界纯化。

## Migration Sequence

建议按以下顺序实施：

1. 新建 `raftstore` 包，落地内存实现和测试
2. 新建 `wkfsm` 包，迁移状态机和命令编解码
3. 修改调用方与测试，改用 `raftstore.NewMemory()` 和 `wkfsm.New()`
4. 删除 `wkdb` 中的 Raft 文件和对外 API
5. 删除 `wkdbraft` 包并完成 import 路径替换
6. 运行全量测试，确认无包循环和语义回退

理由：

- 先新增后删除，迁移风险最低
- 有利于在中间状态维持编译通过
- 便于逐步收敛测试失败面

## Error Handling

要求：

- `wkfsm.New(db, slot)` 内部或在首次调用时校验 `slot != 0`
- `Apply` 对非法命令返回稳定错误
- `Apply` 对 `cmd.GroupID != slot` 返回稳定错误
- `raftstore` 在空状态下返回与 `multiraft` 兼容的零值语义

不要求：

- 第一版为 `raftstore` 暴露细粒度自定义错误类型

## Maintainability Notes

本设计的核心收益是减少职责交叉：

- 业务存储问题在 `wkdb` 内解决
- Raft 存储问题在 `raftstore` 内解决
- 业务状态机问题在 `wkfsm` 内解决

这样做后：

- review 更容易聚焦
- 测试边界更清晰
- 后续新增持久化 backend 不会污染 `wkdb`
- `wkdb` 可以继续保持“slot-scoped business store”的单一语义

## Acceptance Criteria

满足以下条件即可进入实现阶段：

- `wkdb` 对外不再暴露任何 Raft 类型或 API
- 新增 `raftstore.NewMemory()` 并通过单元测试
- 新增 `wkfsm.New()` 并通过状态机测试
- 仓库内不再 import `wkdbraft`
- 全量测试通过
- 现有运行期 proposal -> apply -> snapshot 业务路径仍然正确
- “重启恢复能力暂时缺失” 在测试和文档中被明确表达
