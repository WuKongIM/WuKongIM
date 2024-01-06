package wkstore

import lru "github.com/hashicorp/golang-lru/v2"

type StoreConfig struct {
	SlotNum                    int //
	DataDir                    string
	MaxSegmentCacheNum         int
	EachMessagegMaxSizeOfBytes int
	SegmentMaxBytes            int64 // each segment max size of bytes default 2G
	DecodeMessageFnc           func(msg []byte) (Message, error)
	StreamCacheSize            int // stream cache size

	segmentCache *lru.Cache[string, *segment]
}

func NewStoreConfig() *StoreConfig {
	segmentCache, err := lru.NewWithEvict(100, func(key string, value *segment) {
		value.release()
	})
	if err != nil {
		panic(err)
	}
	return &StoreConfig{
		SlotNum:                    256,
		DataDir:                    "./data",
		MaxSegmentCacheNum:         2000,
		EachMessagegMaxSizeOfBytes: 1024 * 1024 * 2, // 2M
		SegmentMaxBytes:            1024 * 1024 * 1024 * 2,
		StreamCacheSize:            40,
		segmentCache:               segmentCache,
	}
}
