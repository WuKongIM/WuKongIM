package wkstore

import (
	"fmt"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
)

type slot struct {
	topicCache *lru.Cache[string, *topic]
	cfg        *StoreConfig
	num        uint32

	topicLock *keylock.KeyLock
}

func newSlot(num uint32, cfg *StoreConfig) *slot {
	topicCache, err := lru.NewWithEvict(1000, func(key string, value *topic) {
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

func (s *slot) getAllTopics() ([]string, error) {
	topicDir := filepath.Join(s.cfg.DataDir, fmt.Sprintf("%d", s.num), "topics")

	// 获取topicDir目录下的所有文件
	files, err := wkutil.ListDir(topicDir)
	if err != nil {
		return nil, err
	}

	return files, nil
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
