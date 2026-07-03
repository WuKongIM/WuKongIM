package handler

import (
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type appendIdempotencyLock struct {
	mu   sync.Mutex
	refs int
}

func (s *service) lockAppendIdempotencyKeys(keys []channel.IdempotencyKey) func() {
	keys = normalizeAppendIdempotencyKeys(keys)
	if len(keys) == 0 {
		return func() {}
	}

	locks := make([]*appendIdempotencyLock, 0, len(keys))
	for _, key := range keys {
		s.mu.Lock()
		lock := s.appendIdempotencyLocks[key]
		if lock == nil {
			lock = &appendIdempotencyLock{}
			s.appendIdempotencyLocks[key] = lock
		}
		lock.refs++
		s.mu.Unlock()

		lock.mu.Lock()
		locks = append(locks, lock)
	}

	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			lock := locks[i]
			key := keys[i]
			lock.mu.Unlock()

			s.mu.Lock()
			lock.refs--
			if lock.refs == 0 {
				delete(s.appendIdempotencyLocks, key)
			}
			s.mu.Unlock()
		}
	}
}

func normalizeAppendIdempotencyKeys(keys []channel.IdempotencyKey) []channel.IdempotencyKey {
	if len(keys) == 0 {
		return nil
	}
	out := append([]channel.IdempotencyKey(nil), keys...)
	sort.Slice(out, func(i, j int) bool {
		return appendIdempotencyKeyLess(out[i], out[j])
	})
	deduped := out[:0]
	for _, key := range out {
		if len(deduped) > 0 && deduped[len(deduped)-1] == key {
			continue
		}
		deduped = append(deduped, key)
	}
	return deduped
}

func appendIdempotencyKeyLess(a, b channel.IdempotencyKey) bool {
	if a.ChannelID.Type != b.ChannelID.Type {
		return a.ChannelID.Type < b.ChannelID.Type
	}
	if a.ChannelID.ID != b.ChannelID.ID {
		return a.ChannelID.ID < b.ChannelID.ID
	}
	if a.FromUID != b.FromUID {
		return a.FromUID < b.FromUID
	}
	return a.ClientMsgNo < b.ClientMsgNo
}
