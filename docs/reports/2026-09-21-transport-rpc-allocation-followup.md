**RPC 排队分配优化后续验证 — 2026-09-21**

针对 [跨 ECS 测试](2026-09-21-transport-cross-host-validation.md) 发现的并发退化，本轮定位并削减了服务端排队生命周期的重复分配：完整 RPC 从 51 次降为 46 次分配，64B 请求分配字节减少约 14%；Linux 小包并发吞吐在延长窗口对照中改善约 8%。本轮未重新购买服务器、未复测跨 ECS；不能据此宣称此前云上 19%/29% 的退化已完全修复。

基线为 `de625f971cc97621ee07c129ede904f887aa7bb6`（包含已测候选代码及云上报告），实验版本只改变队列到期监听。所有逐次结果与证据校验值见 [机器可读记录](assets/transport-rpc-allocation-2026-09-21.json)。

**证据与修复**

同一候选版本的 RequestBudgets 开关对照中，macOS 小包 16 并发均约 5.1µs/op、51 allocs/op，不能用这组开关对照解释此前云上基线/候选之间约 19% 的吞吐差异。此开关只控制协商及线协议预算传播，两种模式仍都执行服务端排队和执行预算；这不等于这些预算没有成本。

macOS 和 Linux 的 alloc pprof 均将 context 传播、Done channel、deadline 和 timer 列为主要分配来源。Linux 原始样本中 `Service.Enqueue` 累积分配约占 35%；代码路径为 `WithTimeout(requestContext)` 再 `AfterFunc(queueContext)`。即使调用方截止时间更早，仍创建中间取消 context 及其子节点、channel 和回调关系。CPU 剖析仍以系统调用、时钟和调度为主，不能断言唯一 CPU 瓶颈已找到。

修复直接监听原请求 context，仅当排队截止时间更早或调用方没有截止时间时，使用独立队列定时器。队列锁仍串行化取消、到期与出队；到期只释放该请求，不能取消父 context。出队、共享执行池准入和 handler 开始前的截止时间检查保留，已开始写操作的独立执行语义不变。没有增加配置开关或变更线协议。

新增真实 TCP 的 `BenchmarkTransportBudget`，固定 16 个预热连接、1/16 个调用者、64/65537B 载荷；服务并发 64、排队预算 5s、执行预算 30s，每调用超时 2s，Observer 关闭，逐次核对响应。分配包括客户端与服务端；并行 ns/op 是吞吐倒数，不是请求 RTT。修复前已运行 64B 完整路径的分配验收：12 个样本均为 51，超过 48 allocs/op 上限；修复后 12 个样本均为 46，验收通过。该验收是本轮诊断的性能判据，未接入常规 CI 时间阈值。

**实测结果**

Go 1.25.11、GOMAXPROCS=4。macOS 为 Apple M4；Linux 为同机 Docker Desktop ARM64 VM，绑定 CPU 0–3、6GiB 内存，运行静态 ARM64 测试二进制。容器基础镜像为 amd64 Debian rootfs，但测试程序是原生 ARM64 且不依赖该镜像用户态程序；这仍不能替代 x86 ECS 环境。每项 1s、三轮，下表为中位数，预算 true/false 均为调用方开关。

| 环境 | 字节 / 并发 / 预算 | µs/op：前 → 后 | B/op：前 → 后 |
|---|---|---:|---:|
| macOS | 64 / 1 / false | 25.892 → 24.689 | 3,346 → 2,850 |
| macOS | 64 / 1 / true | 25.893 → 24.852 | 3,346 → 2,890 |
| macOS | 64 / 16 / false | 5.156 → 4.900 | 3,347 → 2,851 |
| macOS | 64 / 16 / true | 5.123 → 4.923 | 3,347 → 2,890 |
| Linux | 64 / 1 / false | 11.965 → 13.147 | 3,346 → 2,849 |
| Linux | 64 / 1 / true | 10.715 → 11.767 | 3,346 → 2,889 |
| Linux | 64 / 16 / false | 4.417 → 3.704 | 3,347 → 2,850 |
| Linux | 64 / 16 / true | 4.084 → 3.639 | 3,347 → 2,890 |
| Linux | 65537 / 1 / false | 100.913 → 95.988 | 232,881 → 232,607 |
| Linux | 65537 / 1 / true | 100.585 → 97.511 | 233,747 → 232,752 |
| Linux | 65537 / 16 / false | 74.878 → 74.474 | 231,575 → 230,976 |
| Linux | 65537 / 16 / true | 73.828 → 75.322 | 231,580 → 230,930 |

所有以上用例均为 51 → 46 allocs/op。小包字节分配下降稳定，大包由 payload 复制主导，不能外推相同百分比收益。1s Linux 样本的小包串行耗时上升约 10%，并发改善；为检查波动，额外执行三轮交错、每项 5s 的预算开启对照，同时比较仅移除一次重复检查的实验版：

| Linux 64B，三轮中位数 | 基线 | 保留的修复 | 实验版（未采用） |
|---|---:|---:|---:|
| 串行 µs/op | 11.886 | 12.060 | 12.062 |
| 16 并发 µs/op | 3.992 | 3.693 | 3.633 |

保留修复的并发吞吐提升约 8.1%；串行中位数增加约 1.5%，各轮范围重叠（基线 11.187–12.299µs，修复 10.973–12.683µs）。不能把延长窗口解释为串行必然不退化，也没有看到删除重复检查的明确额外收益，因此保留较小改动。Linux 大包并发没有证实收益；小包分配降低与并发改善是本轮确认的范围。

**验证和复现**

- 全量显式包路径单元测试通过，212 个包。
- transport 全包以及 cluster/net 的 integration + race 通过。
- 队列到期测试在 race 下重复 20 次通过；新增调用方先到期、队列先到期、无调用方截止时间及显式取消四种情况，断言到期释放 payload、FIFO 和 retained ownership，且不取消父 context。
- 既有取消排队、共享执行池饱和、只读取消、已开始修改继续执行、断连、panic 和 typed remote error 回归通过。
- macOS/Linux CPU 与 alloc pprof、所有短窗口和延长窗口原始输出均保存在本地；性能测试与全量单元测试未同时运行。实验版未进入产品改动。

```sh
GOWORK=off go test -tags=integration ./pkg/transport -run '^$' \
  -bench '^BenchmarkTransportBudget$' -benchtime=1s -count=3 -cpu=4 -benchmem
GOWORK=off go test -tags=integration ./pkg/transport -run '^$' \
  -bench '^BenchmarkTransportBudget/Bytes64/Workers16/Budgetstrue$' \
  -benchtime=3s -cpu=4 -cpuprofile=/tmp/rpc.cpu -memprofile=/tmp/rpc.mem
GOWORK=off go test -race -tags=integration ./pkg/transport/... ./pkg/cluster/net -count=1
GOWORK=off go test -race -tags=integration ./pkg/transport/internal/rpc -run TestQueue -count=20
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
```

后续仍需使用相同跨 ECS 场景复测，并拆分压测器调度滞后、客户端提交和 ACK 等待，才能判断约 194ms 的业务 P99 与计划发送缺口。后续代码核对已排除当前匹配客户端的 SENDACK 操作锁等待假说，详见 [压测延迟归因后续](2026-09-21-benchmark-latency-attribution.md)。当前没有云上分段证据，因此未调整业务参数或宣称业务尾延迟恢复。
