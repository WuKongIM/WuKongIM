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
fixed lifecycle cohort integration checks also pass on the final candidate.
The script's fourteen terminal-summary
cases and the named `flow-doc-contracts` check pass. Independent review found no
remaining P1/P2 issue.

Hosted validation uses a fixed set of three independent sequential 600-second
qualifications of `93031a3bfeaad8c1b0ca3ae987d32a8343d283bf`, retaining every
outcome without rerunning a failed attempt. No threshold, synchronous durability,
replication factor, or traffic count is relaxed. The final reports and local-step
reports are identified by artifact ID and SHA-256 in the companion evidence.

| Qualification | Result | Measured SEND/s | Measured sent = ACK | Hot mean | Hot above 400 ms |
| --- | --- | ---: | ---: | ---: | ---: |
| [1](https://github.com/WuKongIM/WuKongIM/actions/runs/38691246936) | clean, 600 s | 500.848 | 300,509 | 28.50 ms | 0.1798% |
| [2](https://github.com/WuKongIM/WuKongIM/actions/runs/38691616836) | clean, 600 s | 500.847 | 300,508 | 18.40 ms | 0.0028% |
| [3](https://github.com/WuKongIM/WuKongIM/actions/runs/38692625961) | product failure, hot latency | unavailable | 170,501 | 92.57 ms | 3.5913% |

Hot latency columns include warmup and are not exact P99 estimates. Configuration
digests and complete threshold objects match the original failure. Each passing
run generates exactly 300,000 primary messages during measurement (270,000 person,
30,000 group), plus ten canaries. In-flight work at the measurement boundaries
explains the small difference between primary generation and measured sends.
The classifier excludes acknowledgments for messages already sent during warmup.

Completed runs report zero terminal sends, losses, duplicates, corruptions,
sequence regressions, harness failures, or offered underdelivery. Delivery
correlation is sampled, not an exhaustive verification of every fanout recipient.
Runs 1 and 2 have two and three local-admission retries respectively; all settle.
Their zero product first-attempt failure counters exclude local admission failures
by the existing reporting policy. Run 3 has 363 retries without local transport
rejection; its retry path requires separate diagnosis.

`operator_stop / operator_requested` in the passing final reports is expected: the script
requests termination after the complete measurement, drains, and classifies the
step as `clean / complete`; `operator_interrupted` is false. The configured
810-second final boundary includes shutdown safety margins and is not the
required measurement duration.

All PR checks on the same code candidate pass: three-node correctness, messaging
unit/race, all three 500-QPS seams, and native package build/install/lifecycle
checks on Ubuntu 24.04, Debian 12, Rocky 9, and AlmaLinux 9. The third qualification
still fails with sustained hot latency and closes measurement early; its throughput
is unavailable, not zero observed traffic. Thus seeded arrival repair improves
the workload but does not yet establish a stable fix for the recurring Action.
The failure is retained for further diagnosis; no additional rerun is substituted.

## Remaining failure after arrival repair

The third candidate has no offered underdelivery. Pending-to-write mean is
1.42 ms, while write-to-ACK mean is 102.77 ms. Its measured successful
`store_append_wait` means are 60–62 ms across the three nodes, versus 11.3–11.7 ms
and 6.3–6.4 ms in the successful comparisons. Physical commit means are only
4.5–4.7 ms; logical storage requests wait 40–44 ms, so queueing and synchronous
completion work amplify the physical storage tail.

| Measured WAL signal, all three nodes | Candidate run 1 | Candidate run 2 | Candidate run 3 |
| --- | ---: | ---: | ---: |
| Channel-log fsync mean | 0.806 ms | 0.636 ms | 3.708 ms |
| Channel-log fsync above 100 ms | 0% | 0.00108% | 0.9254% |
| Metadata fsync mean | 1.193 ms | 0.914 ms | 5.842 ms |
| Metadata fsync above 100 ms | 0% | 0% | 1.7075% |

The failed run closes early, so absolute event counts cannot be compared as if
all measurements had equal duration. All three product nodes share one runner;
only the `host-node-1` alias supplies physical-device I/O evidence. These data
support a strong storage-tail association, not proof of an exclusive host cause.

Independent replay of the coordinator's five-second cuts reproduces the P99
failure exactly. The evaluator subtracts the warmup baseline and uses bounded
rolling windows. At termination, the active window has 4,921 of 143,705 samples
above 400 ms (3.4244%), continuously breached for 300.000000060 seconds. The last
25 seconds still contain 1,268 of 12,136 samples above 400 ms (10.45%). Thus the
failure is not an old warmup spike being misclassified after recovery.

A separate controlled diagnostic reproduces an avoidable amplification boundary
in the physical commit coordinator: pausing its owner beyond the collection
window causes the expired timer to seal a small batch even when compatible
requests are already queued. This demonstrates the mechanism, but existing
hosted metrics do not prove how often it occurs in this failure. The follow-up
repair captures one intake length and one pending-request count after expiry,
then consumes only that bounded set. New arrivals remain for the next cohort;
existing request, record and byte limits, lane fairness, queue credits and stop
semantics remain intact. The 500-microsecond wait window and synchronous
durability are unchanged.

The formal regression fails before the fix (four queued requests split after
two; the three-request cap also admits only two), then passes with the repair.
Virtual-time integration cases cover shutdown, size boundaries, lane fairness,
receipt ownership and new arrivals replenishing released credits. The complete
`pkg/db/...` suite, commit integration race, focused message race, root DB race,
and named `flow-doc-contracts` check pass. This isolated test is necessary to
reproduce an exact owner scheduling pause; hosted qualifications provide the
process-level system evidence.

The first arrival repair was merged in [PR #1011](https://github.com/WuKongIM/WuKongIM/pull/1011).
The follow-up commit-batching candidate uses a new fixed set of three independent
hosted qualifications. Its PR records the exact source SHA, run links, complete
outcomes and measured results as they complete, so validation refers to the
immutable commit actually executed. Neither the earlier failed candidate nor
later failed outcomes may be omitted.
