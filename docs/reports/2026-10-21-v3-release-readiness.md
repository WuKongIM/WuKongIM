# v3 正式版发布复核（2026-10-21）

结论：**产品修复和包来源预检已验收并合入 main；当前仍不能发布正式 v3。** 原迁移修复候选已通过迁移重启读取和完整 Linux 性能验收；本轮完整默认测试又发现 MQTT 取消边界与复制测试夹具问题，已修复并冻结更新候选。更新候选 `7e9da7df1` 的七项 Linux CI 均首次通过，包含默认测试、迁移/消息 E2E、完整性能、SDK、Manager 和四发行版原生包。用户批准后，产品 #1016 合入 `ed705c0fb`、packages #67 合入 `782c14602`，两者源码树分别与已验收 head 相同。剩余条件是既定 RC 长稳验收、stable 签名原生包渠道，以及最终版本的发布交付。没有创建版本 tag、采购云资源或启动正式发布。

## 固定身份和已关闭事项

| 身份 / 证据 | 结论 |
| --- | --- |
| 当前公开版本 `v3.0.0-beta.24` → `4d44b39dda6fdaa7dafb637ca6cfb718e9530f77` | 截至本次审计仍为最新公开版本 |
| 初次审计 main `b469c6e5e2b4ff65611a8fe3389fb5034de3f152` | 该历史快照尚不含迁移恢复修复 |
| 合并后 main `ed705c0fbffdea485bbb65608d6e340bb049d97e` | 包含已验收 head `7e9da7df1`；完整树相同，含迁移、MQTT 及回归修复 |
| 产品修复 `98c25091bb9587a303af982bb14bf4cedd9aa110` | 已完成修复及相关回归，见下方修复报告 |
| 已验收候选 `ef798352c40a94e57f768f76b2f4713712f827fe` | 与审计分支 `32d402f7625c36f5667a290ec95c045f5f35937c` 的 cmd/internal/pkg 和依赖文件相同 |
| 迁移回归 | 1→1、1→3、3→1、3→3、3→5 全通过，55 次完整历史检查；相关冷热读与故障切换通过。该轮为 macOS 功能证据 |
| Linux 完整性能矩阵 [39453556254](https://github.com/WuKongIM/WuKongIM/actions/runs/39453556254)，attempt 1 | 20/20 通过，43,800/43,800 最终完成、43,795 窗内完成，错误和丢弃均为零；最大 P99 75.80928 ms |

迁移修复只将两种明确的临时恢复原因保留为公共 `ErrNotReady`，没有放松 quorum、写入 fence 或永久错误。详情见[迁移修复](2026-10-20-v3-migration-recovery-repair.md)和[固定环境 Linux 验收](2026-10-20-v3-fixed-host-performance.md)。

## 性能证据如何影响放行

完整 Linux 矩阵通过，支持当前产品候选满足已经定义的性能底线。固定二进制 CPU 实验 [39467558163](https://github.com/WuKongIM/WuKongIM/actions/runs/39467558163) 的 18 窗、36 端点数值也全部通过，280,800 请求全部完成、无错误或丢弃。

但同一二进制的先前 A/A 延迟差达 31.18%–36.09%；CPU 放置实验只有 list 满足两组等待下降筛选，sync 不满足，整体因果筛选失败。不能声称历史失败已经唯一归因，或证明 beta 到候选没有性能回退。这是**诊断结论的限制，不是新增发布门禁**。无需为了让因果筛选变绿而反复换共享 runner，或修改产品性能逻辑。见[CPU 证据与限制](2026-10-21-v3-cpu-contention.md)。

## 剩余条件与正在处理的交付

| 条件 | 依据级别 | 当前状态 |
| --- | --- | --- |
| 修复整合、候选自己的默认测试及相关 Linux E2E/SDK/Manager/原生包回归 | 候选交付与验证 | 已完成：[PR #1016](https://github.com/WuKongIM/WuKongIM/pull/1016) 于 2026-10-21 05:48:47Z 合并为 `ed705c0fb`；更新候选 `7e9da7df1` 的七项 CI 全部首次通过，合并树与之相同；原首次失败保留 |
| 2h 预演 → 新 Lease 72h Soak → 同老化数据容量 → 30m 恢复 | 本次已制定的 RC 验收计划 | 尚未执行；采购前需明确累计预算授权及精确候选。不能将其描述成 RELEASING 对所有发行的通用机械门禁 |
| stable 频道来源、签名、审计、对象存储/CDN、安装引导与四客户端 | 仓库强制正式发布交付条件 | 生产 stable 仍 disabled/object_storage_required；只读正式版来源/provenance/native metadata 的 [PR #67](https://github.com/WuKongIM/packages/pull/67) 已合并为 `782c14602`，52 项相关测试、独立审查及新版两项 CI 首次通过；快照检查342项测试、真实native元数据7场景和既有TEST ONLY签名仓库演练通过。合并后 main 的342项测试再次通过，部署跳过；公网生产链路仍未完成 |
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

产品 source 已合并；启动前仍须冻结最终 source/control/bundle、确认相应验收与本地 shakeout、获得精确付费范围，验证当天身份和库存，并要求自动清理调度启用成功后才 Quote/Acquire。静态检查时本机工作树文件系统仅余 9,214,992,384 bytes（0.926%），低于既有 5% 保留线；30 分钟 shakeout 未运行，不能用十分钟 Linux CI 代替。达到保留线至少还需约 40.52 GB 可用空间，另须留运行余量。见[静态前置检查](v3-release-readiness-20261021/cloud/local-shakeout-preconditions.json)，该记录不是 coordinator preflight 或 workload 结果。既定计划的 ¥1,500 上限、¥1,350 停止线是待批准的完整请求预算，不是已收到的授权或今日报价。没有启动采购、云运行或权限修改。

## stable 公网复核和外部边界

初次公网审计绑定包仓库控制 `feb7f21503d35133cf23df7d7ab250c5d4129e71`。该次公网 `status.json` HTTP 200，目标为 `3.0.0-beta.24`，audit Release `402342684`；immutable audit、tag、receipt 摘要和 snapshot 身份一致。PR #67 已将 main 更新至 `782c14602`，没有发布替代这份公开快照。没有重新进行四客户端安装或整份约 294 MiB 快照的密码学校验。

preview 的 beta.21–24 四个 active 版本已达数量上限 4；若先发 RC，需原有两阶段退休及至少 30 分钟缓存窗口，不能直接追加第五版。当前总量低于容量警戒，公开签名子钥尚约 70 天有效；二者不是当前新增阻塞。

用户已明确使用阿里云账号，Chrome 只读核对确认现有文档 CDN 正常运行；未见包域名的 CDN 配置，四个 OSS Bucket 均为备份测试/验收用途，没有已指定的包源站。服务商已落实，生产资源、权限、签名协议与安装入口仍待实施。

签名状态经用户最新澄清已更新：**目前只有 preview 备份，没有独立 stable 密钥。** 仓库两份公开证书与 preview manifest 相符，文档命名 preview 保管人为 `tangtaoit`；用户后来指定的本地目录 README 六个公开指纹也全部匹配这套 preview。早先“另有 stable 密钥”的陈述已被最新信息取代。私钥和吊销文件未读取，任何备份内容均未归档。见[公开证书核对](v3-release-readiness-20261021/packages/public-signing-observation.json)、[本地备份分类](v3-release-readiness-20261021/packages/local-backup-classification.json)及[stable 密钥准备单](2026-10-21-v3-stable-key-preparation.md)。正式签名需要在合同要求的离线/硬件环境新建独立身份，不能改名或复用 preview 证书。

用户随后提供极空间 NAS 作为可用存储位置，并进一步确认只有当前联网电脑与极空间。已将 NAS 记录为**加密备份候选位置**；离线或硬件签名环境尚未具备，实际保管负责人仍待明确。没有访问 NAS、写入备份或生成生产密钥；这项信息不解除 stable 签名阻塞，也不再按“已有 stable 公钥待提供”等待。

[渠道实施计划](2026-10-20-v3-stable-channel-plan.md)保留 preview 兼容及独立签名边界；PR #67（head `0ce7184bbf69472f607159f74a096e9160852843`）的两个新版 PR CI 均首次通过，且现已合并，见[证据](v3-release-readiness-20261021/packages/preflight-ci/README.md)；Pages配置、上传和部署均skipped。七个真实原生元数据场景及既有完整TEST ONLY演练不代表stable生产签名或公网发布。

已形成[OSS/CDN具体设计](v3-release-readiness-20261021/packages/storage-design/design.md)：专用私有源站、不可变快照、APT跨代hash闭包及stable RPM镜像列表建议；明确OSS只有单对象原子性，CDN配置和刷新不是整站事务。下一可独立实现项是无凭证运输计划器和真实客户端A/B切换演练，尚未采购或配置任何资源。

MQTT 继续默认关闭、开发预览。专项 self-chat、100 GiB/4h 迁移、生产 RPO/RTO、4,500 SEND/s 等未承诺范围不会被随意加入新的强制清单，也不凭本轮小规模通过扩大宣传范围。

原始状态审计、规则 digest 与后续验证收据见 [证据目录](v3-release-readiness-20261021/README.md)。
