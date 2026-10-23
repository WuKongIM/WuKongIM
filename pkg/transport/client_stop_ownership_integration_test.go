//go:build integration

package transport_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

type stopNotifyingConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *stopNotifyingConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func TestClientStopOwnershipJoinsRetiredWriter(t *testing.T) {
	local, remote := net.Pipe()
	raw := &stopNotifyingConn{Conn: local, closed: make(chan struct{})}
	defer raw.Close()
	defer remote.Close()
	readerDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, remote); close(readerDone) }()
	defer func() { _ = remote.Close(); <-readerDone }()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var dials, releases atomic.Int32
	client, err := transport.NewClient(transport.ClientConfig{
		Discovery: testkit.StaticDiscovery{2: "ignored"}, PoolSize: 1,
		Dialer: func(string, string, time.Duration) (net.Conn, error) {
			if dials.Add(1) == 1 {
				return raw, nil
			}
			return nil, errors.New("replacement dial rejected")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	err = client.SendOwned(context.Background(), 2, 0, transport.PriorityRPC, 1,
		transport.NewOwnedBuffer([]byte("writer-owned"), func([]byte) {
			releases.Add(1)
			close(entered)
			<-release
		}))
	if err != nil {
		t.Fatal(err)
	}
	budgetWait(t, entered)
	_ = remote.Close()
	budgetWait(t, raw.closed) // Shutdown began, but payload release is still owned.
	acquire, cancelAcquire := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelAcquire()
	if err = client.Send(acquire, 2, 0, transport.PriorityRPC, 1, nil); err == nil {
		t.Fatal("replacement send unexpectedly succeeded")
	}
	stop, cancelStop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelStop()
	if err = stopOwnedClient(stop, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop forgot retiring writer ownership: %v", err)
	}
	unblock()
	join, cancelJoin := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelJoin()
	if err = stopOwnedClient(join, client); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 1 {
		t.Fatalf("payload releases=%d want1", releases.Load())
	}
}

func stopOwnedClient(ctx context.Context, client *transport.Client) error {
	if stopped, ok := any(client).(interface{ StopContext(context.Context) error }); ok {
		return stopped.StopContext(ctx)
	}
	client.Stop()
	return nil
}

func TestClientStopOwnershipJoinsLateDialAndPayload(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	local, remote := net.Pipe()
	defer remote.Close()
	defer local.Close()
	client, err := transport.NewClient(transport.ClientConfig{
		Discovery: testkit.StaticDiscovery{2: "ignored"}, PoolSize: 1,
		Dialer: func(string, string, time.Duration) (net.Conn, error) {
			close(entered)
			<-release // A legacy custom dialer may not accept context cancellation.
			return local, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	var released atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := client.CallOwned(context.Background(), 2, 0, transport.PriorityRPC, 1, transport.NewOwnedBuffer([]byte("owned"), func([]byte) { released.Add(1) }))
		done <- err
	}()
	budgetWait(t, entered)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err = stopOwnedClient(canceled, client)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop reported complete while dial/payload remained owned: %v", err)
	}
	var pending interface{ UnfinishedRequests() int }
	if !errors.As(err, &pending) || pending.UnfinishedRequests() != 1 {
		t.Fatalf("Stop omitted late dial owner: %v", err)
	}
	if released.Load() != 0 {
		t.Fatal("Stop released payload still held by dialing caller")
	}
	if _, err = client.Call(context.Background(), 2, 0, transport.PriorityRPC, 1, nil); !errors.Is(err, transport.ErrStopped) {
		t.Fatalf("Stop reopened admission: %v", err)
	}
	unblock()
	join, joinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer joinCancel()
	if err = stopOwnedClient(join, client); err != nil {
		t.Fatalf("retry did not join late dial: %v", err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, transport.ErrStopped) {
			t.Fatalf("late dial result: %v", err)
		}
	case <-join.Done():
		t.Fatal("late dial caller not joined")
	}
	if released.Load() != 1 {
		t.Fatalf("payload releases=%d want1", released.Load())
	}
	if err = stopOwnedClient(canceled, client); err != nil {
		t.Fatalf("completed stop regressed: %v", err)
	}
}

func TestClientStopOwnershipJoinsObserverWithinCallerBudget(t *testing.T) {
	blocked, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client, err := transport.NewClient(transport.ClientConfig{Discovery: testkit.StaticDiscovery{}, Observer: budgetObserver{fn: func(e transport.Event) {
		if e.Name == "client_rpc" {
			once.Do(func() { close(blocked); <-release })
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	_, _ = client.Call(context.Background(), 2, 0, transport.PriorityRPC, 1, nil)
	budgetWait(t, blocked)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- stopOwnedClient(canceled, client) }()
	select {
	case err = <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked observer stop=%v", err)
		}
	case <-time.After(2 * time.Second):
		unblock()
		<-stopped
		t.Fatal("Stop ignored its caller budget for observer ownership")
	}
	unblock()
	join, joinCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer joinCancel()
	if err = stopOwnedClient(join, client); err != nil {
		t.Fatal(err)
	}
}
