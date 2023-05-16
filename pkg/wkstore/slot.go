package wkstore

import (
	lru "github.com/hashicorp/golang-lru/v2"
)

type slot struct {
	topicCache *lru.Cache[string, *topic]
	cfg        *StoreConfig
	num        uint32
}

func newSlot(num uint32, cfg *StoreConfig) *slot {
	topicCache, err := lru.NewWithEvict(100, func(key string, value *topic) {
		value.close()
	})
	if err != nil {
		panic(err)
	}
	return &slot{
		cfg:        cfg,
		num:        num,
		topicCache: topicCache,
	}
}

func (s *slot) getTopic(topic string) *topic {
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
	keys := s.topicCache.Keys()
	for _, key := range keys {
		s.topicCache.Remove(key) // Trigger onEvicted method
	}
	return nil
}
