# Issue #977: fixed independent-caller permission baseline

## Result

Two real-process runs against clean product revision
`424d03eb298b972ec572261d617972eecb5523c5` pass every count, ACK, completed
Channel ban/unban, topology and exact committed-history assertion. Independent
calls repeat their two mandatory UID/source-Channel facts, node envelopes and
per-physical-Slot barrier calls. This establishes work worth considering for a
bounded cohort; it does not establish a CPU cause or a throughput improvement.

The 32-caller windows in run 2 have the following results (64 SENDs each):

| Actual placement | Plans | Remote envelopes | Slot groups | Successful barrier calls | Client SENDACK p99 ms | Max ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| same-slot-remote | 64 | 64 | 64 | 64 | 471.125 | 471.125 |
| two-slots-one-remote-leader | 64 | 64 | 128 | 128 | 406.622 | 406.622 |
| two-remote-leaders | 64 | 128 | 128 | 128 | 363.774 | 363.774 |
| two-slots-local-leader | 64 | 0 | 128 | 128 | 388.315 | 388.315 |

Sequential windows retain exactly the same plan/envelope/group/barrier counts,
with maxima 23.009–28.141 ms in run 2. Each window plans 128 facts for the same
UID/Channel pair. There are zero measured admission-busy or failed barrier
samples, and each owner is drained after ACK completion. The local-leader case
uses 64 local envelopes. Counts are closed per-node deltas, rather than totals
from setup or inferred Raft wire traffic.

With only 64 latency samples, nearest-rank p99 equals max. These short,
unpaced, one-Channel bursts are diagnostic: they are **not** the existing
500 SEND/s acceptance workload. Several tails exceed 400 ms; do not turn this
into a passed capacity claim or relax the existing p99 gate. Even zero-RPC local
permission dispatch has a long burst tail, so this evidence does not isolate
permission transport as the sole tail cause.

## Fixed source and inputs

- Product: clean commit above; SHA-256
  `bc7b95c4d683dfe6300b18e1f28450ccfeb296a71516302c3943e72dca7939d8`;
  Go 1.25.11, darwin/arm64, E2E build tag. No product source/config-schema change.
- Harness file SHA-256:
  `bbe7237b6af8fa5b8e772517e312703b95f13d97adfcedc0885aeaaed381bce4`.
- Three processes, 256 Hash Slots, 12 physical metadata Slots, one metadata
  voter per Slot, explicitly three message-data replicas, node/driver
  GOMAXPROCS=4, existing permission TTL=1h, loopback debug API enabled.
- Existing system UID isolates two mandatory facts; 32 distinct device IDs
  have independent TCP sessions and at most one outstanding SEND each. An offline
  subscriber supports committed history without online recipient fanout.
- Four deterministic bounded key searches use actual Manager leadership and
  voters. Remote ingress is outside both metadata voter sets. Fingerprints
  match before and after every case. One-voter metadata proves routing/counts;
  it does not characterize replicated ReadIndex quorum cost or prove HA.
- No SEND retry. Warm/setup/writes/history/scrapes are outside latency windows;
  all measured ACKs join before the final metric cut. Receipt config hashes and
  overrides preserve exact rendered input identity, including ephemeral ports.

## Resources and profiles

The receipts retain each node's before/after CPU gauge, RSS, Go heap/allocation
counters and goroutines. In run 2, the 32-caller scrape cuts show 21.1–30.7 MB of
whole-cluster allocation growth, 415.6–497.9 MiB of final RSS, and summed latest
CPU gauges of 32.1–48.3 percent (100 percent represents one busy core). These
periodic CPU gauges are not simultaneous window integrals. Scrapes, background
work and cached Go memstats affect allocation cuts; these are not measured
permission-only bytes/message. Darwin omits the standard process CPU-seconds
counter. Queue-owned bytes have no public gauge and remain unknown, separate
from the documented 16 MiB/1024 waiting-envelope limits.

Run 1 captures one owner's two-second CPU and heap profiles in a separate
256-SEND phase after its latency windows. CPU has 470 ms of samples over 2.10 s,
mostly OS/runtime scheduling and syscalls. Heap `alloc_space` is cumulative
since process start, not that phase's allocation delta. These short profiles
provide inspectable evidence but do not prove the earlier 4500 SEND/s CPU cause.

## Evidence and validation

- [Run 1: profile companion plus all four layouts](assets/permission-caller-baseline-run-1.json)
- [Run 2: repeat with the identical binary/harness](assets/permission-caller-baseline-run-2.json)
- [Receipt/profile hashes and retained failed-fixture identities](assets/permission-caller-baseline-manifest.json)
- [CPU profile](assets/permission-caller-baseline.cpu.pprof),
  [heap profile](assets/permission-caller-baseline.heap.pprof)
- [Failure modes and fixed experiment](../superpowers/plans/2026-09-30-permission-cohort-baseline.md)
- [Reproduction invocation and scope](../../test/e2e/message/send_ban/AGENTS.md)

Focused invocations pass in 34.596 s and 30.734 s. Run 1 verifies histories of
161/417/161/161 successful IDs (the second includes 256 profiled sends); run 2
verifies 161 per layout. Both exclude all 32 rejected IDs per layout and contain
controls before/after unban.

Earlier `first-run` and `verified-run` receipts remain failed, retained in the
local evidence directory and identified in the manifest. They exposed fixture
errors: disabled debug API and use of Manager's node-local singular history
scan on nodes that could lack metadata or message data. The corrected harness
uses routed Product HTTP sync with complete More/sequence pagination. No failed
product capacity evidence was reclassified or discarded.

The existing send_ban package regression also passes in 336.529 s: ten top-level
tests pass, while the two independent opt-ins (this baseline and the 100k group)
skip. The focused baseline is verified separately above. No 500 SEND/s
qualification was run by this diagnostic phase; its
workload and thresholds are unchanged. The earlier 4500 SEND/s run remains
failed. Issue #977 remains open: cohort implementation and its cancellation,
fault, late-arrival, overload/recovery, allocation/queue-memory and same-hardware
candidate comparisons are still future work.
