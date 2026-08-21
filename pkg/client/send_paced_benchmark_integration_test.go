//go:build integration

package client

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	pacedTCPBenchmarkRate     = 1000
	pacedTCPBenchmarkSessions = 2500
	pacedTCPBenchmarkWorkers  = 256
)

// BenchmarkRealTCPSendackPaced1000QPS measures the full client writer,
// loopback TCP, gateway dispatch, physical SENDACK write, and client reader
// path while replaying the retained three-node handler latency distribution.
// Run with -benchtime=5000x so every session sends twice over five seconds.
func BenchmarkRealTCPSendackPaced1000QPS(b *testing.B) {
	benchmarkRealTCPSendackPaced1000QPS(b, false, false)
}

// BenchmarkRealTCPSendackShuffledPaced1000QPS preserves one SEND per client
// per cycle while removing the accidental correlation between client order and
// gateway session IDs. This models assignment sender order without allowing
// concurrent SENDs from one client.
func BenchmarkRealTCPSendackShuffledPaced1000QPS(b *testing.B) {
	benchmarkRealTCPSendackPaced1000QPS(b, false, true)
}

// BenchmarkRealTCPSendackWithSynchronousRecvackPaced1000QPS adds the
// production group-fanout shape to the same 2,500 clients that originate the
// paced SENDs. RECVACK completion therefore contends with SEND on each
// client's single writer exactly as it does in the three-node workload.
func BenchmarkRealTCPSendackWithSynchronousRecvackPaced1000QPS(b *testing.B) {
	benchmarkRealTCPSendackPaced1000QPS(b, true, true)
}

func benchmarkRealTCPSendackPaced1000QPS(b *testing.B, synchronousRecvack bool, shuffledClients bool) {
	if b.N < 1000 {
		return
	}
	handler := &pacedTCPBenchmarkHandler{}
	observer := &pacedTCPBenchmarkObserver{}
	gateway, err := pkgateway.New(pkgateway.Options{
		Handler:  handler,
		Observer: observer,
		DefaultSession: pkgateway.SessionOptions{
			AsyncSendBatchMaxWait:    time.Millisecond,
			AsyncSendBatchMaxRecords: 1,
			AsyncSendBatchMaxBytes:   512 * 1024,
		},
		Runtime: pkgateway.RuntimeOptions{
			AsyncSendWorkers:       1000,
			AsyncSendQueueCapacity: 128 * 1024,
		},
		Authenticator: pkgateway.NewWKProtoAuthenticator(pkgateway.WKProtoAuthOptions{
			DisableEncryption: true,
		}),
		Listeners: []pkgateway.ListenerOptions{
			binding.TCPWKProto("paced-send-benchmark", "127.0.0.1:0"),
		},
	})
	if err != nil {
		b.Fatalf("gateway.New(): %v", err)
	}
	if err := gateway.Start(); err != nil {
		b.Fatalf("gateway.Start(): %v", err)
	}
	defer func() {
		if err := gateway.Stop(); err != nil {
			b.Errorf("gateway.Stop(): %v", err)
		}
	}()

	clients := newPacedTCPBenchmarkClientsWithRecv(b, gateway.ListenerAddr("paced-send-benchmark"), pacedTCPBenchmarkSessions, synchronousRecvack)
	defer closePacedTCPBenchmarkClients(b, clients)
	clientOrder := pacedTCPBenchmarkClientOrder(len(clients), shuffledClients)
	var recvackLoad *pacedTCPRecvackLoad
	if synchronousRecvack {
		sessions := handler.snapshotSessions()
		if len(sessions) != len(clients) {
			b.Fatalf("gateway sessions = %d, want %d", len(sessions), len(clients))
		}
		recvackLoad = newPacedTCPRecvackLoad(clients, sessions, handler)
	}

	latencies := make([]time.Duration, b.N)
	pendingWaits := make([]time.Duration, b.N)
	wireWaits := make([]time.Duration, b.N)
	jobs := make(chan int, pacedTCPBenchmarkWorkers)
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	workers.Add(pacedTCPBenchmarkWorkers)
	for range pacedTCPBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				result, sendErr := clients[clientOrder[index%len(clientOrder)]].Send(ctx, Message{
					ClientSeq:   uint64(index + 1),
					ClientMsgNo: fmt.Sprintf("paced-tcp-%d", index+1),
					ChannelID:   fmt.Sprintf("person-%d", index%2000),
					ChannelType: 1,
					Payload:     make([]byte, 1024),
				})
				cancel()
				if sendErr == nil && result.ReasonCode != frame.ReasonSuccess {
					sendErr = fmt.Errorf("SENDACK reason %v", result.ReasonCode)
				}
				if sendErr != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("send %d: %w", index, sendErr)
					}
					errMu.Unlock()
					continue
				}
				latencies[index] = result.ObservedAt.Sub(result.PendingStartedAt)
				pendingWaits[index] = result.WriteStartedAt.Sub(result.PendingStartedAt)
				wireWaits[index] = result.ObservedAt.Sub(result.WriteStartedAt)
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	if recvackLoad != nil {
		recvackLoad.Start()
	}
	started := time.Now()
	for index := range b.N {
		pacedTCPBenchmarkWaitUntil(started.Add(time.Duration(index) * time.Second / pacedTCPBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if recvackLoad != nil {
		if err := recvackLoad.Stop(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(recvackLoad.writes.Load())/float64(b.N), "recv-writes/op")
		b.ReportMetric(float64(handler.recvAcks.Load())/float64(b.N), "recvacks/op")
	}
	if firstErr != nil {
		b.Fatal(firstErr)
	}

	reportPacedTCPLatency(b, "send", latencies)
	reportPacedTCPLatency(b, "pending-to-write", pendingWaits)
	reportPacedTCPLatency(b, "write-to-ack", wireWaits)
	frameHandled, dispatchWait, transportWrite := observer.snapshot()
	reportPacedTCPLatency(b, "gateway-handler", frameHandled)
	reportPacedTCPLatency(b, "gateway-dispatch-wait", dispatchWait)
	reportPacedTCPLatency(b, "sendack-transport-write", transportWrite)
}

func pacedTCPBenchmarkClientOrder(count int, shuffled bool) []int {
	order := make([]int, count)
	for index := range order {
		order[index] = index
	}
	if !shuffled {
		return order
	}
	state := uint64(0x9e3779b97f4a7c15)
	for index := len(order) - 1; index > 0; index-- {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		other := int(state % uint64(index+1))
		order[index], order[other] = order[other], order[index]
	}
	return order
}

type pacedTCPBenchmarkHandler struct {
	mu       sync.Mutex
	sessions []pkgateway.Context
	recvAcks atomic.Uint64
}

type pacedTCPBenchmarkObserver struct {
	mu             sync.Mutex
	frameHandled   []time.Duration
	dispatchWait   []time.Duration
	transportWrite []time.Duration
}

func (o *pacedTCPBenchmarkObserver) OnConnectionOpen(pkgateway.ConnectionEvent)  {}
func (o *pacedTCPBenchmarkObserver) OnConnectionClose(pkgateway.ConnectionEvent) {}
func (o *pacedTCPBenchmarkObserver) OnAuth(pkgateway.AuthEvent)                  {}
func (o *pacedTCPBenchmarkObserver) OnFrameIn(pkgateway.FrameEvent)              {}
func (o *pacedTCPBenchmarkObserver) OnFrameOut(pkgateway.FrameEvent)             {}
func (o *pacedTCPBenchmarkObserver) OnFrameHandled(event pkgateway.FrameHandleEvent) {
	if event.FrameType != "SEND" {
		return
	}
	o.mu.Lock()
	o.frameHandled = append(o.frameHandled, event.Duration)
	o.mu.Unlock()
}
func (o *pacedTCPBenchmarkObserver) OnAsyncSendQueue(pkgateway.AsyncSendQueueEvent) {}
func (o *pacedTCPBenchmarkObserver) OnAsyncSendBatch(pkgateway.AsyncSendBatchEvent) {}
func (o *pacedTCPBenchmarkObserver) OnAsyncSendDispatchWait(event pkgateway.AsyncSendDispatchWaitEvent) {
	o.mu.Lock()
	o.dispatchWait = append(o.dispatchWait, event.Duration)
	o.mu.Unlock()
}
func (o *pacedTCPBenchmarkObserver) OnTransportWrite(event pkgateway.TransportWriteEvent) {
	if event.FrameType != "SENDACK" {
		return
	}
	o.mu.Lock()
	o.transportWrite = append(o.transportWrite, event.Duration)
	o.mu.Unlock()
}
func (o *pacedTCPBenchmarkObserver) snapshot() ([]time.Duration, []time.Duration, []time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]time.Duration(nil), o.frameHandled...),
		append([]time.Duration(nil), o.dispatchWait...),
		append([]time.Duration(nil), o.transportWrite...)
}

func (h *pacedTCPBenchmarkHandler) OnListenerError(string, error) {}
func (h *pacedTCPBenchmarkHandler) OnSessionOpen(ctx pkgateway.Context) error {
	h.mu.Lock()
	h.sessions = append(h.sessions, ctx)
	h.mu.Unlock()
	return nil
}
func (h *pacedTCPBenchmarkHandler) OnSessionClose(pkgateway.Context) error  { return nil }
func (h *pacedTCPBenchmarkHandler) OnSessionError(pkgateway.Context, error) {}

func (h *pacedTCPBenchmarkHandler) OnFrame(ctx pkgateway.Context, f frame.Frame) error {
	switch packet := f.(type) {
	case *frame.SendPacket:
		return h.reply(ctx, packet)
	case *frame.RecvackPacket:
		h.recvAcks.Add(1)
		return nil
	default:
		return nil
	}
}

func (h *pacedTCPBenchmarkHandler) snapshotSessions() []pkgateway.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]pkgateway.Context(nil), h.sessions...)
}

func (h *pacedTCPBenchmarkHandler) OnSendBatch(items []pkgateway.SendBatchItem) error {
	if len(items) == 0 {
		return nil
	}
	index := int(items[0].Frame.ClientSeq) - 1
	time.Sleep(measuredPacedTCPHandlerLatency(index))
	for _, item := range items {
		if err := h.reply(item.Context, item.Frame); err != nil {
			return err
		}
	}
	return nil
}

func (h *pacedTCPBenchmarkHandler) reply(ctx pkgateway.Context, send *frame.SendPacket) error {
	return ctx.WriteFrame(&frame.SendackPacket{
		MessageID:   int64(send.ClientSeq),
		MessageSeq:  send.ClientSeq,
		ClientSeq:   send.ClientSeq,
		ClientMsgNo: send.ClientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	})
}

func newPacedTCPBenchmarkClients(b *testing.B, addr string, count int) []*Client {
	return newPacedTCPBenchmarkClientsWithRecv(b, addr, count, false)
}

func newPacedTCPBenchmarkClientsWithRecv(b *testing.B, addr string, count int, receive bool) []*Client {
	b.Helper()
	clients := make([]*Client, count)
	jobs := make(chan int)
	var workers sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	workers.Add(128)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for range 128 {
		go func() {
			defer workers.Done()
			for index := range jobs {
				client, err := New(Config{
					Addr: addr, OperationTimeout: 5 * time.Second, AckTimeout: 5 * time.Second,
					SendQueueCapacity: 16, MaxInflight: 1, ReadBufferSize: 1024, InboundFrameBufferSize: 4,
					DiscardInboundRecv: !receive,
				})
				if err == nil {
					_, err = client.Connect(ctx, ConnectOptions{
						UID: fmt.Sprintf("paced-user-%04d", index), DeviceID: "paced-device", DeviceFlag: frame.APP,
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
		closePacedTCPBenchmarkClients(b, clients)
		b.Fatal(firstErr)
	}
	return clients
}

type pacedTCPRecvackLoad struct {
	clients  []*Client
	sessions []pkgateway.Context
	handler  *pacedTCPBenchmarkHandler
	payload  []byte

	cancel      context.CancelFunc
	done        chan struct{}
	drainCancel context.CancelFunc
	drainWG     sync.WaitGroup
	writes      atomic.Uint64
	errMu       sync.Mutex
	err         error
}

func newPacedTCPRecvackLoad(clients []*Client, sessions []pkgateway.Context, handler *pacedTCPBenchmarkHandler) *pacedTCPRecvackLoad {
	return &pacedTCPRecvackLoad{
		clients: clients, sessions: sessions, handler: handler,
		payload: make([]byte, 1024), done: make(chan struct{}),
	}
}

func (l *pacedTCPRecvackLoad) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	drainCtx, drainCancel := context.WithCancel(context.Background())
	l.drainCancel = drainCancel
	for _, client := range l.clients {
		l.drainWG.Add(1)
		go func(client *Client) {
			defer l.drainWG.Done()
			for {
				recv, err := client.Recv(drainCtx)
				if err != nil {
					if drainCtx.Err() == nil {
						l.recordError(err)
					}
					return
				}
				if err := client.RecvAck(drainCtx, recv.MessageID, recv.MessageSeq); err != nil {
					if drainCtx.Err() == nil {
						l.recordError(err)
					}
					return
				}
			}
		}(client)
	}
	go l.run(ctx)
}

func (l *pacedTCPRecvackLoad) Stop() error {
	l.cancel()
	<-l.done
	deadline := time.Now().Add(10 * time.Second)
	for l.handler.recvAcks.Load() < l.writes.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got, want := l.handler.recvAcks.Load(), l.writes.Load(); got != want {
		l.recordError(fmt.Errorf("receive acknowledgements = %d, want %d", got, want))
	}
	l.drainCancel()
	l.drainWG.Wait()
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.err
}

func (l *pacedTCPRecvackLoad) run(ctx context.Context) {
	defer close(l.done)
	jobs := make(chan uint64, 4096)
	var workers sync.WaitGroup
	workers.Add(pacedTCPBenchmarkWorkers)
	for range pacedTCPBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for messageID := range jobs {
				session := l.sessions[int(messageID)%len(l.sessions)]
				if err := session.WriteFrame(&frame.RecvPacket{
					Setting: frame.SettingNoEncrypt, MessageID: int64(messageID + 1), MessageSeq: messageID + 1,
					ClientMsgNo: fmt.Sprintf("paced-recv-%d", messageID+1), Timestamp: int32(time.Now().Unix()),
					ChannelID: fmt.Sprintf("paced-recv-channel-%04d", messageID%1000), ChannelType: 1,
					FromUID: "paced-recv-sender", Payload: l.payload,
				}); err != nil {
					l.recordError(err)
					continue
				}
				l.writes.Add(1)
			}
		}()
	}

	started := time.Now()
	var next uint64
produce:
	for {
		dueAt := started.Add(time.Duration(next/9) * time.Millisecond)
		if wait := time.Until(dueAt); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				break produce
			case <-timer.C:
			}
		}
		for range 9 {
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

func (l *pacedTCPRecvackLoad) recordError(err error) {
	if err == nil {
		return
	}
	l.errMu.Lock()
	if l.err == nil {
		l.err = err
	}
	l.errMu.Unlock()
}

func closePacedTCPBenchmarkClients(b *testing.B, clients []*Client) {
	b.Helper()
	for index, client := range clients {
		if client == nil {
			continue
		}
		if err := client.Close(); err != nil && err != ErrClosed {
			b.Errorf("client %d Close(): %v", index, err)
		}
	}
}

func measuredPacedTCPHandlerLatency(index int) time.Duration {
	rank := (uint64(index+1) * 11400714819323198485) % 59002
	switch {
	case rank < 4:
		return 5 * time.Millisecond
	case rank < 1223:
		return 17500 * time.Microsecond
	case rank < 7664:
		return 37500 * time.Microsecond
	case rank < 34539:
		return 75 * time.Millisecond
	case rank < 54184:
		return 125 * time.Millisecond
	case rank < 58828:
		return 175 * time.Millisecond
	case rank < 58980:
		return 225 * time.Millisecond
	default:
		return 375 * time.Millisecond
	}
}

func pacedTCPBenchmarkWaitUntil(deadline time.Time) {
	if remaining := time.Until(deadline); remaining > 0 {
		timer := time.NewTimer(remaining)
		<-timer.C
	}
}

func reportPacedTCPLatency(b *testing.B, prefix string, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	aboveBudget := 0
	for _, latency := range latencies {
		if latency > 200*time.Millisecond {
			aboveBudget++
		}
	}
	ratio := 100 * float64(aboveBudget) / float64(len(latencies))
	b.Logf("%s latency: p99=%s over_200ms=%.3f%%", prefix, p99, ratio)
	b.ReportMetric(float64(p99)/float64(time.Millisecond), prefix+"-p99-ms")
	b.ReportMetric(ratio, prefix+"-over-200ms-pct")
	if prefix == "send" && len(latencies) >= 1000 && ratio > 1 {
		b.Errorf("real TCP SEND operations above 200ms = %.3f%%, p99=%s; want <= 1%%", ratio, p99)
	}
}

var _ pkgateway.Handler = (*pacedTCPBenchmarkHandler)(nil)
var _ pkgateway.SendBatchHandler = (*pacedTCPBenchmarkHandler)(nil)
var _ pkgateway.Observer = (*pacedTCPBenchmarkObserver)(nil)
var _ pkgateway.AsyncSendObserver = (*pacedTCPBenchmarkObserver)(nil)
var _ pkgateway.TransportWriteObserver = (*pacedTCPBenchmarkObserver)(nil)
