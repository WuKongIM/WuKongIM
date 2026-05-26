package jsonrpc

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
)

func BenchmarkAdapterReplyTokenQueue(b *testing.B) {
	b.Run("per_session_single", func(b *testing.B) {
		adapter := New()
		sess := newBenchmarkSession(1)
		if err := adapter.OnOpen(sess); err != nil {
			b.Fatalf("OnOpen: %v", err)
		}

		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			adapter.pushReplyToken(sess, "req-1")
			tokens := adapter.TakeReplyTokens(sess, 1)
			if len(tokens) != 1 {
				b.Fatalf("tokens = %v, want one token", tokens)
			}
		}
	})

	b.Run("per_session_parallel", func(b *testing.B) {
		adapter := New()
		var nextID atomic.Uint64

		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			sess := newBenchmarkSession(nextID.Add(1))
			if err := adapter.OnOpen(sess); err != nil {
				b.Fatalf("OnOpen: %v", err)
			}
			for pb.Next() {
				adapter.pushReplyToken(sess, "req-1")
				tokens := adapter.TakeReplyTokens(sess, 1)
				if len(tokens) != 1 {
					b.Fatalf("tokens = %v, want one token", tokens)
				}
			}
		})
	})

	b.Run("global_map_single", func(b *testing.B) {
		tracker := newBenchmarkGlobalTokenTracker()
		sess := newBenchmarkSession(1)

		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			tracker.push(sess, "req-1")
			tokens := tracker.take(sess, 1)
			if len(tokens) != 1 {
				b.Fatalf("tokens = %v, want one token", tokens)
			}
		}
	})

	b.Run("global_map_parallel", func(b *testing.B) {
		tracker := newBenchmarkGlobalTokenTracker()
		var nextID atomic.Uint64

		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			sess := newBenchmarkSession(nextID.Add(1))
			for pb.Next() {
				tracker.push(sess, "req-1")
				tokens := tracker.take(sess, 1)
				if len(tokens) != 1 {
					b.Fatalf("tokens = %v, want one token", tokens)
				}
			}
		})
	})
}

type benchmarkGlobalTokenTracker struct {
	mu     sync.Mutex
	tokens map[uint64][]string
}

func newBenchmarkGlobalTokenTracker() *benchmarkGlobalTokenTracker {
	return &benchmarkGlobalTokenTracker{tokens: make(map[uint64][]string)}
}

func (t *benchmarkGlobalTokenTracker) push(sess session.Session, token string) {
	t.mu.Lock()
	t.tokens[sess.ID()] = append(t.tokens[sess.ID()], token)
	t.mu.Unlock()
}

func (t *benchmarkGlobalTokenTracker) take(sess session.Session, count int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	queue := t.tokens[sess.ID()]
	if len(queue) == 0 {
		return nil
	}
	if count > len(queue) {
		count = len(queue)
	}
	tokens := append([]string(nil), queue[:count]...)
	if count == len(queue) {
		delete(t.tokens, sess.ID())
	} else {
		t.tokens[sess.ID()] = append([]string(nil), queue[count:]...)
	}
	return tokens
}

func newBenchmarkSession(id uint64) session.Session {
	return session.New(session.Config{
		ID:         id,
		Listener:   "bench-listener",
		RemoteAddr: "bench-remote",
		LocalAddr:  "bench-local",
	})
}
