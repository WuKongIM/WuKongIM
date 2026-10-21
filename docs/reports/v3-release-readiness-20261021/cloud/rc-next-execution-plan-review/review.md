# #1019 后一次性新租约方案独立复核

截至 2026-10-21 11:23:19 UTC。只读审查源码 `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`（#1019 已合并，tree `0acb67250313fc81a463941295082c828a46124c`）、原方案及公开旧失败收据。原方案“#1019 尚未合并”已过时，其余历史证据不改写。未运行测试、云调用、Quote、Acquire、dispatch，也未修改仓库或权限。

**结论：现有工具没有能完整继承原预算并继续 2h→72h 的现成新租约路径。可以接受一个很窄的新增路径，但原稿还不是可执行采购合同。推荐用同一 workflow 的相邻 run_number 取代完整历史枚举，保留精确身份/旧 zero/费用认证；不要增加跨请求账本、锁、tag 或通用重试编排。**

## 更小的 v2 一次性边界

只读 GitHub GET 已确认旧 top `39581355444` 的 numeric workflow_id 为 `329938003`，run_number=32、run_attempt=1，main/fc182、workflow_dispatch、completed/failure。该 workflow 最新仍是 32；小型原始字段收据见 `workflow-run-sequence-readonly.json`。

GitHub 官方将 run_number 定义为同一工作流每次新运行递增的唯一编号，重跑不改变它；run_attempt 随重跑增加。因此同一 numeric workflow_id 下，要求 `current.run_number == old.run_number + 1`、两端 attempt 都是 1，可把新运行限制为唯一的 33。删除中间运行不应产生另一个同编号的新运行，是根据这两个官方性质得出的推论；不冒称另有已验证的删除重置 API 保证。[GitHub 变量定义](https://docs.github.com/en/actions/reference/workflows-and-actions/variables)、[精确 Run GET 字段](https://docs.github.com/en/rest/actions/workflow-runs#get-a-workflow-run)。

最小条件：

1. 用 GitHub API 读取旧、当前两个精确 numeric run ID，不相信输入自报编号。当前 ID 绑定受保护 workflow 的 `GITHUB_RUN_ID`，attempt/number 与该运行环境一致；repo numeric ID/full name、head repo、numeric workflow_id、允许的 workflow path、main 和 workflow_dispatch 全匹配；当前 control SHA 为实际受审 protected main，旧 control/source 身份按原产物交叉核对。
2. predecessor 必须是真正的 rehearsal-1、原 committed=0、无 continuation 收据；当前只允许 rehearsal-2。禁止把新 33 当 predecessor 再链到 34；formal 不是该输入的可选前序。
3. 非相邻、任一 rerun、缺字段、API 不可达、错误 workflow 或缺历史证据均在 bundle/key/Quote/Acquire 前失败。不论 33 已购买、提前失败、被取消或被删除，34 都不能重用 32；33 的 rerun 也拒绝。这是保守的一次机会，提前失败亦不自动释放机会。
4. 保留现有全局付费并发锁、原 request stop marker、全部旧 selector 的 zero 认证及未知状态拒绝；相邻序号不代替这几项。无需为一次性判断再完整枚举所有请求/运行，也无需新增持久化锁。

源码现有并发锁及既存租约检查见 [rehearsal workflow L29](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/.github/workflows/chat-lifecycle-rehearsal.yml#L29) 与 [L79](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/.github/workflows/chat-lifecycle-rehearsal.yml#L79)。这只是 v2 设计建议，当前代码尚无此 gate。

## 实施前必须落实的边界

| 项目 | 证据与最窄处理 |
|---|---|
| 身份认证不可仅复用函数名 | 现有 `authenticate-handoff-producer.sh:16–26` 核对 repo/main/path/event/status，但未核 run_attempt/number/numeric workflow_id/子 Acquire/Release；`authenticate-cleanup-artifact.sh:38–44` 仅要求 scopes 非空。新窄 helper 可复用下载/生产者验证，但必须增加精确旧 top→Acquire/Release→artifact→Plan/Quote/Receipt/selector 的交叉绑定、12 scope 集合（无缺失/重复）、account/source/pubkey 与原字节摘要。不要把本地自填 JSON 或一次 GitHub green 当认证。旧 handoff 可能已被正常删除，优先使用既有 90 天 cleanup/complete，而非依赖仅 8 天 handoff。 |
| 可选输入默认分支存在重置预算入口 | 当前 `stage-orchestrate.sh:330` 对 rehearsal 置 committed=0，`:536` 固定 attempt1；Issue preflight 只核唯一 open/unlocked，旧 zero 会让 active guard 放行。相邻编号仅约束新 continuation 模式。不得声称所有同 request 重放已封住，却允许省略前序后将已有花费 request 当新请求。root 已接受最窄 guard：利用现有有界 Artifact inventory，发现该 request 的认证 cleanup/complete 后，在采购前拒绝无 predecessor 的重启；不建立新的跨请求账本。非相邻编号、attempt>1、第二次 continuation 和省略 input 的同 request 重启均必须拒绝。此 guard 是本 v2 方案范围内的必要修正，尚未实施。 |
| 费用只继承可信的上界 | ¥74.4776 是原已接纳 Quote 全额 reserve，不是账单。输入认证后重算，禁止自由传入金额。保留 ¥1350/¥1500 总界限、原清理余额、下一 Quote 与 5s guard 的 nonzero prior。旧持有时窗/一个 20Mbps EIP/主机磁盘集合在受验模型中被 Quote 覆盖；计费起点、价格有效期、额外资源或带宽约束不闭合即采购前拒绝，不能用 ¥28.9398 更小估算或账单未到=0 放行。无需为本例改 generic cloudlease/provider。 |
| unknown 不可变成已修复的 preflight | 原失败无合格 2h、没有被接受的 run-start，原因仍 unknown；无 run-start 文件不能证明远端绝未开始执行。新启动决定可明确接受这个未知，但不得改写成已证实 readiness/preflight。任何已知 runtime/correctness terminal、已测阶段/正式证据或 stop 均拒绝。建议字段表述为“accepted run-start 前未分类”，保留原原始事实，不把控制脚本修复等同产品错误已消失。 |
| 下游必须保留相同 prior | 现有 formal 的 `prior + rehearsal_cost` 已能承载非零 prior，无需新成本链 schema。但 finalizer/正式消费者必须复制并重新绑定这个窄 sidecar、旧失败/zero 与 RunPlan prior，90 天证据完整；不能仅把数字传入 CLI 后丢弃来历。零历史的正常路径保持兼容。 |

对应精确源码：[旧 producer helper](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/scripts/chat-lifecycle/authenticate-handoff-producer.sh#L16)、[旧 zero helper](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/scripts/chat-lifecycle/authenticate-cleanup-artifact.sh#L38)、[原 rehearsal prior](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/scripts/chat-lifecycle/stage-orchestrate.sh#L330)、[Issue preflight](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/scripts/chat-lifecycle/comment-request-issue.sh#L69)、[formal 费用校验](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/.github/workflows/chat-lifecycle-formal.yml#L122)。

## 可复用的最短代码路线

- 一个 optional predecessor 输入及一个只读认证 helper，在付费/密钥/新 bundle 之前生成有限的本地可信证明；只接受该模式的固定相邻序号与前序类型。保留原全局锁与 stop 机制。
- 复用现有 TrustedContext/materialize：给 rehearsal 唯一的受认证 attempt2/非零 committed 分支；默认仍拒绝私填 attempt2/prior。不改为循环，不为 formal 制造 rehearsal_pass。[原验证 L321](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/internal/usecase/chatlifecyclerun/run.go#L321)。
- orchestrator 初始化 carry/单次 attempt，沿原 deployment/readiness/cleanup 顺序运行。finalizer 与 formal 只补 sidecar 的携带/再认证，继续原 transition schema 和加法；不改产品 runtime、provider、模板预算、阈值或权限。
- 更新 workflow README/适用 FLOW/ADR0047/CHANGELOG，说明这是已释放后的一次明确新租约例外；旧 ADR 仅允许复用仍存活的 Lease，不能默默当成本功能已获合同支持。[ADR0047](https://github.com/WuKongIM/WuKongIM/blob/ce3c4d9110f17bb71dc99a89dc3c09758fabf980/docs/adr/0047-retain-one-lease-across-deployment-repair.md)。

不建议用 direct-lab 代替：它是当前 Lease 的修复诊断控制，不产生这条受保护的 2h→72h 验收证据链；也不能用正式阶段入口绕过预演 pass。

## 必须先红的聚焦场景

1. same workflow 32→33 / 两端 attempt1 是唯一可入路径；32→34、32 rerun、33 rerun、跨 numeric workflow/path/repo、API/env ID 不符、以续跑 33 为下一前序，全部采购前拒绝。33 的失败/取消/删除不让 34 通过。无需真实删除 GitHub run，用固定 API 替身覆盖。
2. 有旧 request cleanup/complete 却省略 predecessor，不能 reset prior=0；正常真正首次启动兼容。错误或缺失关联证据、仅一个 zero scope、重复/缺 scopes、Release 不是同 selector、child producer/head/attempt 不符、artifact bytes 不符，全部失败且零采购调用。
3. 已知 runtime/correctness/已测/正式/stop 拒绝；unknown 原样保留，不能伪写 preflight，也不能将失败缺产物当一次性机会未使用。
4. old full Quote reserve 从认证输入算出 74477600；金额/价格/时窗/资源/EIP/来源不闭合拒绝；下次 Quote 用剩余 stop 1275522400，累计不得重置。provider invoice 仍 unknown。
5. 成功预演→formal 重算 prior + 当前成本并绑定 sidecar；sidecar 缺失/篡改或非零 prior 无证明拒绝；handoff 清理后仍可从既有 90 天收据验证。Issue/上传失败不能跳过 cleanup 或再次采购。

只读审查不能代替实现与测试；目前没有已可执行的新付费启动 JSON。本次未新增硬件、密钥或负责人要求。所有新的实际采购仍由原授权边界及根任务的明确启动动作控制。
