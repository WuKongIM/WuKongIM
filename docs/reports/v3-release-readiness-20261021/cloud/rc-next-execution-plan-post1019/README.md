# #1019 后下一次云验收：只读方案，尚不可执行

2026-10-21。产品固定 `fc18221282f289088ec9b4282fda8e0bfc67a3b8`；控制源码必须等 #1019 正常合并及下面窄修通过后，再冻结真实 protected main SHA。当前只读快照 main 仍 fc182，#1019 head5403、ready、未合并。未调用云、Quote、Acquire 或 dispatch；未修改仓库、预算、Run Plan 或权限。

**结论：现有入口没有安全的失败后新租约续跑方式。推荐只增加一次、显式启动的“已释放 pre-clock 前序继承”，沿用原 request/Issue/总预算，创建 rehearsal-2；不做跨请求调度或自动重试。实施与下一次付费启动均尚未发生。**

## 现有入口为何不可用

- `stage-orchestrate.sh:330–335`：rehearsal 每次从 `committed_micros=0` 开始，只有 formal 可读通过的 transition；`:536–549` 固定 attempt1。
- `internal/usecase/chatlifecyclerun/run.go:239`：Lease ID 为 `<request>-<stage>-<attempt>`；`:322–345` 强制 rehearsal attempt1、commitment0。CLI 虽有 `--committed-micros`，并不能绕过此验证。`run_test.go:55–70` 明确拒绝第二次采购。
- 因此直接用原 request 再启动，会复用 rehearsal-1 标识，但新时间/密钥/Plan digest 已不同，且旧消费归零；这不是同一 Plan 的幂等恢复。换新 request 也不能继承原预算。
- formal 入口要求真实 `rehearsal_pass`、完整报告、认证 zero、重算费用和原 bundle；旧失败不具备这些条件（`.github/workflows/chat-lifecycle-formal.yml:85–133`）。不得制造 passing transition。
- 原存量修复路径只适用于仍存在的同一 Lease（ADR0047）；旧 Lease 已清零，不能重新部署它。Runtime/correctness 的已知失败仍禁止重试。

## 保留的旧事实与费用

原 request `chat-20261021T095421Z-cf11d65c`；首 top39581355444、Acquire39582604546、Deployment39582829931、Release39583589120。旧 selector 对应 `...-rehearsal-1`，created10:11:00.703Z，zero10:19:19.689Z。预置的 readiness_timeout 标签不证明实际超时；首次退出原因仍 unknown，不能因补了诊断就称产品原因已修复。没有合格的2h结果。

原 Quote ¥74.4776 是12h模型估算，不是账单。按旧真实 Plan/Quote/created→zero 和未测网络 `-1` 运行已有 `accrued-cost.sh`，得到 **¥28.9398**（保留全部11GiB流量与16h EIP风险额度，主机费用按1h）；这也不是账单。脚本与全部输入摘要/输出见 `old-lease-conservative-cost.json`。root只读账单页面仍是加载骨架，actual=null，不是0。

**推荐费用策略：在本次明确续跑决策中保留旧 Quote 整额 ¥74.4776 作为不可向下释放的 conservative reserve，不把28.9398较小估算当折扣；前提是认证证据证明该 reserve 覆盖本次模型中的已发生边界。** 原金额由认证 Quote 重算，不接受操作者填金额。余下 operational envelope 为 ¥1275.5224，hard envelope ¥1425.5224；外部仍是整个 request ¥1350/¥1500，清理保留额150不变。新 Quote、远端5s guard及最终费用都读取非零 commitment。

覆盖核对见 `old-quote-reserve-boundary.json`：已有报价包括3服务主机36host-hour+负载12host-hour（含固定磁盘）、11GiB流量、16h EIP风险。Quote时间到zero向上取整957s，即使整个区间持续20Mbps也仅2,392,500,000bytes，小于11GiB；主机与保留时长亦小于报价。但是20Mbps独立事后实测已不可得；这里只能结合受验代码的 `SetBandwidth(20)`、成功同Plan Acquire和完整资源/zero来建立受审模型边界，**不等于独立证明云商最终账单**。如身份/资源集合/价格有效期/计费起点/带宽约束任何一项不闭合，禁止以74.4776猜测放行，停在采购前要求准确账单或另外审定的保守上界。账单迟到后只追加事实，当前reserve不自动降低；超reserve仍待决策。

## 唯一推荐接口与一次性约束

保留原四个业务输入，增加一个可选、无默认值的 `released_preclock_run_id`（首次启动不填；本次候选值39581355444）。这明确扩大了受审入口，必须更新合同，不伪称现有四输入已支持。没有自由预算、重试次数、阈值或 source override。实际调用仍需 root 作一次明确新启动决定；本文件不是 dispatch 授权收据。

1. 同 request、同 source、同 diagnostic 公钥、固定 budget1500000000/stop1350000000。源始终fc182。新控制按实际main记录，bundle由新控制构建并认证；新预演与其正式阶段共用该新 bundle，旧 bundle保留历史身份。#1019不能直接代替尚未实现的继承能力。
2. 启动采购前复用现有 producer/cleanup authentication 逻辑，读取精确旧 top run、attempt1 handoff/失败包、Quote/Receipt/RunPlan/selector、Release及zero；全量身份、来源、run attempt、字节摘要、account hash与12scope对齐。禁止读取解密凭证。只允许前序为rehearsal-1、已完全释放、无formal、无合格run-start/已测阶段证据、无已知runtime/correctness terminal。**没有run-start不是证明未曾发生runtime问题**：本次历史 unknown 要在一次明确启动决定中原样承认，不自动归类 preflight；若决定不接受这种未知，就继续阻断，无技术绕过。
3. 小型白名单 `released_preclock_continuation/v1` 收据：原request/source、公钥指纹、前序top/Acquire/Release及artifact ID/hash、原selector、zero观测、failure_class=`unknown_preclock`（或未来真实preflight）、carry_method=`full_admitted_quote_reserve`、reserve74477600、budget/stop、next_attempt=2、明确决定的引用。不要嵌套任意历史链或允许第三次。
4. 当前 workflow 必须 attempt1；受限、完整枚举相同request的保护producer，只有原top与当前这一次后继可存在。发现另一个后继、重跑、stop marker、活跃租约、未清理、分页缺失就拒绝；共用现有全局付费并发锁。这样重复提交不得再次消费同一前序；不靠最终artifact是否恰好上传成功来判断是否已使用。
5. 仅该认证收据允许 TrustedContext rehearsal attempt2 / committed74477600，生成新 `...-rehearsal-2`。不把 `for attempt in 1` 改成重试循环，而是单次取认证决定的attempt。任何失败保持原cleanup，并终止；禁止自动attempt3。首次启动与formal1行为不变。
6. 一次预演成功后，现有 `prior + rehearsal_cost` 会自然把旧reserve带入formal；formal消费者独立验证sidecar/reserve与RunPlan prior，最终90天证据携带旧失败/quote/zero/continuation。预算不足、身份不闭合、任意已知runtime/correctness失败均停止。原未知原因不因后次通过而删除。
7. 原 #1018 保持唯一open/unlocked Issue，当前快照已确认，**不创建重复Issue**。新generation的dedupe key含attempt或新producer ID，正文并列旧失败与当前费用；保持原request stop范围，不删除旧stop marker。发布/清理状态与Issue失败仍按既有合同。

## 冻结的最小实现范围（方案，尚未编辑）

- `.github/workflows/chat-lifecycle-rehearsal.yml`：唯一可选前序run输入、认证/一次性检查，权限与原生命周期顺序不变。
- 新 `scripts/chat-lifecycle/authenticate-released-preclock.sh`：严格复用现有受验producer/cleanup机制，组合身份、reserve覆盖和一次性验证；仅GitHub读取，不加provider权限。
- `scripts/chat-lifecycle/stage-orchestrate.sh`：初始化认证carry/单次attempt，传证明到materialize，保留sidecar，generation区别化Issue状态。
- `cmd/wkchatlifecycle/main.go`、`internal/usecase/chatlifecyclerun/run.go`：一个受信本地证明参数/结构，只有本窄路径可attempt2与非零prior；原正常输入不变。不改generic cloudlease/provider或产品runtime。
- `.github/workflows/chat-lifecycle-rehearsal-finalize.yml`、`.github/workflows/chat-lifecycle-formal.yml`、`.github/workflows/chat-lifecycle-formal-finalize.yml`：保留/验证继承sidecar及旧失败证据；保留现有transition schema和prior+current成本计算，零历史的旧正常路径兼容。
- 对应独立静态/黑盒integration合同与现有run/CLI测试；适用FLOW、workflowREADME、ADR0047及CHANGELOG解释一次显式released-preclock continuation。所有预算模板、threshold、权限、Issue/Review Agent控制文件不动。

## 先行失败矩阵

| 失败输入/事件 | 必须观察到的结果 |
|---|---|
| 当前原入口同request重启 | 红测试先证明prior归零/同LeaseID，不实际调云 |
| 首次路径私填nonzero或attempt2 | 仍拒绝；现有正常首次路径兼容 |
| 前序跨repo/fork/非main/错workflow/重跑或不完整分页 | 采购前明确拒绝，0 bundle/key/Quote/Acquire |
| selector/account/source/pubkey/quote/receipt不一致；缺zero或残余 | 明确身份/cleanup拒绝，不把缺失当0 |
| 只有退出失败、unknown却伪写preflight | 拒绝错误分类；历史unknown仅显式决定可保留，不自动放行 |
| 已知runtime/correctness、已测/已有formal、stop marker | 终止；不允许该继承路径 |
| second successor/replay/attempt3/已有后继未完 | fail closed，不再次购买 |
| Quote价格/流量/计费时窗证据不足或reserve溢出 | 拒绝，不用小额估算或0替代 |
| 正常已认证旧失败+zero+显式决定 | 一次attempt2/newLease，prior74477600，预算/stop原样 |
| 下一Quote超过1350000000-prior | 原Quote admission拒绝，cleanup安全保留 |
| 预演成功→formal | prior含旧reserve+新预演成本；formal1；同新bundle，90d含旧失败 |
| 任一采集/Issue/产物上传失败 | 原失败不覆盖，cleanup不丢；不能用缺产物解除once-only限制 |

测试先行，使用GitHub/SSH stub真实shell边界，明确错误类别与零外部写；本地相关named checks+Linux全CI后独审。随后冻结真正control及可读授权/身份/预算manifest，才形成下一次可执行JSON。当前没有安全可提交的付费JSON。
