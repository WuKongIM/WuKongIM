# Membership-Backed Conversation Performance Acceptance

Date: 2026-08-06

Updated: 2026-08-08

Directory candidate: `a864512460d92bef63f4fc0c43d2b427e6ae04aa`

Previous short-gate candidate: `943ea67957bf532f2d78c0aa8c9d7893fd543361`

Updated sustained candidate: current branch candidate

## Decision

**GO for the membership-backed conversation directory, its steady-state SEND
write-amplification objective, and the local three-node 4,500 QPS recipient
hot-path acceptance gate.**

The dedicated three-node directory gate and the 100,000-member group gate pass.
A 20,000-message Cloud Medium-shaped local workload passes at both 500 and
4,500 offered QPS with zero membership mutation rows during each measured SEND
window. At 4,500 QPS the final candidate records 401.73 ms SENDACK P99 and
415.14 ms RECV P99 while fully draining the workload.

This is a reproducible local acceptance result, not a universal deployment
capacity claim. Representative multi-host qualification and a longer soak
remain separate operational gates.

The final combined candidate also passes the unchanged sustained boundary. Its
strict ten-minute gate drained all 2.7 million messages at 4,498.822
completions/s with SENDACK/RECV P99 256/236 ms, zero hard-full counters, and
zero membership mutations. The 30-minute, 5,000-channel gate then drained all
8.1 million SENDs and 16.2 million receiver deliveries at 4,499.684
completions/s with SENDACK/RECV P99 379/320 ms, zero admission/rejection errors,
zero membership mutations, continuous processes, and aggregate heap below
859 MB. The reviewed local 30-minute boundary is therefore **QUALIFIED**.

## Defects Found During Acceptance

### Cross-node person-directory readiness

The first 500 QPS run observed 250 membership mutation rows because the cold
prime used a different sender for every measured person-channel pair. Fixing
the fixture reduced the count but left 106 rows and exposed the product defect:
the authoritative Slot Channel RPC omitted `Channel.DirectoryReady`.

When a SEND node read person-channel metadata from another Slot Leader, the
already-initialized channel appeared unready and both memberships were proposed
again. The Channel RPC response codec now carries `DirectoryReady`, with a new
wire version and legacy v3 decoding. The cold prime now uses the exact sender
that the measured WKProto path uses. Focused binary-codec and two-node
authoritative-read integration tests cover both boundaries.

### Batched permission-check head-of-line blocking

After the readiness fix, repeated 4,500 QPS runs still recorded roughly
1.9-2.1 second SENDACK P99. Gateway queue capacity, Channel RPC admission,
Channel append, and post-commit queues were not saturated. During-load
goroutine evidence instead showed session-scoped gateway workers blocked while
`message.SendBatch` performed independent authoritative permission Slot RPCs
sequentially.

Three A/B controls rejected simpler tuning explanations:

- increasing the Channel RPC batch maximum from 8 to 64 left P99 at about
  1.75 seconds;
- increasing active group-channel cardinality caused earlier gateway admission
  pressure rather than removing the bottleneck;
- shrinking gateway batches reduced fixed-cost amortization and filled the
  async dispatch queue.

The final implementation checks independent batch-item permissions with at
most 16 managed workers. It then restores original item order before
person-directory establishment, plugin hooks, append admission, and result
alignment. Session ordering is therefore preserved, while one slow Slot RPC no
longer serializes every independent permission read in the same gateway batch.
The `PermissionStore` contract now explicitly requires concurrent-call safety.

## Accepted Evidence

### Steady-state SEND at 500 QPS

Three local processes, 256 physical hash slots, 10 logical Slots, three
replicas, 20,000 messages, 1,572,000 recipient rows, and 243,600 online routes:

| Signal | Result |
| --- | ---: |
| Offered / ingress | 500 / 500.02 messages/s |
| Completion | 499.08 messages/s |
| SENDACK P50 / P99 / max | 85.84 / 205.76 / 352.34 ms |
| RECV P99 / max | 231.40 / 355.89 ms |
| Ordinary membership mutation rows | **0** |
| Gateway max queue ratio | 0.00012 |
| Channel RPC admission-full | 0 |
| Channel RPC max queue / worker ratio | 0.00391 / 0.01042 |
| Max node / aggregate heap | 134.2 / 360.8 MB |
| Allocated bytes / GC cycles | 5.54 GB / 86 |
| Plugin accepted / invoked | 10,400 / 10,400 |
| Process continuity / drain | true / true |

Before the cross-node readiness fix, the same gate recorded 250 and then 106
membership rows and SENDACK P99 of about 3.75 seconds and 1.42 seconds. The
accepted run proves both the zero-write invariant and the latency consequence
of preserving the readiness fence.

### Three-node directory synchronization

Each phase used 200 memberships, 32 concurrent clients, 10 requests per client,
and the public `/conversation/list` and `/metrics` endpoints.

| Page | Requests/s | P50 | P95 | P99 | Allocated | Aggregate heap |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 25 | 6,469.8 | 4.14 ms | 7.63 ms | 9.91 ms | 159.4 MB | 346.3 MB |
| 100 | 2,741.1 | 10.01 ms | 18.24 ms | 21.24 ms | 573.3 MB | 365.2 MB |
| 200 | 1,285.7 | 21.42 ms | 43.19 ms | 47.65 ms | 1.13 GB | 428.8 MB |

All three phases recorded:

- zero membership mutation rows and zero Channel mailbox-full admissions;
- exactly one hydration batch per request;
- exactly one Leader-local head read per returned membership;
- exactly two remote Leader batch calls per request on this three-node fixture;
- complete conversations, no deletes, and no unresolved keys.

The Darwin process collector did not publish `process_cpu_seconds_total`, so
the evidence explicitly records `cpu_observed=false` instead of reporting zero
CPU consumption. CPU diagnosis used the profiles described below.

### 100,000-member group

The opt-in single-node-cluster black-box scenario passed in 20.77 seconds. It
created the 100,000-member directory, sent one group message, proved the
ordinary membership mutation counter was exactly unchanged across SEND, and
hydrated sampled subscriber conversations through the public API.

### Steady-state SEND at 4,500 QPS

The final strict run used the same three-process, 256-physical-hash-slot,
10-logical-Slot, three-replica, 20,000-message, 1,572,000-recipient-row, and
243,600-online-route fixture as the 500 QPS control.

| Signal | Result |
| --- | ---: |
| Offered / ingress | 4,500 / 4,500.21 messages/s |
| Completion | 4,332.86 messages/s |
| SENDACK P50 / P99 / max | 191.69 / **401.73** / 510.98 ms |
| RECV P99 / max | **415.14** / 516.49 ms |
| Ordinary membership mutation rows | **0** |
| Gateway max queue ratio | 0.00179 |
| Channel RPC admission-full | 0 |
| Channel RPC max queue / worker ratio | 0 / 0.03125 |
| Max node / aggregate heap | 167.0 / 382.1 MB |
| Allocated bytes / GC cycles | 4.27 GB / 55 |
| Plugin accepted / invoked | 10,400 / 10,400 |
| Process continuity / drain | true / true |

The first post-fix run already reduced SENDACK P99 to 437.70 ms and RECV P99
to 452.38 ms, but its measured ingress was 4,499.09 messages/s. The strict gate
failed only because this was 0.02 percent below the exact 4,500 threshold. An
unchanged rerun measured 4,500.21 messages/s and passed. The sub-millisecond
offered-load pacing boundary should therefore be treated as harness jitter;
latency, mutation, continuity, and drain evidence was healthy in both runs.

### Sustained permission-pressure qualification

The opt-in black-box soak runs three real processes with 256 physical hash
slots, 10 logical Slots, 25 senders, 25 online receivers, and 5,000 naturally
hashed group channels. Every channel has one sender/receiver subscriber pair;
the deterministic fixture proves that both ingress-local and cross-node Slot
permission routes are exercised. SEND never mutates the membership directory.

The harness is bounded over the full 8.1-million-message target: latency uses a
fixed 10,001-bucket histogram, completed message state is deleted, and public
Prometheus samples are aggregated rather than retained. It records transport
executor queue/busy/rejection pressure, permission Slot RPC calls, errors,
admission and in-flight state, `message/permission_batch` goroutine activity,
heap/GC, plugin conservation, process continuity, and membership mutation
rows. Complete runs emit `wukongim/permission-soak-evidence/v1`; premature
failures emit `wukongim/permission-soak-failure/v1` before bounded node
diagnostics.

A 10-second diagnostic control at 4,500 QPS and 100 channels passed:

| Signal | Result |
| --- | ---: |
| Messages / ingress | 45,000 / 4,500.09 messages/s |
| Completion | 4,413.93 messages/s |
| SENDACK P99 / max | 311 / 451.55 ms |
| RECV P99 / max | 292 / 440.77 ms |
| Permission Slot RPC calls / errors | 146,860 / 0 |
| Permission Slot RPC admission errors | 0 |
| Max transport queue / busy ratio | 0 / 0.00265 |
| Permission batches / panics / max active | 38,442 / 0 / 38 |
| Ordinary membership mutation rows | **0** |
| Max node / aggregate heap | 141.0 / 369.7 MB |
| Metric samples / errors | 33 / 0 |
| Process continuity / drain | true / true |

The original long run failed after 862,476 completed client SEND calls when
`message.send` deadlines accumulated and the bounded gateway dispatch queue
filled. Subsequent storage, RPC, and append-path changes removed that service
time bottleneck. A 130-second diagnostic then proved that SENDACK and all three
durable replicas continued at the offered rate, but every receiver stopped at
about 16,200 messages. That uniform boundary was `180 RECV/s * 90s`: the test
clients did not send WKProto PING frames, so their presence routes expired at
the default 90-second TTL. The harness now heartbeats all sender and receiver
sessions every 30 seconds; retrying a bounded `ReadRecv` timeout does not relax
the existing RECV latency acceptance.

The first heartbeat-corrected run completed all messages but exceeded the heap
gate: 611.2 MB maximum node heap and 1.656 GB aggregate heap. A 90-second heap
profile found two avoidable owners:

- the leader recent-record cache retained 57-73 MB of full payloads per node
  after every follower had acknowledged them;
- Pebble retained 104-109 MB per node while fragmenting highly overlapping
  Raft entry-suffix range tombstones created during pure tail appends.

Leader caches now release the prefix acknowledged by every configured follower,
with durable-store fallback for an older pull. Pebble Raft storage now writes a
suffix tombstone only for an actual overwrite, not a pure append. Neither queue
capacity nor worker limits were raised.

The current 130-second diagnostic passed:

| Signal | Result |
| --- | ---: |
| Messages / ingress / completion | 585,000 / 4,500.007/s / 4,496.213/s |
| SENDACK P50 / P99 / max | 118 / **252** / 399.45 ms |
| RECV P99 / max | **229** / 397.55 ms |
| Receiver delivery | 25 × 23,400, zero read timeouts |
| Permission Slot RPC calls / errors / admission errors | 273,815 / 0 / 0 |
| Recipient tasks / recipient rows / errors | 585,000 / 1,170,000 / 0 |
| Durable message records | 1,755,000 (three replicas) |
| Ordinary membership mutation rows | **0** |
| Max node / aggregate heap | **230.5 / 584.4 MB** |
| Heap thresholds | 512 MiB / 1.5 GiB |
| Process continuity / drain | true / true |

Measured-window histogram deltas attribute P99 as follows: gateway dispatch
209.3 ms, Channel store-append wait 96.0 ms, quorum HW-advance wait 88.1 ms,
leader/follower commit request 50.0/51.0 ms, and physical commit 37.4 ms.
These values exclude setup and cold prime work.

One strict 10-minute candidate preserved all correctness and capacity
invariants but failed latency during a compaction wave: SENDACK P99 was 2.174
seconds, RECV P99 was 1.664 seconds, and the store-apply queue reached 100%.
The next quiet-host rerun completed 2.7 million SENDs, 2.7 million designated
receiver deliveries, and 5.4 million recipient rows exactly at 4,500.002/s.
SENDACK/RECV P99 improved to 971/703 ms, membership writes remained zero, and
all 116 coalesced checkpoints were admitted. Its only failure was 1,287
Channel RPC admission-full events, all on node 2. That node completed about
2.156 million PullHint tasks versus 1.611-1.622 million on its peers, but the
aggregate admission metric could not identify the rejected task kind. The
harness and runtime metrics now retain a bounded Pull versus PullHint admission
split before any pool change is attempted.

The measured stage P99 for that strict run was gateway dispatch 435.3 ms,
Channel store-append wait 99.1 ms, quorum HW-advance wait 97.9 ms,
leader/follower commit request 58.1/58.9 ms, and physical commit 37.6 ms. This
confirms the prior storage tail was controlled; the remaining gate is a brief
replication-RPC admission burst rather than sustained storage saturation.

The typed rerun disproved that provisional owner: Pull, PullHint, and aggregate
Channel RPC admission-full all remained zero. Instead, a host I/O pause made
thousands of fixed-window committed-HW checkpoints overdue together. The first
rerun failed after 4m43s with 4,348 accepted checkpoint tasks, 16,030 checkpoint
admission-full events, 2.686-second SENDACK P99, and a 1.924-second gateway
dispatch P99. A deterministic checkpoint-deadline spread delayed but did not
solve pause recovery because a pause longer than the whole window leaves every
deadline overdue; that run failed after 6m24s with 6,599 accepted checkpoint
tasks and 38,829 checkpoint admission-full events, while measured SENDACK P99
outside the terminal stall was 454 ms.

The next candidate added an isolated checkpoint-pool half-capacity high-water
mark and re-spread excess overdue work. It eliminated checkpoint admission
failure entirely, as well as all typed RPC admission failure, but still accepted
3,130 standalone checkpoints before a terminal host pause at 6m16s; measured
SENDACK P99 remained 470 ms. The follower scheduler now gives pending record
apply and dirty Pull work precedence over idle committed-HW checkpoint writes,
so the next foreground apply can persist that HW atomically. The Darwin E2E
workspace is also marked `.metadata_never_index` before Pebble artifacts are
created; repeated post-run `mdworker_shared` activity showed Spotlight was an
unrelated source of large disk spikes. These last two changes still require the
strict 10-minute gate.

A subsequent class-aware RPC pacing candidate made PullHint yield at 50% queue
occupancy and Pull at 75%. It kept aggregate, Pull, and PullHint admission-full
at zero and reached only 13.67% maximum queue occupancy, but one SENDACK timed
out after 7m47s. Before that timeout, measured SENDACK/RECV P99 was 910/619 ms
and stage P99 was 471.1 ms gateway dispatch, 112.8 ms store append, 99.3 ms
quorum wait, 22.1 ms follower pull, 99.3 ms ack return, and 40.2 ms physical
commit. The 7,079 accepted checkpoints were sampled after foreground traffic
stopped and are therefore a failure symptom, not evidence that checkpoints
caused this timeout.

An experiment allowing two concurrent Pebble compactions reduced neither the
tail nor shared-disk contention: it failed after 4m38s with 1.906-second
SENDACK P99, 1.296-second RECV P99, 995.3 ms gateway-dispatch P99, and 63.96%
maximum RPC queue occupancy. That experiment was reverted. The current
candidate instead paces PullHint at 25% and Pull at 50%, records proactive
admission as typed `result="paced"` evidence, and preserves a minimum four-task
watermark so one ordinary multi-replica fanout is never mistaken for queue
pressure. It has passed package and E2E evidence tests but still requires the
strict 10-minute gate.

That tighter RPC-only candidate failed after 6m28s with 2.222-second SENDACK
P99 and 1.379-second RECV P99. RPC Pull, PullHint, and aggregate admission-full
all stayed at zero, but the shared RPC queue reached the 50% Pull watermark and
Pull pacing rose from 52 at minute six to 11,058 at failure. More importantly,
node 2 recorded 4,909 store-apply admission-full events; its store-apply queue
and workers reached 100%, 775 append effects waited on Channel append futures,
and 512 inbound append RPCs waited on channelappend futures. Checkpoint work
was only 182 tasks at minute six and rose after foreground traffic stopped, so
it is again a recovery symptom rather than the initiating owner.

The next single-variable candidate kept those RPC watermarks and stopped
starting new Pulls when the local store-apply queue reached half capacity. It
retained dirty state, retried over 50-100 ms, and left half the apply queue for
responses from already-running Pulls. That guard worked: a 5m08s run kept
store-apply admission-full at zero and queue occupancy below 50%, but still
failed with 2.782-second SENDACK P99. This ruled out hard store-apply admission
failure as the remaining latency owner.

The E2E stage evidence now also retains diagnostic P99.9 and sampled leader
Pull handler attribution. An exact 4,500-QPS rerun failed at 4m06s with
2.133-second SENDACK P99. P99.9 separated the terminal chain: gateway dispatch
was 2.385 seconds, follower Pull/AckOffset/HW advancement was about 907 ms,
store append was 304 ms, leader/follower commit requests were about 202 ms,
physical commit was 95 ms, and leader Pull mailbox wait was only 19 ms with a
sub-millisecond handler. RPC Pull pacing rose from 7 at minute three to 8,974
at failure exactly as the shared queue reached the 50% Pull watermark, while
RPC and store-apply admission-full both stayed zero. A single-variable rerun
then relaxed only Pull headroom to 75%. It disproved that candidate after 54
seconds of measured load: SENDACK P99 reached 2.005 seconds, RPC occupancy
reached 73.3%, and physical-commit/store-append/quorum P99.9 rose to
238/900/1,208 ms even though both admission-full counters remained zero. The
code therefore retains 25% PullHint, 50% Pull, and the independent 50%
store-apply guard while the Pull amplification owner is investigated.

Measured-window amplification evidence was then added to both complete and
premature-failure rows. A 10-second exact slice drained all 45,000 messages and
measured 89,994 record-bearing Pulls, 92,724 empty ACK-return Pulls, 89,994
follower applies, 36,350 multi-item Pull batches covering 173,853 items, and
zero Pull pacing. It nevertheless paced 1,117 PullHints at the 25% watermark,
while first-follower-Pull P99.9 was about 898 ms. The next single-variable
candidate therefore gives a new append's first wakeup the same 50% watermark
as foreground Pull, while retry-only resume PullHint stays best effort at 25%.
Reason-specific paced counters make that hypothesis falsifiable without
changing worker count, queue capacity, or store-apply protection.

The reason-aware candidate then ran for 3m40s and completed 993,821 sends
before one SENDACK timeout. Its measured SENDACK/RECV P99 was 428/361 ms;
gateway/store/quorum P99 was 359/99/92 ms, every Channel RPC and store-apply
admission-full counter stayed zero, and first-follower-Pull P99 was 19 ms.
The terminal wave did include 206 one-second Pull RPC timeouts and reactor
stacks inside the shared due scheduler. Reactor maintenance is now limited to
128 ready deadlines between mailbox turns, leaving the remainder coalesced in
the heap so worker completions can make progress.

An exact rerun with that fairness bound completed 1,064,112 sends in 3m56s
before another SENDACK timeout. It eliminated Pull errors and kept leader Pull
mailbox P99 at 3.2 ms, so the prior reactor starvation signature did not recur.
The remaining wave instead had 2.248-second SENDACK P99, 1.312-second gateway
dispatch P99, 283 ms store-append P99, 188 ms quorum P99, and 647-929 append
futures in flight per node while gateway queue occupancy remained only 2.35%.
This identifies a downstream append wave amplified by session-ordered gateway
dispatch, not a full gateway queue. Measured-window evidence now retains
gateway batch-record P99 to test whether recovery-time micro-batch growth is
the amplifier before changing the explicit same-session ordering contract.

The first run with that evidence failed after 88 seconds on a visibly noisy
host, but it still made the feedback loop measurable: gateway batch-record P99
had grown to 235.6 records, gateway-dispatch P99 was 2.148 seconds, and the
global gateway queue peaked at only 4.5%. A single-variable two-minute rerun
then set `WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=64` without
changing ordering, worker counts, queue capacities, or replication pacing. It
completed all 540,000 sends with exact receiver counts, SENDACK/RECV P99 of
356/315 ms, gateway batch-record P99 of 31.9, and gateway/store/quorum P99 of
236/101/91 ms. Gateway, recipient, reactor, store-append, store-apply, and RPC
occupancy all remained below 24%; every Channel RPC and store-apply
admission-full counter remained zero. This supports bounded micro-batches as a
recovery-wave damper, but two minutes does not qualify a new product default.

The six-minute continuation disproved batch size 64 as a complete fix. It
crossed the previous failure region but timed out after 4m50.8s with 1,308,615
completed sends and 22,325 pending. SENDACK P99 was still 960 ms and batch-
record P99 remained bounded at 54.1, but gateway P99/P99.9 reached 695/1,624 ms
after the store-apply queue crossed its 50% pacing watermark. Channel RPC and
store-apply admission-full plus Pull errors remained zero, while store-apply-
triggered Pull pacing jumped to 47,917. The terminal profile supplied the
missing upstream owner: one node spent about 20% cumulative CPU in the
per-message `lookupIdempotencyByKey -> Pebble DB.Get` path, LSM read
amplification rose from 5 to 26, and compaction debt reached about 195 MB.

Message storage now uses a bounded, lazily rebuilt per-active-Channel
membership filter for idempotency keys. A definite negative skips the durable
point lookup; a possible hit still executes the original index and message-row
verification. Trusted follower applies update an already-loaded filter so a
later leader transition cannot create a false negative. The two fixed layers
use at most about 1.5 KiB per indexed active Channel; saturation can only
increase false positives and durable reads. Public measured-window counters
separate negative-filter skips from point reads. The existing 128-Channel
physical batch benchmark improved from 6.38-6.77 ms/op to 6.03-6.17 ms/op, but
the exact sustained scenario remains the qualification boundary.

The first exact filter diagnostic used the product's unchanged 512-record
gateway batch default for one measured minute. It completed and drained all
270,000 sends at 4,500 offered messages/s across 5,000 channels. SENDACK P99
was 336 ms, RECV P99 was 300 ms, gateway batch-record P99 was 39.2, and both
Channel RPC and store-apply admission-full remained zero. Measured-window
idempotency evidence reported 268,300 definite-negative skips and one durable
point read. This validates that the new filter removes essentially all unique-
message idempotency point lookups without relying on the 64-record gateway
override; the longer gate still determines whether it removes the late LSM
feedback loop.

The six-minute default-batch continuation crossed the previous 4m50.8s
failure point and completed, delivered, and drained all 1.62 million sends.
Channel RPC and store-apply admission-full remained zero. The filter reported
1,618,797 definite-negative skips and 780 point reads, so the former per-
message Pebble lookup did not return as the hot path. The run nevertheless
missed the latency boundary: SENDACK P99 was 1.279 seconds, RECV P99 was 860
ms, and gateway dispatch-wait P99/P99.9 was 702/1,601 ms. Gateway batch-record
P99 reached 52.2 while the global gateway queue ratio stayed below one percent.
Store-append/store-apply peaks reached 68.8/53.1 percent, read amplification
23, and compaction debt about 277 MB, but all messages continued to drain.
Failure-time goroutines located gateway batch workers waiting on local append
futures and remote `ForwardSendBatch` calls. This leaves session-scoped batch
tail amplification, rather than CPU saturation or durable idempotency reads,
as the next measured owner.

Measured-window gateway batch-handler and local/remote router histograms were
then added to make that hypothesis directly testable. A two-minute exact
single-variable run capped one session's gateway micro-batch at 32 records. It
completed and drained all 540,000 sends with SENDACK P99 543 ms, RECV P99 480
ms, batch-record P99 31.3, and zero Channel RPC/store-apply admission-full.
Gateway dispatch/handler P99 was 387/243 ms; local/remote router P99 was
221/225 ms. Their P99.9 values were 942/738 ms and 248/248 ms respectively,
showing the handler tail is the maximum-of-many router-group tail and that it
then queues later work on the same session shard. The short run keeps 32 as a
candidate only; the six-minute recovery region must pass before changing the
product default.

The six-minute 32-record continuation disproved that value as a sustained
default. An early host/recovery pause produced 4,220 pending sends, 3,127
maintenance checkpoints, and 4,311 paced Pulls in the first minute. The system
recovered to 121 pending by minute four and ultimately drained all 1.62
million sends with zero Channel RPC/store-apply admission-full. Storage stayed
bounded: store-apply-triggered Pull pacing ended at 131, idempotency evidence
was 1,617,942 skips versus 798 point reads, and no Pull failed. However,
serially draining the resulting chain of 32-record session batches pushed
gateway dispatch P99 to 2.005 seconds even though handler P99 was 363 ms and
local/remote router P99 was 243/244 ms. SENDACK P99 therefore reached 1.627
seconds. A recovery batch must retain more throughput than 32 without returning
to the unbounded 512-record tail; the previously measured 64-record candidate
is the next single-variable boundary now that the idempotency owner is removed.

The six-minute 64-record continuation also failed the latency boundary. It
drained all 1.62 million sends with zero Channel RPC/store-apply admission-full,
zero Pull errors, 1,616,347 idempotency negative-filter skips, and 745 point
reads. Unlike the 32-record run, pending work stayed continuously bounded and
store-append/store-apply occupancy peaked at only 27.6/33.2 percent. The larger
recovery batch reduced gateway dispatch P99 to 1.078 seconds, but raised batch-
handler P99 to 460 ms; local/remote router P99 remained 245/247 ms. SENDACK and
RECV P99 were consequently 1.379/1.234 seconds. The unchanged 512-record run,
32-record run, and 64-record run now expose the two sides of the same serialized
head-of-line tradeoff: a larger batch amplifies the maximum downstream tail,
while a smaller batch cannot drain a same-session recovery chain quickly
enough. No fixed default has qualified from these samples.

The measured-window evidence now also records one complete router-batch
P99/P99.9 observation per `SendBatch`. Local/remote paths still measure one
canonical-channel group. Their difference attributes authority resolution,
retry, and maximum-of-groups aggregation before changing either concurrency
bound.

The six-minute 128-record continuation is the first sustained batch candidate
to meet the current boundary. It completed and drained all 1.62 million sends
at 4,500 offered messages/s with exact receiver counts, SENDACK P99 exactly
1,000 ms, RECV P99 711 ms, and zero Channel RPC/store-apply admission-full or
Pull errors. Gateway dispatch/handler P99 was 560/486 ms. Complete router-batch
P99 was 247 ms, close to local/remote group P99 of 237/240 ms, so authority
resolution and maximum-of-groups aggregation are not the missing handler time.
The candidate kept gateway batch-record P99 at 43.6, ended with zero pending,
reported 1,616,398 idempotency skips versus 769 point reads, and bounded
store-apply occupancy at 43.8 percent. Because the result has no latency margin
and remains an environment override, it qualifies the six-minute diagnostic,
not the product default or strict gate.

Measured-window message `permission`, `pre_append`, and `submitter` stage
histograms were added next. They will test whether the remaining handler gap is
the permission Slot fanout before changing its fixed four-Slot concurrency.

The first two-minute 128-record run with those stages drained all 540,000 sends
with SENDACK P99 523 ms. Its one-sample-per-batch P99 was 17.5 ms for
permission, 0.5 ms for pre-append, and 240.1 ms for submitter; complete router-
batch P99 was also 240.1 ms and gateway handler P99 was 245.9 ms. This rejects
permission Slot concurrency as the short-window owner, so the four-worker bound
was not changed. It also exposed a histogram weighting error in the longer-run
comparison: gateway handler and SENDACK are per message, while the new stages
and router batch were per operation. Slow large batches were therefore
underweighted. Dedicated item-weighted router-batch and message-stage
histograms now provide the valid per-message comparison; the original router
operation histogram remains useful for batch-level diagnosis.

A one-minute item-weighted verification then proved the corrected attribution:
router-batch-item and message-submitter P99 were both 246.5 ms versus 247.1 ms
for gateway handler; their P99.9 values all reached about 1.905 seconds.
Permission remained 20.2/40.9 ms at P99/P99.9. The long tail therefore belongs
to the complete router submitter, not permission or SENDACK writing.

The next single-variable candidate raised the router's fixed independent-
Channel group bound from 64 to 128 while keeping the gateway override at 128.
A two-minute run drained all 540,000 sends with SENDACK/RECV P99 442/366 ms,
router-batch-item/message-submitter/gateway-handler P99 246/246/247 ms, and
their item-weighted P99.9 about 869 ms. Channel RPC worker/queue peaks rose to
59.4/19.2 percent but every Channel RPC and store-apply admission-full counter
remained zero; store-apply queue occupancy peaked at 28.9 percent. This supports
one bounded router wave for the 128-record candidate, subject to the six-minute
late-pressure gate.

The six-minute continuation rejected that 128-group bound. It still drained
all 1.62 million sends with exact delivery and zero Channel RPC/store-apply
admission-full, but RPC workers reached 100 percent, store-apply queue
occupancy reached 69.6 percent, and SENDACK P99 rose to 1.337 seconds. Gateway
dispatch/handler P99 was 830/924 ms, and the item-weighted router-batch and
message-submitter P99 was 898 ms. The short-window improvement had converted
the rare largest gateway batches directly into downstream pressure.

The fixed router group bound was therefore reduced to 96, leaving at most a
small second wave for the bounded 128-record gateway diagnostic. Under the
same six-minute load it drained all 1.62 million sends with exact delivery,
SENDACK/RECV P99 984/640 ms, and zero Channel RPC/store-apply admission-full.
Gateway dispatch/handler P99 fell to 479/454 ms, while item-weighted router-
batch/message-submitter P99 was 452 ms. RPC worker occupancy peaked at 80.2
percent instead of 100 percent and store-apply queue occupancy peaked at 58.7
percent. The 96-group bound is the first router candidate to survive the late-
pressure region without either violating the one-second boundary or consuming
all RPC worker headroom.

It did not survive the strict ten-minute gate. A heavier startup recovery wave
reached a SENDACK timeout after 2m22.8s with 20,826 messages pending, although
Channel RPC/store-apply admission-full and Pull errors remained zero. SENDACK
P99 was 2.274 seconds, gateway dispatch P99 2.007 seconds, and item-weighted
router-batch/message-submitter P99 1.616 seconds. Gateway batch-record P99 had
grown to 94.5. Failure-time goroutines exposed the missing boundary: on node 1
alone, 321 remote router groups were blocked in transport calls and 117 local
groups were waiting on append Futures; node 2 had another 249 remote and 171
local waits. The 96 limit was per `SendBatch`, so concurrent gateway sessions
could still multiply it into hundreds of downstream waits per node.

The router now has a second, node-local admission limit shared across all
`SendBatch` calls. Its fixed capacity is 192, or two default per-batch shares,
so one batch can use at most half the aggregate allowance while two batches can
still overlap. A group owns its slot through local Future or remote-forward
completion. Public inflight/capacity gauges and permission-soak peak evidence
make this boundary directly auditable. This candidate requires a short exact
A/B before another strict attempt.

The two-minute exact A/B passed. It drained all 540,000 sends with exact
delivery, SENDACK/RECV P99 315/282 ms, completion throughput about 4,496/s,
and zero Channel RPC/store-apply admission-full, Pull pacing, or Pull errors.
The new shared admission reached exactly 192/192 while Channel RPC worker
occupancy remained at 41.7 percent and store-apply queue occupancy at 20.8
percent. Gateway dispatch/handler P99 was 217/244 ms and item-weighted router-
batch/message-submitter P99 was 243 ms. The boundary is active without
under-driving the offered load, but a six-minute continuation must cross the
prior 2m22.8s failure window before another strict attempt.

The six-minute continuation also passed with substantial margin. It drained
all 1.62 million sends with exact delivery, SENDACK/RECV P99 674/555 ms, about
4,498 completions/s, and zero Channel RPC/store-apply admission-full or Pull
errors. Shared router admission again reached exactly 192/192, while Channel
RPC worker occupancy remained at 41.7 percent and store-apply queue occupancy
at 43.3 percent. Gateway dispatch/handler P99 was 346/421 ms and item-weighted
router-batch/message-submitter P99 was 415 ms. One 2,752-message recovery wave
at minute four returned to 382 pending by minute five without RPC Pull or
PullHint pacing; the run ended with zero pending. This crosses the prior strict
failure window and qualifies the shared limit for a new strict ten-minute run.

That strict run failed on a different boundary after 3m09.5s. Aggregate
SENDACK/RECV P99 remained only 469/443 ms and every Channel RPC/store-apply
admission-full counter stayed zero, but one SENDACK exceeded its deadline while
20,406 messages were pending. Failure-time stacks showed 330 node-1 and 448
node-2 router groups blocked specifically on the shared admission slot. The
admitted groups remained at 192 per node while Channel RPC worker occupancy was
only 37.5 percent and store-apply queue occupancy 53.5 percent. Item-weighted
router-batch/message-submitter P99.9 reached 1.819 seconds. The shared boundary
successfully protected downstream capacity, but 192 was too small to drain a
four-to-five-batch recovery chain inside the SENDACK deadline.

An initial six-minute 256-group continuation timed out after 4m40.5s. It first
recovered from 4,173 pending messages at minute three to 471 at minute four,
then timed out one SENDACK with 12,217 pending. Aggregate SENDACK/RECV P99
reached 3.379/3.041 seconds and store-apply pacing reached 33,238. That run was
later found to have occurred while accumulated E2E data had reduced free disk
space to only a few GiB; the following run filled the disk completely. Its
storage-pressure shape is retained as evidence, but it is not a clean
comparison that can reject 256 by itself.

After reclaiming a full local disk and waiting for APFS activity to settle, the
clean six-minute 224-group continuation passed with substantial margin. It
drained all 1.62 million sends with exact delivery, zero pending messages,
about 4,498.7 completions/s, and SENDACK/RECV P99 374/327 ms. Shared admission
reached exactly 224/224 while Channel RPC worker occupancy peaked at 41.7
percent and store-apply queue occupancy at 36.1 percent. Every Channel RPC and
store-apply pacing/full counter stayed zero. Gateway dispatch/handler P99 was
236/249 ms and item-weighted router-batch/message-submitter P99 was 249 ms.
The run remained stable through read amplification 24 and 260 MB compaction
debt, so 224 qualifies a new strict ten-minute attempt.

That strict 224-group attempt failed after only 50.3 seconds. SENDACK/RECV P99
reached 3.882/3.625 seconds and 15,918 messages were pending, but Channel RPC
worker occupancy was only 40.6 percent, store-apply queue occupancy 29.1
percent, store-apply pacing zero, and every admission-full counter zero. The
failure snapshot found exactly 224 admitted groups (178 remote forwards and 46
local futures) plus 411 groups waiting on shared admission. This clean run
therefore confirms that 224 still under-provisions an initial recovery wave.
The candidate returns to 256 with per-batch 96 unchanged; 256 must repeat the
six-minute gate on the cleaned disk before another strict attempt.

The clean six-minute 256-group rerun passed. It drained all 1.62 million sends
with exact delivery, zero pending messages, about 4,498.3 completions/s, and
SENDACK/RECV P99 554/489 ms. Shared admission reached 256/256 while Channel RPC
worker occupancy peaked at 38.5 percent and store-apply queue occupancy at 25.9
percent. Every admission-full counter and store-apply pacing stayed zero; RPC
recorded only one Pull and 27 PullHint soft-pacing events. Gateway
dispatch/handler P99 was 240/369 ms and item-weighted router-batch/message-
submitter P99 was 362 ms. The run stayed stable through read amplification 24
and 277 MB compaction debt, qualifying 256 for a clean strict ten-minute
attempt.

The profile-enabled strict 256 attempt stayed healthy through minute seven,
where pending was 258 and all pacing/full counters remained zero, then timed
out one SENDACK after 7m26.4s. Aggregate SENDACK/RECV P99 was still only
233/216 ms, but the abrupt recovery wave left 22,405 pending and filled shared
admission. The scheduled goroutine snapshot had no admission waiters; 26
seconds later node 1/2 failure stacks had 128/222 waiters while admitted groups
were full. Over the same interval checkpoint tasks rose from 140 to 6,605, but
checkpoint/store/RPC queues and completed-stage P99 remained below their
bounds. CPU, heap, and goroutine capture all began at the exact seven-minute
boundary. Before changing product capacity or checkpoint scheduling, repeat
strict ten minutes without the scheduled profile directory to distinguish a
diagnostic-induced recovery wave from an organic product boundary.

The no-profile strict control reproduced the same cliff after 7m18.4s. At
minute seven only 335 messages were pending and 127 checkpoints had been
accepted; at failure 22,393 messages were pending and accepted checkpoints had
jumped to 6,563. SENDACK/RECV P99 was still 274/244 ms, every hard-full counter
and membership mutation count remained zero, and the scheduled profile work
was absent. Diagnostic capture is therefore not the cause. Dividing checkpoint
worker concurrency from the normal apply-derived bound by another factor of
four also failed at the same 7m18s boundary, with 6,819 accepted checkpoints,
and was reverted. Checkpoint activity is a recovery symptom rather than the
trigger.

The storage evidence identified the trigger. With one compaction, message LSM
read amplification progressed through approximately 5, 10, 15, 21, 24, and
26 while compaction debt continued to grow. Pebble stops L0 writes at read
amplification 24, but the engine previously fixed compaction concurrency to
one. The engine now retains one baseline compaction and permits one additional
compaction only when Pebble's own L0-read-amplification or compaction-debt
pressure heuristic requests it. The first strict run with that reactive slot
processed and drained all 2.7 million messages instead of hitting the old
cliff: checkpoint tasks stayed at 199 and all hard-full counters stayed zero.
It missed only the latency gate, with SENDACK/RECV P99 1.347/1.082 seconds,
while sharing a visibly slower host.

An exact no-profile rerun after the host and disk settled passed the strict
gate. It processed and drained all 2.7 million messages with exact receiver
counts, 4,500.001 ingress/s, 4,498.690 completions/s, and SENDACK/RECV P99
433/350 ms. Gateway dispatch/handler P99 was 245/250 ms and item-weighted
router-batch/message-submitter P99 was 249 ms. The second compaction activated
under pressure, read amplification reached 26, compaction debt reached 454 MB,
and checkpoints stayed at 183. Channel RPC, store-apply, checkpoint, and
transport hard-full counts, delivery errors, and membership mutations were all
zero. This qualifies the reactive two-slot upper bound and the 256 shared
router admission boundary for the unchanged 30-minute candidate; it does not
change the product gateway batch default, because 128 records remains the
explicit test override.

The unchanged 30-minute candidate exposed a higher sustained boundary after
11m49.6s and 3,193,360 completed SEND calls. A first recovery wave at minute
three raised pending to 12,054 and checkpoints to 750, then self-drained by
minute four while checkpoints eventually settled at 11,416. The run remained
near-real-time through minute eleven, but a second wave left 22,371 messages
pending, raised checkpoints to 17,668, and timed out a SENDACK. Aggregate
SENDACK/RECV P99 reached 2.641/2.473 seconds; gateway dispatch and item-weighted
message submitter P99 were 2.335 seconds and 636 ms. Permission P99 was only 24
ms, physical commit P99 45 ms, every hard-full counter and membership mutation
count remained zero, and all processes remained continuous.

Failure stacks located the concentrated wait on node 2, which owns four of the
ten logical Slot leaders: 195 authority append effects, 134 inbound append RPC
handlers, and 60 local router groups were waiting for Channel append futures.
Channel RPC workers peaked at 77.1 percent and the store-apply queue at 50.8
percent, so neither downstream pool was exhausted. Message LSM read
amplification reached 27, compaction debt 446 MB, and both permitted
compactions were active. Pebble adds another compaction at each multiple of its
L0 pressure threshold, but the engine's upper bound of two prevented the next
pressure step before the configured L0 write-stop boundary. The next
single-variable candidate therefore keeps one baseline compaction and raises
only the reactive upper bound from two to three. This explanation remains a
candidate until the six-minute and strict ten-minute gates confirm that the
third slot engages without creating a new latency tail.

The engine range was changed from `[1,2]` to `[1,3]`, leaving Pebble's own
pressure heuristic in control of when the extra slots appear. Its clean
six-minute gate processed and drained all 1.62 million messages with exact
receiver counts, 4,500.002 ingress/s, 4,498.173 completions/s, and SENDACK/RECV
P99 233/214 ms. Gateway dispatch/handler P99 was 193/241 ms, item-weighted
router-batch/message-submitter P99 was 239 ms, checkpoints stayed at 117, and
all hard-full, delivery-error, and membership-mutation counters stayed zero.
Read amplification reached 19 and only two compactions were needed in that
window.

The first strict ten-minute run proved that the third slot activates: maximum
compactions reached three at aggregate read amplification 26. All 2.7 million
messages were exact, drained, and process-continuous, but a minute-four
checkpoint wave raised gateway dispatch P99 to 1.350 seconds and SENDACK/RECV
P99 to 1.807/1.598 seconds. Because the wave began four minutes before the
third compaction first activated at minute eight, it is not evidence that the
new slot caused the tail. Recurrent unrelated three-node tests were also
observed on the shared host around adjacent attempts, so that run is retained
as trigger/correctness evidence rather than latency qualification.

An exact strict rerun after a monitored quiet window passed. It processed and
drained all 2.7 million messages with exact receiver counts, 4,500.002
ingress/s, 4,498.986 completions/s, and SENDACK/RECV P99 243/223 ms. Gateway
dispatch/handler P99 was 210/244 ms, item-weighted
router-batch/message-submitter P99 was 242 ms, checkpoints stayed at 112, and
all hard-full, delivery-error, and membership-mutation counters stayed zero.
Aggregate read amplification reached 26 and debt about 497 MB; this particular
run needed only two compactions. Together, the two strict runs prove both that
the third slot can engage and that the `[1,3]` range passes the six- and
ten-minute gates. The unchanged 30-minute target remains the final
qualification.

The monitored 30-minute attempt disproved the default ten-sublevel concurrency
step for this workload. It remained near-real-time through minute ten and
activated the third compaction at aggregate read amplification 26, with only
680 pending messages and 115 checkpoints. About 32 seconds later one SENDACK
timed out after 2,842,869 completed calls: pending jumped to 22,495 and
checkpoints to 7,048. Aggregate SENDACK/RECV P99 was still only 263/239 ms,
showing a cliff rather than sustained under-capacity. Message compaction debt
reached about 519 MB, all three compactions were active, every hard-full and
membership-mutation counter remained zero, and all processes were continuous.
The precise two-second monitor recorded no overlapping WuKongIM workload.

Failure state again concentrated on node 2, the authority for four logical
Slots: its Channel append writer had 218 in-flight effects versus 30 and 28 on
nodes 1 and 3. Channel RPC worker occupancy peaked at 76 percent and the
store-apply queue at 38 percent, so those pools were not exhausted. Pebble's
default L0 concurrency step of 10 starts the third slot at depth 20, leaving
only four sublevels before the configured write stop at 24. The next
single-variable candidate keeps the `[1,3]` range but sets the concurrency step
to 8. That aligns the second slot with the normal L0 compaction threshold and
starts the third at depth 16, leaving a full eight-sublevel response window
without increasing peak compaction concurrency.

The clean six-minute gate with an eight-sublevel concurrency step passed. It
processed and drained all 1.62 million messages with exact receiver counts,
4,500.003 ingress/s, 4,498.842 completions/s, and SENDACK/RECV P99 239/219 ms.
Gateway dispatch/handler P99 was 205/243 ms, item-weighted
router-batch/message-submitter P99 was 241 ms, checkpoints stayed at 110, and
all hard-full, delivery-error, and membership-mutation counters stayed zero.
Read amplification reached 20, debt about 246 MB, and only two compactions were
needed in this window. The earlier threshold therefore shows no six-minute
foreground-latency regression; strict ten minutes remains the next gate.

The first step-8 strict attempt exposed a separate router-admission boundary
before storage pressure became material. One SENDACK timed out after 166.7
seconds of measured load and 749,987 completed calls. Aggregate SENDACK/RECV
P99 was still 303/275 ms, but pending jumped to 22,359 and accepted checkpoints
from 121 at minute two to 5,214 at failure. Shared router admission reached
256/256; the node-1 failure profile had 354 router workers waiting in
`acquireGroup`, while node 2 had 366 append effects in flight. In contrast,
Channel RPC workers peaked at 45.8 percent, store-apply queue occupancy at 43.6
percent, every hard-full and membership-mutation counter stayed zero, and
message storage reached only read amplification 12, 73.5 MB debt, and two
compactions. The 256 limit, not Pebble write stop, was therefore the immediate
owner of this early recovery failure.

The router remains bounded but now retains five batch-local submission waves:
the per-batch limit stays 96 and the shared node-local limit is 512. The clean
six-minute step-8/global-512 gate processed and drained all 1.62 million
messages with exact receiver counts, 4,500.002 ingress/s, 4,498.107
completions/s, and SENDACK/RECV P99 245/224 ms. Router admission peaked at only
215/512, checkpoints stayed at 121, all hard-full, delivery-error, and
membership-mutation counters stayed zero, and no external WuKongIM workload
overlapped. Read amplification reached 22, debt about 239 MB, and two
compactions. Strict ten minutes remains the next gate for this combined
candidate.

The clean strict ten-minute step-8/global-512 gate also passed. It processed
and drained all 2.7 million messages with exact receiver counts, 4,500.000
ingress/s, 4,499.248 completions/s, and SENDACK/RECV P99 352/293 ms. Gateway
dispatch/handler P99 was 239/247 ms, item-weighted
router-batch/message-submitter P99 was 246 ms, router admission peaked at
338/512, and checkpoints stayed at 136. The third compaction activated during
the measured window; read amplification reached 26 and debt about 435 MB.
Every hard-full, delivery-error, and membership-mutation counter stayed zero,
all processes were continuous, and the run had no external WuKongIM overlap.
The six- and ten-minute gates are qualified; the unchanged 30-minute target is
the remaining gate.

The unchanged 30-minute step-8/global-512 candidate then failed after 15m42.6s
of measured load and 4,241,585 completed SEND calls. It had remained
near-real-time through minute fifteen with only 272 pending messages and 153
accepted checkpoints. One SENDACK timeout then left 22,404 messages pending and
raised checkpoints to 6,588. Aggregate SENDACK/RECV P99 was still 661/515 ms,
all hard-full and membership-mutation counters remained zero, and no external
WuKongIM workload overlapped the run. Router admission reached its bounded
512/512 limit, but the failure stacks localized the blocking chain on node 3:
126 authority append schedulers, 84 inbound append RPC handlers, and 29 local
router groups were waiting for Channel append futures while leader/follower
workers waited to submit commits. Node 3 owns only three of the ten logical
Slots, so the stall was not explained by the earlier four-Slot authority skew.

Storage pressure was the distinguishing late-run signal. Read amplification
reached 26, compaction debt about 640 MB, and all three permitted compactions
were active; Channel RPC workers reached 94.8 percent but neither their queue
nor the store queues filled. Pebble chooses extra compaction concurrency from
the maximum of L0 depth and compaction-debt signals. The upstream debt step is
1 GiB, so debt did not contribute here. With the configured eight-sublevel L0
step, a fourth slot based only on L0 depth would not open until depth 24, the
same point as the configured write stop. The next candidate must therefore
retain bounded router admission and add storage recovery capacity before that
boundary, then repeat the six- and ten-minute gates before another 30-minute
attempt.

The bounded candidate raises the reactive range to `[1,4]`, retains the L0
step of 8, and sets Pebble's compaction-debt step to four configured memtables
(128 MiB with the default 32 MiB memtable). The fourth slot can therefore open
at 384 MiB debt instead of waiting for L0 depth 24. This keeps the prior
six-minute 239 MiB region at no more than two debt-selected compactions while
ensuring the prior strict ten-minute 435 MiB region exercises the fourth slot
before the final gate. These are trigger expectations, not qualification
results; the six- and ten-minute reruns must prove foreground latency and
actual concurrency before another 30-minute attempt.

One six-minute execution of this candidate completed functionally with all
1.62 million messages exact and drained, SENDACK/RECV P99 272/247 ms, 4,496.9
completions/s, zero hard-full or membership mutations, 114 checkpoints, read
amplification 22, about 269 MB debt, and three compactions. It is not a
qualification result because an unrelated scripts integration run overlapped
its final 53 seconds. Two other attempts were stopped during setup or measured
load as soon as separate repository test pipelines appeared. The candidate
therefore remains unqualified until the whole six-minute window runs on a
quiet host.

The later clean six-minute rerun qualified this short gate. It processed and
drained all 1.62 million messages with exact receiver counts, 4,500.003
ingress/s, 4,498.575 completions/s, and SENDACK/RECV P99 227/209 ms. Gateway
dispatch/handler P99 was 194/242 ms, item-weighted router-batch/message-
submitter P99 was 241 ms, router admission peaked at 232/512, and checkpoints
stayed at 116. Every hard-full, pacing, delivery-error, and membership-mutation
counter stayed zero; all processes were continuous and no external workload
overlapped. Read amplification reached 23, debt about 241 MB, and only two
compactions were needed. The debt-triggered fourth slot therefore did not
regress the six-minute foreground gate. Strict ten minutes remains required
and must observe the fourth slot before the 30-minute target proceeds.

The clean strict ten-minute rerun then qualified the peak recovery behavior.
It processed and drained all 2.7 million messages with exact receiver counts,
4,500.001 ingress/s, 4,499.268 completions/s, and SENDACK/RECV P99 248/228 ms.
Gateway dispatch/handler P99 was 227/245 ms, item-weighted router-batch/message-
submitter P99 was 244 ms, router admission peaked at 308/512, and checkpoints
stayed at 192. Every hard-full, delivery-error, and membership-mutation
counter stayed zero; all processes were continuous and no external workload
overlapped. Read amplification reached 27 and debt about 435 MB. Crucially,
the fourth compaction activated after debt crossed the 384 MiB threshold, so
this gate directly exercised the added capacity rather than merely passing
below it. The unchanged 30-minute target is now the remaining gate.

## Resolved 4,500 QPS Limit

After the readiness fix, the full local 4,500 QPS run completed without a
disconnect and kept ordinary membership mutations at zero. It offered and
ingressed 4,500 messages/s but completed about 3,621/s; SENDACK P99 was 1.67
seconds, above the existing one-second acceptance limit, and RECV P99 was 1.73
seconds. A separate profile-enabled control produced the same conclusion at
about 3,700 completions/s and 1.58-second SENDACK P99.

Those profiles correctly showed no conversation or membership CPU hotspot, but
the aggregate CPU view hid request-level queueing inside a synchronous gateway
batch handler. Stage counters and live goroutine stacks exposed the sequential
permission Slot RPC phase. Bounded permission concurrency reduced gateway
async-dispatch wait without changing Channel RPC batch size, gateway queue
capacity, channel cardinality, or conversation behavior, and the unchanged
4,500 QPS gate then passed with P99 below 500 ms.

## Verification

- `GOWORK=off go test ./pkg/slot/proxy -count=1`: passed.
- `GOWORK=off go test -tags=integration ./pkg/slot/proxy -count=1`: passed.
- Focused E2E acceptance/unit contracts for the medium recipient gate: passed.
- 20,000-message, 500 QPS strict medium-recipient acceptance: passed.
- Three-node 25/100/200 directory performance acceptance: passed.
- 100,000-member group conversation/SEND invariant: passed.
- `GOWORK=off go test -race ./internal/usecase/message ./pkg/goroutine -count=1`: passed.
- `GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`: passed.
- 20,000-message, 4,500 QPS strict medium-recipient acceptance: passed on the
  unchanged rerun; the immediately preceding run missed only the ingress clock
  threshold by 0.02 percent while meeting latency and mutation requirements.
- Permission-soak configuration, bounded-latency/in-flight tracking, route-mix,
  acceptance, counter, and public-metric parser contracts: passed.
- 10-second, 45,000-message, 4,500 QPS permission-soak diagnostic: passed with
  zero permission RPC errors and zero membership writes.
- `GOWORK=off go test ./pkg/channel/... -count=1`: passed.
- `GOWORK=off go test -tags=integration ./pkg/raftlog -count=1`: passed.
- 130-second, 585,000-message, 5,000-channel permission-soak diagnostic:
  passed with all receiver counts exact, zero read timeouts, zero membership
  writes, measured stage P99 attribution, and heap below both thresholds.
- Strict 10-minute candidates: one failed SENDACK P99 at 2.174 seconds; the
  quiet-host rerun reached 971 ms with exact delivery but recorded 1,287
  aggregate Channel RPC admission-full events. Three typed reruns kept Pull,
  PullHint, and aggregate RPC admission-full at zero but exposed pause-recovery
  checkpoint bursts. The latest high-water candidate reduced checkpoint full
  from 16,030/38,829 to zero, then failed one SENDACK during a host pause after
  accepting 3,130 redundant maintenance checkpoints. Initial class-aware RPC
  pacing kept all typed admission-full counts at zero but failed one SENDACK at
  7m47s; a two-compaction experiment failed earlier and was reverted. The
  tighter 25% PullHint / 50% Pull pacing candidate failed at 6m28s after the
  store-apply queue saturated. Store-apply-aware Pull admission eliminated
  apply-full but failed at 5m08s. A P99.9-instrumented exact rerun then located
  the remaining tail in follower recovery. Relaxing Pull from 50% to 75% made
  the timeout occur after only 54 seconds and was reverted. A short exact
  amplification slice then isolated delayed initial PullHints from zero-paced
  Pulls. Reason-aware PullHint pacing brought the nonterminal latency back
  below the target; a bounded reactor due-work turn removed the subsequent Pull
  timeout signature, leaving a gateway/append recovery wave to quantify. A
  measured baseline then showed gateway batch-record P99 near 236; limiting
  one session's SEND batch to 64 records passed a two-minute, 540,000-message
  A/B with batch-record P99 31.9 and SENDACK P99 356 ms, but its six-minute
  continuation still timed out at 4m50.8s and exposed the growing per-message
  idempotency Pebble point lookup as the next upstream owner. The following
  one-minute exact run with the bounded filter and unchanged gateway batch
  default completed all 270,000 sends with SENDACK P99 336 ms, zero typed
  admission-full, 268,300 negative skips, and one point read. Its six-minute
  continuation drained all 1.62 million sends with zero full counters and only
  780 point reads, but SENDACK P99 was 1.279 seconds while gateway dispatch P99
  reached 702 ms and batch-record P99 reached 52.2.
  The 128-record continuation then drained another 1.62 million sends with zero
  full counters and exact delivery; SENDACK P99 was exactly 1,000 ms, RECV P99
  711 ms, gateway dispatch/handler P99 560/486 ms, and complete router-batch P99
  247 ms.
  A six-minute 128-group router continuation was rejected at SENDACK P99 1.337
  seconds after RPC worker occupancy reached 100 percent. The following
  96-group continuation passed with SENDACK/RECV P99 984/640 ms, exact
  delivery, zero Channel RPC/store-apply admission-full, RPC worker occupancy
  80.2 percent, and zero pending messages.
- The clean six-minute shared-224 continuation drained all 1.62 million sends
  with exact delivery, SENDACK/RECV P99 374/327 ms, zero pending, zero
  Channel RPC/store-apply pacing or admission-full, RPC worker occupancy 41.7
  percent, and store-apply queue occupancy 36.1 percent.
- The following strict shared-224 attempt failed after 50.3 seconds with 15,918
  pending messages and 411 groups waiting at the full shared boundary while
  downstream admission-full remained zero; 224 is rejected for strict use.
- The clean six-minute shared-256 rerun drained all 1.62 million sends with
  exact delivery, SENDACK/RECV P99 554/489 ms, zero pending, zero
  admission-full, RPC worker occupancy 38.5 percent, and store-apply queue
  occupancy 25.9 percent.
- The profile-enabled strict shared-256 attempt failed one SENDACK after 7m26.4s
  even though aggregate SENDACK P99 remained 233 ms; the recovery wave began
  immediately after simultaneous scheduled profiles, so a no-profile strict
  control was required before another product change.
- The no-profile control failed at the same boundary after 7m18.4s: pending
  rose from 335 to 22,393 and checkpoint tasks from 127 to 6,563 while all
  hard-full counters remained zero. Reducing checkpoint worker concurrency
  reproduced the same failure and was reverted, proving the checkpoint wave
  was downstream of the trigger.
- Allowing Pebble one pressure-triggered compaction in addition to its one
  baseline compaction removed the L0 write-stop cliff. Its first strict run
  drained all 2.7 million messages with only 199 checkpoints and zero hard-full
  counters, but missed the latency gate at SENDACK P99 1.347 seconds on a
  slower host. The exact quiet-host rerun passed with SENDACK/RECV P99 433/350
  ms, 4,498.690 completions/s, exact receiver counts, zero pending, 183
  checkpoints, zero hard-full counters, and zero membership mutations.
- The unchanged 30-minute target failed after 11m49.6s and 3,193,360 completed
  SEND calls. A second recovery wave left 22,371 pending with SENDACK/RECV P99
  2.641/2.473 seconds and 17,668 accepted checkpoints, while all hard-full
  counters and membership mutations stayed zero. Failure stacks concentrated
  on node 2 authority append futures; both allowed compactions were active at
  read amplification 27 and 446 MB debt. A reactive upper bound of three is
  the next single-variable candidate.
- The reactive `[1,3]` range passed its clean six-minute gate with SENDACK/RECV
  P99 233/214 ms, 4,498.173 completions/s, exact delivery, zero pending, 117
  checkpoints, zero hard-full counters, and zero membership mutations. A first
  strict run activated all three compactions and preserved exact delivery and
  drain, but an earlier unrelated pressure wave made SENDACK P99 1.807 seconds.
  The monitored quiet-host strict rerun passed with SENDACK/RECV P99 243/223
  ms, 4,498.986 completions/s, exact delivery, zero pending, 112 checkpoints,
  zero hard-full counters, and zero membership mutations. This qualifies the
  six- and ten-minute gates.
- The monitored 30-minute attempt failed at 10m31.7s after the third compaction
  activated: 22,495 messages were pending and checkpoints jumped from 115 to
  7,048, while aggregate SENDACK/RECV P99 remained 263/239 ms, all hard-full
  counters stayed zero, and no external WuKongIM workload overlapped. The
  default ten-sublevel concurrency step leaves only four sublevels between the
  third slot and write stop. The next unqualified candidate keeps `[1,3]` but
  advances the concurrency step to 8, opening the extra slots at depths 8 and
  16.
- The step-8 candidate passed its clean six-minute gate with SENDACK/RECV P99
  239/219 ms, 4,498.842 completions/s, exact delivery, zero pending, 110
  checkpoints, zero hard-full counters, and zero membership mutations. Read
  amplification reached 20 with two active compactions.
- Its first strict attempt failed early at 2m46.7s of measured load with
  22,359 pending messages and 5,214 checkpoints. Storage had reached only read
  amplification 12 and 73.5 MB debt, but shared router admission was 256/256
  and node-1 had 354 workers waiting for a slot while downstream RPC/store
  capacity remained available. The bounded shared limit is now 512 with the
  per-batch limit unchanged at 96.
- The step-8/global-512 candidate passed a clean six-minute gate with
  SENDACK/RECV P99 245/224 ms, 4,498.107 completions/s, exact delivery, zero
  pending, 121 checkpoints, zero hard-full counters, and zero membership
  mutations. Router admission peaked at 215/512; read amplification reached 22
  with two active compactions.
- Its clean strict ten-minute gate also passed with SENDACK/RECV P99 352/293
  ms, 4,499.248 completions/s, exact delivery, zero pending, 136 checkpoints,
  zero hard-full counters, and zero membership mutations. Router admission
  peaked at 338/512; read amplification reached 26, debt about 435 MB, and all
  three compactions activated.
- The unchanged 30-minute continuation failed at 15m42.6s after 4,241,585
  completed calls. Pending messages jumped from 272 at minute fifteen to
  22,404 and checkpoints from 153 to 6,588, while SENDACK/RECV P99 remained
  661/515 ms, all hard-full and membership counters stayed zero, and no
  external workload overlapped. Failure stacks concentrated on node 3 Channel
  append futures and commit submission. Read amplification reached 26, debt
  about 640 MB, and all three allowed compactions were active. Pebble's default
  1 GiB debt step contributed no extra concurrency; an L0-only fourth slot
  with step 8 would begin at the write-stop depth 24. The 30-minute target
  remains open. The next unqualified candidate uses a bounded `[1,4]` range
  and one extra slot per four memtables (128 MiB by default) of debt, so the
  fourth slot may open at 384 MiB without changing the L0 step.
- The clean `[1,4]`/128 MiB-debt-step six-minute rerun passed with all 1.62
  million messages exact and drained, SENDACK/RECV P99 227/209 ms, 4,498.575
  completions/s, 116 checkpoints, and zero hard-full, pacing, or membership
  mutations. Router admission peaked at 232/512; read amplification reached
  23, debt about 241 MB, and only two compactions were needed. Strict ten
  minutes remains the next gate and must prove the fourth slot activates.
- The clean strict ten-minute `[1,4]`/128 MiB-debt-step rerun passed with all
  2.7 million messages exact and drained, SENDACK/RECV P99 248/228 ms,
  4,499.268 completions/s, 192 checkpoints, and zero hard-full or membership
  mutations. Router admission peaked at 308/512; read amplification reached
  27, debt about 435 MB, and the fourth compaction activated after the 384 MiB
  debt threshold. This qualifies the peak recovery behavior; the unchanged
  30-minute target remains open.
- The following `[1,4]`, L0-step-8 30-minute attempt failed at 13m07.4s after
  3,543,308 completed calls. It left 22,360 messages pending and 6,566 accepted
  checkpoints; SENDACK/RECV P99 was 257/236 ms before the terminal wave. Read
  amplification reached 26, debt about 544 MB, and all four regular compaction
  slots were active. Advancing only the L0 concurrency step from 8 to 6 moved
  the fourth L0-triggered slot from depth 24 to depth 18.
- The L0-step-6 candidate passed clean six- and ten-minute gates with exact
  drain and zero hard-full or membership mutations. The six-minute run reached
  SENDACK/RECV P99 225/208 ms, read amplification 19, and about 267 MB debt;
  the ten-minute run reached 235/216 ms, read amplification 26, and about
  451 MB debt. Its 30-minute attempt still failed at 14m55.9s after 4,031,574
  completed calls with 22,404 pending and 6,537 checkpoints. Read amplification
  reached 28 and debt about 565 MB. Earlier concurrency alone delayed but did
  not remove the L0 write-stop cascade.
- Giving only the high-write message engine a 64 MiB memtable, while retaining
  the shared 32 MiB default elsewhere, halved L0 sublevel creation without
  violating the heap budget. Its six- and ten-minute gates drained all 1.62
  million and 2.7 million messages with SENDACK/RECV P99 234/215 and 223/206 ms;
  read amplification peaked at 12 and 20, and aggregate heap at 657 and
  721 MB. The first 30-minute attempt nevertheless failed at 20m16.4s after
  5,474,011 completed calls, with 22,455 pending, read amplification 26, debt
  about 856 MB, and four active compactions. The larger memtable had
  unintentionally doubled the derived debt-concurrency step from 128 to
  256 MiB, delaying the fourth debt-triggered slot until roughly minute 18.
- The final message-engine candidate decouples those controls: a 64 MiB
  memtable reduces flush cadence while an explicit 128 MiB debt step preserves
  timely recovery. Its strict ten-minute gate drained all 2.7 million messages
  at 4,498.822 completions/s with SENDACK/RECV P99 256/236 ms, read
  amplification 19, debt about 367 MB, zero pending, zero hard-full counters,
  and zero membership mutations.
- The unchanged final 30-minute gate passed. All 8.1 million sends and 16.2
  million receiver deliveries were exact and drained at 4,499.684
  completions/s; SENDACK/RECV P99 was 379/320 ms. Every Channel RPC,
  store-apply, checkpoint, transport, and permission admission/rejection error
  stayed zero, membership mutation rows stayed zero, and all processes were
  continuous. Router pressure peaked transiently at 512/512 but self-drained;
  aggregate heap peaked at 859 MB, below the 1.5 GiB limit. Read amplification
  peaked at 26 and compaction debt at about 951 MB; both formed a sustained
  plateau over the final minutes with the fourth regular recovery slot already
  active. This qualifies the reviewed 30-minute, 4,500-QPS, 5,000-channel
  target for the combined candidate.
- `git diff --check`: passed.

## Qualification Decision Trail

The following trail records how the final qualified settings were selected.
Item-weighted evidence rejected permission as the owner, so its four-Slot
worker bound remains unchanged. The router's 96-group per-batch default passed the six-
minute late-pressure region, but the strict run proved that per-batch control
does not bound aggregate concurrent sessions. Keep the shared node-local limit
observable through inflight/capacity gauges. The initial 192 capacity protected
downstream pressure but was too tight for a strict-run recovery wave. The 192
limit's two-minute
exact A/B passed with SENDACK P99 315 ms and no downstream pacing or full
counters; the six-minute continuation passed with SENDACK P99 674 ms, exact
delivery, zero full counters, and RPC worker occupancy 41.7 percent, but the
following strict attempt timed out one SENDACK with hundreds of groups queued
at the 192-slot boundary. The clean 224 six-minute candidate drained all 1.62
million messages with SENDACK P99 374 ms, zero pending, zero pacing/full
counters, RPC worker occupancy 41.7 percent, and store-apply queue occupancy
36.1 percent, but its strict attempt failed after 50.3 seconds with 411 groups
waiting at the full 224-slot boundary and downstream capacity still free. The
older 256 failure was confounded by a nearly full disk, so the candidate returns
to 256. Its clean six-minute rerun drained all 1.62 million messages with
SENDACK P99 554 ms, zero pending, zero admission-full, RPC worker occupancy
38.5 percent, and store-apply queue occupancy 25.9 percent. Preserve
same-session admission and SENDACK ordering. Run the strict ten-minute gate with
256 shared slots without scheduled profiles as the next single-variable
control. That control first exposed the organic single-compaction L0 write-stop
and then passed after the engine retained one baseline compaction while
allowing a second only under Pebble pressure. A later step-8 strict attempt
failed much earlier with shared router admission fixed at 256 while storage was
below the L0 danger region and downstream queues had headroom.
Keep the 96 per-batch group bound, retain bounded admission, and use 512 shared
slots for the next strict control; its clean six-minute gate reached only
215/512 and passed all latency and correctness criteria. Its strict ten-minute
gate also passed with 338/512 peak admission, exact drain, and all three
compactions active. Run the unchanged 30-minute target with this combined
candidate.
The first unchanged 30-minute attempt failed at 11m49.6s after both permitted
compactions were active and pressure concentrated on the four-Slot authority
node without exhausting its RPC or store pools. Raise only Pebble's reactive
compaction upper bound from two to three: keep one baseline slot, and let the
third appear only at the next L0-pressure multiple. The `[1,3]` candidate
passed clean six-minute and strict ten-minute gates but failed the monitored
30-minute target because the default concurrency step started its third slot
too close to write stop. Keep the same concurrency range and advance only the
L0 concurrency step from 10 to 8, then requalify the six-minute, strict
ten-minute, and 30-minute gates. Abort and repeat any run that overlaps another
local WuKongIM workload, because shared-disk interference has already produced
non-repeatable gateway tails. The 128-record gateway value remained a
diagnostic override during storage qualification. A subsequent product-default
decision accepted that qualified value after the complete 30-minute gate; the
default is now 128 records while same-session admission and SENDACK ordering
remain unchanged.
The storage qualification is now complete. Keep the bounded `[1,4]` reactive
range, L0 concurrency step 6, message-only 64 MiB memtable, explicit 128 MiB
message debt step, 96 per-batch router group bound, and 512 shared router
capacity. Do not recouple debt concurrency to the message memtable size: doing
so delayed the fourth slot by roughly eight minutes and failed the long gate.
The 30-minute pass establishes bounded behavior for this reviewed local shape;
continue exposing read amplification and debt because a materially higher or
longer workload must re-prove convergence rather than extrapolate this result.
The product gateway batch default is now 128 records, matching the exact value
used by the successful 30-minute qualification. Environment and TOML overrides
remain available, and changing the bound does not relax same-session admission
or SENDACK ordering.
Preserve heartbeat, receiver-progress, queue, commit, heap, filter skip/read,
and profile evidence so any late failure has an attributable owner.

## Next Boundary

Keep the exact zero-write, bounded hydration-operation, short 4,500-QPS,
130-second sustained, strict ten-minute, and qualified 30-minute gates
unchanged. Keep the product gateway batch default at 128 records, the
per-batch router bound at 96 groups, and the shared router capacity at 512.
The next qualification boundary is a materially longer or higher-QPS workload,
or a representative multi-host deployment; it must independently re-prove
latency, exact drain, memory bounds, and LSM convergence rather than extrapolate
the reviewed local result.
