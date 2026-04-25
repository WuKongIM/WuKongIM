# Remove Progress Ack Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the legacy `progress_ack` replication path, make channel steady-state replication always use `long_poll`, and silently ignore the old `WK_CLUSTER_REPLICATION_MODE` input.

**Architecture:** Delete replication-mode selection from config and runtime assembly, then remove the legacy progress-ack transport/runtime branches so only long-poll steady-state replication remains. Keep recovery semantics through `ReconcileProbe`, update docs, and verify with focused config/runtime/transport suites.

**Tech Stack:** Go, `testing`, `testify`, `cmd/wukongim`, `internal/app`, `pkg/channel/runtime`, `pkg/channel/transport`, `pkg/channel/replica`, Markdown flow/config docs.

---

### Task 1: Lock the config behavior with tests

**Files:**
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/config_test.go`

- [ ] **Step 1: Write the failing config tests**

Add tests that assert:
- default config no longer stores a replication mode,
- `WK_CLUSTER_REPLICATION_MODE=progress_ack` is ignored,
- long-poll knobs still apply without a mode key.

- [ ] **Step 2: Run the focused config tests to verify they fail**

Run: `go test ./cmd/wukongim ./internal/app -run 'Test.*Replication|TestBuildAppConfig|TestApplyDefaultsAndValidate'`
Expected: FAIL because the config model still exposes replication mode behavior.

- [ ] **Step 3: Implement the minimal config changes**

Remove parsing/storage/validation for `ReplicationMode` while keeping long-poll knob handling and silent ignore semantics for the legacy key.

- [ ] **Step 4: Re-run the focused config tests to verify they pass**

Run: `go test ./cmd/wukongim ./internal/app -run 'Test.*Replication|TestBuildAppConfig|TestApplyDefaultsAndValidate'`
Expected: PASS.

### Task 2: Remove legacy transport protocol tests and replace them with single-mode expectations

**Files:**
- Modify: `pkg/channel/transport/codec_test.go`
- Modify: `pkg/channel/transport/adapter_test.go`
- Modify: `pkg/channel/transport/progress_ack_rpc_test.go`
- Modify: `pkg/channel/transport/longpoll_session_test.go`

- [ ] **Step 1: Write/adjust transport tests for the post-removal API**

Add or adjust tests so they assert:
- long-poll RPC handling still works,
- removed progress-ack/fetch-batch entry points are no longer present in production APIs,
- transport no longer branches on replication mode.

- [ ] **Step 2: Run the focused transport tests to verify they fail**

Run: `go test ./pkg/channel/transport -run 'Test.*LongPoll|Test.*ProgressAck|Test.*FetchBatch'`
Expected: FAIL because the code still exposes legacy progress-ack/fetch-batch behavior.

- [ ] **Step 3: Implement the minimal transport cleanup**

Remove progress-ack and legacy fetch-batch code from transport, keep long-poll and reconcile probe paths, and simplify options/state accordingly.

- [ ] **Step 4: Re-run the focused transport tests to verify they pass**

Run: `go test ./pkg/channel/transport -run 'Test.*LongPoll|Test.*ProgressAck|Test.*FetchBatch'`
Expected: PASS with only the retained long-poll assertions still relevant.

### Task 3: Remove legacy runtime/session handling

**Files:**
- Modify: `pkg/channel/runtime/types.go`
- Modify: `pkg/channel/runtime/backpressure.go`
- Modify: `pkg/channel/runtime/replicator.go`
- Modify: `pkg/channel/runtime/runtime.go`
- Modify: `pkg/channel/runtime/session_test.go`
- Modify: `pkg/channel/runtime/longpoll_test.go`
- Modify: `pkg/channel/runtime/testenv_test.go`

- [ ] **Step 1: Write/adjust failing runtime tests**

Update runtime tests so they assert:
- fetch responses feed long-poll cursor delta behavior instead of progress-ack envelopes,
- runtime no longer exposes replication-mode branches,
- empty steady-state replication no longer uses the legacy progress-ack path.

- [ ] **Step 2: Run the focused runtime tests to verify they fail**

Run: `go test ./pkg/channel/runtime -run 'TestSession|TestLongPoll'`
Expected: FAIL because runtime/session code still references progress-ack and replication-mode branching.

- [ ] **Step 3: Implement the minimal runtime cleanup**

Delete progress-ack message kinds and handlers, remove replication-mode checks, and keep the long-poll-only steady-state path plus reconcile probe recovery.

- [ ] **Step 4: Re-run the focused runtime tests to verify they pass**

Run: `go test ./pkg/channel/runtime -run 'TestSession|TestLongPoll'`
Expected: PASS.

### Task 4: Remove now-dead app wiring and replica-facing assumptions

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/build_test.go`
- Modify: `internal/app/send_stress_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `pkg/channel/channel.go`
- Modify: `pkg/channel/replica/api_test.go`
- Modify: `pkg/channel/replica/progress_async_test.go`
- Modify: `pkg/channel/replica/fetch_test.go`
- Modify: `pkg/channel/replica/recovery_test.go`

- [ ] **Step 1: Write/adjust failing app and replica tests**

Update tests so they no longer set or inspect `ReplicationMode`, and so replica-facing expectations match long-poll-only steady-state behavior.

- [ ] **Step 2: Run the focused app/replica tests to verify they fail**

Run: `go test ./internal/app ./pkg/channel/replica -run 'Test.*Replication|Test.*ProgressAck|Test.*Build|Test.*Multinode'`
Expected: FAIL because app wiring and tests still refer to replication mode / progress ack.

- [ ] **Step 3: Implement the minimal wiring cleanup**

Remove replication-mode plumbing from app/channel assembly and update replica-facing tests/helpers that only existed for the deleted path.

- [ ] **Step 4: Re-run the focused app/replica tests to verify they pass**

Run: `go test ./internal/app ./pkg/channel/replica -run 'Test.*Replication|Test.*ProgressAck|Test.*Build|Test.*Multinode'`
Expected: PASS.

### Task 5: Update docs and verify the end state

**Files:**
- Modify: `pkg/channel/FLOW.md`
- Modify: `wukongim.conf.example`
- Modify: `docs/superpowers/specs/2026-04-21-remove-progress-ack-design.md`

- [ ] **Step 1: Update docs to match the final implementation**

Remove `progress_ack` and replication-mode selection language, document long-poll as the only steady-state replication path, and note that the legacy config key is ignored.

- [ ] **Step 2: Run the focused verification suite**

Run: `go test ./cmd/wukongim ./internal/app ./pkg/channel/runtime ./pkg/channel/transport ./pkg/channel/replica`
Expected: PASS.

- [ ] **Step 3: Run a repository search to confirm legacy references are gone from production code**

Run: `rg -n 'progress_ack|WK_CLUSTER_REPLICATION_MODE|ReplicationMode' cmd internal pkg`
Expected: only intentional doc/test compatibility references remain, or no matches in production code.
