# Gateway Read Idle Timeout Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make gateway sessions close only after 3 minutes without any client-to-server traffic, while treating every inbound client frame including ping as activity.

**Architecture:** Keep the existing gateway idle monitor, but change its tracked timestamp from generic connection activity to client read activity only. Preserve write timeout behavior and outbound delivery semantics, and update the documented/default session idle timeout to 3 minutes.

**Tech Stack:** Go, gateway core session runtime, Go testing package, repository config/docs

---

### Task 1: Lock in the new timeout defaults with tests

**Files:**
- Modify: `internal/gateway/options_test.go`
- Modify: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write the failing default-value test**

Add an assertion in `TestDefaultSessionOptions` that `IdleTimeout == 3*time.Minute`.

- [ ] **Step 2: Run the focused options test to verify it fails**

Run: `go test ./internal/gateway -run TestDefaultSessionOptions`
Expected: FAIL because the default is still `30s`.

- [ ] **Step 3: Write the failing read-idle behavior test**

Add a server test that continuously triggers outbound writes without client inbound traffic and asserts the session still closes with `idle_timeout`. Add another test that injects client inbound traffic before the deadline and asserts the session stays open until the refreshed deadline expires.

- [ ] **Step 4: Run the focused server tests to verify they fail for the right reason**

Run: `go test ./internal/gateway/core -run 'TestIdleTimeout|TestOutboundTrafficDoesNotRefreshIdleTimeout|TestInboundTrafficRefreshesIdleTimeout'`
Expected: FAIL because outbound writes currently refresh the tracked activity timestamp and because the timeout default is still `30s`.

### Task 2: Implement read-only idle activity tracking

**Files:**
- Modify: `internal/gateway/types/options.go`
- Modify: `internal/gateway/core/server.go`

- [ ] **Step 1: Change the default idle timeout to 3 minutes**

Update `DefaultSessionOptions()` so `IdleTimeout` defaults to `3 * time.Minute`.

- [ ] **Step 2: Split idle tracking to read activity only**

Replace the generic `lastActivity` timestamp with a read-activity timestamp and update the idle monitor to consult only client inbound activity. Keep the refresh on connection open and on every call to `onData`, but remove outbound write refreshes from `startWriter`.

- [ ] **Step 3: Keep names/comments aligned with behavior**

Rename helper methods so the code clearly says "read activity" rather than generic activity.

- [ ] **Step 4: Run the focused tests to verify they pass**

Run: `go test ./internal/gateway ./internal/gateway/core -run 'TestDefaultSessionOptions|TestIdleTimeout|TestOutboundTrafficDoesNotRefreshIdleTimeout|TestInboundTrafficRefreshesIdleTimeout'`
Expected: PASS.

### Task 3: Sync docs and config examples

**Files:**
- Modify: `internal/FLOW.md`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Update flow documentation**

Adjust the gateway flow description so idle close is described as client-read inactivity rather than any connection activity.

- [ ] **Step 2: Align the example config**

Add `WK_GATEWAY_DEFAULT_SESSION_IDLE_TIMEOUT=3m` to `wukongim.conf.example` so the shipped example matches the new default and makes the behavior obvious.

- [ ] **Step 3: Run the targeted regression tests again**

Run: `go test ./internal/gateway ./internal/gateway/core`
Expected: PASS.
