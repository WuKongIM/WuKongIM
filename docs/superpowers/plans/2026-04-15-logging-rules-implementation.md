# Logging Rules Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first production-ready slice of the logging rules design by extending `pkg/wklog` with canonical field helpers and migrating the highest-value runtime paths to module-first, event-based logging.

**Architecture:** Keep the existing `wklog.Logger` interface and zap backend intact, then add semantic field helpers in `pkg/wklog` and roll the new conventions out path-by-path. The first rollout covers the log contract itself plus the operationally important paths in `internal/usecase/message`, `internal/usecase/presence`, `internal/access/gateway`, and `pkg/cluster`, using local recording loggers in tests to lock the behavior down.

**Tech Stack:** Go, `pkg/wklog`, `internal/log` zap backend, existing constructor-based dependency injection, Go unit tests.

---

## File Map

### Logging primitives and contract

- Modify: `pkg/wklog/field.go`
  - Keep the primitive constructors and add canonical semantic field helpers such as `Event`, `TraceID`, `NodeID`, `ChannelID`, `SessionID`, `Attempt`, and `Reason`.
- Modify: `pkg/wklog/field_test.go`
  - Add focused tests for helper keys and value encoding.
- Modify: `internal/log/zap_test.go`
  - Ratchet the backend contract so hierarchical `module` names and the new semantic fields stay serialized consistently.

### Message send path

- Modify: `internal/usecase/message/app.go`
  - Add a clear place to derive `message.send` / `message.retry` child loggers from the injected module logger.
- Modify: `internal/usecase/message/send.go`
  - Log primary send-path failures and degraded outcomes with message/channel fields.
- Modify: `internal/usecase/message/retry.go`
  - Log metadata-refresh retry decisions with `event`, object fields, and retry outcome.
- Create: `internal/usecase/message/logging_test.go`
  - Add dedicated tests that assert log ownership, module names, and fields without cluttering the business-behavior tests.

### Gateway access path

- Modify: `internal/access/gateway/handler.go`
  - Derive `access.gateway.conn` and `access.gateway.frame` child loggers.
- Modify: `internal/access/gateway/frame_router.go`
  - Add gateway-side contextual logs for auth rejection, invalid send requests, and downstream send failures without duplicating the usecase-owned `Error`.
- Create: `internal/access/gateway/logging_test.go`
  - Assert the gateway emits contextual `Warn` entries with `sourceModule`, session identifiers, and stable events.

### Presence path

- Modify: `internal/usecase/presence/app.go`
  - Add child logger accessors or cached child loggers for `presence.activate` and `presence.authority`.
- Modify: `internal/usecase/presence/gateway.go`
  - Log activate/deactivate/route-action failures and best-effort rollback paths with route/session identifiers.
- Modify: `internal/usecase/presence/authority.go`
  - Log authority-side state application failures or noteworthy lifecycle boundaries where this module is the owner.
- Create: `internal/usecase/presence/logging_test.go`
  - Assert the new logs are emitted at the right severity and with the expected object fields.

### Cluster transport and controller path

- Modify: `pkg/cluster/cluster.go`
  - Derive child loggers for `cluster.transport` and `cluster.controller` from the injected cluster logger.
- Modify: `pkg/cluster/transport.go`
  - Log skipped raft transport send failures with `nodeID`, `targetNodeID`, and `slotID` while preserving the existing non-fatal send behavior.
- Modify: `pkg/cluster/controller_client.go`
  - Log controller RPC retries, redirect fallthrough, and final failures with slot/node/timeout context.
- Modify: `pkg/cluster/transport_test.go`
  - Add tests for transport logging on swallowed send errors.
- Modify: `pkg/cluster/controller_client_internal_test.go`
  - Add tests that assert controller retry logs and final ownership.

---

### Task 1: Add canonical `wklog` field helpers and freeze the serialization contract

**Files:**
- Modify: `pkg/wklog/field.go`
- Modify: `pkg/wklog/field_test.go`
- Modify: `internal/log/zap_test.go`

- [ ] **Step 1: Add failing tests for the new semantic helpers** in `pkg/wklog/field_test.go`.

```go
func TestSemanticHelpersUseStableKeys(t *testing.T) {
    require.Equal(t, Field{Key: "event", Type: StringType, Value: "message.send.persist.failed"}, Event("message.send.persist.failed"))
    require.Equal(t, Field{Key: "nodeID", Type: Uint64Type, Value: uint64(3)}, NodeID(3))
    require.Equal(t, Field{Key: "timeout", Type: DurationType, Value: 2 * time.Second}, Timeout(2*time.Second))
}
```

- [ ] **Step 2: Add backend contract tests** in `internal/log/zap_test.go` that use nested `Named(...)` loggers plus the new helpers.

```go
child := logger.Named("message").Named("send")
child.Error("persist committed message failed",
    wklog.Event("message.send.persist.failed"),
    wklog.ChannelID("u1@u2"),
    wklog.MessageID(88),
    wklog.Error(errors.New("boom")),
)
```

- [ ] **Step 3: Run the targeted tests to confirm they fail first.**

Run: `go test ./pkg/wklog ./internal/log -run 'Test(SemanticHelpersUseStableKeys|NewLoggerWritesNamedAndContextualFields|NamedLogger.*)'`

Expected: FAIL with undefined helper constructors such as `Event`, `NodeID`, or `ChannelID`.

- [ ] **Step 4: Implement the helper constructors** in `pkg/wklog/field.go` using the existing primitive field types instead of creating a new abstraction layer.

```go
func Event(name string) Field      { return String("event", name) }
func SourceModule(name string) Field { return String("sourceModule", name) }
func NodeID(id uint64) Field       { return Uint64("nodeID", id) }
func ChannelID(id string) Field    { return String("channelID", id) }
func MessageID(id int64) Field     { return Int64("messageID", id) }
func SessionID(id uint64) Field    { return Uint64("sessionID", id) }
func Timeout(d time.Duration) Field { return Duration("timeout", d) }
```

- [ ] **Step 5: Run the same targeted tests again to verify they pass.**

Run: `go test ./pkg/wklog ./internal/log -run 'Test(SemanticHelpersUseStableKeys|NewLoggerWritesNamedAndContextualFields|NamedLogger.*)'`

Expected: PASS.

- [ ] **Step 6: Commit the helper layer and contract tests.**

```bash
git add pkg/wklog/field.go pkg/wklog/field_test.go internal/log/zap_test.go
git commit -m "log: add canonical field helpers"
```

### Task 2: Migrate the primary message send path to `message.send` / `message.retry`

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/retry.go`
- Create: `internal/usecase/message/logging_test.go`

- [ ] **Step 1: Add failing logging tests** for the message send and retry path in `internal/usecase/message/logging_test.go`.

```go
func TestSendLogsPrimaryFailureWithMessageModule(t *testing.T) {
    logger := newRecordingLogger("message")
    app := New(Options{Now: fixedNowFn, Logger: logger, Cluster: &fakeChannelCluster{sendReplies: []fakeChannelClusterSendReply{{err: errors.New("raft quorum unavailable")}}}})

    _, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
    require.Error(t, err)
    requireLogEntry(t, logger, "ERROR", "message.send", "message.send.persist.failed")
}
```

- [ ] **Step 2: Add a failing retry-path test** that expects a retry log when `sendWithMetaRefreshRetry()` refreshes stale metadata.

Run: `go test ./internal/usecase/message -run 'TestSendLogsPrimaryFailureWithMessageModule|TestSendWithMetaRefreshRetryLogsRefreshAttempt'`

Expected: FAIL because the package does not emit the required module/event/object-field logs yet.

- [ ] **Step 3: Add child logger access in `internal/usecase/message/app.go`** so send-path code can derive `message.send` and `message.retry` from the injected `message` logger.

```go
func (a *App) sendLogger() wklog.Logger  { return a.logger.Named("send") }
func (a *App) retryLogger() wklog.Logger { return a.logger.Named("retry") }
```

- [ ] **Step 4: Implement primary send-path logs** in `internal/usecase/message/send.go`.
  - Log unauthenticated sender rejection as `Warn`
  - Log missing cluster wiring as `Error`
  - Log durable append failure as the primary `Error`
  - Log dispatcher submit failure as `Warn` because the send itself already succeeded

- [ ] **Step 5: Implement retry logs** in `internal/usecase/message/retry.go`.
  - Log stale metadata / reroute retry as `Warn`
  - Log refresh failure as `Error` owned by `message.retry`
  - Log second append failure as `Error` with the same object fields and retry context

- [ ] **Step 6: Run the targeted tests again to verify they pass.**

Run: `go test ./internal/usecase/message -run 'TestSendLogsPrimaryFailureWithMessageModule|TestSendWithMetaRefreshRetryLogsRefreshAttempt'`

Expected: PASS.

- [ ] **Step 7: Commit the message-path logging migration.**

```bash
git add internal/usecase/message/app.go internal/usecase/message/send.go internal/usecase/message/retry.go internal/usecase/message/logging_test.go
git commit -m "log: instrument message send path"
```

### Task 3: Add gateway-side contextual logs without duplicating the primary message error

**Files:**
- Modify: `internal/access/gateway/handler.go`
- Modify: `internal/access/gateway/frame_router.go`
- Create: `internal/access/gateway/logging_test.go`

- [ ] **Step 1: Add failing gateway logging tests** for auth rejection, invalid send input, and downstream send failures.

```go
func TestHandleSendLogsContextualWarnWithSourceModule(t *testing.T) {
    logger := newRecordingLogger("access.gateway")
    handler := New(Options{Logger: logger, Messages: &fakeMessageUsecase{sendErr: errors.New("raft quorum unavailable")}})
    err := handler.OnFrame(ctxWithAuthedSender(t), validSendPacket())
    require.Error(t, err)
    requireLogEntry(t, logger, "WARN", "access.gateway.frame", "access.gateway.frame.send_failed")
    requireLogField(t, logger, "sourceModule", "message.send")
}
```

- [ ] **Step 2: Run the gateway-focused tests to confirm they fail first.**

Run: `go test ./internal/access/gateway -run 'Test(HandleSendLogsContextualWarnWithSourceModule|HandlerOnSessionActivateLogsAuthFailure)'`

Expected: FAIL because the handler does not emit the contextual logs yet.

- [ ] **Step 3: Add child logger access in `internal/access/gateway/handler.go`** for `access.gateway.conn` and `access.gateway.frame`.

```go
func (h *Handler) connLogger() wklog.Logger  { return h.logger.Named("conn") }
func (h *Handler) frameLogger() wklog.Logger { return h.logger.Named("frame") }
```

- [ ] **Step 4: Instrument the gateway entrypoints** in `internal/access/gateway/handler.go` and `internal/access/gateway/frame_router.go`.
  - `OnSessionActivate`: log unauthenticated session or missing presence wiring
  - `handleSend`: log contextual `Warn` on invalid request mapping and downstream usecase failure
  - avoid logging another primary `Error` when `message.send` already owns the failure

- [ ] **Step 5: Rerun the targeted gateway tests to verify they pass.**

Run: `go test ./internal/access/gateway -run 'Test(HandleSendLogsContextualWarnWithSourceModule|HandlerOnSessionActivateLogsAuthFailure)'`

Expected: PASS.

- [ ] **Step 6: Commit the gateway migration.**

```bash
git add internal/access/gateway/handler.go internal/access/gateway/frame_router.go internal/access/gateway/logging_test.go
git commit -m "log: add gateway contextual events"
```

### Task 4: Migrate the presence path to `presence.activate` and `presence.authority`

**Files:**
- Modify: `internal/usecase/presence/app.go`
- Modify: `internal/usecase/presence/gateway.go`
- Modify: `internal/usecase/presence/authority.go`
- Create: `internal/usecase/presence/logging_test.go`

- [ ] **Step 1: Add failing presence logging tests** for activate rollback, best-effort unregister failure, and fenced route mismatch.

```go
func TestActivateLogsAuthorityRegisterFailure(t *testing.T) {
    logger := newRecordingLogger("presence")
    app := New(Options{Logger: logger, LocalNodeID: 1, GatewayBootID: 101, Online: online.NewRegistry(), Router: fakeRouter{slotID: 1}, AuthorityClient: &fakeAuthorityClient{registerErr: errors.New("register failed")}})

    err := app.Activate(context.Background(), validActivateCommand())
    require.Error(t, err)
    requireLogEntry(t, logger, "ERROR", "presence.activate", "presence.activate.authority_register.failed")
}
```

- [ ] **Step 2: Run the targeted presence tests to verify they fail first.**

Run: `go test ./internal/usecase/presence -run 'Test(ActivateLogsAuthorityRegisterFailure|DeactivateLogsBestEffortUnregister|ApplyRouteActionLogsFencedMismatch)'`

Expected: FAIL because the presence package does not emit those logs yet.

- [ ] **Step 3: Add child logger access in `internal/usecase/presence/app.go`** for `presence.activate` and `presence.authority`.

- [ ] **Step 4: Instrument `internal/usecase/presence/gateway.go`**.
  - `Activate`: log authority registration failure and action-dispatch rollback failure as primary `Error`
  - `Deactivate`: log best-effort unregister failure as `Warn`
  - `ApplyRouteAction`: log fenced-route mismatches and closing-state transition failures with `uid`, `sessionID`, `nodeID`, and `gatewayBootID`

- [ ] **Step 5: Instrument `internal/usecase/presence/authority.go`** for authority-owned failures or notable state application boundaries.
  - Keep it sparse; only add logs where this module is the real owner

- [ ] **Step 6: Rerun the targeted presence tests to verify they pass.**

Run: `go test ./internal/usecase/presence -run 'Test(ActivateLogsAuthorityRegisterFailure|DeactivateLogsBestEffortUnregister|ApplyRouteActionLogsFencedMismatch)'`

Expected: PASS.

- [ ] **Step 7: Commit the presence migration.**

```bash
git add internal/usecase/presence/app.go internal/usecase/presence/gateway.go internal/usecase/presence/authority.go internal/usecase/presence/logging_test.go
git commit -m "log: instrument presence workflow"
```

### Task 5: Add transport and controller logs in `pkg/cluster`

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/transport.go`
- Modify: `pkg/cluster/controller_client.go`
- Modify: `pkg/cluster/transport_test.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`

- [ ] **Step 1: Add failing cluster logging tests**.
  - In `pkg/cluster/transport_test.go`, expect a `Warn` when raft transport send is skipped because the target is unavailable.
  - In `pkg/cluster/controller_client_internal_test.go`, expect retry/fallthrough logs and a final `Error` when controller calls exhaust all targets.

```go
func TestRaftTransportLogsSkippedSendFailure(t *testing.T) {
    logger := newRecordingLogger("cluster")
    rt := &raftTransport{client: client, logger: logger.Named("transport")}
    require.NoError(t, rt.Send(context.Background(), []multiraft.Envelope{{SlotID: 1, Message: raftpb.Message{To: 9, From: 1}}}))
    requireLogEntry(t, logger, "WARN", "cluster.transport", "cluster.transport.raft_send.skipped")
}
```

- [ ] **Step 2: Run the targeted cluster tests to confirm they fail first.**

Run: `go test ./pkg/cluster -run 'Test(RaftTransportLogsSkippedSendFailure|ControllerClientLogsRetryableRedirect|ControllerClientLogsFinalFailure)'`

Expected: FAIL because the cluster package does not emit the required logs yet.

- [ ] **Step 3: Derive child loggers in `pkg/cluster/cluster.go`** for `cluster.transport` and `cluster.controller` and thread them into the transport/controller helpers.

- [ ] **Step 4: Instrument `pkg/cluster/transport.go`**.
  - Preserve the current return contract: context cancellation still returns immediately; ordinary send failures remain non-fatal
  - Add `Warn` logs for skipped send failures with `nodeID`, `targetNodeID`, `slotID`, and `event=cluster.transport.raft_send.skipped`

- [ ] **Step 5: Instrument `pkg/cluster/controller_client.go`**.
  - Log redirects and retryable peer failures as `Warn`
  - Log final exhaustion as the primary `Error`
  - Include `slotID` when present, `targetNodeID`, and controller RPC kind in every abnormal log

- [ ] **Step 6: Rerun the targeted cluster tests to verify they pass.**

Run: `go test ./pkg/cluster -run 'Test(RaftTransportLogsSkippedSendFailure|ControllerClientLogsRetryableRedirect|ControllerClientLogsFinalFailure)'`

Expected: PASS.

- [ ] **Step 7: Commit the cluster logging migration.**

```bash
git add pkg/cluster/cluster.go pkg/cluster/transport.go pkg/cluster/controller_client.go pkg/cluster/transport_test.go pkg/cluster/controller_client_internal_test.go
git commit -m "log: instrument cluster transport and controller"
```

### Task 6: Run focused regression and repository verification

**Files:**
- Verify: `pkg/wklog/*.go`
- Verify: `internal/log/*.go`
- Verify: `internal/usecase/message/*.go`
- Verify: `internal/access/gateway/*.go`
- Verify: `internal/usecase/presence/*.go`
- Verify: `pkg/cluster/*.go`

- [ ] **Step 1: Run the focused package set.**

Run: `go test ./pkg/wklog ./internal/log ./internal/usecase/message ./internal/access/gateway ./internal/usecase/presence ./pkg/cluster`

Expected: PASS.

- [ ] **Step 2: Run the broader regression set required by the repo instructions.**

Run: `go test ./internal/... ./pkg/...`

Expected: PASS. If unrelated long-running integration tests appear, stop and keep the run to unit-test coverage only.

- [ ] **Step 3: Fix any regressions and rerun only the impacted commands** until clean.

- [ ] **Step 4: Update any touched docs or `FLOW.md` files if implementation drift introduced behavior that the docs now describe incorrectly.**

- [ ] **Step 5: Prepare the final execution summary** listing:
  - helper functions added in `pkg/wklog`
  - modules instrumented in the first rollout
  - tests added or extended
  - remaining modules deferred to a later phase

- [ ] **Step 6: Commit the final verified state.**

```bash
git add pkg/wklog internal/log internal/usecase/message internal/access/gateway internal/usecase/presence pkg/cluster
git commit -m "log: roll out structured module-first logging rules"
```
