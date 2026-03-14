# 事件流 DB 方法与伪代码

## 1. 目标

本文档定义消息事件流在 `wkdb` 层的关键数据结构、方法签名和伪代码，供 `eventappend`、`eventsync`、`messagesync` 实现直接对照。

约束：

1. 事件必须挂在 `client_msg_no` 下。
2. 顺序字段统一为消息级 `msg_event_seq`（跨 `lane_id` 共享）。
3. `MessageLaneState` 更新在 raft 提案中原子完成，事件不逐条落盘（仅更新投影状态）。
4. Key 中包含 channel 维度哈希（`channel_id + channel_type`），确保不同频道的事件隔离。

## 2. 存储对象（逻辑）

### 2.1 MessageEventLog

事件明细日志。

- 主维度：`client_msg_no + msg_event_seq`
- 关键字段：`event_id`、`lane_id`、`event_type`、`payload`、`visibility`、`occurred_at`

### 2.2 MessageEventIdIndex

幂等索引。

- 唯一键：`client_msg_no + event_id`
- 值：`msg_event_seq`（或事件主键）

### 2.3 MessageLaneState

离线聚合投影。

- 主键：`client_msg_no + lane_id`
- 字段：`status`、`last_msg_event_seq`、`snapshot_payload`、`end_reason`、`error`

### 2.4 MessageEventSeq

消息级序号分配器。

- 键：`client_msg_no`
- 值：当前最大 `msg_event_seq`

## 3. 建议 Key 组织（Pebble）

按现有 `pkg/wkdb/key` 风格，可新增以下 key 构造器（命名示意）：

```go
func NewMessageLaneStateKey(channelId string, channelType uint8, clientMsgNo, laneID string) []byte
func NewMessageLaneStateLowKey(channelId string, channelType uint8, clientMsgNo string) []byte
func NewMessageLaneStateHighKey(channelId string, channelType uint8, clientMsgNo string) []byte
func NewMessageEventSeqKey(channelId string, channelType uint8, clientMsgNo string) []byte
```

查询范围：

1. `eventsync`：按 `client_msg_no` 前缀 + `msg_event_seq` 游标顺序扫描。
2. `messagesync`：按 `client_msg_no` 前缀扫描 `MessageLaneState`，不扫全量事件。

## 4. wkdb 方法签名（建议）

```go
type MessageEvent struct {
    ChannelId   string
    ChannelType uint8
    ClientMsgNo string
    MsgEventSeq uint64
    EventID     string
    LaneID      string
    EventType   string
    Visibility  string
    OccurredAt  int64
    Payload     []byte
}

type MessageLaneState struct {
    ChannelId        string
    ChannelType      uint8
    ClientMsgNo      string
    LaneID           string
    Status           string
    LastMsgEventSeq  uint64
    SnapshotPayload  []byte
    EndReason        uint8
    Error            string
}

func (wk *wukongDB) AppendMessageEventWithLaneState(evt *MessageEvent) (*MessageEvent, *MessageLaneState, error)
func (wk *wukongDB) GetMessageEventByEventID(channelId string, channelType uint8, clientMsgNo, eventID string) (*MessageEvent, error)
func (wk *wukongDB) ListMessageEvents(channelId string, channelType uint8, clientMsgNo string, fromMsgEventSeq uint64, laneID string, limit int) ([]MessageEvent, error)
func (wk *wukongDB) GetMessageLaneStates(channelId string, channelType uint8, clientMsgNo string) ([]MessageLaneState, error)
func (wk *wukongDB) GetMessageLaneState(channelId string, channelType uint8, clientMsgNo, laneID string) (*MessageLaneState, error)
```

## 5. 关键伪代码

### 5.1 写入：`AppendMessageEventWithLaneState`

```text
function AppendMessageEventWithLaneState(evt):
  db = shardDB(evt.ClientMsgNo)

  // 1) 幂等检查：通过 lane state 的 last_event_id 判重
  lane = getLaneState(evt.ChannelId, evt.ChannelType, evt.ClientMsgNo, evt.LaneID)
  if lane != nil and lane.LastEventID == evt.EventID:
    return buildProjectedEvent(lane), lane, nil

  // 2) 终态检查
  if lane != nil and isTerminal(lane.Status):
    return buildProjectedEvent(lane), lane, nil

  batch = db.NewBatch()

  // 3) 分配消息级序号（channel 维度隔离）
  seq = incrAndGetMessageEventSeq(evt.ChannelId, evt.ChannelType, evt.ClientMsgNo, batch)
  evt.MsgEventSeq = seq

  // 4) 合并 lane state（事件不逐条落盘，仅更新投影）
  lane = reduceLaneState(lane, evt)
  writeLaneState(lane, batch)  // key 包含 channel hash

  // 5) 原子提交
  batch.CommitWait()
  return evt, lane, nil
```

### 5.2 读取：`ListMessageEvents`（eventsync）

```text
function ListMessageEvents(channelId, channelType, clientMsgNo, fromSeq, laneID, limit):
  // 从 lane state 构建投影视图（非原始事件序列）
  states = GetMessageLaneStates(channelId, channelType, clientMsgNo)
  out = []
  for state in states:
    if laneID != "" and state.LaneID != laneID:
      continue
    if state.LastMsgEventSeq <= fromSeq:
      continue
    out.append(buildProjectedEvent(state))
  sort(out, by=MsgEventSeq)
  return out[:limit]
```

### 5.3 读取：`GetMessageLaneStates`（messagesync）

```text
function GetMessageLaneStates(channelId, channelType, clientMsgNo):
  // Key 范围包含 channel hash，确保频道隔离
  iter = newLaneStateIter(channelId, channelType, clientMsgNo)
  lanes = []
  for row in iter:
    lanes.append(parseLaneState(row))
  return lanes
```

## 6. 与 API 层对接

1. `POST /message/event`：调用 `AppendMessageEventWithLaneState`，直接拿到 `msg_event_seq` 与 lane 最新状态。
2. `POST /message/eventsync`：调用 `ListMessageEvents`，生成 `from_msg_event_seq/next_msg_event_seq`。
3. `POST /channel/messagesync`：先查消息，再用 `GetMessageLaneStates(client_msg_no)` 填充 `event_meta`。

## 7. 实现注意点

1. 同一 `client_msg_no` 的写入必须串行化（或通过提案日志保证顺序），避免序号竞争。
2. `event_id` 判重要在同一批次内完成，避免并发重试重复写入。
3. `lane_id` 为空统一归一化为 `main`，保证查询与合并一致。
4. `payload` 建议保留原始字节，解析放在上层 reducer，降低 DB 层耦合。
