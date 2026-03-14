# Channel 日志压缩设计

> 本文档描述 Channel 日志（基于 `wkdb` 的消息存储）的压缩方案。Channel 日志与 Slot 日志有本质区别，不需要快照机制。

## 1. Channel 日志的特殊性

Channel 日志与 Slot 日志的核心区别在于数据流向：

```
Slot 日志:
  AppendLogs → 存储日志 → Apply → 应用到状态机
  日志和状态是分离的，压缩需要快照保存状态

Channel 日志:
  AppendLogs → 直接写入 wkdb 消息表（日志 = 状态）
  Apply 方法是空操作 (return nil)
  消息本身就是最终状态，不需要快照
```

关键代码证据（`pkg/cluster/channel/storage.go`）：

```go
// Apply 是空操作，因为消息在 AppendLogs 时已经直接写入 wkdb
func (s *storage) Apply(key string, logs []types.Log) error {
    return nil
}

// AppendLogs 直接将日志转为消息写入数据库
func (s *storage) AppendLogs(key string, logs []types.Log, ...) error {
    messages := make([]wkdb.Message, 0, len(logs))
    for _, log := range logs {
        var msg wkdb.Message
        msg.Unmarshal(log.Data)
        msg.MessageSeq = uint32(log.Index)
        messages = append(messages, msg)
    }
    return s.db.AppendMessages(channelId, channelType, messages)
}
```

## 2. 需要清理的内容

Channel 日志压缩**不需要删除消息**（消息是业务数据），只需要清理 Raft 层面的元数据冗余：

| 清理对象 | 说明 |
|---------|------|
| LeaderTermStartIndex | 历史 Term 的起始索引记录，随 Term 增长无限膨胀 |

## 3. 清理策略

### 3.1 Storage 实现

```go
// pkg/cluster/channel/storage.go 新增

func (s *storage) CompactLogTo(key string, index uint64) error {
    // Channel 的日志就是消息本身，不需要删除消息
    // 只需要清理历史的 LeaderTermStartIndex 记录
    // 保留最近一个 term 的记录即可
    return s.db.CleanLeaderTermStartIndexBefore(key, index)
}
```

### 3.2 wkdb 层新增方法

```go
// pkg/wkdb/ 新增

// CleanLeaderTermStartIndexBefore 清理 index 之前的 Term 起始索引记录
// 保留 index 对应的 term 及之后的记录
func (db *DB) CleanLeaderTermStartIndexBefore(shardNo string, index uint64) error {
    // 1. 找到 index 对应的 term
    // 2. 删除该 term 之前的所有 LeaderTermStartIndex 记录
}
```

## 4. 触发条件

```
每 CompactInterval 触发一次检查:

  for each active channel:
      termCount = countLeaderTermStartIndex(channelKey)
      if termCount > TermRetainCount:  // 默认保留最近 10 个 term
          cleanOldTermStartIndex(channelKey)
```

### 配置参数

```go
// Channel 压缩特有配置
TermRetainCount int  // 保留的 Term 记录数，默认 10
```

## 5. 与 Slot 日志压缩的对比

| 特性 | Slot 日志 | Channel 日志 |
|------|-----------|-------------|
| 需要快照 | 是 | 否 |
| 删除日志条目 | 是 | 否（消息是业务数据） |
| 清理 Term 元数据 | 是 | 是 |
| 快照安装 | 支持 | 不需要 |
| 触发条件 | 日志条数阈值 | Term 记录数阈值 |
| 实现复杂度 | 高 | 低 |
