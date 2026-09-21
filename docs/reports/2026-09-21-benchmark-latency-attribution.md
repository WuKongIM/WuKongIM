# 压测延迟归因与调度修复 — 2026-09-21

本轮补齐通用单聊、群聊压测的四段计时，并修复两个可复现的调度边界问题。约 194ms 的云上 SENDACK P99 仍未完成归因；本轮没有重新购买服务器或执行跨 ECS 复测。原租约已销毁，清理证明见 [云上验证报告](2026-09-21-transport-cross-host-validation.md)。

改动基于 `29a8f10d153d21c16a81450412b6dcc52ffec0be`。历史六轮的匿名指标、原始报告校验值及本轮验证输出校验值见 [机器可读记录](assets/benchmark-latency-attribution-2026-09-21.json)。

## 已确认的证据

复核云上压测器精确版本 `7bda37db546fc577dd6fe7cc56ed3b95e6cbeeda`：worker 使用 `WrapPersonClientsForConcurrentReadsWithFanoutProof`，实际 `matchingPersonClient.lockSendackOperation` 返回空解锁函数，不等待锁。因此原报告从计时位置推测 SENDACK 操作锁等待并不成立，已更正原报告。共享读帧、匹配、客户端队列和服务端耗时仍需进一步区分。

历史六轮每轮计划 90,000 次发送。原有调度指标显示：

| 轮次 | 已派发 | 窗口结束丢弃待发送 | 尚未入队 | 最大待发送 | busy-key 停顿计数 |
|---|---:|---:|---:|---:|---:|
| baseline-1 | 87,819 | 2,181 | 0 | 2,181 | 121 |
| candidate-1 | 87,081 | 2,919 | 0 | 2,921 | 108 |
| baseline-2 | 87,080 | 2,919 | 1 | 2,938 | 194 |
| candidate-2 | 87,313 | 2,687 | 0 | 2,692 | 0 |
| baseline-3 | 87,162 | 2,838 | 0 | 2,838 | 159 |
| candidate-3 | 87,155 | 2,845 | 0 | 2,864 | 248 |

各轮最大在途数均为 64，但高水位不等于持续饱和；busy-key 计数不是等待时长。candidate-2 没有 busy-key 停顿仍存在发送缺口，不能把缺口全部归因于同一发送者串行化。上述证据证明计划到达未全部派发，不能独立证明产品容量不足或压测器是唯一瓶颈。旧报告没有新分段数据，无法追溯拆分 P99。

## 新计时口径

| 指标 | 边界 |
|---|---|
| `workload_dispatch_lag_seconds` | 原计划到达时刻到调度准入；包含待发送排队，不包含后续 goroutine 启动与包构建 |
| `workload_send_submit_seconds` | 每次客户端 `Send` API 调用；可能只是入队，不是真实写上网络的时刻 |
| `workload_sendack_wait_seconds` | 每次等待匹配 SENDACK；包含客户端匹配、网络及服务端影响，也包含超时上下文处理 |
| `workload_operation_seconds` | 包构建后到发送操作返回；包含重试、ACK 等待及已配置的接收校验 |

失败尝试同样记录；重试场景的提交/ACK 等待次数可以大于逻辑操作次数。完整操作还包含计时记录等客户端开销，各段分位数不能相加或相减。原 SENDACK 成功样本口径和 SLO 判定不变；新指标只用于诊断。Markdown 报告仅汇总 `phase=run`，缺失阶段保持未观测，不当作零；同时列出计划、已派发和窗口结束丢弃数，并在未全部派发时提示通过门限不代表达到计划速率。

四种新直方图各使用 28 个固定桶，不保存逐条时延。count、sum、min、max 保留观测统计；P50/P95/P99 标记 `percentiles_are_upper_bounds=true`，桶上界以实际最大值封顶。跨 worker/traffic 报告取各序列 P99 上界的最大值，不宣称全局合并样本 P99。原有 SLO 原始样本存储方式没有改变，不能据此声称整个指标注册表的内存均已固定。

## 可复现的问题与修复

1. 通用调度器原先在整批准入前只检查一次截止时间。批内工作耗时后，后续任务仍可能继续准入。现改为每个任务准入前重查截止时间。虚拟时间用例让首个准入的观测耗时越过截止时间，修复前派发 3 个，修复后只派发 1 个。
2. 窗口关闭后仍有在途任务时，原定时器选择器不断针对过去的截止时间创建立即到期的定时器，形成忙循环。现关闭窗口后只等待任务完成或取消。回归用例修复前得到非空定时器，修复后得到空通道。

两项回归用例均先观察到失败再修复。它们没有被证实是历史云上 P99 的主因；尤其窗口关闭后的忙循环不能直接解释测量窗口内的所有尾延迟。

## 验证与开销

- `metrics`、`workload`、`report` 直接相关单元测试通过。
- `go test -race ./internal/bench/... ./pkg/bench/... ./cmd/wkcli/... -count=1` 通过，共 25 个有测试的包及 3 个无测试文件的包。macOS 链接器输出 LC_DYSYMTAB 警告，未影响测试退出状态。
- 虚拟时间注入 3ms 提交与 17ms ACK 等待，新指标分别记录 3ms/17ms，完整操作及原 SENDACK 指标均为 20ms。此计时用例在实现前因指标缺失失败。
- 同一发送者排队用例验证累计调度滞后 60ms、最大 40ms；群聊完整接收校验及重试用例验证阶段计数；10,001 次诊断记录验证固定桶、不保留逐条样本、统计汇总和净化后标记保留。
- 报告用例验证预热隔离、缺失阶段不显示、未派发提示以及原 SLO 计算不变。

Apple M4 / darwin arm64、Go 1.25.0、GOMAXPROCS=4，三轮各 1s 的单独微基准：固定桶更新中位数约 3.22ns、0 分配；通过现有 Registry 和四个标签记录约 327.5ns、288B、4 次分配；四线程共享 Registry 的吞吐倒数约 428.5ns/op。并行 ns/op 不是单次延迟，仍存在共享锁竞争。

新增观测复用了现有标签校验和序列键构建，因此并非零分配。按无重试、每消息四次记录估算，500 SEND/s 约新增 0.576MB/s 临时分配；该估算不包含时钟、调用包装和其他原有指标开销，不能代替完整压测。高发送率场景应复核压测器 CPU、GC 和锁竞争。

```sh
GOWORK=off go test ./internal/bench/metrics ./internal/bench/workload ./internal/bench/report -count=1
GOWORK=off go test -race ./internal/bench/... ./pkg/bench/... ./cmd/wkcli/... -count=1
GOWORK=off go test ./internal/bench/metrics -run '^$' \
  -bench '^BenchmarkDiagnosticLatency$' -benchmem -benchtime=1s -count=3 -cpu=4
```

下一次相同跨节点测试应同时保留四段数据、计划与实际发送差异、压测器及服务端 CPU/alloc pprof，再根据主耗时阶段选择进一步改动。当前结果支持调度修复和诊断能力交付，不支持宣称云上性能回归已消除。
