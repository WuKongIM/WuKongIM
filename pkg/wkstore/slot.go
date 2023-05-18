package wkstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	lru "github.com/hashicorp/golang-lru/v2"
)

type slot struct {
	topicCache *lru.Cache[string, *topic]
	cfg        *StoreConfig
	num        uint32

	topicLock *keylock.KeyLock
}

func newSlot(num uint32, cfg *StoreConfig) *slot {
	topicCache, err := lru.NewWithEvict(100, func(key string, value *topic) {
		value.close()
	})
	if err != nil {
		panic(err)
	}
	topicLock := keylock.NewKeyLock()
	topicLock.StartCleanLoop()
	return &slot{
		cfg:        cfg,
		num:        num,
		topicCache: topicCache,
		topicLock:  topicLock,
	}
}

func (s *slot) getTopic(topic string) *topic {
	s.topicLock.Lock(topic)
	defer s.topicLock.Unlock(topic)
	v, ok := s.topicCache.Get(topic)
	if ok {
		return v
	}
	tc := newTopic(topic, s.num, s.cfg)
	s.topicCache.Add(topic, tc)
	return tc
}

// Close Close
func (s *slot) close() error {
	s.topicLock.StopCleanLoop()
	keys := s.topicCache.Keys()
	for _, key := range keys {
		s.topicCache.Remove(key) // Trigger onEvicted method
	}
	return nil
}
