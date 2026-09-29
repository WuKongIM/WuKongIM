# Send ban 预热失败权限证据补齐

Docker 诊断在冷预热 HTTP 408 后退出，没有进入发送窗口，也没有启动
计划中的 profile。现有失败报告已抓取公开 metrics，却在筛选时丢弃
权限 admission/barrier 结果，导致事后无法区分这两类失败。

本次仅在 E2E `isHotPathDiagnosticMetric` 中加入四个精确名称：
`wukongim_message_permission_counts_total`、
`wukongim_message_permission_duration_seconds_count`、
`wukongim_message_permission_duration_seconds_sum` 和
`wukongim_message_permission_inflight`。复用失败时已有的 HTTP 抓取，
不增加测量期间请求；不扩展到 histogram buckets 或任意前缀。
这些是累计快照，不是 SEND 窗口 delta，也不能单独证明某条请求的时序。

先新增固定名称保留及 bucket/相似前缀排除契约，在改实现前实际失败，
报告四个指标缺失；随后补齐 selector。该契约及既有 admission busy
计数契约在 macOS 0.435 秒、Linux 0.006 秒通过。Linux 新 harness
`/lab/bin/permission-soak-prime.test` 构建成功，四个覆盖文件摘要匹配。
原产品及原 harness 的 SHA-256 保持不变。上述 selector 提交时未启动新的过程 E2E；这些检查仅验证失败报告筛选，
不能计作持续压力或故障修复通过。后续诊断结果记录如下。

适用的 scenario/domain AGENTS 同步说明累积指标范围，FLOW 与产品
行为没有变化。未修改产品参数、RPC、权限策略或持久化，不需要新增
用户行为变更说明。全部 red/green、Linux 构建、冻结规则、可应用补丁和
SHA-256 见[产物清单](assets/send-ban-prime-metrics-20260927/manifest.json)。

下一次诊断应明确使用新 harness，并保留先前 HTTP 408 失败及 profile
缺失；不能覆盖旧二进制身份或把新增观测当作根因已经定位。

## Docker diagnostic-02：磁盘保护中断

使用新 harness 和原产品二进制，在原有 Linux arm64 三节点集群中运行
5000 频道、4500 SEND/s、目标 5 分钟诊断。容器限制保持 8 CPU、6 GiB，
使用同一 Docker volume，未改变权限准入、fresh barrier 或持久化参数。
冷预热 168.238 秒通过。发送窗口第 1、2、3 分钟分别确认并接收
270001、540001、810001 条消息，各分钟 pending 均为 0；这不代表
中断时的最终完整性或后续稳定性。

运行总计 417.144 秒后终止，原因是 runner 的 `host_disk_guard`：
15 秒资源采样观察到主机剩余空间从 9.08 GiB 降到 4.30 GiB，低于既有
8 GiB 门槛。容器 exit 137 来自保护停止，`OOMKilled=false`，采样中的
OOM 和 oom_kill 均为 0，最大 cgroup 内存约 4.20 GiB。共享主机空间
下降不能全部归因于本容器。没有最终 SEND 窗口统计，不能判定五分钟
诊断通过，更不能替代 R2 的三十分钟资格。

在停止前，已依次取得三个节点各 2 秒 CPU、heap 和 goroutine profile，
共九份，全部请求成功且在设置的时间及字节上限内。六份 CPU/heap 文件
均可由 Go 1.25.11 pprof 解析，Build ID 一致。CPU top 的最高 flat 项为
Syscall6（14.65%–18.23%）；heap 的最大 flat 项为 quorumLog.remember
（33.52–48.53 MiB）。短时采样和排名本身不能证明瓶颈、泄漏或故障原因，
三个节点也不是同时采样。计划在 SEND 第四分钟启动的 profile 尚未生成，
从停止容器恢复时明确返回目录不存在，保留该结果。

原始日志以 JSON 无损保存，资源样本、命令、退出结果、profile、pprof
输出和 SHA-256 均见[本轮清单](assets/send-ban-prime-metrics-20260927/diagnostic-02/manifest.json)。
原 Docker runner 保持不变；本轮派生 runner 仅替换容器名及 harness 路径，
变更及摘要另存。容器已经停止，本轮未自动重启或清理其他任务资源。

下一步需要先具备充足且稳定的主机磁盘余量，再按既定档位重跑；当前
保留所有先前 EOF、HTTP 408 和 fresh 对照失败。R2/R5/R6 状态不变。

## 用户授权清理后恢复诊断

用户要求释放磁盘后继续。确认本任务没有运行中的产品进程，校验上一轮
19 份产物摘要，并补存三个节点 stdout/stderr/app 日志后，删除本任务
约 1.7 GiB 的 Darwin 编译缓存和约 3 GiB 的已中断合成测试数据库。
其他 Go 测试结束后，使用 `go clean -cache` 清理约 14 GiB 的可重建
共享编译缓存。没有删除源码、依赖下载、用户数据库、其他容器或测试
证据，Linux 构建缓存及二进制保留。主机剩余空间从约 8 GiB 恢复至
约 26 GiB；清理记录与补存日志见
[清理清单](assets/send-ban-prime-metrics-20260927/cleanup-manifest.json)。

`fresh-diagnostic-01` 使用冻结的逐 Slot fresh 产品二进制与新 harness，
5000 频道、1200 SEND/s、目标 60 秒。冷预热 168.700 秒通过，发送
28.545 秒、34253 次调用后收到 `ReasonNodeNotMatch`，SENDACK P99
52 ms，权限 admission busy 的窗口增量为 4。主机最低可用空间
10.695 GiB，资源保护未触发，最终仅保活 sleep，无残留测试进程。

新增证据将本轮失败收敛到立即拒绝：节点 2 的 admission busy count
为 4、sum 为 0.000001916 秒；按冻结源码仅有的两条 busy 分支及
100 ms 定时等待，这四次是等待名额溢出，不是等待超时。其他节点没有
记录 busy 系列。该证据不说明瞬时并发集中的上游原因，也不能直接
套用于先前三轮失败，未扩大 16 执行/16 等待的边界。

在观察到 prime 完成后约第 20 和 28 秒，各并行抓取三个节点的 metrics
和 goroutine（每节点两种请求串行），12 次请求均成功。每请求限制
3 秒/2 MiB，记录实际时间和退出码。节点 1 第 20 秒、节点 3 第 28 秒
可见 ReadBarrier 栈，但快照不覆盖后来节点 2 的瞬时准入峰值。
采样产生额外诊断负载；本轮不是干净的性能对照，也未替代完整验收。
原始日志、快照、累计失败 metrics、解析及摘要见
[诊断清单](assets/send-ban-prime-metrics-20260927/fresh-diagnostic-01/manifest.json)。

## fresh 失败的发压端时序复核

复核同一轮已保存的 driver/storage 证据，不新增负载：34253 次发送没有
本地 enqueue/socket write 错误，整个窗口最大调度滞后 10.253 ms；
最后五个完整 100 ms 区间分别发送 120/120/120/120/119 条，最后一个
不完整区间为 54 条。保留的十秒环中单个 100 ms 区间最多 122 条。
这不支持长暂停后大批补发的解释，但 100 ms 分箱及本地 TCP 完成不能
排除更短突发或服务器接收批次。87 个存储样本均成功且必需指标齐全，
message flush_count/compaction_count 观测值均为 0；仍不能排除最后
一次采样之后启动的操作。结果与原始输入摘要见
[时序复核](assets/send-ban-prime-metrics-20260927/fresh-diagnostic-01/driver-storage-review.json)。
