# Downward budget review — initial frozen implementation

Standards: 0 additional findings. Spec: 1 P2, open at this freeze. The source remains bound to freeze-v1; later repair and review do not replace this result.

## Standards

Standards 轴未发现额外 P1/P2；这不是整体通过结论。Spec 轴已确认的 finalizer 零资源证明上传 P2 仍需修复，在合并审查中只计一次。

审查固定 base `ff0aa096fe1bd125be70fc1844b2f45cc7f0115e`、freeze-v1 全部 27 文件及 patch SHA `ec2898d3d40d96f304e9e7a0d3b9278f428064d333b5167bd3f2e48be31fcfba`。独立重算文件哈希、改动集合均一致，逐项保留于 scope.json。已读根 AGENTS、适用 package-scope FLOW、Workflow 合同和 policy；code-review 气味清单仅作启发，未提升为硬约束。

- **规则与权限：** Review Agent policy、Skill 测试目录表、bundle/activation/deploy、三份模板均与 base 相同；未发现 Issue/Review Agent 保护控制越权。新参数只向下派生当前请求预算，默认 1500/1350、既有 schema/分层保留，没有新增 provider、跨请求 ledger 或付费权限。
- **tests-first：** 42 项先行失败清单，以及 Go、helper/stage、Receipt 类型、consumer 红收据均保留；后续相关四 Go 包及聚焦 helper/authority/activation/consumer 绿收据可核验。Go 首轮包含一个新 API 缺失的编译失败，不是全部行为型红测。真实 shell/进程测试归 integration，默认层保留纯合同/解析。未重新运行测试，也未宣称全部 named checks 完成。
- **文档与维护：** Unreleased、PROJECT_KNOWLEDGE、两份 FLOW 和索引同步预算边界。先验证模板再局部派生预算；独立信任边界重复校验有用途，未发现应阻断的过度泛化。

**已知 P2 的测试限制：** `scripts/chat_lifecycle_downward_budget_consumers_test.go:50` 固定断言 upload 的原始 if，不能证明前序步骤失败后仍上传。新预算拒绝在 rehearsal finalizer:450 / formal finalizer:431，后续 upload:518 / :456 缺显式状态函数。应与 Spec 修复一起纠正合同测试；现有绿测不能证明 C11。此项是可执行 Workflow 合同缺口，非代码气味判断；本轴未重复其 probe。

旧产品 `fc18221282f289088ec9b4282fda8e0bfc67a3b8` native guard 与 base 字节相同，仍拒 lower；只改 control 无法解决。新候选尚无 Linux CI/云验收，本审查不是新付费启动批准。只读仓库，仅写本目录收据；未改源码、跑测试、调用 API、派发、云操作或 merge。


## Spec

Spec：1 项 P2。低预算拒绝后，零库存证明上传会被隐含 success() 跳过：rehearsal transition 在累计费用达到 S 时 exit1，formal complete 在累计超过 H 时 exit1，后续上传条件均无状态函数。违反设计“失败不能阻止已有Lease Release/zero”和 C11；现有消费者测试只证本地 zero 文件/输出存在，未测 Actions 失败后的上传资格。建议两处上传改为 always() && steps.cleanup.outputs.zero == 'true'，保持预算拒绝。未发现范围扩大或其它预算传递错误。27文件匹配冻结；31项轻量探针符合预期。named checks 尚由 root 收尾，新产品候选及真实云验收未证明，旧 fc182 不支持降低预算。

