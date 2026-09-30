# Send ban Docker Linux 三组对照结果

**九轮全部执行并保留：旧基线 2/3、逐 Slot fresh 0/3、节点聚合 3/3 通过。没有完整 fresh 配对，P99/CPU 的 5% 门槛未验证；原定 30 分钟资格仍失败。**

本报告沿用[环境与源码身份](2026-09-27-send-ban-docker.md)：Linux/arm64、Go 1.25.11、同一共用 harness，三节点同容器、8 CPU/6 GiB 配额、Linux volume、256 Hash Slots/10 个物理 Slots/三副本，5000 频道、1200 SEND/s、60 秒。每轮 append workers128、gateway workers128/queue131072/batch32，不改变复制和同步。所有构建与正向控制在整组前完成。

## 全部轮次

| 顺序 | 版本 | 结果/窗口 | SEND P50/P95/P99 (ms) | 三节点 CPU % | 完成吞吐 /s |
| --- | --- | --- | --- | --- | --- |
| 1 | 旧基线 | 通过，60 s | 7/10/13 | 136.10 | 1199.89 |
| 2 | 逐 Slot fresh | 失败，30.887 s | 7/12/28 | 224.56 | — |
| 3 | 节点聚合 | 通过，60 s | 7/19/478 | 217.18 | 1199.87 |
| 4 | 节点聚合 | 通过，60 s | 7/25/61 | 214.43 | 1199.85 |
| 5 | 旧基线 | 通过，60 s | 7/10/35 | 137.77 | 1199.87 |
| 6 | 逐 Slot fresh | 失败，28.675 s | 7/12/50 | 208.14 | — |
| 7 | 逐 Slot fresh | 失败，28.749 s | 7/10/32 | 179.45 | — |
| 8 | 节点聚合 | 通过，60 s | 8/24/70 | 202.62 | 1199.68 |
| 9 | 旧基线 | 预热失败，未进入窗口 | — | — | — |

CPU 100% 代表一核，三节点合计；不包含测试 driver。失败行是各自较短窗口的原始观测，不能与 60 秒成功窗口作总量或 P99 收益比较。三个成功节点聚合窗口的 P99 为 478/61/70 ms，波动显著；不能用中位数隐藏第一轮尾延迟。

| 顺序 | 权限 RPC 调用 | Raft 信封 | Raft payload MB | 总传输 MB | 分配 GB | GC 次数 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 222100 | 26749 | — | 308.369 | 16.324 | 152 |
| 2 | 114264 | 515926 | 25.204 | 270.763 | 10.338 | 103 |
| 3 | 161260 | 981384 | 48.149 | 521.508 | 19.732 | 192 |
| 4 | 162558 | 986561 | 48.669 | 522.679 | 19.662 | 190 |
| 5 | 221429 | 26761 | — | 308.407 | 16.324 | 153 |
| 6 | 105669 | 473187 | 23.273 | 250.830 | 9.517 | 96 |
| 7 | 106006 | 475991 | 23.356 | 251.313 | 9.511 | 96 |
| 8 | 162159 | 982817 | 48.553 | 522.418 | 19.592 | 191 |
| 9 | — | — | — | — | — | — |

MB/GB 为十进制。分配量是累计分配，不是常驻内存。权限 RPC 沿用共用 harness 的旧服务/节点聚合服务选择器；Raft 是信封数，不是内嵌消息数。Raft payload bytes 不含线协议头、TCP/IP 和重传；旧二进制没有该系列，保留 null/“—”，不拿总传输字节冒充 Raft 字节。旧基线没有同等 fresh barrier 语义，不能用其 CPU 或延迟直接审核 5% 门槛。

## 失败与隔离限制

- 第 2/6/7 轮分别在 30.887/28.675/28.749 秒、37065/34410/34499 次 SEND 调用后收到 `ReasonNodeNotMatch`；同窗口权限 admission busy 为 7/1/12。三轮均失败，没有用重试替换。1 秒 inflight/queue 采样不能排除采样间隔内的短时饱和，也不能仅凭 busy 断定哪一个下游阶段拖慢了准入。
- 第 9 轮在冷预热 109.498 秒时，`prime-03204` 的公开 `/message/send` 返回 HTTP 408 `request timeout`。整个测试 136.23 秒，尚未进入 SEND 测量窗口；该轮所有窗口指标保留缺失。
- 每轮结果都确认产品和 harness 已退出，容器只剩保活 sleep；没有资源保护终止。主机最低可用空间 11.204 GiB，高于 8 GiB 保护阈值。
- 122 个共享 Docker accounting 样本没有采集错误，但其他 WuKongIM 容器在第 1–4 轮期间多次执行启动、checkpoint、写入和 profile 任务。该组不是独占环境，不能将延迟变化都归因于本实现。没有停止或修改这些其他任务。

即使其余结果数值较好，这组也不存在任何完整 fresh 配对；三对的 CPU/P99 变化保持 null。R5 的本次九轮执行已完成，R6 的三次有效配对和 5% 判定仍未完成。R2 的 5000/4500/30m 失败另见 Docker 主报告，不被这里的 60 秒测试替代。

## 复现和证据

- [固定顺序及负载](assets/send-ban-docker-20260927/comparison-01/plan.json)、[九轮退出结果](assets/send-ban-docker-20260927/comparison-01/results.json)。
- [全部结构化指标与配对](assets/send-ban-docker-20260927/comparison-01/analysis.json)、[检查结果](assets/send-ban-docker-20260927/comparison-01/validation.json)。
- [共享资源原始记录](assets/send-ban-docker-20260927/comparison-01/shared-resources.jsonl)；各轮目录另含 command、cgroup/主机空间、退出/清理和无损 `e2e.raw.json`。
- [产物摘要清单](assets/send-ban-docker-20260927/comparison-01/manifest.json)；运行入口为 `compare.py`，分析入口为 `analyze-comparison.py`。两者保留固定九轮，不挑选成功轮。

下一步将针对故障前窗口做独立、明确标记的诊断，保持原压力验收门槛。共享负载会限制根因归因，应争取无其他压测/构建的窗口；不能靠放宽同步、扩大准入或恢复陈旧读来通过。

## 无新增负载的 busy 证据复核

磁盘保护中断后，复核三个失败 fresh 参考的原始日志和 cgroup 样本，
结果及输入摘要存于 [fresh-busy-evidence.json](assets/send-ban-docker-20260927/fresh-busy-evidence.json)。
三轮已采样区间的 `nr_throttled` 和 `throttled_usec` 增量均为 0；
这不覆盖最后一次样本之后的尾段，也不排除共享 VM 调度或 I/O 停顿。
权限阶段 histogram P99 分别为 4.574/4.885/3.564 ms，P999 为
9.447/27.364/5.583 ms。低分位数不能排除极少数 admission 超时，
也不能区分等待名额满时的立即拒绝与 100 ms 等待到期。

旧 harness 的三轮日志都没有权限 admission/barrier 细分 duration 系列；
每轮三个 goroutine HTTP 输入均完整，但失败后输出没有
`acquireSendPermissionEnvelope`、`ReadSlotBarrier` 或 `Fdatasync` 符号。
取消后的栈不是失败前时序证据，不能用其排除这些等待。

已核对实际 Docker preparation 使用环境交接目录的冻结单文件补丁：
只把相同 leader 下的合并条件改成每物理 Slot 一个信封。参考与候选
共用 16 个执行名额、16 个等待名额及 100 ms 等待预算，本次未修改它们。
下一次受控 fresh 参考诊断应使用已补齐细分 metrics 的新 harness，
结合窗口前后计数/时间和故障前采样区分 barrier 变慢、瞬时并发与共享
资源活动；在当前磁盘余量下不启动新压力任务。根因及性能资格仍未确认。
