# 30 分钟持续验证（ReasonSystemBusy 版）：1790.4s 因批量提交超 5s 失败

源码 `4c86f7e9a`，服务端 `wukongim-busy`，harness `permission-soak-busy.test`。
负载 5000 频道、4500 SEND/s、cap32、30m，不注入故障。

- 失败于 1790.4s，`client: sendack timeout`（客户端 AckTimeout 5s）。无 EOF、无断连。
- 权限读准入繁忙 19 次，全部以 `ReasonSystemBusy` 退避重试，未导致失败。
- `sendack_system_busy` = 343872；失败时 pending_messages = 22487。
- 每分钟快照：第 21、25、26 分钟 pending 为 111、196、216，其余为 0。
- 全部写停顿指标为 0。
- 服务端 36 条 `send_failed`，集中在 08:42:06.494–06.636（约 140ms 内），均为
  `message send batch submitter failed ... submitter=4.97–5.15s ... context deadline exceeded`；
  permission 约 2ms、pre_append 微秒级。

结论：批量提交阶段存在一次持续 5s 以上的停顿，与 Pebble 写停顿、权限读无关。
根因未定，需要把这段时间的 flush、compaction、quorum 往返与提交耗时对齐观测。

证据：`assets/send-ban-busy-20260928/`。
