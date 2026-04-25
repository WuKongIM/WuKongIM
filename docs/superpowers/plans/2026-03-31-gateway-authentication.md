# Gateway Authentication Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add gateway-layer wkproto authentication so failed or banned connections receive `CONNACK` and are closed before any business handler sees unauthenticated traffic.

**Architecture:** Keep authentication inside `internal/gateway/core` so session lifecycle and frame gating stay centralized. Introduce a small authentication interface on gateway options, track authenticated state per session, and have the server intercept `CONNECT` plus reject any non-`CONNECT` frame before authentication.

**Tech Stack:** Go, `internal/gateway`, `pkg/wkpacket`, existing core server tests and fake transport/protocol testkit.

---

### Task 1: Define the Authentication Surface

**Files:**
- Modify: `internal/gateway/types/options.go`
- Modify: `internal/gateway/options.go`
- Test: `internal/gateway/options_test.go`

- [ ] **Step 1: Write the failing test**

Add an option-validation test that accepts a gateway with an authenticator and preserves existing validation behavior.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway -run TestOptions -v`
Expected: FAIL because authentication options/types do not exist yet.

- [ ] **Step 3: Write minimal implementation**

Add the authenticator/result types and thread them through `gateway.Options`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway -run TestOptions -v`
Expected: PASS

### Task 2: Gate wkproto Sessions in Core Server

**Files:**
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Test: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write the failing tests**

Add focused tests for:
- unauthenticated non-`CONNECT` frames are closed and never reach the handler
- failed authentication writes `CONNACK` with the returned reason code, then closes
- successful authentication allows later frames through

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/core -run 'TestServer(Auth|RejectsUnauthenticated)' -v`
Expected: FAIL because server does not track authentication or intercept `CONNECT`.

- [ ] **Step 3: Write minimal implementation**

Track per-session authentication state, intercept wkproto `CONNECT`, call the authenticator, write `CONNACK`, and close on failure. Reject any non-`CONNECT` frame while unauthenticated.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/core -run 'TestServer(Auth|RejectsUnauthenticated)' -v`
Expected: PASS

### Task 3: Add a Default Authenticator from v2 Rules

**Files:**
- Create: `internal/gateway/auth.go`
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/gateway_test.go`
- Test: `internal/gateway/gateway_test.go`

- [ ] **Step 1: Write the failing test**

Add gateway-level tests covering:
- token auth failure replies with `CONNACK(ReasonAuthFail)` and closes
- banned uid replies with `CONNACK(ReasonBan)` and closes

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway -run 'TestGateway(Auth|Ban)' -v`
Expected: FAIL because no default gateway authenticator exists.

- [ ] **Step 3: Write minimal implementation**

Create the default authenticator with the scoped v2 logic: token verification and ban lookup for wkproto connections only.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway -run 'TestGateway(Auth|Ban)' -v`
Expected: PASS

### Task 4: Final Verification

**Files:**
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/gateway_test.go`

- [ ] **Step 1: Run focused package verification**

Run: `go test ./internal/gateway/... -v`
Expected: PASS

- [ ] **Step 2: Run broader regression coverage if gateway auth touches shared packet behavior**

Run: `go test ./pkg/wkproto ./pkg/jsonrpc -v`
Expected: PASS
