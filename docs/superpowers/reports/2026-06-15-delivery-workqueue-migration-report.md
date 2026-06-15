# Delivery Workqueue Migration Report

Date: 2026-06-15

## Scope

- Migrated `internalv2/runtime/delivery` manager async admission from a local hand-written queue/worker lifecycle to `pkg/workqueue.BoundedWorkerQueue`.
- Added `BoundedWorkerQueue` as a reusable low-latency workqueue primitive for hot paths where ants-backed execution is too expensive.
- Removed manager restart-after-stop behavior. `Manager.Stop` is now terminal for the instance.

## Performance

Command:

```bash
go test -run '^$' -bench 'BenchmarkManagerAsyncSubmitCommitted$' -benchmem -count=8 ./internalv2/runtime/delivery
```

Benchstat:

```text
                               │ before │ after  │
ManagerAsyncSubmitCommitted-10   278.4n   274.4n   ~ (p=0.367 n=8)
ManagerAsyncSubmitCommitted-10   263 B/op 263 B/op ~ (p=1.000 n=8)
ManagerAsyncSubmitCommitted-10   6 allocs 6 allocs ~ (p=1.000 n=8)
```

An intermediate migration to the existing ants-backed `BoundedPool` was rejected
because it measured around 15-19 us/op and 9 allocs/op for this hot admission
path.

`pkg/workqueue.BoundedWorkerQueue` direct parallel submit reference:

```text
BenchmarkBoundedWorkerQueueSubmitWaitParallel-10   167-216 ns/op   0 B/op   0 allocs/op
```
