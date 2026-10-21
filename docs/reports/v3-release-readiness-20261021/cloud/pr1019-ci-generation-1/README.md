# PR #1019 首次自动 CI 证据

head `5403a3ba28f2a6846aba2bc238cc1d0b7ded349e`，实际 PR merge `33733629ced4bdd21efb68aaf5a94ec1b01dc6bd`，tree 同为 `0acb67250313fc81a463941295082c828a46124c`。四个自动 workflow 的 attempt 1 全部通过。

- Candidate 39587951020：default 377.310s；scripts 545.325s，613 根 pass、6 既有 skip、0 fail、0 unfinished，清理 complete。迁移五拓扑、跨节点和自聊通过。
- 本次关键的 `terminal_preflight`、`terminal_runtime`、`terminal_unknown_summary` 下 `summary_command_and_classifier` 在 Linux 各实际 pass，无 skip。
- Chat 39587950872：三类固定 500/s 压测各三窗通过；90s correctness 接受结果见 `local-step.json`，并非正式长稳。保留 retry_attempts=1、receive_ack_failures=1 以及 operator_stop 终态，不称所有计数为零。
- Native 39587950879：真实 DEB/RPM 与 checksums 一致，四发行版通过。SDK 39587950769 三项通过。

`candidate-summary.json` 含全部测试计数、原始日志摘要和 skip 名称；`chat-reports-summary.json` 与原始选定 JSON 保留性能与本地正确性结果。`logs/` 是 18 个实际执行 job 的完整原始日志压缩，JSON 侧车保留原字节 SHA。普通大文本仅 gzip 压缩，`ARCHIVE_ALLOWLIST.json` 映射压缩前路径、字节数及 SHA。所有下载的原 ZIP 已核 GitHub API SHA/size 与 ZIP CRC。归档不包含大型 ZIP、二进制或数据库；它们仍在父目录，可按 artifact ID 重新获取。

无手动 rerun、dispatch、PR 写入或云调用。现有 skip 均保留。此结果不证明原云端 pre-clock 根因，也不替代两小时预演或 72 小时正式验收。
