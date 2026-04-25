# Channel Log Entry Headers Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox syntax for tracking.

**Goal:** Make channel replicated records carry explicit log-entry header metadata so `Index` is the message sequence and `ID` is the message ID.

**Architecture:** Keep the existing `channel.Record` payload-based flow for compatibility, but extend it with log-entry header fields: `ID`, `Index`, and `Epoch`. Leader-side store reads and transport responses preserve assigned record headers; follower apply validates record index continuity before accepting entries. This lands the core invariant without renaming every existing `Record` call site.

**Tech Stack:** Go, `pkg/channel`, `pkg/channel/store`, `pkg/channel/replica`, `pkg/channel/transport`, targeted `go test`.

---

### Task 1: Record Header Metadata

**Files:**
- Modify: `pkg/channel/types.go`
- Test: `pkg/channel/store/channel_store_test.go`
- Test: `pkg/channel/handler/append_test.go`

- [x] **Step 1: Write failing tests**
  - Add a store test proving `Read` returns records with `Index == MessageSeq` and `ID == MessageID`.
  - Add a handler test proving append passes the generated `MessageID` in the record header.

- [x] **Step 2: Run tests to verify failure**
  - Run: `GOWORK=off go test ./pkg/channel/store ./pkg/channel/handler`
  - Expected: compile or assertion failure because `channel.Record` has no `ID` / `Index` header fields yet.

- [x] **Step 3: Implement minimal metadata**
  - Add documented `ID`, `Index`, and `Epoch` fields to `channel.Record`.
  - Set `Record.ID` in `handler.Append` from generated `MessageID`.
  - Populate `Record.ID` and `Record.Index` when store materializes records from rows.

- [x] **Step 4: Run tests to verify pass**
  - Run: `GOWORK=off go test ./pkg/channel/store ./pkg/channel/handler`

### Task 2: Apply-Fetch Index Validation

**Files:**
- Modify: `pkg/channel/replica/replication.go`
- Modify: `pkg/channel/store/compatibility_record.go`
- Test: `pkg/channel/replica/replication_test.go`
- Test: `pkg/channel/store/channel_store_test.go`

- [x] **Step 1: Write failing tests**
  - Add replica test: follower rejects fetched records whose first `Index` is not `local LEO + 1`.
  - Add store test: `StoreApplyFetch` rejects records whose header `Index` does not match the local append position.

- [x] **Step 2: Run tests to verify failure**
  - Run: `GOWORK=off go test ./pkg/channel/replica ./pkg/channel/store`
  - Expected: tests fail because fetched records are not validated by header index.

- [x] **Step 3: Implement validation**
  - Add helper logic validating non-zero fetched record indexes are contiguous from the expected first index.
  - In replica apply, reject non-contiguous fetched entry indexes before store apply.
  - In store row stamping, reject record headers that contradict the assigned message sequence and reject `ID` headers that contradict decoded payload IDs.

- [x] **Step 4: Run tests to verify pass**
  - Run: `GOWORK=off go test ./pkg/channel/replica ./pkg/channel/store`

### Task 3: Transport Header Preservation

**Files:**
- Modify: `pkg/channel/transport/codec.go`
- Modify: `pkg/channel/transport/longpoll_codec.go`
- Test: `pkg/channel/transport/codec_test.go`
- Test: `pkg/channel/transport/longpoll_codec_test.go`

- [x] **Step 1: Write failing tests**
  - Update fetch response codec tests to expect `ID`, `Index`, and `Epoch` round trips.
  - Update long-poll response codec tests to expect record headers round trip.

- [x] **Step 2: Run tests to verify failure**
  - Run: `GOWORK=off go test ./pkg/channel/transport`
  - Expected: assertions fail because codecs only preserve payload and size today.

- [x] **Step 3: Implement codec preservation**
  - Bump response codec versions.
  - Encode/decode `ID`, `Index`, and `Epoch` before each record payload.

- [x] **Step 4: Run tests to verify pass**
  - Run: `GOWORK=off go test ./pkg/channel/transport`

### Task 4: Integrated Verification

**Files:**
- Existing channel package tests.

- [x] **Step 1: Run targeted channel tests**
  - Run: `GOWORK=off go test ./pkg/channel/...`
  - Expected: all channel packages pass.

- [x] **Step 2: Review diff**
  - Run: `git diff --stat && git diff --check`
  - Expected: no whitespace errors; changes stay within channel log-entry header scope.


### Verification Notes

- `GOWORK=off go test ./pkg/channel/...` passes.
- `GOWORK=off go test ./pkg/...` passes.
- `GOWORK=off go test -run TestThreeNodeAppSendAckSurvivesLeaderRestartAfterPendingCheckpointReconcile -count=1 ./internal/app` is not used as a gate for this plan because the same test also fails on `main` without these changes.
