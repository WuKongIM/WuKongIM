package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type staticDiscovery struct {
	addrs map[NodeID]string
}

func (d staticDiscovery) Resolve(nodeID NodeID) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return addr, nil
}

type mutableDiscovery struct {
	mu    sync.RWMutex
	addrs map[NodeID]string
}

func (d *mutableDiscovery) Resolve(nodeID NodeID) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return addr, nil
}

func (d *mutableDiscovery) Set(nodeID NodeID, addr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addrs[nodeID] = addr
}

func TestPoolSendDeliversMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	received := make(chan []byte, 1)
	go acceptFrames(t, ln, func(msgType uint8, body []byte) {
		if msgType == 9 {
			received <- append([]byte(nil), body...)
		}
	})

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        2,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 9, []byte("hello")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case body := <-received:
		if string(body) != "hello" {
			t.Fatalf("body = %q, want %q", body, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for send")
	}
}

func TestPoolObserverTracksSentBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	received := make(chan struct{}, 1)
	go acceptFrames(t, ln, func(msgType uint8, body []byte) {
		if msgType == 9 && string(body) == "hello" {
			received <- struct{}{}
		}
	})

	var (
		gotType  uint8
		gotBytes int
		calls    int
	)
	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
		Observer: ObserverHooks{
			OnSend: func(msgType uint8, bytes int) {
				calls++
				gotType = msgType
				gotBytes = bytes
			},
		},
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 9, []byte("hello")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for send")
	}

	requireEventually(t, func() bool { return calls == 1 })
	if gotType != 9 {
		t.Fatalf("observer msgType = %d, want 9", gotType)
	}
	if gotBytes != len("hello") {
		t.Fatalf("observer bytes = %d, want %d", gotBytes, len("hello"))
	}
}

func TestPoolObserverTracksDialLifecycle(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		go acceptFrames(t, ln, func(uint8, []byte) {})

		events := make(chan DialEvent, 1)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{4, 4, 4},
			DefaultPri:  PriorityRaft,
			Observer: ObserverHooks{
				OnDial: func(event DialEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		if err := pool.Send(2, 0, 9, []byte("hello")); err != nil {
			t.Fatalf("Send() error = %v", err)
		}

		select {
		case event := <-events:
			if event.TargetNode != 2 {
				t.Fatalf("dial target = %d, want 2", event.TargetNode)
			}
			if event.Result != "ok" {
				t.Fatalf("dial result = %q, want ok", event.Result)
			}
			if event.Duration <= 0 {
				t.Fatalf("dial duration = %s, want > 0", event.Duration)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for dial event")
		}
	})

	t.Run("dial_error", func(t *testing.T) {
		wantErr := errors.New("boom")
		events := make(chan DialEvent, 1)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: "127.0.0.1:1"}},
			Size:        1,
			DialTimeout: time.Second,
			Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
				return nil, wantErr
			},
			QueueSizes: [numPriorities]int{4, 4, 4},
			DefaultPri: PriorityRaft,
			Observer: ObserverHooks{
				OnDial: func(event DialEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		err := pool.Send(2, 0, 9, []byte("hello"))
		if !errors.Is(err, wantErr) {
			t.Fatalf("Send() error = %v, want %v", err, wantErr)
		}

		select {
		case event := <-events:
			if event.TargetNode != 2 {
				t.Fatalf("dial target = %d, want 2", event.TargetNode)
			}
			if event.Result != "dial_error" {
				t.Fatalf("dial result = %q, want dial_error", event.Result)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for dial event")
		}
	})
}

func TestPoolObserverTracksEnqueueResults(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			msgType, body, release, err := readFrame(conn)
			if err != nil {
				return
			}
			release()
			if msgType != MsgTypeRPCRequest {
				return
			}
			reqID := decodeRequestID(body)
			var bufs net.Buffers
			writeFrame(&bufs, MsgTypeRPCResponse, encodeRPCResponse(reqID, 0, []byte("pong")))
			_, _ = bufs.WriteTo(conn)
		}()

		events := make(chan EnqueueEvent, 1)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{4, 4, 4},
			DefaultPri:  PriorityRPC,
			Observer: ObserverHooks{
				OnEnqueue: func(event EnqueueEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		resp, err := pool.RPC(context.Background(), 2, 0, []byte("ping"))
		if err != nil {
			t.Fatalf("RPC() error = %v", err)
		}
		if string(resp) != "pong" {
			t.Fatalf("RPC() response = %q, want %q", resp, "pong")
		}

		select {
		case event := <-events:
			if event.TargetNode != 2 {
				t.Fatalf("enqueue target = %d, want 2", event.TargetNode)
			}
			if event.Kind != "rpc" {
				t.Fatalf("enqueue kind = %q, want rpc", event.Kind)
			}
			if event.Result != "ok" {
				t.Fatalf("enqueue result = %q, want ok", event.Result)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for enqueue event")
		}
	})

	t.Run("queue_full", func(t *testing.T) {
		clientConn := newBlockingConn()

		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: "pipe"}},
			Size:        1,
			DialTimeout: time.Second,
			Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
				return clientConn, nil
			},
			QueueSizes: [numPriorities]int{1, 1, 1},
			DefaultPri: PriorityRaft,
		})
		defer pool.Close()

		events := make(chan EnqueueEvent, 4)
		pool.cfg.Observer.OnEnqueue = func(event EnqueueEvent) {
			events <- event
		}

		if err := pool.Send(2, 0, 9, []byte("a")); err != nil {
			t.Fatalf("first Send() error = %v", err)
		}
		time.Sleep(20 * time.Millisecond)
		if err := pool.Send(2, 0, 9, []byte("b")); err != nil {
			t.Fatalf("second Send() error = %v", err)
		}
		err := pool.Send(2, 0, 9, []byte("c"))
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("third Send() error = %v, want %v", err, ErrQueueFull)
		}

		requireEventually(t, func() bool {
			for len(events) > 0 {
				event := <-events
				if event.Result == "queue_full" {
					return event.TargetNode == 2 && event.Kind == "raft"
				}
			}
			return false
		})
	})

	t.Run("stopped", func(t *testing.T) {
		events := make(chan EnqueueEvent, 1)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: "127.0.0.1:1"}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{1, 1, 1},
			DefaultPri:  PriorityRPC,
			Observer: ObserverHooks{
				OnEnqueue: func(event EnqueueEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		set, err := pool.getOrCreateNodeSet(2)
		if err != nil {
			t.Fatalf("getOrCreateNodeSet() error = %v", err)
		}
		set.slots[0].mu.Lock()
		set.slots[0].lastErr = ErrStopped
		set.slots[0].lastDialFail = time.Now()
		set.slots[0].mu.Unlock()

		_, err = pool.RPC(context.Background(), 2, 0, []byte("ping"))
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("RPC() error = %v, want %v", err, ErrStopped)
		}

		select {
		case event := <-events:
			if event.TargetNode != 2 {
				t.Fatalf("enqueue target = %d, want 2", event.TargetNode)
			}
			if event.Kind != "rpc" {
				t.Fatalf("enqueue kind = %q, want rpc", event.Kind)
			}
			if event.Result != "stopped" {
				t.Fatalf("enqueue result = %q, want stopped", event.Result)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for stopped enqueue event")
		}
	})
}

func TestPoolStatsReportActiveAndIdleConnections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go acceptFrames(t, ln, func(uint8, []byte) {})

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        2,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 1, []byte("a")); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}

	stats := pool.Stats()
	if len(stats) != 1 {
		t.Fatalf("Stats() len = %d, want 1", len(stats))
	}
	if stats[0].NodeID != 2 {
		t.Fatalf("Stats() nodeID = %d, want 2", stats[0].NodeID)
	}
	if stats[0].Active != 1 {
		t.Fatalf("Stats() active = %d, want 1", stats[0].Active)
	}
	if stats[0].Idle != 1 {
		t.Fatalf("Stats() idle = %d, want 1", stats[0].Idle)
	}
}

func TestPoolRPCRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		msgType, body, release, err := readFrame(conn)
		if err != nil {
			return
		}
		release()
		if msgType != MsgTypeRPCRequest {
			return
		}
		reqID := decodeRequestID(body)
		var bufs net.Buffers
		writeFrame(&bufs, MsgTypeRPCResponse, encodeRPCResponse(reqID, 0, []byte("pong")))
		_, _ = bufs.WriteTo(conn)
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRPC,
	})
	defer pool.Close()

	resp, err := pool.RPC(context.Background(), 2, 0, []byte("ping"))
	if err != nil {
		t.Fatalf("RPC() error = %v", err)
	}
	if string(resp) != "pong" {
		t.Fatalf("resp = %q, want %q", resp, "pong")
	}
}

func TestPoolNodeNotFound(t *testing.T) {
	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRPC,
	})
	defer pool.Close()

	err := pool.Send(99, 0, 1, []byte("x"))
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Send() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestPoolReusesConnectionForSameShard(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go drainConn(conn)
		}
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        2,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 1, []byte("a")); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if err := pool.Send(2, 2, 1, []byte("b")); err != nil {
		t.Fatalf("second Send() error = %v", err)
	}

	requireEventually(t, func() bool { return accepted.Load() == 1 })
}

func TestPoolAcquireConcurrentColdSlotSharesSingleDialAndError(t *testing.T) {
	var dials atomic.Int32
	var extraDial atomic.Bool
	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: "dial-counter-test"}},
		Size:        1,
		DialTimeout: 20 * time.Millisecond,
		Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
			if dials.Add(1) > 1 {
				extraDial.Store(true)
			}
			time.Sleep(30 * time.Millisecond)
			return nil, errors.New("boom")
		},
		QueueSizes: [numPriorities]int{4, 4, 4},
		DefaultPri: PriorityRaft,
	})
	defer pool.Close()

	resultCh := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			_, err := pool.acquire(2, 0)
			resultCh <- err
		}()
	}

	close(start)
	firstErr := <-resultCh
	if firstErr == nil {
		t.Fatal("first acquire unexpectedly succeeded")
	}

	for i := 1; i < 8; i++ {
		err := <-resultCh
		if err == nil {
			t.Fatalf("acquire %d succeeded, want cached failure", i)
		}
		if err.Error() != firstErr.Error() {
			t.Fatalf("acquire %d error = %v, want %v", i, err, firstErr)
		}
	}
	if extraDial.Load() {
		t.Fatal("saw more than one dial attempt")
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want 1", got)
	}
}

func TestPoolAcquireReturnsCachedDialFailureDuringCooldown(t *testing.T) {
	var dials atomic.Int32
	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: "cooldown-test"}},
		Size:        1,
		DialTimeout: 20 * time.Millisecond,
		Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("boom")
		},
		QueueSizes: [numPriorities]int{4, 4, 4},
		DefaultPri: PriorityRaft,
	})
	defer pool.Close()

	firstErr := mustAcquireError(t, pool, 2, 0)

	secondErr := mustAcquireError(t, pool, 2, 0)
	if secondErr.Error() != firstErr.Error() {
		t.Fatalf("second acquire error = %v, want cached %v", secondErr, firstErr)
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("dial attempts = %d, want 1", got)
	}
}

func TestPoolAcquireAfterCooldownStartsOneNewDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	var dials atomic.Int32
	var accepts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go drainConn(conn)
		}
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: addr}},
		Size:        1,
		DialTimeout: 20 * time.Millisecond,
		Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
			attempt := dials.Add(1)
			if attempt == 1 {
				return nil, errors.New("boom")
			}
			return net.DialTimeout(network, addr, timeout)
		},
		QueueSizes: [numPriorities]int{4, 4, 4},
		DefaultPri: PriorityRaft,
	})
	defer pool.Close()

	_ = mustAcquireError(t, pool, 2, 0)
	time.Sleep(75 * time.Millisecond)

	resultCh := make(chan error, 8)
	connCh := make(chan *MuxConn, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			mc, err := pool.acquire(2, 0)
			if err == nil {
				connCh <- mc
			}
			resultCh <- err
		}()
	}

	close(start)

	var firstConn *MuxConn
	for i := 0; i < 8; i++ {
		if err := <-resultCh; err != nil {
			t.Fatalf("acquire %d error = %v, want success after cooldown", i, err)
		}
		mc := <-connCh
		if firstConn == nil {
			firstConn = mc
			continue
		}
		if mc != firstConn {
			t.Fatalf("acquire %d returned different connection", i)
		}
	}
	requireEventually(t, func() bool { return dials.Load() == 2 })
	requireEventually(t, func() bool { return accepts.Load() == 1 })
}

func TestPoolAcquireClosedSlotTriggersSingleRewarm(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	defer ln.Close()

	var dials atomic.Int32
	var accepts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: addr}},
		Size:        1,
		DialTimeout: 20 * time.Millisecond,
		Dial: func(network, addr string, timeout time.Duration) (net.Conn, error) {
			dials.Add(1)
			return net.DialTimeout(network, addr, timeout)
		},
		QueueSizes: [numPriorities]int{4, 4, 4},
		DefaultPri: PriorityRaft,
	})
	defer pool.Close()

	mc, err := pool.acquire(2, 0)
	if err != nil {
		t.Fatalf("initial acquire error = %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	mc.Close()

	firstErr := mustAcquireError(t, pool, 2, 0)
	if firstErr == nil {
		t.Fatal("first closed-slot acquire unexpectedly succeeded")
	}

	time.Sleep(75 * time.Millisecond)

	rewarmLn, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	rewarmDone := make(chan struct{})
	go func() {
		defer close(rewarmDone)
		for {
			conn, err := rewarmLn.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go drainConn(conn)
		}
	}()

	results := make(chan error, 8)
	conns := make(chan *MuxConn, 8)
	for i := 0; i < 8; i++ {
		go func() {
			mc, err := pool.acquire(2, 0)
			if err == nil {
				conns <- mc
			}
			results <- err
		}()
	}

	var firstRewarmConn *MuxConn
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatalf("rewarm acquire %d error = %v, want success", i, err)
		}
		mc := <-conns
		if firstRewarmConn == nil {
			firstRewarmConn = mc
			continue
		}
		if mc != firstRewarmConn {
			t.Fatalf("rewarm acquire %d returned a different connection", i)
		}
	}

	requireEventually(t, func() bool { return dials.Load() == 3 })

	reused, err := pool.acquire(2, 0)
	if err != nil {
		t.Fatalf("post-rewarm acquire error = %v", err)
	}
	if reused != firstRewarmConn {
		t.Fatalf("post-rewarm acquire returned a different connection")
	}
	if got := dials.Load(); got != 3 {
		t.Fatalf("dial attempts = %d, want 3", got)
	}

	_ = rewarmLn.Close()
	<-rewarmDone
}

func TestPoolAcquireClosedSlotRefreshesDiscoveryAddressAfterRestart(t *testing.T) {
	oldLn := mustListenOnAddr(t, reserveTCPAddr(t))
	defer oldLn.Close()

	oldAccepted := make(chan struct{}, 1)
	go func() {
		conn, err := oldLn.Accept()
		if err != nil {
			return
		}
		oldAccepted <- struct{}{}
		drainConn(conn)
	}()

	newLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer newLn.Close()

	newAccepted := make(chan struct{}, 1)
	go func() {
		conn, err := newLn.Accept()
		if err != nil {
			return
		}
		newAccepted <- struct{}{}
		drainConn(conn)
	}()

	discovery := &mutableDiscovery{addrs: map[NodeID]string{2: oldLn.Addr().String()}}
	pool := NewPool(PoolConfig{
		Discovery:   discovery,
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	mc, err := pool.acquire(2, 0)
	if err != nil {
		t.Fatalf("initial acquire error = %v", err)
	}
	select {
	case <-oldAccepted:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for old listener accept")
	}

	discovery.Set(2, newLn.Addr().String())
	if err := oldLn.Close(); err != nil {
		t.Fatal(err)
	}
	mc.Close()

	rewarmed, err := pool.acquire(2, 0)
	if err != nil {
		t.Fatalf("acquire after restart error = %v, want redial using refreshed discovery address", err)
	}
	if rewarmed == mc {
		t.Fatal("acquire after restart reused closed connection")
	}
	select {
	case <-newAccepted:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for new listener accept")
	}
}

func TestPoolSendRecoversAfterPeerRestartOnSameAddress(t *testing.T) {
	addr := reserveTCPAddr(t)
	firstLn := mustListenOnAddr(t, addr)

	firstAccepted := make(chan net.Conn, 1)
	firstReceived := make(chan []byte, 1)
	go func() {
		conn, err := firstLn.Accept()
		if err != nil {
			return
		}
		firstAccepted <- conn
		msgType, body, release, err := readFrame(conn)
		if err == nil && msgType == 1 {
			firstReceived <- append([]byte(nil), body...)
			release()
		}
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: addr}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 1, []byte("first")); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	select {
	case body := <-firstReceived:
		if string(body) != "first" {
			t.Fatalf("first body = %q, want %q", body, "first")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first send")
	}

	var firstConn net.Conn
	select {
	case firstConn = <-firstAccepted:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first accept")
	}
	if err := firstLn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstConn.Close(); err != nil {
		t.Fatal(err)
	}

	secondLn := mustListenOnAddr(t, addr)
	defer secondLn.Close()

	secondReceived := make(chan []byte, 1)
	go func() {
		conn, err := secondLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		msgType, body, release, err := readFrame(conn)
		if err == nil && msgType == 1 {
			secondReceived <- append([]byte(nil), body...)
			release()
		}
	}()

	requireEventually(t, func() bool {
		if err := pool.Send(2, 0, 1, []byte("second")); err != nil {
			return false
		}
		select {
		case body := <-secondReceived:
			return string(body) == "second"
		default:
			return false
		}
	})
}

func TestPoolSendRecoversAfterRepeatedDialFailuresOnSameAddress(t *testing.T) {
	addr := reserveTCPAddr(t)
	firstLn := mustListenOnAddr(t, addr)

	firstAccepted := make(chan net.Conn, 1)
	go func() {
		conn, err := firstLn.Accept()
		if err != nil {
			return
		}
		firstAccepted <- conn
		drainConn(conn)
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: addr}},
		Size:        1,
		DialTimeout: 50 * time.Millisecond,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	if err := pool.Send(2, 0, 1, []byte("first")); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}

	var firstConn net.Conn
	select {
	case firstConn = <-firstAccepted:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first accept")
	}
	if err := firstLn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstConn.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = pool.Send(2, 0, 1, []byte("during-downtime"))
		time.Sleep(10 * time.Millisecond)
	}

	secondLn := mustListenOnAddr(t, addr)
	defer secondLn.Close()

	secondReceived := make(chan []byte, 1)
	go func() {
		conn, err := secondLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		msgType, body, release, err := readFrame(conn)
		if err == nil && msgType == 1 {
			secondReceived <- append([]byte(nil), body...)
			release()
		}
	}()

	requireEventually(t, func() bool {
		if err := pool.Send(2, 0, 1, []byte("after-restart")); err != nil {
			return false
		}
		select {
		case body := <-secondReceived:
			return string(body) == "after-restart"
		default:
			return false
		}
	})
}

func TestPoolCloseFailsPendingRPC(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	started := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, release, err := readFrame(conn); err == nil {
			release()
			started <- struct{}{}
			time.Sleep(time.Second)
		}
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRPC,
	})
	defer pool.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := pool.RPC(context.Background(), 2, 0, []byte("req"))
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc to start")
	}

	pool.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("RPC() error = nil, want failure after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for pending rpc failure")
	}
}

func acceptFrames(t *testing.T, ln net.Listener, fn func(uint8, []byte)) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		msgType, body, release, err := readFrame(conn)
		if err != nil {
			return
		}
		fn(msgType, body)
		release()
	}
}

func drainConn(conn net.Conn) {
	defer conn.Close()
	for {
		_, _, release, err := readFrame(conn)
		if err != nil {
			return
		}
		release()
	}
}

func requireEventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func reserveTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

type blockingConn struct {
	closeOnce    sync.Once
	closed       chan struct{}
	releaseWrite chan struct{}
}

func newBlockingConn() *blockingConn {
	return &blockingConn{
		closed:       make(chan struct{}),
		releaseWrite: make(chan struct{}),
	}
}

func (c *blockingConn) Read(_ []byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *blockingConn) Write(b []byte) (int, error) {
	select {
	case <-c.releaseWrite:
		return len(b), nil
	case <-c.closed:
		return 0, io.ErrClosedPipe
	}
}

func (c *blockingConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	return nil
}

func (c *blockingConn) LocalAddr() net.Addr              { return blockingAddr("local") }
func (c *blockingConn) RemoteAddr() net.Addr             { return blockingAddr("remote") }
func (c *blockingConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingConn) SetWriteDeadline(time.Time) error { return nil }

type blockingAddr string

func (a blockingAddr) Network() string { return "blocking" }
func (a blockingAddr) String() string  { return string(a) }

func mustListenOnAddr(t *testing.T, addr string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func mustAcquireError(t *testing.T, pool *Pool, nodeID NodeID, shardKey uint64) error {
	t.Helper()
	_, err := pool.acquire(nodeID, shardKey)
	if err == nil {
		t.Fatal("acquire unexpectedly succeeded")
	}
	return err
}
