# WuKongIM Stream 消息使用指南

本文档面向接入方开发者，手把手介绍如何使用 WuKongIM 的 **Stream（流式消息）** 功能。
典型场景：AI 大模型逐字回复、实时语音转写、协同编辑等需要"边生成边推送"的业务。

---

## 目录

- [核心概念](#核心概念)
- [完整流程总览](#完整流程总览)
- [第一步：发送锚点消息](#第一步发送锚点消息)
- [第二步：发送增量内容](#第二步发送增量内容)
- [第三步：关闭流](#第三步关闭流)
- [第四步：标记消息完成（可选）](#第四步标记消息完成可选)
- [客户端：实时接收推送](#客户端实时接收推送)
- [客户端：历史消息同步](#客户端历史消息同步)
- [客户端：按需拉取事件明细](#客户端按需拉取事件明细)
- [进阶用法](#进阶用法)
  - [多 Event Key（多通道并行）](#多-event-key多通道并行)
  - [AI Agent 完整示例（思维 + 工具 + 内容）](#ai-agent-完整示例思维--工具--内容-1)
  - [stream.finish 消息级终结](#streamfinish-消息级终结)
  - [错误与取消](#错误与取消)
  - [幂等重试](#幂等重试)
- [事件类型速查表](#事件类型速查表)
- [字段速查表](#字段速查表)
- [FAQ](#faq)

---

## 核心概念

开始之前，先理解几个关键概念：

| 概念 | 说明 |
|------|------|
| **锚点消息** | 一条普通消息，带 `is_stream: 1` 标记。它是流的"容器"，后续所有事件都挂在这条消息上。 |
| **事件 (Event)** | 挂在锚点消息上的一条变更记录，比如"打开流"、"追加一段文字"、"关闭流"等。 |
| **Event Key** | 事件通道名称，默认是 `"main"`。一条消息可以有多个通道并行工作（比如 `"thinking"` 和 `"main"`）。 |
| **Event ID** | 每个事件的唯一标识，用于幂等去重。如果同一个 `event_id` 重复发送，服务端不会重复处理。 |
| **Snapshot** | 服务端自动维护的"快照"。对于文本类型，每次 delta 追加后服务端会把文本拼接好存起来，客户端拉历史时直接拿到完整文本。 |

---

## 完整流程总览

下面这张图展示了一次典型的 AI 回复流程：

```
服务端（你的业务）                    WuKongIM                        客户端
    │                                   │                              │
    │  1. POST /message/send            │                              │
    │      (is_stream: 1)               │                              │
    │ ─────────────────────────────────> │  推送消息给在线客户端         │
    │                                   │ ───────────────────────────> │
    │                                   │                              │
    │  2. POST /message/event     │                              │
    │      (stream.delta) × N 次        │                              │
    │ ─────────────────────────────────> │  推送 delta 事件（逐字）     │
    │                                   │ ───────────────────────────> │
    │                                   │                              │
    │  3. POST /message/event     │                              │
    │      (stream.close)               │                              │
    │ ─────────────────────────────────> │  推送 close 事件             │
    │                                   │ ───────────────────────────> │
    │                                   │                              │
    │  4. POST /message/event     │  (可选)                      │
    │      (stream.finish)              │                              │
    │ ─────────────────────────────────> │  推送 finish 事件            │
    │                                   │ ───────────────────────────> │
```

---

## 第一步：发送锚点消息

调用消息发送接口，创建一条流式消息作为"容器"。

**请求**

```
POST /message/send
Content-Type: application/json
```

```json
{
  "header": {
    "no_persist": 0,
    "red_dot": 1,
    "sync_once": 0
  },
  "from_uid": "ai_bot",
  "channel_id": "user_001",
  "channel_type": 1,
  "client_msg_no": "msg_abc_001",
  "is_stream": 1,
  "payload": "eyJ0eXBlIjoxLCJjb250ZW50IjoiQUkg5q2j5Zyo5oCd6ICD5LitLi4uIn0="
}
```

**关键字段说明**

| 字段 | 说明 |
|------|------|
| `is_stream` | **必须设为 `1`**，告诉 WuKongIM 这条消息后续会有流事件。 |
| `client_msg_no` | 客户端消息唯一编号，后续所有事件都通过它关联到这条消息。**请确保唯一**。 |
| `channel_type` | `1` = 单聊，`2` = 群聊 |
| `payload` | Base64 编码的消息体，客户端收到后解码展示（比如显示"AI 正在思考中..."）。 |

> **注意**：`client_msg_no` 是整个流的关联 ID，后续每一步都需要它，请妥善保存。

---

## 第二步：发送增量内容

锚点消息发送成功后，通过 `stream.delta` 逐步发送内容。首次发送 delta 时，服务端会自动创建缓存会话。可以调用多次。

**请求（第 1 次）**

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "event_id": "evt_002",
  "event_type": "stream.delta",
  "event_key": "main",
  "payload": {
    "kind": "text",
    "delta": "你好"
  }
}
```

**请求（第 2 次）**

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "event_id": "evt_003",
  "event_type": "stream.delta",
  "event_key": "main",
  "payload": {
    "kind": "text",
    "delta": "，我是 AI 助手"
  }
}
```

**请求（第 3 次）**

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "event_id": "evt_004",
  "event_type": "stream.delta",
  "event_key": "main",
  "payload": {
    "kind": "text",
    "delta": "，很高兴为你服务！"
  }
}
```

经过 3 次 delta 后，服务端自动维护的快照内容为：`"你好，我是 AI 助手，很高兴为你服务！"`

**payload 格式说明**

| 字段 | 说明 |
|------|------|
| `kind` | 内容类型。`"text"` 表示文本，服务端会自动拼接 delta 到快照中。其他类型（如 `"tool_call"`）会整体替换快照。 |
| `delta` | 本次追加的文本片段。 |

> **性能提示**：`stream.delta` 走内存缓存，不立即写磁盘，延迟极低。适合高频调用（如逐 token 发送）。

---

## 第三步：关闭流

内容全部发送完毕后，关闭流通道。

**请求**

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "event_id": "evt_005",
  "event_type": "stream.close",
  "event_key": "main",
  "payload": {
    "end_reason": 0
  }
}
```

**响应**

```json
{
  "status": 200,
  "data": {
    "client_msg_no": "msg_abc_001",
    "event_key": "main",
    "event_id": "evt_005",
    "msg_event_seq": 5,
    "stream_status": "closed",
    "channel_id": "user_001",
    "channel_type": 1,
    "from_uid": "ai_bot"
  }
}
```

`stream_status` 返回 `"closed"` 说明通道已关闭。此时：
- 内存中累积的快照会合并落盘持久化
- 客户端拉取历史消息时能拿到完整的快照文本

**payload 说明**

| 字段 | 说明 |
|------|------|
| `end_reason` | 结束原因码，由业务自定义。`0` 通常表示正常结束。 |
| `snapshot` | （可选）如果你想在 close 时传入最终快照覆盖服务端累积的版本，可以加此字段。 |

---

## 第四步：标记消息完成（可选）

如果一条消息有多个 Event Key（例如 `"thinking"` + `"main"`），各自独立关闭后，客户端无法判断"这条消息的所有流是否都结束了"。

`stream.finish` 解决了这个问题——它是一个**消息级别**的终结信号。

**请求**

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "event_id": "evt_006",
  "event_type": "stream.finish"
}
```

> 注意：不需要传 `event_key`，服务端会自动使用保留 key `"__finish__"`。也不需要传 `payload`。

**响应**

```json
{
  "status": 200,
  "data": {
    "client_msg_no": "msg_abc_001",
    "event_key": "__finish__",
    "event_id": "evt_006",
    "msg_event_seq": 6,
    "stream_status": "closed",
    "channel_id": "user_001",
    "channel_type": 1,
    "from_uid": "ai_bot"
  }
}
```

发送 `stream.finish` 后的效果：
- 历史消息同步时 `event_meta.completed` 会变为 `true`
- `__finish__` **不会**出现在 `event_meta.events` 数组中，不影响业务层的事件列表
- 客户端通过 WebSocket 收到 `stream.finish` 推送，可直接标记消息为"已完成"

---

## 客户端：实时接收推送

客户端通过 WebSocket 连接 WuKongIM 后，会实时收到事件推送。每个推送的 `event.dataJson` 是一个 JSON 对象：

```json
{
  "client_msg_no": "msg_abc_001",
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "message_id": 123456789,
  "event_key": "main",
  "msg_event_seq": 3,
  "stream_status": "open",
  "visibility": "public",
  "payload": {
    "kind": "text",
    "delta": "，我是 AI 助手"
  }
}
```

**客户端处理示例（JavaScript）**

```javascript
sdk.eventManager.addEventListener((event) => {
  const data = event.dataJson
  const clientMsgNo = data.client_msg_no

  // 找到对应的消息
  const message = findMessageByClientMsgNo(clientMsgNo)
  if (!message) return

  switch (event.type) {
    case "stream.delta":
      // 增量文本追加
      message.streaming = true
      if (data.payload?.kind === "text" && data.payload?.delta) {
        message.streamText = (message.streamText || "") + data.payload.delta
        // 重新渲染消息内容（如 Markdown 转 HTML）
        message.displayText = renderMarkdown(message.streamText)
      }
      break

    case "stream.close":
      // 流关闭，可能携带最终快照
      message.streaming = false
      if (data.payload?.snapshot?.kind === "text") {
        message.streamText = data.payload.snapshot.text
        message.displayText = renderMarkdown(message.streamText)
      }
      break

    case "stream.error":
      // 发生错误
      message.streaming = false
      message.error = data.payload?.error || "未知错误"
      break

    case "stream.cancel":
      // 流被取消
      message.streaming = false
      break

    case "stream.finish":
      // 整条消息的所有流全部完成
      message.completed = true
      break
  }
})
```

---

## 客户端：历史消息同步

客户端从服务端拉取历史消息时，可以通过参数获取事件元信息，直接拿到累积的快照文本。

**请求**

```
POST /channel/messagesync
Content-Type: application/json
```

```json
{
  "login_uid": "user_001",
  "channel_id": "ai_bot",
  "channel_type": 1,
  "start_message_seq": 0,
  "end_message_seq": 0,
  "limit": 50,
  "pull_mode": 1,
  "include_event_meta": 1,
  "event_summary_mode": "full"
}
```

| 字段 | 说明 |
|------|------|
| `include_event_meta` | 设为 `1`，响应中会包含 `event_meta` 字段。 |
| `event_summary_mode` | `"full"` = 包含快照内容，`"basic"` = 只有状态不含快照（节省流量）。 |

**响应中的流式消息**

```json
{
  "messages": [
    {
      "message_id": 123456789,
      "message_idstr": "123456789",
      "client_msg_no": "msg_abc_001",
      "from_uid": "ai_bot",
      "setting": 32,
      "payload": "eyJ0eXBlIjoxLCJjb250ZW50IjoiQUkg5q2j5Zyo5oCd6ICD5LitLi4uIn0=",
      "event_meta": {
        "has_events": true,
        "completed": true,
        "event_version": 6,
        "last_msg_event_seq": 6,
        "event_count": 1,
        "open_event_count": 0,
        "events": [
          {
            "event_key": "main",
            "status": "closed",
            "last_msg_event_seq": 5,
            "snapshot": {
              "kind": "text",
              "text": "你好，我是 AI 助手，很高兴为你服务！"
            },
            "end_reason": 0
          }
        ]
      },
      "event_sync_hint": {
        "client_msg_no": "msg_abc_001",
        "from_msg_event_seq": 0
      }
    }
  ]
}
```

**`event_meta` 字段说明**

| 字段 | 说明 |
|------|------|
| `has_events` | 是否有事件数据 |
| `completed` | 是否已发送 `stream.finish`（消息级完成标记） |
| `event_count` | 事件通道数量（不含 `__finish__`） |
| `open_event_count` | 仍处于 open 状态的通道数量 |
| `events` | 各通道的状态摘要数组 |
| `events[].snapshot` | 累积的快照内容（仅 `event_summary_mode: "full"` 时返回） |

**客户端渲染流式消息的逻辑**

```javascript
function renderStreamMessage(message) {
  const meta = message.event_meta
  if (!meta || !meta.has_events) return

  // 从 event_meta 中找到 main 通道的快照
  for (const event of meta.events) {
    if (event.event_key === "main" && event.snapshot?.kind === "text") {
      message.displayText = renderMarkdown(event.snapshot.text)
      break
    }
  }

  // 判断消息是否还在流式输出中
  message.streaming = meta.open_event_count > 0
  message.completed = meta.completed
}
```

---

## 客户端：按需拉取事件明细

如果需要查看某条消息的事件历史（调试用），可以调用事件同步接口。

**请求**

```
POST /message/eventsync
Content-Type: application/json
```

```json
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_bot",
  "client_msg_no": "msg_abc_001",
  "from_msg_event_seq": 0,
  "limit": 100
}
```

| 字段 | 说明 |
|------|------|
| `from_msg_event_seq` | 从哪个序号开始拉取（用于分页，`0` 表示从头开始）。 |
| `event_key` | （可选）只拉取指定通道的事件。不传则返回所有通道。 |
| `limit` | 最多返回条数，默认 200，最大 2000。 |

**响应**

```json
{
  "status": 200,
  "data": {
    "client_msg_no": "msg_abc_001",
    "from_msg_event_seq": 0,
    "next_msg_event_seq": 5,
    "more": 0,
    "events": [
      {
        "msg_event_seq": 5,
        "event_id": "evt_005",
        "event_key": "main",
        "event_type": "stream.snapshot",
        "payload": {
          "kind": "text",
          "text": "你好，我是 AI 助手，很高兴为你服务！"
        }
      }
    ]
  }
}
```

> 注意：delta 事件在落盘后会被合并为 snapshot 投影，所以拉取到的是最终快照而非每条 delta。

---

## 进阶用法

### 多 Event Key（多通道并行）

一条消息可以同时有多个事件通道。例如 AI 先"思考"再"回答"：

```
时间线：
  thinking: delta("正在分析...") → delta("思考完成") → close
  main:     delta("你好") → delta("，答案是42") → close
  finish:   stream.finish
```

服务端调用示例：

```json
// 1. thinking 增量
{ "event_type": "stream.delta", "event_key": "thinking", "event_id": "t1",
  "payload": {"kind": "text", "delta": "正在分析..."}, ... }

// 2. 关闭 thinking
{ "event_type": "stream.close", "event_key": "thinking", "event_id": "t2", ... }

// 3. main 增量
{ "event_type": "stream.delta", "event_key": "main", "event_id": "m1",
  "payload": {"kind": "text", "delta": "答案是42"}, ... }

// 4. 关闭 main
{ "event_type": "stream.close", "event_key": "main", "event_id": "m2", ... }

// 5. 标记整条消息完成
{ "event_type": "stream.finish", "event_id": "f1", ... }
```

历史同步时，`event_meta.events` 会包含两个条目：

```json
{
  "events": [
    { "event_key": "thinking", "status": "closed", "snapshot": {"kind":"text","text":"正在分析..."} },
    { "event_key": "main",     "status": "closed", "snapshot": {"kind":"text","text":"答案是42"} }
  ],
  "completed": true,
  "event_count": 2,
  "open_event_count": 0
}
```

### AI Agent 完整示例（思维 + 工具 + 内容）

下面演示一个典型的 AI Agent 场景：用户提问"北京今天天气怎么样？"，Agent 经历 **思考 → 调用工具 → 生成回答** 三个阶段，每个阶段使用不同的 Event Key。

```
时间线：
  thinking:        delta × N → close        （思考过程）
  tool:call_001:   delta × N → close        （工具调用：查天气）
  main:            delta × N → close        （最终回答）
  finish:          stream.finish             （消息完成）
```

> **Event Key 命名建议**：工具通道使用 `tool:<tool_call_id>` 格式，每次工具调用一个独立通道。如果需要调用多个工具，每个工具各自一个通道（如 `tool:call_001`、`tool:call_002`），可并行执行、独立关闭。

#### 第 1 步：发送锚点消息

```json
POST /message/send

{
  "header": { "no_persist": 0, "red_dot": 1, "sync_once": 0 },
  "from_uid": "ai_agent",
  "channel_id": "user_001",
  "channel_type": 1,
  "client_msg_no": "agent_msg_001",
  "is_stream": 1,
  "payload": "eyJ0eXBlIjoxLCJjb250ZW50IjoiQUkg5q2j5Zyo5aSE55CG5Lit4oCmIn0="
}
```

#### 第 2 步：思考阶段（event_key: "thinking"）

Agent 分析用户意图，将思考过程通过 `thinking` 通道流式输出。

```json
// 思考 delta 1
POST /message/event
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_agent",
  "client_msg_no": "agent_msg_001",
  "event_id": "think_001",
  "event_type": "stream.delta",
  "event_key": "thinking",
  "payload": { "kind": "text", "delta": "用户在问天气信息，" }
}

// 思考 delta 2
{
  ...
  "event_id": "think_002",
  "event_key": "thinking",
  "payload": { "kind": "text", "delta": "我需要调用天气查询工具来获取北京的实时天气。" }
}

// 关闭思考通道
{
  ...
  "event_id": "think_003",
  "event_type": "stream.close",
  "event_key": "thinking",
  "payload": { "end_reason": 0 }
}
```

此时 `thinking` 通道快照为：`"用户在问天气信息，我需要调用天气查询工具来获取北京的实时天气。"`

#### 第 3 步：工具调用阶段（event_key: "tool:call_001"）

Agent 调用外部工具并将过程通过 `tool:call_001` 通道输出。工具调用使用 `kind: "tool_call"` 格式，服务端不做文本拼接，而是整体替换快照。

```json
// 发起调用
POST /message/event
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_agent",
  "client_msg_no": "agent_msg_001",
  "event_id": "tool_001_calling",
  "event_type": "stream.delta",
  "event_key": "tool:call_001",
  "payload": {
    "kind": "tool_call",
    "tool_name": "get_weather",
    "tool_call_id": "call_001",
    "arguments": { "city": "北京" },
    "status": "calling"
  }
}

// 返回结果
{
  ...
  "event_id": "tool_001_result",
  "event_key": "tool:call_001",
  "payload": {
    "kind": "tool_call",
    "tool_name": "get_weather",
    "tool_call_id": "call_001",
    "arguments": { "city": "北京" },
    "status": "completed",
    "result": { "temperature": "22°C", "weather": "晴", "humidity": "45%" }
  }
}

// 关闭工具通道
{
  ...
  "event_id": "tool_001_close",
  "event_type": "stream.close",
  "event_key": "tool:call_001",
  "payload": { "end_reason": 0 }
}
```

> `kind: "tool_call"` 不是文本类型，服务端会用最新的 payload 整体替换快照，不做拼接。

#### 第 4 步：生成回答（event_key: "main"）

Agent 根据工具结果生成最终回答，通过 `main` 通道逐字输出。

```json
// 回答 delta 1
POST /message/event
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_agent",
  "client_msg_no": "agent_msg_001",
  "event_id": "main_001",
  "event_type": "stream.delta",
  "event_key": "main",
  "payload": { "kind": "text", "delta": "北京今天天气晴朗，" }
}

// 回答 delta 2
{
  ...
  "event_id": "main_002",
  "event_key": "main",
  "payload": { "kind": "text", "delta": "气温 22°C，湿度 45%，" }
}

// 回答 delta 3
{
  ...
  "event_id": "main_003",
  "event_key": "main",
  "payload": { "kind": "text", "delta": "非常适合户外活动。" }
}

// 关闭回答通道
{
  ...
  "event_id": "main_004",
  "event_type": "stream.close",
  "event_key": "main",
  "payload": { "end_reason": 0 }
}
```

#### 第 5 步：标记消息完成

所有通道关闭后，发送 `stream.finish` 标记整条消息完成。

```json
POST /message/event
{
  "channel_id": "user_001",
  "channel_type": 1,
  "from_uid": "ai_agent",
  "client_msg_no": "agent_msg_001",
  "event_id": "finish_001",
  "event_type": "stream.finish"
}
```

#### 历史消息同步结果

客户端拉取历史消息时，`event_meta` 包含三个通道的快照：

```json
{
  "event_meta": {
    "has_events": true,
    "completed": true,
    "event_count": 3,
    "open_event_count": 0,
    "events": [
      {
        "event_key": "thinking",
        "status": "closed",
        "snapshot": {
          "kind": "text",
          "text": "用户在问天气信息，我需要调用天气查询工具来获取北京的实时天气。"
        }
      },
      {
        "event_key": "tool:call_001",
        "status": "closed",
        "snapshot": {
          "kind": "tool_call",
          "tool_name": "get_weather",
          "tool_call_id": "call_001",
          "arguments": { "city": "北京" },
          "status": "completed",
          "result": { "temperature": "22°C", "weather": "晴", "humidity": "45%" }
        }
      },
      {
        "event_key": "main",
        "status": "closed",
        "snapshot": {
          "kind": "text",
          "text": "北京今天天气晴朗，气温 22°C，湿度 45%，非常适合户外活动。"
        }
      }
    ]
  }
}
```

#### 客户端渲染建议

```javascript
function renderAgentMessage(message) {
  const meta = message.event_meta
  if (!meta || !meta.has_events) return

  message.toolCalls = []

  for (const event of meta.events) {
    // 思考通道
    if (event.event_key === "thinking") {
      if (event.snapshot?.kind === "text") {
        message.thinkingText = event.snapshot.text
      }
      continue
    }

    // 工具通道（以 "tool:" 开头，支持多个工具）
    if (event.event_key.startsWith("tool:")) {
      if (event.snapshot?.kind === "tool_call") {
        message.toolCalls.push({
          callId: event.snapshot.tool_call_id,
          name: event.snapshot.tool_name,
          arguments: event.snapshot.arguments,
          status: event.snapshot.status,
          result: event.snapshot.result,
        })
      }
      continue
    }

    // 主内容通道
    if (event.event_key === "main") {
      if (event.snapshot?.kind === "text") {
        message.displayText = renderMarkdown(event.snapshot.text)
      }
      continue
    }
  }

  message.streaming = meta.open_event_count > 0
  message.completed = meta.completed
}
```

### stream.finish 消息级终结

**什么时候用？**

- 消息只有一个 `main` 通道 → `stream.close` 就够了，`stream.finish` 可选
- 消息有多个通道（如 `thinking` + `main`）→ 各通道 close 后，**建议发送 `stream.finish`**，让客户端确定性地知道"整条消息完了"

**为什么需要？**

在多通道场景下，`thinking` close 之后到 `main` 首次 delta 之前存在一个间隙。这个间隙中所有通道的 `open_event_count == 0`，客户端可能误以为消息已完成。`stream.finish` 提供了一个明确的终结信号。

### 错误与取消

**发生错误时：**

```json
{
  "event_type": "stream.error",
  "event_key": "main",
  "event_id": "evt_err",
  "client_msg_no": "msg_abc_001",
  "channel_id": "user_001",
  "channel_type": 1,
  "payload": {
    "error": "AI 服务超时"
  }
}
```

响应中 `stream_status` 为 `"error"`。

**主动取消时：**

```json
{
  "event_type": "stream.cancel",
  "event_key": "main",
  "event_id": "evt_cancel",
  "client_msg_no": "msg_abc_001",
  "channel_id": "user_001",
  "channel_type": 1
}
```

响应中 `stream_status` 为 `"cancelled"`。

> close / error / cancel 都是终态，发送后该通道不再接受新事件。

### 幂等重试

每个事件都有唯一的 `event_id`。如果网络超时导致不确定是否发送成功，可以用**相同的 `event_id`** 重新调用，服务端会返回相同的结果而不会重复处理。

```
第 1 次调用: event_id="evt_002" → 超时
第 2 次调用: event_id="evt_002" → 返回第 1 次的结果（幂等）
```

---

## 事件类型速查表

| event_type | 说明 | stream_status | 是否终态 | 是否落盘 |
|------------|------|---------------|----------|----------|
| `stream.delta` | 追加增量内容（首次调用自动创建会话） | `open` | 否 | 否（内存） |
| `stream.close` | 正常关闭通道 | `closed` | 是 | 是（Raft） |
| `stream.error` | 错误关闭通道 | `error` | 是 | 是（Raft） |
| `stream.cancel` | 取消关闭通道 | `cancelled` | 是 | 是（Raft） |
| `stream.finish` | 标记消息完成 | `closed` | 是 | 是（Raft） |

---

## 字段速查表

### event 请求字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `channel_id` | string | 是 | 频道 ID（单聊填对方 UID） |
| `channel_type` | uint8 | 是 | `1`=单聊, `2`=群聊 |
| `from_uid` | string | 否 | 发送者 UID，不填则自动从缓存填充（若 send 时已缓存），仍为空则使用系统 UID |
| `client_msg_no` | string | 是 | 锚点消息的唯一编号 |
| `event_id` | string | 是 | 事件唯一 ID（用于幂等） |
| `event_type` | string | 是 | 事件类型（见上表） |
| `event_key` | string | 否 | 事件通道名，默认 `"main"` |
| `visibility` | string | 否 | `"public"` / `"private"` / `"restricted"` |
| `payload` | object | 否 | 事件负载（格式取决于 event_type） |
| `headers` | object | 否 | 自定义键值对 |

### event 响应字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `client_msg_no` | string | 锚点消息编号 |
| `event_key` | string | 事件通道名 |
| `event_id` | string | 事件 ID |
| `msg_event_seq` | uint64 | 事件序列号 |
| `stream_status` | string | 当前通道状态 |
| `channel_id` | string | 频道 ID |
| `channel_type` | uint8 | 频道类型 |
| `from_uid` | string | 发送者 UID |

---

## FAQ

**Q: `stream.delta` 的 payload 一定要是 `{"kind":"text","delta":"..."}` 吗？**

不一定。`kind: "text"` 时服务端会自动拼接文本快照。其他 kind（如 `"tool_call"`）服务端会用整个 payload 替换快照，不做拼接。

**Q: 流的完整调用顺序是什么？**

`send → delta × N → close/error/cancel`。首次 `stream.delta` 会自动创建缓存会话，无需额外的"打开"步骤。

**Q: `stream.close` 的 payload 里可以传最终快照吗？**

可以。在 payload 中加入 `"snapshot": {...}` 字段，服务端会用它覆盖自动累积的快照。

**Q: 单通道场景需要发 `stream.finish` 吗？**

不强制。只有一个 `main` 通道时，`stream.close` 后 `open_event_count` 就是 0，客户端可以直接判断完成。但发 `stream.finish` 可以让 `event_meta.completed` 显式为 `true`，语义更明确。

**Q: 多次发送相同 `event_id` 会怎样？**

服务端保证幂等，返回首次处理的结果，不会重复追加。

**Q: `event_type` 大小写敏感吗？**

不敏感。`"STREAM.DELTA"`、`"Stream.Delta"`、`"stream.delta"` 效果相同。

**Q: event 可以不传 `from_uid` 吗？**

可以。当 `/message/send` 发送 `is_stream: 1` 的锚点消息时，服务端会将 `from_uid` 和 `message_id` 缓存到本地内存。后续 `event` 如果不传这两个字段，会自动从缓存填充。注意：缓存是节点本地的，如果 `send` 和 `event` 请求落在不同节点且无缓存，`from_uid` 为空时会降级使用系统 UID。`channel_id` 和 `channel_type` 仍然必传。

**Q: 客户端离线后重新上线，怎么拿到流式消息的完整内容？**

调用 `/channel/messagesync` 并传 `include_event_meta: 1` + `event_summary_mode: "full"`，响应中的 `event_meta.events[].snapshot` 就是完整快照，无需重放所有 delta��
