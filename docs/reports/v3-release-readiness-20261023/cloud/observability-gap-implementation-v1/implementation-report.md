# Online Delivery observation patch

Fixed local commit: `a93837d3fb55369acfe2d131aff52914f55a867f`.
Base: `25a4765be7bed6696e1c9748142bdd61543b508c`.
Tree: `0f24d5dfbdb8de60789fbf8760d5c3a1b3c33344`.
Worktree: `.worktrees/v3-loss-observability`, branch
`codex/v3-loss-observability`; clean and not pushed. No PR or merge performed.

The two approved observation gaps are repaired. The app and Prometheus reason
allowlists now preserve the existing busy SENDACK as `system_busy`. The existing
runtime terminal callback copies MessageID and sequence as uint64 into its
observation; the existing incomplete-plan warning logs the same two numeric
identities. It retains exactly one warning per failed plan, and successful plans
remain quiet. No wire value, queue, timeout, retry, permission, routing, storage,
workload, or metric label name changed.

The diff has 12 files and 252 added lines: ten production lines, 233 test lines,
four CHANGELOG lines and five PROJECT_KNOWLEDGE lines. The patch SHA-256 is
`7f14186fb691be9ee36d8c660b2d776a7d89137fee6a02f35c3428cb643e1da7`.
All applicable AGENTS/FLOW inputs were frozen before changes. Existing FLOW
ownership and lifecycle statements remain accurate; no FLOW edit is needed.

Testing followed the approved seam matrix with retained RED then GREEN logs.
Busy first failed in the app classifier; after that narrow repair, it still
failed through the actual app-to-Prometheus observer and direct metrics seam,
then passed after the metrics allowlist repair. Runtime identity first failed
to compile because the DTO fields were absent; that is a compile RED, not a
runtime failure. Existing warning tests then failed specifically because the
numeric fields were missing and passed after the two field additions.

One test-fixture mistake is preserved: the initial metric-series privacy test
expected only one result series, while the existing metrics contract
pre-materializes seven terminal result partitions with zero values. The test
was corrected before the logging implementation to require seven existing
partitions, two error observations in the same series and no identity labels.
Both the initial and corrected RED logs remain separate. Individual RED source
snapshots were not frozen; the exact logs, explicit fixture correction and final
test/source patch are retained rather than claiming such snapshots exist.

The complete directly related default suites passed on Darwin arm64:

| Package | Top-level tests | Pass events including subtests | Package elapsed |
| --- | ---: | ---: | ---: |
| internal/app | 501 | 824 | 3.193 s |
| internal/runtime/delivery | 58 | 76 | 0.537 s |
| internal/access/gateway | 42 | 49 | 0.683 s |
| pkg/metrics | 70 | 70 | 1.872 s |

The machine-readable counts are in `test-results-summary.json`. All four
package results are pass, with 671 top-level tests, 1,019 pass events including
subtests and zero fail/skip events. `git diff --check` passed. The full raw suite JSON remains private and
is bound by size/hash in the summary; public focused events contain only test
name, package, action, time and duration. RED logs contain synthetic test
fixtures, not production recipient records.

No pressure workload or paid cloud operation ran during implementation.
Independent Standards/Spec review and final Linux CI are pending root action.
This observation patch does not repair the original cloud message expiry,
prove that the local failure has the same root cause, or certify GA readiness.
