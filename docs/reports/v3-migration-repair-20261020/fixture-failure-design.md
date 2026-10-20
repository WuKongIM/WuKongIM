# ordinary_history fixture repair: failure-first design

Base: 288247b3ab410eed30c1ee70b064c37e93b304d8. This record precedes fixture edits.

## Existing red evidence

/tmp/wukongim-v3-rc-20261020/logs/08-ordinary-history.jsonl preserves the original 1→3 post-full-restart read failure:
HTTP 400, internal/message: append failed: channel replication: recovery probe incomplete.
The original 20-second history loop only retried HTTP 400 containing a route-not-ready substring. The public current contract instead uses HTTP 503 with top-level code="unavailable" and numeric status=503. Product error classification repair is owned by the root agent. This fixture change must not hide the original 400.

## Acceptance and failure cases (before edits)

1. Only HTTP 503 plus valid JSON top-level exact lowercase code="unavailable" and numeric status=503 permits another identical public history request.
2. HTTP 400, including the original recovery-probe-incomplete symptom and legacy route-not-ready text, fails immediately.
3. Other HTTP statuses, network/transport errors, malformed JSON, absent/mistyped status/code, other code, maintenance responses, and unstructured HTTP 503 fail immediately.
4. Repeated exact unavailable responses end at the unchanged 20-second deadline; retry spacing remains 100 ms. No outer test timeout increase or per-attempt deadline reset.
5. Any successful but wrong history page fails immediately against the complete expected rows; no retry on missing, reordered, extra, changed-ID/sequence/payload/header records.
6. Preserve source fixtures, original credentials, idempotent import, real WKProto append, exact HTTP append idempotency, complete restart before any warming append, and Channel Leader stop/restart assertions across all five topology cases.
7. Add only bounded scalar stage/node/retry observations to the existing go test JSONL evidence if needed; no credential, private database, or unbounded response collection.

## Minimal implementation plan

Replace only the local history retry predicate with HTTP 503 plus exact top-level JSON map keys code/status. Avoid substring matching and broad 503 retries. Retain 20 s/100 ms as written. Remove unused strings import and add net/http for the named status. Give history checks phase names and log one verified read observation per node/phase, with temporary-retry count; include phase/node in existing bounded failure diagnostics. The root agent's -json runner retains this evidence, so do not add another artifact writer or environment knob. Update the scenario AGENTS.md with this narrow public contract and reproducible commands.

## Validation handoff (no E2E run by this subagent)

First run the 1→3 slice against the root agent's built repaired candidate, then the existing entire five-topology package, preserving separate JSONL logs and binary/source identity. Existing public API negative classification tests remain the product owner's responsibility. No implementation-mirroring fixture unit tests are added. Compile-only verification and gofmt/diff checks may run here without launching the E2E binary; root owns live process tests.

Targeted selector: ^TestOriginalOrdinaryHistoryTopology$/^1_source_nodes_to_3_target_nodes$
Package: ./test/e2e/migration/ordinary_history
Full bound remains: -count=1 -timeout=12m -p=1 -parallel=1 -json

This work is fixture alignment, not proof the product recovery defect is fixed. Original red evidence remains unchanged and must be compared with candidate regression evidence.
