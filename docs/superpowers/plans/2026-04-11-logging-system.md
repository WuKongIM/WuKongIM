# Logging System Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the logging system described in `docs/raw/logging-architecture.md`, wire it through the app composition root, and expose a reusable logger interface for packages.

**Architecture:** Add a `pkg/wklog` abstraction layer and an `internal/log` zap+lumberjack implementation, then initialize a root logger in `internal/app` and pass named child loggers through constructor options. Keep business packages depending only on `pkg/wklog` while `internal/app` remains the only place that knows about the zap implementation.

**Tech Stack:** Go, `go.uber.org/zap`, `gopkg.in/natefinch/lumberjack.v2`, existing app/config wiring, Go tests.

---

### Task 1: Add the public logging abstraction

**Files:**
- Create: `pkg/wklog/logger.go`
- Create: `pkg/wklog/field.go`
- Create: `pkg/wklog/nop.go`
- Test: `pkg/wklog/field_test.go`
- Test: `pkg/wklog/nop_test.go`

- [ ] **Step 1: Write the failing tests** for `Field` constructors and `NewNop()` behavior.
- [ ] **Step 2: Run `go test ./pkg/wklog` to verify they fail** because the package does not exist yet.
- [ ] **Step 3: Implement the minimal abstraction** with `Logger`, `Field`, `FieldType`, helpers, and `NopLogger`.
- [ ] **Step 4: Run `go test ./pkg/wklog` to verify it passes.**

### Task 2: Add the zap-backed internal logger

**Files:**
- Create: `internal/log/config.go`
- Create: `internal/log/zap.go`
- Create: `internal/log/writer.go`
- Test: `internal/log/zap_test.go`

- [ ] **Step 1: Write the failing tests** covering defaults, field conversion, `Named`, `With`, and level/file routing using a temp log directory.
- [ ] **Step 2: Run `go test ./internal/log` to verify they fail** for the expected missing implementation reasons.
- [ ] **Step 3: Implement the zap logger** with encoder selection, rotating writers, `wklog.Field` conversion, and safe `Sync` semantics.
- [ ] **Step 4: Run `go test ./internal/log` to verify it passes.**

### Task 3: Wire logging config into app and config loading

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write failing config tests** for `WK_LOG_*` parsing and defaulting.
- [ ] **Step 2: Run `go test ./cmd/wukongim -run Test.*Log` to verify they fail.**
- [ ] **Step 3: Add `LogConfig` to `app.Config` and parse env/config values** into it.
- [ ] **Step 4: Run `go test ./cmd/wukongim -run Test.*Log` to verify it passes.**

### Task 4: Inject loggers from the composition root

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle.go`
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/presence/app.go`
- Modify: `internal/usecase/delivery/app.go`
- Modify: `internal/usecase/conversation/app.go`
- Modify: `internal/usecase/conversation/projector.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/channel/log/cluster.go`
- Modify: `pkg/channel/node/runtime.go`
- Modify: related option/type files as required
- Test: `internal/app/build_test.go`
- Test: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write failing app/build tests** asserting the app constructs a root logger and syncs it on shutdown, plus constructor-level defaults for injected modules.
- [ ] **Step 2: Run targeted `go test` commands** to verify the failures are due to missing logger plumbing.
- [ ] **Step 3: Thread `wklog.Logger` through constructor options** and initialize named child loggers in `internal/app/build.go`.
- [ ] **Step 4: Replace direct standard-library logging in `cmd/wukongim/main.go` if needed** so startup/shutdown errors also use the new system consistently.
- [ ] **Step 5: Run the targeted tests again** to verify logger injection passes.

### Task 5: Verify the integrated logging system

**Files:**
- Verify: `pkg/wklog/*.go`
- Verify: `internal/log/*.go`
- Verify: `internal/app/*.go`
- Verify: `cmd/wukongim/*.go`

- [ ] **Step 1: Run focused packages** with `go test ./pkg/wklog ./internal/log ./internal/app ./cmd/wukongim`.
- [ ] **Step 2: Run broader regression coverage** with `go test ./internal/... ./pkg/...`.
- [ ] **Step 3: Fix any remaining failures** and rerun the impacted commands until clean.
- [ ] **Step 4: Summarize the final file/behavior changes** and any follow-up logging adoption work.
