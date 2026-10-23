//go:build integration

package transport_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// The fallback exercises the old public Stop behavior before StopContext exists.
// A return from that compatibility method must not be mistaken for quiescence.
func stopOwnedServer(ctx context.Context, server *transport.Server) error {
	if stopped, ok := any(server).(interface{ StopContext(context.Context) error }); ok {
		return stopped.StopContext(ctx)
	}
	server.Stop()
	return nil
}

func TestServerStopOwnershipRetainsUncooperativeTCPHandler(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	opts := budgetOpts()
	opts.Timeout = 0
	client, server := budgetPair(t, opts, func(context.Context, []byte) ([]byte, error) {
		close(entered)
		<-release // An admitted mutation need not cooperate with cancellation.
		return nil, nil
	}, nil)
	callDone := make(chan error, 1)
	go func() { callDone <- budgetCall(context.Background(), client, []byte("owned")) }()
	budgetWait(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := stopOwnedServer(ctx, server)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop reported quiescence with a live handler: %v", err)
	}
	var pending interface{ UnfinishedRequests() int }
	if !errors.As(err, &pending) || pending.UnfinishedRequests() != 1 {
		t.Fatalf("incomplete Stop omitted the actual request owner: %v", err)
	}
	if err := server.Handle(2, func(context.Context, []byte) ([]byte, error) { return nil, nil }, opts); !errors.Is(err, transport.ErrStopped) {
		t.Fatalf("incomplete Stop reopened admission: %v", err)
	}
	unblock()
	join, joinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer joinCancel()
	if err := stopOwnedServer(join, server); err != nil {
		t.Fatalf("second Stop did not join the original owner: %v", err)
	}
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("abandoned TCP caller inferred successful mutation")
		}
	case <-join.Done():
		t.Fatal("TCP caller still retained after complete Stop")
	}
	if err := stopOwnedServer(ctx, server); err != nil {
		t.Fatalf("already complete Stop did not preserve success: %v", err)
	}
}

func TestServerStopOwnershipJoinsLateObserverDrain(t *testing.T) {
	blocked, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	observer := budgetObserver{fn: func(e transport.Event) {
		if e.Name == "service_task" {
			once.Do(func() { close(blocked); <-release })
		}
	}}
	client, server := budgetPair(t, budgetOpts(), func(context.Context, []byte) ([]byte, error) { return nil, nil }, observer)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := budgetCall(ctx, client, nil); err != nil {
		t.Fatal(err)
	}
	budgetWait(t, blocked)
	stopCtx, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	stopped := make(chan error, 1)
	go func() { stopped <- stopOwnedServer(stopCtx, server) }()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("incomplete observer drain did not report cancellation: %v", err)
		}
	case <-ctx.Done():
		unblock()
		<-stopped
		t.Fatal("Stop ignored its caller budget while the observer still owned work")
	}
	unblock()
	join, joinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer joinCancel()
	if err := stopOwnedServer(join, server); err != nil {
		t.Fatal(err)
	}
}

func TestServerStopOwnershipBeforeListenAndConcurrentJoin(t *testing.T) {
	server, err := transport.NewServer(transport.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(1, func(context.Context, []byte) ([]byte, error) { return nil, nil }, budgetOpts()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, 4)
	for range 4 {
		go func() { results <- stopOwnedServer(ctx, server) }()
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := server.ListenAndServe("127.0.0.1:0"); !errors.Is(err, transport.ErrStopped) {
		t.Fatalf("stopped server accepted a listener: %v", err)
	}
}
