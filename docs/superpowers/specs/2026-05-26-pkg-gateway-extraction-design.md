# Package Gateway Extraction Design

## Goal

Promote the reusable gateway infrastructure from `internal/gateway` to `pkg/gateway` so other Go projects can import the client entry runtime directly:

```go
import "github.com/WuKongIM/WuKongIM/pkg/gateway"
```

The extracted package remains a generic client gateway foundation. It owns listener binding, transport integration, protocol adaptation, session lifecycle, authentication hooks, frame dispatch, idle handling, async SEND dispatch, and gateway observation events.

## Non-Goals

- Do not move `internal/access/gateway` into `pkg/gateway`.
- Do not publicize message, presence, online, or channel business usecases.
- Do not change wire protocol behavior.
- Do not change runtime cluster semantics.
- Do not introduce a standalone module or separate repository in this phase.
- Do not add bypasses for single-node deployment; a single-node deployment remains a single-node cluster.
- Do not tune performance or alter SEND batching behavior as part of this move.

## Current Boundary

`internal/gateway` is already the generic gateway infrastructure:

- TCP and WebSocket listener lifecycle.
- WKProto, JSON-RPC, and WSMux protocol adapters.
- Session creation, values, close handling, direct outbound writes, and idle timeout.
- WKProto CONNECT authentication and optional encryption material storage.
- Frame dispatch to an injected handler.
- Gateway drain admission and session summary.

`internal/access/gateway` is not generic. It maps client frames to WuKongIM business usecases:

- `SendPacket` to `message.SendCommand`.
- `RecvackPacket` to `message.RecvAckCommand`.
- session activation/deactivation to `presence` usecases.
- channel/runtime errors to client-facing `frame.ReasonCode`.
- gateway sessions to the internal online runtime writer interface.

The extraction must preserve this split. `pkg/gateway` must not know about message, presence, online registry, or channel business rules.

## Target Package Layout

Move the existing gateway infrastructure tree to `pkg/gateway` while preserving its internal subpackage layout:

```text
pkg/
  gateway/
    FLOW.md
    auth.go
    binding/
    core/
    errors.go
    event.go
    gateway.go
    options.go
    protocol/
      protocol.go
      jsonrpc/
      wkproto/
      wsmux/
    session/
    testkit/
    transport/
      listener.go
      logging.go
      transport.go
      gnet/
    types/
    wkprotoenc/
```

The WuKongIM server-specific adapter remains:

```text
internal/
  access/
    gateway/
```

## Public API

Keep the existing top-level API names stable after the move:

```go
gateway.New(opts gateway.Options) (*gateway.Gateway, error)
(*gateway.Gateway).Start() error
(*gateway.Gateway).Stop() error
(*gateway.Gateway).ListenerAddr(name string) string
(*gateway.Gateway).SetAcceptingNewSessions(accepting bool)
(*gateway.Gateway).AcceptingNewSessions() bool
(*gateway.Gateway).SessionSummary() core.SessionSummary
```

All currently exported top-level identifiers remain part of the first-move compatibility surface unless a later focused API cleanup explicitly changes them. This includes existing aliases and constants such as:

- `Authenticator`, `AuthenticatorFunc`, and `AuthResult`.
- `Options`, `ListenerOptions`, `SessionOptions`, `TransportOptions`, and `GnetTransportOptions`.
- `Handler`, `Context`, `Observer`, gateway event types, `SendBatchItem`, `SendBatchHandler`, and `SessionActivator`.
- `CloseReason`, gateway error variables, and `SessionValue*` keys.

Keep the existing extension contracts:

```go
type Handler interface {
    OnListenerError(listener string, err error)
    OnSessionOpen(ctx Context) error
    OnFrame(ctx Context, f frame.Frame) error
    OnSessionClose(ctx Context) error
    OnSessionError(ctx Context, err error)
}

type SessionActivator interface {
    OnSessionActivate(ctx *Context) (*frame.ConnackPacket, error)
}

type SendBatchHandler interface {
    OnSendBatch(items []SendBatchItem) error
}
```

Keep the existing concrete helpers:

- `gateway.NewWKProtoAuthenticator`
- `gateway.DefaultSessionOptions`
- `gateway.NormalizeSessionOptions`
- `binding.TCPWKProto`
- `binding.WSJSONRPC`
- `binding.WSMux`
- `testkit` helpers for gateway consumers and tests

The first extraction may still expose subpackage types such as `core.SessionSummary`. A later cleanup may add top-level aliases if that improves public API ergonomics.

## Dependency Rules

`pkg/gateway/...` may depend on:

- Go standard library.
- `github.com/WuKongIM/WuKongIM/pkg/protocol/*`.
- `github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace`, if still required by current gateway core.
- `github.com/WuKongIM/WuKongIM/pkg/wklog`.
- external transport/runtime dependencies such as gnet.

`pkg/gateway/...` must not import:

- `github.com/WuKongIM/WuKongIM/internal/...`
- `internal/access/*`
- `internal/usecase/*`
- `internal/runtime/*`
- `internal/app`

`internal/access/gateway` may import `pkg/gateway` and remains responsible for translating gateway frames into WuKongIM usecase commands.

## Migration Plan

### Phase 1: Mechanical Move

Move `internal/gateway/**` to `pkg/gateway/**`.

Do not move local filesystem artifacts such as `.DS_Store`; remove them from the moved tree if encountered.

Update all imports:

```text
github.com/WuKongIM/WuKongIM/internal/gateway
  -> github.com/WuKongIM/WuKongIM/pkg/gateway

github.com/WuKongIM/WuKongIM/internal/gateway/<subpkg>
  -> github.com/WuKongIM/WuKongIM/pkg/gateway/<subpkg>
```

Do not change behavior in this phase.

### Phase 2: Server Adapter Rewire

Update `internal/access/gateway` to import `pkg/gateway` and `pkg/gateway/session`.

Keep these responsibilities inside `internal/access/gateway`:

- SEND, RECVACK, and PING business frame routing.
- message and presence command mapping.
- sendack reason mapping.
- sendtrace integration around message send and sendack write.
- online runtime session adapter.

`internal/app` remains the composition root:

```text
internal/app
  -> internal/access/gateway.Handler
  -> pkg/gateway.New
```

### Phase 3: Boundary Tests

Add or update import boundary tests so `pkg/gateway/...` cannot import any `internal/...` package.

The boundary test must inspect normal imports and test-only imports. Use `go list -json` data that includes `Imports`, `TestImports`, and `XTestImports`; failing to check test imports can leave `pkg/gateway` unit tests coupled to internal packages even though production code is clean.

Update existing server import tests:

- Keep `internal/runtime` and `internal/usecase` boundary checks.
- Remove now-obsolete references to `internal/gateway` after the directory moves.
- Allow `cmd/wkbench` or future external tools to import `pkg/gateway` if they need generic gateway test/client infrastructure.
- Continue forbidding benchmark tools from importing server internals such as `internal/app`, `internal/access`, `internal/usecase`, and `internal/runtime`.

### Phase 4: Documentation

Move `internal/gateway/FLOW.md` to `pkg/gateway/FLOW.md` and update the wording:

- `pkg/gateway` is a public reusable gateway infrastructure package.
- It does not understand WuKongIM message, presence, online, channel, slot, or controller business rules.
- Business frame handling is delegated through `Handler`.
- WuKongIM server business adaptation is implemented by `internal/access/gateway`.

Update:

- `AGENTS.md` directory structure.
- docs or tests that mention `internal/gateway` as the gateway infrastructure path.
- comments that refer to gateway package import paths.

### Phase 5: Verification

Run targeted tests first:

```bash
go test ./pkg/gateway/... -count=1
go test ./internal/access/gateway -count=1
go test ./cmd/wukongim -count=1
go test ./test/e2e/suite -count=1
go test ./internal -run TestInternalImportBoundaries -count=1
go test ./cmd/wkbench -run TestWkbenchDoesNotImportServerInternals -count=1
```

Run app-level gateway assembly tests:

```bash
go test ./internal/app -run Gateway -count=1
```

If real protocol listener behavior is touched or import rewiring affects integration harnesses, run:

```bash
go test -tags=integration ./internal/access/gateway -count=1
```

Run `go test ./...` only after targeted tests pass and the wider workspace is in a suitable state.

## Compatibility Strategy

Do not keep `internal/gateway` compatibility aliases. A real move keeps the boundary clear and avoids a public package that secretly depends on internal implementation.

This means all in-repository imports must be updated in the same change. External consumers should use `pkg/gateway` going forward.

## Risks

- Import rewrite misses can leave stale `internal/gateway` references. Mitigation: require `rg "internal/gateway"` to return only historical docs or no results after migration.
- Public API may expose subpackage types such as `core.SessionSummary`. Mitigation: accept this initially, then optionally add top-level aliases in a later focused cleanup.
- `pkg/gateway/testkit` may expose more testing helpers than a minimal public package normally would. Mitigation: document it as test support.
- Boundary checks can miss test-only imports if they only inspect `Imports`. Mitigation: assert against `Imports`, `TestImports`, and `XTestImports`.
- Large file moves can obscure behavior changes in review. Mitigation: keep migration mechanical and avoid functional edits in the same change.
- Dirty worktrees can accidentally include unrelated files. Mitigation: stage only gateway move, import rewrites, tests, and docs required for this extraction.

## Rollback

Because the extraction should be behavior-preserving, rollback is a normal revert of the migration commit. Avoid mixing this move with feature work, performance tuning, or protocol changes so the revert stays low risk.

## Success Criteria

- `pkg/gateway/...` builds and tests pass.
- `pkg/gateway/...` has zero normal or test-only imports of `internal/...`.
- `internal/access/gateway` continues to own WuKongIM message/presence adaptation.
- Existing gateway behavior and tests remain unchanged except for import paths.
- Documentation describes the new public package boundary and the server adapter boundary.
