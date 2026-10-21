# 已释放 pre-clock 请求的一次显式续跑：generation 2

方案基线 `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`（#1019 正常合并，tree `0acb67250313fc81a463941295082c828a46124c`）。产品仍固定 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`。v1及其原始哈希完全保留；本代采纳独审R1–R5，只准备接口、范围和测试，不含可执行采购输入，不改变仓库源码。

**唯一推荐切片：同 request 的前序已释放失败租约，经过一次明确启动决定，允许唯一相邻的 protected workflow run 承接旧全額 Quote reserve，创建 rehearsal-2。无自动重试、跨请求账本、新锁、新tag或provider修改。** 当前能力尚未实现，不能直接 dispatch。

## 已确认的不适用路径

当前 `stage-orchestrate.sh:330–335,536–549` 将预演prior置0、attempt固定1；`run.go:239,321–345` 生成 `<request>-rehearsal-1` 且拒绝attempt2/nonzero prior。`formal.yml:85–133` 必须消费真实rehearsal_pass与zero，不接受旧失败。ADR0047仅允许修复仍活着的同一Lease。不能直接重启同request、换request另起预算，或伪造正式transition。

原 top39581355444 / Acquire39582604546 / Release39583589120 的 selector已清零。旧请求 `chat-20261021T095421Z-cf11d65c`；Issue #1018 复用，保持唯一open/unlocked。原始远端退出原因 **unknown**；没有被接受的run-start不证明远端绝未运行。本能力不是产品修复，也不把unknown改写为已证实preflight。

## 精确接口与一次性限制

`chat-lifecycle-rehearsal.yml` 增加唯一可选字符串输入 `released_preclock_run_id`，缺省空。它是明确选择前序证据的输入，不接受金额、次数、lease ID、control或阈值override。现有四输入不变；首次路径仍要求未用过的request。只有root在审阅实现/CI/证据后作一次明确启动决定；本方案不代替该动作。

认证 helper 在 **Issue只读preflight之后、bundle/任何key/Quote/Acquire之前**执行：

1. **相邻fence**：精确API GET前序与当前run。repository numeric ID/full_name、head_repository、numeric workflow_id、允许path、main、workflow_dispatch必须一致；当前run ID来自GITHUB_RUN_ID，并核API与GITHUB_RUN_NUMBER/GITHUB_RUN_ATTEMPT/GITHUB_SHA。前序必须completed/failure、attempt1；当前attempt1；当前run_number必须前序+1。本例旧workflow_id329938003、number32，只允许33。非相邻、rerun、API缺失均拒绝。即便33提前失败/取消/删除，34仍不允许；33的rerun也不允许。这个保守“一次机会”不会自动返还。
2. **不允许链式重启**：前序Run Plan只能rehearsal、attempt1、prior0、Lease后缀rehearsal-1且无continuation sidecar。不能拿33充当前序去启动34。formal不能成为前序。源、operator、request、诊断公钥必须与原证据相等。
3. **默认入口闭合**：复用现有200页×100的完整、有限Artifact inventory；可给 `discover-active-handoffs.sh` 增加一个可选第三输出文件，保存原已获取的非过期Artifact元数据，既有两参数/`.include`格式不变。只保留本request cleanup/complete候选供新helper认证，不另枚举所有workflow历史。只要存在本request的认证cleanup/complete，无 predecessor 的启动就采购前拒绝。身份读取不可达/分页不完整不可当无历史；不声称能从已完全删除/过期的GitHub历史恢复全局账本。
4. **原安全门保留**：全局paid concurrency、两stage活跃handoff检查、request stop marker、cleanup/AutoRelease/sweep不变。stop是request级，不清除旧stop来获得机会；API未知fail closed。相邻编号不替代任何一项。

GitHub编号性质与API字段来自独审的官方依据；删除后不能重新产生相同唯一编号是据此推论，不声称另有删除重置API保证。运行时必须重新GET，11:23历史快照不是执行时证明。

## 必须闭合的前序证据

复用既有helper的代码路径/约定，**不能仅因旧helper exit0就算全部认证**。其目前未核run_attempt/numeric workflow/精确child，zero也只要求scopes非空。

新helper仅从指定GitHub生产者下载白名单公开证据，不读/解密私钥、Manager或其他credential。按API artifact ID/run/size/SHA绑定实际ZIP与所需原始文件摘要；新窄读取明确限制：仅预先列出的JSON/文本证据，单JSON≤2MiB、所选ZIP≤16MiB、总展开≤64MiB/128条目；拒绝路径穿越、链接、重复路径、缺文件及超限，不解压/执行任意payload。这些是待先红验证的新读取边界，不声称旧helper已具备。优先已存在90天complete及cleanup；完整前序失败证据不足就停，不仅凭cleanup中的金额自填。

必须交叉核对：旧top workflow/path/head/event/attempt/title；top里的Acquire/Release numeric run引用；两个child的同repo/main、各自正确workflow/head/attempt/status；Acquire的Quote/Receipt/Plan/public bootstrap、公钥、预算、28资源集合；Release原始artifact→cleanup→zero的相同selector/account；且zero时间晚于创建与Release，固定12 scope集合无缺失/重复：`disk_attachments,disks,eip_associations,eips,enis,instances,nat_gateways,route_entries,security_group_rules,security_groups,vpcs,vswitches`。child control可按其真实受保护运行记录，不强行伪造与旧top同一head，但其源码/关联必须认证。

拒绝任何已知runtime/correctness terminal、合格run-start/已测阶段/正式证据、stop、缺失前序证明或未清零。对于历史unknown，sidecar原样记录 `unclassified_before_accepted_run_start`，明确“无run-start不是未执行证明”。允许选择这一路径是一次明确的新启动决定；不是自动诊断为preflight，更不是无条件忽略失败。

## 费用与下游

只从认证Quote四类line items重算本例 `74477600` micros，全额保留；不采用较小的elapsed公式 `28939800`，也不因账单未读到写0。旧Quote含服务36host-hour/负载12host-hour（含磁盘）、11GiB公网、16h EIP保留风险；Quote→zero上取整957s，20Mbps模型上界2,392,500,000bytes，小于11GiB。认证时检查：价格有效期、创建/释放边界、固定完整资源集合、同Plan受验20Mbps配置、报价总和和所有身份闭包；任何缺口就拒绝，不自行猜新价格。

这只是受审报价模型的保守reserve，不是实际账单/独立事后带宽实测；actual_invoice保持unknown。原budget1500000000/stop1350000000保持不变，新Quote可用stop余额1275522400，hard余额1425522400。清理保留差150000000不变。未来真实账单只追加核对，本次不自动返还reserve。

认证输出小sidecar `released_preclock_continuation/v1`，包含request/source、公钥指纹、前后run ID/workflow ID/number/attempt/control、旧child/artifact摘要、selector/12scopezero、保留的unknown分类、`cost_method=full_admitted_quote_reserve`、reserve与固定budget/stop、next_attempt2。输出不包含raw日志、环境或解密凭证。consumer不得只信其中自报的valid字段：CLI校严格schema与身份/预算，后继workflow按原始证据重新绑定。

TrustedContext增加一个可选窄continuation证明，CLI新增本地 `--released-preclock-proof`；无证明仍拒绝attempt2/nonzero prior。仅该证明允许rehearsal2/prior74477600；不把单次执行改成attempt循环。当前unknown历史和旧失败必须随sidecar保存。

成功新预演仍必须真正2h pass并清零，随后原 `prior + current_rehearsal_cost` 自然进入formal1；formal重新绑定sidecar、旧证据与RunPlan prior，拒绝nonzero prior无证明/篡改。保留原transition schema、72h→≤8h容量→30min恢复、原阈值与5s guard。最终90天证据含原失败和两次预演费用，不因新通过覆盖旧失败。Issue dedupe key带新attempt或producer ID，避免抑制同request的新状态；不创建第二个Issue。

## 拟编辑范围及测试 seam

详见 `implementation-scope.json`（准确路径及责任，未编辑）。仅一个认证helper；不建立通用continuation引擎。核心为rehearsal输入/认证、已有有界inventory可选输出、stage单次身份/预算、materialize窄证明、三处finalizer/formal sidecar携带。正常零历史路径兼容。

测试先行seam：①真实helper CLI配固定GitHub API/ZIP替身；②真实stage shell黑盒确认失败时bundle/key/Quote/Acquire调用数严格0；③Materialize公开函数与CLI；④formal/finalizer已有命令边界的sidecar传播；⑤workflow静态权限/顺序合同。`failure-matrix.json`逐项给预期拒绝类别，不把任意exit1当正确负例。采用逐个垂直red→green切片，保留首红；当前尚未写测试或实现。

对应named checks从冻结policy读取：`go-unit`、`go-vet`、`go-format`、`go-mod-tidy`、`scripts-integration`、`workflow-contracts`、`flow-doc-contracts`；最终changed paths重选。Linux首次完整CI必须执行过程级合同，不能拿本地stub绿冒充真实云成功。独审与精确source/control/budget冻结完成后才可形成付费输入；本代没有该输入。
