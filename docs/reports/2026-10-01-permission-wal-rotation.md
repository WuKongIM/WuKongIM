# Fixed sequential SEND WAL-boundary diagnosis

The comparative verdict remains **FAILED: 6/12 sequential rows**. These four
additional process-level runs are diagnostic only. They neither replace the six
original runs nor qualify p99, CPU, a repair, or Issue #977 completion. Runtime
candidate remains `ec21c1a586941ffd74cfb497f0da4522941bc1a4`; the frozen driver is
`c0e95b5a45babbd1907c590a9dcdab83e74634a9`. No candidate Go code changes in this
follow-up.

## What the new evidence establishes

The repeated candidate maximum at `two-slots-one-remote-leader-c1-038` crosses
Pebble's memtable-growth/WAL rotation boundary. A one-variable counterfactual,
disabling only OrderedSubmitter append coalescing, moves the observed rotation
and maximum back to c1-003, the old runtime's repeated maximum position. This
supports a change in where the storage boundary occurs within the fixed prefix;
it does not show that disabling coalescing improves latency.

| Source / exact target | SENDACK | Leader `local_durable` | `rotateWAL` in that interval |
| --- | ---: | ---: | --- |
| Candidate ec21 / c1-038 | 31.705 ms | 28.457000 ms | Yes, 9 matching transitions |
| Old 424d / c1-038, same-target control | 10.911 ms | 8.076708 ms | No matching transition |
| Old 424d / c1-003, rotation control | 28.065 ms | 24.729625 ms | Yes, 9 matching transitions |
| Diagnostic 94d3345 / c1-003, append coalescing off | 37.038 ms | 33.754792 ms | Yes, 9 matching transitions |

In the candidate's two-Slot sequential window, c1-038 is the maximum; in both
c1-003 controls, c1-003 is the maximum. All four complete the unchanged four
placements, sequential and burst counts, fresh ban/unban controls, exact
161-item histories per placement, and joined ownership checks. The disabled
coalescing run keeps permission cohorts enabled and passes their functional
burst RPC/barrier reductions. Its worse raw maximum is retained.

## Candidate causal chain

The selected SEND spans have one trace identity, on ingress node 1 and Channel
leader node 2. Permission is 0.193 ms, ordered append-admission wait is
0.004416 ms, and the Channel leader's local interval is 28.457 ms. As established
in the previous follow-up, `replica.leader.local_durable` encloses quorum task
submission through reactor result consumption; its name alone is not a disk
attribution. This runtime trace distinguishes the nested stages.

Times below are relative to that leader interval's start, using the runtime's
own clock snapshots, not the host HTTP request timestamp:

| Relative time | Runtime trace fact |
| ---: | --- |
| 0.008 ms | Reactor wakes the quorum task pool dispatcher. |
| 0.024 ms | Quorum submission wakes the local durability pool. |
| 0.135 ms | Local append worker submits to the message commit coordinator. |
| 0.140–0.696 ms | Coordinator collects its bounded physical commit group. |
| 0.702 ms | `makeRoomForWrite` / `rotateWAL` closes the old log. |
| 8.397–8.880 ms | Close/create calls and a new WAL disk-health owner appear. |
| 16.263 ms | Coordinator is preempted with `FD.Fsync` / directory `Sync` / WAL creation on its stack. |
| 16.271–16.325 ms | A new log writer and memtable allocation appear. |
| 16.331–28.429 ms | Coordinator waits 12.098112 ms for Pebble synchronous commit publication; the new log writer releases it. |
| 28.437 ms | Coordinator publishes the local append result. |
| 28.451 ms | Quorum completion wakes the reactor mailbox. |

These intervals overlap and must not be added as separate request costs. A
preemption stack shows where execution was interrupted; it does not certify
that an entire preceding running interval was spent in that system call.
The target-window capture has no overlapping GC range. Foreground peer waiting
also occurs, but local completion arrives later in this selected request. This
does not exclude peer or GC costs in other placements or runs.

The old c1-003 rotation control contains the same close/create, directory-sync,
new-log-writer and memtable-growth sequence. The old c1-038 control instead has
an ordinary synchronous WAL write without a matching rotation stack in its
selected interval.

## Why a small fixture can cross this boundary

The exact Pebble v2.1.4 source used by these binaries starts a writable database
with at most a 256-KiB memtable (`open.go`, `initialMemTableSize` and
`d.mu.mem.nextSize`), even though WuKongIM's message engine configures a 64-MiB
eventual memtable. `makeRoomForWrite` rotates WAL before growing that table.
Changing only the configured eventual memtable size therefore does not remove
the initial growth boundary.

The counterfactual forces `opts.BatchMaxRecords = 0` immediately after existing
OrderedSubmitter option validation. Both append-merge paths already require a
positive record target; gateway session batching, permission cohorts, admission
bounds, routing, FIFO callbacks and synchronous durability remain unchanged.
That intervention moves the observed boundary back to c1-003. The more specific
explanation that fewer prior exact proposals reduce memtable bytes is an
inference; this experiment did not directly count proposal-index bytes. No
storage-schema, WAL, fsync, or acknowledgement semantics were changed.

This result is not a waiver of the original comparison. A 64-SEND p99 is the
maximum, so a relocated storage boundary materially changes the measured row.
The remaining two-remote-leader p99/CPU rows and aggregate CPU regressions are
not explained by this one target. Overall non-regression remains unverified.

## Bounds, provenance and retained failures

Every run retains the complete four-placement prefix on three owned loopback
nodes, GOMAXPROCS=4, 256 Hash Slots, 12 physical Slots, one metadata voter and
three message replicas. Only one exact-client-number 100%/120-second debug
match is added; the baseline sampling rate and buffer capacity remain unchanged.
One public `/debug/pprof/trace?seconds=4` capture on node 2 has a six-second
timeout and a 16-MiB response limit. All four HTTP captures complete, each below
1 MiB. Each run also performs exactly three completed-target timeline queries,
bounded to two seconds, 64 KiB and 32 events each, with no query error or
truncation. Empty node-3 timelines remain explicit.

The extra trace capture and 50-ms host progress observer make these runs
unqualified. The execution plans declare failures and hypotheses before their
respective runs; no observed run is overwritten, replaced or retried. All four
owned temporary directories are removed after their test processes exit.

Go's parsed trace dump prints wall clocks only to whole seconds. The analysis
therefore joins parsed Sync events to the wire ClockSnapshot's full seconds and
nanoseconds through their identical monotonic timestamps. The observed offset
spread is recorded for each capture (candidate 45.896 microseconds). This is a
near-simultaneous runtime clock reading, with diagnostics timestamp rounding,
not a certified clock-error bound. It is adequate to distinguish the multi-ms
storage stages here; it should not be used to certify sub-microsecond ordering.

The exact diagnostic binary revision is
`94d3345b9486e0d4d87ef0a8384f6c0fcca6aea2`, with a clean VCS stamp and SHA-256
`23ade4938693c8e1b388833b93ee83dc18b86d95e195402d3ec009fa6bca533d`.
Its initial FLOW check failed because the generated index was stale; that failure
is retained. Documentation-only tip
`b9bf8c7bf` refreshes the index and passes `flow-doc-contracts`; no Go files
change after the tested binary revision. The probe is deliberately not a
product PR or a merge candidate. A small Git bundle retains both exact commits
with ec21 as prerequisite.

The [archive receipt](2026-10-01-permission-wal-archive.json) records every full
artifact hash. The full local archive contains all four raw runtime traces,
raw E2E reports/timelines, plans, source bundle, analysis and all sixteen derived
network/synchronization/system-call/scheduler profiles. Large parsed text dumps
are reproducible from the raw traces; their hashes are retained rather than
duplicating hundreds of megabytes of text.

The smaller bounded evidence bundle in this Draft PR contains all raw E2E reports,
timelines and plans, source/check receipts, the source bundle, the derived
profiles and complete bounded target-event excerpts. It omits raw runtime trace
binaries and unbounded derived text, with their full-archive hashes recorded.
The raw traces remain in the full local archive; the public excerpts alone do
not independently certify the entire runtime capture. The original primary
archive and the previous delivery-follow-up archive stay byte-for-byte unchanged.

## Reproduction

Extract the full archive and verify the internal `manifest.json` first. For each
label `new`, `old`, `old-rotation`, and `no-coalesce`, regenerate the two decoded
views with the same Go 1.25.0 trace reader before running the included analyzer.
Outside a Go module, explicitly select that toolchain; an older default Go CLI
may reject the named parsed/wire modes:

```sh
GOTOOLCHAIN=go1.25.0 go tool trace -d=parsed new.node-2.trace > new.trace.parsed.txt
GOTOOLCHAIN=go1.25.0 go tool trace -d=wire new.node-2.trace > new.trace.wire.txt
python3 analyze-quorum-trace.py new
```

Repeat with each label. `verify-wal-evidence.py` checks the four retained runs,
source identities, timeline bounds, maximum positions and the expected rotation
observations. The source runner contains exact local binary/driver paths for
this workspace; starting a new run requires a new output label and a declared
plan, never overwriting these observations. A future performance candidate still
needs a new predeclared qualification series under the unchanged gate.

The embedded archive report was frozen before PR scope accounting and still
names #989 as the intended bounded-bundle destination. This report and receipt
record its actual separate PR destination; the immutable archive bytes are
unchanged. The first extraction replay selected an older default Go CLI outside
the module and failed before parsing. Its receipt is retained alongside the
corrected Go-1.25.0 replay, which reproduces all four analyses exactly.
