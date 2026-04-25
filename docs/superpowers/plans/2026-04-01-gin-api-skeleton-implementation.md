# Gin API Skeleton Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a minimal Gin-based API ingress under `internal/access/api` with `GET /healthz` and `POST /api/messages/send`, and wire it into `internal/app` as an optional runtime.

**Architecture:** The implementation stays thin. First add the API package and route tests around a narrow message-usecase port. Then add app-level config and lifecycle wiring so the API server can be enabled without disturbing gateway behavior. The message endpoint maps JSON input into `message.SendCommand` and maps `message.SendResult` back to JSON without adding API-specific business logic.

**Tech Stack:** Go 1.23, Gin, `net/http`, `httptest`, existing `internal/usecase/message`, `internal/app`.

**Spec:** `docs/superpowers/specs/2026-04-01-gin-api-skeleton-design.md`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/access/api/server.go` | API server struct, Gin engine creation, HTTP start/stop lifecycle |
| `internal/access/api/routes.go` | route registration |
| `internal/access/api/health.go` | health endpoint |
| `internal/access/api/message_send.go` | send-message request/response mapping and handler |
| `internal/access/api/server_test.go` | API unit tests with fake message usecase |
| `internal/access/api/integration_test.go` | real API server integration test |
| `internal/app/config.go` | optional `APIConfig` |
| `internal/app/config_test.go` | API config defaults/validation coverage |
| `internal/app/app.go` | optional API server field and accessor |
| `internal/app/build.go` | optional API server construction |
| `internal/app/lifecycle.go` | API start/stop wiring |
| `internal/app/lifecycle_test.go` | lifecycle ordering with API enabled |
| `go.mod` | add Gin dependency |

## Task 1: Add the API adapter package

**Files:**
- Create: `internal/access/api/server.go`
- Create: `internal/access/api/routes.go`
- Create: `internal/access/api/health.go`
- Create: `internal/access/api/message_send.go`
- Create: `internal/access/api/server_test.go`

- [ ] **Step 1: Write the failing API adapter tests**

Add tests for:
- `TestHealthzReturnsOK`
- `TestSendMessageMapsJSONToUsecaseCommand`
- `TestSendMessageRejectsInvalidBase64Payload`
- `TestSendMessageReturnsInternalServerErrorWhenUsecaseFails`

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/access/api -v`

Expected: FAIL because the package does not exist yet.

- [ ] **Step 3: Implement the minimal Gin API server**

Implementation notes:
- create a narrow `MessageUsecase` port with `Send(message.SendCommand) (message.SendResult, error)`
- `GET /healthz` returns `200` and `{"status":"ok"}`
- `POST /api/messages/send` binds JSON, decodes base64 payload, calls the usecase, returns JSON result
- return `400` on invalid request / base64
- return `500` with `{"error":"..."}` on usecase failure

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/access/api -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/api go.mod go.sum
git commit -m "feat(api): add gin ingress skeleton"
```

## Task 2: Add API server integration coverage

**Files:**
- Create: `internal/access/api/integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Add one integration test that:
- constructs a real `message.App`
- starts the API server on `127.0.0.1:0`
- posts a valid send request
- verifies the JSON response shape and status code

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/access/api -run 'TestAPIServer' -v`

Expected: FAIL until the server lifecycle is correctly implemented.

- [ ] **Step 3: Implement any missing lifecycle/start-stop behavior**

Implementation notes:
- use `http.Server`
- support `Start()` and `Stop(ctx)`
- expose bound address if needed for tests

- [ ] **Step 4: Run the targeted test to verify it passes**

Run: `go test ./internal/access/api -run 'TestAPIServer' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/api/integration_test.go internal/access/api/server.go
git commit -m "test(api): cover gin server lifecycle"
```

## Task 3: Wire API into `internal/app`

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write the failing app tests**

Add tests for:
- `TestNewBuildsOptionalAPIServerWhenConfigured`
- `TestStartStartsAPIServerWhenEnabled`
- `TestStopStopsAPIServerBeforeClusterClose`

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -v`

Expected: FAIL because `internal/app` does not yet know about API runtime.

- [ ] **Step 3: Implement minimal app integration**

Implementation notes:
- add `APIConfig` with `ListenAddr string`
- treat empty `ListenAddr` as disabled
- add `api *api.Server` field plus accessor
- build API server using the existing `message.App`
- start API server during `App.Start()`
- stop API server during `App.Stop()` before storage closes

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/app -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat(app): wire optional gin api runtime"
```

## Task 4: Run full verification

**Files:**
- Verify only

- [ ] **Step 1: Run targeted package verification**

Run: `go test ./internal/access/api ./internal/app -v`

Expected: PASS

- [ ] **Step 2: Run full repository verification**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: Commit final cleanups if needed**

```bash
git add -A
git commit -m "test: verify gin api skeleton integration"
```
