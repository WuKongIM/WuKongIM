# Internalv2 Online Route Batch 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect online lifecycle to gateway authentication without changing default internalv2 app wiring yet.

**Architecture:** Add a narrow `pkg/gateway` rollback hook for sessions that activate before CONNACK but never reach `OnSessionOpen`. Then teach `internalv2/access/gateway` to map gateway lifecycle events to `online.Activate` and `online.Deactivate` when an online usecase is injected. Real node RPC and default app wiring are intentionally left for later batches.

**Tech Stack:** Go 1.23, `pkg/gateway`, `internalv2/access/gateway`, `internalv2/usecase/online`, `testing`.

**Spec:** `docs/superpowers/specs/2026-06-01-internalv2-online-route-design.md`

---

## Batch Scope

This batch adds:

- A gateway activation rollback interface.
- Core rollback calls when activation succeeded but success CONNACK did not complete.
- `internalv2/access/gateway` online lifecycle adapter.
- Tests proving activation, close, and rollback mapping.

This batch does not:

- Add clusterv2 online RPC service IDs.
- Add `internalv2/access/node`.
- Wire online into `internalv2/app.New` by default.
- Implement concrete online outbound frame encoding for real delivery writes.

Keeping default app wiring unchanged avoids a temporary multi-node regression
where CONNECT to a non-leader node would fail before real authority RPC exists.

## File Map

| Path | Responsibility |
|------|----------------|
| `pkg/gateway/types/event.go` | Define optional activation rollback handler. |
| `pkg/gateway/event.go` | Re-export the rollback handler at package edge. |
| `pkg/gateway/core/server.go` | Invoke rollback when pre-open activation must be undone. |
| `pkg/gateway/core/server_test.go` | Lock activation rollback behavior. |
| `pkg/gateway/FLOW.md` | Document activation rollback lifecycle. |
| `internalv2/access/gateway/handler.go` | Accept optional online lifecycle dependency. |
| `internalv2/access/gateway/online_lifecycle.go` | Map gateway contexts to online commands and rollback. |
| `internalv2/access/gateway/online_lifecycle_test.go` | Test activate, close, rollback, and nil-online behavior. |
| `internalv2/access/gateway/FLOW.md` | Document online lifecycle adapter boundary. |

## Task 1: Gateway Activation Rollback Hook

**Files:**
- Modify: `pkg/gateway/types/event.go`
- Modify: `pkg/gateway/event.go`
- Modify: `pkg/gateway/core/server.go`
- Modify: `pkg/gateway/core/server_test.go`
- Modify: `pkg/gateway/FLOW.md`

- [ ] **Step 1: Write failing rollback tests**

Add tests to `pkg/gateway/core/server_test.go`:

```go
func TestServerWKProtoActivationRollbackRunsWhenSuccessConnackWriteFails(t *testing.T)
func TestServerWKProtoActivationRollbackRunsWhenActivatorReturnsNonSuccessOverride(t *testing.T)
func TestServerWKProtoActivationRollbackDoesNotRunAfterSessionOpen(t *testing.T)
```

Test expectations:

- `OnSessionActivate` is called after auth values are stored.
- If `OnSessionActivate` returns nil error and CONNACK write fails, rollback is
  called once with the same session ID and `OnSessionClose` is not required.
- If `OnSessionActivate` returns a non-success CONNACK override, rollback is
  called once before the session closes.
- If CONNACK success is written and `OnSessionOpen` runs, rollback is not called;
  later close still uses normal `OnSessionClose`.

- [ ] **Step 2: Run gateway core tests and verify they fail**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServerWKProtoActivationRollback' -count=1
```

Expected: FAIL because the rollback interface and calls do not exist.

- [ ] **Step 3: Add rollback interface**

Add to `pkg/gateway/types/event.go`:

```go
// SessionActivationRollback is optionally implemented by handlers that need to
// undo OnSessionActivate when authentication cannot complete the success path.
type SessionActivationRollback interface {
    OnSessionActivationRollback(ctx Context) error
}
```

Add to `pkg/gateway/event.go`:

```go
type SessionActivationRollback = gatewaytypes.SessionActivationRollback
```

- [ ] **Step 4: Invoke rollback in gateway core**

In `pkg/gateway/core/server.go`, track whether `OnSessionActivate` completed
successfully before success CONNACK completion:

```go
activated := false
ctx = s.dispatcher.context(state, replyToken, state.closeReason(), nil)
if activator, ok := s.options.Handler.(gatewaytypes.SessionActivator); ok {
    override, err := activator.OnSessionActivate(&ctx)
    if err != nil {
        // existing error CONNACK path, no rollback because activation failed.
    }
    activated = true
    if override != nil {
        connack = override
    }
}
```

Add a helper on `Server`:

```go
func (s *Server) rollbackSessionActivation(state *sessionState, replyToken string) {
    if s == nil || s.options.Handler == nil || state == nil || state.openWasDispatched() {
        return
    }
    rollback, ok := s.options.Handler.(gatewaytypes.SessionActivationRollback)
    if !ok {
        return
    }
    ctx := s.dispatcher.context(state, replyToken, state.closeReason(), nil)
    if err := rollback.OnSessionActivationRollback(ctx); err != nil {
        s.dispatcher.sessionError(state, state.closeReason(), err)
    }
}
```

Call the helper only when `activated == true` and the success path is not
completed:

- before closing after a CONNACK write error.
- before closing after an activation override changes CONNACK to non-success.

Do not call rollback for `OnSessionActivate` returning an error, because the
online route was not successfully registered in that case.

- [ ] **Step 5: Run gateway core tests and verify they pass**

Run:

```bash
go test ./pkg/gateway/core -run 'TestServerWKProtoActivationRollback' -count=1
```

Expected: PASS.

- [ ] **Step 6: Update gateway FLOW**

Update `pkg/gateway/FLOW.md` section 6.4 and 6.6:

```text
If OnSessionActivate succeeds but success CONNACK cannot complete before
OnSessionOpen, core invokes optional OnSessionActivationRollback. Normal
OnSessionClose remains tied to sessions that reached OnSessionOpen.
```

- [ ] **Step 7: Commit**

```bash
git add pkg/gateway/types/event.go pkg/gateway/event.go pkg/gateway/core/server.go pkg/gateway/core/server_test.go pkg/gateway/FLOW.md
git commit -m "feat(gateway): add activation rollback hook"
```

## Task 2: Internalv2 Gateway Online Lifecycle Adapter

**Files:**
- Modify: `internalv2/access/gateway/handler.go`
- Create: `internalv2/access/gateway/online_lifecycle.go`
- Create: `internalv2/access/gateway/online_lifecycle_test.go`
- Modify: `internalv2/access/gateway/FLOW.md`

- [ ] **Step 1: Write failing lifecycle adapter tests**

Create `internalv2/access/gateway/online_lifecycle_test.go` with tests:

```go
func TestOnSessionActivateCallsOnlineActivate(t *testing.T)
func TestOnSessionActivateWithoutOnlineIsNoop(t *testing.T)
func TestOnSessionCloseCallsOnlineDeactivate(t *testing.T)
func TestOnSessionActivationRollbackCallsOnlineDeactivate(t *testing.T)
func TestOnSessionActivateRejectsMissingUID(t *testing.T)
```

Use a fake online lifecycle:

```go
type recordingOnline struct {
    activates   []online.ActivateCommand
    deactivates []online.DeactivateCommand
    activateErr error
}
```

Build contexts with `coregateway.Context{Session: sess, Listener: "tcp",
RequestContext: context.Background()}` and session values:

```text
gateway.uid = "u1"
gateway.device_id = "d1"
gateway.device_flag = uint8(1)
gateway.device_level = uint8(1)
```

- [ ] **Step 2: Run access gateway tests and verify they fail**

Run:

```bash
go test ./internalv2/access/gateway -run 'TestOnSessionActivate|TestOnSessionClose|TestOnSessionActivationRollback' -count=1
```

Expected: FAIL because the handler does not implement online lifecycle methods.

- [ ] **Step 3: Add online dependency to handler options**

In `internalv2/access/gateway/handler.go`, add:

```go
// OnlineLifecycle is the online session lifecycle surface used by the gateway.
type OnlineLifecycle interface {
    Activate(context.Context, online.ActivateCommand) error
    Deactivate(context.Context, online.DeactivateCommand) error
}

type Options struct {
    Messages MessageUsecase
    Online   OnlineLifecycle
    SendTimeout time.Duration
}
```

Store `online OnlineLifecycle` on `Handler`. Keep nil online as a no-op so
existing SEND tests and app construction keep working before default wiring.
Move the current no-op `OnSessionClose` method out of `handler.go` when adding
the lifecycle implementation so the package has only one `OnSessionClose`
method.

- [ ] **Step 4: Implement lifecycle mapping**

Create `internalv2/access/gateway/online_lifecycle.go`:

```go
func (h *Handler) OnSessionActivate(ctx *coregateway.Context) (*frame.ConnackPacket, error) {
    if h == nil || h.online == nil {
        return nil, nil
    }
    cmd, err := mapOnlineActivateCommand(ctx)
    if err != nil {
        return nil, err
    }
    return nil, h.online.Activate(requestContext(ctx), cmd)
}

func (h *Handler) OnSessionClose(ctx coregateway.Context) error {
    return h.deactivateOnline(&ctx)
}

func (h *Handler) OnSessionActivationRollback(ctx coregateway.Context) error {
    return h.deactivateOnline(&ctx)
}
```

`mapOnlineActivateCommand` must read UID and device values from session values,
set `SessionID`, `Listener`, `ConnectedAt`, and wrap the gateway session in a
small adapter implementing `online.SessionWriter`.

For this batch, the session writer adapter may return `ErrUnsupportedFrame` from
`WriteOnline`; real outbound frame conversion belongs to the delivery/write
integration batch. The adapter must still return the correct session ID.

Add compile-time assertions near the existing handler assertions:

```go
var _ coregateway.SessionActivator = (*Handler)(nil)
var _ coregateway.SessionActivationRollback = (*Handler)(nil)
```

- [ ] **Step 5: Run access gateway tests and verify they pass**

Run:

```bash
go test ./internalv2/access/gateway -run 'TestOnSessionActivate|TestOnSessionClose|TestOnSessionActivationRollback' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run existing access gateway tests**

Run:

```bash
go test ./internalv2/access/gateway -count=1
```

Expected: PASS.

- [ ] **Step 7: Update access gateway FLOW**

Update `internalv2/access/gateway/FLOW.md`:

```text
OnSessionActivate
  -> map authenticated gateway session values into online.ActivateCommand
  -> call online.Activate before CONNACK success

OnSessionClose / OnSessionActivationRollback
  -> call online.Deactivate best-effort
```

Mention that nil online keeps the current SEND-only skeleton behavior.

- [ ] **Step 8: Commit**

```bash
git add internalv2/access/gateway/handler.go internalv2/access/gateway/online_lifecycle.go internalv2/access/gateway/online_lifecycle_test.go internalv2/access/gateway/FLOW.md
git commit -m "feat(internalv2): adapt gateway lifecycle to online usecase"
```

## Batch 2 Verification

Run:

```bash
go test ./pkg/gateway/core ./internalv2/access/gateway -count=1
go test ./pkg/gateway/... ./internalv2/access/gateway -count=1
```

Expected: PASS.

Do not run full `go test ./...` for this batch unless requested; it is broader
than the gateway lifecycle surface touched here.

## Batch 2 Acceptance

- `pkg/gateway` has an optional rollback hook for successful activation that
  cannot reach pre-open CONNACK success.
- Existing `OnSessionClose` behavior remains unchanged for sessions that never
  reached `OnSessionOpen`.
- `internalv2/access/gateway` can activate, deactivate, and rollback online
  lifecycle when an online dependency is injected.
- Nil online keeps current internalv2 SEND-only behavior.
- Default `internalv2/app.New` wiring remains unchanged until real authority RPC
  exists.
