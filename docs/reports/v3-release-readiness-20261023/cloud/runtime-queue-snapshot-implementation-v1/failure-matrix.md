# Tests-first matrix

All tests freeze before production changes. This is a deterministic collector transaction contract: the registry calls Metric.Write after Collect; process E2E and stress loops cannot deterministically position that boundary. In-memory real Gather and promhttp HTTP suffice; no TCP/listeners/sleeps, production hooks or workload are needed.

1. Gather old valid depth8/cap10/bytes80/bytecap100; after the depth Metric.Write captures8, publish valid1/1/10/10. Require the complete old tuple, not mixed8/1. Test unversioned and revisioned public setters independently.
2. Real promhttp HTTP handler at the identical boundary: HTTP200, complete labels/families, old coherent tuple; subsequent ordinary Gather sees the new tuple. No pressure clamps.
3. First binding during Collect: after depth-family collection, publish a second logical queue. Require all4 families to expose the same complete series set. The new collector snapshots complete series membership before emission.
4. Setter compatibility controls: individual setters preserve other fields, negative clamps unchanged, zero capacity retained, revision0 unconditional, stale revision rejected after unconditional update, nil receiver behavior unchanged.
5. Existing warmed-series zero-allocation control plus new all-setter warmed allocation control, including increasing revisions; no new labels/history on warmed updates.
6. Existing target missing-key, true over-capacity, nonfinite/overflow and cardinality tests remain rejection controls. Existing app runtime-pressure and transport aggregation tests exercise wiring. Channel worker, Slot scheduler and parser/report/gates stay byte-identical.

RED must compile and exercise the old mutable Gauge behavior; missing-seam/compile failures are not substitutes. First failure is retained; no rerun-to-green without source correction. Coherence bug is independently reproducible, not proof of this run's missing response or presence expiry cause.
