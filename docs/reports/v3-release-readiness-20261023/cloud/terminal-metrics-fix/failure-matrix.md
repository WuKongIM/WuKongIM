# Terminal metric name repair: failure inventory before tests/code

Scope: existing terminal-evidence fixed selectors and their fixture. No runtime, cloud, workflow, policy or skill modification. Approved seams are collect_ranges JSON transformation and the existing collector CLI fixture.

1. Registered standard go_goroutines, go_memstats_* and scheduler/GC histograms fail to match invented wukongim_go_*: assert real metric names retain exact matrix bytes in the go partition.
2. Registered channel(v2)_meta_created_total and activation_rejected_total fail to match fabricated root-level names: assert both registered/promoted families exactly once in counters; reject suffix/near-miss families.
3. Standard process_cpu_seconds_total/start_time_seconds/resident_memory_bytes are omitted: assert standard process resource families coexist with old independent wukongim_process_* in the same single partition.
4. Broadening/counter prefix ambiguity may duplicate runtime/channel-worker or include node/other/high-identity families: assert exact family ownership, unchanged names/order of 8 partitions, and independent near-miss exclusions.
5. A fixture that invents exporter names can validate a wrong selector: replace fabricated NAMES with actual names and add mixed real-registration matrices; fixture query selection obeys real emitted selector rather than blindly supplying desired bytes.
6. Empty results remain evidence of no observed coverage, never numeric zero: preserve collected state with series/sample_count=0 and null observed bounds for both go/counters.
7. Process-level capture must retain actual resource matrices and original terminal report hash/cut; verify 8 actual calls, 15s step and shared 60s/24MiB/4MiB envelopes. This is existing Go integration tier; coordinate quiet window before process run.
8. Wrong-family/partial/oversized/time-bound invalid results must stay invalid/incomplete; old validated sibling partitions survive. Retain original integration negative matrix and original known empty semantics.
9. Standard process and Go resource series are low-cardinality exporter families; do not widen other 5 partitions or add UID/channel/request label matching.

TDD sequence: write failing transformation/selector regression first and record RED against unchanged production helper; write fixture/process regressions before helper change; execute actual process RED only in parent-approved quiet window. Then minimally change the three selectors and update operator knowledge/Unreleased. Repeat relevant focused static and existing integration suites to GREEN. No commit/push/PR in this subtask.
