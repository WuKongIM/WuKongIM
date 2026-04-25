# Gateway Module Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/gateway` as a transport- and protocol-pluggable frontend gateway that accepts `TCP -> wkproto` and `WebSocket -> jsonrpc`, exposing only `wkpacket.Frame` to upstream logic.

**Architecture:** The implementation is split into four layers: top-level gateway API, session/core orchestration, pluggable protocol adapters, and pluggable transport adapters. The first version uses a standard-library-driven transport baseline for TCP plus HTTP/WebSocket upgrade handling, while keeping `gnet` out of core-facing packages so it can be added later without redesign.

**Tech Stack:** Go 1.23, `net`, `net/http`, `context`, `sync`, existing `pkg/wkproto`, `pkg/jsonrpc`, `pkg/wkpacket`, `github.com/gorilla/websocket`, `stretchr/testify`.

**Spec:** `docs/superpowers/specs/2026-03-31-gateway-design.md`

---

## File Map

### New files

| File | Responsibility |
|------|----------------|
| `internal/gateway/errors.go` | Shared gateway errors and close reasons |
| `internal/gateway/options.go` | `Options`, `ListenerOptions`, validation |
| `internal/gateway/event.go` | `Handler`, `Context`, `Context.WriteFrame`, outbound metadata helpers |
| `internal/gateway/gateway.go` | Public `Gateway` entrypoint (`New`, `Start`, `Stop`) |
| `internal/gateway/options_test.go` | Option validation and built-in preset coverage |
| `internal/gateway/core/registry.go` | Transport/protocol registry and listener preset lookup |
| `internal/gateway/core/registry_test.go` | Registry lookup and duplicate registration tests |
| `internal/gateway/core/dispatcher.go` | Thin wrapper around `gateway.Handler` callbacks |
| `internal/gateway/core/server.go` | Session lifecycle orchestration and connection event handling |
| `internal/gateway/core/server_test.go` | Core lifecycle, error propagation, reply token, buffer limit tests |
| `internal/gateway/session/session.go` | Session implementation, write queue, write options |
| `internal/gateway/session/manager.go` | Session index and close bookkeeping |
| `internal/gateway/session/manager_test.go` | Manager add/remove/get and single-close semantics |
| `internal/gateway/transport/transport.go` | `Factory`, `Listener`, `Conn`, `ConnHandler` interfaces |
| `internal/gateway/transport/listener.go` | Shared transport listener config types |
| `internal/gateway/transport/stdnet/factory.go` | Concrete `stdnet` transport factory that selects TCP or WebSocket by `Network` |
| `internal/gateway/transport/stdnet/conn.go` | `transport.Conn` adapter for TCP and WebSocket connections |
| `internal/gateway/transport/stdnet/tcp_listener.go` | TCP listener implementation |
| `internal/gateway/transport/stdnet/tcp_listener_test.go` | TCP listener start/stop/data path tests |
| `internal/gateway/transport/stdnet/ws_listener.go` | HTTP + WebSocket listener implementation |
| `internal/gateway/transport/stdnet/ws_listener_test.go` | WebSocket upgrade and message delivery tests |
| `internal/gateway/protocol/protocol.go` | `protocol.Adapter` interface |
| `internal/gateway/protocol/wkproto/adapter.go` | `wkproto` protocol adapter |
| `internal/gateway/protocol/wkproto/adapter_test.go` | Sticky/partial frame decode and encode tests |
| `internal/gateway/protocol/jsonrpc/adapter.go` | `jsonrpc` protocol adapter |
| `internal/gateway/protocol/jsonrpc/adapter_test.go` | Request decode, reply token, response/notification encode tests |
| `internal/gateway/binding/binding.go` | Listener preset helpers and preset registration surface |
| `internal/gateway/binding/builtin.go` | `TCPWKProto` and `WSJSONRPC` convenience constructors |
| `internal/gateway/testkit/fake_transport.go` | Fake transport factory, listener, conn for core tests |
| `internal/gateway/testkit/fake_protocol.go` | Fake protocol adapter for core tests |
| `internal/gateway/testkit/fake_session.go` | Minimal session helper for protocol adapter tests |
| `internal/gateway/testkit/fake_handler.go` | Recording handler used in core tests |
| `internal/gateway/testkit/helpers_test.go` | Shared test-only helpers such as `WaitUntil`, `MustSendWKProtoPing`, and `JSONContains` |
| `internal/gateway/gateway_test.go` | End-to-end gateway smoke tests for `tcp->wkproto` and `ws->jsonrpc` |

### Modified files

| File | Change |
|------|--------|
| `go.mod` | Add `github.com/gorilla/websocket` for the first WebSocket transport implementation |
| `go.sum` | Dependency checksum updates |

### Deferred files

These are intentionally excluded from the first implementation plan:

- `internal/gateway/transport/gnet/*`

The abstractions are shaped so that `gnet` can be added in a follow-up without changing `core`, `session`, `protocol`, or the public `internal/gateway` API.

---

## Task 1: Public gateway foundation

**Files:**
- Create: `internal/gateway/errors.go`
- Create: `internal/gateway/options.go`
- Create: `internal/gateway/event.go`
- Create: `internal/gateway/options_test.go`
- Create: `internal/gateway/binding/binding.go`
- Create: `internal/gateway/binding/builtin.go`

- [ ] **Step 1: Write failing option and preset tests**

```go
package gateway_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
)

func TestOptionsValidateRejectsDuplicateListenerNames(t *testing.T) {
	opts := gateway.Options{
		Listeners: []gateway.ListenerOptions{
			{Name: "dup", Network: "tcp", Address: ":5100", Transport: "stdnet", Protocol: "wkproto"},
			{Name: "dup", Network: "websocket", Address: ":5200", Transport: "stdnet", Protocol: "jsonrpc"},
		},
	}
	if err := opts.Validate(); err == nil {
		t.Fatal("expected duplicate listener validation error")
	}
}

func TestBuiltinPresetsPopulateCanonicalFields(t *testing.T) {
	tcp := binding.TCPWKProto("tcp-wkproto", ":5100")
	if tcp.Network != "tcp" || tcp.Transport != "stdnet" || tcp.Protocol != "wkproto" {
		t.Fatalf("unexpected tcp preset: %+v", tcp)
	}

	ws := binding.WSJSONRPC("ws-jsonrpc", ":5200")
	if ws.Network != "websocket" || ws.Transport != "stdnet" || ws.Protocol != "jsonrpc" || ws.Path != binding.DefaultWSPath {
		t.Fatalf("unexpected ws preset: %+v", ws)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway -run 'TestOptionsValidate|TestBuiltinPresets' -v`
Expected: FAIL because `Options`, `Validate`, and preset helpers do not exist yet.

- [ ] **Step 3: Implement public types and validation**

```go
package gateway

type Options struct {
	Handler        Handler
	DefaultSession SessionOptions
	Listeners      []ListenerOptions
}

type ListenerOptions struct {
	Name      string
	Network   string
	Address   string
	Path      string
	Transport string
	Protocol  string
}

type SessionOptions struct {
	ReadBufferSize      int
	WriteQueueSize      int
	MaxInboundBytes     int
	MaxOutboundBytes    int
	IdleTimeout         time.Duration
	WriteTimeout        time.Duration
	CloseOnHandlerError bool
}
```

Implementation notes:
- Add `Options.Validate()` to reject nil handlers, blank names, duplicate listener names, missing addresses, and blank `Network/Transport/Protocol`
- Require `Path` for `Network == "websocket"` and ignore it for TCP listeners
- Add `DefaultSessionOptions()` returning `CloseOnHandlerError: true` plus sane buffer and timeout defaults, then have `Options.Validate()` normalize `Options.DefaultSession`
- Add `CloseReason` constants plus gateway-scoped lifecycle errors in `internal/gateway/errors.go`
- Define `Handler` and `Context` in `internal/gateway/event.go`
- Keep `WriteOption`, `OutboundMeta`, and `WithReplyToken` in `internal/gateway/session/session.go` as the canonical owner of write metadata
- Make `Context.WriteFrame` a convenience wrapper that delegates to `ctx.Session.WriteFrame(frame, session.WithReplyToken(ctx.ReplyToken))`
- `binding.TCPWKProto` and `binding.WSJSONRPC` should return fully populated `gateway.ListenerOptions` values, not partial presets
- `binding` should define `DefaultWSPath = "/ws"` and make `WSJSONRPC` populate that path automatically
- Keep session defaults at the top-level `Options` scope in the first version; per-listener session overrides are deferred so `ListenerOptions` remains aligned with the spec's listener-responsibility boundary
- Keep queue/backpressure errors such as `ErrWriteQueueFull` and `ErrOutboundOverflow` in the `session` package to avoid a `gateway -> session -> gateway` import cycle

- [ ] **Step 4: Run tests to verify the package foundation passes**

Run: `go test ./internal/gateway ./internal/gateway/binding -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/errors.go internal/gateway/options.go internal/gateway/event.go internal/gateway/options_test.go internal/gateway/binding/binding.go internal/gateway/binding/builtin.go
git commit -m "feat(gateway): add public gateway foundation"
```

---

## Task 2: Session implementation and lifecycle manager

**Files:**
- Create: `internal/gateway/session/session.go`
- Create: `internal/gateway/session/manager.go`
- Create: `internal/gateway/session/manager_test.go`

- [ ] **Step 1: Write failing manager and session tests**

```go
package session

import (
	"testing"

)

func TestManagerAddGetRemove(t *testing.T) {
	mgr := NewManager()
	s := newTestSession(1, "tcp-wkproto")

	mgr.Add(s)
	if got, ok := mgr.Get(1); !ok || got.ID() != 1 {
		t.Fatalf("expected session 1, got ok=%v session=%v", ok, got)
	}

	mgr.Remove(1)
	if _, ok := mgr.Get(1); ok {
		t.Fatal("expected session removal")
	}
}

func TestSessionCloseOnce(t *testing.T) {
	s := newTestSession(1, "tcp-wkproto")
	if err := s.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close should be idempotent: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/session -run 'TestManager|TestSession' -v`
Expected: FAIL because session manager types do not exist yet.

- [ ] **Step 3: Implement session state and manager**

```go
package session

type Session interface {
	ID() uint64
	Listener() string
	RemoteAddr() string
	LocalAddr() string

	WriteFrame(frame wkpacket.Frame, opts ...WriteOption) error
	Close() error

	SetValue(key string, value any)
	Value(key string) any
}
```

Implementation notes:
- Implement an internal concrete session struct with:
  - immutable identity fields
  - `sync.Map` or guarded map for connection-local values
  - bounded `writeCh`
  - queued outbound byte accounting
  - an injected `writeFrameFn(frame wkpacket.Frame, meta OutboundMeta) error`
  - a `closeOnce` guard
- `manager.go` should provide `Add`, `Get`, `Remove`, `Range`, and `Count`
- Keep `newTestSession` in `manager_test.go` or a package-internal test helper file so the production API stays small
- Define `ErrWriteQueueFull` and `ErrOutboundOverflow` in the `session` package, not in the root `gateway` package

- [ ] **Step 4: Add write-path helpers used later by core**

Implementation notes:
- `Session.WriteFrame` should only collect `WriteOption`s and delegate to the injected `writeFrameFn`
- Add a non-exported `enqueueEncoded(payload []byte)` method that:
  - rejects queue-full with `session.ErrWriteQueueFull`
  - rejects outbound-byte overflow with `session.ErrOutboundOverflow`
- Keep actual byte encoding out of the session package; core owns `protocol.Encode`, while session owns only queue accounting and encoded-byte delivery
- Decrement `MaxOutboundBytes` accounting when the writer loop removes a payload from the queue, regardless of whether the subsequent socket write succeeds, so queued-byte accounting reflects only bytes still waiting in memory

- [ ] **Step 5: Run tests to verify the session layer passes**

Run: `go test ./internal/gateway/session -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/session/session.go internal/gateway/session/manager.go internal/gateway/session/manager_test.go
git commit -m "feat(gateway): add session and manager layer"
```

---

## Task 3: Transport/protocol abstractions, registry, and testkit

**Files:**
- Create: `internal/gateway/transport/transport.go`
- Create: `internal/gateway/transport/listener.go`
- Create: `internal/gateway/protocol/protocol.go`
- Create: `internal/gateway/core/registry.go`
- Create: `internal/gateway/core/registry_test.go`
- Create: `internal/gateway/testkit/fake_transport.go`
- Create: `internal/gateway/testkit/fake_protocol.go`
- Create: `internal/gateway/testkit/fake_session.go`
- Create: `internal/gateway/testkit/fake_handler.go`

- [ ] **Step 1: Write failing registry tests**

```go
package core_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/core"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
)

func TestRegistryRejectsDuplicateTransportFactory(t *testing.T) {
	r := core.NewRegistry()
	if err := r.RegisterTransport(testkit.NewFakeTransportFactory("fake")); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := r.RegisterTransport(testkit.NewFakeTransportFactory("fake")); err == nil {
		t.Fatal("expected duplicate transport registration error")
	}
}

func TestRegistryResolvesProtocolByName(t *testing.T) {
	r := core.NewRegistry()
	adapter := testkit.NewFakeProtocol("fake-proto")
	if err := r.RegisterProtocol(adapter); err != nil {
		t.Fatalf("register protocol failed: %v", err)
	}
	got, err := r.Protocol("fake-proto")
	if err != nil || got.Name() != "fake-proto" {
		t.Fatalf("unexpected protocol lookup: adapter=%v err=%v", got, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/core -run TestRegistry -v`
Expected: FAIL because the registry and fake adapters do not exist yet.

- [ ] **Step 3: Implement minimal interfaces and registry**

```go
package transport

type Factory interface {
	Name() string
	New(opts ListenerOptions, handler ConnHandler) (Listener, error)
}

type Listener interface {
	Start() error
	Stop() error
	Addr() string
}

type ConnHandler interface {
	OnOpen(conn Conn) error
	OnData(conn Conn, data []byte) error
	OnClose(conn Conn, err error)
}
```

```go
package transport

type ListenerOptions struct {
	Name    string
	Network string
	Address string
	Path    string

	OnError func(error)
}
```

```go
package protocol

type Adapter interface {
	Name() string
	Decode(session session.Session, in []byte) ([]wkpacket.Frame, int, error)
	Encode(session session.Session, frame wkpacket.Frame, meta session.OutboundMeta) ([]byte, error)
	OnOpen(session session.Session) error
	OnClose(session session.Session) error
}
```

```go
package protocol

type ReplyTokenTracker interface {
	TakeReplyTokens(session session.Session, count int) []string
}
```

Implementation notes:
- `core.Registry` should store named transport factories and named protocol adapters
- Duplicate names must return deterministic errors
- Keep registry logic free of default globals; the top-level gateway can assemble defaults later
- `protocol.ReplyTokenTracker` is an optional internal side channel used by protocols like `jsonrpc` to surface request IDs without changing the spec-defined `protocol.Adapter` signature
- `protocol.ReplyTokenTracker` is an internal implementation bridge for JSON-RPC correlation, not a new public gateway contract
- When `ReplyTokenTracker.TakeReplyTokens(session, count)` is used, it must return a slice aligned 1:1 with the decoded frames in that same order, using empty strings for frames that do not carry request IDs

- [ ] **Step 4: Add fakes used by core tests**

Implementation notes:
- `fake_transport.go` should expose a controllable fake listener and fake conn that can:
  - emit `OnOpen`
  - emit inbound data
  - record writes
  - emit close events
  - emit listener-scoped errors through `transport.ListenerOptions.OnError`
- `fake_protocol.go` should expose configurable decode outputs and errors
- `fake_session.go` should provide `NewProtocolSession()` and any minimal helpers needed by protocol adapter tests without pulling in the full core server
- `fake_handler.go` should record callback order, reply tokens, close reasons, and errors

- [ ] **Step 5: Run tests to verify abstractions and registry pass**

Run: `go test ./internal/gateway/core -run TestRegistry -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/transport/transport.go internal/gateway/transport/listener.go internal/gateway/protocol/protocol.go internal/gateway/core/registry.go internal/gateway/core/registry_test.go internal/gateway/testkit/fake_transport.go internal/gateway/testkit/fake_protocol.go internal/gateway/testkit/fake_session.go internal/gateway/testkit/fake_handler.go
git commit -m "feat(gateway): add registry and adapter abstractions"
```

---

## Task 4: Core server orchestration with in-memory tests

**Files:**
- Create: `internal/gateway/core/dispatcher.go`
- Create: `internal/gateway/core/server.go`
- Create: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write failing core orchestration tests**

```go
package core_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/core"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
)

func TestServerDeliversDecodedFramesToHandler(t *testing.T) {
	reg := core.NewRegistry()
	tf := testkit.NewFakeTransportFactory("fake")
	pf := testkit.NewFakeProtocol("wkproto")
	handler := testkit.NewRecordingHandler()

	if err := reg.RegisterTransport(tf); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterProtocol(pf); err != nil {
		t.Fatal(err)
	}

	srv, err := core.NewServer(gateway.Options{
		Handler: handler,
		Listeners: []gateway.ListenerOptions{
			{Name: "tcp-wkproto", Network: "tcp", Address: "fake://listener", Transport: "fake", Protocol: "wkproto"},
		},
	}, reg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	pf.DecodedFrames = []wkpacket.Frame{&wkpacket.PingPacket{}}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tf.MustOpen("tcp-wkproto", 1)
	tf.MustData(1, []byte{0xC0})

	if handler.FrameCount() != 1 {
		t.Fatalf("expected one frame, got %d", handler.FrameCount())
	}
}
```

Add companion tests for:
- listener `Start()` failure is reported through `OnListenerError`
- `OnListenerError` receives accept/upgrade-style errors before session creation
- partial inbound data does not dispatch until a complete frame is available
- inbound overflow closes with `CloseReasonInboundOverflow`
- handler error closes when `CloseOnHandlerError` is true
- reply token from protocol decode is visible to the handler context
- protocol decode failure closes with `CloseReasonProtocolError`
- `OnSessionError` fires before close on protocol decode failure
- `OnSessionError` fires before close on queue full and outbound overflow
- peer close maps to `CloseReasonPeerClosed`
- `Stop()` maps active sessions to `CloseReasonServerStop`
- close callback fires once

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/core -run TestServer -v`
Expected: FAIL because `core.Server` does not exist yet.

- [ ] **Step 3: Implement dispatcher and core server**

Implementation notes:
- `dispatcher.go` should centralize calls to `Handler` and create per-callback `gateway.Context` snapshots
- `server.go` should:
  - validate options up front
  - create transport listeners from the registry
  - pass `transport.ListenerOptions.OnError = func(err error) { handler.OnListenerError(listenerName, err) }`
  - report any `listener.Start()` error through `OnListenerError` before returning it from `Start()`
  - create sessions on `ConnHandler.OnOpen` using normalized `Options.DefaultSession`
  - append inbound bytes and enforce `MaxInboundBytes`
  - loop on `protocol.Decode` until no further progress is made
  - call protocol `OnOpen`/`OnClose`
  - inject a session `writeFrameFn` that performs `protocol.Encode` followed by `session.enqueueEncoded`
  - report session-scoped transport/protocol/policy/handler failures through `OnSessionError` before closing

Core decode loop sketch:

```go
for {
	frames, consumed, err := adapter.Decode(sess, inbound)
	if err != nil {
		return closeWithProtocolError(err)
	}
	if consumed == 0 && len(frames) == 0 {
		break
	}
	inbound = inbound[consumed:]
	tokens := takeReplyTokens(adapter, sess, len(frames))
	for i, frame := range frames {
		replyToken := ""
		if i < len(tokens) {
			replyToken = tokens[i]
		}
		if err := dispatchFrame(frame, replyToken); err != nil {
			return closeWithHandlerError(err)
		}
	}
}
```

- [ ] **Step 4: Add writer loop wiring inside the core/session boundary**

Implementation notes:
- Start one goroutine per session that drains queued encoded payloads to the underlying `transport.Conn`
- If `transport.Conn.Write` fails, surface it as a session-scoped transport error and close once
- Make `Stop()` close listeners first, then active sessions, then wait for goroutines

- [ ] **Step 5: Run tests to verify the in-memory core passes**

Run: `go test ./internal/gateway/core -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/core/dispatcher.go internal/gateway/core/server.go internal/gateway/core/server_test.go
git commit -m "feat(gateway): implement core server orchestration"
```

---

## Task 5: Timeout behavior and handler-error defaults

**Files:**
- Modify: `internal/gateway/options.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/session/session.go`

- [ ] **Step 1: Write failing timeout tests**

Add tests for:
- idle timeout closes an inactive session with `CloseReasonIdleTimeout`
- write timeout turns a blocked connection write into a session-scoped transport error plus close
- `DefaultSessionOptions()` yields `CloseOnHandlerError == true`

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/core -run 'TestIdleTimeout|TestWriteTimeout|TestDefaultSessionOptions' -v`
Expected: FAIL because timeout behavior is not implemented yet.

- [ ] **Step 3: Implement idle timeout**

Implementation notes:
- Track last-read and last-write activity timestamps per session
- Use a ticker or timer per session to close inactive sessions once `IdleTimeout` elapses
- Treat disabled or zero timeout values as “use normalized defaults”

- [ ] **Step 4: Implement write timeout**

Implementation notes:
- Apply `WriteTimeout` around each concrete `transport.Conn.Write` attempt
- For `net.Conn`-backed transports, use deadlines
- For WebSocket writes, set the write deadline before `WriteMessage`
- Surface timeout as `OnSessionError` followed by a normal session close path

- [ ] **Step 5: Run timeout tests and the full core suite**

Run: `go test ./internal/gateway/core -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/options.go internal/gateway/core/server.go internal/gateway/core/server_test.go internal/gateway/session/session.go
git commit -m "feat(gateway): add timeout and handler-error defaults"
```

---

## Task 6: `wkproto` protocol adapter

**Files:**
- Create: `internal/gateway/protocol/wkproto/adapter.go`
- Create: `internal/gateway/protocol/wkproto/adapter_test.go`

- [ ] **Step 1: Write failing `wkproto` adapter tests**

```go
package wkproto_test

import (
	"testing"

	adapterpkg "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/wkproto"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
)

func TestAdapterDecodeReturnsZeroUntilFrameIsComplete(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	frame, _ := testkit.MustEncodeWKProto(t, &wkpacket.PingPacket{}, wkpacket.LatestVersion)
	frames, consumed, err := adapter.Decode(sess, frame[:0])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(frames) != 0 || consumed != 0 {
		t.Fatalf("expected no progress for incomplete frame")
	}
}

func TestAdapterEncodeRoundTrip(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	encoded, err := adapter.Encode(sess, &wkpacket.PingPacket{}, session.OutboundMeta{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	frames, consumed, err := adapter.Decode(sess, encoded)
	if err != nil || consumed != len(encoded) || len(frames) != 1 {
		t.Fatalf("unexpected round trip: consumed=%d frames=%d err=%v", consumed, len(frames), err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/protocol/wkproto -v`
Expected: FAIL because the adapter does not exist yet.

- [ ] **Step 3: Implement the adapter using `pkg/wkproto`**

Implementation notes:
- Create a small wrapper around `pkg/wkproto.New()`
- Use `wkpacket.LatestVersion` as the default encode/decode version unless a future session override is added
- Return `nil, 0, nil` when the underlying codec reports incomplete data
- Ignore `ReplyToken`; it has no meaning for native `wkproto`

- [ ] **Step 4: Add sticky-packet coverage**

Add a test that concatenates two encoded frames in one input buffer and asserts:
- two decoded frames
- `consumed == len(buf)`

- [ ] **Step 5: Run tests to verify the adapter passes**

Run: `go test ./internal/gateway/protocol/wkproto -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/protocol/wkproto/adapter.go internal/gateway/protocol/wkproto/adapter_test.go
git commit -m "feat(gateway): add wkproto protocol adapter"
```

---

## Task 7: `jsonrpc` protocol adapter

**Files:**
- Create: `internal/gateway/protocol/jsonrpc/adapter.go`
- Create: `internal/gateway/protocol/jsonrpc/adapter_test.go`

- [ ] **Step 1: Write failing `jsonrpc` adapter tests**

```go
package jsonrpc_test

import (
	"testing"

	adapterpkg "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/jsonrpc"
	"github.com/WuKongIM/WuKongIM/internal/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/proto/jsonrpc"
	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
)

func TestAdapterDecodeReturnsReplyTokenForRequest(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	payload, _ := jsonrpc.Encode(jsonrpc.PingRequest{
		BaseRequest: jsonrpc.BaseRequest{Jsonrpc: "2.0", Method: jsonrpc.MethodPing, ID: "req-1"},
	})

	frames, consumed, err := adapter.Decode(sess, payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	tracker := adapter.(protocol.ReplyTokenTracker)
	tokens := tracker.TakeReplyTokens(sess, len(frames))
	if consumed != len(payload) || len(frames) != 1 || len(tokens) != 1 || tokens[0] != "req-1" {
		t.Fatalf("unexpected decode result: consumed=%d frames=%d tokens=%v", consumed, len(frames), tokens)
	}
}

func TestAdapterEncodeUsesReplyTokenAsResponseID(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	body, err := adapter.Encode(sess, &wkpacket.PongPacket{}, session.OutboundMeta{ReplyToken: "req-1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !testkit.JSONContains(body, `"id":"req-1"`) {
		t.Fatalf("expected response id in payload: %s", body)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/protocol/jsonrpc -v`
Expected: FAIL because the adapter does not exist yet.

- [ ] **Step 3: Implement request decode and response correlation**

Implementation notes:
- Use `encoding/json.Decoder` over a `bytes.Reader` so one message consumes exactly the bytes it decodes
- Call `pkg/jsonrpc.Decode` to classify the inbound message
- Convert the decoded message to `wkpacket.Frame` with `pkg/jsonrpc.ToFrame`
- Capture the returned request ID into a per-session queue exposed through `protocol.ReplyTokenTracker`, so core can place it on `gateway.Context.ReplyToken` without changing the `protocol.Adapter` contract
- If one decode call yields multiple frames, the adapter must queue one aligned reply-token entry per decoded frame, using empty strings for notification-only frames
- On encode:
  - use `pkg/jsonrpc.FromFrame(meta.ReplyToken, frame)` when `ReplyToken` is non-empty
  - use notification-style output when `ReplyToken` is empty and the frame type supports it

- [ ] **Step 4: Add malformed payload and unsupported frame tests**

Cover:
- invalid JSON
- unknown method
- frame type that cannot be represented as JSON-RPC

- [ ] **Step 5: Run tests to verify the adapter passes**

Run: `go test ./internal/gateway/protocol/jsonrpc -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/protocol/jsonrpc/adapter.go internal/gateway/protocol/jsonrpc/adapter_test.go
git commit -m "feat(gateway): add jsonrpc protocol adapter"
```

---

## Task 8: `stdnet` TCP transport

**Files:**
- Create: `internal/gateway/transport/stdnet/factory.go`
- Create: `internal/gateway/transport/stdnet/conn.go`
- Create: `internal/gateway/transport/stdnet/tcp_listener.go`
- Create: `internal/gateway/transport/stdnet/tcp_listener_test.go`

- [ ] **Step 1: Write failing TCP transport tests**

```go
package stdnet_test

import (
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
)

func TestTCPListenerDeliversOpenDataAndClose(t *testing.T) {
	handler := testkit.NewConnRecordingHandler()
	l, err := stdnet.NewTCPListener(transport.ListenerOptions{
		Name:    "tcp-wkproto",
		Address: "127.0.0.1:0",
	}, handler)
	if err != nil {
		t.Fatalf("NewTCPListener: %v", err)
	}
	defer l.Stop()

	if err := l.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", l.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_, _ = conn.Write([]byte("ping"))
	_ = conn.Close()

	testkit.WaitUntil(t, time.Second, func() bool {
		return handler.OpenCount() == 1 && handler.DataCount() == 1 && handler.CloseCount() == 1
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway/transport/stdnet -run TestTCPListener -v`
Expected: FAIL because the TCP listener does not exist yet.

- [ ] **Step 3: Implement the TCP listener**

Implementation notes:
- `factory.go` should implement `transport.Factory` for the `"stdnet"` name and dispatch:
  - `Network == "tcp"` -> `NewTCPListener`
  - `Network == "websocket"` -> `NewWSListener`
  - any other value -> validation error
- `conn.go` should wrap `net.Conn` and provide a stable generated `uint64` ID
- `tcp_listener.go` should:
  - call `net.Listen`
  - accept in a loop
  - invoke `handler.OnOpen`
  - run a read loop per connection
  - invoke `handler.OnData` on every successful read
  - invoke `handler.OnClose` once on EOF/error
- Route accept-loop errors that happen while still running to `ListenerOptions.OnError`

- [ ] **Step 4: Add stop/unblock coverage**

Add a test that starts the listener, opens a client connection, calls `Stop()`, and verifies:
- listener stop returns
- open connections close
- no goroutine leaks are visible from the test's synchronization points

- [ ] **Step 5: Run tests to verify the TCP transport passes**

Run: `go test ./internal/gateway/transport/stdnet -run TestTCPListener -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/transport/stdnet/factory.go internal/gateway/transport/stdnet/conn.go internal/gateway/transport/stdnet/tcp_listener.go internal/gateway/transport/stdnet/tcp_listener_test.go
git commit -m "feat(gateway): add stdnet tcp transport"
```

---

## Task 9: `stdnet` WebSocket transport

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/gateway/transport/stdnet/ws_listener.go`
- Create: `internal/gateway/transport/stdnet/ws_listener_test.go`

- [ ] **Step 1: Add the WebSocket dependency**

Run: `go get github.com/gorilla/websocket@v1.5.3`
Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 2: Write failing WebSocket transport tests**

```go
package stdnet_test

import (
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
	"github.com/gorilla/websocket"
)

func TestWSListenerUpgradesAndDeliversMessages(t *testing.T) {
	handler := testkit.NewConnRecordingHandler()
	l, err := stdnet.NewWSListener(transport.ListenerOptions{
		Name:    "ws-jsonrpc",
		Address: "127.0.0.1:0",
		Path:    "/ws",
	}, handler)
	if err != nil {
		t.Fatalf("NewWSListener: %v", err)
	}
	defer l.Stop()

	if err := l.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	u := "ws://" + strings.TrimPrefix(l.Addr(), "http://") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"ping","id":"1"}`))

	testkit.WaitUntil(t, time.Second, func() bool { return handler.DataCount() == 1 })
	_ = conn.Close()
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/gateway/transport/stdnet -run TestWSListener -v`
Expected: FAIL because the WebSocket listener does not exist yet.

- [ ] **Step 4: Implement the WebSocket listener**

Implementation notes:
- Use `net/http` plus `gorilla/websocket.Upgrader`
- Each accepted WebSocket connection should:
  - produce one `transport.Conn`
  - convert each WebSocket text or binary message into one `OnData` callback
  - write outbound payloads with `WriteMessage`
- Upgrade failures must be surfaced through `ListenerOptions.OnError`, not `OnSessionError`

- [ ] **Step 5: Run tests to verify the WebSocket transport passes**

Run: `go test ./internal/gateway/transport/stdnet -run TestWSListener -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/gateway/transport/stdnet/ws_listener.go internal/gateway/transport/stdnet/ws_listener_test.go
git commit -m "feat(gateway): add stdnet websocket transport"
```

---

## Task 10: Top-level gateway wiring and end-to-end smoke tests

**Files:**
- Create: `internal/gateway/gateway.go`
- Create: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/core/server.go`

- [ ] **Step 1: Write failing gateway smoke tests**

```go
package gateway_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
)

func TestGateway_StartStopTCPWKProto(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer gw.Stop()

	testkit.MustSendWKProtoPing(t, gw.ListenerAddr("tcp-wkproto"))
	testkit.WaitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
}
```

Add a sibling smoke test for `binding.WSJSONRPC` that dials `ws://` + `gw.ListenerAddr("ws-jsonrpc")` + `binding.DefaultWSPath`, sends a JSON-RPC `ping`, and asserts the handler sees one `wkpacket.PingPacket`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gateway -run TestGateway -v`
Expected: FAIL because the top-level gateway entrypoint does not exist yet.

- [ ] **Step 3: Implement `gateway.New`, `Start`, `Stop`, and listener address lookup**

Implementation notes:
- `gateway.New` should:
  - validate options
  - register built-in `stdnet`, `wkproto`, and `jsonrpc`
  - create the core server
- `gateway.Start` and `gateway.Stop` should delegate to the core server
- Add `ListenerAddr(name string) string` for tests and bootstrap consumers

- [ ] **Step 4: Run the targeted gateway tests**

Run: `go test ./internal/gateway -run TestGateway -v`
Expected: PASS.

- [ ] **Step 5: Run the full gateway test slice**

Run: `go test ./internal/gateway/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/gateway_test.go internal/gateway/core/server.go
git commit -m "feat(gateway): wire gateway end to end"
```

---

## Task 11: Final verification

**Files:**
- No code changes expected

- [ ] **Step 1: Run the full targeted test suite**

Run: `go test ./internal/gateway/... ./pkg/jsonrpc ./pkg/wkproto -v`
Expected: PASS.

- [ ] **Step 2: Run the repository-wide build as a regression check**

Run: `go test ./...`
Expected: PASS. If unrelated failures already exist on the branch, capture them before making any gateway changes and compare after implementation.

- [ ] **Step 3: Verify no unexpected dependency drift**

Run: `git diff --stat`
Expected: only the planned gateway files plus `go.mod`/`go.sum`.

- [ ] **Step 4: Prepare execution handoff**

Document in the execution summary:
- listener names and addresses used in tests
- that `transport/gnet` remains intentionally deferred
- that `github.com/gorilla/websocket` was introduced only for WebSocket transport support
