//go:build integration
// +build integration

package transport

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestAdapterRoundTripsFetchRequestAndResponse(t *testing.T) {
	server1 := transport.NewServer()
	mux1 := transport.NewRPCMux()
	server1.HandleRPCMux(mux1)
	if err := server1.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server1.Start() error = %v", err)
	}
	defer server1.Stop()

	server2 := transport.NewServer()
	mux2 := transport.NewRPCMux()
	server2.HandleRPCMux(mux2)
	if err := server2.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server2.Start() error = %v", err)
	}
	defer server2.Stop()

	client1 := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server2.Listener().Addr().String()},
	}, 1, 5*time.Second))
	defer client1.Stop()

	client2 := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{1: server1.Listener().Addr().String()},
	}, 1, 5*time.Second))
	defer client2.Stop()

	adapter1, err := New(Options{
		LocalNode: 1,
		Client:    client1,
		RPCMux:    mux1,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New(adapter1) error = %v", err)
	}
	adapter2, err := New(Options{
		LocalNode: 2,
		Client:    client2,
		RPCMux:    mux2,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			truncateTo := uint64(8)
			return runtime.FetchResponseEnvelope{
				ChannelKey: req.ChannelKey,
				Epoch:      req.Epoch + 1,
				Generation: req.Generation,
				TruncateTo: &truncateTo,
				LeaderHW:   12,
				Records: []channel.Record{
					{Payload: []byte("ok"), SizeBytes: 2},
				},
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New(adapter2) error = %v", err)
	}

	delivered := make(chan runtime.Envelope, 1)
	pressure := make(chan runtime.BackpressureState, 1)
	session := adapter1.SessionManager().Session(2)
	adapter1.RegisterHandler(func(env runtime.Envelope) {
		pressure <- session.Backpressure()
		delivered <- env
	})
	adapter2.RegisterHandler(func(env runtime.Envelope) {})

	err = session.Send(runtime.Envelope{
		Peer:       2,
		ChannelKey: channel.ChannelKey("g1"),
		Epoch:      3,
		Generation: 7,
		RequestID:  9,
		Kind:       runtime.MessageKindFetchRequest,
		FetchRequest: &runtime.FetchRequestEnvelope{
			ChannelKey:  channel.ChannelKey("g1"),
			Epoch:       3,
			Generation:  7,
			ReplicaID:   1,
			FetchOffset: 11,
			OffsetEpoch: 5,
			MaxBytes:    4096,
		},
	})
	if err != nil {
		t.Fatalf("session.Send() error = %v", err)
	}

	select {
	case env := <-delivered:
		state := <-pressure
		if state.Level != runtime.BackpressureNone {
			t.Fatalf("handler observed backpressure = %+v, want none", state)
		}
		if env.Kind != runtime.MessageKindFetchResponse {
			t.Fatalf("response kind = %v, want fetch response", env.Kind)
		}
		if env.Epoch != 4 {
			t.Fatalf("response epoch = %d, want 4", env.Epoch)
		}
		if env.FetchResponse == nil || env.FetchResponse.LeaderHW != 12 {
			t.Fatalf("response envelope = %+v", env.FetchResponse)
		}
		if env.FetchResponse.Epoch != 4 {
			t.Fatalf("response payload epoch = %d, want 4", env.FetchResponse.Epoch)
		}
		if env.FetchResponse.TruncateTo == nil || *env.FetchResponse.TruncateTo != 8 {
			t.Fatalf("response truncate = %+v", env.FetchResponse.TruncateTo)
		}
		if len(env.FetchResponse.Records) != 1 || string(env.FetchResponse.Records[0].Payload) != "ok" {
			t.Fatalf("response records = %+v", env.FetchResponse.Records)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for fetch response")
	}
}
