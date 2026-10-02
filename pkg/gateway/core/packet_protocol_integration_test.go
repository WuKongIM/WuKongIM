//go:build integration

package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	mqttadapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
)

type packetHandler struct {
	connect       func(gt.Context, any) (*gt.PacketAuthResult, error)
	packet        func(gt.Context, any) error
	opened        chan gt.Context
	open          func(gt.Context) error
	closed        chan struct{}
	listenerError func(string, error)
}

func (h *packetHandler) OnConnect(c gt.Context, p any) (*gt.PacketAuthResult, error) {
	return h.connect(c, p)
}
func (h *packetHandler) OnPacket(c gt.Context, p any) error { return h.packet(c, p) }
func (h *packetHandler) OnSessionOpen(c gt.Context) error {
	if h.open != nil {
		return h.open(c)
	}
	h.opened <- c
	return nil
}
func (h *packetHandler) OnSessionClose(gt.Context) error { h.closed <- struct{}{}; return nil }
func (*packetHandler) OnSessionError(gt.Context, error)  {}
func (h *packetHandler) OnListenerError(name string, err error) {
	if h.listenerError != nil {
		h.listenerError(name, err)
	}
}

// packetDeferredHandler detects protocol packets incorrectly mapped to WK SEND.
type packetDeferredHandler struct {
	*testkit.RecordingHandler
	calls atomic.Int32
}

func (h *packetDeferredHandler) OnSendBatchDeferred([]gt.SendBatchItem, func(int, func() error) error, func(error)) error {
	h.calls.Add(1)
	return errors.New("packet reached WK deferred handler")
}

func packetServer(t *testing.T, h *packetHandler, runtimeOptions ...gt.RuntimeOptions) (*core.Server, *testkit.FakeTransportFactory) {
	runtime := gt.RuntimeOptions{}
	if len(runtimeOptions) > 0 {
		runtime = runtimeOptions[0]
	}
	return packetServerOptions(t, h, gt.Options{Runtime: runtime})
}

func packetServerOptions(t *testing.T, h *packetHandler, options gt.Options) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()
	r := core.NewRegistry()
	f := testkit.NewFakeTransportFactory("test")
	require.NoError(t, r.RegisterTransport(f))
	require.NoError(t, r.RegisterPacketProtocol(mqttadapter.New(mqtt.Limits{})))
	options.PacketHandler = h
	options.Listeners = []gt.ListenerOptions{{Name: "mqtt", Network: "tcp", Address: "test", Transport: "test", Protocol: "mqtt"}}
	s, err := core.NewServer(r, &options)
	require.NoError(t, err)
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Stop()) })
	return s, f
}

var packetConnect = []byte{0x10, 0x0e, 0, 4, 'M', 'Q', 'T', 'T', 5, 2, 0, 60, 0, 0, 1, 'c'}

func TestPacketProtocolSharesAuthAndOrderedDispatch(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred-%t", deferred), func(t *testing.T) {
			wk := &packetDeferredHandler{RecordingHandler: &testkit.RecordingHandler{}}
			entered, release := make(chan struct{}), make(chan struct{})
			h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1)}
			h.connect = func(ctx gt.Context, p any) (*gt.PacketAuthResult, error) {
				if _, ok := p.(*mqtt.Connect); !ok {
					return nil, errors.New("not MQTT CONNECT")
				}
				close(entered)
				<-release
				return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}, SessionValues: map[string]any{"test.authenticated": true}}, nil
			}
			packets := make(chan any, 3)
			h.packet = func(ctx gt.Context, p any) error {
				packets <- p
				return ctx.WritePacket(&mqtt.Pingresp{})
			}
			options := gt.Options{}
			if deferred {
				options.Handler = wk
			}
			s, f := packetServerOptions(t, h, options)
			conn := f.MustOpen("mqtt", 1)
			returned := make(chan struct{})
			go func() { f.MustData("mqtt", 1, packetConnect); close(returned) }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("auth not scheduled")
			}
			select {
			case <-returned:
			case <-ctx.Done():
				t.Fatal("auth ran on transport callback")
			}
			require.Empty(t, conn.Writes(), "CONNACK before auth completes")
			close(release)
			var sessionContext gt.Context
			select {
			case sessionContext = <-h.opened:
			case <-ctx.Done():
				t.Fatal("session did not open")
			}
			require.Equal(t, true, sessionContext.Session.Value("test.authenticated"))
			require.Equal(t, [][]byte{{0x20, 3, 0, 0, 0}}, conn.Writes(), "open preceded CONNACK")
			f.MustData("mqtt", 1, []byte{0xc0, 0, 0xc0, 0})
			for range 2 {
				select {
				case p := <-packets:
					require.IsType(t, &mqtt.Pingreq{}, p)
				case <-ctx.Done():
					t.Fatal("packet dispatch blocked")
				}
			}
			require.NoError(t, s.DrainSends(ctx))
			require.Equal(t, [][]byte{{0x20, 3, 0, 0, 0}, {0xd0, 0}, {0xd0, 0}}, conn.Writes())
			f.MustData("mqtt", 1, []byte{0xc0, 0})
			select {
			case <-h.closed:
			case <-ctx.Done():
				t.Fatal("drained connection stayed admitted")
			}
			require.Empty(t, packets, "packet dispatched after drain")
			require.Zero(t, wk.calls.Load(), "MQTT packets reached WK SEND preparation")
		})
	}

}

func TestPacketProtocolRejectsUnauthenticatedAndPipelinedInput(t *testing.T) {
	for _, input := range [][]byte{{0xc0, 0}, append(append([]byte(nil), packetConnect...), 0xc0, 0)} {
		var authCalls atomic.Int32
		h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
			authCalls.Add(1)
			return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
		}, packet: func(gt.Context, any) error { return nil }}
		_, f := packetServer(t, h)
		conn := f.MustOpen("mqtt", 1)
		f.MustData("mqtt", 1, input)
		require.EqualValues(t, 0, authCalls.Load())
		require.True(t, connClosed(conn))
		require.Empty(t, conn.Writes())
		require.Empty(t, h.opened)
	}
}

func TestPacketProtocolRefusesAuthenticationWithoutOpening(t *testing.T) {
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Reply: &mqtt.Connack{Reason: 0x86}}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServer(t, h)
	conn := f.MustOpen("mqtt", 1)
	f.MustData("mqtt", 1, packetConnect)
	waitFor(t, func() bool { return connClosed(conn) })
	require.Empty(t, h.opened)
	require.Len(t, conn.Writes(), 1)
	require.True(t, bytes.Equal(conn.Writes()[0], []byte{0x20, 3, 0, 0x86, 0}))
}

func TestPacketProtocolRollsBackAcceptedConnectionOnWriteFailure(t *testing.T) {
	rolled := make(chan error, 2)
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}, Rollback: func(err error) { rolled <- err }}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServer(t, h)
	conn := f.MustOpen("mqtt", 1)
	conn.SetWriteErr(errors.New("fixture transport failure"))
	f.MustData("mqtt", 1, packetConnect)
	select {
	case err := <-rolled:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("activation leaked after output failure")
	}
	require.Empty(t, h.opened)
	require.Empty(t, h.closed, "unopened activation cleanup belongs to rollback")
	require.Empty(t, rolled, "activation rolled back twice")
}

func TestPacketProtocolChecksAcceptedReplyBeforeEnqueue(t *testing.T) {
	for _, panicCheck := range []bool{false, true} {
		rolled := make(chan error, 1)
		h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
			return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}, CheckReply: func() error {
				if panicCheck {
					panic("secret")
				}
				return errors.New("owner expired before reply")
			}, Rollback: func(err error) { rolled <- err }}, nil
		}, packet: func(gt.Context, any) error { return nil }}
		_, factory := packetServer(t, h)
		conn := factory.MustOpen("mqtt", 1)
		factory.MustData("mqtt", 1, packetConnect)
		select {
		case err := <-rolled:
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		case <-time.After(5 * time.Second):
			t.Fatal("reply check did not roll back")
		}
		require.Empty(t, conn.Writes())
		require.Empty(t, h.opened)
		require.Empty(t, h.closed)
	}
}

func TestPacketProtocolLateAuthenticationRollsBackAfterPeerViolation(t *testing.T) {
	entered, release, rolled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		close(entered)
		<-release
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}, Rollback: func(error) { rolled <- struct{}{} }}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServer(t, h)
	conn := f.MustOpen("mqtt", 1)
	f.MustData("mqtt", 1, packetConnect)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not scheduled")
	}
	f.MustData("mqtt", 1, []byte{0xc0, 0})
	require.True(t, connClosed(conn))
	close(release)
	select {
	case <-rolled:
	case <-time.After(5 * time.Second):
		t.Fatal("late activation escaped rollback")
	}
	require.Empty(t, h.opened)
	require.Empty(t, conn.Writes())
}

func TestPacketProtocolRetainedBytesIncludeExecutingWorkAndDrainReleasesThem(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred-%t", deferred), func(t *testing.T) {
			wk := &packetDeferredHandler{RecordingHandler: &testkit.RecordingHandler{}}
			entered, release := make(chan struct{}), make(chan struct{})
			var handled atomic.Int32
			h := &packetHandler{opened: make(chan gt.Context, 2), closed: make(chan struct{}, 2), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
				return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
			}, packet: func(gt.Context, any) error {
				if handled.Add(1) == 1 {
					close(entered)
					<-release
				}
				return nil
			}}
			options := gt.Options{Runtime: gt.RuntimeOptions{AsyncPacketMaxBytes: 16}}
			if deferred {
				options.Handler = wk
			}
			s, f := packetServerOptions(t, h, options)
			conn := f.MustOpen("mqtt", 1)
			f.MustData("mqtt", 1, packetConnect)
			select {
			case <-h.opened:
			case <-time.After(5 * time.Second):
				t.Fatal("auth not ready")
			}
			f.MustData("mqtt", 1, []byte{0xc0, 0})
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch not running")
			}
			f.MustData("mqtt", 1, bytes.Repeat([]byte{0xc0, 0}, 8))
			require.True(t, connClosed(conn), "running+queued packets escaped byte cap")
			close(release)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, s.DrainSends(ctx))
			require.EqualValues(t, 1, handled.Load(), "closed owner executed queued packets")
			f.MustOpen("mqtt", 2)
			f.MustData("mqtt", 2, packetConnect)
			select {
			case <-h.opened:
			case <-ctx.Done():
				t.Fatal("drain leaked packet budget")
			}
			require.Zero(t, wk.calls.Load(), "MQTT packets reached WK SEND preparation")
		})
	}

}

func TestPacketProtocolDecoderBoundsCoalescedBatchAllocation(t *testing.T) {
	adapter := mqttadapter.New(mqtt.Limits{})
	packets, n, err := adapter.DecodePackets(nil, bytes.Repeat([]byte{0xc0, 0}, 1000))
	require.NoError(t, err)
	require.Len(t, packets, 128)
	require.Equal(t, 256, n)
}

func TestPacketProtocolCallbackPanicTerminatesOnlyThatConnection(t *testing.T) {
	for _, stage := range []string{"connect", "open", "packet"} {
		t.Run(stage, func(t *testing.T) {
			h := &packetHandler{opened: make(chan gt.Context, 2), closed: make(chan struct{}, 2), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
				if stage == "connect" {
					panic("secret fixture must not be logged")
				}
				return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
			}, packet: func(gt.Context, any) error { panic("private payload fixture") }}
			if stage == "open" {
				h.open = func(gt.Context) error { panic("private session fixture") }
			}
			_, f := packetServer(t, h)
			conn := f.MustOpen("mqtt", 1)
			f.MustData("mqtt", 1, packetConnect)
			if stage == "packet" {
				select {
				case <-h.opened:
				case <-time.After(5 * time.Second):
					t.Fatal("no open")
				}
				f.MustData("mqtt", 1, []byte{0xc0, 0})
			}
			waitFor(t, func() bool { return connClosed(conn) })
		})
	}
}

func TestPacketProtocolFragmentBufferGrowsAmortized(t *testing.T) {
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServer(t, h)
	f.MustOpen("mqtt", 1)
	f.MustData("mqtt", 1, packetConnect)
	select {
	case <-h.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not ready")
	}
	input, err := mqtt.Encode(&mqtt.Publish{Topic: "a", Payload: make([]byte, 2048)}, mqtt.Limits{})
	require.NoError(t, err)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < len(input)-1; i++ {
		f.MustData("mqtt", 1, input[i:i+1])
	}
	runtime.ReadMemStats(&after)
	require.Less(t, after.Mallocs-before.Mallocs, uint64(128), "fragment handling copied the whole buffered prefix per byte")
}

func TestMQTTAdapterHonorsPeerMaximumPacketSize(t *testing.T) {
	sess := session.New(session.Config{ID: 1})
	sess.SetValue(mqttadapter.SessionMaximumPacketSize, uint32(5))
	adapter := mqttadapter.New(mqtt.Limits{})
	_, err := adapter.EncodePacket(sess, &mqtt.Publish{Topic: "a", Payload: []byte("x")}, session.OutboundMeta{})
	var protocolErr *mqtt.Error
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, mqtt.PacketTooLarge, protocolErr.Reason)
	_, err = adapter.EncodePacket(sess, &mqtt.Connack{}, session.OutboundMeta{})
	require.NoError(t, err, "packet exactly at peer limit rejected")
}

func TestPacketProtocolReportsListenerError(t *testing.T) {
	reported := make(chan error, 1)
	h := &packetHandler{listenerError: func(name string, err error) {
		require.Equal(t, "mqtt", name)
		reported <- err
	}}
	_, f := packetServer(t, h)
	failure := errors.New("fixture listener failure")
	f.MustError("mqtt", failure)
	select {
	case got := <-reported:
		require.ErrorIs(t, got, failure)
	default:
		t.Fatal("independent protocol listener error was lost")
	}
}

func TestMQTTKeepAliveRequiresCompletePacket(t *testing.T) {
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServer(t, h)
	conn := f.MustOpen("mqtt", 1)
	connect := append([]byte(nil), packetConnect...)
	connect[11] = 1 // MQTT Keep Alive = 1 second.
	start := time.Now()
	f.MustData("mqtt", 1, connect)
	select {
	case <-h.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not ready")
	}
	// Announce a body longer than this test will send, then keep dripping bytes.
	f.MustData("mqtt", 1, []byte{0x30, 0xff, 0x07})
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-conn.CloseCh():
			require.GreaterOrEqual(t, time.Since(start), 1400*time.Millisecond, "closed before negotiated Keep Alive")
			return
		case <-tick.C:
			f.MustData("mqtt", 1, []byte{0})
		case <-deadline.C:
			t.Fatal("incomplete packet indefinitely renewed Keep Alive")
		}
	}
}

func TestMQTTKeepAliveZeroDisablesDefaultReadIdle(t *testing.T) {
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
	}, packet: func(gt.Context, any) error { return nil }}
	_, f := packetServerOptions(t, h, gt.Options{DefaultSession: gt.SessionOptions{IdleTimeout: 100 * time.Millisecond}})
	conn := f.MustOpen("mqtt", 1)
	connect := append([]byte(nil), packetConnect...)
	connect[11] = 0
	f.MustData("mqtt", 1, connect)
	select {
	case <-h.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not ready")
	}
	select {
	case <-conn.CloseCh():
		t.Fatal("Keep Alive zero inherited the default read-idle deadline")
	case <-time.After(300 * time.Millisecond):
	}
}

type packetObserver struct {
	in      chan gt.FrameEvent
	out     chan gt.FrameEvent
	handled chan gt.FrameHandleEvent
}

func (*packetObserver) OnConnectionOpen(gt.ConnectionEvent)    {}
func (*packetObserver) OnConnectionClose(gt.ConnectionEvent)   {}
func (*packetObserver) OnAuth(gt.AuthEvent)                    {}
func (o *packetObserver) OnFrameIn(e gt.FrameEvent)            { o.in <- e }
func (o *packetObserver) OnFrameOut(e gt.FrameEvent)           { o.out <- e }
func (o *packetObserver) OnFrameHandled(e gt.FrameHandleEvent) { o.handled <- e }

func TestPacketProtocolReportsBoundedPacketObservations(t *testing.T) {
	observer := &packetObserver{in: make(chan gt.FrameEvent, 8), out: make(chan gt.FrameEvent, 8), handled: make(chan gt.FrameHandleEvent, 8)}
	h := &packetHandler{opened: make(chan gt.Context, 1), closed: make(chan struct{}, 1), connect: func(gt.Context, any) (*gt.PacketAuthResult, error) {
		return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
	}, packet: func(c gt.Context, _ any) error { return c.WritePacket(&mqtt.Pingresp{}) }}
	s, f := packetServerOptions(t, h, gt.Options{Observer: observer})
	f.MustOpen("mqtt", 1)
	f.MustData("mqtt", 1, packetConnect)
	select {
	case <-h.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("auth not ready")
	}
	f.MustData("mqtt", 1, []byte{0xc0, 0})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, s.DrainSends(ctx))
	require.Len(t, observer.in, 2)
	for i, name := range []string{"CONNECT", "PINGREQ"} {
		e := <-observer.in
		require.Equal(t, name, e.FrameType)
		require.Equal(t, "mqtt", e.Listener)
		require.Equal(t, "tcp", e.Protocol, "existing observation protocol denotes transport")
		require.Equal(t, []int{16, 2}[i], e.Bytes)
	}
	require.Len(t, observer.out, 2)
	for i, name := range []string{"CONNACK", "PINGRESP"} {
		e := <-observer.out
		require.Equal(t, name, e.FrameType)
		require.Equal(t, []int{5, 2}[i], e.Bytes)
	}
	require.Len(t, observer.handled, 1)
	e := <-observer.handled
	require.Equal(t, "PINGREQ", e.FrameType)
	require.NoError(t, e.Err)
}
