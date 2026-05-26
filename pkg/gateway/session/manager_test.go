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
		ID:         id,
		Listener:   "listener-a",
		RemoteAddr: "remote-a",
		LocalAddr:  "local-a",
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

func TestSessionWriteFramePassesReplyToken(t *testing.T) {
	var gotToken string
	sess := New(Config{
		ID:         11,
		Listener:   "listener-a",
		RemoteAddr: "remote-a",
		LocalAddr:  "local-a",
		WriteFrameFn: func(f frame.Frame, meta OutboundMeta) error {
			gotToken = meta.ReplyToken
			return nil
		},
	})

	if err := sess.WriteFrame(&frame.PingPacket{}, WithReplyToken("reply-1")); err != nil {
		t.Fatalf("WriteFrame failed: %v", err)
	}
	if gotToken != "reply-1" {
		t.Fatalf("reply token = %q, want reply-1", gotToken)
	}
}

func TestSessionCloseBlocksConcurrentWriteUntilClosed(t *testing.T) {
	sess := newSession(13, "listener-a", "remote-a", "local-a", nil)

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
		return nil
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
