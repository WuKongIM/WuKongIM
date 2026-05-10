# 发送链路业务逻辑差异分析

## 背景

本文对比当前项目发送链路与旧版 `learn_project/WuKongIM` 的业务逻辑差异，重点关注发送前权限、频道状态、黑白名单、订阅者、持久化、投递和扩展钩子等语义。

当前项目的发送链路已经重构为“薄入口 + message usecase + channel log + 异步投递”的结构；旧版则在发送事件流中内置了大量业务规则。两者不是等价迁移关系，当前链路在权限和兼容语义上仍有明显缺口。

## 当前发送链路概览

### 网关入口

当前网关处理 `SendPacket` 时主要做：

1. 必要时解密 send packet。
2. 从 session 读取发送者 UID。
3. 将个人频道规范化。
4. 调用 `message.App.Send`。
5. 写回 sendack。

关键代码：

- `internal/access/gateway/frame_router.go:29`
- `internal/access/gateway/mapper.go:10`
- `internal/access/gateway/error_map.go:12`

网关入口目前不承载频道业务权限。

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

相比旧版，当前 HTTP send 不支持 `header`、`expire`、`subscribers`、`sync_once` 临时投递、`sendbatch`、流消息等旧接口语义。

### message usecase

当前 `message.App.Send` 只做以下业务校验：

- `FromUID` 不能为空。
- 只支持个人频道和群频道。
- 个人频道做规范化。
- 必须配置 channel cluster。

随后直接进入 durable append。

关键代码：

- `internal/usecase/message/send.go:16`
- `internal/usecase/message/send.go:26`
- `internal/usecase/message/send.go:37`
- `internal/usecase/message/send.go:46`

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

- `internal/usecase/message/send.go:87`
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

当前：

- 个人频道只做 channel ID 规范化。
- 不检查接收者黑名单。
- 不检查接收者白名单。
- 没有个人权限跨节点 RPC。

关键代码：

- `internal/access/gateway/mapper.go:33`
- `internal/usecase/message/send.go:29`

影响：被接收方拉黑的用户当前仍可能发送个人消息。

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

当前：

- 当前已有 system UID cache 和 API。
- 但 `message.Options` 未注入 system UID checker，`message.Send` 也没有使用系统账号绕过权限。
- 当前 HTTP send 要求 `from_uid` 非空。

关键代码：

- `internal/usecase/user/legacy.go:152`
- `internal/access/api/routes.go:45`
- `internal/app/build.go:534`
- `internal/app/build.go:565`
- `internal/access/api/message_send.go:35`

影响：旧版系统消息兼容语义缺失；后续补权限时也需要避免误拦截系统账号。

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

当前发送链路只支持：

- `ChannelTypePerson`
- `ChannelTypeGroup`

关键代码：

- `internal/usecase/message/send.go:26`

当前 delivery subscriber resolver 已经具备一些特殊频道订阅者来源逻辑，例如 agent、temp、visitors、customer service、info 等，但 send usecase 没有开放这些频道类型发送。

关键代码：

- `internal/usecase/delivery/subscriber.go:127`
- `internal/usecase/delivery/subscriber.go:139`
- `internal/usecase/delivery/subscriber.go:150`
- `internal/usecase/delivery/subscriber.go:155`
- `internal/usecase/delivery/subscriber.go:167`
- `internal/usecase/delivery/subscriber.go:178`

影响：旧版特殊频道业务尚未恢复到发送入口。

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

当前：

- `NoPersist` 只是 `Framer` bit。
- `message.Send` 会把 `Framer` 原样写入 durable channel log。
- 没有根据 `NoPersist` 绕开 append。

关键代码：

- `internal/usecase/message/send.go:102`
- `internal/usecase/message/send.go:104`
- `pkg/channel/handler/append.go:70`

影响：旧版命令/临时/不落库消息在当前可能被持久化，语义差异很大。

### 2. SyncOnce / cmd channel

旧版：

- HTTP send 支持 `header.sync_once`。
- `sync_once=1` 且不是在线 cmd channel / temp channel 时，会把原频道转换成 cmd channel。
- request-scoped subscribers 必须搭配 `sync_once=1`。

关键代码：

- `learn_project/WuKongIM/internal/api/message.go:95`
- `learn_project/WuKongIM/internal/api/message.go:107`
- `learn_project/WuKongIM/internal/api/message.go:256`

当前：

- `SyncOnce` 只是 `Framer` bit。
- 不做 cmd channel 转换。
- `SendCommand` 没有 subscribers 字段。

关键代码：

- `internal/usecase/message/command.go:8`
- `internal/usecase/message/send.go:102`

影响：旧版一次性同步消息和 cmd 消息语义未恢复。

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

当前：

- HTTP send 不支持 `subscribers`。
- delivery 层有 `SubscriberSnapshotRequest.MessageScopedUIDs`，但当前 send path 没有调用 `BeginSnapshotWithRequest`，因此这个能力未接入发送入口。

关键代码：

- `internal/access/api/message_send.go:14`
- `internal/usecase/delivery/source.go:37`
- `internal/usecase/delivery/subscriber.go:102`

影响：指定一批用户临时投递的旧 API 语义缺失。

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

- `NoPersist` 不写 channel log。
- `SyncOnce` cmd channel 转换。
- `/message/send` request-scoped `subscribers`。
- 临时频道 / message-scoped subscriber source 接入 send path。

### P3：恢复特殊频道、sendbatch、插件和 Webhook

范围：

- `/message/sendbatch`
- agent / customer service / visitors / info 等特殊频道发送权限。
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

当前发送链路的基础设施能力更强，尤其 channel log 幂等、复制、异步投递和可观测性更清晰。但从业务兼容角度看，当前链路仍缺少旧版发送前权限和若干旧 API 语义。

最优先需要恢复的是 P0 权限：频道 Ban、群黑白名单/订阅者、个人黑名单、系统 UID 绕过。该阶段能先补上安全边界，同时不打断当前 channel log 和投递架构。

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

仍未恢复的旧版差异包括：`SendBan`、`Disband`、`AllowStranger`、`NoPersist` 非持久化分支、`SyncOnce`/cmd 频道转换、request-scoped subscribers、sendbatch、plugin/webhook/AI 钩子以及特殊频道发送支持。
