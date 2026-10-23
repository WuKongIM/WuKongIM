## Summary / 变更摘要

Runtime queue gauges could mix two individually valid observations in one scrape because Prometheus serializes Gauge values after Collect. For example, changing a transport scheduler aggregate from depth/capacity `8/10` to `1/1` after depth serialization could expose `8/1`, causing strict metrics consumers to reject the scrape.

Register one collector for the four RuntimePressure queue families. It copies complete cached series before acquiring per-series publication locks, then constructs immutable ConstMetrics under the same lock used by all queue setters. Metric names, help, labels, negative clamps, revision rules and warmed zero-allocation publication behavior remain unchanged. Output drains after releasing the publication lock.

This repairs an independently reproduced metrics coherence defect. The actual failed response was not retained, so this does not establish its cause or fix the separate presence timeout/message loss. Parser/report/gate strictness, capacities, deadlines, Channel worker metrics and Slot scheduler behavior stay unchanged. The candidate remains **NO-GO for v3 GA** until the outstanding product failure and required acceptance evidence are resolved.

## Release notes / 发布说明

- [x] I added every user-visible change to `CHANGELOG.md` under `[Unreleased]`.
- [ ] This pull request has no user-visible change. I explained why below and a maintainer added the `skip-changelog` label.

## Verification / 验证

- All failure/control tests and their hashes were frozen before production changes. First real RED compiled and deterministically produced mixed `[8,1,10,10]` through Registry.Gather and the real promhttp HTTP handler, with both unversioned and revisioned setters. New-series membership also failed. The same frozen tests passed after the fix, retaining complete old `[8,10,80,100]` and then current `[1,1,10,10]` observations.
- The collector transaction boundary uses controlled Collect/Write ordering with no sleeps, TCP listeners or production hooks. This fast isolated contract is deterministic where a process E2E stress loop would depend on scheduling.
- Full pkg/metrics + target normal and race suites passed (292 parent/subtest pass events each); 15 focused app observation/transport aggregation tests passed in normal and race modes.
- Existing parser missing-key, true depth-over-capacity, nonfinite/overflow and cardinality rejection tests still pass. Existing and new warmed-publication allocation controls remain zero.
- Policy-named go-format, go-vet and go-mod-tidy passed with the original policy arguments. No FLOW or index changes were needed.

No runtime workload, cloud resource operation, database access, manual workflow dispatch or rerun was performed. This PR remains draft pending independent review and first automatic CI.
