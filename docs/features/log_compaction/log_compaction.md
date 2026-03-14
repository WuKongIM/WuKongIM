# WuKongIM 分布式日志压缩（Log Compaction）实现方案

## 1. 背景与动机

### 1.1 当前问题

WuKongIM 采用魔改 Raft 协议实现分布式共识，系统中存在两类 Raft 日志存储：

- **Slot 日志**：基于 Pebble DB 的 `PebbleShardLogStorage`，存储集群元数据命令日志
- **Channel 日志**：基于 `wkdb` 的消息日志，将 Raft Log 映射为 Channel 消息

当前系统**没有日志压缩机制**，所有已应用的日志永久保留在磁盘上。随着系统长时间运行，将产生以下问题：

| 问题 | 影响 |
|------|------|
| 磁盘空间持续增长 | 已应用的日志不再被状态机使用，但仍占用磁盘 |
| 新节点加入缓慢 | Learner 需要从头同步全部日志，耗时极长 |
| 日志查询性能下降 | Pebble DB 中的日志量膨胀，LSM-Tree 层级增多 |
| LeaderTermStartIndex 元数据膨胀 | 历史 Term 的起始索引永远不清理 |

### 1.2 设计目标

1. 安全删除已应用的历史日志，控制磁盘占用
2. 支持通过快照加速新节点追赶（替代全量日志回放）
3. 对现有 Raft 协议流程的侵入性最小
4. Slot 日志和 Channel 日志分别处理，策略不同

## 2. 系统现状分析

### 2.1 关键索引体系

```
 storedIndex     lastLogIndex
     |                |
     v                v
[已持久化日志] [内存队列日志]
     ^                ^
     |                |
appliedIndex   committedIndex

不变式：
  appliedIndex <= committedIndex <= lastLogIndex
  appliedIndex <= storedIndex <= lastLogIndex
```

- `appliedIndex`：已应用到状态机的最后日志索引
- `committedIndex`：已被多数节点确认的日志索引
- `storedIndex`：已持久化到磁盘的日志索引
- `lastLogIndex`：最后一条日志索引（含内存）

### 2.2 现有存储接口

```go
// pkg/raft/raft/storage.go
type Storage interface {
    AppendLogs(logs []types.Log, termStartIndex *types.TermStartIndexInfo) error
    GetLogs(startLogIndex uint64, endLogIndex uint64, limitSize uint64) ([]types.Log, error)
    GetState() (types.RaftState, error)
    GetTermStartIndex(term uint32) (uint64, error)
    LeaderTermGreaterEqThan(term uint32) (uint32, error)
    LeaderLastTerm() (uint32, error)
    TruncateLogTo(index uint64) error
    DeleteLeaderTermStartIndexGreaterThanTerm(term uint32) error
    Apply(logs []types.Log) error
    SaveConfig(cfg types.Config) error
}
```

### 2.3 现有截断机制（仅用于冲突解决）

当前 `TruncateLogTo` 仅在 Follower 检测到日志冲突时使用，删除冲突索引之后的日志。这是**尾部截断**，而日志压缩需要的是**头部清理**（删除已应用的旧日志）。

## 3. 总体架构设计

```
┌─────────────────────────────────────────────────────────┐
│                     Log Compaction                       │
│                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌────────────┐ │
│  │  Compaction   │    │   Snapshot    │    │  Snapshot   │ │
│  │   Trigger     │───>│   Creator    │───>│   Store    │ │
│  │  (定时/阈值)   │    │  (状态快照)   │    │ (快照存储)  │ │
│  └──────────────┘    └──────────────┘    └────────────┘ │
│         │                                      │         │
│         v                                      v         │
│  ┌──────────────┐                      ┌────────────┐   │
│  │  Log Cleaner  │                      │  Snapshot   │   │
│  │  (日志清理)    │                      │  Sender    │   │
│  └──────────────┘                      │ (快照传输)   │   │
│                                        └────────────┘   │
└─────────────────────────────────────────────────────────┘
```

### 两类日志的不同策略

| 特性 | Slot 日志 | Channel 日志 |
|------|-----------|-------------|
| 存储引擎 | Pebble DB | wkdb (消息表) |
| 日志含义 | 集群命令（配置变更等） | 频道消息 |
| 压缩策略 | 快照 + 日志清理 | 仅日志清理（消息本身即状态） |
| 快照内容 | Slot 状态的完整快照 | 不需要（消息即快照） |
| 清理方式 | 删除 `appliedIndex` 之前的日志 | 删除 `appliedIndex` 之前的日志 |

**关键区别**：Channel 日志的 `Apply` 方法是空操作（`return nil`），因为消息在 `AppendLogs` 时已经直接写入 `wkdb`，消息本身就是最终状态，不需要额外的快照机制。

## 4. 详细设计文档索引

| 文档 | 说明 |
|------|------|
| [Slot 日志压缩设计](./slot_compaction.md) | 快照数据结构、Storage 接口扩展、Pebble 实现、快照创建流程 |
| [快照安装流程](./snapshot_install.md) | Follower 快照安装、大快照分片传输 |
| [Channel 日志压缩设计](./channel_compaction.md) | Channel 日志清理策略、触发条件 |
| [Raft 核心层变更](./raft_core_changes.md) | 事件类型、事件循环、Queue 适配、Node 状态机变更 |
| [安全性与实现计划](./safety_and_plan.md) | 安全规则、崩溃恢复、实现阶段、文件清单、风险 |
