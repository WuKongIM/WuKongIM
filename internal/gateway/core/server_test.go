package core_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/core"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestServer(t *testing.T) {
	t.Run("unauthenticated wkproto frames are rejected before reaching the handler", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("wkproto")
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
			}, nil
		}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return connClosed(conn) })
		if got := handler.frameCount(); got != 0 {
			t.Fatalf("expected no frames to reach handler, got %d", got)
		}
		if got := len(handler.callOrder()); got != 0 {
			t.Fatalf("expected handler not to observe rejected session, got calls %v", handler.callOrder())
		}
		if got := len(conn.Writes()); got != 0 {
			t.Fatalf("expected no writes for rejected unauthenticated frame, got %d", got)
		}
		if got := connClosed(conn); !got {
			t.Fatal("expected connection to close after policy violation")
		}
	})

	t.Run("failed wkproto authentication replies with connack and closes", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("wkproto")
		proto.encodedBytes = []byte("connack-auth-fail")
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:   "u1",
				Token: "bad-token",
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
			}, nil
		}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return connClosed(conn) && len(conn.Writes()) == 1 })
		if got := handler.frameCount(); got != 0 {
			t.Fatalf("expected connect not to reach handler, got %d", got)
		}
		if got := len(handler.callOrder()); got != 0 {
			t.Fatalf("expected handler not to observe failed auth session, got calls %v", handler.callOrder())
		}
		if !reflect.DeepEqual(conn.Writes()[0], []byte("connack-auth-fail")) {
			t.Fatalf("unexpected connack payload: %q", conn.Writes()[0])
		}
	})

	t.Run("successful wkproto activation runs before success connack", func(t *testing.T) {
		handler := newTestHandler()
		handler.onActivate = func(ctx *gateway.Context) (*frame.ConnackPacket, error) {
			if got := ctx.Session.Value(gateway.SessionValueDeviceID); got != "d-1" {
				t.Fatalf("expected device id before activation, got %#v", got)
			}
			return nil, nil
		}

		proto := newScriptedProtocol("wkproto")
		proto.encodedBytes = []byte("connack-success")
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:        "u1",
				DeviceID:   "d-1",
				DeviceFlag: frame.APP,
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("c"))

		waitFor(t, func() bool { return len(handler.callOrder()) == 2 && len(conn.Writes()) == 1 })
		if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "open"}) {
			t.Fatalf("unexpected call order: %v", got)
		}
		if got := conn.Writes()[0]; !reflect.DeepEqual(got, []byte("connack-success")) {
			t.Fatalf("unexpected connack payload: %q", got)
		}
	})

	t.Run("successful wkproto activation sees device id from generic auth result", func(t *testing.T) {
		handler := newTestHandler()
		handler.onActivate = func(ctx *gateway.Context) (*frame.ConnackPacket, error) {
			if got := ctx.Session.Value(gateway.SessionValueDeviceID); got != "d-1" {
				t.Fatalf("expected device id before activation, got %#v", got)
			}
			return nil, nil
		}

		proto := newScriptedProtocol("wkproto")
		proto.encodedBytes = []byte("connack-success")
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:        "u1",
				DeviceID:   "d-1",
				DeviceFlag: frame.APP,
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
			}, nil
		}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("c"))

		waitFor(t, func() bool { return len(handler.callOrder()) == 2 && len(conn.Writes()) == 1 })
		if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "open"}) {
			t.Fatalf("unexpected call order: %v", got)
		}
	})

	t.Run("wkproto activation failure writes retryable connack and closes", func(t *testing.T) {
		handler := newTestHandler()
		handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) {
			return nil, errors.New("activate boom")
		}

		proto := newScriptedProtocol("wkproto")
		proto.encodeFn = func(_ session.Session, f frame.Frame, _ session.OutboundMeta) ([]byte, error) {
			connack, ok := f.(*frame.ConnackPacket)
			if !ok {
				t.Fatalf("expected connack frame, got %T", f)
			}
			if connack.ReasonCode != frame.ReasonSystemError {
				t.Fatalf("expected retryable connack, got %v", connack.ReasonCode)
			}
			return []byte("connack-system"), nil
		}
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:        "u1",
				DeviceID:   "d-1",
				DeviceFlag: frame.APP,
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("c"))

		waitFor(t, func() bool { return connClosed(conn) && len(conn.Writes()) == 1 })
		if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate"}) {
			t.Fatalf("unexpected call order: %v", got)
		}
		if got := conn.Writes()[0]; !reflect.DeepEqual(got, []byte("connack-system")) {
			t.Fatalf("unexpected connack payload: %q", got)
		}
	})

	t.Run("wkproto activation normalizes zero reason override to success", func(t *testing.T) {
		handler := newTestHandler()
		handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) {
			return &frame.ConnackPacket{}, nil
		}

		proto := newScriptedProtocol("wkproto")
		proto.encodeFn = func(_ session.Session, f frame.Frame, _ session.OutboundMeta) ([]byte, error) {
			connack, ok := f.(*frame.ConnackPacket)
			if !ok {
				t.Fatalf("expected connack frame, got %T", f)
			}
			if connack.ReasonCode != frame.ReasonSuccess {
				t.Fatalf("expected normalized success connack, got %v", connack.ReasonCode)
			}
			return []byte("connack-success"), nil
		}
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:        "u1",
				DeviceID:   "d-1",
				DeviceFlag: frame.APP,
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
			}, nil
		}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("c"))

		waitFor(t, func() bool { return len(conn.Writes()) == 1 && len(handler.callOrder()) == 2 })
		if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "open"}) {
			t.Fatalf("unexpected call order: %v", got)
		}
		if got := conn.Writes()[0]; !reflect.DeepEqual(got, []byte("connack-success")) {
			t.Fatalf("unexpected connack payload: %q", got)
		}
	})

	t.Run("successful wkproto authentication replies with connack and allows later frames", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("wkproto")
		proto.encodedBytes = []byte("connack-success")
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.ConnectPacket{
				UID:   "u1",
				Token: "good-token",
			}},
			consumed: 1,
		})

		srv, transportFactory := newTestServerWithAuthenticator(t, handler, proto, gateway.SessionOptions{}, gateway.AuthenticatorFunc(func(ctx *gateway.Context, connect *frame.ConnectPacket) (*gateway.AuthResult, error) {
			if connect.UID != "u1" {
				t.Fatalf("unexpected uid: %q", connect.UID)
			}
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
				SessionValues: map[string]any{
					"uid": connect.UID,
				},
			}, nil
		}))
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("c"))
		waitFor(t, func() bool { return len(conn.Writes()) == 1 })

		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})
		transportFactory.MustData("listener-a", 1, []byte("p"))
		waitFor(t, func() bool { return handler.frameCount() == 1 })

		if got := conn.Writes()[0]; !reflect.DeepEqual(got, []byte("connack-success")) {
			t.Fatalf("unexpected connack payload: %q", got)
		}
		if got := handler.contexts()[0].Session.Value("uid"); got != "u1" {
			t.Fatalf("expected session uid to be stored, got %#v", got)
		}
	})

	t.Run("decoded frames are delivered to the handler", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames:     []frame.Frame{&frame.PingPacket{}},
			consumed:   1,
			tokenBatch: []string{""},
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return handler.frameCount() == 1 })
		if _, ok := handler.frames()[0].(*frame.PingPacket); !ok {
			t.Fatalf("expected ping packet, got %T", handler.frames()[0])
		}
	})

	t.Run("throughput send frames do not block later frame dispatch on the same connection", func(t *testing.T) {
		handler := newTestHandler()
		sendStarted := make(chan struct{})
		releaseSend := make(chan struct{})
		pingSeen := make(chan struct{})
		handler.onFrame = func(_ *gateway.Context, f frame.Frame) error {
			switch f.(type) {
			case *frame.SendPacket:
				close(sendStarted)
				<-releaseSend
			case *frame.PingPacket:
				close(pingSeen)
			}
			return nil
		}

		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames: []frame.Frame{&frame.SendPacket{
				ChannelID:   "u2",
				ChannelType: frame.ChannelTypePerson,
				ClientSeq:   1,
				ClientMsgNo: "m1",
			}},
			consumed: 1,
		})
		proto.pushDecode(decodeResult{})
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
			AsyncSendDispatch: true,
		})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		go transportFactory.MustData("listener-a", 1, []byte("s"))

		waitFor(t, func() bool {
			select {
			case <-sendStarted:
				return true
			default:
				return false
			}
		})

		go transportFactory.MustData("listener-a", 1, []byte("p"))

		select {
		case <-pingSeen:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("expected ping to dispatch before throughput send finished")
		}

		close(releaseSend)
		waitFor(t, func() bool { return handler.frameCount() == 2 })
	})

	t.Run("listener scoped errors go to OnListenerError before a session exists", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		listenerErr := errors.New("listener boom")
		transportFactory.MustError("listener-a", listenerErr)

		waitFor(t, func() bool { return len(handler.listenerErrors()) == 1 })
		got := handler.listenerErrors()[0]
		if got.Listener != "listener-a" {
			t.Fatalf("expected listener-a, got %q", got.Listener)
		}
		if !errors.Is(got.Err, listenerErr) {
			t.Fatalf("expected %v, got %v", listenerErr, got.Err)
		}
		if len(handler.sessionErrors()) != 0 {
			t.Fatalf("expected no session errors, got %d", len(handler.sessionErrors()))
		}
	})

	t.Run("partial inbound data does not dispatch until a complete frame is available", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{})
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 2,
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("a"))
		if got := handler.frameCount(); got != 0 {
			t.Fatalf("expected no frames after partial data, got %d", got)
		}

		transportFactory.MustData("listener-a", 1, []byte("b"))
		waitFor(t, func() bool { return handler.frameCount() == 1 })
	})

	t.Run("inbound overflow closes with CloseReasonInboundOverflow", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
			MaxInboundBytes: 1,
		})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("ab"))

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		if got := handler.closeReasons()[0]; got != gateway.CloseReasonInboundOverflow {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonInboundOverflow, got)
		}
		if len(handler.sessionErrors()) != 1 {
			t.Fatalf("expected one session error, got %d", len(handler.sessionErrors()))
		}
		if !reflect.DeepEqual(handler.callOrder(), []string{"open", "error", "close"}) {
			t.Fatalf("unexpected call order: %v", handler.callOrder())
		}
	})

	t.Run("handler error closes when CloseOnHandlerError is true", func(t *testing.T) {
		handlerErr := errors.New("handler boom")
		handler := newTestHandler()
		handler.onFrame = func(*gateway.Context, frame.Frame) error { return handlerErr }

		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
			CloseOnHandlerError: boolPtr(true),
		})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		reasons := handler.closeReasons()
		if got := reasons[len(reasons)-1]; got != gateway.CloseReasonHandlerError {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonHandlerError, got)
		}
		if len(handler.sessionErrors()) != 1 || !errors.Is(handler.sessionErrors()[0], handlerErr) {
			t.Fatalf("expected session error %v, got %v", handlerErr, handler.sessionErrors())
		}
	})

	t.Run("handler error stays open when CloseOnHandlerError is explicitly false", func(t *testing.T) {
		handlerErr := errors.New("handler boom")
		handler := newTestHandler()
		handler.onFrame = func(*gateway.Context, frame.Frame) error { return handlerErr }

		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
			CloseOnHandlerError: boolPtr(false),
		})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		conn := transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return len(handler.sessionErrors()) == 1 })
		if connClosed(conn) {
			t.Fatal("expected connection to remain open when CloseOnHandlerError is false")
		}
		if handler.closeCount() != 0 {
			t.Fatalf("expected no close callbacks, got %d", handler.closeCount())
		}
		if len(handler.sessionErrors()) != 1 || !errors.Is(handler.sessionErrors()[0], handlerErr) {
			t.Fatalf("expected session error %v, got %v", handlerErr, handler.sessionErrors())
		}
	})

	t.Run("reply token from protocol decode is visible in handler context", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames:     []frame.Frame{&frame.PingPacket{}},
			consumed:   1,
			tokenBatch: []string{"reply-1"},
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return handler.frameCount() == 1 })
		if got := handler.contexts()[1].ReplyToken; got != "reply-1" {
			t.Fatalf("expected reply token reply-1, got %q", got)
		}
	})

	t.Run("protocol decode failure closes with CloseReasonProtocolError", func(t *testing.T) {
		decodeErr := errors.New("decode boom")
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{err: decodeErr})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		if got := handler.closeReasons()[0]; got != gateway.CloseReasonProtocolError {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonProtocolError, got)
		}
	})

	t.Run("OnSessionError fires before close on protocol decode failure", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{err: errors.New("decode boom")})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		if !reflect.DeepEqual(handler.callOrder(), []string{"open", "error", "close"}) {
			t.Fatalf("unexpected call order: %v", handler.callOrder())
		}
	})

	t.Run("OnSessionError fires before close on queue full and outbound overflow", func(t *testing.T) {
		t.Run("queue full", func(t *testing.T) {
			handler := newTestHandler()
			handler.onOpen = func(ctx *gateway.Context) error {
				if err := ctx.WriteFrame(&frame.PingPacket{}); err != nil {
					return err
				}
				return ctx.WriteFrame(&frame.PingPacket{})
			}

			proto := newScriptedProtocol("fake-proto")
			proto.encodedBytes = []byte("x")

			srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
				WriteQueueSize:   1,
				MaxOutboundBytes: 16,
			})
			if err := srv.Start(); err != nil {
				t.Fatalf("start failed: %v", err)
			}
			t.Cleanup(func() { _ = srv.Stop() })

			transportFactory.MustOpen("listener-a", 1)

			waitFor(t, func() bool { return handler.closeCount() == 1 })
			if !errors.Is(handler.sessionErrors()[0], session.ErrWriteQueueFull) {
				t.Fatalf("expected queue full error, got %v", handler.sessionErrors())
			}
			if got := handler.closeReasons()[0]; got != gateway.CloseReasonWriteQueueFull {
				t.Fatalf("expected %q, got %q", gateway.CloseReasonWriteQueueFull, got)
			}
			if !reflect.DeepEqual(handler.callOrder(), []string{"open", "error", "close"}) {
				t.Fatalf("unexpected call order: %v", handler.callOrder())
			}
		})

		t.Run("outbound overflow", func(t *testing.T) {
			handler := newTestHandler()
			handler.onOpen = func(ctx *gateway.Context) error {
				return ctx.WriteFrame(&frame.PingPacket{})
			}

			proto := newScriptedProtocol("fake-proto")
			proto.encodedBytes = []byte("xx")

			srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
				WriteQueueSize:   1,
				MaxOutboundBytes: 1,
			})
			if err := srv.Start(); err != nil {
				t.Fatalf("start failed: %v", err)
			}
			t.Cleanup(func() { _ = srv.Stop() })

			transportFactory.MustOpen("listener-a", 1)

			waitFor(t, func() bool { return handler.closeCount() == 1 })
			if !errors.Is(handler.sessionErrors()[0], session.ErrOutboundOverflow) {
				t.Fatalf("expected outbound overflow error, got %v", handler.sessionErrors())
			}
			if got := handler.closeReasons()[0]; got != gateway.CloseReasonOutboundOverflow {
				t.Fatalf("expected %q, got %q", gateway.CloseReasonOutboundOverflow, got)
			}
			if !reflect.DeepEqual(handler.callOrder(), []string{"open", "error", "close"}) {
				t.Fatalf("unexpected call order: %v", handler.callOrder())
			}
		})
	})

	t.Run("peer close maps to CloseReasonPeerClosed", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustClose("listener-a", 1, nil)

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		if got := handler.closeReasons()[0]; got != gateway.CloseReasonPeerClosed {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonPeerClosed, got)
		}
	})

	t.Run("Stop maps active sessions to CloseReasonServerStop", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}

		transportFactory.MustOpen("listener-a", 1)
		if err := srv.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}

		waitFor(t, func() bool { return handler.closeCount() == 1 })
		if got := handler.closeReasons()[0]; got != gateway.CloseReasonServerStop {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonServerStop, got)
		}
	})

	t.Run("close callback fires once", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustClose("listener-a", 1, nil)
		waitFor(t, func() bool { return handler.closeCount() == 1 })

		if err := srv.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}

		if got := handler.closeCount(); got != 1 {
			t.Fatalf("expected close callback once, got %d", got)
		}
		if proto.onCloseCalls() != 1 {
			t.Fatalf("expected protocol OnClose once, got %d", proto.onCloseCalls())
		}
	})
}

func TestServerStart(t *testing.T) {
	t.Run("groups listener construction by transport and assigns listeners in input order", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		factory := &buildSpyFactory{
			name: "grouped-transport",
			buildFn: func(specs []transport.ListenerSpec) ([]transport.Listener, error) {
				listeners := make([]transport.Listener, 0, len(specs))
				for _, spec := range specs {
					listeners = append(listeners, &spyListener{
						addr: "logical://" + spec.Options.Name,
					})
				}
				return listeners, nil
			},
		}

		srv := newServerWithListeners(t, handler, proto, factory, gateway.SessionOptions{}, nil, nil, []gateway.ListenerOptions{
			{
				Name:      "listener-a",
				Network:   "tcp",
				Address:   "127.0.0.1:9000",
				Transport: factory.Name(),
				Protocol:  proto.Name(),
			},
			{
				Name:      "listener-b",
				Network:   "websocket",
				Address:   "127.0.0.1:9001",
				Path:      "/ws",
				Transport: factory.Name(),
				Protocol:  proto.Name(),
			},
		})

		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}
		t.Cleanup(func() { _ = srv.Stop() })

		if got := factory.BuildCallCount(); got != 1 {
			t.Fatalf("expected one Build call, got %d", got)
		}
		specs := factory.BuildSpecs(0)
		if got := len(specs); got != 2 {
			t.Fatalf("expected two listener specs, got %d", got)
		}
		if specs[0].Options.Name != "listener-a" || specs[1].Options.Name != "listener-b" {
			t.Fatalf("unexpected listener build order: %q, %q", specs[0].Options.Name, specs[1].Options.Name)
		}
		if specs[0].Handler == nil || specs[1].Handler == nil {
			t.Fatal("expected grouped Build handlers to be populated")
		}
		if got := srv.ListenerAddr("listener-a"); got != "logical://listener-a" {
			t.Fatalf("expected listener-a logical addr, got %q", got)
		}
		if got := srv.ListenerAddr("listener-b"); got != "logical://listener-b" {
			t.Fatalf("expected listener-b logical addr, got %q", got)
		}
	})

	t.Run("rolls back started listeners when a later listener start fails", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")

		first := &spyListener{addr: "logical://listener-a"}
		second := &spyListener{addr: "logical://listener-b", startErr: errors.New("start boom")}
		factory := &buildSpyFactory{
			name: "grouped-transport",
			buildFn: func(specs []transport.ListenerSpec) ([]transport.Listener, error) {
				return []transport.Listener{first, second}, nil
			},
		}

		srv := newServerWithListeners(t, handler, proto, factory, gateway.SessionOptions{}, nil, nil, []gateway.ListenerOptions{
			{
				Name:      "listener-a",
				Network:   "tcp",
				Address:   "127.0.0.1:9000",
				Transport: factory.Name(),
				Protocol:  proto.Name(),
			},
			{
				Name:      "listener-b",
				Network:   "tcp",
				Address:   "127.0.0.1:9001",
				Transport: factory.Name(),
				Protocol:  proto.Name(),
			},
		})

		err := srv.Start()
		if err == nil || err.Error() != "start boom" {
			t.Fatalf("expected start boom, got %v", err)
		}
		if !first.Started() {
			t.Fatal("expected first listener to start before rollback")
		}
		if !first.Stopped() {
			t.Fatal("expected first listener to be stopped during rollback")
		}
		if !second.Started() {
			t.Fatal("expected second listener start to be attempted")
		}
		if !second.Stopped() {
			t.Fatal("expected failing listener to be stopped")
		}
	})

	t.Run("stops already built listeners when a later transport build fails", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")

		firstBuilt := &spyListener{addr: "logical://listener-a"}
		firstFactory := &buildSpyFactory{
			name: "transport-a",
			buildFn: func(specs []transport.ListenerSpec) ([]transport.Listener, error) {
				return []transport.Listener{firstBuilt}, nil
			},
		}
		secondFactory := &buildSpyFactory{
			name: "transport-b",
			buildFn: func(specs []transport.ListenerSpec) ([]transport.Listener, error) {
				return nil, errors.New("build boom")
			},
		}

		registry := core.NewRegistry()
		if err := registry.RegisterTransport(firstFactory); err != nil {
			t.Fatalf("register first transport failed: %v", err)
		}
		if err := registry.RegisterTransport(secondFactory); err != nil {
			t.Fatalf("register second transport failed: %v", err)
		}
		if err := registry.RegisterProtocol(proto); err != nil {
			t.Fatalf("register protocol failed: %v", err)
		}

		srv, err := core.NewServer(registry, &gateway.Options{
			Handler: handler,
			Listeners: []gateway.ListenerOptions{
				{
					Name:      "listener-a",
					Network:   "tcp",
					Address:   "127.0.0.1:9000",
					Transport: firstFactory.Name(),
					Protocol:  proto.Name(),
				},
				{
					Name:      "listener-b",
					Network:   "tcp",
					Address:   "127.0.0.1:9001",
					Transport: secondFactory.Name(),
					Protocol:  proto.Name(),
				},
			},
		})
		if err != nil {
			t.Fatalf("new server failed: %v", err)
		}

		err = srv.Start()
		if err == nil || err.Error() != "build boom" {
			t.Fatalf("expected build boom, got %v", err)
		}
		if !firstBuilt.Stopped() {
			t.Fatal("expected already built listener to be stopped after later build failure")
		}
	})
}

func TestServerStop(t *testing.T) {
	t.Run("fake transport still drives callbacks through grouped listeners", func(t *testing.T) {
		handler := newTestHandler()
		proto := newScriptedProtocol("fake-proto")
		proto.pushDecode(decodeResult{
			frames:   []frame.Frame{&frame.PingPacket{}},
			consumed: 1,
		})

		srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{})
		if err := srv.Start(); err != nil {
			t.Fatalf("start failed: %v", err)
		}

		transportFactory.MustOpen("listener-a", 1)
		transportFactory.MustData("listener-a", 1, []byte("x"))
		listenerErr := errors.New("listener boom")
		transportFactory.MustError("listener-a", listenerErr)
		transportFactory.MustClose("listener-a", 1, nil)

		waitFor(t, func() bool {
			return handler.frameCount() == 1 && handler.closeCount() == 1 && len(handler.listenerErrors()) == 1
		})
		if err := srv.Stop(); err != nil {
			t.Fatalf("stop failed: %v", err)
		}
		reasons := handler.closeReasons()
		if got := reasons[len(reasons)-1]; got != gateway.CloseReasonPeerClosed {
			t.Fatalf("expected %q, got %q", gateway.CloseReasonPeerClosed, got)
		}
		if got := handler.listenerErrors()[0]; got.Listener != "listener-a" || !errors.Is(got.Err, listenerErr) {
			t.Fatalf("unexpected listener error: %+v", got)
		}
	})
}

func TestIdleTimeout(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("fake-proto")

	srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
		IdleTimeout: 30 * time.Millisecond,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	transportFactory.MustOpen("listener-a", 1)

	waitFor(t, func() bool { return handler.closeCount() == 1 })
	reasons := handler.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonIdleTimeout {
		t.Fatalf("expected %q, got %q", gateway.CloseReasonIdleTimeout, got)
	}
	if len(handler.sessionErrors()) != 1 {
		t.Fatalf("expected one session error, got %d", len(handler.sessionErrors()))
	}
}

func TestOutboundTrafficDoesNotRefreshIdleTimeout(t *testing.T) {
	handler := newTestHandler()
	stopWrites := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			close(stopWrites)
		})
	}
	t.Cleanup(stop)
	handler.onOpen = func(ctx *gateway.Context) error {
		go func() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopWrites:
					return
				case <-ticker.C:
					_ = ctx.WriteFrame(&frame.PingPacket{})
				}
			}
		}()
		return nil
	}
	handler.onClose = func(*gateway.Context) error {
		stop()
		return nil
	}

	proto := newScriptedProtocol("fake-proto")
	proto.encodedBytes = []byte("x")

	srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
		IdleTimeout: 60 * time.Millisecond,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	conn := transportFactory.MustOpen("listener-a", 1)

	waitFor(t, func() bool { return len(conn.Writes()) >= 2 })
	waitFor(t, func() bool { return handler.closeCount() == 1 })
	reasons := handler.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonIdleTimeout {
		t.Fatalf("expected %q, got %q", gateway.CloseReasonIdleTimeout, got)
	}
}

func TestInboundTrafficRefreshesIdleTimeout(t *testing.T) {
	handler := newTestHandler()
	proto := newScriptedProtocol("fake-proto")

	srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
		IdleTimeout: 80 * time.Millisecond,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	transportFactory.MustOpen("listener-a", 1)

	time.Sleep(45 * time.Millisecond)
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.PingPacket{}},
		consumed: 1,
	})
	transportFactory.MustData("listener-a", 1, []byte("p"))

	time.Sleep(50 * time.Millisecond)
	if got := handler.closeCount(); got != 0 {
		t.Fatalf("expected connection to stay open after inbound refresh, got close count %d", got)
	}

	waitFor(t, func() bool { return handler.closeCount() == 1 })
	reasons := handler.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonIdleTimeout {
		t.Fatalf("expected %q, got %q", gateway.CloseReasonIdleTimeout, got)
	}
}

func TestWriteTimeout(t *testing.T) {
	handler := newTestHandler()
	handler.onFrame = func(ctx *gateway.Context, f frame.Frame) error {
		return ctx.WriteFrame(&frame.PingPacket{})
	}

	proto := newScriptedProtocol("fake-proto")
	proto.encodedBytes = []byte("x")
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.PingPacket{}},
		consumed: 1,
	})

	srv, transportFactory := newTestServer(t, handler, proto, gateway.SessionOptions{
		WriteTimeout: 30 * time.Millisecond,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	conn := transportFactory.MustOpen("listener-a", 1)
	conn.BlockWrites()
	transportFactory.MustData("listener-a", 1, []byte("x"))

	waitFor(t, func() bool { return handler.closeCount() == 1 })
	reasons := handler.closeReasons()
	if got := reasons[len(reasons)-1]; got != gateway.CloseReasonPolicyTimeout {
		t.Fatalf("expected %q, got %q", gateway.CloseReasonPolicyTimeout, got)
	}
	if len(handler.sessionErrors()) != 1 {
		t.Fatalf("expected one session error, got %d", len(handler.sessionErrors()))
	}
	if !errors.Is(handler.sessionErrors()[0], gateway.ErrWriteTimeout) {
		t.Fatalf("expected write timeout error, got %v", handler.sessionErrors()[0])
	}
}

func TestObserverReceivesGatewayLifecycleEvents(t *testing.T) {
	handler := newTestHandler()
	handler.onFrame = func(ctx *gateway.Context, f frame.Frame) error {
		return ctx.WriteFrame(&frame.RecvPacket{Payload: []byte("ack")})
	}

	proto := newScriptedProtocol("wkproto")
	proto.encodedBytes = []byte("encoded")
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.ConnectPacket{UID: "u1"}},
		consumed: 1,
	})

	observer := &recordingObserver{}
	srv, transportFactory := newTestServerWithObserver(
		t,
		handler,
		proto,
		gateway.SessionOptions{},
		gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
		}),
		observer,
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Stop()
	})

	conn := transportFactory.MustOpen("listener-a", 1)
	transportFactory.MustData("listener-a", 1, []byte("c"))
	proto.pushDecode(decodeResult{
		frames:   []frame.Frame{&frame.SendPacket{Payload: []byte("hello")}},
		consumed: 1,
	})
	transportFactory.MustData("listener-a", 1, []byte("s"))
	waitFor(t, func() bool {
		return observer.openCount() == 1 &&
			observer.authCount() == 1 &&
			observer.inboundCount() == 1 &&
			observer.outboundCount() >= 2 &&
			observer.handledCount() == 1 &&
			len(conn.Writes()) >= 2
	})

	transportFactory.MustClose("listener-a", 1, nil)
	waitFor(t, func() bool { return observer.closeCount() == 1 })

	require.Equal(t, "tcp", observer.opens[0].Protocol)
	require.Equal(t, "ok", observer.auths[0].Status)
	require.Equal(t, "SEND", observer.inbound[0].FrameType)
	require.Equal(t, 5, observer.inbound[0].Bytes)
	require.Equal(t, "RECV", observer.outbound[len(observer.outbound)-1].FrameType)
	require.Equal(t, 3, observer.outbound[len(observer.outbound)-1].Bytes)
	require.Equal(t, "SEND", observer.handled[0].FrameType)
}

func newTestServer(t *testing.T, handler *testHandler, proto *scriptedProtocol, sessOpts gateway.SessionOptions) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()
	return newTestServerWithAuthenticator(t, handler, proto, sessOpts, nil)
}

func newTestServerWithAuthenticator(t *testing.T, handler *testHandler, proto *scriptedProtocol, sessOpts gateway.SessionOptions, authenticator gateway.Authenticator) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()

	transportFactory := testkit.NewFakeTransportFactory("fake-transport")
	srv := newServerWithListeners(t, handler, proto, transportFactory, sessOpts, authenticator, nil, []gateway.ListenerOptions{
		{
			Name:      "listener-a",
			Network:   "tcp",
			Address:   "127.0.0.1:9000",
			Transport: transportFactory.Name(),
			Protocol:  proto.Name(),
		},
	})
	return srv, transportFactory
}

func newTestServerWithObserver(t *testing.T, handler *testHandler, proto *scriptedProtocol, sessOpts gateway.SessionOptions, authenticator gateway.Authenticator, observer gateway.Observer) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()

	transportFactory := testkit.NewFakeTransportFactory("fake-transport")
	srv := newServerWithListeners(t, handler, proto, transportFactory, sessOpts, authenticator, observer, []gateway.ListenerOptions{
		{
			Name:      "listener-a",
			Network:   "tcp",
			Address:   "127.0.0.1:9000",
			Transport: transportFactory.Name(),
			Protocol:  proto.Name(),
		},
	})
	return srv, transportFactory
}

func newServerWithListeners(t *testing.T, handler *testHandler, proto *scriptedProtocol, transportFactory transport.Factory, sessOpts gateway.SessionOptions, authenticator gateway.Authenticator, observer gateway.Observer, listeners []gateway.ListenerOptions) *core.Server {
	t.Helper()

	registry := core.NewRegistry()
	if err := registry.RegisterTransport(transportFactory); err != nil {
		t.Fatalf("register transport failed: %v", err)
	}
	if err := registry.RegisterProtocol(proto); err != nil {
		t.Fatalf("register protocol failed: %v", err)
	}

	srv, err := core.NewServer(registry, &gateway.Options{
		Handler:        handler,
		Authenticator:  authenticator,
		DefaultSession: sessOpts,
		Observer:       observer,
		Listeners:      append([]gateway.ListenerOptions(nil), listeners...),
	})
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}
	return srv
}

type recordingObserver struct {
	mu       sync.Mutex
	opens    []gateway.ConnectionEvent
	closes   []gateway.ConnectionEvent
	auths    []gateway.AuthEvent
	inbound  []gateway.FrameEvent
	outbound []gateway.FrameEvent
	handled  []gateway.FrameHandleEvent
}

func (r *recordingObserver) OnConnectionOpen(event gateway.ConnectionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opens = append(r.opens, event)
}

func (r *recordingObserver) OnConnectionClose(event gateway.ConnectionEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes = append(r.closes, event)
}

func (r *recordingObserver) OnAuth(event gateway.AuthEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auths = append(r.auths, event)
}

func (r *recordingObserver) OnFrameIn(event gateway.FrameEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inbound = append(r.inbound, event)
}

func (r *recordingObserver) OnFrameOut(event gateway.FrameEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outbound = append(r.outbound, event)
}

func (r *recordingObserver) OnFrameHandled(event gateway.FrameHandleEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handled = append(r.handled, event)
}

func (r *recordingObserver) openCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.opens)
}

func (r *recordingObserver) closeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.closes)
}

func (r *recordingObserver) authCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.auths)
}

func (r *recordingObserver) inboundCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inbound)
}

func (r *recordingObserver) outboundCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.outbound)
}

func (r *recordingObserver) handledCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.handled)
}

type decodeResult struct {
	frames     []frame.Frame
	consumed   int
	err        error
	tokenBatch []string
}

type scriptedProtocol struct {
	name string

	mu           sync.Mutex
	decodeQueue  []decodeResult
	tokenQueue   [][]string
	encodedBytes []byte
	encodeFn     func(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error)
	encodeErr    error
	openErr      error
	closeErr     error
	closeCalls   int
}

func newScriptedProtocol(name string) *scriptedProtocol {
	return &scriptedProtocol{name: name}
}

func (p *scriptedProtocol) Name() string { return p.name }

func (p *scriptedProtocol) Decode(_ session.Session, _ []byte) ([]frame.Frame, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.decodeQueue) == 0 {
		return nil, 0, nil
	}
	step := p.decodeQueue[0]
	p.decodeQueue = p.decodeQueue[1:]
	if len(step.tokenBatch) > 0 {
		p.tokenQueue = append(p.tokenQueue, append([]string(nil), step.tokenBatch...))
	}
	return append([]frame.Frame(nil), step.frames...), step.consumed, step.err
}

func (p *scriptedProtocol) Encode(sess session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.encodeFn != nil {
		return p.encodeFn(sess, f, meta)
	}
	if p.encodeErr != nil {
		return nil, p.encodeErr
	}
	return append([]byte(nil), p.encodedBytes...), nil
}

func (p *scriptedProtocol) OnOpen(session.Session) error { return p.openErr }

func (p *scriptedProtocol) OnClose(session.Session) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCalls++
	return p.closeErr
}

func (p *scriptedProtocol) TakeReplyTokens(session.Session, int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tokenQueue) == 0 {
		return nil
	}
	tokens := append([]string(nil), p.tokenQueue[0]...)
	p.tokenQueue = p.tokenQueue[1:]
	return tokens
}

func (p *scriptedProtocol) pushDecode(step decodeResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.decodeQueue = append(p.decodeQueue, step)
}

func (p *scriptedProtocol) onCloseCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeCalls
}

type testHandler struct {
	mu sync.Mutex

	order          []string
	listenerErrs   []testkit.ListenerError
	sessionErrs    []error
	closeReasonLog []gateway.CloseReason
	framesSeen     []frame.Frame
	contextCopies  []gateway.Context

	onOpen     func(*gateway.Context) error
	onActivate func(*gateway.Context) (*frame.ConnackPacket, error)
	onFrame    func(*gateway.Context, frame.Frame) error
	onClose    func(*gateway.Context) error
}

func newTestHandler() *testHandler { return &testHandler{} }

func (h *testHandler) OnListenerError(listener string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.order = append(h.order, "listener_error")
	h.listenerErrs = append(h.listenerErrs, testkit.ListenerError{Listener: listener, Err: err})
}

func (h *testHandler) OnSessionOpen(ctx *gateway.Context) error {
	h.mu.Lock()
	h.order = append(h.order, "open")
	if ctx != nil {
		h.contextCopies = append(h.contextCopies, *ctx)
	}
	fn := h.onOpen
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (h *testHandler) OnSessionActivate(ctx *gateway.Context) (*frame.ConnackPacket, error) {
	fn := h.onActivate
	if fn == nil {
		return nil, nil
	}

	h.mu.Lock()
	h.order = append(h.order, "activate")
	if ctx != nil {
		h.contextCopies = append(h.contextCopies, *ctx)
	}
	h.mu.Unlock()
	return fn(ctx)
}

func (h *testHandler) OnFrame(ctx *gateway.Context, f frame.Frame) error {
	h.mu.Lock()
	h.order = append(h.order, "frame")
	if ctx != nil {
		h.contextCopies = append(h.contextCopies, *ctx)
		h.closeReasonLog = append(h.closeReasonLog, ctx.CloseReason)
	}
	h.framesSeen = append(h.framesSeen, f)
	fn := h.onFrame
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, f)
	}
	return nil
}

func (h *testHandler) OnSessionClose(ctx *gateway.Context) error {
	h.mu.Lock()
	h.order = append(h.order, "close")
	if ctx != nil {
		h.contextCopies = append(h.contextCopies, *ctx)
		h.closeReasonLog = append(h.closeReasonLog, ctx.CloseReason)
	}
	fn := h.onClose
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

func (h *testHandler) OnSessionError(ctx *gateway.Context, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.order = append(h.order, "error")
	if ctx != nil {
		h.contextCopies = append(h.contextCopies, *ctx)
		h.closeReasonLog = append(h.closeReasonLog, ctx.CloseReason)
	}
	h.sessionErrs = append(h.sessionErrs, err)
}

func (h *testHandler) frameCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.framesSeen)
}

func (h *testHandler) closeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, call := range h.order {
		if call == "close" {
			count++
		}
	}
	return count
}

func (h *testHandler) frames() []frame.Frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]frame.Frame(nil), h.framesSeen...)
}

func (h *testHandler) closeReasons() []gateway.CloseReason {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gateway.CloseReason(nil), h.closeReasonLog...)
}

func (h *testHandler) contexts() []gateway.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gateway.Context(nil), h.contextCopies...)
}

func (h *testHandler) callOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.order...)
}

func (h *testHandler) listenerErrors() []testkit.ListenerError {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]testkit.ListenerError(nil), h.listenerErrs...)
}

func (h *testHandler) sessionErrors() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.sessionErrs...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func connClosed(conn *testkit.FakeConn) bool {
	if conn == nil {
		return false
	}

	select {
	case <-conn.CloseCh():
		return true
	default:
		return false
	}
}

func boolPtr(v bool) *bool { return &v }

type buildSpyFactory struct {
	name    string
	buildFn func([]transport.ListenerSpec) ([]transport.Listener, error)

	mu         sync.Mutex
	buildCalls [][]transport.ListenerSpec
}

func (f *buildSpyFactory) Name() string {
	if f == nil {
		return ""
	}
	return f.name
}

func (f *buildSpyFactory) Build(specs []transport.ListenerSpec) ([]transport.Listener, error) {
	if f == nil {
		return nil, nil
	}

	f.mu.Lock()
	copied := append([]transport.ListenerSpec(nil), specs...)
	f.buildCalls = append(f.buildCalls, copied)
	fn := f.buildFn
	f.mu.Unlock()

	if fn == nil {
		return nil, nil
	}
	return fn(specs)
}

func (f *buildSpyFactory) BuildCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.buildCalls)
}

func (f *buildSpyFactory) BuildSpecs(idx int) []transport.ListenerSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.buildCalls) {
		return nil
	}
	return append([]transport.ListenerSpec(nil), f.buildCalls[idx]...)
}

type spyListener struct {
	addr     string
	startErr error
	stopErr  error

	mu      sync.Mutex
	started bool
	stopped bool
}

func (l *spyListener) Start() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = true
	return l.startErr
}

func (l *spyListener) Stop() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	return l.stopErr
}

func (l *spyListener) Addr() string {
	if l == nil {
		return ""
	}
	return l.addr
}

func (l *spyListener) Started() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.started
}

func (l *spyListener) Stopped() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped
}
