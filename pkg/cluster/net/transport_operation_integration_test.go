//go:build integration

package clusternet

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type operationDeclaredHandler struct{}

func (operationDeclaredHandler) HandleRPC(_ context.Context, payload []byte) ([]byte, error) {
	return payload, nil
}
func (operationDeclaredHandler) TraceOperationFor([]byte) uint8 { return 11 }

func TestTransportOperationRegistrationSurvivesRealTCP(t *testing.T) {
	observer := &recordingTransportObserver{}
	server := NewTransportServer(TransportServerConfig{RPCTracing: true, Observer: observer})
	server.Register(RPCPresenceOwner, operationDeclaredHandler{})
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1, RPCTraceEvery: 1, Observer: observer})
	t.Cleanup(client.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := client.Call(ctx, 2, RPCPresenceOwner, []byte("unchanged"))
	if err != nil || string(got) != "unchanged" {
		t.Fatal("operation registration changed business bytes/result")
	}
	client.Stop()
	server.Stop()
	var clientSample, serverSample *rpctrace.Sample
	for _, event := range observer.snapshot() {
		if event.RPCTrace == nil {
			continue
		}
		if event.RPCTrace.Side == rpctrace.SideServer {
			serverSample = event.RPCTrace
		} else {
			clientSample = event.RPCTrace
		}
	}
	if clientSample == nil || serverSample == nil || clientSample.TraceID != serverSample.TraceID || clientSample.Operation != 0 || serverSample.Operation != 11 {
		t.Fatal("optional registration operation lost or endpoint pairing invented")
	}
}
