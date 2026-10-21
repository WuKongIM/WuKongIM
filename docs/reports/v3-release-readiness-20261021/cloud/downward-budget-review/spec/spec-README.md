# 新 request 向下预算：最小实现设计（尚未实施）

**固定产品 fc182 时 NO-GO。** 不仅控制层，产品内 `wkcli` 的远端 guard 也拒绝降低后的上限。`cmd/wkcli/internal/benchmark/formal_chain_command.go:163–165,229–237` 在 fc182 与 ff0 字节相同；`cloud-deployment-bundle.yml:91–99` 将 wkcli 从产品 source 构建，而控制工具从 control 构建。不能只合并控制改动便声称可运行，也不能静默把 wkcli 换成控制源。本方案需要未来包含合法向下预算支持的新产品候选，重新验收并另行冻结/批准其源；当前没有这些批准，也没有新付费启动批准。

源码边界：已知合并控制基线 `e30299fa59ea6669ea71024eabe209840f241f19`；本次只读候选 `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`。ff0 的相关差异仅为空 Job 分类/诊断，尚不含预算能力。本轮未做 API/云/SSH/派发、未改仓库或写 shipping 测试；第一版冻结目录未改动。

## 最小接口建议

只新增一个可选**字符串**输入 `request_budget_limit_micros`，省略保持 `1500000000`。严格接受规范十进制整数 `H`，范围 **150000000 < H <= 1500000000**；自动导出 **S = H − 150000000**，保留原 ¥150 的 hard/stop 差额，避免两个可矛盾的操作员输入。显式空、0、负数、浮点、指数、前导正号/零、空白及溢出均拒绝。此范围是拟新增且待批准的工具契约。

本次将来的具体值为 `H=1351044800`、`S=1201044800`。旧两次 full Quote reserve **148955200** 留在任务批准/证据中，新 request 的 prior=0：`旧保留額+新H=1500000000`、`旧保留額+新S=1350000000`。实际账单仍未知；该方案不宣称自动认证跨 request 支出，不重置任务预算。

CLI 增加同名预算参数并绑定 `TrustedContext` 的明确可选授权上限；区分 omitted 与 explicit zero。materialize 只从原受审默认模板派生局部有效预算，不改模板文件或其他拓扑/时间/阈值。仅 rehearsal/formal 可用；direct-repair 不接受此选项。**非默认 H 与 released-preclock proof/入口不能组合**，原 proof schema、默认1500/1350、prior、相邻 run、同 request、一次续跑和 Stop 规则保持逐字语义。降低预算的新 request 不取得额外续跑资格。

## 贯穿与认证

| 边界（ff0 行号） | 最小行为 |
| --- | --- |
| rehearsal workflow:5–28；stage:53–69,559–573 | 在 bundle/identity/Quote 前验证输入与模式；把唯一 H 送入 materialize。没有额外输入时旧字节/行为保持。 |
| `chatlifecyclerun/run.go:323–325,377–379,429–440,456–488` | 新 TrustedContext 仅向下派生有效模板；root `operational_stop_micros` 与 Lease budget 严格同值；首次 prior0，formal prior仍来自同 request 的过渡成本。拒绝通过篡改模板而不携带受信输入绕过。 |
| cloudlease `control.go:706–713,823–840` | **已有动态行为，可不改生产代码**：Plan摘要含预算，Receipt承接预算；Quote须不超过 stop−prior。边界保持现有 `quote == remaining` 接受、`+1` 拒绝。 |
| clouddeploy `deployment.go:345,432–440,688–717` | immutable budget 当前硬等于默认，必须改为同一向下范围/差额；保留报价求和/先前成本检查，repair分支不改。Plan摘要继续封住预算。 |
| activate workflow:237–247 | **已有动态行为，可不改生产代码**：从已验证 DeploymentPlan 取 H/S/prior/Quote，写 root-only load.env。增加契约/实际生成测试证明无回退默认、无日志泄密。 |
| wkcli `formal_chain_command.go:163–242`；`chat_lifecycle_runner.go:213–220` | **产品侧必须改**：immutable rehearsal/formal 接受同一向下预算范围。保留 Quote/prior、2h/81.5h、expiry 等全部原 admission。禁止通过 direct_repair 模式绕过。 |
| wkcli `formal_chain_command.go:245–307` | **已有动态 Observe 可不改**：`accrued >= g.operationalStop` 终止；用降低 S 的边界测试证明 soak/capacity/recovery 一直使用同一 guard。 |
| `authenticate-released-preclock.py:534–554` | 无sidecar/prior0/attempt1首启消费者认可有效向下预算，并检查 root stop 与 Lease stop一致；旧有sidecar分支仍只接受原默认 envelope。不得把新预算路径变成跨 request proof。 |
| rehearsal-finalize:436–459 | 从认证原 RunPlan 的 S 判定新累计费用，继续原 v1 transition + 完整 RunPlan/Receipt 打包；不增加 transition schema 或费用证明类型。 |
| formal workflow:97–135；stage:347–350 | 从已认证 rehearsal RunPlan 读取 H/S并验证 Receipt和派生关系，传入 formal materialize；缺失/冲突必须拒绝，**不得默认恢复1500/1350**。保留 prior+本次rehearsal cost 等式。JQ过渡费用筛选改用该 Plan 的 S。 |
| formal-finalize:378–433 | 在已先行 cleanup 后，核 handoff、final/previous-final、rehearsal RunPlan、各 Receipt/DeploymentPlan 的预算一致；aggregate检查用该 H，丢失/提高cap不得形成资格。失败不能阻止已有Lease Release/zero。 |

**现有公开字段足以表达，无需跨 request authority 或新 sidecar schema。** 保留 `RunPlan.lease_plan.budget`、root stop、Receipt和DeploymentPlan预算；既有 handoff/transition/terminal bundles 已包含这些完整文件。新的 reader 校验只认证同一新 request 内预算没有被丢失或提高。formal没有额外操作员预算入口，它必须从认证 rehearsal Plan 派生。纯 Go TrustedContext 仍不声称验证 GitHub；来源认证由原 protected shell/helper 完成。

`accrued-cost.sh:57` 的1500上限与 `diagnosis-budget.sh:32` 的1500检查可保留为全局数值范围，**不能当新请求授权门禁**；后者:18,34 已使用 Plan S 决定是否可保留诊断窗口。新预算消费者的范围与一致性检查必须在其调用之前成立。无需改 Sweep/provider/Stop 或新增外部监控来兜底预算。

## 预计最小文件范围

11 个生产逻辑文件（最终差分以实现为准）：

1. `.github/workflows/chat-lifecycle-rehearsal.yml`
2. `.github/workflows/chat-lifecycle-formal.yml`
3. `.github/workflows/chat-lifecycle-rehearsal-finalize.yml`
4. `.github/workflows/chat-lifecycle-formal-finalize.yml`
5. `scripts/chat-lifecycle/stage-orchestrate.sh`
6. `scripts/chat-lifecycle/select-formal-transition-committed.jq`
7. `scripts/chat-lifecycle/authenticate-released-preclock.py`
8. `cmd/wkchatlifecycle/main.go`
9. `internal/usecase/chatlifecyclerun/run.go`
10. `internal/usecase/clouddeploy/deployment.go`
11. `cmd/wkcli/internal/benchmark/formal_chain_command.go`

针对性测试优先沿已有 `main_test.go`、`run_test.go`、`deployment_test.go`、`formal_chain_command_test.go`、`chat_lifecycle_workflows_test.go`、`chat_lifecycle_released_preclock*_test.go` 和 cloud activation契约；真实 shell/进程的预算链测试必须在 `*_integration_test.go`，不是仅静态字符串断言。不复制通用调度/认证框架；若提取窄测试文件可单列。

文档需同步 `.github/workflows/README.md`、根 CHANGELOG、PROJECT_KNOWLEDGE、`chatlifecyclerun/FLOW.md`、`clouddeploy/FLOW.md`；FLOW_INDEX 仅在生成工具实际要求时更新。模板、provider、Direct Lab Skill、原 Stop/一次续跑证明、bundle产品/控制来源规则、Issue/Review Agent控制与policy均不在修改范围。

## 失败矩阵与验收边界

`failure-inventory.json` 冻结 **42 项计划病例**，尚未编写或执行：8输入、10 materialize、11 reader/跨阶段、5 Lease/deployment、8原生guard。包括完整默认正向、降低预算的两阶段正向、所有非法值、逐层预算漂移/缺失、费用恰等于边界、未知/丢失sidecar、旧proof不放宽、已拥有Lease时消费者失败仍cleanup，以及旧fc182应拒绝的对照。

实施时先写对应真实CLI/消费者/guard测试并留首红，再改代码。适用命名检查预计为 `go-unit`、`go-vet`、`go-format`、`go-mod-tidy`、`scripts-integration`、`workflow-contracts`、`flow-doc-contracts`；最终按 frozen policy 与实际 changed paths 重新选择，不能复制/改短其命令或时限。当前阶段没有执行测试。

未来验收需证明新产品候选的 wkcli 原生守卫合法接收降低预算，并在边界停止，同时保留远端源/构建身份与既有Linux候选相关验收；既有fc182验收不能冒充新候选。长稳仍需之后单独批准的真实完整运行。当前只批准工具设计，不能先换 source、启动新请求或追加预算。
