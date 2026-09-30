# Fresh uninstrumented R2: EOF remains reproducible

The original 5,000-Channel / 4,500-SEND/s / 30-minute qualification failed after
206.139466261 measured seconds and 927,626 SEND calls (`sender_read: EOF`).
Pending messages were 6,363. Node 2 recorded one `async_dispatch_queue_full`;
permission admission busy, permission RPC errors, transport RPC rejection and
Channel RPC admission rejection were zero. R2 remains unqualified. This run does
not erase previous failures or convert the preceding diagnostic pass into a fix.

## Identity and execution

The preflight froze commit `eff8d06ed`, the unchanged clean product
`wukongim-handoff` SHA256 `39afc971f70a84c48582ca3b2cb5f4355c062e2e441a0c6c7022743055b9e879`
and the prime-metrics harness SHA256
`13c0fede4dcb09d2f5e658380a620db6ca66fc019024f121170836b9c308a313`.
Product sources under `cmd`, `internal` and `pkg` had no changes since the
admission-handoff fix `4bd9778fa`; post-run binary hashes match preflight.
The existing clean runner was copied unchanged. It retained 128 Store workers,
a SEND batch cap of 32, real synchronous persistence and the original load.
No flight recorder, syscall observer, artificial pause or compaction pacing was
enabled. No task build, trace parser or cleanup overlapped the measured workload.

The task-owned Linux/arm64 Docker container had only its idle sleep before the
run. Channel priming took 2m49.740791618s. The runner exited 1 after 405.410s,
with no guard termination; all workload children exited and only idle sleep
remained. Twenty-seven bounded external resource samples had no command errors.
Host free space stayed above 48,264,364,032 bytes. No OOM was reported; the last
15-second resource sample preceded failure, so these samples are not a precise
failure-time kernel timeline.

## Failure evidence

The last complete minute reported 810,001 SENDACKs and receives, pending zero.
At failure the partial-window SENDACK P99 was 445ms and aggregate CPU mean was
508.41% (100%=one CPU). These partial-window statistics are not qualification
performance results. Metric sample errors and ordinary membership mutations
were zero, and node processes remained continuous until test teardown.

The sampled failure profiles show all three nodes in Pebble WAL `Fdatasync`,
with synchronous commit publication and append-result waits. Nodes 2 and 3 also
have compaction `Fdatasync` stacks. These are post-failure stack observations,
not syscall durations or an exact join to the rejected ordering shard. They are
consistent with the earlier proven slow-durable-completion backlog mechanism,
but do not independently establish which physical I/O stage first stalled.

The earlier [kernel observation](2026-09-28-send-ban-kernel-waits.md) passed with
real page, journal and block waits; this clean run failed without those probes.
Together these sequential observations show variability, not a measured probe
effect or proof that a diagnostic success repaired the failure. No queue-size,
timeout, freshness or durability relaxation is justified by this result.

## Next investigation

Do not launch another unchanged qualification hoping for a pass. Inspect the
synchronous completion boundary between gateway batches, append routing and the
durable commit coordinator, and identify any avoidable serialization that
amplifies storage stalls. Preserve same-Channel order, ordered acknowledgements,
bounded admission, fresh authorization and durable-before-success semantics.
Any proposed change needs a failing contract before implementation and the same
original-load qualification afterwards. R6 still needs three complete fresh
comparisons; no failed window may be substituted with a historical pass.

The artifact directory is `assets/send-ban-clean-r2-20260928`; its manifest
includes the frozen run plan, preflight and post-run identities, raw failure
log, runtime counters, selected stack blocks, resource samples and terminal
runner result. No production code changed in this follow-up.
