# 节点故障与消息删除的数据一致性分析

## 问题描述

在集群环境中，当执行范围删除消息操作时，如果某个副本节点处于故障状态，可能会导致节点恢复后出现数据不一致的问题。

### 问题场景

```
时间线：
T1: 集群有3个副本节点 (Node1-Leader, Node2-Follower, Node3-Follower)
T2: Node3 故障离线
T3: 用户调用 API 删除频道消息 (messageSeq: 100-200)
T4: Node1 和 Node2 成功执行删除操作
T5: Node1 的 Raft 日志被截断 (TruncateLogTo)
T6: Node3 恢复上线
T7: Node3 无法获取已被截断的删除日志
结果: Node3 上的消息 100-200 仍然存在，导致数据不一致
```

## 当前实现分析

### 1. 消息删除流程

#### API 层 (`internal/api/message.go`)
```go
func (m *message) deleteRangeMessages(c *wkhttp.Context)
```
- 接收删除请求
- 在集群模式下，转发到频道的 Leader 节点
- 调用 `service.Store.DeleteRangeMessages`

#### 集群存储层 (`pkg/cluster/store/message.go`)
```go
func (s *Store) DeleteRangeMessages(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64) error
```
- 将删除操作编码为 Raft 命令 (`CMDDeleteRangeMessages`)
- 通过 `ProposeUntilApplied` 提交到 Raft 日志
- **关键点**：只有在线的副本节点会收到这条日志

#### 应用层 (`pkg/cluster/store/store_apply.go`)
```go
func (s *Store) handleDeleteRangeMessages(cmd *CMD) error
```
- 每个副本节点应用 Raft 日志时执行删除操作
- 调用底层的 `wdb.DeleteRangeMessages`

### 2. Raft 日志管理

#### 日志截断机制 (`pkg/cluster/slot/storage_pebble.go`)
```go
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error
```
- Raft 会定期截断已应用的日志以节省存储空间
- 截断条件：`index < appliedIdx` 时会清理旧日志
- **问题根源**：一旦日志被截断，故障节点恢复后无法获取这些历史日志

#### 日志同步机制 (`pkg/raft`)
- Leader 通过 `SyncResp` 向 Follower 同步日志
- Follower 通过 `SyncReq` 向 Leader 请求日志
- **限制**：只能同步 Leader 当前保留的日志范围

### 3. 数据库删除操作 (`pkg/wkdb/message.go`)
```go
func (wk *wukongDB) DeleteRangeMessages(channelId string, channelType uint8, startMessageSeq, endMessageSeq uint64) error
```
- 使用 `batch.DeleteRange` 删除指定范围的消息
- 操作是幂等的，但依赖于 Raft 日志的同步

## 问题根本原因

1. **缺少快照机制**：当前 Raft 实现没有 snapshot 机制
2. **日志截断策略**：为了节省空间，已应用的日志会被定期截断
3. **无法追溯历史**：故障节点恢复时，无法获取已被截断的日志
4. **缺少全量同步**：没有基于快照的全量数据同步机制

## 潜在影响

### 数据层面
- **数据不一致**：故障节点上的消息未被删除
- **查询结果不一致**：不同副本返回不同的消息列表
- **序列号错乱**：消息序列号可能出现空洞

### 业务层面
- **已删除消息重现**：用户以为已删除的消息仍然可见
- **隐私泄露风险**：敏感消息删除失败
- **合规性问题**：数据删除不彻底

### 运维层面
- **难以发现**：数据不一致可能长期存在而不被察觉
- **难以修复**：发现后需要手动介入修复
- **信任度降低**：系统可靠性受质疑

## 解决方案

### 方案一：实现 Raft Snapshot 机制（推荐）

#### 原理
参考 Raft 论文和 etcd 实现，增加快照功能：
1. Leader 定期创建应用状态快照
2. 快照包含所有已应用的数据状态（包括已删除消息的记录）
3. 故障节点恢复时，如果日志已被截断，则使用快照恢复

#### 实现要点

**1. 定义快照数据结构**
```go
// pkg/cluster/store/snapshot.go
type Snapshot struct {
    // 快照元数据
    LastIncludedIndex uint64  // 快照包含的最后一条日志索引
    LastIncludedTerm  uint32  // 快照包含的最后一条日志任期
    
    // 频道数据状态
    Channels map[string]*ChannelSnapshot
}

type ChannelSnapshot struct {
    ChannelID   string
    ChannelType uint8
    
    // 关键：记录已删除的消息范围
    DeletedRanges []MessageRange
    
    // 最大消息序列号
    MaxMessageSeq uint64
    
    // 其他必要的状态信息
}

type MessageRange struct {
    StartSeq uint64
    EndSeq   uint64
}
```

**2. 创建快照**
```go
func (s *Store) CreateSnapshot(slotId uint32, lastIndex uint64) (*Snapshot, error) {
    snapshot := &Snapshot{
        LastIncludedIndex: lastIndex,
        Channels:          make(map[string]*ChannelSnapshot),
    }
    
    // 遍历该 slot 的所有频道
    channels, err := s.wdb.GetChannelsBySlot(slotId)
    if err != nil {
        return nil, err
    }
    
    for _, ch := range channels {
        // 获取频道的消息状态
        maxSeq, err := s.wdb.GetMaxMessageSeq(ch.ChannelID, ch.ChannelType)
        if err != nil {
            return nil, err
        }
        
        // 保存频道快照
        snapshot.Channels[ch.Key()] = &ChannelSnapshot{
            ChannelID:     ch.ChannelID,
            ChannelType:   ch.ChannelType,
            MaxMessageSeq: maxSeq,
            DeletedRanges: []MessageRange{}, // 可以通过扫描消息序列号空洞来推断
        }
    }
    
    return snapshot, nil
}
```

**3. 应用快照**
```go
func (s *Store) ApplySnapshot(snapshot *Snapshot) error {
    for _, chSnap := range snapshot.Channels {
        // 恢复频道的删除状态
        for _, delRange := range chSnap.DeletedRanges {
            err := s.wdb.DeleteRangeMessages(
                chSnap.ChannelID,
                chSnap.ChannelType,
                delRange.StartSeq,
                delRange.EndSeq,
            )
            if err != nil {
                return err
            }
        }
    }
    return nil
}
```

**4. 修改日志截断逻辑**
```go
// pkg/cluster/slot/storage_pebble.go
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
    // 在截断之前，确保已经创建了快照
    lastSnapshotIndex, err := p.GetLastSnapshotIndex(shardNo)
    if err != nil {
        return err
    }
    
    // 只截断已经包含在快照中的日志
    if index <= lastSnapshotIndex {
        return p.doTruncate(shardNo, index)
    }
    
    // 如果截断点超过快照，先创建新快照
    // 这里需要触发快照创建流程
    return errors.New("need create snapshot before truncate")
}
```

#### 优点
- **彻底解决问题**：快照包含完整的数据状态
- **符合 Raft 标准**：标准的 Raft 实现方式
- **支持长时间故障**：节点可以长时间离线后恢复

#### 缺点
- **实现复杂度高**：需要大量开发和测试工作
- **存储开销**：需要额外存储空间保存快照
- **性能影响**：创建快照时可能影响系统性能

---

### 方案二：延长 Raft 日志保留时间

#### 原理
延长 Raft 日志的保留时间，确保短期故障节点恢复时仍能获取到删除日志。

#### 实现要点

**1. 添加配置项**
```yaml
# config/wk.yaml
cluster:
  # Raft 日志保留策略
  raft_log_retention:
    # 保留最近 N 条已应用的日志
    keep_applied_logs: 100000
    # 或基于时间：保留最近 N 小时的日志
    keep_hours: 48
    # 最小保留日志数（即使超过时间也保留）
    min_keep_logs: 10000
```

**2. 修改截断逻辑**
```go
// pkg/cluster/slot/storage_pebble.go
func (p *PebbleShardLogStorage) TruncateLogTo(shardNo string, index uint64) error {
    if index == 0 {
        return errors.New("index must be greater than 0")
    }
    
    lastLog, err := p.lastLog(shardNo)
    if err != nil {
        return err
    }
    lastIndex := lastLog.Index
    
    appliedIdx, err := p.AppliedIndex(shardNo)
    if err != nil {
        return err
    }
    
    // 新增：检查保留策略
    minKeepIndex := appliedIdx
    if appliedIdx > p.s.opts.RaftLogRetention.KeepAppliedLogs {
        minKeepIndex = appliedIdx - p.s.opts.RaftLogRetention.KeepAppliedLogs
    }
    
    // 只截断到保留策略允许的位置
    truncateIndex := index
    if truncateIndex < minKeepIndex {
        truncateIndex = minKeepIndex
    }
    
    if truncateIndex >= lastIndex {
        return nil
    }
    
    // 执行截断
    keyData := key.NewLogKey(shardNo, truncateIndex+1)
    maxKeyData := key.NewLogKey(shardNo, math.MaxUint64)
    return p.shardDB(shardNo).DeleteRange(keyData, maxKeyData, p.sync)
}
```

**3. 增加监控指标**
```go
// 监控 Raft 日志的大小和数量
func (p *PebbleShardLogStorage) GetLogMetrics(shardNo string) (*LogMetrics, error) {
    firstLog, err := p.firstLog(shardNo)
    if err != nil {
        return nil, err
    }
    
    lastLog, err := p.lastLog(shardNo)
    if err != nil {
        return nil, err
    }
    
    appliedIdx, err := p.AppliedIndex(shardNo)
    if err != nil {
        return nil, err
    }
    
    return &LogMetrics{
        FirstLogIndex:    firstLog.Index,
        LastLogIndex:     lastLog.Index,
        AppliedIndex:     appliedIdx,
        TotalLogCount:    lastLog.Index - firstLog.Index + 1,
        UnappliedCount:   lastLog.Index - appliedIdx,
    }, nil
}
```

#### 优点
- **实现简单**：改动较小，风险可控
- **立即见效**：可以快速部署
- **性能影响小**：只是延迟清理，不影响核心逻辑

#### 缺点
- **治标不治本**：只能应对短期故障，长时间故障仍会有问题
- **存储开销**：需要保留更多日志，增加磁盘使用
- **参数难调**：保留时间难以确定最优值

---

### 方案三：增加全量数据同步机制

#### 原理
当检测到副本节点数据落后过多时，触发全量数据同步。

#### 实现要点

**1. 检测数据落后**
```go
// pkg/cluster/slot/slot.go
func (s *Slot) CheckReplicaLag(replicaId uint64) (*ReplicaLagInfo, error) {
    // 获取副本的日志索引
    replicaLastIndex := s.GetReplicaLastIndex(replicaId)
    
    // 获取本地最旧的日志索引
    firstLogIndex, err := s.GetFirstLogIndex()
    if err != nil {
        return nil, err
    }
    
    // 判断副本是否落后太多
    lagInfo := &ReplicaLagInfo{
        ReplicaId:       replicaId,
        LastLogIndex:    replicaLastIndex,
        LeaderFirstLog:  firstLogIndex,
        IsLaggingBehind: replicaLastIndex < firstLogIndex,
    }
    
    return lagInfo, nil
}
```

**2. 触发全量同步**
```go
// pkg/cluster/store/sync.go
func (s *Store) FullSyncChannel(channelId string, channelType uint8, targetNodeId uint64) error {
    // 1. 锁定频道，暂停写入
    s.lockChannel(channelId, channelType)
    defer s.unlockChannel(channelId, channelType)
    
    // 2. 获取当前频道的所有消息序列号
    messageSeqs, err := s.wdb.GetAllMessageSeqs(channelId, channelType)
    if err != nil {
        return err
    }
    
    // 3. 发送给目标节点
    err = s.sendFullSyncData(targetNodeId, &FullSyncData{
        ChannelID:    channelId,
        ChannelType:  channelType,
        MessageSeqs:  messageSeqs, // 只发送序列号列表
        MaxSeq:       s.wdb.GetMaxMessageSeq(channelId, channelType),
    })
    if err != nil {
        return err
    }
    
    return nil
}

// 目标节点接收全量同步数据
func (s *Store) ApplyFullSyncData(data *FullSyncData) error {
    // 1. 获取本地的消息序列号
    localSeqs, err := s.wdb.GetAllMessageSeqs(data.ChannelID, data.ChannelType)
    if err != nil {
        return err
    }
    
    // 2. 比较差异，删除多余的消息
    seqSet := make(map[uint64]bool)
    for _, seq := range data.MessageSeqs {
        seqSet[seq] = true
    }
    
    // 3. 删除本地有但同步数据中没有的消息（即被删除的消息）
    for _, localSeq := range localSeqs {
        if !seqSet[localSeq] {
            err = s.wdb.DeleteMessage(data.ChannelID, data.ChannelType, localSeq)
            if err != nil {
                return err
            }
        }
    }
    
    return nil
}
```

**3. 集成到节点恢复流程**
```go
// pkg/cluster/cluster/server.go
func (s *Server) OnNodeRecovered(nodeId uint64) error {
    // 获取该节点负责的所有 slot
    slots := s.GetNodeSlots(nodeId)
    
    for _, slot := range slots {
        // 检查该节点在这个 slot 上的日志落后情况
        lagInfo, err := slot.CheckReplicaLag(nodeId)
        if err != nil {
            return err
        }
        
        // 如果落后太多，触发全量同步
        if lagInfo.IsLaggingBehind {
            s.Info("节点落后过多，触发全量同步",
                zap.Uint64("nodeId", nodeId),
                zap.Uint32("slotId", slot.Id),
                zap.Uint64("replicaLastIndex", lagInfo.LastLogIndex),
                zap.Uint64("leaderFirstIndex", lagInfo.LeaderFirstLog))
            
            // 对该 slot 下的所有频道进行全量同步
            err = s.fullSyncSlot(slot.Id, nodeId)
            if err != nil {
                return err
            }
        }
    }
    
    return nil
}
```

#### 优点
- **精准修复**：只同步有差异的数据
- **不依赖日志**：绕过 Raft 日志机制
- **可观测性强**：明确知道何时触发同步

#### 缺点
- **实现复杂**：需要设计同步协议和状态机
- **性能开销**：全量扫描消息序列号可能较慢
- **可能阻塞**：同步期间可能影响服务

---

### 方案四：记录删除操作的元数据（轻量级快照）

#### 原理
不保存完整快照，只保存删除操作的元数据记录，作为轻量级的状态追踪。

#### 实现要点

**1. 定义删除记录表**
```go
// pkg/wkdb/message_delete_log.go
type MessageDeleteLog struct {
    ChannelID    string
    ChannelType  uint8
    StartSeq     uint64
    EndSeq       uint64
    DeletedAt    time.Time
    LogIndex     uint64  // 对应的 Raft 日志索引
}

// 存储删除记录
func (wk *wukongDB) SaveDeleteLog(log *MessageDeleteLog) error {
    // 持久化删除记录，不随 Raft 日志截断而删除
    key := makeDeleteLogKey(log.ChannelID, log.ChannelType, log.LogIndex)
    data, err := log.Marshal()
    if err != nil {
        return err
    }
    return wk.metaDB.Put(key, data)
}

// 获取某个日志索引之后的所有删除记录
func (wk *wukongDB) GetDeleteLogsSince(channelID string, channelType uint8, sinceLogIndex uint64) ([]*MessageDeleteLog, error) {
    // 查询并返回
}
```

**2. 修改删除操作**
```go
// pkg/cluster/store/store_apply.go
func (s *Store) handleDeleteRangeMessages(cmd *CMD) error {
    channelId, channelType, startMessageSeq, endMessageSeq, err := cmd.DecodeCMDDeleteRangeMessages()
    if err != nil {
        return err
    }
    
    // 执行删除
    err = s.wdb.DeleteRangeMessages(channelId, channelType, startMessageSeq, endMessageSeq)
    if err != nil {
        return err
    }
    
    // 新增：记录删除操作到元数据表
    deleteLog := &wkdb.MessageDeleteLog{
        ChannelID:   channelId,
        ChannelType: channelType,
        StartSeq:    startMessageSeq,
        EndSeq:      endMessageSeq,
        DeletedAt:   time.Now(),
        LogIndex:    cmd.LogIndex, // 需要在 CMD 中携带日志索引
    }
    
    err = s.wdb.SaveDeleteLog(deleteLog)
    if err != nil {
        s.Error("保存删除日志失败", zap.Error(err))
        // 这里可以选择不返回错误，因为删除操作本身已成功
    }
    
    return nil
}
```

**3. 节点恢复时补偿**
```go
// pkg/cluster/store/recovery.go
func (s *Store) RecoverFromDeleteLogs(channelID string, channelType uint8, lastAppliedIndex uint64) error {
    // 获取该频道在此索引之后的所有删除记录
    deleteLogs, err := s.wdb.GetDeleteLogsSince(channelID, channelType, lastAppliedIndex)
    if err != nil {
        return err
    }
    
    s.Info("应用补偿删除记录",
        zap.String("channelID", channelID),
        zap.Uint8("channelType", channelType),
        zap.Int("count", len(deleteLogs)))
    
    // 重新应用这些删除操作
    for _, log := range deleteLogs {
        err = s.wdb.DeleteRangeMessages(
            log.ChannelID,
            log.ChannelType,
            log.StartSeq,
            log.EndSeq,
        )
        if err != nil {
            s.Error("补偿删除失败", zap.Error(err), zap.Any("log", log))
            return err
        }
    }
    
    return nil
}
```

**4. 定期清理旧记录**
```go
// 清理 30 天前的删除记录
func (wk *wukongDB) CleanupOldDeleteLogs(beforeDays int) error {
    cutoffTime := time.Now().AddDate(0, 0, -beforeDays)
    return wk.metaDB.DeleteRange(
        makeDeleteLogKeyPrefix(),
        makeDeleteLogKeyWithTime(cutoffTime),
    )
}
```

#### 优点
- **轻量级**：只存储元数据，存储开销小
- **实现相对简单**：不需要完整的快照机制
- **可长期保留**：删除记录可以保留很长时间
- **支持审计**：可以追溯删除历史

#### 缺点
- **只解决删除问题**：不能解决其他类型的数据不一致
- **需要额外维护**：需要管理删除记录的生命周期
- **恢复时间较长**：如果删除记录很多，恢复会较慢

---

## 推荐方案

### 短期方案（1-2周实现）
**方案四（删除操作元数据记录） + 方案二（延长日志保留）**

理由：
1. **快速见效**：可以立即解决当前问题
2. **风险可控**：改动较小，测试充分后可快速上线
3. **存储开销小**：删除操作通常不频繁，元数据占用空间很小
4. **双重保险**：日志保留解决短期故障，元数据记录解决长期故障

### 长期方案（2-3个月实现）
**方案一（完整的 Raft Snapshot 机制）**

理由：
1. **彻底解决**：不仅解决删除问题，还能处理所有类型的数据同步
2. **符合标准**：标准的 Raft 实现，经过广泛验证
3. **支持扩展**：为未来的功能（如数据迁移、备份恢复）打下基础
4. **提升可靠性**：显著提升系统的容错能力

## 测试建议

### 功能测试
1. **正常删除**：验证删除功能基本可用
2. **短期故障恢复**：节点离线 5 分钟后恢复
3. **长期故障恢复**：节点离线 24 小时后恢复
4. **日志截断后恢复**：确保日志被截断后节点仍能恢复

### 压力测试
1. **高频删除**：持续高频率的删除操作
2. **大范围删除**：删除数万条消息
3. **并发删除**：多个频道同时删除
4. **故障注入**：随机让副本节点故障和恢复

### 数据一致性测试
```go
// test/consistency/message_delete_test.go
func TestMessageDeleteConsistency(t *testing.T) {
    // 1. 启动 3 节点集群
    cluster := setupCluster(3)
    
    // 2. 写入消息
    for i := 1; i <= 1000; i++ {
        cluster.SendMessage("test-channel", fmt.Sprintf("msg-%d", i))
    }
    
    // 3. 停止 node3
    cluster.StopNode(3)
    
    // 4. 删除消息 100-200
    cluster.DeleteMessages("test-channel", 100, 200)
    
    // 5. 等待日志截断
    time.Sleep(10 * time.Minute) // 触发日志截断
    
    // 6. 恢复 node3
    cluster.StartNode(3)
    
    // 7. 等待同步完成
    cluster.WaitForSync()
    
    // 8. 验证所有节点数据一致
    for i := 1; i <= 3; i++ {
        messages := cluster.GetMessages(i, "test-channel")
        assert.Equal(t, 900, len(messages), "node %d should have 900 messages", i)
        
        // 验证被删除的消息不存在
        for seq := 100; seq <= 200; seq++ {
            assert.False(t, messageExists(messages, seq), 
                "node %d should not have message seq %d", i, seq)
        }
    }
}
```

## 监控指标

### 新增指标
```go
// 删除操作指标
wukongim_message_delete_total           // 删除操作总数
wukongim_message_delete_range_total     // 范围删除操作总数
wukongim_message_delete_records_count   // 删除记录总数

// Raft 日志指标
wukongim_raft_log_count                 // Raft 日志数量
wukongim_raft_log_size_bytes            // Raft 日志大小
wukongim_raft_log_first_index           // 最早日志索引
wukongim_raft_log_last_index            // 最新日志索引
wukongim_raft_log_applied_index         // 已应用索引

// 数据一致性指标
wukongim_replica_lag_count              // 副本落后数量
wukongim_replica_full_sync_total        // 全量同步次数
wukongim_replica_recovery_total         // 节点恢复次数
```

### 告警规则
```yaml
# prometheus/alerts/wukongim.yml
groups:
  - name: wukongim_consistency
    rules:
      - alert: RaftLogTooLarge
        expr: wukongim_raft_log_count > 1000000
        for: 5m
        annotations:
          summary: "Raft 日志过多，可能影响恢复时间"
          
      - alert: ReplicaLaggingBehind
        expr: wukongim_raft_log_last_index - wukongim_raft_log_first_index > 10000
        for: 10m
        annotations:
          summary: "副本落后过多，可能无法通过日志恢复"
          
      - alert: DeleteRecordsTooMany
        expr: wukongim_message_delete_records_count > 100000
        for: 1h
        annotations:
          summary: "删除记录过多，需要清理"
```

## 参考资料

1. [Raft 论文](https://raft.github.io/raft.pdf) - 第 7 节：Log Compaction
2. [etcd Raft Snapshot 实现](https://github.com/etcd-io/etcd/tree/main/server/storage/wal)
3. [TiKV Snapshot 机制](https://tikv.org/deep-dive/raft-in-tikv/introduction/)
4. WuKongIM 现有文档：
   - `docs/api/raft_log_cleanup_quick_reference.md`（如存在）
   - `docs/api/raft_log_and_message_storage_analysis.md`（如存在）

## 总结

节点故障后的消息删除一致性问题是分布式系统中的经典问题，需要通过 **快照机制** 或 **元数据记录** 来解决。建议采用短期 + 长期的组合方案，在保证系统稳定的前提下逐步完善。

最重要的是增加完善的**监控和告警**，及时发现数据不一致问题，并通过**自动化测试**确保修复方案的有效性。
