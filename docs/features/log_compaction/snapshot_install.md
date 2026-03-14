# 快照安装流程

> 本文档描述当 Follower 需要同步的日志已被 Leader 压缩删除时，通过快照传输完成数据同步的流程。

## 1. 触发场景

当 Leader 发现 Follower 需要同步的日志已被压缩删除时，需要通过快照传输数据：

```
Leader 处理 GetLogsReq:

  1. 查询 follower 需要的 startLogIndex
  2. 如果 startLogIndex <= snapshot.LastIncludedIndex
     → 日志已被压缩，无法通过正常同步
     → 触发快照安装流程
  3. 否则正常返回日志
```

## 2. 安装流程

```
Leader                                Follower
  │                                      │
  │  InstallSnapshotReq                  │
  │  (含快照元数据 + 数据块)              │
  │─────────────────────────────────────>│
  │                                      │
  │                              1. 暂停 Raft 状态机
  │                              2. 保存快照数据
  │                              3. 应用快照到状态机
  │                              4. 更新 appliedIndex
  │                              5. 清理旧日志
  │                              6. 恢复 Raft 状态机
  │                                      │
  │  InstallSnapshotResp                 │
  │  (成功/失败)                          │
  │<─────────────────────────────────────│
  │                                      │
  │  后续正常日志同步从                    │
  │  snapshot.LastIncludedIndex+1 开始     │
  │                                      │
```

### Follower 端详细步骤

1. **暂停 Raft 状态机**：调用 `Pause()`，防止安装期间处理其他事件
2. **保存快照数据**：`Storage.SaveSnapshot(snapshotData)`
3. **应用快照到状态机**：用快照数据覆盖当前状态
4. **更新索引**：
   - `appliedIndex = snapshot.LastIncludedIndex`
   - `committedIndex = snapshot.LastIncludedIndex`
   - `lastLogIndex = snapshot.LastIncludedIndex`
5. **清理旧日志**：删除所有日志（快照已包含完整状态）
6. **恢复 Raft 状态机**：调用 `Resume()`

## 3. 大快照分片传输

当快照数据较大时，需要分片传输。

### 3.1 分片数据结构

```go
// 快照传输分片
type SnapshotChunk struct {
    // 快照元数据（仅第一片包含完整元数据）
    Meta     Snapshot
    // 分片偏移
    Offset   uint64
    // 分片数据
    Data     []byte
    // 是否是最后一片
    Done     bool
}
```

### 3.2 传输策略

- 单次传输大小上限：与 `MaxLogSizePerBatch`（10MB）保持一致
- 如果快照小于 10MB，单次传输
- 如果快照大于 10MB，分片传输，每片 10MB

### 3.3 分片传输流程

```
Leader                                    Follower
  │                                          │
  │  Chunk 1 (Meta + Data, Offset=0)         │
  │─────────────────────────────────────────>│
  │                                          │ 写入临时文件
  │  InstallSnapshotResp (继续)               │
  │<─────────────────────────────────────────│
  │                                          │
  │  Chunk 2 (Data, Offset=10MB)             │
  │─────────────────────────────────────────>│
  │                                          │ 追加写入
  │  InstallSnapshotResp (继续)               │
  │<─────────────────────────────────────────│
  │                                          │
  │  Chunk N (Data, Done=true)               │
  │─────────────────────────────────────────>│
  │                                          │ 完整快照，执行安装
  │  InstallSnapshotResp (完成)               │
  │<─────────────────────────────────────────│
```

### 3.4 异常处理

- **传输中断**：Follower 丢弃不完整快照数据，Leader 重新发送
- **分片乱序**：通过 `Offset` 检测，乱序则拒绝并要求重传
- **过期快照**：如果收到的快照 `LastIncludedIndex <= appliedIndex`，直接忽略

## 4. 新节点追赶流程（有快照时）

```
时间线:
  t0: Learner 加入集群
  t1: Leader 收到 SyncReq，发现需要的日志已压缩
  t2: Leader 发送 InstallSnapshotReq
  t3: Learner 安装快照，更新 appliedIndex
  t4: Learner 发送 SyncReq（从 snapshot.LastIncludedIndex + 1 开始）
  t5: 正常日志同步追赶
  t6: 追赶完成，Learner -> Follower
```

## 5. Leader 端代码变更

```go
// raft.go handleGetLogsReq 修改
func (r *Raft) handleGetLogsReq(e types.Event) {
    // ... 现有逻辑 ...

    // 新增：检查是否需要发送快照
    snapshotMeta, _ := r.opts.Storage.GetSnapshotMeta()
    if e.Index <= snapshotMeta.LastIncludedIndex {
        // 需要的日志已被压缩，触发快照安装
        snapshot, err := r.opts.Storage.GetSnapshot()
        if err != nil {
            // 错误处理
            return
        }
        r.stepC <- stepReq{event: types.Event{
            To:   e.From,
            Type: types.InstallSnapshotReq,
            // 携带快照数据
        }}
        return
    }

    // 正常日志同步逻辑
}
```
