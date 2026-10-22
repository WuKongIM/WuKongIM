# 79a rehearsal status check — 2026-10-23

The two-hour rehearsal did **not pass**. The exact request `chat-20261021T214505Z-00299e37` started at 01:35:17.578381188 Asia/Shanghai and automatically stopped at 01:36:18.801475417 after 61.223094218 seconds with `product_failure / message_loss`. Source is `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`; no replacement run was dispatched.

Actual terminal counts are 122,002 SEND and 122,002 SENDACK: 1,992.7448 per second averaged over the measured lifetime (includes two canary messages). This is not a current sliding-window measurement. The workload has stopped; all three workers are in final phase and all 10,000 sessions closed for generation_stop. The worker daemon services remain active, while the rehearsal coordinator service is failed with exit code 7. Active daemons do not mean active sending.

There is one expired delivery-correlation sample: 1,277 sampled, 1,276 delivered, one expired. No SEND first-attempt failures, duplicates, corruptions, sequence regressions, or harness saturation/underdelivery were recorded. Worker 2 evidence includes sample index 9223380832947807030, fingerprint dd2ac72c40e88775085c2847098368e0, and a 10-second expiration value. These are failure evidence, not a proven product root cause.

Hot SENDACK mean was 61.37 ms; cold first-create SENDACK mean was 83.75 ms. Both P99 histogram upper bounds are 400 ms (not precise percentiles). Maximum observed values were 439.02/496.51 ms. Latency warnings/anomalies were zero. Twelve cluster health samples were healthy; long-window performance and 2-hour acceptance remain incomplete.

The scheduled finalizer run 39824958109 completed successfully after collecting bounded terminal evidence. Its success means evidence collection succeeded, not workload acceptance. Issue #1025 advanced to rehearsal_diagnosis_pending at 01:44:32. The diagnosis window ends at 03:36:18 Asia/Shanghai; the original four hosts are retained within this existing bound. Both rehearsal-finalizer and safety-release schedules were active at the latest read. This artifact contains no zero-inventory release proof and does not permit a formal 72-hour continuation.

The typed terminal cost estimate is CNY 20.9398, below this request's CNY 1126.5672 operational stop. It is not an actual bill, and the separate CNY 223.4328 prior full-quote reserve is not a bill either. No budget, permissions, timeout, resources, or source were changed by this read-only check.

Artifact 12445294344 was downloaded and verified for exact byte size, SHA-256, ZIP CRC and all member hashes. Its SHA-256 is 3d220165e8cb1e9d310c49708ffe3a72582ed0732bc4e0c5d4a13e435083d11f. Terminal payloads are bound to the original handoff, source, budget, run fence, receipt and quote. The capture manifest at 01:44:24 has complete=true and capture_failures=0; range-go and range-counters contain no series, while other fixed metric captures contain samples. The captured metric window is bounded to the terminal timestamp, not the time of this local check. Heap and goroutine profiles were collected after the failure.

Only the explicit public allowlist was retained. Private SSH material, SSH configuration, raw Actions job logs, and the raw downloaded ZIP are excluded. No extra SSH or MCP call, retry, procurement or dispatch was performed. Both earlier frozen public evidence groups were rechecked and remain unchanged.

Evidence links:
- https://github.com/WuKongIM/WuKongIM/issues/1025
- https://github.com/WuKongIM/WuKongIM/actions/runs/39824958109
- https://github.com/WuKongIM/WuKongIM/actions/runs/39824958109/artifacts/12445294344
