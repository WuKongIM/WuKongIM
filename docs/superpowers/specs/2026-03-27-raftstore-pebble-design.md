# Persistent Raftstore Pebble Design

## Goal

为 `wraft` 增加一个可持久化的 `raftstore` 后端，使 `multiraft.Storage` 不再只依赖内存实现，并在进程重启后恢复：

- `HardState`
- `raftpb.Entry` 日志
- `raftpb.Snapshot`
- `AppliedIndex`

本次设计的重点不是复制 Cockroach 的完整 Raft log 子系统，而是在当前 `wraft` 运行时语义下，提供一版最小、清晰、可落地的持久化设计。

约束：

- `multiraft.GroupID == wkdb slotID`
- `raftstore` 与 `wkdb` 必须使用独立的 Pebble 实例
- 同一个进程内的所有 group 共享一个 `raftstore` Pebble 实例
- `wkdb` 继续只负责业务数据和业务 snapshot
- `multiraft` 当前运行时顺序保持不变：先 `Save`，再 `Apply`，再 `MarkApplied`

## Scope

### In Scope

- 在 `raftstore` 包内新增基于 Pebble 的持久化实现
- 为所有 group 提供共享的 `raftstore.DB`
- 定义 Raft key layout
- 定义 `Save` / `InitialState` / `Entries` / `Snapshot` / `MarkApplied` 的持久化语义
- 明确崩溃恢复语义
- 为后续 snapshot/truncation 演进预留扩展点

### Out of Scope

- 修改 `multiraft.Storage` 接口
- 修改 `wkfsm` 的业务命令协议
- 本地自动生成 snapshot 的运行时链路
- Raft log compaction 调度
- sideload 大 entry
- 类似 Cockroach 的 append durability ack、lease index、closed timestamp 等扩展元数据

## Current State

当前仓库内：

- `raftstore.NewMemory()` 是唯一的 `multiraft.Storage` 实现
- `multiraft` 在 group 打开时会把持久层内容全部加载到 `raft.MemoryStorage`
- `multiraft` 在 `Ready()` 阶段先调用 `storage.Save(...)`
- committed entry 随后交给 `StateMachine.Apply(...)`
- 最后调用 `storage.MarkApplied(...)`

这意味着当前恢复模型有两个特点：

1. `Storage` 已经是独立职责，但还没有 durable backend
2. Raft 状态持久化与业务状态应用不是单批原子提交

因此，新设计的目标不是“获得 Cockroach 级别的跨子系统原子语义”，而是先补齐“Raft 状态可恢复”。

## Why Not Mirror Cockroach Exactly

Cockroach 的做法是：

- Raft key 与业务 key 共用同一个底层 Engine/Pebble
- 通过 keyspace 区分 Raft 状态和业务状态
- 围绕 `TruncatedState`、sideload、append durability ack 等机制构建完整的 logstore

本仓库当前不适合直接照搬，原因有三点：

1. 包边界已经明确拆成 `raftstore` 和 `wkdb`
2. 当前运行时不具备跨 Raft 持久化与业务 apply 的原子提交语义
3. 第一阶段最重要的是恢复能力，而不是完整的 Cockroach below-raft 机制

所以本设计刻意选择：

- 在包边界上继续保持 `raftstore` 独立
- 在物理存储上也保持独立 Pebble 实例
- 用最小 key model 先完成 durable backend

## Recommended Architecture

推荐结构：

```text
multiraft
  ^        ^
  |        |
raftstore  wkfsm
             ^
             |
            wkdb
```

运行时实例关系：

```text
process
  |- wkdb.DB        -> business Pebble
  |- raftstore.DB   -> raft Pebble
  |- group 1
  |- group 2
  |- group N
```

说明：

- `wkdb.DB` 只负责业务 KV 和业务 snapshot payload
- `raftstore.DB` 只负责 `multiraft.Storage`
- group 不应一组一个 Pebble 文件；group 只通过 key 前缀隔离

## Public API

推荐在 `raftstore` 中提供两层 API：

```go
package raftstore

type DB struct {
    // wraps a single Pebble instance for all raft groups in the process
}

func Open(path string) (*DB, error)
func (db *DB) Close() error
func (db *DB) ForGroup(group uint64) multiraft.Storage

func NewMemory() multiraft.Storage
```

设计理由：

- `Open(path)` 负责 Pebble 生命周期
- `ForGroup(group)` 返回按 group 前缀隔离的 `multiraft.Storage`
- 避免 `NewPebble(path, group)` 这种“一组一个 DB”的错误演进方向
- 保留 `NewMemory()` 作为测试与轻量场景实现

## Key Layout

第一版 key layout 采用固定二进制前缀和大端编码：

```text
0x01 | group(8) | 0x01                 -> HardState
0x01 | group(8) | 0x02                 -> AppliedIndex
0x01 | group(8) | 0x03                 -> Snapshot
0x01 | group(8) | 0x10 | index(8)      -> Entry
0x01 | group(8) | 0x11                 -> TruncatedState   // reserved, phase 2
```

要求：

- 同一 group 的全部 Raft key 必须连续可扫描
- `Entry` key 必须按 `index` 的自然顺序排序
- 第一版不要求 `TruncatedState` 实际启用，但保留 key type 位置

推荐 value 编码：

- `HardState`：直接存 `raftpb.HardState` protobuf
- `Snapshot`：直接存 `raftpb.Snapshot` protobuf
- `Entry`：直接存 `raftpb.Entry` protobuf
- `AppliedIndex`：固定 8 字节无符号整数

第一版不建议额外自定义 envelope。因为当前目标是把持久化语义做对，不是压榨编码空间。

## Storage Semantics

### `InitialState`

读取：

- `HardState`
- `AppliedIndex`
- `Snapshot`

`ConfState` 规则：

- 如果 snapshot 存在，直接使用 `snapshot.Metadata.ConfState`
- 如果 snapshot 不存在，则从已提交的 conf change entry 推导

这要求 Pebble 实现保持与当前内存版兼容的 `ConfState` 推导语义。

### `Entries(lo, hi, maxSize)`

在 `Entry` 前缀上顺序扫描 `[lo, hi)`：

- 只返回连续日志
- `maxSize` 语义保持与现有 `raftstore.NewMemory()` 一致
- 返回的切片及其中的字节内容必须做拷贝

### `Term(index)`

规则：

- 若命中某条 entry，返回其 `Term`
- 若 `index == snapshot.Metadata.Index`，返回 snapshot term
- 否则返回零值

第一版保持现有存储语义，不额外引入复杂错误类型。

### `FirstIndex()`

规则：

- 有 entry 时返回第一条 entry 的 index
- 没有 entry 但有 snapshot 时返回 `snapshot.Metadata.Index + 1`
- 两者都没有时返回 `1`

### `LastIndex()`

规则：

- 有 entry 时返回最后一条 entry 的 index
- 否则返回 `snapshot.Metadata.Index`

### `Snapshot()`

返回完整的 `raftpb.Snapshot`。

注意：

- 这是 Raft 层 snapshot record
- 不是通过扫描业务表来临时构造
- 其中的 `Data` 由上层状态机 snapshot 流程提供

### `Save(st)`

`Save` 必须在一个 Pebble batch 内完成，并 `Commit(pebble.Sync)`。

推荐顺序：

1. 若包含 `Snapshot`：
   - 写入 snapshot record
   - 删除所有 `index <= snapshot.Metadata.Index` 的 entry
2. 若包含 `Entries`：
   - 取第一条新 entry 的 `firstNewIndex`
   - 删除所有 `index >= firstNewIndex` 的旧 entry
   - 写入新 entry
3. 若包含 `HardState`：
   - 覆盖写入 `HardState`

要求：

- `HardState` 与 `Entries` 必须同批提交
- snapshot 覆盖和日志清理必须同批提交
- 对于外部调用者，`Save` 成功意味着该批 Raft 状态已 durable

### `MarkApplied(index)`

`MarkApplied` 单独持久化 `AppliedIndex`，并 `Sync` 提交。

理由：

- 当前 `multiraft` 运行时在 `Apply` 之后单独调用 `MarkApplied`
- 不应伪造一种当前系统并不具备的“与业务 apply 原子提交”语义

## Crash Recovery Semantics

当前系统必须明确接受以下恢复模型：

### Case 1: `Save` 成功，`Apply` 前崩溃

结果：

- Raft 日志与 `HardState` 已持久化
- 业务状态尚未应用
- 重启后 committed entry 会重新 apply

这是预期行为。

### Case 2: `Apply` 成功，`MarkApplied` 前崩溃

结果：

- 业务状态已经变化
- `AppliedIndex` 可能仍停留在旧位置
- 重启后这些 entry 可能再次 apply

这意味着第一版状态机命令必须保持幂等，或者至少对重复 apply 安全。

当前 `wkfsm` 的 `upsert_user` / `upsert_channel` 符合这一要求，因此第一阶段可接受。

### Case 3: `MarkApplied` 成功

结果：

- Raft durable state、业务状态、应用水位在当前模型下达成一致

## Snapshot and Truncation Strategy

第一阶段只持久化已存在的 snapshot record，不新增本地 snapshot 生产流程。

原因：

- 当前 `multiraft.StateMachine.Snapshot()` 虽然存在接口，但运行时尚未驱动本地产生 snapshot
- 在没有本地 snapshot/compact 链路之前，引入完整 `TruncatedState` 收益有限

因此分阶段策略如下：

### Phase 1

- 持久化 `HardState`
- 持久化 `Entries`
- 持久化 `Snapshot`
- 持久化 `AppliedIndex`
- 重启恢复可用

### Phase 2

- 引入本地 snapshot 生成链路
- 持久化并真正使用 `TruncatedState`
- 对日志清理和恢复边界建立更明确语义

### Phase 3

- 视需要引入大 entry sideload
- 视需要引入更丰富的 append durability / callback 机制

## Testing Strategy

`raftstore` Pebble 实现至少需要覆盖：

- 空 DB 的 `InitialState`
- `Save(HardState)` round-trip
- `Save(Entries)` round-trip
- `Save(Snapshot)` round-trip
- `Entries(lo, hi, maxSize)` 窗口语义
- `Term(index)` 与 snapshot 边界
- `FirstIndex()` / `LastIndex()` 语义
- `MarkApplied(index)` 持久化
- 重启后重新 `Open()` 能恢复先前状态
- snapshot 写入后删除 `<= snapshot.Index` 的旧日志
- 新 entry 覆盖旧后缀日志
- 返回值的深拷贝语义

集成测试至少需要覆盖：

- 单节点 group 提案后，关闭 runtime，再重新打开，业务状态能够从持久化 Raft 状态恢复
- `Save` 成功但业务未 apply 的场景，重启后能重新 apply
- `Apply` 成功但 `AppliedIndex` 未推进的场景，重复 apply 仍不破坏状态

## Non-Goals and Guardrails

为避免设计回退，明确以下边界：

- 不把 Raft key 编码塞回 `wkdb`
- 不让 `wkdb.DeleteSlotData()` 影响 Raft DB
- 不做“每 group 一个 Pebble 实例”
- 不把业务 snapshot payload 当成 `Storage.Snapshot()` 的替代来源
- 不在第一版中模拟 Cockroach 的完整 below-raft 模型

## Decision Summary

本设计确定以下决策：

1. `raftstore` 使用独立 Pebble 实例
2. 一个进程内所有 group 共用一个 `raftstore.DB`
3. `wkdb` 继续只负责业务数据
4. 第一版只实现最小持久化 `multiraft.Storage`
5. 接受当前运行时带来的“可能重复 apply”恢复语义
6. `TruncatedState` 保留扩展位，但不在第一阶段启用
