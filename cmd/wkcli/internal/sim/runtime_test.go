package sim

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestRuntimeConnectsAndSendsUntilMaxRuntimeStops(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:        []string{"http://127.0.0.1:5001"},
		Gateways:       []string{"127.0.0.1:5100"},
		Users:          2,
		Groups:         1,
		GroupMembers:   2,
		RatePerGroup:   "1000/s",
		RunID:          "run-1",
		MaxRuntime:     20 * time.Millisecond,
		StatusInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	status := newStatus(cfg.RunID)
	pool := &fakePool{}
	target := &fakeTarget{resolved: targetPreflight{GatewayTCPAddrs: cfg.Gateways, MaxBatchSize: 10}}
	runtime := &Runtime{
		Config: cfg,
		Status: status,
		Target: target,
		NewPool: func(Config, []string, wkclient.Observer) (simPool, error) {
			return pool, nil
		},
	}

	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if target.setupN != 1 {
		t.Fatalf("setup calls = %d, want 1", target.setupN)
	}
	if len(pool.connected) != 2 {
		t.Fatalf("connected identities = %d, want 2", len(pool.connected))
	}
	if !pool.closed {
		t.Fatalf("pool was not closed")
	}
	if pool.sent.Load() == 0 {
		t.Fatalf("expected sends")
	}
	snapshot := status.snapshot()
	if snapshot.State != stateStopped {
		t.Fatalf("state = %q, want %q", snapshot.State, stateStopped)
	}
	if snapshot.MessagesSent == 0 {
		t.Fatalf("MessagesSent = 0, want non-zero")
	}
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0", snapshot.SendErrors)
	}
	if snapshot.ActiveUsers != 2 || snapshot.Groups != 1 || snapshot.GroupMembers != 2 {
		t.Fatalf("topology = active %d groups %d members %d", snapshot.ActiveUsers, snapshot.Groups, snapshot.GroupMembers)
	}
}

func TestRuntimeInitializesMissingStatus(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:      []string{"http://127.0.0.1:5001"},
		Gateways:     []string{"127.0.0.1:5100"},
		Users:        1,
		Groups:       1,
		GroupMembers: 1,
		RatePerGroup: "1000/s",
		RunID:        "run-1",
		MaxRuntime:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	runtime := &Runtime{
		Config: cfg,
		Target: &fakeTarget{resolved: targetPreflight{
			GatewayTCPAddrs: cfg.Gateways,
			MaxBatchSize:    10,
		}},
		NewPool: func(Config, []string, wkclient.Observer) (simPool, error) {
			return &fakePool{}, nil
		},
	}

	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runtime.Status == nil {
		t.Fatalf("runtime did not publish initialized status")
	}
	if snapshot := runtime.Status.snapshot(); snapshot.State != stateStopped {
		t.Fatalf("state = %q, want %q", snapshot.State, stateStopped)
	}
}

func TestRuntimeDoesNotCountInFlightDeadlineSendsAsErrors(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:      []string{"http://127.0.0.1:5001"},
		Gateways:     []string{"127.0.0.1:5100"},
		Users:        1,
		Groups:       1,
		GroupMembers: 1,
		RatePerGroup: "20/s",
		RunID:        "run-1",
		MaxRuntime:   60 * time.Millisecond,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	status := newStatus(cfg.RunID)
	pool := &deadlinePool{}
	runtime := &Runtime{
		Config: cfg,
		Status: status,
		Target: &fakeTarget{resolved: targetPreflight{
			GatewayTCPAddrs: cfg.Gateways,
			MaxBatchSize:    10,
		}},
		NewPool: func(Config, []string, wkclient.Observer) (simPool, error) {
			return pool, nil
		},
	}

	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	snapshot := status.snapshot()
	if snapshot.State != stateStopped {
		t.Fatalf("state = %q, want %q", snapshot.State, stateStopped)
	}
	if pool.sendCalls == 0 {
		t.Fatalf("expected at least one in-flight send")
	}
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0 for graceful max-runtime stop; last error %q", snapshot.SendErrors, snapshot.LastError)
	}
}

func TestRuntimeStaggersGroupTrafficWithoutLocalQueueFull(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:      []string{"http://127.0.0.1:5001"},
		Gateways:     []string{"127.0.0.1:5100"},
		Users:        100,
		Groups:       100,
		GroupMembers: 1,
		RatePerGroup: "20/s",
		RunID:        "run-1",
		MaxRuntime:   80 * time.Millisecond,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	status := newStatus(cfg.RunID)
	pool := &fakePool{}
	runtime := &Runtime{
		Config: cfg,
		Status: status,
		Target: &fakeTarget{resolved: targetPreflight{
			GatewayTCPAddrs: cfg.Gateways,
			MaxBatchSize:    100,
		}},
		NewPool: func(Config, []string, wkclient.Observer) (simPool, error) {
			return pool, nil
		},
	}

	if err := runtime.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if pool.sent.Load() == 0 {
		t.Fatalf("expected sends")
	}
	snapshot := status.snapshot()
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0 for paced group scheduling; last error %q", snapshot.SendErrors, snapshot.LastError)
	}
}

func TestTrafficPacerBatchesHighRatesAndCatchesUpAfterDelayedTicks(t *testing.T) {
	const totalRate = 4000.0
	if got := trafficTickInterval(totalRate); got != 10*time.Millisecond {
		t.Fatalf("trafficTickInterval() = %s, want 10ms", got)
	}

	var scheduled uint64
	due := trafficMessagesDue(totalRate, 10*time.Millisecond, scheduled)
	if due != 40 {
		t.Fatalf("trafficMessagesDue(10ms) = %d, want 40", due)
	}
	scheduled += due

	// If the runtime does not observe the next timer tick until 30ms, it must
	// schedule all 80 messages that became due instead of losing two ticks.
	due = trafficMessagesDue(totalRate, 30*time.Millisecond, scheduled)
	if due != 80 {
		t.Fatalf("trafficMessagesDue(30ms) = %d, want 80", due)
	}
}

func TestClientObserverIgnoresShutdownErrorsAfterRuntimeStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status := newStatus("run-1")
	observer := &clientObserver{status: status, ctx: ctx}

	observer.OnConnect(wkclient.ConnectEvent{Err: context.Canceled})
	observer.OnSendBatch(wkclient.SendBatchEvent{Err: net.ErrClosed})
	observer.OnSendAck(wkclient.SendAckEvent{Err: context.DeadlineExceeded})
	observer.OnError(wkclient.ErrorEvent{Op: "write", Err: net.ErrClosed})

	snapshot := status.snapshot()
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0 for shutdown observer errors; last error %q", snapshot.SendErrors, snapshot.LastError)
	}
}

func TestClientObserverIgnoresMaxRuntimeDeadlineDuringStopRace(t *testing.T) {
	status := newStatus("run-1")
	var stopping atomic.Bool
	stopping.Store(true)
	observer := &clientObserver{
		status:   status,
		ctx:      context.Background(),
		stopping: &stopping,
	}

	observer.OnSendBatch(wkclient.SendBatchEvent{Err: context.DeadlineExceeded})
	observer.OnSendAck(wkclient.SendAckEvent{Err: context.DeadlineExceeded})

	snapshot := status.snapshot()
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0 while max-runtime stop is propagating; last error %q", snapshot.SendErrors, snapshot.LastError)
	}
}

func TestSimClientPoolHeartbeatsConnectedClientBeforeConnectReturns(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Gateways:          []string{"127.0.0.1:5100"},
		Users:             2,
		Groups:            1,
		GroupMembers:      1,
		RatePerGroup:      "1/s",
		RunID:             "run-1",
		ConnectRate:       1000,
		Concurrency:       2,
		HeartbeatInterval: 5 * time.Millisecond,
		HeartbeatTimeout:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	secondConnectStarted := make(chan struct{})
	unblockSecondConnect := make(chan struct{})
	firstPinged := make(chan struct{}, 1)
	pool := newSimClientPool(cfg, cfg.Gateways, nil)
	pool.newClient = func(wkclient.Config) (simClient, error) {
		return &fakeSimClient{
			onConnect: func(uid string) {
				if uid == "u2" {
					close(secondConnectStarted)
					<-unblockSecondConnect
				}
			},
			onPing: func(uid string) {
				if uid == "u1" {
					select {
					case firstPinged <- struct{}{}:
					default:
					}
				}
			},
		}, nil
	}

	connectDone := make(chan error, 1)
	go func() {
		connectDone <- pool.Connect(context.Background(), []wkclient.Identity{
			{UID: "u1", DeviceID: "d1"},
			{UID: "u2", DeviceID: "d2"},
		})
	}()

	select {
	case <-secondConnectStarted:
	case <-time.After(time.Second):
		t.Fatal("second client did not start connecting")
	}
	select {
	case <-firstPinged:
	case <-time.After(time.Second):
		t.Fatal("first connected client was not heartbeated while later connect was still blocked")
	}

	close(unblockSecondConnect)
	if err := <-connectDone; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSimClientPoolUsesBoundedPerClientBuffers(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Gateways:     []string{"127.0.0.1:5100"},
		Users:        1,
		Groups:       1,
		GroupMembers: 1,
		RatePerGroup: "1/s",
		RunID:        "run-1",
		Concurrency:  2800,
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	pool := newSimClientPool(cfg, cfg.Gateways, nil)
	t.Cleanup(func() {
		_ = pool.Close()
	})
	clientCfg := pool.clientConfig(cfg.Gateways[0])
	if got := clientCfg.SendQueueCapacity; got != simClientSendQueueCapacity {
		t.Fatalf("SendQueueCapacity = %d, want %d", got, simClientSendQueueCapacity)
	}
	if got := clientCfg.MaxInflight; got != simClientMaxInflight {
		t.Fatalf("MaxInflight = %d, want %d", got, simClientMaxInflight)
	}
	if got := clientCfg.InboundFrameBufferSize; got != simInboundFrameBufferSize {
		t.Fatalf("InboundFrameBufferSize = %d, want %d", got, simInboundFrameBufferSize)
	}
	if !clientCfg.DiscardInboundRecv {
		t.Fatal("DiscardInboundRecv = false, want true")
	}
	if !clientCfg.AutoRecvAck {
		t.Fatal("AutoRecvAck = false, want true")
	}
}

type fakeTarget struct {
	resolved targetPreflight
	setupN   int
}

func (f *fakeTarget) preflight(context.Context) (targetPreflight, error) {
	return f.resolved, nil
}

func (f *fakeTarget) setup(context.Context, Config, targetPreflight, Plan) error {
	f.setupN++
	return nil
}

type fakePool struct {
	connected []wkclient.Identity
	sent      atomic.Int64
	closed    bool
}

func (f *fakePool) Connect(ctx context.Context, identities []wkclient.Identity) error {
	f.connected = append([]wkclient.Identity(nil), identities...)
	return nil
}

func (f *fakePool) SendBatch(ctx context.Context, messages []wkclient.RoutedMessage) ([]wkclient.SendResult, error) {
	f.sent.Add(int64(len(messages)))
	results := make([]wkclient.SendResult, 0, len(messages))
	for _, msg := range messages {
		results = append(results, wkclient.SendResult{
			ClientSeq:   msg.Message.ClientSeq,
			ClientMsgNo: msg.Message.ClientMsgNo,
			ReasonCode:  frame.ReasonSuccess,
		})
	}
	return results, nil
}

func (f *fakePool) Close() error {
	f.closed = true
	return nil
}

type deadlinePool struct {
	connected []wkclient.Identity
	sendCalls int
	closed    bool
}

func (f *deadlinePool) Connect(ctx context.Context, identities []wkclient.Identity) error {
	f.connected = append([]wkclient.Identity(nil), identities...)
	return nil
}

func (f *deadlinePool) SendBatch(ctx context.Context, messages []wkclient.RoutedMessage) ([]wkclient.SendResult, error) {
	f.sendCalls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *deadlinePool) Close() error {
	f.closed = true
	return nil
}

type fakeSimClient struct {
	uid       string
	closed    bool
	onConnect func(uid string)
	onPing    func(uid string)
}

func (f *fakeSimClient) Connect(ctx context.Context, opts wkclient.ConnectOptions) (*frame.ConnackPacket, error) {
	f.uid = opts.UID
	if f.onConnect != nil {
		f.onConnect(opts.UID)
	}
	return &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}, nil
}

func (f *fakeSimClient) SendBatch(context.Context, []wkclient.Message) ([]wkclient.SendResult, error) {
	return []wkclient.SendResult{{ReasonCode: frame.ReasonSuccess}}, nil
}

func (f *fakeSimClient) Ping(context.Context) error {
	if f.onPing != nil {
		f.onPing(f.uid)
	}
	return nil
}

func (f *fakeSimClient) Close() error {
	f.closed = true
	return nil
}
