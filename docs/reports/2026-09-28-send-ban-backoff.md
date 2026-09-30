# Exponential resend backoff cuts retries but not fault-window p99

Harness change: rate-limited SENDs resend with per-message exponential backoff
(20 ms doubling to 500 ms, 50% jitter) instead of a fixed 20 ms. Product binary
unchanged from `22f473832` (`wukongim-ratelimit`).

Load: 5000 channels, 4500 SEND/s, cap32, 60 s, 380 ms WAL sync hold from +25 s
for 5 s. The fault was confirmed armed: `sync-control.json` written and every
node's `sync-window.json` shows 13-14 eligible/completed held syncs.

| Metric | fixed 20 ms | exponential backoff |
| --- | ---: | ---: |
| connection EOF | none | none |
| rate-limited SENDACKs | 1157958 | 186968 |
| completion/s | ~4500 | 4498.5 |
| SENDACK p50 / p95 / p99 ms | 12 / - / 4809 | 14 / 3536 / 4689 |
| result | FAIL (p99 > 1000 ms) | FAIL (p99 > 1000 ms) |

Backoff removes about 84% of resends, yet p99 barely moves. The tail is set by
the ~5 s window in which every sync takes 380 ms, not by retry amplification.

An earlier run of this harness passed with p99 92 ms and zero rate limits; it
omitted `WK_E2E_FLIGHT_DIR`, so the fault was never armed. It is not evidence.
