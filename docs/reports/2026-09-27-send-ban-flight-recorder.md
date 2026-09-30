# Bounded recent execution capture for the sustained EOF

The clean repaired-node run still failed at SEND 1701.108 seconds with EOF and
one `async_dispatch_queue_full` close. Permission busy was zero. The controlled
70ms WAL completion experiment explains the separate per-Slot permission busy
amplification; it does not establish the cause of this long-run connection close.

## Discriminating evidence

The ranked predictions remain storage/compaction wait, scheduler/GC delay, then
gateway-local contention. A queue-full counter identifies the close trigger,
not why the relevant worker stopped making progress. Capture recent execution
before post-failure HTTP scrapes can replace the causal window. Do not increase
queues, timeouts or admission limits to obtain a pass.

This experiment adds Go 1.25.11 flight recorders to an isolated overlay of the
frozen repaired node source `/lab/handoff-src`. Product dependencies and all
business paths remain unchanged. The product worktree is not edited. Two added
command files consume a private opt-in before configuration parsing and expose
one task-owned Unix control socket per process. The diagnostic harness adds
its own recorder before measured SEND and concurrently requests all four
snapshots at the first observed send/read failure, after freezing driver
statistics and before failure HTTP scrapes. Successful completion also captures
once; deferred cleanup cannot overwrite the first snapshot.

Each recorder requests a 10-second recent window and 32-MiB size hint. Both are
runtime hints; size can shorten the time window and neither is a hard memory
bound. A separate writer enforces a hard 64-MiB artifact cap and preserves
partial-write, cap, close and snapshot errors. Socket requests have a 20-second
deadline, but that deadline cannot forcibly interrupt a runtime trace flush or
filesystem write. The existing process timeout and runner resource guard remain
final bounds. Captures are written once, with exclusive file creation, PID,
node config path, byte counts, completion state and timestamps. No periodic
trace files are emitted.

## Test-first preparation and positive control

`assets/send-ban-flight-20260927/failure-cases.md` predates the implementation.
The retained RED run failed on undefined implementation symbols. Fast writer,
validation, completion and concurrent single-claim contracts subsequently passed;
the race check also passed. All build commands, source/module hashes, overlays
and frozen binary identities are retained alongside the test sources.

`positive-01` ran the real three-node public WKProto scenario with 25 Channels,
500 SEND/s and a ten-second measured window. It passed 5,000 messages in
22.949 seconds total. All three node records map to distinct node config paths;
the fourth belongs to the harness. All four snapshots were complete and passed
`go tool trace -pprof=sched` parsing. Raw sizes were 1.75 / 8.94 / 9.85 / 8.19 MB,
below their caps; concurrent snapshot collection took 4.954ms. No product process
remained after cleanup. Losslessly compressed traces and SHA-256 identities
make the parser checks independently repeatable.

After all builds, tests and parsing terminated, this task's idle Go build cache
was cleared before any long workload. The retained cleanup record includes the
idle process inventory. No cleanup, compilation or trace parsing is permitted
during the following measured workload.

## Next diagnostic and interpretation limits

Executed `python3 docs/reports/assets/send-ban-flight-20260927/run.py sustained-01`
from the worktree. This retains 5000 Channels, 4500 SEND/s, 30 minutes, 256 hash
Slots, 128 append workers, batch cap 32, and existing zero-rejection/latency gates.
The runner stops its exact owned lab on less than 8 GiB host free disk or its
outer timeout and records that as failed/aborted evidence.

The long run is now terminal; its failed result and execution evidence follow.
Recording changes runtime overhead; even a 30-minute pass is diagnostic and
cannot replace clean R2 acceptance or the three complete R6 comparison pairs.
A failed run with an incomplete or too-short trace remains insufficient for
root attribution. Completed trace parsing does not by itself identify a cause:
inspect the gateway worker's actual wait/wakeup dependencies and event ordering.


## Sustained diagnostic result: the EOF is reproduced

`sustained-01` failed at SEND 359.797339748 seconds with `sender_read: EOF`,
after 1,619,089 SEND calls, pending=6,413 and permission busy=0. Each of the three
nodes subsequently exposed one `async_dispatch_queue_full` close. The partial
SENDACK P99 was 88ms and must not be presented as a complete 30-minute result.
Total runner time was 550.847 seconds, including setup/cold prime and cleanup.
No resource guard fired; minimum sampled host free disk was 12.069 GiB and
cgroup OOM counters stayed zero. Cleanup left only the container's idle sleep.
No additional builds, cleanup or parsing ran during the workload.

All four snapshots are complete and parsed. Raw sizes are 10,515,800 /
35,894,514 / 38,641,487 / 33,637,465 bytes. Concurrent collection took 53.266ms.
The captured windows span 10.307 seconds for the harness and 8.959 / 9.132 /
8.443 seconds for the nodes. The latter demonstrate the documented retention
hint limitation; they are not ten-second guarantees. Full native parsing saw
2,058,213 / 6,448,633 / 6,888,012 / 6,167,320 events without parser errors.

### Evidence that changes the next action

Native stacks distinguish WAL log-writer syncs from SST flush/compaction syncs.
The largest syncs are not all WAL operations. For example node 2's 754.264ms
Fdatasync belongs to `RawRowWriter.Close -> compact1`, while its WAL writer
has consecutive waits culminating in 393.344ms and 361.394ms. All three node
WAL writers show a similar late series of ~119ms, ~199ms, ~390ms, then ~361ms
sync intervals. The ~390ms wave ends at native trace clocks:

| Node | WAL goroutine | Syscall interval (native ns) | Duration |
| --- | ---: | --- | ---: |
| 1 | 1654531 | 23361979054528–23362371191296 | 392.137ms |
| 2 | 1912856 | 23361977834944–23362371179264 | 393.344ms |
| 3 | 1462119 | 23361982031744–23362371194432 | 389.163ms |

Node 1's observed wake edge is `1654531 -> 604` through WAL `syncQueue.pop`,
then `604 ->` commit request consumers through the commit coordinator. The
retained graph continues into Channel durable rounds and reactor completions.
This identifies an actual dependency on WAL completion rather than treating
post-failure goroutine stacks as proof. The short wake graph is a goroutine
scheduling graph: worker reuse and network boundaries still prevent labeling
its entire transitive closure as one SEND request.

Gateway handler goroutines were blocked in
`Router.submitResolvedGroupsEach -> message.SendBatchEach -> OnSendBatch` for
up to 749.014 / 968.481 / 758.918ms on the three nodes. Maximum captured completed
runnable delays were 13.632 / 16.362 / 13.748ms; node GC stop intervals were
at most 1.463 / 2.105 / 1.335ms. Harness runnable delay was at most 7.142ms and
its largest captured GC stop was 4.622ms. These observations favor durable-I/O
waiting over a comparably long Go scheduler/GC stall in this captured window.
Syscall wall time still includes kernel/OS scheduling; it is not pure device
service time and does not establish a physical storage/VM cause.

The fixture explicitly sets gateway SEND workers to 128 and global queue
capacity to 131,072. The actual mailbox calculation yields 512 shards and
256 queued items per shard. Batch cap 32 is not queue capacity. At 4,500 SEND/s
over 25 connections, ideal even pacing is 180/s per connection, so a completely
undrained empty 256-item shard has about 1.422 seconds of backlog allowance.
This is a capacity calculation, not an observed per-shard timeline. The existing
aggregate metrics and native trace do not give exact per-shard occupancy or
map a particular EOF to its oldest outstanding SEND.

### Remaining root-cause boundary

The failed trace supports sustained WAL completion stalls blocking append and
gateway progress, with concurrent compaction/flush synchronization. It does not
yet prove that compaction *caused* the WAL latency, rather than both being slowed
by a shared filesystem/VM/device event. The next investigation must distinguish
those alternatives before choosing storage scheduling or gateway backpressure
changes. Do not treat the largest compaction sync as a WAL event, tune queue
capacity from a mistaken 32-item assumption, or claim this diagnostic repaired
the clean long-run failure. R2 and R6 remain unfulfilled.

The two exploratory compaction wake graphs are retained, including the one
that hit its 2,000-event output cap. That capped graph is explicitly truncated
and is not used as complete causal evidence. Original complete trace artifacts
remain available for another bounded extraction.
