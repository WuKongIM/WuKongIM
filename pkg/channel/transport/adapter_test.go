package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestSessionManagerReusesSessionPerPeer(t *testing.T) {
	mux := transport.NewRPCMux()
	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    mux,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first := adapter.SessionManager().Session(2)
	second := adapter.SessionManager().Session(2)
	if first != second {
		t.Fatal("expected session reuse per peer")
	}
}

func TestNewDoesNotCollideWithClusterManagedSlotRPCService(t *testing.T) {
	const managedSlotRPCServiceID uint8 = 20

	mux := transport.NewRPCMux()
	mux.Handle(managedSlotRPCServiceID, func(ctx context.Context, body []byte) ([]byte, error) {
		return nil, nil
	})

	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{},
	}, 1, time.Second))
	defer client.Stop()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("New() panicked after cluster reserved service registration: %v", r)
		}
	}()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    mux,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if adapter == nil {
		t.Fatal("New() returned nil adapter")
	}
}

func TestBindFetchServiceAdaptsRuntimeLanePollService(t *testing.T) {
	mux := transport.NewRPCMux()
	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode:         1,
		Client:            client,
		RPCMux:            mux,
		LongPollLaneCount: 4,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	service := &lanePollFetchServiceStub{}
	adapter.BindFetchService(service)

	body, err := encodeLongPollFetchRequest(LongPollFetchRequest{
		PeerID:      2,
		LaneID:      1,
		LaneCount:   4,
		Op:          LanePollOpOpen,
		MaxWaitMs:   1,
		MaxBytes:    4096,
		MaxChannels: 64,
		FullMembership: []LongPollMembership{
			{ChannelKey: "g1", ChannelEpoch: 7},
		},
	})
	if err != nil {
		t.Fatalf("encodeLongPollFetchRequest() error = %v", err)
	}

	respBody, err := adapter.handleLongPollFetchRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("handleLongPollFetchRPC() error = %v", err)
	}
	resp, err := decodeLongPollFetchResponse(respBody)
	if err != nil {
		t.Fatalf("decodeLongPollFetchResponse() error = %v", err)
	}
	if !service.called.Load() {
		t.Fatal("expected runtime lane poll service to be invoked")
	}
	if resp.Status != LanePollStatusOK {
		t.Fatalf("resp.Status = %d, want %d", resp.Status, LanePollStatusOK)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("len(resp.Items) = %d, want 1", len(resp.Items))
	}
	if resp.Items[0].ChannelKey != "g1" {
		t.Fatalf("resp.Items[0].ChannelKey = %q, want %q", resp.Items[0].ChannelKey, "g1")
	}
}

func TestPeerSessionReportsHardBackpressureWhileRPCInFlight(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	mux.Handle(RPCServiceFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return encodeFetchResponse(runtime.FetchResponseEnvelope{
			ChannelKey: "g1",
			Epoch:      3,
			Generation: 7,
			LeaderHW:   9,
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    transport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	session := adapter.SessionManager().Session(2)
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Send(runtime.Envelope{
			Peer:       2,
			ChannelKey: "g1",
			Epoch:      3,
			Generation: 7,
			RequestID:  1,
			Kind:       runtime.MessageKindFetchRequest,
			FetchRequest: &runtime.FetchRequestEnvelope{
				ChannelKey:  "g1",
				Epoch:       3,
				Generation:  7,
				ReplicaID:   1,
				FetchOffset: 11,
				OffsetEpoch: 3,
				MaxBytes:    4096,
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for rpc handler to block")
	}

	state := session.Backpressure()
	if state.Level != runtime.BackpressureHard {
		t.Fatalf("Backpressure().Level = %v, want hard", state.Level)
	}
	if state.PendingRequests != 1 {
		t.Fatalf("PendingRequests = %d, want 1", state.PendingRequests)
	}
	if state.PendingBytes <= 0 {
		t.Fatalf("PendingBytes = %d, want > 0", state.PendingBytes)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("session.Send() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for session.Send()")
	}

	if state := session.Backpressure(); state.Level != runtime.BackpressureNone {
		t.Fatalf("Backpressure().Level after response = %v, want none", state.Level)
	}
}

func TestPeerSessionBackpressureAllowsConfiguredConcurrentInflightRPCs(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	mux.Handle(RPCServiceFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return encodeFetchResponse(runtime.FetchResponseEnvelope{
			ChannelKey: "g-concurrent",
			Epoch:      3,
			Generation: 7,
			LeaderHW:   9,
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    transport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
		MaxPendingFetchRPC: 2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	session := adapter.SessionManager().Session(2)
	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- session.Send(fetchRequestEnvelopeForTest("g-concurrent-1"))
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first rpc handler to block")
	}

	state := session.Backpressure()
	if state.Level != runtime.BackpressureNone {
		t.Fatalf("Backpressure().Level after first in-flight request = %v, want none", state.Level)
	}
	if state.PendingRequests != 1 {
		t.Fatalf("PendingRequests after first in-flight request = %d, want 1", state.PendingRequests)
	}

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- session.Send(fetchRequestEnvelopeForTest("g-concurrent-2"))
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second rpc handler to block")
	}

	state = session.Backpressure()
	if state.Level != runtime.BackpressureHard {
		t.Fatalf("Backpressure().Level after reaching configured in-flight limit = %v, want hard", state.Level)
	}
	if state.PendingRequests != 2 {
		t.Fatalf("PendingRequests after second in-flight request = %d, want 2", state.PendingRequests)
	}

	releaseOnce.Do(func() { close(release) })
	for _, errCh := range []<-chan error{errCh1, errCh2} {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("session.Send() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for session.Send()")
		}
	}

	if state := session.Backpressure(); state.Level != runtime.BackpressureNone {
		t.Fatalf("Backpressure().Level after responses = %v, want none", state.Level)
	}
}

func TestPeerSessionDistributesConcurrentFetchRPCsAcrossPoolShards(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer ln.Close()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var serverWG sync.WaitGroup
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = ln.Close()
		serverWG.Wait()
	}()

	serverWG.Add(1)
	go func() {
		defer serverWG.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			serverWG.Add(1)
			go func(conn net.Conn) {
				defer serverWG.Done()
				defer conn.Close()

				for {
					msgType, body, err := transport.ReadMessage(conn)
					if err != nil {
						return
					}
					if msgType != transport.MsgTypeRPCRequest || len(body) < 8 {
						return
					}

					requestID := binary.BigEndian.Uint64(body[:8])
					started <- struct{}{}
					<-release

					respPayload, err := encodeFetchResponse(runtime.FetchResponseEnvelope{
						ChannelKey: "g-ok",
						Epoch:      3,
						Generation: 7,
						LeaderHW:   9,
					})
					if err != nil {
						return
					}
					if err := transport.WriteMessage(conn, transport.MsgTypeRPCResponse, encodeRPCResponseForTest(requestID, respPayload)); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: ln.Addr().String()},
	}, 2, time.Second))
	defer client.Stop()

	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    transport.NewRPCMux(),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
		MaxPendingFetchRPC: 2,
		RPCTimeout:         2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	channelKey0 := channelKeyForShard(t, 0, 2)
	channelKey1 := channelKeyForShard(t, 1, 2)
	session := adapter.SessionManager().Session(2)

	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- session.Send(fetchRequestEnvelopeForTest(string(channelKey0)))
	}()

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- session.Send(fetchRequestEnvelopeForTest(string(channelKey1)))
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("started fetch handlers = %d, want 2", i)
		}
	}

	releaseOnce.Do(func() { close(release) })
	for _, errCh := range []<-chan error{errCh1, errCh2} {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("session.Send() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for session.Send()")
		}
	}
}

func TestPeerSessionUsesConfiguredRPCTimeout(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mux.Handle(RPCServiceFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return encodeFetchResponse(runtime.FetchResponseEnvelope{
			ChannelKey: "g-timeout",
			Epoch:      3,
			Generation: 7,
			LeaderHW:   9,
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

	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))
	defer client.Stop()

	adapter := newAdapterWithTestTimeout(t, client, 25*time.Millisecond)
	session := adapter.SessionManager().Session(2)

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Send(fetchRequestEnvelopeForTest("g-timeout"))
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for rpc handler to block")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("session.Send() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected configured rpc timeout to abort fetch request")
	}
}

func TestPeerSessionSendReturnsErrStoppedWhenClientStops(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mux.Handle(RPCServiceFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return encodeFetchResponse(runtime.FetchResponseEnvelope{
			ChannelKey: "g-stop",
			Epoch:      3,
			Generation: 7,
			LeaderHW:   9,
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

	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 1, time.Second))

	adapter := newAdapterWithTestTimeout(t, client, time.Second)
	session := adapter.SessionManager().Session(2)

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Send(fetchRequestEnvelopeForTest("g-stop"))
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for rpc handler to block")
	}

	client.Stop()

	select {
	case err := <-errCh:
		if !errors.Is(err, transport.ErrStopped) {
			t.Fatalf("session.Send() error = %v, want ErrStopped", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected client.Stop to abort pending fetch rpc")
	}

	if state := session.Backpressure(); state.Level != runtime.BackpressureNone {
		t.Fatalf("Backpressure().Level after stop = %v, want none", state.Level)
	}
}

func TestTransportLongPollModeRegistersLongPollRPC(t *testing.T) {
	client := transport.NewClient(transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{},
	}, 1, time.Second))
	defer client.Stop()

	mux := transport.NewRPCMux()
	got := make(chan LongPollFetchRequest, 1)
	adapter, err := New(Options{
		LocalNode: 1,
		Client:    client,
		RPCMux:    mux,
		LongPollService: longPollServiceFunc(func(ctx context.Context, req LongPollFetchRequest) (LongPollFetchResponse, error) {
			got <- req
			return LongPollFetchResponse{
				Status:       LanePollStatusOK,
				SessionID:    req.SessionID,
				SessionEpoch: req.SessionEpoch,
				TimedOut:     true,
			}, nil
		}),
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer adapter.Close()

	body, err := encodeLongPollFetchRequest(LongPollFetchRequest{
		PeerID:          2,
		LaneID:          4,
		LaneCount:       8,
		SessionID:       101,
		SessionEpoch:    6,
		Op:              LanePollOpOpen,
		ProtocolVersion: 1,
		Capabilities:    LongPollCapabilityQuorumAck | LongPollCapabilityLocalAck,
		MaxWaitMs:       1,
		MaxBytes:        64 * 1024,
		MaxChannels:     64,
		FullMembership: []LongPollMembership{
			{ChannelKey: "g1", ChannelEpoch: 11},
		},
	})
	if err != nil {
		t.Fatalf("encodeLongPollFetchRequest() error = %v", err)
	}

	respBody, err := mux.HandleRPC(context.Background(), append([]byte{RPCServiceLongPollFetch}, body...))
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	resp, err := decodeLongPollFetchResponse(respBody)
	if err != nil {
		t.Fatalf("decodeLongPollFetchResponse() error = %v", err)
	}
	if !resp.TimedOut || resp.Status != LanePollStatusOK {
		t.Fatalf("response = %+v, want timed out ok response", resp)
	}

	select {
	case req := <-got:
		if req.LaneID != 4 || req.SessionID != 101 {
			t.Fatalf("request = %+v, want lane=4 session=101", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for long poll request delivery")
	}
}

func TestPeerSessionReconcileProbeSharesFetchShardConnection(t *testing.T) {
	server := transport.NewServer()
	mux := transport.NewRPCMux()
	mux.Handle(RPCServiceFetch, func(ctx context.Context, body []byte) ([]byte, error) {
		req, err := decodeFetchRequest(body)
		if err != nil {
			return nil, err
		}
		return encodeFetchResponse(runtime.FetchResponseEnvelope{
			ChannelKey: req.ChannelKey,
			Epoch:      req.Epoch,
			Generation: req.Generation,
			LeaderHW:   req.FetchOffset,
		})
	})
	mux.Handle(RPCServiceReconcileProbe, func(ctx context.Context, body []byte) ([]byte, error) {
		req, err := decodeReconcileProbeRequest(body)
		if err != nil {
			return nil, err
		}
		return encodeReconcileProbeResponse(runtime.ReconcileProbeResponseEnvelope{
			ChannelKey:   req.ChannelKey,
			Epoch:        req.Epoch,
			Generation:   req.Generation,
			ReplicaID:    req.ReplicaID,
			LogEndOffset: 11,
		})
	})
	server.HandleRPCMux(mux)
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	defer server.Stop()

	pool := transport.NewPool(staticDiscovery{
		addrs: map[uint64]string{2: server.Listener().Addr().String()},
	}, 2, time.Second)
	client := transport.NewClient(pool)
	defer client.Stop()

	adapter := newAdapterWithTestTimeout(t, client, time.Second)
	session := adapter.SessionManager().Session(2)
	channelKey := channelKeyForShard(t, 1, 2)

	if err := session.Send(fetchRequestEnvelopeForTest(string(channelKey))); err != nil {
		t.Fatalf("session.Send(fetch) error = %v", err)
	}
	if err := session.Send(runtime.Envelope{
		Peer:       2,
		ChannelKey: channelKey,
		Epoch:      3,
		Generation: 7,
		RequestID:  2,
		Kind:       runtime.MessageKindReconcileProbeRequest,
		ReconcileProbeRequest: &runtime.ReconcileProbeRequestEnvelope{
			ChannelKey: channelKey,
			Epoch:      3,
			Generation: 7,
			ReplicaID:  1,
		},
	}); err != nil {
		t.Fatalf("session.Send(reconcile probe) error = %v", err)
	}

	stats := pool.Stats()
	if len(stats) != 1 {
		t.Fatalf("pool stats peers = %d, want 1", len(stats))
	}
	if stats[0].Active != 1 {
		t.Fatalf("pool active connections = %d, want 1", stats[0].Active)
	}
}

func (d staticDiscovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", transport.ErrNodeNotFound
	}
	return addr, nil
}

type fetchServiceFunc func(context.Context, runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error)

func (f fetchServiceFunc) ServeFetch(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
	return f(ctx, req)
}

type lanePollFetchServiceStub struct {
	called atomic.Bool
}

func (s *lanePollFetchServiceStub) ServeFetch(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
	return runtime.FetchResponseEnvelope{}, nil
}

func (s *lanePollFetchServiceStub) ServeLanePoll(ctx context.Context, req runtime.LanePollRequestEnvelope) (runtime.LanePollResponseEnvelope, error) {
	s.called.Store(true)
	return runtime.LanePollResponseEnvelope{
		LaneID:       req.LaneID,
		Status:       runtime.LanePollStatusOK,
		SessionID:    11,
		SessionEpoch: 3,
		Items: []runtime.LaneResponseItem{
			{
				ChannelKey:   "g1",
				ChannelEpoch: 7,
				LeaderEpoch:  7,
				Flags:        runtime.LanePollItemFlagData,
				Records: []channel.Record{
					{Payload: []byte("wake"), SizeBytes: len("wake")},
				},
				LeaderHW: 1,
			},
		},
	}, nil
}

type longPollServiceFunc func(context.Context, LongPollFetchRequest) (LongPollFetchResponse, error)

func (f longPollServiceFunc) ServeLongPollFetch(ctx context.Context, req LongPollFetchRequest) (LongPollFetchResponse, error) {
	return f(ctx, req)
}

func newAdapterWithTestTimeout(t *testing.T, client *transport.Client, timeout time.Duration) *Transport {
	t.Helper()

	opts := Options{
		LocalNode:  1,
		Client:     client,
		RPCMux:     transport.NewRPCMux(),
		RPCTimeout: timeout,
		FetchService: fetchServiceFunc(func(ctx context.Context, req runtime.FetchRequestEnvelope) (runtime.FetchResponseEnvelope, error) {
			return runtime.FetchResponseEnvelope{}, nil
		}),
	}

	adapter, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
}

func fetchRequestEnvelopeForTest(channelKey string) runtime.Envelope {
	return fetchRequestEnvelopeWithRequestIDForTest(channelKey, 1)
}

func fetchRequestEnvelopeWithRequestIDForTest(channelKey string, requestID uint64) runtime.Envelope {
	return fetchRequestEnvelopeWithOffsetForTest(channelKey, requestID, 11)
}

func fetchRequestEnvelopeWithOffsetForTest(channelKey string, requestID uint64, fetchOffset uint64) runtime.Envelope {
	key := channel.ChannelKey(channelKey)
	return runtime.Envelope{
		Peer:       2,
		ChannelKey: key,
		Epoch:      3,
		Generation: 7,
		RequestID:  requestID,
		Kind:       runtime.MessageKindFetchRequest,
		FetchRequest: &runtime.FetchRequestEnvelope{
			ChannelKey:  key,
			Epoch:       3,
			Generation:  7,
			ReplicaID:   1,
			FetchOffset: fetchOffset,
			OffsetEpoch: 3,
			MaxBytes:    4096,
		},
	}
}

func channelKeyForShard(t *testing.T, shard int, poolSize int) channel.ChannelKey {
	t.Helper()

	for i := 0; i < 1024; i++ {
		key := channel.ChannelKey(fmt.Sprintf("g-shard-%d", i))
		if fetchShardForTest(key, poolSize) == shard {
			return key
		}
	}
	t.Fatalf("no group key found for shard %d", shard)
	return ""
}

func fetchShardForTest(channelKey channel.ChannelKey, poolSize int) int {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(channelKey))
	return int(hasher.Sum64() % uint64(poolSize))
}

func encodeRPCResponseForTest(requestID uint64, data []byte) []byte {
	buf := make([]byte, 9+len(data))
	binary.BigEndian.PutUint64(buf[:8], requestID)
	copy(buf[9:], data)
	return buf
}
