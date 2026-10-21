# Spec review — migration-connect repair v2

**No actionable Spec findings; no scope creep.** Static review of exactly the three frozen working-copy files at HEAD/base `9a4900dc67ac674791edba11ea9a719cd91ec570`. Current diff bytes match the frozen v2 patch (`66520f2d1742d4512baf83b820fc9c0c5c5254eaa0395d0a1904a3fe9eba653a`); all three source hashes match.

The delegated specification requires “原首次after-import完整历史冷读取保持在前”, then only the existing node-1 readiness helper before retained-v2 Connect/Send, with “无新Connect/Send重试或产品readyz变化” and “仅failure取bounded diagnostics”. Scenario `AGENTS.md:22–31` requires complete history comparisons and preservation of the original credential assertion without business retries.

`ordinary_history_test.go:167–182` satisfies that order: every ingress completes the unchanged `after-import` comparison before `NodeProcess.WaitWKProtoReady` on node 1. The existing helper (`suite/readiness.go:31–99`) registers a separate `e2e-ready-1` token and proves a real authenticated handshake; the following business Connect still uses the retained `migrationalice` credential once, followed by the original Send once. Both new diagnostic calls are inside failure branches. No product readiness behavior changes.

The original three-minute scenario context, five-second client operation timeout, 20-second history deadlines and retry rules, exact history comparisons, ACK bindings, append/idempotency checks, restart-before-append ordering, and applicable leader failover remain unchanged.

**Validation remains incomplete.** No tests, source edits, CI or cloud actions were performed by this review. The supplied freeze records v1 focused success, v1 full-suite failure after restart, and unexecuted v2. The original Linux connection refusal remains undiagnosed, three local observations did not reproduce it, and the separate v1 1→3 restart HTTP 400 investigation cannot be counted as a full pass. This report establishes narrow spec conformance only.
