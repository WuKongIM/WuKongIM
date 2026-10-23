# Spec review — runtime queue snapshot

Fixed scope:364253b3be532acb023757791a54b85d4c76cbd2 →b47c03d461a6d8918e537e027bf56539ce78c585; tree7ce68e726fea61bd6d176fbc7053dede920d5b00.

**0 findings.** No missing requirement, unrequested behavior or incorrect implementation was found against the originating per-queue Gather-coherence contract and six-slice tests-first matrix.

- The new collector copies complete cached membership under seriesMu and releases it before acquiring each publication mutex. All four values become immutable ConstMetrics under that queue's mutex; output sends occur only after unlocking. Later Registry.Write cannot observe changed live Gauges. This is per-series coherence, not a global atomic cut across independent queues.
- Existing vector descriptors supply unchanged metric names/help, gauge type, constant labels and four variable labels in their original order. A newly bound series enters the cache only after all available children/publication state exist, so its four families share snapshot membership. Nil receiver and partial-vector guards remain valid; binding always creates the publication state.
- Unversioned, individual and revisioned setters share that mutex. Individual setters preserve other fields; revision0, stale-revision rejection, normalization, clamps and warmed cache ownership remain intact. Setter declarations, revisioned implementation, descriptors and cache/helper suffix were compared with base. The fixed collector contains no alternate mutable-Gauge output path or lock held during channel sends. No additional Fowler heuristic finding was identified.
- Frozen tests control the real Gather/promhttp Collect→Write boundary with no TCP, sleeps or production hook. Four versioned/unversioned serialization cases and whole-series membership reproduce the old defect; compatibility/allocation controls remain. Seven pre-production test hashes match. Author normal/race, app aggregation and named-check receipts were inspected, not rerun.

All five changed files match exact-head Git blobs. The supplied21-payload33,536B manifest was verified completely. Parser, report, Channel-worker and other tracked sources remain outside the diff; selected protected files were independently byte-checked. No applicable pkg or metrics FLOW exists to update. Checkout remained clean and head/tree fixed before/after. This reviewer executed no test/build/service/network/GitHub/cloud action and made no repository edits.

The actual invalid_queue_utilization response was not retained. This independently reproduced coherence fix neither attributes that missing response nor resolves presence expiry/message_loss or grants Linux/GA acceptance.
