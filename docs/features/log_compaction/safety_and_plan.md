# 安全性与实现计划

> 本文档描述日志压缩的安全性保证、崩溃恢复、实现阶段划分和涉及的文件清单。

## 1. 压缩安全规则

1. **只压缩已应用的日志**：`compactIndex <= appliedIndex`，确保状态机已持有数据
2. **保留足够日志给 Follower**：`compactIndex = appliedIndex - CompactMinLogRetain`，减少快照安装频率
3. **Leader 才触发压缩**：Follower 不主动压缩，由 Leader 协调
4. **压缩前必须持久化快照**：先写快照，后删日志，确保崩溃恢复安全
5. **分片锁保护**：复用现有的 `shardLocks`，防止压缩与 Apply/Truncate 并发

## 2. 崩溃恢复

```
场景1：快照已写入，日志未清理
  → 恢复后日志仍存在，下次压缩会清理（幂等安全）

场景2：日志已清理，快照未写入
  → 不可能发生（先写快照后删日志）

场景3：快照传输中断
  → Follower 丢弃不完整快照，Leader 重新发送

场景4：压缩过程中发生 Leader 切换
  → 新 Leader 独立判断是否需要压缩
```

## 3. CompactLogTo 与 TruncateLogTo 对比

| 操作 | TruncateLogTo | CompactLogTo |
|------|--------------|-------------|
| 方向 | 尾部截断（删除 index 之后的日志） | 头部清理（删除 index 之前的日志） |
| 触发场景 | Follower 日志冲突 | 日志数量超过阈值 |
| 安全约束 | `index >= appliedIndex` | `index <= appliedIndex` |
| 是否需要快照 | 否 | Slot 日志需要，Channel 日志不需要 |

```
日志序列:  [1] [2] [3] [4] [5] [6] [7] [8] [9] [10]
                              ^appliedIndex

TruncateLogTo(6):  [1] [2] [3] [4] [5] [6]  ← 删除 7-10（尾部）
CompactLogTo(4):         [5] [6] [7] [8] [9] [10]  ← 删除 1-4（头部）
```

## 4. 实现阶段

### 第一阶段：基础设施

1. 定义快照相关数据结构（`types/snapshot.go`）
2. 扩展 `Storage` 和 `IStorage` 接口
3. 实现 `PebbleShardLogStorage.CompactLogTo()`
4. 实现 Pebble 快照存储（Key 设计 + 读写）
5. 新增事件类型 `CompactReq/Resp`、`InstallSnapshotReq/Resp`

### 第二阶段：Slot 日志压缩

1. 实现 Slot 状态机快照的序列化/反序列化
2. 实现压缩触发器（定时检查 + 阈值触发）
3. 在 Raft 事件循环中处理 `CompactReq`
4. 实现快照创建 + 日志清理的完整流程
5. 修改 `GetState()` 支持从快照恢复初始状态

### 第三阶段：快照安装

1. 实现 `InstallSnapshotReq/Resp` 事件处理
2. 修改 Leader 的 `handleGetLogsReq`，检测日志已压缩场景
3. 实现快照分片传输（大快照场景）
4. Follower 端快照安装和状态重建
5. Queue 增加 `compactedIndex` 感知

### 第四阶段：Channel 日志压缩

1. 实现 Channel Storage 的 `CompactLogTo()`（仅清理 Term 元数据）
2. Channel 压缩触发器
3. 集成到 RaftGroup 事件处理

### 第五阶段：测试与调优

1. 单元测试：压缩、快照创建、快照安装
2. 集成测试：多节点场景下的压缩与恢复
3. 压力测试：大量日志场景下的压缩性能
4. 混沌测试：压缩过程中的节点崩溃、Leader 切换

## 5. 涉及修改的文件清单

| 文件路径 | 变更类型 | 说明 |
|---------|---------|------|
| `pkg/raft/types/types.go` | 修改 | 新增事件类型 |
| `pkg/raft/types/snapshot.go` | **新增** | 快照相关类型定义 |
| `pkg/raft/raft/storage.go` | 修改 | Storage 接口新增方法 |
| `pkg/raft/raft/raft.go` | 修改 | 事件循环新增压缩/快照处理 |
| `pkg/raft/raft/node.go` | 修改 | Node 新增压缩状态和触发逻辑 |
| `pkg/raft/raft/queue.go` | 修改 | 新增 compactedIndex |
| `pkg/raft/raft/options.go` | 修改 | 新增压缩相关配置 |
| `pkg/raft/raftgroup/storage.go` | 修改 | IStorage 接口新增方法 |
| `pkg/raft/raftgroup/raftgroup.go` | 修改 | 压缩触发器集成 |
| `pkg/raft/raftgroup/handle.go` | 修改 | 新增事件处理分支 |
| `pkg/cluster/slot/storage_pebble.go` | 修改 | 实现 CompactLogTo + 快照存取 |
| `pkg/cluster/slot/key/` | 修改 | 新增 Snapshot Key |
| `pkg/cluster/channel/storage.go` | 修改 | 实现 CompactLogTo |
| `pkg/cluster/store/store_apply.go` | 修改 | 状态机快照序列化 |

## 6. 风险与注意事项

1. **数据丢失风险**：压缩是不可逆操作，必须确保快照完整后才能删除日志
2. **性能影响**：快照创建期间可能有短暂的写入延迟，建议在异步协程中进行
3. **磁盘空间**：快照本身也占用空间，需要限制保留的快照数量（建议保留最近 2 个）
4. **网络带宽**：快照传输可能消耗大量带宽，需考虑限速或非高峰期传输
5. **兼容性**：需要处理新旧版本节点混合部署的场景（旧节点不支持快照安装）
