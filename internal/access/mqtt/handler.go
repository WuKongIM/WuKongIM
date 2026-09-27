package mqtt

import (
	"context"
	"errors"
	"sync"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	adapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

var (
	ErrHandlerInvalid  = errors.New("mqtt: invalid gateway entry configuration or context")
	ErrHandlerClosed   = errors.New("mqtt: connection entry unavailable")
	ErrHandlerCallback = errors.New("mqtt: gateway entry callback failed")
)

const connectionStateKey = "mqtt.access.connection"

// SessionLifecycle is implemented by the entry-neutral Session usecase. Direct
// disconnect is used only for a candidate rejected before supervisor registration.
type SessionLifecycle interface {
	Connect(context.Context, sessioncase.ConnectCommand) (sessioncase.Connection, error)
	Disconnect(context.Context, sessioncase.DisconnectCommand) error
}

// ConnectionSupervisor reserves bounded lifecycle work before acceptance.
// Disconnect must be nonblocking: gateway close can run inside a PUBLISH scope.
type ConnectionSupervisor interface {
	Register(contract.Owner) error
	Disconnect(runtime.DisconnectIntent) error
}

// ConnectionDeliveries registers one bound task after CONNACK enqueue. App owns
// its lifetime, including terminal cleanup after entry closes. Wake is a
// nonblocking hint; failures must be recoverable by runtime idle polling.
type ConnectionDeliveries interface {
	Register(context.Context, sessioncase.Connection, sessioncase.DeliverySink) error
	Wake(contract.Owner) error
}

type HandlerOptions struct {
	Namespace   string
	Sessions    SessionLifecycle
	Connections ConnectionSupervisor
	Owners      *runtime.Owners
	Publisher   *Publisher
	// Acknowledgements completes exact outbound exchanges; nil disables QoS 1 delivery.
	Acknowledgements OutboundAcknowledgements
	// Deliveries binds accepted connections to scheduled sending; nil disables it.
	// A configured delivery port requires Acknowledgements.
	Deliveries ConnectionDeliveries
	// Subscriptions maps confirmed intent transitions; nil disables SUB/UNSUB.
	// It requires delivery registration and durable acknowledgement support.
	Subscriptions SessionSubscriptions
	// SubscriptionTimeout bounds the entire control packet, including reply;
	// default five seconds, maximum one minute, independent of its filter count.
	SubscriptionTimeout time.Duration
	// ConnectTimeout bounds acquisition and open handoff separately; default
	// five seconds, maximum one minute.
	ConnectTimeout time.Duration
	// CleanupTimeout bounds rejected-candidate cleanup, default one second, max five.
	CleanupTimeout time.Duration
	// MaxPacketBytes must match the gateway codec's inbound limit, at most 1 MiB.
	MaxPacketBytes uint32
	// Now uses the same trusted monotonic clock as Owners and Connections.
	Now func() time.Time
}

// Handler owns gateway mapping and handoff only. App must not expose product
// MQTT until all required paths, including inbox projection, are composed.
type Handler struct{ options HandlerOptions }

var _ gt.PacketHandler = (*Handler)(nil)

// connectionState is private immutable evidence plus a small lifecycle gate.
// No credentials, per-connection goroutine or timer survive authentication.
type connectionState struct {
	connection      sessioncase.Connection
	gatewayID       uint64
	mu              sync.Mutex
	handshake       *runtime.Operation
	opened, closing bool
	// Only identity bindings survive enqueue; bodies belong to the bounded writer.
	receiveMaximum    uint16
	sent              map[uint16]sessioncase.AcknowledgementCommand
	sending           bool
	lastDeliveryOrder uint64
}

func NewHandler(o HandlerOptions) (*Handler, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SubscriptionTimeout == 0 {
		o.SubscriptionTimeout = 5 * time.Second
	}
	if o.ConnectTimeout == 0 {
		o.ConnectTimeout = 5 * time.Second
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = time.Second
	}
	if o.MaxPacketBytes == 0 {
		o.MaxPacketBytes = wire.DefaultMaxPacketBytes
	}
	now := o.Now()
	if (o.Deliveries != nil && o.Acknowledgements == nil) || (o.Subscriptions != nil && o.Deliveries == nil) || o.SubscriptionTimeout <= 0 || o.SubscriptionTimeout > time.Minute {
		return nil, ErrHandlerInvalid
	}
	if !contract.ValidIdentity(o.Namespace, 1024) || o.Sessions == nil || o.Connections == nil || o.Owners == nil || o.Publisher == nil || o.Publisher.options.Owners != o.Owners || o.ConnectTimeout <= 0 || o.ConnectTimeout > time.Minute || o.CleanupTimeout <= 0 || o.CleanupTimeout > 5*time.Second || o.MaxPacketBytes > wire.DefaultMaxPacketBytes || now == now.Round(0) {
		return nil, ErrHandlerInvalid
	}
	return &Handler{options: o}, nil
}

// OnConnect retains owner execution across the core-owned CONNACK write. Core
// transfers that operation exactly once to OnSessionOpen or the rollback closure.
func (h *Handler) OnConnect(g gt.Context, packet any) (result *gt.PacketAuthResult, err error) {
	var state *connectionState
	var acquired *sessioncase.Connection
	registered := false
	handedOff := false
	defer func() {
		if recover() != nil {
			result = nil
			err = ErrHandlerCallback
		}
		if !handedOff && acquired != nil {
			if registered {
				h.closeState(state, false, nil)
			} else {
				h.cleanupCandidate(*acquired)
			}
		}
	}()
	if h == nil || g.Session == nil || g.Session.ID() == 0 || g.RequestContext == nil || g.TransportCloser == nil {
		return nil, ErrHandlerInvalid
	}
	p, ok := packet.(*wire.Connect)
	if !ok || p == nil {
		return nil, ErrHandlerInvalid
	}
	command, peerMax, mapErr := h.mapConnect(p, g)
	reject := func(reason byte) *gt.PacketAuthResult {
		return &gt.PacketAuthResult{Reply: &wire.Connack{Reason: reason}, SessionValues: map[string]any{adapter.SessionMaximumPacketSize: peerMax}}
	}
	if mapErr != nil {
		return reject(connectReason(mapErr)), nil
	}
	ctx, cancel := context.WithTimeout(g.RequestContext, h.options.ConnectTimeout)
	defer cancel()
	connection, connectErr := h.options.Sessions.Connect(ctx, command)
	if connectErr != nil {
		return reject(connectReason(connectErr)), nil
	}
	acquired = &connection
	if ctx.Err() != nil {
		return reject(0x88), nil
	}
	if connection.Owner.Validate() != nil || connection.Owner.Key != command.Key || connection.UID != command.UID || connection.DeviceFlag != command.DeviceFlag {
		return reject(0x88), nil
	}
	state = &connectionState{connection: connection, gatewayID: g.Session.ID(), receiveMaximum: command.ReceiveMaximum}
	if registerErr := h.options.Connections.Register(connection.Owner); registerErr != nil {
		return reject(connectReason(registerErr)), nil
	}
	registered = true
	// The acquisition timeout ends at this return; handshake execution must instead
	// follow the gateway connection context until core finishes reply admission.
	op, beginErr := h.options.Owners.Begin(g.RequestContext, connection.Owner)
	if beginErr != nil {
		return reject(connectReason(beginErr)), nil
	}
	state.handshake = op
	if op.UID() != connection.UID || op.Check() != nil || g.RequestContext.Err() != nil {
		return reject(0x88), nil
	}
	result = &gt.PacketAuthResult{Accepted: true, Reply: h.connack(connection), SessionValues: map[string]any{connectionStateKey: state, adapter.SessionMaximumPacketSize: peerMax}, Rollback: func(error) { h.closeState(state, false, nil) }}
	result.CheckReply = func() error {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.closing || state.handshake == nil || g.RequestContext.Err() != nil || state.handshake.Check() != nil {
			return ErrHandlerClosed
		}
		return nil
	}
	handedOff = true
	return result, nil
}

// cleanupCandidate has no admitted scope and never runs from a close callback.
// Failure retains a fenced owner for app-owned sweep/deadline reconciliation.
func (h *Handler) cleanupCandidate(c sessioncase.Connection) {
	_ = h.options.Owners.Fence(c.Owner)
	ctx, cancel := context.WithTimeout(context.Background(), h.options.CleanupTimeout)
	defer cancel()
	// A dependency panic must not replace the original redacted handshake error.
	defer func() { _ = recover() }()
	_ = h.options.Sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: c.Owner, ObservedAt: h.options.Now()})
}

func (h *Handler) state(g gt.Context) *connectionState {
	if h == nil || g.Session == nil {
		return nil
	}
	state, _ := g.Session.Value(connectionStateKey).(*connectionState)
	if state == nil || state.gatewayID != g.Session.ID() {
		return nil
	}
	return state
}

// OnSessionOpen releases the handshake only after checking its live execution
// gate. An expired or cancelled acquisition cannot become packet-admissible.
func (h *Handler) OnSessionOpen(g gt.Context) (err error) {
	s := h.state(g)
	if s == nil {
		return ErrHandlerClosed
	}
	defer func() {
		if recover() != nil {
			err = ErrHandlerCallback
		}
		if err != nil {
			// CONNACK may already be on the wire when EOF cancels open-time
			// registration. Preserve any validated decoded peer close intent.
			_ = h.OnSessionClose(g)
		}
	}()
	s.mu.Lock()
	op := s.handshake
	valid := !s.closing && !s.opened && op != nil && g.RequestContext != nil && g.RequestContext.Err() == nil && op.Check() == nil
	s.handshake = nil
	if valid {
		s.opened = true
	}
	s.mu.Unlock()
	op.Done()
	if !valid {
		return ErrHandlerClosed
	}
	if h.options.Deliveries != nil {
		ctx, cancel := context.WithTimeout(g.RequestContext, h.options.ConnectTimeout)
		defer cancel()
		connection, sink, bindErr := h.BindDelivery(g)
		if bindErr != nil || h.options.Deliveries.Register(ctx, connection, sink) != nil || ctx.Err() != nil {
			return ErrHandlerClosed
		}
		// A newly scheduled turn may already occupy the operation budget. Begin
		// checks identity/lease before that limit; saturation alone is not closure.
		check, checkErr := h.options.Owners.Begin(ctx, connection.Owner)
		if check != nil {
			checkErr = check.Check()
			check.Done()
		}
		if checkErr != nil && !errors.Is(checkErr, runtime.ErrOwnerLimit) {
			return ErrHandlerClosed
		}
		s.mu.Lock()
		closed := s.closing
		s.mu.Unlock()
		if closed || ctx.Err() != nil {
			return ErrHandlerClosed
		}
	}
	return nil
}

// wakeDelivery never changes committed results or interrupts required cleanup.
// No connection lock is held: callbacks may schedule immediately or reenter.
func (h *Handler) wakeDelivery(owner contract.Owner) {
	if h.options.Deliveries == nil {
		return
	}
	defer func() { _ = recover() }()
	_ = h.options.Deliveries.Wake(owner)
}

// closeState preserves first intent before any transport callback can reenter.
// It releases only the handshake scope; packet scopes belong to their callers.
func (h *Handler) closeState(s *connectionState, normal bool, expiry *uint32) {
	h.closeStateAt(s, normal, expiry, h.options.Now())
}

func (h *Handler) closeStateAt(s *connectionState, normal bool, expiry *uint32, observed time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	op := s.handshake
	s.handshake = nil
	s.mu.Unlock()
	op.Done()
	// The supervisor records intent before it fences. Fencing first lets a
	// concurrent renewal synthesize abnormal cleanup ahead of normal DISCONNECT.
	defer func() {
		_ = h.options.Owners.Fence(s.connection.Owner)
		h.wakeDelivery(s.connection.Owner)
	}()
	_ = h.options.Connections.Disconnect(runtime.DisconnectIntent{Owner: s.connection.Owner, Normal: normal, SessionExpirySec: expiry, ObservedAt: observed})
}

func (h *Handler) OnSessionClose(g gt.Context) error {
	s := h.state(g)
	if s == nil {
		return nil
	}
	if p, observed, ok := receivedDisconnect(g); ok {
		expiry, valid := disconnectExpiry(p, s.connection.SessionExpirySec)
		if valid {
			h.closeStateAt(s, p.Reason == 0, expiry, observed)
			return nil
		}
	}
	h.closeState(s, false, nil)
	return nil
}

// Gateway owns transport diagnostics and invokes close cleanup after errors.
func (*Handler) OnSessionError(gt.Context, error) {}
func (*Handler) OnListenerError(string, error)    {}

// OnPacket runs on the existing session-ordered gateway mailbox. Ordinary
// returned errors do not close gateway sessions, so fatal branches close explicitly.
func (h *Handler) OnPacket(g gt.Context, packet any) (err error) {
	s := h.state(g)
	defer func() {
		if recover() != nil {
			h.terminate(g, s, 0)
			err = ErrHandlerCallback
		}
	}()
	if s == nil || g.RequestContext == nil {
		return h.terminate(g, s, 0)
	}
	s.mu.Lock()
	live := s.opened && !s.closing
	s.mu.Unlock()
	if !live {
		return h.terminate(g, s, 0)
	}
	// DISCONNECT only records cleanup intent. Requiring a new owner operation
	// would turn a decoded normal receipt into abnormal closure when TCP EOF
	// cancels dispatch or fencing/operation pressure closes execution admission.
	if p, ok := packet.(*wire.Disconnect); ok {
		expiry, valid := disconnectExpiry(p, s.connection.SessionExpirySec)
		if !valid {
			return h.terminate(g, s, wire.ProtocolError)
		}
		observed := h.options.Now()
		if _, received, ok := receivedDisconnect(g); ok {
			observed = received
		}
		h.closeStateAt(s, p.Reason == 0, expiry, observed)
		_ = g.CloseSession(gt.CloseReasonPeerClosed, nil)
		return nil
	}
	if p, ok := packet.(*wire.Publish); ok {
		return h.options.Publisher.Publish(g, s.connection, p)
	}
	if p, ok := packet.(*wire.Puback); ok {
		return h.acknowledge(g, s, p)
	}
	switch packet.(type) {
	case *wire.Subscribe, *wire.Unsubscribe:
		return h.subscriptionPacket(g, s, packet)
	}
	op, beginErr := h.options.Owners.Begin(g.RequestContext, s.connection.Owner)
	if beginErr != nil {
		return h.terminate(g, s, 0)
	}
	defer op.Done()
	if op.Check() != nil || g.RequestContext.Err() != nil {
		return h.terminate(g, s, 0)
	}
	switch p := packet.(type) {
	case *wire.Pingreq:
		if p == nil {
			return h.terminate(g, s, wire.ProtocolError)
		}
		if g.WritePacket(&wire.Pingresp{}) != nil {
			return h.terminate(g, s, 0)
		}
		return nil
	default:
		return h.terminate(g, s, 0x83)
	}
}

func receivedDisconnect(g gt.Context) (*wire.Disconnect, time.Time, bool) {
	o, ok := adapter.ReceivedDisconnect(g.Session)
	if !ok {
		return nil, time.Time{}, false
	}
	p := &wire.Disconnect{Reason: o.Reason}
	if o.HasSessionExpiry {
		p.Properties = append(p.Properties, wire.Property{ID: wire.SessionExpiryInterval, Number: o.SessionExpirySec})
	}
	if o.HasServerReference {
		p.Properties = append(p.Properties, wire.Property{ID: wire.ServerReference})
	}
	return p, o.ObservedAt, true
}

func (h *Handler) terminate(g gt.Context, s *connectionState, reason byte) error {
	if reason != 0 {
		_ = g.WritePacket(&wire.Disconnect{Reason: reason})
	}
	if h != nil {
		if reason == 0 {
			// Transport/open/delivery cancellation can beat the gateway close
			// callback. An explicit protocol rejection still takes the error path.
			_ = h.OnSessionClose(g)
		} else {
			h.closeState(s, false, nil)
		}
	}
	_ = g.CloseSession(gt.CloseReasonPolicyViolation, ErrHandlerClosed)
	return ErrHandlerClosed
}
