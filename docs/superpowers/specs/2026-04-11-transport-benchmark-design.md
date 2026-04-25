# pkg/transport Benchmark 设计

## 1. 目标

在现有 `pkg/transport/stress_test.go` 提供混合负载延迟护栏的基础上，补充一组偏微基准的 benchmark，用来回答两个更细的问题：

- 单次 `Send` 的端到端平均成本是多少；
- 单次 `RPC` 的端到端平均成本是多少；
- 小 payload 与稍大 payload 对 `ns/op` 与 `alloc/op` 的影响如何。

本次 benchmark 的首要目标是为开发阶段提供稳定、可重复的版本内/版本间对比数据，而不是构建更复杂的混合负载场景。延迟分位数与优先级隔离仍主要由 `stress_test.go` 负责。

## 2. 范围

本次新增文件：

- `pkg/transport/benchmark_test.go`

首版只提供两组真实调用路径的端到端 benchmark：

- `BenchmarkTransportSend`
- `BenchmarkTransportRPC`

每组 benchmark 先覆盖两个 payload 大小：

- `16B`
- `256B`

## 3. 设计原则

- **真实路径优先**：直接走 `Client -> Pool -> MuxConn -> Server`，不做 mock。
- **首版最小化**：只做 `Send` / `RPC` 两组 benchmark，不在首版加入 `MuxConn` 内部剖面或混合负载 benchmark。
- **避免测量污染**：在 `b.ResetTimer()` 前完成 server、pool、client 初始化；循环内不动态构造 payload。
- **可扩展**：后续如果需要，再增加 `RunParallel`、`MuxConn` 级 benchmark、或混合负载 benchmark。

## 4. 结构设计

### 4.1 `BenchmarkTransportSend`

- 复用与 `stress_test.go` 一致的本地 harness 思路；
- 服务端注册一个普通消息 handler，只做轻量接收与 release，不做重逻辑；
- benchmark 循环中重复调用 `Client.Send`；
- 使用 `b.ReportAllocs()` 输出 `alloc/op`。

### 4.2 `BenchmarkTransportRPC`

- 同样复用本地 harness；
- 服务端 `RPC` handler 直接回 `ok`；
- benchmark 循环中重复调用 `Client.RPC`；
- 使用 `b.ReportAllocs()` 输出 `alloc/op`。

## 5. 预期输出

通过 `go test -bench . ./pkg/transport -run '^$'` 或定向 `-bench BenchmarkTransport` 获取：

- `ns/op`
- `alloc/op`
- `B/op`

对 `Send` 而言，主要用来观察不同 payload 大小下的单次发送成本。
对 `RPC` 而言，主要用来观察完整请求-响应回路的平均成本。

## 6. 暂不纳入

首版 benchmark 不包含：

- `RunParallel` 并发 benchmark
- `MuxConn.Send` / `MuxConn.RPC` 的内部剖面 benchmark
- mixed load benchmark
- 基于 p50/p95/p99 的分位数统计

这些内容后续可以独立追加，不阻塞首版落地。
