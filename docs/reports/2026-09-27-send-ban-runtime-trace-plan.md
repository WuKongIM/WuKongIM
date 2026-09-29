# Sustained EOF: next discriminating diagnostic (not implemented)

The repaired product still closed a connection at SEND 1701.108 s, after one
node reported `async_dispatch_queue_full`. The permission busy count was zero.
Post-failure stacks show Fdatasync but do not time it. The instrumented sync
run completed 30 minutes once; that is not a verified fix of the clean failure.
Do not rerun the same uninstrumented 30-minute workload blindly or increase
queues/relax durability to force a pass.

Three hypotheses remain, in evidence-ranked order:

1. A long storage synchronization or compaction wait holds a session's admitted
   SEND batch long enough to fill its shard. Prediction: failure-time traces
   show long storage/syscall waits on the affected batch's dependencies.
2. Scheduler or GC delay prevents eligible work from running. Prediction:
   traces show long runnable delays or GC stop intervals, potentially overlapping
   across processes, rather than a corresponding long storage operation alone.
3. Gateway-local shard contention or a delayed worker/handler causes local
   saturation despite aggregate free capacity. Prediction: the affected work
   waits in mailbox/handler scheduling while its downstream dependencies remain
   active; queue occupancy alone cannot establish the cause.

The current gateway deliberately performs nonblocking SEND admission and closes
only the saturated session; existing tests require that behavior. Changing that
contract is not a substitute for identifying the dependency that stopped making
progress under the accepted workload.

## Available diagnostic mechanism

The installed Linux Go 1.25.11 standard-library source at
`/usr/local/go/src/runtime/trace/flightrecorder.go` was inspected. A flight
recorder retains a recent execution window, and its WriteTo operation forces a
trace flush and can produce a normal execution trace. Its MaxBytes setting is a
hint: neither retained memory nor emitted bytes have a guaranteed hard bound.
MinAge can be overridden by that size hint. Any implementation must state those
limits and separately cap written artifacts; it must not advertise a hard RAM
bound supplied by the runtime API.

A possible next experiment is an isolated diagnostic binary, with no production
worktree changes: a single recorder per process, a small recent-window target,
a task-owned Unix control socket, and one capture per node requested concurrently
at the harness's first failure, before normal post-failure scrapes and cleanup.
No periodic trace files or extra mid-window metrics scrapes. Preserve capture
start/end times and explicit errors/truncation, limit each output file, and parse
all successful files before using them as evidence. Recorder overhead means this
is a diagnostic, never a replacement R2/R6 qualification.

Before implementing: write failure cases/contracts, inspect the exact harness
failure seam and applicable FLOW/AGENTS, retain isolated-source hashes, and prove
three-node positive capture plus `go tool trace` parsing. Keep production limits,
load, freshness and durability unchanged. The ongoing nine-arm performance
campaign has priority: no builds, tests or additional probes run alongside it.

This document records a future evidence plan only. No flight recorder, socket,
harness hook or diagnostic binary has been implemented or started.

## Implementation update

The isolated flight recorder is now built and its three-node positive capture
passed. See [implementation and evidence](2026-09-27-send-ban-flight-recorder.md).
The preceding text records the original plan; long-run root attribution remains open.
