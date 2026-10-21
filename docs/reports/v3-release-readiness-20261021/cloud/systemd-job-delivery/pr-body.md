## Summary / 变更摘要

修复云聊天生命周期启动时对 systemd 空 `Job=` 的误判。实际续跑中服务已是 `active/running`，启动与状态读取都成功，但旧分类器返回失败并在约 7 秒后停止了服务。现在将明确出现的空 Job 解释为没有排队任务；缺失、重复或畸形字段仍拒绝，Bash 3/5 使用一致的显式退出行为。

同时保留诊断中原始字段的摘要，并覆盖 uint32 十进制边界与无结尾换行的输入。状态策略、工作流、预算、清理和一次性续跑资格保持不变。关联 #1018；该请求已按授权停止并清零，本 PR 不再次采购资源。

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

全部新回归在实现前冻结并保留原失败。Bash 3/5 各 26 个 parser 场景与 11 个实际 readiness-loop 分支通过；真实故障前的 104 字节输入及 SHA 均保留。相关回归每个 shell 各 9 根用例、74 个 pass 事件，4 个 Darwin journal summary 子检查因既有 awk 限制跳过，不能计作 Linux 覆盖。语法、格式、diff 检查及原 policy 的 `flow-doc-contracts` 通过。

规范与行为两轴独立复核均无发现，独立的 56 个输入探针通过。精确提交的完整 Linux CI 将在提交后补充。本地回归不代表 2 小时预演或 72 小时长稳通过。
