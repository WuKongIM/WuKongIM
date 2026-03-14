# pkg/raft/raft 代码审查报告

> 审查日期: 2026-03-13

## 概述

对 `pkg/raft/raft` 包进行全面代码审查，涵盖所有核心文件，共发现 **6 个 BUG**（2 严重 / 2 中等 / 2 低）、**4 个性能问题** 和 **1 个代码建议**。

---

## BUG

### BUG-1 [严重] stepLearner 缺少 advance() 和 sendSyncReq() 调用

**文件**: `node_step.go:310-390`

`stepLearner` 与 `stepFollower` 逻辑高度相似，但 learner 路径遗漏了多个关键调用：

| 事件 | stepFollower | stepLearner | 差异 |
|------|-------------|-------------|------|
| **SyncResp** (收到日志) | `n.advance()` (line 246) | 无 | 日志不会被及时存储 |
| **StoreResp** (存储完成) | `n.sendSyncReq()` + `n.advance()` (line 284-285) | 无 | 不会主动发起下一轮同步 |
| **TruncateResp** (截断完成) | `n.sendSyncReq()` + `n.advance()` (line 274-275) | 无 | 截断后不会继续同步 |

**对比代码**:

```go
// stepFollower - SyncResp 处理 (正确)
case types.SyncResp:
    if len(e.Logs) > 0 {
        err := n.queue.append(e.Logs...)
        if err != nil { return err }
        n.advance()  // ← follower 有
    }

// stepLearner - SyncResp 处理 (缺失)
case types.SyncResp:
    if len(e.Logs) > 0 {
        err := n.queue.append(e.Logs...)
        if err != nil { return err }
        // ← learner 缺少 n.advance()
    }
```

```go
// stepFollower - StoreResp 处理 (正确)
case types.StoreResp:
    n.queue.appending = false
    if e.Reason == types.ReasonOk {
        n.queue.storeTo(e.Index)
        n.sendSyncReq()  // ← follower 有
        n.advance()      // ← follower 有
    }

// stepLearner - StoreResp 处理 (缺失)
case types.StoreResp:
    n.queue.appending = false
    if e.Reason == types.ReasonOk {
        n.queue.storeTo(e.Index)
        // ← learner 缺少 n.sendSyncReq() 和 n.advance()
    }
```

**影响**: learner 无法形成「同步 → 存储 → 再同步」的连续流水线，只能依赖 tick 定时（默认 2 tick ≈ 300ms）触发同步。在节点迁移场景中，learner 追赶日志的速度会慢数倍，直接延长迁移时间。

**修复建议**:

在 `stepLearner` 中补齐与 `stepFollower` 一致的调用：

```go
// SyncResp 中补充:
if len(e.Logs) > 0 {
    err := n.queue.append(e.Logs...)
    if err != nil { return err }
    n.advance()  // ← 补充
}

// StoreResp 中补充:
if e.Reason == types.ReasonOk {
    n.queue.storeTo(e.Index)
    n.sendSyncReq()  // ← 补充
    n.advance()      // ← 补充
}

// TruncateResp 中补充:
if e.Reason == types.ReasonOk {
    if e.Index < n.queue.lastLogIndex {
        n.queue.truncateLogTo(e.Index)
    }
    n.sendSyncReq()  // ← 补充
    n.advance()      // ← 补充
}
```

---

### BUG-2 [严重] sendPing 广播时缺少 CommittedIndex 和 ConfigVersion

**文件**: `node_send.go:93-135`

```go
func (n *Node) sendPing(to uint64) {
    if to != All {
        // 单播: 包含关键字段 ✓
        n.events = append(n.events, types.Event{
            Type:           types.Ping,
            CommittedIndex: n.queue.committedIndex,  // ✓
            ConfigVersion:  n.cfg.Version,           // ✓
            // ...
        })
        return
    }
    // 广播: 缺少关键字段 ✗
    for _, replicaId := range n.cfg.Replicas {
        n.events = append(n.events, types.Event{
            Type:  types.Ping,
            Index: n.queue.lastLogIndex,
            // CommittedIndex: ???  ← 缺失!
            // ConfigVersion:  ???  ← 缺失!
        })
    }
}
```

**影响**:

当 `ElectionOn=true` 时，leader 通过 `tickHeartbeat()` 周期性调用 `sendPing(All)` 广播心跳。由于广播 ping 缺少 `CommittedIndex`：
- follower 收到 ping 后 `e.CommittedIndex == 0`，`updateFollowCommittedIndex(0)` 直接跳过
- follower 的 committedIndex 只能通过 SyncResp 更新，心跳无法推进提交进度

缺少 `ConfigVersion`：
- follower 收到 ping 后 `e.ConfigVersion == 0`，永远不会触发 `sendConfigReq()` 请求最新配置

**修复建议**:

```go
// 广播时也携带 CommittedIndex 和 ConfigVersion
for _, replicaId := range n.cfg.Replicas {
    if replicaId == n.opts.NodeId { continue }
    n.events = append(n.events, types.Event{
        Type:           types.Ping,
        From:           n.opts.NodeId,
        To:             replicaId,
        Term:           n.cfg.Term,
        Index:          n.queue.lastLogIndex,
        CommittedIndex: n.queue.committedIndex,  // ← 补充
        ConfigVersion:  n.cfg.Version,           // ← 补充
    })
}
// Learners 同理
```

---

### BUG-3 [中等] Pause/Resume 存在竞态条件，可能导致永久挂起

**文件**: `raft.go:82-95, 192-199`

```go
// loop() goroutine - 时刻 T1
if r.pause.Load() {           // T1: 读到 pause=true
    r.pauseCond.L.Lock()       // T3: 获取锁
    r.pauseCond.Wait()         // T4: 开始等待 (但 Signal 已在 T2 丢失)
    r.pauseCond.L.Unlock()
}

// Resume() goroutine - 时刻 T2 (T1 < T2 < T3)
r.pause.Store(false)           // T2: 设置 pause=false
r.pauseCond.L.Lock()
r.pauseCond.Signal()           // T2: Signal 发出，但此时没有 goroutine 在 Wait
r.pauseCond.L.Unlock()
```

**问题**: 如果 `Resume()` 在 `pause.Load()` 之后、`pauseCond.Wait()` 之前执行，Signal 会丢失，loop 将永久阻塞在 `Wait()` 上。

**修复建议**:

```go
// loop() 中
r.pauseCond.L.Lock()
for r.pause.Load() {       // 在锁内检查条件
    r.pauseCond.Wait()
}
r.pauseCond.L.Unlock()
```

---

### BUG-4 [中等] switchConfig 错误被静默忽略

**文件**: `node_step.go:38-39`

```go
case types.ConfChange:
    n.switchConfig(e.Config)  // 返回的 error 被丢弃
```

`switchConfig` 在版本号低于当前版本时会返回 error，但调用方完全忽略了返回值。配置变更失败时节点状态可能不一致。

**修复建议**:

```go
case types.ConfChange:
    if err := n.switchConfig(e.Config); err != nil {
        n.Warn("switch config failed", zap.Error(err))
    }
```

---

### BUG-5 [低] queue.nextApplyLogs 中 applying 标志可能卡死

**文件**: `queue.go:184-194`

```go
func (r *queue) nextApplyLogs() (uint64, uint64) {
    if r.applying { return 0, 0 }
    r.applying = true                       // ← 设为 true
    if r.appliedIndex > r.committedIndex {  // ← 异常分支
        return 0, 0                         // ← 返回但 applying 未重置!
    }
    return r.appliedIndex + 1, r.committedIndex + 1
}
```

虽然正常流程中 `hasNextApplyLogs()` 会阻止进入此路径，但如果出现 `appliedIndex > committedIndex` 的异常状态，`applying` 将被永久锁定为 true，所有后续 apply 操作将被阻塞。

**修复建议**:

```go
func (r *queue) nextApplyLogs() (uint64, uint64) {
    if r.applying { return 0, 0 }
    if r.appliedIndex > r.committedIndex {
        return 0, 0  // 不设置 applying
    }
    r.applying = true  // 移到确认有效之后
    return r.appliedIndex + 1, r.committedIndex + 1
}
```

---

### BUG-6 [低] Panic 后存在不可达代码

**文件**: `raft.go:420-426`

```go
r.Panic("apply logs failed", zap.Error(err))
r.stepC <- stepReq{event: types.Event{   // ← 如果 Panic 真正 panic，此行不可达
    Type:   types.ApplyResp,
    Reason: types.ReasonError,
}}
```

如果 `Panic()` 内部调用 `log.Panic()` (触发 `panic()`)，后续代码永远不会执行。建议改为 `Error` 级别或删除后续死代码。

---

## 性能问题

### PERF-1 选举超时随机化使用 crypto/rand，开销过大

**文件**: `common.go:16-21`

```go
func (r *lockedRand) Intn(n int) int {
    r.mu.Lock()
    v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))  // crypto/rand + big.Int 分配
    r.mu.Unlock()
    return int(v.Int64())
}
```

选举超时的随机化不需要密码学安全性。`crypto/rand` 涉及系统调用 (`/dev/urandom`)，且需要 `big.Int` 内存分配，比 `math/rand` 慢约 100 倍。

**修复建议**:

```go
import "math/rand/v2"

func (r *lockedRand) Intn(n int) int {
    r.mu.Lock()
    v := rand.IntN(n)
    r.mu.Unlock()
    return v
}
```

---

### PERF-2 WaitUtilCommit 使用忙轮询

**文件**: `raft.go:166-178`

```go
func (r *Raft) WaitUtilCommit(ctx context.Context, index uint64) error {
    for {
        if r.node.queue.committedIndex >= index {
            return nil
        }
        time.Sleep(time.Millisecond * 10)  // 忙轮询，浪费 CPU
    }
}
```

已有 `wait.waitCommit()` 事件驱动机制，应该使用它而非忙轮询。

**修复建议**:

```go
func (r *Raft) WaitUtilCommit(ctx context.Context, index uint64) error {
    if r.node.queue.committedIndex >= index {
        return nil
    }
    pg := r.wait.waitCommit(index)
    select {
    case <-pg.waitC:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    case <-r.stopper.ShouldStop():
        return types.ErrStopped
    }
}
```

---

### PERF-3 wait.clean() 切片删除为 O(n^2)

**文件**: `wait.go:85-93`

```go
func (m *wait) clean() {
    for i := 0; i < len(m.progresses); {
        if m.progresses[i].done {
            // 每次删除都导致后续所有元素前移 O(n)
            m.progresses = append(m.progresses[:i], m.progresses[i+1:]...)
        } else {
            i++
        }
    }
}
```

高吞吐场景下 progresses 列表可能较长，每次删除触发数组元素移动，总体为 O(n^2)。

**修复建议** (双指针过滤，O(n)):

```go
func (m *wait) clean() {
    j := 0
    for i := range m.progresses {
        if !m.progresses[i].done {
            m.progresses[j] = m.progresses[i]
            j++
        }
    }
    m.progresses = m.progresses[:j]
}
```

---

### PERF-4 loop() 每次迭代只处理一个 step 事件

**文件**: `raft.go:192-217`

```go
func (r *Raft) loop() {
    for {
        r.readyEvents()
        select {
        case <-tk.C:
            r.node.Tick()
        case req := <-r.stepC:      // ← 每次只取一个
            err := r.node.Step(req.event)
            // ...
        }
    }
}
```

在高负载下 `stepC` 可能积压大量事件，但每次循环只处理一个事件，中间还要调用 `readyEvents()`。

**修复建议** (批量 drain):

```go
case req := <-r.stepC:
    err := r.node.Step(req.event)
    if req.resp != nil { req.resp <- err }
    // 批量处理积压的事件
    for i := 0; i < len(r.stepC); i++ {
        req = <-r.stepC
        err = r.node.Step(req.event)
        if req.resp != nil { req.resp <- err }
    }
```

---

## 代码建议

### SUGGESTION-1 Ready() 中 events 切片复用底层数组

**文件**: `node.go:167-169`

```go
events := n.events
n.events = n.events[:0]  // 底层数组共享
return events
```

`n.events[:0]` 与返回的 `events` 共享底层数组。后续 append 会覆盖底层数组中已返回的位置。

**经分析，当前代码在实际运行中是安全的**，原因：
1. `readyEvents()` 是 `Ready()` 的唯一调用方，整个流程在同一个 goroutine 中同步执行
2. `for _, e := range events` 在每次迭代开始时就将 `events[i]` 值拷贝到局部变量 `e`
3. `r.node.Step(e)` 产生的新事件写入底层数组的 position 0, 1, ...，而此时这些 position 已经被迭代过
4. 能走到 `r.node.Step(e)` 的本地事件很少（自投票等），每次最多 append 1 个事件

但如果未来有人修改遍历方式（如通过索引访问而非 range 拷贝），可能引入问题。建议改为 `n.events = nil` 以彻底消除隐患。

---

## 汇总

| 编号 | 严重程度 | 类型 | 文件 | 简述 |
|------|---------|------|------|------|
| BUG-1 | **严重** | 逻辑遗漏 | node_step.go:310-390 | stepLearner 缺少 advance/sendSyncReq |
| BUG-2 | **严重** | 逻辑遗漏 | node_send.go:109-134 | 广播 Ping 缺少 CommittedIndex/ConfigVersion |
| BUG-3 | 中等 | 竞态条件 | raft.go:192-199 | Pause/Resume 的 Signal 可能丢失 |
| BUG-4 | 中等 | 错误处理 | node_step.go:39 | switchConfig error 被忽略 |
| BUG-5 | 低 | 状态锁定 | queue.go:184-194 | nextApplyLogs 异常分支 applying 未重置 |
| BUG-6 | 低 | 死代码 | raft.go:420 | Panic 后代码不可达 |
| PERF-1 | - | 性能 | common.go:16 | crypto/rand 用于非安全场景 |
| PERF-2 | - | 性能 | raft.go:166 | WaitUtilCommit 忙轮询 |
| PERF-3 | - | 性能 | wait.go:85 | clean() O(n^2) 复杂度 |
| PERF-4 | - | 性能 | raft.go:204 | 每次循环只处理一个 step 事件 |
| SUGGESTION-1 | 建议 | 代码风格 | node.go:167 | Ready() 切片复用可改为 nil 赋值 |

**建议修复优先级**: BUG-1 > BUG-2 > BUG-3 > PERF-2 > 其余
