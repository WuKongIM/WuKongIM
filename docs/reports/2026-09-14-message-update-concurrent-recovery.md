# Concurrent message-edit recovery validation

Status: passed one complete real-process run on 2026-09-14 Asia/Shanghai
(2026-09-13 UTC). The same frozen server binary completed Channel Leader and
physical Slot Leader SIGKILL/restart cycles under concurrent edits and reads.
This is bounded correctness/recovery evidence, not production capacity or
release qualification. Product code was unchanged during this task; feature
changes remain uncommitted in `codex/message-updates`.

## Workload and acceptance

A Linux ARM64 container runs three real servers with 256 Hash Slots, 12 physical
Slots, three replicas, CPUs 0–5, 6 GiB and driver/server GOMAXPROCS=2. Image
`golang:1.25.11-bookworm` has immutable ID
`sha256:63cacf247cfd45aa03d105a9a86e2811f978514997be98068979ba6e4c2d0534`.
Server SHA-256:
`c0d08e5b3423282bbb6b56636a48c1930c9cede0ea495107b3ff93dd1ee4af66`.
The [machine-readable report](2026-09-14-message-update-concurrent-recovery.json)
preserves the final harness/instruction hashes, all seven attempts, timestamps,
counts, bounded first-error samples and raw-log hashes. Raw diagnostics remain
under `tmp/message-update-stability/` in the implementation worktree.

Three independent writers edit twelve retained originals, partitioned four per
writer. Six other workers exercise history, exact lookup, conversation list,
legacy conversation sync, incremental pages and historical idempotent retries.
Each worker uses a 200 ms ticker and has at most one request in flight; the
nominal workload is 15 mutations plus 30 read/idempotency checks per second.
Timeouts and missed ticks are not an offered-throughput benchmark. Requests use
three ingress addresses, excluding the intentionally stopped node after failure
injection; already in-flight requests can still reach that node.

The healthy phase lasts 60 seconds. The observed Channel Leader is then killed
for 45 seconds and restarted, followed by 45 seconds of continued traffic. The
same cycle targets the current physical Slot Leader. Public Manager evidence
must prove an authority change, and each writer and each read path must complete
more than ten operations during the down phase **before** restarting the former
leader. Restart phases require the same per-worker progress. Only one owned
process is stopped at a time. These are bounded progress assertions, not precise
measurements of the first recovered request or an uninterrupted-availability SLO.

Every successful ordinary read is checked against versions acknowledged before
request dispatch. Payloads encode message identity and version. Sequence, ID,
client number, sender and original timestamp must remain stable. Conversation
list order, activation time, unread count and the static control conversation
must remain unchanged; both conversation endpoints must show the edited tail.
Legacy sync uses a caught-up tail sequence and must still include that edit.

Incremental pages use limit two and persist each cursor atomically with the
merged version cache. Normal leader movement/restart must never request a restore
reset or change the content epoch. The test retries uncertain mutations with the
same request ID, expected version, epoch and payload. A historical successful
request must continue returning its original response despite later edits.
Initial competing CAS requests at version zero must produce exactly one success
and one version conflict.

## Completed run

Attempt 7 passed in **256.20 seconds**. Its workload completed **3,519 mutation
requests** and **7,039 read/idempotency checks**, with **346 explicitly counted
transient failures** during fault/recovery phases. No healthy-phase request
failed. These counts exclude fixture setup and final reconciliation checks.

The following counts are phase deltas, not cumulative totals. Read checks include
historical idempotency calls; reported leader changes are observed before restart.

| Phase | Successful edits | Successful read/idempotency checks | Transient attempts | Authority change |
| --- | ---: | ---: | ---: | --- |
| healthy | 897 | 1794 | 0 | — |
| channel_leader_down | 590 | 1185 | 253 | 3 → 1 |
| channel_leader_restored | 694 | 1385 | 1 | — |
| slot_leader_down | 645 | 1290 | 92 | 3 → 2 |
| slot_leader_restored | 693 | 1385 | 0 | — |

Both cycles targeted node 3 because it was the observed relevant authority at
each injection. The first cycle moved the Channel Leader to node 1. The second
moved the physical Slot Leader to node 2 while the Channel had already failed
over. This does not claim coverage of every placement or topology.

After workers joined, exact retries resolved any canceled in-flight mutation and
advanced each original once for deterministic final verification. The incremental
cache converged to all twelve final versions (295–296); no update was missing.
There were 932 `more=true` pages including final drain. All four ordinary read
APIs then passed through each of the three ingress nodes. A final incremental
request returned no rows, no reset and `more=false`. CAS, historical idempotency,
immutable identity, content epoch, preview, unread and ordering assertions passed.

## Calibration failures and compatibility finding

All failed attempts are preserved, rather than counted as successful validation:

| Attempt | Exact reason the test stopped | Classification |
| --- | --- | --- |
| 1 | Compared missing legacy `timestamp` in list DTO with historical seconds | Harness DTO mismatch; normalize list `server_timestamp_ms` to seconds while separately checking unchanged milliseconds |
| 2 | `/messages` returned HTTP 400 with `internal/message: append failed: EOF` after termination | Legacy transport-error envelope |
| 3 | `/channel/messagesync` returned HTTP 400 for connection refusal to the killed node | Legacy transport-error envelope |
| 4 | `/conversation/sync` added its `route not ready` wrapper around that refusal | Legacy conversation error wrapping |
| 5 | `/conversation/sync` returned the killed peer's connection-reset error | Existing-connection failure during SIGKILL |
| 6 | Restart produced `remote_error: multiraft: not leader` through `/messages` | Temporary physical Slot authority change; its preceding Channel down phase had already passed |

Source inspection confirmed that legacy handlers use `{msg,status}` and may map
infrastructure errors to HTTP 400; `/conversation/list` can instead return 503,
and the new edit API returns typed 503/409 failures. The harness normalizes only
known wrappers and matches bounded causes: EOF, refusal/reset/broken-pipe for the
injected peer, deadline expiry and enumerated authority errors. It does not accept
arbitrary 400s, arbitrary `append failed` causes or successful stale payloads.
Failures never clear cache entries or advance an incremental cursor. Final
verification accepts no errors. Cancellation during worker teardown is excluded
from the final harness counters; earlier calibration receipts retain their
original counters and samples.

This leaves an API usability issue: a client cannot infer retryability from HTTP
400 alone on old read endpoints. Standardizing retryable error codes needs an
explicit compatibility design; production SDKs should not copy test-specific
error-string matching or blindly retry every 400. No product error envelope was
changed to obtain this pass.

Follow-up: the legacy retryability finding was subsequently addressed and
verified with a strict public-code client; see the
[retry-error implementation report](2026-09-14-message-read-retry-errors.md).
The observations above describe the earlier binary and remain unchanged.

## Reproduction and scope

Run the new scenario from the repository root, with an optional prebuilt server:

```sh
WK_E2E_MESSAGE_UPDATE_STABILITY=1 \
WK_E2E_MESSAGE_UPDATE_STABILITY_REPORT=/tmp/message-update-stability.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/message_updates -count=1 -timeout=8m -v
```

The recorded run used a precompiled Linux ARM64 harness and `WK_E2E_BINARY`
pointing to the frozen candidate. Focused `go vet -tags=e2e` and `git diff --check`
also passed. Scenario instructions and both E2E catalogs are updated.

The complete pass uses twelve messages, one edited group and one control group,
with loopback transport and container-local storage. It does not qualify long
multi-hour operation, maximum QPS, 100,000-member fanout, SDK persistence, online
EVENT delivery, network partitions, restore-epoch replacement, rolling upgrade
or membership/retention changes during editing. Earlier CPU/query reports remain
the performance evidence; this run adds concurrent-edit and process-crash recovery
coverage. Test processes and the temporary container were removed after completion.
