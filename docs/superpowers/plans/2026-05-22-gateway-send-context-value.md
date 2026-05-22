# Gateway Send Context Value Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use @superpowers:test-driven-development and @superpowers:verification-before-completion while executing this plan. Keep the changes narrow, benchmark-driven, and safe to revert.

**Goal:** Remove the hot-path heap allocation caused by gateway handler context boxing by passing frame contexts by value and storing SEND batch contexts by value.

**Architecture:** Change `Handler` callbacks to receive `gatewaytypes.Context` values, while keeping `Authenticator` and `SessionActivator` pointer-based because they are not on the per-frame hot path and existing tests use nil context inputs. Change the async SEND batch pipeline so `internal/gateway/core` stores `gatewaytypes.Context` by value in batch items and builds addressable context values only where the access layer needs a pointer for helper reuse. This reduces heap pressure in the hot send path without changing send semantics or session state ownership. After this round, the next allocation target should be SEND payload ownership and `cloneAsyncSendFrame`.

**Tech Stack:** Go, `go test`, `-benchmem`, existing gateway core/access tests, benchmark profiles.

---

### Task 1: Make gateway handler contexts value-based in core

**Files:**
- Modify: `internal/gateway/types/event.go`
- Modify: `internal/gateway/core/dispatcher.go`
- Modify: `internal/gateway/core/server.go`
- Test: `internal/gateway/core/async_dispatch_test.go`
- Test: `internal/gateway/core/dispatcher_test.go`
- Test: `internal/gateway/core/server_test.go`
- Test: `internal/gateway/core/server_benchmark_test.go`

- [ ] **Step 1: Write the failing test**

Add a focused test that exercises async SEND batching and asserts the batch item context is copied by value, not stored as a pointer. Keep the existing dispatch benchmarks as the performance signal for `OnFrame` fallback context boxing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/gateway/core -run 'Test.*SendBatch.*Context' -count=1`

Expected: compile or assertion failure because `SendBatchItem.Context` is still a pointer.

- [ ] **Step 3: Write the minimal implementation**

Change `gatewaytypes.Handler` callbacks and `gatewaytypes.SendBatchItem.Context` from pointer context to value context. Update `dispatcher.context` to return `gatewaytypes.Context` by value and pass it directly to handler callbacks. Keep `Authenticator` and `SessionActivator` pointer-based.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/gateway/core -run 'Test.*SendBatch.*Context|TestServerAsyncSendDispatch.*' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/types/event.go internal/gateway/core/dispatcher.go internal/gateway/core/server.go internal/gateway/core/async_dispatch_test.go internal/gateway/core/dispatcher_test.go internal/gateway/core/server_test.go internal/gateway/core/server_benchmark_test.go
git commit -m "perf(gateway): pass handler context by value"
```

### Task 2: Update gateway handler implementers and access batch path

**Files:**
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/gateway/logging_test.go`
- Modify: `internal/access/gateway/router_test.go`
- Modify: `internal/gateway/testkit/fake_handler.go`
- Modify: gateway handler test fakes under `internal/gateway`, `internal/app`

- [ ] **Step 1: Write the failing test**

Add or adjust tests so they still verify:
- frame handler dispatch still sees the same context fields
- valid and invalid batch items are handled independently
- reply tokens are preserved
- sendack writes still use the correct per-item context

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOWORK=off go test ./internal/access/gateway -run 'TestHandlerOnSendBatch' -count=1`

Expected: compile failure or assertion failure until the batch path is updated for value-based contexts.

- [ ] **Step 3: Write the minimal implementation**

Update handler implementations to receive context values. In the access gateway, keep `coregateway.Context` values in a slice for batch processing, take addresses only from addressable slice elements or local variables, and remove any helper behavior that exists only to heap-box a batch context pointer.

- [ ] **Step 4: Run the test to verify it passes**

Run: `GOWORK=off go test ./internal/access/gateway -run 'TestHandlerOnSendBatch' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/access/gateway/frame_router.go internal/access/gateway/handler.go internal/access/gateway/handler_test.go internal/access/gateway/logging_test.go internal/access/gateway/router_test.go internal/gateway/testkit/fake_handler.go internal/gateway/gateway_test.go internal/gateway/options_test.go internal/app/node_drain_state_test.go
git commit -m "refactor(gateway): update handler context callers"
```

### Task 3: Verify benchmark impact and keep a clean baseline

**Files:**
- Modify: `internal/gateway/core/server_benchmark_test.go` only if a small benchmark assertion or helper is needed
- Test: `internal/gateway/core/server_benchmark_test.go`

- [ ] **Step 1: Run the benchmark before and after the change**

Run:
```bash
GOWORK=off go test ./internal/gateway/core -bench BenchmarkServerSendDispatch -benchmem -run '^$' -count=1
```

Expected: lower `B/op` and fewer allocs/op than the current `416 B/op, 3 allocs/op` baseline.

- [ ] **Step 2: Run the focused regression suite**

Run:
```bash
GOWORK=off go test ./internal/gateway/core ./internal/access/gateway -count=1
```

Expected: PASS.

- [ ] **Step 3: Decide whether the next optimization should target payload ownership**

If `cloneAsyncSendFrame` remains the dominant hotspot after this pass, capture that in the benchmark notes and queue the next refactor separately instead of expanding this one.

- [ ] **Step 4: Commit the verification notes if benchmark results are recorded in-repo**

Only update docs or benchmark notes if the repo already keeps such records for this path.
