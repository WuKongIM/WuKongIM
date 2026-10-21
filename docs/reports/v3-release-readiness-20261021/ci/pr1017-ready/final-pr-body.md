## Summary / 变更摘要

The local three-node shakeout admitted 2,500 users at 100 SEND/s, but mandatory startup activity could not drain before the eligibility deadlines. Raise its local default to 500 SEND/s with burst 1,000, while retaining the users, startup obligations, deadlines, and latency/correctness thresholds. The regression exercises the real worker factory and grant allocator for all three workers across 360 virtual seconds.

Fix Bash 5 cleanup paths whose bare successful returns could skip Analysis close, local cleanup, or a child join while handling a prior failure. Preserve the original command/operator exit status. Make the process fixtures portable: reuse compilation cache while proving a fresh link and exact VCS revision, request text metadata despite inherited Go JSON flags, and use fixed-string grep. Signal tests now send TERM from Go after a ready handshake; they retain the real production join, live-at-signal/dead-after-join assertions, and cleanup of their own process group. No Go product runtime changed.

Add an independent full scripts-integration CI job using the frozen base's named-check policy. The original nine-minute package and 600-second outer limits remain unchanged, with no filtering or automatic retry. Default tests and migration/messaging E2E run independently. Also correct a pre-existing test-only sync.Pool copy found by vet.

## Release notes / 发布说明

- [x] User-visible changes are recorded under CHANGELOG.md Unreleased.

## Verification / 验证

All four workflows passed on the first attempt for `ab75c226f4b3c246797aee4f876f23af602d07ce`; actual checkout `7204d1ba4a5fe101397c85545e84df15e28e0082` has the identical tree.

- [Candidate Linux gate](https://github.com/WuKongIM/WuKongIM/actions/runs/39575183132): 219 default-test packages passed; full scripts check passed in 545.528 seconds with 602 top-level passes, six explicit skips and no unfinished tests. Both signal fixtures passed three attempts; all seven process-supervision failure controls passed. Output was complete and owned-process cleanup verified.
- The same gate passed all five migration topologies, four cross-node functional cases, and single-node/three-node self-chat restart/history tests. The existing opt-in 2,000-QPS E2E case remained skipped.
- [Three-node regression](https://github.com/WuKongIM/WuKongIM/actions/runs/39575183129): all three 500-QPS profiles, correctness, and focused unit/race checks passed.
- [Native packages](https://github.com/WuKongIM/WuKongIM/actions/runs/39575183181): build/install and Ubuntu 24.04, Debian 12, Rocky 9 and Alma 9 lifecycle passed. [Released SDK acceptance](https://github.com/WuKongIM/WuKongIM/actions/runs/39575183191): Android, iOS and Flutter passed.
- Test-first startup and cleanup regressions, final Bash 3/5 process tests, and independent review passed. Named policy, product join implementations and performance thresholds were not relaxed.

Earlier timeouts and assertion failures remain in the [readiness evidence](https://github.com/WuKongIM/WuKongIM/tree/codex/v3-rc-qualification/docs/reports/v3-release-readiness-20261021). Linux tracing located the current 255 at the fixture-only signaler wait after a successful production join; it does not independently identify the historical observer failure or an exact upstream Bash patch.

The local helper completed an operator stop with 1,753.104 measured seconds, clean process/port cleanup and no recorded correctness or under-delivery errors. Its two retries, 102 hot samples over 400 ms, presence warnings and shutdown warnings remain explicit. This PR does not establish the planned cloud soak or publish a release.
