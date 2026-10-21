## Standards

# Standards 审查

本轮 0 硬规则违规、0 可行动代码味发现。已评估全部 12 项启发式；仓库规则优先。

五文件及完整补丁与冻结一致，无 staged/untracked 漏项，无适用子 AGENTS/FLOW。删除固定诊断行、解析函数及 finally 赋值后，两个实现文件逐字节还原基线：命令、资源、600 秒限制、原 49 案及验收逻辑未变。

解析只保留原有界输出中的固定完整标记，最多 16 项，保序、保留重复并标记截断；不推断成功，APT 不报告 RPM 签验。

依据根 AGENTS.md 测试先行规则核验：9 个新方法首红 22 处，测试字节此后未变；首绿 9/9，默认 580 通过、1 原有跳过，合同 4 通过。四个验证命令均有终态回执。

未执行测试、CI、云操作或改源码。原超时原因仍未知，本轮不证明 native49 或发布通过。

## Spec

# Independent Spec review — PR #77 phase diagnostics

**0 findings:** no missing/partial requirement, unauthorized scope, or implementation error found in the five-file delta from `19ce29fb2233b3039e29c503963e27bd1fab972a`. No commits, staged changes or untracked files; all 121 other tracked files match HEAD. Before/after source hashes match the supplied freeze.

- **Protocol and phase boundaries:** spec `pr77-ci-generation-1/phase-diagnostics-plan.md:11,24–27` requires eight literal RPM events and six APT events. `scripts/validate-production-package-clients.py:1396–1423` places the applicable begin/complete markers around unchanged strict-shell commands. Independent generated-command comparison confirms all other arguments and the marker-stripped shell are byte-identical, including the 600-second sandbox limit.
- **Bounded, diagnostic-only receipt:** spec lines 44–46 require ordered duplicates, a 16-event cap, truthful truncation and original capture/error limits. `tests/native_stable_clients_drill.py:68–86,130–131` implements exact complete-line recognition and records only installed-client output in finalization. The original return, exception, process cleanup, size/hash/tail and acceptance logic remain unchanged. Independent AST comparison confirms the remaining driver and validator, including 49 scenarios and outer deadline, are unchanged.
- **Meaningful tests first:** spec lines 50–57 is covered by actual generated-shell failures (`tests/test_installed_cli_acceptance.py:88–121`) and actual subprocess noise/deadline/cap tests (`tests/test_native_stable_clients_drill.py:40–109`). Frozen first-red test bytes equal final bytes: nine methods/22 assertion failures against original implementation, then nine passing. Reviewed full-suite receipts report 581 tests with one existing skip and four contracts; these were not rerun.

Independent probes passed 14 parser assertions, both family command-equivalence checks, and one actual generated RPM shell whose dependency fixture emitted 6000 bytes without a newline before CLI exit 23. The summary retained checksum through CLI begin despite tail loss, preserved the original failure, and did not fabricate CLI completion. This was a controlled tool fixture, not native DNF or product execution.

Original Alma timeout subphase remains unknown; this change does not close that historical cause or establish a passing Linux 49-case gate. No full suite, CI, cloud, source mutation, or other review-axis report was used. All reviewer processes terminated.

Standards: 0 findings; Spec: 0 findings.
