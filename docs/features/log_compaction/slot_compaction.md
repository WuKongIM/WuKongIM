# Slot 日志压缩详细设计

> 本文档描述 Slot 日志（基于 Pebble DB 的 `PebbleShardLogStorage`）的压缩方案，包括快照数据结构、接口扩展、存储实现和创建流程。

## 1. 快照数据结构

```go
// pkg/raft/types/snapshot.go（新文件）

// Snapshot 快照元数据
type Snapshot struct {
    // 快照包含的最后一条日志索引
    LastIncludedIndex uint64
    // 快照包含的最后一条日志任期
    LastIncludedTerm  uint32
    // 快照创建时的 Raft 配置
    Config            Config
    // 快照数据的大小（字节）
    Size              uint64
    // 快照创建时间
    CreatedAt         int64
}

// SnapshotData 快照数据载体
type SnapshotData struct {
    Meta Snapshot
    // 状态机的序列化数据
    Data []byte
}
```

## 2. Storage 接口扩展

### 2.1 单 Raft 实例接口

```go
// pkg/raft/raft/storage.go 扩展

type Storage interface {
    // ... 现有方法保持不变 ...

    // === 新增：日志压缩相关 ===

    // CompactLogTo 删除 index（含）之前的所有日志条目
    // 这是头部清理，与现有的 TruncateLogTo（尾部截断）不同
    CompactLogTo(index uint64) error

    // SaveSnapshot 保存快照数据
    SaveSnapshot(snapshot types.SnapshotData) error

    // GetSnapshot 获取最近的快照
    GetSnapshot() (types.SnapshotData, error)

    // GetSnapshotMeta 获取最近快照的元数据（不含数据体）
    GetSnapshotMeta() (types.Snapshot, error)
}
```

### 2.2 RaftGroup 接口

```go
// pkg/raft/raftgroup/storage.go 扩展

type IStorage interface {
    // ... 现有方法保持不变 ...

    // CompactLogTo 删除 key 对应的 index 之前的所有日志
    CompactLogTo(key string, index uint64) error

    // SaveSnapshot 保存快照
    SaveSnapshot(key string, snapshot types.SnapshotData) error

    // GetSnapshot 获取快照
    GetSnapshot(key string) (types.SnapshotData, error)

    // GetSnapshotMeta 获取快照元数据
    GetSnapshotMeta(key string) (types.Snapshot, error)
}
```

## 3. PebbleShardLogStorage 实现

### 3.1 CompactLogTo

```go
// pkg/cluster/slot/storage_pebble.go 新增方法

// CompactLogTo 删除 index（含）之前的所有日志
func (p *PebbleShardLogStorage) CompactLogTo(shardNo string, index uint64) error {
    shardId := p.shardId(shardNo)
    p.shardLocks[shardId].Lock()
    defer p.shardLocks[shardId].Unlock()

    // 安全检查：不能清理未应用的日志
    appliedIdx, err := p.AppliedIndex(shardNo)
    if err != nil {
        return err
    }
    if index > appliedIdx {
        return fmt.Errorf("cannot compact beyond applied index: compact=%d, applied=%d",
            index, appliedIdx)
    }

    // 删除 [0, index] 范围内的日志
    startKey := key.NewLogKey(shardNo, 0)
    endKey := key.NewLogKey(shardNo, index+1) // endKey 不包含
    return p.shardDB(shardNo).DeleteRange(startKey, endKey, p.sync)
}
```

### 3.2 快照存取

```go
// SaveSnapshot 保存快照到 Pebble
func (p *PebbleShardLogStorage) SaveSnapshot(shardNo string, snapshot types.SnapshotData) error {
    data, err := snapshot.Marshal()
    if err != nil {
        return err
    }
    snapshotKey := key.NewSnapshotKey(shardNo)
    return p.shardDB(shardNo).Set(snapshotKey, data, p.sync)
}

// GetSnapshot 获取最近的快照
func (p *PebbleShardLogStorage) GetSnapshot(shardNo string) (types.SnapshotData, error) {
    snapshotKey := key.NewSnapshotKey(shardNo)
    data, closer, err := p.shardDB(shardNo).Get(snapshotKey)
    if err != nil {
        if err == pebble.ErrNotFound {
            return types.SnapshotData{}, nil
        }
        return types.SnapshotData{}, err
    }
    defer closer.Close()

    result := make([]byte, len(data))
    copy(result, data)

    var snapshot types.SnapshotData
    if err := snapshot.Unmarshal(result); err != nil {
        return types.SnapshotData{}, err
    }
    return snapshot, nil
}
```

## 4. Pebble Key 设计

```go
// pkg/cluster/slot/key/ 新增

// 快照 Key 格式：/snapshots/{shardNo}
func NewSnapshotKey(shardNo string) []byte {
    return []byte(fmt.Sprintf("/snapshots/%s", shardNo))
}

// 压缩状态 Key 格式：/compaction/{shardNo}
func NewCompactedIndexKey(shardNo string) []byte {
    return []byte(fmt.Sprintf("/compaction/%s", shardNo))
}
```

## 5. 快照创建流程

```
Leader 节点:

1. 压缩触发器检测到需要压缩
   - 条件：lastLogIndex - snapshotIndex > CompactLogCount（可配置）
   - 或者：距上次压缩超过 CompactInterval

2. 创建快照请求
   ┌──────────────────────────────────────┐
   │  compactIndex = appliedIndex         │
   │  compactTerm  = logs[compactIndex]   │
   └──────────┬───────────────────────────┘
              │
              v
3. 调用状态机序列化
   ┌──────────────────────────────────────┐
   │  snapshot.Data = StateMachine.Save() │
   │  snapshot.Meta = {                   │
   │    LastIncludedIndex: compactIndex   │
   │    LastIncludedTerm:  compactTerm    │
   │    Config: currentConfig             │
   │  }                                   │
   └──────────┬───────────────────────────┘
              │
              v
4. 持久化快照
   ┌──────────────────────────────────────┐
   │  Storage.SaveSnapshot(snapshot)      │
   └──────────┬───────────────────────────┘
              │
              v
5. 清理旧日志
   ┌──────────────────────────────────────┐
   │  Storage.CompactLogTo(compactIndex)  │
   │  清理 compactIndex 之前的             │
   │  LeaderTermStartIndex 记录           │
   └──────────────────────────────────────┘
```

## 6. 压缩触发配置

```go
// 新增配置项（Options 扩展）

// CompactLogCount 触发压缩的日志条数阈值
// 当 lastLogIndex - lastSnapshotIndex > CompactLogCount 时触发
CompactLogCount uint64  // 默认 10000

// CompactInterval 压缩检查间隔
CompactInterval time.Duration  // 默认 30 分钟

// CompactMinLogRetain 压缩后保留的最小日志数
// 保留最近 N 条日志避免频繁触发快照安装
CompactMinLogRetain uint64  // 默认 1000

// SnapshotEnabled 是否开启快照功能
SnapshotEnabled bool  // 默认 true
```

## 7. 触发逻辑

```
每 CompactInterval 触发一次检查:

  snapshotMeta = Storage.GetSnapshotMeta()
  lastSnapshotIndex = snapshotMeta.LastIncludedIndex  // 没有快照则为 0

  if appliedIndex - lastSnapshotIndex > CompactLogCount:
      compactIndex = appliedIndex - CompactMinLogRetain
      if compactIndex <= lastSnapshotIndex:
          return  // 不需要压缩

      // 执行压缩
      1. 创建快照（Slot 日志）
      2. 清理 compactIndex 之前的日志
      3. 清理 compactIndex 之前的 LeaderTermStartIndex
```
