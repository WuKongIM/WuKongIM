# Message update concurrency and recovery

This scenario owns a bounded black-box three-node message-edit stability test.
Use real processes and public API/Manager evidence only. Keep 256 Hash Slots,
12 physical Slots and three replicas. Use three independent writers over twelve
retained originals, a static control conversation, and concurrent history,
exact lookup, list, legacy sync, incremental cursor and historical-idempotency
readers. Payloads encode original identity and version; acknowledged versions
captured before request dispatch are lower bounds for ordinary read responses.

Run with `WK_E2E_MESSAGE_UPDATE_STABILITY=1`, an absolute
`WK_E2E_MESSAGE_UPDATE_STABILITY_REPORT` path and optional `WK_E2E_BINARY`:
`GOWORK=off go test -tags=e2e ./test/e2e/message/message_updates -count=1 -timeout=8m -v`.

Preserve 60 seconds healthy, 45 seconds with the observed Channel leader
abruptly terminated with SIGKILL, 45 seconds restored, then the same stop/restore
cycle for the observed physical Slot leader. Stop only one owned process at a time. Public Manager
evidence must identify each actual leader. Before restarting its former leader,
require the surviving cluster to elect a different authority and every writer
and reader to make progress. Record failures during convergence separately from
successful reads; retry uncertain edits only with the exact same request ID, expected
version and payload. All four ordinary read APIs must return HTTP 503 with `code: unavailable`
for temporary failures; edit/delta retries also recognize 409 `stale_meta`.
Transport failures remain explicit temporary attempts during fault injection.
Never parse legacy error text or retry an HTTP 400. Every failed query preserves
its original cursor and cache state.
Other unexpected status/envelope or incorrect successful content fails immediately. Never treat a failed query as an empty page or advance its
cursor. Preserve bounded per-endpoint error counts and phase timestamps in the
report.
Register bounded process diagnostics before waiting for initial HTTP readiness,
so startup exits retain the same evidence as workload and recovery failures.
Recovery phases also require progress from every reader and writer; final
verification accepts no transient failures.

Incremental reads use limit two and persist merged versions with each cursor.
After stopping writers, drain to exact final state, verify all retained messages
through every ingress and both conversation interfaces, then require a caught-up
empty page. Ordinary restart/leader movement must preserve restore epoch.
Initial competing CAS edits must produce one winner; later identical retries
must return their original result. Keep goroutine errors in a bounded channel
and join all workers before teardown. This is bounded same-host correctness and
recovery evidence, not maximum throughput or SDK/online EVENT acceptance.
