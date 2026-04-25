# Conversation Sync 实现 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 `WuKongIM v3` 落地基于 `UserConversationState + ChannelUpdateLog` 的最近会话同步能力，并提供与旧版对齐的 `/conversation/sync` HTTP 接口。

**Architecture:** `pkg/storage/metadb` / `pkg/storage/metastore` 负责会话目录真相层与频道更新索引；`internal/usecase/conversation` 负责 sync 候选集裁剪、冷降级、消息事实聚合与 projector；`internal/access/api` 只做旧版请求/响应兼容。消息发送热路径仍然只提交 `channellog.Append + async committed dispatcher`，projector 通过异步 committed 路径维护 hot `ChannelUpdateLog` 和冷唤醒 fanout，不在发送同步链路中写 per-user 状态。

**Tech Stack:** Go 1.23、Gin、`pkg/storage/metadb`、`pkg/storage/metastore`、`pkg/storage/metafsm`、`pkg/storage/channellog`、`internal/usecase/*`、`testing`、`testify`。

**Spec:** `docs/superpowers/specs/2026-04-07-conversation-sync-design.md`

---

## 执行说明

- 每个任务都使用 `@superpowers/test-driven-development`：先写失败测试，再写最小实现，再跑测试确认通过。
- 严格遵守本仓库依赖方向：`access -> usecase/runtime`，`usecase -> runtime/pkg`，`app -> all`。
- `UserConversationState.active_at` 只服务 working set；任何仅修改 `active_at` 的写入都不得推进 `updated_at`。
- `version > 0` 的增量发现只允许扫描该用户的 `UserConversationState` 目录，再按这些 channel 批量探测 `ChannelUpdateLog`；绝不做全局 `ChannelUpdateLog` 时间范围扫描。
- `/conversation/sync` 不实现 `page/page_size/cursor/sync_version` 兼容，也不扩成历史全量目录接口。
- `limit` 是最近窗口硬截断，不提供补页；测试必须把这个 lossiness 明确锁住。
- projector 允许 eventual consistency，但消息发送热路径不能增加同步 metastore 写。
- 最终交付前运行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `pkg/storage/metadb/catalog.go` | 注册 `UserConversationState`、`ChannelUpdateLog` 的 table / family / index 常量 |
| `pkg/storage/metadb/batch.go` | 为新表提供 batch write 原语 |
| `pkg/storage/metadb/user_conversation_state.go` | 本地 shard 上的 state 读写、active 索引读取、目录分页扫描、`active_at` touch/clear |
| `pkg/storage/metadb/user_conversation_state_test.go` | `active_at desc`、目录分页、`updated_at` 语义、删除/清零覆盖 |
| `pkg/storage/metadb/channel_update_log.go` | 本地 shard 上的频道更新索引读写、批量查询、删除 |
| `pkg/storage/metadb/channel_update_log_test.go` | 批量 upsert/get/delete、排序字段与冷数据查询覆盖 |
| `pkg/storage/metafsm/command.go` | 新增 conversation state / channel update log 命令编码、解码与 apply |
| `pkg/storage/metafsm/state_machine_test.go` | 新命令复制应用与原子性覆盖 |
| `pkg/storage/metastore/store.go` | 暴露 conversation state / channel update log 的 authoritative API 与 hot overlay 注册点 |
| `pkg/storage/metastore/user_conversation_state_rpc.go` | `uid` owner 侧 active scan、目录分页、批量 touch/clear RPC |
| `pkg/storage/metastore/channel_update_log_rpc.go` | `channel` owner 侧 batch get / batch upsert / batch delete RPC，并在读路径优先命中 hot overlay |
| `pkg/storage/metastore/integration_test.go` | conversation 元数据 authoritative 路由与跨 slot 分组覆盖 |
| `internal/usecase/conversation/app.go` | conversation usecase 入口、参数配置、异步回写调度 |
| `internal/usecase/conversation/deps.go` | sync / projector 所需依赖接口定义 |
| `internal/usecase/conversation/types.go` | sync query/result、消息事实、排序键与 legacy 映射前的领域类型 |
| `internal/usecase/conversation/sync.go` | `/conversation/sync` 核心算法：候选发现、冷降级、事实加载、排序与截断 |
| `internal/usecase/conversation/sync_test.go` | working set、`version > 0`、overlay、`only_unread`、`limit` 截断覆盖 |
| `internal/usecase/conversation/projector.go` | hot `ChannelUpdateLog`、周期 flush、冷唤醒 fanout、overlay 读取实现 |
| `internal/usecase/conversation/projector_test.go` | coalesce flush、cold wakeup、overlay 命中与 retry 语义覆盖 |
| `internal/access/api/server.go` | 注入 `ConversationUsecase` 依赖 |
| `internal/access/api/routes.go` | 注册 `/conversation/sync` 路由 |
| `internal/access/api/conversation_legacy_model.go` | 旧版请求参数、响应项与 `last_msg_seqs` 协议解析 |
| `internal/access/api/conversation_sync.go` | Gin handler：校验、usecase 映射、旧版 JSON 输出 |
| `internal/access/api/server_test.go` | legacy API 兼容与错误语义覆盖 |
| `internal/app/config.go` | 增加 conversation 配置项与默认值 |
| `internal/app/app.go` | 挂载 conversation app / projector / 生命周期字段 |
| `internal/app/build.go` | 装配 metastore、conversation usecase、projector、API 与 committed dispatcher |
| `internal/app/lifecycle.go` | 启停 conversation projector |
| `internal/app/lifecycle_test.go` | projector 生命周期与失败回滚覆盖 |
| `internal/app/deliveryrouting.go` | 将 committed message 异步分发到 delivery 与 conversation projector |
| `internal/app/deliveryrouting_test.go` | committed dispatcher 多 sink 行为覆盖 |
| `internal/app/conversation_sync_integration_test.go` | 端到端覆盖：发送消息后 `/conversation/sync` 返回旧版兼容结果 |
| `docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md` | 固化 backfill、抽样校验与 cutover gate 的上线步骤 |

## Task 1: 在 metadb 中落会话目录与频道更新索引本地表

**Files:**
- Modify: `pkg/storage/metadb/catalog.go`
- Modify: `pkg/storage/metadb/batch.go`
- Create: `pkg/storage/metadb/user_conversation_state.go`
- Create: `pkg/storage/metadb/user_conversation_state_test.go`
- Create: `pkg/storage/metadb/channel_update_log.go`
- Create: `pkg/storage/metadb/channel_update_log_test.go`

- [ ] **Step 1: 先写失败测试，锁定本地表语义**

在 `pkg/storage/metadb/user_conversation_state_test.go` 增加：

- `TestShardListUserConversationActiveReturnsActiveAtDesc`
- `TestShardTouchUserConversationActiveAtPreservesUpdatedAt`
- `TestShardListUserConversationStatePageReturnsStableCursor`
- `TestShardClearUserConversationActiveAtZeroesOnlyActiveField`

在 `pkg/storage/metadb/channel_update_log_test.go` 增加：

- `TestShardBatchGetChannelUpdateLogsReturnsLatestEntries`
- `TestShardDeleteChannelUpdateLogsRemovesEntries`

测试骨架：

```go
func TestShardTouchUserConversationActiveAtPreservesUpdatedAt(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	state := UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 3,
		ActiveAt:     100,
		UpdatedAt:    200,
	}
	require.NoError(t, shard.UpsertUserConversationState(ctx, state))
	require.NoError(t, shard.TouchUserConversationActiveAt(ctx, "u1", "g1", 2, 300))

	got, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(200), got.UpdatedAt)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./pkg/storage/metadb -run "TestShard(ListUserConversation|TouchUserConversation|BatchGetChannelUpdateLog|DeleteChannelUpdateLog)" -count=1`

Expected: FAIL，因为 conversation 两张表及其 shard API 还不存在。

- [ ] **Step 3: 写最小实现**

在 `pkg/storage/metadb/user_conversation_state.go` 定义：

```go
type UserConversationState struct {
	UID          string
	ChannelID    string
	ChannelType  int64
	ReadSeq      uint64
	DeletedToSeq uint64
	ActiveAt     int64
	UpdatedAt    int64
}
```

提供最小 API：

```go
func (s *ShardStore) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (UserConversationState, error)
func (s *ShardStore) UpsertUserConversationState(ctx context.Context, state UserConversationState) error
func (s *ShardStore) TouchUserConversationActiveAt(ctx context.Context, uid, channelID string, channelType int64, activeAt int64) error
func (s *ShardStore) ClearUserConversationActiveAt(ctx context.Context, uid string, keys []ConversationKey) error
func (s *ShardStore) ListUserConversationActive(ctx context.Context, uid string, limit int) ([]UserConversationState, error)
func (s *ShardStore) ListUserConversationStatePage(ctx context.Context, uid string, after ConversationCursor, limit int) ([]UserConversationState, ConversationCursor, bool, error)
```

要求：

- 主键：`(uid, channel_type, channel_id)`
- 二级索引：`(uid, active_at desc, channel_type, channel_id)`
- `TouchUserConversationActiveAt` 语义是 `active_at = max(existing, incoming)`，且不改 `updated_at`
- `ClearUserConversationActiveAt` 只清零 `active_at`

在 `pkg/storage/metadb/channel_update_log.go` 定义：

```go
type ChannelUpdateLog struct {
	ChannelID       string
	ChannelType     int64
	UpdatedAt       int64
	LastMsgSeq      uint64
	LastClientMsgNo string
	LastMsgAt       int64
}
```

提供：

```go
func (s *ShardStore) UpsertChannelUpdateLog(ctx context.Context, entry ChannelUpdateLog) error
func (s *ShardStore) BatchGetChannelUpdateLogs(ctx context.Context, keys []ConversationKey) (map[ConversationKey]ChannelUpdateLog, error)
func (s *ShardStore) DeleteChannelUpdateLogs(ctx context.Context, keys []ConversationKey) error
```

`ChannelUpdateLog` 此阶段只保留主键，不做全局时间索引。

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./pkg/storage/metadb -run "TestShard(ListUserConversation|TouchUserConversation|BatchGetChannelUpdateLog|DeleteChannelUpdateLog)" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/metadb/catalog.go pkg/storage/metadb/batch.go pkg/storage/metadb/user_conversation_state.go pkg/storage/metadb/user_conversation_state_test.go pkg/storage/metadb/channel_update_log.go pkg/storage/metadb/channel_update_log_test.go
git commit -m "feat(metadb): add conversation state tables"
```

## Task 2: 接通 metafsm / metastore 的 authoritative 写读路径

**Files:**
- Modify: `pkg/storage/metafsm/command.go`
- Modify: `pkg/storage/metafsm/state_machine_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Create: `pkg/storage/metastore/user_conversation_state_rpc.go`
- Create: `pkg/storage/metastore/channel_update_log_rpc.go`
- Modify: `pkg/storage/metastore/integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定复制命令和 authoritative API**

在 `pkg/storage/metafsm/state_machine_test.go` 增加：

- `TestApplyBatchTouchUserConversationActiveAtPreservesUpdatedAt`
- `TestApplyBatchUpsertChannelUpdateLogs`

在 `pkg/storage/metastore/integration_test.go` 增加：

- `TestStoreListUserConversationActiveReadsAuthoritativeSlot`
- `TestStoreScanUserConversationStatePageReadsAuthoritativeSlot`
- `TestStoreBatchGetChannelUpdateLogsGroupsByChannelSlot`
- `TestStoreTouchUserConversationActiveAtGroupsByUIDSlot`

测试骨架：

```go
func TestStoreTouchUserConversationActiveAtGroupsByUIDSlot(t *testing.T) {
	ctx := context.Background()
	store := newConversationTestStore(t)

	patches := []metadb.UserConversationActivePatch{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
		{UID: "u2", ChannelID: "g2", ChannelType: 2, ActiveAt: 200},
	}
	require.NoError(t, store.TouchUserConversationActiveAt(ctx, patches))

	got1, err := store.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(100), got1.ActiveAt)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./pkg/storage/metafsm ./pkg/storage/metastore -run "Test(ApplyBatchTouchUserConversation|ApplyBatchUpsertChannelUpdateLogs|StoreListUserConversationActive|StoreScanUserConversationStatePage|StoreBatchGetChannelUpdateLogs|StoreTouchUserConversationActiveAt)" -count=1`

Expected: FAIL，因为命令编码、RPC 与 `Store` API 还未实现。

- [ ] **Step 3: 写最小实现**

在 `pkg/storage/metafsm/command.go` 增加批量命令：

```go
func EncodeUpsertUserConversationStatesCommand(states []metadb.UserConversationState) []byte
func EncodeTouchUserConversationActiveAtCommand(patches []metadb.UserConversationActivePatch) []byte
func EncodeClearUserConversationActiveAtCommand(uid string, keys []metadb.ConversationKey) []byte
func EncodeUpsertChannelUpdateLogsCommand(entries []metadb.ChannelUpdateLog) []byte
func EncodeDeleteChannelUpdateLogsCommand(keys []metadb.ConversationKey) []byte
```

在 `pkg/storage/metastore/store.go` 暴露：

```go
func (s *Store) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, error)
func (s *Store) ListUserConversationActive(ctx context.Context, uid string, limit int) ([]metadb.UserConversationState, error)
func (s *Store) ScanUserConversationStatePage(ctx context.Context, uid string, after metadb.ConversationCursor, limit int) ([]metadb.UserConversationState, metadb.ConversationCursor, bool, error)
func (s *Store) TouchUserConversationActiveAt(ctx context.Context, patches []metadb.UserConversationActivePatch) error
func (s *Store) ClearUserConversationActiveAt(ctx context.Context, uid string, keys []metadb.ConversationKey) error
func (s *Store) BatchGetChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
func (s *Store) UpsertChannelUpdateLogs(ctx context.Context, entries []metadb.ChannelUpdateLog) error
func (s *Store) DeleteChannelUpdateLogs(ctx context.Context, keys []metadb.ConversationKey) error
```

实现要求：

- `uid` 维度请求按 `uid` slot 分组提案
- `channel` 维度请求按 `channel_id` slot 分组提案
- `ListUserConversationActive` 与 `ScanUserConversationStatePage` 走 `uid` authoritative RPC
- `BatchGetChannelUpdateLogs` 走 `channel` authoritative RPC，并保留批量接口形状，供后续 hot overlay 扩展

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./pkg/storage/metafsm ./pkg/storage/metastore -run "Test(ApplyBatchTouchUserConversation|ApplyBatchUpsertChannelUpdateLogs|StoreListUserConversationActive|StoreScanUserConversationStatePage|StoreBatchGetChannelUpdateLogs|StoreTouchUserConversationActiveAt)" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/metafsm/command.go pkg/storage/metafsm/state_machine_test.go pkg/storage/metastore/store.go pkg/storage/metastore/user_conversation_state_rpc.go pkg/storage/metastore/channel_update_log_rpc.go pkg/storage/metastore/integration_test.go
git commit -m "feat(metastore): add conversation state rpc APIs"
```

## Task 3: 实现 conversation sync 核心 usecase

**Files:**
- Create: `internal/usecase/conversation/app.go`
- Create: `internal/usecase/conversation/deps.go`
- Create: `internal/usecase/conversation/types.go`
- Create: `internal/usecase/conversation/sync.go`
- Create: `internal/usecase/conversation/sync_test.go`

- [ ] **Step 1: 先写失败测试，锁定 sync 算法边界**

在 `internal/usecase/conversation/sync_test.go` 增加：

- `TestSyncVersionZeroUsesWorkingSetAndClientOverlay`
- `TestSyncVersionPositiveScansUserDirectoryForStateAndChannelDeltas`
- `TestSyncColdRowsAreExcludedAndDemotedAsync`
- `TestSyncAppliesOnlyUnreadDeleteLineAndStableLimitOrdering`
- `TestSyncLoadsRecentsOnlyForFinalLimitedWindow`

测试骨架：

```go
func TestSyncColdRowsAreExcludedAndDemotedAsync(t *testing.T) {
	repo := newConversationSyncRepoStub()
	repo.active = []metadb.UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10},
	}
	repo.channelUpdates[key("g1", 2)] = metadb.ChannelUpdateLog{
		ChannelID:   "g1",
		ChannelType: 2,
		UpdatedAt:   20,
		LastMsgAt:   time.Now().Add(-31 * 24 * time.Hour).UnixNano(),
	}

	app := New(Options{
		States:        repo,
		ChannelUpdate: repo,
		Facts:         repo,
		Now:           time.Now,
		ColdThreshold: 30 * 24 * time.Hour,
	})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 100})
	require.NoError(t, err)
	require.Empty(t, got.Conversations)
	require.Equal(t, []metadb.ConversationKey{{ChannelID: "g1", ChannelType: 2}}, repo.clearedActive)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./internal/usecase/conversation -run "TestSync(VersionZero|VersionPositive|ColdRows|AppliesOnlyUnread)" -count=1`

Expected: FAIL，因为 conversation usecase 尚不存在。

- [ ] **Step 3: 写最小实现**

在 `internal/usecase/conversation/types.go` 定义：

```go
type SyncQuery struct {
	UID                 string
	Version             int64
	LastMsgSeqs         map[ConversationKey]uint64
	MsgCount            int
	OnlyUnread          bool
	ExcludeChannelTypes []uint8
	Limit               int
}

type SyncConversation struct {
	ChannelID       string
	ChannelType     uint8
	Unread          int
	Timestamp       int64
	LastMsgSeq      uint32
	LastClientMsgNo string
	ReadedToMsgSeq  uint32
	Version         int64
	Recents         []channellog.Message
}
```

在 `internal/usecase/conversation/sync.go` 实现：

```go
func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error)
```

实现顺序必须与 spec 对齐：

1. `ListUserConversationActive(uid, activeScanLimit)`
2. 批量 `BatchGetChannelUpdateLogs(active rows)` 做冷检查
3. 冷行异步 `ClearUserConversationActiveAt`
4. 若 `version > 0`，分页 `ScanUserConversationStatePage`，对扫描到的 channel 批量探测 `ChannelUpdateLog`
5. 合并 `working set ∪ client overlay ∪ incremental`
6. 读取最小消息事实，计算 `unread` / `readed_to_msg_seq` / `deleted_to_seq`
7. 应用 `only_unread`、稳定排序和 `limit`
8. 仅对最终返回窗口按 `msg_count` 读取 `recents`

必须显式锁住：

- `active_at` 只影响候选发现，不参与最终显示排序
- overlay candidate 不反写 `UserConversationState`
- `version > 0` 不能全局扫 `ChannelUpdateLog`
- `sync_updated_at = max(display_updated_at, state.updated_at)`
- `msg_count <= 0` 时不得读取 `recents`

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./internal/usecase/conversation -run "TestSync(VersionZero|VersionPositive|ColdRows|AppliesOnlyUnread)" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/usecase/conversation/app.go internal/usecase/conversation/deps.go internal/usecase/conversation/types.go internal/usecase/conversation/sync.go internal/usecase/conversation/sync_test.go
git commit -m "feat(conversation): implement sync usecase"
```

## Task 4: 实现 projector、hot overlay 和冷唤醒 fanout

**Files:**
- Modify: `internal/usecase/conversation/app.go`
- Create: `internal/usecase/conversation/projector.go`
- Create: `internal/usecase/conversation/projector_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Modify: `pkg/storage/metastore/channel_update_log_rpc.go`

- [ ] **Step 1: 先写失败测试，锁定 hot/cold 更新行为**

在 `internal/usecase/conversation/projector_test.go` 增加：

- `TestProjectorCoalescesMultipleMessagesIntoSingleFlushEntry`
- `TestProjectorColdWakeupTouchesPersonConversationStates`
- `TestProjectorColdWakeupPagesGroupSubscribers`
- `TestProjectorOverlayReadWinsBeforeColdFlush`

测试骨架：

```go
func TestProjectorCoalescesMultipleMessagesIntoSingleFlushEntry(t *testing.T) {
	store := newProjectorStoreStub()
	projector := NewProjector(ProjectorOptions{
		Store:         store,
		FlushInterval: time.Hour,
		DirtyLimit:    2,
		ColdThreshold: 30 * 24 * time.Hour,
		Now:           func() time.Time { return time.Unix(100, 0) },
	})

	msg1 := channellog.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 10, ClientMsgNo: "c1", Timestamp: 100}
	msg2 := channellog.Message{ChannelID: "g1", ChannelType: 2, MessageSeq: 11, ClientMsgNo: "c2", Timestamp: 101}
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg1))
	require.NoError(t, projector.SubmitCommitted(context.Background(), msg2))
	require.NoError(t, projector.Flush(context.Background()))

	require.Len(t, store.updates, 1)
	require.Equal(t, uint64(11), store.updates[0].LastMsgSeq)
	require.Equal(t, "c2", store.updates[0].LastClientMsgNo)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./internal/usecase/conversation -run "TestProjector(Coalesces|ColdWakeup|OverlayRead)" -count=1`

Expected: FAIL，因为 projector 与 hot overlay 尚未实现。

- [ ] **Step 3: 写最小实现**

在 `internal/usecase/conversation/projector.go` 实现 `Projector`，要求：

```go
type Projector interface {
	Start() error
	Stop() error
	SubmitCommitted(ctx context.Context, msg channellog.Message) error
	BatchGetHotChannelUpdates(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
	Flush(ctx context.Context) error
}
```

关键语义：

- 同一频道连续消息只覆盖 hot entry，flush 时只落一次 cold `ChannelUpdateLog`
- `updated_at` 使用消息业务时间，不使用 flush 时间
- 读路径先查 hot overlay，再回退 cold metastore
- `cold -> hot` 只在第一条重新活跃消息上触发：
  - 单聊：`DecodePersonChannel` 后为双方 `TouchUserConversationActiveAt`
  - 群聊：分页 `ListChannelSubscribers`，按 `uid` slot 分组 touch
- touch 语义是 `active_at = max(existing, message_time)`，不改 `updated_at`
- flush / retry 失败不影响发送主链路，只影响后续 sync 收敛速度

同时在 `pkg/storage/metastore/store.go` 增加 hot overlay 注册点，例如：

```go
type ChannelUpdateOverlay interface {
	BatchGetHotChannelUpdates(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
}
```

并在 `channel_update_log_rpc.go` 的 authoritative 读路径中优先命中 overlay，再回退 Pebble。

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./internal/usecase/conversation -run "TestProjector(Coalesces|ColdWakeup|OverlayRead)" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/usecase/conversation/app.go internal/usecase/conversation/projector.go internal/usecase/conversation/projector_test.go pkg/storage/metastore/store.go pkg/storage/metastore/channel_update_log_rpc.go
git commit -m "feat(conversation): add projector and hot overlay"
```

## Task 5: 接入旧版 `/conversation/sync` API 并完成 app 装配

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Create: `internal/access/api/conversation_legacy_model.go`
- Create: `internal/access/api/conversation_sync.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Create: `internal/app/conversation_sync_integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定 legacy HTTP 契约与装配行为**

在 `internal/access/api/server_test.go` 增加：

- `TestConversationSyncMapsLegacyRequestToUsecaseQuery`
- `TestConversationSyncRejectsInvalidLastMsgSeqs`
- `TestConversationSyncReturnsLegacyArrayResponse`
- `TestConversationSyncReturns503WhenSyncGateDisabled`

在 `internal/app/deliveryrouting_test.go` / `internal/app/lifecycle_test.go` 增加：

- `TestAsyncCommittedDispatcherSubmitsToConversationProjector`
- `TestStartStopIncludesConversationProjector`

在 `internal/app/conversation_sync_integration_test.go` 增加：

- `TestAppConversationSyncReturnsLegacyConversationAfterSend`

测试骨架：

```go
func TestConversationSyncRejectsInvalidLastMsgSeqs(t *testing.T) {
	srv := New(Options{Conversations: &recordingConversationUsecase{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","last_msg_seqs":"bad-format"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid last_msg_seqs"}`, rec.Body.String())
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./internal/access/api ./internal/app -run "Test(ConversationSync|AsyncCommittedDispatcherSubmitsToConversationProjector|StartStopIncludesConversationProjector|AppConversationSyncReturnsLegacyConversationAfterSend)" -count=1`

Expected: FAIL，因为 API handler、conversation usecase 注入、projector 装配和 committed dispatcher fanout 尚未接通。

- [ ] **Step 3: 写最小实现**

在 `internal/access/api/conversation_legacy_model.go` 定义旧版兼容模型：

```go
type syncConversationRequest struct {
	UID                 string  `json:"uid"`
	Version             int64   `json:"version"`
	LastMsgSeqs         string  `json:"last_msg_seqs"`
	MsgCount            int     `json:"msg_count"`
	OnlyUnread          uint8   `json:"only_unread"`
	ExcludeChannelTypes []uint8 `json:"exclude_channel_types"`
	Limit               int     `json:"limit"`
}
```

保留旧版返回字段：

- `channel_id`
- `channel_type`
- `unread`
- `timestamp`
- `last_msg_seq`
- `last_client_msg_no`
- `offset_msg_seq`
- `readed_to_msg_seq`
- `version`
- `recents`

在 `internal/access/api/conversation_sync.go`：

- 解析 `last_msg_seqs` 字符串协议
- 把 `only_unread == 1` 映射为 `true`
- 调用 `conversation.App.Sync`
- 把 `channellog.Message` 转成旧版 `MessageResp` JSON 形状
- 保证单聊返回对端 uid，而不是内部 `u1@u2`

在 `internal/app/config.go` 增加：

```go
type ConversationConfig struct {
	SyncEnabled           bool
	ColdThreshold         time.Duration
	ActiveScanLimit       int
	ChannelProbeBatchSize int
	SyncDefaultLimit      int
	SyncMaxLimit          int
	FlushInterval         time.Duration
	FlushDirtyLimit       int
	SubscriberPageSize    int
}
```

默认值：

- `SyncEnabled = false`
- `ColdThreshold = 30 * 24 * time.Hour`
- `ActiveScanLimit = 2000`
- `ChannelProbeBatchSize = 512`
- `SyncDefaultLimit = 200`
- `SyncMaxLimit = 500`
- `FlushInterval = 200 * time.Millisecond`
- `FlushDirtyLimit = 1024`
- `SubscriberPageSize = 512`

在 `internal/app/build.go` / `deliveryrouting.go` / `lifecycle.go` 中：

- 构建 conversation app 与 projector
- 把 projector 注册为 metastore hot overlay
- committed dispatcher 同时 fanout 到 delivery 和 projector
- 在 `App.Start/Stop` 中启停 projector
- `internal/access/api/conversation_sync.go` 在 `SyncEnabled == false` 时返回 `503 {"error":"conversation sync not enabled"}`

- [ ] **Step 4: 跑完整目标测试，确认通过**

Run: `go test ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore ./internal/usecase/conversation ./internal/access/api ./internal/app -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/access/api/server.go internal/access/api/routes.go internal/access/api/conversation_legacy_model.go internal/access/api/conversation_sync.go internal/access/api/server_test.go internal/app/config.go internal/app/app.go internal/app/build.go internal/app/lifecycle.go internal/app/lifecycle_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/app/conversation_sync_integration_test.go
git commit -m "feat(api): add legacy conversation sync endpoint"
```

## Task 6: 固化 backfill 与 cutover runbook，避免未回填直接切流

**Files:**
- Create: `docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md`

- [ ] **Step 1: 编写 runbook，固化 spec 的 rollout contract**

在 `docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md` 明确写出：

1. backfill 输入源优先级：
   - 旧会话目录
   - 订阅关系 / 单聊关系只补缺失行
2. 必须回填的字段：
   - `read_seq`
   - `deleted_to_seq`
   - `active_at`
   - `updated_at`
3. cutover gate：
   - 默认部署 `Conversation.SyncEnabled=false`
   - 只有完成 backfill 与抽样校验后，才允许切成 `true`
4. 抽样校验：
   - 随机抽样用户 `UserConversationState` 行数
   - 随机抽样最近活跃会话 `active_at`
   - 验证 brand-new `version=0` 请求结果非空且不过量

runbook 不是实现 backfill 程序本身，而是把 spec 的上线前置条件收敛成可执行清单。

- [ ] **Step 2: 自审 runbook 是否与 spec 一致**

Check:

- 明确写出 `ChannelUpdateLog` 无需全历史 backfill，只需 cutover 后正常累积
- 明确写出 backfill 未完成不得打开 sync gate
- 明确写出抽样校验失败时保持 gate 关闭

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/runbooks/2026-04-07-conversation-sync-cutover.md
git commit -m "docs: add conversation sync cutover runbook"
```

## 收尾验证

- [ ] 运行 `go test ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore ./internal/usecase/conversation ./internal/access/api ./internal/app -count=1`
- [ ] 如时间允许，运行 `go test ./... -count=1`
- [ ] 使用 `@superpowers/requesting-code-review` 对最终实现做代码审阅
- [ ] 确认 `/conversation/sync` 无 `page/page_size/cursor/sync_version` 行为，且 `updated_at` 没有被 `active_at` 维护写污染
