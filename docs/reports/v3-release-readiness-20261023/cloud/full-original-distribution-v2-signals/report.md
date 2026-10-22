# Original-distribution Mac run v2: offline signals diagnosis

本轮直接触发首发失败率门禁的 32 项，已定位到 SEND permission cohort 的有界准入拒绝；它们不是仅凭 `route_not_ready` 标签即可解释的冷路由未就绪。三个节点还出现明确的 recipient queue 满载与投递超时。形成这些压力的底层原因，以及本地三个采样过期与云 RC 的关系，仍未唯一确认。不能把本地结论标签直接当作云端根因。

## 时序、实际发送与停止

实际报告窗口为上海 2026-10-23 03:01:30.891168 至 03:01:44.221268；速率分母采用原始 monotonic elapsed 13.330270417 秒，不用两个截断后的墙钟相减替代。

| 切面 | 新逻辑发送 | 发送尝试 | 成功逻辑 SENDACK | 首次失败 | 采样过期 |
| --- | ---: | ---: | ---: | ---: | ---: |
| periodic，03:01:35.925803125，距起点约 5.035 秒 | 10,003 | 10,379 | 8,753 | 20 | 0 |
| terminal，03:01:44.221268 | 12,001 | 12,589 | 12,001 | 32 | 3 |

初段按保留墙钟切面估算发送约 1,986.84/s，接近目标 primary 2,000/s；初段 ACK 约 1,738.56/s。全窗口逻辑发送与逻辑 ACK 均约 900.28/s，物理尝试约 944.39/s，RECV/RECVACK 均约 1,086.85/s；这些全窗口均值包括失败后的停止与收口，不能解释为发生器始终只能提供 900/s。primary 实际生成 12,000，另有一个 canary。

首个 periodic 切面已违反首发失败率门禁，尚无采样过期；终态保存 `overall_first_attempt_rate`，3 个 10 秒采样过期在后续终态收口时出现。不能据此断言“本地首先由消息丢失触发停止”，也不能断言停止造成了过期。32/12,001 的最终首失败率约 0.2666%，超过原来的严格 `< 1/10,000` 门槛。门槛、负载、种子与产品源没有为此诊断放宽。

588 次 retry 是原始尝试计数的一部分。terminal 产品 gateway 指标为 12,557 个 success SENDACK 与 32 个拒绝，合计 12,589 个物理回应；verifier 只将每个逻辑发送的首次成功完成计入 12,001 ACK。重试的其他成功回应不应与新的逻辑消息相加。终态未知 ACK、冲突完成、重复完成均为零。

worker/harness 最终记录 failures=0、command_saturation=0、offered_underdelivery=0、transport_rejected=0、unexpected_exit=false、drain_timed_out=false。197,875 个 planned_cancellations 是停止时尚未运行的计划工作，不能计为已经发送后丢失。work/retry/inflight 报告峰值分别为 219,331/458,000、64/27,072、2,736/27,072，未达到各自上限；work 包括未来排程，不能视为纯就绪积压。transport capacity=0 不提供有效占用分母。

## 32 项首失败的确定边界与缺失信息

三份服务日志中 `internal.access.gateway.send_failed` 共 32 条，节点 1/2/3 为 0/14/18；全部 `sourceModule=message.send`、`source=batch_result_error`、`errorClass=route_not_ready`。经固定短语白名单归类，32/32 的 error 链同时匹配以下源码 sentinel；公开产物不保留原始日志、UID、频道或 ClientMsgNo：

- `permission read admission busy`
- `internal/message: route not ready`
- `internal/message: backpressured`

同时 terminal `wukongim_message_permission_counts_total{kind="cohort_busy"}` 在节点 2/3 恰为 14/18。`pkg/slot/proxy/send_permission_cohort.go:99–103` 是唯一增加该计数的分支：owned calls 达 1,024、owned bytes 超 16 MiB，或新 cohort 需要创建而 active cohorts 达 64 时，直接返回 `ErrPermissionBusy`。该分支在该被拒项的新 permission RPC/读屏障执行前发生。固定额度定义在同文件 13–20 行；这些额度没有被修改。

`internal/infra/cluster/channel_metadata.go:605–607` 将 `ErrPermissionBusy` 包装为 RouteNotReady + Backpressured。Gateway `error_map.go:54–56` 优先将 Backpressured 映射为 SystemBusy；错误分类函数则先命中 RouteNotReady。按这条源码路径推断预期 wire reason 是 `ReasonSystemBusy`，数值 30；此次没有保留逐包数字码，不能将推断写成抓包事实。app 的 `gatewaySendackReasonLabel` 缺少 SystemBusy 分支，因此实际 gateway 指标显示 `reason=unknown`。这是另一个已确认的可观测性缺口，不能据 `unknown` 反推 wire 原因码为 0。

首失败日志时间节点 2 为 03:01:33.862–36.620，节点 3 为 03:01:33.952–33.967。没有 permission/pre-append/submitter duration 或发送开始时间字段，所以每次失败等待多久未知。整体 pending→write 平均约 1.77 ms、最大 12.68 ms，write→ACK 平均约 903.70 ms、最大约 2,097.21 ms；它们是全体尝试的聚合，不能当成这 32 项的耗时。

该分支有三个额度条件，而现场只保留有限切面：first-measured active cohorts 为 37/44/51、owned calls 195/210/231、约 0.80/0.86/0.95 MB。不能据较晚切面唯一断言此前触发了哪一个额度。已定位“cohort 准入压力”，尚未定位到具体额度、慢任务或代码缺陷。

## 100% 队列必须分开解释

final `service_queue_percent_basis_points=[10000,10000,10000]` 是一次 observer 资源轮的聚合最大值。`internal/bench/target/client.go:1239–1248` 只将 `wukongim_runtime_pool_queue_*` 与 legacy `wukongim_channelv2_worker_queue_*` 纳入该值；`production_observation.go` 再取两族最大值。target 在计算后清空了逐 series map，最终报告没有保存胜出 pool。因此该报告的“100%”不能从 final 本身唯一归属某个 pool，也不能直接等同于 delivery 族。

独立保存的稍后 raw first-measured 切面可作如下拆分。三节点是顺序抓取，未单独记录每节点采样时刻；它们不等于 observer 同一切面。

| 实际 raw 指标 | 节点 1 | 节点 2 | 节点 3 |
| --- | ---: | ---: | ---: |
| delivery recipient queue depth/capacity | 1,024/1,024 | 1,024/1,024 | 1,024/1,024 |
| delivery recipient inflight/capacity | 320/320 | 320/320 | 320/320 |
| runtime `component=message,pool=person_directory,queue=task` | 455/512 | 467/512 | 392/512 |
| runtime 最大占用百分比 | 88.87% | 91.21% | 76.56% |
| channel store-append queue depth/capacity | 0/1,024 | 4/1,024 | 0/1,024 |
| channel store-append inflight | 11 | 15 | 5 |

recipient 的 1,024 默认队列和实际 320 worker 均由保留配置/运行指标证实。person_directory 的实际容量为 512。不能把 person_directory 与 recipient queue 的不同指标相加；也不能仅据 append 队列较低排除等待 Raft、回调或 RPC 的成本。这里有真实服务端队列压力证据，尚无持续 15 分钟饱和的证据；资源轮只有一次。

## 投递、RPC 与数据库边界

本轮日志有 256 次 `delivery.plan_incomplete`，节点 1/2/3 为 81/90/85；presence 为 254、owner_push 为 2，均为 durable timeout，集中在上海 03:01:36–39。terminal recipient-worker process timeout 计数也恰为 81/90/85。该组信号与本轮 32 项 permission 拒绝不同，不能混计为丢失消息数；也未与三个 expired sample 作逐项身份关联。

terminal transport client 还保留 presence-authority timeout 322 次、delivery-push timeout 4 次、presence-owner-action timeout 5 次。它们是这组进程的累积指标，覆盖异步请求及停止收口；单位是 RPC，不是消息或 recipient。两个异步切面之间，采样 RPC client duration 的加权均值约为：permission 329 ms、presence authority 369 ms、channel quorum exchange 103 ms、slot propose 353 ms，delivery push 7 ms。不能把各阶段均值相加，也不能据此认定是 TCP/网络本身导致；transport latency 包含对端处理和排队，且原有采样不覆盖每个 RPC。

permission 阶段支持一个更窄观察：例如节点 1 两切面之间，fresh barrier 平均约 267 ms（1,085 次），snapshot 平均约 0.011 ms（1,085 次），RPC 平均约 321 ms（310 次）。在此保留区间，等待 fresh barrier/RPC 完成比读取 snapshot 本身长，可使 cohort 长时间占用。尚不能说明慢来自共享 Mac 调度、Slot ReadIndex/磁盘、批量形态、服务处理或跨节点 RPC 的哪一项。

metadata 创建报告 expected=created=6,796、errors=0，单次 cluster 观察为 12/12 leader/full replica、无 unhealthy。它们反对“全部初始路由或所有元数据均未就绪”这种粗结论，但不是逐次冷路由可用性的证明。独立 sample 轴已通过9/9只读副本查询，核对三个样本分别在三份持久副本中具备同一 MessageID/seq=1；它们是持久成功样本，与32个permission前置准入拒绝不应混为一类。本 signals 轴不重读数据库，也不据存在持久记录宣称已成功在线投递。32个准入拒绝、256个投递timeout、3个持久成功但采样过期项，三者没有自动因果join。

## 资源、平台与连续性

本轮为 Darwin/arm64、Go 1.25.0，一台 10 CPU、32 GiB Mac 承担全部三节点、三 worker、协调器、观察器与同一磁盘。服务 GOMAXPROCS=2，worker/协调器/观察器=1。它不是原四台 Linux 云主机的等价性能环境；独立配置轴已确认 gateway.session_batch_max_records 本地为1，云模板省略该字段后使用源默认128；Slot compaction 本地1000 entries/5s，云模板+源默认为10000 entries/30s。cloud 实际 GOMAXPROCS 仍未知：systemd/template 未显式设置，原 range-go 空序列不能用4vCPU规格替代。还存在 Darwin 与 Linux engine BytesPerSync 参数差异。因此本轮既有拓扑和资源差异，也有直接改变 SEND 批量形态的配置差异。配置对照SHA及只包含相关字段的摘录见 configuration-axis-reference.json；该轴没有将模板推导当作 live cloud 测量。

测量窗口内只有三个独立 ps 切点：UTC 19:01:33.184、38.416、43.498。三个服务 PID 在这三点保持相同；未观察到其他 wukongim/wkcli 进程。各点全系统 ps CPU 百分比求和约 133.5/80.7/62.4；这是逐进程 ps 的 lifetime 百分比，不是独立测量的区间整机利用率，不能据此证明没有调度瓶颈。三个服务最后一点 RSS 合计约 2.36 GB，三个真正 worker 合计约 2.03 GB；没有接近物理内存总量的直观证据，也没有完整的压力/分页区间数据来排除短暂内存影响。

final capacity 的四个 host CPU/内存值相同（14.02% /61.94%），来自同一物理 Mac 的四个逻辑 endpoint，不能当作四台独立主机 headroom。标准 `process_*` 在六份 Mac 产品 scrape 中全部缺失，必须标 missing；只有独立 process collector/ps 数据及 PID 文件，没有标准 process_start_time_seconds 的连续性证明。成功 CONNECT/full-sync 都为 10,000；终态三 worker 同 generation/snapshot=2，10,000 次 generation_stop，其他 close reason 为零。清理 receipt 证实 19:03:16.235699 UTC 所属 PGID 无进程、64 端口无监听；清理完成不等于故障瞬间没有停顿。

Go scheduler/heap/GC 在两个切面有原始数据，但窗口太短，final 只有一个 forced-GC 观察、heap 起止相同，不能判断长期趋势。两个切面之间三节点各新增 4 次 GC；它们不是 per-failure pause 的证据。没有本次故障时 CPU/mutex/block/worker profile；本子任务没有主动采样。

原始 hot SENDACK 平均约 717 ms、P99 直方图上界 2 s，98.77% hot 样本超过 400 ms。report latency_warnings=0 发生在远早于两小时 warmup 完成的短窗口，不能解释为已通过延迟验收。

## 假说与下一步最小对照

| 假说 | 已有支持或反证 | 仍未知 |
| --- | --- | --- |
| 本地 SEND permission cohort 压力 | sentinel、日志、cohort_busy 及拒绝数均 32；拒绝源码边界确定 | 哪个限额首先触发，哪些工作占位太久 |
| 纯冷路由 readiness 导致这 32 项 | 精确错误链证明是 permission admission busy；不是仅凭路由标签推断 | 冷数据 fresh barrier 是否使 cohort 执行变慢；其他样本的路由状态 |
| server-side delivery 拥塞 | 三节点 queue 1,024/1,024、workers 320/320、256 个 timeout | 与三个 expired 的逐项因果关联，最先受阻点 |
| load generator 自己先无法供给 | 初段约 1,987/s；无报告 harness/transport admission 失败，pending→write 较短 | worker GOMAXPROCS=1 的瞬时调度余量、同机争用影响 |
| RPC 退化是底层根因 | 确有 timeout 和数百 ms 的 fresh-barrier/RPC 完成耗时 | 网络、对端排队、Raft、磁盘、调度或批量形态的贡献；不能将现象名当根因 |
| 共享 Mac/较低 GOMAXPROCS 是原因 | 与云拓扑不同，服务只允许 2 个 P；高并发队列已受压 | 缺少单变量对照，不能凭资源规格归因 |

下一次诊断优先只将 gateway.session_batch_max_records 从1恢复至云模板+源默认128，保持二进制、种子、10,000 在线、2,000 群、2,000 primary/s、GOMAXPROCS、Slot compaction、队列容量、超时与清理上限不变。128是批量上限，不代表每批实际均有128条；要核对有效配置与实际batch/cohort形成情况。不能同时调整GOMAXPROCS、compaction或QPS。该单变量仍只做Mac诊断，不能称环境已等价云端。完整可证伪预测及判定矩阵见 batch128-controlled-predictions.json。

若该单变量对照仍出现相同准入压力，再独立对照 service GOMAXPROCS，worker 配额不变，并在两个对照均采用相同的有限采集时序，保留 cohort owned/忙计数、permission barrier/RPC、person_directory、recipient queue 与 host 资源。一个改善结果只提高相应假说的可信度，不直接证明云 RC 根因，更不构成正式性能/长稳验收。本子任务没有启动这些实验，没有修改产品源码或调整任何门槛。

## 产物边界

`source-input-binding.json` 逐一绑定 36 个实际输入；6 份 metrics 的字节与原 receipt 完全一致，诊断后所有输入 hash 再核对未变。34 个已阅读的源码/规则文件都与 79a HEAD 完全一致；同 worktree 中另一 agent 的 E2E 变更有记录，本诊断未修改任何文件。公开 allowlist 只包含本报告、有限聚合、输入/源码 hash 及离线复算脚本；不复制原始私有日志、配置、UID/频道/ClientMsgNo、身份匹配表或凭据。
