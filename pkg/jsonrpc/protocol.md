# WuKongIM JSON-RPC 协议文档

## 概述

本文档描述了 WuKongIM 使用 JSON-RPC 2.0 规范进行通信的协议格式。所有请求、响应和通知都遵循 JSON-RPC 2.0 标准结构。

*   `jsonrpc`: **可选** 字符串，固定为 "2.0"。如果省略，服务器应假定其为 "2.0"。
*   `method`: 请求或通知的方法名。
*   `params`: 请求或通知的参数，通常是一个对象。
*   `id`: 请求的唯一标识符（字符串类型）。响应必须包含与请求相同的 id。通知没有 id。
*   `result`: 成功响应的结果数据。
*   `error`: 错误响应的错误对象。

## 注意事项

1. 建立Websocket连接后，需要在2秒之内进行认证(`connect`),超过2秒或者认证失败的连接将断开

2. 发送错误的数据格式或系统不支持的`method`方法，系统都将断开连接

## 通用组件

以下是一些在多个消息类型中复用的组件定义：

### ErrorObject

当请求处理失败时，响应中会包含此对象。

| 字段      | 类型    | 必填 | 描述           |
| :-------- | :------ | :--- | :------------- |
| `code`    | integer | 是   | 错误码         |
| `message` | string  | 是   | 错误描述       |
| `data`    | any     | 否   | 附加错误数据   |

### Header

可选的消息头信息。

| 字段        | 类型    | 必填 | 描述                 |
| :---------- | :------ | :--- | :------------------- |
| `noPersist` | boolean | 否   | 消息是否不存储       |
| `redDot`    | boolean | 否   | 是否显示红点         |
| `syncOnce`  | boolean | 否   | 是否只被同步一次     |
| `dup`       | boolean | 否   | 是否是重发的消息     |

### SettingFlags

消息设置标记位。

| 字段      | 类型    | 必填 | 描述               |
| :-------- | :------ | :--- | :----------------- |
| `receipt` | boolean | 否   | 消息已读回执       |
| `stream`  | boolean | 否   | 是否为流式消息     |
| `topic`   | boolean | 否   | 是否包含 Topic     |

## 消息类型详解

### 1. 连接 (Connect)

#### Connect Request (`connect`)

客户端发起的第一个请求，用于建立连接和认证。

**参数 (`params`)**

| 字段            | 类型    | 必填 | 描述                       |
| :-------------- | :------ | :--- | :------------------------- |
| `uid`           | string  | 是   | 用户ID                     |
| `token`         | string  | 是   | 认证Token                  |
| `header`        | Header  | 否   | 消息头                     |
| `version`       | integer | 否   | 客户端协议版本             |
| `clientKey`     | string  | 否   | 客户端公钥                 |
| `deviceId`      | string  | 否   | 设备ID                     |
| `deviceFlag`    | integer | 否   | 设备标识 (1:APP, 2:WEB...) |
| `clientTimestamp`| integer | 否   | 客户端13位毫秒时间戳       |

**最小示例**

```json
{
  "method": "connect",
  "params": {
    "uid": "testUser",
    "token": "testToken"
  },
  "id": "req-conn-1"
}
```

#### Connect Response

服务器对 `connect` 请求的响应。

**成功结果 (`result`)**

| 字段          | 类型    | 必填 | 描述                           |
| :------------ | :------ | :--- | :----------------------------- |
| `serverKey`   | string  | 是   | 服务端的DH公钥                 |
| `salt`        | string  | 是   | 加密盐值                       |
| `timeDiff`    | integer | 是   | 客户端与服务器时间差(毫秒)     |
| `reasonCode`  | integer | 是   | 原因码 (成功时通常为0)         |
| `header`      | Header  | 否   | 消息头                         |
| `serverVersion`| integer | 否   | 服务端版本                     |
| `nodeId`      | integer | 否   | 连接的节点ID (协议版本 >= 4)   |

**错误结果 (`error`)**: 参考 `ErrorObject`。

**最小成功示例**

```json
{
  "result": {
    "serverKey": "serverPublicKey",
    "salt": "randomSalt",
    "timeDiff": -15,
    "reasonCode": 0
  },
  "id": "req-conn-1"
}
```

**最小错误示例**

```json
{
  "error": {
    "code": 1001,
    "message": "Authentication Failed"
  },
  "id": "req-conn-1"
}
```

### 2. 发送消息 (Send)

#### Send Request (`send`)

客户端发送消息到指定频道。

**参数 (`params`)**

| 字段          | 类型         | 必填 | 描述                             |
| :------------ | :----------- | :--- | :------------------------------- |
| `clientMsgNo` | string       | 是   | 客户端消息唯一编号(UUID)         |
| `channelId`   | string       | 是   | 频道ID                           |
| `channelType` | integer      | 是   | 频道类型 (1:个人, 2:群组)        |
| `payload`     | object       | 是   | 消息内容 (业务自定义JSON对象)    |
| `header`      | Header       | 否   | 消息头                           |
| `setting`     | SettingFlags | 否   | 消息设置                         |
| `msgKey`      | string       | 否   | 消息验证Key                      |
| `expire`      | integer      | 否   | 消息过期时间(秒), 0表示不过期    |
| `streamNo`    | string       | 否   | 流编号 (如果 setting.stream 为 true) |
| `topic`       | string       | 否   | 消息 Topic (如果 setting.topic 为 true) |

**最小示例**

```json
{
  "method": "send",
  "params": {
    "clientMsgNo": "uuid-12345",
    "channelId": "targetUser",
    "channelType": 1,
    "payload": {"content": "Hello!","type":1}
  },
  "id": "req-send-1"
}
```

#### Send Response

服务器对 `send` 请求的响应，表示消息已收到并分配了服务端 ID。

**成功结果 (`result`)**

| 字段          | 类型    | 必填 | 描述                       |
| :------------ | :------ | :--- | :------------------------- |
| `messageId`   | string  | 是   | 服务端消息ID               |
| `messageSeq`  | integer | 是   | 服务端消息序列号           |
| `reasonCode`  | integer | 是   | 原因码 (成功时通常为0)     |
| `header`      | Header  | 否   | 消息头                     |

**错误结果 (`error`)**: 参考 `ErrorObject`。

**最小成功示例**

```json
{
  "result": {
    "messageId": "serverMsgId1",
    "messageSeq": 1001,
    "reasonCode": 0
  },
  "id": "req-send-1"
}
```

### 3. 收到消息 (Recv)

#### Recv Notification (`recv`)

服务器推送消息给客户端。

**参数 (`params`)**

| 字段          | 类型         | 必填 | 描述                                   |
| :------------ | :----------- | :--- | :------------------------------------- |
| `messageId`   | string       | 是   | 服务端消息ID                           |
| `messageSeq`  | integer      | 是   | 服务端消息序列号                       |
| `timestamp`   | integer      | 是   | 服务端消息时间戳(秒)                   |
| `channelId`   | string       | 是   | 频道ID                                 |
| `channelType` | integer      | 是   | 频道类型                               |
| `fromUid`     | string       | 是   | 发送者UID                              |
| `payload`     | object       | 是   | 消息内容 (业务自定义JSON对象)          |
| `header`      | Header       | 否   | 消息头                                 |
| `setting`     | SettingFlags | 否   | 消息设置                               |
| `msgKey`      | string       | 否   | 消息验证Key                            |
| `expire`      | integer      | 否   | 消息过期时间(秒) (协议版本 >= 3)       |
| `clientMsgNo` | string       | 否   | 客户端消息唯一编号 (用于去重)          |
| `streamNo`    | string       | 否   | 流编号 (协议版本 >= 2)                 |
| `streamId`    | string       | 否   | 流序列号 (协议版本 >= 2)               |
| `streamFlag`  | integer      | 否   | 流标记 (0:Start, 1:Ing, 2:End)         |
| `topic`       | string       | 否   | 消息 Topic (如果 setting.topic 为 true) |

**最小示例**

```json
{
  "method": "recv",
  "params": {
    "messageId": "serverMsgId2",
    "messageSeq": 50,
    "timestamp": 1678886400,
    "channelId": "senderUser",
    "channelType": 1,
    "fromUid": "senderUser",
    "payload": {"content": "How are you?","type":1}
  }
}
```

### 4. 收到消息确认 (RecvAck)

#### RecvAck Request (`recvack`)

客户端确认收到某条消息。

**参数 (`params`)**

| 字段         | 类型    | 必填 | 描述                     |
| :----------- | :------ | :--- | :----------------------- |
| `messageId`  | string  | 是   | 要确认的服务端消息ID     |
| `messageSeq` | integer | 是   | 要确认的服务端消息序列号 |
| `header`     | Header  | 否   | 消息头                   |

**最小示例**

```json
{
  "method": "recvack",
  "params": {
    "messageId": "serverMsgId2",
    "messageSeq": 50
  },
  "id": "req-ack-1"
}
```

*(注：`recvack` 通常没有特定的响应体，如果需要响应，服务器可能会返回一个空的成功 `result` 或错误)*

### 5. 订阅频道 (Subscribe)(暂不支持)

#### Subscribe Request (`subscribe`)

客户端请求订阅指定频道的消息。

**参数 (`params`)**

| 字段          | 类型         | 必填 | 描述                       |
| :------------ | :----------- | :--- | :------------------------- |
| `subNo`       | string       | 是   | 订阅请求编号 (客户端生成)  |
| `channelId`   | string       | 是   | 要订阅的频道ID             |
| `channelType` | integer      | 是   | 频道类型                   |
| `header`      | Header       | 否   | 消息头                     |
| `setting`     | SettingFlags | 否   | 消息设置                   |
| `param`       | string       | 否   | 订阅参数 (可选)            |

**最小示例**

```json
{
  "method": "subscribe",
  "params": {
    "subNo": "sub-req-1",
    "channelId": "group123",
    "channelType": 2
  },
  "id": "req-sub-1"
}
```

### 6. 取消订阅频道 (Unsubscribe)（暂不支持）

#### Unsubscribe Request (`unsubscribe`)

客户端请求取消订阅指定频道。

**参数 (`params`)**

| 字段          | 类型         | 必填 | 描述                           |
| :------------ | :----------- | :--- | :----------------------------- |
| `subNo`       | string       | 是   | 取消订阅请求编号 (客户端生成)  |
| `channelId`   | string       | 是   | 要取消订阅的频道ID             |
| `channelType` | integer      | 是   | 频道类型                       |
| `header`      | Header       | 否   | 消息头                         |
| `setting`     | SettingFlags | 否   | 消息设置                       |

**最小示例**

```json
{
  "method": "unsubscribe",
  "params": {
    "subNo": "unsub-req-1",
    "channelId": "group123",
    "channelType": 2
  },
  "id": "req-unsub-1"
}
```

#### Subscription Response

服务器对 `subscribe` 或 `unsubscribe` 请求的响应。

**成功结果 (`result`)**

| 字段          | 类型    | 必填 | 描述                           |
| :------------ | :------ | :--- | :----------------------------- |
| `subNo`       | string  | 是   | 对应的请求编号                 |
| `channelId`   | string  | 是   | 对应的频道ID                   |
| `channelType` | integer | 是   | 对应的频道类型                 |
| `action`      | integer | 是   | 动作 (0: Subscribe, 1: Unsubscribe) |
| `reasonCode`  | integer | 是   | 原因码 (成功时通常为0)         |
| `header`      | Header  | 否   | 消息头                         |

**错误结果 (`error`)**: 参考 `ErrorObject`。

**最小成功示例 (Subscribe)**

```json
{
  "result": {
    "subNo": "sub-req-1",
    "channelId": "group123",
    "channelType": 2,
    "action": 0,
    "reasonCode": 0
  },
  "id": "req-sub-1"
}
```

### 7. 心跳 (Ping/Pong)

#### Ping Request (`ping`)

客户端发送心跳以保持连接。

**参数 (`params`)**: 通常为 `null` 或空对象 `{}`。

**最小示例**

```json
{
  "method": "ping",
  "id": "req-ping-1"
}
```

#### Pong Response

服务器对 `ping` 请求的响应。

**成功结果 (`result`)**: 通常为 `null` 或空对象 `{}`。
**错误结果 (`error`)**: 参考 `ErrorObject`。

**最小成功示例**

```json
{
  "method": "pong"
}
```

### 8. 断开连接 (Disconnect)(暂不支持)

#### Disconnect Request (`disconnect`)

客户端主动通知服务器断开连接。

**参数 (`params`)**

| 字段         | 类型    | 必填 | 描述                   |
| :----------- | :------ | :--- | :--------------------- |
| `reasonCode` | integer | 是   | 原因码                 |
| `header`     | Header  | 否   | 消息头                 |
| `reason`     | string  | 否   | 断开原因描述 (可选)    |

**最小示例**

```json
{
  "method": "disconnect",
  "params": {
    "reasonCode": 0
  },
  "id": "req-disc-1"
}
```

*(注：`disconnect` 请求通常不需要服务器响应)*

#### Disconnect Notification (`disconnect`)

服务器通知客户端连接已断开（例如，被踢出）。

**参数 (`params`)**

| 字段         | 类型    | 必填 | 描述                   |
| :----------- | :------ | :--- | :--------------------- |
| `reasonCode` | integer | 是   | 原因码                 |
| `header`     | Header  | 否   | 消息头                 |
| `reason`     | string  | 否   | 断开原因描述 (可选)    |

**最小示例**

```json
{
  "method": "disconnect",
  "params": {
    "reasonCode": 401,
    "reason": "Kicked by another device"
  }
}
``` 