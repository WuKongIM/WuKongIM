package channelappend

import (
	"sync"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// writerAdvanceScheduler is the single admission owner for the blocking
// advance worker pool. Callers and effect-pool completions only append an
// already de-duplicated writer pointer to this unbounded-in-count but
// activation-bounded queue; they never wait for an advance worker.
//
// A channelWriter may own at most one scheduled activation, so queue memory is
// bounded by active writer state rather than SEND rate. Keeping blocking pool
// admission in this dedicated dispatcher breaks the advance<->append pool
// dependency cycle without bypassing the configured advance concurrency.
type writerAdvanceScheduler struct {
	pool *workerPool

	mu    sync.Mutex
	cond  *sync.Cond
	queue []*channelWriter
	head  int
	// dispatching reports the one writer removed from queue while the
	// dispatcher is waiting for or handing it to an advance worker.
	dispatching bool
	started     bool
	stopping    bool
	done        chan struct{}

	onStateChange func()
}

func newWriterAdvanceScheduler(pool *workerPool) *writerAdvanceScheduler {
	s := &writerAdvanceScheduler{
		pool: pool,
		done: make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *writerAdvanceScheduler) start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskChannelAppendAdvanceScheduler, s.run)
}

func (s *writerAdvanceScheduler) schedule(writer *channelWriter) {
	if s == nil || writer == nil {
		return
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, writer)
	s.cond.Signal()
	s.mu.Unlock()
	s.notifyStateChange()
}

func (s *writerAdvanceScheduler) run() {
	defer close(s.done)
	for {
		s.mu.Lock()
		for s.head == len(s.queue) && !s.stopping {
			s.cond.Wait()
		}
		if s.head == len(s.queue) && s.stopping {
			s.mu.Unlock()
			return
		}
		writer := s.queue[s.head]
		s.queue[s.head] = nil
		s.head++
		if s.head == len(s.queue) {
			s.queue = s.queue[:0]
			s.head = 0
		} else {
			s.compactQueueLocked()
		}
		s.dispatching = true
		s.mu.Unlock()
		s.notifyStateChange()

		if err := s.pool.submit(func() { writer.advance() }); err != nil {
			// The pool is stopped only after the writer drain and scheduler
			// stop. Running inline is a final fail-safe that preserves accepted
			// work if that lifecycle invariant is ever violated.
			writer.advance()
		}
		s.mu.Lock()
		s.dispatching = false
		s.mu.Unlock()
		s.notifyStateChange()
	}
}

func (s *writerAdvanceScheduler) waiting() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	waiting := len(s.queue) - s.head
	if s.dispatching {
		waiting++
	}
	return waiting
}

func (s *writerAdvanceScheduler) compactQueueLocked() {
	if s.head < 1024 || s.head*2 < len(s.queue) {
		return
	}
	oldLen := len(s.queue)
	remaining := oldLen - s.head
	copy(s.queue[:remaining], s.queue[s.head:])
	clear(s.queue[remaining:oldLen])
	s.queue = s.queue[:remaining]
	s.head = 0
}

func (s *writerAdvanceScheduler) notifyStateChange() {
	if s != nil && s.onStateChange != nil {
		s.onStateChange()
	}
}

func (s *writerAdvanceScheduler) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.started {
		s.stopping = true
		s.mu.Unlock()
		return
	}
	if !s.stopping {
		s.stopping = true
		s.cond.Broadcast()
	}
	done := s.done
	s.mu.Unlock()
	<-done
}
