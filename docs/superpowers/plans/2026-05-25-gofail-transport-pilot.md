# Gofail Transport Pilot Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a minimal opt-in gofail pilot for node transport fault injection.

**Architecture:** Keep normal builds untouched by using gofail comment markers in `pkg/transport` and a temporary-copy build script. Extend e2e process launching with per-node env vars so failpoint-enabled binaries can be controlled independently. Tests stay focused and fast.

**Tech Stack:** Go 1.23, `go.etcd.io/gofail@v0.2.0`, shell script, existing e2e harness.

---

### Task 1: E2E Node Env Support

**Files:**
- Modify: `test/e2e/suite/config.go`
- Modify: `test/e2e/suite/process.go`
- Modify: `test/e2e/suite/runtime.go`
- Test: `test/e2e/suite/process_test.go`
- Test: `test/e2e/suite/runtime_test.go`

- [ ] Write failing tests for env merging and per-node env overrides.
- [ ] Run focused e2e suite tests and verify failure.
- [ ] Add `Env []string` to `NodeSpec`, `WithNodeEnv`, and `NodeProcess.Start` env merging.
- [ ] Run focused e2e suite tests and verify pass.

### Task 2: Transport Failpoint Markers

**Files:**
- Modify: `pkg/transport/pool.go`
- Test: `pkg/transport/gofail_markers_test.go`

- [ ] Write failing marker tests for expected gofail comments.
- [ ] Run focused transport marker test and verify failure.
- [ ] Add `wkTransportSendFault` and `wkTransportRPCFault` comments.
- [ ] Run transport tests and verify pass.

### Task 3: Failpoint Binary Build Script

**Files:**
- Create: `scripts/build-gofail-binary.sh`
- Test: `scripts/gofail_build_script_test.go`

- [ ] Write failing script tests for defaults and command behavior.
- [ ] Run focused script tests and verify failure.
- [ ] Implement script using a temporary source copy, `go install go.etcd.io/gofail@v0.2.0`, `gofail enable`, and `GOWORK=off go build`.
- [ ] Run script tests and a smoke build if feasible.

### Task 4: Opt-In Gofail E2E Smoke

**Files:**
- Create: `test/e2e/cluster/gofail_transport/AGENTS.md`
- Create: `test/e2e/cluster/gofail_transport/gofail_transport_test.go`
- Modify: `test/e2e/AGENTS.md`
- Modify: `test/e2e/cluster/AGENTS.md`

- [ ] Add an opt-in e2e smoke guarded by `WK_E2E_GOFAIL_TRANSPORT_SMOKE=1`.
- [ ] Start a single-node cluster with `GOFAIL_HTTP` injected through `WithNodeEnv`.
- [ ] Verify `wkTransportSendFault` and `wkTransportRPCFault` are listed by the gofail HTTP endpoint.
- [ ] Update e2e catalogs.

### Task 5: Verification

**Files:**
- All modified files

- [ ] Run `GOWORK=off go test ./pkg/transport ./scripts -count=1`.
- [ ] Run `GOWORK=off go test -tags=e2e ./test/e2e/suite ./test/e2e/cluster/gofail_transport -count=1`.
- [ ] Run `scripts/build-gofail-binary.sh --out /tmp/wukongim-gofail`.
- [ ] Run `WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_TRANSPORT_SMOKE=1 GOWORK=off go test -tags=e2e ./test/e2e/cluster/gofail_transport -count=1`.
