# Access / Usecase / Runtime Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `internal/service` with a clearer long-term application structure based on `internal/access`, `internal/usecase`, and `internal/runtime`, while preserving current gateway message behavior during the migration.

**Architecture:** The migration is staged. First extract node-local runtime primitives out of `internal/service` so registry, delivery, and sequencing have their own stable home. Then move message behavior behind ingress-agnostic commands/results in `internal/usecase/message`. Next replace the gateway-facing `Service` implementation with a thin adapter in `internal/access/gateway`. Finally rewire `internal/app` to compose explicit collaborators and delete `internal/service` entirely.

**Tech Stack:** Go 1.23, `internal/gateway`, `pkg/wkpacket`, `pkg/wkstore`, `pkg/wkcluster`, `testing`, existing gateway-backed integration tests.

**Spec:** `docs/superpowers/specs/2026-04-01-access-usecase-runtime-design.md`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/runtime/online/types.go` | `OnlineConn` and runtime-facing connection metadata |
| `internal/runtime/online/registry.go` | thread-safe online connection registry |
| `internal/runtime/online/delivery.go` | local realtime delivery to online connections |
| `internal/runtime/sequence/allocator.go` | in-memory message ID and channel sequence allocation |
| `internal/usecase/message/deps.go` | message-usecase dependency interfaces |
| `internal/usecase/message/command.go` | ingress-agnostic send and recvack commands |
| `internal/usecase/message/result.go` | use-case result contracts such as send result |
| `internal/usecase/message/app.go` | message use-case constructor and collaborator storage |
| `internal/usecase/message/send.go` | send-message orchestration |
| `internal/usecase/message/recvack.go` | recvack handling |
| `internal/access/gateway/lifecycle.go` | gateway session open/close mapping into runtime registration |
| `internal/access/gateway/mapper.go` | `wkpacket` <-> use-case command/result translation |
| `internal/access/gateway/frame_router.go` | gateway frame routing |
| `internal/access/gateway/handler.go` | `gateway.Handler` implementation |
| `internal/app/build.go` | explicit runtime/usecase/access composition |
| `internal/service/*` | deleted after migration |

## Task 1: Extract node-local runtime primitives

**Files:**
- Create: `internal/runtime/online/types.go`
- Create: `internal/runtime/online/registry.go`
- Create: `internal/runtime/online/delivery.go`
- Create: `internal/runtime/sequence/allocator.go`
- Create: `internal/runtime/online/registry_test.go`
- Create: `internal/runtime/online/delivery_test.go`
- Create: `internal/runtime/sequence/allocator_test.go`

- [ ] **Step 1: Write the failing runtime tests**

Add tests equivalent to the current service-level coverage:
- register, overwrite, lookup, and unregister online connections
- list connections by UID in stable order
- local delivery continues after one recipient write failure
- sequence allocator increments message IDs and per-channel sequences independently

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/runtime/... -v`

Expected: FAIL because the new runtime packages do not exist yet.

- [ ] **Step 3: Move registry, delivery, and sequence logic into runtime packages**

Implementation notes:
- rename `SessionMeta` to `OnlineConn`
- rename `SessionRegistry` to `OnlineRegistry`
- rename `SessionsByUID` to `ConnectionsByUID`
- keep the current semantics of register, unregister, delivery error handling, and in-memory sequence allocation
- keep the runtime packages free of gateway handler logic

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/runtime/... -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/online internal/runtime/sequence
git commit -m "refactor(runtime): extract online and sequence primitives"
```

## Task 2: Build ingress-agnostic message use cases

**Files:**
- Create: `internal/usecase/message/deps.go`
- Create: `internal/usecase/message/command.go`
- Create: `internal/usecase/message/result.go`
- Create: `internal/usecase/message/app.go`
- Create: `internal/usecase/message/send.go`
- Create: `internal/usecase/message/recvack.go`
- Create: `internal/usecase/message/send_test.go`
- Create: `internal/usecase/message/recvack_test.go`

- [ ] **Step 1: Write the failing message-usecase tests**

Add tests covering:
- unauthenticated send command rejection
- unsupported channel-type handling
- local person-send success
- no-local-recipient path returning the current not-on-node reason
- delivery failure producing the current system-error reason

The tests should call `message.App.Send(...)` directly without importing `internal/gateway`.

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/usecase/message -v`

Expected: FAIL because the message package does not exist yet.

- [ ] **Step 3: Implement `SendCommand`, `SendResult`, and `MessageApp`**

Implementation notes:
- do not accept `*wkpacket.SendPacket` as the main use-case input
- accept an ingress-agnostic command such as `SendCommand`
- use collaborators for identity lookup, channel lookup, online registry, delivery, sequencing, and clock
- preserve current send behavior for person channels
- keep `RecvAck` explicit even if it remains a no-op in the first pass

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/usecase/message -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message
git commit -m "refactor(message): introduce ingress-agnostic usecases"
```

## Task 3: Replace service with a thin gateway adapter

**Files:**
- Create: `internal/access/gateway/lifecycle.go`
- Create: `internal/access/gateway/mapper.go`
- Create: `internal/access/gateway/frame_router.go`
- Create: `internal/access/gateway/handler.go`
- Create: `internal/access/gateway/handler_test.go`
- Create: `internal/access/gateway/router_test.go`

- [ ] **Step 1: Write the failing gateway-adapter tests**

Add tests covering:
- session open registers an online connection
- session close unregisters the connection
- `SendPacket` is mapped to `SendCommand` and the result is mapped back to `SendackPacket`
- `RecvackPacket` routes into the message use case
- unsupported frame returns `ErrUnsupportedFrame`

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/access/gateway -v`

Expected: FAIL because the adapter package does not exist yet.

- [ ] **Step 3: Implement the gateway adapter**

Implementation notes:
- `handler.go` should own the `gateway.Handler` implementation
- `lifecycle.go` should contain auth-derived connection extraction from `gateway.Context`
- `frame_router.go` should switch on `wkpacket.Frame`
- `mapper.go` should translate between `wkpacket` objects and message commands/results
- do not let `internal/usecase/message` import `internal/gateway`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/access/gateway -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/gateway
git commit -m "refactor(gateway): replace service with adapter layer"
```

## Task 4: Rewire `internal/app` to explicit collaborators

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/lifecycle_test.go`

- [ ] **Step 1: Write the failing app-construction tests**

Add or update tests to prove:
- app builds runtime collaborators explicitly
- app constructs message use cases and gateway adapter without `internal/service`
- existing startup and shutdown order is preserved

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/app -v`

Expected: FAIL because `internal/app` still builds `internal/service`.

- [ ] **Step 3: Update build wiring**

Implementation notes:
- build `runtime/online` registry
- build `runtime/sequence` allocator
- build `usecase/message.App`
- build `access/gateway.Handler`
- pass the gateway adapter to `gateway.New`
- remove the `service *service.Service` field and accessor from `App`
- if a read-only accessor is still needed, expose the message app or gateway adapter explicitly instead of reintroducing a generic service bucket

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/app -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "refactor(app): compose access usecase runtime layers"
```

## Task 5: Delete `internal/service` and migrate remaining tests

**Files:**
- Delete: `internal/service/errors.go`
- Delete: `internal/service/options.go`
- Delete: `internal/service/session_state.go`
- Delete: `internal/service/registry.go`
- Delete: `internal/service/delivery.go`
- Delete: `internal/service/sequence.go`
- Delete: `internal/service/send.go`
- Delete: `internal/service/recvack.go`
- Delete: `internal/service/frame_router.go`
- Delete: `internal/service/handler.go`
- Delete: `internal/service/service.go`
- Delete or move: `internal/service/*_test.go`
- Delete or move: `internal/service/testkit/*`

- [ ] **Step 1: Move or rewrite remaining tests under the new packages**

Migration rules:
- runtime-focused tests move under `internal/runtime/...`
- message behavior tests move under `internal/usecase/message`
- gateway-handler and mapper tests move under `internal/access/gateway`
- gateway-backed end-to-end coverage should remain, but it should target the new adapter wiring

- [ ] **Step 2: Run the full targeted package set**

Run: `go test ./internal/runtime/... ./internal/usecase/message ./internal/access/gateway ./internal/app -v`

Expected: PASS

- [ ] **Step 3: Delete the old package**

Remove the entire `internal/service` tree once no imports remain.

- [ ] **Step 4: Run the full repository tests needed for confidence**

Run: `go test ./internal/... ./pkg/...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime internal/usecase internal/access internal/app
git rm -r internal/service
git commit -m "refactor: replace internal service with layered architecture"
```

## Guardrails

- Do not create a replacement fat `Service` object under a different package name.
- Do not let `usecase/message` import `internal/gateway`.
- Do not let use cases accept wire models like `*wkpacket.SendPacket` as their primary API.
- Do not create empty future packages such as `access/api` until they have real code.
- Preserve current behavior before broadening message semantics.

## Completion Criteria

- `internal/service` no longer exists
- gateway-specific knowledge is contained under `internal/access/gateway`
- message use cases are directly testable without gateway imports
- runtime online primitives are reusable and named independently from gateway session vocabulary
- `internal/app` assembles explicit collaborators instead of one umbrella service object
