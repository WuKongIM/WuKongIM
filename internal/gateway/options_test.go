package gateway_test

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/gateway/binding"
	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type noopHandler struct{}

func (noopHandler) OnListenerError(string, error)               {}
func (noopHandler) OnSessionOpen(*gateway.Context) error        { return nil }
func (noopHandler) OnFrame(*gateway.Context, frame.Frame) error { return nil }
func (noopHandler) OnSessionClose(*gateway.Context) error       { return nil }
func (noopHandler) OnSessionError(*gateway.Context, error)      {}

func TestOptionsValidateRejectsDuplicateListenerNames(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "dup", Network: "tcp", Address: ":5100", Transport: "stdnet", Protocol: "wkproto"},
			{Name: "dup", Network: "websocket", Address: ":5200", Transport: "stdnet", Protocol: "jsonrpc"},
		},
	}
	if err := opts.Validate(); err == nil {
		t.Fatal("expected duplicate listener validation error")
	}
}

func TestOptionsValidateRejectsDuplicateListenerAddresses(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "tcp-a", Network: "tcp", Address: ":5100", Transport: "stdnet", Protocol: "wkproto"},
			{Name: "ws-b", Network: "websocket", Address: ":5100", Transport: "stdnet", Protocol: "jsonrpc"},
		},
	}
	err := opts.Validate()
	if !errors.Is(err, gatewaytypes.ErrListenerAddressDuplicate) {
		t.Fatalf("expected ErrListenerAddressDuplicate, got %v", err)
	}
}

func TestBuiltinPresetsPopulateCanonicalFields(t *testing.T) {
	tcp := binding.TCPWKProto("tcp-wkproto", ":5100")
	if tcp.Network != "tcp" || tcp.Transport != "gnet" || tcp.Protocol != "wkproto" {
		t.Fatalf("unexpected tcp preset: %+v", tcp)
	}

	ws := binding.WSJSONRPC("ws-jsonrpc", ":5200")
	if ws.Network != "websocket" || ws.Transport != "gnet" || ws.Protocol != "jsonrpc" || ws.Path != "" {
		t.Fatalf("unexpected ws preset: %+v", ws)
	}
}

func TestOptionsValidateNormalizesDefaultSession(t *testing.T) {
	opts := gateway.Options{Handler: noopHandler{}}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.DefaultSession.CloseOnHandlerError == nil {
		t.Fatal("expected default CloseOnHandlerError to be populated")
	}
	if !*opts.DefaultSession.CloseOnHandlerError {
		t.Fatal("expected default CloseOnHandlerError to be true")
	}
	if opts.DefaultSession.ReadBufferSize == 0 || opts.DefaultSession.WriteQueueSize == 0 || opts.DefaultSession.IdleTimeout == 0 {
		t.Fatalf("expected default session fields to be populated: %+v", opts.DefaultSession)
	}
}

func TestDefaultSessionOptions(t *testing.T) {
	opts := gateway.DefaultSessionOptions()
	if opts.CloseOnHandlerError == nil || !*opts.CloseOnHandlerError {
		t.Fatal("expected CloseOnHandlerError default to be true")
	}
	if opts.IdleTimeout <= 0 || opts.WriteTimeout <= 0 {
		t.Fatalf("expected positive timeout defaults, got %+v", opts)
	}
	if opts.IdleTimeout != 3*time.Minute {
		t.Fatalf("expected default idle timeout %v, got %v", 3*time.Minute, opts.IdleTimeout)
	}
}

func TestOptionsValidateNormalizesPartialSessionOverrides(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		DefaultSession: gateway.SessionOptions{
			ReadBufferSize: 8192,
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.DefaultSession.ReadBufferSize != 8192 {
		t.Fatalf("expected custom read buffer size to be preserved, got %+v", opts.DefaultSession)
	}
	if opts.DefaultSession.CloseOnHandlerError == nil || !*opts.DefaultSession.CloseOnHandlerError {
		t.Fatalf("expected CloseOnHandlerError to remain true after normalization, got %+v", opts.DefaultSession)
	}
}

func TestOptionsValidatePreservesExplicitFalseCloseOnHandlerError(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		DefaultSession: gateway.SessionOptions{
			CloseOnHandlerError: boolPtr(false),
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.DefaultSession.CloseOnHandlerError == nil {
		t.Fatalf("expected explicit false CloseOnHandlerError to be preserved, got %+v", opts.DefaultSession)
	}
	if *opts.DefaultSession.CloseOnHandlerError {
		t.Fatalf("expected explicit false CloseOnHandlerError to be preserved, got %+v", opts.DefaultSession)
	}
}

func TestOptionsValidateAllowsWebsocketRootPath(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "ws", Network: "websocket", Address: ":5200", Transport: "stdnet", Protocol: "jsonrpc"},
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("expected websocket listener without explicit path to be valid, got %v", err)
	}
}

func TestOptionsValidateAcceptsAuthenticator(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Authenticator: gateway.AuthenticatorFunc(func(*gateway.Context, *frame.ConnectPacket) (*gateway.AuthResult, error) {
			return &gateway.AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess},
			}, nil
		}),
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("expected authenticator to be accepted, got %v", err)
	}
}

func TestOptionsValidateAcceptsExplicitStdnetListeners(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "tcp", Network: "tcp", Address: ":5100", Transport: "stdnet", Protocol: "wkproto"},
			{Name: "ws", Network: "websocket", Address: ":5200", Transport: "stdnet", Protocol: "jsonrpc"},
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("expected explicit stdnet listeners to remain valid, got %v", err)
	}
}

func TestOptionsValidateTrimsListenerFieldsInPlace(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{
				Name:      "  ws-jsonrpc  ",
				Network:   "  websocket  ",
				Address:   "  :5200  ",
				Path:      "  /ws  ",
				Transport: "  stdnet  ",
				Protocol:  "  jsonrpc  ",
			},
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	got := opts.Listeners[0]
	if got.Name != "ws-jsonrpc" || got.Network != "websocket" || got.Address != ":5200" || got.Path != "/ws" || got.Transport != "stdnet" || got.Protocol != "jsonrpc" {
		t.Fatalf("expected listener fields to be trimmed in place, got %+v", got)
	}
}

func boolPtr(v bool) *bool { return &v }
