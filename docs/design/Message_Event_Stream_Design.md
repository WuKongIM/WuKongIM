# 消息地基 + 通用事件流设计（Message-Centric Event Stream）

## 1. 设计目标

本设计的核心约束：**消息是地基，所有事件必须关联到某条消息**。

目标：

1. 保持现有 `POST /channel/messagesync` 作为离线主入口，不破坏既有客户端。
2. 支持一条消息下多路并行流（文本流、工具流、状态流等）。
3. 发送事件时仅依赖 `client_msg_no`，不要求客户端传 `message_id`。
4. 事件协议保持通用，不绑定 Agent 语义。

相关细则：

- 合并状态机与离线聚合细节见：[事件合并规则](./Event_Merge_Rules.md)

## 2. 核心模型

### 2.1 Message（地基）

- 使用现有消息模型（`message_id`、`client_msg_no`、`message_seq` 等）。
- `client_msg_no` 是客户端主标识。

### 2.2 MessageEvent（事件）

事件是消息的子资源，唯一性建议：`(channel_id, channel_type, client_msg_no, event_id)`。

建议字段：

- `client_msg_no`：消息关联键（必填）
- `lane_id`：消息内事件轨道ID（可选，默认 `main`）
- `event_id`：事件幂等键（必填）
- `event_type`：通用语义事件类型（必填）
- `payload`：结构化负载（JSON）
- `visibility`：`public` / `private` / `restricted`
- `occurred_at`：事件发生时间（毫秒）
- `msg_event_seq`：服务端分配的消息内递增序号

命名说明：

- `msg_event_seq` 是**消息级序号**，作用域为同一 `client_msg_no`。
- 同一消息下不同 `lane_id` 共用同一序号空间，不按 lane 单独重置。

### 2.3 MessageLaneState（投影）

用于离线快速返回，不要求客户端逐条回放所有 event：

- `client_msg_no`
- `lane_id`
- `status`（`open/closed/error/cancelled`）
- `last_msg_event_seq`
- `snapshot_payload`
- `end_reason`
- `error`

## 3. 通用事件类型

推荐通用事件类型（不绑定 Agent）：

1. `stream.open`
2. `stream.delta`
3. `stream.snapshot`
4. `stream.close`
5. `stream.error`
6. `stream.cancel`

业务类型通过 `payload.kind` 区分，例如：`text/tool/workflow/asr/order`。

## 4. 接口设计

## 4.1 发送事件：`POST /message/event`

说明：客户端只传 `client_msg_no`，不传 `message_id`。

请求示例：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "u1",
  "client_msg_no": "cmn_abc_001",
  "event_id": "evt_0001",
  "event_type": "stream.delta",
  "lane_id": "main",
  "visibility": "public",
  "occurred_at": 1772860800000,
  "payload": {
    "kind": "text",
    "delta": "你好，"
  },
  "headers": {
    "schema": "v1",
    "source": "llm-a"
  }
}
```

服务端处理规则：

1. 通过 `(channel_id, channel_type, client_msg_no)` 查找消息。
2. 若不存在可按策略创建锚点消息（或返回错误，取决于配置）。
3. 追加 `MessageEvent`，分配 `msg_event_seq`。
4. 更新 `MessageLaneState`。

响应示例：

```json
{
  "status": 200,
  "data": {
    "client_msg_no": "cmn_abc_001",
    "lane_id": "main",
    "event_id": "evt_0001",
    "msg_event_seq": 12,
    "stream_status": "open",
    "message_seq": 10241
  }
}
```

## 4.2 离线消息主入口（扩展）：`POST /channel/messagesync`

在原有响应上扩展 `event_meta`，保持兼容。

请求示例：

```json
{
  "login_uid": "u1",
  "channel_id": "u2",
  "channel_type": 1,
  "start_message_seq": 10000,
  "end_message_seq": 0,
  "limit": 100,
  "pull_mode": 1,
  "stream_v2": 1,
  "include_event_meta": 1,
  "event_summary_mode": "basic"
}
```

响应示例（单条消息）：

```json
{
  "message_id": 73918291023123,
  "message_seq": 10241,
  "client_msg_no": "cmn_abc_001",
  "payload": "base-message-payload",
  "end": 0,
  "end_reason": 0,
  "error": "",
  "stream_data": "aggregated-bytes",
  "event_meta": {
    "has_events": true,
    "event_version": 7,
    "last_msg_event_seq": 12,
    "lane_count": 2,
    "open_lane_count": 1,
    "lanes": [
      {
        "lane_id": "main",
        "status": "open",
        "last_msg_event_seq": 12,
        "snapshot": { "text": "你好，今天北京天气..." }
      },
      {
        "lane_id": "tool_weather",
        "status": "closed",
        "last_msg_event_seq": 5,
        "end_reason": 0
      }
    ]
  },
  "event_sync_hint": {
    "client_msg_no": "cmn_abc_001",
    "from_msg_event_seq": 0
  }
}
```

## 4.3 事件离线增量：`POST /message/eventsync`

用于按消息补拉细粒度事件。

游标语义：

- `from_msg_event_seq` / `next_msg_event_seq` 均为消息级游标。
- 当请求带 `lane_id` 过滤时，返回序号可能不连续（因为跨 lane 共用序号空间）。

请求示例：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "client_msg_no": "cmn_abc_001",
  "lane_id": "",
  "from_msg_event_seq": 0,
  "limit": 200,
  "include_private": 0
}
```

响应示例：

```json
{
  "client_msg_no": "cmn_abc_001",
  "from_msg_event_seq": 0,
  "next_msg_event_seq": 200,
  "more": 1,
  "events": [
    {
      "msg_event_seq": 1,
      "event_id": "evt_0001",
      "lane_id": "main",
      "event_type": "stream.open",
      "visibility": "public",
      "occurred_at": 1772860800000,
      "payload": { "kind": "text" }
    }
  ]
}
```

## 5. `lane_id` 的定位

`lane_id` 不用于定位消息，消息定位只看 `client_msg_no`。

`lane_id` 的价值是对一条消息内并行事件流做隔离：

1. `main`：文本输出流
2. `tool_weather`：工具调用流
3. `workflow`：状态推进流

如果业务是单流场景，`lane_id` 可省略，默认 `main`。

## 6. 三个完整例子

## 6.1 文本流（单流）

事件序列：

1. `stream.open` (`lane_id=main`)
2. `stream.delta`（多次）
3. `stream.close`

离线时：

1. `messagesync` 返回消息 + `event_meta.last_msg_event_seq`
2. 客户端如需逐字回放，再调用 `eventsync`

## 6.2 工具流（同消息多流）

同一 `client_msg_no` 下：

1. `stream.open` (`main`)
2. `stream.open` (`tool_weather`)
3. `stream.delta` (`tool_weather`, args)
4. `stream.close` (`tool_weather`, result)
5. `stream.delta` (`main`, 文本总结)
6. `stream.close` (`main`)

## 6.3 业务状态流（非 Agent）

`payload.kind=order`，`lane_id=order_state`：

1. `stream.open`
2. `stream.snapshot`：`paid`
3. `stream.snapshot`：`shipping`
4. `stream.close`：`delivered`

同样可通过 `messagesync + eventsync` 离线恢复。

## 6.4 Agent 事件示例（使用通用事件类型）

下面示例统一使用文档定义的通用事件类型（`stream.open`、`stream.delta`、`stream.snapshot`、`stream.close`、`stream.error`、`stream.cancel`）。

说明：

1. 仍然通过 `client_msg_no` 关联消息。
2. 使用 `lane_id` 区分同一消息内的文本轨道与工具轨道。

### 示例A：Agent 文本流（lane_id=`assistant_text`）

`POST /message/event` 请求示例（文本增量）：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "agent_bot",
  "client_msg_no": "cmn_agent_001",
  "event_id": "evt_txt_002",
  "event_type": "stream.delta",
  "lane_id": "assistant_text",
  "visibility": "public",
  "occurred_at": 1772860800100,
  "payload": {
    "kind": "agent.text",
    "delta": "我先帮你查一下天气。"
  }
}
```

### 示例B：Agent 工具调用流（lane_id=`tool_weather`）

`POST /message/event` 请求示例（工具调用开始）：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "agent_bot",
  "client_msg_no": "cmn_agent_001",
  "event_id": "evt_tool_001",
  "event_type": "stream.open",
  "lane_id": "tool_weather",
  "visibility": "public",
  "occurred_at": 1772860800200,
  "payload": {
    "kind": "agent.tool",
    "tool_name": "weather.search",
    "tool_call_id": "tc_weather_01"
  }
}
```

`POST /message/event` 请求示例（工具参数增量）：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "agent_bot",
  "client_msg_no": "cmn_agent_001",
  "event_id": "evt_tool_002",
  "event_type": "stream.delta",
  "lane_id": "tool_weather",
  "visibility": "public",
  "occurred_at": 1772860800300,
  "payload": {
    "kind": "agent.tool",
    "tool_call_id": "tc_weather_01",
    "args_delta": "{\"city\":\"Beijing\"}"
  }
}
```

`POST /message/event` 请求示例（工具结果）：

```json
{
  "channel_id": "u2",
  "channel_type": 1,
  "from_uid": "agent_bot",
  "client_msg_no": "cmn_agent_001",
  "event_id": "evt_tool_004",
  "event_type": "stream.snapshot",
  "lane_id": "tool_weather",
  "visibility": "public",
  "occurred_at": 1772860800500,
  "payload": {
    "kind": "agent.tool",
    "tool_call_id": "tc_weather_01",
    "result": {
      "temp_c": 26,
      "condition": "Sunny"
    }
  }
}
```

### 示例C：离线 `messagesync` 返回中的 Agent 轨道摘要

```json
{
  "client_msg_no": "cmn_agent_001",
  "event_meta": {
    "has_events": true,
    "last_msg_event_seq": 12,
    "lane_count": 2,
    "open_lane_count": 1,
    "lanes": [
      {
        "lane_id": "assistant_text",
        "status": "open",
        "last_msg_event_seq": 12,
        "snapshot": {
          "text": "北京今天 26°C，晴。"
        }
      },
      {
        "lane_id": "tool_weather",
        "status": "closed",
        "last_msg_event_seq": 8,
        "snapshot": {
          "tool_name": "weather.search",
          "tool_call_id": "tc_weather_01",
          "result": {
            "temp_c": 26,
            "condition": "Sunny"
          }
        }
      }
    ]
  }
}
```

## 7. 幂等、顺序与一致性

1. 消息幂等：`(channel_id, channel_type, client_msg_no)`。
2. 事件幂等：`(client_msg_no, event_id)`。
3. 事件顺序：`msg_event_seq` 在 `client_msg_no` 维度递增。
4. 集群一致性：`MessageEvent` 与 `MessageLaneState` 必须在同一提案链路中落盘。

兼容说明：

- 历史字段中已存在 `stream_id`（旧语义），新设计统一使用 `lane_id` 表示“消息内事件轨道”，避免歧义。

## 8. 迁移建议

1. 第一阶段：新增 `eventappend/eventsync` 与存储，不改现有调用。
2. 第二阶段：`messagesync` 增加 `event_meta`。
3. 第三阶段：新客户端优先读 `event_meta + eventsync`，旧客户端继续使用 `stream_v2` 字段。

## 9. 存储策略说明

出于性能考量，**事件不逐条落盘**。服务端仅持久化 `MessageLaneState`（投影状态），不存储独立的事件行。

- 写路径：每次 `eventappend` 只更新对应 lane 的投影状态（`MessageLaneState`），包括 `msg_event_seq` 递增、snapshot 合并、status 变更。
- 读路径：`eventsync` 返回的是从 `MessageLaneState` 构建的投影视图（每个 lane 至多一条），而非原始事件序列。客户端如需细粒度回放，应在在线时通过实时推送获取。
- `messagesync` 读取 `MessageLaneState` 填充 `event_meta`，不扫描全量事件。

## 10. 实现伪代码入口

关键伪代码已整理到独立合并规则文档，建议实现时按以下顺序落地：

1. `HandleEventAppend` 写路径（幂等 + `msg_event_seq` 分配 + `MessageLaneState` 更新）
2. `reduceLaneState` 合并函数（`stream.open/delta/snapshot/close/error/cancel`）
3. `FillMessageEventMeta` 离线读取路径（`messagesync` 读投影 + 旧字段兼容）
4. `HandleEventSync` 投影视图拉取

参考位置：`docs/design/Event_Merge_Rules.md` 的「10. 关键伪代码（可直接映射实现）」。
DB 层方法与批量写入细节见：`docs/design/Event_DB_Methods_Pseudocode.md`。
