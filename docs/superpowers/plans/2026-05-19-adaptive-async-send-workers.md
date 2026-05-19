# Adaptive Async Send Workers Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current GOMAXPROCS-only async SEND worker default and remove the send-stress fixed worker preset while preserving the ~2000 QPS acceptance target.

**Architecture:** Keep `AsyncSendDispatchWorkers` as an explicit operator override. When it is unset/non-positive, compute an IO-wait-aware default as `clamp(GOMAXPROCS * 64, 64, 1024)`, so small servers get a bounded minimum, larger servers scale up, and the value remains capped. Acceptance send stress should rely on the adaptive default, not a hardcoded preset.

**Tech Stack:** Go, gateway core session options, cmd config loader, integration send-stress tests.

---

### Task 1: Adaptive worker default in gateway core

**Files:**
- Modify: `internal/gateway/core/server.go`
- Test: `internal/gateway/core/async_dispatch_test.go`
- Modify docs/comments: `internal/gateway/types/options.go`

- [ ] Add a failing unit test that `asyncDispatchWorkerCount` returns explicit values unchanged.
- [ ] Add failing unit tests for adaptive defaults: low GOMAXPROCS clamps to 64, normal values scale by 64, high values cap at 1024.
- [ ] Implement helper constants and `adaptiveAsyncDispatchWorkerCount(gomaxprocs int)`.
- [ ] Update `asyncDispatchWorkerCount` to use the adaptive helper when `AsyncSendDispatchWorkers <= 0`.
- [ ] Update the `AsyncSendDispatchWorkers` comment to describe the adaptive default.
- [ ] Run `GOWORK=off go test ./internal/gateway/core -run 'TestServerAsyncSendDispatch|TestRecordAsyncDispatchWait|TestAsyncDispatchWorkerCount' -count=1`.

### Task 2: Remove fixed send-stress worker preset

**Files:**
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/comm_integration_test.go`
- Modify: `wukongim.conf.example`

- [ ] Add/update assertions so send-stress acceptance does not require `GatewayAsyncSendDispatchWorkers` to be a fixed non-zero value.
- [ ] Remove `GatewayAsyncSendDispatchWorkers` from `sendStressAcceptanceSpec` and `applySendPathTuning`.
- [ ] Update config example text to say the default is adaptive, not GOMAXPROCS.
- [ ] Run `GOWORK=off go test ./internal/app -run 'TestSendStressConfigDefaultsAndOverrides|TestSelectSendStressThreeNodeRun|TestApplySendStressCommitCoordinatorEnvTuningOverridesConfig' -count=1`.

### Task 3: Verification and commit

**Files:**
- No further production changes unless tests fail.

- [ ] Run focused package tests: `GOWORK=off go test ./internal/gateway/... ./cmd/wukongim ./internal/app ./pkg/observability/... -count=1`.
- [ ] Run acceptance send stress with adaptive default: `GOWORK=off WK_SEND_STRESS=1 WK_SEND_TRACE=1 go test -tags=integration ./internal/app -run '^TestSendStressThreeNode$' -count=1 -timeout=240s -v`.
- [ ] Confirm QPS is near/above 2000 and `gateway.async_dispatch_wait` remains visible for future tuning.
- [ ] Commit the changes.
