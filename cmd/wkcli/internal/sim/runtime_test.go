package sim

import (
	"context"
	"net"
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
	if pool.sent == 0 {
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
	if pool.sent == 0 {
		t.Fatalf("expected sends")
	}
	snapshot := status.snapshot()
	if snapshot.SendErrors != 0 {
		t.Fatalf("SendErrors = %d, want 0 for paced group scheduling; last error %q", snapshot.SendErrors, snapshot.LastError)
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
	sent      int
	closed    bool
}

func (f *fakePool) Connect(ctx context.Context, identities []wkclient.Identity) error {
	f.connected = append([]wkclient.Identity(nil), identities...)
	return nil
}

func (f *fakePool) SendBatch(ctx context.Context, messages []wkclient.RoutedMessage) ([]wkclient.SendResult, error) {
	f.sent += len(messages)
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
