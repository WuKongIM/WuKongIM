# Permission busy: execution trace reproduction

The remaining pressure failure is not fixed. This diagnostic narrows the
mechanism beyond `busy`: the captured failure follows a simultaneous WAL-sync
stall and a short burst of permission work after durable completions resume.
The underlying reason for the syscall latency and a controlled causal
reproduction still need evidence. This does not establish the separate
30-minute node-batched EOF root cause or satisfy R2/R6.

## Instrument and positive control

An isolated Go overlay adds a deferred trace join to the existing E2E harness;
production sources, the repaired per-Slot binary, workload and limits do not
change. It requests the existing public pprof trace endpoint on all three nodes
and records the harness runtime concurrently. The existing driver probe records
bounded enqueue/schedule/socket timing. Each capture has explicit status, byte
count, start/end time and errors. HTTP deadlines are duration+5 seconds. The
join executes before node cleanup, including a failing test's deferred cleanup.

Boundary contracts were written first, failed to compile before implementation,
and passed after implementation. They cover exact byte caps, cap+1 overflow,
body errors and runtime-writer overflow. A three-node positive control with
25 Channels, 500 SEND/s and a 10-second window completed 5000 messages. All
four two-second traces were complete and parsed with `go tool trace -pprof=sched`.
Its cap was 32 MiB/file. The main run uses 64 MiB/file (256 MiB total), increased
only for diagnostic file retention after observing positive-control trace size.
No production capacity changed. Raw files are losslessly gzip archived with
uncompressed and compressed SHA-256 hashes; decompress before using go tool.

## Failure captured with original load

`reproduce-01` uses the frozen per-Slot product, 5000 Channels, 1200 SEND/s,
60-second requested SEND window, 256 hash Slots, 128 append workers and gateway
batch cap 32. It failed after 39,560 SEND calls at SEND 32.96612014 seconds,
with 14 permission busy admissions: node 1 had eleven, node 2 had three.
The busy-admission sums were 3.916 and 3.001 microseconds respectively, again
selecting immediate full-queue rejection rather than 100-ms expiry.

All four 20-second traces cover approximately measured +20 through +40 seconds
and parsed completely: 971,457 harness events and 4,259,988 / 4,473,496 /
4,131,567 node events. Artifact sizes were 4,973,001 / 23,940,933 / 24,896,258 /
23,024,228 bytes; none hit its cap. The test failure was retained while the
trace join finished, total runner duration 229.810 s. No resource guard fired,
minimum host free space was 15.99 GiB, and only the lab's idle sleep remained.
The native traces include observer overhead and cannot qualify R2/R6.

## Observed ordering and limits

The trace clock shows these overlapping `Fdatasync` intervals in Pebble WAL
flush goroutines. These are syscall-state durations, not a measurement of pure
physical-device service time; OS scheduling can contribute.

| Node | Goroutine | Enter trace ns | Exit trace ns | Duration ms |
| --- | ---: | ---: | ---: | ---: |
| 1 | 58710 | 19097037357632 | 19097105033344 | 67.676 |
| 2 | 33662 | 19097037401792 | 19097111899584 | 74.498 |
| 3 | 32831 | 19097037572928 | 19097111934336 | 74.361 |

In node 2, directly recorded wake edges after WAL sync include goroutine
33662 -> 564 (`syncQueue.pop`), 564 -> 33870 (commit coordinator completion),
33870 -> 5212 (Channel durable-round completion), 5212 -> 597 (reactor
completion), 597 -> 1218 (Channel future completion), and 1218 -> 1171
(channelappend future completion). This identifies message commit work released
by the WAL flush. Further temporal descendants include permission reads, but
pool workers can be reused: goroutine ancestry alone is not end-to-end request
identity and must not be overstated as a complete causal proof.

Complete native event windows around the subsequent permission burst show:

- Node 2 enters 57 permission ReadBarrier waits in four adjacent 1-ms bins,
  approximately 12–16 ms after the WAL-sync return. Across the selected window,
  66 matched ReadBarrier waits have median 0.461 ms and maximum 1.153 ms.
  Thirty matched admission waits have median 0.544 ms and maximum 2.201 ms.
- Node 1 enters 33 ReadBarrier waits in two adjacent 1-ms bins. Forty-two
  matched barrier waits have median 0.482 ms and maximum 0.993 ms; seventeen
  admission waits have median 0.642 ms and maximum 0.695 ms.
- Blocked acquire-select counts are not exact queue depth: assigned permits
  can remain blocked until their goroutines resume. Do not interpret a count
  above sixteen as a recurrence of the repaired queue-accounting bug.

Thus the observed rejection is associated with concentrated arrivals after
commit recovery, while the individual read barriers remain short. The evidence
supports burst amplification more strongly than a continuously slow permission
lookup. It still needs an intervention that reproduces the sync-pause/recovery
sequence while preserving all limits, before claiming causal closure or choosing
a product repair. Increasing queues or weakening freshness would skip that step.

Maximum completed GC stop-the-world ranges are 1.394 / 1.645 / 1.501 ms on the
nodes and 0.950 ms in the harness. The largest node-1 runnable delay was 17.927 ms
about 1.6 seconds before this failure, not during the 74-ms sync interval.
The driver's last second has max schedule lag 4.305 ms, enqueue 0.090 ms and
socket write 0.142 ms. Its whole-window max schedule lag was 17.240 ms earlier.
These measurements do not support an equivalent 74-ms client catch-up pause;
100-ms bins and top-N summaries still are not a complete packet-arrival record.

## Repeat and inspect

Artifacts: `assets/send-ban-execution-trace-20260927/`.

- `failure-cases.md`, helper contracts, `contracts-red.json` and
  `contracts-green.json` document diagnostic behavior.
- `build.py`, `overlay.json`, source hashes and binary identities preserve the
  isolated harness. `run.py reproduce-01` is the recorded invocation; use a new
  label for another run. It requires this task-owned idle lab and prebuilt files.
- `analyze.py reproduce-01` parses the four existing traces only when the lab is
  idle. Generation status snapshots that preserve a goroutine state are ignored
  when measuring intervals; they are not artificial wait completions.
- `window.py reproduce-01 node-2 19097103000000 19097135000000` and the equivalent
  node-1 command produce complete bounded event windows. The earlier wider
  node-2 extraction hit its cap and is retained as explicitly overflowed evidence;
  it was not used for the complete-window claims.
- `analyze-window.py reproduce-01 node-1 58710` and the equivalent node-2 command
  with root 33662 derive matched waits and temporal wake edges. Their summaries
  retain worker-reuse and wait-versus-service-time limits explicitly.

Next: use a controlled, isolated commit-sync pause matching the observed
~70-ms event to test the recovery-burst prediction; distinguish it from the
shared-storage/VM cause of the latency itself. Keep the original failing trace
as the comparison authority, and validate a repair against the untouched load.
