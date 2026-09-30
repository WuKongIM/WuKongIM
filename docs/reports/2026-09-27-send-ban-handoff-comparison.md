# Admission-handoff repair: complete-metric comparison

R6 remains unverified after the fixed nine-attempt campaign: only one of three
fresh pairs completed both windows. R2's separate
5000/4500/30m run failed with EOF after 28m21s; a successful short comparison
must not replace that sustained qualification.

The original baseline lacked Raft-lane byte counters. An isolated baseline copy
now carries exactly the same passive observation implementation already present
in both fresh arms: priority-preserving byte events and fixed-label counters.
Only three production files differ from baseline revision `64f73d99b3`.
The old product behavior is retained; the additional observation overhead is
explicit and comparable to the other arms. Tests were ported and run red first,
then conn/core/metrics, app transport checks and focused race checks passed.
A real three-node 25-channel/500-per-second/10-second positive control completed
5000 messages, with pending=0 and 186,990 outbound Raft-lane payload bytes.

[Baseline preparation manifest](assets/send-ban-admission-handoff-20260927/baseline-observability/manifest.json)
retains context/source hashes, separated test/product patches, actual red/green,
race/build, binary identity and positive-control evidence. Existing missing-byte
historical runs remain missing; no total transport byte count is relabeled Raft.

Campaign arms are the baseline with observation parity, the repaired per-Slot
fresh reference, and the repaired node-batched implementation. All use the same
prebuilt harness, local Docker container (8 CPUs / 6 GiB), 256 hash Slots,
128 append workers and gateway batch cap 32. Each measured window is 5000
Channels, 1200 SEND/s and 60 seconds, after cold prime. The fixed order is:

`baseline, slot, node, node, baseline, slot, slot, node, baseline`.

Each arm runs three times, and fresh pairs are matched by their ordinal run.
Preserve every failure and require three complete fresh pairs within +5% for
both SENDACK P99 and aggregate CPU. Report throughput, P50/P95/P99, permission
RPC, Raft envelopes/bytes, CPU, allocations and GC. No compilation, extra product
probes or other task-owned load runs concurrently. External Docker accounting
records shared containers; it does not prove all host activity is isolated.

## All nine terminal results

| Attempt | Arm | Result | SENDACK P99 ms | Aggregate CPU % | Permission RPC | Raft payload bytes |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| 01 | baseline | PASS | 16 | 135.95 | 222148 | 1128622 |
| 02 | slot | FAIL (partial metrics) | 13 | 189.87 | 105441 | 23257156 |
| 03 | node | PASS | 14 | 185.71 | 163553 | 49014937 |
| 04 | node | PASS | 53 | 210.10 | 162658 | 48749828 |
| 05 | baseline | FAIL (partial metrics) | 4788 | 148.25 | 170995 | 1201189 |
| 06 | slot | FAIL (partial metrics) | 24 | 198.83 | 105433 | 23253183 |
| 07 | slot | PASS | 14 | 186.51 | 222223 | 48990229 |
| 08 | node | PASS | 13 | 174.70 | 163461 | 48970920 |
| 09 | baseline | PASS | 13 | 136.05 | 222168 | 1132166 |

Every successful window completed 72,000 messages. Attempts 02 and 06 stopped
at SEND 28.490 and 28.498 seconds, with four and seventeen immediate permission
busy responses respectively. Attempt 05 completed its 72,000 SEND calls but
failed while waiting for SENDACKs, with 1,386 messages pending. Its roughly
60-second elapsed field is the completed send-loop duration, not the wall-time
of the later timeout. Failed-window quantiles and counters are partial and
must not be compared as full windows.

Ordinal fresh pairs 02/03 and 06/04 are invalid because their per-Slot windows
failed. The only complete pair, 07/08, changed P99 by -7.14% and CPU by -6.33%;
permission RPC decreased from 222,223 to 163,461 (-26.44%). This one pair cannot
replace three complete pairs, nor may the three successful node-arm results
be selectively paired with passing historical references. R6 is not satisfied.
All complete/partial fields, allocations and GC remain in the raw summaries.

The 133 external Docker accounting samples had no collection errors and showed
the owned lab plus `buildx_buildkit_multiarch0` throughout. This membership
observation does not prove whole-host isolation. All measured windows reported
zero metric-sample errors, all nine attempts ended without a resource-guard stop,
and every run's process check retained only the container's idle sleep.

[Analysis](assets/send-ban-admission-handoff-20260927/comparison-01/analysis.json)
and lossless per-attempt JSON logs retain every result. The campaign's exit 0
means nine terminal attempts, not nine passing tests. The earlier sustained
EOF remains an independent unresolved root-cause investigation.
