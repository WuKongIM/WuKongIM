package wkdb

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
)

// userLastMsgSeqCache 用户在频道内发送的最后一条消息序号缓存
// 用于优化批量查询用户最后消息序号的性能
type userLastMsgSeqCache struct {
	lruCache *lru.Cache[string, uint64]
	maxSize  int
	wklog.Log
}

// newUserLastMsgSeqCache 创建用户最后消息序号缓存
func newUserLastMsgSeqCache(maxSize int) *userLastMsgSeqCache {
	if maxSize <= 0 {
		panic(fmt.Sprintf("UserLastMsgSeqCache: maxSize must be positive, got %d", maxSize))
	}
	lruCache, err := lru.New[string, uint64](maxSize)
	if err != nil {
		panic(fmt.Sprintf("UserLastMsgSeqCache: failed to initialize LRU cache: %v", err))
	}
	return &userLastMsgSeqCache{
		lruCache: lruCache,
		maxSize:  maxSize,
		Log:      wklog.NewWKLog("UserLastMsgSeqCache"),
	}
}

// get 获取用户在指定频道内发送的最后一条消息序号
func (c *userLastMsgSeqCache) get(fromUid string, channelId string, channelType uint8) (uint64, bool) {
	key := c.makeKey(fromUid, channelId, channelType)
	return c.lruCache.Get(key)
}

// set 设置用户在指定频道内发送的最后一条消息序号
func (c *userLastMsgSeqCache) set(fromUid string, channelId string, channelType uint8, seq uint64) {
	key := c.makeKey(fromUid, channelId, channelType)
	c.lruCache.Add(key, seq)
}

// updateIfGreater 如果新的序号大于缓存中的序号，则更新缓存
func (c *userLastMsgSeqCache) updateIfGreater(fromUid string, channelId string, channelType uint8, seq uint64) {
	key := c.makeKey(fromUid, channelId, channelType)
	existing, ok := c.lruCache.Get(key)
	if !ok || seq > existing {
		c.lruCache.Add(key, seq)
	}
}

// invalidate 使缓存失效
func (c *userLastMsgSeqCache) invalidate(fromUid string, channelId string, channelType uint8) {
	key := c.makeKey(fromUid, channelId, channelType)
	c.lruCache.Remove(key)
}

// makeKey 生成缓存键：uid:channelId:channelType
func (c *userLastMsgSeqCache) makeKey(fromUid string, channelId string, channelType uint8) string {
	var b strings.Builder
	b.WriteString(fromUid)
	b.WriteByte(':')
	b.WriteString(channelId)
	b.WriteByte(':')
	b.WriteByte(channelType)
	return b.String()
}

