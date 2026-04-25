# 2026-04-07 `channellog` 完整消息模型设计

## 概述

本设计将 `pkg/storage/channellog` 的日志记录从“瘦 payload 记录”收敛为“完整消息记录”。

目标不是把日志简单看作“消息负载”，而是让分布式日志条目本身就表达一条可还原的频道消息。这样历史读取、故障恢复、异步投递、跨节点提交都围绕同一个消息模型展开，不再依赖发送热路径里临时拼出来的上下文。

本轮设计明确不兼容当前 `channellog` 已落盘的旧消息格式，直接切换为新存储格式，不做迁移。

## 背景与问题

当前 `pkg/storage/channellog` 中的消息记录只包含：

- `MessageID`
- `SenderUID`
- `ClientMsgNo`
- `PayloadHash`
- `Payload`

这会导致几个结构性问题：

- 分布式日志并不等于完整消息，只能算“消息的一部分”
- `Fetch` / `LoadMsg` / `LoadNextRangeMsgs` 读回来的只是瘦对象，无法直接还原完整接收消息
- 实时投递依赖 `internal/usecase/message.Send` 中的 `SendCommand` 临时拼 `CommittedMessageEnvelope`
- durable truth 与 realtime envelope 出现双模型，字段容易漂移
- `Timestamp` 等字段只存在于运行时临时对象中，无法保证历史读取与实时投递语义一致
- `actor.dispatchLate` 目前也默认认为日志里无法重建完整消息

旧版 `wkdb` 的方向更接近正确语义：消息以频道日志归属为主键，但消息体本身保存完整接收消息字段。当前 `channellog` 需要回到这个原则，同时保持新的 ISR 偏移语义与包结构边界。

## 已确认约束

- 分布式日志就是消息，日志项必须承载完整消息语义
- 这里的“消息”特指已提交日志，不包含本地未提交尾部
- `pkg/storage/channellog` 需要有自己的稳定消息模型，不直接把 `wkframe.RecvPacket` 作为存储对象
- 当前已落盘 `channellog` 数据不需要兼容读取
- 发送热路径不能在提交成功后为每条消息再回库查询一次
- 个人频道的接收包 `ChannelID` 仍然允许按接收者视角改写，但这种改写属于 realtime 视图，不属于 durable 消息本体
- `messageSeq` 仍然定义为 `committed offset + 1`

## 目标

- 让 `pkg/storage/channellog` 提供唯一的完整消息模型
- 让日志编码、历史读取、异步投递围绕同一条消息对象展开
- 保持发送热路径零额外回库读取
- 保持 `ChannelKey -> GroupKey` 的日志归属与 ISR 路由职责不变
- 明确 durable message 与 realtime `RecvPacket` 的边界
- 让后续字段扩展只改一处消息编解码和转换逻辑

## 非目标

- 不在本轮引入旧格式迁移器
- 不在本轮把 `channellog` 直接绑死到 `wkframe.RecvPacket`
- 不在本轮为历史消息增加次级索引或搜索能力
- 不在本轮改变 `messageSeq = offset + 1` 这一语义
- 不在本轮把个人频道的接收者视角改写固化进 durable log
- 不在本轮改变 ISR 对未提交冲突尾部的截断语义

## 核心决策

### 1. 引入 `channellog.Message` 作为唯一消息模型

`pkg/storage/channellog` 新增公开 `Message`，作为包内唯一的完整消息模型。

建议字段：

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

字段语义按“完整接收消息”定义，但不等于“已经按某个具体接收者视角改写后的在线下发包”。

### 2. `ChannelKey` 与 `Message` 同时存在，但职责不同

`ChannelKey` 继续承担：

- 频道日志归属
- `GroupKey` 推导
- 元数据查找
- ISR runtime 路由

`Message` 承担：

- 消息内容与消息元数据
- 历史读取结果
- durable truth
- realtime 视图转换的输入

这两个对象不是互斥关系。

尤其在个人频道里：

- `ChannelKey.ChannelID` 仍然是 canonical personal channel id
- `Message.ChannelID` 表示 durable 的频道消息语义
- 面向具体接收者的 `RecvPacket.ChannelID` 改写放在 realtime 转换层完成

### 3. `PayloadHash` 降为内部技术字段

`PayloadHash` 不再属于公开消息模型。

它只用于：

- 幂等冲突检测
- 内部消息编码

它不应出现在 `Fetch`、`LoadMsg` 或跨模块消息对象里。

### 4. 发送请求改为携带完整消息

`SendRequest` 不再只携带 `SenderUID/ClientMsgNo/Payload` 这类半消息字段，而是直接携带一条待提交的 `Message`。

建议形态：

```go
type SendRequest struct {
    ChannelID             string
    ChannelType           uint8
    Message               Message
    SupportsMessageSeqU64 bool
    ExpectedChannelEpoch  uint64
    ExpectedLeaderEpoch   uint64
}
```

其中：

- `ChannelID/ChannelType` 继续作为路由与元数据校验输入
- `Message.MessageSeq` 在提交前必须为 `0`
- `Message.MessageID` 在提交前允许为 `0`

### 5. `cluster.Send` 只补齐提交态字段

发送前，业务入口先构造完整 durable `Message`。

`cluster.Send` 的职责收敛为：

- 校验元数据与 leader 身份
- 生成 `MessageID`
- 编码完整消息并写入 ISR 日志
- 用 `commit.NextCommitHW` 回填 `MessageSeq`
- 写入幂等状态
- 返回提交后的完整消息

`cluster.Send` 不负责重新拼消息，也不负责在提交后从本地存储反查消息。

### 6. 提交成功后走内存快路径，不回库

实时投递热路径必须复用提交成功后内存中的完整消息对象。

不允许的做法：

- `Send` 成功后立即按 `ChannelKey + MessageSeq` 再查一次 `Store`

推荐做法：

- `cluster.Send` 内部返回一条已提交的 `Message`
- `internal/usecase/message` 直接把这条消息交给后续异步投递链路
- 如果 owner 在远端，则通过 RPC 把完整消息转交 owner

`Store.LoadMsg` 的价值保留给：

- 历史读取
- 故障恢复
- late replay
- 补消息

而不是每条实时发送。

### 7. “消息就是日志”以 committed prefix 为边界

本设计对“消息就是分布式日志”的严格定义是：

```text
消息 = HW 以内的已提交日志记录
```

而不是：

```text
消息 = 所有已经 append 到本地 log 的记录
```

原因是 ISR 现有实现明确允许在冲突恢复时截断未提交尾部：

- leader 在 `Fetch` 中会根据 epoch history 计算分歧点，并要求 follower `TruncateTo`
- follower 在 `ApplyFetch` 中会先截断未提交冲突尾部，再追加 leader 记录
- replica 重启恢复时，如果 `LEO > checkpoint.HW`，也会把 dirty tail 截断到 `HW`

因此：

- 未提交尾部即使已经本地 append，也不能被视为 durable message
- durable message 的可见性必须严格受 `HW` 约束
- 任何面向业务的 `SendAck`、`Fetch`、`LoadMsg`、异步投递提交都只能基于已提交消息

这个边界必须体现在 API、实现和测试里，不能只停留在口头约定。

### 8. `Fetch` / `Store` 统一返回完整消息

以下 API 全部统一返回完整 `Message`：

- `Cluster.Fetch`
- `Store.LoadMsg`
- `Store.LoadNextRangeMsgs`
- `Store.LoadPrevRangeMsgs`

当前 `ChannelMessage` 可以删除，或短期作为：

```go
type ChannelMessage = Message
```

但最终应收敛为单一消息类型，避免语义重复。

### 9. realtime `RecvPacket` 是 durable `Message` 的视图

`wkframe.RecvPacket` 不是存储模型，而是在线投递视图。

转换规则：

- 输入是 durable `channellog.Message`
- 输出是在线 `wkframe.RecvPacket`
- 个人频道接收者视角的 `ChannelID` 改写只在这一层做
- 不重新生成 `Timestamp`
- 不重新生成 `MsgKey`
- 不依赖 `SendCommand`

也就是说：

```text
durable Message -> realtime RecvPacket
```

而不是：

```text
SendCommand -> realtime RecvPacket
```

### 10. 实时投递链路不再依赖 `CommittedMessageEnvelope`

当前 `CommittedMessageEnvelope` 基本是在把 `SendCommand` 再镜像一遍。

本设计要求把它逐步收敛为以下之一：

- 直接传 `channellog.Message`
- 或极薄包装：

```go
type CommittedMessage struct {
    Message channellog.Message
}
```

重点不是名字，而是：

- 投递链路必须以 durable `Message` 为输入
- 不再以 `SendCommand` 派生 envelope 为输入

### 11. 幂等键不变，幂等结果提升为完整消息语义

幂等键仍然使用：

```text
(channelID, channelType, senderUID, clientMsgNo)
```

幂等冲突检测仍可使用内部 `PayloadHash`。

但是幂等命中后的语义应提升为：

- 返回同一 `MessageID`
- 返回同一 `MessageSeq`
- 内部可以继续复用同一条完整 durable `Message`

这样首发与重放命中的后续投递链路可以保持同一消息模型。

## 写入路径

### 1. 入口层

入口层把发送协议对象映射成 durable `Message`。

该对象在提交前必须带齐以下字段：

- `Framer`
- `Setting`
- `MsgKey`
- `Expire`
- `ClientSeq`
- `ClientMsgNo`
- `StreamNo`
- `StreamID`
- `StreamFlag`
- `Timestamp`
- `ChannelID`
- `ChannelType`
- `Topic`
- `FromUID`
- `Payload`

`Timestamp` 在这里一次性确定，后续不再重算。

### 2. `internal/usecase/message.Send`

`Send` 用例负责：

- 组装 durable `Message`
- 调用 `channellog.Send`
- 获取已提交的完整消息
- 异步提交给投递链路

它不再负责基于 `SendCommand` 拼 `CommittedMessageEnvelope`。

这里的“已提交”不是泛指 append 成功，而是必须已经拿到 `commit.NextCommitHW`。

也就是说：

- 未进入 committed prefix 的日志记录不能向上冒泡成业务消息
- 未提交记录不能产生 `SendAck`
- 未提交记录不能进入 realtime 投递链路

### 3. `channellog.Send`

`Send` 完成后应返回：

- 面向外部 `SendAck` 需要的 `MessageID/MessageSeq`
- 面向内部异步投递的已提交 `Message`

如果维持现有公开接口稳定，可以采用：

```go
type SendResult struct {
    MessageID  uint64
    MessageSeq uint64
    Message    Message
}
```

如果不希望公开暴露 `Message`，则至少需要在包内有等价返回对象供上层复用。

`SendResult.Message` 必须是 committed message，而不是“刚 encode 完准备写入的 message 草稿”。

## 存储编码

新消息编码以完整 `Message` 为输入。

编码层应包含：

- 完整 durable message 字段
- 内部 `PayloadHash`

编码不再围绕旧的 `storedMessage{MessageID, SenderUID, ClientMsgNo, Payload}` 组织。

建议：

- `MessageSeq` 仍然不在日志 payload 中重复编码，读取时由 `offset + 1` 恢复
- `PayloadHash` 作为内部字段随 payload 一起编码
- 统一由一套 `encodeMessage` / `decodeMessageView` / `decodeMessage` 负责

## 读取路径

### `Fetch`

`Fetch` 基于 committed offsets 读取日志，再解码为完整 `Message`。

恢复规则：

- `MessageSeq = record.Offset + 1`
- 其他字段全部从消息 payload 解出

`Fetch` 不得暴露 `HW` 之外的未提交尾部。当前 `channellog.Fetch` 已按 `state.HW` 做 committed fencing，新模型必须保持这一点。

### `Store.LoadMsg` / Range APIs

本地读取接口同样统一为完整 `Message`。

这样历史读取与集群读取不再出现两套消息语义。

本地读取同样必须继续受 checkpoint `HW` 约束，避免把冲突恢复前的 dirty tail 误当成历史消息。

## realtime 投递视图

新增稳定转换函数，例如：

```go
func MessageToRecvPacket(msg channellog.Message, recipientUID string) *wkframe.RecvPacket
```

语义：

- 默认字段直接从 `Message` 复制
- `MessageID` 从 `uint64` 转成 `int64`
- 个人频道根据接收者视角改写 `ChannelID`
- `Timestamp` 直接使用 `Message.Timestamp`

这样 `buildRealtimeRecvPacket` 的责任变成“视图变换”，不再承担“重建消息”。

## ISR 冲突语义

消息模型收敛为完整消息后，仍然必须保留并显式承认 ISR 冲突语义：

1. follower 复制时如果发现和 leader 在某个 offset 之后发生分歧，leader 会要求 follower 先截断到匹配点，再继续复制
2. 节点重启时如果本地 `LEO > checkpoint.HW`，恢复过程会主动截断 dirty tail 到 `HW`
3. `ApplyFetch` 明确禁止把日志截断到 `HW` 以下，因此已提交前缀不能被冲突破坏

这意味着：

- 允许消失的只有未提交消息
- 不允许消失的是任何已经暴露给业务的 committed message
- `messageSeq` 的业务可见性以 committed offset 为准，而不是 append 顺序为准

换句话说，本设计不会试图“屏蔽 ISR 冲突”，而是把冲突后的真值边界明确限定为 committed prefix。

## `dispatchLate` 语义修正

`internal/runtime/delivery/actor.dispatchLate` 当前注释基于“日志里无法还原完整消息”的前提。

新模型下这个前提失效，注释和实现语义都应修正为：

- durable log 已可表达完整消息
- late dispatch 只是处理乱序已提交消息
- 不再把当前行为描述成因模型缺失而退化的 best-effort

## 错误处理

- `Message.MessageID` 生成失败或编码失败时，`Send` 直接失败
- ISR append 成功前，不写入幂等状态
- ISR append 成功后若幂等状态写入失败，仍按当前一致性语义返回错误，由调用方决定重试
- `Fetch` / `LoadMsg` 解码失败时返回显式错误，不返回半消息对象
- `MessageID` 到 `RecvPacket.MessageID(int64)` 的转换需要保证生成器不会溢出 `int64`；若后续生成器语义变化，应显式加边界保护
- 如果 ISR 冲突导致 follower 或恢复流程截断 dirty tail，被截断记录必须被视为“未提交草稿”，而不是“已存在后又回滚的消息”

## 测试设计

### `pkg/storage/channellog`

需要补或改以下测试：

- 完整消息 codec round-trip
- `Send` 返回完整已提交消息
- `Fetch` 返回完整消息
- `LoadMsg` / `LoadNextRangeMsgs` / `LoadPrevRangeMsgs` 返回完整消息
- 幂等命中返回同一消息语义
- 冲突截断后未提交尾部不可见
- 恢复截断后未提交尾部不可见

完整消息断言至少覆盖：

- `Framer`
- `Setting`
- `MsgKey`
- `Expire`
- `ClientSeq`
- `ClientMsgNo`
- `StreamNo`
- `StreamID`
- `StreamFlag`
- `Timestamp`
- `ChannelID`
- `ChannelType`
- `Topic`
- `FromUID`
- `Payload`
- `MessageID`
- `MessageSeq`

### `internal/usecase/message`

需要改动：

- `Send` 用例测试改为断言提交给异步投递链路的是 durable `Message`
- 补 `Timestamp` 稳定性测试，确保提交后实时投递不重新取当前时间
- 补“只在 committed 后才进入投递链路”的测试

### `internal/app` / realtime 投递

需要补或改：

- `Message -> RecvPacket` 转换测试
- 个人频道 `ChannelID` 按接收者视角改写测试
- 远端 owner 提交完整消息测试
- 冲突恢复后不会对已被截断的未提交消息继续投递的测试

## 分步实施建议

建议按以下顺序落地：

1. 引入 `channellog.Message` 并完成新 codec
2. 调整 `SendRequest` / `SendResult`，让发送链路返回完整已提交消息
3. 统一 `Fetch` 与 `Store` 读取接口返回完整消息
4. 把 `CommittedMessageEnvelope` 收敛为 durable message 输入
5. 把 realtime `RecvPacket` 构造改成 `Message -> RecvPacket`
6. 清理旧注释、旧瘦消息模型与重复字段镜像

## 结论

`pkg/storage/channellog` 的日志项必须提升为完整消息对象，而不是消息 payload 的薄包装。

收敛后的原则是：

- durable truth 只有一套：`channellog.Message`
- realtime packet 是这套 truth 的投递视图
- 热路径不回库
- 历史读、实时投递、故障恢复围绕同一消息模型展开

这能把当前“日志模型、读取模型、投递模型”三套近似对象收回到一套稳定边界上。
