# Raft 核心层变更

> 本文档描述日志压缩功能对 Raft 核心层（事件类型、事件循环、Queue、Node）的修改。

## 1. 新增事件类型

```go
// pkg/raft/types/types.go 新增

const (
    // ... 现有事件保持不变 ...

    // CompactReq 日志压缩请求（本地事件）
    CompactReq
    // CompactResp 日志压缩响应（本地事件）
    CompactResp

    // InstallSnapshotReq 安装快照请求 leader -> follower
    InstallSnapshotReq
    // InstallSnapshotResp 安装快照响应 follower -> leader
    InstallSnapshotResp
)
```

需要同步更新 `EventType.String()` 方法。

## 2. Raft 事件循环扩展

```go
// pkg/raft/raft/raft.go readyEvents() 中新增 case

case types.CompactReq:
    r.node.KeepAlive()
    r.handleCompactReq(e)
    continue

case types.InstallSnapshotReq:
    r.node.KeepAlive()
    r.handleInstallSnapshotReq(e)
    continue
```

### 2.1 handleCompactReq 实现

```go
func (r *Raft) handleCompactReq(e types.Event) {
    err := r.pool.Submit(func() {
        // 1. 创建状态机快照
        // 2. 保存快照: Storage.SaveSnapshot()
        // 3. 清理旧日志: Storage.CompactLogTo(e.Index)
        // 4. 清理旧 Term 元数据

        reason := types.ReasonOk
        if err != nil {
            reason = types.ReasonError
        }
        r.stepC <- stepReq{event: types.Event{
            Type:   types.CompactResp,
            Index:  e.Index,
            Reason: reason,
        }}
    })
}
```

### 2.2 handleInstallSnapshotReq 实现

```go
func (r *Raft) handleInstallSnapshotReq(e types.Event) {
    err := r.pool.Submit(func() {
        // 1. 保存快照数据
        // 2. 应用快照到状态机
        // 3. 更新 appliedIndex / committedIndex
        // 4. 清理旧日志

        r.stepC <- stepReq{event: types.Event{
            Type:   types.InstallSnapshotResp,
            To:     e.From,
            Index:  snapshotLastIndex,
            Reason: reason,
        }}
    })
}
```

## 3. Queue（内存队列）适配

### 3.1 现有结构

```go
type queue struct {
    logs           []types.Log
    storedIndex    uint64
    lastLogIndex   uint64
    committedIndex uint64
    appliedIndex   uint64
    appending      bool
    applying       bool
}
```

Queue 在 `storeTo` 后会裁剪已持久化的日志，但不感知压缩。

### 3.2 新增字段

```go
type queue struct {
    // ... 现有字段 ...
    compactedIndex uint64 // 已压缩到此索引（含），此索引之前的日志不可用
}
```

### 3.3 影响的操作

**日志获取**：当请求的日志已被压缩时返回错误

```go
// 新增错误类型
var ErrLogCompacted = errors.New("requested log has been compacted")

// GetLogs 需要检查压缩边界
func (r *queue) checkLogAvailable(startIndex uint64) error {
    if startIndex <= r.compactedIndex {
        return ErrLogCompacted
    }
    return nil
}
```

**压缩更新**：压缩完成后更新 compactedIndex

```go
func (r *queue) compactedTo(index uint64) {
    if index > r.compactedIndex {
        r.compactedIndex = index
    }
}
```

**快照恢复**：安装快照后重置 Queue 状态

```go
func (r *queue) resetFromSnapshot(snapshotIndex uint64) {
    r.logs = nil
    r.storedIndex = snapshotIndex
    r.lastLogIndex = snapshotIndex
    r.committedIndex = snapshotIndex
    r.appliedIndex = snapshotIndex
    r.compactedIndex = snapshotIndex
    r.appending = false
    r.applying = false
}
```

## 4. Node 状态机变更

### 4.1 新增压缩状态

```go
// pkg/raft/raft/node.go 新增字段

type Node struct {
    // ... 现有字段 ...
    compacting     bool   // 是否正在压缩中
    lastCompactTime time.Time // 上次压缩时间
}
```

### 4.2 Ready 事件产出

Node 的 `Ready()` 方法需要新增压缩事件的产出：

```go
func (n *Node) Ready() []types.Event {
    events := n.existingReadyLogic()

    // 新增：检查是否需要压缩
    if n.shouldCompact() {
        events = append(events, types.Event{
            Type:        types.CompactReq,
            Index:       n.compactTargetIndex(),
            LastLogTerm: n.getTermAtIndex(n.compactTargetIndex()),
        })
        n.compacting = true
    }

    return events
}
```

### 4.3 压缩判断逻辑

```go
func (n *Node) shouldCompact() bool {
    // 1. 不在压缩中
    if n.compacting {
        return false
    }
    // 2. 必须是 Leader
    if !n.IsLeader() {
        return false
    }
    // 3. 检查时间间隔
    if time.Since(n.lastCompactTime) < n.opts.CompactInterval {
        return false
    }
    // 4. 检查日志数量阈值
    snapshotIndex := n.queue.compactedIndex
    if n.queue.appliedIndex - snapshotIndex <= n.opts.CompactLogCount {
        return false
    }
    return true
}

func (n *Node) compactTargetIndex() uint64 {
    // 保留最近 CompactMinLogRetain 条日志
    return n.queue.appliedIndex - n.opts.CompactMinLogRetain
}
```

### 4.4 Step 处理压缩响应

```go
// node_step.go 新增

case types.CompactResp:
    n.compacting = false
    if e.Reason == types.ReasonOk {
        n.queue.compactedTo(e.Index)
        n.lastCompactTime = time.Now()
    }

case types.InstallSnapshotResp:
    if e.Reason == types.ReasonOk {
        // 更新 Follower 的同步信息
        n.updateReplicaSyncInfo(e.From, e.Index)
    }
```

## 5. GetLogsReq 处理变更

当 Leader 处理 Follower 的同步请求时，需要检查请求的日志是否已被压缩：

```go
// raft.go handleGetLogsReq 修改

func (r *Raft) handleGetLogsReq(e types.Event) {
    err := r.pool.Submit(func() {
        // 新增：检查是否需要发送快照
        snapshotMeta, _ := r.opts.Storage.GetSnapshotMeta()
        if snapshotMeta.LastIncludedIndex > 0 && e.Index <= snapshotMeta.LastIncludedIndex {
            // 需要的日志已被压缩，触发快照安装
            snapshot, err := r.opts.Storage.GetSnapshot()
            if err != nil {
                // 错误处理，返回 ReasonError
                return
            }
            r.stepC <- stepReq{event: types.Event{
                To:   e.From,
                Type: types.InstallSnapshotReq,
                // 携带快照数据
            }}
            return
        }

        // ... 现有的日志同步逻辑 ...
    })
}
```

## 6. Options 扩展

```go
// pkg/raft/raft/options.go 新增

type Options struct {
    // ... 现有字段 ...

    // CompactLogCount 触发压缩的日志条数阈值
    CompactLogCount uint64
    // CompactInterval 压缩检查间隔
    CompactInterval time.Duration
    // CompactMinLogRetain 压缩后保留的最小日志数
    CompactMinLogRetain uint64
    // SnapshotEnabled 是否开启快照功能
    SnapshotEnabled bool
}

// NewOptions 默认值更新
func NewOptions(opt ...Option) *Options {
    opts := &Options{
        // ... 现有默认值 ...
        CompactLogCount:     10000,
        CompactInterval:     30 * time.Minute,
        CompactMinLogRetain: 1000,
        SnapshotEnabled:     true,
    }
    // ...
}
```
