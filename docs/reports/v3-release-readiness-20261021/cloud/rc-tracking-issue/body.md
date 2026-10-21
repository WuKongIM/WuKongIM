本 Issue 记录已获用户明确授权的 v3 云 RC 请求；是在首次运行结束后补建的历史跟踪记录。

- Request：`chat-20261021T095421Z-cf11d65c`
- 精确 source/control：`fc18221282f289088ec9b4282fda8e0bfc67a3b8`
- 已批准范围：杭州四台 4核8G；2小时预演释放后，以新租约进行72小时长稳、最多8小时容量和30分钟恢复；请求累计预算上限 ¥1,500、估算累计 ¥1,350 停止。
- 唯一顶层运行：[39581355444 / attempt 1](https://github.com/WuKongIM/WuKongIM/actions/runs/39581355444)，2026-10-21 10:19:39 UTC 以 failure 结束。

Bundle、Quote、Acquire、Deployment 均首次成功。部署于10:17:40 UTC证明三个节点、256 healthy leaders/replica sets、12 logical groups、零 pending Controller tasks、三个 worker 和七个监控目标 ready。之后的负载准备检查退出；没有 `run-start.json` 或实际两小时预演通过证据，也未进入正式长稳。

已确认启动准备遗漏：运行前没有创建这个 exact tracking Issue。失败后修复状态无法发布，流程随即释放租约。当前 exact 搜索为零、原 jq 在该响应上离线重放 exit 4，支持该准备缺陷；当前搜索不是运行时历史响应。更早的准备检查为何退出仍未知：`stage_readiness_timeout` 是预设通用字段，7200秒窗口仅约18秒后即进入释放，不能据此认定超时或产品性能失败。

[Release 39583589120](https://github.com/WuKongIM/WuKongIM/actions/runs/39583589120) 首次成功；10:19:19.689029905 UTC 的 typed `zero_inventory` 已验证，selector六字段、账户摘要及12类资源范围均匹配本次 Acquire。两份顶层清理 artifact 的26个成员逐字一致，无实际远端 handoff。¥74.4776 是12小时保守 Quote，不是实际账单；实际账单尚未取得。

后续：补采购前只读 Issue 门禁和终止前有界诊断，并完成独立评审及回归。当前没有发起额外采购、云重跑或更改 source/预算/权限；本 Issue 不代表新的付费运行授权，也不代表 v3 RC 验收通过。

完整公开证据：[首次失败、日志及清零证明](https://github.com/WuKongIM/WuKongIM/blob/88c1d311d5604e77215c1e2275503f76c066d769/docs/reports/v3-release-readiness-20261021/cloud/rc-terminal-fc182/README.md)；[发布复核](https://github.com/WuKongIM/WuKongIM/blob/88c1d311d5604e77215c1e2275503f76c066d769/docs/reports/2026-10-21-v3-release-readiness.md)。
