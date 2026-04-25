# L3 · Channel 消息层

> 一致性算法：**ISR（In-Sync Replicas）**，每个频道一个独立的 ISR 组
> 职责：以频道为粒度管理消息数据——存储、复制、提交、查询

## 1. 概述

Channel 层是消息的"最后一公里"。它为每个频道维护一个独立的 ISR 组，采用类似 Kafka 的 ISR 复制协议（而非 Raft），实现了：

- **更短的写入路径**：不需要 Raft 日志复制全流程，Leader 直接推送给 Follower
- **灵活的提交策略**：通过 MinISR 参数控制提交门槛，可在一致性和性能间取舍
- **百万级频道扩展**：每个频道一个 ISR 组，彼此独立，天然适合水平扩展
- **精确去重**：基于 (FromUID, ClientMsgNo) 的幂等写入，保证 Exactly-Once 语义

## 2. 代码结构

```
pkg/
├── replication/
│   ├── isr/                    # ISR 共识核心引擎
│   │   ├── replica.go          # Replica 实现（219 行）
│   │   ├── append.go           # 消息追加 + Group Commit
│   │   ├── fetch.go            # Fetch 协议（Leader → Follower）
│   │   ├── progress.go         # HW 推进 + 副本进度跟踪
│   │   ├── progress_ack.go     # ProgressAck 处理
│   │   ├── recovery.go         # 启动恢复
│   │   ├── history.go          # Epoch History 管理
│   │   ├── types.go            # 类型与接口定义（159 行）
│   │   └── meta.go             # 元数据校验
│   │
│   └── isrnode/                # 节点级 ISR 编排
│       ├── runtime.go          # 多 ISR 组运行时
│       ├── group.go            # 单组状态管理
│       ├── scheduler.go        # 任务调度器
│       ├── transport.go        # 消息分发
│       ├── batch.go            # 请求批处理
│       └── session.go          # Peer 会话管理
│
└── storage/
    └── channellog/             # 频道日志存储
        ├── types.go            # 频道/消息类型（175 行）
        ├── cluster.go          # 集群抽象
        ├── send.go             # 消息追加入口
        ├── fetch.go            # 消息查询入口
        ├── meta.go             # 元数据应用
        ├── store.go            # Channel Store
        ├── db.go               # Pebble 数据库
        ├── log_store.go        # 日志持久化
        ├── checkpoint_store.go # Checkpoint 持久化
        ├── history_store.go    # Epoch History 持久化
        ├── state_store.go      # 幂等状态存储
        ├── isr_bridge.go       # ISR 接口适配
        ├── codec.go            # 消息编解码
        ├── seq_read.go         # 按序列号查询
        ├── store_keys.go       # Pebble Key 编码
        ├── groupkey.go         # GroupKey 派生
        ├── replica.go          # Replica 工厂
        ├── apply.go            # Follower Apply + 幂等提交
        └── commit_coordinator.go # 提交协调器
```

## 3. ISR 共识协议

### 3.1 核心概念

| 概念          | 说明                                                              |
| ------------- | ----------------------------------------------------------------- |
| ISR           | In-Sync Replicas，与 Leader 日志保持同步的副本子集               |
| HW            | High Water Mark，ISR 内所有副本都已确认的 offset，对外可见的提交点 |
| LEO           | Log End Offset，本地日志的末尾 offset                             |
| Epoch         | 配置版本号，每次 ISR 成员变化或 Leader 切换时递增                |
| MinISR        | 最小同步副本数，HW 推进至少需要这么多副本确认                    |
| Lease         | Leader 租约，过期后降级为 FencedLeader                            |

### 3.2 角色状态

```
                  MetaChange(leader=self)
   Follower  ──────────────────────────▸  Leader
                                            │
                                            │ Lease 过期
                                            ▼
                                       FencedLeader
                                       （只读不写）

   任何角色  ──────[关闭]──────▸  Tombstoned
```

### 3.3 GroupMeta 结构

```go
// isr/types.go:20-28
type GroupMeta struct {
    GroupKey   GroupKey       // "channel/<type>/<base64_id>"
    Epoch      uint64         // 配置版本号
    Leader     NodeID         // 当前 Leader
    Replicas   []NodeID       // 所有副本
    ISR        []NodeID       // 同步副本集
    MinISR     int            // 最小同步副本数
    LeaseUntil time.Time      // Leader 租约到期时间
}
```

**校验规则**（`isr/meta.go:34-63`）：
- Leader 必须在 Replicas 中
- Leader 必须在 ISR 中
- ISR 的所有成员必须在 Replicas 中
- MinISR 必须在 `[1, len(Replicas)]` 范围内

## 4. 消息追加流程

### 4.1 完整写入链路

```
Client
  │ 1. channellog.Append(channelKey, message)           // send.go:13
  ▼
channellog 层
  │ 2. 校验：频道存在? Active? Epoch 匹配? LeaderEpoch 匹配?
  │ 3. 幂等检查：(FromUID, ClientMsgNo) 是否已存在?
  │    ├─ 重复 → 直接返回缓存结果
  │    └─ 首次 → 继续
  │ 4. 消息编码为二进制
  ▼
isr.Replica.Append(records)                              // append.go:14
  │ 5. 校验：必须是 Leader? Lease 未过期? len(ISR) >= MinISR?
  │ 6. Group Commit 批处理：
  │    │  等待 maxWait(1ms) 或凑够 maxRecords(64) / maxBytes(64KB)
  │    │  将多个并发 Append 合并为一批
  │ 7. 写入本地日志 → LEO 前进
  ▼
ISR 复制（推模型）
  │ 8. Leader 通过 FetchResponse 将新记录推送给所有 ISR Follower
  │ 9. Follower 收到后：
  │    │  a. ApplyFetch → 写入本地日志
  │    │  b. 发送 ProgressAck(matchOffset) 给 Leader
  ▼
HW 推进                                                   // progress.go:23-49
  │ 10. Leader 收集 ISR 中所有副本的 matchOffset
  │ 11. 排序后取第 (len(ISR) - MinISR + 1) 大的值 → 候选 HW
  │     例：ISR=[A,B,C], MinISR=2, progress=[100,80,90]
  │         排序=[80,90,100], candidateHW = sorted[3-2] = sorted[1] = 90
  │ 12. 如果候选 HW > 当前 HW → 推进 HW，持久化 Checkpoint
  ▼
返回给客户端
  │ 13. messageSeq = HW + 1（1-indexed）
  │ 14. 原子写入幂等记录
  ▼
完成
```

### 4.2 Group Commit 机制

为了提高吞吐，多个并发的 Append 请求会被合并为一批：

```go
// append.go:100-120
func collectAppendBatch(maxWait, maxRecords, maxBytes) {
    // 等待 maxWait 时间，或凑够 maxRecords 条记录 / maxBytes 字节
    // 将多个 Append 的 records 合并为一个大批次
    // 一次 fsync 持久化整批数据
}
```

默认参数：
- maxWait = 1ms
- maxRecords = 64
- maxBytes = 64KB

### 4.3 幂等去重

```go
// send.go:58-80
// 复合幂等键：(ChannelID, ChannelType, FromUID, ClientMsgNo)
entry := stateStore.Get(fromUID, clientMsgNo)
if entry != nil {
    if entry.PayloadHash == hash(payload) {
        return entry.Result   // 真重复，返回缓存结果
    }
    return ConflictError      // 同 Key 不同内容，冲突
}
```

幂等记录与 Checkpoint 原子写入，保证 Exactly-Once 语义。

## 5. ISR 复制协议

### 5.1 Fetch 协议（Leader → Follower）

```
Follower                               Leader
   │                                      │
   │ ◄──── FetchResponse ────────────────│
   │    (records, leaderHW, truncateTo)   │
   │                                      │
   │──── ProgressAck(matchOffset) ──────▸ │
   │                                      │
   │ ◄──── FetchResponse ────────────────│
   │    (new records, updated HW)         │
   ▼                                      ▼
```

**Fetch 处理逻辑**（`fetch.go:5-73`）：

1. Leader 验证 Epoch 一致性
2. 检查 Follower 的 FetchOffset 和 OffsetEpoch
3. 如果 Epoch 不匹配 → 计算分歧点，返回 truncateTo 要求 Follower 截断
4. 返回 `[FetchOffset, min(FetchOffset + maxBytes, LEO))` 范围内的记录
5. 附带当前 HW，让 Follower 更新本地 HW

### 5.2 分歧检测与恢复

当发生 Leader 切换后，旧 Leader 和新 Leader 的日志可能存在分歧：

```
旧 Leader 日志：  [1, 2, 3, 4(epoch=1), 5(epoch=1)]
新 Leader 日志：  [1, 2, 3, 4(epoch=2), 5(epoch=2), 6(epoch=2)]

Follower（旧 Leader）发送 FetchRequest(offset=6, epoch=1)
                                         ↓
Leader 查找 epoch 1 在 EpochHistory 中的范围
                                         ↓
epoch 1 的 startOffset=1, nextEpochStart=4
                                         ↓
返回 truncateTo=4，要求 Follower 截断 offset 4 及之后的数据
                                         ↓
Follower 截断到 offset 3，重新从 offset 4 拉取新 Leader 的数据
```

**EpochHistory**（`history.go`）：记录每个 Epoch 的起始 offset，用于精确定位分歧点。

### 5.3 HW 推进算法

```go
// progress.go:50-75
func (r *replica) calculateCandidateHW() uint64 {
    matches := make([]uint64, 0, len(ISR))
    for _, nodeID := range r.meta.ISR {
        matches = append(matches, r.progress[nodeID].matchOffset)
    }
    sort.Sort(sort.Reverse(uint64Slice(matches)))
    // 取第 MinISR 大的值
    return matches[r.meta.MinISR - 1]
}
```

例：3 副本 ISR，MinISR=2
- 进度：[A=100, B=90, C=80]
- 排序降序：[100, 90, 80]
- candidateHW = matches[2-1] = matches[1] = 90

含义：至少有 2 个副本（A 和 B）都确认了 offset ≤ 90 的数据。

## 6. Leader 角色管理

### 6.1 BecomeLeader

```go
// replica.go:132-183
func (r *replica) BecomeLeader(meta GroupMeta) {
    // 1. 验证恢复状态
    // 2. 截断 LEO 到 HW（防止未提交数据泄露）
    // 3. 初始化 EpochHistory（记录新 Epoch 起始点）
    // 4. 初始化所有副本的进度（self=LEO, others=HW）
    // 5. 检查 Lease 是否过期
}
```

关键：**新 Leader 会截断 LEO 到 HW**，确保未提交的数据不会对外可见。

### 6.2 Lease 机制

```go
// append.go:28-32
if now.After(r.meta.LeaseUntil) {
    r.role = RoleFencedLeader   // 降级为只读
    return ErrLeaseExpired
}
```

- Leader 持有租约（LeaseUntil），在租约期内可以接受写入。
- 租约过期后，Leader 降级为 FencedLeader：
  - 拒绝新的 Append 请求
  - 仍然可以响应 Fetch 请求（供 Follower 追数据）
- 防止双 Leader 同时写入（脑裂保护）。

### 6.3 BecomeFollower

```go
// replica.go
func (r *replica) BecomeFollower(meta GroupMeta) {
    r.role = RoleFollower
    r.meta = meta
    // Follower 不需要维护 progress 表
    // 被动接收 Leader 推送的 FetchResponse
}
```

## 7. 存储层

### 7.1 Pebble Keyspace 设计

```
Prefix   | Key 格式                                     | 用途
---------|----------------------------------------------|-----
0x10     | [0x10 | groupKey | offset(uint64)]           | 消息日志
0x11     | [0x11 | groupKey]                             | Checkpoint（Epoch + HW）
0x12     | [0x12 | groupKey | startOffset(uint64)]       | Epoch History
0x13     | [0x13 | groupKey]                             | 快照
0x14     | [0x14 | groupKey | fromUID | clientMsgNo]     | 幂等状态
```

### 7.2 消息编码格式

```
[Version:1] [Header:45 bytes] [Variable-length fields]

Header:
  MessageID(8) | Framer(1) | Setting(1) | StreamFlag(1) | ChannelType(1) |
  Expire(4) | ClientSeq(4) | StreamID(4) | Timestamp(4) | PayloadHash(4) |
  ...

Variable-length:
  MsgKey | ClientMsgNo | StreamNo | ChannelID | Topic | FromUID | Payload
```

### 7.3 日志写入

```go
// log_store.go:20-71
func (s *Store) appendPayloads(records []Record) error {
    batch := s.db.NewBatch()
    for _, record := range records {
        key := encodeLogKey(s.groupKey, offset)   // [0x10 | groupKey | offset]
        batch.Set(key, record.Payload)
        offset++
    }
    batch.Commit(pebble.Sync)   // fsync 保证持久化
    s.cachedLEO = offset        // 更新内存中的 LEO 缓存
}
```

### 7.4 Checkpoint 持久化

```go
// checkpoint_store.go
type Checkpoint struct {
    Epoch          uint64    // 当前 Epoch
    LogStartOffset uint64   // 日志起始 offset
    HW             uint64   // High Water Mark
}
```

Checkpoint 在 HW 推进时原子写入 Pebble，用于重启恢复。

### 7.5 ISR Bridge 适配

`isr_bridge.go` 将 channellog.Store 适配为 ISR 的各种接口：

```go
// isr_bridge.go:10-113
type isrLogStoreBridge struct {
    store *Store
}

func (b *isrLogStoreBridge) Append(records) error {
    return b.store.appendPayloadsNoSync(records)  // 不 fsync（Group Commit 批处理）
}

func (b *isrLogStoreBridge) LEO() uint64 {
    if b.store.writeInProgress {
        return b.store.cachedLEO  // 写入中使用缓存值
    }
    return b.store.durableLEO()   // 否则从 Pebble 读取
}
```

## 8. 消息查询

### 8.1 查询接口

```go
// seq_read.go
func (s *Store) LoadMsg(seq uint64) (*Message, error)
func (s *Store) LoadNextRangeMsgs(startSeq, endSeq, limit) ([]*Message, error)
func (s *Store) LoadPrevRangeMsgs(startSeq, endSeq, limit) ([]*Message, error)
```

### 8.2 查询约束

- 所有查询都被 HW 限制——只返回已提交的消息
- `seq` 是 1-indexed 的用户可见序号，内部 offset 是 0-indexed
- 转换关系：`offset = seq - 1`，`seq = offset + 1`

### 8.3 Fetch 接口（fetch.go:5-72）

```go
func (c *cluster) Fetch(key ChannelKey, startSeq uint64, limit int) ([]*Message, error) {
    committedSeq := group.Status().HW  // 获取当前 HW
    if startSeq > committedSeq {
        return nil, nil  // 没有新数据
    }
    records := c.log.Read(groupKey, startSeq-1, limit, maxBytes)
    messages := decode(records)  // 解码并截止到 HW
    return messages, nil
}
```

## 9. ISR Node 编排（isrnode）

### 9.1 Runtime

```go
// isrnode/runtime.go:24-79
type Runtime struct {
    groups     map[GroupKey]*group     // 活跃的 ISR 组
    scheduler  *scheduler             // 任务调度
    sessions   *sessionCache          // Peer 会话复用
    limits     Limits                 // 资源限制
}
```

### 9.2 任务类型

每个 ISR 组通过 bitmask 标记待处理的任务：

| 任务       | 说明                                   |
| ---------- | -------------------------------------- |
| control    | 元数据变更（BecomeLeader/Follower）    |
| replication| 复制进度推进（Fetch / ProgressAck）    |
| commit     | HW 推进 & Checkpoint 持久化           |
| lease      | 租约续期/过期检查                      |
| snapshot   | 快照创建/恢复                          |

### 9.3 消息传输

```go
// isrnode/types.go:36-112
type Envelope struct {
    Peer       NodeID
    GroupKey   GroupKey
    Epoch      uint64
    Generation uint64
    RequestID  uint64
    Kind       MessageKind
    Payload    []byte
}

// MessageKind 枚举
FetchRequest  | FetchResponse | FetchFailure |
ProgressAck   | Truncate      | SnapshotChunk | Ack
```

### 9.4 背压控制

```go
// batch.go
type BackpressureLevel int

const (
    BackpressureNone BackpressureLevel = iota  // 正常
    BackpressureSoft                            // 减缓发送
    BackpressureHard                            // 停止发送
)
```

当 Follower 处理速度跟不上时，背压机制会限制 Leader 的发送速率。

## 10. Commit 协调器

### 10.1 跨频道批处理

```go
// commit_coordinator.go:23-100
// 单个后台 Worker 处理多个频道的提交请求
// 在 flushWindow（默认 200μs）内合并多个频道的写入
// 一次 Pebble batch commit 覆盖多个频道的 Checkpoint + 幂等状态
```

这是关键性能优化：多个频道的 fsync 可以合并，大幅降低 I/O 开销。

### 10.2 原子提交

```go
// apply.go
func CommitCommittedWithCheckpoint(batch, checkpoint, idempotencyEntry) {
    // 在同一个 Pebble batch 中：
    //   1. 写入 Checkpoint（新 HW）
    //   2. 写入幂等记录
    // 原子提交，保证不会出现 HW 前进但幂等记录丢失的情况
}
```

## 11. GroupKey 派生

```go
// groupkey.go:10-15
func channelGroupKey(key ChannelKey) isr.GroupKey {
    return isr.GroupKey(fmt.Sprintf("channel/%d/%s",
        key.ChannelType,
        base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID)),
    ))
}
```

每个业务频道（ChannelID + ChannelType）确定性地映射为一个 ISR GroupKey。

## 12. 恢复流程

### 12.1 启动恢复（recovery.go）

```
1. 从 Pebble 读取 Checkpoint → 恢复 HW、Epoch、LogStartOffset
2. 从 Pebble 读取 EpochHistory → 恢复 Epoch 历史记录
3. 从 Pebble 读取 LEO → 恢复日志末尾位置
4. 校验：HW ≤ LEO（否则数据损坏）
5. 校验：EpochHistory 单调递增
6. 设置 Role = Follower（等待 Group 层下发元数据）
```

### 12.2 分歧恢复

当 Follower 发现 Epoch 不匹配时：
1. Leader 根据 EpochHistory 计算分歧点
2. 返回 truncateTo 给 Follower
3. Follower 截断本地日志到分歧点
4. 从分歧点开始重新拉取 Leader 的数据

## 13. ISR vs Raft 对比

| 维度         | ISR（Channel 层）                | Raft（Group 层）                   |
| ------------ | -------------------------------- | ---------------------------------- |
| 写入路径     | Leader 直接推送 → HW 推进       | Leader 提交日志 → 多数确认         |
| 提交门槛     | MinISR（可配置）                 | 多数派（固定）                     |
| Leader 选举  | 外部指定（由 Group 层元数据决定）| 内部选举（etcd/raft 自动）         |
| 分歧恢复     | Epoch History + 截断            | Raft 日志自动覆盖                  |
| 成员变更     | 元数据下发                      | Raft 配置变更（AddVoter 等）       |
| 适用规模     | 百万级组                        | 数百级组                           |
| 复杂度       | 低（无选举协议）                | 高（完整 Raft 协议栈）             |

## 14. 关键配置

| 参数                       | 默认值  | 说明                              |
| -------------------------- | ------- | --------------------------------- |
| MinISR                     | -       | 最小同步副本数（频道级别）        |
| Group Commit maxWait       | 1ms     | 批处理等待时间                    |
| Group Commit maxRecords    | 64      | 批处理最大记录数                  |
| Group Commit maxBytes      | 64KB    | 批处理最大字节数                  |
| Commit flushWindow         | 200μs   | 跨频道 fsync 合并窗口            |
| Backpressure thresholds    | -       | Soft/Hard 背压级别               |
| MaxFetchInflightPeer       | -       | 每 Peer 最大并行 Fetch 请求数    |
| MaxSnapshotInflight        | -       | 最大并行快照传输数               |
| MaxRecoveryBytesPerSecond  | -       | 恢复限速                         |
