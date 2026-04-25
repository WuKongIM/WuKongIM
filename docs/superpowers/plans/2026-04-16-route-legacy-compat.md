# Route Legacy Compatibility Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore `/route` and `/route/batch` with the legacy WuKongIM request and response contract in the new API server.

**Architecture:** Keep the compatibility logic inside `internal/access/api` as thin HTTP handlers. Inject precomputed legacy route addresses from the app composition root so the handlers stay entry-focused and preserve the old response shape without adding new routing semantics.

**Tech Stack:** Go, Gin, existing `internal/access/api` server tests, app build wiring.

---

### Task 1: Add legacy route API tests

**Files:**
- Modify: `internal/access/api/server_test.go`
- Test: `internal/access/api/server_test.go`

- [ ] **Step 1: Write the failing test**
Add tests for `GET /route`, `GET /route?intranet=1`, `POST /route/batch`, and invalid batch JSON.

- [ ] **Step 2: Run test to verify it fails**
Run: `go test ./internal/access/api -run 'TestRoute'`
Expected: FAIL because the routes and route config wiring do not exist yet.

- [ ] **Step 3: Write minimal implementation**
Add only the server options and handlers required for the legacy JSON contract.

- [ ] **Step 4: Run test to verify it passes**
Run: `go test ./internal/access/api -run 'TestRoute'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/access/api/server_test.go internal/access/api/routes.go internal/access/api/route.go internal/access/api/server.go internal/app/build.go docs/superpowers/plans/2026-04-16-route-legacy-compat.md
git commit -m "feat: restore legacy route api"
```

### Task 2: Wire legacy route config from app build

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/access/api/server.go`
- Create: `internal/access/api/route.go`
- Modify: `internal/access/api/routes.go`

- [ ] **Step 1: Write the failing test**
Extend API tests so constructor-injected address data drives the legacy responses.

- [ ] **Step 2: Run test to verify it fails**
Run: `go test ./internal/access/api -run 'TestRoute'`
Expected: FAIL until constructor fields and handlers are wired.

- [ ] **Step 3: Write minimal implementation**
Inject external/intranet address strings from the configured gateway listeners and expose them through the two handlers.

- [ ] **Step 4: Run test to verify it passes**
Run: `go test ./internal/access/api -run 'TestRoute'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/access/api/server_test.go internal/access/api/routes.go internal/access/api/route.go internal/access/api/server.go internal/app/build.go docs/superpowers/plans/2026-04-16-route-legacy-compat.md
git commit -m "feat: wire legacy route api"
```

### Task 3: Verify targeted regressions

**Files:**
- Test: `internal/access/api/server_test.go`

- [ ] **Step 1: Run focused API verification**
Run: `go test ./internal/access/api`
Expected: PASS.

- [ ] **Step 2: Run app wiring verification**
Run: `go test ./internal/app -run 'TestNewBuildsOptionalAPIServerWhenConfigured|TestObservability|TestConversationSync'`
Expected: PASS for impacted app wiring coverage.

- [ ] **Step 3: Review diff for compatibility scope**
Confirm only the legacy endpoints and their address wiring were added.
