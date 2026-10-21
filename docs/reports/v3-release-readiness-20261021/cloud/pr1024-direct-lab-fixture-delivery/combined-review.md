## Standards

Standards review: **0 documented-rule violations; 0 actionable heuristic findings.** Scope is the three frozen files at `44de9452d94edae16129b5781d794f8c1c6c7c57`; HEAD/tree, freeze digest, all source hashes, empty staged diff, empty commit list and the single untracked integration file were independently verified.

The real Bash/SSH-key fixture now lives in `scripts/chat_lifecycle_direct_lab_start_integration_test.go:1–13`, satisfying `AGENTS.md:133–139`. Its existing `repoRoot(t)` uses the unchanged bounded integration scheduler. The fail-if-called tool at lines 18–24 and the two environment inputs at lines 84–85 reuse existing seams; an unexpected invocation also breaks the unchanged exact call trace at lines 103–104. Removing only those additions reproduces the original function byte-for-byte, including every permission, state and budget assertion. The remaining default file is byte-identical after removing that function.

The frozen failure matrix and scratch source precede the retained first RED/GREEN evidence (`AGENTS.md:129–132,167–169`). Both scratch runs passed original assertions; baseline recorded two successful unnecessary builds and failed its added postcondition, while the single-input variant recorded none and passed. Actual focused receipts confirm 11 default and 15 integration passes, without failures/skips; target discovery is zero/default and one/integration. Six owned process groups have successful cleanup receipts and no remaining members in an independent read-only census.

All 12 requested smell heuristics were assessed as judgment calls, with repository rules taking precedence. The narrow stub and tier move warrant no new abstraction (`AGENTS.md:86–91`); the knowledge entry documents the boundary (`AGENTS.md:174–175`). No applicable FLOW exists for these paths.

No tests, CI or cloud actions were run during review. The original Linux 600-second failure remains unresolved by this local evidence; no complete-suite recovery or causal speedup is claimed. Exact before/after hashes, evidence manifest and terminal state accompany this report.

## Spec

# Spec review — 0 findings

冻结 HEAD `44de9452d94edae16129b5781d794f8c1c6c7c57`，仅计划中的三文件变化；审前后哈希一致，staged、commit list 均空，未读取 Standards。

- **完整性**：计划 `plan-and-failure-matrix.md:7–11,27–30` 已满足。默认文件仅删除目标函数及分隔空行（108 行）；新文件 `scripts/chat_lifecycle_direct_lab_start_integration_test.go:13–128` 去除声明的 stub 和两条环境输入后，与原函数逐字相同。八操作顺序、产物 presence/private mode、active state 与预算断言完整保留。
- **入口与范围**：计划 `:15,25–26,31` 已满足。新文件 `:18–24,84–85` 仅注入原 `direct-lab.sh:325–330` 两个既有 seam；共同替身记录意外调用并失败，`:103–105` 精确顺序断言也会拒绝该记录。原脚本、调度器、policy、workflow 均未改。
- **证据**：独立核完 36 个归档 payload。相同 serial scratch 的原断言均通过，真实 Go 调用从两次成功 build 降到零；A 首红保留，B 首绿；JSON 仅两项时间戳归一化，其他内容与文件模式一致。计划 `:29–33` 的默认 list 0、integration list 1、首次相关默认 11 / integration 15 PASS（目标一次）及六次零残留收据吻合。
- **边界**：计划 `:17,37` 与 README 均明确，1.889 秒只是单次本机 A/B 差值；不能外推 Linux、纯编译成本或证明 600 秒门禁恢复。未发现 missing、partial、unasked 或 wrong implementation。

本次仅静态比较和既有收据核验，未重跑测试、CI 或负载。审查脚本首轮把“含空行 108 行”误认成函数 108 行，已保留原错误并修正为函数 107 行；不是被审代码失败。

Standards: 0 findings; Spec: 0 findings.
