# Offline storage and Slot signals: unprofiled 2,000 SEND/s run

结论：这六份快照确认同一短区间内 fresh read barrier/完整proposal延迟明显高于所记录的平均WAL fsync和message commit batch耗时，但**不能把约200ms barrier拆分归因于CPU排队、Raft持久化或metadata apply**。没有逐请求身份连接，也没有新采样。

全部输入由原 `safe-private-artifact-inventory.json` 固定定位并验证size/SHA。first快照写入为04:15:19.890–20.091Z；terminal为04:15:23.196–23.227Z，各节点区间约3.14–3.31秒，晚于04:15:14.685909Z firstgrant。mtime只代表本机写完，不是精确服务端采样开始。terminal在coordinator结束后，不能等同完整8.127秒测量窗口。

| Node | barrier 次数 / Δsum(s) / mean(ms) | 完整proposal mean(ms) | Slot process_slot mean(ms) | message batch total / commit mean(ms) | meta WAL 次数 / mean(ms) | channel WAL 次数 / mean(ms) |
|---|---|---|---|---|---|---|
| 1 | 1240 / 294.762 / 237.711 | 273.504 | 15.721 | 33.428 / 27.838 | 96 / 12.081 | 71 / 15.670 |
| 2 | 1123 / 273.374 / 243.432 | 263.199 | 17.757 | 36.712 / 32.547 | 86 / 11.632 | 53 / 13.258 |
| 3 | 1031 / 219.391 / 212.794 | 252.650 | 14.820 | 28.324 / 25.701 | 80 / 14.443 | 79 / 17.183 |

三个节点已记录的write-stall、disk-slow累计计数/时间增量均0；WAL fsync slow阈值100ms/1s/5s增量均0。它们没有支持“该区间新出现明确长WAL fsync或Pebble write-stall”的证据，但周期刷新与不同群体意味着不能据此排除所有存储等待。raft/controller store部分累计序列为初始化0，尤其不能宣称Raft持久化耗时0。

Slot scheduler有20workers；first inflight为12/10/8，terminal均0；两端queue depth均0、capacity1024。Slot apply gap两端最大值8/11/4。这里的“最大”只取两个端点，不能证明区间内部无排队或峰值。

源码语义（4cb45b3）：
- `pkg/slot/multiraft/slot.go:2302–2310` → `pkg/metrics/slot.go:142–149`：slot_apply_duration从future.createdAt到完成，包含完整proposal等待，非纯FSM时间。
- `pkg/slot/multiraft/runtime.go:83–89`及`internal/app/observability.go:913–916`：process_slot仅dequeue后执行wall time；没有直接Slot scheduler queue-wait观测。
- `internal/app/observability.go:1509–1515`：DB message_commit的runtime wait使用完整caller-visible request Duration，不能单称CPU/队列等待。message batch的collect/build/commit/publish群体与barrier不同，不能相减当因果分解。
- `pkg/metrics/storage.go:328–350,510–512`及`internal/app/top_collector.go:605–618`：WAL/stall是周期刷新、开店以来累计gauge，预置零值；bucket阈值不是精确percentile。

signals.json保留549组固定低基数series的first/terminal原数值，57组histogram count/sum/bucket边界及其单调差值，192组累计差值、300组gauge端点。均同series/无reset；不跨缺失序列造0，分组中无动态node_name/UID/channel/config。未采集的关联阶段明确unknown，未将缺失指标当通过。

提取器首次因遗漏source-fixed ConstLabel键node_name而保守丢弃每份500行，0组；修正为丢弃其公开值后对相同六输入重新核hash并提取。首输出SHA/计数和读取次数保存在signals.json.extractor_correction；这不是产品或采样失败。未运行HTTP、产品、测试、Go、CPU/heap/trace解码。所有自有命令已终态。
