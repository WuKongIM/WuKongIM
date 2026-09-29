# 30 分钟持续验证：不再 EOF，约 26 分钟后 SENDACK 超时

源码 `d09def278`，服务端二进制 sha256 `1ec9082f…`，harness `permission-soak-gate.test`。
负载 5000 频道、4500 SEND/s、cap32、30m，不注入故障。

- 结果：失败，1574.2s 后客户端报 `client: sendack timeout`（客户端默认 AckTimeout 5s）。
- 全程没有 EOF，没有断连；此前同档位在 503.7s 和 277.7s 以 EOF 失败。
- 失败前已完成 7083798 次 SEND，入口 4500.0/s，send_errors 0。
- 限流 SENDACK 472797 次，说明未注入故障时也周期性出现背压。
- 每分钟快照：第 22 分钟 pending=805、第 26 分钟 pending=450，其余分钟为 0；失败时 pending_messages=20888。
- 失败证据：SENDACK p50/p95/p99 = 11/182/1231ms，RECV p99 1023ms。
- 存储指标：memtable 峰值 9 个 / 128 MiB，compaction debt 峰值 640737610 字节，8 个并发 compaction，read amplification 8。

推断（未验证）：周期性停顿与 Pebble memtable 堆积或 compaction 压力吻合，
使个别 SEND 超过 5s 才得到 SENDACK。现有证据没有记录 write stall 的起止时间，
需要有界的 stall 时序观测才能确认。

证据：`docs/reports/assets/send-ban-sustained-20260928/`。
