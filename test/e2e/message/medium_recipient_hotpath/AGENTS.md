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
shape and permits only bounded diagnostic overrides: duration 10 seconds to 30
minutes, QPS 500 to 20,000, and 25 to 5,000 group channels in multiples of 25.
It uses 25 sender/receiver pairs across all three nodes and naturally hashed
channels, and it requires both local and remote permission routes. Its latency
histograms have a fixed 10,001-bucket bound and only incomplete messages occupy
the in-flight map, so the harness does not retain one object per completed
message over a long run. Public metrics sample transport RPC executor pressure,
permission Slot RPC queue/admission/in-flight state, managed permission-batch
goroutines, heap/GC, plugin conservation, and membership mutation rows. A
premature failure emits one bounded `WKRC-PERMISSION-SOAK-FAILURE` JSON row;
a completed run emits `WKRC-PERMISSION-SOAK-EVIDENCE`.


## Rules

- Keep the scenario black-box through real `cmd/wukongim` processes, public
  WKProto sockets, public channel APIs, and public Prometheus metrics.
- Preserve 256 physical hash slots, 10 logical Slot groups, and three replicas.
- Preserve the reviewed 96-worker, 8-item Channel replication RPC envelope.
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
