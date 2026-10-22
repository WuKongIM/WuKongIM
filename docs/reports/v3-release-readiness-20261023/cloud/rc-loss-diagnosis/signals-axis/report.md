# 新云 RC 的离线指标、日志与保留 profile 诊断

结论：正式报告的 message_loss 判定有保留证据；当前材料支持“已接受的发送中有一个采样接收在 10 秒期限内未完成”，仍不足以确认产品内部根因。负载机没有已观察到的进程退出或明显持续 CPU 饱和证据，但缺少整机、调度、GC 和独立进程代际数据，不能宣布完整 headroom 已排除。应在上海时间 2026-10-23 03:36:18 的诊断期限前，补取同一历史窗口中缺失的准确指标及进程连续性材料；无需重新启动负载来取得已有 Prometheus 历史数据。

## 证据与方法

- 冻结源为 79a7177032a6ae1ec7015cefe0e6f94eb0f19f06，工作树只读且干净。适用 AGENTS、FLOW 与实际阅读源码的 SHA-256 在 source-binding.json 中。
- public-v1 的 SHA256SUMS 为 50fba8e114bf93182734305a312808bc8c840981bd3ba03e869834cd482f7def，49 个条目逐一核对通过。仅校验身份文件字节哈希，未读取其内容；未打开私有完整 ZIP。
- 实际负载窗口为上海 01:35:17.578381188 至 01:36:18.801475417，61.223094218 秒。发送、SENDACK 均为 122,002，窗口平均约 1,992.74 SEND/s；负载因失败终止，不能解释为仍在发送的实时 QPS。
- 所有范围分析都保留原始 query_range 评价点，不补零、不外推、不把不同时间/指标的累计值当成同一消息的因果链。limited-summary.json 是可复算有限聚合；analyze-signals.py 是纯离线复算脚本。
- 已保留的六个 profile 各运行一次最多 15 项 top，单次 45 秒上限，GOPROXY/GOSUMDB 关闭，未主动采集 profile。命令、耗时与输入哈希见 offline-pprof-receipts.json。

## 支持的观察

1. 终端 workload 证据为 3 个 worker 的同 generation / snapshot 13 最终切面，worker 2 保留一个 10,000,000,000 ns 过期项。报告 sampled=1,277、delivered=1,276、expired=1、pending/outstanding=0。发送尝试与成功 SENDACK 均为 122,002，无重试、拒绝或终端发送错误。具体过期 sample 的语义归属由独立 sample 轴核对。
2. 三个服务节点保留日志出现本轮独有的 37 次 delivery.plan_incomplete，上海 01:36:08 至 01:36:10，按节点 1/2/3 为 12/13/12；其中 presence 36 次、owner_push 1 次。recipient_worker_process_total 的 timeout 从零上升至同样的 12/13/12。存在同一窗口内的下游投递超时信号，但没有消息标识关联，不能认定这 37 次中某次就是采样过期的原因。此处没有使用上一轮的 47 次告警。
3. 三节点 process_up 在已保留的 5 个窗口内评价点均为 1，CPU jiffies 单调；系统快照显示 wukongim 及负载 worker NRestarts=0。7 个 Prometheus target 的 up 保留点均为 1，稍后 targets 快照也为 up。没有保留日志中的 panic、OOM 或 ERROR 级事件。只能说未观察到退出/抓取断开，不能把 15 秒采样当作连续 PID 证据。
4. 负载机三个 worker 60 秒评价跨度的 CPU jiffies 增量为 678/674/659；协调器为 33。collector 源码取 /proc MainPID utime+stime，未输出 CLK_TCK，故这里只报告 ticks，不把它未经核对地换算为 CPU 百分比。三个 worker 末点 RSS 为 1,018,351,616 / 936,071,168 / 887,750,656 字节，合计 2,842,173,440；协调器末点 41,558,016 字节。这不显示 worker 主进程长期占满 4 核，但尚缺同机 Prometheus/其他进程、内核和 steal 的整体证据。
5. recipient worker queue 采样最大值节点 1/2/3 为 117/48/59，容量均为 1,024；inflight 最大值为 39/37/13，capacity 均 320。channel store-append 的 queue depth 采样均为零，但历史 inflight_peak 节点 1/2/3 为 124/128/102，而 workers=128，节点 2 曾触及 worker 数量。该 peak 是进程累计峰值，不能定位到唯一失败时刻。队列未满的采样并不能排除短暂峰值、排队或截止时间耗尽。
6. 六个 profile 都是上海 01:43 左右的停止后切面。三个 inuse_space 为约 499.57 / 516.61 / 528.60 MB，最大 flat 分配点是 Pebble manual.New（约 274–280 MB）。goroutine 总数 3,497 / 3,533 / 3,420，约 99.7% flat 在 runtime.gopark，delivery worker 的主要栈已在 dequeue 等待。这支持采集时系统已空闲化，不构成故障瞬间无锁争用、无 CPU 饥饿、无 GC 压力或无内存增长的证明。没有故障时 load-generator 的 profile，也没有 CPU profile。

## 明确的采集缺口

collect-terminal-evidence 成功表示请求/格式/字节预算内完成，并不等于诊断所需全部信号存在。manifest complete=true、capture_failures=0 与两个空结果可以同时成立。

| 原查询 | 79a 源码注册/导出的名称 | 影响 |
| --- | --- | --- |
| wukongim_go_.* | collectors.NewGoCollector 注册标准 go_*，并含 go_sched_latencies_seconds；另有 wukongim_node_goroutines | range-go 空序列不能解释为零 goroutine、零 GC 或无 scheduler latency |
| wukongim_metadata_create_total | wukongim_channelv2_meta_created_total，Handler 另导出 wukongim_channel_meta_created_total 别名 | metadata 创建/失败证据未捕获 |
| wukongim_activation_rejected_total | wukongim_channel_activation_rejected_total 及 wukongim_channelv2_activation_rejected_total | activation rejection 证据未捕获 |
| wukongim_process_.* | 自定义 systemd MainPID collector；不是标准 process_*，也不含 node_* 整机指标 | 主进程资源有有限证据，整机饥饿与 PID/starttime 连续性仍缺 |

这是名称错配而非时间窗口错配。manifest 请求失败前 900 秒至最终报告 end，其他 families 在同一窗口有样本：process/up/runtime/channel_worker 从 01:31:18.801 至 01:36:18.801，presence 从 01:35:33.801 起出现。Prometheus step 为 15 秒，因此 1 秒级或 10 秒级故障仍可能落在抓取间隔内；评价时间也不是实际采样新鲜度，process collector_last_success 与末点相差约 14–19 秒。

## 必须补取的有限历史材料

在现有诊断期限内优先保留以下同一 request 的历史范围或原始只读证据，由拥有 live 权限的父任务执行。本子任务没有发出云、SSH 或 MCP 请求。

- 标准 go_goroutines、go_memstats_*、go_gc_duration_seconds*、go_sched_latencies_seconds*，以及 process_cpu_seconds_total / process_start_time_seconds / process_resident_memory_bytes 的故障前后范围。应明确标准 Go 指标只覆盖产品 API target，不自动覆盖 worker。
- 已存在 host exporter 的 node_cpu_seconds_total（含 idle/iowait/steal）、node_memory_MemAvailable_bytes / MemTotal、node_load*、node_pressure_*、disk 和网络丢包/错误；覆盖 load 与三个 service，保留实际 scrape 时间/健康。
- 正确的 channel(v2)_meta_created_total、activation_rejected_total，必要时 meta_create_queue_depth 等已存在指标；保留相同毫秒 end 与 15 秒步长边界。
- 四机 process collector 原始 clock tick 频率、unit MainPID 与进程 starttime / systemd invocation identity，以及原有 host evidence 中已保存的该时段进程资源或 worker 观测。避免仅凭 active daemon 认定正在产生流量。

补取失败时保留缺失，不重跑负载来覆盖此次失败；现有证据足以维持本轮预演失败和 v3 发布 NO-GO，尚不足把底层原因唯一归结为产品、基础设施或负载发生器。
