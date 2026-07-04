package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
	baseTransport "github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
)

func TestLongPollIntegrationPeerSessionDeliversLanePollResponse(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	got := make(chan LongPollFetchRequest, 1)
	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		req, err := decodeLongPollFetchRequest(body)
		if err != nil {
			return nil, err
		}
		got <- req
		return encodeLongPollFetchResponse(LongPollFetchResponse{
			Status:       LanePollStatusOK,
			SessionID:    701,
			SessionEpoch: 2,
			TimedOut:     true,
		})
	})

	client := baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    baseTransport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	delivered := make(chan runtime.Envelope, 1)
	adapter.RegisterHandler(func(env runtime.Envelope) {
		delivered <- env
	})

	err = adapter.SessionManager().Session(2).Send(runtime.Envelope{
		Peer: 2,
		Kind: runtime.MessageKindLanePollRequest,
		LanePollRequest: &runtime.LanePollRequestEnvelope{
			LaneID:          4,
			LaneCount:       8,
			Op:              runtime.LanePollOpOpen,
			ProtocolVersion: 1,
			MaxWait:         time.Millisecond,
			MaxBytes:        64 * 1024,
			MaxChannels:     64,
			FullMembership: []runtime.LaneMembership{
				{ChannelKey: "g1", ChannelEpoch: 11, ChannelGeneration: 7},
			},
		},
	})
	if err != nil {
		t.Fatalf("session.Send() error = %v", err)
	}

	select {
	case req := <-got:
		if req.LaneID != 4 || req.Op != LanePollOpOpen {
			t.Fatalf("request = %+v, want open lane=4", req)
		}
		if len(req.FullMembership) != 1 || req.FullMembership[0].ChannelGeneration != 7 {
			t.Fatalf("request membership = %+v, want generation 7", req.FullMembership)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for outbound long poll request")
	}

	select {
	case env := <-delivered:
		if env.Kind != runtime.MessageKindLanePollResponse {
			t.Fatalf("response kind = %v, want lane poll response", env.Kind)
		}
		if env.LanePollResponse == nil || !env.LanePollResponse.TimedOut {
			t.Fatalf("lane poll response = %+v, want timed out response", env.LanePollResponse)
		}
		if env.LanePollResponse.SessionID != 701 || env.LanePollResponse.SessionEpoch != 2 {
			t.Fatalf("lane poll response = %+v, want session 701/2", env.LanePollResponse)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delivered lane poll response")
	}
}

func TestLongPollIntegrationPeerSessionDeliversRetentionResetItem(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		if _, err := decodeLongPollFetchRequest(body); err != nil {
			return nil, err
		}
		return encodeLongPollFetchResponse(LongPollFetchResponse{
			Status:       LanePollStatusOK,
			SessionID:    701,
			SessionEpoch: 2,
			Items: []LongPollItem{
				{
					ChannelKey:        "g-reset",
					ChannelEpoch:      11,
					ChannelGeneration: 7,
					Flags:             LongPollItemFlagReset,
					LeaderHW:          10,
					RetentionReset: &channel.RetentionReset{
						RetentionThroughSeq:   5,
						RetainedThroughOffset: 5,
						MinAvailableSeq:       6,
					},
				},
			},
		})
	})

	client := baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    baseTransport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	delivered := make(chan runtime.Envelope, 1)
	adapter.RegisterHandler(func(env runtime.Envelope) {
		delivered <- env
	})

	err = adapter.SessionManager().Session(2).Send(runtime.Envelope{
		Peer: 2,
		Kind: runtime.MessageKindLanePollRequest,
		LanePollRequest: &runtime.LanePollRequestEnvelope{
			LaneID:          4,
			LaneCount:       8,
			Op:              runtime.LanePollOpOpen,
			ProtocolVersion: 1,
			MaxWait:         time.Millisecond,
			MaxBytes:        64 * 1024,
			MaxChannels:     64,
			FullMembership: []runtime.LaneMembership{
				{ChannelKey: "g-reset", ChannelEpoch: 11, ChannelGeneration: 7},
			},
		},
	})
	if err != nil {
		t.Fatalf("session.Send() error = %v", err)
	}

	select {
	case env := <-delivered:
		if env.LanePollResponse == nil || len(env.LanePollResponse.Items) != 1 {
			t.Fatalf("lane poll response = %+v, want one item", env.LanePollResponse)
		}
		reset := env.LanePollResponse.Items[0].RetentionReset
		if reset == nil || reset.RetainedThroughOffset != 5 || reset.MinAvailableSeq != 6 {
			t.Fatalf("RetentionReset = %+v, want retained offset 5 min 6", reset)
		}
		if env.LanePollResponse.Items[0].ChannelGeneration != 7 {
			t.Fatalf("item generation = %d, want 7", env.LanePollResponse.Items[0].ChannelGeneration)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delivered lane poll response")
	}
}

func TestLongPollPeerSessionUsesConfiguredRPCTimeout(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return encodeLongPollFetchResponse(LongPollFetchResponse{
			Status:    LanePollStatusOK,
			TimedOut:  true,
			SessionID: 88,
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer func() {
		close(release)
		server.Stop()
	}()

	client := baseTransport.NewClient(baseTransport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode:         1,
		Client:            client,
		RPCMux:            baseTransport.NewRPCMux(),
		RPCTimeout:        25 * time.Millisecond,
		LongPollLaneCount: 4,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.SessionManager().Session(2).Send(runtime.Envelope{
			Peer: 2,
			Kind: runtime.MessageKindLanePollRequest,
			LanePollRequest: &runtime.LanePollRequestEnvelope{
				LaneID:          1,
				LaneCount:       4,
				Op:              runtime.LanePollOpOpen,
				ProtocolVersion: 1,
				MaxWait:         time.Second,
				MaxBytes:        64 * 1024,
				MaxChannels:     64,
				FullMembership: []runtime.LaneMembership{
					{ChannelKey: "g-timeout", ChannelEpoch: 9},
				},
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for long poll rpc handler to block")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("session.Send() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected configured rpc timeout to abort long poll request")
	}
}

func TestLongPollFetchRecordsExpectedTimeoutFromTimedOutResponse(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		return encodeLongPollFetchResponse(LongPollFetchResponse{
			Status:   LanePollStatusOK,
			TimedOut: true,
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	events := make(chan baseTransport.RPCClientEvent, 4)
	client := baseTransport.NewClient(baseTransport.NewPool(baseTransport.PoolConfig{
		Discovery:   staticDiscovery{addrs: map[uint64]string{2: server.Listener().Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		Observer: baseTransport.ObserverHooks{
			OnRPCClient: func(event baseTransport.RPCClientEvent) {
				events <- event
			},
		},
	}))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    baseTransport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	resp, err := adapter.LongPollFetch(context.Background(), 2, LongPollFetchRequest{LaneID: 1})
	if err != nil {
		t.Fatalf("LongPollFetch() error = %v", err)
	}
	if !resp.TimedOut {
		t.Fatalf("LongPollFetch() timedOut = false, want true")
	}

	<-events
	done := <-events
	if done.Result != "expected_timeout" {
		t.Fatalf("done result = %q, want expected_timeout", done.Result)
	}
}

func TestLongPollFetchRecordsDeadlineTimeoutAsAbnormalTimeout(t *testing.T) {
	server := baseTransport.NewServer()
	mux := baseTransport.NewRPCMux()
	mux.Handle(RPCServiceLongPollFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	events := make(chan baseTransport.RPCClientEvent, 4)
	client := baseTransport.NewClient(baseTransport.NewPool(baseTransport.PoolConfig{
		Discovery:   staticDiscovery{addrs: map[uint64]string{2: server.Listener().Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		Observer: baseTransport.ObserverHooks{
			OnRPCClient: func(event baseTransport.RPCClientEvent) {
				events <- event
			},
		},
	}))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode:  1,
		Client:     client,
		RPCMux:     baseTransport.NewRPCMux(),
		RPCTimeout: 20 * time.Millisecond,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = adapter.LongPollFetch(ctx, 2, LongPollFetchRequest{LaneID: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LongPollFetch() error = %v, want %v", err, context.DeadlineExceeded)
	}

	<-events
	done := <-events
	if done.Result != "timeout" {
		t.Fatalf("done result = %q, want timeout", done.Result)
	}
}
