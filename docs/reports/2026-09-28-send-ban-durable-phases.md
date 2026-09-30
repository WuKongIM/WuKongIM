# Proposal-identity evidence locates the durable waiting chain

The repeated WAL-completion hold again produced EOF, now with exact proposal
identities connecting local durability, peer exchange and follower storage.
The selected slow calls wait primarily for the commit coordinator result, while
all sixteen exchange flights to the target are occupied at peer submission.
This explains where completion delay accumulates; it does not establish the
physical cause of the earlier natural stalls or constitute a product repair.
R2 and three complete fresh R6 pairs remain incomplete.

## Frozen experiment and validation

Source is `4a88c8912`; production source remains unchanged. A diagnostic-only Go
overlay adds twelve phase labels with the full command ID, proposal digest,
offset range, peer, native GID and native time. It writes only into the existing
bounded runtime flight recorder while tracing is enabled. IDs are not truncated;
no message body, UID, channel string or public per-identity metric is added.
Queue anchors precede submission attempts. End events mean returned, not success;
only the ready events follow validated closed durable outcomes.

The frozen sequence first checks25 Channels/4500 SEND/s/15s with no hold, then
5000 Channels/4500 SEND/s/60s with real message-WAL completion padded to380ms
only during measured seconds25..30. Both use the original32-record gateway batch
cap and the same product, harness and observer. All six node snapshots verify
512 SEND shards,128 SEND workers,131072 global queued records and256 per shard.
No build, offline trace parsing or cleanup overlapped either measured SEND run.

The positive run passed67500 messages. All twelve phase labels appeared on every
node, and all six directed peer links had complete exact-identity matches.
Capture boundaries and repeated identities remain explicitly missing or ambiguous;
they are not assigned zero latency or silently paired. The full diagnostic failed
after26.541517s and119434 SEND calls, with6898 pending and zero permission busy.
Its prime took2m48.995s; the runner ended normally with only the container sleep
remaining. No OOM or resource guard fired.

All eight captures passed full native decoding, scheduler extraction and SHA-256
gzip roundtrip verification. The phase decoder retained92702/104753/85083 node
events for the positive and95236/125312/104460 for the full diagnostic. Twenty
offline contracts passed, including test-first identity, conservative pairing and
native wait reconstruction checks. The Go probe has RED/GREEN/race evidence;
all replication-package tests and overlay build passed. Production logic was not
modified. Minimum host free space was48,389,033,984/47,700,004,864 bytes.

## Exact proposal example

Node1 proposal command
`eda3fb8f7bfe9b7eaa823217252ba6929f53fa1283219da201c783d1d5e565cf`,
digest `33247541a2f27d304d8cfbf57cb16f8ca83e42700a5624722781e4e9126e592e`,
range23..24 has this leader-native timeline:

| Event | Milliseconds after round begin |
| --- | ---: |
| Local submission / preferred peer3 submission |0.002 /0.005|
| Local store begins |0.080|
| Hedge peer2 submission |25.866|
| Peer3 / peer2 exchange begins |377.856 /387.274|
| Local store returns / validated local ready |765.926 /765.939|
| Peer3 exchange returns / validated peer ready |1145.000 /1145.288|
| Round returns |1145.290|

The local765.846ms store interval shares GID145069 with a765.227ms wait inside
`commit.(*Coordinator).submitResult`, or99.919% of that interval. The matching
proposal on follower3 spends765.976ms in store.Sync;99.361% is coordinator-result
wait. Its follower duration uses its own native clock, not cross-node subtraction.
The preferred peer's377.851ms pre-exchange delay begins with sixteen observed
open exchange GIDs to that target. The hedge likewise encounters sixteen.

Two longest complete rounds per node supply six selected proposals. All twelve
selected peer submission anchors see sixteen observed open flights, matching
the configured per-target limit. Every one of their eighteen matched local or
follower store calls has97.497–99.937% coordinator-result wait. This narrows these
samples away from preparation/lock acquisition and post-storage result delivery.
It does not identify the specific physical commit containing a proposal: the
coordinator result wait covers both queued and executing time. Occupied flights
also do not exclude additional per-Channel ordering barriers.

All three nodes' longest complete rounds are about1.134–1.150s. For the positive
run, round P99 was7.1–7.7ms. The six selected rounds fall before first rejection
using the retained clock-sync extrema; node3 examples end only2.5–2.8ms before it.
Observed offsets are not a guaranteed clock-error bound. The sample is not a
unique mapping to the rejected gateway message or shard, and no such claim is made.

## Queue and I/O evidence

First rejection occurs1.540606s after hold start. Exact frozen shard arithmetic:

| Node/shard | Active records | Active age at rejection | Initial queue + new admissions |
| --- | ---: | ---: | ---: |
|1/3|32|762.583ms|119 +137 =256|
|1/4|32|1150.936ms|48 +208 =256|
|3/2|32|770.144ms|117 +139 =256|

Each has one session and zero arithmetic residual. These are occupied-batch
observations, not inferred full durations from one native select wait.
Each node completed four held syncs and had one still active at capture. Completed
totals were380.014–381.570ms; actual syncs were1.822–14.501ms. The real syscall
still runs once and preserves its error; the artificial completion hold remains
separate. Five fully pre-rejection kernel intervals span1249.520ms with0.0588%
I/O-full pressure, zero CPU-throttle delta and zero memory-some delta.

## Consequence for the repair

Source inspection rejects a mandatory local-sync, then peer-sync, then HW-sync
explanation: `runDurableRound` submits local and preferred peer without waiting
for the local result, and `finishCommit` publishes an in-memory receipt after
durable quorum. The deployed factory uses batched storage; the adapter fallback
loop does not describe this execution.

The evidenced chain is durable-result waiting occupying bounded exchange
flights, later requests waiting to enter exchange, and the gateway joining the
whole active batch while new arrivals accumulate. Merely increasing gateway
batch cap already failed the prior32/128 intervention. Increasing queues, peer
flights or deadlines is not a demonstrated repair. Moving a wait to an unbounded
goroutine or acknowledging before durability would violate the required bounds.

The next repair experiment should examine whether admitted independent-Channel
work can continue through a bounded pipeline without retaining the whole-batch
execution barrier. It must retain a fixed outstanding-record/byte budget across
queued, executing and completed-but-not-published work, same-Channel submission
order, fresh permissions, session ACK order and durable-before-success semantics.
This is a candidate design direction, not a validated fix. Preserve this failing
5000/4500 loop as the immediate check; clean R2 and fresh complete R6 pairs still
follow any real repair. The physical cause of natural filesystem stalls remains
separately unproved.

Artifacts: `assets/send-ban-durable-phases-20260928/manifest.json`.
`run-plan.json` freezes the sequence; `selected-dependencies.json` retains exact
identity joins, open-flight snapshots and same-GID stack intersections;
`verify_evidence.py` reproduces queue arithmetic, archive verification and timing
limitations. No cloud resources, PR, push or release were created.
