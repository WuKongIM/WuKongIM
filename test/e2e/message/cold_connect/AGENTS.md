# Cold CONNECT scenario

This process-level black-box test is opt-in with `WK_E2E_COLD_CONNECT=1`. Use a frozen native server through `WK_E2E_BINARY`; no Docker or server-internal imports.

- Fresh three-node cluster,256HashSlots,12logicalSlots,three replicas;300distinct authenticated users at100firstCONNECT/s.
- The default `paced` arrivals are10ms apart. `WK_E2E_COLD_CONNECT_ARRIVAL=one_second_burst` releases100 callers at each of three one-second boundaries, isolating the native bootstrap's arrival shape. Tokens remain preprovisioned in both controls; this does not reproduce the full native bootstrap.
- `WK_E2E_COLD_CONNECT_TOKEN_TIMING=on_arrival` prepares each token immediately before its first CONNECT, with an independent unchanged5s preparation budget and5s CONNECT budget. The fixed public `/user/token` API reaches the durable user port; it does not reproduce the native Bench API adapter or full-sync work. Retain preparation failures and timing separately.
- `WK_E2E_COLD_CONNECT_POPULATION=10000` selects the sole larger diagnostic population at the same100firstCONNECT/s, with a180s scenario bound and4MiB final-artifact cap; use `-timeout=5m` and `on_arrival` token timing. It retains10k clients but adds no full-sync, group setup or SENDs, so it remains a reduction rather than original-scale qualification. The default300-user budget remains110s.
- `WK_E2E_COLD_CONNECT_STARTUP_STACKS=1` adds exactly two diagnostic cuts at2s/4s after first arrival, one concurrent request per node per cut. Each uses the existing2s/1MiB aggregate-stack bound; captures may be truncated. Probes cancel/join before the after-cut. This instrumented control never replaces unprofiled performance qualification or changes any original outcome/deadline.
- Keep production3s activation and5s client deadlines; no Register/CONNECT retry or injected delay.
- Top history must be disabled; metrics remain enabled. No groups or business SENDs are prepared.
- Retain closed CONNACK/timeout categories and timing, bounded public metric observations and post-failure stack hints. Missing evidence is unknown; a1MiB stack file is potentially truncated.
- Existing before/after scrapes also retain the fixed `gateway/async_auth` runtime-pressure families, including queue wait. This adds no scrape, no traffic-window observation and no gate change.
- Join all request, client and process ownership before writing the final bounded JSON. This reduction never replaces original10k/2k/256 or2h qualification.

Run: `WK_E2E_COLD_CONNECT=1 WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_COLD_CONNECT_REPORT_DIR=/absolute/new/report GOWORK=off go test -tags=e2e ./test/e2e/message/cold_connect -count=1 -timeout=3m -p=1 -v`.
