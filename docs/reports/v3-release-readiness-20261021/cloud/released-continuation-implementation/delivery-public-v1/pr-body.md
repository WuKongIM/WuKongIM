## Summary / 变更摘要

A released pre-clock attempt previously had no safe entry point to resume the same authorized request without resetting its cumulative cost. Add an explicit, once-only continuation to the rehearsal workflow: authenticate the adjacent same-workflow predecessor, its original Acquire/Release artifacts and exact 12-scope zero inventory, then carry the complete admitted Quote as a conservative reserve into a new attempt-2 Lease. The reserve is not an actual invoice, and the original unknown pre-clock cause remains unknown.

The continuation sidecar is reauthenticated by rehearsal/formal consumers and retained through finalization. Known terminal evidence and trusted retained same-request cleanup block a fresh zero-prior start. Invalid sidecars block new procurement or qualification while preserving cleanup of an already owned Lease. There is no automatic retry, provider or permission change, new ledger, or workload/threshold change.

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

Skip reason / 豁免理由：Not applicable.

## Verification / 验证

- Test-first real helper CLI/shell fixtures use synthetic, exact read-only GitHub API and ZIP evidence. The final focused suite passed with 85 test events and no failures/skips; independent authority and consumer reviews passed.
- Full `go-unit` passed against freeze-v2 in 453.555 seconds. The v3 delta only changes the Python helper and integration-tagged tests; its new failure cases were observed red before the fix and passed afterward. This is not a single-source full-suite first pass.
- Final v3 named `go-format`, `flow-doc-contracts`, `workflow-contracts`, `go-vet`, and `go-mod-tidy` passed. The unchanged Mac `scripts-integration` gate **failed on the fixed 9-minute whole-package timeout** (scripts 540.658s, wrapper 542.175s). The log has no assertion-failure lines; two existing baseline fixtures were active for 4s/5s and other fixtures were queued. The complete first-failure log is retained, owned-session cleanup was verified with zero remaining processes, and no retry or limit/filter change was made. This is not an all-gates-green result.
- All four changed workflows passed actionlint; shell syntax and whitespace checks passed.
- Linux first-run CI and live continuation authority remain unverified. No cloud, dispatch, or procurement action was performed during implementation. Original failures and their public receipts are preserved.
