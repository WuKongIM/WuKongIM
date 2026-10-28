# Batch and tooling-client allocation repair

## Scope

The R36 local diagnostic run used 10,000 online users, 2,000 SEND/s and 256
slots. It ended `harness_invalid`: 125 offered messages missed admission,
187 first attempts were observed late, and the final latency window was not
qualified. Its detected-loss counter was zero; this is not a successful
acceptance result. Continuous tracing also had measurable execution cost.

An exact driver turn spent 119.874 ms Runnable after `runtime.Gosched`.
Concurrent GC background work and allocation assists occupied much of that
interval. The writer backlog append and reader accumulation append were among
the assist-triggering stacks. This identifies a scheduling/GC mechanism in the
instrumented driver, not an RPC-queue wait or a trace-free counterfactual.
Separate service heap samples attributed about 9.4 GiB of cumulative sampled
allocation per node to batch collection; those values include startup and are
neither resident memory nor allocations for one request.

## Changes

- Batch collection allocates initially for the ready queue depth. The original
  item limit still bounds later collection and executor retry; no pool or larger
  admission queue is added.
- After synchronous writes and completions, the client compacts the unconsumed
  request tail and clears completed request references while retaining storage.
- The reader copies a small incomplete suffix into its configured read base.
  Decoded payloads already have independent ownership. A temporary allocation
  for a large frame is released when its remainder fits the base.

The workload, first-attempt handling, correctness checks, latency thresholds,
and dispatch deadlines are unchanged.

## Reproduction and validation

Tests and benchmarks were written before their corresponding production
changes. Before the repair, the writer pump added one allocation per request
over a direct write, and the reader failed to return to its fixed base after a
fragmented large frame. Both tests pass after the repair, along with payload,
FIFO, cancellation and control-frame-tail checks.

Go 1.25.11, Darwin/arm64 component measurements (three runs):

| Fixture | Before B/op | After B/op |
| --- | ---: | ---: |
| Batch: 1 ready item, limit 256 | 90,112 | 352 |
| Batch: 4 ready items, limit 256 | 90,112 | 1,536 |
| Batch: 256 ready items | 90,112 | 90,112 |
| Client writer pump, one SEND at a time | 232 | 40 |
| Client reader pump, one SENDACK per read | 160 | 112 |

The batch fixture uses a pointer-bearing 328-byte value and 352-byte task; it
is not a production DTO mirror. Channel refill is included. Ready-full batches
keep one allocation; sparse batches that later grow can incur additional
allocations and copying. The table does not measure that arrival distribution.

Validation includes the workqueue and replication unit suites, workqueue race
checks, real durability/owner-alignment integration cases, and the complete
client integration suite with race detection, including encrypted TCP and
terminal-fence cases.

The first full client run also exposed a pre-existing EOF-fixture race. On
unchanged base `37f27dee`, 19 of 30 attempts closed the peer before read-deadline
admission, yielding `ErrClosedPipe` instead of EOF. The fixture now waits until
the reader enters `Read` before closing the peer. Its EOF assertion and product
classification are unchanged. Thirty repetitions and the full suite then pass;
the original failure logs remain retained.

Reproduce the component measurements with:

```sh
GOWORK=off go test ./pkg/workqueue -run '^$' -bench '^BenchmarkBoundedBatchCollectAllocation$' -benchmem -benchtime=200ms -count=3
GOWORK=off go test -tags=integration ./pkg/client -run '^$' -bench 'Benchmark(WriterPump|ReaderPump)SteadyState' -benchmem -benchtime=10000x -count=3
GOWORK=off go test -race -tags=integration ./pkg/client -count=1
```

These results do not establish recovery of the original 2,000 SEND/s scenario.
A same-scale run without continuous profiling remains required. Repeated
presence recovery for a working set larger than its proof cache is a separate
reproduced mechanism and is not changed by this allocation repair.
