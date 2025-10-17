package wkdb

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// ChannelClusterConfigCache 频道集群配置的 LRU 缓存
type ChannelClusterConfigCache struct {
	// 集群配置缓存 key: channelId:channelType
	cache *lru.Cache[string, *ChannelClusterConfigCacheItem]

	// 配置
	maxCacheSize int           // 缓存最大数量
	cacheTTL     time.Duration // 缓存过期时间

	wklog.Log
}

// ChannelClusterConfigCacheItem 集群配置缓存项
type ChannelClusterConfigCacheItem struct {
	Config   ChannelClusterConfig `json:"config"`    // 集群配置
	CachedAt time.Time            `json:"cached_at"` // 缓存时间
	TTL      time.Duration        `json:"ttl"`       // 过期时间
}

// IsExpired 检查缓存是否过期
func (c *ChannelClusterConfigCacheItem) IsExpired() bool {
	return time.Since(c.CachedAt) > c.TTL
}

// NewChannelClusterConfigCache 创建集群配置缓存
func NewChannelClusterConfigCache(maxCacheSize int) *ChannelClusterConfigCache {
	if maxCacheSize <= 0 {
		maxCacheSize = 10000 // 默认缓存1万个集群配置
	}

	cache, _ := lru.New[string, *ChannelClusterConfigCacheItem](maxCacheSize)

	return &ChannelClusterConfigCache{
		cache:        cache,
		maxCacheSize: maxCacheSize,
		cacheTTL:     10 * time.Minute, // 缓存时间
		Log:          wklog.NewWKLog("ChannelClusterConfigCache"),
	}
}

// GetChannelClusterConfig 从缓存获取集群配置
func (c *ChannelClusterConfigCache) GetChannelClusterConfig(channelId string, channelType uint8) (ChannelClusterConfig, bool) {
	key := c.getConfigKey(channelId, channelType)
	if item, ok := c.cache.Get(key); ok {
		if !item.IsExpired() {
			return item.Config, true
		}
		// 缓存过期，异步删除
		go func() {
			c.cache.Remove(key)
		}()
	}
	return EmptyChannelClusterConfig, false
}

// SetChannelClusterConfig 设置集群配置到缓存
func (c *ChannelClusterConfigCache) SetChannelClusterConfig(config ChannelClusterConfig) {
	key := c.getConfigKey(config.ChannelId, config.ChannelType)

	item := &ChannelClusterConfigCacheItem{
		Config:   config,
		CachedAt: time.Now(),
		TTL:      c.cacheTTL,
	}
	c.cache.Add(key, item)
}

// InvalidateChannelClusterConfig 使指定频道的集群配置缓存失效
func (c *ChannelClusterConfigCache) InvalidateChannelClusterConfig(channelId string, channelType uint8) {
	key := c.getConfigKey(channelId, channelType)
	c.cache.Remove(key)
}

// BatchSetChannelClusterConfigs 批量设置集群配置到缓存
func (c *ChannelClusterConfigCache) BatchSetChannelClusterConfigs(configs []ChannelClusterConfig) {
	for _, config := range configs {
		c.SetChannelClusterConfig(config)
	}
}

// BatchInvalidateChannelClusterConfigs 批量使集群配置缓存失效
func (c *ChannelClusterConfigCache) BatchInvalidateChannelClusterConfigs(channels []struct {
	ChannelId   string
	ChannelType uint8
}) {
	for _, channel := range channels {
		c.InvalidateChannelClusterConfig(channel.ChannelId, channel.ChannelType)
	}
}

// InvalidateByLeaderId 使指定 LeaderId 的所有集群配置缓存失效
func (c *ChannelClusterConfigCache) InvalidateByLeaderId(leaderId uint64) {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok {
			if item.Config.LeaderId == leaderId {
				c.cache.Remove(key)
				removedCount++
			}
		}
	}

	if removedCount > 0 {
		c.Debug("Invalidated cluster configs by leader ID",
			zap.Uint64("leader_id", leaderId),
			zap.Int("count", removedCount))
	}
}

// InvalidateBySlotId 使指定 SlotId 的所有集群配置缓存失效
func (c *ChannelClusterConfigCache) InvalidateBySlotId(slotId uint32, channelSlotIdFunc func(channelId string) uint32) {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok {
			if channelSlotIdFunc(item.Config.ChannelId) == slotId {
				c.cache.Remove(key)
				removedCount++
			}
		}
	}

	if removedCount > 0 {
		c.Debug("Invalidated cluster configs by slot ID",
			zap.Uint32("slot_id", slotId),
			zap.Int("count", removedCount))
	}
}

// GetCacheStats 获取缓存统计信息
func (c *ChannelClusterConfigCache) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"cluster_config_cache_len": c.cache.Len(),
		"cluster_config_cache_max": c.maxCacheSize,
		"cache_ttl_seconds":        c.cacheTTL.Seconds(),
	}
}

// ClearCache 清空所有缓存
func (c *ChannelClusterConfigCache) ClearCache() {
	c.cache.Purge()
	c.Info("Channel cluster config cache cleared")
}

// getConfigKey 生成集群配置缓存键
func (c *ChannelClusterConfigCache) getConfigKey(channelId string, channelType uint8) string {
	return fmt.Sprintf("%s:%d", channelId, channelType)
}

// GetCacheSize 获取当前缓存大小
func (c *ChannelClusterConfigCache) GetCacheSize() int {
	return c.cache.Len()
}

// GetMaxCacheSize 获取最大缓存大小
func (c *ChannelClusterConfigCache) GetMaxCacheSize() int {
	return c.maxCacheSize
}

// SetCacheTTL 设置缓存过期时间
func (c *ChannelClusterConfigCache) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

// GetCacheTTL 获取缓存过期时间
func (c *ChannelClusterConfigCache) GetCacheTTL() time.Duration {
	return c.cacheTTL
}

// RemoveExpiredItems 清理过期的缓存项
func (c *ChannelClusterConfigCache) RemoveExpiredItems() int {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && item.IsExpired() {
			c.cache.Remove(key)
			removedCount++
		}
	}

	if removedCount > 0 {
		c.Debug("Removed expired cluster config cache items", zap.Int("count", removedCount))
	}

	return removedCount
}

// WarmUpCache 预热缓存（可以在启动时调用）
func (c *ChannelClusterConfigCache) WarmUpCache(configs []ChannelClusterConfig) {
	count := 0
	for _, config := range configs {
		c.SetChannelClusterConfig(config)
		count++
	}
	c.Info("Channel cluster config cache warmed up", zap.Int("count", count))
}

// GetCachedChannels 获取已缓存的频道列表
func (c *ChannelClusterConfigCache) GetCachedChannels() []string {
	keys := c.cache.Keys()
	channels := make([]string, 0, len(keys))

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
			channels = append(channels, key)
		}
	}

	return channels
}

// GetConfigsByLeaderId 获取指定 LeaderId 的所有缓存配置
func (c *ChannelClusterConfigCache) GetConfigsByLeaderId(leaderId uint64) []ChannelClusterConfig {
	keys := c.cache.Keys()
	configs := make([]ChannelClusterConfig, 0)

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
			if item.Config.LeaderId == leaderId {
				configs = append(configs, item.Config)
			}
		}
	}

	return configs
}

// GetConfigsBySlotId 获取指定 SlotId 的所有缓存配置
func (c *ChannelClusterConfigCache) GetConfigsBySlotId(slotId uint32, channelSlotIdFunc func(channelId string) uint32) []ChannelClusterConfig {
	keys := c.cache.Keys()
	configs := make([]ChannelClusterConfig, 0)

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
			if channelSlotIdFunc(item.Config.ChannelId) == slotId {
				configs = append(configs, item.Config)
			}
		}
	}

	return configs
}

// UpdateConfigVersion 更新指定频道的配置版本（用于版本检查）
func (c *ChannelClusterConfigCache) UpdateConfigVersion(channelId string, channelType uint8, version uint64) {
	key := c.getConfigKey(channelId, channelType)
	if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
		item.Config.ConfVersion = version
		// 更新缓存时间
		item.CachedAt = time.Now()
	}
}

// GetConfigVersion 获取指定频道的配置版本
func (c *ChannelClusterConfigCache) GetConfigVersion(channelId string, channelType uint8) (uint64, bool) {
	key := c.getConfigKey(channelId, channelType)
	if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
		return item.Config.ConfVersion, true
	}
	return 0, false
}
