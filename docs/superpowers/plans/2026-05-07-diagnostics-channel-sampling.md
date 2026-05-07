# Diagnostics Channel Sampling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make diagnostics debug matches by `channel_key` retain the main send-path events for that channel so operators can query them by `client_msg_no`.

**Architecture:** Add diagnostics-safe `ChannelKey` to gateway and message sendtrace events using the existing `pkg/channel/handler.KeyFromChannelID` format. Extend the legacy HTTP `/message/send` adapter to accept and forward `client_msg_no` so API-sent messages can be queried the same way as gateway-sent messages. Keep sampling logic unchanged; channel-scoped sampling continues to use existing `WK_DIAGNOSTICS_DEBUG_MATCHES` rules.

**Tech Stack:** Go, sendtrace diagnostics sink, existing gateway/API/message usecase tests, docker `WK_` config files.

---

### Task 1: Gateway sendtrace channel key

**Files:**
- Modify: `internal/access/gateway/frame_router.go`
- Test: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write the failing test**

Add assertions to `TestHandleSendAssignsTraceIDToCommandAndSendTrace` that `gateway.messages_send` and `gateway.write_sendack` events include `ChannelKey=channel/1/dTJAdTE` for person channel `u2@u1`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/access/gateway -run TestHandleSendAssignsTraceIDToCommandAndSendTrace -count=1`

Expected: FAIL because gateway sendtrace events do not set `ChannelKey` yet.

- [ ] **Step 3: Write minimal implementation**

Compute `channelhandler.KeyFromChannelID(channel.ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType})` after mapping the command and attach it to both gateway sendtrace events.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/access/gateway -run TestHandleSendAssignsTraceIDToCommandAndSendTrace -count=1`

Expected: PASS.

### Task 2: Message durable sendtrace channel key

**Files:**
- Modify: `internal/usecase/message/send.go`
- Test: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write the failing test**

Add a sendtrace sink test that calls `message.App.Send` for a group channel and asserts the `message.send_durable` event includes `ChannelKey=channel/2/ZzE`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/usecase/message -run TestSendRecordsDurableTraceWithChannelKey -count=1`

Expected: FAIL because the durable sendtrace event does not set `ChannelKey` yet.

- [ ] **Step 3: Write minimal implementation**

Attach `channelhandler.KeyFromChannelID(channelID)` to the `message.send_durable` event.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/usecase/message -run TestSendRecordsDurableTraceWithChannelKey -count=1`

Expected: PASS.

### Task 3: HTTP send client_msg_no mapping

**Files:**
- Modify: `internal/access/api/message_send.go`
- Test: `internal/access/api/server_test.go`

- [ ] **Step 1: Write the failing test**

Extend `TestSendMessageMapsJSONToUsecaseCommand` to include `client_msg_no` and assert it reaches `message.SendCommand.ClientMsgNo`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/access/api -run TestSendMessageMapsJSONToUsecaseCommand -count=1`

Expected: FAIL because HTTP send ignores `client_msg_no`.

- [ ] **Step 3: Write minimal implementation**

Add `ClientMsgNo string \`json:"client_msg_no"\`` to `sendMessageRequest` and forward it into `message.SendCommand`.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off go test ./internal/access/api -run TestSendMessageMapsJSONToUsecaseCommand -count=1`

Expected: PASS.

### Task 4: Documentation and verification

**Files:**
- Modify: `wukongim.conf.example`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Document channel debug match format**

Add a concise comment showing `channel/<type>/<base64url(channel_id)>` and a sample `WK_DIAGNOSTICS_DEBUG_MATCHES` value.

- [ ] **Step 2: Run focused tests**

Run: `GOWORK=off go test ./internal/access/gateway ./internal/usecase/message ./internal/access/api -count=1`

Expected: PASS.

- [ ] **Step 3: Run diagnostics-adjacent tests**

Run: `GOWORK=off go test ./internal/observability/diagnostics ./internal/app ./internal/access/manager -count=1`

Expected: PASS.

- [ ] **Step 4: Commit implementation**

```bash
git add internal/access/gateway/handler_test.go internal/access/gateway/frame_router.go internal/usecase/message/send_test.go internal/usecase/message/send.go internal/access/api/server_test.go internal/access/api/message_send.go wukongim.conf.example docs/development/PROJECT_KNOWLEDGE.md docs/superpowers/plans/2026-05-07-diagnostics-channel-sampling.md
git commit -m "feat: support channel-scoped diagnostics sampling"
```
