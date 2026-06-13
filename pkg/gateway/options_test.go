package gateway_test

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type noopHandler struct{}

func (noopHandler) OnListenerError(string, error)              {}
func (noopHandler) OnSessionOpen(gateway.Context) error        { return nil }
func (noopHandler) OnFrame(gateway.Context, frame.Frame) error { return nil }
func (noopHandler) OnSessionClose(gateway.Context) error       { return nil }
func (noopHandler) OnSessionError(gateway.Context, error)      {}

func TestOptionsValidateRejectsDuplicateListenerNames(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "dup", Network: "tcp", Address: ":5100", Transport: "gnet", Protocol: "wkproto"},
			{Name: "dup", Network: "websocket", Address: ":5200", Transport: "gnet", Protocol: "jsonrpc"},
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
			{Name: "tcp-a", Network: "tcp", Address: ":5100", Transport: "gnet", Protocol: "wkproto"},
			{Name: "ws-b", Network: "websocket", Address: ":5100", Transport: "gnet", Protocol: "jsonrpc"},
		},
	}
	err := opts.Validate()
	if !errors.Is(err, gatewaytypes.ErrListenerAddressDuplicate) {
		t.Fatalf("expected ErrListenerAddressDuplicate, got %v", err)
	}
}

func TestGatewayExportsAsyncAuthQueueFullError(t *testing.T) {
	if gateway.ErrAsyncAuthQueueFull != gatewaytypes.ErrAsyncAuthQueueFull {
		t.Fatal("ErrAsyncAuthQueueFull is not gateway/types alias")
	}
	if gateway.CloseReasonAsyncAuthQueueFull != "async_auth_queue_full" {
		t.Fatalf("CloseReasonAsyncAuthQueueFull = %q", gateway.CloseReasonAsyncAuthQueueFull)
	}
	if gateway.CloseReasonAsyncAuthQueueFull != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("CloseReasonAsyncAuthQueueFull = %q, want %q", gateway.CloseReasonAsyncAuthQueueFull, gatewaytypes.CloseReasonAsyncAuthQueueFull)
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
	if opts.DefaultSession.MaxInboundBytes == 0 || opts.DefaultSession.MaxOutboundBytes == 0 || opts.DefaultSession.IdleTimeout == 0 {
		t.Fatalf("expected default session fields to be populated: %+v", opts.DefaultSession)
	}
}

func TestDefaultSessionOptions(t *testing.T) {
	opts := gateway.DefaultSessionOptions()
	if opts.CloseOnHandlerError == nil || !*opts.CloseOnHandlerError {
		t.Fatal("expected CloseOnHandlerError default to be true")
	}
	if opts.MaxInboundBytes <= 0 || opts.MaxOutboundBytes <= 0 {
		t.Fatalf("expected positive byte limit defaults, got %+v", opts)
	}
	if opts.IdleTimeout != 3*time.Minute {
		t.Fatalf("expected default idle timeout %v, got %v", 3*time.Minute, opts.IdleTimeout)
	}
	if opts.AsyncSendBatchMaxWait != time.Millisecond {
		t.Fatalf("expected default async SEND batch wait 1ms, got %s", opts.AsyncSendBatchMaxWait)
	}
	if opts.AsyncSendBatchMaxRecords != 512 {
		t.Fatalf("expected default async SEND batch records 512, got %d", opts.AsyncSendBatchMaxRecords)
	}
}

func TestDefaultRuntimeOptions(t *testing.T) {
	opts := gateway.DefaultRuntimeOptions()
	if opts.AsyncSendWorkers <= 0 {
		t.Fatalf("AsyncSendWorkers = %d, want > 0", opts.AsyncSendWorkers)
	}
	if opts.AsyncSendQueueCapacity <= 0 {
		t.Fatalf("AsyncSendQueueCapacity = %d, want > 0", opts.AsyncSendQueueCapacity)
	}
	if opts.AsyncAuthWorkers <= 0 {
		t.Fatalf("AsyncAuthWorkers = %d, want > 0", opts.AsyncAuthWorkers)
	}
	if opts.AsyncAuthQueueCapacity <= 0 {
		t.Fatalf("AsyncAuthQueueCapacity = %d, want > 0", opts.AsyncAuthQueueCapacity)
	}
	if opts.AsyncPoolReleaseTimeout != 100*time.Millisecond {
		t.Fatalf("AsyncPoolReleaseTimeout = %s, want 100ms", opts.AsyncPoolReleaseTimeout)
	}
}

func TestOptionsValidateNormalizesRuntimeOptions(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Runtime: gateway.RuntimeOptions{
			AsyncSendWorkers:        7,
			AsyncSendQueueCapacity:  17,
			AsyncAuthWorkers:        3,
			AsyncAuthQueueCapacity:  11,
			AsyncPoolReleaseTimeout: 250 * time.Millisecond,
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.Runtime.AsyncSendWorkers != 7 {
		t.Fatalf("AsyncSendWorkers = %d, want 7", opts.Runtime.AsyncSendWorkers)
	}
	if opts.Runtime.AsyncSendQueueCapacity != 17 {
		t.Fatalf("AsyncSendQueueCapacity = %d, want 17", opts.Runtime.AsyncSendQueueCapacity)
	}
	if opts.Runtime.AsyncAuthWorkers != 3 {
		t.Fatalf("AsyncAuthWorkers = %d, want 3", opts.Runtime.AsyncAuthWorkers)
	}
	if opts.Runtime.AsyncAuthQueueCapacity != 11 {
		t.Fatalf("AsyncAuthQueueCapacity = %d, want 11", opts.Runtime.AsyncAuthQueueCapacity)
	}
	if opts.Runtime.AsyncPoolReleaseTimeout != 250*time.Millisecond {
		t.Fatalf("AsyncPoolReleaseTimeout = %s, want 250ms", opts.Runtime.AsyncPoolReleaseTimeout)
	}
}

func TestNormalizeRuntimeOptionsUsesDefaultsForInvalidValues(t *testing.T) {
	opts := gateway.NormalizeRuntimeOptions(gateway.RuntimeOptions{
		AsyncSendWorkers:        -1,
		AsyncSendQueueCapacity:  -1,
		AsyncAuthWorkers:        -1,
		AsyncAuthQueueCapacity:  -1,
		AsyncPoolReleaseTimeout: -1,
	})
	def := gateway.DefaultRuntimeOptions()
	if opts != def {
		t.Fatalf("normalized runtime options = %+v, want %+v", opts, def)
	}
}

func TestOptionsValidateNormalizesPartialSessionOverrides(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		DefaultSession: gateway.SessionOptions{
			MaxInboundBytes: 8192,
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if opts.DefaultSession.MaxInboundBytes != 8192 {
		t.Fatalf("expected custom max inbound bytes to be preserved, got %+v", opts.DefaultSession)
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
			{Name: "ws", Network: "websocket", Address: ":5200", Transport: "gnet", Protocol: "jsonrpc"},
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

func TestOptionsValidateAcceptsExplicitGnetListeners(t *testing.T) {
	opts := gateway.Options{
		Handler: noopHandler{},
		Listeners: []gateway.ListenerOptions{
			{Name: "tcp", Network: "tcp", Address: ":5100", Transport: "gnet", Protocol: "wkproto"},
			{Name: "ws", Network: "websocket", Address: ":5200", Transport: "gnet", Protocol: "jsonrpc"},
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("expected explicit gnet listeners to remain valid, got %v", err)
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
				Transport: "  gnet  ",
				Protocol:  "  jsonrpc  ",
			},
		},
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	got := opts.Listeners[0]
	if got.Name != "ws-jsonrpc" || got.Network != "websocket" || got.Address != ":5200" || got.Path != "/ws" || got.Transport != "gnet" || got.Protocol != "jsonrpc" {
		t.Fatalf("expected listener fields to be trimmed in place, got %+v", got)
	}
}

func boolPtr(v bool) *bool { return &v }
