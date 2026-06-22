# Internalv2 Plugin Channel Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PDK-compatible plugin-origin `/channel/messages` host RPC support to `internalv2`.

**Architecture:** `internalv2/access/plugin` registers and decodes `/channel/messages`, then delegates to `internalv2/usecase/plugin.App.ChannelMessages`. The plugin usecase maps `pluginproto.ChannelMessageBatchReq` into `internalv2/usecase/message.ChannelMessageQuery` calls and uses a narrow `MessageReader` port wired by `internalv2/app` to the existing clusterv2 committed-message reader.

**Tech Stack:** Go, `internal/usecase/plugin/pluginproto`, `internalv2/usecase/message`, `internalv2/infra/cluster`, `wkrpc`, standard `go test` benchmarks.

---

## Files

- Create: `internalv2/usecase/plugin/host_rpc_channel_messages.go`
- Create: `internalv2/usecase/plugin/host_rpc_channel_messages_test.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/mapping.go`
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

### Task 1: Usecase ChannelMessages Mapping

**Files:**
- Create: `internalv2/usecase/plugin/host_rpc_channel_messages.go`
- Create: `internalv2/usecase/plugin/host_rpc_channel_messages_test.go`
- Modify: `internalv2/usecase/plugin/types.go`
- Modify: `internalv2/usecase/plugin/mapping.go`

- [ ] **Step 1: Write failing usecase tests**

Add tests that construct a plugin app with a fake `MessageReader` and assert batch query mapping, response message mapping, default limit, max limit, not-found empty response, missing reader error, and payload clone behavior.

- [ ] **Step 2: Run usecase tests and verify RED**

Run: `go test ./internalv2/usecase/plugin -run 'TestChannelMessages' -count=1`

Expected: fail because `Options.MessageReader`, `App.ChannelMessages`, and `ErrMessageReaderRequired` do not exist in v2.

- [ ] **Step 3: Implement minimal usecase support**

Add `MessageReader`, `ErrMessageReaderRequired`, `Options.MessageReader`, `App.messageReader`, mapping helpers, and `App.ChannelMessages`.

- [ ] **Step 4: Run usecase tests and verify GREEN**

Run: `go test ./internalv2/usecase/plugin -run 'TestChannelMessages' -count=1`

Expected: pass.

### Task 2: Access Host RPC Route

**Files:**
- Modify: `internalv2/access/plugin/server.go`
- Modify: `internalv2/access/plugin/handlers_message.go`
- Modify: `internalv2/access/plugin/server_test.go`

- [ ] **Step 1: Write failing access tests**

Extend `recordingUsecase` with `ChannelMessages`, assert `/channel/messages` is registered, and add a handler test that decodes `ChannelMessageBatchReq`, uses timeout context, passes caller UID, and writes `ChannelMessageBatchResp`.

- [ ] **Step 2: Run access tests and verify RED**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleChannelMessages|TestChannelMessagesHostRPC' -count=1`

Expected: fail because `/channel/messages` is not registered and the usecase interface lacks `ChannelMessages`.

- [ ] **Step 3: Implement access handler**

Extend the access `Usecase` interface, register `/channel/messages`, dispatch it in `handlePath`, and implement `handleChannelMessages` using existing `decodeProto`, `usecaseContext`, and `writeProto` helpers.

- [ ] **Step 4: Run access tests and verify GREEN**

Run: `go test ./internalv2/access/plugin -run 'TestServerRegisters|TestHandleChannelMessages|TestChannelMessagesHostRPC' -count=1`

Expected: pass.

### Task 3: App Wiring

**Files:**
- Modify: `internalv2/app/plugin.go`
- Modify: `internalv2/app/plugin_test.go`

- [ ] **Step 1: Write failing app wiring test**

Add a test proving plugin `ChannelMessages` is backed by `clusterinfra.NewChannelMessageReader` when the cluster supports `ChannelMessageReadNode`.

- [ ] **Step 2: Run app plugin tests and verify RED**

Run: `go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsChannelMessageReader|TestNewWiresPluginUsecaseAsMessageSender' -count=1`

Expected: fail because v2 plugin options do not wire a message reader.

- [ ] **Step 3: Implement app reader wiring**

In `wirePluginSubsystem`, set `pluginusecase.Options.MessageReader` to `clusterinfra.NewChannelMessageReader(readNode)` when the cluster implements `clusterinfra.ChannelMessageReadNode`.

- [ ] **Step 4: Run app plugin tests and verify GREEN**

Run: `go test ./internalv2/app -run 'TestNewWiresPluginUsecaseAsChannelMessageReader|TestNewWiresPluginUsecaseAsMessageSender' -count=1`

Expected: pass.

### Task 4: Benchmarks And Flow Docs

**Files:**
- Modify: `internalv2/usecase/plugin/benchmark_test.go`
- Modify: `internalv2/access/plugin/server_test.go`
- Modify: `internalv2/usecase/plugin/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Add benchmark tests**

Add `BenchmarkChannelMessagesFromPluginReq` in `internalv2/usecase/plugin` and `BenchmarkChannelMessagesHostRPCHandler` in `internalv2/access/plugin`.

- [ ] **Step 2: Run benchmarks**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(ChannelMessagesFromPluginReq|ChannelMessagesHostRPCHandler)' -benchmem`

Expected: benchmark output with allocation counts and no test failures.

- [ ] **Step 3: Update FLOW docs**

Document `/channel/messages` in `internalv2/usecase/plugin/FLOW.md` and update `internalv2/app/FLOW.md` to mention lifecycle plus message send plus channel messages host RPCs.

- [ ] **Step 4: Run focused package tests**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/app -count=1`

Expected: pass.

### Task 5: Final Verification And Commit

**Files:**
- All modified files.

- [ ] **Step 1: Run focused migration verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/usecase/message ./internalv2/infra/cluster ./internalv2/app -count=1`

Expected: pass.

- [ ] **Step 2: Run benchmark verification**

Run: `go test ./internalv2/usecase/plugin ./internalv2/access/plugin -run '^$' -bench 'Benchmark(ChannelMessagesFromPluginReq|ChannelMessagesHostRPCHandler|SendMessageFromPluginReq|MessageSendHostRPCHandler)' -benchmem`

Expected: benchmark output with no failures.

- [ ] **Step 3: Inspect diff**

Run: `git diff --stat && git diff --check`

Expected: changed files match this plan and `git diff --check` exits 0.

- [ ] **Step 4: Commit**

Run: `git add ... && git commit -m "feat: support internalv2 plugin channel messages"`

Expected: one commit containing the migration.

## Self Review

- Spec coverage: compatibility rules map to Task 1 tests; route behavior maps to Task 2; app reader wiring maps to Task 3; benchmarks/docs map to Task 4.
- Placeholder scan: no TBD or open-ended implementation steps.
- Type consistency: `MessageReader.SyncMessages(context.Context, message.ChannelMessageQuery) (message.ChannelMessagePage, error)` matches the v2 message usecase port.
