# 事件合并规则（Event Merge Rules）

## 1. 目的

本文档定义 `Message-Centric Event Stream` 的**确定性合并规则**，用于生成离线接口（如 `POST /channel/messagesync`）中的合并结果。

核心约束：

1. `Message` 是地基，`Event` 必须挂在 `client_msg_no` 上。
2. 合并在 `client_msg_no + lane_id` 维度执行。
3. 合并输出为 `MessageLaneState` 投影，离线读取优先使用投影而非全量回放。

## 2. 输入与输出

输入：

- 某条消息下的事件集合：`(client_msg_no, lane_id, msg_event_seq, event_type, payload, visibility, occurred_at)`

输出：

- `MessageLaneState`：
  - `client_msg_no`
  - `lane_id`
  - `status` (`open/closed/error/cancelled`)
  - `last_msg_event_seq`
  - `snapshot_payload`
  - `end_reason`
  - `error`

## 3. 排序与幂等

1. **排序键**：`msg_event_seq`（服务端分配）升序。
2. **幂等键**：`(client_msg_no, event_id)`；重复事件忽略。
3. `occurred_at` 仅用于展示，不参与合并顺序。

命名说明：

1. `msg_event_seq` 表示“消息级事件序号”，作用域是同一 `client_msg_no`。
2. 不同 `lane_id` 共享同一 `msg_event_seq` 序号空间，不单独编号。

## 4. 通用事件语义

支持类型：

1. `stream.open`
2. `stream.delta`
3. `stream.snapshot`
4. `stream.close`
5. `stream.error`
6. `stream.cancel`

状态转换：

1. 初始：`status=init`
2. `open` -> `status=open`
3. `delta/snapshot`：仅更新内容，不改变终态
4. `close` -> `status=closed`
5. `error` -> `status=error`
6. `cancel` -> `status=cancelled`

终态规则：

1. 进入终态后，后续非幂等重复事件默认忽略（可记录告警）。

## 5. payload.kind 合并策略

### 5.1 `kind=text`

- `stream.delta`：按顺序字符串追加
- `stream.snapshot`：覆盖当前文本快照
- `stream.close`：冻结最终文本

### 5.2 `kind=tool`

- `stream.open`：初始化工具上下文（`tool_name/tool_call_id`）
- `stream.delta`：通常用于 `args_delta` 追加
- `stream.snapshot`：写入结果对象（覆盖）
- `stream.close`：冻结结果

### 5.3 `kind=workflow/order`

- 以 `stream.snapshot` 为主（状态覆盖）
- `delta` 可用于日志增量，但不应改变最终状态语义

### 5.4 `kind=binary`

- `delta` 使用分片拼接（建议 payload 带 `chunk_index`）
- `snapshot` 可直接覆盖为完整二进制引用（如对象存储URL）

## 6. 合并伪代码

```text
for each lane in groupBy(client_msg_no, lane_id):
  state = loadOrInit(lane)
  events = sortBy(msg_event_seq)

  for e in events:
    if isDuplicate(client_msg_no, e.event_id):
      continue
    if isTerminal(state.status):
      continue

    switch e.event_type:
      case stream.open:
        state.status = open
        applyOpenMeta(state, e.payload)
      case stream.delta:
        applyDelta(state, e.payload.kind, e.payload)
      case stream.snapshot:
        applySnapshot(state, e.payload.kind, e.payload)
      case stream.close:
        state.status = closed
        applyClose(state, e.payload)
      case stream.error:
        state.status = error
        state.error = e.payload.error
      case stream.cancel:
        state.status = cancelled

    state.last_msg_event_seq = e.msg_event_seq

  saveLaneState(state)
```

## 7. 离线接口映射

### 7.1 `POST /channel/messagesync`

返回每条消息时：

1. 读取该消息所有 `MessageLaneState`
2. 填充 `event_meta.lanes[]`
3. 兼容旧字段：
   - `stream_data` <- `lane_id=main` 的文本或二进制快照
   - `end/end_reason/error` <- `lane_id=main` 终态

### 7.2 `POST /message/eventsync`

按 `client_msg_no + from_msg_event_seq` 返回事件增量，用于细粒度回放。

## 8. 示例

### 示例A：文本单轨道

事件序列（`lane_id=main`）：

1. `stream.open`
2. `stream.delta` `{"kind":"text","delta":"你好"}`
3. `stream.delta` `{"kind":"text","delta":"，世界"}`
4. `stream.close`

合并结果：

```json
{
  "lane_id": "main",
  "status": "closed",
  "last_msg_event_seq": 4,
  "snapshot_payload": {
    "kind": "text",
    "text": "你好，世界"
  }
}
```

### 示例B：工具轨道

事件序列（`lane_id=tool_weather`）：

1. `stream.open` `{"kind":"tool","tool_name":"weather.search","tool_call_id":"tc1"}`
2. `stream.delta` `{"kind":"tool","tool_call_id":"tc1","args_delta":"{\"city\":\"Beijing\"}"}`
3. `stream.snapshot` `{"kind":"tool","tool_call_id":"tc1","result":{"temp_c":26}}`
4. `stream.close`

合并结果：

```json
{
  "lane_id": "tool_weather",
  "status": "closed",
  "last_msg_event_seq": 4,
  "snapshot_payload": {
    "kind": "tool",
    "tool_name": "weather.search",
    "tool_call_id": "tc1",
    "result": {"temp_c": 26}
  }
}
```

### 示例C：同消息多轨道（文本 + 工具）

`client_msg_no=cmn_agent_001`，有两条轨道：

1. `main`（文本）
2. `tool_weather`（工具）

`messagesync` 可返回：

```json
{
  "client_msg_no": "cmn_agent_001",
  "event_meta": {
    "has_events": true,
    "lane_count": 2,
    "open_lane_count": 1,
    "lanes": [
      {
        "lane_id": "main",
        "status": "open",
        "last_msg_event_seq": 12,
        "snapshot": {"kind":"text","text":"北京今天26°C，晴。"}
      },
      {
        "lane_id": "tool_weather",
        "status": "closed",
        "last_msg_event_seq": 8,
        "snapshot": {
          "kind": "tool",
          "tool_name": "weather.search",
          "tool_call_id": "tc_weather_01",
          "result": {"temp_c": 26, "condition": "Sunny"}
        }
      }
    ]
  }
}
```

## 9. 实现建议

1. 写路径：`MessageLaneState` 更新在 raft 提案链路中原子完成，**事件不逐条落盘**（仅更新投影状态）。
2. 读路径：`messagesync` 默认只读 `MessageLaneState`，不扫描全量事件。`eventsync` 返回的是从 `MessageLaneState` 构建的投影视图（每个 lane 至多一条），而非原始事件序列。
3. 清理策略：终态轨道保留投影快照即可。

## 10. 关键伪代码（可直接映射实现）

### 10.1 `POST /message/eventappend` 写路径

说明：事件不逐条落盘，仅更新 `MessageLaneState` 投影。`msg_event_seq` 用于标记投影版本。

```text
function HandleEventAppend(req):
  assert req.client_msg_no != ""
  assert req.event_id != ""
  assert req.event_type in {
    "stream.open", "stream.delta", "stream.snapshot",
    "stream.close", "stream.error", "stream.cancel"
  }

  msg = loadMessage(req.channel_id, req.channel_type, req.client_msg_no)
  if msg == nil:
    return 404

  beginTx()
    // 幂等：检查当前 lane 的 last_event_id
    lane = loadOrInitLaneState(req.channel_id, req.channel_type, req.client_msg_no, defaultLane(req.lane_id))
    if lane.last_event_id == req.event_id:
      commitTx()
      return toAppendResp(lane)  // 幂等返回

    if isTerminal(lane.status):
      commitTx()
      return toAppendResp(lane)  // 终态不接受新事件

    // 消息级序号：同 client_msg_no 全局递增（跨 lane 共享）
    seq = allocMsgEventSeq(req.channel_id, req.channel_type, req.client_msg_no)

    evt = {
      channel_id: req.channel_id,
      channel_type: req.channel_type,
      client_msg_no: req.client_msg_no,
      lane_id: defaultLane(req.lane_id),
      event_id: req.event_id,
      event_type: lowercase(req.event_type),
      msg_event_seq: seq,
      payload: req.payload,
      visibility: req.visibility,
      occurred_at: req.occurred_at
    }

    lane = reduceLaneState(lane, evt)
    upsertLaneState(lane)
  commitTx()

  return toAppendResp(evt, lane)
```

### 10.2 `reduceLaneState` 合并函数

```text
function reduceLaneState(state, evt):
  if isTerminal(state.status):
    return state

  switch evt.event_type:
    case "stream.open":
      state.status = "open"
      applyOpenMeta(state, evt.payload)
    case "stream.delta":
      applyDelta(state, evt.payload.kind, evt.payload)
    case "stream.snapshot":
      applySnapshot(state, evt.payload.kind, evt.payload)
    case "stream.close":
      state.status = "closed"
      state.end_reason = evt.payload.end_reason or 0
    case "stream.error":
      state.status = "error"
      state.error = evt.payload.error or ""
    case "stream.cancel":
      state.status = "cancelled"

  state.last_msg_event_seq = evt.msg_event_seq
  return state
```

### 10.3 `POST /channel/messagesync` 读路径

```text
function FillMessageEventMeta(msg):
  lanes = loadLaneStates(msg.client_msg_no) // 不扫全量 event
  meta = {
    has_events: len(lanes) > 0,
    lane_count: len(lanes),
    open_lane_count: count(lanes where status == "open"),
    last_msg_event_seq: max(lanes.last_msg_event_seq),
    lanes: []
  }

  for lane in lanes:
    meta.lanes.append({
      lane_id: lane.lane_id,
      status: lane.status,
      last_msg_event_seq: lane.last_msg_event_seq,
      snapshot: lane.snapshot_payload,
      end_reason: lane.end_reason,
      error: lane.error
    })

  // 兼容旧字段：只映射 main lane
  main = findLane(lanes, "main")
  if main != nil:
    msg.stream_data = toLegacyStreamData(main.snapshot_payload)
    msg.end = toLegacyEnd(main.status)
    msg.end_reason = main.end_reason
    msg.error = main.error
  msg.event_meta = meta
  msg.event_sync_hint = {client_msg_no: msg.client_msg_no, from_msg_event_seq: 0}
  return msg
```

### 10.4 `POST /message/eventsync` 投影视图拉取

说明：返回的是从 `MessageLaneState` 构建的投影视图（每个 lane 至多一条），而非原始事件序列。

```text
function HandleEventSync(req):
  assert req.client_msg_no != ""

  // 从 lane state 构建投影视图
  states = loadLaneStates(req.channel_id, req.channel_type, req.client_msg_no)

  rows = []
  for state in states:
    if req.lane_id != "" and state.lane_id != req.lane_id:
      continue
    if state.last_msg_event_seq <= req.from_msg_event_seq:
      continue
    rows.append(buildProjectedEvent(state))

  sortBy(rows, msg_event_seq)
  more = len(rows) > req.limit
  rows = rows[:req.limit]

  next = req.from_msg_event_seq
  if len(rows) > 0:
    next = rows[-1].msg_event_seq

  return {
    client_msg_no: req.client_msg_no,
    from_msg_event_seq: req.from_msg_event_seq,
    next_msg_event_seq: next,
    more: more ? 1 : 0,
    events: rows
  }
```
