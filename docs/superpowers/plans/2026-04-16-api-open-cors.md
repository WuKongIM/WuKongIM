# API Open CORS Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every HTTP API endpoint in `internal/access/api` respond with open CORS headers and handle browser preflight requests successfully.

**Architecture:** Add one global CORS middleware at API server construction time so every current and future route inherits the same headers. Pair it with catch-all `OPTIONS` handling in the API router so browser preflight requests succeed even when only `POST` or `GET` business routes are registered.

**Tech Stack:** Go, Gin, existing `internal/access/api` tests, app build wiring.

---

### Task 1: Add failing CORS behavior tests

**Files:**
- Modify: `internal/access/api/server_test.go`
- Test: `internal/access/api/server_test.go`

- [ ] **Step 1: Write the failing test**
Add one test proving a normal API response includes `Access-Control-Allow-Origin: *`, and one test proving `OPTIONS /user/token` returns a successful preflight response with the expected allow headers/methods.

- [ ] **Step 2: Run test to verify it fails**
Run: `go test ./internal/access/api -run 'TestCORS'`
Expected: FAIL because the API server currently has no CORS middleware or preflight handling.

- [ ] **Step 3: Write minimal implementation**
Add only the middleware and router wiring necessary to satisfy the open CORS contract.

- [ ] **Step 4: Run test to verify it passes**
Run: `go test ./internal/access/api -run 'TestCORS'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/access/api/server_test.go internal/access/api/cors.go internal/access/api/routes.go docs/superpowers/plans/2026-04-16-api-open-cors.md
git commit -m "feat: enable open cors for api"
```

### Task 2: Verify API-wide impact

**Files:**
- Modify: `internal/access/api/cors.go`
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`

- [ ] **Step 1: Run focused API verification**
Run: `go test ./internal/access/api`
Expected: PASS.

- [ ] **Step 2: Run impacted app verification**
Run: `go test ./internal/app -run 'TestNewBuildsOptionalAPIServerWhenConfigured|TestObservability|TestConversationSync'`
Expected: PASS.

- [ ] **Step 3: Review diff scope**
Confirm the change is limited to API-level CORS behavior and does not alter gateway transport semantics.
