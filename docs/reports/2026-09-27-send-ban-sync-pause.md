# Controlled WAL completion pause: causal experiment

This experiment tests the recovery-burst hypothesis from the captured native
execution trace. It is not a product repair and does not qualify R2 or R6.
Production queue limits, timeouts, batching, freshness and real Fdatasync remain
unchanged. Both full-load arms use one frozen diagnostic product/harness pair.

## Contract and preparation

The isolated Pebble copy wraps only linuxFile.SyncData. A future control file
arms a one-shot claim for a `/messages/*.log` path on each node. The wrapper
executes the real Fdatasync once and preserves its error; it pads the total
operation duration to 70ms rather than adding 70ms after an already slow sync.
A zero-hold arm executes the same path and writes the same one-shot evidence.
A bounded startup poll reads the atomic control file once; sync operations do
not perform per-call file checks. The private diagnostic opt-in environment is
consumed before product configuration validation. Neither the original module
cache nor either frozen product source tree is edited.

Failure contracts were written first and cover arm timing, file scope,
concurrent one-shot ownership, input validation, nonnegative remaining budget,
and preservation of actual sync errors. The initial module-cache overlay was
rejected by Go before compilation; that rejection is retained. A copied Pebble
module selected through a separate `-modfile` then produced the expected missing
implementation compile failure, followed by passing contracts after the helper
was implemented. The harness retains the bounded runtime-trace contracts from
the preceding diagnostic, with one additional atomic control-file publication.

## Positive control

`positive-01` completed 5000 messages at 25 Channels / 500 SEND/s / 10 seconds.
The pause was armed at measured+1s. Three distinct node processes recorded
exactly one message-WAL completion: 70.28, 70.97 and 70.88ms; actual Fdatasync
was 0.33, 0.57 and 0.47ms respectively. Real sync success was preserved.
All four two-second runtime traces were complete and parsed via Go's trace
scheduler-profile converter. Total runner time was 22.976s, with no guard stop
and only the container's idle sleep remaining after cleanup.

## Fixed full-load comparison

The reviewed order is zero-hold `control-01`, then `pause-70-01`. Both retain
5000 Channels / 1200 SEND/s / 60 seconds, 256 hash Slots, 128 append workers,
and gateway batch cap 32. The pause is armed at measured+25s; execution traces
cover measured+20 through +40s, including a failed run's deferred capture join.
Every result must remain, including a failed control or missing injection.

Prediction: if completion-delay recovery is sufficient for the observed busy
failure, the treatment should fail close to its recorded ~25.07s release and
show a comparable permission burst. A later unrelated failure is insufficient.
An untriggered/late/wrong-file injection makes the run inconclusive. Even a
positive result explains the effect of delayed completion, not the underlying
kernel/storage/VM source of the original syscall latency. That distinction
remains necessary before selecting any product change.

Artifacts and reproducible commands are under
`assets/send-ban-sync-pause-20260927/`. The scripts require this task's owned
idle Docker lab and its explicit frozen source/dependency copies. The module
copy, helper source, exact default_linux.go substitution, modfile mechanism,
red/green/build commands, binary identities and original-load plan are retained.


### Zero-hold full-load control result

`control-01` failed at SEND 34.076261307s with two immediate permission busy
responses. All three zero-hold claims occurred at measured+25s; real syncs
completed in 0.64 / 1.11 / 0.73ms with no injected sleep. The control therefore
did not fail at its arming/release point, but it is not a passing 60-second
reference. Keep that later natural failure in the comparison. Total runner
229.471s includes joining the trace window; all three injection records exist.
The 70ms treatment is terminal, below.


### 70ms treatment result

`pause-70-01` reproduced four permission busy responses at SEND 25.080121928s
(after 30,096 SEND calls). Node claims all occurred just after +25s on message
WALs; their total completion durations were 70.073, 70.103 and 71.450ms.
Real Fdatasync took 0.626, 0.572 and 0.388ms, with the remainder deliberately
held by the wrapper. Last completion was at +25.072538247s, only 7.583681ms
before the harness observed failure. The zero-hold control had instead failed
at +34.076s, well after its +25s marker and without an injected delay.

This is an intervention, not just matching timestamps: a completion delay
matching the observed natural stall was sufficient to provoke the same typed
busy failure immediately after recovery in this run. It does not establish
that every 70ms pause fails, nor identify the hardware/kernel origin of a
natural slow sync. The precise supported mechanism is completion-delay recovery
amplifying short-lived permission concurrency; the bounded admission refusal
is the downstream consequence.

All four treatment traces are complete and parsed: 415,852 harness events and
1,789,416 / 1,875,265 / 1,732,053 node events. Native trace contains the actual
injected sleep in the VFS wrapper, not a claimed 70ms kernel Fdatasync syscall.
Related existing VFS sync tests and injection contracts passed together (0.033s).
Total treatment runner time was 229.619s including trace join, no guard stop,
and no remaining product process. Raw timing and all three exact paths are in
`pause-70-01/causal-timing.json` and `result.json`.

### Node-batched follow-up: same disturbance passes

The same isolated VFS helper is now linked with the frozen node-batched source,
without changing the wrapper, harness, limits or workload. `node-pause-70-01`
applied the same +25s / 70ms disturbance and completed 72,000 messages, with
busy=0 and SENDACK P99=20ms. All three message-WAL claims were present, with
completion durations of 71.184, 70.345 and 70.690ms. Total runner time was
249.217s, with no guard stop or remaining product process. It cannot
replace the separate uninterrupted R2 run or the three complete R6 pairs.


Comparison of 1,515 non-test production Go/module files in the two frozen
source trees found exactly one differing file: `send_permission_rpc.go`.
The retained diff is the reference-only condition that flushes an envelope on
every Slot, versus flushing only when the target leader node changes. Both
builds use the exact same isolated VFS module, harness, limits and load. This
supports attributing the observed difference to node aggregation, rather than
a hidden admission increase or stale-read shortcut.

| Arm | Injected hold | Result | Observation |
| --- | ---: | --- | --- |
| per-Slot control | 0ms | FAIL | Natural busy at +34.076s, not the +25s marker |
| per-Slot treatment | 70ms | FAIL | Four busy at +25.080s, 7.584ms after last delayed completion |
| node-batched treatment | 70ms | PASS | 72,000 messages, busy=0, P99=20ms |

The application-level failure mechanism is now supported by a controlled
intervention and an otherwise equivalent grouping comparison: durable
completion delay releases concentrated work, per-Slot fanout amplifies the
number of admitted envelopes, and the unchanged node envelope gate rejects
the burst. The implemented node aggregation avoids rejection in this exact
experiment. This is one disturbance and one run per arm, not a universal
stability proof. The storage/VM cause of natural sync latency and the separate
long-soak EOF remain open. Formal R2/R6 requirements remain incomplete; do not
turn this diagnostic's PASS into those acceptance results.
