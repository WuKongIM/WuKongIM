package gateway_test

import (
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	codec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/jsonrpc"
	"github.com/gorilla/websocket"
)

func TestGatewayStartStopTCPWKProto(t *testing.T) {
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
	t.Cleanup(func() { _ = gw.Stop() })

	conn, err := net.Dial("tcp", gw.ListenerAddr("tcp-wkproto"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	payload, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
}

func TestGatewayWKProtoAuthRejectsBadToken(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
			TokenAuthOn: true,
			VerifyToken: func(uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
				if uid == "u1" && token == "good-token" {
					return frame.DeviceLevelMaster, nil
				}
				return 0, errors.New("token verify fail")
			},
		}),
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto-auth", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialTCPGateway(t, gw, "tcp-wkproto-auth")
	t.Cleanup(func() { _ = conn.Close() })

	connack := mustConnectWKProto(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		Token:           "bad-token",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	if connack.ReasonCode != frame.ReasonAuthFail {
		t.Fatalf("expected auth fail connack, got %v", connack.ReasonCode)
	}
	assertConnClosed(t, conn)
	if got := handler.FrameCount(); got != 0 {
		t.Fatalf("expected handler to see no frames, got %d", got)
	}
}

func TestGatewayWKProtoAuthRejectsBannedUID(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
			TokenAuthOn: false,
			IsBanned: func(uid string) (bool, error) {
				return uid == "banned-user", nil
			},
		}),
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto-ban", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialTCPGateway(t, gw, "tcp-wkproto-ban")
	t.Cleanup(func() { _ = conn.Close() })

	connack := mustConnectWKProto(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "banned-user",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	if connack.ReasonCode != frame.ReasonBan {
		t.Fatalf("expected ban connack, got %v", connack.ReasonCode)
	}
	assertConnClosed(t, conn)
	if got := handler.FrameCount(); got != 0 {
		t.Fatalf("expected handler to see no frames, got %d", got)
	}
}

func TestGatewayWKProtoAuthAcceptsConnectBeforeDispatchingFrames(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
			TokenAuthOn: true,
			NodeID:      42,
			Now: func() time.Time {
				return time.UnixMilli(10_000)
			},
			VerifyToken: func(uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
				if uid == "u1" && token == "good-token" {
					return frame.DeviceLevelMaster, nil
				}
				return 0, errors.New("token verify fail")
			},
		}),
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto-auth-ok", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialTCPGateway(t, gw, "tcp-wkproto-auth-ok")
	t.Cleanup(func() { _ = conn.Close() })

	connack := mustConnectWKProto(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		Token:           "good-token",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: 9_000,
	})
	if connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("expected success connack, got %v", connack.ReasonCode)
	}
	if connack.NodeId != 42 || connack.TimeDiff != 1000 {
		t.Fatalf("unexpected connack: %+v", connack)
	}

	payload, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
	if got := handler.Contexts[0].Session.Value(gateway.SessionValueUID); got != "u1" {
		t.Fatalf("expected session uid to be stored, got %#v", got)
	}
	if got := handler.Contexts[0].Session.Value(gateway.SessionValueDeviceLevel); got != frame.DeviceLevelMaster {
		t.Fatalf("expected device level to be stored, got %#v", got)
	}
}

func TestGatewayWKProtoActivationSeesDeviceIDBeforeConnectSucceeds(t *testing.T) {
	handler := &activationHandler{}
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
			TokenAuthOn: true,
			VerifyToken: func(uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
				if uid == "u1" && token == "good-token" {
					return frame.DeviceLevelMaster, nil
				}
				return 0, errors.New("token verify fail")
			},
		}),
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto-activate", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialTCPGateway(t, gw, "tcp-wkproto-activate")
	t.Cleanup(func() { _ = conn.Close() })

	connack := mustConnectWKProto(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		Token:           "good-token",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	if connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("expected success connack, got %v", connack.ReasonCode)
	}

	waitUntil(t, time.Second, func() bool { return handler.openSeen() })
	if got := handler.deviceID(); got != "d1" {
		t.Fatalf("expected activation to see device id, got %#v", got)
	}
	if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "open"}) {
		t.Fatalf("unexpected call order: %v", got)
	}
}

func TestGatewayWKProtoActivationSeesDeviceIDWithCustomAuthenticator(t *testing.T) {
	handler := &activationHandler{}
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
			}, nil
		}),
		Listeners: []gateway.ListenerOptions{
			binding.TCPWKProto("tcp-wkproto-custom-auth", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn := dialTCPGateway(t, gw, "tcp-wkproto-custom-auth")
	t.Cleanup(func() { _ = conn.Close() })

	connack := mustConnectWKProto(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})
	if connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("expected success connack, got %v", connack.ReasonCode)
	}

	waitUntil(t, time.Second, func() bool { return handler.openSeen() })
	if got := handler.deviceID(); got != "d1" {
		t.Fatalf("expected activation to see device id, got %#v", got)
	}
	if got := handler.callOrder(); !reflect.DeepEqual(got, []string{"activate", "open"}) {
		t.Fatalf("unexpected call order: %v", got)
	}
}

type activationHandler struct {
	mu            sync.Mutex
	order         []string
	deviceIDValue string
	open          bool
}

func (h *activationHandler) OnListenerError(string, error) {}

func (h *activationHandler) OnSessionActivate(ctx *gateway.Context) (*frame.ConnackPacket, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.order = append(h.order, "activate")
	if ctx != nil && ctx.Session != nil {
		if got := ctx.Session.Value(gateway.SessionValueDeviceID); got != nil {
			h.deviceIDValue, _ = got.(string)
		}
	}
	return nil, nil
}

func (h *activationHandler) OnSessionOpen(ctx *gateway.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.order = append(h.order, "open")
	h.open = true
	return nil
}

func (h *activationHandler) OnFrame(*gateway.Context, frame.Frame) error { return nil }
func (h *activationHandler) OnSessionClose(*gateway.Context) error       { return nil }
func (h *activationHandler) OnSessionError(*gateway.Context, error)      {}

func (h *activationHandler) deviceID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deviceIDValue
}

func (h *activationHandler) openSeen() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open
}

func (h *activationHandler) callOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.order...)
}

func TestGatewayStartStopWSJSONRPC(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Listeners: []gateway.ListenerOptions{
			binding.WSJSONRPC("ws-jsonrpc", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	url := "ws://" + gw.ListenerAddr("ws-jsonrpc")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	payload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "1",
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
}

func TestGatewayStartStopTCPWKProtoOverStdnet(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Listeners: []gateway.ListenerOptions{
			{
				Name:      "tcp-wkproto-stdnet",
				Network:   "tcp",
				Address:   "127.0.0.1:0",
				Transport: "stdnet",
				Protocol:  "wkproto",
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	conn, err := net.Dial("tcp", gw.ListenerAddr("tcp-wkproto-stdnet"))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	payload, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
}

func TestGatewayStartStopWSJSONRPCOverStdnet(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Listeners: []gateway.ListenerOptions{
			{
				Name:      "ws-jsonrpc-stdnet",
				Network:   "websocket",
				Address:   "127.0.0.1:0",
				Transport: "stdnet",
				Protocol:  "jsonrpc",
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	url := "ws://" + gw.ListenerAddr("ws-jsonrpc-stdnet")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	payload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "1",
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
}

func TestGatewayWSMuxSupportsJSONRPCAndWKProto(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler:       handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{}),
		Listeners: []gateway.ListenerOptions{
			{
				Name:      "ws-gateway",
				Network:   "websocket",
				Address:   "127.0.0.1:0",
				Transport: "gnet",
				Protocol:  "wsmux",
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	url := "ws://" + gw.ListenerAddr("ws-gateway")

	jsonConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(jsonrpc): %v", err)
	}
	t.Cleanup(func() { _ = jsonConn.Close() })

	jsonPayload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "json-1",
		},
	})
	if err != nil {
		t.Fatalf("Encode(jsonrpc): %v", err)
	}
	if err := jsonConn.WriteMessage(websocket.TextMessage, jsonPayload); err != nil {
		t.Fatalf("WriteMessage(jsonrpc): %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
	waitUntil(t, time.Second, func() bool { return handlerHasProtocol(handler, "jsonrpc") })

	wkConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(wkproto): %v", err)
	}
	t.Cleanup(func() { _ = wkConn.Close() })

	_ = mustConnectWKProtoWS(t, wkConn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})

	pingPayload, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(wkproto ping): %v", err)
	}
	if err := wkConn.WriteMessage(websocket.BinaryMessage, pingPayload); err != nil {
		t.Fatalf("WriteMessage(wkproto ping): %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 2 })
	waitUntil(t, time.Second, func() bool { return handlerHasProtocol(handler, "wkproto") })
}

func TestGatewayWSMuxSupportsJSONRPCAndWKProtoOverStdnet(t *testing.T) {
	handler := testkit.NewRecordingHandler()
	gw, err := gateway.New(gateway.Options{
		Handler:       handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{}),
		Listeners: []gateway.ListenerOptions{
			{
				Name:      "ws-gateway-stdnet",
				Network:   "websocket",
				Address:   "127.0.0.1:0",
				Transport: "stdnet",
				Protocol:  "wsmux",
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })

	url := "ws://" + gw.ListenerAddr("ws-gateway-stdnet")

	jsonConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(jsonrpc): %v", err)
	}
	t.Cleanup(func() { _ = jsonConn.Close() })

	jsonPayload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "json-1",
		},
	})
	if err != nil {
		t.Fatalf("Encode(jsonrpc): %v", err)
	}
	if err := jsonConn.WriteMessage(websocket.TextMessage, jsonPayload); err != nil {
		t.Fatalf("WriteMessage(jsonrpc): %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 1 })
	waitUntil(t, time.Second, func() bool { return handlerHasProtocol(handler, "jsonrpc") })

	wkConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(wkproto): %v", err)
	}
	t.Cleanup(func() { _ = wkConn.Close() })

	_ = mustConnectWKProtoWS(t, wkConn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "u1",
		DeviceID:        "d1",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})

	pingPayload, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(wkproto ping): %v", err)
	}
	if err := wkConn.WriteMessage(websocket.BinaryMessage, pingPayload); err != nil {
		t.Fatalf("WriteMessage(wkproto ping): %v", err)
	}

	waitUntil(t, time.Second, func() bool { return handler.FrameCount() == 2 })
	waitUntil(t, time.Second, func() bool { return handlerHasProtocol(handler, "wkproto") })
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func dialTCPGateway(t *testing.T, gw *gateway.Gateway, listener string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", gw.ListenerAddr(listener))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return conn
}

func mustConnectWKProtoWS(t *testing.T, conn *websocket.Conn, connect *frame.ConnectPacket) *frame.ConnackPacket {
	t.Helper()

	client := mustWKProtoTestClient(t)
	connect, err := client.UseClientKey(connect)
	if err != nil {
		t.Fatalf("UseClientKey: %v", err)
	}

	payload, err := codec.New().EncodeFrame(connect, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("messageType = %d, want %d", messageType, websocket.BinaryMessage)
	}

	f, _, err := codec.New().DecodeFrame(data, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		t.Fatalf("frame = %T, want *frame.ConnackPacket", f)
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("connack.ReasonCode = %v, want %v", connack.ReasonCode, frame.ReasonSuccess)
	}
	if err := client.ApplyConnack(connack); err != nil {
		t.Fatalf("ApplyConnack: %v", err)
	}
	return connack
}

func handlerHasProtocol(handler *testkit.RecordingHandler, protocol string) bool {
	if handler == nil {
		return false
	}
	for _, got := range handler.Protocols() {
		if got == protocol {
			return true
		}
	}
	return false
}

func mustConnectWKProto(t *testing.T, conn net.Conn, connect *frame.ConnectPacket) *frame.ConnackPacket {
	t.Helper()

	client := mustWKProtoTestClient(t)
	connect, err := client.UseClientKey(connect)
	if err != nil {
		t.Fatalf("UseClientKey: %v", err)
	}

	payload, err := codec.New().EncodeFrame(connect, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodePacketWithConn: %v", err)
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		t.Fatalf("expected connack packet, got %T", f)
	}
	if connack.ReasonCode == frame.ReasonSuccess {
		if err := client.ApplyConnack(connack); err != nil {
			t.Fatalf("ApplyConnack: %v", err)
		}
	}
	return connack
}

func mustWKProtoTestClient(t *testing.T) *testkit.WKProtoClient {
	t.Helper()

	client, err := testkit.NewWKProtoClient()
	if err != nil {
		t.Fatalf("NewWKProtoClient: %v", err)
	}
	return client
}

func assertConnClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()

	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err == nil {
		t.Fatal("expected connection to be closed")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("expected closed connection, got timeout: %v", err)
	}
	if !errors.Is(err, io.EOF) {
		return
	}
}
