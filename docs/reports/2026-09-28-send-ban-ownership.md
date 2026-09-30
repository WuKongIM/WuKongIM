# First-rejection SEND ownership and exact proposal diagnosis

The private diagnostic overlay on `29b2b707c` reproduced the original
5,000-channel / 4,500-SEND/s / cap32 controlled WAL failure. At the first full
Gateway shard in each node, 254–255 of 256 retained records awaited append results;
only 1–2 waited for ACK order and none were published while awaiting release fences.
The exact oldest requests spent 705–708 ms between worker submission observation
and execution, then waited on durable quorum work. This rules out ACK publication
or release bookkeeping as the dominant retained population in this capture.
It does **not** prove the physical cause of earlier natural stalls or qualify R2/R6.

Evidence: [frozen assets](assets/send-ban-ownership-20260928/),
[run contracts](assets/send-ban-ownership-20260928/contracts.md),
[source/context hashes](assets/send-ban-ownership-20260928/context.json).
No product source, capacity, deadline, permission, scheduling or durability behavior
was changed in this diagnostic. The overlay adds bounded observation only.

## Validation and observations

The tests were written before the probe and first failed for missing symbols.
They cover immutable first capture, all ownership states, strict fixture identity,
registry retirement, overflow and the actual deferred SEND admission/publication
lifecycle. Linux arm64 race checks ran ten times; the gateway core, replication and
worker default package tests passed. Fifteen offline parser/pairing/wait checks
passed, including missing/duplicate/invalid endpoints. The filtered race run had
no matching worker tests; the worker default suite ran separately.

The diagnostic binary SHA-256 is
`a910700484d77df34ccfc50ab3e3e888dea9c547f3ae81360130abe9d7f5ba06`.
Both fresh runs used the same existing harness, three-node Docker cluster, 128
store-append workers, 5000 channels, 4500 SEND/s and Gateway batch cap32. Neither
run overlapped builds, offline trace parsing, or cleanup. All eight traces were
complete, parsed successfully and retained with compressed/raw hash verification.

| Run | Result | Relevant observation |
| --- | --- | --- |
| Zero-hold control | FAIL at 43.754628437 s, 196868 SEND calls | First failure: `ReasonNodeNotMatch`, permission-read admission busy; busy metric 8; no node captured a full Gateway shard |
| 380 ms WAL floor, seconds 25–30 | FAIL at 26.480415095 s, 119158 SEND calls | EOF, pending 6355, permission busy 0; router peak 512 of 512 |

The zero-hold control is a retained failure, **not** a passed overhead control.
Its aggregate CPU mean was 582.285%; this observation alone cannot establish that
instrumentation had no effect. Request logging was limited to fixture ordinals
100000–140000 and is outside the final control trace window. Its permission failure
therefore remains a separate unresolved boundary, not evidence about the selected
WAL-fault requests.

Each fault node recorded three completed real message-WAL syncs plus one active
sync. Completed real syscall durations were 0.735–3.462ms; padded totals were
380.001–380.823ms. This verifies the intended controlled perturbation, not a
380ms physical-disk latency claim. Neither run had OOM or a disk-guard stop.

## Exact first-rejection partition

| Ingress node | Awaiting result | Ready/unpublished | Published/fences | Oldest ordinal | Age at snapshot |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 254 | 2 | 0 | 112741 | 1422.666 ms |
| 2 | 254 | 2 | 0 | 112748 | 1421.078 ms |
| 3 | 255 | 1 | 0 | 112756 | 1420.589 ms |

All snapshots had observed/before/after reserved counts 256, retained 256, both
unclassified residuals 0 and overflow=false. All three oldest records were lane
heads, preparation-returned=true, completed=false, ready=false. Record ownership
is copied under its existing mutex; reservation counters are separate observations,
so matching counts are internally consistent, not proof of an atomic counter/record
transaction or absence of concurrent ABA changes.

## Exact oldest-request joins

[Full request/manifest events](assets/send-ban-ownership-20260928/fault-380/oldest-request-joins.json)
join each numeric fixture ordinal to one full CommandID and manifest digest.
All three were led by node 2 and replicated to nodes 1/3. There are no inferred
identity joins based on similar times or ordinal suffixes.

| Ordinal | Worker submit→begin | Commit enter→lock | Complete local Store.Sync |
| --- | ---: | ---: | ---: |
| 112741 | 708.373 ms | 1.664 µs | 389.162 ms |
| 112748 | 704.626 ms | 2.304 µs | 389.674 ms |
| 112756 | 705.800 ms | 1.664 µs | 389.163 ms |

Submission is an observation immediately before the bounded worker's Submit call,
not an exact queue-entry event. The accepted event follows within microseconds.
Each worker/quorum call remained unfinished at trace capture. Local store scheduling
was 0.074–3.633ms; same-process, same-GID interval intersections attribute
98.122–98.148% of complete local store calls to `Coordinator.submitResult` waits.
This includes coordinator queued and executing time and is not an individual
physical-commit identity mapping.

The exact corresponding follower store calls had begin events and no end events.
Their GIDs remained in `Coordinator.submitResult`, with observed unfinished waits
334.233–716.354ms. These are right-censored waits, not fabricated completed latency.
The leader's corresponding exchange calls likewise had no end events. One fallback
peer dispatch waited 357.571ms for an exchange start. The observed maximum was 16
open exchange GIDs per remote target; these are begin-without-end lower bounds.

See [store/peer dependencies](assets/send-ban-ownership-20260928/fault-380/selected-dependencies.json),
[follower censored waits](assets/send-ban-ownership-20260928/fault-380/selected-follower-censored.json)
and [native wait parsing](assets/send-ban-ownership-20260928/fault-380/selected-wait-parse-results.json).

## Next repair boundary and remaining requirements

The first rejected shard is now connected to exact queued quorum jobs and their
local/follower durable waits. Another Gateway ACK scheduling change is unsupported
by this partition. Inspect how the existing bounded durable workers and commit
batching use their record budgets during a slow sync, and how backpressure reaches
the connection when a shard is full. Do not merely increase queues, worker counts,
timeouts, weaken durability, or reuse stale permission decisions.

The independent zero-hold permission-admission failure also requires explanation.
The natural I/O initiating cause remains unproved. Original clean uninterrupted
30-minute R2 and three fresh complete R6 comparison pairs remain incomplete;
no outcome in this report changes those gates to PASS.
