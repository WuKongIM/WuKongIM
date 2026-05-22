package gnet

import (
	"runtime"
	"sync"
)

const actorReadyQueueSize = 1024

// actorPool owns a fixed set of connection actors for one running gnet engine.
type actorPool struct {
	shards    []*actorShard
	stopCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// actorShard serializes gateway callbacks for the connections assigned to it.
type actorShard struct {
	id int

	mu      sync.Mutex
	ready   []*connState
	drain   []*connState
	wake    chan struct{}
	stopped bool
	stopCh  <-chan struct{}
}

func newActorPool(shards int) *actorPool {
	if shards <= 0 {
		shards = runtime.GOMAXPROCS(0)
		if shards <= 0 {
			shards = 1
		}
	}
	pool := &actorPool{
		shards: make([]*actorShard, shards),
		stopCh: make(chan struct{}),
	}
	for i := range pool.shards {
		pool.shards[i] = newActorShard(i, pool.stopCh)
	}
	return pool
}

func (p *actorPool) start() {
	if p == nil {
		return
	}
	p.startOnce.Do(func() {
		for _, shard := range p.shards {
			shard := shard
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				shard.run()
			}()
		}
	})
}

func (p *actorPool) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		for _, shard := range p.shards {
			shard.markStopped()
		}
		close(p.stopCh)
	})
	p.wg.Wait()
}

func (p *actorPool) shardForConn(connID uint64) *actorShard {
	if p == nil || len(p.shards) == 0 {
		return nil
	}
	return p.shards[int(connID%uint64(len(p.shards)))]
}

func newActorShard(id int, stopCh <-chan struct{}) *actorShard {
	return &actorShard{
		id:     id,
		ready:  make([]*connState, 0, actorReadyQueueSize),
		drain:  make([]*connState, 0, actorReadyQueueSize),
		wake:   make(chan struct{}, 1),
		stopCh: stopCh,
	}
}

func (s *actorShard) schedule(state *connState) bool {
	if s == nil || state == nil {
		return false
	}
	if !state.scheduled.CompareAndSwap(false, true) {
		return false
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		state.scheduled.Store(false)
		return false
	}
	wasEmpty := len(s.ready) == 0
	s.ready = append(s.ready, state)
	s.mu.Unlock()

	if wasEmpty {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return true
}

func (s *actorShard) run() {
	for {
		select {
		case <-s.stopCh:
			s.drainReady()
			return
		case <-s.wake:
			s.drainReady()
		}
	}
}

func (s *actorShard) markStopped() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

func (s *actorShard) drainReady() {
	if s == nil {
		return
	}
	for {
		s.mu.Lock()
		if len(s.ready) == 0 {
			s.mu.Unlock()
			return
		}
		s.ready, s.drain = s.drain[:0], s.ready
		batch := s.drain
		s.mu.Unlock()

		for i, state := range batch {
			if state != nil {
				state.processReady()
			}
			batch[i] = nil
		}
		s.drain = batch[:0]
	}
}
