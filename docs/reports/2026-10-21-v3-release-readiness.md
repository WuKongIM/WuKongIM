# v3 正式版发布复核（2026-10-21）

证据截至 **2026-10-21 09:06Z（北京时间17:06）**；后续状态须绑定新的提交和收据。

结论：**NO-GO，当前仍不能发布正式 v3。** 已合并的迁移与MQTT修复候选已完成完整 Linux 性能/功能验收；产品 #1016、packages #67/#68/#69/#70/#71 已合并。500 SEND/s 的本地有界 helper 已完整结束并生成终态，Mac 独立 stable APT/RPM 密钥也已生成、备份恢复验证；公钥登记 #70 和 stable 安装引导准备 #71 经批准已合并；#71 合并后精确源码的410+4项检查通过，未触发部署。Linux scripts 的唯一255已定位到测试辅助进程wait，最窄修复正在实施，尚未取得修复后的完整门禁。既定云 RC 计划和 stable 公网签名包交付仍未完成，没有创建正式 tag、采购云资源或启动正式发布。

本地 helper 的实际测量为1,753.104秒，终态是 `operator_stop`，不能写成完整1,800秒测量或正式长稳通过。保留2次retry、102个超过400ms的热延迟样本、未解释的presence及停机警告。原100 SEND/s失败、Mac两次scripts超时、Linux首轮和JSON诊断失败均作为历史证据保留；后续修复与通过结果不覆盖原失败。

## 固定身份和已关闭事项

| 身份 / 证据 | 结论 |
| --- | --- |
| 当前公开版本 `v3.0.0-beta.24` → `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` | 09:04Z重新读取公开Release列表仍为最新v3预发布版本；GitHub latest端点排除预发布版，不能据此误报v2 |
| 初次审计 main `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` | 该历史快照尚不含迁移恢复修复 |
| 合并后 main `ed705c0fbffdea485bbb65608d6e340bb049d97e` | 包含已验收 head `7e9da7df1`；完整树相同，含迁移、MQTT 及回归修复 |
| 产品修复 `98c25091bb9587a303af982bb14bf4cedd9aa110` | 已完成修复及相关回归，见下方修复报告 |
| 已验收候选 `ef798352c40a94e57f768f76b2f4713712f827fe` | 与审计分支 `32d402f7625c36f5667a290ec95c045f5f35937c` 的 cmd/internal/pkg 和依赖文件相同 |
| 迁移回归 | 1→1、1→3、3→1、3→3、3→5 全通过，55 次完整历史检查；相关冷热读与故障切换通过。该轮为 macOS 功能证据 |
| Linux 完整性能矩阵 [39453556254](https://github.com/WuKongIM/WuKongIM/actions/runs/39453556254)，attempt 1 | 20/20 通过，43,800/43,800 最终完成、43,795 窗内完成，错误和丢弃均为零；最大 P99 75.80928 ms |

迁移修复只将两种明确的临时恢复原因保留为公共 `ErrNotReady`，没有放松 quorum、写入 fence 或永久错误。详情见[迁移修复](2026-10-20-v3-migration-recovery-repair.md)和[固定环境 Linux 验收](2026-10-20-v3-fixed-host-performance.md)。

## 性能证据如何影响放行

完整 Linux 矩阵通过，支持该次固定候选满足已经定义的性能底线；不跨提交替代后续候选的完整验收。固定二进制 CPU 实验 [39467558163](https://github.com/WuKongIM/WuKongIM/actions/runs/39467558163) 的 18 窗、36 端点数值也全部通过，280,800 请求全部完成、无错误或丢弃。

但同一二进制的先前 A/A 延迟差达 31.18%–36.09%；CPU 放置实验只有 list 满足两组等待下降筛选，sync 不满足，整体因果筛选失败。不能声称历史失败已经唯一归因，或证明 beta 到候选没有性能回退。这是**诊断结论的限制，不是新增发布门禁**。无需为了让因果筛选变绿而反复换共享 runner，或修改产品性能逻辑。见[CPU 证据与限制](2026-10-21-v3-cpu-contention.md)。

## 剩余条件与正在处理的交付

| 条件 | 依据级别 | 当前状态 |
| --- | --- | --- |
| 修复整合、候选自己的默认测试及相关 Linux E2E/SDK/Manager/原生包回归 | 候选交付与验证 | 已完成：[PR #1016](https://github.com/WuKongIM/WuKongIM/pull/1016) 于 2026-10-21 05:48:47Z 合并为 `ed705c0fb`；更新候选 `7e9da7df1` 的七项 CI 全部首次通过，合并树与之相同；原首次失败保留 |
| 本地 30m 预检 → 2h 预演 → 新 Lease 72h Soak → 同老化数据容量 → 30m 恢复 | 本次已制定的 RC 验收计划 | 原100 SEND/s预检提前失败并保留；500 SEND/s修复后的单次本地helper已完成operator-stop和清理，实际测量1,753.104秒且保留日志警告。新Linux scripts完整门禁已结束但仍有1项255；新增trace确认发生在测试辅助signaler的wait，生产join已成功。测试夹具修复及新完整门禁尚未完成，云阶段未执行，采购前仍需精确候选和累计预算授权。不能将该RC计划描述成RELEASING对所有发行的通用机械门禁 |
| stable 频道来源、签名、审计、对象存储/CDN、安装引导与四客户端 | 仓库强制正式发布交付条件 | 生产stable仍disabled/object_storage_required。来源预检#67、preview运输计划器#68、本地签名输入与保管约定#69均已合并；Mac独立生产密钥已完成，public-only登记#70已合并，PR的400+3测试及真实TEST ONLY原生演练已绿，合并后400+3检查再次通过、部署跳过。安装引导准备#71也已合并，通过真实preview/stable共存及重复安装；仍是未签发准备包。完整stable组装/审计、OSS/CDN发布与精确版本四客户端公网验收未完成 |
| 最终 tag 对应的性能 gate、精确 CHANGELOG、immutable 资产、三镜像源、文档及 stable 公网验收 | 仓库强制发布条件 | 仅在前置条件满足、目标版本确定后执行，候选诊断不能代替发布器对最终 tag 的门禁 |

初次审计 main 的三节点 nightly 已连续五次首次通过，最近为 [39475335838](https://github.com/WuKongIM/WuKongIM/actions/runs/39475335838)。[SDK 39367425765](https://github.com/WuKongIM/WuKongIM/actions/runs/39367425765) 已于 10 月 20 日验证 Android 1.0.5、iOS 1.1.1、Flutter 1.1.0。它们绑定旧 main，不能直接认证包含恢复修复的候选。本轮新增 CI 已独立核验实际 head/tree 和执行步骤。

完整验收分支包含超过 400 个证据文件，超过 Review Agent 的 50 个 changed files 上限。因此保留证据分支 `codex/v3-rc-qualification`，另从 main 建立 `codex/v3-release-recovery`，只整合修复、回归和所需 CI。两分支产品代码按候选修复同步，但每代验收仍须绑定实际 source/tree；不得通过删除证据或更改受保护评审限额满足上限。

PR #1016 第一代 head 为 `5f3ecdf93e762a1b1551b44977a44f2e63c97802`，产品与依赖树和 `ef798352c40a94e57f768f76b2f4713712f827fe` 相同。自动 PR CI 实际 checkout 的 synthetic merge 为 `0ab6ea4bad09c76dae8c71f8ca67c00d4e220b89`；两者 tree 均为 `f030a2fdd558266bc8f3d87f516b9f73273df93a`。这一区别已核实，不将 GitHub run 的 head 字段误当实际 checkout。

第一代 [39546957124](https://github.com/WuKongIM/WuKongIM/actions/runs/39546957124) 默认测试首次失败，后续五拓扑与自聊 E2E skipped。独立 SDK、Manager、原生包和三节点 PR 检查通过；十分钟 [39546982565](https://github.com/WuKongIM/WuKongIM/actions/runs/39546982565) 通过，但不能覆盖默认层红灯。十分钟实际测量 sent=ack=300,509，约 500.848 SEND/s，队列收敛；完整含预热流程 330,511 条不能冒充测量数。保留 27 次 retry、一个 hot latency warning；聚合 hot P99 为直方图上界 400 ms，最大 1482.669 ms，不声称零延迟异常或长稳通过。

### 首失败根因与更新候选

- MQTT 是产品缺陷：三层订阅/投影/SourceDrain 只同步检查直接 parent，原始 caller 取消经异步回调传播，在 CAS 冲突后可能错误返回 pending 并继续资格读取。确定性延迟传播用例 3/3 先红；修复仅通过私有作用域指针保留祖先取消和各层 Owner 检查，原 deadline 不变。19 分支争用矩阵三遍、完整包和相关 race 通过，严格要求取消后不再读取或封口。见[诊断](v3-release-readiness-20261021/mqtt-diagnosis/README.md)及[独立复核](v3-release-readiness-20261021/mqtt-diagnosis/independent-review.md)。
- 复制是测试夹具缺陷：故障注入仅处理单项批次，合法合批绕过拒绝，少的 repair 对应实际已经落盘的精确 proposal。150 次观测有六次失败，14 个缺 repair 全部已 durable；确定性真实 batcher 用例先红后绿。按 item 注入后原 66 项场景 150/150 通过，完整包/race 通过，另加精确键检查；未改变复制产品代码或放宽阈值。见[诊断](v3-release-readiness-20261021/replication-diagnosis/diagnosis.md)。

更新 head `7e9da7df100d193330b3d509cfdbe079ea32a6f8` 已推送。MQTT 产品变化使其不再与旧 C 等价，因此重新执行完整候选 CI、首次[十分钟负载](https://github.com/WuKongIM/WuKongIM/actions/runs/39548576246)和首次[固定 Linux 完整矩阵](https://github.com/WuKongIM/WuKongIM/actions/runs/39548584710)。这是修复后的新候选验收，不是对原失败重跑取绿。七条运行现已全部 success/attempt 1，无取消或重跑；实际 checkout 与二进制身份、执行步骤及清理收据均已核对。完整收据见[第二代冻结矩阵](v3-release-readiness-20261021/ci/final-v2/second-generation-summary.md)。

更新候选 [39548558671](https://github.com/WuKongIM/WuKongIM/actions/runs/39548558671) 的 Linux 默认测试与 E2E 已首次通过：219 个包通过、15 个无测试；五种迁移拓扑的 55 次历史校验全部通过，其中 1→3 重启后的两次临时重试均收敛。跨节点四项默认功能通过，显式 opt-in 的 2,000-QPS 用例 skipped，不计为覆盖。自聊单节点/三节点各 6 ACK、一次全群重启、四次发送会话抑制检查及重启后发送前历史读取通过；保留预期重复实时投递事实。构建和三个场景均 exit 0，Linux 清理 verified/complete、remaining=[]。

更新候选完整 conversation 矩阵为20/20、43,800最终完成、零错误/丢弃，最大P99为85.935013ms，原有分配预算均通过；十分钟实际测量600秒、300,504 SEND=ACK、500.84/s，retry、消息失败、latency warning及cleanup error均为零，队列收敛。完整含预热流程330,511条不能冒充测量数。SDK三平台双向消息、Manager Chromium及Ubuntu/Debian/Rocky/Alma四发行版真实systemd生命周期全部通过。Alma较慢步骤由日志确认是约14分钟仓库元数据下载，没有对应产品失败。

### 合并交付

用户明确批准两个精确 head 的评审与合并。产品 PR #1016 的 `@review-agent review` 评论已提交，但 Signal、Controller、Worker 三个工作流均被仓库手动停用，未生成本代 Review/Verdict。依据 `.github/workflows/README.md` 与 `docs/agents/review-agent.md` 保留的管理员普通合并路径，在独立审查、候选 CI 和明确授权齐备后正常 merge；没有启用自动化或使用 `--admin` 绕过。合并为 `ed705c0fbffdea485bbb65608d6e340bb049d97e`，与候选完整 tree `753ddfc8c7813bf0b7a7ec325cb4f81c5a36d4db` 相同。本地 main 已快进同步，验证祖先关系后移除干净的 recovery worktree 并以 `git branch -d` 清理原任务分支。合并前自动链路阻塞事实完整保留，见[合并解决记录](v3-release-readiness-20261021/review-merge/resolution-summary.md)。

packages PR #67 按线性历史要求正常 rebase 合并为 `782c14602a6b243d893e6abf4d1682b7370736c4`，完整 tree `ccfaa2893b024a61902ce1d3bf6dd35d9f38eb99` 与已验收 head `0ce7184b` 相同，分支保护未变。合并后 [main CI 39551632767](https://github.com/WuKongIM/packages/actions/runs/39551632767) attempt 1 通过，342 项测试、92.229 秒；Pages 配置、上传与部署均 skipped，stable 仍禁用。rebase 后原 tip 不是 main 祖先，故保留其干净本地分支与 checkout，没有 force-delete。见[包仓库合并收据](v3-release-readiness-20261021/packages/merge/README.md)。

## 本轮验证收据

本地默认测试首次因磁盘不足在构建阶段失败；保留日志。仅清理本任务可重建的重复临时产物后，降低编译并发再次运行，118 个包通过、14 个包无测试；另 102 个包因共享 Go 缓存文件在运行中缺失而构建失败，没有对应产品断言失败。仅对这些未完成包使用独立缓存重建，101 个包通过、1 个包无测试。合并覆盖最终为 219 个包通过、15 个包无测试，没有产品断言失败；这不是声称某次全量命令首次全绿。命令、每包所属运行和全部初次失败见随附收据。

自聊专门黑盒覆盖使用既有公开契约：同 UID 的另一设备应收到消息，发送会话被抑制，客户端按 MessageID 去重；不引入 exactly-once wire 承诺。首轮使用零边界误取最新页，第二轮业务完整通过但重复关闭客户端导致清理失败；两次均为测试夹具错误，原件保留。修正调用语义和一次性关闭后，最终单节点/三节点均通过：12 条 ACK、12 次完整历史检查、两次全节点重启。记录实时重复帧但没有重复持久化，未发现新的产品阻塞。

候选 CI 包装器复核发现，单独终止 Go driver 的进程组无法覆盖 E2E 节点独立的进程组。修复后按本次拥有的 session 清理，保留原失败状态，枚举失败不得伪报零残留。本机真实进程探针覆盖通过、exit 7、超时、输出超限、TERM 取消及枚举故障，外部 sentinel 均保留；Linux 分支的实际运行结果另以候选 CI 收据核准。不可捕获的 hard kill/runner 丢失不在正常清理保证内。见[独立复核](v3-release-readiness-20261021/review.md)。

## 正式长稳准备边界

已完成[云入口只读审计](v3-release-readiness-20261021/cloud/readiness-audit.md)：七个身份变量、wrapping 公钥、OIDC subject 及正式工作流配置存在；27 份历史清理 payload 结构相符，三个缺清理回执的早期请求均在报价失败、采购被跳过。审计快照未见活动 GitHub 请求，但没有调用云 API，因此今日云侧库存、角色权限和公私钥匹配仍未知。Environment 没有人审符合既定 unattended 设计，不新增 reviewer 要求。

产品 source 已合并；启动前仍须冻结最终 source/control/bundle、确认相应验收与本地 shakeout、获得精确付费范围，验证当天身份和库存，并要求自动清理调度启用成功后才 Quote/Acquire。最初静态检查时本机工作树文件系统仅余 9,214,992,384 bytes（0.926%），低于既有 5% 保留线，当时未运行 30 分钟 shakeout。见[历史静态前置检查](v3-release-readiness-20261021/cloud/local-shakeout-preconditions.json)，该记录不是 coordinator preflight 或 workload 结果。既定计划的 ¥1,500 上限、¥1,350 停止线是待批准的完整请求预算，不是已收到的授权或今日报价。没有启动采购、云运行或权限修改。

合并后 06:06Z [磁盘只读复核](v3-release-readiness-20261021/cloud/disk-followup/README.md)记录可用 8.743 GB（0.879%），距 5% 仍缺 40.990 GB，未含运行余量。本任务独立缓存及有校验 ZIP 副本的展开目录仅 3.297 GB；六个已知任务输出根目录全部也仅 3.833 GB。该范围内清理不能解除阻塞，未删除文件、扩大扫描或用 NAS 承载实时数据库；原证据保留。

随后文件系统可用空间上升，06:14:48Z 记录 338,215,362,560 bytes（34.003%），原磁盘阻塞解除；本任务未据此推断释放空间的操作者或原因。按原 5% 保留线与 400ms 热延迟阈值，在干净 ed705 上启动 100 SEND/s、2,500 在线、三 worker、12 logical Slots/256 hash slots 的原始 30 分钟 helper。协调器 06:23:03Z 开始，06:29:11Z 提前 exit 8：`harness_invalid / invalid_observation / grant / delivery / offered_load_under_delivery`；首条 worker 错误为 worker 2、grant sequence 315，未生成 `final.json`。这既不是完成 30 分钟预检，也不是产品长稳结论。

最后完整 cut 的 sent=31,005、ACK=31,000，其差包含在途请求；当时各消息失败、重试、丢失、重复、损坏和序号回退计数均为零。不能将最后一次快照扩大成全程正确性通过。现有资源证据没有 pprof 或失败时连续 CPU/IO 采样，不能归因共享主机竞争。12 个所属进程均已退出，64 个预留端口无 TCP listener，源树保持干净。[原失败、缺失终态和清理收据](v3-release-readiness-20261021/cloud/local-shakeout-ed705/README.md)与后续诊断分别保留。

后续[固定时钟诊断](v3-release-readiness-20261021/cloud/local-shakeout-grant-diagnosis/findings.md)使用真实 worker factory 和 coordinator grant allocator、即时 ACK，复现了相同 under-delivery 类型：原 100 SEND/s 对 2,500 在线用户生成的启动活动供给不足，worker 2 的 16,746 项初始义务无法在既有资格窗口内完成。延长会话 TTL 仍失败，提高供给可消除该机制；不需要假设产品背压或宿主延迟。修复将本地默认调整到 500 SEND/s、burst 1,000，保留用户数、全部启动义务、五分钟资格窗口及 400ms 阈值；三 worker 的固定时钟回归覆盖至 360 秒。此机制证据不替代真实收发或持续负载验收。

该修复的默认 Go 测试、vet、format、tidy 和 FLOW 检查通过；[独立复核](v3-release-readiness-20261021/cloud/local-shakeout-repair-review/implementation-review.md)未发现阻塞。完整 scripts integration 在 Mac 上首次及唯一一次隔离复验均触及原九分钟总时限，第二次结束于不同用例，不能标记通过或归因单个测试卡死；两次失败和所属 session 清理记录均保留。后续Linux独立job也在原600秒外层限额超时，原失败保留；没有放宽时限或第三次原样本机重跑。

### 本地500 SEND/s实机结果与脚本门禁的新发现

唯一一次新实测绑定干净 `0672c115950f65e6d92381aa5b3ea9f0a20fc790`、tree `ad05497c99502a4d076bacb7dee150d10e939d28`，三真实产品节点，2,500在线、500 SEND/s、burst1,000。helper于07:35:26Z启动，08:06:11Z结束；原 `--stop-after 1800` 从协调器进程启动计时，最终测量窗仅1,753.104288875秒。helper/wrapper均exit130，原生终态 `operator_stop/operator_requested`，满足本地有界helper的既定终止形式，但不是1800测量秒、正式 `pass` 或云长稳。见[完整本地收据](v3-release-readiness-20261021/cloud/local-shakeout-0672c1159-500/README.md)。

最终SEND=ACK=876,529，RECV=RECVACK=1,099,924；首次发送失败、最终发送失败、ACK失败、记录到的丢失/重复/损坏/序号回退及under-delivery均为零。保留retry2和transport_rejected2，不称零重试；关联抽样10,758/10,758完成、无pending/outstanding，不扩大为每个接收者的独立完整证明。hot SENDACK共810,725样本，102个超过400ms，最大663.132291ms；直方图仅支持P99在(100,200]ms、P99.9在(200,400]ms，原生latency warning为零不等于没有慢请求。

12个记录进程均退出，所属SID空、64个端口关闭，限定范围凭证扫描和panic/fatal扫描通过。另保留报告结束前93条presence route-not-ready警告及停机阶段2条同类警告，原因尚未建立，不能归为已证明无害的会话变化；停机后node1/node3共6条delivery_worker、plugin_hook、cluster停止超时。共享5秒Stop预算可使后续组件继承到期context，但现有日志不足以给出各组件耗时或完整因果。OS清理完成、benchmark cleanup_error_count=0均不能抹掉这些产品警告。见[有界日志分类](v3-release-readiness-20261021/cloud/local-shakeout-0672c1159-500/warning-classification.md)；不事后新增warning即失败的门槛。

产品[PR #1017](https://github.com/WuKongIM/WuKongIM/pull/1017)新增JSON观测的[Linux运行39565861218](https://github.com/WuKongIM/WuKongIM/actions/runs/39565861218)绑定head `eda77697db42ba14f14bfad58cdd3557342b9989`，实际checkout `abd628769baf7552a8cf8abb29a7ba63e52c3326`。仍在原600秒超时，日志未截断，已结束的根测试489通过、6跳过、10失败，另15未完成。scripts启动前约120.49秒混合了下载/构建等成本；顶层串行阶段216.34秒，占已观察包时长45.1%，其中真实90秒负载用例总耗时123.64秒。另一VCS夹具的空GOCACHE构建耗时113.94秒。不能只归因cold build，更不能把未完成用例计为通过。

JSON日志另揭示实际Bash5清理缺陷：成功分支的裸return在失败trap中继承状态，跳过Analysis关闭/本地清理，或提前终止子进程join。5处显式return0的窄修已通过Bash5先红后绿、Bash3兼容及9项现有聚焦回归；原退出17/143和关闭失败警告保留。测试夹具两处纯字面断言已由rg改为grep -Fq，消除runner缺少rg的依赖；定向进程测试通过。VCS夹具显式使用 `go version -json=false -m`，避免新增外层-json污染其文本读取，继承编译缓存并校验唯一链接build ID；Mac定向真实构建18.22秒通过，不能作为跨平台提速比例。原Linux observer exit255仍未定位，本地Bash5重复通过不算修复。以上变更均未放宽既有门禁，这些修复已整合推送为 `d7affe274935e78a644f901400db15ff1353a2c9`；新[候选CI 39569706125](https://github.com/WuKongIM/WuKongIM/actions/runs/39569706125)为attempt1，实际测试checkout为 `61cab03b700ff07439733171cbdcbd9072bfebc5`。08:39:12Z脚本门禁完成但失败：命令533.570秒，未触600秒外层超时；顶层600通过、6跳过、1失败、无未完成用例，进程清理完整。唯一失败为 `TestChatLifecycleShakeoutJoinSurvivesFirstSignal`（0.41秒，exit255），正在继续窄范围取证；旧observer用例本轮0.40秒通过，不证明原间歇255已经根治。既有Bash清理/rg/VCS修复对应用例均通过，VCS夹具39.38秒。默认完整go-unit 408.937秒通过，chat及三端SDK已通过；随后migration E2E与原生包四发行版均成功，见[终态补充](v3-release-readiness-20261021/ci/scripts-combined-d7affe-final/README.md)。这些通过不能替代失败的scripts门禁。

随后仅为两个失败夹具加入有界诊断的head `76a3ba23303e288345dbd37bc354b41d81b6f070`，在[首次Linux运行39572139549](https://github.com/WuKongIM/WuKongIM/actions/runs/39572139549)捕获决定性轨迹。实际checkout `974545635cc9be14a6b5f00413eb4bdb56ae0b7b` 与head完整tree相同；scripts命令545.255秒正常结束，600根测试通过、6跳过、1失败、无未完成项，输出未截断且所属进程清理完整。shakeout第3次重复失败：生产wait先被TERM打断为143，确认目标child仍活后再次wait成功，join返回0、PPROF_EXIT_STATUS=0并清空PID；随后测试自己的 `wait signaler` 得到Bash内部-1，退出为255。见[原始失败与轨迹](v3-release-readiness-20261021/ci/scripts-wait255-observed-failure/README.md)。

这直接支持本次失败在测试辅助进程等待阶段，不支持修改生产join或忽略255。修复方向为Go测试进程在ready握手后向精确shell发TERM，移除额外Bash signaler作业，并保留信号到达、当时child存活、最终回收及原退出状态断言；截至本报告仍在实施。旧observer255没有独立捕获，不能因本轮20次通过就声称同一根因。GNU Bash关于specific-PID wait255的[上游研究](v3-release-readiness-20261021/ci/scripts-wait255-upstream-research/research.md)只是相符线索，不证明实际Ubuntu包对应某个已确认上游缺陷。

凭证无关的[离线部署包准备](v3-release-readiness-20261021/cloud/bundle-ed705/README.md)已首次通过 [39555917411](https://github.com/WuKongIM/WuKongIM/actions/runs/39555917411)，source/control 均为 ed705。完整 ZIP/tar、110 个文件、manifest、9 个 ELF 与完整 DefaultIntent 已核验；bundle digest 为 `sha256:f274ffe1a3a74278d893fd16dd3a8df2e563fda375ac0295dfd8f6f5e982900a`。此项仅完成离线 payload 准备，不替代本地/云验收；artifact 有限期保留，未宣称人工构建可直接接入顶层付费编排。

## stable 公网复核和外部边界

初次公网审计绑定包仓库控制 `feb7f21503d35133cf23df7d7ab250c5d4129e71`。该次公网 `status.json` HTTP 200，目标为 `3.0.0-beta.24`，audit Release `402342684`；immutable audit、tag、receipt 摘要和 snapshot 身份一致。PR #67 已将 main 更新至 `782c14602`，没有发布替代这份公开快照。没有重新进行四客户端安装或整份约 294 MiB 快照的密码学校验。

preview 的 beta.21–24 四个 active 版本已达数量上限 4；若先发 RC，需原有两阶段退休及至少 30 分钟缓存窗口，不能直接追加第五版。当前总量低于容量警戒，公开签名子钥尚约 70 天有效；二者不是当前新增阻塞。

用户已明确使用阿里云账号，Chrome 只读核对确认现有文档 CDN 正常运行；未见包域名的 CDN 配置，四个 OSS Bucket 均为备份测试/验收用途，没有已指定的包源站。服务商已落实，本地签名输入与保管工具已推进；生产对象存储/CDN资源、发布接入、权限及安装入口仍待实施。

前期核对时，用户既有目录确实只有preview备份，README六个公开指纹与preview相同；“已另有stable密钥”的早期陈述被纠正，原[公开核对](v3-release-readiness-20261021/packages/public-signing-observation.json)和[备份分类](v3-release-readiness-20261021/packages/local-backup-classification.json)保留。该事实属于当时状态，已被随后新建的独立stable身份更新，没有复用或改名preview密钥。

用户明确选择“就放Mac或极空间里”，因此采用Mac本地保管及签名，不再等待离线设备或仪式；此选择不是正式发布授权。[PR #69](https://github.com/WuKongIM/packages/pull/69)已于08:19:40Z按用户授权正常rebase合并：已验收head `c21736edcf67bac4b5c1c495d1cd18490b1d154a` → `8b3ae9a08b943e51aa614779a58baab204c584ca`，完整tree相同。保管约定和既有signer的本地文件输入已同步，CI环境变量输入继续兼容。[该head验收](v3-release-readiness-20261021/packages/pr69-ci-c217/README.md)包含387项Python、3项workflow检查、真实metadata7/7、本地签名10/10及完整TEST ONLY包仓库构建/签名/验签/安装；首轮失败仍保留。合并后[39568424562](https://github.com/WuKongIM/packages/actions/runs/39568424562)首次成功，387+3项测试，Pages配置/上传/部署均跳过。

Mac生产密钥于08:09:17Z完成：APT主钥 `F68735072235EDC3BEBA83A55F19C486C2108B64`，RPM主钥 `4C76B7B37EBDAF05EB44976F96EC1B2D5976F45D`，两家族独立RSA3072 primary/current/next，有效期365/90/180天。加密完整备份恢复后，current/next各验证错误口令拒绝和正确签名的独立公钥验签，primary在恢复scratch完成认证证明，保留证书未改写；current-only validator通过。两项Mac Keychain口令记录已创建并核对。只归档公钥和公开收据，未访问或复制到NAS、未托管CI私钥、未签发正式版本包；见[生产准备收据](v3-release-readiness-20261021/packages/mac-key-preparation/README.md)和[密钥准备单](2026-10-21-v3-stable-key-preparation.md)。

public-only登记[PR #70](https://github.com/WuKongIM/packages/pull/70)的head `0c26496b624058db813aa1206a7910f0abae6b13` 在08:28Z仍为draft，[08:32:32Z复核](v3-release-readiness-20261021/packages/pr70-review-state.json)为ready待批准；这些是保留的历史状态。用户随后批准，08:34:33Z观察到合并成功，merge为 `51bfedbe88c78758fa05bfb87b13541b4bca4796`。精确Git对象证明其完整tree与评审head相同（`2c4f7d3a9b24a6f67c18b114b8a5c5f1f5f6fa5e`）。[合并后检查39570313491](https://github.com/WuKongIM/packages/actions/runs/39570313491)于08:36:26Z首次成功，400项Python测试102.131秒及3项workflow检查通过，Pages配置、上传、部署均跳过。rebase后的merge不包含原branch tip的祖先关系，因此保留原干净worktree/branch，未强删。其[400+3检查](https://github.com/WuKongIM/packages/actions/runs/39568724449)及[真实原生演练](https://github.com/WuKongIM/packages/actions/runs/39568724312)均attempt1成功，metadata7/7、本地签名10/10及完整TEST ONLY仓库签验安装通过；验证前删除测试秘密后独立验签，生产私钥没有进入CI。它登记公开身份并校验跨家族/preview的完整、16位和8位指纹唯一性，不开启stable、不改变preview控制或发布生产包。stable公网交付仍未就绪。

stable安装引导准备[PR #71](https://github.com/WuKongIM/packages/pull/71)经用户批准，于09:02:19Z正常rebase合并为 `0cd625cbee214927a81b0a1015d825a6f53fbf28`；与已验收head `fb62e03bb8ceaf300610becdc1f23ff41558c675` 的完整tree `8bffb645f4db3a313c2049b022bed90376df4db8` 相同。[PR验收](v3-release-readiness-20261021/packages/pr71-ci-fb62e03/README.md)包含410项测试、4项合同检查、6项真实Linux准备包可复现/双向共存/重复安装用例，以及既有metadata7项、本地签名10项与完整TEST ONLY签名仓库演练。新准备包自身尚未签发，这些证据不等于stable公网安装验收。

[合并后39573852736](https://github.com/WuKongIM/packages/actions/runs/39573852736)在精确 `0cd625c` 上首次通过410项测试（106.521秒、零跳过）和4项合同检查，09:04:19Z完成；Pages配置、上传和部署均跳过。原任务分支在rebase后非main祖先，保留干净worktree/branch、不强删，见[合并交付](v3-release-readiness-20261021/packages/pr71-merge/README.md)。stable仍disabled/object_storage_required；独立包名和配置路径已准备，但完整离线stable组装/审计及OSS/CDN切换、公网客户端仍需完成。

[渠道实施计划](2026-10-20-v3-stable-channel-plan.md)保留 preview 兼容及独立签名边界；PR #67（head `0ce7184bbf69472f607159f74a096e9160852843`）的两个新版 PR CI 均首次通过，且现已合并，见[证据](v3-release-readiness-20261021/packages/preflight-ci/README.md)；Pages配置、上传和部署均skipped。七个真实原生元数据场景及既有完整TEST ONLY演练不代表stable生产签名或公网发布。

已形成[OSS/CDN具体设计](v3-release-readiness-20261021/packages/storage-design/design.md)：专用私有源站、不可变快照、APT跨代hash闭包及stable RPM镜像列表建议；明确OSS只有单对象原子性，CDN配置和刷新不是整站事务。其中现有 preview 的离线运输计划器 [PR #68](https://github.com/WuKongIM/packages/pull/68) 已按用户单独授权于 06:34:42Z 正常 rebase 合并为 `45619df14b931ef0da3eae7a6e3d5402307daa60`，与已审查 head `09360d07d0bffa3258be252d7730902041026ba8` 的完整 tree 相同。它只接受有外部身份锚的 preview add_release 审计，复用 canonical tar、receipt 和元数据闭包校验，生成确定性 plan_only 清单、当前入口和跨代保留集合；不访问凭证或网络，也不执行上传、切换或删除。该计划器不提供stable签名或激活；云验证及原生客户端A/B切换验收仍未完成，后续本地TEST ONLY签名通过不补足这些边界。

#68 的两项 [PR CI](v3-release-readiness-20261021/packages/transport-plan-ci/summary.md) 均首次通过：373 项完整 Python 测试与 TEST ONLY 原生包演练，实际 synthetic merge 与 head 树相同。[合并后 main 收据](v3-release-readiness-20261021/packages/transport-plan-merge/README.md)确认 [39556280194](https://github.com/WuKongIM/packages/actions/runs/39556280194) attempt 1 的 373 项测试通过（99.318 秒），Pages 配置、上传、部署跳过。分支保护未修改；rebase 后原 tip 不在 main 祖先链，保留干净 worktree/分支，不强删。历史 draft/pending 收据保留为当时状态。

计划器 31 项聚焦用例、84 项相关回归及合同检查通过；先补失败用例并修复初始预哈希大小限制缺口，独立审查已关闭该发现。第一轮真实 beta.24 输入完整验明 308,536,832-byte archive 与历史公开控制/来源身份，但因三份普通命名 RPM metadata 不在首版范围而拒绝；原失败保留。随后先补两代普通 gzip 布局失败用例，将完整闭包校验后的普通 metadata 归为当前 no-store 入口，并在各代 snapshot 单独保留，不加入共享不可变别名；未放松 repomd、压缩/open 哈希或引用检查。

更新实现的真实 beta.24 同输入复验通过，44 个文件逐项 size/SHA 与 plan 相符，17 个可变入口、14 个不可变别名；plan SHA-256 为 `3c1dd765103ede4839f4d213d58bb8674365091d190008bc39ee84a289aed114`。这只是字节与结构一致性，不是签名、完整 provenance、云状态或公开客户端认证；固定 RPM baseurl 混代仍可能安全拒绝，未承诺原子或无缝切换。见[实现与先红后绿收据](v3-release-readiness-20261021/packages/object-storage-plan/generation-2-plain-rpm/handoff.md)、[独立续审](v3-release-readiness-20261021/packages/transport-plan-review/generation-2/review-summary.md)、[真实快照复验](v3-release-readiness-20261021/packages/transport-plan-real/generation-2/README.md)。

MQTT 继续默认关闭、开发预览。专项 self-chat、100 GiB/4h 迁移、生产 RPO/RTO、4,500 SEND/s 等未承诺范围不会被随意加入新的强制清单，也不凭本轮小规模通过扩大宣传范围。

原始状态审计、规则 digest 与后续验证收据见 [证据目录](v3-release-readiness-20261021/README.md)。
