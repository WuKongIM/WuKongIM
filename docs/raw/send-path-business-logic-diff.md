# 发送链路业务逻辑差异分析

## 背景

本文对比当前项目发送链路与旧版 `learn_project/WuKongIM` 的业务逻辑差异，重点关注发送前权限、频道状态、黑白名单、订阅者、持久化、投递和扩展钩子等语义。

当前项目的发送链路已经重构为“薄入口 + message usecase + channel log + 异步投递”的结构；旧版则在发送事件流中内置了大量业务规则。两者不是等价迁移关系，当前链路在权限和兼容语义上仍有明显缺口。

## 当前发送链路概览

### 网关入口

当前网关处理 `SendPacket` 时主要做：

1. 必要时解密 send packet。
2. 从 session 读取发送者 UID。
3. 将原始频道 ID、频道类型和 header 标志映射到 `SendCommand`。
4. 调用 `message.App.Send`。
5. 写回 sendack。

关键代码：

- `internal/access/gateway/frame_router.go:29`
- `internal/access/gateway/mapper.go:10`
- `internal/access/gateway/error_map.go:12`

网关入口目前不承载频道业务权限。
个人频道规范化、cmd 后缀剥离和发送权限判断统一在 `message.App.Send` 中完成，避免入口提前拒绝已派生的 cmd 个人频道。

### HTTP 入口

当前 `/message/send` 仅支持简单字段：

- `from_uid` / `sender_uid`
- `channel_id`
- `channel_type`
- `client_msg_no`
- base64 编码的 `payload`

关键代码：

- `internal/access/api/message_send.go:14`
- `internal/access/api/routes.go:66`

相比旧版，当前 HTTP send 已恢复 `header.no_persist` / `header.sync_once` 和 request-scoped `subscribers` 的核心兼容语义，但仍不支持 `expire`、`sendbatch`、流消息、CMD 会话 / 离线同步等旧接口语义。

P2c 状态（2026-05-11）：当前 `/message/send` 已恢复 request-scoped `subscribers` 的核心语义。带 `subscribers` 的请求要求 `sync_once=1`、拒绝非空 `channel_id`，并忽略 `channel_type`，由 message usecase 派生内部 temp `____cmd` channel。

### message usecase

当前 `message.App.Send` 负责发送业务边界：

- `FromUID` 不能为空。
- 只支持个人频道和群频道。
- 先剥离可选的 cmd 后缀，再对个人频道做规范化。
- 在原始频道上检查发送权限，包括发送者 `SendBan`、群 `Ban` / `Disband`、群黑白名单和订阅者资格、个人接收方黑名单。
- `NoPersist` 在权限通过后直接返回成功，不要求 channel cluster。
- `SyncOnce` 或已派生 cmd 输入会把 durable append 目标切换到原频道派生的 `____cmd`。
- request-scoped `RequestSubscribers` 会先规范化和去重；持久化路径写入派生 temp cmd channel，并把精确订阅者快照作为 `MessageScopedUIDs` 传给投递；非持久化路径分配 transient message ID 后直接走 realtime delivery。
- 持久化发送必须配置 channel cluster。

随后进入 durable append，`pkg/channel` 仍只负责日志和复制语义。

关键代码：

- `internal/usecase/message/send.go:16`
- `internal/usecase/message/send.go:26`
- `internal/usecase/message/send.go:30`
- `internal/usecase/message/send.go:41`
- `internal/usecase/message/send.go:49`
- `internal/usecase/message/send.go:53`

### channel log append

当前 `pkg/channel` append 层负责日志和复制语义，不负责业务权限：

- 加载 channel meta。
- 检查 epoch / leader / channel deleting / deleted。
- 检查协议版本。
- 做 `FromUID + ClientMsgNo + PayloadHash` 幂等。
- 追加到 channel log。

关键代码：

- `pkg/channel/handler/append.go:12`
- `pkg/channel/handler/append.go:21`
- `pkg/channel/handler/append.go:45`

该层不应放入黑白名单、订阅者等业务规则。

### append 后副作用

当前 durable append 成功后，`message.Send` 提交 committed event。投递、会话等副作用异步执行；提交失败只记录日志，不把 durable append 成功的消息改成发送失败。

关键代码：

- `internal/usecase/message/send.go:108`
- `internal/app/committed_events.go:18`
- `internal/app/deliveryrouting.go:304`

## 旧版发送链路概览

旧版用户发送大致流程为：

```text
user.onSend
  -> checkGlobalSendPermission
  -> decryptPayload
  -> PluginSend
  -> channel.onSend
      -> permission
      -> persist
      -> sendack
      -> distribute
          -> pusher online/offline
```

关键代码：

- 用户入口：`learn_project/WuKongIM/internal/user/handler/event_onsend.go:35`
- 全局发送封禁：`learn_project/WuKongIM/internal/user/handler/event_onsend.go:145`
- 插件发送前钩子：`learn_project/WuKongIM/internal/user/handler/event_onsend.go:157`
- 频道事件流程：`learn_project/WuKongIM/internal/channel/handler/event_onsend.go:14`
- 频道 permission：`learn_project/WuKongIM/internal/channel/handler/event_permission.go:22`
- 权限核心逻辑：`learn_project/WuKongIM/internal/service/permission.go:54`
- 持久化：`learn_project/WuKongIM/internal/channel/handler/event_persist.go:18`
- 回执：`learn_project/WuKongIM/internal/channel/handler/event_sendack.go:9`
- 分发：`learn_project/WuKongIM/internal/channel/handler/event_distribute.go:47`
- 在线推送：`learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:20`
- 离线推送：`learn_project/WuKongIM/internal/pusher/handler/event_pushoffline.go:9`

## 权限逻辑差异

### 1. 全局发送封禁 SendBan

旧版：

- 发送前读取发送者个人频道信息。
- 如果 `SendBan=true`，直接返回 `ReasonSendBan`。

关键代码：

- `learn_project/WuKongIM/internal/user/handler/event_onsend.go:145`
- `learn_project/WuKongIM/internal/user/handler/event_onsend.go:151`

当前：

- `metadb.Channel` 没有 `SendBan` 字段。
- `message.Send` 没有全局发送封禁检查。
- 当前 `/channel/info` 接收 `send_ban`，但只是 compatibility 字段，未持久化。

P1 状态（2026-05-11）：当前项目已在 slot channel metadata 中持久化 `SendBan`，并在 `internal/usecase/message` 的 durable append 前检查发送者个人频道 `SendBan`，命中时返回 `ReasonSendBan`。缺失个人频道元数据时按未封禁处理，避免阻断旧数据中没有个人频道记录的用户。

关键代码：

- `pkg/slot/meta/channel.go:11`
- `internal/usecase/channel/types.go:38`
- `internal/usecase/channel/app.go:75`
- `internal/usecase/message/send.go:16`

影响：旧版被禁言用户当前仍可能发送成功。

### 2. 频道 Ban / Disband

旧版：

- 非公开频道发送前读取 channel info。
- `Ban=true` 返回 `ReasonBan`。
- `Disband=true` 返回 `ReasonDisband`。

关键代码：

- `learn_project/WuKongIM/internal/service/permission.go:54`
- `learn_project/WuKongIM/internal/service/permission.go:69`
- `learn_project/WuKongIM/internal/service/permission.go:73`

当前：

- `metadb.Channel` 只持久化 `Ban`。
- `Disband` 目前只是兼容字段，不持久化。
- 发送链路不读取 `Ban`，因此即使后台写了 `ban=1`，当前发送也不会被业务层拦截。

P1 状态（2026-05-11）：当前项目已在 slot channel metadata 中持久化 `Disband` 和 `SendBan`；群/频道发送会在 durable append 前读取 channel metadata，先按 `Ban -> ReasonBan`、再按 `Disband -> ReasonDisband` 拦截，之后才继续黑名单、订阅者和白名单检查。

关键代码：

- `pkg/slot/meta/channel.go:11`
- `internal/usecase/channel/types.go:34`
- `internal/usecase/channel/types.go:36`
- `internal/usecase/channel/app.go:75`
- `pkg/slot/proxy/store.go:67`
- `internal/usecase/message/send.go:16`

影响：频道封禁、解散语义没有完整恢复。

### 3. 群/普通频道黑名单、订阅者、白名单

旧版普通频道发送者权限顺序：

1. cmd 频道先转回原始频道。
2. 命中 denylist 返回 `ReasonInBlacklist`。
3. 不是 subscriber 返回 `ReasonSubscriberNotExist`。
4. 如果存在 allowlist，且发送者不在 allowlist，返回 `ReasonNotInWhitelist`。

关键代码：

- `learn_project/WuKongIM/internal/service/permission.go:126`
- `learn_project/WuKongIM/internal/service/permission.go:131`
- `learn_project/WuKongIM/internal/service/permission.go:136`
- `learn_project/WuKongIM/internal/service/permission.go:146`
- `learn_project/WuKongIM/internal/service/permission.go:156`

当前：

- 发送前不检查 denylist。
- 发送前不检查 subscriber。
- 发送前不检查 allowlist。
- subscriber 当前主要用于投递目标解析，不是发送资格判断。
- 当前 allowlist/denylist 用 namespaced subscriber list 模拟，但 message usecase 没有读取。

关键代码：

- `internal/usecase/message/send.go:16`
- `internal/usecase/channel/types.go:95`
- `internal/usecase/channel/app.go:147`
- `internal/usecase/channel/app.go:167`
- `internal/usecase/delivery/subscriber.go:197`

影响：非群成员或黑名单用户可能成功写入群频道日志。

### 4. 个人频道接收方黑白名单

旧版个人频道权限：

1. 从 fake channel ID 解析出发送者和接收者。
2. 接收者是系统账号则直接通过。
3. 请求接收方所在 slot owner 做本地 `allowSend` 判断。
4. 判断接收者维度 denylist。
5. 如果 `WhitelistOffOfPerson=false`，再判断接收者维度 allowlist。

关键代码：

- `learn_project/WuKongIM/internal/service/permission.go:177`
- `learn_project/WuKongIM/internal/service/permission.go:195`
- `learn_project/WuKongIM/internal/service/permission.go:201`
- `learn_project/WuKongIM/internal/service/permission.go:215`
- `learn_project/WuKongIM/internal/service/permission.go:240`
- `learn_project/WuKongIM/internal/ingress/server.go:135`

旧版默认关闭个人白名单校验：

- `learn_project/WuKongIM/internal/options/options.go:366`
- `learn_project/WuKongIM/internal/service/permission.go:252`

P0/P2 权限收敛后当前状态：

- 个人频道仍在 `message.App.Send` 中做 channel ID 规范化。
- 已在 durable append 前检查接收方维度 denylist，命中返回 `ReasonInBlacklist`。
- 已支持接收方是系统 UID 时直接通过接收方黑白名单校验。
- 已新增 `PersonWhitelistEnabled` / `WK_MESSAGE_PERSON_WHITELIST_ENABLED`，默认关闭；开启后会检查接收方维度 allowlist，发送者不在 allowlist 时返回 `ReasonNotInWhitelist`。
- 当前没有恢复旧版个人权限跨节点 RPC；新链路通过 `PermissionStore` 的 authoritative read / proxy 能力读取权限数据。

关键代码：

- `internal/access/gateway/mapper.go:33`
- `internal/usecase/message/send.go:29`
- `internal/usecase/message/permission.go`
- `internal/app/config.go`
- `cmd/wukongim/config.go`

影响：个人 denylist、可选 allowlist 和接收方系统 UID 语义已恢复；剩余差异主要是旧版 `requestAllowSend` RPC 形态没有原样保留。

### 5. 系统设备和系统账号绕过

旧版：

- 系统设备直接通过发送者权限。
- 系统账号直接通过发送者权限。
- HTTP send 未传 `from_uid` 时默认使用 `SystemUID`，并以 `SystemDeviceId` 构造连接。

关键代码：

- `learn_project/WuKongIM/internal/service/permission.go:108`
- `learn_project/WuKongIM/internal/service/permission.go:113`
- `learn_project/WuKongIM/internal/api/message.go:81`
- `learn_project/WuKongIM/internal/api/message.go:280`
- `learn_project/WuKongIM/internal/options/options.go:364`
- `learn_project/WuKongIM/internal/options/options.go:1305`

P0/P2 权限收敛后当前状态：

- 当前已有 system UID cache 和 API，并已注入 `message.Options.SystemUIDs`。
- `message.Send` 会在普通权限之前检查 system UID，命中时绕过发送业务权限。
- gateway 已把 session 中的 `DeviceID` / `DeviceFlag` 透传到 `message.SendCommand`。
- 已新增 `SystemDeviceID` / `WK_MESSAGE_SYSTEM_DEVICE_ID`，默认 `____device`。
- system device 绕过发生在发送者个人 `SendBan` 之后；也就是说 system device 不绕过发送者 `SendBan`，只绕过后续频道侧权限。
- 当前 HTTP send 要求 `from_uid` 非空。

关键代码：

- `internal/usecase/user/legacy.go:152`
- `internal/access/api/routes.go:45`
- `internal/app/build.go:534`
- `internal/app/build.go:565`
- `internal/access/api/message_send.go:35`
- `internal/access/gateway/mapper.go`
- `internal/usecase/message/permission.go`
- `internal/usecase/message/command.go`

影响：系统 UID 和 gateway system device 的核心发送权限绕过语义已恢复；HTTP send 默认 system UID / system device 构造仍未恢复。

## 支持频道类型差异

旧版权限逻辑覆盖多种频道类型：

- `Info`
- `CustomerService`
- `Visitors`
- `Agent`
- `Person`
- 普通频道 / 群频道
- cmd 派生频道
- temp 频道

关键代码：

- `learn_project/WuKongIM/internal/service/permission.go:38`
- `learn_project/WuKongIM/internal/service/permission.go:83`
- `learn_project/WuKongIM/internal/service/permission.go:91`
- `learn_project/WuKongIM/internal/service/permission.go:99`

P2 权限收敛前当前发送链路只支持：

- `ChannelTypePerson`
- `ChannelTypeGroup`

关键代码：

- `internal/usecase/message/send.go:26`

P2 权限收敛后当前发送链路已开放：

- `ChannelTypePerson`
- `ChannelTypeGroup`
- `ChannelTypeInfo`
- `ChannelTypeCustomerService`
- `ChannelTypeVisitors`
- `ChannelTypeAgent`

其中：

- `Info` / `CustomerService` 在发送者 `SendBan` 和系统绕过之后直接通过频道权限。
- `Agent` 支持裸 agent UID 规范化为 `fromUID@agentUID`，并要求发送者是 `uid@agentUID` 的任一侧，否则返回 `ReasonNotAllowSend`。
- `Visitors` 支持访客本人发送直接通过；非访客本人发送时，按 `(visitor channel ID, CustomerService)` 维度读取 denylist / subscriber / allowlist。
- `Temp` 仍只作为 request-scoped subscribers 派生的内部临时 cmd channel，不作为普通外部 send 类型开放。

当前 delivery subscriber resolver 已具备 agent、temp、visitors、customer service、info 等特殊频道订阅者来源逻辑，send usecase 已接入其中与发送权限直接相关的特殊频道类型。

关键代码：

- `internal/usecase/delivery/subscriber.go:127`
- `internal/usecase/delivery/subscriber.go:139`
- `internal/usecase/delivery/subscriber.go:150`
- `internal/usecase/delivery/subscriber.go:155`
- `internal/usecase/delivery/subscriber.go:167`
- `internal/usecase/delivery/subscriber.go:178`
- `internal/runtime/channelid/agent.go`
- `internal/usecase/message/send.go`
- `internal/usecase/message/permission.go`

影响：旧版特殊频道的核心发送入口和发送前权限已恢复；特殊频道的 webhook / AI / 离线等后续副作用仍未恢复。

## 持久化语义差异

### 1. NoPersist

旧版：

- `NoPersist=true` 的消息不写入存储。
- 不触发存储后的 `PersistAfter` 插件。
- 不触发存储消息 webhook。
- 在线推送重试也只对持久化消息建立 retry。

关键代码：

- `learn_project/WuKongIM/internal/channel/handler/event_persist.go:198`
- `learn_project/WuKongIM/internal/channel/handler/event_persist.go:202`
- `learn_project/WuKongIM/internal/channel/handler/event_persist.go:90`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:124`

P2a 前：

- `NoPersist` 只是 `Framer` bit。
- `message.Send` 会把 `Framer` 原样写入 durable channel log。
- 没有根据 `NoPersist` 绕开 append。

关键代码：

- `internal/usecase/message/send.go:102`
- `internal/usecase/message/send.go:104`
- `pkg/channel/handler/append.go:70`

影响：旧版命令/临时/不落库消息在 P2a 前可能被持久化，语义差异很大。

P2a 状态（2026-05-11）：当前项目已在 `internal/usecase/message` 的 P0/P1 权限检查之后恢复 `NoPersist` 非持久化边界。`Framer.NoPersist=true` 且权限通过时，发送返回 `ReasonSuccess`，`MessageID=0`，`MessageSeq=0`，不要求 channel cluster，不写入 durable channel log，也不提交 committed-message event。`/message/send` 已支持 `header.no_persist` 映射，并兼容顶层 `no_persist` 别名。

P2b 状态（2026-05-11）：`NoPersist + SyncOnce` 仍沿用 P2a 非持久化边界，成功返回但不做 durable append，也不做实时投递。

P2c 状态（2026-05-11）：`NoPersist + SyncOnce + subscribers` 已恢复为 transient realtime delivery；普通 `NoPersist + SyncOnce` 仍沿用 P2a 非持久化边界，成功返回但不做 durable append，也不做实时投递。

仍未恢复：普通临时频道投递、在线 cmd 投递 / `systemcmdonline`、CMD 会话 / 离线 cmd 同步、sendbatch、plugin/webhook/AI 钩子，以及非持久化消息的完整在线临时投递链路。

### 2. SyncOnce / cmd channel

旧版：

- HTTP send 支持 `header.sync_once`。
- `sync_once=1` 且不是在线 cmd channel / temp channel 时，会把原频道转换成 cmd channel。
- request-scoped subscribers 必须搭配 `sync_once=1`。

关键代码：

- `learn_project/WuKongIM/internal/api/message.go:95`
- `learn_project/WuKongIM/internal/api/message.go:107`
- `learn_project/WuKongIM/internal/api/message.go:256`

P2b 状态（2026-05-11）：

- 持久化 `SyncOnce` 发送会把 durable append 目标转换为原频道派生的 cmd channel，即 `source____cmd`。
- 发送权限仍在原始频道上检查，不用 cmd channel 重新判断黑白名单、订阅者和频道状态。
- 投递订阅者仍从原始频道解析，避免 cmd 派生频道缺少普通订阅者导致投递目标为空。
- 实时投递的 `RecvPacket` 对客户端展示原始频道，不暴露内部 cmd channel。
- 普通会话投影会过滤 cmd 派生频道和 `SyncOnce` 消息，避免一次性同步消息进入普通会话列表。
- P2c 后 `SendCommand` 已有 request-scoped subscribers 字段；P2b 时该字段尚未接入。

关键代码：

- `internal/usecase/message/command.go:8`
- `internal/usecase/message/send.go:30`
- `internal/usecase/message/send.go:53`

影响：持久化 `SyncOnce` 的核心 cmd channel 写入、权限、订阅者解析、实时展示和普通会话过滤已恢复；P2c 又补上 request-scoped subscribers 的核心定向投递。在线 cmd 投递、CMD 会话 / 离线 cmd 同步、普通临时频道和 sendbatch 仍未恢复。

### 3. request-scoped subscribers / 临时频道

旧版 `/message/send` 支持 `subscribers`：

- `subscribers` 有值时，必须是 `sync_once` 消息。
- `channel_id` 和 `subscribers` 不能同时存在。
- 持久化场景会生成临时频道并设置临时订阅者。
- 不持久化场景会生成 tag，用在线 cmd channel 投递。

关键代码：

- `learn_project/WuKongIM/internal/api/message.go:89`
- `learn_project/WuKongIM/internal/api/message.go:95`
- `learn_project/WuKongIM/internal/api/message.go:101`
- `learn_project/WuKongIM/internal/api/message.go:107`
- `learn_project/WuKongIM/internal/api/message.go:118`
- `learn_project/WuKongIM/internal/api/message.go:128`

P2c 状态（2026-05-11）：

- HTTP send 已支持 `subscribers`，该模式拒绝非空 `channel_id`，忽略 `channel_type`，并将订阅者列表传入 message usecase。
- delivery 层通过 `SubscriberSnapshotRequest.MessageScopedUIDs` 解析消息级 subscriber 快照；tag path 使用 ephemeral tag，避免覆盖普通频道 tag ref。
- 持久化 request-scoped send 写 temp cmd channel；非持久化 request-scoped send 分配 transient message ID 并直接进入 realtime delivery。

关键代码：

- `internal/access/api/message_send.go:14`
- `internal/usecase/delivery/source.go:37`
- `internal/usecase/delivery/subscriber.go:102`

影响：指定一批用户临时投递的核心旧 API 语义已恢复；但旧版通过临时订阅者状态支撑的 CMD 会话 / 离线同步仍未恢复。

## 投递与回执语义差异

### 1. 发送者自身连接跳过

旧版在线推送会跳过发送消息的同一连接：

- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:98`

当前也会跳过 origin session：

- `internal/app/deliveryrouting.go:1264`

这部分语义基本一致。

### 2. 自己多端红点

旧版：

- 同 UID 的其他设备仍可收到消息。
- 如果接收连接 UID 等于发送者 UID，则红点置为 false。

关键代码：

- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:198`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:200`

当前：

- 只跳过原始 session。
- `buildRealtimeRecvPacket` 直接复制消息的 `Framer`，没有针对同 UID 接收者关闭红点。

关键代码：

- `internal/app/deliveryrouting.go:1264`
- `internal/app/deliveryrouting.go:1824`
- `internal/app/deliveryrouting.go:1829`

影响：自己其他端红点表现可能不同。

### 3. 接收包加密和 MsgKey

旧版在线推送时会按目标连接重新加密 payload，并生成 `MsgKey`：

- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:152`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:206`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushonline.go:237`

当前：

- send path 会解密入口 payload。
- realtime recv packet 直接复制 `Payload` 和 `MsgKey`。

关键代码：

- `internal/access/gateway/frame_router.go:29`
- `internal/app/deliveryrouting.go:1824`
- `internal/app/deliveryrouting.go:1831`
- `internal/app/deliveryrouting.go:1844`

影响：加密层行为和旧版不完全一致。该项属于协议/传输兼容问题，不是 P0 权限问题。

### 4. 离线 Webhook / AI

旧版：

- 分发时收集离线用户。
- 离线事件会触发 AI 推送和离线 webhook。

关键代码：

- `learn_project/WuKongIM/internal/channel/handler/event_distribute.go:178`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushoffline.go:9`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushoffline.go:21`

当前：

- 当前 delivery runtime 有 session close / ack / retry 相关运行时能力。
- 未看到旧版离线 webhook / AI 业务语义接入当前 send path。

影响：业务扩展和离线通知兼容缺失。

## 插件与 Webhook 差异

旧版发送链路有多个扩展点：

- `PluginSend`：发送前可改 payload 或拒绝消息。
- `PluginPersistAfter`：消息持久化后通知插件。
- `EventMsgNotify` webhook：持久化成功且非 `NoPersist` 时触发。
- Offline webhook：离线用户通知。

关键代码：

- `learn_project/WuKongIM/internal/user/handler/event_onsend.go:157`
- `learn_project/WuKongIM/internal/channel/handler/event_persist.go:59`
- `learn_project/WuKongIM/internal/channel/handler/event_persist.go:90`
- `learn_project/WuKongIM/internal/pusher/handler/event_pushoffline.go:21`

当前：

- 当前 send path 未接入上述插件和 webhook 扩展点。
- durable append 后只有 committed dispatcher，用于 delivery / conversation 等内部副作用。

关键代码：

- `internal/usecase/message/send.go:87`
- `internal/app/committed_events.go:14`

影响：旧业务如果依赖插件或 webhook，当前发送链路不会触发这些外部扩展。

## 数据模型差异

### 旧版 ChannelInfo

旧版 `ChannelInfo` 包含：

- `Ban`
- `Large`
- `Disband`
- `SendBan`
- `AllowStranger`
- 各种计数和 webhook 字段

关键代码：

- `learn_project/WuKongIM/pkg/wkdb/model.go:147`

### 当前 Channel

当前 slot meta 的 `Channel` 只包含：

- `ChannelID`
- `ChannelType`
- `Ban`
- `SubscriberMutationVersion`

关键代码：

- `pkg/slot/meta/channel.go:11`

### 当前兼容入口

当前 API 仍接收旧字段：

- `large`
- `ban`
- `disband`
- `send_ban`
- `allow_stranger`

但 usecase 仅持久化 `Ban`。

关键代码：

- `internal/access/api/channel_management.go:13`
- `internal/access/api/channel_management.go:361`
- `internal/usecase/channel/types.go:26`
- `internal/usecase/channel/app.go:75`

影响：后台接口看起来兼容旧字段，但大多数字段不影响发送业务逻辑。

## 当前已有但未被发送权限复用的能力

当前项目并不是完全没有相关基础能力，只是还未接入发送前权限：

- allowlist / denylist 通过 namespaced subscriber list 存储。
- ordinary subscribers 已有分页存储和管理 API。
- system UID cache 已有本地缓存和集群广播。
- reason code 已包含旧版所需权限拒绝码。

关键代码：

- namespaced list：`internal/usecase/channel/types.go:95`
- denylist API：`internal/usecase/channel/app.go:147`
- allowlist API：`internal/usecase/channel/app.go:167`
- subscriber storage：`pkg/slot/meta/subscriber.go:142`
- system UID cache：`internal/usecase/user/legacy.go:152`
- reason code：`pkg/protocol/frame/common.go:124`

主要缺口是：缺少面向发送权限的高效点查接口，以及 message usecase 未接入这些规则。

## 推荐迁移优先级

### P0：恢复当前支持频道的发送前权限

建议第一阶段只覆盖当前已经开放的个人/群发送，不扩大频道类型。

范围：

- 群频道：
  - 检查 channel `Ban`。
  - 检查 denylist。
  - 检查 sender 是否是 subscriber。
  - 如果 allowlist 存在，检查 sender 是否在 allowlist。
- 个人频道：
  - 检查接收者维度 denylist。
  - 个人白名单默认保持旧版默认关闭，即先不校验。
- 系统 UID：
  - 作为权限绕过。
- 返回旧版已有 reason code：
  - `ReasonBan`
  - `ReasonSubscriberNotExist`
  - `ReasonInBlacklist`
  - `ReasonNotInWhitelist`

建议落点：

- `internal/usecase/message`：新增发送权限组件，并在 `Send` 进入 `sendDurable` 前调用。
- `pkg/slot/meta`：增加 subscriber/member point lookup。
- `pkg/slot/proxy`：增加 authoritative point lookup RPC。
- `internal/app/build.go`：注入 store / system UID checker。
- `internal/access/gateway/error_map.go`、`internal/access/api/error_map.go`：如采用 error 返回，需要补 reason/status 映射。

不建议把业务权限放入 `pkg/channel` append 层。append 层应继续只负责 channel log 和复制一致性。

### P1：补齐频道元数据语义

在 P0 稳定后，再补：

- `Disband`
- `SendBan`
- `AllowStranger`
- 必要时 `Large`

涉及：

- `metadb.Channel` schema。
- slot fsm command codec。
- `pkg/slot/proxy.Store.UpdateChannel`。
- channel management usecase。
- 相关迁移和测试。

### P2：恢复 NoPersist / SyncOnce / 临时投递

该阶段涉及发送路径分叉，风险比 P0 高。

范围：

- `NoPersist` 不写 channel log（P2a 已恢复核心非持久化边界，但尚未恢复完整临时在线投递语义）。
- `SyncOnce` cmd channel 转换（P2b 已恢复持久化 `SyncOnce` 写入 `____cmd` 的核心路径）。
- `/message/send` request-scoped `subscribers`。
- 临时频道 / message-scoped subscriber source 接入 send path。
- 在线 cmd 投递 / `systemcmdonline` 与 CMD 会话 / 离线 cmd 同步。

### P3：恢复特殊频道、sendbatch、插件和 Webhook

范围：

- `/message/sendbatch`
- agent / customer service / visitors / info 等特殊频道的 webhook、AI、离线等后续副作用（核心发送权限已在 P2 权限收敛中恢复）。
- `PluginSend`。
- `PersistAfter`。
- 消息 webhook / 离线 webhook / AI 处理。

这些能力最好单独设计，避免把旧版事件总线式逻辑直接搬回当前分层架构。

## P0 实现注意事项

1. 权限必须在 durable append 之前执行。
2. 权限组件应放在 `internal/usecase/message`，保持入口协议无关。
3. `pkg/channel` 不承载业务规则。
4. 对 subscriber / allowlist / denylist 应优先增加点查接口，不要在发送热路径分页扫描。
5. 个人频道应先沿用旧版默认：只检查接收方黑名单，不开启个人白名单。
6. 系统 UID 绕过必须在普通权限之前处理。
7. sendack reason 尽量使用已有 `frame.ReasonCode`，避免新增不兼容码。
8. 当前项目没有绕过集群的独立部署语义；单节点部署也按单节点集群处理。

## 结论

当前发送链路的基础设施能力更强，尤其 channel log 幂等、复制、异步投递和可观测性更清晰。经过 P0/P1/P2a/P2b/P2c、P2 权限收敛以及 P2d-a/P2d-b 后，当前链路已经恢复了主要发送前权限、频道状态、系统 UID / system device 绕过、NoPersist 核心边界、持久化 cmd channel 写入、request-scoped subscribers、CMD source-channel 订阅者解析、command-style NoPersist realtime 投递，以及 `Info` / `CustomerService` / `Visitors` / `Agent` 的核心发送权限。

后续最值得优先设计的是剩余 CMD 会话与恢复语义：CMD conversation/offline sync，以及 request-scoped subscribers 快照在 durable replay 后的恢复策略。这些能力会影响会话、离线和恢复模型，应单独设计，避免把旧版事件总线式逻辑直接搬回当前分层架构。

## P0 实施后状态（2026-05-11）

本轮 P0 已恢复以下发送前业务权限，并放在 `internal/usecase/message` 的 durable append 之前：

- 群频道 `Ban` 拦截，返回 `ReasonBan`。
- 群 denylist 命中拦截，返回 `ReasonInBlacklist`。
- 群发送者不是普通 subscriber 时拦截，返回 `ReasonSubscriberNotExist`。
- 群 allowlist 非空且发送者不在 allowlist 时拦截，返回 `ReasonNotInWhitelist`。
- 个人频道检查接收方 denylist，命中返回 `ReasonInBlacklist`；个人 allowlist 仍按旧版默认关闭。
- 系统 UID 通过 `user.App.IsSystemUID` 绕过上述 P0 权限。
- 权限拒绝返回正常 `SendResult.Reason`，不进入 channel log durable append，也不作为基础设施错误返回。

为支持热路径权限查询，本轮新增了：

- `internal/contracts/channelmembers` 统一 allowlist/denylist/temp list 的内部 channel ID 推导。
- `pkg/slot/meta` subscriber 点查与非空查询。
- `pkg/slot/proxy` authoritative subscriber lookup RPC 与 channel permission metadata RPC。

## P1 实施后状态（2026-05-11）

本轮 P1 已恢复以下发送前频道状态权限，并继续放在 `internal/usecase/message` 的 durable append 之前：

- 发送者个人频道 `SendBan`：命中返回 `ReasonSendBan`。
- 群/频道 `Disband`：命中返回 `ReasonDisband`。
- `/channel/info` 传入的 `ban` / `disband` / `send_ban` 会通过 channel usecase 持久化到 slot channel metadata。
- `pkg/slot/meta`、`pkg/slot/fsm`、`pkg/slot/proxy` 均保留 `Disband` / `SendBan` 字段，authoritative permission read 可读取完整状态。

仍未恢复的旧版差异包括：`AllowStranger`、`NoPersist` 完整在线临时投递语义、request-scoped subscribers、临时频道投递、在线 cmd 投递 / `systemcmdonline`、CMD 会话 / 离线 cmd 同步、sendbatch、plugin/webhook/AI 钩子以及特殊频道发送支持。

## P2a 实施后状态（2026-05-11）

本轮 P2a 已恢复 `NoPersist` 的核心非持久化发送边界，并继续放在 `internal/usecase/message` 的发送前校验和权限检查之后：

- `Framer.NoPersist=true` 且权限通过时，不再进入 channel log durable append。
- NoPersist 成功返回 `ReasonSuccess`，`MessageID=0`，`MessageSeq=0`。
- NoPersist 成功不要求 channel cluster / meta refresher / remote appender。
- NoPersist 成功不提交 committed-message event，避免把不存在的 durable commit 分发给后续副作用。
- `/message/send` 已支持旧版兼容字段 `header.no_persist`，并兼容顶层 `no_persist` 别名。

仍未恢复的旧版差异包括：`AllowStranger`、request-scoped subscribers、临时频道投递、在线 cmd 投递 / `systemcmdonline`、CMD 会话 / 离线 cmd 同步、sendbatch、plugin/webhook/AI 钩子、特殊频道发送支持，以及非持久化消息的完整在线临时投递链路。

## P2b 实施后状态（2026-05-11）

本轮 P2b 已恢复持久化 `SyncOnce` 的核心 cmd channel 发送语义：

- 持久化 `SyncOnce` 发送写入原频道派生的 `____cmd` channel。
- 发送权限继续基于原始频道检查。
- 投递订阅者继续从原始频道解析。
- 实时投递给客户端时展示原始频道视图。
- 普通会话投影过滤 cmd 派生频道和 `SyncOnce` 消息。

P2b 阶段普通 `NoPersist + SyncOnce` 仍返回成功，但不写 durable channel log，也不做实时投递。

P2b 阶段仍未恢复的旧版差异包括：`AllowStranger`、request-scoped subscribers、临时频道投递、在线 cmd 投递 / `systemcmdonline`、CMD 会话 / 离线 cmd 同步、sendbatch、plugin/webhook/AI 钩子、特殊频道发送支持，以及非持久化消息的完整在线临时投递链路。

## P2c 实施后状态（2026-05-11）

本轮 P2c 已恢复 `/message/send` request-scoped `subscribers` 的核心定向投递语义：

- HTTP `/message/send` 接收 `subscribers`；该模式要求 `sync_once=1`，拒绝非空 `channel_id`，并忽略 `channel_type`。
- subscriber 列表会 trim 空值并按首次出现顺序去重；空列表返回 request-subscriber validation error。
- 持久化 request-scoped send 不写旧版临时订阅者状态，而是根据规范化 subscriber 快照派生稳定 temp channel，再写入其 `____cmd` channel。
- durable append 成功后，committed delivery envelope 携带 `MessageScopedUIDs`，投递 resolver 只使用该消息级 subscriber 快照，不复用普通频道 subscriber tag state。
- message-scoped delivery tag 是 ephemeral tag，只缓存本次消息 tag body，不覆盖同 channel key 下可复用 tag ref。
- `NoPersist + SyncOnce + subscribers` 分配 transient message ID，`MessageSeq=0`，不写 channel log，直接提交 realtime delivery；本地接入节点作为该 transient delivery 的 ack/retry owner。
- temp cmd realtime packet 展示给客户端时会剥离 `____cmd` 后缀，保留 `SyncOnce` / `NoPersist` header。

仍未恢复的旧版差异包括：CMD conversation/offline sync、`/message/sendbatch`、`systemcmdonline` 专用语义、`expire`、plugin/webhook/AI 钩子以及特殊频道发送支持。P2c 的 durable request-scoped send 只保证 channel log 中有消息；如果进程崩溃后仅靠 durable replay，精确 `MessageScopedUIDs` 快照不会从日志中恢复，后续如要恢复完整旧版离线/CMD 同步需要单独设计持久化快照或事件模型。

## P2 权限收敛后状态（2026-05-12）

本轮 P2 权限收敛已恢复以下旧版发送权限兼容语义：

- 个人频道接收方是系统 UID 时，直接通过接收方黑白名单校验。
- 个人频道接收方 allowlist 可通过 `WK_MESSAGE_PERSON_WHITELIST_ENABLED=true` 开启；默认关闭，保持旧版 `WhitelistOffOfPerson=true` 的默认兼容语义。
- gateway session 中的 `DeviceID` / `DeviceFlag` 会透传到 `message.SendCommand`；`WK_MESSAGE_SYSTEM_DEVICE_ID` 默认 `____device`。
- system device 绕过发生在发送者个人 `SendBan` 之后，只绕过后续频道侧权限，不绕过发送者 `SendBan`。
- `Info` / `CustomerService` 发送在发送者 `SendBan` 和系统绕过之后直接通过频道权限。
- `Agent` 频道支持裸 agent UID 到 `fromUID@agentUID` 的规范化；发送者必须是 `uid@agentUID` 两端之一，否则返回 `ReasonNotAllowSend`。
- `Visitors` 频道支持访客本人直接发送；非访客本人发送时按 `(visitor channel ID, CustomerService)` 维度检查 denylist / subscriber / allowlist。
- 已新增 `internal/runtime/channelid` 的 agent channel 编解码辅助，避免把 agent channel ID 解析散落在发送路径。
- invalid agent channel ID 在 gateway sendack 映射为 `ReasonChannelIDError`，HTTP send 映射为 400 `invalid channel id`。

本轮之后仍未恢复的旧版差异包括：CMD conversation/offline sync、在线 cmd 投递 / `systemcmdonline` 专用语义、普通临时频道投递、`/message/sendbatch`、`expire`、`AllowStranger`、plugin/webhook/AI 钩子，以及特殊频道的后续副作用（例如 webhook / AI / 离线处理）。P2c 中提到的 durable request-scoped subscribers 快照不可 replay 恢复的问题仍然存在，后续如要恢复完整 CMD 离线同步需要单独设计持久化快照或事件模型。

## P2d-a/P2d-b 实施后状态（2026-05-12）

本轮 CMD 频道语义收敛已恢复 / 固化以下行为：

- CMD 频道发送权限继续按 source channel 检查，durable append / delivery actor 继续使用 `source____cmd`。
- 已寻址到 `____cmd` 的输入不会重复追加后缀，person channel append key 使用规范化后的 source channel。
- group / person / agent / customer-service / visitors / info / temp CMD 订阅者解析均按 source channel 或 message-scoped snapshot 解析。
- visitors CMD 非访客本人发送时按 `(source, CustomerService)` 维度检查 denylist / subscriber / allowlist。
- info CMD 如果合并 temporary overlay，则 delivery tag 标记为 non-reusable，避免只用 info source mutation version 复用包含临时成员的 tag。
- 普通 `NoPersist + SyncOnce` 和已寻址 CMD 的 `NoPersist` 发送不写 channel log，但会分配 transient message ID 并通过 realtime delivery 投递，`MessageSeq=0`。
- 非 command-style 的普通 `NoPersist` 仍保持 P2a 行为：权限通过后返回成功，不写 durable append，也不做 realtime 投递。

仍未恢复的旧版差异包括：CMD conversation/offline sync、durable request-scoped subscribers 快照 replay 恢复、普通临时频道投递、`/message/sendbatch`、`expire`、`AllowStranger`、plugin/webhook/AI 钩子，以及特殊频道的后续副作用。
