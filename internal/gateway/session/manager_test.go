package session

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func newTestSession(id uint64) Session {
	return New(Config{
		ID:               id,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   1,
		MaxOutboundBytes: 1,
	})
}

func TestManagerAddGetRemove(t *testing.T) {
	mgr := NewManager()
	sess := newTestSession(42)

	mgr.Add(sess)

	got, ok := mgr.Get(42)
	if !ok {
		t.Fatal("expected session to be present after Add")
	}
	if got.ID() != sess.ID() {
		t.Fatalf("expected session ID %d, got %d", sess.ID(), got.ID())
	}

	if count := mgr.Count(); count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}

	mgr.Remove(42)

	if _, ok := mgr.Get(42); ok {
		t.Fatal("expected session to be removed")
	}
	if count := mgr.Count(); count != 0 {
		t.Fatalf("expected count 0, got %d", count)
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	sess := newTestSession(7)

	if err := sess.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}

func TestManagerRangeVisitsSessions(t *testing.T) {
	mgr := NewManager()
	mgr.Add(newTestSession(1))
	mgr.Add(newTestSession(2))

	visited := make(map[uint64]struct{}, 2)
	mgr.Range(func(sess Session) bool {
		visited[sess.ID()] = struct{}{}
		return true
	})

	if len(visited) != 2 {
		t.Fatalf("expected 2 sessions to be visited, got %d", len(visited))
	}
	if _, ok := visited[1]; !ok {
		t.Fatal("expected session 1 to be visited")
	}
	if _, ok := visited[2]; !ok {
		t.Fatal("expected session 2 to be visited")
	}
}

func TestSessionEnqueueEncodedReturnsWriteQueueFull(t *testing.T) {
	queue := mustEncodedQueue(t, New(Config{
		ID:               11,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   1,
		MaxOutboundBytes: 10,
	}))

	if err := queue.EnqueueEncoded([]byte("a")); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := queue.EnqueueEncoded([]byte("b")); !errors.Is(err, ErrWriteQueueFull) {
		t.Fatalf("expected write queue full error, got %v", err)
	}
}

func TestSessionEnqueueEncodedReturnsOutboundOverflow(t *testing.T) {
	queue := mustEncodedQueue(t, New(Config{
		ID:               12,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   2,
		MaxOutboundBytes: 3,
	}))

	if err := queue.EnqueueEncoded([]byte("abc")); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := queue.EnqueueEncoded([]byte("d")); !errors.Is(err, ErrOutboundOverflow) {
		t.Fatalf("expected outbound overflow error, got %v", err)
	}
}

func TestSessionDequeueEncodedReleasesOutboundAccounting(t *testing.T) {
	queue := mustEncodedQueue(t, New(Config{
		ID:               14,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   1,
		MaxOutboundBytes: 3,
	}))

	if err := queue.EnqueueEncoded([]byte("abc")); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	payload, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected payload to be dequeued")
	}
	if string(payload) != "abc" {
		t.Fatalf("expected payload abc, got %q", payload)
	}
	if err := queue.EnqueueEncoded([]byte("d")); err != nil {
		t.Fatalf("expected accounting to be released after dequeue, got %v", err)
	}
}

func TestSessionCloseBlocksConcurrentWriteUntilClosed(t *testing.T) {
	sess := newSession(13, "listener-a", "remote-a", "local-a", 2, 10, nil)

	var writeCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	sess.writeFrameFn = func(f frame.Frame, meta OutboundMeta) error {
		writeCalls.Add(1)
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return sess.enqueueEncoded([]byte("x"))
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- sess.WriteFrame(&frame.PingPacket{})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- sess.Close()
	}()

	deadline := time.After(time.Second)
	for !sess.closing.Load() {
		select {
		case <-deadline:
			t.Fatal("close did not begin")
		default:
			runtime.Gosched()
		}
	}

	secondWriteDone := make(chan error, 1)
	go func() {
		secondWriteDone <- sess.WriteFrame(&frame.PingPacket{})
	}()

	select {
	case err := <-secondWriteDone:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("expected second write to fail after close began, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second write did not complete")
	}

	close(release)

	if err := <-writeDone; err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if calls := writeCalls.Load(); calls != 1 {
		t.Fatalf("expected exactly one writeFrameFn call, got %d", calls)
	}
}

func mustEncodedQueue(t *testing.T, sess Session) EncodedQueue {
	t.Helper()
	queue, ok := sess.(EncodedQueue)
	if !ok {
		t.Fatalf("session does not expose encoded queue operations: %T", sess)
	}
	return queue
}
