# 空 Job 修复后的下一次执行：只读可行性结论

**当前正式工具不能在新 request 中保持所需的任务累计预算。需要一项窄工具改动；不能只改输入后直接启动。** 本文不生成可派发 payload、不请求或复用新的付费批准。原 request 已 Stop、两次 Lease 都已清零，一次 released continuation 已用尽。

本次源码冻结为 draft #1021 的 `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`；其空 Job 修复与预算能力无关。未来控制必须以正常合并后的实际 SHA 重新冻结，目前为待定。产品仍为 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`。本轮仅本地 `git show` 和已有公开文件读取，没有 GitHub/provider/SSH 调用，没有代码编辑、工作流派发、资源操作或密钥访问。

## 已有契约为何不适用

精确来源均为上述 ff0 提交，原文件 SHA256 见 `source-freeze.json`，带行号摘录见 `source-excerpts.txt`。

| 已核入口 | 实际约束与影响 |
| --- | --- |
| `.github/workflows/chat-lifecycle-rehearsal.yml:5–28,57` | 只有原四输入和 `released_preclock_run_id`，固定 rehearsal 模板；没有预算、旧费用或任意模板入口。 |
| `scripts/chat-lifecycle/stage-orchestrate.sh:53–69`；`authenticate-released-preclock.py:517–531` | 首次启动固定 attempt 1 / committed 0；不是从环境接受任意已花费额。 |
| `internal/usecase/chatlifecyclerun/run.go:429–440` | rehearsal 与 formal 均严格要求 1,500,000,000 / 1,350,000,000 micros。把模板直接缩为剩余额度会拒绝。 |
| `cmd/wkchatlifecycle/main.go:219–221`；`run.go:456–488` | CLI 虽有 `--committed-micros`，无 continuation proof 的 rehearsal 必须为 0。该内部 flag 不是可用的操作员跨 request 预算入口。`--authorized-repair-budget-cny` 仅用于不同的 direct-repair 契约。 |
| `authenticate-released-preclock.py:127–142,254–259,341–346`；`run.go:234–245` | 旧续跑证明要求同 request、原 rehearsal-1 / prior 0 / 无 sidecar、相邻 run、next attempt 2。run 33 的 rehearsal-2 不能再作 predecessor，换 request 也不匹配身份。 |
| `authenticate-released-preclock.py:155–193` | 原 Stop marker 继续有效；原 request 已花费记录不会因省略 predecessor 变成首次。不得删 marker 或把原 request 伪装成新启动。 |
| `.github/workflows/chat-lifecycle-formal.yml:97–135` | formal 只消费同 request 的真实 rehearsal_pass + zero 及其费用等式；不能手填历史两次失败为 passing transition。 |
| `authenticate-released-preclock.py:534–554` | 消费端首次路径也要求固定 BUDGET、prior 0；仅放宽模板而不完整传递预算仍会失败。 |

工作流 README 1018–1085、1170–1218 明确：预算来自受审 Run Plan，旧 continuation 不是通用跨 request 账本，Stop/cleanup 和不重试 runtime/correctness 的规则保留。

## 保守预算计算

两份已验证公开 Quote 各 74,477,600 micros。本次额外只在本地调用同一冻结 helper 的**纯 `validate_cost` 函数**，分别对两份原 Plan/Receipt/Quote/zero bytes 检查固定 4-host、磁盘、EIP/20 Mbps 流量、报价至清零时间覆盖，两次通过。输入绝对路径与字节 SHA、时段见 `options.json`。此检查没有调用 helper 的 API/prepare/validate 模式，也不是新增 live authentication 或账单查询。

| 项目 | micros | CNY |
| --- | ---: | ---: |
| 历史两次 full Quote reserve | 148,955,200 | 148.9552 |
| 整个任务 hard limit | 1,500,000,000 | 1500 |
| 整个任务 operational stop | 1,350,000,000 | 1350 |
| 新 request 可用 hard limit | **1,351,044,800** | **1351.0448** |
| 新 request 可用 operational stop | **1,201,044,800** | **1201.0448** |

保留额不是实际支出或已结算账单；实际账单仍未知，不以“主机运行较短”为由退还保留额。上述差额不等于新 Quote 已通过，也不保证剩余金额能覆盖后续所有阶段。

## 两种最小未来工具选项

### A. 首选：新 request 的向下预算配置

增加一个明确受审、仅向下的预算绑定；新 request 的 hard/stop 分别严格使用 **1351044800 / 1201044800** micros，prior 仍为 0。默认未选该配置时，现有 1500/1350、首次路径和一次续跑证明行为保持不变。

这可以是受审 Run Plan 的明确预算配置选择，不需要建立跨 request 调度器或新账本。无论最终接口是配置选择还是显式整数输入，必须在实际 materialize/Quote/Acquire/远端环境与预算检查/formal transition/finalizer 中保留同一预算，不能只写在审批文本或交给外部人工监控。适用新批准必须明确绑定历史保留额、精确剩余 hard/stop 和唯一新 request。原 stop marker、原失败及原续跑限制完全保留。

必要改动边界：

- `chat-lifecycle-rehearsal.yml`、`stage-orchestrate.sh`、`cmd/wkchatlifecycle/main.go`、`internal/usecase/chatlifecyclerun/run.go`：接收并严格物化受审向下预算；不误把旧 direct-repair 选项用于正式流程。
- 原两个 Run Plan 模板保持默认；新预算选择按受审契约派生或使用独立配置，不就地覆盖历史默认。
- `authenticate-released-preclock.py` 的 first-start/consumer BUDGET 判定须识别此新预算契约；旧 released-preclock 证明的精确 1500/1350 及一次性规则不放宽。
- formal transition/start/finalizer 和费用摘要须从认证 Run Plan 继承并核对所选预算。当前硬编码处包括 formal workflow:107、rehearsal-finalize:442、formal-finalize:419、`select-formal-transition-committed.jq:11`；需要逐项判断使用“所选预算”还是“全局绝对最大值”，不能机械放宽。
- 相应合同/黑盒测试及 docs/FLOW/CHANGELOG 同步。这里只列必要边界，未认定这些文件都必须改或完成最终接口设计。

优点：保留现有总额恒等式 `旧 reserve + 新 request 已用 ≤ 原任务上限`；不把旧 Stop request 重新激活；不新增跨 request 费用真实性模型。局限：工具只执行新 request 的明确剩余上限，不会自动发现未来第三、第四个 request 的费用；本次固定旧 reserve 与新批准的关联需要留在受审计划中。此方案不能被描述为通用全账户账本。

### B. 备选：显式认证跨 request 旧费用

保留原 hard/stop 1500/1350，把新 request 的初始 commitment 设置为 **148955200**，由独立的受审类型绑定新 request、旧两个已释放 Lease、真实 Quote/Receipt/zero 与费用覆盖证据，再沿既有 prior-cost 通路传递到 formal。

这不是把当前 `released_preclock_run_id` 换成 run 33：必须是新的显式授权/证据类型，既有同 request 原 rehearsal-1 规则不变。需要增加或扩展只读认证 helper、typed Go proof/CLI、stage 接线、远端与终态 sidecar 携带/重新认证及正式消费者，处理证据过期、重复费用与跨请求身份。优点是原上限和累计承诺可直接表达；代价明显高于 A，容易扩成多 request 调度/账本，不建议作为本次最小修复的首选。

## 后续决策边界

当前没有可安全执行的五输入命令。先由维护者选择并评审上述窄预算能力；ff0 的空 Job 修复本身不提供该能力。未来代码、CI、合并及实际控制 SHA 都确定后，才能向用户呈现包含唯一新 request、固定产品、完整 2h → release → 新72h/≤8h/30min 链、历史保留额和严格剩余预算的具体启动单，另取明确启动批准。届时沿原流程执行已有身份、Issue、库存、Quote 与 cleanup 门禁；本分析不增加新的云门禁，也不替代其中任何门禁。

禁止的替代：直接用新 request 重置到 1500/1350；忽略/删除旧 Stop；伪造 predecessor 或 rehearsal_pass；延长一次续跑次数；只依赖外部提醒控制累计成本；改走 direct-lab；以合并批准或上轮已消耗的启动授权视为新启动批准。
