package wkdb

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// PermissionType 权限类型
type PermissionType uint8

const (
	PermissionTypeDenylist     PermissionType = 1 // 黑名单
	PermissionTypeSubscriber   PermissionType = 2 // 订阅者
	PermissionTypeAllowlist    PermissionType = 3 // 白名单
	PermissionTypeHasAllowlist PermissionType = 4 // 是否有白名单（频道级别）
)

// PermissionCache 统一的权限缓存
type PermissionCache struct {
	// 统一的权限缓存 key: permissionType:channelId:channelType[:uid]
	cache *lru.Cache[string, *PermissionCacheItem]

	// 配置
	maxCacheSize int           // 缓存最大数量
	cacheTTL     time.Duration // 缓存过期时间

	wklog.Log
}

// PermissionCacheItem 权限缓存项
type PermissionCacheItem struct {
	Exists   bool          `json:"exists"`    // 权限状态
	CachedAt time.Time     `json:"cached_at"` // 缓存时间
	TTL      time.Duration `json:"ttl"`       // 过期时间
}

// IsExpired 检查缓存是否过期
func (c *PermissionCacheItem) IsExpired() bool {
	return time.Since(c.CachedAt) > c.TTL
}

// NewPermissionCache 创建统一权限缓存
func NewPermissionCache(maxCacheSize int) *PermissionCache {
	if maxCacheSize <= 0 {
		maxCacheSize = 10000 // 默认缓存1万个权限查询结果
	}

	cache, _ := lru.New[string, *PermissionCacheItem](maxCacheSize)

	return &PermissionCache{
		cache:        cache,
		maxCacheSize: maxCacheSize,
		cacheTTL:     30 * time.Minute, // 缓存时间
		Log:          wklog.NewWKLog("PermissionCache"),
	}
}

// GetDenylistExists 获取黑名单存在状态
func (c *PermissionCache) GetDenylistExists(channelId string, channelType uint8, uid string) (bool, bool) {
	key := c.getPermissionKey(PermissionTypeDenylist, channelId, channelType, uid)
	return c.getPermission(key)
}

// SetDenylistExists 设置黑名单存在状态
func (c *PermissionCache) SetDenylistExists(channelId string, channelType uint8, uid string, exists bool) {
	key := c.getPermissionKey(PermissionTypeDenylist, channelId, channelType, uid)
	c.setPermission(key, exists)
}

// GetSubscriberExists 获取订阅者存在状态
func (c *PermissionCache) GetSubscriberExists(channelId string, channelType uint8, uid string) (bool, bool) {
	key := c.getPermissionKey(PermissionTypeSubscriber, channelId, channelType, uid)
	return c.getPermission(key)
}

// SetSubscriberExists 设置订阅者存在状态
func (c *PermissionCache) SetSubscriberExists(channelId string, channelType uint8, uid string, exists bool) {
	key := c.getPermissionKey(PermissionTypeSubscriber, channelId, channelType, uid)
	c.setPermission(key, exists)
}

// GetAllowlistExists 获取白名单存在状态
func (c *PermissionCache) GetAllowlistExists(channelId string, channelType uint8, uid string) (bool, bool) {
	key := c.getPermissionKey(PermissionTypeAllowlist, channelId, channelType, uid)
	return c.getPermission(key)
}

// SetAllowlistExists 设置白名单存在状态
func (c *PermissionCache) SetAllowlistExists(channelId string, channelType uint8, uid string, exists bool) {
	key := c.getPermissionKey(PermissionTypeAllowlist, channelId, channelType, uid)
	c.setPermission(key, exists)
}

// GetHasAllowlist 获取是否有白名单状态
func (c *PermissionCache) GetHasAllowlist(channelId string, channelType uint8) (bool, bool) {
	key := c.getPermissionKey(PermissionTypeHasAllowlist, channelId, channelType, "")
	return c.getPermission(key)
}

// SetHasAllowlist 设置是否有白名单状态
func (c *PermissionCache) SetHasAllowlist(channelId string, channelType uint8, hasAllowlist bool) {
	key := c.getPermissionKey(PermissionTypeHasAllowlist, channelId, channelType, "")
	c.setPermission(key, hasAllowlist)
}

// getPermission 通用获取权限方法
func (c *PermissionCache) getPermission(key string) (bool, bool) {
	if item, ok := c.cache.Get(key); ok {
		if !item.IsExpired() {
			return item.Exists, true
		}
		// 缓存过期，异步删除
		go func() {
			c.cache.Remove(key)
		}()
	}
	return false, false
}

// setPermission 通用设置权限方法
func (c *PermissionCache) setPermission(key string, exists bool) {
	item := &PermissionCacheItem{
		Exists:   exists,
		CachedAt: time.Now(),
		TTL:      c.cacheTTL,
	}
	c.cache.Add(key, item)
}

// BatchSetDenylistExists 批量设置黑名单存在状态
func (c *PermissionCache) BatchSetDenylistExists(channelId string, channelType uint8, uids []string, exists bool) {
	for _, uid := range uids {
		c.SetDenylistExists(channelId, channelType, uid, exists)
	}
}

// BatchSetSubscriberExists 批量设置订阅者存在状态
func (c *PermissionCache) BatchSetSubscriberExists(channelId string, channelType uint8, uids []string, exists bool) {
	for _, uid := range uids {
		c.SetSubscriberExists(channelId, channelType, uid, exists)
	}
}

// BatchSetAllowlistExists 批量设置白名单存在状态
func (c *PermissionCache) BatchSetAllowlistExists(channelId string, channelType uint8, uids []string, exists bool) {
	for _, uid := range uids {
		c.SetAllowlistExists(channelId, channelType, uid, exists)
	}

	// 如果添加了白名单成员，则该频道有白名单
	if exists && len(uids) > 0 {
		c.SetHasAllowlist(channelId, channelType, true)
	}
}

// InvalidateUser 使指定用户的所有权限缓存失效
func (c *PermissionCache) InvalidateUser(channelId string, channelType uint8, uid string) {
	// 删除该用户的所有权限类型缓存
	permissionTypes := []PermissionType{
		PermissionTypeDenylist,
		PermissionTypeSubscriber,
		PermissionTypeAllowlist,
	}

	for _, permType := range permissionTypes {
		key := c.getPermissionKey(permType, channelId, channelType, uid)
		c.cache.Remove(key)
	}
}

// InvalidateChannel 使指定频道的所有权限缓存失效
func (c *PermissionCache) InvalidateChannel(channelId string, channelType uint8) {
	keys := c.cache.Keys()
	channelPrefix := fmt.Sprintf("%d:%s:%d:", int(PermissionTypeDenylist), channelId, channelType)
	subscriberPrefix := fmt.Sprintf("%d:%s:%d:", int(PermissionTypeSubscriber), channelId, channelType)
	allowlistPrefix := fmt.Sprintf("%d:%s:%d:", int(PermissionTypeAllowlist), channelId, channelType)
	hasAllowlistKey := c.getPermissionKey(PermissionTypeHasAllowlist, channelId, channelType, "")

	for _, key := range keys {
		if (len(key) > len(channelPrefix) && key[:len(channelPrefix)] == channelPrefix) ||
			(len(key) > len(subscriberPrefix) && key[:len(subscriberPrefix)] == subscriberPrefix) ||
			(len(key) > len(allowlistPrefix) && key[:len(allowlistPrefix)] == allowlistPrefix) ||
			key == hasAllowlistKey {
			c.cache.Remove(key)
		}
	}
}

// InvalidateChannelByType 使指定频道的特定权限类型缓存失效
func (c *PermissionCache) InvalidateChannelByType(permType PermissionType, channelId string, channelType uint8) {
	if permType == PermissionTypeHasAllowlist {
		// HasAllowlist 是频道级别的
		key := c.getPermissionKey(permType, channelId, channelType, "")
		c.cache.Remove(key)
		return
	}

	keys := c.cache.Keys()
	prefix := fmt.Sprintf("%d:%s:%d:", int(permType), channelId, channelType)

	for _, key := range keys {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			c.cache.Remove(key)
		}
	}
}

// GetCacheStats 获取缓存统计信息
func (c *PermissionCache) GetCacheStats() map[string]interface{} {
	// 统计不同权限类型的缓存数量
	keys := c.cache.Keys()
	stats := map[string]int{
		"denylist":      0,
		"subscriber":    0,
		"allowlist":     0,
		"has_allowlist": 0,
	}

	for _, key := range keys {
		if len(key) > 0 {
			switch key[0] {
			case '1': // PermissionTypeDenylist
				stats["denylist"]++
			case '2': // PermissionTypeSubscriber
				stats["subscriber"]++
			case '3': // PermissionTypeAllowlist
				stats["allowlist"]++
			case '4': // PermissionTypeHasAllowlist
				stats["has_allowlist"]++
			}
		}
	}

	return map[string]interface{}{
		"total_cache_len":         c.cache.Len(),
		"denylist_cache_len":      stats["denylist"],
		"subscriber_cache_len":    stats["subscriber"],
		"allowlist_cache_len":     stats["allowlist"],
		"has_allowlist_cache_len": stats["has_allowlist"],
		"cache_max":               c.maxCacheSize,
		"cache_ttl_seconds":       c.cacheTTL.Seconds(),
	}
}

// ClearCache 清空所有缓存
func (c *PermissionCache) ClearCache() {
	c.cache.Purge()
	c.Info("Permission cache cleared")
}

// getPermissionKey 生成权限缓存键
func (c *PermissionCache) getPermissionKey(permType PermissionType, channelId string, channelType uint8, uid string) string {
	if uid == "" {
		// 频道级别的权限（如 HasAllowlist）
		return fmt.Sprintf("%d:%s:%d", int(permType), channelId, channelType)
	}
	// 用户级别的权限
	return fmt.Sprintf("%d:%s:%d:%s", int(permType), channelId, channelType, uid)
}

// GetCacheSize 获取当前缓存大小
func (c *PermissionCache) GetCacheSize() int {
	return c.cache.Len()
}

// GetMaxCacheSize 获取最大缓存大小
func (c *PermissionCache) GetMaxCacheSize() int {
	return c.maxCacheSize
}

// SetCacheTTL 设置缓存过期时间
func (c *PermissionCache) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

// GetCacheTTL 获取缓存过期时间
func (c *PermissionCache) GetCacheTTL() time.Duration {
	return c.cacheTTL
}

// RemoveExpiredItems 清理过期的缓存项
func (c *PermissionCache) RemoveExpiredItems() int {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && item.IsExpired() {
			c.cache.Remove(key)
			removedCount++
		}
	}

	if removedCount > 0 {
		c.Debug("Removed expired permission cache items", zap.Int("count", removedCount))
	}

	return removedCount
}
