# Chat Lifecycle Soak Phase 3: Worker Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the deterministic workload through real HTTP login synchronization and TCP WKProto while preserving every SENDACK/RECV correctness signal and exposing bounded, generation-fenced worker snapshots.

**Architecture:** A dedicated chat-lifecycle worker owns online sessions, bounded event queues, retry timers, verifier windows, and aggregate histograms. It reconstructs history on demand from Phase 2 indexes. Existing generic worker behavior remains the default; `wkbench worker --mode chat-lifecycle` selects a dedicated control server whose assignment generation cannot be mixed with another run.

**Tech Stack:** Go HTTP server/client, existing `internal/bench/wkproto`, existing `internal/bench/target`, bounded channels/heaps, WKProto TCP CONNECT/SEND/SENDACK/RECV/RECVACK, deterministic clocks in unit tests.

---

## Task 1: Make WKProto receive handling non-dropping

**Files:**

- Modify: `internal/bench/wkproto/client.go`
- Modify: `internal/bench/wkproto/client_test.go`

- [ ] **Step 1: Write a receive-pressure regression test**

Drive interleaved `SENDACK` and `RECV` frames through a tiny configured buffer. Pause the RECV consumer, then resume it. Assert every RECV is returned exactly once and in wire order; no frame may be silently discarded to make room for SENDACK.

Run:

```bash
GOWORK=off go test ./internal/bench/wkproto -run 'TestClient.*ReceivePressure' -count=1
```

Expected: FAIL because the current client drops non-priority RECV frames when the shared queue is full.

- [ ] **Step 2: Split priority SENDACK and lossless RECV paths**

Replace the shared drop-on-pressure queue with separate bounded `sendackCh`, `recvCh`, and `errCh` paths. The read pump must backpressure on `recvCh` instead of discarding frames; `ReadFrame` gives SENDACK bounded priority, then services RECV so a sustained SENDACK stream cannot starve delivery. Cancellation must unblock every send/receive wait.

- [ ] **Step 3: Add saturation and close tests**

Prove cancellation releases a blocked read pump, remote close returns the original terminal error once, bounded SENDACK preference cannot starve RECV, and buffer saturation is observable in worker queue gauges. Do not introduce an unbounded slice as a hidden queue.

- [ ] **Step 4: Run all WKProto client tests**

```bash
GOWORK=off go test ./internal/bench/wkproto -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/wkproto
git commit -m "fix(wkbench): preserve all received frames under pressure"
```

## Task 2: Add full conversation synchronization to the target client

**Files:**

- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`
- Create: `internal/bench/chatlifecycle/sync.go`
- Create: `internal/bench/chatlifecycle/sync_test.go`

- [ ] **Step 1: Write the exact request-contract test**

The fake target must receive this logical request for every login:

```json
{
  "uid": "derived-user",
  "version": 0,
  "last_msg_seqs": "",
  "msg_count": 20,
  "only_unread": 0,
  "limit": 500
}
```

Assert no cursor, last sequence, or prior client state survives logout/login.

- [ ] **Step 2: Implement `ConversationSync` decoding**

Decode the response array including channel ID/type, unread, timestamp, last/offset/read sequence, version, and recent messages. Decode recent payload bytes so the verifier can inspect the marker. Keep the method generic in `internal/bench/target`; put workload-specific rules in `chatlifecycle`.

- [ ] **Step 3: Write synchronization verdict tests**

Assert:

- 499 conversations may succeed;
- exactly 500 is `harness_invalid` because full sync is unproven;
- malformed/base64-invalid recents fail the login;
- per-conversation recent sequences never regress or duplicate;
- a response cannot seed the next login request.

- [ ] **Step 4: Implement the login sync evaluator**

CONNECT must succeed before sync. A session is traffic-ready only after sync completes. Record sync latency separately from gateway connection latency.

- [ ] **Step 5: Run focused tests**

```bash
GOWORK=off go test ./internal/bench/target ./internal/bench/chatlifecycle -run 'ConversationSync|LoginSync' -count=1
```

Expected: PASS.

## Task 3: Implement bounded message verification

**Files:**

- Create: `internal/bench/chatlifecycle/verifier.go`
- Create: `internal/bench/chatlifecycle/verifier_test.go`
- Create: `internal/bench/chatlifecycle/evidence.go`
- Create: `internal/bench/chatlifecycle/evidence_test.go`

- [ ] **Step 1: Write SENDACK and terminal-send tests**

For every logical send, require one successful SENDACK or a classified terminal result. Assert attempts reuse the Phase 2 `client_msg_no`, terminal-send count is exposed, and the verifier rejects conflicting message sequence assignments for the same logical message.

- [ ] **Step 2: Write full RECV validation tests**

Feed every payload class through RECV parsing and assert checksum, run marker, sender/channel identity, message sequence monotonicity, and RECVACK emission. Loss, duplicate delivery, corruption, or sequence regression must create a bounded failure sample and a non-pass verdict.

- [ ] **Step 3: Write exact-correlation sampling tests**

Deterministically sample exactly 1% of logical sends. Keep sampled correlations only until delivery or the configured terminal deadline, then remove them. Assert verifier memory is bounded by `rate * deadline`, not elapsed run duration.

- [ ] **Step 4: Implement bounded evidence retention**

Store aggregate counters/histograms plus a fixed maximum number of redacted first/last examples per failure class. Evidence keys must be stable hashes or sample indexes; raw UIDs, payloads, and tokens are forbidden.

- [ ] **Step 5: Run verifier tests with race detection**

```bash
GOWORK=off go test -race ./internal/bench/chatlifecycle -run 'Verifier|Evidence|Correlation' -count=1
```

Expected: PASS.

## Task 4: Run sessions, relationships, and traffic

**Files:**

- Create: `internal/bench/chatlifecycle/session.go`
- Create: `internal/bench/chatlifecycle/session_test.go`
- Create: `internal/bench/chatlifecycle/traffic.go`
- Create: `internal/bench/chatlifecycle/traffic_test.go`
- Create: `internal/bench/chatlifecycle/engine.go`
- Create: `internal/bench/chatlifecycle/engine_test.go`

- [ ] **Step 1: Write session lifecycle tests with a fake clock**

Prove a login performs CONNECT then full sync, schedules the approved duration, becomes eligible for edges only when both endpoints are online, and closes its socket/state on logout. Re-login must create a fresh connection and full version-zero sync. CONNECT carries a deterministic per-user token so the frame is realistic, but token persistence/validation is explicitly not a correctness assertion in this scenario.

- [ ] **Step 2: Write traffic-mix tests**

Run a short virtual workload and assert aggregate grants preserve 2,000 SEND/s, 90/10 person/group traffic, direction/payload distributions, one-minute traffic for the 100k group, and the approximately 10k bounded hot set. There must be no direct metadata/runtime mutation.

- [ ] **Step 3: Write retry and overload tests**

Simulate delayed SENDACKs and receive consumer pressure. Assert the engine uses only the three approved retries, records queue depth/inflight, and reports its own queue or CPU saturation as `harness_invalid` rather than blaming the target.

- [ ] **Step 4: Implement the engine with bounded ownership**

Own only online sessions, at-most-five-index pending relationship activation, active lifecycle timers, verifier deadlines, and bounded evidence. Use a min-heap or timing wheel for future work; never one goroutine or timer per historical user/channel.

- [ ] **Step 5: Add leak/race tests**

Start/stop the engine repeatedly with fake clients. Assert all goroutines exit, queues return to baseline, and the second start does not retain first-run identities.

- [ ] **Step 6: Run worker-engine tests**

```bash
GOWORK=off go test -race ./internal/bench/chatlifecycle -run 'Session|Traffic|Engine|Retry|Leak' -count=1
```

Expected: PASS.

## Task 5: Add a dedicated generation-fenced worker API

**Files:**

- Create: `internal/bench/chatlifecycle/worker_protocol.go`
- Create: `internal/bench/chatlifecycle/worker_server.go`
- Create: `internal/bench/chatlifecycle/worker_server_test.go`
- Create: `internal/bench/chatlifecycle/worker_client.go`
- Create: `internal/bench/chatlifecycle/worker_client_test.go`
- Modify: `cmd/wkbench/worker_command.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write the HTTP protocol tests**

Cover these authenticated endpoints:

```text
GET  /healthz
GET  /v1/info
POST /v1/chat-lifecycle/assign
POST /v1/chat-lifecycle/start
GET  /v1/chat-lifecycle/status
GET  /v1/chat-lifecycle/snapshot
POST /v1/chat-lifecycle/checkpoint
POST /v1/chat-lifecycle/rate
POST /v1/chat-lifecycle/stop
```

Every mutating request carries `run_id`, `assignment_id`, and generation. Reject mismatches, duplicate active assignments, unauthenticated requests, rate updates for a stopped generation, and start-before-assign.

- [ ] **Step 2: Define bounded snapshots**

Snapshots include phase, uptime, online/session counts, generated indexes, sent/SENDACK/RECV/RECVACK counters, retry/terminal counts, sync counts/latencies, delivery correlations, latency histograms, queue/inflight gauges, harness utilization, and bounded redacted evidence. They must not enumerate users or channels.

- [ ] **Step 3: Implement server/client state machines**

Default `wkbench worker` behavior remains unchanged. Add `--mode chat-lifecycle`; in that mode the process hosts only the dedicated lifecycle server and exits non-zero if its engine terminates unexpectedly.

- [ ] **Step 4: Test disconnect and stop semantics**

Coordinator polling loss does not silently stop load, but an explicit stop drains in-flight verification for a bounded interval and returns a final snapshot. Worker process termination is later classified as `harness_invalid` by the coordinator.

- [ ] **Step 5: Run control-plane and CLI tests**

```bash
GOWORK=off go test -race ./internal/bench/chatlifecycle ./cmd/wkbench -run 'Worker|ChatLifecycle' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bench/wkproto internal/bench/target internal/bench/chatlifecycle cmd/wkbench/worker_command.go cmd/wkbench/main_test.go
git commit -m "feat(wkbench): add chat lifecycle worker runtime"
```

## Task 6: Phase verification

- [ ] **Step 1: Run all directly affected tests**

```bash
GOWORK=off go test -race ./internal/bench/wkproto ./internal/bench/target ./internal/bench/chatlifecycle ./cmd/wkbench -count=1
```

Expected: PASS.

- [ ] **Step 2: Verify absence of silent RECV dropping and history maps**

```bash
rg -n 'default:.*drop|drop.*RECV|map\[.*\](User|Channel|Relationship)' internal/bench/wkproto internal/bench/chatlifecycle
```

Expected: no silent RECV-drop branch and no map whose lifetime tracks all historical users/channels.

- [ ] **Step 3: Inspect the phase diff**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors and no unrelated files staged.
