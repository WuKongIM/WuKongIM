# Standards review — migration-connect repair v2

No actionable documented-standard violations or baseline smell findings in this three-file patch.

Source: base/head `9a4900dc67ac674791edba11ea9a719cd91ec570`, working-copy patch SHA-256 `66520f2d1742d4512baf83b820fc9c0c5c5254eaa0395d0a1904a3fe9eba653a`. All three current file hashes and the complete `git diff HEAD` match the v2 freeze. This report is separate from the object-delivery review.

- `ordinary_history_test.go:170–174` reuses the existing process-aware, authenticated WKProto readiness helper after the original all-ingress imported-history checks. It keeps the E2E process/protocol boundary and reusable-harness placement required by `test/e2e/AGENTS.md` Core Rules and `migration/AGENTS.md`.
- `ordinary_history_test.go:178–184` preserves the original retained-v2 credential Connect and SEND assertions, without business retries. History comparisons, the existing retry policy, restart and failover assertions remain unchanged, as required by the baseline `ordinary_history/AGENTS.md`.
- Newly added diagnostics at lines 173 and 180 execute only on error. They reuse the existing bounded/redacted process-tail mechanism, satisfying the E2E rule “Keep failure diagnostics bounded.” The earlier eager-evaluation issue is absent from v2.
- The scenario instruction amendment records the readiness constraint without weakening the existing contract. The Unreleased changelog entry and English explanation are present. The twelve smell heuristics yielded no actionable judgment-call findings.

Validation remains outstanding, not passed: the v2 freeze says it was not executed; the recorded v1 focused run passed but its full run failed after restart. Root AGENTS requires directly related tests, and the scenario instructions require focused then full topology validation. This static review does not satisfy those execution requirements or diagnose the separate HTTP 400 failure.

No tests, implementation edits, cloud, CI or network actions were performed by this reviewer.

Standards: 0 findings; no worst issue. Required validation remains incomplete.
