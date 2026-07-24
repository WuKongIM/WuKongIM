# pkg/goroutine Flow

## Responsibility

`pkg/goroutine` is the process-wide ownership registry for first-party
goroutines reachable from `cmd/wukongim`. It owns the fixed low-cardinality
module/task catalog, lifecycle counters, pprof labels, panic policy, pool
snapshots, Prometheus collection, and bounded shutdown evidence. Business
packages still own cancellation, queue closure, dependency order, and restart
policy.

Standalone bench/cloud tools, `pkg/client`, tests, generated code, and
third-party goroutines are outside this registry. `Snapshot` therefore reports
the Go process total, managed total, and a non-negative unmanaged remainder.

## Launch Flow

```text
module Start / bounded async operation
  -> SafeGo(registry, fixed TaskID, fn)
  -> validate TaskID against the compiled catalog
  -> increment active/started and apply module/task pprof labels
  -> run fn
  -> record bounded panic evidence
     -> recover isolated burst tasks
     -> re-panic critical permanent loops
  -> decrement active and update process-boot peak
```

`Default()` is the always-on process registry. App construction reuses it so
goroutines launched by lower-level packages before or without an explicit
registry remain in the same node snapshot. Dynamic node, Slot, channel, UID,
connection, plugin, error, and function values are never task labels.

## Pool Accounting

Audited ants/workqueue adapters call `RegisterPool` with a scrape-time callback.
`Goroutines` is the actual created worker count plus known ants maintenance
loops; busy tasks, capacity, queue depth, and rejected admissions are separate.
Closing a pool unregisters its callback only after every worker has exited.
Rejected totals from retired pools are retained so Prometheus counters never
move backwards. Task peaks are exact for direct managed launches; pool and
module peaks include the highest aggregate value observed by a snapshot or
scrape and never sum unrelated task peaks.

## Observation And Shutdown

`Snapshot` is lock-bounded and safe during concurrent task starts, exits, and
pool tuning. The registry is also a Prometheus collector for active, started,
panic, busy, capacity, queue, and rejected metrics. The app registers the
collector with constant `node_id` and `node_name` labels.

`Registry.Group(module).Wait(ctx)` does not cancel work. It waits for the
module's managed goroutines and registered pool workers, returning a bounded
list of live task IDs, counts, and running ages if the caller deadline expires.
`Baseline` / `WaitFrom` provide an unlabeled lifecycle fence: an App waits only
for activity added after its construction baseline, so a process-wide registry
does not make one App instance own a task that already existed.

After app readiness, fixed tasks that previously started but are below their
declared count are unhealthy; over-declared fixed counts and critical panics
are critical. Pool rejection or saturation is critical, while sustained queue
pressure is warning. Optional runtimes that have never started are not marked
missing.
