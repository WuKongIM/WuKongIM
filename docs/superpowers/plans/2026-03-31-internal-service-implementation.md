# Internal Service Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first `internal/service` business runtime above `internal/gateway`, including online session registry, gateway lifecycle handling, frame routing, a narrow local `SendPacket` path, and one real gateway-backed integration test.

**Architecture:** The implementation stays intentionally narrow. First add the package skeleton, auth-derived session metadata extraction, and a thread-safe local registry. Then add a gateway-facing service adapter plus frame router, followed by in-memory sequencing and local delivery primitives, and finally a person-channel-only `SendPacket` path that writes `SendackPacket` back to the sender and `RecvPacket` to same-node recipient sessions. No subscription handling, remote forwarding, or durable message persistence is included in this first pass.

**Tech Stack:** Go 1.23, `internal/gateway`, `pkg/wkpacket`, `pkg/wkproto`, `pkg/wkstore`, `testing`, `net`

---

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/service/options.go` | Constructor options and dependency ports for stores, delivery, sequencing, and time source |
| `internal/service/errors.go` | Service-level sentinel errors such as unsupported frame and unauthenticated session |
| `internal/service/session_state.go` | `SessionMeta` plus helpers that extract UID/device data from gateway-authenticated sessions |
| `internal/service/registry.go` | Thread-safe node-local online session registry (`sessionID`, `uid -> sessions`) |
| `internal/service/service.go` | `Service` struct, constructor, defaults, and dependency wiring |
| `internal/service/handler.go` | `gateway.Handler` lifecycle methods: open, close, and error |
| `internal/service/frame_router.go` | `OnFrame` type-switch routing to focused handlers |
| `internal/service/recvack.go` | Explicit v1 no-op `RecvackPacket` handling |
| `internal/service/sequence.go` | In-memory message ID and per-user message-sequence allocator for local delivery |
| `internal/service/delivery.go` | Registry-backed local delivery port that writes frames to recipient sessions |
| `internal/service/send.go` | Person-channel-only `SendPacket` handling, `SendackPacket` response, and `RecvPacket` fan-out |
| `internal/service/testkit/fakes.go` | Recording sessions and tiny fake collaborators for focused unit tests |
| `internal/service/registry_test.go` | Session metadata and registry unit tests |
| `internal/service/handler_test.go` | Service lifecycle unit tests |
| `internal/service/router_test.go` | Frame-router and `RecvackPacket` tests |
| `internal/service/delivery_test.go` | Sequencer and local delivery tests |
| `internal/service/send_test.go` | `SendPacket` behavior tests |
| `internal/service/integration_test.go` | Gateway-backed end-to-end smoke test over real `wkproto` transport |

### Task 1: Session Metadata and Registry Primitives

**Files:**
- Create: `internal/service/errors.go`
- Create: `internal/service/options.go`
- Create: `internal/service/session_state.go`
- Create: `internal/service/registry.go`
- Create: `internal/service/registry_test.go`

- [ ] **Step 1: Write the failing session-metadata and registry tests**

Add tests for:
- `TestSessionMetaFromContextReadsGatewayAuthValues`
- `TestSessionMetaFromContextRequiresUID`
- `TestRegistryRegisterLookupAndUnregister`
- `TestRegistryUnregisterIsIdempotent`

Use a recording or simple fake session and assert that UID, device flag, device level, listener name, and session pointer are preserved.

```go
func TestSessionMetaFromContextReadsGatewayAuthValues(t *testing.T) {
    sess := session.New(session.Config{
        ID:       1,
        Listener: "tcp",
    })
    sess.SetValue(gateway.SessionValueUID, "u1")
    sess.SetValue(gateway.SessionValueDeviceFlag, wkpacket.APP)
    sess.SetValue(gateway.SessionValueDeviceLevel, wkpacket.DeviceLevelMaster)

    meta, err := sessionMetaFromContext(&gateway.Context{
        Session:  sess,
        Listener: "tcp",
    }, fixedNow)
    require.NoError(t, err)
    require.Equal(t, "u1", meta.UID)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestSessionMeta|TestRegistry' -v`

Expected: FAIL because `internal/service` does not exist yet.

- [ ] **Step 3: Implement the minimal metadata and registry code**

Create:
- `SessionMeta` with `SessionID`, `UID`, `DeviceFlag`, `DeviceLevel`, `Listener`, `ConnectedAt`, and `Session`
- `sessionMetaFromContext(ctx *gateway.Context, now time.Time) (SessionMeta, error)`
- `Registry` with `Register`, `Unregister`, `Session`, and `SessionsByUID`

Implementation shape:

```go
type SessionMeta struct {
    SessionID   uint64
    UID         string
    DeviceFlag  wkpacket.DeviceFlag
    DeviceLevel wkpacket.DeviceLevel
    Listener    string
    ConnectedAt time.Time
    Session     session.Session
}

type Registry struct {
    mu         sync.RWMutex
    bySession  map[uint64]SessionMeta
    byUID      map[string]map[uint64]SessionMeta
}
```

Use typed sentinel errors such as `ErrUnauthenticatedSession` and return copies from `SessionsByUID` so callers cannot mutate registry internals.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestSessionMeta|TestRegistry' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/errors.go internal/service/options.go internal/service/session_state.go internal/service/registry.go internal/service/registry_test.go
git commit -m "feat(service): add session registry primitives"
```

### Task 2: Gateway Lifecycle Adapter

**Files:**
- Create: `internal/service/service.go`
- Create: `internal/service/handler.go`
- Create: `internal/service/handler_test.go`
- Modify: `internal/service/options.go`

- [ ] **Step 1: Write the failing lifecycle tests**

Add tests for:
- `TestServiceOnSessionOpenRegistersAuthenticatedSession`
- `TestServiceOnSessionCloseUnregistersSession`
- `TestServiceOnSessionErrorDoesNotMutateRegistry`
- `TestNewServiceUsesDefaultNowWhenUnset`

Sketch:

```go
func TestServiceOnSessionOpenRegistersAuthenticatedSession(t *testing.T) {
    svc := New(Options{Now: func() time.Time { return fixedNow }})
    ctx := newAuthedContext(t, 1, "u1")

    require.NoError(t, svc.OnSessionOpen(ctx))

    sessions := svc.registry.SessionsByUID("u1")
    require.Len(t, sessions, 1)
    require.Equal(t, uint64(1), sessions[0].SessionID)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestServiceOnSession(Open|Close|Error)|TestNewServiceUsesDefaultNowWhenUnset' -v`

Expected: FAIL because `Service` does not exist yet.

- [ ] **Step 3: Implement the minimal service constructor and lifecycle methods**

Create:
- `Service` struct with `registry`, `opts`, and future dependencies
- `New(opts Options) *Service`
- `OnSessionOpen`, `OnSessionClose`, and `OnSessionError`

Use this shape:

```go
type Service struct {
    registry *Registry
    opts     Options
}

func New(opts Options) *Service {
    if opts.Now == nil {
        opts.Now = time.Now
    }
    return &Service{
        registry: NewRegistry(),
        opts:     opts,
    }
}
```

`OnSessionOpen` should extract `SessionMeta` and call `registry.Register`. `OnSessionClose` should no-op safely on nil context/session and call `registry.Unregister` when present. `OnSessionError` should remain side-effect free in v1.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestServiceOnSession(Open|Close|Error)|TestNewServiceUsesDefaultNowWhenUnset' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/handler.go internal/service/handler_test.go internal/service/options.go
git commit -m "feat(service): add gateway lifecycle adapter"
```

### Task 3: Frame Router and `RecvackPacket` Stub

**Files:**
- Create: `internal/service/frame_router.go`
- Create: `internal/service/recvack.go`
- Create: `internal/service/send.go`
- Create: `internal/service/router_test.go`
- Modify: `internal/service/errors.go`

- [ ] **Step 1: Write the failing router tests**

Add tests for:
- `TestServiceOnFrameReturnsUnsupportedFrameError`
- `TestServiceOnFrameRecvackIsNoop`

Do not test `SendPacket` behavior here yet; this task only establishes routing shape and the explicit `RecvackPacket` no-op.

```go
func TestServiceOnFrameReturnsUnsupportedFrameError(t *testing.T) {
    svc := New(Options{})
    err := svc.OnFrame(newAuthedContext(t, 1, "u1"), &wkpacket.PingPacket{})
    require.ErrorIs(t, err, ErrUnsupportedFrame)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestServiceOnFrame(ReturnsUnsupportedFrameError|RecvackIsNoop)' -v`

Expected: FAIL because `OnFrame` routing does not exist yet.

- [ ] **Step 3: Implement the router and explicit `RecvackPacket` stub**

Create:
- `OnFrame` type-switch in `frame_router.go`
- `handleRecvack` returning `nil`
- temporary `handleSend` stub in `send.go`

Implementation shape:

```go
func (s *Service) OnFrame(ctx *gateway.Context, frame wkpacket.Frame) error {
    switch pkt := frame.(type) {
    case *wkpacket.SendPacket:
        return s.handleSend(ctx, pkt)
    case *wkpacket.RecvackPacket:
        return s.handleRecvack(ctx, pkt)
    default:
        return ErrUnsupportedFrame
    }
}

func (s *Service) handleRecvack(_ *gateway.Context, _ *wkpacket.RecvackPacket) error {
    return nil
}
```

The temporary `handleSend` can return a narrow internal sentinel such as `ErrSendNotReady` until Task 5 replaces it with the real path.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestServiceOnFrame(ReturnsUnsupportedFrameError|RecvackIsNoop)' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/frame_router.go internal/service/recvack.go internal/service/send.go internal/service/router_test.go internal/service/errors.go
git commit -m "feat(service): add frame router skeleton"
```

### Task 4: Sequencer and Local Delivery Primitives

**Files:**
- Create: `internal/service/sequence.go`
- Create: `internal/service/delivery.go`
- Create: `internal/service/testkit/fakes.go`
- Create: `internal/service/delivery_test.go`
- Modify: `internal/service/options.go`

- [ ] **Step 1: Write the failing sequencing and delivery tests**

Add tests for:
- `TestMemorySequencerAllocatesMonotonicMessageIDs`
- `TestMemorySequencerAllocatesPerUserSequences`
- `TestLocalDeliveryWritesFrameToEveryRecipientSession`

Use a recording session fake that captures frames written through `WriteFrame`.

```go
func TestLocalDeliveryWritesFrameToEveryRecipientSession(t *testing.T) {
    s1 := testkit.NewRecordingSession(11, "tcp")
    s2 := testkit.NewRecordingSession(12, "tcp")
    delivery := localDelivery{}

    err := delivery.Deliver(context.Background(), []SessionMeta{
        {UID: "u2", Session: s1},
        {UID: "u2", Session: s2},
    }, &wkpacket.PingPacket{})
    require.NoError(t, err)
    require.Len(t, s1.WrittenFrames(), 1)
    require.Len(t, s2.WrittenFrames(), 1)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestMemorySequencer|TestLocalDelivery' -v`

Expected: FAIL because the sequencer, delivery port, and recording-session fake do not exist yet.

- [ ] **Step 3: Implement the in-memory sequencer and delivery port**

Create:
- `SequenceAllocator` interface in `options.go`
- `memorySequencer` in `sequence.go`
- `DeliveryPort` interface plus `localDelivery` in `delivery.go`
- recording-session fake in `testkit/fakes.go`

Implementation shape:

```go
type SequenceAllocator interface {
    NextMessageID() int64
    NextUserSequence(uid string) uint32
}

type memorySequencer struct {
    nextMessageID atomic.Int64
    mu            sync.Mutex
    userSeq       map[string]uint32
}
```

`localDelivery.Deliver(...)` should iterate recipients and call `recipient.Session.WriteFrame(frame)` once per recipient, returning the first error.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestMemorySequencer|TestLocalDelivery' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/sequence.go internal/service/delivery.go internal/service/testkit/fakes.go internal/service/delivery_test.go internal/service/options.go
git commit -m "feat(service): add local delivery primitives"
```

### Task 5: Narrow `SendPacket` Path for Local Person Channels

**Files:**
- Modify: `internal/service/send.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/options.go`
- Create: `internal/service/send_test.go`

- [ ] **Step 1: Write the failing send-path tests**

Add tests for:
- `TestHandleSendDeliversLocalPersonMessage`
- `TestHandleSendWritesUserNotOnNodeAckWhenRecipientIsOffline`
- `TestHandleSendRejectsUnsupportedChannelType`
- `TestHandleSendRequiresAuthenticatedSender`

The happy-path test should assert:
- sender gets one `SendackPacket` with `ReasonSuccess`
- every local recipient session gets one `RecvPacket`
- recipient `RecvPacket.FromUID == senderUID`
- recipient `RecvPacket.ChannelID == senderUID` for person channels
- recipient `RecvPacket.MessageID` is non-zero and `MessageSeq` is stable across same-user recipient sessions

```go
func TestHandleSendDeliversLocalPersonMessage(t *testing.T) {
    sender := testkit.NewRecordingSession(1, "tcp")
    recipientA := testkit.NewRecordingSession(2, "tcp")
    recipientB := testkit.NewRecordingSession(3, "tcp")

    svc := New(Options{Now: fixedNowFn})
    registerRecipient(t, svc, "u2", recipientA, recipientB)

    sender.SetValue(gateway.SessionValueUID, "u1")
    ctx := &gateway.Context{Session: sender, Listener: "tcp"}
    pkt := &wkpacket.SendPacket{ChannelID: "u2", ChannelType: wkpacket.ChannelTypePerson, Payload: []byte("hi"), ClientSeq: 9, ClientMsgNo: "m1"}

    require.NoError(t, svc.OnFrame(ctx, pkt))
    require.Len(t, sender.WrittenFrames(), 1)
    require.Len(t, recipientA.WrittenFrames(), 1)
    require.Len(t, recipientB.WrittenFrames(), 1)
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./internal/service -run 'TestHandleSend' -v`

Expected: FAIL because `handleSend` is still a placeholder.

- [ ] **Step 3: Implement the minimal v1 send behavior**

Replace the placeholder `handleSend` with a real implementation that:
- extracts sender UID from the session
- supports only `wkpacket.ChannelTypePerson`
- looks up recipient sessions by `packet.ChannelID`
- writes `ReasonUserNotOnNode` when no local recipient exists
- allocates one message ID and one recipient-user sequence
- builds one `RecvPacket` and delivers it to every local recipient session
- writes one `SendackPacket` back to the sender

Suggested shape:

```go
func (s *Service) handleSend(ctx *gateway.Context, pkt *wkpacket.SendPacket) error {
    senderUID, err := senderUIDFromContext(ctx)
    if err != nil {
        return err
    }
    if pkt.ChannelType != wkpacket.ChannelTypePerson {
        return s.writeSendack(ctx, pkt, 0, 0, wkpacket.ReasonNotSupportChannelType)
    }

    recipients := s.registry.SessionsByUID(pkt.ChannelID)
    if len(recipients) == 0 {
        return s.writeSendack(ctx, pkt, 0, 0, wkpacket.ReasonUserNotOnNode)
    }

    msgID := s.sequencer.NextMessageID()
    msgSeq := s.sequencer.NextUserSequence(pkt.ChannelID)
    recv := buildPersonRecvPacket(senderUID, pkt, msgID, msgSeq, s.opts.Now())
    if err := s.delivery.Deliver(context.Background(), recipients, recv); err != nil {
        return err
    }
    return s.writeSendack(ctx, pkt, msgID, msgSeq, wkpacket.ReasonSuccess)
}
```

Also update `New(...)` so it installs default `memorySequencer` and `localDelivery` when none are provided.

- [ ] **Step 4: Run the targeted tests to verify they pass**

Run: `go test ./internal/service -run 'TestHandleSend' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/send.go internal/service/service.go internal/service/options.go internal/service/send_test.go
git commit -m "feat(service): add local person send path"
```

### Task 6: Gateway-Backed Integration Smoke Test

**Files:**
- Create: `internal/service/integration_test.go`
- Modify: `internal/service/send.go`
- Modify: `internal/service/handler.go`

- [ ] **Step 1: Write the failing gateway integration test**

Add `TestGatewayWKProtoServiceRoutesLocalPersonSend` that:
- starts a real `gateway.Gateway` with `service.New(...)` as the handler
- uses `gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{TokenAuthOn: false})`
- opens two `wkproto` TCP clients as `u1` and `u2`
- sends a person message from `u1` to `u2`
- verifies `u1` receives `SendackPacket{ReasonCode: ReasonSuccess}`
- verifies `u2` receives `RecvPacket` with `FromUID == "u1"` and `ChannelID == "u1"`

Use helper functions local to the test file for:
- dialing the gateway
- sending `ConnectPacket`
- encoding/decoding frames with `pkg/wkproto`

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `go test ./internal/service -run TestGatewayWKProtoServiceRoutesLocalPersonSend -v`

Expected: FAIL because at least one integration gap remains in the newly added service path.

- [ ] **Step 3: Close the smallest integration gaps**

Fix whatever the integration test exposes, keeping the scope narrow:
- wrong person-channel `ChannelID` mapping in `RecvPacket`
- missing `SendackPacket` fields
- missing default collaborator wiring in `New(...)`
- lifecycle timing assumptions around session registration

Do not widen scope into remote forwarding, subscription handling, or durable message storage.

- [ ] **Step 4: Run the full service test package**

Run: `go test ./internal/service -v`

Expected: PASS

Also run one gateway regression sweep:

Run: `go test ./internal/gateway -run 'TestGatewayWKProtoAuthAcceptsConnectBeforeDispatchingFrames|TestGatewayStartStopTCPWKProto' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/integration_test.go internal/service/send.go internal/service/handler.go internal/service/service.go internal/service/options.go
git commit -m "test(service): add gateway integration coverage"
```

## Notes

- Keep `SubPacket` completely out of this implementation pass.
- Keep non-person channel send behavior explicit and narrow; returning `ReasonNotSupportChannelType` is preferable to guessing semantics.
- Do not add globals. Dependencies should be constructor-injected through `Options`.
- Keep `RecvackPacket` handling explicit but empty so later work has a stable hook point.
- If a test suggests cross-node delivery, stop and write a follow-up design instead of extending this plan ad hoc.
