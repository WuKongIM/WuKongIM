# Channelwrite Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add low-cardinality metrics for conversation active cache/flush and recipient delivery worker queue/process behavior.

**Architecture:** Runtime packages emit observer events. `internalv2/app` maps those events to `pkg/metrics`. Prometheus metrics stay low-cardinality and do not include UID, channel, slot, or node labels beyond existing node const labels.

**Tech Stack:** Go, `internalv2/runtime/conversationactive`, `internalv2/runtime/channelwrite`, `internalv2/app`, `pkg/metrics`, unit tests.

---

### Task 1: Conversation Active Runtime Observations

**Files:**
- Modify: `internalv2/runtime/conversationactive/types.go`
- Modify: `internalv2/runtime/conversationactive/manager.go`
- Test: `internalv2/runtime/conversationactive/manager_test.go`

- [ ] **Step 1: Write failing tests for cache and flush events**

Add tests showing `MarkActive` reports cache rows/dirty rows and `Flush` reports selected/flushed rows, result, duration, and oldest dirty age.

- [ ] **Step 2: Run red test**

```bash
GOWORK=off go test ./internalv2/runtime/conversationactive -run 'Test.*Observe' -count=1
```

Expected: FAIL because observer APIs do not exist.

- [ ] **Step 3: Implement observer events**

Add `Observer`, `CacheObservation`, and `FlushObservation` types. Extend
`Options` with `Observer`. Emit cache observations after cache mutations and
flush observations around store writes.

- [ ] **Step 4: Run green test**

```bash
GOWORK=off go test ./internalv2/runtime/conversationactive -count=1
```

Expected: PASS.

### Task 2: Conversation Metrics Mapping

**Files:**
- Modify: `pkg/metrics/conversation.go`
- Test: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/observability.go`
- Test: `internalv2/app/observability_test.go`
- Modify: `internalv2/app/conversation_authority.go`

- [ ] **Step 1: Write failing metrics tests**

Add registry and app observer tests for active cache rows, dirty rows, oldest
dirty age, flush totals, flush rows, and flush duration.

- [ ] **Step 2: Run red tests**

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/app -run 'Test.*Conversation.*Active|Test.*Conversation.*Flush' -count=1
```

Expected: FAIL because metrics methods and mapping do not exist.

- [ ] **Step 3: Implement mapping**

Add metrics fields/methods and make the app conversation authority observer
implement `conversationactive.Observer`.

- [ ] **Step 4: Run green tests**

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/app -count=1
```

Expected: PASS.

### Task 3: Recipient Delivery Worker Runtime Observations

**Files:**
- Modify: `internalv2/runtime/channelwrite/options.go`
- Modify: `internalv2/runtime/channelwrite/observer.go`
- Modify: `internalv2/runtime/channelwrite/delivery_worker.go`
- Test: `internalv2/runtime/channelwrite/delivery_worker_test.go`

- [ ] **Step 1: Write failing worker observation tests**

Add tests showing queue gauges, admission results/wait time, process duration,
batch recipient count, and panic/error result observations.

- [ ] **Step 2: Run red test**

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipientDeliveryWorker.*Observ' -count=1
```

Expected: FAIL because worker observation APIs do not exist.

- [ ] **Step 3: Implement worker observation**

Add observation types and observer interfaces under channelwrite. Emit queue
pressure on start/enqueue/dequeue/stop, admission observations for every
enqueue outcome, and process observations after every worker command.

- [ ] **Step 4: Run green test**

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipientDeliveryWorker' -count=1
```

Expected: PASS.

### Task 4: Recipient Delivery Metrics Mapping

**Files:**
- Modify: `pkg/metrics/delivery.go`
- Test: `pkg/metrics/registry_test.go`
- Modify: `internalv2/app/delivery.go`
- Test: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing metrics tests**

Add registry and app observer tests for recipient delivery queue gauges,
admission totals/wait duration, process totals/duration, and batch recipient
histograms.

- [ ] **Step 2: Run red tests**

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/app -run 'Test.*RecipientDelivery' -count=1
```

Expected: FAIL because metrics mapping does not exist.

- [ ] **Step 3: Implement metrics mapping**

Add delivery metric fields/methods and map channelwrite worker observations in
`deliveryMessageObserver`.

- [ ] **Step 4: Run green tests**

```bash
GOWORK=off go test ./pkg/metrics ./internalv2/app -count=1
```

Expected: PASS.

### Task 5: Docs And Verification

**Files:**
- Modify: `internalv2/runtime/channelwrite/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `docs/superpowers/plans/2026-06-11-channelwrite-observability.md`

- [ ] **Step 1: Update FLOW docs**

Document active cache/flush and recipient delivery worker pressure metrics.

- [ ] **Step 2: Run final verification**

```bash
GOWORK=off go test ./internalv2/runtime/conversationactive ./internalv2/runtime/channelwrite ./internalv2/app ./pkg/metrics -count=1
git diff --check HEAD
```

Expected: PASS and no whitespace errors.
