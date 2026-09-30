# Issue #977: bounded fresh permission cohorts

Source: fc0905c06f80399635aef0779a5c3e6006bf99c7, with baseline
product 424d03eb298b972ec572261d617972eecb5523c5 and its frozen receipts.
Failure cases below precede tests and product implementation.

## Failure cases

1. Independent callers repeat mandatory facts/envelopes/barriers. The real
   three-process fixed baseline must go red on the new burst-reduction assertion;
   sequential reads retain one new barrier per Slot per call. Compare identical
   inputs, actual placement, clean binaries and metric windows, without retries.
2. A caller arriving after seal must not join an executing read, even when a
   completed ban/unban changes the policy while the first barrier is blocked.
   Seal precedes route resolution and every barrier; no completed fact cache.
3. Two Slots sharing a leader keep separate fences, fresh barriers and snapshots.
   One failed Slot preserves the other's indexed results; only stale groups retry
   once. Deduplication must preserve duplicates and original caller/index order.
4. Short deadlines, explicit cancellation and pre-canceled calls must not poison
   another caller. Shared execution follows the longest live sealed budget;
   individual results still follow their own deadlines. Last cancellation joins
   the canceled worker before returning. No caller joins after execution starts.
5. Overload must fail typed busy before taking ownership of copied facts. Bound
   active cohorts, total retained calls, total input references and conservative
   retained-memory credits. Canceled callers remain charged until their started
   work joins; result ownership transfers only when the caller resumes.
6. Close seals admission, cancels collecting/executing cohorts and joins their
   route/RPC/barrier/snapshot work before Node closes transport or databases.
   Repeated close is safe; restart constructs a new Store. Empty/invalid requests
   and route errors cannot leave credits or managed goroutines behind.
7. A small coalescing wait may worsen latency/CPU/allocations. Report measurements
   honestly; preserve the three-node 500 SEND/s gates and earlier 4500 SEND/s
   failure. Lower counts alone are not capacity qualification or a CPU diagnosis.

## Implementation and test seam

Use only the existing Store's SEND-fact entry, existing node RPC and per-Slot
reader. Collect for at most 1 ms, then seal; at most 64 calls and 4096 input facts
per cohort, 64 collecting/executing cohorts, 1024 retained calls and 16 MiB of
conservative memory credits per Store. There is no additional execution queue
or retry. Existing receiver admission remains independent (64 executing,
1024 waiting/16 MiB undecoded/2 s). Ingress memory credits include input/result
arrays, dedup/index maps and retained string bytes; they are a conservative
budget, not measured allocator bytes. All ownership is publicly observable
through fixed-dimension gauges and counters.

Controlled-port integration tests are necessary to stop exactly at seal,
barrier entry and cancellation: public E2E APIs do not expose these instants.
They exercise the production proxy, codec, real database snapshot and managed
worker, retain a verdict artifact, and do not qualify real Raft/network capacity.
The private collection-window setting exists only for deterministic integration
scheduling; the product constructor uses the fixed 1 ms bound.

The process-level test reuses the fixed baseline experiment, adds a candidate
mode and actual reduction assertions, and retains source/fixture/counters/ACKs/
history/resource evidence. Existing all-ingress ban/unban, delivery, non-replica,
leader-transfer, parallel and overload/recovery E2E scenarios remain mandatory.

Fixed old/new comparison windows also sample the ingress ownership gauges every
20 ms (at most 200 samples, canceled/joined). Missing old metrics remain absent;
sampled peaks are lower bounds, and scrape overhead belongs to both comparisons.
