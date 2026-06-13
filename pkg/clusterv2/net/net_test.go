package clusternet

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

func TestDiscoveryUpdatesAtomically(t *testing.T) {
	d := NewDiscovery()
	d.Update([]NodeAddress{{NodeID: 1, Addr: "a"}, {NodeID: 2, Addr: "b"}})
	addr, ok := d.Addr(1)
	if !ok || addr != "a" {
		t.Fatalf("Addr(1) = %q,%v want a,true", addr, ok)
	}
	snap := d.Snapshot()
	d.Update([]NodeAddress{{NodeID: 2, Addr: "bb"}})
	if snap[1] != "a" {
		t.Fatalf("old snapshot mutated: %#v", snap)
	}
	addr, ok = d.Addr(1)
	if ok || addr != "" {
		t.Fatalf("Addr(1) after update = %q,%v want empty,false", addr, ok)
	}
}

func TestLocalNetworkDispatchesRPC(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(2, RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("echo:"), payload...), nil
	}))
	got, err := network.Call(context.Background(), 2, RPCSlotForwardPropose, []byte("hello"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "echo:hello" {
		t.Fatalf("Call() = %q, want echo:hello", got)
	}
}

func TestLocalNetworkReturnsTypedErrors(t *testing.T) {
	network := NewLocalNetwork()
	if _, err := network.Call(context.Background(), 9, RPCSlotForwardPropose, nil); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node error = %v, want ErrNodeNotFound", err)
	}
	network.Register(1, RPCSlotForwardPropose, HandlerFunc(func(context.Context, []byte) ([]byte, error) { return nil, nil }))
	if _, err := network.Call(context.Background(), 1, RPCChannelPull, nil); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("missing service error = %v, want ErrServiceNotFound", err)
	}
}

func TestCodecHeaderRoundTrip(t *testing.T) {
	payload := []byte("payload")
	frame := PutHeader(nil, 1, 7)
	frame = append(frame, payload...)
	got, err := CheckHeader(frame, 1, 7)
	if err != nil {
		t.Fatalf("CheckHeader() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestCodecRejectsInvalidHeader(t *testing.T) {
	if _, err := CheckHeader([]byte{1}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("short frame error = %v, want ErrInvalidFrame", err)
	}
	if _, err := CheckHeader([]byte{2, 7}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("version error = %v, want ErrInvalidFrame", err)
	}
	if _, err := CheckHeader([]byte{1, 8}, 1, 7); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("kind error = %v, want ErrInvalidFrame", err)
	}
}

func TestTransportLoopbackRPC(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	server.Register(RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("resp:"), payload...), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	got, err := client.Call(context.Background(), 2, RPCSlotForwardPropose, []byte("ping"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "resp:ping" {
		t.Fatalf("Call() = %q, want resp:ping", got)
	}
}

func TestTransportLoopbackCallOwnedReleasesPayload(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	server.Register(RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("resp:"), payload...), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	released := make(chan []byte, 1)
	payload := transportv2.NewOwnedBuffer([]byte("ping"), func(data []byte) {
		released <- append([]byte(nil), data...)
	})
	got, err := client.CallOwned(context.Background(), 2, RPCSlotForwardPropose, payload)
	if err != nil {
		t.Fatalf("CallOwned() error = %v", err)
	}
	if string(got) != "resp:ping" {
		t.Fatalf("CallOwned() = %q, want resp:ping", got)
	}

	select {
	case got := <-released:
		if string(got) != "ping" {
			t.Fatalf("released payload = %q, want ping", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for owned payload release")
	}
}

func TestTransportClientUsesClusterSizedQueuesByDefault(t *testing.T) {
	client := NewTransportClient(TransportClientConfig{})
	defer client.Stop()

	limits := client.Limits()
	if limits.MaxQueuedItemsPerConn != defaultTransportQueueSize {
		t.Fatalf("MaxQueuedItemsPerConn = %d, want %d", limits.MaxQueuedItemsPerConn, defaultTransportQueueSize)
	}
}

func TestTransportClientUsesClusterPoolSizeByDefault(t *testing.T) {
	client := NewTransportClient(TransportClientConfig{})
	defer client.Stop()

	if client.poolSize != defaultTransportPoolSize {
		t.Fatalf("poolSize = %d, want %d", client.poolSize, defaultTransportPoolSize)
	}
}

func TestCallOwnedPayloadUsesOwnedCaller(t *testing.T) {
	caller := &recordingOwnedNetworkClient{response: []byte("ok")}

	got, err := CallOwnedPayload(context.Background(), caller, 2, RPCSlotForwardPropose, []byte("ping"))

	if err != nil {
		t.Fatalf("CallOwnedPayload() error = %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("CallOwnedPayload() = %q, want ok", got)
	}
	if caller.callOwnedCount != 1 || caller.callCount != 0 {
		t.Fatalf("call counts owned=%d normal=%d, want owned only", caller.callOwnedCount, caller.callCount)
	}
	if string(caller.lastPayload) != "ping" {
		t.Fatalf("payload = %q, want ping", caller.lastPayload)
	}
}

func TestSendOwnedPayloadUsesOwnedSender(t *testing.T) {
	sender := &recordingOwnedNetworkClient{}

	if err := SendOwnedPayload(context.Background(), sender, 2, RPCControlRaft, []byte("raft")); err != nil {
		t.Fatalf("SendOwnedPayload() error = %v", err)
	}
	if sender.sendOwnedCount != 1 || sender.sendCount != 0 {
		t.Fatalf("send counts owned=%d normal=%d, want owned only", sender.sendOwnedCount, sender.sendCount)
	}
	if string(sender.lastPayload) != "raft" {
		t.Fatalf("payload = %q, want raft", sender.lastPayload)
	}
}

func TestTransportClientShardsRPCsByService(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	for _, serviceID := range []uint8{RPCChannelPull, RPCChannelPullHint, RPCChannelAppendBatch} {
		serviceID := serviceID
		server.Register(serviceID, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return append([]byte{serviceID}, payload...), nil
		}))
	}
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 4})
	defer client.Stop()

	for _, serviceID := range []uint8{RPCChannelPull, RPCChannelPullHint, RPCChannelAppendBatch} {
		if _, err := client.Call(context.Background(), 2, serviceID, []byte("ping")); err != nil {
			t.Fatalf("Call(service=%d) error = %v", serviceID, err)
		}
	}
	stats := client.Stats()
	if stats.Peers != 1 {
		t.Fatalf("Stats().Peers = %d, want 1", stats.Peers)
	}
	if stats.Connections < 3 {
		t.Fatalf("Stats().Connections = %d, want at least 3 services sharded across pool", stats.Connections)
	}
}

func TestTransportServerUsesLargerForegroundWriteServiceConcurrency(t *testing.T) {
	const wantWriteConcurrency = 512

	observer := &recordingTransportObserver{}
	server := NewTransportServer(TransportServerConfig{Observer: observer})
	for _, serviceID := range []uint8{RPCChannelPull, RPCChannelAppendBatch, RPCChannelAuthoritySend} {
		serviceID := serviceID
		server.Register(serviceID, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte{serviceID}, nil
		}))
	}
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	if _, err := client.Call(context.Background(), 2, RPCChannelPull, []byte("pull")); err != nil {
		t.Fatalf("Call(pull) error = %v", err)
	}
	if _, err := client.Call(context.Background(), 2, RPCChannelAppendBatch, []byte("append")); err != nil {
		t.Fatalf("Call(append batch) error = %v", err)
	}
	if _, err := client.Call(context.Background(), 2, RPCChannelAuthoritySend, []byte("channel authority send")); err != nil {
		t.Fatalf("Call(channel authority send) error = %v", err)
	}

	pullEvent := waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "service_inflight" &&
			event.ServiceID == uint16(RPCChannelPull) &&
			event.Inflight == 1
	})
	if pullEvent.Capacity != defaultTransportServiceConcurrency {
		t.Fatalf("pull service capacity = %d, want default %d", pullEvent.Capacity, defaultTransportServiceConcurrency)
	}

	appendEvent := waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "service_inflight" &&
			event.ServiceID == uint16(RPCChannelAppendBatch) &&
			event.Inflight == 1
	})
	if appendEvent.Capacity != wantWriteConcurrency {
		t.Fatalf("append service capacity = %d, want %d", appendEvent.Capacity, wantWriteConcurrency)
	}

	writeEvent := waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "service_inflight" &&
			event.ServiceID == uint16(RPCChannelAuthoritySend) &&
			event.Inflight == 1
	})
	if writeEvent.Capacity != wantWriteConcurrency {
		t.Fatalf("channel authority send service capacity = %d, want %d", writeEvent.Capacity, wantWriteConcurrency)
	}
}

func TestTransportLoopbackReportsTransportV2Pressure(t *testing.T) {
	observer := &recordingTransportObserver{}
	server := NewTransportServer(TransportServerConfig{
		Observer: observer,
		Service: TransportServiceConfig{
			Concurrency:   1,
			QueueSize:     4,
			MaxQueueBytes: 1024,
		},
	})
	server.Register(RPCSlotForwardPropose, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("resp:"), payload...), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1, Observer: observer})
	defer client.Stop()

	got, err := client.Call(context.Background(), 2, RPCSlotForwardPropose, []byte("ping"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(got) != "resp:ping" {
		t.Fatalf("Call() = %q, want resp:ping", got)
	}

	waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "service_admission" && event.ServiceID == uint16(RPCSlotForwardPropose) && event.Result == "ok"
	})
	waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "service_task" && event.ServiceID == uint16(RPCSlotForwardPropose) && event.Result == "ok"
	})
	waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "scheduler_admission" && event.Result == "ok"
	})
	waitTransportEvent(t, observer, func(event transportv2.Event) bool {
		return event.Name == "pending_rpc" && event.Result == "ok"
	})
}

func TestTransportLoopbackSendDoesNotWaitForResponse(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	received := make(chan []byte, 1)
	release := make(chan struct{})
	server.Register(RPCControlRaft, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		received <- append([]byte(nil), payload...)
		<-release
		return []byte("ignored"), nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()
	defer close(release)

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := client.Send(ctx, 2, RPCControlRaft, []byte("raft")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case got := <-received:
		if string(got) != "raft" {
			t.Fatalf("payload = %q, want raft", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for one-way send")
	}
}

func TestTransportLoopbackSendOwnedReleasesPayload(t *testing.T) {
	server := NewTransportServer(TransportServerConfig{})
	received := make(chan []byte, 1)
	server.Register(RPCControlRaft, HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		received <- append([]byte(nil), payload...)
		return nil, nil
	}))
	if err := server.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop()

	discovery := NewDiscovery()
	discovery.Update([]NodeAddress{{NodeID: 2, Addr: server.Addr()}})
	client := NewTransportClient(TransportClientConfig{Discovery: discovery, PoolSize: 1})
	defer client.Stop()

	released := make(chan []byte, 1)
	payload := transportv2.NewOwnedBuffer([]byte("raft"), func(data []byte) {
		released <- append([]byte(nil), data...)
	})
	if err := client.SendOwned(context.Background(), 2, RPCControlRaft, payload); err != nil {
		t.Fatalf("SendOwned() error = %v", err)
	}

	select {
	case got := <-received:
		if string(got) != "raft" {
			t.Fatalf("payload = %q, want raft", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for one-way owned send")
	}
	select {
	case got := <-released:
		if string(got) != "raft" {
			t.Fatalf("released payload = %q, want raft", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for owned payload release")
	}
}

type recordingTransportObserver struct {
	mu     sync.Mutex
	events []transportv2.Event
}

type recordingOwnedNetworkClient struct {
	response       []byte
	lastPayload    []byte
	callCount      int
	callOwnedCount int
	sendCount      int
	sendOwnedCount int
}

func (c *recordingOwnedNetworkClient) Call(context.Context, uint64, uint8, []byte) ([]byte, error) {
	c.callCount++
	return nil, errors.New("normal call")
}

func (c *recordingOwnedNetworkClient) CallOwned(_ context.Context, _ uint64, _ uint8, payload transportv2.OwnedBuffer) ([]byte, error) {
	c.callOwnedCount++
	c.lastPayload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	return append([]byte(nil), c.response...), nil
}

func (c *recordingOwnedNetworkClient) Send(context.Context, uint64, uint8, []byte) error {
	c.sendCount++
	return errors.New("normal send")
}

func (c *recordingOwnedNetworkClient) SendOwned(_ context.Context, _ uint64, _ uint8, payload transportv2.OwnedBuffer) error {
	c.sendOwnedCount++
	c.lastPayload = append([]byte(nil), payload.Bytes()...)
	payload.Release()
	return nil
}

func (o *recordingTransportObserver) ObserveTransport(event transportv2.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingTransportObserver) snapshot() []transportv2.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]transportv2.Event(nil), o.events...)
}

func waitTransportEvent(t *testing.T, observer *recordingTransportObserver, match func(transportv2.Event) bool) transportv2.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, event := range observer.snapshot() {
			if match(event) {
				return event
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for transport event, observed %#v", observer.snapshot())
		case <-ticker.C:
		}
	}
}
