//go:build integration

package replication_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clientpkg "github.com/WuKongIM/WuKongIM/pkg/client"
	pkggateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	channelAppendDeliveryPressureSessions = 2500
	channelAppendDeliveryPressureFanout   = 9
	channelAppendDeliveryPressureWorkers  = 256
)

type channelAppendDeliveryAckMode uint8

const (
	channelAppendDeliveryNoAck channelAppendDeliveryAckMode = iota
	channelAppendDeliveryClientAutoAck
	channelAppendDeliveryQueuedAutoAck
	channelAppendDeliverySynchronousAck
)

// BenchmarkThreeNodeChannelAppendWithGatewayDeliveryPressure1000QPS adds the
// product-scale outbound half of a ten-member group workload to the real TCP
// quorum and three physical MessageDB engines. It isolates whether 2,500 live
// WKProto sessions and 9,000 RECV writes/s create the append tail that the
// storage-only benchmark does not reproduce.
func BenchmarkThreeNodeChannelAppendWithGatewayDeliveryPressure1000QPS(b *testing.B) {
	for _, tc := range []struct {
		name string
		mode channelAppendDeliveryAckMode
	}{
		{name: "without-recvack", mode: channelAppendDeliveryNoAck},
		{name: "client-auto-recvack", mode: channelAppendDeliveryClientAutoAck},
		{name: "queued-auto-recvack", mode: channelAppendDeliveryQueuedAutoAck},
		{name: "synchronous-recvack", mode: channelAppendDeliverySynchronousAck},
	} {
		name, mode := tc.name, tc.mode
		b.Run(name, func(b *testing.B) {
			if b.N < 1000 {
				return
			}
			cluster := newChannelAppendBenchmarkCluster(b, threeNodeBenchmarkChannels)
			pressure := newChannelAppendGatewayDeliveryPressure(b, channelAppendDeliveryPressureSessions, mode)
			benchmarkThreeNodeChannelAppendClusterWithLoad1000QPS(b, cluster, "append-with-delivery", pressure)
			b.ReportMetric(float64(pressure.writes.Load())/float64(b.N), "delivery-writes/op")
			b.ReportMetric(float64(pressure.handler.recvAcks.Load())/float64(b.N), "recvacks/op")
		})
	}
}

type channelAppendGatewayDeliveryPressure struct {
	gateway  *pkggateway.Gateway
	handler  *channelAppendDeliveryPressureHandler
	clients  []*clientpkg.Client
	sessions []gatewaytypes.Context
	payload  []byte
	ackMode  channelAppendDeliveryAckMode

	cancel      context.CancelFunc
	done        chan struct{}
	drainCancel context.CancelFunc
	drainWG     sync.WaitGroup
	writes      atomic.Uint64
	errMu       sync.Mutex
	err         error
}

func newChannelAppendGatewayDeliveryPressure(b *testing.B, sessionCount int, ackMode channelAppendDeliveryAckMode) *channelAppendGatewayDeliveryPressure {
	b.Helper()
	handler := &channelAppendDeliveryPressureHandler{}
	gateway, err := pkggateway.New(pkggateway.Options{
		Handler: handler,
		Authenticator: pkggateway.NewWKProtoAuthenticator(pkggateway.WKProtoAuthOptions{
			DisableEncryption: true,
		}),
		Transport: pkggateway.TransportOptions{Gnet: pkggateway.GnetTransportOptions{Multicore: true}},
		Listeners: []pkggateway.ListenerOptions{
			binding.TCPWKProto("channel-append-delivery-pressure", "127.0.0.1:0"),
		},
	})
	if err != nil {
		b.Fatalf("gateway.New(): %v", err)
	}
	if err := gateway.Start(); err != nil {
		b.Fatalf("gateway.Start(): %v", err)
	}
	clients := connectChannelAppendDeliveryPressureClients(b, gateway.ListenerAddr("channel-append-delivery-pressure"), sessionCount, ackMode)
	sessions := handler.snapshot()
	if len(sessions) != sessionCount {
		closeChannelAppendDeliveryPressureClients(clients)
		_ = gateway.Stop()
		b.Fatalf("gateway sessions = %d, want %d", len(sessions), sessionCount)
	}
	return &channelAppendGatewayDeliveryPressure{
		gateway: gateway, handler: handler, clients: clients, sessions: sessions,
		payload: make([]byte, 1024), ackMode: ackMode, done: make(chan struct{}),
	}
}

func (p *channelAppendGatewayDeliveryPressure) Start() {
	if p == nil || p.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	if p.ackMode == channelAppendDeliveryQueuedAutoAck || p.ackMode == channelAppendDeliverySynchronousAck {
		drainCtx, drainCancel := context.WithCancel(context.Background())
		p.drainCancel = drainCancel
		p.startSynchronousRecvAcks(drainCtx)
	}
	go p.run(ctx)
}

func (p *channelAppendGatewayDeliveryPressure) Stop() error {
	if p == nil {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
		<-p.done
	}
	if p.ackMode != channelAppendDeliveryNoAck {
		deadline := time.Now().Add(10 * time.Second)
		for p.handler.recvAcks.Load() < p.writes.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got, want := p.handler.recvAcks.Load(), p.writes.Load(); got != want {
			p.recordError(fmt.Errorf("receive acknowledgements = %d, want %d", got, want))
		}
	}
	if p.drainCancel != nil {
		p.drainCancel()
		p.drainWG.Wait()
	}
	closeChannelAppendDeliveryPressureClients(p.clients)
	gatewayErr := p.gateway.Stop()
	p.errMu.Lock()
	loadErr := p.err
	p.errMu.Unlock()
	return errors.Join(loadErr, gatewayErr)
}

func (p *channelAppendGatewayDeliveryPressure) startSynchronousRecvAcks(ctx context.Context) {
	for _, client := range p.clients {
		if client == nil {
			continue
		}
		p.drainWG.Add(1)
		go func(client *clientpkg.Client) {
			defer p.drainWG.Done()
			for {
				recv, err := client.Recv(ctx)
				if err != nil {
					if ctx.Err() == nil {
						p.recordError(err)
					}
					return
				}
				if p.ackMode != channelAppendDeliverySynchronousAck {
					continue
				}
				if err := client.RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
					if ctx.Err() == nil {
						p.recordError(err)
					}
					return
				}
			}
		}(client)
	}
}

func (p *channelAppendGatewayDeliveryPressure) run(ctx context.Context) {
	defer close(p.done)
	jobs := make(chan uint64, 4096)
	var workers sync.WaitGroup
	workers.Add(channelAppendDeliveryPressureWorkers)
	for range channelAppendDeliveryPressureWorkers {
		go func() {
			defer workers.Done()
			for messageID := range jobs {
				session := p.sessions[int(messageID)%len(p.sessions)]
				err := session.WriteFrame(&frame.RecvPacket{
					Setting:     frame.SettingNoEncrypt,
					MessageID:   int64(messageID + 1),
					MessageSeq:  messageID + 1,
					ClientMsgNo: fmt.Sprintf("delivery-pressure-%d", messageID+1),
					Timestamp:   int32(time.Now().Unix()),
					ChannelID:   fmt.Sprintf("delivery-pressure-%04d", messageID%1000),
					ChannelType: 1,
					FromUID:     "delivery-pressure-sender",
					Payload:     p.payload,
				})
				if err != nil {
					p.recordError(err)
					continue
				}
				p.writes.Add(1)
			}
		}()
	}

	started := time.Now()
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var next uint64
produce:
	for {
		dueAt := started.Add(time.Duration(next/channelAppendDeliveryPressureFanout) * time.Millisecond)
		if wait := time.Until(dueAt); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				break produce
			case <-timer.C:
			}
		}
		for range channelAppendDeliveryPressureFanout {
			select {
			case jobs <- next:
				next++
			case <-ctx.Done():
				break produce
			}
		}
	}
	close(jobs)
	workers.Wait()
}

func (p *channelAppendGatewayDeliveryPressure) recordError(err error) {
	if err == nil {
		return
	}
	p.errMu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.errMu.Unlock()
}

type channelAppendDeliveryPressureHandler struct {
	mu       sync.Mutex
	sessions []gatewaytypes.Context
	recvAcks atomic.Uint64
}

func (h *channelAppendDeliveryPressureHandler) OnListenerError(string, error) {}

func (h *channelAppendDeliveryPressureHandler) OnSessionOpen(ctx gatewaytypes.Context) error {
	h.mu.Lock()
	h.sessions = append(h.sessions, ctx)
	h.mu.Unlock()
	return nil
}

func (h *channelAppendDeliveryPressureHandler) OnFrame(_ gatewaytypes.Context, packet frame.Frame) error {
	if _, ok := packet.(*frame.RecvackPacket); ok {
		h.recvAcks.Add(1)
	}
	return nil
}

func (h *channelAppendDeliveryPressureHandler) OnSessionClose(gatewaytypes.Context) error {
	return nil
}

func (h *channelAppendDeliveryPressureHandler) OnSessionError(gatewaytypes.Context, error) {}

func (h *channelAppendDeliveryPressureHandler) snapshot() []gatewaytypes.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gatewaytypes.Context(nil), h.sessions...)
}

func connectChannelAppendDeliveryPressureClients(b *testing.B, addr string, count int, ackMode channelAppendDeliveryAckMode) []*clientpkg.Client {
	b.Helper()
	clients := make([]*clientpkg.Client, count)
	jobs := make(chan int)
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	workers.Add(128)
	for range 128 {
		go func() {
			defer workers.Done()
			for index := range jobs {
				client, err := clientpkg.New(clientpkg.Config{
					Addr: addr, OperationTimeout: 5 * time.Second, AckTimeout: 5 * time.Second,
					SendQueueCapacity: 16, MaxInflight: 1, ReadBufferSize: 1024, InboundFrameBufferSize: 4,
					DiscardInboundRecv: ackMode == channelAppendDeliveryNoAck || ackMode == channelAppendDeliveryClientAutoAck,
					AutoRecvAck:        ackMode == channelAppendDeliveryClientAutoAck || ackMode == channelAppendDeliveryQueuedAutoAck,
				})
				if err == nil {
					_, err = client.Connect(ctx, clientpkg.ConnectOptions{
						UID: fmt.Sprintf("delivery-pressure-user-%04d", index), DeviceID: "delivery-pressure", DeviceFlag: frame.APP,
					})
				}
				if err != nil {
					if client != nil {
						_ = client.Close()
					}
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("connect client %d: %w", index, err)
					}
					errMu.Unlock()
					continue
				}
				clients[index] = client
			}
		}()
	}
	for index := range count {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		closeChannelAppendDeliveryPressureClients(clients)
		b.Fatal(firstErr)
	}
	return clients
}

func closeChannelAppendDeliveryPressureClients(clients []*clientpkg.Client) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		if err := client.Close(); err != nil && !errors.Is(err, clientpkg.ErrClosed) {
			continue
		}
	}
}

var _ gatewaytypes.Handler = (*channelAppendDeliveryPressureHandler)(nil)
