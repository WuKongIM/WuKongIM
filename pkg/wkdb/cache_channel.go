package wkdb

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// ChannelInfoCache ChannelInfo 的 LRU 缓存
type ChannelInfoCache struct {
	// ChannelInfo 缓存 key: channelId:channelType
	channelInfoCache *lru.Cache[string, *ChannelInfoCacheItem]

	// 配置
	maxCacheSize int           // 缓存最大数量
	cacheTTL     time.Duration // 缓存过期时间

	wklog.Log
}

// ChannelInfoCacheItem ChannelInfo 缓存项
type ChannelInfoCacheItem struct {
	ChannelInfo ChannelInfo   `json:"channel_info"`
	CachedAt    time.Time     `json:"cached_at"`
	TTL         time.Duration `json:"ttl"`
}

// IsExpired 检查缓存是否过期
func (c *ChannelInfoCacheItem) IsExpired() bool {
	return time.Since(c.CachedAt) > c.TTL
}

// NewChannelInfoCache 创建 ChannelInfo 缓存
func NewChannelInfoCache(maxCacheSize int) *ChannelInfoCache {
	if maxCacheSize <= 0 {
		maxCacheSize = 1000 // 默认缓存1000个频道信息
	}

	channelInfoCache, _ := lru.New[string, *ChannelInfoCacheItem](maxCacheSize)

	return &ChannelInfoCache{
		channelInfoCache: channelInfoCache,
		maxCacheSize:     maxCacheSize,
		cacheTTL:         10 * time.Minute, // 缓存10分钟
		Log:              wklog.NewWKLog("ChannelInfoCache"),
	}
}

// GetChannelInfo 从缓存获取 ChannelInfo
func (c *ChannelInfoCache) GetChannelInfo(channelId string, channelType uint8) (ChannelInfo, bool) {
	key := c.getChannelKey(channelId, channelType)
	if item, ok := c.channelInfoCache.Get(key); ok {
		if !item.IsExpired() {
			return item.ChannelInfo, true
		}
		// 缓存过期，异步删除
		go func() {
			c.channelInfoCache.Remove(key)
		}()
	}
	return EmptyChannelInfo, false
}

// SetChannelInfo 设置 ChannelInfo 到缓存
func (c *ChannelInfoCache) SetChannelInfo(channelInfo ChannelInfo) {
	key := c.getChannelKey(channelInfo.ChannelId, channelInfo.ChannelType)

	item := &ChannelInfoCacheItem{
		ChannelInfo: channelInfo,
		CachedAt:    time.Now(),
		TTL:         c.cacheTTL,
	}
	c.channelInfoCache.Add(key, item)
}

// UpdateChannelInfo 更新缓存中的 ChannelInfo
func (c *ChannelInfoCache) UpdateChannelInfo(channelInfo ChannelInfo) {
	key := c.getChannelKey(channelInfo.ChannelId, channelInfo.ChannelType)

	// 只有当缓存中已存在该频道信息时才更新
	if _, exists := c.channelInfoCache.Get(key); exists {
		item := &ChannelInfoCacheItem{
			ChannelInfo: channelInfo,
			CachedAt:    time.Now(),
			TTL:         c.cacheTTL,
		}
		c.channelInfoCache.Add(key, item)
	}
}

// InvalidateChannelInfo 使指定频道的缓存失效
func (c *ChannelInfoCache) InvalidateChannelInfo(channelId string, channelType uint8) {
	key := c.getChannelKey(channelId, channelType)
	c.channelInfoCache.Remove(key)
}

// BatchSetChannelInfos 批量设置 ChannelInfo 到缓存
func (c *ChannelInfoCache) BatchSetChannelInfos(channelInfos []ChannelInfo) {
	for _, channelInfo := range channelInfos {
		key := c.getChannelKey(channelInfo.ChannelId, channelInfo.ChannelType)
		item := &ChannelInfoCacheItem{
			ChannelInfo: channelInfo,
			CachedAt:    time.Now(),
			TTL:         c.cacheTTL,
		}
		c.channelInfoCache.Add(key, item)
	}
}

// BatchUpdateChannelInfos 批量更新缓存中的 ChannelInfo
func (c *ChannelInfoCache) BatchUpdateChannelInfos(channelInfos []ChannelInfo) {
	for _, channelInfo := range channelInfos {
		key := c.getChannelKey(channelInfo.ChannelId, channelInfo.ChannelType)

		// 只有当缓存中已存在该频道信息时才更新
		if _, exists := c.channelInfoCache.Get(key); exists {
			item := &ChannelInfoCacheItem{
				ChannelInfo: channelInfo,
				CachedAt:    time.Now(),
				TTL:         c.cacheTTL,
			}
			c.channelInfoCache.Add(key, item)
		}
	}
}

// BatchInvalidateChannelInfos 批量使频道缓存失效
func (c *ChannelInfoCache) BatchInvalidateChannelInfos(channels []Channel) {
	for _, channel := range channels {
		key := c.getChannelKey(channel.ChannelId, channel.ChannelType)
		c.channelInfoCache.Remove(key)
	}
}

// GetCacheStats 获取缓存统计信息
func (c *ChannelInfoCache) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"channel_info_cache_len": c.channelInfoCache.Len(),
		"channel_info_cache_max": c.maxCacheSize,
		"cache_ttl_seconds":      c.cacheTTL.Seconds(),
	}
}

// ClearCache 清空所有缓存
func (c *ChannelInfoCache) ClearCache() {
	c.channelInfoCache.Purge()
	c.Info("ChannelInfo cache cleared")
}

// getChannelKey 生成频道缓存键
func (c *ChannelInfoCache) getChannelKey(channelId string, channelType uint8) string {
	return fmt.Sprintf("%s:%d", channelId, channelType)
}

// GetCacheSize 获取当前缓存大小
func (c *ChannelInfoCache) GetCacheSize() int {
	return c.channelInfoCache.Len()
}

// GetMaxCacheSize 获取最大缓存大小
func (c *ChannelInfoCache) GetMaxCacheSize() int {
	return c.maxCacheSize
}

// SetCacheTTL 设置缓存过期时间
func (c *ChannelInfoCache) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

// GetCacheTTL 获取缓存过期时间
func (c *ChannelInfoCache) GetCacheTTL() time.Duration {
	return c.cacheTTL
}

// RemoveExpiredItems 清理过期的缓存项
func (c *ChannelInfoCache) RemoveExpiredItems() int {
	keys := c.channelInfoCache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.channelInfoCache.Get(key); ok && item.IsExpired() {
			c.channelInfoCache.Remove(key)
			removedCount++
		}
	}

	if removedCount > 0 {
		c.Debug("Removed expired cache items", zap.Int("count", removedCount))
	}

	return removedCount
}

// WarmUpCache 预热缓存（可以在启动时调用）
func (c *ChannelInfoCache) WarmUpCache(channelInfos []ChannelInfo) {
	c.BatchSetChannelInfos(channelInfos)
	c.Info("Cache warmed up", zap.Int("count", len(channelInfos)))
}
