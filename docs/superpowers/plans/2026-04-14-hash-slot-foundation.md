# Hash Slot 路由基础 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将路由从 `CRC32 % SlotCount` 改为 `CRC32 % HashSlotCount → Assignment[] → SlotID`，Pebble Key 前缀从 `slotID:8` 改为 `hashSlot:2`，所有现有功能在新路由下正常工作。

**Architecture:** 引入 HashSlotTable 数据结构管理固定数量 hash slot 到动态物理 Slot 的映射。Key 编码层、StateMachine、Router、Config 全部适配。本 Plan 不含迁移逻辑，物理 Slot 仍静态分配。

**前提假设：** 项目尚未上线，无存量数据，不需要旧格式兼容。

> **WARNING:** 本 Plan 单独部署时不支持在线迁移。如果外部触发 hash slot 重分配，命令可能路由到错误的物理 Slot。必须与 Plan B（迁移执行器）一起部署才安全。

**Tech Stack:** Go, Pebble, etcd/raft

**Design doc:** `docs/wiki/design/slot-auto-scaling.md`

---

## File Structure

### New files
- `pkg/cluster/hashslottable.go` — HashSlotTable 数据结构、Lookup、Assignment 管理
- `pkg/cluster/hashslottable_test.go` — 单元测试

### Modified files
- `pkg/slot/meta/codec.go` — `slot uint64` → `hashSlot uint16` in all encode functions
- `pkg/slot/meta/shard_spans.go` — span 按 hashSlot 组织（prefix 从 9 字节降到 3 字节）
- `pkg/slot/meta/db.go` — `ForSlot(uint64)` → `ForHashSlot(uint16)` + `ForHashSlots([]uint16)`
- `pkg/slot/meta/batch.go` — 所有 WriteBatch 方法 `slot uint64` → `hashSlot uint16`
- `pkg/slot/meta/snapshot.go` — 快照按 hash slot 集合导出/导入
- `pkg/slot/meta/snapshot_codec.go` — slotID → hashSlot in codec
- `pkg/slot/multiraft/types.go` — Command 增加 HashSlot uint16 字段
- `pkg/slot/fsm/statemachine.go` — 验证 hashSlot 归属替代 slotID 验证
- `pkg/slot/fsm/command.go` — `apply(wb, slot)` → `apply(wb, hashSlot uint16)`
- `pkg/cluster/router.go` — HashSlotTable 查表替代 CRC32 取模
- `pkg/cluster/config.go` — 新增 HashSlotCount，SlotCount 改为物理 Slot 初始数
- `pkg/cluster/cluster.go` — 创建并下发 HashSlotTable
- `pkg/slot/proxy/store.go` — SlotForKey 返回物理 Slot + 携带 hashSlot
- `internal/app/build.go` — 透传 HashSlotCount

### Test files to update
- `pkg/slot/meta/*_test.go` — 全部 `slot uint64` 参数改为 `hashSlot uint16`
- `pkg/slot/meta/testutil_test.go` — 测试工具方法适配
- `pkg/slot/fsm/state_machine_test.go` — Command.HashSlot + ownership 验证
- `pkg/slot/fsm/testutil_test.go` — 测试工具适配
- `pkg/cluster/router_test.go` — 新路由测试
- `pkg/cluster/cluster_test.go` — 集成测试适配
- `pkg/cluster/cluster_integration_test.go` — 集成测试适配

---

### Task 1: HashSlotTable 数据结构

**Files:**
- Create: `pkg/cluster/hashslottable.go`
- Create: `pkg/cluster/hashslottable_test.go`

- [ ] **Step 1: Write HashSlotTable tests**

```go
// pkg/cluster/hashslottable_test.go
func TestHashSlotTable_Lookup(t *testing.T) {
    table := NewHashSlotTable(256, 4) // 256 hash slots, 4 physical slots
    // Initial assignment: even distribution
    // hash slots 0-63 → slot 1, 64-127 → slot 2, 128-191 → slot 3, 192-255 → slot 4
    assert slot for hash 0 == 1
    assert slot for hash 63 == 1
    assert slot for hash 64 == 2
    assert slot for hash 255 == 4
}

func TestHashSlotTable_Reassign(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    table.Reassign(64, 1) // move hash slot 64 from slot 2 to slot 1
    assert table.Lookup(64) == 1
    assert table.Version() == 2
}

func TestHashSlotTable_SlotsOwning(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    owned := table.HashSlotsOf(1) // slot 1 owns hash slots 0-63
    assert len(owned) == 64
}

func TestHashSlotTable_SlotForKey(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    // CRC32("test") % 256 → deterministic hash slot → deterministic physical slot
    hs := HashSlotForKey("test", 256)
    slotID := table.Lookup(hs)
    assert slotID >= 1 && slotID <= 4
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/cluster/ -run TestHashSlotTable -v`

- [ ] **Step 3: Implement HashSlotTable**

```go
// pkg/cluster/hashslottable.go
package raftcluster

import (
    "hash/crc32"
    "sync/atomic"
    "github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type HashSlotTable struct {
    version    uint64
    assignment []multiraft.SlotID // [hashSlotCount]SlotID
    hashSlotCount uint16
}

func NewHashSlotTable(hashSlotCount uint16, physicalSlotCount int) *HashSlotTable { ... }
func (t *HashSlotTable) Lookup(hashSlot uint16) multiraft.SlotID { ... }
func (t *HashSlotTable) Reassign(hashSlot uint16, slotID multiraft.SlotID) { ... }
func (t *HashSlotTable) HashSlotsOf(slotID multiraft.SlotID) []uint16 { ... }
func (t *HashSlotTable) Version() uint64 { ... }
func (t *HashSlotTable) Clone() *HashSlotTable { ... }
func HashSlotForKey(key string, hashSlotCount uint16) uint16 {
    return uint16(crc32.ChecksumIEEE([]byte(key)) % uint32(hashSlotCount))
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./pkg/cluster/ -run TestHashSlotTable -v`

- [ ] **Step 5: Implement Encode/Decode for Raft persistence and heartbeat**

```go
func (t *HashSlotTable) Encode() []byte { ... }     // 序列化为 Controller Raft 命令 / 心跳响应
func DecodeHashSlotTable(data []byte) (*HashSlotTable, error) { ... }

func TestHashSlotTable_EncodeDecode(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    data := table.Encode()
    decoded, err := DecodeHashSlotTable(data)
    assert no error
    assert decoded.Version() == table.Version()
    for hs := uint16(0); hs < 256; hs++ {
        assert decoded.Lookup(hs) == table.Lookup(hs)
    }
}
```

- [ ] **Step 6: Run all tests to verify pass**

Run: `go test ./pkg/cluster/ -run TestHashSlotTable -v`

- [ ] **Step 7: Commit**

```bash
git add pkg/cluster/hashslottable.go pkg/cluster/hashslottable_test.go
git commit -m "feat: add HashSlotTable data structure with encode/decode for Raft persistence"
```

---

### Task 2: Key 编码层改造（codec.go + shard_spans.go）

**Files:**
- Modify: `pkg/slot/meta/codec.go` — 所有 `slot uint64` 参数改为 `hashSlot uint16`
- Modify: `pkg/slot/meta/shard_spans.go` — span prefix 从 `[kind:1][slot:8]` 改为 `[kind:1][hashSlot:2]`
- Modify: `pkg/slot/meta/codec_test.go`

- [ ] **Step 1: Update codec_test.go for new signature**

把所有测试中的 `slot uint64` 参数改为 `hashSlot uint16`。验证 key 前缀长度从 13 字节（1+8+4）变为 7 字节（1+2+4）。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/slot/meta/ -run TestCodec -v`

- [ ] **Step 3: Update codec.go**

核心改动：

```go
// encodeStatePrefix: slot uint64 → hashSlot uint16
func encodeStatePrefix(hashSlot uint16, tableID uint32) []byte {
    buf := make([]byte, 1+2+4) // keyspace(1) + hashSlot(2) + tableID(4)
    buf[0] = keyspaceState
    binary.BigEndian.PutUint16(buf[1:3], hashSlot)
    binary.BigEndian.PutUint32(buf[3:7], tableID)
    return buf
}

// encodeIndexPrefix: 同理
func encodeIndexPrefix(hashSlot uint16, tableID uint32, indexID uint16) []byte { ... }

// encodeMetaPrefix: 同理
func encodeMetaPrefix(hashSlot uint16) []byte { ... }

// 所有 encode*PrimaryKey / encode*IndexKey 函数：slot uint64 → hashSlot uint16
```

- [ ] **Step 4: Update shard_spans.go**

```go
func encodeHashSlotKeyspacePrefix(kind byte, hashSlot uint16) []byte {
    buf := make([]byte, 3) // kind(1) + hashSlot(2)
    buf[0] = kind
    binary.BigEndian.PutUint16(buf[1:3], hashSlot)
    return buf
}

func hashSlotStateSpan(hashSlot uint16) Span { ... }
func hashSlotIndexSpan(hashSlot uint16) Span { ... }
func hashSlotMetaSpan(hashSlot uint16) Span { ... }
func hashSlotAllDataSpans(hashSlot uint16) []Span { ... }
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./pkg/slot/meta/ -run TestCodec -v`

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/meta/codec.go pkg/slot/meta/shard_spans.go pkg/slot/meta/codec_test.go
git commit -m "refactor: key encoding from slotID:8 to hashSlot:2"
```

---

### Task 3: DB 和 WriteBatch 适配

**Files:**
- Modify: `pkg/slot/meta/db.go` — ForSlot → ForHashSlot
- Modify: `pkg/slot/meta/batch.go` — 所有方法 `slot uint64` → `hashSlot uint16`
- Modify: `pkg/slot/meta/*_test.go` — 所有测试适配
- Modify: `pkg/slot/meta/testutil_test.go`

- [ ] **Step 1: Update test files for new signatures**

所有 `_test.go` 中的 `slot uint64` → `hashSlot uint16`，`db.ForSlot(x)` → `db.ForHashSlot(x)`。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/slot/meta/ -v`

- [ ] **Step 3: Update db.go**

```go
func (db *DB) ForHashSlot(hashSlot uint16) *ShardStore {
    return &ShardStore{db: db, hashSlot: hashSlot}
}

func (db *DB) DeleteHashSlotData(hashSlot uint16) error {
    spans := hashSlotAllDataSpans(hashSlot)
    // ... delete ranges
}
```

- [ ] **Step 4: Update batch.go**

所有 WriteBatch 方法签名 `slot uint64` → `hashSlot uint16`：
- `CreateUser(hashSlot uint16, u User)`
- `UpsertUser(hashSlot uint16, u User)`
- `UpsertChannel(hashSlot uint16, ch Channel)`
- `UpsertChannelRuntimeMeta(hashSlot uint16, meta ChannelRuntimeMeta)`
- ... 等等，所有方法同步改

内部调用 `encodeUserPrimaryKey(hashSlot, ...)` 等。

同时更新：
- `validateSlot(slot uint64)` → `validateHashSlot(hashSlot uint16)`
- `loadUserConversationState` 等内部方法中的 `b.db.ForSlot(slot)` → `b.db.ForHashSlot(hashSlot)`

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./pkg/slot/meta/ -v`

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/meta/
git commit -m "refactor: DB and WriteBatch use hashSlot uint16 instead of slotID uint64"
```

---

### Task 4: Snapshot 适配

**Files:**
- Modify: `pkg/slot/meta/snapshot.go` — 按 hash slot 集合导出/导入
- Modify: `pkg/slot/meta/snapshot_codec.go` — slotID uint64 → hashSlot uint16 in codec
- Modify: `pkg/slot/meta/snapshot_test.go`

- [ ] **Step 1: Update snapshot_test.go**

测试改为传入 `[]uint16` hash slot 集合进行导出/导入。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/slot/meta/ -run TestSnapshot -v`

- [ ] **Step 3: Update snapshot_codec.go**

```go
type SlotSnapshot struct {
    HashSlots []uint16  // 本快照包含哪些 hash slot
    Data      []byte
    Stats     SnapshotStats
}
```

Bump `slotSnapshotVersion` 从 1 到 2。Header 改为：`[magic:4][version:2(=2)][hashSlotCount:2][hashSlots:N*2][entryCount:8]`
旧版 v1 快照不需要兼容（项目未上线）。

- [ ] **Step 4: Update snapshot.go**

```go
// ExportHashSlotSnapshot 导出指定 hash slot 集合的数据
func (db *DB) ExportHashSlotSnapshot(hashSlots []uint16) (SlotSnapshot, error) {
    snap := db.db.NewSnapshot()
    defer snap.Close()
    for _, hs := range hashSlots {
        for _, span := range hashSlotAllDataSpans(hs) {
            // iterate and append entries
        }
    }
    return snapshot, nil
}

// ImportHashSlotSnapshot 导入快照（原子替换目标 hash slot 的数据）
func (db *DB) ImportHashSlotSnapshot(snap SlotSnapshot) error { ... }
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./pkg/slot/meta/ -run TestSnapshot -v`

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/meta/snapshot.go pkg/slot/meta/snapshot_codec.go pkg/slot/meta/snapshot_test.go
git commit -m "refactor: snapshot export/import by hash slot set"
```

---

### Task 5: Command 和 FSM 适配

**Files:**
- Modify: `pkg/slot/multiraft/types.go` — Command 增加 HashSlot 字段
- Modify: `pkg/slot/fsm/statemachine.go` — 用 hashSlot 替代 slotID 验证
- Modify: `pkg/slot/fsm/command.go` — `apply(wb, slot uint64)` → `apply(wb, hashSlot uint16)`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/fsm/testutil_test.go`
- Modify: `pkg/slot/multiraft/slot.go` — applyCommittedEntries 传递 HashSlot

- [ ] **Step 1: Update multiraft Command struct**

```go
// pkg/slot/multiraft/types.go
type Command struct {
    SlotID   SlotID   // 物理 Slot ID（Raft 组标识）
    HashSlot uint16   // 该命令属于哪个 hash slot
    Index    uint64
    Term     uint64
    Data     []byte
}
```

- [ ] **Step 2: Update FSM test for hashSlot ownership**

```go
// pkg/slot/fsm/state_machine_test.go
func TestApplyBatch_ValidatesHashSlot(t *testing.T) {
    sm := newTestStateMachine([]uint16{0, 1, 2}) // owns hash slots 0,1,2
    cmd := multiraft.Command{HashSlot: 5, Data: encodeCmd(...)}
    _, err := sm.ApplyBatch(ctx, []multiraft.Command{cmd})
    assert err contains "hash slot not owned"
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./pkg/slot/fsm/ -v`

- [ ] **Step 4: Update statemachine.go**

```go
type stateMachine struct {
    db             *metadb.DB
    ownedHashSlots map[uint16]bool  // 替代原来的 slot uint64
}

func (m *stateMachine) ApplyBatch(ctx context.Context, cmds []multiraft.Command) ([][]byte, error) {
    wb := m.db.NewWriteBatch()
    for _, cmd := range cmds {
        if !m.ownedHashSlots[cmd.HashSlot] {
            continue // skip commands for hash slots we don't own
        }
        decoded := decodeCommand(cmd.Data)
        decoded.apply(wb, cmd.HashSlot)
    }
    return results, wb.Commit()
}
```

- [ ] **Step 5: Update command.go**

所有 command struct 的 `apply` 方法签名从 `apply(wb *WriteBatch, slot uint64)` 改为 `apply(wb *WriteBatch, hashSlot uint16)`。

- [ ] **Step 6: Raft Propose 增加 HashSlot 信封**

Raft entry.Data 需要携带 HashSlot 信息。采用信封方案：`Cluster.Propose()` 在提交 Raft 前在 data 前面加 2 字节 `hashSlot`：

```
Raft entry.Data 格式: [hashSlot:2 big-endian][originalCmdData...]
```

修改 `pkg/cluster/cluster.go` 的 `Propose` 方法：
```go
func (c *Cluster) ProposeWithHashSlot(ctx context.Context, slotID SlotID, hashSlot uint16, data []byte) error {
    envelope := make([]byte, 2+len(data))
    binary.BigEndian.PutUint16(envelope[:2], hashSlot)
    copy(envelope[2:], data)
    return c.runtime.Propose(ctx, slotID, envelope)
}
```

- [ ] **Step 7: Update multiraft/slot.go applyCommittedEntries**

在 `applyCommittedEntries`（slot.go:329-457）中构造 Command 时，从 entry.Data 前 2 字节解析 HashSlot：

```go
hs := binary.BigEndian.Uint16(entry.Data[:2])
cmds[i] = Command{
    SlotID:   g.id,
    HashSlot: hs,
    Index:    entry.Index,
    Term:     entry.Term,
    Data:     entry.Data[2:],  // 去掉信封
}
```

- [ ] **Step 7: Run all FSM tests**

Run: `go test ./pkg/slot/fsm/ -v`

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/multiraft/types.go pkg/slot/multiraft/slot.go pkg/slot/fsm/
git commit -m "refactor: FSM validates hashSlot ownership, Command carries HashSlot"
```

---

### Task 6: Router 改造

**Files:**
- Modify: `pkg/cluster/router.go` — 用 HashSlotTable 替代 CRC32 取模
- Modify: `pkg/cluster/router_test.go`

- [ ] **Step 1: Update router_test.go**

```go
func TestRouter_SlotForKey_UsesHashSlotTable(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    router := NewRouter(table, ...)
    // "test" key → CRC32 % 256 → hash slot → assignment → physical SlotID
    slotID := router.SlotForKey("test")
    assert slotID >= 1 && slotID <= 4
}

func TestRouter_HashSlotForKey(t *testing.T) {
    router := NewRouter(...)
    hs := router.HashSlotForKey("test")
    assert hs < 256
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/cluster/ -run TestRouter -v`

- [ ] **Step 3: Update router.go**

```go
type Router struct {
    hashSlotTable atomic.Pointer[HashSlotTable]
    runtime       *multiraft.Runtime
    localNode     multiraft.NodeID
}

func (r *Router) SlotForKey(key string) multiraft.SlotID {
    table := r.hashSlotTable.Load()
    hs := HashSlotForKey(key, table.hashSlotCount)
    return table.Lookup(hs)
}

func (r *Router) HashSlotForKey(key string) uint16 {
    table := r.hashSlotTable.Load()
    return HashSlotForKey(key, table.hashSlotCount)
}

func (r *Router) UpdateHashSlotTable(table *HashSlotTable) {
    r.hashSlotTable.Store(table)
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./pkg/cluster/ -run TestRouter -v`

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/router.go pkg/cluster/router_test.go
git commit -m "refactor: router uses HashSlotTable lookup instead of CRC32 modulo"
```

---

### Task 7a: Config + Cluster 启动

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Update config.go**

```go
type Config struct {
    // ... existing fields ...
    HashSlotCount     uint16   // 固定 hash slot 数量（默认 256）
    InitialSlotCount  int      // 初始物理 Slot 数量（替代原 SlotCount）
}
```

validate() 新增校验：HashSlotCount 必须 > 0 且 >= InitialSlotCount。

- [ ] **Step 2: Update cluster.go startup**

在 `NewCluster` 中创建 HashSlotTable（均分 hash slot 到 InitialSlotCount 个物理 Slot），传递给 Router。

在 `NewStateMachineFactory` 工厂函数中传递每个物理 Slot 拥有的 hash slot 集合（通过 HashSlotTable 计算）。

- [ ] **Step 3: Update internal/app/build.go**

在 `runtimeConfig()` 中透传 HashSlotCount。

- [ ] **Step 4: Commit**

```bash
git add pkg/cluster/config.go pkg/cluster/cluster.go internal/app/build.go
git commit -m "feat: config and cluster startup with HashSlotTable"
```

---

### Task 7b: Proxy Store 读写路径适配

**Files:**
- Modify: `pkg/slot/proxy/store.go` — 写路径 + 读路径全部适配

- [ ] **Step 1: Update 写路径（Propose 调用）**

所有 Propose 调用增加 hashSlot 参数：

```go
func (s *Store) CreateChannel(ctx context.Context, channelID string, ...) error {
    hashSlot := s.cluster.HashSlotForKey(channelID)
    slotID := s.cluster.SlotForKey(channelID)
    cmd := metafsm.EncodeUpsertChannelCommand(hashSlot, ...)
    return s.cluster.ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
}
```

影响的写方法：CreateChannel, UpdateChannel, DeleteChannel, AddChannelSubscribers, RemoveChannelSubscribers, UpsertChannelRuntimeMeta, UpsertUser, UpsertDevice, 以及所有其他 Propose 调用。

- [ ] **Step 2: Update 读路径（ForSlot → ForHashSlot）**

所有直接读取 Pebble 的调用必须改用 hashSlot：

```go
// 改前
func (s *Store) GetChannel(ctx context.Context, channelID string, channelType int64) (*Channel, error) {
    slotID := s.cluster.SlotForKey(channelID)
    return s.db.ForSlot(uint64(slotID)).GetChannel(ctx, channelID, channelType)
}

// 改后
func (s *Store) GetChannel(ctx context.Context, channelID string, channelType int64) (*Channel, error) {
    hashSlot := s.cluster.HashSlotForKey(channelID)
    return s.db.ForHashSlot(hashSlot).GetChannel(ctx, channelID, channelType)
}
```

影响的读方法（至少 6 处）：GetChannel, GetUser, GetDevice, GetChannelRuntimeMeta, ListChannelSubscribers, ListChannelRuntimeMeta。

注意 `ListChannelRuntimeMeta` 需要遍历所有 hash slot（而非物理 Slot），逻辑需要改为遍历 `0..HashSlotCount-1`。

- [ ] **Step 3: Run tests**

```bash
go test ./pkg/slot/proxy/ -v
```

- [ ] **Step 4: Commit**

```bash
git add pkg/slot/proxy/store.go
git commit -m "refactor: proxy store read/write paths use hashSlot"
```

---

### Task 7c: ErrRerouted 和 channelmeta 适配

**Files:**
- Modify: `internal/usecase/message/retry.go`
- Modify: `internal/app/channelmeta.go`

- [ ] **Step 1: Update retry.go**

```go
func shouldRefreshAndRetry(err error) bool {
    return errors.Is(err, channel.ErrStaleMeta) ||
           errors.Is(err, channel.ErrNotLeader) ||
           errors.Is(err, raftcluster.ErrRerouted)  // 新增
}
```

- [ ] **Step 2: Update channelmeta.go**

缓存增加 `HashSlotTableVersion` 字段，命中缓存时校验版本是否过期。

- [ ] **Step 3: Run tests**

```bash
go test ./internal/usecase/message/ -v
go test ./internal/app/ -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/usecase/message/retry.go internal/app/channelmeta.go
git commit -m "feat: ErrRerouted retry and channelmeta cache version check"
```

---

### Task 8: 集成测试验证

**Files:**
- Modify: `pkg/cluster/cluster_integration_test.go`
- Modify: `pkg/cluster/cluster_test.go`

- [ ] **Step 1: Update integration tests for HashSlotTable**

确保多节点集群启动后：
- HashSlotTable 被正确初始化（256 hash slot 均分到 N 个物理 Slot）
- 通过 proxy/store 写入 Channel/User 数据后能正确读回
- 不同 Key 路由到不同物理 Slot

- [ ] **Step 2: Run integration tests**

```bash
go test ./pkg/cluster/ -v -count=1
```

- [ ] **Step 3: Fix any issues found**

- [ ] **Step 4: Run stress tests**

```bash
go test ./pkg/cluster/ -run TestStress -v -count=1
go test ./pkg/slot/meta/ -run TestStress -v -count=1
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/*_test.go
git commit -m "test: update integration and stress tests for hash slot routing"
```
