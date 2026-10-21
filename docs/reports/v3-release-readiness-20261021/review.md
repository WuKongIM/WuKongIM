# V3 candidate CI 会话清理复核收据

审查日期：2026-10-21。审查方式：只读代码及已存探针证据；本次未运行测试、产品负载或工作流。

审查源为 `5f3ecdf93e762a1b1551b44977a44f2e63c97802`，工作区 clean。`ci/final/source-tree-identity.json` 记录 PR 1016 的 synthetic merge `0ab6ea4bad09c76dae8c71f8ca67c00d4e220b89` 与该 HEAD 的 tree 同为 `f030a2fdd558266bc8f3d87f516b9f73273df93a`。本收据只关闭此前提出的 SID/PGID 清理代码问题，不代替 Linux CI、完整候选验收或正式发布资格。

## 旧问题及修复依据

旧包装器只终止 driver 的 PGID，而 `test/e2e/suite/process_group_unix.go:12–17` 为产品节点设置独立 PGID。driver 失败、超时或取消而未执行测试 cleanup 时，节点可以存活。`ci/session-cleanup-probes/red-independent-pgid.json` 已记录 driver `91179` 被杀后，SID `91179` 内的独立 PGID/PID `91180` 仍存活。

当前 `scripts/run-v3-candidate-check.py` 已实现相应修复：

- 第 139–142 行为每个命令创建独立 session；第 19–50 行按 SID 枚举所有非 zombie 存活成员，不受节点单独 PGID 影响。
- 第 53–84 行只清理所拥有的 SID，拒绝 orchestrator 自己的 SID；逐次发送 TERM/KILL 前重新核对成员 SID，并重复枚举、记录最终剩余成员。枚举失败保留错误，不伪报零残留。
- 第 165–197 行在统一 finally 中屏蔽重复取消、清理 session、等待 driver、保存最终输出及原始命令状态。清理失败不能通过：成功命令改为 `cleanup_failed`，失败/超时/取消保留原命令结果，且 `passed=false`。
- `.github/workflows/v3-candidate-regression.yml:110–117,175–192` 的 frozen `go-unit`、一次构建及三个串行 E2E 均调用此 runner，原有测试执行路径实际接入修复。

## 本机证据

已读取 `ci/session-cleanup-probes/verify_owned_sessions.py`、各案例 child/result 及汇总 `green-owned-session-cases.json`。探针创建与 driver 同 SID、独立 PGID 且忽略 TERM 的 child；另有独立 SID sentinel 验证不误清理。正常退出案例 child 为 PID/PGID `92566`、SID `92565`，确实覆盖旧缺口。

| 探针 | 保留结果 | wrapper exit | 清理结论 |
| --- | --- | ---: | --- |
| 正常退出 | passed | 0 | verified/complete，零存活成员 |
| exit 7 | failed，原 exit 7 | 7 | verified/complete，零存活成员 |
| 超时 | timeout | 124 | verified/complete，零存活成员 |
| 输出超限 | output_limit，truncated | 125 | verified/complete，零存活成员 |
| TERM 取消 | interrupted | 143 | verified/complete，零存活成员 |
| 枚举故障 | cleanup_failed，command_status=passed | 1 | verified=false/complete=false，不伪通过 |
| caller SID | 明确拒绝清理 | — | verified=false/complete=false |

这些是 **macOS 本机 `ps + getsid` 路径**的既有探针收据，不能称为 Linux `/proc` 验证。代码审查确认 Linux 分支按 `/proc/<pid>/stat` 的 session 字段识别跨 PGID 成员；该分支的实际执行证据仍待 PR 1016 的 Linux CI 完成后独立确认。本次未查询或宣称其最新完成状态，也不将常规 CI 成功等同于已覆盖 Linux 全部故障注入分支。不可捕获的 hard kill/runner 丢失不由本机 TERM 探针证明，缺失最终收据仍属于未验证。

## 结论与证据摘要

此前 P2 问题在实际接入的代码与本机故障探针层面已关闭；本次限定复核未发现需要继续修改的同范围问题。Linux 实际清理结果 **PENDING**。

| 文件 | SHA-256 |
| --- | --- |
| `scripts/run-v3-candidate-check.py`（222 行） | `c650a3c828e074197520defe79b386b160f2465debc487acc1172f7216be05dc` |
| `.github/workflows/v3-candidate-regression.yml`（204 行） | `37a8998cfba6c3c0984eea6d27cd1e0d4563fa90adc585068ce97b8524397016` |
| `ci/session-cleanup-probes/green-owned-session-cases.json` | `c50e9a7da386b167f5b36d859168afcb4917dbf7fb9a7a35be23086ebe5c5273` |
