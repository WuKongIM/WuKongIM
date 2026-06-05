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

func TestManagerConcurrentAcquireDialsOnce(t *testing.T) {
	startDial := make(chan struct{})
	releaseDial := make(chan struct{})
	var dials atomic.Int32
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
	time.Sleep(20 * time.Millisecond)
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial count while waiters blocked = %d, want 1", got)
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
