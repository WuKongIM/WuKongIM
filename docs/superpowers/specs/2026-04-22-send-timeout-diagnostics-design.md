# Send Timeout Diagnostics Design

## 概述

本设计面向发送消息偶发出现 `channel: not leader` 后又在约 20 秒后以 `context deadline exceeded` 失败的场景，目标是在不改变 durable send 语义的前提下，给 send path 补齐结构化 DEBUG 诊断点，并提供一个稳定的三节点 `go test` 复现入口。

## 目标

- 精确区分请求卡在 refresh、forward、remote append 还是 leader quorum wait。
- 在三节点测试环境下稳定复现 send timeout，便于本地和 docker 集群对照调试。
- 保持现有错误语义与协议返回不变。

## 非目标

- 不修改 durable commit 语义。
- 不引入新的重试策略。
- 不修改 channel 元数据或 ISR 复制协议。

## 方案

### 方案 A：只在 message retry 层加日志

优点：改动最小。

缺点：只能看到 refresh 前后，无法判断是否卡在 forward 或 quorum wait。

### 方案 B：send path 分层加结构化 DEBUG 日志，并补可复现测试（推荐）

在 `message retry`、`app channel cluster forward`、`node channel append RPC`、`replica append timeout` 四个边界增加结构化 DEBUG 日志；同时新增一个三节点集成测试，通过缩短 gateway send timeout、维持 `MinISR=3`、停止一个 follower 来稳定触发 quorum wait timeout。

优点：定位路径完整、日志噪音可控、可直接用于 docker 三节点复现。

缺点：需要跨 `internal/usecase/message`、`internal/app`、`internal/access/node`、`pkg/channel/replica` 多处落点。

## 推荐方案

采用方案 B。

## 设计细节

### 1. message retry 诊断

在 `internal/usecase/message/retry.go` 中为以下节点补充 DEBUG/ERROR 字段：

- 初次 append 失败时的错误类型与上下文。
- refresh 返回后的 leader、epoch、replica/isr/minISR、lease 剩余时间。
- refresh 后第二次 append 失败时的错误及总耗时。

### 2. app channel cluster forward 诊断

在 `internal/app/channelcluster.go` 中记录：

- 本地 append 命中 `ErrNotLeader` 后的 meta 快照。
- 是否决定 forward、目标 leader、forward 耗时、结果错误。
- 当没有可用 leader 或本地仍被识别为 leader 时，明确记录未 forward 原因。

### 3. node remote append 诊断

在 `internal/access/node/channel_append_rpc.go` 中记录：

- 远端 leader 收到 append 时的初始状态。
- 因 `ErrNotLeader`/`ErrStaleMeta` 触发 refresh 的上下文。
- refresh 后是本地重试成功、再次重定向、还是最终失败。

### 4. replica quorum wait timeout 诊断

在 `pkg/channel/replica/append.go` 中，当 append 等待期间 `ctx.Done()` 触发时，记录：

- channel key、local node、role、leader、epoch。
- `CommitReady/HW/LEO/CheckpointHW/MinISR/ISR`。
- 当前 waiter 的 `target/rangeStart/rangeEnd`。
- 当前 `progress` 快照，直接判断卡在哪个 follower。

### 5. 可复现三节点测试

在 `internal/app/multinode_integration_test.go` 新增一个定向测试：

- 使用三节点 harness。
- 把 `Gateway.SendTimeout` 调小到便于快速复现。
- 建一个 `MinISR=3` 的 person channel 并预热 meta。
- 停掉一个 follower，保持 runtime meta 仍为三副本 ISR。
- 通过 gateway 发送消息，断言返回 `ReasonSystemError` / `context deadline exceeded` 语义。
- 该测试的主要目的不是验证日志文本，而是稳定触发 timeout，使新增 DEBUG 日志在 `go test -v` 或 docker 集群中可见。
