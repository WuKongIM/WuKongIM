# Stable retryable errors for message and conversation reads

Status: implemented and verified on 2026-09-14 Asia/Shanghai. This resolves the
legacy HTTP 400 retryability finding in the
[concurrent recovery report](2026-09-14-message-update-concurrent-recovery.md).
Changes remain uncommitted in the `codex/message-updates` worktree.

## Contract

`/messages`, single-channel `/channel/messagesync`, `/conversation/list` and
`/conversation/sync` return known temporary failures as HTTP 503:

```json
{"status":503,"code":"unavailable","msg":"temporarily unavailable"}
```

Legacy `status`/`msg` fields and successful response shapes remain present. This
intentionally changes some prior 400/500 infrastructure failures to 503. Clients
retain the exact query, cursor and cached content, then retry with bounded
backoff/jitter. A connection failure remains an uncertain attempt, never an empty
page. Parameter/business rejection and unknown failures do not automatically
acquire retryability. CMD, mutation and batch error contracts are unchanged.

The API adapter owns the envelope; `internal/runtime/readavailability` classifies
wrapped/joined typed network and authority failures. Generic transport RPC errors
are accepted only when the complete remote message equals a known transient
cause. Arbitrary substring matching is forbidden. No changes were made to Raft,
storage, retry scheduling, success-path payload assembly or query fanout. The new
classification only executes when a read already failed and adds no success-path
DB operations or RPCs. No performance improvement is claimed or newly benchmarked.

## Validation

The new four-route regression failed before implementation: several temporary
errors returned HTTP 400/500, and even the existing list 503 response lacked a
stable code. After implementation, the complete API package and new runtime
package unit suites passed. Negative tests cover business/visibility errors,
unknown corruption errors, arbitrary `EOF`/leader text, unrelated RPC error codes
and cancellation. Positive tests cover wrapped/joined causes, connection reset,
read deadlines and the observed remote physical Slot not-leader error.

The real-process E2E client removed all legacy error-message matching. HTTP retry
acceptance now requires 503 `code: unavailable` or the existing edit 409
`stale_meta`; arbitrary HTTP 400/503 errors fail the run. The same 256-Hash-Slot,
12-physical-Slot, three-replica workload completed one full run in **256.38 seconds**:

- **3,511 successful concurrent edits**, **7,021 read/idempotency checks**.
- **377 counted transient attempts** during fault/recovery; zero healthy-phase errors.
- Observed Channel Leader changed 3 → 1 and physical Slot Leader changed 3 → 2.
  Every writer and read path progressed before the former leader restarted.
- All twelve final versions (294–295) converged through incremental pages; every
  ordinary read API then passed through each of the three ingress nodes.
- Final incremental response was caught up and empty without reset. CAS,
  historical idempotency, content epoch, immutable identity, tail previews,
  ordering and unread assertions passed.

The first captured HTTP failure from each of the four selected read APIs was
exactly the new 503 envelope. The harness checked every HTTP error's status/code,
not just these samples. All failed requests preserved cursor/cache state.

`go vet` passed for the API, runtime and E2E packages. The policy-selected
`flow-doc-contracts` check passed (81 compliant FLOW files, no invalid files;
eight line-count warnings). Its initial missing-heading failure was corrected.
`git diff --check` passed. The scenario instructions, both E2E catalogs, API spec,
project knowledge, FLOW index and Unreleased changelog are updated.

## Evidence and limits

[Machine-readable receipt](2026-09-14-message-read-retry-errors.json) includes
phase counts/timestamps, first-error samples and source/instruction hashes.
The server SHA-256 is `434e2b4e9c63f8f7c8525c893141392a4fbab5f6de1b47663430c06dc7b85577`.
The harness SHA-256 is `eb72404ff79520cbed754f10b11f7e84a89c4d059a87ae5d251a7bbf7a08f386`.
Raw logs and binaries remain under `tmp/message-read-errors/`. The same frozen
Linux ARM64 Docker image, CPU set 0–5, 6 GiB and driver/server GOMAXPROCS=2 were
used as in the preceding recovery test. The temporary container and owned test
processes were removed after completion.

This is one bounded same-host correctness/recovery run with twelve edited
messages, not maximum throughput, multi-hour endurance, SDK artifact execution,
100,000-member fanout, network-partition or production release qualification.
The generic RPC protocol still lacks a typed code for every underlying cause;
unrecognized remote errors remain failures under the existing envelope rather
than being guessed retryable. The stable public HTTP code removes this protocol
knowledge from clients for the recognized transient failures.
