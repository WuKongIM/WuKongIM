# Cold CONNECT scenario

This process-level black-box test is opt-in with `WK_E2E_COLD_CONNECT=1`. Use a frozen native server through `WK_E2E_BINARY`; no Docker or server-internal imports.

- Fresh three-node cluster,256HashSlots,12logicalSlots,three replicas;300distinct authenticated users at100firstCONNECT/s.
- Keep production3s activation and5s client deadlines; no Register/CONNECT retry or injected delay.
- Top history must be disabled; metrics remain enabled. No groups or business SENDs are prepared.
- Retain closed CONNACK/timeout categories and timing, bounded public metric observations and post-failure stack hints. Missing evidence is unknown; a1MiB stack file is potentially truncated.
- Join all request, client and process ownership before writing the final bounded JSON. This reduction never replaces original10k/2k/256 or2h qualification.

Run: `WK_E2E_COLD_CONNECT=1 WK_E2E_BINARY=/absolute/frozen/wukongim WK_E2E_COLD_CONNECT_REPORT_DIR=/absolute/new/report GOWORK=off go test -tags=e2e ./test/e2e/message/cold_connect -count=1 -timeout=3m -p=1 -v`.
