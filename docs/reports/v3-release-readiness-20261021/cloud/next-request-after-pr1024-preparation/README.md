# 下一云 RC — #1024 合并前待审批模板

**当前不可执行，且没有本次新付费授权。** #1024 候选为 `c91e37b4e35e1009c9c5cb55ff351a38b870014f` / tree `47eb4e4b7240441456d54c1935eb37372c80cddf`，基于 `cee5d51f325fb84033060f546337d326110adae5`。七文件只涉及采集器、测试和文档；首次 Linux CI 尚待父任务终态确认，合并 SHA 尚未给出。候选观察摘要不是最终规则冻结。收到实际 merge SHA 后，在新一代文件中核完整树/CI并冻结精确控制和产品来源，不能静默填入候选 head、旧 cee5 bundle 或其他来源。

旧请求 `chat-20261021T163850Z-4ccd7506` 在 61.212052848 秒以 `message_loss=1` / `product_failure` 终止；原因仍未确证，#1024 **不声称修复消息丢失**。旧终态采集存在 metrics/range oversized。已有 Stop 标记；12 scope 零库存观测为 **2026-10-21 17:43:09.4025176 UTC / 2026-10-22 01:43:09.4025176 上海**，Release run 39646126395，cleanup artifact 12366858052。formal 没有启动。出处及字节摘要见 prior-evidence.json；本轮没有刷新云库存。

旧两次完整 Quote reserve 148955200 + 本次 74477600 = **223432800 micros（¥223.4328）**，实际账单未知。新 request 仅允许硬上限 **1276567200**、停止线 **1126567200**，差值固定150000000；分别加历史预留仍为整项1500000000/1350000000。新 request 初始 prior=0，历史预留通过降低两条硬门槛保留，不伪造跨请求 ledger 或重复计费。新请求内部 formal prior 继续由认证预演 transition 产生。详见 budget.json。

未来获准后才建立新 request、诊断公钥及唯一 `[chat-lifecycle] <request_id>` Issue，通过同一正式入口的六项输入启动（pending-plan.json 是带 null 的不可执行模板）。杭州4台4核8G，3服务+1负载；原2h预演通过并释放后，使用新96h Lease进行连续72h长稳、同一老化数据≤8h容量和30m恢复。原负载、正确性、延迟、磁盘与资源阈值不变。任何身份、库存、容量、报价、预算或来源准入失败即停，不追加预算/权限；运行时或正确性失败不自动重试，按原安全清理流程释放。

**旧请求不能 continuation。** 已接受 run-start、出现运行时失败且有 Stop，不能伪装为 released-preclock、清除标记或复用旧租约。新的独立实验必须另有明确启动决定和付费授权；合并授权不等于启动授权。准备此模板不形成批准提问，也不创建任何资源。

适用依据（均按上述候选 Git 对象读取）：

- `.github/workflows/README.md:1018–1067`：明确付费授权、Issue采购前预检、向下预算贯穿及旧来源限制；`1069–1098`：continuation限制；`1145–1177`：失败/保留/清理及未结算账单；`1194–1248`：Stop与新正式Lease链。
- `.github/workflows/chat-lifecycle-rehearsal.yml:7–32,74–119`：六输入、main/source/公钥/预算准入、现有库存与finalizer；不是新增门禁。
- `docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md:14–27,159–189,205–220,250–267`：顺序、不可恢复旧运行、正式通过与容量恢复条件。
- `internal/usecase/chatlifecyclerun/FLOW.md`：合法 package scope，默认/向下预算及一次性续跑边界。无脚本目录附加 FLOW。
- `.agents/skills/wukongim-chat-lifecycle/SKILL.md` 已阅读用于路线判别；它的 Direct Lab 是另一个诊断流程，明确不是正式证据，**本模板不使用其 ¥300/同Lease修复入口**，以已选正式 Workflow 合同为准。

合并后再补最终来源/规则hash，获准前不新建Issue、身份、调度或获取Quote。不访问私钥/secret/云API，不创建automation，不改源仓库。后续启动前仅复用现有流程要求的live guard；本次未声称已有live OIDC/provider/库存认证。

现场诊断的额外限制（父任务证据交接，未在此重试）：前次 Analysis 两次均未连上 MCP；第二次使用已有兼容 CLI 0.163，12 次 health 请求超时，原因未知，相关授权已关闭。不得假定下一轮现场 MCP 可用。#1024 仅改进既有失败样本/状态及有界 metrics 的留存，不改变产品 runtime，不证明消息丢失或过期原因已修复。这一风险应如实纳入之后审批；不得借此自动扩 IAM 或进行第三次诊断尝试。
