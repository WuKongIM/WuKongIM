## Standards

# Standards 审查

本轮 0 硬规则违规、0 可行动代码味发现。已评估全部 12 项启发式。

四文件、固定 HEAD、完整 diff 和冻结摘要一致；无 staged/untracked 漏项，无适用子 AGENTS/FLOW。信号延后至完整登记后处理，沿用原 PID 清理范围；英文约束注释、Changelog 和知识记录符合根规则。

9 个进程回归均在 helper 修改前首红，原测试字节未变；integration 分层和真实请求边界符合 AGENTS.md 测试政策。首绿为 15 顶层测试、38 PASS。9 案均退出 0、请求归零、子进程消失、无临时文件；只读核查 6 个首红进程组已不存在。

原 CI 失败保留。未重跑测试、CI 或修改源码；本地结果不证明完整 Linux 门禁或发布通过。源码、规则及终态见绑定文件。

## Spec

Spec：0 发现；未见需求遗漏、错误实现或范围扩大。

合同要求“partial/interrupted 正常退出、全部请求结束、无存活子进程及 .next 文件”（修复计划:7–13；原 interrupt 测试:392–443）。helper:398–408 覆盖启动至完整登记；332–349 延迟信号且不改 $!，随后立即复用原取消/join；312、325 覆盖 Bash3 空数组。

独立核验九例完整测试早于 helper 改动，6+3 首红保留；首绿15根/38 PASS、0 skip，九例请求/子进程/临时文件均清零。原测试正文逐字未变，四文件及规则 hash 前后一致，staged/untracked 为空。

未读另一轴、未复跑或改源码。该机制复现不证明原 Linux 失败的精确指令位置；新 head 首次完整 Linux CI 仍待执行。

Standards: 0 findings; Spec: 0 findings. Full Linux validation remains pending.
