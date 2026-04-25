# pkg/transport 压力测试设计

## 1. 背景与目标

`pkg/transport` 当前已经有较完整的单元测试，覆盖了 frame 编解码、`MuxConn`、`Pool`、`Server`、`RPCMux` 与优先级 writer 的基础行为。但这些测试主要验证功能正确性，尚未提供一条稳定的混合负载性能护栏。

结合 `docs/raw/transport-pipeline-priority-refactor.md` 中“Raft 流量 + RPC 流量并发，验证 P0 延迟不受影响”的目标，本次先补一条默认关闭、按环境变量显式开启的压力测试，验证在单节点集群语义下、单个 transport server 的混合负载场景中，高优先级发送不会因为 RPC 负载而明显退化。

本设计的首要目标不是追求极限吞吐，而是建立一个可复现的回归护栏：

- `PriorityRaft` 发送链路在 `PriorityRPC` 压力下保持低延迟；
- `Send` / `RPC` 路径在持续并发期间不出现非预期错误；
- 测试默认不干扰日常开发与 CI，仅在显式开启时运行；
- 输出足够的统计信息，便于后续版本间对比。

## 2. 范围

### 2.1 本次覆盖

新增 `pkg/transport/stress_test.go`，提供一个主压力测试：

- `TestTransportStressRaftSendUnderRPCLoad`

测试使用真实 transport 组件拼装：

- `Server`
- `Pool`
- `Client`
- `MuxConn`
- frame 编解码与 RPC pending 路径

测试场景：

- 一组 worker 持续通过 `PriorityRaft` 对服务端执行 `Send`
- 一组 worker 持续通过 `PriorityRPC` 对服务端执行 `RPC`
- 服务端为普通消息注册轻量 handler，为 RPC 注册可控延迟 handler
- 统计 `PriorityRaft` 端到端延迟分位数，确认其在混部下仍满足预算

### 2.2 暂不覆盖

首版不纳入以下内容：

- benchmark（`Benchmark*`）与历史版本性能对比
- 多节点、多 server、真实跨机网络抖动场景
- 复杂背景噪声，例如连接抖动、突发重连、半开连接恢复
- 长时间 soak test（分钟级 / 小时级）

这些内容后续可以独立补充，但不应阻塞首条混合负载护栏落地。

## 3. 设计原则

- **真实路径优先**：不引入 mock transport 主链路，直接使用真实 `Server + Pool + Client`，确保覆盖优先级队列、连接复用、RPC 回包与服务端分发。
- **默认关闭**：遵循仓库既有 stress test 风格，未设置环境变量时 `t.Skip`。
- **单职责**：首版只做一个主压力测试，避免测试框架过度抽象。
- **统计可解释**：聚焦 `PriorityRaft` 的 `p99` 作为硬门槛，其余分位数与负载信息作为日志输出。
- **单节点集群语义**：测试描述统一面向单节点集群，不引入绕过集群语义的特殊分支。

## 4. 测试结构

### 4.1 文件布局

新增文件：

- `pkg/transport/stress_test.go`

文件内维持单文件实现，主要包含四类内容：

1. `transportStressConfig`
   - 加载环境变量
   - 提供默认值与参数校验
2. `transportLatencySummary` / `transportStressStats`
   - 收集 `raft send` 与 `rpc` 延迟样本
   - 汇总 `count / p50 / p95 / p99 / max`
   - 记录错误数与操作总数
3. `transportStressHarness`
   - 启动本地 `Server`
   - 注册普通消息与 RPC handler
   - 创建 `raftPool`、`rpcPool` 以及对应 `Client`
   - 负责资源清理
4. `TestTransportStressRaftSendUnderRPCLoad`
   - 协调 worker 生命周期
   - 汇总统计结果
   - 执行最终断言并输出测试日志

### 4.2 核心组件关系

服务端：

- 启动一个本地 `Server`
- 注册一个测试专用消息类型（例如仅在测试文件内定义的常量）
- 该消息 handler 负责接收 `PriorityRaft` 发送的数据、解码时间戳并记录到达延迟
- 注册一个 `RPC` handler，按配置注入少量固定延迟，再返回响应

客户端：

- 创建 `raftPool`，`DefaultPri = PriorityRaft`
- 创建 `rpcPool`，`DefaultPri = PriorityRPC`
- 使用固定 discovery 将测试 node id 指向本地服务端监听地址
- 可直接使用 `Client` 包装调用，也可直接经由 `Pool`；推荐复用 `Client` 以贴近上游调用习惯

worker：

- `raft` workers：循环发送小体积消息，消息体包含发送时间戳与序号
- `rpc` workers：循环发起 RPC，确保服务端存在持续 RPC 压力
- 所有 worker 通过同一个 `context.WithTimeout` 受控退出

## 5. 延迟测量方案

### 5.1 `raft send latency`

主指标定义为：

- 从客户端准备调用 `Send` 前记录 `sentAt`
- 在服务端普通消息 handler 收到消息时计算 `time.Since(sentAt)`

这样得到的延迟包含：

- 客户端进入 writer 队列的等待
- 实际写 socket 的时间
- 服务端读 frame 与 dispatch 的时间

但不包含服务端 handler 中额外的业务处理逻辑，因为该 handler 仅做解码与采样上报。这样更能反映 transport 库本身的优先级调度表现。

### 5.2 `rpc latency`

辅助指标定义为：

- 客户端 `RPC` 调用开始到收到响应的往返耗时

其目的主要是：

- 验证压力确实建立起来了
- 为日志输出提供负载背景
- 观察混部时 RPC 自身延迟的变化

`rpc latency` 不是首版的硬门槛，但会纳入统计输出。

### 5.3 统计方法

测试结束后统一计算：

- `count`
- `p50`
- `p95`
- `p99`
- `max`

首版直接保留全量 `time.Duration` 样本，原因：

- 默认时长仅数秒
- 逻辑清晰，易读易校验
- 首版重点是建立护栏，不需要先引入 reservoir sampling 等复杂度

如果后续样本规模显著增大，再演进为限长采样或分桶统计。

## 6. 环境变量与默认值

建议新增以下环境变量：

- `WK_TRANSPORT_STRESS`
  - 是否开启压力测试；默认关闭
- `WK_TRANSPORT_STRESS_DURATION`
  - 压测时长；默认 `5s`
- `WK_TRANSPORT_STRESS_RAFT_WORKERS`
  - `PriorityRaft` worker 数；默认 `max(2, GOMAXPROCS/2)`
- `WK_TRANSPORT_STRESS_RPC_WORKERS`
  - `PriorityRPC` worker 数；默认 `max(2, GOMAXPROCS)`
- `WK_TRANSPORT_STRESS_RPC_DELAY`
  - 服务端 RPC handler 的固定延迟；默认 `2ms`
- `WK_TRANSPORT_STRESS_SEED`
  - 固定随机种子；默认一个稳定常量，便于复现
- `WK_TRANSPORT_STRESS_P99_BUDGET`
  - `raft send` 的 `p99` 预算；默认 `20ms`

参数校验要求：

- `duration > 0`
- `raft workers > 0`
- `rpc workers > 0`
- `rpc delay >= 0`
- `p99 budget > 0`

## 7. 通过/失败判定

测试必须同时满足：

- `raft send` 无错误
- `rpc` 无错误
- `raft send` 样本数达到最小阈值
- `rpc` 样本数达到最小阈值
- `raft send p99 <= WK_TRANSPORT_STRESS_P99_BUDGET`

关于最小样本量，首版建议使用固定下限，例如：

- `raft count >= 1000`
- `rpc count >= 1000`

选择 `p99` 作为硬门槛的理由：

- `p50` 对优先级退化不敏感
- `max` 波动过大，不适合作为稳定断言
- `p99` 最能反映“少数慢请求是否被混部显著拖慢”

如果样本量不足，则直接失败并打印当前配置，避免“压力没打起来也算通过”。

## 8. harness 细节

### 8.1 消息格式

普通消息体仅携带最小必要字段：

- `sentAtUnixNano`
- `sequence`

首版可直接采用定长字节编码（例如 16 字节：8 字节时间戳 + 8 字节序号），避免额外引入 JSON 或其他编码开销。

RPC payload 无需复杂结构，保持小体积即可，只需保证：

- 能持续驱动服务端 `RPC` handler
- 可在响应中带回简单确认字段，避免测试误判

### 8.2 服务端 handler 约束

普通消息 handler：

- 读取消息体
- 计算 `latency`
- 记录统计
- 不执行任何额外慢逻辑

RPC handler：

- 按 `rpc delay` 配置执行 `time.Sleep`
- 返回小响应体
- 不额外引入不可控阻塞

### 8.3 并发控制

主测试使用：

- `context.WithTimeout`：控制整体压测时长
- `sync.WaitGroup`：等待所有 worker 退出
- 原子计数 / 轻量锁：汇总错误数与样本

如果任一 worker 发生非预期错误：

- 记录首个错误
- 允许其他 goroutine 在 `ctx.Done()` 后自然退出
- 最终统一 `Fatal`

### 8.4 资源清理

`transportStressHarness.Close()` 负责统一清理：

- `raftClient.Stop()` 或 `raftPool.Close()`
- `rpcClient.Stop()` 或 `rpcPool.Close()`
- `server.Stop()`

测试退出前确保 listener、连接与 goroutine 均可收敛，避免压力测试污染后续用例。

## 9. 日志输出

测试结束时输出一条可读的汇总日志，至少包含：

- `seed`
- `duration`
- `raft_workers`
- `rpc_workers`
- `rpc_delay`
- `raft_count`
- `raft_p50/p95/p99/max`
- `rpc_count`
- `rpc_p50/p95/p99/max`

输出目标：

- 方便本地调参
- 方便后续版本间手工对比
- 当断言失败时能快速定位是样本不足、整体压力太低，还是高优先级延迟真的退化

## 10. TDD 落地顺序

实现阶段遵循 test-first：

1. 先写配置加载与默认 skip 行为的测试（如需要拆 helper）
2. 先写延迟统计 helper 的单元测试
3. 再写主压力测试骨架，并先验证其在未实现完整 helper 时失败
4. 最后补齐 harness、统计、断言与日志输出

注意：压力测试本身默认 `skip`，因此实现时应尽量把可确定的逻辑拆成普通单元测试 helper，避免所有逻辑都只能依赖慢测试验证。

## 11. 验证方式

开发时至少验证：

- `go test ./pkg/transport/...`
- 显式开启压力测试后执行 `go test -run TestTransportStressRaftSendUnderRPCLoad ./pkg/transport -count=1`

在手工压测场景下可使用环境变量，例如：

```bash
WK_TRANSPORT_STRESS=1 \
WK_TRANSPORT_STRESS_DURATION=5s \
WK_TRANSPORT_STRESS_RPC_DELAY=2ms \
WK_TRANSPORT_STRESS_P99_BUDGET=20ms \
go test -run TestTransportStressRaftSendUnderRPCLoad ./pkg/transport -count=1
```

## 12. 后续演进方向

本设计落地后，可按需要继续补充：

- `pkg/transport/benchmark_test.go`：测 `Send` QPS、RPC RTT、`alloc/op`
- 多 server / 多 node 压测场景
- 更严格的优先级隔离测试，例如放大 RPC handler 阻塞或增加 bulk 流量
- 长时间 soak test 与关闭收敛测试

这些后续工作不影响本次目标：先为 `pkg/transport` 建立一条可靠的混合负载延迟护栏。
