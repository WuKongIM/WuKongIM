//go:build integration

package gateway_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gorilla/websocket"
)

func TestGatewayJSONRPCConnectsBeforeDispatchForDirectAndMuxListeners(t *testing.T) {
	tests := []struct {
		name     string
		listener gateway.ListenerOptions
	}{
		{
			name:     "direct jsonrpc",
			listener: binding.WSJSONRPC("ws-jsonrpc", "127.0.0.1:0"),
		},
		{
			name: "wsmux selected jsonrpc",
			listener: gateway.ListenerOptions{
				Name:      "ws-wsmux",
				Network:   "websocket",
				Address:   "127.0.0.1:0",
				Transport: "gnet",
				Protocol:  "wsmux",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &jsonrpcIntegrationHandler{}
			gw := startJSONRPCIntegrationGateway(t, handler, tt.listener)
			conn := dialJSONRPCIntegrationGateway(t, gw, tt.listener.Name)

			if got := handler.opened(); got != 0 {
				t.Fatalf("session opened before CONNECT: %d", got)
			}

			writeJSONRPCIntegrationMessage(t, conn, websocket.TextMessage, `{"jsonrpc":"2.0","method":"connect","params":{"uid":"alice","token":"good-token","device_id":"android-device","device_flag":0,"client_timestamp":1700000000000},"id":"connect-android"}`)
			response := readJSONRPCIntegrationResponse(t, conn)
			assertJSONRPCIntegrationID(t, response, "connect-android")
			assertJSONRPCIntegrationSuccessReason(t, response)

			waitForJSONRPCIntegrationOpen(t, handler)
			if uid := handler.uid(); uid != "alice" {
				t.Fatalf("session uid = %q, want alice", uid)
			}
			if protocol := handler.protocol(); protocol != "jsonrpc" {
				t.Fatalf("session protocol = %q, want jsonrpc", protocol)
			}

			writeJSONRPCIntegrationMessage(t, conn, websocket.TextMessage, `{"jsonrpc":"2.0","method":"ping","id":"ping-android"}`)
			pong := readJSONRPCIntegrationResponse(t, conn)
			assertJSONRPCIntegrationID(t, pong, "ping-android")
			if result, ok := pong["result"]; !ok || string(result) != "null" {
				t.Fatalf("pong result = %s, want explicit null", result)
			}
		})
	}
}

func TestGatewayJSONRPCRejectsBadTokenWithCorrelatedError(t *testing.T) {
	handler := &jsonrpcIntegrationHandler{}
	listener := binding.WSJSONRPC("ws-jsonrpc-auth", "127.0.0.1:0")
	gw := startJSONRPCIntegrationGateway(t, handler, listener)
	conn := dialJSONRPCIntegrationGateway(t, gw, listener.Name)

	writeJSONRPCIntegrationMessage(t, conn, websocket.BinaryMessage, `{"jsonrpc":"2.0","method":"connect","params":{"uid":"alice","token":"bad-token","deviceId":"ios-device","deviceFlag":0},"id":"connect-ios"}`)
	response := readJSONRPCIntegrationResponse(t, conn)
	assertJSONRPCIntegrationID(t, response, "connect-ios")
	if _, ok := response["result"]; ok {
		t.Fatalf("failed CONNECT returned result: %s", response["result"])
	}
	var rpcError struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(response["error"], &rpcError); err != nil {
		t.Fatalf("decode error response: %v; response=%s", err, response["error"])
	}
	if rpcError.Code != int(frame.ReasonAuthFail) {
		t.Fatalf("error code = %d, want %d", rpcError.Code, frame.ReasonAuthFail)
	}
	if got := handler.opened(); got != 0 {
		t.Fatalf("rejected session opened: %d", got)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("rejected JSON-RPC session remained open")
	}
}

func TestGatewayJSONRPCRejectsRequestsBeforeConnect(t *testing.T) {
	handler := &jsonrpcIntegrationHandler{}
	listener := binding.WSJSONRPC("ws-jsonrpc-connect-first", "127.0.0.1:0")
	gw := startJSONRPCIntegrationGateway(t, handler, listener)
	conn := dialJSONRPCIntegrationGateway(t, gw, listener.Name)

	writeJSONRPCIntegrationMessage(t, conn, websocket.TextMessage, `{"jsonrpc":"2.0","method":"ping","id":"too-early"}`)
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("JSON-RPC request before CONNECT did not close the session")
	}
	if got := handler.opened(); got != 0 {
		t.Fatalf("session opened before CONNECT: %d", got)
	}
	if got := handler.frames(); got != 0 {
		t.Fatalf("request before CONNECT reached handler: %d", got)
	}
}

type jsonrpcIntegrationHandler struct {
	mu            sync.Mutex
	openCount     int
	frameCount    int
	openedChanged chan struct{}
	openedUID     string
	openedProto   string
}

func (*jsonrpcIntegrationHandler) OnListenerError(string, error) {}

func (h *jsonrpcIntegrationHandler) OnSessionOpen(ctx gateway.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.openCount++
	h.openedUID, _ = ctx.Session.Value(gateway.SessionValueUID).(string)
	h.openedProto = ctx.Protocol
	if h.openedChanged != nil {
		close(h.openedChanged)
		h.openedChanged = nil
	}
	return nil
}

func (h *jsonrpcIntegrationHandler) OnFrame(ctx gateway.Context, got frame.Frame) error {
	h.mu.Lock()
	h.frameCount++
	h.mu.Unlock()
	if _, ok := got.(*frame.PingPacket); ok {
		return ctx.WriteFrame(&frame.PongPacket{})
	}
	return nil
}

func (*jsonrpcIntegrationHandler) OnSessionClose(gateway.Context) error  { return nil }
func (*jsonrpcIntegrationHandler) OnSessionError(gateway.Context, error) {}

func (h *jsonrpcIntegrationHandler) opened() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openCount
}

func (h *jsonrpcIntegrationHandler) frames() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frameCount
}

func (h *jsonrpcIntegrationHandler) uid() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openedUID
}

func (h *jsonrpcIntegrationHandler) protocol() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openedProto
}

func (h *jsonrpcIntegrationHandler) waitForOpen(deadline time.Time) bool {
	for {
		h.mu.Lock()
		if h.openCount > 0 {
			h.mu.Unlock()
			return true
		}
		if h.openedChanged == nil {
			h.openedChanged = make(chan struct{})
		}
		changed := h.openedChanged
		h.mu.Unlock()

		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			return false
		}
	}
}

func startJSONRPCIntegrationGateway(t *testing.T, handler gateway.Handler, listener gateway.ListenerOptions) *gateway.Gateway {
	t.Helper()
	gw, err := gateway.New(gateway.Options{
		Handler: handler,
		Authenticator: gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
			TokenAuthOn: true,
			NodeID:      42,
			VerifyToken: func(uid string, _ frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
				if uid == "alice" && token == "good-token" {
					return frame.DeviceLevelMaster, nil
				}
				return 0, errors.New("invalid token")
			},
		}),
		Listeners: []gateway.ListenerOptions{listener},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = gw.Stop() })
	return gw
}

func dialJSONRPCIntegrationGateway(t *testing.T, gw *gateway.Gateway, listener string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+gw.ListenerAddr(listener), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func writeJSONRPCIntegrationMessage(t *testing.T, conn *websocket.Conn, messageType int, payload string) {
	t.Helper()
	if err := conn.WriteMessage(messageType, []byte(payload)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
}

func readJSONRPCIntegrationResponse(t *testing.T, conn *websocket.Conn) map[string]json.RawMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode response %q: %v", payload, err)
	}
	return response
}

func assertJSONRPCIntegrationID(t *testing.T, response map[string]json.RawMessage, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(response["id"], &got); err != nil {
		t.Fatalf("decode response id: %v; response=%v", err, response)
	}
	if got != want {
		t.Fatalf("response id = %q, want %q", got, want)
	}
}

func assertJSONRPCIntegrationSuccessReason(t *testing.T, response map[string]json.RawMessage) {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal(response["result"], &result); err != nil {
		t.Fatalf("decode result: %v; response=%s", err, response["result"])
	}
	reason := result["reasonCode"]
	if len(reason) == 0 {
		reason = result["reason_code"]
	}
	var got int
	if err := json.Unmarshal(reason, &got); err != nil {
		t.Fatalf("decode reason code: %v; result=%v", err, result)
	}
	if got != int(frame.ReasonSuccess) {
		t.Fatalf("reason code = %d, want %d", got, frame.ReasonSuccess)
	}
}

func waitForJSONRPCIntegrationOpen(t *testing.T, handler *jsonrpcIntegrationHandler) {
	t.Helper()
	if !handler.waitForOpen(time.Now().Add(3 * time.Second)) {
		t.Fatal("session did not open after CONNECT")
	}
}
