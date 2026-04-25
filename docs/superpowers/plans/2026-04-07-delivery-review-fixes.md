# Delivery Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the delivery actor progress wedges found in review, make resolver failures retry instead of silently dropping realtime delivery, and harden person-channel canonicalization compatibility.

**Architecture:** Keep send durability and realtime delivery decoupled. Extend the in-memory delivery actor so budget release and resolver retry both flow through a single resume path for unresolved inflight messages. Preserve legacy person-channel ordering for normal cases while adding a deterministic tie-break only for CRC32 collisions.

**Tech Stack:** Go, testify, WuKongIM delivery runtime, metadb/metafsm tests

---

### Task 1: Lock Behavior With Failing Tests

**Files:**
- Modify: `internal/runtime/delivery/actor_test.go`
- Modify: `internal/runtime/delivery/retrywheel_test.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `pkg/storage/metafsm/state_machine_test.go`

- [ ] **Step 1: Write failing tests for unresolved message resume and resolver retry**
- [ ] **Step 2: Run targeted delivery runtime tests to verify they fail for the expected reasons**
- [ ] **Step 3: Write failing tests for person-channel CRC32 collision tie-break and raw recipient send compatibility**
- [ ] **Step 4: Write failing test proving `ApplyBatch(DeleteChannel)` clears subscribers**
- [ ] **Step 5: Run the targeted suites again and confirm red**

### Task 2: Fix Delivery Actor Progress And Retry Semantics

**Files:**
- Modify: `internal/runtime/delivery/actor.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/runtime/delivery/retrywheel.go`
- Modify: `internal/runtime/delivery/shard.go`

- [ ] **Step 1: Add minimal state/event support for resolver retry and unresolved inflight resume**
- [ ] **Step 2: Resume unresolved messages whenever route budget is released, regardless of which message freed it**
- [ ] **Step 3: Retry resolver failures in memory instead of marking realtime delivery done**
- [ ] **Step 4: Run delivery runtime tests and confirm green**

### Task 3: Fix Person Channel Determinism And Regression Coverage

**Files:**
- Modify: `internal/runtime/channelid/person.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add deterministic tie-break for equal CRC32 values without changing non-collision ordering**
- [ ] **Step 2: Keep raw recipient normalization behavior covered at usecase level**
- [ ] **Step 3: Run focused person-channel tests and confirm green**

### Task 4: Verify End To End

**Files:**
- No code changes expected

- [ ] **Step 1: Run focused suites covering delivery runtime, message send, and metafsm**
- [ ] **Step 2: Run `go test ./... -count=1`**
- [ ] **Step 3: Summarize remaining risks, if any**
