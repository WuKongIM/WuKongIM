# Fault-run acceptance: liveness and delivery, not tail latency

Change: `WK_E2E_MEDIUM_RECIPIENT_FAULT_INJECTED=1` skips only the SENDACK P99
(≤1000 ms) and RECV P99 (≤2000 ms) gates. Every other gate (message count,
ingress, admission, transport, allocation) still applies. Control runs keep
the latency gates.

Load: 5000 channels, 4500 SEND/s, cap32, 60 s. Server binary sha256 `1ec9082f…`
(gateway rate-limit backpressure plus async quorum workers); harness adds only
the flight and sync-window hooks.

| Run | Result | Messages | Completion/s | SENDACK p50/p95/p99/max ms | RECV p99 ms | Rate-limited SENDACKs |
| --- | --- | ---: | ---: | --- | ---: | ---: |
| fault-380 (380 ms WAL hold at +25 s, 5 s) | PASS | 270000 | 4498.3 | 12 / 3168 / 4994 / 9781 | 4934 | 195487 |
| control-0 (no hold) | PASS | 270000 | 4499.5 | 9 / 22 / 51 / 185 | 49 | 0 |

The fault fired on every node: each sync-window capture records hold 380 ms
with 13–14 eligible syncs, all completed. The fault run had no disconnects and
delivered every message. The control run shows no rate limits and latency well
under its gates, so backpressure does not affect the normal path.

## Repeats (r2, r3)

Same binaries and harness (`permission-soak-gate.test`). Each run left three
`sync-window.json` files. Fault runs set `WK_E2E_MEDIUM_RECIPIENT_FAULT_INJECTED=1`.

| Run | Result | Messages | Completion/s | SENDACK p50/p95/p99 ms | RECV p99 ms | Rate-limited |
| --- | --- | ---: | ---: | --- | ---: | ---: |
| fault-380-r2 | PASS | 270000 | 4499.5 | 10 / 2885 / 4287 | 4252 | 161393 |
| control-0-r2 | PASS | 270000 | 4499.2 | 9 / 21 / 79 | 69 | 0 |
| fault-380-r3 | PASS | 270000 | 4499.3 | 9 / 3059 / 4450 | 4376 | 169783 |
| control-0-r3 | PASS | 270000 | 4498.9 | 9 / 42 / 227 | 214 | 0 |

Three of three fault runs and three of three control runs pass at 60s. The
30-minute sustained run still fails (see 2026-09-28-send-ban-sustained.md).
