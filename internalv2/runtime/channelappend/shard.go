package channelappend

import (
	"sync"
	"sync/atomic"
	"time"
)

// shard owns a subset of channel writers selected by channel-key hash.
// Its lock guards only writer lookup/creation, never advance execution.
type shard struct {
	limits              channelStateLimits
	ports               writerPorts
	admissionCapacity   int64
	admissionUsed       atomic.Int64
	writerIdleRetention time.Duration
	mu                  sync.RWMutex
	writers             map[string]*channelWriter
}

func newShard(limits channelStateLimits, admissionCapacity int64, writerIdleRetention time.Duration) *shard {
	return &shard{limits: limits, admissionCapacity: admissionCapacity, writerIdleRetention: writerIdleRetention, writers: make(map[string]*channelWriter)}
}

func (s *shard) tryAcquireAdmission() bool {
	if s == nil {
		return false
	}
	for {
		used := s.admissionUsed.Load()
		if used >= s.admissionCapacity {
			return false
		}
		if s.admissionUsed.CompareAndSwap(used, used+1) {
			return true
		}
	}
}

func (s *shard) releaseAdmission() {
	if s == nil {
		return
	}
	s.admissionUsed.Add(-1)
}

// getOrCreate returns the writer for target's channel, creating it once.
func (s *shard) getOrCreate(target AuthorityTarget) *channelWriter {
	key := targetKey(target)
	s.mu.RLock()
	w := s.writers[key]
	s.mu.RUnlock()
	if w != nil {
		return w
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reclaimIdleWritersLocked(time.Now(), s.writerIdleRetention)
	if w = s.writers[key]; w != nil {
		return w
	}
	w = newChannelWriter(target, s.limits)
	w.ports = s.ports
	s.writers[key] = w
	return w
}

// lookup returns the writer for key if present.
func (s *shard) lookup(key string) *channelWriter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writers[key]
}

func (s *shard) reclaimIdleWriters(now time.Time, idleRetention time.Duration) int {
	if s == nil || idleRetention <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaimIdleWritersLocked(now, idleRetention)
}

func (s *shard) reclaimIdleWritersLocked(now time.Time, idleRetention time.Duration) int {
	if s == nil || idleRetention <= 0 {
		return 0
	}
	removed := 0
	for key, writer := range s.writers {
		if writer.idleExpired(now, idleRetention) {
			delete(s.writers, key)
			removed++
		}
	}
	return removed
}
