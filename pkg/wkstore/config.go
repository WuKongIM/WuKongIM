package wkstore

import (
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

type StoreConfig struct {
	SlotNum                    int //
	DataDir                    string
	MaxSegmentCacheNum         int
	EachMessagegMaxSizeOfBytes int
	SegmentMaxBytes            int64 // each segment max size of bytes default 2G
	DecodeMessageFnc           func(msg []byte) (Message, error)
	StreamCacheSize            int // stream cache size

	segmentCache     *lru.Cache[string, *segment]
	segmentCacheLock sync.RWMutex
}

func NewStoreConfig() *StoreConfig {

	cfg := &StoreConfig{
		SlotNum:                    256,
		DataDir:                    "./data",
		MaxSegmentCacheNum:         2000,
		EachMessagegMaxSizeOfBytes: 1024 * 1024 * 2, // 2M
		SegmentMaxBytes:            1024 * 1024 * 1024 * 2,
		StreamCacheSize:            40,
	}

	segmentCache, err := lru.NewWithEvict(1500, func(key string, value *segment) {
		cfg.segmentCacheLock.Lock()
		value.Debug("release segment", zap.String("key", key))
		value.release()
		cfg.segmentCacheLock.Unlock()
	})
	if err != nil {
		panic(err)
	}
	cfg.segmentCache = segmentCache

	return cfg
}
