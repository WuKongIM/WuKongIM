# 30 分钟持续验证（提交协调器观测版）：发送全程跑满，仅因权限读繁忙门槛失败

源码 `ecea82a54`，服务端 `wukongim-disk`（`1996a2768`），harness `permission-soak-commit.test`。
负载 5000 频道、4500 SEND/s、cap32、30m，不注入故障。

## 结果

- 第一次跑满 30 分钟发送：8100000 条消息全部送达，pending 为 0；入口 4500.0/s，完成 4500.0/s。
- 没有 SENDACK 超时、没有 EOF、没有断连。
- SENDACK p50/p95/p99/max = 15 / 106 / 425 / 2620ms；RECV p99 = 349ms。
- `sendack_system_busy` = 27589（均已退避重试成功）。
- 失败断言：`permission admission busy = 5, want 0`（`permission_soak_test.go:1700`）。
- 第 24、25 分钟 pending 为 1102、1003，第 26 分钟归零。

## 提交协调器（channel 消息库，三节点一致）

- 每批各阶段均值：collect 0.68ms、build 0.03ms、commit 1.43ms、publish 0.005ms、total 2.15ms。
- 批次 total 超过 100ms 的 95–101 次，超过 1s 的 0 次。
- 请求耗时均值 6.8–8.5ms；超过 1s 的每节点每 lane 9–26 次，超过 2.5s 的 0 次。
- WAL fsync 超过 100ms 68–71 次，超过 1s 0 次；写停顿、慢盘均为 0。

请求耗时超过 1s 而没有任何一批超过 1s，说明这些秒级等待发生在请求进入批次之前
（在协调器队列里等待），而不是单批提交慢。可能是短时排队堆积，也可能是协调器
goroutine 没拿到 CPU；只有结束时的队列深度快照（0），无法区分。采样时容器 CPU
约 690–800%，接近 8 CPU 上限。

## 结论

本轮没有复现 5s SENDACK 超时，最长 2.62s。剩余失败是验收门槛：权限读准入繁忙
次数必须为 0。`ReasonSystemBusy` 已让这些请求退避重试并成功送达，是否放宽该门槛
需要决定，未修改。

证据：`assets/send-ban-commit-20260928/`。
