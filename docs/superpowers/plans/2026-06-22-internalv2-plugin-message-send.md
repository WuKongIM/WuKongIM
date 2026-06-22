# Internalv2 Plugin Message Send Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PDK-compatible plugin-origin `/message/send` host RPC support to `internalv2`.

**Architecture:** `internalv2/access/plugin` registers and decodes `/message/send`, then delegates to `internalv2/usecase/plugin.App.SendMessage`. The plugin usecase maps `pluginproto.SendReq` into `internalv2/usecase/message.SendCommand` and calls a narrow `MessageSender` port wired by `internalv2/app`.

**Tech Stack:** Go, `internal/usecase/plugin/pluginproto`, `internalv2/usecase/message`, `wkrpc`, standard `go test` benchmarks.

---

## Files

- Create: `internalv2/usecase/plugin/host_rpc_message.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/mapping.go`
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Create: `internalv2/usecase/plugin/host_rpc_message_test.go`
- Create: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

### Task 1: Usecase Message Send Mapping

**Files:**
- Create: `internalv2/usecase/plugin/host_rpc_message_test.go`
- Create: `internalv2/usecase/plugin/host_rpc_message.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/app.go`
- Modify: `internalv2/usecase/plugin/mapping.go`

- [ ] **Step 1: Write failing usecase tests**

Add tests that construct a plugin app with a fake `MessageSender` and assert `SendMessage` maps `pluginproto.SendReq` to `message.SendCommand` with default system UID fallback, header flags, cloned payload, person-channel normalization, and plugin origin.

- [ ] **Step 2: Run usecase tests and verify RED**

Run: `go test ./internalv2/usecase/plugin -run 'TestSendMessage' -count=1`

Expected: fail because `App.SendMessage`, `MessageSender`, and related errors do not exist.

- [ ] **Step 3: Implement minimal usecase support**

Add `MessageSender`, `ErrMessageSenderRequired`, `ErrDefaultSenderUIDRequired`, `Options.Messages`, `Options.DefaultSenderUID`, `App.messages`, `App.defaultSenderUID`, mapping helper, and `App.SendMessage`.

- [ ] **Step 4: Run usecase tests and verify GREEN**

Run: `go test ./internalv2/usecase/plugin -run 'TestSendMessage' -count=1`

Expected: pass.

### Task 2: Access Host RPC Route

**Files:**
- Create: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/server_test.go`

- [ ] **Step 1: Write failing access tests**

Extend `recordingUsecase` with `SendMessage`, assert `/message/send` is registered, and add a handler test that decodes `pluginproto.SendReq`, uses the shorter timeout context, passes caller UID, and writes `pluginproto.SendResp`.

- [ ] **Step 2: Run access tests and verify RED**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleSendMessage|TestMessageHostRPC' -count=1`

Expected: fail because `/message/send` is not registered and the usecase interface lacks `SendMessage`.

- [ ] **Step 3: Implement access handler**

Extend the access `Usecase` interface, register `/message/send`, dispatch it in `handlePath`, and implement `handleSendMessage` using existing `decodeProto`, `usecaseContext`, and `writeProto` helpers.

- [ ] **Step 4: Run access tests and verify GREEN**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleSendMessage|TestMessageHostRPC' -count=1`

Expected: pass.

### Task 3: App Wiring

**Files:**
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`

- [ ] **Step 1: Write failing app wiring test**

Add a test proving `app.plugins.SendMessage` can call `app.Messages().Send` through app wiring and returns `ErrRouteNotReady` from the real message usecase rather than `plugin.ErrMessageSenderRequired`.

- [ ] **Step 2: Run app plugin tests and verify RED**

Run: `go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsMessageSender|TestNewWiresPluginUsecaseAsMessageSendHook' -count=1`

Expected: fail because v2 plugin options do not wire a message sender.

- [ ] **Step 3: Implement app adapter**

Add `pluginMessageSender` in `internalv2/app/plugin.go`, pass it to `pluginusecase.Options`, and set `DefaultSenderUID` to `internalv2/usecase/user.DefaultSystemUID`.

- [ ] **Step 4: Run app plugin tests and verify GREEN**

Run: `go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsMessageSender|TestNewWiresPluginUsecaseAsMessageSendHook' -count=1`

Expected: pass.

### Task 4: Benchmarks And Flow Docs

**Files:**
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Add benchmark tests first**

Add `BenchmarkSendMessageFromPluginReq` in `internalv2/usecase/plugin` and `BenchmarkMessageSendHostRPCHandler` in `internalv2/access/plugin`.

- [ ] **Step 2: Run benchmarks**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(SendMessageFromPluginReq|MessageSendHostRPCHandler)' -benchmem`

Expected: benchmark output with allocation counts and no test failures.

- [ ] **Step 3: Update FLOW docs**

Document `/message/send` in `internalv2/usecase/plugin/FLOW.md` and update the plugin subsystem description in `internalv2/app/FLOW.md` from lifecycle-only host RPC to include message send.

- [ ] **Step 4: Run focused package tests**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/app -count=1`

Expected: pass.

### Task 5: Final Verification

**Files:**
- All modified files.

- [ ] **Step 1: Run focused migration verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/usecase/message ./internalv2/app -count=1`

Expected: pass.

- [ ] **Step 2: Run benchmark verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(SendMessageFromPluginReq|MessageSendHostRPCHandler|SendPluginCandidates|BeforeSend)' -benchmem`

Expected: benchmark output with no failures.

- [ ] **Step 3: Inspect diff**

Run: `git diff --stat && git diff --check`

Expected: changed files match this plan and `git diff --check` exits 0.

- [ ] **Step 4: Commit**

Run: `git add ... && git commit -m "feat: support internalv2 plugin message send"`

Expected: one commit containing the migration.

## Self Review

- Spec coverage: all compatibility rules map to Task 1 tests and implementation; route behavior maps to Task 2; app circular wiring maps to Task 3; benchmark/doc requirements map to Task 4.
- Placeholder scan: no task uses TBD or undefined later-only functions without introducing them.
- Type consistency: `MessageSender.Send(context.Context, message.SendCommand) (message.SendResult, error)` matches the v2 message usecase.
