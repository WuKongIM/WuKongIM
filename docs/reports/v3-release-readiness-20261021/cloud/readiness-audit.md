# 正式 RC 云长稳：启动准备只读审计

2026-10-21，GitHub 快照约 04:56–05:02 UTC。**未发现需要修复的正式链静态配置缺失；当前仍不能据此宣布可付费启动。** 仍需合并后冻结 source/control/bundle、绑定最终候选验收及 shakeout、确认明确付费范围，并在获准后取得当天的 live OIDC/密钥匹配、库存及 Quote 证据。本轮未调度 Actions、访问云 API、报价、采购、调整权限或创建 Issue；只读取公开绑定变量与 Secret 名称，没有读取 Secret 值。

本地检查以 recovery 工作树检查时 HEAD `84cf66c0fb24a561dea8fd8dc4e95a5aaf4453fe` 为背景，存在未提交整合工作；GitHub main 快照仍为 `b469c6e5e2b4ff65611a8fe3389fb5034de3f152`。产品 `ef798352c40a94e57f768f76b2f4713712f827fe` 的迁移/五拓扑/20 项 Linux 性能证据保留，但不能替代合并后新 source 的相应验收。正式控制文件相对 b469 未变化。为避免主任务继续编辑 README 造成行号漂移，本报告的控制规则引用固定到本目录 `source-b469/`；[manifest](source-b469-manifest.json) 记录 SHA256。更详细本地资源/阶段清单见 [local-formal-checklist.md](local-formal-checklist.md)，其中工作树行号可能随着整合移动。

## 已核对的 GitHub 绑定

以下只读取精确 allowlist 中的七个身份变量及一个 wrapping 公钥变量；四个 Environment 均没有 Variable 覆盖。

| Repo Variable | 当前非秘密值 |
| --- | --- |
| `ALIBABA_CLOUD_LEASE_REGION` | `cn-hangzhou` |
| `ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH` | `sha256:00757c95a924e7e2e5cb4dfc23c8e5388166cfed3534dde2380dccea36bb21f2` |
| `ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN` | `acs:ram::50175678:oidc-provider/wukongim-cloud-lease-github` |
| `ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE` | `wukongim-cloud-lease` |
| `ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN` | `acs:ram::50175678:role/cloudleaseprovisioner` |
| `ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN` | `acs:ram::50175678:role/cloudleaseobserver` |
| `ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN` | `acs:ram::50175678:role/cloudleasereleaser` |

`WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY` 为 Ed25519，指纹 `SHA256:6b7vRnc8rpraVMG2sbpSf5I0+jTFCr6gnD/gBe28E1g`；完整公开值在同名 `variable-*.json`。`cloud-deployment` 下 `WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY` Secret 名称存在；三种 Lease Environment 不含 Secret。仓库 bootstrap AccessKey 两个 Secret 名称均存在。名称存在不证明值有效、密钥匹配或任何权限可用。

OIDC subject 模板实测：`use_default=false`、`use_immutable_subject=false`、`include_claim_keys=["repo","context","job_workflow_ref"]`，符合 [工作流断言](source-b469/.github/workflows/cloud-lease-oidc-setup.yml:64)。本地期待的精确角色 trust subject（不是从 RAM 读取的实际策略）形式为：

```text
repo:WuKongIM/WuKongIM:environment:<对应 environment>:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/<允许 workflow>@refs/heads/main
```

各角色只对应自己的 provision/observe/release Environment，并分别允许 setup、自身工具及 `cloud-lease-analyze.yml` 三个 Workflow；完整九条精确字符串在 [readiness-audit-summary.json](readiness-audit-summary.json) 的 `expected_trust_subjects_only_not_observed_RAM_trust`。issuer 期待 `https://token.actions.githubusercontent.com`，audience 如表；[源代码](source-b469/internal/infra/cloudlease/alibaba/identity_bootstrap.go:341) 要求精确 subject，无通配符。本轮未读取云侧 trust、policy 或 session。

## Environment、main 与执行权限

四个正式 Environment `cloud-lease-provision`、`cloud-lease-observe`、`cloud-lease-release`、`cloud-deployment` 均存在，`protection_rules=[]`、`deployment_branch_policy=null`，没有人工审核、等待计时或 Environment 级分支限制。**无人工审核符合 unattended 设计，不能作为新增阻塞要求设置 reviewer。** [README](source-b469/.github/workflows/README.md:863) 和 [identity runbook](source-b469/docs/superpowers/runbooks/cloud-lease-identity.md:24) 明确 setup 移除 reviewer、保留已有其他设置。当前授权限制依赖 main 上的 Workflow guard、调用授权及精确 OIDC subject，不能声称 Environment 自身已限制到 main。

main 分支保护 API：strict=true，禁止 force push/delete；required check contexts/checks 为空、required approvals=0、enforce_admins=false，rulesets 为空。这描述当前保护强度，不等同于“所有候选 CI 都被 GitHub 强制执行”。本次未改权限。Actions enabled=true、allowed_actions=all；默认 token 只读，各正式 Workflow 自行申请必要 actions/issues 或 id-token 权限。API 允许配置存在，也不证明下一次 enable/dispatch 能成功。

历史 [setup run 32570381582](https://github.com/WuKongIM/WuKongIM/actions/runs/32570381582) 于 2026-08-22 成功，三个角色 live verify job 与 wrapping identity job 均 success，见 `jobs-32570381582.json`。它只证明当时执行完成，不能作为今天 live verification。若需现时角色验证，原 setup Workflow 在 `operation=apply`、`force_reconcile=false` 且七变量齐全时选择已有绑定再验证；变量缺失会进入 IAM reconcile 分支，故不能把该入口笼统称为无条件只读。也不要自动运行会配置 GitHub/在漂移后 force 修复的本地 `setup-identity.sh`。[分支逻辑](source-b469/.github/workflows/cloud-lease-oidc-setup.yml:103)

## 正式链及清理安全调度

| Workflow | 当前状态 | 本轮含义 |
| --- | --- | --- |
| `chat-lifecycle-rehearsal.yml` | active | 唯一顶层付费入口可见，未调用 |
| `chat-lifecycle-rehearsal-finalize.yml` | disabled_manually | 预演开始前由顶层 enable；当前停用本身不等于故障 |
| `chat-lifecycle-formal.yml` | disabled_manually | released passing rehearsal transition 后 enable，内部续接入口 |
| `chat-lifecycle-formal-finalize.yml` | disabled_manually | formal Acquire 前 enable |
| `cloud-lease-release.yml` | disabled_manually | 每次 paid Acquire 前独立无云凭据 job enable；空闲时可自动停用 |
| `chat-lifecycle-stop.yml` | active | request-scoped stop marker + finalization，未调用 |
| bundle / activate / provision / observe / analyze / OIDC setup | active | 只核对存在与状态，未调用 |

安全启用约定见 [README](source-b469/.github/workflows/README.md:882)、[Acquire 的 arm job](source-b469/.github/workflows/cloud-lease-provision.yml:45)、[release disarm 条件](source-b469/.github/workflows/cloud-lease-release.yml:130)。启动时必须取得这些 arm 步骤的成功证据；本审计没有通过状态修改来试验权限，也不建议空闲时擅自打开四个定时器。

最后的预演 finalizer [33479692602](https://github.com/WuKongIM/WuKongIM/actions/runs/33479692602) 在 9/1 失败，原因是当时完整 Artifact 盘点超过 **50 页**，而不是该 job 中测得产品失败。只保留四行错误摘录。当前 [discovery](source-b469/scripts/chat-lifecycle/discover-active-handoffs.sh:20) 上界 **200 页**；本次完整元数据扫描只需 5 页。最新 formal continuation 33502076809、formal finalizer 33502821176 的发现/idle-disable 成功、实际 acquire/finalize skipped；不是正式长稳通过。最新通用 Release 35720371242（9/22）的 release 和 idle-disable jobs 成功，其 7 天 release artifact 已不在当前盘点，不能升级为今日库存证明。

## 旧请求检查：未发现 GitHub 活动请求，当前云库存仍未知

- 五种非终态 `queued/in_progress/waiting/requested/pending` 全仓库分页查询均为 0。完整 Artifact 元数据快照 445 件、5 页，未见未过期的正式或预演 handoff；见 `nonterminal-runs.json`、`artifact-inventory.json`。这仅是 GitHub 状态，没有覆盖已删除/过期 handoff 或外部创建资源。
- 最新 27 个不同预演 request 的 retained cleanup archive 已逐个下载小 ZIP并校验其 API SHA256，只读取 allowlist JSON。均有 `cleanup.zero_inventory=true`、typed `cloud_lease.release/v1`、匹配 request/account/region/repository、非空 observed time 与 12 类资源 scopes。7 件还含独立 release-selector 且相等；其余 20 件未重下载 complete artifact做 receipt/handoff 绑定，也未重新认证所有 producer。因此结论是 **27 份历史清理 payload 结构相符**，不是对全部历史 request 再签发零库存认证。原 ZIP、其他成员及任何 encrypted handoff/身份均未留存。
- 全部 31 条顶层预演 run 已终态，其中 3 条早期失败没有 matching retained cleanup：31261098792、31260364699、31259617695。追溯它们各自的 Provision run 31261198276、31260469032、31259766615 后确认都在 `Quote exact capacity and budget` 失败，`Acquire explicitly authorized paid Lease` skipped；原因分别是 EIP quota incomplete、GetProductQuota、unsupported plan。由此解释缺少 cleanup 的前采购状态，相关 jobs 与少量错误行已保留，不据此声称今日整个账户清零。
- 标题搜索返回 38 个 chat-lifecycle Issue，全部 closed；closed Issue 不能替代 selector-bound provider proof。direct repair cleanup 只记录为元数据，未将它作为正式 RC 证据。

## 可 review 的最终启动清单

| 顺序 | 必须落实的具体项 | 当前状态 / 最小后续行为 |
| --- | --- | --- |
| 1 | 当前小 PR 合并 protected main，确认最终候选相关回归；冻结完整 product source、独立 control SHA 与 AGENTS/FLOW digests | merge 尚未完成；ef798 历史绿灯保留，不直接代替 merged source |
| 2 | 最终候选的本地 30 分钟 shakeout，退出后 PID/端口清理证据 | 本轮未执行；核对已有有效 receipt，缺失则补齐，不用常规10分钟 smoke冒充 |
| 3 | 精确付费范围、source、请求总预算 | 主任务按当前直接人类授权判断并复用；本任务只读授权不构成付费许可 |
| 4 | 当天三角色 OIDC exchange、exact sole policy/trust/1h session，wrapping公私钥匹配 | 静态配置齐全、8/22历史成功；今日 runtime 未验证。任何权限修复需独立明确授权，不自动 force |
| 5 | 完整旧 handoff/producer 检查及必要云侧 repository inventory/exact selector 核对 | GitHub 空闲快照已完成；历史27份结构证据，今日 provider 未调用，库存未知 |
| 6 | 唯一 request ID，operator=`tangtaoit`，request 专属诊断 Ed25519 公钥；私钥仓库外受限保留 | 本轮没有生成；启动前冻结，不复用旧 request/stop marker |
| 7 | 一次内容寻址 Linux bundle：产品/负载 source，控制工具 control，记录 bundle/配置/二进制摘要 | merge后才构建和封存；预演与正式复用同一 source/bundle |
| 8 | 按正式入口执行 arm→Quote→exact admitted Acquire | 获得授权才调用；先证明 finalizer/generic release arm成功；Quote当前库存、配额、机型/镜像/可用区与预算合格后才采购，不另购第二次 placement |
| 9 | 2h预演、终端证据、exact release到zero；新Lease72h→容量≤8h→30m恢复；统一终端归档和清理 | 固定合同不变，下面预算适用整个request；failure不重试为绿，cleanup-pending保留交backstop |

正式资源仍为杭州同区同型非突发 PostPaid Ubuntu24.04 x86_64 四台 4 vCPU/8 GiB：3 service（40 GiB系统盘+500 GiB数据盘），1 load（40+200 GiB、唯一20 Mbps EIP）；两阶段共享 **¥1,500硬上限，估算累计达到¥1,350停止**。预演12h Lease、2h测量；释放清零后正式96h新Lease、72h连续测量，容量及恢复保持同一 formal 进程、worker fence和老化数据。具体阈值及失败/证据规则维持旧正式计划及源码，[本地核对清单](local-formal-checklist.md)给出逐项行引用。

供主任务在 source 已冻结后审阅的付费范围文字（不是本轮已经收到的授权，也不调用）：

> 授权对 `<合并且验收通过的完整 SHA>` 开始聊天生命周期全流程压测：按仓库正式 GitHub OIDC 方案在阿里云购买临时资源，执行预演、72 小时长稳、容量及恢复，创建跟踪 Issue 并完成清理。整个请求累计预算上限人民币 1,500 元，估算累计达到 1,350 元停止。

正式 OIDC 链与 direct-lab Skill 的诊断 Lease 分开：后者默认¥300/¥250和直接实验记录不能变成正式资格证据，延长到72小时也不改变证据身份。正式长稳通过也仍不等于已发布 v3；签名 native APT/RPM渠道及精确版本公开客户端验证属于后续发行步骤。

## 审计证据与复现边界

- `readiness-audit-summary.json`：配置匹配、期待trust、Workflow状态、历史payload形状及限制。
- `variable-*` / `environment-*` / `oidc-subject.json` / `main-protection.json` / `rulesets.json` / `settings-*`：GET快照，只保存 allowlist非秘密值、Secret名称。
- `artifact-inventory.json`、`nonterminal-runs.json`、`rehearsal-request-runs.json`、`jobs-*`、`cleanup-selected-json/`：盘点与历史证据。
- `collect_github.py`、`collect_history.py`、`inspect_cleanup.py`：GET-only采集源码；不得把重新采集当作与本快照同一时刻。输出文件默认拒绝覆盖；在新目录复制脚本后执行。
- `summarize_audit.py`：仅本目录已保存证据的离线校验/汇总，可用 `python3 -B summarize_audit.py` 复算；它会重写派生summary，不会发网络请求。
- `SHA256SUMS`：归档文件摘要；所有历史通过、已完成workflow和静态配置均没有被提升为本轮正式运行成功。
