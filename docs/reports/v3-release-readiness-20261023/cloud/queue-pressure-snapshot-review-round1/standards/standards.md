# Standards review

Fixed scope: `364253b3be532acb023757791a54b85d4c76cbd2...b47c03d461a6d8918e537e027bf56539ce78c585`; tree `7ce68e726fea61bd6d176fbc7053dede920d5b00`. Worktree clean before and after review.

**Hard documented-rule violations: 0. Fowler-smell judgement findings: 0.**

`runtime_pressure_queue_collector.go:31-74` copies fully bound membership under the cache lock, releases that lock before taking any per-series publication lock, freezes four values into immutable ConstMetrics, then unlocks before sending output. The existing publisher path also releases its cache lock before publication. This avoids lock inversion and consumer backpressure while holding a publication lock. Its temporary memory is proportional to existing series and scrape output; it adds no independent label history or goroutine. This addresses `AGENTS.md`'s contention, memory, bounds and backpressure requirements.

`runtime_pressure.go:198-291` puts unversioned, revisioned and individual setters under the same publication lock while retaining previous revision-zero, stale-revision, clamp and individual-update behavior. Warmed producer code adds no allocation operation; scrape-time ConstMetric allocation is explicit. Reusing each Gauge descriptor preserves names, help, constant labels and the original four normalized variable labels, consistent with `internal/app/FLOW.md`'s bounded observability invariant. The pinned Prometheus Gauge.Write/NewConstMetric implementation was inspected locally.

Critical comments explain ownership and serialization constraints. Changelog and knowledge describe the behavior and its attribution limit, meeting `AGENTS.md`'s documentation rules; no applicable pkg/metrics FLOW exists. The deterministic Gather/HTTP-recorder and membership tests exercise the real Collect/Write split without TCP listeners or sleeps, fitting the fast default-tier rule. Tests-first pins and finite author receipts were hash-verified.

All twelve Fowler heuristics were considered as judgement calls, with repository rules taking precedence; no material smell was found. Tests, builds and checks were not rerun; no source, product, GitHub or cloud state changed. This review does not attribute the unretained scrape or message loss, or grant workload/Linux acceptance.
