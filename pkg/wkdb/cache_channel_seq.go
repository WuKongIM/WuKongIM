package wkdb

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
)

type channelSeqCache struct {
	lruCache *lru.Cache[string, []byte]
	// Store maxSize for logging purposes as lru.Cache doesn't expose Cap()
	maxSize int
	wklog.Log
	endian binary.ByteOrder
}

func newChannelSeqCache(maxSize int, endian binary.ByteOrder) *channelSeqCache {
	if maxSize <= 0 {
		panic(fmt.Sprintf("ChannelSeqCache: maxSize must be positive, got %d", maxSize))
	}
	lruCache, err := lru.New[string, []byte](maxSize)
	if err != nil {
		// This should ideally not happen if maxSize > 0 and memory is available
		panic(fmt.Sprintf("ChannelSeqCache: failed to initialize LRU cache: %v", err))
	}
	return &channelSeqCache{
		lruCache: lruCache,
		maxSize:  maxSize, // Store for logging
		Log:      wklog.NewWKLog("ChannelSeqCache"),
		endian:   endian,
	}
}

func (c *channelSeqCache) getChannelLastSeq(channelId string, channelType uint8) (uint64, uint64, bool) {
	key := c.getChannelKey(channelId, channelType)

	if item, ok := c.lruCache.Get(key); ok {
		seq := c.endian.Uint64(item[0:8])
		setTime := c.endian.Uint64(item[8:16])
		return seq, setTime, true
	}

	return 0, 0, false
}

func (c *channelSeqCache) setChannelLastSeq(channelId string, channelType uint8, seq uint64) {
	key := c.getChannelKey(channelId, channelType)
	data := make([]byte, 16)
	c.endian.PutUint64(data[0:8], seq)
	setTime := time.Now().UnixNano()
	c.endian.PutUint64(data[8:16], uint64(setTime))
	c.lruCache.Add(key, data) // Add will handle eviction if capacity is reached
}

func (c *channelSeqCache) setChannelLastSeqWithBytes(channelId string, channelType uint8, data []byte) {
	key := c.getChannelKey(channelId, channelType)
	c.lruCache.Add(key, data) // Add will handle eviction if capacity is reached
}

func (c *channelSeqCache) invalidateChannelLastSeq(channelId string, channelType uint8) {
	key := c.getChannelKey(channelId, channelType)
	c.lruCache.Remove(key)
}

func (c *channelSeqCache) getChannelKey(channelId string, channelType uint8) string {
	return wkutil.ChannelToKey(channelId, channelType)
}
