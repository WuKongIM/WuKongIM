# Releasing quorum workers does not remove the 380 ms WAL-fault EOF

Source `e5259c5f3` adds callback-driven quorum commits: a commit releases its
store-append worker after submission while keeping its admission budget, task
context and a Close join slot until the single terminal callback.

## Verification

- Unit, race and integration suites for `pkg/channel/...` passed.
- The send-ban E2E suite passed (9 tests; the 100K case is skipped by default).
  Artifacts: `assets/send-ban-async-worker-20260928/send-ban*.json`.

## Fault reproduction (unchanged load)

5000 Channels, 4500 SEND/s, cap32, 380 ms message-WAL hold from +25 s, same
Docker lab. It failed with `sender_read: EOF` at 26.41 s after 118861 SENDs.
The prior baseline failed at 26.48 s after 119173 SENDs.

At failure, node1 closed a connection for `async_dispatch_queue_full`. Gateway
async-send depth was 2200, 1861 and 1840 per node, and 438–706 appends were in
flight. Almost all append latency sits in `store_append_wait`: 1354 s of 1355 s
on node1. Post-commit and HW-advance waits stay under 0.4 s in aggregate.

## Conclusion

Worker occupancy was not the limiting boundary. With workers released, the
durable WAL completion (380 ms per sync) still bounds throughput, and gateway
async-send queues fill within about 1.4 s. Remaining levers are upstream of the
worker: gateway admission/backpressure policy under slow durability (reject
instead of closing), or larger WAL group commits per sync. Neither is decided here.
