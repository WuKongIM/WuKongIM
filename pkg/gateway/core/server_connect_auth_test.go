package core_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	protojsonrpc "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/jsonrpc"
	protowsmux "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/wsmux"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestServerAuthenticatesJSONRPCConnectBeforeOpeningSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter protocol.Adapter
	}{
		{name: "direct jsonrpc", adapter: protojsonrpc.New()},
		{name: "wsmux selected jsonrpc", adapter: protowsmux.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestHandler()
			handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) { return nil, nil }
			var authCalls atomic.Int64
			var authProtocol atomic.Value
			var authUID atomic.Value
			var authDeviceID atomic.Value
			authenticator := gateway.AuthenticatorFunc(func(ctx *gateway.Context, connect *frame.ConnectPacket) (*gateway.AuthResult, error) {
				authCalls.Add(1)
				authProtocol.Store(ctx.Protocol)
				authUID.Store(connect.UID)
				authDeviceID.Store(connect.DeviceID)
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			})

			srv, factory := newBuiltinProtocolServer(t, handler, tc.adapter, authenticator, gateway.RuntimeOptions{})
			if err := srv.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			t.Cleanup(func() { _ = srv.Stop() })

			conn := factory.MustOpen("listener-a", 1)
			factory.MustData("listener-a", 1, jsonConnectRequest("connect-1", "u1"))

			waitFor(t, func() bool { return len(conn.Writes()) == 1 || handler.frameCount() > 0 })
			if got := authCalls.Load(); got != 1 {
				t.Fatalf("Authenticate calls = %d, want 1", got)
			}
			if got, _ := authProtocol.Load().(string); got != "jsonrpc" {
				t.Fatalf("auth context protocol = %q, want jsonrpc", got)
			}
			if got, _ := authUID.Load().(string); got != "u1" {
				t.Fatalf("authenticated uid = %q, want u1", got)
			}
			if got, _ := authDeviceID.Load().(string); got != "d-1" {
				t.Fatalf("authenticated device id = %q, want d-1", got)
			}
			if got := handler.frameCount(); got != 0 {
				t.Fatalf("CONNECT reached business handler %d times", got)
			}
			if got := handler.callOrder(); len(got) != 2 || got[0] != "activate" || got[1] != "open" {
				t.Fatalf("handler order = %v, want [activate open]", got)
			}
			if got := string(conn.Writes()[0]); !strings.Contains(got, `"id":"connect-1"`) {
				t.Fatalf("CONNACK response lost request id: %s", got)
			}

			factory.MustData("listener-a", 1, jsonPingRequest("ping-1"))
			waitFor(t, func() bool { return handler.frameCount() == 1 })
			if _, ok := handler.frames()[0].(*frame.PingPacket); !ok {
				t.Fatalf("post-auth frame = %T, want *frame.PingPacket", handler.frames()[0])
			}
		})
	}
}

func TestServerRejectsJSONRPCFrameBeforeConnect(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter protocol.Adapter
	}{
		{name: "direct jsonrpc", adapter: protojsonrpc.New()},
		{name: "wsmux selected jsonrpc", adapter: protowsmux.New()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestHandler()
			srv, factory := newBuiltinProtocolServer(t, handler, tc.adapter, gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{}), gateway.RuntimeOptions{})
			if err := srv.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			t.Cleanup(func() { _ = srv.Stop() })

			conn := factory.MustOpen("listener-a", 1)
			factory.MustData("listener-a", 1, jsonPingRequest("ping-1"))

			waitFor(t, func() bool { return connClosed(conn) || handler.frameCount() > 0 })
			if !connClosed(conn) {
				t.Fatal("connection stayed open after a pre-CONNECT JSON-RPC frame")
			}
			if got := handler.callOrder(); len(got) != 0 {
				t.Fatalf("handler observed unauthenticated session: %v", got)
			}
			if got := len(conn.Writes()); got != 0 {
				t.Fatalf("pre-CONNECT violation wrote %d responses, want none", got)
			}
		})
	}
}

func TestServerPreservesJSONRPCConnectIDOnAuthenticationFailures(t *testing.T) {
	for _, tc := range []struct {
		name            string
		authenticator   gateway.Authenticator
		activateConnack *frame.ConnackPacket
		activateErr     error
	}{
		{
			name: "authentication rejection",
			authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail}}, nil
			}),
		},
		{
			name: "authenticator error",
			authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				return nil, errors.New("auth failed")
			}),
		},
		{
			name: "activation error",
			authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			activateErr: errors.New("activation failed"),
		},
		{
			name: "activation rejection",
			authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
				return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			activateConnack: &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestHandler()
			if tc.activateErr != nil || tc.activateConnack != nil {
				handler.onActivate = func(*gateway.Context) (*frame.ConnackPacket, error) {
					return tc.activateConnack, tc.activateErr
				}
			}
			srv, factory := newBuiltinProtocolServer(t, handler, protojsonrpc.New(), tc.authenticator, gateway.RuntimeOptions{})
			if err := srv.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			t.Cleanup(func() { _ = srv.Stop() })

			conn := factory.MustOpen("listener-a", 1)
			factory.MustData("listener-a", 1, jsonConnectRequest("failed-connect", "u1"))

			waitFor(t, func() bool { return connClosed(conn) && len(conn.Writes()) == 1 })
			if got := string(conn.Writes()[0]); !strings.Contains(got, `"id":"failed-connect"`) {
				t.Fatalf("failure CONNACK lost request id: %s", got)
			}
		})
	}
}

func TestServerPreservesJSONRPCConnectIDWhenAuthQueueIsFull(t *testing.T) {
	handler := newTestHandler()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	authenticator := gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return &gateway.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
	})

	srv, factory := newBuiltinProtocolServer(t, handler, protojsonrpc.New(), authenticator, gateway.RuntimeOptions{
		AsyncAuthWorkers:       1,
		AsyncAuthQueueCapacity: 1,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	first := factory.MustOpen("listener-a", 1)
	factory.MustData("listener-a", 1, jsonConnectRequest("connect-1", "u1"))
	select {
	case <-started:
	case <-first.CloseCh():
		t.Fatal("first connection closed before authentication started")
	case <-time.After(time.Second):
		t.Fatal("first JSON-RPC CONNECT never entered authentication")
	}

	factory.MustOpen("listener-a", 2)
	factory.MustData("listener-a", 2, jsonConnectRequest("connect-2", "u2"))
	third := factory.MustOpen("listener-a", 3)
	factory.MustData("listener-a", 3, jsonConnectRequest("connect-3", "u3"))

	waitFor(t, func() bool { return connClosed(third) && len(third.Writes()) == 1 })
	if got := string(third.Writes()[0]); !strings.Contains(got, `"id":"connect-3"`) {
		t.Fatalf("queue-full CONNACK lost rejected request id: %s", got)
	}
	releaseOnce.Do(func() { close(release) })
}

func newBuiltinProtocolServer(t *testing.T, handler gateway.Handler, adapter protocol.Adapter, authenticator gateway.Authenticator, runtime gateway.RuntimeOptions) (*core.Server, *testkit.FakeTransportFactory) {
	t.Helper()
	factory := testkit.NewFakeTransportFactory("fake-transport")
	registry := core.NewRegistry()
	if err := registry.RegisterTransport(factory); err != nil {
		t.Fatalf("RegisterTransport() error = %v", err)
	}
	if err := registry.RegisterProtocol(adapter); err != nil {
		t.Fatalf("RegisterProtocol() error = %v", err)
	}
	srv, err := core.NewServer(registry, &gateway.Options{
		Handler:       handler,
		Authenticator: authenticator,
		Runtime:       runtime,
		Listeners: []gateway.ListenerOptions{{
			Name:      "listener-a",
			Network:   "websocket",
			Address:   "127.0.0.1:9000",
			Transport: factory.Name(),
			Protocol:  adapter.Name(),
		}},
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return srv, factory
}

func jsonConnectRequest(id, uid string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":"` + id + `","method":"connect","params":{"uid":"` + uid + `","token":"","deviceId":"d-1","deviceFlag":0}}`)
}

func jsonPingRequest(id string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":"` + id + `","method":"ping"}`)
}
