# PR #1022 后的新 request：只读启动准备

当前状态是 **准备完成，尚不能启动**。本目录只核对源码、公开 GitHub 状态和既有公开证据，没有派发/启停 Workflow、创建/评论 Issue、调用云厂商、采购、改权限、生成密钥或读取凭据。`dispatch-draft.json` 不是可执行授权。

受审候选为 `b3fdb777b9270128fe56086bc47562b321c2c947`，tree `ae5a4175337f10f521a13a6c704c1b3040cf2bd2`，工作树干净。公开快照中 #1022 仍 open/unmerged：默认单测、脚本 integration、原生包预览及 Flutter iOS 尚在运行；已完成的其他检查不能替代它们。最终控制源记作 `pending_control_sha`，产品源也须由 root 在 CI/合并后明确冻结。本目录不会把 PR 分支当作已获准采购的源。

`source-freeze.json` 保存候选源、tree、规则文件 SHA-256/Git blob 以及预算链源码摘要。`github-public-snapshot.json` 记录 GET 起止时刻、公开端点、精确 head 检查、Issue 和 Workflow 状态。它们是时间点证据，不是当前云库存证明。

## 预算和旧 request

| 项目 | CNY micros | 人民币 |
| --- | ---: | ---: |
| 旧两次完整 Quote 的保守 reserve | 148955200 | 148.9552 |
| 新 request 硬上限 H | 1351044800 | 1351.0448 |
| 新 request 运行停止阈值 S | 1201044800 | 1201.0448 |
| 旧 reserve + 新 H | 1500000000 | 1500 |
| 旧 reserve + 新 S | 1350000000 | 1350 |

H−S 仍为 ¥150。旧 reserve **不是实际账单**；实际厂商账单未知。跨 request 的保留额在任务证据和明确累计授权中体现，不伪装成新 request 已经支出的 `committed_micros`。新 request 首次 prior=0、attempt=1，只有新 request 自己的合格预演成本才在正式阶段成为 prior。

旧 request `chat-20261021T095421Z-cf11d65c` 的两份 Lease 已由既有证据释放，一次 released-preclock continuation 已消耗。本次读取的 [#1018](https://github.com/WuKongIM/WuKongIM/issues/1018) 仍 open/unlocked，有 3 条历史评论，最后一条为 `rehearsal_zero_inventory`；它保留旧 request 的 stop 和失败历史，不能充当新 request 的唯一跟踪 Issue。公开评论也不能代替获准启动时重新认证的原始零库存证据。

降低预算与非空 `released_preclock_run_id` 的组合会被拒绝。不能通过复用旧 request、忽略 stop、换 request 伪造续跑、补写跨 request prior 或恢复默认 ¥1500 来绕过累计边界。

## 可审查的 dispatch 参数

入口只能是 `.github/workflows/chat-lifecycle-rehearsal.yml`，仓库 `WuKongIM/WuKongIM`，ref=`main`。六项输入如下；具体草案见 `dispatch-draft.json`。

| 输入 | 本次值/待定项 |
| --- | --- |
| `source_sha` | 候选草案 `b3fdb777b9270128fe56086bc47562b321c2c947`；等待 root 冻结最终产品 SHA |
| `operator` | `tangtaoit` |
| `codex_diagnostic_pubkey` | 待最终 request 的 Ed25519 公钥；本次未访问或生成密钥 |
| `request_id` | 待冻结的新唯一 request；不能使用旧 request |
| `released_preclock_run_id` | 空字符串 |
| `request_budget_limit_micros` | **字符串 `"1351044800"`**，不可省略而采用默认 |

没有独立 S、prior、机器数量、时长或重试输入。S 自动派生为 `1201044800`，其他值来自受审模板。未来获准后的调度形式为 `gh workflow run chat-lifecycle-rehearsal.yml --repo WuKongIM/WuKongIM --ref main --json < approved-inputs.json`；`approved-inputs.json` 必须仅含最终六项输入，本目录没有创建该可执行文件。不能直接把 `dispatch-draft.json` 传入。

不能将 `--ref` 改成裸 SHA 来“固定控制源”：入口的 `refs/heads/main` 条件会跳过任务。应在最终获准启动前将 main 读回值与 root 冻结的控制 SHA 对齐，并记录每个 bundle/Acquire/Deploy/finalizer producer 的真实控制 SHA。main 意外变更须重新审查相关差异，不能静默使用新控制代码。

用户的“以后你自己合并”只解决逐 PR 合并确认，不是上述付费启动批准。root 在源固定后统一提出一次具体付费启动问题；当前不向用户追加问题。

## 预算贯通已核实的范围

`budget-chain.json` 逐层列出文件和行号：字符串输入 → stage 脚本 → materialize CLI/TrustedContext → RunPlan/Lease → Quote/Receipt → DeploymentPlan → root-only load environment → **产品源构建的 wkcli** → 正式过渡/最终认证。

当前源码可接收同一降低后的 H/S；formal 从认证后的预演 RunPlan/Receipt 派生，缺失或冲突不能恢复默认。Quote 必须满足 `estimate <= S−prior`；远端在 `accrued >= S` 停止。finalizer 在已拥有 Lease 的 cleanup 后做预算资格认证，资格失败仍保留 Release/零库存上传责任。

bundle 中的 `wukongim`、`wkcli`、`wkanalysis` 从精确产品源构建。旧 `fc182` 产品的 wkcli 不支持此预算，仅更新控制代码不足以运行；b3f 候选已含原生守卫支持，但此静态核对不证明新候选 Linux CI 或真实远端运行通过。

既有 `9a4900dc67ac674791edba11ea9a719cd91ec570` 本地交付是另一源身份：6/7 named checks 通过，完整 scripts integration 原 9m 超时保留。不可拿它的单测绿替代 b3f 首次完整 CI。当前未重跑任何测试。

## 获准后按既有流程执行

1. 等 #1022 精确 head 的 CI/审查及合并，root 冻结最终产品/控制 SHA；更新源与规则 digest，并取得具体付费启动授权。
2. 授权后创建新 request 唯一、open、unlocked 的跟踪 Issue，绑定公钥身份；startup 的只读 Issue preflight 在 bundle/key/Quote 前执行。
3. startup 认证完整有界历史，拒绝任何尚无 selector-bound zero proof 的预演/正式 handoff 或 cleanup-pending Lease，拒绝冲突的 paid producer。旧 Issue 或停用的 schedules 不提供库存权威。
4. 取得当前报价、容量/配额、账号/区域/Plan/源/bundle/公钥及完整库存证据；价格、身份、库存未知或不一致即拒绝。Quote 和部署身份必须先封入原 Plan，release selector 必须在 Acquire 前可推导。此次没有获取真实新报价。
5. 杭州 3 个服务节点 + 1 个负载节点，每台 4 vCPU/8 GiB、Ubuntu x86_64、非突发按量；每台系统盘40 GiB，服务数据盘500 GiB，负载数据盘200 GiB，仅负载节点一个公网 EIP。原256 hash slots、2,000 SEND/s、10,000 在线用户/初始全量同步及原始阈值不变。
6. 12h AutoRelease 预演 Lease：2h readiness 上限后，必须出现真实 run-start 再累计2h预演。失败保留原始证据，按原有同 Lease 控制修复/停止规则处理；已知 runtime/correctness 失败不重试采购。
7. 预演通过且 Release 到 exact zero 后，认证同 request 的 transition；scheduled formal starter 消费一次，复用原不可变 bundle，在新 Quote 通过后获取**全新**96h Lease。不能手动派发 formal 冒充过渡。
8. 新 Lease 上运行连续72h、h24/h72资格、≤8h容量阶梯和30m恢复，同一 formal-chain 进程/数据集/worker fence；81.5h入场余量、每5秒费用守卫、1h过期清理余量保留。
9. 完整收集报告和有限诊断后 Release 至 exact zero。诊断最多2h、报告救援最多再2h，预算/过期/失控/磁盘/Stop可提前结束；报告存储失败不能无限保留资源。完整报告/身份/费用链/零库存缺一不能宣称 formal pass。

`execution-and-live-gates.json` 给出逐步责任和 fail-closed 条件。当前所有 live 项均未核验，不从旧价格、旧账号成功或本地 CI 推断它们。

## 安全自动化的当前状态与未来启用点

| Workflow | 公开快照状态 | 既有受权流程的启用点 |
| --- | --- | --- |
| rehearsal-finalize | disabled_manually | 顶层预演在 orchestration 前启用 |
| cloud-lease-release | disabled_manually | provision 的独立 arm_release 在付费 Acquire 前启用；旧 sweep 结束后再封定启用 |
| formal starter | disabled_manually | 合格预演 finalizer 发布 transition 前启用 |
| formal-finalize | disabled_manually | formal starter 在正式 orchestration 前启用 |

这与仓库 idle 自动停用设计兼容，不是资源零库存证明；本次没有开启它们。入口 rehearsal/stop、bundle/activate、provision/observe 当前 active。将来各自 owner 的启用失败或观测不完整会阻断依赖动作；不能绕过防线采购。详见 `safety-automation-plan.json`。

人工停止仅走 exact request 的 `chat-lifecycle-stop.yml`，先持久化 stop marker，再交付 finalizer 清理，不能硬取消 cleanup owner。

## 证据复验与边界

`SHA256SUMS` 覆盖本目录显式 allowlist 内全部公开文件（不含自身）；`ARCHIVE_ALLOWLIST.json` 给出每个文件的用途和排除边界。复验命令：在此目录运行 `LC_ALL=C shasum -a 256 -c SHA256SUMS`。显式 C locale 避免本机 Perl 不支持环境中的 C.UTF-8；文件摘要通过只证明归档字节一致，不赋予 dispatch 或 provider 权限。

本方案最多使下一次真实 RC 具备可审查入口；没有证明2h/72h云验收通过，也不是 v3 正式发布。发布仍需独立完整资格和仓库要求的签名原生包渠道及精确版本公共 APT/RPM 验证。
