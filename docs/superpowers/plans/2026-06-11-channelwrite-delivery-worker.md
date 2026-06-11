# Channelwrite Delivery Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move online message delivery out of `internalv2/runtime/channelwrite` post-commit execution into an independent bounded worker channel while keeping channelwrite responsible for subscriber expansion and recent-conversation active admission.

**Architecture:** `channelwrite` keeps the post-commit subscriber scan, active admission, UID authority grouping, and batch slicing. Instead of running presence resolution and owner push synchronously, it enqueues immutable `RecipientBatch` commands to a dedicated delivery worker. The app wires that worker to the existing `RecipientProcessor`, so online delivery remains delivery-only and SENDACK remains dependent only on durable append.

**Tech Stack:** Go, `internalv2/runtime/channelwrite`, `internalv2/app`, existing `RecipientProcessor`, bounded goroutine worker queue, package unit tests.

---

### Task 1: Channelwrite Enqueue Contract

**Files:**
- Modify: `internalv2/runtime/channelwrite/options.go`
- Modify: `internalv2/runtime/channelwrite/recipient.go`
- Test: `internalv2/runtime/channelwrite/recipient_test.go`
- Update: `internalv2/runtime/channelwrite/FLOW.md`

- [x] **Step 1: Write failing recipient enqueue tests**

Add tests that show `dispatchRecipientSet` resolves and groups recipients by exact `RecipientAuthorityTarget`, then calls a delivery enqueue port without running delivery inline. Include one test that mutates the original event payload after dispatch and verifies the queued batch owns an independent copy.

- [x] **Step 2: Run tests to verify red**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipientDelivery.*' -count=1
```

Expected: FAIL because no delivery enqueue port exists.

- [x] **Step 3: Introduce `RecipientDeliveryEnqueuer`**

Add a small interface near `RecipientAuthorityRouter`:

```go
// RecipientDeliveryEnqueuer accepts recipient-authority batches for asynchronous online delivery.
type RecipientDeliveryEnqueuer interface {
    // EnqueueRecipientBatch admits one recipient-authority delivery batch.
    EnqueueRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error
}
```

Keep `RecipientAuthorityRouter` only if needed for compatibility during the task; the final wiring should use the new port for post-commit delivery.

- [x] **Step 4: Route dispatch through enqueue**

Change `commitPorts` and `dispatchRecipientTarget` so channelwrite slices `RecipientBatch` as before but calls `EnqueueRecipientBatch`. Preserve:

- active admission before delivery enqueue
- unique UID route resolution with duplicate recipients preserved
- target validation failure detail
- `recipient_dispatch` phase for enqueue errors
- target concurrency semantics

- [x] **Step 5: Run tests to verify green**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipient|TestScoped|TestActive|TestPerson|TestGroupChannel' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/runtime/channelwrite docs/superpowers/plans/2026-06-11-channelwrite-delivery-worker.md
git commit -m "channelwrite: enqueue recipient delivery batches"
```

### Task 2: Independent Recipient Delivery Worker

**Files:**
- Modify: `internalv2/runtime/channelwrite/delivery.go`
- Test: `internalv2/runtime/channelwrite/delivery_worker_test.go`
- Update: `internalv2/runtime/channelwrite/FLOW.md`

- [x] **Step 1: Write failing worker lifecycle tests**

Add tests for a worker that:

- returns `ErrManagerClosed`-style closed admission before `Start` and after `Stop`
- admits batches into a bounded queue after `Start`
- processes accepted batches with `RecipientProcessor`
- blocks or returns context error when the queue is full
- drains accepted work on `Stop(ctx)` until the context expires
- records failures through an observer-compatible hook without returning them to channelwrite after admission

- [x] **Step 2: Run tests to verify red**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipientDeliveryWorker' -count=1
```

Expected: FAIL because the worker does not exist.

- [x] **Step 3: Implement the bounded worker**

Add `RecipientDeliveryWorker` with options:

```go
type RecipientDeliveryWorkerOptions struct {
    Processor *RecipientProcessor
    QueueSize int
    Workers int
    Observer AppendObserver
}
```

The worker should implement:

```go
Start(context.Context) error
Stop(context.Context) error
EnqueueRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error
```

Use internal queue commands that clone `RecipientBatch`. Worker goroutines call `processor.ProcessRecipientBatch`; errors are observed as post-commit failures using the existing detail extraction. Admission errors should use the same sentinel and low-cardinality error classes as existing channelwrite pressure where practical.

- [x] **Step 4: Run tests to verify green**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite -run 'TestRecipientDeliveryWorker|TestRecipientProcessor' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internalv2/runtime/channelwrite
git commit -m "channelwrite: add recipient delivery worker"
```

### Task 3: App Wiring And Lifecycle

**Files:**
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/channel_write.go`
- Modify: `internalv2/app/delivery.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`
- Update: `internalv2/app/FLOW.md`

- [ ] **Step 1: Write failing wiring tests**

Update `TestNewWiresDeliveryWhenEnabled` to require the delivery worker group to include retry scheduler, delivery manager, and the new recipient delivery worker. Add a focused test that channelwrite uses an enqueueing router when delivery is enabled and still admits active batches when delivery is disabled.

- [ ] **Step 2: Run tests to verify red**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestNewWiresDeliveryWhenEnabled|TestNewWiresChannelWriteCommitEffectsWhenDeliveryDisabled|TestDeliveryEnabled' -count=1
```

Expected: FAIL because app wiring still calls `RecipientProcessor` directly from channelwrite.

- [ ] **Step 3: Wire worker in the app**

Create the `RecipientProcessor` in `wireChannelWrite`, wrap it in `channelwrite.NewRecipientDeliveryWorker`, put that worker in `a.deliveryWorker` lifecycle group when delivery is enabled, and pass the worker as the channelwrite delivery enqueuer. Remove the direct inline `channelWriteRecipientRouter` processor path.

- [ ] **Step 4: Preserve disabled-delivery behavior**

When delivery is disabled, keep `ConversationActiveAdmitter` and subscriber expansion wired, but do not enqueue online delivery. The channelwrite post-commit path should return after active admission.

- [ ] **Step 5: Run app tests to verify green**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestNewWiresDeliveryWhenEnabled|TestNewWiresChannelWriteCommitEffectsWhenDeliveryDisabled|TestDeliveryEnabled' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/app internalv2/runtime/channelwrite/FLOW.md internalv2/app/FLOW.md
git commit -m "app: run recipient delivery through worker"
```

### Task 4: Cleanup, Docs, And Verification

**Files:**
- Modify as needed: `internalv2/runtime/channelwrite/FLOW.md`
- Modify as needed: `internalv2/app/FLOW.md`
- Modify as needed: `internalv2/app/config.go`
- Modify as needed: `cmd/wukongimv2/config.go`
- Modify as needed: `wukongim.conf.example`

- [ ] **Step 1: Decide config reuse vs new fields**

Prefer reusing `Delivery.EventQueueSize` and `ChannelWriteRecipientDispatchConcurrency` for the new worker unless tests show a need for separate worker count. Add new config only if the existing knobs cannot express the desired pressure boundary.

- [ ] **Step 2: Remove stale inline delivery wording**

Update FLOW files so they say channelwrite expands subscribers and enqueues delivery batches, while the recipient delivery worker owns presence resolution and owner push.

- [ ] **Step 3: Run targeted verification**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelwrite ./internalv2/app ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/usecase/delivery ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 4: Run diff checks**

Run:

```bash
git diff --check HEAD
rg -n "channelWriteRecipientRouter|RecipientRouter|ProcessRecipientBatch\\(ctx, batch\\)|post-commit effects resolve online routes and push" internalv2/runtime/channelwrite internalv2/app
```

Expected: no whitespace errors; only intentional compatibility references remain.

- [ ] **Step 5: Final review and commit**

Request a final code review against the feature branch, fix Critical/Important issues, rerun verification, then commit any remaining docs or cleanup.
