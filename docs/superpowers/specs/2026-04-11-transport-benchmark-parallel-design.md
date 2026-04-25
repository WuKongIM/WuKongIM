# pkg/transport 并发 Benchmark 设计

## 1. 目标

在已有串行微基准 `BenchmarkTransportSend` 与 `BenchmarkTransportRPC` 基础上，补充 `RunParallel` 版 benchmark，回答两个并发场景问题：

- 多 goroutine 并发 `Send` 时，`pkg/transport` 的平均成本与分配行为如何变化；
- 多 goroutine 并发 `RPC` 时，完整请求-响应回路的平均成本与分配行为如何变化。

本次目标仍然是偏开发期回归分析，而不是建立新的压力测试护栏。并发版 benchmark 主要用于观察串行与并发下的成本差异，以及识别是否存在明显的共享路径瓶颈。

## 2. 范围

继续修改：

- `pkg/transport/benchmark_test.go`

新增两组并发 benchmark：

- `BenchmarkTransportSendParallel`
- `BenchmarkTransportRPCParallel`

每组仍保留两个 payload 档位：

- `16B`
- `256B`

## 3. 设计原则

- **复用现有 harness**：不再造新拓扑，直接复用 benchmark harness。
- **使用标准 `RunParallel`**：交给 `testing.B` 统一控制 worker 数量，避免自写并发驱动器。
- **最小限度回压**：并发 `Send` 仍保留必要的 in-flight 限制，避免 benchmark 退化成单纯测 `ErrQueueFull`。
- **结果可对比**：命名和 payload 档位保持与串行 benchmark 一致，方便直接比较。

## 4. 结构设计

### 4.1 `BenchmarkTransportSendParallel`

- 使用一个本地 harness；
- 预分配固定 payload；
- 在 `b.RunParallel` 回调里持续调用 `Client.Send`；
- 沿用轻量回压等待，确保服务端能跟上而不频繁报 `queue full`；
- 输出 `ns/op`、`B/op`、`alloc/op`。

### 4.2 `BenchmarkTransportRPCParallel`

- 同样使用一个本地 harness；
- 在 `b.RunParallel` 回调里持续调用 `Client.RPC(context.Background(), ...)`；
- 服务端 RPC handler 仍返回固定 `ok`；
- 输出 `ns/op`、`B/op`、`alloc/op`。

## 5. 预期用途

这组并发 benchmark 主要用于：

- 对比串行/并发下 `Send` 与 `RPC` 的成本变化；
- 观察共享连接、共享 writer、共享 pending map 在并发下的额外开销；
- 为后续是否值得增加更细粒度 benchmark（如 `MuxConn` 级别）提供依据。

## 6. 暂不纳入

本次不额外引入：

- 自定义 worker 数配置
- 混合 `Send + RPC` 的并发 benchmark
- 新的 stress 级并发指标
- 分位数统计
