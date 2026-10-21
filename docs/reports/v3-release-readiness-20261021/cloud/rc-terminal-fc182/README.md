# 本次正式流程首失败与精确清零

唯一请求 `chat-20261021T095421Z-cf11d65c`，source/control `fc18221282f289088ec9b4282fda8e0bfc67a3b8`，top run `39581355444` attempt 1 于 10:19:39Z 结束为 failure。Bundle、Quote、付费 Acquire、Deployment 均通过；未取得 run-start 或实际 2h 测量证据，未进入新 Lease 的正式长稳。

Release `39583589120` 返回 10:19:19.689029905Z 的 typed zero_inventory：selector 六字段、账户摘要与 Acquire 完全相符，12 scopes 与固定 provider 源码一致。两份 top artifact（handoff 名称与 cleanup 名称）API SHA/size/CRC 均已验证，26 个成员逐字相同；零库存与独立 Release artifact 逐字一致。完整验证在 `evidence/verification.json`。Acquire 收据证明付费创建确实发生，Quote 74.4776 元是 12h 保守报价，实际账单尚未读取。

首个公开运行提示为 reset-failed 报 unit 未加载；源码明确容忍该命令失败，不能当作根因。部署后进入准备检查，typed pending 中的 stage_readiness_timeout 是 start 成功后预设字段。配置是 7200 秒，约 18 秒后已 dispatch Release；terminal / unreachable / deadline 共用该字段，原 artifact 没有服务状态、journal、pre-clock terminal summary。初始准备失败原因仍未知。

触发释放的已证错误为 `pre-clock readiness repair state could not be published; exact Lease was released`，exit 1。`stage-orchestrate.sh:324–352` 写 pending 后三次调用 Issue comment；`781–783` 在其失败后精确释放。最终 Issue step exit 4，无 stderr。当前 exact Issue search 返回零项（完整响应），在其公开 JSON 上重放 `comment-request-issue.sh:20–23` 原 jq 表达式得到 exit 4。该现状复现不是运行时历史搜索响应的替代证据。

`top-log.redacted.txt.gz` 保留完整 1003 行脱敏日志，不截取为成功摘要；原 213058 bytes 日志只在内存处理，原 SHA、脱敏 SHA 与采集命令均留存。没有私钥或解密内容，opaque encrypted-access JSON 原样保留。SSH 配置及 known_hosts 仅留成员 hash，不归档路径内容；二进制和 bundle ZIP 不在此档案。检查器依赖本机原两份小 ZIP 才能重新验证 API 原始 archive hash；本档案可独立重算各文件 SHA。没有发起任何额外 workflow、重试采购、修复 trailer 或变更 source/预算/权限。

固定源码映射：

- `source/scripts/chat-lifecycle/stage-orchestrate.sh:669`：reset-failed 明确容忍失败；672 预赋 readiness code；760–767 不同提前退出路径；781–783 发布失败后释放。
- `source/scripts/chat-lifecycle/comment-request-issue.sh:20`：要求 exact-one Issue 搜索结果。
- `source/internal/usecase/cloudlease/types.go:358`：zero proof 的 point-in-time / exact selector 语义。
- `source/internal/infra/cloudlease/alibaba/lifecycle.go:889`：固定 12 类 scope。

当前请求已清零，本档案不能作为预演或 72h 正式验收通过证据。后续先诊断并评审修复；没有自动重跑。
