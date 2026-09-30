# 30 分钟持续验证（WAL fsync / 慢盘观测版）：1624.6s SENDACK 超时，WAL fsync 未超过 1s

源码 `1996a2768`，服务端 `wukongim-disk`，harness `permission-soak-disk.test`。
负载 5000 频道、4500 SEND/s、cap32、30m，不注入故障，慢盘阈值 1s（默认）。

- 失败于 1624.6s，`client: sendack timeout`。无 EOF、无断连。
- 第 1–23 分钟每分钟快照 pending 均为 0；失败时 pending_messages = 25383。
- 服务端 32 条 `send_failed`，全部为批量提交超过 5s 截止时间（context deadline exceeded），
  集中在 10:26:51.879–10:26:52.160。
- `sendack_system_busy` = 464542，SENDACK p99 = 611ms。

## 存储观测（channel_log，三节点）

| 节点 | WAL fsync 次数 | 总耗时 s | >100ms | >1s | >5s | 慢盘事件（WAL/其他） | 写停顿 |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- |
| node-1 | 703133 | 546.3 | 38 | 0 | 0 | 0 / 0 | 0 |
| node-2 | 703318 | 546.8 | 37 | 0 | 0 | 0 / 0 | 0 |
| node-3 | 705561 | 547.0 | 41 | 0 | 0 | 0 / 0 | 0 |

平均每次 WAL fsync 约 0.78ms。整轮没有一次 WAL fsync 超过 1s，也没有任何超过 1s 的
慢盘操作。上一轮"单次 WAL fdatasync 超过 5s"的推断被这组数据否定。

## 仍然成立的事实

- 各节点 `store_append_wait` 仍占追加总耗时 99.9% 以上（如 node-1 25148.7s / 25159.5s）。
- 失败瞬间 goroutine 快照与上一轮一致：每节点 33–44 个追加请求停在
  `commit.(*Coordinator).submitResult`；coordinator 主循环在 `nextRequest`；
  一个提交停在 Pebble `commitPipeline.publish`；有 WAL `Fdatasync` 正在执行。
  这只是一瞬间的状态，而 fsync 分布表明单次同步都很快。

## 限制

- 提交协调器指标（`wukongim_storage_commit_*`：队列深度、批大小、批/请求耗时）
  不在失败诊断白名单中，本轮没有抓到。
- flight recorder 的 sync-window 记录只覆盖压测开始后第 25–30s 的窗口，
  与失败时刻相差约 1400s，不能用于本次定位。

## 结论

5s 卡顿不是 WAL fsync、慢盘或 Pebble 写停顿造成的。等待发生在提交协调器
接收请求到返回结果之间，但单次物理同步很快，说明问题更可能在协调器的排队、
批次组织或结果回传上。下一步需要提交协调器的队列深度和请求耗时分布。

证据：`assets/send-ban-disk-20260928/`。
