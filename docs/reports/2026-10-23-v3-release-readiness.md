# v3 正式版发布复核（2026-10-23）

结论仍为 **NO-GO**。迁移修复及既定 Linux 性能门禁已通过；新增本地 A–B–A 对照支持冷 presence 恢复并发宽度是本地采样过期的重要因素，相应修复已合并。后续权限收集、ACK 观测和目录调度修复也已合并并通过相关 Linux CI，但原规模本地 2,000 SEND/s 仍未通过。仅提高恢复并发至 64 的诊断候选再次失败，未合并为最终方案。当前转为[系统性 RPC 容量与调度设计](2026-10-23-rpc-capacity-system-design.md)，尚未实施。原云两小时预演仍失败、72 小时长稳未完成，生产 stable 签名包公网交付和精确版本 APT/RPM 客户端验收也未完成。

本报告承接 [10 月 21 日的完整历史记录](2026-10-21-v3-release-readiness.md)。旧报告和原失败证据保持原样；新证据收录在 [本次有限公开归档](v3-release-readiness-20261023/README.md)。

## 云 RC

用户明确授权以产品/控制提交 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06` 启动新请求。中断后的只读对账确认此前没有 dispatch；继续沿用唯一请求 `chat-20261021T214505Z-00299e37`，完成精确 main、身份、完整有界 handoff 库存及唯一 open/unlocked Issue 预检后，于北京时间 **2026-10-23 01:14:52** 单次启动 [run 39820950900](https://github.com/WuKongIM/WuKongIM/actions/runs/39820950900)，跟踪 [Issue #1025](https://github.com/WuKongIM/WuKongIM/issues/1025)。

新请求硬上限 **¥1,276.5672**、停止线 **¥1,126.5672**，外部保守保留旧三次报价 **¥223.4328**；整项累计仍为 **¥1,500 / ¥1,350**。报价预留不是已确认账单。四台杭州 4 核 8 GiB、2 小时预演释放后新租约 72 小时长稳、最多 8 小时容量和 30 分钟恢复的计划保持原样。

不可变 bundle 首次构建成功，其完整 ZIP 摘要、110 个成员、9 个 Linux AMD64 ELF 与固定部署意图均已核对。Quote 通过，新租约于北京时间 **01:24:38** 创建，实际四台 4 核 8 GiB、8 盘、1 EIP 与原计划一致，新报价 **¥74.4776**，仍非实际账单；Acquire 前既有 `arm_release` 已成功。

部署子运行 `39822605006` 于 **01:32:06** 通过三节点、256 Slot、12 逻辑组及全部监控目标 readiness。负载于 **01:35:17.578381188** 启动，在 **01:36:18.801475417** 自动停止，实测 **61.223094218 秒**，终态为 `product_failure / message_loss`，两小时预演未通过。SEND 与 SENDACK 均为 **122,002**（包括两条 canary），运行期平均 **1,992.7448 条/秒**；这不是当前滑动窗口读数。三名 worker 均为 final，10,000 个连接均因 generation_stop 关闭，当前验收负载已停止，发送 QPS 为 **0**。worker 守护服务仍 active 不代表仍在发消息。

投递关联采样为 **1,277** 条，其中 **1,276** 条收到、**1** 条在 10 秒期限内未收到。终态证据通过大小、SHA-256、ZIP CRC 和成员摘要校验；[finalizer 39824958109](https://github.com/WuKongIM/WuKongIM/actions/runs/39824958109) 成功只证明采集成功。Hot/cold SENDACK 均值为 **61.37/83.75 ms**，两者 P99 直方图上界均为 **400 ms**，不能当作精确百分位。终态费用估计 **¥20.9398** 也不是实际账单。

离线复核已把过期样本定位到普通群消息，并发现同一群的 presence 查询超时；尚缺该样本到 MessageID/sequence 的唯一关联，不能认定根因。本轮另有 **37** 次 delivery 超时（36 次 presence、1 次 owner_push）。原采集器的 Go 和 metadata/activation 指标名称存在错配，空结果不能解释为零；停止后 profile 不能代替故障时刻资源余量。

指标错配已在测试先行的独立修复中关闭：[WuKongIM #1026](https://github.com/WuKongIM/WuKongIM/pull/1026) 按实际 `go_*`、标准及既有 `process`、精确 Channel 计数名采集，保持 8 请求、60 秒、总 24 MiB、单文件 4 MiB 和 15 秒步长。静态首轮 20 个失败子项、实际 collector 首轮失败均保留，修复后 23 方法/80 场景通过，双轴独审零问题。首次 Linux CI 的默认套件、脚本集成、迁移/消息 E2E、三节点正确性和三组 500 QPS 均通过；该 source 的原生包及 2,000 QPS 条件跳过没有冒称通过。PR 于 **02:51:24** 普通 merge 为 `25a4765be7bed6696e1c9748142bdd61543b508c`，合并树与复核树一致、保护不变，原分支 tip 确已成为 main 祖先，干净工作树和本地任务分支已删除。这只修复采集缺口，没有修复或证明原消息超时根因。

用户于本次状态查询后明确要求：**分析完先结束租约，再本地复现修复**。单次有界被动分析在 provider inspect 全部子任务已终态、总 run 仍持续 `in_progress` 的矛盾处结束；原 helper 安全退出并移除临时目录，从未 prepare 开窗、签发 grant、连接 MCP 或产生 DiagnosisResult。库存读取成功不等于根因分析完成，缺失证据仍缺失。

正式 [Stop 39829057187](https://github.com/WuKongIM/WuKongIM/actions/runs/39829057187) 成功后，原 stage finalizer 收集终态并调用 [Release 39829507204](https://github.com/WuKongIM/WuKongIM/actions/runs/39829507204)。**02:14:57.935** 的 typed provider 收据证明精确原账户/选择器下 12 个资源范围已穷尽且为零，包括实例、磁盘、挂载、EIP、关联、ENI、NAT、路由、安全组规则/组、VPC 和交换机；分析授权没有残留。证明 ZIP 为 558 字节，SHA-256 为 `d816089a61e82b5fcf7bf077a20bbc4ab8b3173efd07485edbfecdb0120e045d`，已核验。cleanup/complete 内容另通过 39 项绑定校验；GitHub finalizer 子任务均成功但总状态仍陈旧，严格阶段终态认证标为未验证，独立 Release 成功及实际资源清零已有证明。没有为解除旧 inspect 的显示状态取消清理任务，也没有新采购。现在仅在本地复现和修复，不自动进入 72 小时长稳。

旧请求在 61.212 秒后报告 `message_loss=1` 的根因仍未知。`79a71770` 包含终态证据采集改进；它没有修复或证明旧丢失样本的产品根因。旧精确资源清零证明和失败记录保留。

## 本地复现

租约清零后才启动本地实验，启动和源码冻结上下文声明 `79a71770`，未为诊断修改 delivery/retry/SENDACK 语义。实际产品产物固定为 SHA-256 `c2531b920ba7b9ff99d777c6bf5d988840426a5dc5f15d66ae0005aa2660b379`；其最初 override 构建链尚缺完整记录，不能直接将以下结果认定为已验证的 79a 候选验收，来源边界见下文。新增 opt-in 黑盒场景复用现有身份和群目录：原群索引 1730、435 成员、5 在线、7 次名义发送；9014 号指纹一致。首次测试的 Manager ID 类型适配错误、其后编译适配错误和即时历史长度断言失败均保留，未计为产品根因。

最后一轮小群自然重放 **78.26 秒通过**：7 个实际 SENDACK、28 个其他在线成员实际 RECV、三个 ingress 各自完整 7 条 history ID/sequence 均匹配。node 2 首次 history 只有 6 条，约 **101 ms** 后在既有 10 秒只读收敛期限内变为 7 条，没有重发 SEND。三产品 PID、12 个监听端口已核零。这是低压力同群诊断，未复现原 10 秒漏收，不能替代原云分布或 Linux 性能验收。

原分布本地诊断保留原 10,000 在线、2,000 群、seed 1、2,000 总 SEND/s 的完整 Workload。首次预检误用了 deployment-only validator，在其正确的 formal guard 处拒绝；未启动服务/负载，64 端口已核零。只修正临时启动器入口为既有 local soak 路径，其严格 `LoadConfig`/`Config.Validate` 保留，未伪装 formal。第二个独立实验于 **02:57:12** 开始总 600 秒监督，实际测量 **13.330 秒**后自然失败：ACK **12,001**、RECV **14,488**、投递采样过期 **3**、首尝试失败 **32**，首先触发 `overall_first_attempt_rate`，coordinator exit 7。这是本地自然失败，尚不能等同于云端根因；Mac ARM64 单机不等价于原四台 Linux AMD64 主机，实际资源压力和发送分母仍需核对。

本地实验于 **03:03:16.235699** 自动完成进程组和全部 64 端口清零；没有延长诊断或第三轮自动重放。必须保留的缺口是上下文恢复跨过了预留 90 秒 live history 查询窗口，没有取得当时三入口现场历史。原始本地 data/config/logs 已保全，随后只作离线身份和只读数据库核对。三个过期指纹都属于普通群，分别指向群索引 1731、1766、1646，各有一次同群 presence 超时，均不是原云样本群 1730；在缺 exact MessageID 关联前不认定同根因。

后续只读检查在复制出的数据库上完成 9/9 次查询：上述三个样本在三个持久副本中均存在，各副本 MessageID/sequence 一致。原数据的 35 个消息文件（76,564,416 字节）前后摘要和修改时间均未变。这证明这些样本已持久化，不能证明在线接收完成；保留日志未出现对应 MessageID，同群 presence 警告仍不是精确消息关联。

本地 32 次发送失败与 `cohort_busy` 计数逐项对齐，错误链均为 `permission read admission busy`，触发于权限读取新 RPC 之前的有界并发配额。首个测量点三节点的 recipient queue 均为 1024/1024、inflight 均为 320/320；另有 256 次投递计划不完整警告（254 presence、2 owner_push）。这些信号证明本地存在背压，尚不能把 32 次拒绝、256 次警告和 3 个接收过期作逐消息因果关联。单点主机 CPU、累计进程 CPU 和缺失的标准 process 指标均不能证明全程资源余量。

实际 TOML、执行脚本与冻结云 bundle 的对照发现两处明确差异：本地 gateway session batch limit 为 1，云模板使用源码默认 128；Slot compaction 本地为 1000 entries/5 秒，云模板默认 10000/30 秒。recipient、person-directory、RPC、append 等主要固定容量相同。本地三节点 GOMAXPROCS 实测均为 2，云端保留证据没有其实际值，不能以 4 核规格代替测量。两者配置及平台并不等价；后续先只对照 batch limit，不同时改并发、超时、队列或压测率。

只改 batch limit 为 128 的单次对照也已结束，保留独立失败记录：实际 24,063 批中 702 批超过 1 条（2.917%，全部不超过 4 条），没有形成每批 128 条的负载。worker 最后切面保留 22,001 次逻辑发送、22,000 个成功 ACK、10 个普通群接收采样过期和 1 个发送重试耗尽；三个 recipient pool 仍满载或接近满载。两轮切面及暴露窗口不同，不能把累计数差异作为配对性能结论。

该对照 coordinator 返回 exit 8，未生成 `final.json`；`harness_invalid / invalid_observation / coordinator_code=finalize` 是 CLI 通用映射，不证明指标本身损坏。收尾多个分支折叠了内层错误，现有证据无法唯一选择实际失败分支。因此保留 worker 观察，整轮仍属终态不完整，不能报告验收通过、批量调整改善或已确认的产品退化。**03:32:16.921200** 所属进程组及全部 64 端口已清零，没有自动重跑。

基线实际 gateway batch 全部为 1，但 permission 自身已通过 cohort 约合并 6 个调用；旧云端八个指标分区没有批次或 permission cohort 信号，云实际形态为缺失。十万成员 canary 已生成且有聚合 ACK，但缺精确发送/投递/冷重建时间；本地失败的代表 recipient 均不属该大群，不能排除其间接压力，也不能直接归因为该群投递超时。

两个确定的观测缺口已由 [#1027](https://github.com/WuKongIM/WuKongIM/pull/1027) 修复合并：在 app 和 metrics 两层保留现有 `system_busy` 分类；从 immutable plan 向同一终态回调传递两个 uint64，再写入原有失败 warning 的 MessageID/sequence。仅新增固定标签值和两个数值字段，不增加身份指标标签、payload、成功日志或业务重试。固定 head `a93837d3fb55369acfe2d131aff52914f55a867f` 的 671 项顶层相关测试（1,019 个通过事件）全绿，分阶段 RED、一次夹具修正和 GREEN 均保留；Standards/Spec 独审均零问题。首次 Linux CI 的 19 项检查中 17 项成功、2 项按事件条件跳过。PR 于 **UTC 2026-10-22 20:01:25（北京时间 10 月 23 日 04:01:25）** 普通 merge 为 `2ab3e1a8bd95670603bc38314036c797a5124682`，合并树与复核及 CI 树一致，保护规则未变；确认目标包含原 tip 后，干净工作树和本地任务分支已清理。这是诊断能力修复，不是原云端消息超时修复。

另一次仅 node 2 有界采样的本地诊断保留了完整终态：实际测量 **10.706 秒**后因 `overall_first_attempt_rate` 返回 exit 7，SEND/ACK **12,001**、首尝试失败 **173**、服务拒绝 **185**、RECV **14,618**，102 条关联采样中 2 条过期。两个样本在三个停止后的持久副本中均可读；这仍不能证明在线接收完成。CPU 捕获为 40,474 字节、窗口约 10.169 秒；goroutine 捕获缺失且未保留足以区分超时、HTTP 拒绝或大小上限的状态，因此整次 profile 为部分证据。**04:11:13.474352** 所属进程组及 64 端口已清零。CPU 样本可以描述实际产物的运行路径，不能证明等待位置、云端根因或 Linux 性能通过。

独立来源审计进一步区分了嵌入标记和实际编译源码：产品 c253 与 CLI e36e 均嵌入 **Go 1.25.11 / a804f4a5 / clean**。该工具链的 Git 根查找只接受 `.git` 目录，跳过嵌套 worktree 的 `.git` 文件，因而会取外层 primary checkout 的标记；不能据此认定执行了旧 a804 源码。两者的反汇编源码路径都指向 79a 诊断工作树；产品包含 79a 新增的 recovery `errors.Join` 分支，CLI 包含对应的预算分支，形成实际编译内容的正向证据。但这些函数见证和保留的 CLI 构建记录仍不能补出最初产品 override 的完整构建链，也不等于对所有输入的完整认证。旧 c253 结果的严格候选资格仍未获完整认证，后续清洁构建也不能追溯补出该旧链。早期冻结记录中将 stamp 直接称为“实际 a804 源码”的结论已被此审计纠正，原始字节与失败记录继续保留。

后续已在 `.worktrees` 下建立具有真实 `.git` 目录的独立本地 clone，以精确 clean `79a71770` 来源重新构建。固定编译器为 **Go 1.25.11**，使用 **CGO=0、trimpath** 及与云工作流一致的 `-s -w -buildid=`；76 个受 Git 跟踪的前端嵌入资源与原 cloud tar 逐字节相同。交叉构建的 Linux AMD64 server/CLI 分别为 `99469fc9ab58c6180c909ccef65b2fdb8ad8fc9f0b91161292bda9b69b0eed00`、`43dd65554569ef29596d5a21349f0a3e08c4093652413afbd2f654f1856c884b`，均与原云 ELF 逐字节相同。首轮未带完整链接参数的 Linux 产物不相等记录保留，其差异不能归为源码错误。新 native Mac 产物 5b307…/081948… 来自同一精确源码；构建绑定成立不等于 Linux 上的执行或性能验收。

使用上述新 native 产物的一次原分布诊断于 **UTC 2026-10-22 20:42:06.546400（北京 10 月 23 日 04:42:06.546400）** 进入 first grant；终态报告覆盖 **10.742 秒** 的窗口（含停止/收尾），首个失败原因为 `overall_first_attempt_rate`，coordinator 返回 exit 7。该窗口结束不是已知的 first gate 触发或 active SEND 结束时刻。ACK **12,001**、首尝试失败 **160**、拒绝 **172**、RECV **14,433**；102 条关联样本中收到 100 条、过期 2 条。CPU **42,936** 字节及 goroutine **25,282** 字节均完整采集，curl exit 0、HTTP 200；这是带采样扰动的 Mac 诊断，不能报告为性能通过或已确认云端根因。监督启动后 **356.976 秒**，于北京时间 **04:43:49.817745** 完成 PGID 8675 和全部 64 端口核零。首次测量指标切面到终态的 Pebble compaction count 总和为 **9→9**；该切面不覆盖 first grant 后约前 5 秒，也没有源码映射证明它等同于物理 Slot 日志压缩，因此不能据此确认或排除根因。


独立离线分析已读取此次完整 goroutine 捕获：**UTC 20:42:11.818699** 的 node 2 单点共有 **4,928** 个 goroutine，可识别 **204** 个冷 presence lane 等待、**212** 个 ReadBarrier response 等待，以及 **7** 个已经通过 admission、停在 metadata `pending.done` 的等待。两个 owner admission holder 正在远端调用，单节点栈不能把远端持有者与这些上游等待强行逐一关联。320 个 delivery worker 的互斥分组为 **209 远端 presence + 57 本地 lane + 46 leader join + 1 owner RPC + 7 owner push = 320**。这是一次栈切面，不是等待时长或整个运行窗口的分布；metadata commit 内部分阶段指标仍缺失，因此尚不能确认因果链或将其归为原云端根因。

共享 RPC executor 的总容量由各 service 额度相加，并分别排队；不能把一个 service 的额度当作整个 executor 固定总容量 128。该单点保留的 owner **0/0**、quorum **0/2** 只反驳“固定总 128 pool 已耗尽”的特定解释，不能据此排除其他队列争用或证明全窗口资源余量。

收尾诊断缺口已创建 [#1028](https://github.com/WuKongIM/WuKongIM/pull/1028)，固定 head 为 `3d243bccc8caed21a8719071ad7c1883156713c2`。四个生产文件仅补充有界诊断；相关测试为 **866** 个顶层通过、**1,613** 个通过事件。首次 Spec 复核发现七项必需场景遗漏并报告 P2，补齐测试和夹具边界后，第二轮 Standards/Spec 均为零问题。首次 Linux CI 的 **18** 项检查均已终结，**16 项成功、2 项既有事件条件跳过**；三节点 500 QPS、完整 scripts、迁移 E2E 和四发行版原生包生命周期均通过。Manager 工作流未触发、2,000 QPS 比较按条件跳过，不计作该项验收。#1028 于 **UTC 21:26:01（北京 10 月 23 日 05:26:01）** 普通合并为 `d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e`，合并树与复核/CI 树一致；保护和禁用的 Review Agent 状态未改变。确认原 tip 是 target 祖先并检查干净后，已移除任务工作树和本地分支；primary checkout 保持原样。 这不会追溯恢复旧 batch128 收尾丢失的内层错误，也不是消息丢失修复。

降至 500 总 SEND/s 的提案先经过既有严格 `LoadConfig`：只改 rate 的第一版因 burst 合同被拒绝，该失败保留；合法第二版同时设 **rate 500、burst 1,000**，保持原两秒 credit。它是两个有派生关系字段的变更，不能称为仅修改单一字段。一次已停止的诊断在 **UTC 21:08:58.095096（北京 10 月 23 日 05:08:58.095096）** 进入 first grant，保留的 worker 终态切面为 **SEND=ACK 8,001、首尝试失败 0、拒绝 0、RECV 9,605、5 个接收样本过期**。coordinator 随后在 `finalize` 返回 **exit 8**，没有 `final.json`，故 worker 的 `product_failure` 观察不构成完整终态报告或验收通过，也未证明与原云失败同根因。该次仍使用精确 79a 产物，#1028 的新诊断未在此运行中生效。监督启动后 **276.718 秒**，于 **UTC 21:09:22.115253（北京 05:09:22.115253）** 完成所属进程组及全部 64 端口核零，当前没有继续发送。

离线补充只读取既有证据：五个过期样本均唯一对应原分布中的普通群；三个 canonical service 日志在 first grant 至终态 worker 切面之间共有 **207** 次 presence deadline 失败，owner push 为 0。该汇总没有精确 MessageID 关联，不能将五个样本逐一归到这些警告。node 2 首个保留指标切面 queue **14/1,024**、inflight **40/320**，并未在该点占满；切面仍不覆盖整个窗口。

已有 goroutine 捕获的离线互斥分类显示：**UTC 21:09:03.284063**，node 2 的 **320** 个 delivery worker 中 **291 空闲、12 等冷 presence lane、9 等 leader join、3 远端 authority、1 owner recovery、4 owner push**。全体 47 个 `recoverPage` 调用中 45 个等 lane、2 个持 lane 等 owner RPC。这让跨 owner I/O 持有多个 lane 导致无关冷 UID 排队的机制值得受控检验；数量不证明等待时长、五个过期样本经过该处、owner 未响应原因或原云端根因。下述受控 recovery 边界失败现已保留，仍需结合实际样本关联再决定产品修复；不新增一层重试、不放宽 expiry 或验收门禁。原始 profiles、运行身份和配置仍不入库。

已另从具有真实 `.git` 目录的精确 clean `d349a7bd` clone 构建新 native server/CLI，固定 Go 1.25.11、CGO=0、trimpath 和原云链接参数，76 个嵌入资源与既有来源一致。79a→d349 的 20 个变动路径及生产 diff 已独立核对；变化只补观测和收尾诊断，未包含 presence 投递修复。native server `6b088040…42e2af`、CLI `53e603d9…826e7cc` 的构建绑定不等于 Linux 执行或云 ELF 相等。

在配置与前次 500 诊断逐字节相同的对照中，新 d349 产物于 **UTC 21:39:44.005837（北京 10 月 23 日 05:39:44.005837）** 进入 first grant。约 +15.034 秒的保留切面为 **SEND 7,501、ACK 7,204、首尝试失败 0、1 个过期**；终态保留切面为 **SEND=ACK 8,001、首尝试失败 0、拒绝 0、RECV 9,458、5 个过期**。#1028 首次把收尾失败定位到 **`finalize / meta_checkpoint / invalid`**；冻结的产品观察保留 **`product_failure / message_loss / terminal=true`**，CLI 仍为 **`harness_invalid / invalid_observation`、exit 8**，`final.json` 依然缺失。这区分了保留的产品失败观察与完整报告不可用，未确定 metadata 的内层不变量或投递根因，也不是完整 120 秒或正式性能验收。CPU/goroutine 采集完成只表示捕获有效；本批未包含其后续分析。于 **UTC 21:40:08.573206（北京 05:40:08.573206）**、监督启动后 **284.852 秒**，所属 PGID 30175 及 64 端口均已清零，并作第二次只读核对。

公开 `AuthorityRecovery.EndpointsByTargets` 的 tests-only 受控实验已在精确 `2ab3e1a8` 来源保留 **1 个 intentional RED、8 个 PASS**。A 用每个 lane 一个冷 target 占住全部 32 lanes，并暂停在可控 owner 端口；B 查询同 target 下另一个冷 UID，owner 本可立即响应，却在 A 释放前于 250 毫秒截止，owner 调用数仍为 1。非冲突 lane、同 UID 证明复用、取消 waiter 隔离及五项既有合同均通过，全部新调用 joined、所属进程组清零。生产与规则文件未变，该分支仍未提交且不可合并。

该 RED 证明粗粒度 admission 的 head-of-line 机制，并提出独立冷 UID 隔离性质；现有注释本来就描述固定 lanes 和页共享 timeout，因此它本身不能升级为正确性缺陷或原云根因。独立静态挑战要求保留成功 siblings、fence/tombstone、FIFO 淘汰后重读、取消语义及有界数据所有权，并强调 fake owner 可用与生产 owner 全局不可用之间的区别。后续 full distributed target+UID flight 目前只有私有机制设计和 **19 项实现前失败矩阵**；该批冻结的早期设计为 **4 cohorts、2,048 keys、4 MiB owned UID bytes，并限制每个不可拆分页不超过 1 MiB**；其中字节预算不是既有 RPC 上限或已批准生产默认值。后续实现前正在复核该字节上限与既有 UID/页 packing 的兼容性；早期设计原件保持不变，后续调整尚未纳入完成收据。**本次仍无完成的生产实现、GREEN 或性能收益证明**。owner 等待区间、因果关联及同条件前后对照仍缺；不能把无关 UID 的 RED 或单点栈数量当作五个过期样本的因果闭环。

在独立封存的离线补充中，按 ordinal 排序选出的首个过期样本（worker 0、ordinal 114、普通群 index 1656）现已完成精确关联：三个停止后的只读副本各返回唯一完整行，均为 **MessageID `2113384847920603137` / seq `2`**，其 ClientMsgNo 指纹与过期样本一致。同次运行 node 2 于北京时间 **05:39:57.176** 恰有一条相同 MessageID/seq 的 **presence / timeout / durable / deadline_exceeded** 警告。它把该样本直接关联到 presence 阶段超时的投递 plan；仍不解释具体 lane/owner/底层超时来源，不独立证明当时的线上接收历史或原云端同根因，其余四个样本也未做 DB/MessageID 查询。三次有界查询均只打开私有只读副本并 joined，原 36 个文件、50,732,443 字节及原证据/日志的哈希与 mtime 均不变。原始身份、SQL、查询输出、DB 和运行日志继续私存，公开内容只含固定关联字段和完整性引用。

另一次已封存的 metadata 结构重放在精确 d349 来源、Go 1.25.11 下首次单次通过（总 **8.223 秒**，测试包 **0.580 秒**）。六份既有 scrape 通过真实 `target.Client.Metrics` parser、`MetaCreateAccounting` 和 `ProductionMetaController`，但使用了明确 **synthetic 的空 expected vectors 和 final worker shapes**。这仅验证附近切面的解析及结构一致性；实际 terminal worker vectors 和发生失败的精确 metrics round 未保存，故无法识别原 `meta_checkpoint/invalid` 的 leaf，也不能宣称原 checkpoint 成功。首测 created 总数 690/690/726、保留 terminal 总数 1,465/1,447/1,471 不是实际 expectation 的替代物；负向守卫仍拒绝无效 worker/result，原报告 completeness 门禁保持。该重放只编译测试包、未启动服务或负载；其 leaf 诊断方案和 UID 隔离方案已获 tests-first 实现授权，但本批不包含未封存代码、后续实现或 GREEN，发布结论仍为 **NO-GO**。

在后续封存的 tests-first 实现中，presence 候选 `5a8c42fa69ddc15ef341e2992148c7c7fc36eb79` 已把跨 owner I/O 的粗粒度 lane 互斥改为UID 与五项分布式 authority 身份（不含本地 RouteRevision/AuthorityEpoch） 的私有 flight。实现前固定的最终合同保留 4 个 leader cohorts、2,048 个 keys，新增 owned UID 字节预算为每个不可拆分 subset **32 MiB**、registry 合计 **128 MiB**；该预算约束 registry 新持有的 UID 字符串，不是进程全部堆内存或 RPC 上限。早期 1 MiB/4 MiB 设计不符合既有最大 UID/512 页 packing，已在写实现前修订，旧设计原件继续保留。实现没有增加 goroutine、重试、超时、公共配置或 owner 并发；成功完成后重新读取 Directory，失败完成可直接传播 typed failure，FIFO 淘汰不制造 offline。

原 tests-first 场景在旧代码保留 **23 PASS / 13 FAIL**，相同测试在候选为 **36 PASS / 0 FAIL**；完整 presence/runtime 测试与 race（各 **106** 个通过事件）、真实 owner adapter 的 **4** 项 race 均通过。收尾诊断候选 `486ec5f154b120b9daedcf5a427d51966a82e06c` 增加 **13** 个固定 metadata leaf reason，保留既有 `invalid` fallback，未修改 accounting、worker schema、verdict、完整报告或退出门禁。它的首轮 RED 为 69 个失败/3 个通过事件，GREEN/race 各 130 个通过事件，两包完整 **871** 个顶层测试、**1,685** 个通过事件。该补丁帮助定位错误，未修复或识别旧 d349 收尾实际失败的 leaf。

两项实现已合并为本地候选 `3e668ac4733ae403e57242ddec559e6d937b0d3c`，各独立组件的生产与测试字节保留。首轮 Standards 为零问题，Spec 报告一处 P3 文档表述过宽：成功等待者需重读 Directory，失败状态可直接返回 typed failure。仅修正文档和生成的 FLOW index 后，固定 head 为 **`c4954c6a638080e6a915ecec305a3381f4afd9a0`**、tree `98fead03ba0c727dd733f742790e7737327f3942`；两轴关闭复核均为零问题，全部 4,202 个 Go blob 与首轮 head 相同。首轮发现、第一次 FLOW 检查失败与既有 20 条 advisory warning 均保留，不能把组件通过数简单相加称为完整合并套件。

另一次静态挑战已反驳“独立全局 created counter 与 per-slot Counter 在 Gather 时各自取样导致 scalar/sum 矛盾”的特定解释：当前 parser 直接从同一组唯一 per-slot 样本同时构造两种表示，重复、非精确整数和溢出会被拒绝。不同 series 不是同一时刻的事实不等于上述矛盾成立。该挑战没有执行新测试或服务，不能由此认定旧失败 leaf、message_loss 根因或原 checkpoint 成功。

首轮 `3e668` 与文档修订后 `c495` 均有独立、clean、真实 `.git` clone 的 native 构建和有限 500 SEND/s 提案。`3e668` 构建后没有启动负载；后续只对 `c495` 启动一次原分布本地实验，保持相同配置、采样、门禁和 600 秒总监督预算。10000 用户于 **UTC 22:29:07.269544（北京 10 月 23 日 06:29:07.269544）** 进入 first grant。+20.019647 秒的保留切面为 SEND 10,002、ACK 9,738、首尝试失败/拒绝 0、1 个过期；终态 +22.822771 秒为 **SEND=ACK 10,502、RECV 13,043、首尝试失败/拒绝 0、1 个过期**。这些是累计 worker 切面，不能当作有效最终报告或完整 120 秒结果。候选仍复现了过期，未证明实际负载修复。于 **UTC 22:29:36.543145（北京 06:29:36.543145）**、监督启动后 282.848405 秒，所属 PGID 50713 和全部 64 端口已核零。

本次新诊断把收尾失败细分为 **`finalize / meta_checkpoint / metrics_fetch`**，冻结产品观察仍为 `product_failure/message_loss/terminal=true`，CLI 仍为 `harness_invalid/invalid_observation`、exit 8，`final.json` 缺失。另保留一次准备期额外读取错误：错误猜测 root review 的 manifest basename 导致 FileNotFoundError；实际 supervisor 来源/产物/进程/端口 guards 均通过，root 澄清并核验真实清单，明确继续已启动的一次实验，没有重启或修改冻结输入。该限制不从记录中删除。

仅解读既有 node 2 profiles 的离线分析显示，**UTC 22:29:12.473778** 的 2,554 个 goroutine 中，320 个 delivery worker 互斥分组为 **296 空闲、10 presence leader join、7 owner push、5 remote authority、1 owner recovery、1 registry quota wait**。全部 12 个 recoverPage 调用为 **8 quota wait + 4 owner-read holder**；无旧 cold-lane 等待，四个 holder 都停在 Conn.Call。它描述候选已改变等待形态，不能确定 quota 的具体绑定上限、等待时长、远端 RPC 根因，或把唯一过期样本强行关联到某条栈。CPU 窗口 10.119805541 秒、2.28 秒 sampled CPU 也不能当作磁盘延迟或 Linux 容量。原始 profiles 与完整解码私存。

六份附近已保留的 metrics scrape 在精确 c495 的真实 `target.Client.Metrics` 和 in-memory RoundTripper 中单次重放全部解析通过（1.810 秒）；未启动 TCP、服务或负载，输入字节/哈希/mtime 不变。**实际失败的 fetch round 未保留**，附近成功既不能复现也不能反驳该次失败，无法区分网络、HTTP、body 或 parser 分支，也未恢复实际 worker vectors 或完整终态。

[#1029](https://github.com/WuKongIM/WuKongIM/pull/1029) 固定 head `c4954c6a638080e6a915ecec305a3381f4afd9a0` 的首轮 Linux CI 已完成：实际 checkout `15289a0a91d1b858db667f7783955d022f965913` 与复核树相同，14 个 checkout job 均已绑定；原四个工作流 attempt 1 全部成功，18 项检查为 16 成功/2 条件跳过。更新 PR 正文后另一次 credential-free Issue Signal 成功，最终表为 19 项、17 成功/2 跳过，不是回归补跑。默认 named check 为 219 package summaries + 15 no-test-file summaries；脚本集成为 1,317 个通过事件、7 个 package-pass、6 个显式 opt-in/subprocess skip、零失败，cleanup 完整。新 presence 并发合同带 integration tag，默认 Linux 套件不构成其逐项 Linux 执行回执，本地 integration/race 不重标为 Linux。

五种迁移拓扑（含 1→3）、四项跨节点投递、一/三节点 self-chat 整群重启、三类各三窗 500 QPS 和四发行版真实 systemd 原生包 lifecycle 均通过。Manager 未触发，2,000 QPS opt-in 比较跳过，不构成容量或浏览器验收。五份公开 proof ZIP 的 59 个 UTF-8 成员均校验大小、SHA、CRC；private self-chat 原始报告/ZIP 排除，大 correctness/native ZIP 只保留 API 元数据，未下载验字节。随后已完成普通合并（见下文）。这些 focused CI 通过不覆盖同候选原分布过期与缺最终报告，也不证明原云同根因、72 小时长稳、正式包公网交付或 GA 就绪，结论仍为 **NO-GO**。

#1029 已普通合并为 **`27fa6d63cf3b2b6c3a9a28575f917d60cc054ded`**，合并树与复核及首轮 CI 精确相同。合并前再次核对 exact head、权限、保护规则与评审状态；没有改保护，Review Agent 两个工作流继续保持 disabled。四个已合并的干净任务 tip 均确为 target 祖先，随后移除对应 worktree 并不带 force 删除本地分支；初次清理拒绝及其修正记录保留。primary checkout 仍 clean `a804f4a5`。此合并只交付已复核的 UID 隔离和收尾诊断能力，没有把失败的原分布实验改判为通过。

候选唯一过期样本现已完成独立封存的精确离线关联：worker 2 / ordinal 314 / 普通群 index 1646，在三个停止副本各返回唯一完整行，均为 **MessageID `2113397295402721287` / seq `1`**。同次 node 2 在北京时间 **06:29:20.098** 恰有一条相同 MessageID/seq 的 **presence / timeout / durable / deadline_exceeded** 警告，位于 first grant→终态切面窗口内。这直接关联该过期消息的 presence plan 超时；仍不定位下层等待来源、实际历史在线 RECV 或原云同根因。该窗口 canonical incomplete warning 共 75 条（74 presence、1 owner_push），窗口外 184 条单列，不能混作运行窗口计数。

该关联只执行已复核 v2 驱动的一次有界调用，3 次查询 **0.367391 秒**内全部完成、无重试/其他群、owned child/group joined 并核零。36 个原文件、**66,553,406 字节**的 size/SHA/mtime 前后一致；先核验固定 expected inventory SHA，再校验完整原件，全部私有副本 path/size/SHA 在 CLI 打开前一致，原 DB 未打开。旧 v1 提案原件也保留，并修正首次审阅中的误述：**v1 已将当前 inventory 与 expected file 比较**，缺的是 expected-file 固定 SHA pin、复制后完整性和覆盖整个进程的外层预算等 guards，v2 才补齐这些边界。公开原件不因后续修正被重写；raw UID/channel/ClientMsgNo、驱动/SQL、查询 body/error、DB 和运行日志继续私存。

为进一步解释 `metrics_fetch`，已封存 **54 项写代码前 failure/control cases** 和 option A 设计，并获 root 的实现批准。设计采用 typed Metrics 边界和固定三来源诊断投影，保留原解析、三源 joining、parent context 优先、timeout/retry/accounting/verdict/report 门禁；冻结 README 的“awaiting approval”是当时历史状态。**本批仅归档设计，实际实现、后续测试/独审/CI 和 owner 静态跟踪仍进行中且排除**，不宣称新的修复完成、原失败 leaf 已解或发布通过。

后续已交付 [#1030](https://github.com/WuKongIM/WuKongIM/pull/1030) 的有界 metrics-fetch 诊断。固定候选 **`56c287493a5e9fc3df65ea0631b99612672cd678`**、tree `582e5bc024147b32aa68074d04c041391af89b96`，在原有一次 HTTP 获取循环中记录封闭错误类别，并只在 `meta_checkpoint/metrics_fetch` 投影固定三来源 mask/vector；来源位置按 `observation.service_nodes` 声明顺序。私有三个原因不进入公开文本，aggregate 仍只 unwrap 既有 HarnessInvalid；三源 joining、parent context 优先、fallback、解析、所有超时/重试/accounting/report 门禁保留。它是观测修复，不是剩余消息过期修复。

54 项实现前矩阵的首轮 RED 保留 **127 失败/325 通过事件**，其中 Authorization 夹具误漏既有 `Bearer ` 前缀；在生产仍未改动时修正夹具后，RED v2 为 **122 失败/330 通过事件**。相同冻结 v2 用例在 GREEN 与 race 各 **452 个通过事件/22 个顶层测试**；target、chatlifecycle 及全部 wkcli 包完整回归为 **2,452 个通过事件/1,109 个顶层测试**，无测试失败/跳过，另有三项 no-test-file package。13 个变更路径、未改 parser/helper suffix 与 finalization 门禁均有来源绑定。Standards/Spec 独立复核均为零问题，首轮错误和 v2 测试先行字节保持。

#1030 的四个原始自动 CI run 均首次 attempt 1 通过，18 项检查为 **16 成功/2 既有事件条件跳过**。实际 checkout `a514c73b4c950b9ead150c28ff08ef89123f5238`、复核 head 与合并树一致；完整默认套件、scripts integration、五种迁移拓扑（含 1→3）、跨节点投递、1/3 节点整群重启 self-chat、三类各三窗 500 QPS、四发行版真实 systemd native lifecycle 均通过。2,000 QPS 比较、Manager 未触发及工具 cosign 缺失等边界保持明确，TEST ONLY native 不等于正式渠道发布。五份公开 proof ZIP 的 59 个 UTF-8 成员完整校验；原始 self-chat 身份报告/ZIP 私存，大 correctness/native ZIP 仅 API 元数据。于 **UTC 23:26:45（北京 10 月 23 日 07:26:45）** 普通合并为 **`8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf`**，确认 target 包含原 tip 后清理干净 task worktree/分支；保护和禁用的 Review Agent 未变，primary 仍 clean a804。

新 native 产物来自真实 `.git` 的独立 clean 56c clone，固定 Go 1.25.11、CGO=0、原链接参数，76 个嵌入资源与既有原云来源逐字节相等；执行产物的完整 hash、buildinfo 和 clean source 绑定公开。构建不构成 Linux 运行/容量验收。三节点 sampler 的 V1 独审发现两项 P2：未拥有 claim 的失败路径可覆盖旧状态，以及已退出 helper root 的后代清理所有权缺口。V2/V3 的限定修正和失败记录分开保留，V3 复核关闭两项发现，但 unknown orphan/OS 调度硬上限仍不能由局部 fixture 证明；实际全 owned-PGID 最终清零仍由外层 supervisor 负责。最初 sampler-only 方案尚无新产品 revision/build，不能把后来 56c 绑定倒灌进这些历史原件。

随后一次已批准的 56c 原分布 **500 SEND/s**、三节点同时请求采样的本地实验于 **UTC 23:35:08.707503** 启动监督，**23:39:22.590978** 进入 first grant。+20.019 秒切面为 SEND 9,988、ACK 9,744、零首尝试失败、1 个过期；终态 +22.904 秒为 **SEND=ACK 10,502、attempts 10,651、RECV/RECVACK 13,379、retry 149、1 个过期**。这些均为累计 worker 切面，不能作为完整 120 秒报告或修复成功。launcher exit 8、完整 final report 仍缺失。冻结产品观察继续保留 `product_failure/message_loss/terminal=true`。

本次首次得到严格校验的 **`source_mask=4`、`kinds=["", "", "parse"]`**，仅说明本轮声明顺序的**来源位置 3**在 `meta_checkpoint/metrics_fetch` 被分类为 parse；未由此绑定物理节点、原始 parser 失败分支，更不能追溯定位旧轮失败或认定消息过期根因。三节点六次 CPU/GOR 请求均 HTTP 200/curl 0、无重试，原 profiles 私存。后续已封存本轮三节点 profile 解读、六份近邻 scrape 回放及独立 DB/MessageID 关联，详见下文；旧 c495 关联不作为新运行的替代证据。**UTC 23:39:51.821770（北京 07:39:51.821770）**、监督启动后 283.111 秒，PGID 64933 和 64 端口核零；后续再核无其他产品进程，原输入/私有证据 SHA/size/mtime 均保持。

另两份 c495 静态复核已封存具体反驳：owner read op9/10 读取 owner 本地 active Registry，不经过不存在于该来源的 UIDSessionView/metadataReadBarrier、Slot ReadIndex 或持久 UID 存储；`Conn.Call` 等待栈也不证明请求已进入远端 handler。proof FIFO 的 1,024 UID 容量是**每 authority HashSlot**，固定十万 roster 在 256 HashSlot 各 333–465 人，且 512 页结果先返回并累积，故“全 roster 必须同时驻留并自我淘汰”的假设不成立。混合工作集并发淘汰、quota 与 RPC 生命周期仍未量化；这些反驳和已有 GC/队列指标不构成新运行或根因证明。

针对 Service13/14 混合读写服务，目前仅封存真实 transport cancellation 的最小 integration 设计与 failure matrix：caller 取消后，已开始的 handler 可继续使用服务执行 context；可用同连接 capability ACK 建立远端已处理取消的有限测试栅栏，同时必须保留修改恰好一次的语义。**该设计未实现、未运行，也不支持直接把整个混合服务设为 CancelRunning=true**。当前应继续取得受控失败和精确阶段证据，原门禁不放宽；正式发布仍为 **NO-GO**。

三节点已保留 profiles 的一次离线分析现已封存，三个 GOR 切面分别在 **UTC 23:39:27.728675 / .732950 / .746700**。各节点 recoverPage 调用分别为 **19 / 26 / 16**，互斥分组各为 **15 quota wait + 4 owner RPC、22 + 4、12 + 4**，UID-flight join 均为 0。它们是三个独立切面，不能相加称为同一全局瞬时 49 个等待者；quota select 位于解锁之后，也不是持 admission mutex 的证据。各节点 idle delivery 为 303/320、298/320、285/320，node 2 另有一个无法归类的 delivery worker，未强行塞入其他类别。未采到 Mutex/RWMutex、owner-registry read 或 server ReadOwner 帧不能排除别时争用，也不能由 Conn.Call 推定远端执行位置或等待时长。CPU 三窗口的 on-CPU samples 分别为 **2.08 / 2.10 / 2.16 秒**，不是磁盘/off-CPU 延迟或性能收益；active SEND 结束时刻仍未知。

本次离线执行的限制明确保留：前三个 GOR Python worker **继承执行器环境，未遵守原计划环境 allowlist**；源码/AST 检查未见环境读取、子命令、网络或环境输出，仍不能追溯称其符合 allowlist。CPU worker 和 12 个 pprof 过程使用显式 allowlist，未为修补历史重跑 profile。首个 GOR worker hash 为事后按记录重建，非执行前当场 pin；decoder stdout 仅固定小 schema 加**完成后**字节检查，不是 streaming cap。18 个合成 adapter 控制用例实现前准备，首 RED 为缺失模块而非 18 个数据分支实际执行失败，首次实现的 macOS alias 守卫失败也保留；原 decoder 语义及分类优先级未改。全部原输入 size/SHA/mtime 不变，raw profiles、完整 traces/stacks 私存，公开只含固定 symbol/line/count 汇总。上述限制不掩去，也不以无超限结果代替完全符合计划的声明。

精确 56c 的六份已保留 first-measured/terminal scrape 经单次真实 `target.Client.Metrics`、in-memory HTTP 响应回放，**1.670685417 秒全部解析通过**；没有 parse leaf 失败或 private cause 文件，没有 DNS/TCP、服务、负载或 DB 请求。原输入及六份私有备份 SHA/size/mtime 不变，临时 one-off test 校验后移除，干净 detached worktree 无 force 清理。**实际失败的 checkpoint response 未保存**，附近成功既不能复现也不能反驳 mask4/parse，不能推断错误指标行、parser leaf、GaugeVec/Gather 因果或完整 final accounting。

本轮唯一过期样本已通过一次独立有界离线查询精确关联：三个预先完整校验的私有只读副本均返回唯一完整行，MessageID 为十进制字符串 **`2113414966718509057` / seq `1`**；同次 **node 3** canonical 日志在北京时间 **07:39:33.369** 有唯一相同 tuple 的 **presence/timeout/durable/deadline_exceeded** 警告，处于本轮 first grant→终态窗口内。这仅确定该样本的 presence plan 失败阶段，不证明历史线上 RECV、下层等待来源或原云同根因，也不把 metrics 来源位置 3 与物理节点作额外绑定。3 次查询 **0.382091 秒**完成，无其他群/重试，owned PGID 71330 独立核零；36 个原文件、**66,550,277 字节**的 size/SHA/mtime 前后及最终复核一致，全部副本 path/size/SHA 在任何 CLI 打开前匹配，原 DB 未被 engine 打开，warning 原件不变。SQL、驱动、UID/channel/ClientMsgNo、查询 body/error 和 DB 继续私存。

为后续真实 parse 失败取得更细且封闭的原因，已封存基于合并 `8dfcd35b` 的 **52 项写代码前 failure/control cases** 与 typed parser leaf 设计，root 已批准 tests-first 实施。设计保留 20 组原错误条件/文本、原判定顺序、界限和 acquisition/settle/accounting/verdict；用真实私有 typed cause 识别，不按 Error 字符串猜测。可选三来源 parse_reasons 保留 legacy absence，不能补造历史 unknown 或改变既有 mask/kinds 的有效性。**第十五批当时仅归档设计；后续已完成的实现和验证见下节**；真实 transport cancellation 独立方案也仍属 prepared，不是本次过期的因果证明或 GA 通过。

## 后续修复与受控对照

本节更新第十五批之后的完成状态；前述早期段落及封存材料中的 `pending` 均指各自冻结时点，原失败与限制不被新结果覆盖。证据入口为[第十六批有限清单](v3-release-readiness-20261023/COPY_BATCH16_ALLOWLIST)。

[#1031](https://github.com/WuKongIM/WuKongIM/pull/1031) 已交付 typed parse leaf 诊断及 legacy 字段复用兼容修正。精确候选 `2662fe93b754d614422f2936185cf781a33b831a` 的测试先行、差分兼容回归、相关 normal/race、第二轮双轴复核和最终 Linux CI 均已封存；普通合并为 `364253b3be532acb023757791a54b85d4c76cbd2`，合并树与候选树一致，干净任务工作树及本地分支已普通清理。该改动只细化有界诊断，不改变原 parser 判断、超时、重试或验收门禁，也不是投递过期修复。

随后精确 2662 的三节点 trace 诊断仍观察到 **4 个采样过期**，并把收尾错误细分为 `meta_checkpoint/metrics_fetch`、来源位置 3 的 `parse/invalid_queue_utilization`；仍为 exit 8、没有完整 final report。实际失败 response 未保留，不能由相邻成功 scrape 反推具体错误指标行。三份原始 trace 只私存；离线首轮完整事件读取触及输出上限、三份 sched profile 解码失败的记录保留。后续一次有限补充兼容合法的可选空符号，并以完整 EOF 读取接受三份 trace，未重跑原产品负载。各类 profile 的计数与累计等待可以跨 goroutine 重叠，sched 数值采用工具自身定义，不是逐 RPC 延迟、精确 runnable 区间或逐消息因果关联。六份固定 scrape 的修正别名分析观察到 authority 队列等待，但首次切面缺失的 timeout series，其差值和比率仍为未知；端点、每 32 次抽样直方图及 trace 窗口不能证明整个运行期资源余量。

[#1032](https://github.com/WuKongIM/WuKongIM/pull/1032) 独立复现了同一 queue 的 Gauge 值在收集与写出之间被更新而形成混合观测的缺陷，改为一次冻结 depth/capacity/bytes 四项值后发布不可变指标。冻结用例先 RED 后 GREEN，相关 normal/race、双轴复核及首次 Linux CI 通过；精确候选 `b47c03d461a6d8918e537e027bf56539ce78c585` 普通合并为 `9e5d8eeadb7b4c1998857640d2160c3af3232996`。原失败 scrape 缺失，不能追溯声称已证明它由此缺陷造成。后续不采 profile 的 A 对照恢复了有效的 `product_failure/message_loss` 最终报告，但仍有 **2 个采样过期**。

A 的两个过期样本经一次有界离线调用，在三个预先完整校验的私有 DB 副本中各读得唯一、相同的 MessageID/sequence，并分别与本轮 node 1/node 3 的精确 **presence/timeout/durable/deadline_exceeded** 警告关联。6 次查询在 **0.660427 秒**内完成，原 36 个文件、82,376,917 字节的 size/SHA/mtime 未变，原件未被 engine 打开，所属进程组已独立核零。这把两个样本定位到 presence plan 失败阶段；持久行不证明在线 RECV，日志没有 ChannelID/ChannelType，也不解释内部等待来源或原云同根因。见[两样本关联收据](v3-release-readiness-20261023/cloud/full-original-distribution-b47c-rate500-unprofiled-control-v1/expired-two-offline-addendum-v1/README.md)。

相应修复候选 `4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac` 仅把冷恢复 leader cohorts **4→16**、key 配额 **2,048→8,192**、owner admission **4→16**；registry 新持有 UID 字符串的合计预算显式固定为 **128 MiB**，不是进程总堆内存上限。32 MiB 页预算、512 UID/256 targets、原超时/重试、proof/fence、取消语义和全部验收门禁保持。实现前冻结失败矩阵和用例，保留旧实现 RED；新宽度、17th waiter 取消、完整 key 配额及有空余 cohort 时的真实 128 MiB 字节边界在候选通过。Mac 的相关 normal/race 和独立双轴复核通过；重型字节场景只作 normal，不冒称 race 覆盖。初次 FLOW index 校验失败与单行修正也保留，没有隐藏为全程首次通过。

三轮均固定原 10,000 在线、2,000 群、seed 1、500 总 SEND/s、同一配置与监督边界，均不采 profile。下面的过期数仅是**采样关联过期**；SENDACK 表示发送确认，不能等同于全部消息端到端收到。

| 轮次 | 产品来源 | SEND = SENDACK | 采样关联过期 | 保留的原始终态 | 最终窗口（含排空） |
| --- | --- | ---: | ---: | --- | ---: |
| A | b47，旧宽度 | 13,002 | 2 | product_failure / message_loss | 33.076 秒 |
| B | 4cb，宽度 16 | 60,503 | 0 | operator_stop / operator_requested | 122.978 秒 |
| A2 | b47，恢复旧宽度 | 18,002 | 1 | product_failure / message_loss | 38.530 秒 |

B 的监督器在 first grant 后 **120.109 秒**请求停止，终态关联采样 **664/664 收到、0 过期、0 pending/outstanding**；这不是对全部 60,503 次发送的端到端投递证明，也不是正式 `pass`。A/A2 的 safe-summary 没有 correlation 字段，不据此补零或臆造完整采样分母；A2 未作 DB/MessageID 关联。三轮均保留有效 final report，所属产品进程组及 64 端口均独立清零。A–B–A 的 **2/0/1** 结果支持恢复并发宽度是此本地负载的重要因素；共享 Mac 的时间变化及残余混杂未完全排除，更不能认定原云故障的唯一根因、Linux 全量性能或长稳验收通过。

[#1033](https://github.com/WuKongIM/WuKongIM/pull/1033) 精确 4cb 的四个自然 CI run 均 attempt 1 成功，原 18 项检查为 **16 成功/2 条件跳过**；之后两个自然 Issue PR Signal 成功，不是 CI 重跑。默认套件 **219 个 package PASS**；scripts **519.119 秒**，651 个顶层 PASS、6 个显式 SKIP、7 个 package PASS。五种迁移拓扑（含 1→3）有 **55 次 history 校验**，其中 1→3 整群重启后 node 1 的 rows4 场景使用 **1 次原合同允许的临时只读重试**；六个 owned-SID 清理均完整且为零。三个固定 seam 各完成三窗 60 秒/500 QPS，合计 **270,000** 次测量、零 errors/drop，另有 90,000 次 warmup；P99 最大值 TCP **178.023683 ms**、append **33.469629 ms**、mixed **20.693029 ms**，均在未变的 **400 ms** 绝对门禁内。这不是与旧候选配对的相对无退化结论，也不覆盖原分布两小时或 72 小时长稳。四发行版 TEST ONLY 签名仓库/真实 systemd 生命周期通过，不能代替生产渠道发布；大 correctness/native artifacts 仅保留 API 元数据，未下载验证其字节。

自然 CI 不直接执行新带 integration tag 的 presence/owner 边界夹具，因此另作了固定 Linux ARM64 正常功能补测。首次 legacy image build 在 **60 秒**超时，两个测试尚未执行，这份失败保留；后续改用不依赖 builder 的空 rootfs import 准备，沿用同一对已编译、精确 VCS 绑定的 ELF，两个隔离、断网、只读容器各执行一次并正常退出、无 OOM。实际有 **5 个顶层测试及 2 个子测试，共 7 个 PASS 事件**，包括并发/取消、proof 重查和真实 128 MiB 边界；task container/image/tag/PGID 均已清零。**Linux race 未执行**，不能由 Mac race 替代。该项是功能回归，不是 Linux 原分布性能验收。空 tar 二进制不入归档，只保留[外部完整清单身份和明确排除元数据](v3-release-readiness-20261023/cloud/width16-linux-import-once-v1/SOURCE_MANIFEST_METADATA.json)；原 13 成员 manifest 不被冒充为完整归档校验表。

#1033 于 **UTC 2026-10-23 03:31:17（北京时间 11:31:17）** 普通合并为 **`966ec67df1435ad07bbb82724cd940f550755179`**，父提交为 9e5/4cb，tree `1f2826553a264053e954229ac2581bcf48afe6ad` 与独审、实际 CI 和专项来源一致。保护/rulesets 及四个禁用 Review Agent 状态未变；确认 candidate tip 是 target 祖先且任务干净后，原工作树和本地分支已普通删除。其他 13 个 linked worktree、8 个 real-Git 来源 clone 及 clean primary `a804f4a5` 均保留。至此交付了本地有证据支持的有界修复；原云内层原因仍未知，生产 stable 仍关闭，发布结论仍为 **NO-GO**。

**新增本地 2,000 SEND/s（未通过）。** 用户授权后，同一 clean `4cb45b34` native 共享 Mac 候选仅将 rate 500→2,000 及两秒 credit 派生 burst 1,000→4,000，保留 10,000 在线/2,000 群和原门禁。唯一完整 periodic 切面为 +5.046341 秒/9,998 次首尝试，约 **1,981.24 SEND/s**，不是整段稳定吞吐。原最终窗口 **8.127061834 秒**（含排空）以 `product_failure/overall_first_attempt_rate`、exit 7 结束，**120 秒目标未完成**。SEND/ACK 均 12,001；首尝试失败 **196/12,001 = 1.633197%**，超过原 `<0.01%` 门禁，拒绝 205、重试 847、RECV 15,155。102 个关联采样全收到、0 过期；不是本轮 `message_loss`，也不证明全部发送端到端完成。harness failures/saturation/underdelivery 均 0。208 条 permission-busy 只是同轮聚合，未与 196/205 逐消息配对。hot/cold P99 均仅桶界 **(1 s, 2 s]**，不是精确值，也不能与 500 轮作严格配对结论。所拥有进程采样 RSS 之和最高约 4.07 GiB，不是唯一物理内存；ps CPU 不能证明全程余量。清理后所属进程组和 64 端口独立核零。此短窗不构成 Linux、两小时/72 小时或容量验收；**NO-GO 不变**。见[本轮封存摘要](v3-release-readiness-20261023/cloud/full-original-distribution-4cb4-rate2000-unprofiled-v1/safe-summary.json)。

## 2,000 SEND/s 后续结论与系统方案

本节补充第十八至二十批有限公开证据，保留之前失败轮次和来源限制。三节点、10,000 在线、2,000 群（含十万成员群）、seed、原 workload、超时、重试及门禁均固定；运行平台仍是共享 Mac，不能当成 Linux 原分布性能资格。

[#1034](https://github.com/WuKongIM/WuKongIM/pull/1034) 交付 person-directory 完成提案的有界并行与 Ready 两轮公平进展，合并为 `830ebbdc`。[#1035](https://github.com/WuKongIM/WuKongIM/pull/1035) 交付权限收集窗口、ACK gauge 原子发布、actor-ready 容量语义和封闭诊断，候选 `5f679978` 普通合并为 `3768ad3e`。两项均完成独立双轴复核、相关测试与 Linux CI，干净原任务工作树和分支已普通清理。#1035 原生包首轮 Rocky 在 artifact 下载阶段失败，验证与生命周期尚未执行；仅该 job 一次显式补跑后两项均通过，不改写成所有 CI 首次成功。合并与精确树绑定见[1034 收据](v3-release-readiness-20261023/cloud/pr1034-merge-cleanup-v1.json)、[1035 收据](v3-release-readiness-20261023/cloud/permission2000-pr1035-merge-cleanup-v1.json)。

精确 5f 的无 profile 原分布诊断活跃区间约 **1,999.15 SEND/s**，SEND/ACK 为 42,002、首次失败 0，最终仍有 **3 个关联样本超期**，终态 `product_failure/message_loss`。因此权限入口改善不等于 Online Delivery 已修复。旧 8100 带 profile 轮次的一个预先固定超期样本，经三个副本唯一一致的消息身份和同轮精确日志关联，定位在提交后的 presence timeout；仅解释这个样本的阶段，不能据此证明全部超期同根因。见[5f 摘要](v3-release-readiness-20261023/cloud/full-original-distribution-ack-gauge-rate2000-unprofiled-v1/safe-summary.json)与[旧样本有界关联](v3-release-readiness-20261023/cloud/full-original-distribution-coalescing-rate2000-three-profile-v1/first-expired-offline-addendum-v1/safe-exact-log-association.json)。

另一次真实三节点空载隔离中，32 个独立双 UID 调用产生 64 RPC、完成 2.880ms；相同 64 UID 预打包产生 2 RPC、完成 1.035ms。这证明特定场景存在小调用放大，**未复现原负载的五秒等待**。旧 8100 带 profile 轮次的六份抓栈都有恢复 quota 等待、各有 16 个 owner RPC 等待，没有采到 owner handler/Registry 锁住的现场；它们不能与 5f 指标拼成同请求因果，不同采样直方图的 client、服务排队和 handler 均值也不能相减为单请求分解。ACK 发布锁等待与容量误报有独立证据，不能统一改写成 RPC 队列故障。

仅将两层恢复并发 **16→64**、保留 8,192 keys、128 MiB UID 字节界限和全部原门禁的候选 `103292a9`，在约 **2,000.72 SEND/s** 时再次失败：+35.037 秒首次坏切面为 **20 次首次失败/70,017 次发送**，超过 `<0.01%` 原上限。最终 3,633 次失败大部分来自停止排空期 `session_closed`，不能全部算运行期 RPC 错误；732 条采样中 677 收到、0 expired、55 outstanding，也不能当作投递通过。相邻窗口 permission-busy/deadline 日志含重试且不能逐条配对首失败，根因仍未闭合。资源和 64 端口已独立核零，候选未推送或合并。见[原终态摘要](v3-release-readiness-20261023/cloud/full-original-distribution-recovery64-rate2000-unprofiled-v1/safe-summary.json)与[首失败窗口分析](v3-release-readiness-20261023/cloud/recovery64-first-failure-analysis-v1.json)。

响应用户“系统设计而非继续小补丁”的要求，已完成三个独立方案和[组合提案](2026-10-23-rpc-capacity-system-design.md)：复用现有 transport，统一 ownership、静态 method policy、公平分派与有限连接归属，补齐同请求阶段观测，再实现保留独立取消、freshness、fences 和 4096 结果上限的领域合批。当前是设计交付，不是实现或性能通过。先闭合运行期 RPC 放大与排队阶段，再冻结容量参数和混合负载验收；不继续将并发数字增大当成最终修复。现有 120 秒诊断不足以覆盖五分钟延迟、十五分钟资源持续门禁，也不替代后续 Linux、长稳、容量与恢复资格。**NO-GO 不变**。

## 包交付实现

新增 [packages #79](https://github.com/WuKongIM/packages/pull/79) 修复 RPM 验证器把路径文本当作签名状态的问题。相同已签 RPM 和公钥在 `good` 路径通过、在 `bad` 路径失败的独立对照已复现。修复分离精确路径标题与签名状态行，同时保留隔离候选证书、RSA/SHA256、完整签名者指纹及真实密码学校验。

六个新增测试方法先在旧实现产生 10 个失败子例；随后 35 项定向测试、完整本地 587 项（586 通过、1 项既有 Mac RPM 工具缺失跳过）及 4 项仓库合同通过。固定 Linux 镜像中五种路径的真实验签均通过，损坏包、未签名包和错误证书均拒绝，共 8/8。双轴独审均为零问题。这项路径缺陷不能归因为先前那次未保留工具输出的 native 失败。

#79 首轮 Pages 的 587 项默认测试及 4 项合同通过、SDK 的 34+10 项通过；原生任务的 7/10/6/31/29 项通过后，在安装固定 GoReleaser v2.18.0 时下载 `golang.org/x/tools@v0.49.0` 遇到 Go 官方代理 HTTP/2 peer `INTERNAL_ERROR`，任务 exit 1，后续 unsigned build 和完整签名安装跳过。原始失败日志保留。仅该明确基础设施失败的 job 获一次同源显式补跑，后续完整签名安装通过，独立原 stable 49 项未重跑且通过。#79 已于北京时间 **01:42:08** 普通 rebase 合并为 `721712e6d77c5f159fff94dcdb2bd1fb84ad464c`，合并树与复核树一致；main 首轮 587 项、4 项合同及 34+10 项 SDK 均通过。原任务分支 tip 未成为 rebase 合并提交的祖先，因此保留干净工作树和分支，不强删。

完整 native→SDK 演练已完成七文件实现：两代各 153 个对象、178 条路由、17 项配置，保留实际消费的 SDK 响应、完整 GET 的大小/哈希/EOF、配置前后读回、刷新完整覆盖、部分副作用和清理结果。刷新支持阿里云允许的聚合任务 ID，并保持原 1,000 URL、50 URL 提交、10 ID 查询及轮询上限。

独立 Spec 复核发现的公共证据丢失、网络异常被拒绝分类吞掉、清理失败覆盖原错误三项 P2 已逐项修复并关闭；v2 的 20/20 native→SDK、49 项 SDK、16 项输入和 4 项合同均通过。随后新增零 ResourceWarning 断言先在四种网络守卫场景失败，再显式关闭 probe socket；16 项回归通过且无警告，双轴增量复核零问题。该项已基于 #79 合并源创建 [packages #80](https://github.com/WuKongIM/packages/pull/80)，head 为 `4d868f264fb4758f7451c058dd329a06ad460449`，七文件中仅 CHANGELOG 处理了继承条目。

#80 首轮 Linux CI 的默认 **593** 项与 4 项合同、49 项 SDK 与 16 项输入、独立 stable 49 项均通过；新增 native→SDK 流程在首个场景前的 `pathlib.stat` 出现 `PermissionError`，清理也出现 `PermissionError`，新 20 项场景实际完成 **0** 项，完整签名安装未执行，该代不可合并。当时原回执没有失败路径/UID/mode，权限交接仍是假设；首轮失败和未清理事实保持原样。

云租约清零后，真实 Linux tmpfs 原生输入对照确认：UID 65532 生产、1001 消费时，0700 目录阻止读取和删除；生产/消费均为 1001 时，保持私有权限，完整验证 **414** 个绑定文件并成功清理。修复仅让新 prepare 显式使用宿主非 root UID/GID，并将两处 0777 交接目录收紧至 0700；坏身份在 Docker 前拒绝，旧步骤、driver、来源/验签和资源边界不变。18 项回归先 RED 后 GREEN、4 项合同、当前来源 native 29、prepare 2→SDK 20 均通过，中间夹具失败也保留。

复核 head `b8fc6d5e384630ef1d4c0f00281a836d115456e8` 的首次 Linux CI 全部通过：**595** 默认项、4 合同、49 SDK、18 输入，旧 native 7/10/6/31/29/49、新 prepare 2/SDK 20 以及完整签名安装均实际执行。`cleanup.json` 证明 SDK work/container 清零，四客户端的 91 容器、1 网络和 4 缓存也清零；全部仍是 TEST ONLY。双轴独审零问题、五个生产控制工作流全部活动状态穷尽为空后，#80 于 **02:59:40** 普通 rebase 合并为 `e0982275c158b5bd69520c42719ce6f833ee63bc`，树与复核树一致、保护不变。原 tip 不在 rebase 后 target 祖先链中，因此保留干净原工作树和分支，不强删。

该合并源的 main 首轮 Pages **595+4**、SDK **49+18** 也通过；Pages 配置/上传/部署按条件跳过，没有因此启用 stable 或发布正式包。

生产上传/激活入口的第一片已完成默认关闭的 manifest、固定 CLI 拒绝边界、原始 clean-Git 来源检查和真实局域 TLS 读回测试。9 项快速测试和 17 项真实 Git/TLS 集成通过，完整默认套件 589 通过、1 项既有工具跳过。该片仍未接入完整 SDK 正向编排，未提交或发布，不能报告为 stable 渠道已就绪。

## 继续处理的条件

1. 本地有界修复及上述回归/合并已完成，先前宽度边界的 Linux race 未验证；原云内层原因仍未知。原规模本地 2,000 SEND/s 在最新已合并源仍有投递超期，64 并发诊断又触发首尝试失败率门禁。接下来按系统提案先闭合阶段归因、实现并验证资源和调度契约，再重跑原规模原门禁。原两小时预演失败、72 小时长稳、容量及恢复阶段尚未完成。后续应对精确修复来源补齐正式 Linux 验收；新的付费云启动须另有明确授权，不复用已释放租约，也不把合并许可当作采购或发布许可。
2. #80 权限/清理修复及 Linux CI 已完成并合并，生产上传/激活入口仍仅部分实现，继续补齐 SDK 正向编排和真实签名候选的审计链。
3. 完成实际 OSS/CDN、TLS、精确运行身份与权限接入，以及生产候选来源、签名、审计和公网验收。stable 当前关闭；TEST ONLY、SDK 夹具、构建或合并成功不代表已发布。现有 ECS 授权不扩展为包分发资源采购或正式发布授权。
4. 满足前述条件后，冻结正式目标版本和 CHANGELOG，执行最终 tag 对应的发布门禁，并完成适用 signed native channel 的公开 APT/RPM 精确版本安装验证；任何一项未完成都不能报告正式发布完成。
