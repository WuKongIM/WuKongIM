# Incremental fixture review

Original c91 collector review remains separate; this reviews only the test fixture optimization.

## Standards

# Standards review — PR #1024 fixture optimization

No actionable findings: 0 documented-rule violations and 0 actionable smell judgments.

Scope is only `scripts/testdata/chat_lifecycle_terminal_evidence_test.py` relative to `c91e37b4e35e1009c9c5cb55ff351a38b870014f` (+110/−67). HEAD, status, untracked discovery, supplied freeze, and every frozen file digest match. The earlier seven-file implementation is outside this review.

The change retains the real collector and helper process boundary (lines 179–183), serial execution, actual NUL-delimited SSH command observations (lines 61–77, 129), requested byte clipping, nonzero partial-output behavior, and stdout/stderr/manifest secret-canary assertions. Paths remain quoted; commands are matched and parsed rather than evaluated. Boundary fixtures derive timestamps independently and require the actual query bounds before returning bytes (lines 91–124).

Applicable rules are root `AGENTS.md` (test-first, directly related validation, process-test integration tier, bounded design), `CONTEXT.md`, and `.github/workflows/README.md` (V3 candidate regression evidence and unchanged original limits). No applicable ancestor FLOW or child AGENTS exists. The failure matrix and unchanged pre-existing tests support the test-first requirement; all 20 test methods and the remaining test/assertion tail are byte-identical. No production configuration, policy, workflow, or concurrency changes occur.

All 12 supplied smell heuristics were assessed as judgments, with repository rules taking precedence. The small shell response adapter and Python actual-command decoder serve distinct responsibilities; this bounded seam does not justify additional abstraction.

Evidence inspected, without execution: the first focused JSONL records both related tests passing; the retained comparison records 79/79 complete case receipts equal, with both input digests verified and only observation timestamps/derived manifest hashes normalized. The original Linux receipt still records a 600.108-second timeout. This local focused result does **not** establish recovery of the original full Linux named check. No tests, CI, collector probes, source edits, or cloud actions were performed during this review.


## Spec

Spec 发现：0。缺失/部分要求、额外范围、实现错误均未发现。

- 计划第 23–25 行要求记录实际命令并拒绝错误边界。fixture 第 61–77、121–156、187 行从真实 NUL 命令记录重建 calls，按实际请求裁剪；15 项独立 adapter 探针确认错 start/end/step、未知 query 拒绝，stage/family 分派、partial、timeout、missing、stderr canary 保持。
- 计划第 26 行要求原方法/输入/断言逐字节不变：独立核验全部 20 方法一致；仅该文件 110 增/67 删，无 untracked，collector、Go 入口及规则哈希未变。
- 按计划第 30 行，独立比较 79 场景，先验证双方原 manifest 字节数/SHA，再仅归一 observed_at 及其派生 SHA，其余完全相等。已读首次 focused 两入口通过收据。

源码前后均为 3db0a415…ecc2b1。未读取另一轴、未改源码、未运行完整 fixture/full suite/CI。历史 Linux 超时仍保留，本复核不证明 Linux 门禁恢复。


Standards: 0 findings. Spec: 0 findings. Linux policy recovery remains unverified.
