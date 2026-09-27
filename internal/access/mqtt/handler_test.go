package mqtt_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	adapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
)

type handlerSessions struct {
	connect    func(context.Context, sessioncase.ConnectCommand) (sessioncase.Connection, error)
	disconnect func(context.Context, sessioncase.DisconnectCommand) error
}

func (s handlerSessions) Connect(ctx context.Context, c sessioncase.ConnectCommand) (sessioncase.Connection, error) {
	return s.connect(ctx, c)
}
func (s handlerSessions) Disconnect(ctx context.Context, c sessioncase.DisconnectCommand) error {
	return s.disconnect(ctx, c)
}

type handlerConnections struct {
	registerErr      error
	registered       []contract.Owner
	intents          []runtime.DisconnectIntent
	beforeDisconnect func(runtime.DisconnectIntent)
}

func (s *handlerConnections) Register(o contract.Owner) error {
	s.registered = append(s.registered, o)
	return s.registerErr
}
func (s *handlerConnections) Disconnect(i runtime.DisconnectIntent) error {
	if s.beforeDisconnect != nil {
		s.beforeDisconnect(i)
	}
	s.intents = append(s.intents, i)
	return nil
}

func TestHandlerAcceptsNormalIntentBeforeFencingRenewal(t *testing.T) {
	f := newHandlerFixture(t)
	f.accept(t)
	require.NoError(t, f.h.OnSessionOpen(f.gateway))
	f.connections.beforeDisconnect = func(i runtime.DisconnectIntent) {
		if !i.Normal {
			return
		}
		// The supervisor must receive intent before it can observe a fenced
		// lease and synthesize abnormal cleanup on a concurrent renewal.
		op, err := f.owners.Begin(context.Background(), i.Owner)
		if op != nil {
			op.Done()
		}
		require.NoError(t, err, "disconnect intent does not require a new execution scope")
	}
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Disconnect{}))
}

type handlerCloser struct{}

func (handlerCloser) CloseTransportAndWait(context.Context, gt.CloseReason) error { return nil }

type handlerFixture struct {
	*publishFixture
	h           *access.Handler
	sessions    *handlerSessions
	connections *handlerConnections
	command     sessioncase.ConnectCommand
	cleaned     []sessioncase.DisconnectCommand
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	f := &handlerFixture{publishFixture: newPublishFixture(t), connections: &handlerConnections{}}
	f.connection.SessionExpirySec = 60
	f.connection.Lease = sessioncase.Lease{Revision: 1, Until: f.now.Add(30 * time.Second)}
	f.gateway.TransportCloser = handlerCloser{}
	f.sessions = &handlerSessions{connect: func(ctx context.Context, c sessioncase.ConnectCommand) (sessioncase.Connection, error) {
		_, ok := ctx.Deadline()
		require.True(t, ok)
		f.command = c
		return f.connection, nil
	}, disconnect: func(ctx context.Context, c sessioncase.DisconnectCommand) error {
		require.NoError(t, ctx.Err())
		f.cleaned = append(f.cleaned, c)
		return f.owners.Quiesce(ctx, c.Owner)
	}}
	var err error
	f.h, err = access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.gateway.CloseSessionFn = func(gt.CloseReason, error) { f.closed = true; require.NoError(t, f.h.OnSessionClose(f.gateway)) }
	return f
}
func handlerConnect() *wire.Connect {
	return &wire.Connect{ClientID: "SYSTEM", Username: "alice", UsernameFlag: true, PasswordFlag: true, Password: []byte("secret"), Properties: []wire.Property{{ID: wire.UserProperty, Text: "wk.device_flag", Value: "1"}, {ID: wire.SessionExpiryInterval, Number: 60}}}
}
func (f *handlerFixture) accept(t *testing.T) *gt.PacketAuthResult {
	t.Helper()
	r, err := f.h.OnConnect(f.gateway, handlerConnect())
	require.NoError(t, err)
	require.True(t, r.Accepted)
	for k, v := range r.SessionValues {
		f.gateway.Session.SetValue(k, v)
	}
	t.Cleanup(func() { r.Rollback(errors.New("cleanup")) })
	return r
}
func TestHandlerHandoffHoldsOwnerUntilOpenOrRollback(t *testing.T) {
	for _, mode := range []string{"open", "rollback", "fenced", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newHandlerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.gateway.RequestContext = ctx
			r := f.accept(t)
			require.Equal(t, uint16(math.MaxUint16), f.command.ReceiveMaximum)
			require.Equal(t, uint32(math.MaxUint32), f.command.MaxPacketBytes)
			require.Equal(t, uint32(math.MaxUint32), r.SessionValues[adapter.SessionMaximumPacketSize])
			require.Equal(t, 1, f.owners.Snapshot().Operations)
			switch mode {
			case "rollback":
				r.Rollback(errors.New("secret"))
				r.Rollback(nil)
				require.Len(t, f.connections.intents, 1)
			case "fenced":
				require.NoError(t, f.owners.Fence(f.connection.Owner))
				require.Error(t, r.CheckReply())
				require.Error(t, f.h.OnSessionOpen(f.gateway))
				require.Len(t, f.connections.intents, 1)
			case "cancelled":
				cancel()
				require.Error(t, r.CheckReply())
				require.Error(t, f.h.OnSessionOpen(f.gateway))
				require.Len(t, f.connections.intents, 1)
			default:
				require.NoError(t, r.CheckReply())
				require.NoError(t, f.h.OnSessionOpen(f.gateway))
				require.NoError(t, f.h.OnPacket(f.gateway, &wire.Pingreq{}))
				require.Equal(t, []any{&wire.Pingresp{}}, f.writes)
				require.NoError(t, f.h.OnPacket(f.gateway, publishPacket()))
				require.Equal(t, &wire.Puback{PacketID: 29}, f.writes[1])
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestHandlerRejectionsAreRedactedAndBounded(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason byte
	}{{user.ErrInvalidToken, 0x87}, {sessioncase.ErrBinding, 0x87}, {sessioncase.ErrWillDenied, 0x87}, {sessioncase.ErrConflict, 0x89}, {runtime.ErrOwnerLimit, 0x97}, {errors.New("secret"), 0x88}} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			f := newHandlerFixture(t)
			f.sessions.connect = func(context.Context, sessioncase.ConnectCommand) (sessioncase.Connection, error) {
				return sessioncase.Connection{}, tc.err
			}
			p := handlerConnect()
			p.Properties = append(p.Properties, wire.Property{ID: wire.MaximumPacketSize, Number: 9})
			r, err := f.h.OnConnect(f.gateway, p)
			require.NoError(t, err)
			require.False(t, r.Accepted)
			require.Equal(t, &wire.Connack{Reason: tc.reason}, r.Reply)
			require.Equal(t, uint32(9), r.SessionValues[adapter.SessionMaximumPacketSize])
			require.Empty(t, f.connections.registered)
		})
	}
}
func TestHandlerRegistrationFailureCleansAcquisition(t *testing.T) {
	f := newHandlerFixture(t)
	f.connections.registerErr = runtime.ErrConnectionsLimit
	r, err := f.h.OnConnect(f.gateway, handlerConnect())
	require.NoError(t, err)
	require.False(t, r.Accepted)
	require.Len(t, f.cleaned, 1)
	require.Equal(t, f.connection.Owner, f.cleaned[0].Owner)
	require.False(t, f.cleaned[0].Normal)
	require.Empty(t, f.connections.intents)
	require.Zero(t, f.owners.Snapshot().Held)
}
func TestHandlerMapsWillAndRejectsInvalidInputBeforeAcquisition(t *testing.T) {
	f := newHandlerFixture(t)
	p := handlerConnect()
	p.Will = &wire.Will{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte("gone"), Properties: []wire.Property{{ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "will-1"}, {ID: wire.WillDelayInterval, Number: 7}}}
	r, err := f.h.OnConnect(f.gateway, p)
	require.NoError(t, err)
	require.True(t, r.Accepted)
	defer r.Rollback(nil)
	require.Equal(t, "bob", f.command.Will.TargetID)
	require.Equal(t, uint32(7), f.command.Will.DelaySeconds)
	p.Will.Payload[0] = 'X'
	require.Equal(t, "gone", string(f.command.Will.Payload))
	for _, change := range []func(*wire.Connect){func(p *wire.Connect) { p.Will.Retain = true }, func(p *wire.Connect) { p.Properties = append(p.Properties, wire.Property{ID: wire.ReceiveMaximum}) }, func(p *wire.Connect) {
		p.Properties = append(p.Properties, wire.Property{ID: wire.SessionExpiryInterval, Number: 1})
	}} {
		q := handlerConnect()
		q.Will = &wire.Will{Topic: p.Will.Topic, Properties: p.Will.Properties}
		change(q)
		f.sessions.connect = func(context.Context, sessioncase.ConnectCommand) (sessioncase.Connection, error) {
			t.Fatal("invalid input reached acquisition")
			return sessioncase.Connection{}, nil
		}
		rejected, e := f.h.OnConnect(f.gateway, q)
		require.NoError(t, e)
		require.False(t, rejected.Accepted)
	}
}
func TestHandlerDisconnectFirstIntentAndControlValidation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		p             *wire.Disconnect
		expiry        uint32
		normal, valid bool
	}{
		{"normal", &wire.Disconnect{}, 60, true, true},
		{"will", &wire.Disconnect{Reason: 4}, 60, false, true},
		{"client_error", &wire.Disconnect{Reason: 0x99}, 60, false, true},
		{"zero_extension", &wire.Disconnect{Properties: []wire.Property{{ID: wire.SessionExpiryInterval, Number: 1}}}, 0, false, false},
		{"server_reason", &wire.Disconnect{Reason: 0x8e}, 60, false, false},
		{"server_property", &wire.Disconnect{Properties: []wire.Property{{ID: wire.ServerReference, Text: "secret"}}}, 60, false, false},
		{"expiry_override", &wire.Disconnect{Properties: []wire.Property{{ID: wire.SessionExpiryInterval, Number: 12}}}, 60, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerFixture(t)
			f.connection.SessionExpirySec = tc.expiry
			f.accept(t)
			require.NoError(t, f.h.OnSessionOpen(f.gateway))
			err := f.h.OnPacket(f.gateway, tc.p)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, &wire.Disconnect{Reason: 0x82}, f.writes[0])
			}
			require.True(t, f.closed)
			require.NoError(t, f.h.OnSessionClose(f.gateway))
			require.Len(t, f.connections.intents, 1)
			intent := f.connections.intents[0]
			require.Equal(t, tc.normal, intent.Normal)
			require.Equal(t, f.now, intent.ObservedAt)
			if tc.name == "expiry_override" {
				require.Equal(t, uint32(12), *intent.SessionExpirySec)
			}
			before := len(f.writes)
			require.Error(t, f.h.OnPacket(f.gateway, &wire.Pingreq{}))
			require.Len(t, f.writes, before)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestHandlerCloseCallbackDoesNotWaitForPacketScope(t *testing.T) {
	f := newHandlerFixture(t)
	f.accept(t)
	require.NoError(t, f.h.OnSessionOpen(f.gateway))
	op, err := f.owners.Begin(context.Background(), f.connection.Owner)
	require.NoError(t, err)
	defer op.Done()
	require.NoError(t, f.h.OnSessionClose(f.gateway))
	require.Len(t, f.connections.intents, 1)
	require.Equal(t, 1, f.owners.Snapshot().Operations)
}

func TestHandlerDecodedDisconnectSurvivesTransportCloseBeforeDispatch(t *testing.T) {
	for _, normal := range []bool{true, false} {
		f := newHandlerFixture(t)
		f.accept(t)
		require.NoError(t, f.h.OnSessionOpen(f.gateway))
		packet := &wire.Disconnect{}
		if !normal {
			packet.Reason = 4
		}
		encoded, err := wire.Encode(packet, wire.Limits{})
		require.NoError(t, err)
		_, _, err = adapter.New(wire.Limits{}).DecodePackets(f.gateway.Session, encoded)
		require.NoError(t, err)
		// No OnPacket: EOF can close the transport before the ordered mailbox runs.
		require.NoError(t, f.h.OnSessionClose(f.gateway))
		require.Len(t, f.connections.intents, 1)
		require.Equal(t, normal, f.connections.intents[0].Normal)
	}
}

func TestHandlerDisconnectIntentSurvivesCancelledOrFencedDispatch(t *testing.T) {
	for _, state := range []string{"cancelled", "fenced", "busy"} {
		t.Run(state, func(t *testing.T) {
			f := newHandlerFixture(t)
			f.accept(t)
			require.NoError(t, f.h.OnSessionOpen(f.gateway))
			packet := &wire.Disconnect{}
			encoded, err := wire.Encode(packet, wire.Limits{})
			require.NoError(t, err)
			_, _, err = adapter.New(wire.Limits{}).DecodePackets(f.gateway.Session, encoded)
			require.NoError(t, err)
			observed, ok := adapter.ReceivedDisconnect(f.gateway.Session)
			require.True(t, ok)
			f.now = time.Now()
			switch state {
			case "cancelled":
				ctx, cancel := context.WithCancel(f.gateway.RequestContext)
				cancel()
				f.gateway.RequestContext = ctx
			case "fenced":
				require.NoError(t, f.owners.Fence(f.connection.Owner))
			case "busy":
				op, e := f.owners.Begin(context.Background(), f.connection.Owner)
				require.NoError(t, e)
				defer op.Done()
			}
			require.NoError(t, f.h.OnPacket(f.gateway, packet))
			require.NoError(t, f.h.OnSessionClose(f.gateway))
			require.Len(t, f.connections.intents, 1)
			require.True(t, f.connections.intents[0].Normal, "transport cancellation replaced received normal intent")
			require.Equal(t, observed.ObservedAt, f.connections.intents[0].ObservedAt)
		})
	}
}
