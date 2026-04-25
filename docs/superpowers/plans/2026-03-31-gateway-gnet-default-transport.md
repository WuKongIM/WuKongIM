# Gateway gnet Default Transport Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gnet` transport support for gateway TCP and WebSocket listeners, keep `stdnet` available, and switch built-in listener presets to default to `gnet`.

**Architecture:** The implementation proceeds in layers: first tighten gateway validation and transport construction so grouped listeners are supported without changing core callback semantics, then adapt `stdnet` to the new grouped factory API, then add a shared-engine `gnet` transport where event-loop callbacks only parse and enqueue while per-connection workers call existing gateway handlers. The gateway core remains transport-agnostic and only sees logical listeners.

**Tech Stack:** Go 1.23, `internal/gateway`, `github.com/panjf2000/gnet/v2`, `github.com/gorilla/websocket`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/gateway/transport/transport.go` | Grouped transport factory API (`ListenerSpec`, `Factory.Build`) |
| `internal/gateway/core/server.go` | Group listener runtimes by transport, build logical listeners, preserve start/stop rollback |
| `internal/gateway/transport/stdnet/factory.go` | Adapt `stdnet` to grouped build semantics |
| `internal/gateway/testkit/fake_transport.go` | Fake grouped transport factory and fake listeners for core tests |
| `internal/gateway/types/options.go` | Reject duplicate listener addresses before transport build |
| `internal/gateway/types/errors.go` | Add `ErrListenerAddressDuplicate` |
| `internal/gateway/options_test.go` | Validation and default preset assertions |
| `internal/gateway/gateway.go` | Register both `stdnet` and `gnet` factories |
| `internal/gateway/binding/builtin.go` | Switch built-in presets to `gnet` |
| `internal/gateway/gateway_test.go` | End-to-end regression for default `gnet` and explicit `stdnet` |
| `internal/gateway/transport/gnet/factory.go` | Group all `gnet` listener specs into one shared engine group |
| `internal/gateway/transport/gnet/group.go` | `gnet` event-loop runtime, group lifecycle, routing, per-connection worker enqueue |
| `internal/gateway/transport/gnet/listener.go` | Logical listener handle backed by an engine group |
| `internal/gateway/transport/gnet/conn.go` | `transport.Conn` implementation for grouped `gnet` connections |
| `internal/gateway/transport/gnet/ws_handshake.go` | HTTP upgrade parsing and response generation |
| `internal/gateway/transport/gnet/ws_frame.go` | WebSocket frame parsing, control frames, outbound framing |
| `internal/gateway/transport/gnet/factory_test.go` | Group construction and shared lifecycle tests |
| `internal/gateway/transport/gnet/tcp_listener_test.go` | TCP transport behavior over `gnet` |
| `internal/gateway/transport/gnet/ws_listener_test.go` | WebSocket transport behavior over `gnet` |
| `go.mod` / `go.sum` | Add `gnet` dependency |

### Task 1: Validation and Default-Behavior Red Tests

**Files:**
- Modify: `internal/gateway/options_test.go`
- Modify: `internal/gateway/types/errors.go`
- Modify: `internal/gateway/types/options.go`
- Modify: `internal/gateway/binding/builtin.go`

- [ ] **Step 1: Write the failing validation and default tests**

Add tests that:
- reject duplicate listener addresses even when names differ
- expect the duplicate-address error to be `gatewaytypes.ErrListenerAddressDuplicate`
- expect `binding.TCPWKProto(...)` and `binding.WSJSONRPC(...)` to default to `Transport == "gnet"`
- keep explicit `Transport: "stdnet"` configurations valid

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway -run 'TestBuiltinPresetsPopulateCanonicalFields|TestOptionsValidateRejectsDuplicateListenerAddresses|TestOptionsValidateRejectsDuplicateListenerNames|TestOptionsValidateRequiresWebsocketPath' -v`

Expected: FAIL because duplicate address validation and `gnet` defaults do not exist yet.

- [ ] **Step 3: Implement the minimal validation and preset changes**

Make these changes:
- add `ErrListenerAddressDuplicate` in `internal/gateway/types/errors.go`
- in `Options.Validate()`, track normalized listener addresses and reject duplicates before transport build
- switch `binding.TCPWKProto` and `binding.WSJSONRPC` to emit `Transport: "gnet"`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway -run 'TestBuiltinPresetsPopulateCanonicalFields|TestOptionsValidateRejectsDuplicateListenerAddresses|TestOptionsValidateRejectsDuplicateListenerNames|TestOptionsValidateRequiresWebsocketPath' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/options_test.go internal/gateway/types/errors.go internal/gateway/types/options.go internal/gateway/binding/builtin.go
git commit -m "test(gateway): cover gnet defaults and duplicate addresses"
```

### Task 2: Grouped Transport API and Core Construction

**Files:**
- Modify: `internal/gateway/transport/transport.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/testkit/fake_transport.go`

- [ ] **Step 1: Write failing core tests for grouped transport construction**

Add tests that assert:
- `core.Server.Start()` calls transport factories through a grouped build path
- grouped build returns one logical listener per configured runtime
- startup rollback still stops already-started listeners if a later listener start fails
- `fake_transport` can still drive `OnOpen`, `OnData`, `OnClose`, and `OnError` through the updated API

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway/core -run 'TestServerStart|TestServerStop' -v`

Expected: FAIL because the transport factory still exposes `New(...)` instead of grouped `Build(...)`.

- [ ] **Step 3: Implement the grouped transport API and fake transport**

Make these changes:
- add `transport.ListenerSpec`
- replace `Factory.New(...)` with `Factory.Build([]ListenerSpec) ([]Listener, error)`
- update `core.Server.Start()` to group listener runtimes by transport, call `Build(...)`, assign returned logical listeners back to runtimes in input order, and preserve rollback behavior
- update `testkit.FakeTransportFactory` to build one fake listener per input spec while preserving lookup by listener name

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway/core -run 'TestServerStart|TestServerStop' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/transport/transport.go internal/gateway/core/server.go internal/gateway/core/server_test.go internal/gateway/testkit/fake_transport.go
git commit -m "refactor(gateway): add grouped transport factory API"
```

### Task 3: Adapt `stdnet` to the Grouped Factory API

**Files:**
- Modify: `internal/gateway/transport/stdnet/factory.go`
- Modify: `internal/gateway/transport/stdnet/tcp_listener_test.go`
- Modify: `internal/gateway/transport/stdnet/ws_listener_test.go`

- [ ] **Step 1: Write or update failing tests around grouped `stdnet` factory behavior**

Add a small factory-level test or extend existing transport tests so they cover:
- `stdnet.Factory.Build(...)` returns one logical listener per input spec
- unsupported networks still fail with the existing error behavior

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway/transport/stdnet -v`

Expected: FAIL because `stdnet.Factory` still implements `New(...)`.

- [ ] **Step 3: Implement the minimal `stdnet` grouped build adapter**

Implement `Build(...)` by iterating input specs and constructing TCP or WebSocket listeners exactly as today. Do not change runtime behavior beyond the factory signature.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway/transport/stdnet -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/transport/stdnet/factory.go internal/gateway/transport/stdnet/tcp_listener_test.go internal/gateway/transport/stdnet/ws_listener_test.go
git commit -m "refactor(gateway): adapt stdnet transport factory"
```

### Task 4: Add `gnet` Dependency and Shared-Group Factory Skeleton

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/gateway/transport/gnet/factory.go`
- Create: `internal/gateway/transport/gnet/listener.go`
- Create: `internal/gateway/transport/gnet/factory_test.go`

- [ ] **Step 1: Write the failing grouped-factory tests for `gnet`**

Add tests that assert:
- all `gnet` listener specs in one gateway instance are assigned to one engine group
- `Build(...)` preserves logical-listener ordering
- the returned logical listeners each expose their own address and share the same underlying group

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway/transport/gnet -run TestFactory -v`

Expected: FAIL because the package and dependency do not exist yet.

- [ ] **Step 3: Add dependency and implement the factory skeleton**

Make these changes:
- add `github.com/panjf2000/gnet/v2` to `go.mod`
- implement a `gnet` factory that groups all input specs into a single engine group
- add `listenerHandle` objects that satisfy `transport.Listener` and reference the same group
- leave event-loop details for the next tasks; this task only establishes grouping and lifecycle scaffolding

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway/transport/gnet -run TestFactory -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/gateway/transport/gnet/factory.go internal/gateway/transport/gnet/listener.go internal/gateway/transport/gnet/factory_test.go
git commit -m "feat(gateway): add gnet grouped factory skeleton"
```

### Task 5: Implement Shared-Engine `gnet` TCP Transport

**Files:**
- Create: `internal/gateway/transport/gnet/group.go`
- Create: `internal/gateway/transport/gnet/conn.go`
- Create: `internal/gateway/transport/gnet/tcp_listener_test.go`

- [ ] **Step 1: Write failing TCP transport tests**

Add tests that verify:
- a `gnet` TCP listener can start, accept a client, and deliver bytes to `ConnHandler.OnData`
- multiple TCP logical listeners share one engine group but remain independently addressable
- stopping one logical listener does not stop the shared group while another logical listener is still running
- the last logical stop shuts the group down

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway/transport/gnet -run TestTCP -v`

Expected: FAIL because the shared engine group and `transport.Conn` implementation do not exist yet.

- [ ] **Step 3: Implement minimal shared-engine TCP behavior**

Implement:
- engine-group start/stop reference counting
- one per-connection worker queue that serializes `OnOpen`, `OnData`, and `OnClose`
- `gnet` event callbacks that only drain inbound bytes, copy payloads, enqueue worker events, and use async writes
- `stateConn` with correct `ID`, `LocalAddr`, `RemoteAddr`, `Write`, and `Close` behavior for TCP mode

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway/transport/gnet -run TestTCP -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/transport/gnet/group.go internal/gateway/transport/gnet/conn.go internal/gateway/transport/gnet/tcp_listener_test.go
git commit -m "feat(gateway): add gnet tcp transport"
```

### Task 6: Implement `gnet` WebSocket Handshake and Frame Transport

**Files:**
- Create: `internal/gateway/transport/gnet/ws_handshake.go`
- Create: `internal/gateway/transport/gnet/ws_frame.go`
- Modify: `internal/gateway/transport/gnet/group.go`
- Modify: `internal/gateway/transport/gnet/conn.go`
- Create: `internal/gateway/transport/gnet/ws_listener_test.go`

- [ ] **Step 1: Write failing WebSocket transport tests**

Add tests that verify:
- successful HTTP upgrade on the configured path
- path mismatch rejection
- invalid upgrade method rejection
- missing or invalid `Upgrade` / `Connection` headers rejection
- missing or invalid `Sec-WebSocket-Key` rejection
- unsupported WebSocket version rejection
- handshake failures invoke the listener error callback before the connection closes
- text and binary messages are delivered to `ConnHandler.OnData`
- unmasked client frames are rejected
- ping/pong and close frames are handled correctly
- fragmented inbound messages are reassembled before delivery
- outbound `transport.Conn.Write(...)` on a WebSocket connection produces a valid WebSocket frame on the wire
- multiple logical WebSocket listeners that share one `gnet` group route traffic by bound address correctly

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway/transport/gnet -run TestWS -v`

Expected: FAIL because the transport has no upgrade or frame parser yet.

- [ ] **Step 3: Implement minimal WebSocket support inside `transport/gnet`**

Implement:
- HTTP upgrade parsing and `101 Switching Protocols` response generation
- handshake validation for method, required upgrade headers, `Sec-WebSocket-Key`, and supported version
- per-connection mode switch from `ws_handshake` to `ws_frames`
- WebSocket frame decode, mandatory client masking checks, fragmentation reassembly, control-frame handling, and close sequencing
- outbound WebSocket frame encoding inside `stateConn.Write()`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway/transport/gnet -run TestWS -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/transport/gnet/ws_handshake.go internal/gateway/transport/gnet/ws_frame.go internal/gateway/transport/gnet/group.go internal/gateway/transport/gnet/conn.go internal/gateway/transport/gnet/ws_listener_test.go
git commit -m "feat(gateway): add gnet websocket transport"
```

### Task 7: Register `gnet` and Add Gateway Regressions

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/options_test.go`

- [ ] **Step 1: Write failing gateway integration tests**

Add tests that verify:
- `gateway.New(...)` registers both `stdnet` and `gnet`
- the built-in TCP and WebSocket presets work without explicitly naming a transport and therefore run through `gnet`
- explicitly configured `stdnet` listeners still start and pass the current end-to-end TCP and WebSocket smoke tests

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/gateway -run 'TestGatewayStartStop|TestBuiltinPresetsPopulateCanonicalFields' -v`

Expected: FAIL because `gateway.New(...)` only registers `stdnet` and the default presets do not yet route through a working `gnet` transport.

- [ ] **Step 3: Implement the registration and integration changes**

Make these changes:
- register both `stdnet.NewFactory()` and `gnet.NewFactory()`
- keep explicit `stdnet` support unchanged
- update integration tests to cover both default `gnet` and explicit `stdnet`

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/gateway -run 'TestGatewayStartStop|TestBuiltinPresetsPopulateCanonicalFields' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/gateway_test.go internal/gateway/options_test.go
git commit -m "feat(gateway): default built-in listeners to gnet"
```

### Task 8: Full Verification

**Files:**
- Test: `internal/gateway/...`

- [ ] **Step 1: Run the gateway-focused test suite**

Run: `go test ./internal/gateway/... -v`

Expected: PASS

- [ ] **Step 2: Run a broader regression that covers transport consumers if needed**

Run: `go test ./...`

Expected: PASS, or investigate and fix any regressions caused by the transport API change.

- [ ] **Step 3: Commit the final verified state**

```bash
git add go.mod go.sum internal/gateway
git commit -m "feat(gateway): add gnet transport support"
```
