## Summary / 变更摘要

The local three-node shakeout admitted 2,500 users at 100 SEND/s, but its mandatory startup activity could not drain before the unchanged eligibility deadlines. This caused `offered_load_under_delivery` even with immediate fake ACKs. Raise the local default to 500 SEND/s with a two-second burst budget, preserving users, required activity, deadlines, and latency/correctness thresholds.

The regression exercises the real worker factory and grant allocator for all three workers and retains the historical 100 SEND/s failure. The helper also verifies default and overridden rendered rates. Add an independent Linux `scripts-integration` candidate job consuming the frozen base policy; fix an existing test-only `sync.Pool` copy caught by vet.

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

- Test-first budget and rendered-config regressions passed; independent review found no blocking issues.
- Local go-unit (219 packages), go-vet, go-format, go-mod-tidy, FLOW and workflow contracts passed. The later test-only pointer correction also passed focused test/vet checks.
- Both complete macOS scripts-integration attempts hit the original nine-minute package limit, at different active tests. These failures remain preserved; no timeout, filter, or retry policy was loosened. The new Linux job will run the exact frozen-policy check once.
- A fresh 30-minute native local shakeout on committed head `0672c115950f65e6d92381aa5b3ea9f0a20fc790` is in progress. Full sustained-load and Linux results are pending, so this PR remains draft.

[Diagnosis and original failures](https://github.com/WuKongIM/WuKongIM/tree/9a23014ba7e38d9a57770e317cf40fc21bd288cd/docs/reports/v3-release-readiness-20261021/cloud/local-shakeout-grant-diagnosis) · [Independent review](https://github.com/WuKongIM/WuKongIM/tree/9a23014ba7e38d9a57770e317cf40fc21bd288cd/docs/reports/v3-release-readiness-20261021/cloud/local-shakeout-repair-review)
