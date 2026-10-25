//go:build integration

package clusternet

import (
	"context"
	"testing"
	"time"
)

type mixedReadHandler struct {
	started chan context.Context
	release chan struct{}
}

func (h mixedReadHandler) HandleRPC(ctx context.Context, _ []byte) ([]byte, error) {
	h.started <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return nil, nil
	}
}
func (mixedReadHandler) CancelRunningFor(payload []byte) bool {
	return len(payload) == 1 && payload[0] == 1
}

func TestTransportRegistrationPreservesMixedRequestCancellation(t *testing.T) {
	for _, read := range []bool{true, false} {
		t.Run(map[bool]string{true: "read", false: "mutation"}[read], func(t *testing.T) {
			h := mixedReadHandler{started: make(chan context.Context, 1), release: make(chan struct{})}
			server := NewTransportServer(TransportServerConfig{})
			server.Register(RPCPresenceAuthority, h)
			if err := server.Start("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer server.Stop()
			discovery := NewDiscovery()
			discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
			client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
			defer client.Stop()
			defer close(h.release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			payload := []byte{2}
			if read {
				payload[0] = 1
			}
			go func() { defer close(done); _, _ = client.Call(ctx, 2, RPCPresenceAuthority, payload) }()
			var executed context.Context
			select {
			case executed = <-h.started:
			case <-time.After(2 * time.Second):
				t.Fatal("handler never started")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("caller did not return")
			}
			if read {
				select {
				case <-executed.Done():
				case <-time.After(500 * time.Millisecond):
					t.Fatal("mixed-service read lost request cancellation")
				}
			} else {
				select {
				case <-executed.Done():
					t.Fatal("started mutation inherited cancellation")
				case <-time.After(25 * time.Millisecond):
				}
			}
		})
	}
}
