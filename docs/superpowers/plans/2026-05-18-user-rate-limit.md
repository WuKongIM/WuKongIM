# User Send Rate Limit Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add high-performance node-local UID send rate limiting to the unified message send path.

**Architecture:** The message usecase owns business enforcement and calls a small limiter interface before expensive send work. `internal/runtime/userlimit` provides a sharded in-memory token bucket implementation with lazy refill and idle cleanup. App config builds the limiter only when `WK_MESSAGE_USER_RATE_LIMIT_ENABLED` is true, preserving current behavior by default.

**Tech Stack:** Go, standard library synchronization/time, existing WuKongIM app config and frame reason model.

---

### Task 1: Protocol Reason

**Files:**
- Modify: `pkg/protocol/frame/reason_code.go` or equivalent reason-code file
- Test: existing gateway/message tests that assert sendack reason

- [ ] Locate existing `ReasonCode` constants with `rg "type ReasonCode|Reason[A-Za-z]+" pkg/protocol/frame`.
- [ ] If no rate-limit reason exists, add `ReasonRateLimit` with a stable numeric value near other send failure reasons.
- [ ] Keep the name generic, not transport-specific.

### Task 2: Runtime Limiter TDD

**Files:**
- Create: `internal/runtime/userlimit/limiter.go`
- Create: `internal/runtime/userlimit/limiter_test.go`

- [ ] Write tests for burst allow, exhaustion reject, elapsed-time refill, retry-after, idle eviction, max bucket cap, disabled/nil-safe behavior, and concurrent `AllowSend`.
- [ ] Run `go test ./internal/runtime/userlimit` and verify tests fail because implementation is missing.
- [ ] Implement minimal sharded token bucket with lazy refill.
- [ ] Run `go test ./internal/runtime/userlimit` and verify it passes.

### Task 3: Message Usecase TDD

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/command.go` if request/decision types belong there
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] Add tests proving over-limit sends return `ReasonRateLimit` and do not call permission store, plugin hook, or channel append.
- [ ] Add tests proving system UID bypass and plugin-origin default limit behavior.
- [ ] Run focused message tests and verify failure before implementation.
- [ ] Add `UserSendLimiter` interface, request/decision structs, options field, app field, and early send check.
- [ ] Run focused message tests and verify pass.

### Task 4: Config and App Wiring TDD

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/build.go`
- Modify: `wukongim.conf.example`

- [ ] Add app config tests for defaults and validation.
- [ ] Add command config parsing tests for `WK_MESSAGE_USER_RATE_LIMIT_*` keys.
- [ ] Run focused config tests and verify failure.
- [ ] Add config fields with detailed English comments, parsing, validation, defaults, and example keys.
- [ ] Wire `userlimit.New` into `app.build` and inject into `message.Options`.
- [ ] Run focused config tests and verify pass.

### Task 5: Gateway Sendack Coverage

**Files:**
- Modify: `internal/access/gateway/handler_test.go`

- [ ] Add or adjust a test that stubs `MessageUsecase.Send` returning `SendResult{Reason: frame.ReasonRateLimit}` and verifies sendack reason.
- [ ] Run gateway focused test and verify pass.

### Task 6: Verification

**Files:**
- All touched files

- [ ] Run `go test ./internal/runtime/userlimit ./internal/usecase/message ./internal/access/gateway ./internal/app ./cmd/wukongim`.
- [ ] If package-level app tests are too broad due existing dirty worktree issues, run narrower named tests and report exactly what was verified.
- [ ] Check `git diff --stat` and ensure unrelated user changes were not reverted.
