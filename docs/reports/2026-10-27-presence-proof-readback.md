# Presence recovery readback after proof eviction

A deterministic public-interface test found a recovery failure without any owner
RPC error or concurrency: scan 1,025 same-Hash-Slot UIDs in 512/512/1 pages, then
reread the first 512. Reconstructing the one evicted UID evicted another UID in
that requested page; the old readback returned `recovery_readback: route not ready`.
The baseline failed identically in three further runs. A 1,024-UID warm control,
authority fencing and tombstone controls passed.

The built-in Directory now captures the original known/missing partition under
one lock. After joined recovery succeeds, readback validates the distributed
target and a separate local authority-generation marker, then copies current
routes. Initially known proof survives FIFO residency loss for that request.
Initially missing proof must still be current; a new instance joining an old
flight cannot use nil completion as proof of absence. Missing proof evicted
before readback still fails closed. Ordinary lookups and wrapped/legacy Directory
ports retain their original proof checks.

Scopes remain caller-local, never in shared flights/work/creators. Their fixed
mask and closure metadata are conservatively charged to the existing caller
ledger. The cache, page, cohort, deadline, retry and verdict limits are unchanged.
The separate nonzero marker does not keep a retired slot's route maps alive.

Targeted tests, both complete related packages with integration tags, and their
full race suites pass. Controls cover current route/owner changes, exact-fence
local replacement, revision-only changes, missing-proof eviction, duplicate/empty
inputs, returned-slice mutation, bounds, wrappers and typed nil. Run:

```sh
GOWORK=off go test -tags=integration ./internal/runtime/presence ./internal/usecase/presence -count=1
GOWORK=off go test -race -tags=integration ./internal/runtime/presence ./internal/usecase/presence -count=1
```

This is a mechanism repair, not a demonstrated cause of the two R13 online
delivery expirations. That local run with 10,000 online users and 2,000 SEND/s run stopped after 48.613s,
with 92,002 sends/ACKs and two of 996 sampled deliveries expired. Both exact
messages were later found on all three stores with identical identity/content;
the selected receiver and original delivery timing were not retained. RPC/queue
pressure exists, but its exact link to those messages remains unproven.
An original-scale committed-source replay and exact-head Linux CI remain pending;
formal Linux 72-hour soak, capacity and recovery checks and signed stable-channel acceptance are separate.
