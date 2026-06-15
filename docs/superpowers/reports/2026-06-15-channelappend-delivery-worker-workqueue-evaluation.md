# Channelappend Delivery Worker Workqueue Evaluation

Date: 2026-06-15

## Scope

Evaluated migrating `internalv2/runtime/channelappend.RecipientDeliveryWorker`
to `pkg/workqueue.BoundedWorkerQueue`.

## Result

Do not migrate this worker in the current form. The existing implementation is
a very tight blocking channel admission path and already runs with zero
allocations. Replacing it with `BoundedWorkerQueue` preserved allocations but
regressed enqueue latency.

## Performance

Command:

```bash
go test -run '^$' -bench 'BenchmarkRecipientDeliveryWorkerEnqueue$' -benchmem -count=8 ./internalv2/runtime/channelappend
```

Benchstat for the rejected migration:

```text
RecipientDeliveryWorkerEnqueue-10  217.5 ns/op -> 384.8 ns/op  +76.90%
RecipientDeliveryWorkerEnqueue-10  0 B/op     -> 0 B/op
RecipientDeliveryWorkerEnqueue-10  0 allocs   -> 0 allocs
```

The migration was reverted. The benchmark remains so future worker changes have
a dedicated performance guard.

Final state after reverting the migration:

```text
RecipientDeliveryWorkerEnqueue-10  217.5 ns/op -> 211.8 ns/op  ~ (p=0.645 n=8)
RecipientDeliveryWorkerEnqueue-10  0 B/op     -> 0 B/op
RecipientDeliveryWorkerEnqueue-10  0 allocs   -> 0 allocs
```
