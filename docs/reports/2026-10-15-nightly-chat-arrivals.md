# Nightly chat lifecycle arrival diagnosis

Source: `e416a3c5bc2607c8cd75cfaf37a36f6a300af5b4`.
Primary failure: [38615901566](https://github.com/WuKongIM/WuKongIM/actions/runs/38615901566).
Successful comparison: [38076083656](https://github.com/WuKongIM/WuKongIM/actions/runs/38076083656).

## Observed failure

The latest ten scheduled qualifications use the same source revision. Eight
fail and two pass; all three separate 500-QPS seam jobs pass in every run.
Every failed final report has `product_failure / hot_latency`. This is a
sustained hot SENDACK latency rejection, not a build, download, or test timeout.

The primary failure sends and acknowledges 183,006 messages, including warmup,
with zero first-attempt failures, terminal sends, losses, duplicates, or
corruptions. Of 137,877 hot samples, 16,221 (11.76%) exceed 400 ms. The five-minute
sustained-breach rule ends measurement early. The outer report deliberately
leaves actual throughput unavailable for an incomplete 600-second measurement.

These full-run sample percentages include warmup and are not the qualification
algorithm. In particular, the other passing run,
[38291742971](https://github.com/WuKongIM/WuKongIM/actions/runs/38291742971), has
4.58% above 400 ms overall but does not sustain a terminal breach. A passing
Action does not mean every sample or every short window meets 400 ms.

## Attribution

Measured node-1 counter deltas from the two retained artifacts:

| Signal | Successful run | Failed run |
| --- | ---: | ---: |
| Channel-log WAL fsync mean | 1.95 ms | 13.79 ms |
| Channel-log fsync at least 100 ms | 0 | 191 |
| Physical message commit mean | 3.10 ms | 16.48 ms |
| Channel store-append wait mean | 92.14 ms | 156.61 ms |
| SEND permission stage mean | 35.90 ms | 62.42 ms |
| Maximum sampled disk service time | 0.44 ms | 9.57 ms |

These stages have different populations and overlapping scopes; their means
must not be summed into one request latency. Counter deltas are retained in
the companion [evidence](2026-10-15-nightly-chat-arrivals.json).

The failed run has spare CPU and memory and no cgroup CPU throttling. Its hot
profile context has substantial I/O pressure. The three ten-second CPU profiles
consume only 2.97–3.17 CPU-seconds each. They start after the triggering window,
when append latency has already improved, so they cannot establish a specific
mutex or late-run storage-build hotspot. The evidence supports storage latency
amplifying queueing; it does not prove storage is the only contributor.

The workload adds a reproducible amplification mechanism:

1. The coordinator allocates a complete one-second budget at once.
2. Each worker generates its entire share with the same SEND due timestamp.
3. Workers advance due work only once per second.

Consequently, the 500 SEND/s workload becomes a synchronized batch of roughly
500 SENDs every second. Retained worker cuts often show exactly 500 outstanding
ACKs, while profiles between batches find the submitters idle. This also
explains why the scheduled-arrival seam tests can pass beside a failing nightly.

The workload specification explicitly requires seeded arrival times and permits
bounded short bursts; it does not require perfectly uniform arrivals. See
[the arrival contract](../superpowers/specs/2026-08-04-chat-lifecycle-soak-design.md).
The repair restores seeded subsecond arrivals within the existing grant budget.
It changes the harness arrival model, and must not be presented as proof that
the product now meets the old synchronized-burst workload on arbitrary disks.

## Additional diagnostic gap

[37667782311](https://github.com/WuKongIM/WuKongIM/actions/runs/37667782311)
also loses process continuity for `process-metrics-collector`; its three service
and three worker processes remain continuous. Its final product cause is still
`hot_latency`, but the outer classifier correctly reports insufficient evidence.
This collector issue is separate from the repeated latency rejection.

## Validation

The initial regression command was
`GOWORK=off go test ./internal/bench/chatlifecycle -run 'Test(EngineExternalGrant|WorkerExternalGrant)' -count=1`.
Before the repair it demonstrated all 100 test messages in one subsecond bucket,
replayed overdue messages after a two-second scheduler delay, and observed the
production worker's one-second advancement cadence. These tests reproduce the
arrival defect; they are not a local reproduction of the hosted disk latency.

The repair retains the immediate bootstrap grant, apportioned budgets, traffic
mix, grant replay fences, and existing bounded group-route deferral. Later
available primary sends receive seeded deadlines within their grant second.
Only due-work advancement runs every 10 ms; login and local rate allocation
retain their one-second cadence. Late scheduled work produces explicit
underdelivery. Future admitted SENDs prevent a premature shutdown fence.

Focused tests and the complete `internal/bench/...` unit suite pass locally.
The final chat-lifecycle package tests, focused race checks, and first-round
1,000/2,000 SEND/s churn integration checks pass. The formal one-hour grant and
fixed lifecycle cohort integration checks also pass; they preceded the final
deferred-route boundary regression fixes. The script's fourteen terminal-summary
cases and the named `flow-doc-contracts` check pass. Independent review found no
remaining P1/P2 issue. Hosted candidate qualification is pending.
Hosted validation will retain all results from a fixed set of three independent
600-second qualifications, including any failure. No threshold, synchronous
durability, replication factor, or traffic count may be relaxed to obtain a pass.
