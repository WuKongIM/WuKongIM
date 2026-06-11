package channelappend

import "sync"

// shard owns a subset of channel appendrs selected by channel-key hash.
// Its lock guards only writer lookup/creation, never advance execution.
type shard struct {
	limits  channelStateLimits
	ports   writerPorts
	mu      sync.RWMutex
	writers map[string]*channelAppendr
}

func newShard(limits channelStateLimits) *shard {
	return &shard{limits: limits, writers: make(map[string]*channelAppendr)}
}

// getOrCreate returns the writer for target's channel, creating it once.
func (s *shard) getOrCreate(target AuthorityTarget) *channelAppendr {
	key := targetKey(target)
	s.mu.RLock()
	w := s.writers[key]
	s.mu.RUnlock()
	if w != nil {
		return w
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w = s.writers[key]; w != nil {
		return w
	}
	w = newChannelAppendr(target, s.limits)
	w.ports = s.ports
	s.writers[key] = w
	return w
}

// lookup returns the writer for key if present.
func (s *shard) lookup(key string) *channelAppendr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writers[key]
}
