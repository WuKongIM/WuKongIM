# Send Stress Design

## Overview

本设计为 `WuKongIM v3.1` 增加一条真实发送链路的长压测试，只覆盖发送主路径：

- `WKProto/TCP`
- `internal/gateway`
- `internal/access/gateway`
- `internal/usecase/message`
- `pkg/storage/channellog` / 集群提交
- `SendAck`

本次只压单聊发送，不把收件端实时投递、`RecvPacket`、`RecvAck` 作为成功条件。

测试必须满足两条原则：

- 压测走真实装配，不退化成 mock usecase 或只压 `message.App.Send`
- 如果压测暴露程序缺陷，先修生产代码，不为了让测试通过而放宽断言或删除真实校验

## Goals

- 为真实发送入口建立 env-gated 的稳定长压测试
- 让发送链路在三节点稳定集群下经受高并发 `SendPacket -> SendAck` 压力
- 对每条成功发送建立可追踪记录，并在压测结束后做全量持久化提交校验
- 输出吞吐、错误率、延迟分位数和持久化校验结果，便于回归比较

## Non-Goals

- 本次不覆盖群聊发送压测
- 本次不把收件端实时投递纳入成功条件
- 本次不在压测过程中加入 leader 切换、节点重启、网络扰动
- 本次不把长压测试纳入默认 `go test ./...`
- 本次不把 benchmark `ns/op` 作为主要输出；关注正确性和稳定吞吐

## Existing Context

- `internal/app/multinode_integration_test.go` 已提供真实三节点应用装配 `threeNodeAppHarness`
- 现有 harness 已能启动真实 cluster、gateway、api，并支持真实 WKProto 客户端连接
- `internal/app/conversation_sync_stress_test.go` 已形成仓库内 env-gated stress 风格，可复用配置、统计和错误报告模式
- 当前发送用例已经明确“发送成功只以 durable commit + SendAck 为准”

## Recommended Approach

新增独立文件 `internal/app/send_stress_test.go`，复用 `threeNodeAppHarness` 作为真实装配基座，增加发送压测专用配置、预热、发送 worker、指标汇总和全量持久化校验。

选择独立测试文件而不是复用 `conversation_sync_stress_test.go` 的原因：

- 职责单一，问题归因直接落在发送链路
- 不混入 `/conversation/sync` 语义和读取流量
- 便于后续单独扩展群聊发送、故障扰动和 soak 变体

## Architecture

### 1. Test Entry

测试入口放在：

- `internal/app/send_stress_test.go`

主测试建议形态：

- `TestSendStressThreeNode`

只有在显式设置 `WK_SEND_STRESS=1` 时才执行；默认 `go test ./...` 跳过。

### 2. Real Topology

测试固定使用稳定三节点集群：

- 节点数固定为 `3`
- 单 group
- 不在压测期间停节点、切主、重启

这样可以把失败信号尽量约束到发送链路本身，而不是故障恢复路径。

### 3. Message Shape

仅覆盖 `person` 单聊：

- 发送者 UID 集合：`stress-sender-%03d`
- 接收者 UID 集合：`stress-recipient-%03d`
- channel 统一使用 canonical person channel 语义
- 每条消息使用唯一 `client_msg_no`

消息负载使用固定前缀加递增序号，便于持久化回读校验。

### 4. Success Contract

每次发送的在线成功条件只有一个：

- 在超时内收到 `ReasonSuccess` 的 `SendAck`

并且：

- `message_id != 0`
- `message_seq != 0`

压测结束后的离线成功条件：

- 对全部成功发送记录，按 `channel + message_seq` 从 `channellog` 读回消息
- 核对 `payload`
- 核对 `from_uid`
- 核对 `client_msg_no`
- 核对 `channel_id/channel_type`

如果任一记录 `SendAck` 成功但持久化校验失败，测试直接失败。

## Components

### 1. Config Loader

新增独立 env 前缀：

- `WK_SEND_STRESS`
- `WK_SEND_STRESS_DURATION`
- `WK_SEND_STRESS_WORKERS`
- `WK_SEND_STRESS_SENDERS`
- `WK_SEND_STRESS_MESSAGES_PER_WORKER`
- `WK_SEND_STRESS_DIAL_TIMEOUT`
- `WK_SEND_STRESS_ACK_TIMEOUT`
- `WK_SEND_STRESS_SEED`

默认值原则：

- 默认关闭
- 默认规模控制在开发机可运行的分钟级
- 参数非法时直接 `t.Fatalf`

### 2. Dataset Preload

预热阶段职责：

- 生成发送者/接收者 UID 映射
- 为每对单聊 channel 写入 `ChannelRuntimeMeta`
- 把 channel meta 刷新到各节点缓存

预热不预发消息；本压测只关心发送实时写入和 ack 闭环。

### 3. Sender Workers

每个 worker：

- 建立真实 TCP WKProto 连接
- 先发送 `ConnectPacket`
- 校验 `Connack`
- 按循环发送 `SendPacket`
- 每发一条立即阻塞等待对应 `SendAck`
- 记录单次耗时和发送结果

worker 使用固定 sender 身份，避免每条消息都建连。

### 4. Result Recorder

需要记录：

- worker id
- sender uid
- recipient uid
- canonical channel id
- client sequence
- client msg no
- payload
- ack message id
- ack message seq
- ack latency
- owner/target node

这些记录在压测结束后用于全量持久化校验。

### 5. Commit Verifier

校验器遍历全部成功记录：

- 优先从 owner 节点 `channellog.Store` 读取对应 `message_seq`
- 必要时在 replica 节点重复确认
- 检查读取到的消息与发送记录完全匹配

为了避免时序误判，读取阶段允许使用 `require.Eventually` 在有限窗口内等待提交可见。

## Data Flow

1. 测试启动 `threeNodeAppHarness`
2. 预热单聊 channel runtime meta
3. 每个 worker 建立到某个节点的真实 WKProto 连接
4. worker 发送 `SendPacket`
5. gateway 把请求路由到真实发送用例
6. `message.Send` 调用 `channellog` / cluster durable append
7. 发送方收到 `SendAck`
8. 测试记录 `message_id/message_seq`
9. 压测结束后，校验器从 `channellog` 全量读取并比对

## Error Handling

以下任一情况都计为失败并保留失败样本：

- TCP 连接失败
- `ConnectPacket` 后未收到成功 `Connack`
- `SendPacket` 写失败
- 读取超时
- 返回的不是 `SendAck`
- `SendAck.ReasonCode != ReasonSuccess`
- `message_id == 0`
- `message_seq == 0`
- 持久化回读不到消息
- 回读消息字段与发送记录不一致

错误处理原则：

- 发送阶段失败直接计数，不伪造成功
- 测试结尾 `require.Zero(failed)`，不允许“有失败但整体还能接受”
- 如果测试先暴露生产缺陷，优先修生产代码，测试只保留真实期望

## Metrics And Output

测试日志输出：

- duration
- workers
- senders
- total sends
- success sends
- failed sends
- qps
- error rate
- p50
- p95
- p99
- max latency
- verification count
- verification failures

额外输出有限数量失败样本，便于快速定位。

## Verification Strategy

实现完成后至少应执行：

- 目标测试的默认关闭验证
- 小规模显式开启验证
- 若压测中发现缺陷，先补对应最小回归测试，再修代码，再回跑发送压测

建议命令：

```bash
go test ./internal/app -run TestSendStressThreeNode -count=1
WK_SEND_STRESS=1 WK_SEND_STRESS_DURATION=3s WK_SEND_STRESS_WORKERS=4 WK_SEND_STRESS_SENDERS=8 WK_SEND_STRESS_MESSAGES_PER_WORKER=25 go test ./internal/app -run TestSendStressThreeNode -count=1 -v
```

## Rollout

该测试作为 env-gated 长压测试存在：

- PR 默认不跑
- 开发者手动验证时显式开启
- nightly / 发布前 soak 可按更大参数放大

## Acceptance

设计完成后的实现必须满足：

- 测试走真实 `WKProto -> Gateway -> SendAck`
- 只覆盖 `person` 单聊
- 默认关闭，显式 env 开启
- 压测期间不混入故障扰动
- 每条发送都要求成功 `SendAck`
- 压测结束后对全部成功消息做持久化提交校验
- 发现程序 bug 时先修生产代码，而不是改测试降低标准
