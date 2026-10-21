# 已授权 fc182 正式云 RC：唯一请求准备

`request_id=chat-20261021T095421Z-cf11d65c`，`source_sha=fc18221282f289088ec9b4282fda8e0bfc67a3b8`。本目录只完成本地身份准备、精确源码审阅和 GitHub GET；没有provider调用、Quote、工作流调度/启用、Issue创建或权限修改。顶层root负责调度。人类授权由root明确转述，原问题与回答在父对话；authorization.json如实记录该来源，未伪造逐字用户文本。

授权范围为杭州4台4核8GiB，2h预演→释放→新Lease72h长稳/≤8h老化容量/30m恢复；整个请求累计¥1500上限、¥1350停止，包含临时公网SSH/HTTP、跟踪Issue、清理调度与最终释放。身份、库存或Quote失败即停止。不得自动修改RAM/IAM、DNS/CDN、Secrets、roles或追加预算。

## 已完成

- 09:54Z新public guard：main前后仍fc182；七OIDC变量全部非空且摘要与上一快照一致；wrapping public key同样一致；OIDC subject exact。没有Secret值读取。该检查是时点证据，工作流运行时变量改变仍必须停止，不能宣称已存在服务端verify-only开关。
- GitHub exact request Issue搜索0，`incomplete_results=false`；完整505项artifact快照中相关136项无ID冲突。没有创建跟踪Issue。
- 仅生成一次诊断Ed25519：目录 `/Users/tt/.wukongim-cloud-rc-identities/chat-20261021T095421Z-cf11d65c` 为0700，`diagnostic_ed25519` 为0600。公钥位于diagnostic.pub；`ssh-keygen -y`已证明对应，私钥未打印/归档，不在仓库或此证据目录。保留至本请求终态、诊断窗口与清理结束。
- 历史27份cleanup结构结果及精确selector原样引用到historical-selector-boundary.json；此前各结构检查通过，**不是当前provider zero证明或新的producer/handoff认证**。09:47完整分页未见retained handoff/transition/pending容器。正式入口仍运行原authenticated discovery；live库存须由授权的既有只读Observer/原入口确认。

## OIDC既有绑定验证：仅root执行

`oidc-existing-verification-dispatch.json` 是现有setup入口的精确输入：`ref=main`、`setup_id=verify-chat-20261021T095421Z-cf11d65c`、`operation=apply`、`force_reconcile=false`。

已核fc182源码：当且仅当 `operation=apply && force_reconcile!=true && configured_count==7`，reconcile步骤取 `use_existing=true`，仅将七个现有public binding写为本地JSON/outputs，**不执行wkcloudleaseoidc apply**。之后三独立job以短期OIDC交换各自角色，执行`wkcloudleaseoidc verify`；deployment job仅在Runner验证wrapping公私钥fingerprint匹配并always删除临时明文。

verify调用链只使用GetCallerIdentity、ListPoliciesForRole、GetPolicyVersion、GetRole读取：要求exact assumed role、唯一同名Custom policy及当前默认版本canonical document、完整expected workflow trust、MaxSessionDuration=3600和account hash相同。它不建立或更改RAM资源；此处尚未实际运行，不能报告live通过。

**条件边界：**同一setup workflow的else分支会借旧AK执行identity reconciler，因此不能将整个apply入口描述为无条件只读。root在调用前再次确认当前control==fc182、七字段非空且摘要未变；任一缺失/变化不调用，不改force，不重试身份修复。已有输入不得用于`remove`或不同operation。实际运行需核对checkout/control、reconcile快路径与所有四verify job成功后才继续。

依据（精确source）：[setup入口](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/.github/workflows/cloud-lease-oidc-setup.yml#L122)、[verify命令](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/cmd/wkcloudleaseoidc/main.go#L173)、[trust/session](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/internal/infra/cloudlease/alibaba/openapi.go#L337)、[sole policy](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/internal/infra/cloudlease/alibaba/openapi.go#L983)。source-manifest.json固定适用AGENTS/FLOW和读取源码摘要。

## 唯一付费顶层输入与依赖

`rehearsal-dispatch.json`已经填好全部四字段，没有占位符。root在身份验证/库存前置满足后，可向 `chat-lifecycle-rehearsal.yml` 的workflow_dispatch提交此JSON，且仅提交一次。顶层自身构建并绑定fc182 bundle，勿额外调度独立bundle或复用旧ed705产物。

顶层会再次拒绝未经authenticated zero proof的旧handoff，先启用现有finalizer/backstop，再执行Quote→exact admitted Acquire。当前没有Quote/当前云库存证据，不能提前标记通过。当前请求不允许source或bundle自动换版；代码中的protected-main修复trailer分支可重建candidate source，因此root不得为本请求生成repair trailer，若需产品变化则先终止/清零并重新授权。正式转移仍须复用实际预演source与bundle；所有control和run/attempt身份分别记录。

停止使用现有 `chat-lifecycle-stop.yml`（exact request、operator-stop-chat-lifecycle），不硬取消cleanup owner。成功启动不等于正式pass，完整72h/capacity/recovery、终端证据和两次zero proof仍按原契约判断。

本目录 `prepare.py` 是本次一次性准备记录，不要为复核再运行（它拒绝覆盖authorization，避免生成多个request/key）。复核读取JSON及SHA256SUMS即可。归档allowlist只有本目录public小文件，**不包含仓库外私钥**。后续运行收据放新目录/代次，保持本目录不变。
