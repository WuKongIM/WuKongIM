# Rate-limit SENDACK removes the fault-window disconnect

Source `22f473832` (gateway rejects overflowing SEND with `ReasonRateLimit`
instead of closing the session). Same load as prior fault loops: 5000
channels, 4500 SEND/s, cap32, 60 s, 380 ms WAL sync hold starting at 25 s.

| Metric | async-worker (`f93205581`) | rate-limit (`22f473832`) |
| --- | --- | --- |
| Outcome | `sender_read: EOF` at 217 s | ran to completion, no EOF |
| Messages completed | partial | 270000 / 270000 |
| Completion/s | - | 4499 |
| SENDACK p50 / p99 / max | 12 / - / - ms | 12 / 4809 / 9817 ms |
| RECV p99 | - | 4771 ms |
| Rate-limit SENDACKs (retries) | n/a | 1157958 |

Verdict: the disconnect is gone and throughput recovers to offered load after
the fault window. The run still fails the soak's `SENDACK P99 <= 1000ms`
assertion. Each message was rejected ~4.3 times on average: the fixed 20 ms
harness backoff re-offers load faster than the 380 ms sync drains it, so the
tail latency is dominated by retry churn during and right after the window.

Open questions: whether the 1000 ms p99 gate should apply to an injected
380 ms WAL fault run, and whether retries should use exponential backoff.
Evidence: `assets/send-ban-ratelimit-20260928/`.
