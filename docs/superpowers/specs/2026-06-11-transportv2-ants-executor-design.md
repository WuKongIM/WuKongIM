# TransportV2 Ants Executor Design

Date: 2026-06-11

## Context

`pkg/transportv2` already has explicit connection actors, byte-aware outbound
scheduling, owned payload buffers, bounded service queues, and transport
observer events. The current server-side service execution model starts
`ServiceOptions.Concurrency` fixed worker goroutines per registered service in
`internal/rpc.Service`.

The repository already depends on `github.com/panjf2000/ants/v2` and
`internalv2/runtime/channelwrite` uses `PoolWithFuncGeneric` with non-blocking
submission plus package-owned admission tokens. That local pattern fits
transport workloads better than using ants as an implicit business queue.

## Goal

Introduce ants in `pkg/transportv2` to reduce idle service-worker goroutine
footprint and centralize service task execution, without changing transport
wire behavior, caller-facing errors, payload ownership, queue limits, or
connection write ordering.

## Non-Goals

- Do not replace `internal/sched.Scheduler`.
- Do not run connection writes through ants.
- Do not change `Client`, `Server`, `ServiceOptions`, `Handler`, or wire APIs.
- Do not introduce deployment semantics outside the existing single-node
  cluster and multi-node cluster model.
- Do not rely on ants internal waiting queues for transport backpressure.

## Design Choice

Use ants as a server-owned service executor while keeping per-service admission
and mailbox accounting inside `internal/rpc.Service`.

```text
accepted frame
  -> server dispatch
  -> service.Enqueue admission
  -> service-owned bounded mailbox/accounting
  -> shared ants executor runs handler task
  -> optional RPC response written through the original connection actor
```

`conn.readLoop`, `conn.writeLoop`, and `sched.Scheduler` stay actor-like and
deterministic. The write path must preserve single-connection ordering,
priority scheduling, byte batching, and owned-buffer release rules. Ants tasks
do not guarantee execution order, so the pool only runs independent service
handler work.

## Components

### ServiceExecutor

Add a small internal executor type under `pkg/transportv2/internal/rpc`.

Responsibilities:

- Own one `ants.PoolWithFuncGeneric[serviceTask]`.
- Run handler tasks with panic recovery and existing service observations.
- Map `ants.ErrPoolOverload` to `core.ErrBusy`.
- Map `ants.ErrPoolClosed` to `core.ErrStopped`.
- Release with `ReleaseTimeout` during server stop.

Pool options:

- `ants.WithNonblocking(true)` so submit never parks a reader or service
  admission path.
- `ants.WithDisablePurge(true)` for stable hot-path latency.
- `ants.WithPanicHandler(...)` as a final safety net; task-level recovery still
  owns response and release semantics.
- `ants.WithPreAlloc(true)` only when the executor capacity is large and stable.

### Service

`rpc.Service` keeps the public service contract and owns admission:

- `QueueSize` remains the maximum queued service item count.
- `MaxQueueBytes` remains the maximum queued payload byte budget.
- `MaxPayload` rejection releases payload immediately.
- `ErrBusy`, `ErrStopped`, and `ErrMsgTooLarge` behavior remains unchanged.
- Queued RPC requests still receive `ErrStopped` on service stop.
- `service_admission`, `service_queue`, `service_inflight`, and
  `service_task` observations remain compatible.

The fixed worker goroutines are replaced by one lightweight service pump that
moves accepted requests from the service mailbox into the executor only when the
service has capacity. Per-service concurrency is still enforced by
service-owned tokens, not by the global ants pool. If ants returns overload
despite token admission, the pump releases the token and waits for the next
completion or enqueue notification before retrying; it must not spin.

### Server

`Server` owns the executor and passes it to each registered `rpc.Service`.
Executor capacity is the sum of registered service concurrency. The first
implementation can build the executor lazily when the first service is
registered, then tune capacity upward as more services register.

`Server.Stop` stops accepting connections, closes active connections, stops
services, then releases the executor. Service stop must drain queued payloads
before executor release so payload ownership remains deterministic.

## Backpressure Semantics

Backpressure remains two-stage:

1. Service admission checks stopped state, payload size, queued item capacity,
   and queued byte capacity.
2. Executor submission checks service concurrency tokens and ants availability.

If the mailbox is full, `Enqueue` returns `core.ErrBusy`. If the executor is
temporarily saturated after admission, the request stays in the service mailbox
until a service token or executor worker is available. The reader loop does not
block on ants availability.

This keeps transport pressure explicit and observable. Ants is an execution
primitive, not the source of queueing truth.

## Error Handling

Handler panic handling is task-local:

- Recover inside the service task.
- Release the request payload exactly once.
- Send an RPC reply with a transport error when a reply channel exists.
- Emit `service_task` with result `panic` or `err`.
- Decrement inflight and release the service token.

The ants `PanicHandler` records any panic that escapes task-local recovery, but
normal correctness must not depend on it.

## Observability

Keep existing transport events stable:

- `service_admission`
- `service_queue`
- `service_inflight`
- `service_task`

Add low-cardinality executor events only if needed for triage:

- `service_executor_pool` with result `created`, `tuned`, `stopped`, `busy`,
  or `closed`
- `service_executor_submit` with result `ok`, `busy`, `stopped`, or `err`

Do not label by connection id, request id, node address, or raw error string.

## Testing

Targeted tests:

- Existing `pkg/transportv2/internal/rpc` tests stay green.
- Queue-full and max-byte tests still return `core.ErrBusy` and release payloads.
- Stop drains queued payloads and replies `core.ErrStopped` for queued RPC.
- A handler that ignores context does not block `Service.Stop`.
- Handler panic releases payload, decrements inflight, and replies with error.
- Service concurrency remains enforced when executor capacity is larger than one.
- Executor overload does not block `Service.Enqueue` or `conn.readLoop`.

Package tests:

```sh
go test ./pkg/transportv2/...
```

Benchmark comparison:

```sh
go test -run '^$' -bench 'TransportV2|Service|Scheduler' -benchmem ./pkg/transportv2/...
```

Compare before and after with `benchstat`, focusing on p50-style benchmark
latency, allocations, goroutine count under many registered services, and busy
admission behavior under saturation.

## Rollout

1. Add executor behind `internal/rpc` only.
2. Convert `rpc.Service` to use the executor while preserving tests.
3. Wire executor ownership in `Server`.
4. Add panic and overload tests.
5. Run targeted tests and benchmarks.
6. Only after stable results, consider exposing optional executor capacity in
   public config with detailed English comments and example config alignment.

## Open Decision

Do not expose a public config field in the first slice. The initial capacity
should derive from registered service concurrency to keep the public API stable.
If benchmark or production triage shows a need for independent tuning, add a
documented `ServiceExecutorCapacity` field later and update example config at
the same time.
