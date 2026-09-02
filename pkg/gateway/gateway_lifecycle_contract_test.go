package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type facadeNoopHandler struct{}

func (facadeNoopHandler) OnListenerError(string, error)      {}
func (facadeNoopHandler) OnSessionOpen(Context) error        { return nil }
func (facadeNoopHandler) OnFrame(Context, frame.Frame) error { return nil }
func (facadeNoopHandler) OnSessionClose(Context) error       { return nil }
func (facadeNoopHandler) OnSessionError(Context, error)      {}

func TestGatewayZeroValueIsSafeAndClosed(t *testing.T) {
	var gateway *Gateway

	if err := gateway.Start(); !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("Start() error = %v, want %v", err, ErrGatewayClosed)
	}
	if err := gateway.DrainSends(context.Background()); !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("DrainSends() error = %v, want %v", err, ErrGatewayClosed)
	}
	if err := gateway.Stop(); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if got := gateway.ListenerAddr("missing"); got != "" {
		t.Fatalf("ListenerAddr() = %q, want empty", got)
	}
	if gateway.AcceptingNewSessions() {
		t.Fatal("nil gateway unexpectedly accepts new sessions")
	}
	gateway.SetAcceptingNewSessions(false)
	gateway.DisconnectAll()

	summary := gateway.SessionSummary()
	if summary.GatewaySessions != 0 || summary.AcceptingNewSessions {
		t.Fatalf("SessionSummary() = %+v, want closed empty summary", summary)
	}
	if summary.SessionsByListener == nil {
		t.Fatal("SessionSummary() returned a nil listener map")
	}
}

func TestNewRejectsInvalidRuntimeDependencies(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want error
	}{
		{
			name: "handler is required",
			opts: Options{},
			want: ErrNilHandler,
		},
		{
			name: "transport must be registered",
			opts: Options{
				Handler: facadeNoopHandler{},
				Listeners: []ListenerOptions{{
					Name: "client", Network: "tcp", Address: "127.0.0.1:1",
					Transport: "missing", Protocol: "wkproto",
				}},
			},
			want: core.ErrTransportFactoryNotFound,
		},
		{
			name: "protocol must be registered",
			opts: Options{
				Handler: facadeNoopHandler{},
				Listeners: []ListenerOptions{{
					Name: "client", Network: "tcp", Address: "127.0.0.1:1",
					Transport: "gnet", Protocol: "missing",
				}},
			},
			want: core.ErrProtocolAdapterNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.opts)
			if got != nil {
				t.Fatalf("New() gateway = %#v, want nil", got)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGatewayNoListenerLifecycleAndDrainContract(t *testing.T) {
	gateway, err := New(Options{
		Handler: facadeNoopHandler{},
		DefaultSession: SessionOptions{
			IdleTimeout: -1,
		},
		Runtime: RuntimeOptions{
			AsyncSendWorkers:        1,
			AsyncSendQueueCapacity:  4,
			AsyncAuthWorkers:        1,
			AsyncAuthQueueCapacity:  4,
			AsyncPoolReleaseTimeout: 20 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !gateway.AcceptingNewSessions() {
		t.Fatal("new gateway must initially accept sessions")
	}
	gateway.SetAcceptingNewSessions(false)
	if gateway.AcceptingNewSessions() {
		t.Fatal("SetAcceptingNewSessions(false) did not close admission")
	}
	if got := gateway.SessionSummary(); got.GatewaySessions != 0 || got.AcceptingNewSessions {
		t.Fatalf("SessionSummary() before start = %+v", got)
	}
	if got := gateway.ListenerAddr("missing"); got != "" {
		t.Fatalf("ListenerAddr(missing) = %q, want empty", got)
	}
	gateway.DisconnectAll()

	if err := gateway.DrainSends(context.Background()); !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("DrainSends() before Start error = %v, want %v", err, ErrGatewayClosed)
	}
	if err := gateway.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := gateway.Start(); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if err := gateway.DrainSends(nil); err != nil {
		t.Fatalf("DrainSends(nil) error = %v", err)
	}
	if err := gateway.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := gateway.Stop(); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if err := gateway.Start(); !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("Start() after Stop error = %v, want %v", err, ErrGatewayClosed)
	}
}
