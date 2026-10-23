# Runtime queue snapshot candidate

Draft PR: https://github.com/WuKongIM/WuKongIM/pull/1032
Head: b47c03d461a6d8918e537e027bf56539ce78c585
Tree: 7ce68e726fea61bd6d176fbc7053dede920d5b00
Base: 364253b3be532acb023757791a54b85d4c76cbd2 (tree1182)

Five changed paths: RuntimePressure publication locks, one 75-line immutable collector, one tests-first file, CHANGELOG and PROJECT_KNOWLEDGE. All other tracked files are unchanged, including Channel worker metrics, parser, report, thresholds, capacities and deadlines. No FLOW/index edits were necessary.

The complete failure/control matrix and seven test-source files froze before production changes. No compile/missing-seam RED was used: old code compiled and deterministically produced [8,1,10,10] from two individually-valid observations through real Gather and promhttp HTTP, both unversioned and revisioned. A new-series membership case also failed. First RED exit1/2.164s is preserved. Identical test hashes passed after correction (1.257s), retaining the complete old [8,10,80,100] snapshot, followed by the current [1,1,10,10]. Setter compatibility and warmed publication allocation controls passed before and after.

Metrics+target full normal/race passed:292 parent/subtest events each,3.465/8.192s. Fifteen app observability/transport aggregation tests passed normal/race:23.141/59.376s. Policy-named go-format, whole-repository go-vet, and go-mod-tidy passed with exact policy arguments. No parser rejection or gate was relaxed; true over-capacity/missing-key/nonfinite/overflow/cardinality controls remain passing rejection tests.

The collector copies fully bound entries under seriesMu, releases that lock, then freezes ConstMetrics under each publication mutex. All queue setters share that mutex; output sends happen after unlocking. Existing names/help/labels/clamps/revision semantics and zero-allocation warmed publication are preserved. The fast isolated contract controls Registry Collect/Write ordering without TCP, sleeps, stress loops or production hooks; an E2E scheduling race would be nondeterministic.

This independently reproduced coherence defect is not attribution of the missing actual response or the separate presence expiry/message_loss. Candidate remains NO-GO for GA pending product repairs and acceptance. No product workload, cloud, database, workflow dispatch/rerun, self-review or merge occurred. Root owns independent two-axis review and first automatic CI. The clean unmerged worktree/branch remains for review and post-merge cleanup.

Raw test/check logs are excluded from public payloads; only their fixed sizes/hashes are retained as metadata. No original production payload or profile was read or copied.
