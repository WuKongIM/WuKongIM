# medium_recipient_hotpath AGENTS

This opt-in scenario is the higher-fidelity local Cloud Medium recipient
hot-path evidence gate.

## Run

```bash
WK_E2E_MEDIUM_RECIPIENT_HOTPATH=1 \
WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE=1 \
GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath \
  -run TestCloudMediumScaledRecipientHotPath -count=1 -timeout 5m -p=1 -v
```

Run the sustained permission-pressure qualification separately:

```bash
WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 \
WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=30m \
WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 \
WK_E2E_MEDIUM_RECIPIENT_QPS=4500 \
GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath \
  -run TestCloudMediumPermissionSoak -count=1 -timeout 40m -p=1 -v
```

Set `WK_E2E_MEDIUM_RECIPIENT_QPS` or
`WK_E2E_MEDIUM_RECIPIENT_ROUNDS` only for bounded diagnostic stress runs.
`WK_E2E_MEDIUM_RECIPIENT_RPC_BATCH_MAX_ITEMS` is likewise diagnostic-only for
same-binary A/B evidence; normal acceptance remains fixed at 8.
`WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS` may raise the four profile fixtures
up to the Cloud Medium mix of 5,000 group channels. It preserves the measured
message and recipient totals while rotating measured messages across the
configured channel set, so high-cardinality Channel RPC scheduling can be
reproduced without changing the accepted traffic volume.
Normal acceptance stays fixed at 4,500 offered messages per second. Nightly
sets `WK_E2E_MEDIUM_RECIPIENT_CI_SCALE=1` together with the strictly reviewed
500/s QPS override because a shared two-core runner cannot represent absolute
Cloud Medium capacity while hosting three nodes, all clients, and the sampler.
The CI-scaled gate retains the exact workload shape, latency limits, queue and
plugin conservation, allocation/GC ceilings, and process continuity.
Public pressure metrics are sampled once per second. That remains bounded while
avoiding observer-induced tail latency and allocation from three concurrent
full-registry Prometheus scrapes.
Allocation acceptance separates a 360,000-byte/message budget from a bounded
40MB/s allowance over the fixed paced duration. A slow drain cannot enlarge
that allowance and hide a product-path allocation regression.

The permission soak defaults to the reviewed 30-minute, 4,500-QPS, 5,000-group
shape with 128 Store append workers, matching the current product default.
Verify the rendered worker count on every node and report it in both success
and failure evidence. Keep the historical 8-worker failures as failed evidence;
this does not claim that configuration passed. All comparison arms must use
the same 128-worker configuration. The separate mixed-recipient scenario retains
its existing eight append workers. Replication workers, batch size, apply workers,
durability, workload, latency and zero-rejection gates remain unchanged.
It permits only bounded diagnostic overrides: duration 10 seconds to 30
minutes, QPS 500 to 20,000, and 25 to 5,000 group channels in multiples of 25.
It uses 25 sender/receiver pairs across all three nodes and naturally hashed
channels, and it requires both local and remote permission routes. Its latency
histograms have a fixed 10,001-bucket bound and only incomplete messages occupy
the in-flight map, so the harness does not retain one object per completed
message over a long run. Public metrics sample transport RPC executor pressure,
permission Slot RPC queue/admission/in-flight state, managed permission-batch
goroutines, heap/GC, plugin conservation, and membership mutation rows. A
premature failure emits one bounded `WKRC-PERMISSION-SOAK-FAILURE` JSON row;
a completed run emits `WKRC-PERMISSION-SOAK-EVIDENCE`. Both rows include
measured-window histogram-delta P99 and diagnostic P99.9 attribution for
gateway dispatch, gateway SEND batch handling, complete and local/remote
channelappend routing, message permission/pre-append/submitter stages, Channel
store/quorum waits, sampled leader Pull handling, and leader/follower storage
commit requests; setup and cold prime observations must
remain outside those deltas. Channel RPC admission-full
evidence must retain the typed Pull versus PullHint split; do not tune the
shared pool from the aggregate counter alone.
The same evidence should retain typed `paced` counts so proactive watermark
activity is not confused with bounded-pool rejection.
It must also retain store-apply `full` and store-apply-triggered Pull `paced`
counts. Sustained acceptance requires store-apply `full` to remain zero; the
paced count is diagnostic and may be nonzero during bounded recovery.
Complete router-batch and message-stage comparisons with per-message SENDACK
must use their item-weighted histograms; one-sample-per-batch quantiles
underweight slow large batches.
The evidence must retain measured-window record-bearing versus empty Pull
counts, Pull/PullHint batch calls and items, and append-versus-resume PullHint
pacing. These distinguish expected replication amplification from a delayed
first wakeup; do not infer either from the aggregate RPC queue alone.
It must also retain measured-window gateway batch-record P99. A low global
gateway queue ratio does not rule out session-scoped head-of-line amplification
when recovery makes individual SEND micro-batches grow.
It must retain measured-window message idempotency definite-negative filter
skips and durable point reads. This proves whether unique high-QPS sends avoid
the per-message Pebble lookup instead of inferring that result from aggregate
LSM read amplification or CPU profiles.
It must retain maximum node-local channelappend router-group inflight,
capacity, and ratio. The per-batch group bound alone does not prove bounded
aggregate pressure when several gateway sessions submit concurrently.
Use `WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS` only for an
explicit single-variable diagnostic until the candidate value passes the
longer acceptance gates; changing it must not weaken same-session ordering.


## Rules

- Keep the scenario black-box through real `cmd/wukongim` processes, public
  WKProto sockets, public channel APIs, and public Prometheus metrics.
- Preserve 256 Hash Slots, 10 physical Slots, and three replicas.
- Explicitly disable Gateway Token authentication for these controlled tokenless
  load clients and verify the rendered setting. This fixture does not qualify
  SDK authentication; message send permissions remain enabled.
- Preserve the reviewed 96-worker, 8-item Channel replication RPC envelope and
  one commit-coordinator shard per physical message database.
- Keep the 250-message / 19,650-recipient-row / 2,545-online-route slice exact;
  diagnostic group-channel cardinality may change only channel reuse.
- Require the measured high-QPS SEND window to add zero ordinary membership
  mutation rows; setup mutations remain outside the counter delta.
- Keep setup outside the measured SEND window.
- Before setup and again immediately before cold prime, require all three nodes
  to agree on every actual Raft leader for the 10 non-empty logical Slots for a
  bounded stability window. A healthy `readyz` or a PreferredLeader assignment
  alone is not workload readiness.
- Emit one machine-readable `WKRC-HIFI-EVIDENCE` line for revision-neutral
  runners.
- Keep the sustained soak at 5,000 naturally hashed channels for acceptance;
  short duration or lower-cardinality runs are diagnostics, never substitutes
  for the 30-minute qualification.
- Do not treat absolute local throughput as cloud capacity. Compare exact
  revisions on the same host and preserve raw evidence.
- Keep the scenario opt-in and bounded. It is e2e evidence, not a unit test.

Revision-neutral permission RPC counters include both old per-Slot services and
the node-batched service. Soak evidence also reports P50/P95/P99, total outbound
transport bytes, Raft handler envelope counts, and complete-scrape aggregate CPU
mean (100% is one CPU). These Raft counts are envelopes, not embedded messages.
Current binaries expose fixed transport lane payload counters. Report the measured
outbound Raft-lane byte delta only when every node has the series at both
window boundaries; old/missing series or counter rollback remains null. These
bytes exclude wire headers, TCP/IP, TLS and network retransmission overhead.
Do not relabel total transport bytes as Raft bytes. Missing CPU remains null.

Keep `send_permission_admission_busy` separate from transport RPC admission
errors: the permission service can reject work in a successful RPC response or
on the local leader path. Count the public permission histogram's admission/busy
observations in both completed and failed windows; sustained acceptance requires
zero. Transport success alone is not evidence of successful permission reads.

Failure runtime diagnostics retain existing bounded gateway connection-close
reason counters to distinguish server overload closure from other EOF causes.
These snapshots do not claim per-shard queue telemetry or alter acceptance.

`WK_E2E_MEDIUM_RECIPIENT_DRIVER_DIAGNOSTICS=1` adds optional load-generator
timing through an observing socket Dialer. It separates scheduling lag,
SendFrame queue admission, and socket Write duration. The fixed 100-bin,
100-ms ring retains only the last ten seconds plus cumulative counts/maxima;
it retains no payload or user identity. Freeze it before failure-report HTTP
calls and emit `WKRC-PERMISSION-SOAK-DRIVER` once on success or failure.
Socket bytes include control frames and mean local TCP acceptance, not peer
receipt or SENDACK; fixed-bin maxima are diagnostic, not exact arrival bursts.
Keep pacing, timeout, workload, production configuration and acceptance gates
unchanged. Do not mix a probe-enabled run into an uninstrumented comparison.

`WK_E2E_MEDIUM_RECIPIENT_STALL_DIAGNOSTICS=1` adds a separate E2E-only
250-ms observer of the oldest unacknowledged SEND ordinal per sender. RECV
completion does not clear SENDACK pending state. Retain at most 32 samples
from the final eight seconds and only numeric sender/channel indices. The
first nonempty sample verifies the existing public channel-runtime probe;
later rounds probe at most 25 channels on each of three nodes only when an
oldest SENDACK age reaches 750 ms. One round at a time and a two-second
per-node deadline bound observer work. Freeze, cancel and join before failure
HTTP diagnostics or cleanup. Concurrent tracker cuts and node HTTP reads are
not atomic; LEO/HW do not identify a pending request's exact append phase.
Keep this diagnostic out of uninstrumented performance comparisons.

Failure goroutine diagnostics include independent Channel replication,
MessageDB, and commit/engine executor stacks as well as the quorum waiters.
The filtered output has a 256-KiB aggregate cap and reads at most 2 MiB per
node under the existing three-second request budget. Record node coverage and
truncation when interpreting it; these later snapshots do not prove a precise
pre-failure request phase. In unprofiled permission-soak runs, this collection
occurs after failure, not during SEND; the existing explicit profile mode may
also request a scheduled diagnostic and must remain separately identified.

The failure filter also retains Pebble-owned background goroutines. Every
fetched node reports HTTP status, input byte count, and whether the 2-MiB
input bound truncated it; filtered output saturation emits an explicit marker.
`WK_E2E_MEDIUM_RECIPIENT_REPLICATION_DIAGNOSTICS=1` preserves replication-stage
bucket/count/sum rows from the existing pre/post window scrapes, with the exact
queried node ID. It adds no mid-window request. Each boundary permits at most
4096 rows; overflow or capture failure stays explicit. The runtime samples
these completed stages, so histogram deltas cannot identify an in-flight
request or include a still-blocked operation's eventual duration.

`WK_E2E_MEDIUM_RECIPIENT_STORAGE_HISTORY=1` retains at most 180 node snapshots
from the existing one-second pressure sampler, restricted to fixed Pebble
metrics for channel_log/meta. Missing series stay absent and scrape failure
is explicit. The history logs after the sampler joins, including fatal exits;
wall-clock sample starts and fetch durations permit correlation but are not
an atomic cross-node snapshot or individual disk-operation timings.

Existing failure scrapes also retain exact permission count, duration count/sum,
and inflight families, including cold-prime failures before pressure sampling.
Do not expand this capture to histogram buckets or arbitrary name prefixes.
These cumulative failure snapshots are not measured-window deltas.
