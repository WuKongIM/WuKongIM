# Goroutine Supervisor And Realtime Monitor Design

Date: 2026-07-24

## Goal

Manage every first-party goroutine reachable from the official
`cmd/wukongim` server process and expose bounded, low-cardinality ownership
snapshots in the existing Web realtime monitor.

Management means lifecycle ownership, observability, and shutdown evidence. It
does not mean forcing long-lived Raft, reactor, network, or database loops into
a generic worker pool.

## Scope

The official server path is in scope:

- `internal/app`, API, Manager, and Gateway entry runtimes;
- Cluster, Controller, Slot, Channel, Transport, message/meta DB, and Raft log;
- Presence, ChannelAppend, Delivery, Conversation, Webhook, Plugin, and Backup;
- shared first-party workqueue and ants adapters used by those modules.

Standalone `wkcli`, `wkbench`, Cloud Simulation/Analysis processes,
`pkg/client`, tests, benchmarks, generated code, and third-party goroutines are
out of scope. The runtime may therefore retain a non-zero `unmanaged` count.

## Ownership Model

Long-lived loops run in dedicated goroutines through a module-owned
`goroutine.Group`. The module owns cancellation, connection closure, queue
closure, and shutdown ordering. The central Supervisor owns registration,
fixed labels, counters, snapshots, and bounded wait evidence.

Short, bursty work uses an existing typed `pkg/workqueue` primitive backed by
ants where appropriate. Pools are isolated by module and service quality; no
global executor is introduced. A generic workqueue is attributed to its
business owner rather than to a top-level `workqueue` module.

The official server always constructs the root Supervisor. Lifecycle
management has no disable switch. Prometheus export and Manager/Web exposure
retain their existing configuration gates.

## Stable Taxonomy

The first version uses these fixed modules:

- `app`
- `api`
- `manager`
- `gateway`
- `transport`
- `cluster`
- `controller`
- `slot`
- `channel`
- `database`
- `presence`
- `channelappend`
- `delivery`
- `conversation`
- `webhook`
- `plugin`
- `backup`
- `observability`

Every goroutine group has a compile-time `module/task` identity. Node IDs,
Slot IDs, channel IDs, UIDs, connection IDs, plugin numbers, error strings,
and other dynamic values are forbidden as labels.

## Runtime Accounting

The primary `goroutines` value is the number of actual live goroutines owned by
the group. For ants-backed execution it includes created workers, including
idle workers retained by `WithDisablePurge(true)`, plus reliably known pool
maintenance goroutines. It does not use inflight task count as a substitute.

Pool pressure is reported separately:

- `busy_tasks`
- `pool_capacity`
- `queue_depth`
- `queue_capacity` when the queue is bounded
- `rejected_total`

Snapshots also return process total, managed total, and
`unmanaged = max(process_total - managed_total, 0)`. A reconciliation flag
reports impossible or concurrently inconsistent samples instead of hiding
them.

`process_peak` is scoped to the current process boot. Prometheus provides a
separate selected-window peak. Snapshots carry process start time and boot ID
so the Web client can recognize counter and peak resets.

## Panic And Shutdown Semantics

Critical permanent loops record panic evidence and re-panic, allowing the node
to exit non-zero and cluster/process supervision to recover it. Isolated
short-lived tasks recover at their defined task boundary, release waiters, and
report the failed result through the bounded panic counter, task health, and
optional panic observer. There is no generic goroutine auto-restart.

Module shutdown waits for its own Group with the caller's deadline. On timeout,
the Group returns bounded `module/task`, active-count, and running-age
evidence. `internal/app` continues safe reverse-order cleanup and returns an
aggregate error. Packages do not call `os.Exit`.
Each App captures an unlabeled process-registry launch/registration baseline
before constructing owned runtimes. Sparse direct-task fences and pool
registration sequences ensure its Stop waits for the activity it added without
treating pre-existing process tasks as its own or letting their later exit mask
new App-owned work. A direct-task fence exists only when that task is already
active at baseline capture; normal non-overlapping launches keep lock-free
aggregate accounting and do not allocate one registry map entry per goroutine.

## Health Rules

Health is type-specific:

- a catalog-required fixed critical task missing after readiness is critical,
  including when it never started;
- a fixed non-critical task missing is degraded;
- a fixed worker count above its declaration is an invariant failure;
- per-pool pressure observations spanning at least ten seconds at or above 80
  percent with queueing are warning; observed relief or a monitoring gap over
  twenty seconds starts a new observation window;
- a full queue or rejected admission in any registered pool is critical before
  module/task values are aggregated;
- dynamic task counts and unmanaged totals are trends, not absolute alarms.

No universal goroutine-count threshold is introduced.

## Data Flow

Current state is an in-memory node snapshot. The selected node is read over a
new optional internal node RPC. The global view performs bounded, timed,
partial-success fan-out. Old nodes report `unsupported`; timeout, stale, and
unsupported are distinct from zero.

Prometheus supplies historical series, rates, and window peaks. Current
snapshots remain available when Prometheus is disabled or unavailable.
Prometheus and current snapshots use distinct source identifiers.

Manager responses and node RPCs are additive and rolling-upgrade safe. The
Manager route requires the existing `cluster.node:r` permission. It never
returns raw stack traces, function addresses, business identifiers, or
unbounded panic text.
Missing services use a stable transport error code mapped to
`clusternet.ErrServiceNotFound`; exact matching of the legacy server message is
retained only at that transport boundary for old binaries.

## Web

The existing `/cluster/monitor` page gains a `goroutine` category.

The view contains:

- a summary strip for process, managed, unmanaged, panic, and saturated pools;
- a current table keyed by node and module;
- expandable fixed task rows;
- worker capacity, busy, queue, peak, and health columns;
- historical cards for totals and a bounded set of top modules when
  Prometheus is available.

Global mode preserves node identity instead of summing away hotspots. Selected
node mode shows complete module/task details. Live snapshots refresh every
five seconds only while the category is active. The Manager applies bounded
fan-out, short-lived caching, and request coalescing.

## Performance

Registration and pprof labels occur at goroutine lifecycle boundaries, never
per message. Queue submission hot paths add no allocations and use only
existing bounded observations or atomic counters.

Acceptance targets:

- SEND benchmark throughput regression no greater than 2 percent;
- p99 latency regression no greater than 5 percent;
- a local snapshot of roughly one hundred fixed groups completes within 1 ms;
- Web fan-out has explicit concurrency, timeout, cache, and response bounds.

## Enforcement

A Go AST/analysis gate covers the official server package roots. It rejects
raw `go`, `errgroup.Group.Go`, and known unmanaged launch helpers outside the
single low-level goroutine implementation. Tests, benchmarks, generated code,
third-party modules, and explicitly justified low-level exceptions are
excluded.

Direct ants construction is limited to approved `pkg/workqueue` primitives and
small audited adapters. The module/task catalog is validated by tests and CI.
The migration uses a monotonically shrinking baseline; completion requires no
unapproved first-party launch sites.

## Tests

Required evidence:

- Supervisor/Group unit tests for registration, exit, peaks, panic policy,
  concurrent snapshots, and bounded wait evidence;
- pool ownership and accounting tests;
- module `Start`/`Stop` tests proving Group drain;
- `internal/app` wiring and reverse shutdown tests;
- Manager and node RPC tests for multi-node, timeout, unsupported, stale, and
  partial responses;
- Prometheus query and restart-reset tests;
- Web tests for global/selected-node views, expansion, five-second refresh,
  disabled Prometheus, and partial sources;
- one tagged three-node black-box E2E;
- focused performance comparisons and the repository's explicit-root unit
  gates.

## Rollout

1. Land the taxonomy, Supervisor/Group contract, pool observation seam, and
   AST baseline.
2. Migrate Controller, Slot, Channel, Transport, database, and Gateway.
3. Migrate remaining official server runtimes.
4. Shrink the core baseline to zero.
5. Add node RPC, Manager hybrid responses, Prometheus history, and Web.
6. Run E2E and performance evidence before declaring complete.

The Web must report coverage or partial state during migration and must not
claim full attribution until the first-party baseline is zero.
