package peer

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []core.Event
}

func (o *recordingObserver) ObserveTransport(event core.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingObserver) snapshot() []core.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]core.Event(nil), o.events...)
}

type testDiscovery map[core.NodeID]string

func (d testDiscovery) Resolve(nodeID core.NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", core.ErrNodeNotFound
	}
	return addr, nil
}

func testLimits() core.Limits {
	return core.Limits{
		MaxFrameBodyBytes:     1024,
		MaxQueuedBytesPerConn: 1024,
		MaxQueuedItemsPerConn: 16,
		MaxBatchBytes:         1024,
		MaxBatchFrames:        4,
		DialFailureCooldown:   time.Millisecond,
		WriteTimeout:          time.Second,
	}
}

func TestManagerAcquireDialsOncePerSlot(t *testing.T) {
	var dials atomic.Int32
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		PoolSize:  2,
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer func() {
		serversMu.Lock()
		defer serversMu.Unlock()
		for _, server := range servers {
			_ = server.Close()
		}
	}()

	first, err := m.Acquire(context.Background(), 2, 7)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	second, err := m.Acquire(context.Background(), 2, 9)
	if err != nil {
		t.Fatalf("Acquire() second error = %v", err)
	}
	if first != second {
		t.Fatalf("Acquire() returned different conns for same slot")
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial count = %d, want 1", got)
	}
}

func TestManagerObservesPeerPool(t *testing.T) {
	observer := &recordingObserver{}
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		PoolSize:  2,
		Limits:    testLimits(),
		Observer:  observer,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer closeServers(&serversMu, &servers)

	if _, err := m.Acquire(context.Background(), 2, 0); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	waitPeerEvent(t, observer, func(event core.Event) bool {
		return event.Name == "peer_pool" && event.Result == "ok" && event.Items == 1 && event.Capacity == 2
	})

	stats := m.Stats()
	if stats.Peers != 1 || stats.Connections != 1 {
		t.Fatalf("Stats() = %+v, want one peer and one connection", stats)
	}
	waitPeerEvent(t, observer, func(event core.Event) bool {
		return event.Name == "peer_pool" && event.Result == "stats" && event.Items == 1 && event.Capacity == 2
	})

	m.ClosePeer(2)
	waitPeerEvent(t, observer, func(event core.Event) bool {
		return event.Name == "peer_pool" && event.Result == "closed" && event.Items == 0 && event.Capacity == 0
	})

	m.Stop()
	waitPeerEvent(t, observer, func(event core.Event) bool {
		return event.Name == "peer_pool" && event.Result == "stopped" && event.Items == 0 && event.Capacity == 0
	})
}

func TestManagerConcurrentAcquireDialsOnce(t *testing.T) {
	startDial := make(chan struct{})
	releaseDial := make(chan struct{})
	waitersReady := make(chan struct{})
	var dials atomic.Int32
	var blocked atomic.Int32
	var serversMu sync.Mutex
	var servers []net.Conn

	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		PoolSize:  1,
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			if dials.Add(1) == 1 {
				close(startDial)
			}
			if blocked.Add(1) == 1 {
				close(waitersReady)
			}
			<-releaseDial
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer func() {
		serversMu.Lock()
		defer serversMu.Unlock()
		for _, server := range servers {
			_ = server.Close()
		}
	}()

	const callers = 8
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			_, err := m.Acquire(context.Background(), 2, 0)
			results <- err
		}()
	}

	select {
	case <-startDial:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	waitFor(t, func() bool {
		return blocked.Load() == 1
	})
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial count while waiters blocked = %d, want 1", got)
	}
	select {
	case <-waitersReady:
	default:
		t.Fatal("owner dial did not block")
	}
	close(releaseDial)

	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("final dial count = %d, want 1", got)
	}
}

func TestManagerOwnerCancelDoesNotPoisonCooldown(t *testing.T) {
	startDial := make(chan struct{})
	var dials atomic.Int32
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if dials.Add(1) == 1 {
				close(startDial)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer closeServers(&serversMu, &servers)

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerResult := make(chan error, 1)
	go func() {
		_, err := m.Acquire(ownerCtx, 2, 0)
		ownerResult <- err
	}()
	waitForChannel(t, startDial, "owner dial did not start")

	waiterResult := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), 2, 0)
		waiterResult <- err
	}()
	cancelOwner()

	if err := <-ownerResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner Acquire() error = %v, want context.Canceled", err)
	}
	if err := <-waiterResult; err != nil {
		t.Fatalf("waiter Acquire() error = %v, want nil", err)
	}
	if got := dials.Load(); got < 2 {
		t.Fatalf("dial count = %d, want at least 2", got)
	}
}

func TestManagerClosePeerWhileDialingReturnsStoppedAndClosesDialedConn(t *testing.T) {
	startDial := make(chan struct{})
	releaseDial := make(chan struct{})
	var dialed net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			close(startDial)
			<-releaseDial
			client, server := net.Pipe()
			dialed = client
			_ = server.Close()
			return client, nil
		},
	})
	defer m.Stop()

	result := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), 2, 0)
		result <- err
	}()
	waitForChannel(t, startDial, "dial did not start")
	m.ClosePeer(2)
	close(releaseDial)

	if err := <-result; !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Acquire() error = %v, want ErrStopped", err)
	}
	if dialed == nil {
		t.Fatal("dial did not return a conn")
	}
	if _, err := dialed.Write([]byte("x")); err == nil {
		t.Fatal("dialed conn was not closed")
	}
	stats := m.Stats()
	if stats.Peers != 0 || stats.Connections != 0 {
		t.Fatalf("Stats() = %+v, want no peers or conns", stats)
	}
}

func TestManagerStopWhileDialingReturnsStoppedAndClosesDialedConn(t *testing.T) {
	startDial := make(chan struct{})
	releaseDial := make(chan struct{})
	var dialed net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			close(startDial)
			<-releaseDial
			client, server := net.Pipe()
			dialed = client
			_ = server.Close()
			return client, nil
		},
	})

	result := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), 2, 0)
		result <- err
	}()
	waitForChannel(t, startDial, "dial did not start")
	m.Stop()
	close(releaseDial)

	if err := <-result; !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Acquire() error = %v, want ErrStopped", err)
	}
	if dialed == nil {
		t.Fatal("dial did not return a conn")
	}
	if _, err := dialed.Write([]byte("x")); err == nil {
		t.Fatal("dialed conn was not closed")
	}
}

func TestManagerWaiterContextCancellation(t *testing.T) {
	startDial := make(chan struct{})
	releaseDial := make(chan struct{})
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			close(startDial)
			<-releaseDial
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer closeServers(&serversMu, &servers)

	ownerResult := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), 2, 0)
		ownerResult <- err
	}()
	waitForChannel(t, startDial, "dial did not start")

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterResult := make(chan error, 1)
	go func() {
		_, err := m.Acquire(waiterCtx, 2, 0)
		waiterResult <- err
	}()
	cancelWaiter()

	if err := <-waiterResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter Acquire() error = %v, want context.Canceled", err)
	}
	close(releaseDial)
	if err := <-ownerResult; err != nil {
		t.Fatalf("owner Acquire() error = %v", err)
	}
}

func TestManagerDialFailureCooldown(t *testing.T) {
	var dials atomic.Int32
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("dial boom")
		},
	})
	defer m.Stop()

	_, firstErr := m.Acquire(context.Background(), 2, 0)
	if !errors.Is(firstErr, core.ErrDialFailed) {
		t.Fatalf("first Acquire() error = %v, want ErrDialFailed", firstErr)
	}
	_, secondErr := m.Acquire(context.Background(), 2, 0)
	if !errors.Is(secondErr, core.ErrDialFailed) {
		t.Fatalf("second Acquire() error = %v, want ErrDialFailed", secondErr)
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial count during cooldown = %d, want 1", got)
	}

	time.Sleep(2 * testLimits().DialFailureCooldown)
	_, thirdErr := m.Acquire(context.Background(), 2, 0)
	if !errors.Is(thirdErr, core.ErrDialFailed) {
		t.Fatalf("third Acquire() error = %v, want ErrDialFailed", thirdErr)
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("dial count after cooldown = %d, want 2", got)
	}
}

func TestManagerAcquireRedialsClosedConn(t *testing.T) {
	var dials atomic.Int32
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer closeServers(&serversMu, &servers)

	first, err := m.Acquire(context.Background(), 2, 0)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	first.Close(core.ErrStopped)
	second, err := m.Acquire(context.Background(), 2, 0)
	if err != nil {
		t.Fatalf("Acquire() after close error = %v", err)
	}
	if first == second {
		t.Fatalf("Acquire() returned closed conn")
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}

func TestManagerStats(t *testing.T) {
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2", 3: "node-3"},
		PoolSize:  2,
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer closeServers(&serversMu, &servers)

	if _, err := m.Acquire(context.Background(), 2, 0); err != nil {
		t.Fatalf("Acquire() peer 2 slot 0 error = %v", err)
	}
	if _, err := m.Acquire(context.Background(), 2, 1); err != nil {
		t.Fatalf("Acquire() peer 2 slot 1 error = %v", err)
	}
	if _, err := m.Acquire(context.Background(), 3, 0); err != nil {
		t.Fatalf("Acquire() peer 3 error = %v", err)
	}

	stats := m.Stats()
	if stats.Peers != 2 || stats.Connections != 3 {
		t.Fatalf("Stats() = %+v, want 2 peers and 3 connections", stats)
	}
	m.ClosePeer(2)
	stats = m.Stats()
	if stats.Peers != 1 || stats.Connections != 1 {
		t.Fatalf("Stats() after ClosePeer = %+v, want 1 peer and 1 connection", stats)
	}
}

func TestManagerStopPreventsDial(t *testing.T) {
	var dials atomic.Int32
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			client, _ := net.Pipe()
			return client, nil
		},
	})
	m.Stop()

	_, err := m.Acquire(context.Background(), 2, 0)
	if !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Acquire() error = %v, want ErrStopped", err)
	}
	if got := dials.Load(); got != 0 {
		t.Fatalf("dial count = %d, want 0", got)
	}
}

func closeServers(mu *sync.Mutex, servers *[]net.Conn) {
	mu.Lock()
	defer mu.Unlock()
	for _, server := range *servers {
		_ = server.Close()
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func waitForChannel(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal(msg)
	}
}

func waitPeerEvent(t *testing.T, observer *recordingObserver, predicate func(core.Event) bool) []core.Event {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		events := observer.snapshot()
		for _, event := range events {
			if predicate(event) {
				return events
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for peer event; events=%#v", events)
		case <-ticker.C:
		}
	}
}

func TestManagerClosePeerRedialsNextAcquire(t *testing.T) {
	var dials atomic.Int32
	var serversMu sync.Mutex
	var servers []net.Conn
	m := NewManager(Config{
		Discovery: testDiscovery{2: "node-2"},
		Limits:    testLimits(),
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			client, server := net.Pipe()
			serversMu.Lock()
			servers = append(servers, server)
			serversMu.Unlock()
			return client, nil
		},
	})
	defer m.Stop()
	defer func() {
		serversMu.Lock()
		defer serversMu.Unlock()
		for _, server := range servers {
			_ = server.Close()
		}
	}()

	first, err := m.Acquire(context.Background(), 2, 0)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	m.ClosePeer(2)
	second, err := m.Acquire(context.Background(), 2, 0)
	if err != nil {
		t.Fatalf("Acquire() after ClosePeer error = %v", err)
	}
	if first == second {
		t.Fatalf("Acquire() returned closed conn after ClosePeer")
	}
	if got := dials.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}
