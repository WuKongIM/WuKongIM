# 新云 RC 冻结计划独立只读评审

结论：**0 项开放发现**。这份计划符合当前正式 RC Actions 合同，可以作为新的明确付费启动决定的审批材料；这不是启动授权、实时准入通过或正式版发布通过。评审对象是 `next-request-after-pr1024-merged-preparation` 的原冻结件，manifest SHA256 `b180f97bf0813bcd40578540b365a68102d35d5e6e03b86ebcfac7fa7572de35`。

## 核验结果

| 项目 | 独立证据与结论 |
| --- | --- |
| 产品与控制源 | 本地 Git 中 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06` 的父提交是 cee5 + f419；79a 与 `f419ea8f82c8bd16a777cfc3bf6cbf8c85be60dd` 完整 tree 同为 `a13367b2dc4658e6c27a1992f240544636e5178d`。118 个离线工具闭包文件逐字相同。计划要求启动前远端 main 仍为 79a，否则重新审查，禁止隐式替换源码。 |
| Linux 证据边界 | 留存的 generation 4 Git API 对象把实际 CI checkout `0c575fd63bcf8164332ca28053039f8fdf84137f` 绑定到同一 tree；原 default/scripts/E2E 终态收据通过并有各自清理零残留。scripts 为 576.017s，未改原 600s/9m/parallel 2。此轮没有刷新或重跑 CI；候选通过不等于云 2h/72h 通过。 |
| 旧授权不可复用 | 用户先前批准的是 cee5 新云 RC，该 request 已经实际运行至终态。长期自主合并授权不等于新付费采购授权。新计划 `paid_authorized_for_this_new_request=false`、`executable=false`，request 与诊断公钥留空。 |
| 不可作为 released pre-clock 续跑 | 旧 request `chat-20261021T163850Z-4ccd7506` 的原 final.json 是 rehearsal/runtime `product_failure/message_loss`，61.212052848s，losses=1，之后有 Stop；并非未计时启动失败。计划不带 predecessor，`released_preclock_run_id` 为空，新 request 从 attempt 1/prior 0 开始，符合续跑拒绝条件。 |
| 三份历史报价 | 两个旧 Lease 与最近 cee5 Lease 的留存 Quote 各 74477600 微元，三个不同 lease_id，且逐份 line items 之和都等于 Quote 总额；累计 223432800 微元（¥223.4328）。这是完整报价保守预留，实际账单仍未知。 |
| 新旧预算守恒 | 223432800 + 1276567200 = 1500000000；223432800 + 1126567200 = 1350000000。新请求保留 ¥150 固定安全差额，初始 committed/prior 为 0。历史预留留在请求外，不塞入未认证 prior；formal 仅继承新 rehearsal 创建至清零的认证保守费用。 |
| 固定资源与阶段 | 与原模板逐项相符：杭州，3 service + 1 load，每台 4c/8GiB、40GiB 系统盘，service 各 500GiB/load 200GiB 数据盘，1 load EIP、20Mbps 上限。12h Lease 中 2h 预演，pass + 完整证据 + 清零后才取得 fresh 96h formal Lease；同 source/bundle/request，连续 72h 后同老化数据容量 ≤8h、2000 SEND/s 恢复 30m。10k 在线、2k SEND/s、12/256/3/3 和 YAML 门槛不变。 |
| 实际准入没有旁路 | 计划保持已有身份/公钥、完整有界活跃与 cleanup/Stop 库存、唯一 open/unlocked Issue、Issue preflight、真实不可变 bundle、容量/配额/价格/预算 Quote、Acquire 前精确 selector 等合同。真实 Quote 必须适配新 stop 减 committed，不能用离线 TEST ONLY plan 替代。失败停止；未知 dispatch 先对账，不再次盲派；不自动修 IAM、增预算、改 source 或重试运行期正确性失败。 |
| 清理责任 | 原 selector、observed_at 和 12 个清理 scope 与留存 zero-inventory.json 一致；旧于 2026-10-21T17:43:09.4025176Z 精确清零。此为历史证明，不是本轮新库存扫描。新阶段仍由原 finalizer/sweeper/Stop/Release 清理并保留精确零库存，不能用“已请求释放”代替完成。 |
| 9 项离线 CLI 用例 | 没有重跑：逐项读回原 stdout/stderr、exit、保存的 TEST ONLY plan 与检查结果，均一致。成功 formal 使用合成 transition，只证明结构传递；固定测试公钥、假 request/bundle、过期时间不构成身份、Quote、Lease 或转移授权。初次 Go 版本构建失败原件保留，不冒称九项用例验证了云准入。 |
| MCP 与历史问题 | 前两次 live Analysis 未连通，原因未明，原授权关闭；新计划没有保证下次可用或暗含第三次尝试授权。#1024 改善采证和相关本地 helper，并未证明或修复历史 message_loss 根因；仍需这次新运行的真实证据。 |
| 路径适用性 | 本方案沿用正式 RC Actions，不是 Direct Lab。精确源码下 Direct Lab Skill 的 CNY300/whole-CNY 规则属于另一入口，其运行只产生诊断证据；此计划没有借该 Skill 绕过正式授权或将诊断结果冒充正式验收。 |

## 源码核对位置

- `AGENTS.md`：冻结适用规则，明确运行证据和正式 native 包发布要求；本次不修改源码/规则。
- `.github/workflows/README.md:1018–1285`：正式入口、单次启动授权、Issue/preflight、预算、续跑、阶段转移、清理合同。
- `.github/workflows/chat-lifecycle-rehearsal.yml:5–119`：六项输入、main、输入/预算/身份验证、活跃 Lease 检查、安全调度。
- `internal/usecase/chatlifecyclerun/run.go:325–365,472–504`：向下预算、固定 ¥150 reserve、同源同 bundle 转移、fresh prior/attempt 规则。
- `internal/usecase/cloudlease/control.go:42–56,817–840`：Quote 校验后的 selector；身份、币种、容量、配额、价格和剩余额度验证。
- `scripts/chat-lifecycle/stage-orchestrate.sh:54–76,560–621`：Issue 与 continuation 准入；materialize、真实 Quote、selector 先于 Acquire。
- `scripts/chat-lifecycle/authenticate-released-preclock.py:256–259,367,463`：拒绝已计时/运行期 predecessor。
- `cmd/wkcli/internal/benchmark/formal_chain_command.go:163–236`：运行时预算与阶段剩余时间合同。
- 两个 `configs/cloud/chat-lifecycle/*-v1.json` 与 `configs/wkbench/chat-lifecycle/formal.yaml`：资源、时长和产品阈值。
- 精确 `.agents/skills/wukongim-chat-lifecycle/SKILL.md`：仅用于核对路由区别，本次未执行该 Skill 的云操作。

## 证据与限制

本目录 `verification.json` 记录 46 项本地一致性检查，全部通过；`source-freeze.json`、`evidence-freeze.json` 记录精确来源及 digest，`historical-quote-recalculation.json` 与 `offline-case-readback.json` 提供有限可复核数值。原计划 41 个 manifest payload 全部匹配，原 allowlist 无额外未列 payload，六份原公开证据及八份上一代审批材料哈希未改。

本轮第一次尝试本地读取合成 CI commit 时收到 Git exit 128（对象不在本地）。`first-inspection-limit.json` 保留此边界；没有 fetch，而是以已留存 Git API 对象和候选收据核对 CI tree。79a 与 f419 两个正式 Git 对象的同树关系在本地独立验证。GitHub 当前 main、云身份权限、实时价格库存配额、实际账单、未来 MCP 与采证完整性仍未验证，不因本次零发现而视为通过。

只读评审没有运行任何 materialize/测试/构建、GitHub 或 provider 工具，没有读私钥、创建 Issue/key/request、操作云资源、dispatch/rerun CI，也没有修改原计划和源码。正式版发布仍取决于完整云验收及 applicable signed native channel 的精确版本公开客户端验证；本评审不宣告这些要求完成。
