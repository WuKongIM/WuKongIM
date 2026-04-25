package transport

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestClientSend(t *testing.T) {
	s := NewServer()
	received := make(chan []byte, 1)
	s.Handle(1, func(body []byte) {
		received <- append([]byte(nil), body...)
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	defer pool.Close()

	client := NewClient(pool)
	if err := client.Send(2, 0, 1, []byte("hello")); err != nil {
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

func TestClientRPCService(t *testing.T) {
	s := NewServer()
	mux := NewRPCMux()
	mux.Handle(7, func(ctx context.Context, body []byte) ([]byte, error) {
		return append([]byte("ok:"), body...), nil
	})
	s.HandleRPCMux(mux)
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRPC,
	})
	defer pool.Close()

	client := NewClient(pool)
	resp, err := client.RPCService(context.Background(), 2, 0, 7, []byte("ping"))
	if err != nil {
		t.Fatalf("RPCService() error = %v", err)
	}
	if string(resp) != "ok:ping" {
		t.Fatalf("resp = %q, want %q", resp, "ok:ping")
	}
}

func TestClientRPCServiceTracksObserverEvents(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := NewServer()
		mux := NewRPCMux()
		mux.Handle(7, func(ctx context.Context, body []byte) ([]byte, error) {
			return append([]byte("ok:"), body...), nil
		})
		s.HandleRPCMux(mux)
		if err := s.Start("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		events := make(chan RPCClientEvent, 4)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{4, 4, 4},
			DefaultPri:  PriorityRPC,
			Observer: ObserverHooks{
				OnRPCClient: func(event RPCClientEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		client := NewClient(pool)
		resp, err := client.RPCService(context.Background(), 2, 0, 7, []byte("ping"))
		if err != nil {
			t.Fatalf("RPCService() error = %v", err)
		}
		if string(resp) != "ok:ping" {
			t.Fatalf("resp = %q, want %q", resp, "ok:ping")
		}

		start := <-events
		if start.TargetNode != 2 || start.ServiceID != 7 || start.Inflight != 1 {
			t.Fatalf("start event = %+v, want target=2 service=7 inflight=1", start)
		}
		done := <-events
		if done.TargetNode != 2 || done.ServiceID != 7 {
			t.Fatalf("done event = %+v, want target=2 service=7", done)
		}
		if done.Result != "ok" {
			t.Fatalf("done result = %q, want ok", done.Result)
		}
		if done.Inflight != 0 {
			t.Fatalf("done inflight = %d, want 0", done.Inflight)
		}
		if done.Duration <= 0 {
			t.Fatalf("done duration = %s, want > 0", done.Duration)
		}
	})

	t.Run("timeout", func(t *testing.T) {
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
			if _, _, release, err := readFrame(conn); err == nil {
				release()
				time.Sleep(time.Second)
			}
		}()

		events := make(chan RPCClientEvent, 4)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{4, 4, 4},
			DefaultPri:  PriorityRPC,
			Observer: ObserverHooks{
				OnRPCClient: func(event RPCClientEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		client := NewClient(pool)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err = client.RPCService(ctx, 2, 0, 7, []byte("ping"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RPCService() error = %v, want %v", err, context.DeadlineExceeded)
		}

		<-events
		done := <-events
		if done.Result != "timeout" {
			t.Fatalf("done result = %q, want timeout", done.Result)
		}
	})

	t.Run("remote_error", func(t *testing.T) {
		s := NewServer()
		mux := NewRPCMux()
		mux.Handle(7, func(ctx context.Context, body []byte) ([]byte, error) {
			return nil, errors.New("boom")
		})
		s.HandleRPCMux(mux)
		if err := s.Start("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		defer s.Stop()

		events := make(chan RPCClientEvent, 4)
		pool := NewPool(PoolConfig{
			Discovery:   staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}},
			Size:        1,
			DialTimeout: time.Second,
			QueueSizes:  [numPriorities]int{4, 4, 4},
			DefaultPri:  PriorityRPC,
			Observer: ObserverHooks{
				OnRPCClient: func(event RPCClientEvent) {
					events <- event
				},
			},
		})
		defer pool.Close()

		client := NewClient(pool)
		_, err := client.RPCService(context.Background(), 2, 0, 7, []byte("ping"))
		if err == nil || !strings.Contains(err.Error(), "remote error") {
			t.Fatalf("RPCService() error = %v, want remote error", err)
		}

		<-events
		done := <-events
		if done.Result != "remote_error" {
			t.Fatalf("done result = %q, want remote_error", done.Result)
		}
	})
}

func TestClientRPCServiceTracksInflightGauge(t *testing.T) {
	s := NewServer()
	mux := NewRPCMux()
	release := make(chan struct{})
	mux.Handle(7, func(ctx context.Context, body []byte) ([]byte, error) {
		<-release
		return []byte("ok"), nil
	})
	s.HandleRPCMux(mux)
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	events := make(chan RPCClientEvent, 4)
	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRPC,
		Observer: ObserverHooks{
			OnRPCClient: func(event RPCClientEvent) {
				events <- event
			},
		},
	})
	defer pool.Close()

	client := NewClient(pool)
	errCh := make(chan error, 1)
	go func() {
		_, err := client.RPCService(context.Background(), 2, 0, 7, []byte("ping"))
		errCh <- err
	}()

	start := <-events
	if start.Inflight != 1 {
		t.Fatalf("start inflight = %d, want 1", start.Inflight)
	}

	close(release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RPCService() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc completion")
	}

	done := <-events
	if done.Inflight != 0 {
		t.Fatalf("done inflight = %d, want 0", done.Inflight)
	}
	if done.Result != "ok" {
		t.Fatalf("done result = %q, want ok", done.Result)
	}
}

func TestClientStopClosesPoolConnections(t *testing.T) {
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
		drainConn(conn)
	}()

	pool := NewPool(PoolConfig{
		Discovery:   staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}},
		Size:        1,
		DialTimeout: time.Second,
		QueueSizes:  [numPriorities]int{4, 4, 4},
		DefaultPri:  PriorityRaft,
	})
	client := NewClient(pool)

	if err := client.Send(2, 0, 1, []byte("hello")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	set, err := pool.getOrCreateNodeSet(2)
	if err != nil {
		t.Fatalf("getOrCreateNodeSet() error = %v", err)
	}
	requireEventually(t, func() bool {
		return set.conns[0].Load() != nil
	})

	client.Stop()

	requireEventually(t, func() bool {
		mc := set.conns[0].Load()
		return mc != nil && mc.closed.Load()
	})
}

func TestClientPendingRPCFailsWhenPoolCloses(t *testing.T) {
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
	client := NewClient(pool)
	defer pool.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := client.RPC(context.Background(), 2, 0, []byte("req"))
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
			t.Fatal("RPC() error = nil, want failure")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc failure")
	}
}

func TestClientStopFailsPendingRPCWithErrStopped(t *testing.T) {
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
	client := NewClient(pool)

	errCh := make(chan error, 1)
	go func() {
		_, err := client.RPC(context.Background(), 2, 0, []byte("req"))
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc to start")
	}

	client.Stop()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("RPC() error = %v, want %v", err, ErrStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rpc failure")
	}
}
