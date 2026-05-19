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

func TestSessionLazilyAllocatesWriteQueue(t *testing.T) {
	sess := newSession(8, "listener-a", "remote-a", "local-a", 2, 10, nil)

	if sess.writeCh != nil {
		t.Fatal("expected write queue to stay nil before first enqueue")
	}

	if err := sess.enqueueEncoded([]byte("a")); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if sess.writeCh == nil {
		t.Fatal("expected write queue to be allocated on first enqueue")
	}
	if got, want := cap(sess.writeCh), 2; got != want {
		t.Fatalf("write queue capacity = %d, want %d", got, want)
	}
}

func TestSessionCloseBeforeFirstEnqueueDoesNotAllocateWriteQueue(t *testing.T) {
	sess := newSession(10, "listener-a", "remote-a", "local-a", 2, 10, nil)

	if err := sess.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if sess.writeCh != nil {
		t.Fatal("expected write queue to remain nil after closing an idle session")
	}
	if err := sess.enqueueEncoded([]byte("a")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("enqueue after close = %v, want %v", err, ErrSessionClosed)
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

func TestSessionValueStoresHotAndCustomValues(t *testing.T) {
	sess := newTestSession(9)

	sess.SetValue("gateway.uid", "u1")
	sess.SetValue("custom.key", "custom-value")

	if got := sess.Value("gateway.uid"); got != "u1" {
		t.Fatalf("hot value = %#v, want u1", got)
	}
	if got := sess.Value("custom.key"); got != "custom-value" {
		t.Fatalf("custom value = %#v, want custom-value", got)
	}
	if got := sess.Value("gateway.device_id"); got != nil {
		t.Fatalf("unset hot value = %#v, want nil", got)
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

func TestSessionReleaseEncodedReleasesOutboundAccounting(t *testing.T) {
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
	if err := queue.EnqueueEncoded([]byte("d")); !errors.Is(err, ErrOutboundOverflow) {
		t.Fatalf("expected accounting to remain held until release, got %v", err)
	}
	queue.ReleaseEncoded(payload)
	if err := queue.EnqueueEncoded([]byte("d")); err != nil {
		t.Fatalf("expected accounting to be released after explicit release, got %v", err)
	}
}

func TestSessionEnqueueEncodedCopiesPayload(t *testing.T) {
	queue := mustEncodedQueue(t, New(Config{
		ID:               15,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   1,
		MaxOutboundBytes: 10,
	}))
	payload := []byte("abc")

	if err := queue.EnqueueEncoded(payload); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	payload[0] = 'x'

	got, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected payload to be dequeued")
	}
	if string(got) != "abc" {
		t.Fatalf("dequeued payload = %q, want original copy", got)
	}
}

func TestSessionEnqueueOwnedEncodedRetainsPayloadBackingArray(t *testing.T) {
	queue := mustEncodedQueue(t, New(Config{
		ID:               16,
		Listener:         "listener-a",
		RemoteAddr:       "remote-a",
		LocalAddr:        "local-a",
		WriteQueueSize:   1,
		MaxOutboundBytes: 10,
	}))
	owned, ok := queue.(interface {
		EnqueueOwnedEncoded([]byte) error
	})
	if !ok {
		t.Fatalf("session queue %T does not expose owned encoded enqueue", queue)
	}
	payload := []byte("abc")

	if err := owned.EnqueueOwnedEncoded(payload); err != nil {
		t.Fatalf("owned enqueue failed: %v", err)
	}
	got, ok := queue.DequeueEncoded()
	if !ok {
		t.Fatal("expected payload to be dequeued")
	}
	if len(got) == 0 || &got[0] != &payload[0] {
		t.Fatal("owned enqueue copied payload backing array")
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
