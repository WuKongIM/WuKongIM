# Spec review

**1 finding: P2 — Complete the approved failure-matrix coverage.**

`internal/bench/chatlifecycle/finalize_diagnostics_test.go:76` and `:284` cover twelve controller failures and four ordinary coordinator cases, but omit several explicitly approved rejection/diagnostic seams. The spec says “then implement their tests against the frozen source” (`proposal.md:37`). Its matrix requires “io.Writer returns synthetic error or short write” (`failure-matrix.md:39`) and hook failures at “ordinary and continuous sites” (`:34`), plus nil receiver/context and unfrozen terminal (`:8`, `:13`). No corresponding diagnostic-writer failure/short-write fixture, continuous-finalize failure case, or nil/unfrozen guard classification assertion exists in the new or existing package tests. The filesystem obstruction case tests status/report output, not failure of `DiagnosticLog.Write`.

Add deterministic cases at those existing seams and verify the primary sentinel/private cause, bounded observations, unchanged fail-closed result, exact worker cleanup and absence of continuation. For diagnostic-log write errors/short writes, preserve best-effort logging: the original rejection and safe return must survive unchanged; do not introduce a new fail-closed gate. This is a missing acceptance requirement, **not evidence of a production behavior defect**. Passing broad suites do not establish the omitted contracts.

Other findings: no unasked scope expansion or incorrect production implementation identified. Static inspection confirms the four approved production files preserve validator/report preference, exit mapping, error precedence, cleanup limits and JSON-before-Markdown commit order. Fixed wrapper text hides causes while Unwrap preserves `errors.Is/As`; stage/reason and frozen observations stay bounded. CHANGELOG/knowledge additions match scope.

Scope: sole commit `73c93ac22b4a20189d7b74cf93efd9fa61b1601d`, base `2ab3e1a8bd95670603bc38314036c797a5124682`, tree `26067d613ec88d831af7b199f6fddb44fdb4511d`; clean worktree, eight changed files independently verified. Existing evidence was rehashed and parsed: 864 top-level passes, 1,604 pass events, zero failures/skips. Four behavioral RED/GREEN slices and two separately recorded fixture/build failures are retained.

Limits: read-only Spec review; no Go build/test, workload or cloud operation. Standards conclusions were not consulted. This diagnostic patch does not establish B128’s historical rejecting branch, a cloud message-loss fix, performance qualification or GA readiness.
