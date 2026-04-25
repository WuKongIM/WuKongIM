# channellog 完整消息模型改造 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `pkg/storage/channellog` 从瘦日志记录改造成完整消息模型，并让发送、读取、异步投递统一围绕已提交的 durable message 工作。

**Architecture:** 在 `pkg/storage/channellog` 内引入公开的 `Message` 作为唯一消息模型，保留 `messageSeq = committed offset + 1` 的 ISR 语义，同时把 `SendRequest`、`SendResult`、`Fetch`、`Store` 读取接口全部收敛到这套模型。`internal/usecase/message` 在发送前构造 durable message，提交成功后直接复用内存中的已提交消息推进异步投递；`internal/app` / `internal/usecase/delivery` / `internal/access/node` 再把 durable message 转成 realtime `wkframe.RecvPacket` 视图，不再依赖 `SendCommand` 派生 envelope。

**Tech Stack:** Go 1.23、`pkg/storage/channellog`、`pkg/replication/isr`、`internal/usecase/message`、`internal/usecase/delivery`、`internal/runtime/delivery`、`internal/access/node`、`internal/app`、`testing`、`testify`。

**Spec:** `docs/superpowers/specs/2026-04-07-channellog-message-model-design.md`

---

## 执行说明

- 每个任务都使用 `@superpowers/test-driven-development`：先补失败测试，再写最小实现，再跑测试确认通过。
- 每个任务提交都必须保持可编译；跨层接口翻转要么同任务同步调用方，要么先加临时桥接/别名，再在后续任务清理。
- 保持 AGENTS 约束的依赖方向：`access -> usecase/runtime`，`usecase -> runtime/pkg`，`app -> all`。
- 不要为正常发送热路径增加“提交成功后回库读取消息”的额外随机读。
- `消息 = 已提交日志记录`。任何 `HW` 之外的 dirty tail 都不能向业务层暴露，也不能进入 realtime 投递。
- 不要把 `wkframe.RecvPacket` 直接塞进 `pkg/storage/channellog` 作为存储模型；持久化模型必须是 `channellog.Message`。
- 不要擅自发明新的 `StreamID/StreamFlag` 业务语义；如果当前入口没有提供更强语义，就保持当前零值/默认值行为，并让测试把这一点锁住。
- 个人频道的 `RecvPacket.ChannelID` 改写仍然只属于 realtime 视图层，不得写回 durable log。
- 计划编写阶段默认执行本地自审；若用户显式要求，则补充子代理审阅并把结论回灌到计划里。
- 最终交付前运行 `@superpowers/verification-before-completion`。

## 文件映射

| 路径 | 责任 |
|------|------|
| `pkg/storage/channellog/types.go` | 定义公开 `Message`、过渡期保留 `type ChannelMessage = Message`、更新 `SendRequest` / `SendResult` / `FetchResult` 类型边界 |
| `pkg/storage/channellog/codec.go` | 以完整消息为输入/输出的日志编码与视图解码 |
| `pkg/storage/channellog/send.go` | 基于完整消息执行 committed append、幂等命中与 committed message 返回 |
| `pkg/storage/channellog/fetch.go` | 返回 committed full messages，而不是瘦 `ChannelMessage` |
| `pkg/storage/channellog/seq_read.go` | `LoadMsg` / range 读取返回 full messages，并保持 `HW` fencing |
| `pkg/storage/channellog/apply.go` | 从 full message 中抽取幂等状态，适配 checkpoint bridge |
| `pkg/storage/channellog/codec_test.go` | 完整消息 round-trip 与截断解码失败覆盖 |
| `pkg/storage/channellog/send_test.go` | `Send` 返回 committed message、幂等命中与冲突覆盖 |
| `pkg/storage/channellog/fetch_test.go` | `Fetch` 返回 full message 且只暴露 committed prefix |
| `pkg/storage/channellog/seq_read_test.go` | 本地顺序读、逆序读、截断与 dirty-tail 不可见覆盖 |
| `pkg/storage/channellog/storage_integration_test.go` | 真实存储路径上的 send/fetch/seq-read/full-message 一致性覆盖 |
| `pkg/storage/channellog/multinode_integration_test.go` | 多节点 committed message 复制与恢复覆盖 |
| `internal/usecase/message/command.go` | 保留发送命令，删除/收敛旧 `CommittedMessageEnvelope` 重复字段 |
| `internal/usecase/message/deps.go` | 让异步提交通道直接收/发 `channellog.Message` |
| `internal/usecase/message/send.go` | 在发送前构造 durable message，发送后提交 committed message |
| `internal/usecase/message/send_test.go` | durable message 构造、提交时机、热路径不回库覆盖 |
| `internal/usecase/delivery/deps.go` | delivery usecase runtime 接口改为接收 durable message |
| `internal/usecase/delivery/submit.go` | 提交接口改为接收 `channellog.Message` |
| `internal/usecase/delivery/types.go` | 从 message usecase 到 delivery runtime 的消息映射收敛 |
| `internal/usecase/delivery/app_test.go` | 提交 full message 到 runtime 的覆盖 |
| `internal/runtime/delivery/types.go` | 运行时 envelope 改为直接承载或包装 `channellog.Message` |
| `internal/runtime/delivery/manager.go` | `Submit` 边界改为接受 durable message |
| `internal/runtime/delivery/shard.go` | shard submit / mailbox 事件适配新的消息载体 |
| `internal/runtime/delivery/actor.go` | inflight / reorder / push 链路改用 durable message |
| `internal/app/deliveryrouting.go` | `Message -> RecvPacket` 视图转换与 owner 提交通路 |
| `internal/app/deliveryrouting_test.go` | durable message 到 realtime packet 的字段映射覆盖 |
| `internal/access/node/options.go` | 远端提交接口签名更新 |
| `internal/access/node/client.go` | 远端提交 committed full message |
| `internal/access/node/delivery_submit_rpc.go` | RPC 请求/响应改为 full message |
| `internal/access/node/delivery_submit_rpc_test.go` | full message RPC 路由覆盖 |
| `internal/app/build.go` | wiring 适配新的 committed message 提交签名 |
| `internal/app/integration_test.go` | 单节点 durable send / fetch / realtime packet 一致性覆盖 |
| `internal/app/multinode_integration_test.go` | 多节点 app 场景下 committed message 读取 helper 与断言迁移 |
| `internal/access/gateway/handler_test.go` | cluster fake 返回值跟随新 `SendResult` 结构更新 |
| `internal/access/api/integration_test.go` | API fake cluster 返回值跟随新 `SendResult` 结构更新 |

## Task 1: 在 `channellog` 中定义完整消息模型和新 codec

**Files:**
- Modify: `pkg/storage/channellog/types.go`
- Modify: `pkg/storage/channellog/codec.go`
- Modify: `pkg/storage/channellog/codec_test.go`

- [ ] **Step 1: 先写失败测试，锁定完整消息 round-trip**

在 `pkg/storage/channellog/codec_test.go` 增加：

- `TestEncodeMessageRoundTripPreservesDurableFields`
- `TestDecodeMessageViewRejectsTruncatedPayload`

测试骨架：

```go
func TestEncodeMessageRoundTripPreservesDurableFields(t *testing.T) {
	msg := Message{
		MessageID:   42,
		Framer:      wkframe.Framer{NoPersist: true, RedDot: true, SyncOnce: true},
		Setting:     3,
		MsgKey:      "k1",
		Expire:      60,
		ClientSeq:   9,
		ClientMsgNo: "m-1",
		StreamNo:    "s-1",
		StreamID:    77,
		StreamFlag:  wkframe.StreamFlagEnd,
		Timestamp:   123456,
		ChannelID:   "u1@u2",
		ChannelType: wkframe.ChannelTypePerson,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	encoded, err := encodeMessage(msg)
	require.NoError(t, err)

	view, err := decodeMessageView(encoded)
	require.NoError(t, err)
	require.Equal(t, msg.MessageID, view.MessageID)
	require.Equal(t, msg.MsgKey, view.MsgKey)
	require.Equal(t, msg.StreamFlag, view.StreamFlag)
	require.Equal(t, msg.Payload, view.Payload)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./pkg/storage/channellog -run "TestEncodeMessage|TestDecodeMessageView" -count=1`

Expected: FAIL，因为当前只有 `storedMessage`，没有完整 `Message` codec。

- [ ] **Step 3: 写最小实现**

在 `pkg/storage/channellog/types.go` 中引入公开模型：

```go
type Message struct {
	MessageID   uint64
	MessageSeq  uint64
	Framer      wkframe.Framer
	Setting     wkframe.Setting
	MsgKey      string
	Expire      uint32
	ClientSeq   uint64
	ClientMsgNo string
	StreamNo    string
	StreamID    uint64
	StreamFlag  wkframe.StreamFlag
	Timestamp   int32
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	Payload     []byte
}
```

为保证 Task 1 到 Task 5 之间的中间提交持续可编译，先保留过渡别名：

```go
type ChannelMessage = Message
```

该别名只用于迁移窗口；等 Task 6 把 `internal/app/multinode_integration_test.go` 等剩余旧调用点清理完后再删除。

在 `pkg/storage/channellog/codec.go` 中：

- 把旧 `storedMessage` 收敛成内部编码载体，例如：

```go
type encodedMessage struct {
	Message     Message
	PayloadHash uint64
}
```

- 提供统一入口：

```go
func encodeMessage(msg Message) ([]byte, error)
func decodeMessage(payload []byte) (Message, error)
func decodeMessageView(payload []byte) (messageView, error)
```

- 保留内部 `PayloadHash`，但不要把它暴露到公开 `Message`。
- 为了不在 Task 1 一次性打穿 `send.go` / `fetch.go` / `apply.go` 与相关测试，先保留 `storedMessage`、`storedMessageView`、`encodeStoredMessage`、`decodeStoredMessage`、`decodeStoredMessageView` 这组内部兼容 wrapper，让它们转调新 codec；等 Task 2/Task 3 把调用点迁走后再删除。

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./pkg/storage/channellog -run "TestEncodeMessage|TestDecodeMessageView" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/types.go pkg/storage/channellog/codec.go pkg/storage/channellog/codec_test.go
git commit -m "refactor(channellog): add durable message codec"
```

## Task 2: 改造 `channellog.Send`，返回 committed full message，并同步迁移 `SendRequest` 构造点

**Files:**
- Modify: `pkg/storage/channellog/types.go`
- Modify: `pkg/storage/channellog/send.go`
- Modify: `pkg/storage/channellog/send_test.go`
- Modify: `pkg/storage/channellog/benchmark_test.go`
- Modify: `pkg/storage/channellog/storage_integration_test.go`
- Modify: `pkg/storage/channellog/multinode_integration_test.go`
- Modify: `pkg/storage/channellog/storage_testenv_test.go`
- Modify: `internal/usecase/message/send.go`

- [ ] **Step 1: 先写失败测试，锁定 committed message 返回值**

在 `pkg/storage/channellog/send_test.go` 增加：

- `TestSendReturnsCommittedMessageWithDurableFields`
- `TestSendDuplicateReturnsOriginalCommittedMessage`

测试骨架：

```go
func TestSendReturnsCommittedMessageWithDurableFields(t *testing.T) {
	env := newSendEnv(t)

	result, err := env.cluster.Send(context.Background(), SendRequest{
		ChannelID:   "c1",
		ChannelType: 1,
		Message: Message{
			Framer:      wkframe.Framer{RedDot: true},
			Setting:     1,
			MsgKey:      "k1",
			ClientSeq:   9,
			ClientMsgNo: "m1",
			Timestamp:   123,
			ChannelID:   "c1",
			ChannelType: 1,
			Topic:       "chat",
			FromUID:     "u1",
			Payload:     []byte("hello"),
		},
		SupportsMessageSeqU64: true,
	})

	require.NoError(t, err)
	require.Equal(t, uint64(1), result.MessageSeq)
	require.Equal(t, result.MessageID, result.Message.MessageID)
	require.Equal(t, uint64(1), result.Message.MessageSeq)
	require.Equal(t, "u1", result.Message.FromUID)
	require.Equal(t, []byte("hello"), result.Message.Payload)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./pkg/storage/channellog -run "TestSendReturnsCommittedMessage|TestSendDuplicateReturnsOriginalCommittedMessage" -count=1`

Expected: FAIL，因为 `SendRequest` / `SendResult` 还没有承载 `Message`，现有构造点仍在写旧字段。

- [ ] **Step 3: 写最小实现**

更新 `pkg/storage/channellog/types.go`：

```go
type SendRequest struct {
	ChannelID             string
	ChannelType           uint8
	Message               Message
	SupportsMessageSeqU64 bool
	ExpectedChannelEpoch  uint64
	ExpectedLeaderEpoch   uint64
}

type SendResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
}
```

更新 `pkg/storage/channellog/send.go`：

- 正常热路径使用请求内的 `Message` 草稿，补 `MessageID` / `MessageSeq`
- `group.Append` 成功后返回 committed message
- 幂等命中时允许通过 `offset` 读取单条日志并 `decodeMessageView`，仅用于 duplicate path，不用于正常热路径

实现轮廓：

```go
draft := req.Message
draft.MessageID = c.cfg.MessageIDs.Next()

encoded, err := encodeMessage(draft)
commit, err := group.Append(ctx, []isr.Record{{Payload: encoded, SizeBytes: len(encoded)}})

committed := draft
committed.MessageSeq = commit.NextCommitHW

return SendResult{
	MessageID:  committed.MessageID,
	MessageSeq: committed.MessageSeq,
	Message:    committed,
}, nil
```

duplicate path helper：

```go
func (c *cluster) loadMessageAtOffset(groupKey isr.GroupKey, offset uint64) (Message, error)
```

同一个任务里同步迁移现存 `SendRequest` 构造点，避免提交后树处于不可编译状态：

- `internal/usecase/message/send.go` 先做最小兼容填充，只把当前已有 durable 字段写入 `req.Message`，暂不翻转 `CommittedMessageDispatcher`
- `pkg/storage/channellog/storage_integration_test.go`
- `pkg/storage/channellog/multinode_integration_test.go`
- `pkg/storage/channellog/benchmark_test.go`

`internal/usecase/message/send.go` 在本任务只需要做到：

```go
draft := channellog.Message{
	ClientMsgNo: cmd.ClientMsgNo,
	ChannelID:   cmd.ChannelID,
	ChannelType: cmd.ChannelType,
	FromUID:     cmd.SenderUID,
	Payload:     append([]byte(nil), cmd.Payload...),
}
result, err := sendWithMetaRefreshRetry(ctx, a.cluster, a.refresher, channellog.SendRequest{
	ChannelID:             cmd.ChannelID,
	ChannelType:           cmd.ChannelType,
	Message:               draft,
	SupportsMessageSeqU64: supportsMessageSeqU64(cmd.ProtocolVersion),
	ExpectedChannelEpoch:  cmd.ExpectedChannelEpoch,
	ExpectedLeaderEpoch:   cmd.ExpectedLeaderEpoch,
})
```

完整的 durable message builder、`Timestamp` / `MsgKey` / `Framer` 等字段收敛，以及 committed message 调度签名翻转，留到 Task 4 一次性处理。

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./pkg/storage/channellog -run "TestSendReturnsCommittedMessage|TestSendDuplicateReturnsOriginalCommittedMessage" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/types.go pkg/storage/channellog/send.go pkg/storage/channellog/send_test.go pkg/storage/channellog/benchmark_test.go pkg/storage/channellog/storage_integration_test.go pkg/storage/channellog/multinode_integration_test.go pkg/storage/channellog/storage_testenv_test.go internal/usecase/message/send.go
git commit -m "refactor(channellog): return committed durable message"
```

## Task 3: 统一 `Fetch` / `Store` 读取路径到 full message，并锁定 committed visibility

**Files:**
- Modify: `pkg/storage/channellog/fetch.go`
- Modify: `pkg/storage/channellog/seq_read.go`
- Modify: `pkg/storage/channellog/apply.go`
- Modify: `pkg/storage/channellog/fetch_test.go`
- Modify: `pkg/storage/channellog/seq_read_test.go`
- Modify: `pkg/storage/channellog/apply_test.go`
- Modify: `pkg/storage/channellog/storage_integration_test.go`
- Modify: `pkg/storage/channellog/multinode_integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定 full message 读取和 dirty tail 不可见**

补或改以下测试：

- `TestFetchReturnsCommittedFullMessages`
- `TestLoadMsgReturnsFullMessage`
- `TestClusterWithRealStoreFetchHidesDirtyTailAfterReplicaRecovery`
- `TestCheckpointBridgeUsesFromUIDForIdempotency`

测试骨架：

```go
func TestLoadMsgReturnsFullMessage(t *testing.T) {
	store := openTestStore(t, ChannelKey{ChannelID: "c1", ChannelType: 1})
	mustAppendMessages(t, store, []Message{{
		MessageID:   11,
		Framer:      wkframe.Framer{SyncOnce: true},
		MsgKey:      "k1",
		Timestamp:   123,
		ChannelID:   "c1",
		ChannelType: 1,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("one"),
	}})
	mustStoreCheckpoint(t, store, isr.Checkpoint{Epoch: 1, HW: 1})

	msg, err := store.LoadMsg(1)
	require.NoError(t, err)
	require.Equal(t, uint64(11), msg.MessageID)
	require.Equal(t, "u1", msg.FromUID)
	require.Equal(t, "chat", msg.Topic)
	require.Equal(t, []byte("one"), msg.Payload)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./pkg/storage/channellog -run "TestFetch|TestLoadMsg|TestClusterWithRealStoreFetchHidesDirtyTailAfterReplicaRecovery|TestCheckpointBridge" -count=1`

Expected: FAIL，因为当前读取路径仍然返回瘦 `ChannelMessage`，`apply.go` 仍然使用旧字段名。

- [ ] **Step 3: 写最小实现**

在 `pkg/storage/channellog/fetch.go` 和 `pkg/storage/channellog/seq_read.go` 中统一使用：

```go
func decodeLogRecordMessage(record LogRecord) (Message, error) {
	view, err := decodeMessageView(record.Payload)
	if err != nil {
		return Message{}, err
	}
	msg := view.Message
	msg.MessageSeq = record.Offset + 1
	return msg, nil
}
```

并更新：

- `FetchResult.Messages []Message`
- `LoadMsg` / `LoadNextRangeMsgs` / `LoadPrevRangeMsgs` 返回 `Message`
- `apply.go` 从 `message.FromUID` 提取幂等键

同时保持：

- `Fetch` 继续以 `state.HW` 为 committed fence
- `Store` 继续以 checkpoint `HW` 为 committed fence
- `TruncateLogTo` 继续同步修正 checkpoint 与 epoch history

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./pkg/storage/channellog -run "TestFetch|TestLoadMsg|TestClusterWithRealStoreFetchHidesDirtyTailAfterReplicaRecovery|TestCheckpointBridge" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/fetch.go pkg/storage/channellog/seq_read.go pkg/storage/channellog/apply.go pkg/storage/channellog/fetch_test.go pkg/storage/channellog/seq_read_test.go pkg/storage/channellog/apply_test.go pkg/storage/channellog/storage_integration_test.go pkg/storage/channellog/multinode_integration_test.go
git commit -m "refactor(channellog): return full messages from read paths"
```

## Task 4: 让 `internal/usecase/message` 在发送前构造 durable message，并把 committed dispatcher 翻转到 `Message`（含 app 兼容适配）

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/api/integration_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: 先写失败测试，锁定 durable message 构造和提交时机**

在 `internal/usecase/message/send_test.go` 增加或改造：

- `TestSendBuildsDurableMessageBeforeClusterSend`
- `TestSendSubmitsCommittedMessageFromClusterResult`
- `TestSendDoesNotDispatchUncommittedDraftMessage`

并在 `internal/app/deliveryrouting_test.go` 增加过渡桥测试：

- `TestAsyncCommittedDispatcherBridgesDurableMessageToLegacyEnvelope`

测试骨架：

```go
func TestSendSubmitsCommittedMessageFromClusterResult(t *testing.T) {
	dispatcher := &recordingCommittedDispatcher{}
	cluster := &fakeChannelCluster{
		result: channellog.SendResult{
			MessageID:  88,
			MessageSeq: 7,
			Message: channellog.Message{
				MessageID:   88,
				MessageSeq:  7,
				MsgKey:      "k1",
				Timestamp:   123,
				ChannelID:   "u1@u2",
				ChannelType: wkframe.ChannelTypePerson,
				FromUID:     "u1",
				Payload:     []byte("hello"),
			},
		},
	}

	app := New(Options{Cluster: cluster, CommittedDispatcher: dispatcher})

	_, err := app.Send(context.Background(), SendCommand{
		SenderUID:   "u1",
		ChannelID:   "u2",
		ChannelType: wkframe.ChannelTypePerson,
		ClientMsgNo: "m1",
		Payload:     []byte("hello"),
	})

	require.NoError(t, err)
	require.Equal(t, uint64(88), dispatcher.calls[0].MessageID)
	require.Equal(t, uint64(7), dispatcher.calls[0].MessageSeq)
	require.Equal(t, int32(123), dispatcher.calls[0].Timestamp)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -run "TestSendBuildsDurableMessage|TestSendSubmitsCommittedMessage|TestSendDoesNotDispatchUncommittedDraftMessage|TestAsyncCommittedDispatcher" -count=1`

Expected: FAIL，因为当前 `message.Send` 仍然从 `SendCommand` 派生 `CommittedMessageEnvelope`，`internal/app` wiring 也还没跟上 `CommittedMessageDispatcher` 的新签名。

- [ ] **Step 3: 写最小实现**

在 `internal/usecase/message/send.go` 中引入 durable message builder：

```go
func buildDurableMessage(cmd SendCommand, now time.Time) channellog.Message {
	return channellog.Message{
		Framer:      cmd.Framer,
		Setting:     cmd.Setting,
		MsgKey:      cmd.MsgKey,
		Expire:      cmd.Expire,
		ClientSeq:   cmd.ClientSeq,
		ClientMsgNo: cmd.ClientMsgNo,
		StreamNo:    cmd.StreamNo,
		Timestamp:   int32(now.Unix()),
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
		Topic:       cmd.Topic,
		FromUID:     cmd.SenderUID,
		Payload:     append([]byte(nil), cmd.Payload...),
	}
}
```

并把依赖签名改成直接提交 `channellog.Message`：

```go
type CommittedMessageDispatcher interface {
	SubmitCommitted(ctx context.Context, msg channellog.Message) error
}
```

发送时：

```go
draft := buildDurableMessage(cmd, a.now())
result, err := sendWithMetaRefreshRetry(ctx, a.cluster, a.refresher, channellog.SendRequest{
	ChannelID:             cmd.ChannelID,
	ChannelType:           cmd.ChannelType,
	Message:               draft,
	SupportsMessageSeqU64: supportsMessageSeqU64(cmd.ProtocolVersion),
	ExpectedChannelEpoch:  cmd.ExpectedChannelEpoch,
	ExpectedLeaderEpoch:   cmd.ExpectedLeaderEpoch,
})
if a.dispatcher != nil {
	_ = a.dispatcher.SubmitCommitted(ctx, result.Message)
}
```

同一个任务里同步更新 `internal/app` 适配层，保证接口翻转后仍然可编译：

```go
func (d asyncCommittedDispatcher) SubmitCommitted(ctx context.Context, msg channellog.Message) error {
	env := message.CommittedMessageEnvelope{
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		MessageID:   msg.MessageID,
		MessageSeq:  msg.MessageSeq,
		SenderUID:   msg.FromUID,
		ClientMsgNo: msg.ClientMsgNo,
		Topic:       msg.Topic,
		Payload:     append([]byte(nil), msg.Payload...),
		Framer:      msg.Framer,
		Setting:     msg.Setting,
		MsgKey:      msg.MsgKey,
		Expire:      msg.Expire,
		StreamNo:    msg.StreamNo,
		ClientSeq:   msg.ClientSeq,
	}
	// 其余 owner routing 逻辑保持不变
}
```

也就是说，Task 4 只翻转 `message -> app dispatcher` 这一段边界；`app -> delivery/node/runtime` 先保留旧 envelope，等 Task 5 再整段切成 `channellog.Message`，并删除这段过渡桥。

同步更新 fake cluster / handler test / api test / deliveryrouting test 的构造值。

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app -run "TestSendBuildsDurableMessage|TestSendSubmitsCommittedMessage|TestSendDoesNotDispatchUncommittedDraftMessage|TestAsyncCommittedDispatcher" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/usecase/message/command.go internal/usecase/message/deps.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/access/gateway/handler_test.go internal/access/api/integration_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/app/build.go
git commit -m "refactor(message): send durable committed messages"
```

## Task 5: 让 delivery / node / app 下游链路直接消费 durable message，并删除 Task 4 的兼容桥

**Files:**
- Modify: `internal/usecase/delivery/deps.go`
- Modify: `internal/usecase/delivery/types.go`
- Modify: `internal/usecase/delivery/submit.go`
- Modify: `internal/usecase/delivery/app_test.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/manager.go`
- Modify: `internal/runtime/delivery/shard.go`
- Modify: `internal/runtime/delivery/actor.go`
- Modify: `internal/runtime/delivery/manager_test.go`
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/client.go`
- Modify: `internal/access/node/delivery_submit_rpc.go`
- Modify: `internal/access/node/delivery_submit_rpc_test.go`
- Modify: `internal/app/integration_test.go`

- [ ] **Step 1: 先写失败测试，锁定 realtime packet 由 durable message 生成**

补或改以下测试：

- `TestSubmitCommittedDelegatesToRuntimeWithDurableMessage`
- `TestBuildRealtimeRecvPacketUsesDurableTimestamp`
- `TestBuildRealtimeRecvPacketRewritesPersonChannelIDForRecipient`
- `TestSubmitCommittedMessageRPCRoutesDurableMessage`

测试骨架：

```go
func TestBuildRealtimeRecvPacketUsesDurableTimestamp(t *testing.T) {
	msg := channellog.Message{
		MessageID:   88,
		MessageSeq:  7,
		Framer:      wkframe.Framer{RedDot: true},
		Setting:     1,
		MsgKey:      "k1",
		ClientSeq:   9,
		ClientMsgNo: "m1",
		Timestamp:   123,
		ChannelID:   "u1@u2",
		ChannelType: wkframe.ChannelTypePerson,
		Topic:       "chat",
		FromUID:     "u1",
		Payload:     []byte("hello"),
	}

	packet := buildRealtimeRecvPacket(msg, "u2")
	require.Equal(t, int32(123), packet.Timestamp)
	require.Equal(t, "u1", packet.ChannelID)
	require.Equal(t, "u1", packet.FromUID)
}
```

- [ ] **Step 2: 跑聚焦测试，确认当前实现失败**

Run: `go test ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/app -run "TestSubmitCommitted|TestBuildRealtimeRecvPacket|TestSubmitCommittedMessageRPC" -count=1`

Expected: FAIL，因为当前提交链路仍然使用 `CommittedMessageEnvelope` 镜像对象，`buildRealtimeRecvPacket` 仍然依赖 runtime envelope 和 `now()`。

- [ ] **Step 3: 写最小实现**

把提交通路统一成 `channellog.Message`：

```go
type Runtime interface {
	Submit(ctx context.Context, msg channellog.Message) error
}
```

在 `internal/app/deliveryrouting.go` 中引入稳定视图转换：

```go
func buildRealtimeRecvPacket(msg channellog.Message, recipientUID string) *wkframe.RecvPacket {
	packet := &wkframe.RecvPacket{
		Framer:      msg.Framer,
		Setting:     msg.Setting,
		MsgKey:      msg.MsgKey,
		Expire:      msg.Expire,
		MessageID:   int64(msg.MessageID),
		MessageSeq:  msg.MessageSeq,
		ClientMsgNo: msg.ClientMsgNo,
		StreamNo:    msg.StreamNo,
		StreamId:    msg.StreamID,
		StreamFlag:  msg.StreamFlag,
		Timestamp:   msg.Timestamp,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		FromUID:     msg.FromUID,
		Payload:     append([]byte(nil), msg.Payload...),
		ClientSeq:   msg.ClientSeq,
	}
	if msg.ChannelType == wkframe.ChannelTypePerson && recipientUID != "" {
		packet.ChannelID = msg.FromUID
	}
	return packet
}
```

更新：

- delivery usecase 的 `deps.go`
- delivery runtime 的 envelope / test helper
- delivery runtime 的 `manager.go` / `shard.go` / `actor.go`
- node submit RPC 的 request struct
- `asyncCommittedDispatcher` / `recordingCommittedSubmitter`
- 删除 Task 4 中 `Message -> CommittedMessageEnvelope` 的临时桥接，让 app 下游直接消费 `channellog.Message`

- [ ] **Step 4: 重新跑聚焦测试，确认通过**

Run: `go test ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/app -run "TestSubmitCommitted|TestBuildRealtimeRecvPacket|TestSubmitCommittedMessageRPC" -count=1`

Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/usecase/delivery/deps.go internal/usecase/delivery/types.go internal/usecase/delivery/submit.go internal/usecase/delivery/app_test.go internal/runtime/delivery/types.go internal/runtime/delivery/manager.go internal/runtime/delivery/shard.go internal/runtime/delivery/actor.go internal/runtime/delivery/manager_test.go internal/runtime/delivery/actor_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go internal/access/node/options.go internal/access/node/client.go internal/access/node/delivery_submit_rpc.go internal/access/node/delivery_submit_rpc_test.go internal/app/integration_test.go
git commit -m "refactor(delivery): use durable messages end to end"
```

## Task 6: 跑受影响回归并修正冲突语义遗漏

**Files:**
- Modify: `pkg/storage/channellog/types.go`
- Modify: `pkg/storage/channellog/storage_integration_test.go`
- Modify: `pkg/storage/channellog/multinode_integration_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: 其他在全量受影响测试中暴露编译或断言漂移的文件

- [ ] **Step 1: 先跑受影响回归，记录失败点**

Run:

```bash
go test ./pkg/storage/channellog ./internal/usecase/message ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/access/gateway ./internal/access/api ./internal/app -count=1
```

Expected: 初次运行可能 FAIL，暴露遗漏的 fake、断言字段、老类型别名或冲突语义回归问题。

- [ ] **Step 2: 补失败测试或修正已有断言**

重点补齐以下遗漏：

- dirty tail 截断后 `Fetch` / `LoadMsg` 仍不可见
- 多节点恢复后读取到的是 full message，而不是瘦消息
- 单节点 app 集成测试中 `Fetch` 返回的消息字段与 realtime packet 一致
- 清理仍引用 `ChannelMessage` 旧名的 helper / 断言，至少包含 `internal/app/multinode_integration_test.go`
- 删除 Task 1 引入的 `type ChannelMessage = Message` 过渡别名

示例断言：

```go
require.Equal(t, result.MessageSeq, fetch.Messages[0].MessageSeq)
require.Equal(t, "sender", fetch.Messages[0].FromUID)
require.Equal(t, []byte("hello durable"), fetch.Messages[0].Payload)
```

- [ ] **Step 3: 跑受影响回归，确认通过**

Run:

```bash
go test ./pkg/storage/channellog ./internal/usecase/message ./internal/usecase/delivery ./internal/runtime/delivery ./internal/access/node ./internal/access/gateway ./internal/access/api ./internal/app -count=1
```

Expected: PASS

- [ ] **Step 4: 跑最终验证命令**

Run:

```bash
go test ./pkg/storage/channellog -count=1
go test ./internal/usecase/message ./internal/usecase/delivery ./internal/runtime/delivery -count=1
go test ./internal/access/node ./internal/access/gateway ./internal/access/api ./internal/app -count=1
```

Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/channellog/types.go pkg/storage/channellog/storage_integration_test.go pkg/storage/channellog/multinode_integration_test.go internal/app/integration_test.go internal/app/multinode_integration_test.go
git add -u
git commit -m "test: cover durable message model regressions"
```

## 本地审查清单

- [ ] `pkg/storage/channellog` 不再向外暴露瘦 `ChannelMessage` 语义
- [ ] `type ChannelMessage = Message` 过渡别名已删除
- [ ] 正常发送热路径没有新增“提交后回库读消息”的逻辑
- [ ] duplicate path 如需读一条日志，仅发生在幂等命中分支
- [ ] `SendAck` 只基于 committed message 返回
- [ ] `Fetch` / `LoadMsg` / range 读取继续严格受 `HW` fencing 约束
- [ ] realtime packet 的 `Timestamp`、`MsgKey`、`ClientSeq` 等字段来自 durable message，而不是重新生成
- [ ] 个人频道 `ChannelID` 改写只存在于 realtime 视图层
- [ ] `dispatchLate` 注释与语义已更新为“处理乱序已提交消息”

Plan complete and saved to `docs/superpowers/plans/2026-04-07-channellog-message-model-implementation.md`. Ready to execute?
