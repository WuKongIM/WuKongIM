**内部通信层性能改进与验证 — 2026-09-21**

实现基于 `68662c4c8fa7a447ac2864c7352789ae4a5021b7`，位于
`codex/transport-performance`。本次完成默认凑批等待、缓冲池及内存准入、
观测聚合、请求预算和取消传播的改进。未调整连接池大小或业务 worker 数量。
逐次结果和环境保存在 [机器可读证据](assets/transport-performance-2026-09-21.json)。

**实现与语义**

- 默认 `WriteBatchMaxWait=0`，仍合并已就绪帧。显式配置非零等待时，
  Raft/control 到达及连接关闭能够中断等待。
- 默认 slab 改为 512B 至 1MiB 的二次幂档位。调度器和服务队列按 backing
  capacity 准入，批次仍按逻辑载荷计数；服务的 queued + executing 保留字节
  在实际释放前持续计费，默认上限为队列字节预算的两倍。
- pending 数量改为 O(1)；迟到响应发现请求已移除后直接释放，不复制载荷。
- 观测每 10ms 聚合计数及最新状态；时延固定按 1/32 采样，计数不采样。
  有界目录或采样缓冲溢出单独计入 `wukongim_transport_observer_dropped_total`，
  并补入 Grafana。增加客户端完整调用时延、服务排队时延及保留内存观测；
  客户端和服务端使用一致的服务名称。
- 客户端先用 v1 对保留服务 65535 协商能力，确认后才发送 v2 budget/cancel
  帧；显式 service-not-found 才退回 v1。协商超时或无效响应不会重放业务请求。
  旧节点在升级前继续使用 v1，无法接收调用方取消。
- 发送时计算相对预算，接收方从接收时开始计时；调用方仍约束端到端截止时间。
  默认服务排队预算 5s，涵盖共享执行池等待；默认执行 context 为 30s，备份类为
  5min，显式执行超时配置优先。过期或取消会从 FIFO 中移除并释放容量。
- 只有明确只读服务允许取消正在运行的 handler。已开始的修改使用独立执行
  context，可以在调用方超时后完成；超时/取消不表示回滚。执行超时要求 handler
  配合检查 context，不能强制终止任意 Go 代码，内存归属在实际返回前仍保留。

**性能结果**

macOS Apple M4 / Go 1.25.0 / GOMAXPROCS=8，64B loopback RPC，16 个预热连接，
每项三次、每次 1s，以下为中位数。Observer 使用实际 Prometheus 接入。

| 默认配置、观测开启 | 原始版本 | 候选版本 |
|---|---:|---:|
| 串行平均 RTT | 313.96µs | 26.66µs |
| 并行吞吐折算耗时 | 37.07µs/op | 10.41µs/op |

候选版本的观测额外开销：串行约 6.8%，并行约 8.8%。并行 ns/op 是吞吐的倒数，
不是单请求 RTT。结果不能直接外推业务吞吐。

Linux ARM64 Docker Desktop / Go 1.25.11 / 4 CPUs / 6GiB / GOMAXPROCS=4，
相同微基准重复三次。候选版本串行 10.87µs（无观测）/12.54µs（有观测），
并行 7.23µs/op /7.86µs/op。并行观测开销约 8.7%；串行仍为约 15.4%
（绝对增加 1.67µs），没有宣称所有场景均低于 10%。原始默认等待在该 VM 的串行
观测路径达到约 4.54ms；这受到系统定时器/调度影响，不作为跨主机收益估计。

Linux 三节点混合消息基准保持 256 Hash Slots、2,500 用户、100 个热单聊、
500 个群（每群 10 人）、单聊/群聊 90%/10%，以 500 条/秒发送 3,000 条消息。
三轮原始版本和三轮候选版本顺序交错执行，每轮都全部完成，零错误、零丢弃。
该基准在同一进程中运行三个真实 App 节点及 TCP/存储；另有三进程 E2E 验证。

| 三轮统计 | 原始版本 | 候选版本 |
|---|---:|---:|
| 处理 P99 中位数 | 20.21ms | 10.13ms |
| 各轮处理 P99 | 20.21 / 16.23 / 49.03ms | 17.40 / 8.96 / 10.13ms |
| 每条消息分配字节中位数 | 200,957B | 221,706B |
| 每条消息分配次数中位数 | 1,321 | 1,448 |

P99 中位数约下降 50%，但样本只有三轮，原始版本也存在明显波动。预算/context/
timer 管理使业务分配字节增加约 10.3%、次数增加约 9.6%。单独 pprof 显示
context 与 timer 是主要分配来源，不能将延迟改善描述成所有资源消耗都下降。

重新执行原内存探针，同时保留 32 个缓冲区：4097B 边界总分配从约 2.10MB
降至 0.267MB，65537B 边界从约 33.57MB 降至 4.20MB，均减少约 87%。
这是堆累计分配探针，不是进程 RSS；队列预算也不包含对象头和业务额外分配。

**验证与复现**

- 全量显式包路径单元测试通过，覆盖 cmd/internal/pkg/scripts/docker。
- transport 和 cluster/net 的 integration + race 通过；app/metrics/Grafana
  接线测试、`flow-doc-contracts` 通过（9 条建议行数告警）。
- 回归覆盖零等待、紧急帧/关闭唤醒、物理内存预算、执行期间计费、排队取消、
  共享池饱和时预算、只读取消、修改继续完成、断连、panic 响应、旧节点回退、
  无效协商禁止业务发送、迟到响应零复制和观测计数/样本分离。
- Linux 三进程 E2E：单聊/群聊跨入口突发收发及严格递增序列通过；节点停止、
  保留 TCP 的进程暂停、故障转移、恢复后写入及历史完整性通过。
- 生命周期夹具原本使用无 Token 客户端却未关闭鉴权，原始版本和候选版本均
  在登录阶段失败。已显式设置该测试夹具的 Token 鉴权为 false，更新场景说明；
  产品鉴权及消息数量/顺序断言未改变。

```sh
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
GOWORK=off go test -race -tags=integration ./pkg/transport/... ./pkg/cluster/net -count=1
GOWORK=off go test -tags=integration ./internal/app -run '^$' -bench '^BenchmarkClusterRPCObservation$' -benchtime=1s -count=3 -cpu=4
GOWORK=off go test -tags=integration ./internal/app -run '^$' -bench '^BenchmarkThreeNodeMixedSendPath500QPS$' -benchtime=3000x -count=3 -cpu=4
GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run 'Test(PersonChannelCrossIngressBurstPreservesReceiveSequence|GroupChannelCrossIngressBurstPreservesReceiveSequence)$' -count=1 -timeout=9m -p=1
GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout=6m -p=1
```

本次完成本机及 Linux 同宿主验证；未进行跨物理主机/WAN、十万成员群容量或
长时间生产负载验收，不据此声明生产最大容量。
