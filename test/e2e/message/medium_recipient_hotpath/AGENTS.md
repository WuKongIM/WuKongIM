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

Set `WK_E2E_MEDIUM_RECIPIENT_QPS` or
`WK_E2E_MEDIUM_RECIPIENT_ROUNDS` only for bounded diagnostic stress runs.
Normal acceptance stays fixed at 4,500 offered messages per second. Nightly
sets `WK_E2E_MEDIUM_RECIPIENT_CI_SCALE=1` together with the strictly reviewed
500/s QPS override because a shared two-core runner cannot represent absolute
Cloud Medium capacity while hosting three nodes, all clients, and the sampler.
The CI-scaled gate retains the exact workload shape, latency limits, queue and
plugin conservation, allocation/GC ceilings, and process continuity.
Public pressure metrics are sampled every 500ms for the 500/s CI gate and every
250ms otherwise, keeping roughly the same cross-node sample count without
letting the three in-process Prometheus encoders dominate allocation on a
shared two-core runner.
Allocation acceptance separates a 360,000-byte/message budget from a bounded
30MB/s allowance over the fixed paced duration. A slow drain cannot enlarge
that allowance and hide a product-path allocation regression.

## Rules

- Keep the scenario black-box through real `cmd/wukongim` processes, public
  WKProto sockets, public channel APIs, and public Prometheus metrics.
- Preserve 256 physical hash slots, 10 logical Slot groups, and three replicas.
- Keep the 250-message / 19,650-recipient-row / 2,545-online-route slice exact.
- Keep setup outside the measured SEND window.
- Emit one machine-readable `WKRC-HIFI-EVIDENCE` line for revision-neutral
  runners.
- Do not treat absolute local throughput as cloud capacity. Compare exact
  revisions on the same host and preserve raw evidence.
- Keep the scenario opt-in and bounded. It is e2e evidence, not a unit test.
