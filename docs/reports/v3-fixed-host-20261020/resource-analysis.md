# Run 39453556254：资源与时序离线复核

**本轮不能把 A/A 差异归结为一个具体代码缺陷，也没有足够证据提出已定位的产品性能修复。** 同一个 B 二进制的第 4/5 组，三窗 P99 中位数仍超出预声明的 25% 重复性边界：list 22.42→29.40 ms（+31.18%），sync 48.11→65.47 ms（+36.09%）。这是比较解释的限制，不能用它否定另行验收通过的候选完整 gate，也不能把完整 gate 通过写成 A/A 通过。以下只分析归档数据，不运行负载。

## 同一个 B：多窗持续差异，尚非唯一因果

数据来自 `conversation-fixed-host/blocks/{04-B,05-B}/report.json`。两份产品二进制 SHA 相等；list/sync 每窗分别完成 12,000/3,600 个请求，未记录 drops 或业务错误。

| 观察 | B4 的三个窗口 | B5 的三个窗口 |
| --- | --- | --- |
| list handler 平均 ms | 7.265 / 6.344 / 6.371 | 8.394 / 8.527 / 8.442 |
| sync handler 平均 ms | 18.283 / 16.145 / 16.143 | 21.054 / 21.502 / 21.434 |
| list driver-wait P99 ms | 2.06 / 2.00 / 1.98 | 2.46 / 2.46 / 2.35 |
| 产品 CPU 秒 | 161.12 / 155.71 / 155.93 | 163.85 / 165.56 / 165.12 |
| 实际 service CPU PSI `some` | 40.89% / 38.10% / 37.69% | 44.05% / 43.58% / 43.69% |

三窗以 stage count 加权，list/sync handler 均值分别增加 **26.95%/26.54%**，metadata/heads/edit-overlay/barrier 均值分别增加 **34.70%/22.11%/30.33%/27.12%**。多个阶段一起变慢，且三个窗口均有差异，不能把结果只解释为一次尾部偶发值；仍不能声称每一秒都持续变慢。阶段 sum/count 是 wall time，人口不同且可嵌套，不能相加成请求 P99 或 CPU 成本。

产品三窗 CPU 总量 472.76→494.53 秒（**+4.60%**），分配总量仅 **+0.00127%**。驱动进程 CPU 在资源采样区间为 71.49→78.02 秒。延迟差异主要仍在 request 调用内：list request-P99 为 21.18–22.97→27.24–28.71 ms，sync 为 45.07–49.96→58.01–65.92 ms；它包含客户端编码、网络、服务端和解码，不能等同服务端时间。CPU PSI `some` 与变慢相关，但 `full` 反而略低；不存在“所有压力信号均同向”的证据。

## B1 第三窗：最后几秒发生请求变慢和等待累积

`blocks/01-B/report.json` 的 list P99 为 32.71/31.00/**196.20** ms；第三窗 driver-wait P99 **153.26** ms，request-P99 **53.14** ms。按**计划到达秒**分桶，而非完成秒：

| 第三窗计划秒 | list 最大等待 ms | list 最大请求 ms | sync 最大请求 ms |
| --- | ---: | ---: | ---: |
| 0–54 的各桶最大值 | 7.75 | 49.71 | 86.44 |
| 55 | 2.64 | 55.95 | 127.91 |
| 56 | 56.25 | 86.43 | 167.21 |
| 57 | 107.54 | 73.32 | 167.96 |
| 58 | 153.48 | 78.99 | 166.35 |
| 59 | 209.98 | 76.20 | 169.14 |

两个 endpoint 的 request 时间都在末段升高，随后 list 的等待逐秒累积。每个 list 桶仍完成 200 个请求，第三窗共有 33 个 list、4 个 sync 请求在截止后完成，**没有 drop**。保存的前 16 个 list slow samples 位于 index 11765–11849，即计划到达 58.825–59.245 秒；这是按完成时机保留的有界样本，不能当随机分布或全部最慢样本。

三个窗口的产品 CPU 为 172.17/170.39/169.49 秒，service PSI `some` 为 46.57%/46.41%/46.90%。窗口均值没有定位末段事件；driver wait 合并了调度迟到与排队等待，不能拆成纯队列或纯调度。证据支持**末段数秒的请求变慢伴随等待积累**，不足以认定单次暂停、Go 强制 GC、CPU 频率或宿主竞争中的某一个原因。

## 实际资源范围与准备状态

四进程的真实 cgroup 均解析到 `/sys/fs/cgroup/system.slice/hosted-compute-agent.service`，挂载根 `/`，实际 affinity 为 `0-3`。service 和可见祖先 `system.slice` 的 `cpu.max` 是 `max 100000`；根文件缺失单独保留，未伪造无限值。**21 个窗口中所有已采集可见层级均无 throttling 增量；所有可用 memory.events 增量为 0，所检查进程 major-fault 增量为 0。** 这是共享 service，并非仅包含实验四进程的独占 scope；不能把全部 cgroup CPU 都归给产品。资源 cut 包含 warmup 和窗外观察，约 61 秒，不是精确的 60 秒测量切片。当前观测不支持把 B4/B5 归因为已记录的 quota 节流或内存限额事件；不追溯证明旧运行 quota。

固定 180 秒是**产品进程年龄控制，不是相同 GC/存储状态**。B4/B5 准备结束年龄为 31.852/33.374 秒，随后等待约 148.165/146.640 秒。等待结束、原 warmup 之前，两者三节点累计 GC 数同为 `[18,17,18]`，但最后 GC 年龄分别约 `[20.18,29.93,2.94]` 与 `[16.11,28.14,9.38]` 秒，heap alloc 约 `[129.4,147.8,82.7]` 与 `[117.3,142.7,98.9]` MB。状态未对齐，GC 数相等也不构成等价。

等待区间仍有后台工作：B4/B5 分配约 1.131/1.113 GB，产品 CPU 14.32/13.59 秒；逐节点 GC 增量均 `[5,4,5]`。匹配各节点、各 store 完整标签后，WAL bytes-written 增量分别 798,860/89,970 bytes，fsync count 262/47；较快 B4 反而更多，不能据此指认 WAL 造成 B5 变慢。B1/B4/B5 四个准备边界的已记录 compaction、flush、write-stall 系列均存在且为 0。该结论只覆盖准备边界；原 60 秒窗诊断指标未保留 storage 系列，不能扩大成测量期间无存储活动。

三节点 GC pause 累计每窗：B4 0.0715/0.0705/0.0686 秒，B5 0.0882/0.0999/0.0813 秒；B1 为 0.1122/0.1055/0.1253 秒。只有 count/sum，不知道各次时间位置，也没有并发 GC/assist CPU 的精确分解；不能据此宣称已确认 GC 根因或已排除 GC 影响。

## 历史失败实际证实到哪一层

详见同目录 `historical-causal-boundary.md/.json/.py`。原 `39368295521` 的直接 drops 机制是驱动非阻塞入队时有界队列满，list drops 1441/1142/1032，driver-wait P99 750/620/608 ms。list handler 平均 40.48/39.33/39.12 ms 已接近 8 workers 在 200/s 下的总占用预算 40 ms，尚未计客户端开销，因此不能全归于驱动 JSON。高 CPU 使用、PSI 和多阶段同时变慢支持可用 CPU/处理能力不足的候选，但未识别硬件、调度、有效 quota 或准备历史的各自贡献。

旧 RPC/完成请求比例差异可由 `L/S` 完成混合精确解释（9 窗 54 个模型残差全部为 0），不能继续独立当作新增路由或扇出证据。旧运行只读了根 cgroup 控制，实际 service quota 历史未知。独立短 profile 并未覆盖失败的三个 60 秒窗；它能显示 CPU 成本候选，不能定位原窗暂停。**目前没有已定位到具体可修复性能代码的证据，不应据此修改 ReadIndex、GC 或 worker/队列/门槛。**

## 可重算口径

`resource-analysis.py` 只读 ZIP 中七个 block 的 `report.json`，或读取解压目录；不运行产品、不联网。JSON 保存每份 raw SHA、全 60 秒 arrival 桶、slow samples、每节点 GC/存储差分、每进程 CPU/fault 差分和所有可见 cgroup 祖先控制及差分。`summaries.B4_B5`、`summaries.B1_window3_late_timeline` 对应上面的主要派生结果。

```bash
python3 resource-analysis.py --artifact /path/to/artifact.zip --output /tmp/resource-analysis.json
# 也可使用保留 blocks/*/report.json 的非二进制归档目录
python3 resource-analysis.py --artifact /path/to/conversation-fixed-host --output /tmp/resource-analysis.json
```

CPU 秒来自 `(Δutime+Δstime)/clock_tick_hz`；PSI 比例来自 `Δtotal_usec/1e6/资源采样中点间隔`；stage 均值来自 `1000×ΣΔsum/ΣΔcount`。准备 storage 差分按完整标签匹配；gauge 的差分只是状态变化，不冒充累计工作量。各节点 scrape 非原子，不将 GC pause 求和当作集群同时停顿。
