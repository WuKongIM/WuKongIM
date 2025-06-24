package wkdb

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

// DeviceCache 设备的 LRU 缓存
type DeviceCache struct {
	// 设备缓存 key: uid:deviceFlag
	cache *lru.Cache[string, *DeviceCacheItem]

	// 配置
	maxCacheSize int           // 缓存最大数量
	cacheTTL     time.Duration // 缓存过期时间

	wklog.Log
}

// DeviceCacheItem 设备缓存项
type DeviceCacheItem struct {
	Device   Device        `json:"device"`    // 设备信息
	CachedAt time.Time     `json:"cached_at"` // 缓存时间
	TTL      time.Duration `json:"ttl"`       // 过期时间
}

// IsExpired 检查缓存是否过期
func (c *DeviceCacheItem) IsExpired() bool {
	return time.Since(c.CachedAt) > c.TTL
}

// NewDeviceCache 创建设备缓存
func NewDeviceCache(maxCacheSize int) *DeviceCache {
	if maxCacheSize <= 0 {
		maxCacheSize = 20000 // 默认缓存2万个设备
	}

	cache, _ := lru.New[string, *DeviceCacheItem](maxCacheSize)

	return &DeviceCache{
		cache:        cache,
		maxCacheSize: maxCacheSize,
		cacheTTL:     10 * time.Minute, // 缓存5分钟
		Log:          wklog.NewWKLog("DeviceCache"),
	}
}

// GetDevice 从缓存获取设备
func (c *DeviceCache) GetDevice(uid string, deviceFlag uint64) (Device, bool) {
	key := c.getDeviceKey(uid, deviceFlag)
	if item, ok := c.cache.Get(key); ok {
		if !item.IsExpired() {
			return item.Device, true
		}
		// 缓存过期，异步删除
		go func() {
			c.cache.Remove(key)
		}()
	}
	return EmptyDevice, false
}

// SetDevice 设置设备到缓存
func (c *DeviceCache) SetDevice(device Device) {
	key := c.getDeviceKey(device.Uid, device.DeviceFlag)

	item := &DeviceCacheItem{
		Device:   device,
		CachedAt: time.Now(),
		TTL:      c.cacheTTL,
	}
	c.cache.Add(key, item)
}

// InvalidateDevice 使指定设备缓存失效
func (c *DeviceCache) InvalidateDevice(uid string, deviceFlag uint64) {
	key := c.getDeviceKey(uid, deviceFlag)
	c.cache.Remove(key)
}

// InvalidateUserDevices 使指定用户的所有设备缓存失效
func (c *DeviceCache) InvalidateUserDevices(uid string) {
	keys := c.cache.Keys()
	userPrefix := uid + ":"

	for _, key := range keys {
		if len(key) > len(userPrefix) && key[:len(userPrefix)] == userPrefix {
			c.cache.Remove(key)
		}
	}
}

// BatchSetDevices 批量设置设备到缓存
func (c *DeviceCache) BatchSetDevices(devices []Device) {
	for _, device := range devices {
		c.SetDevice(device)
	}
}

// BatchInvalidateDevices 批量使设备缓存失效
func (c *DeviceCache) BatchInvalidateDevices(devices []struct {
	Uid        string
	DeviceFlag uint64
}) {
	for _, device := range devices {
		c.InvalidateDevice(device.Uid, device.DeviceFlag)
	}
}

// InvalidateByDeviceId 使指定设备ID的缓存失效
func (c *DeviceCache) InvalidateByDeviceId(deviceId uint64) {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok {
			if item.Device.Id == deviceId {
				c.cache.Remove(key)
				removedCount++
			}
		}
	}

	if removedCount > 0 {
		c.Debug("Invalidated devices by device ID",
			zap.Uint64("device_id", deviceId),
			zap.Int("count", removedCount))
	}
}

// GetCacheStats 获取缓存统计信息
func (c *DeviceCache) GetCacheStats() map[string]interface{} {
	return map[string]interface{}{
		"device_cache_len":  c.cache.Len(),
		"device_cache_max":  c.maxCacheSize,
		"cache_ttl_seconds": c.cacheTTL.Seconds(),
	}
}

// ClearCache 清空所有缓存
func (c *DeviceCache) ClearCache() {
	c.cache.Purge()
	c.Info("Device cache cleared")
}

// getDeviceKey 生成设备缓存键
func (c *DeviceCache) getDeviceKey(uid string, deviceFlag uint64) string {
	return fmt.Sprintf("%s:%d", uid, deviceFlag)
}

// GetCacheSize 获取当前缓存大小
func (c *DeviceCache) GetCacheSize() int {
	return c.cache.Len()
}

// GetMaxCacheSize 获取最大缓存大小
func (c *DeviceCache) GetMaxCacheSize() int {
	return c.maxCacheSize
}

// SetCacheTTL 设置缓存过期时间
func (c *DeviceCache) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

// GetCacheTTL 获取缓存过期时间
func (c *DeviceCache) GetCacheTTL() time.Duration {
	return c.cacheTTL
}

// RemoveExpiredItems 清理过期的缓存项
func (c *DeviceCache) RemoveExpiredItems() int {
	keys := c.cache.Keys()
	removedCount := 0

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && item.IsExpired() {
			c.cache.Remove(key)
			removedCount++
		}
	}

	if removedCount > 0 {
		c.Debug("Removed expired device cache items", zap.Int("count", removedCount))
	}

	return removedCount
}

// WarmUpCache 预热缓存（可以在启动时调用）
func (c *DeviceCache) WarmUpCache(devices []Device) {
	count := 0
	for _, device := range devices {
		c.SetDevice(device)
		count++
	}
	c.Info("Device cache warmed up", zap.Int("count", count))
}

// GetCachedUsers 获取已缓存的用户列表
func (c *DeviceCache) GetCachedUsers() []string {
	keys := c.cache.Keys()
	userMap := make(map[string]bool)

	for _, key := range keys {
		// 解析 uid:deviceFlag 格式
		for i, char := range key {
			if char == ':' {
				uid := key[:i]
				userMap[uid] = true
				break
			}
		}
	}

	users := make([]string, 0, len(userMap))
	for user := range userMap {
		users = append(users, user)
	}

	return users
}

// GetUserDeviceCount 获取指定用户的缓存设备数量
func (c *DeviceCache) GetUserDeviceCount(uid string) int {
	keys := c.cache.Keys()
	userPrefix := uid + ":"
	count := 0

	for _, key := range keys {
		if len(key) > len(userPrefix) && key[:len(userPrefix)] == userPrefix {
			if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
				count++
			}
		}
	}

	return count
}

// GetDevicesByUser 获取指定用户的所有缓存设备
func (c *DeviceCache) GetDevicesByUser(uid string) []Device {
	keys := c.cache.Keys()
	userPrefix := uid + ":"
	devices := make([]Device, 0)

	for _, key := range keys {
		if len(key) > len(userPrefix) && key[:len(userPrefix)] == userPrefix {
			if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
				devices = append(devices, item.Device)
			}
		}
	}

	return devices
}

// UpdateDeviceToken 更新设备Token（用于设备更新时）
func (c *DeviceCache) UpdateDeviceToken(uid string, deviceFlag uint64, newToken string) {
	key := c.getDeviceKey(uid, deviceFlag)
	if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
		item.Device.Token = newToken
		// 更新缓存时间
		item.CachedAt = time.Now()
	}
}

// UpdateDeviceLevel 更新设备级别
func (c *DeviceCache) UpdateDeviceLevel(uid string, deviceFlag uint64, newLevel uint8) {
	key := c.getDeviceKey(uid, deviceFlag)
	if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
		item.Device.DeviceLevel = newLevel
		// 更新缓存时间
		item.CachedAt = time.Now()
	}
}

// GetDevicesByLevel 获取指定级别的所有缓存设备
func (c *DeviceCache) GetDevicesByLevel(level uint8) []Device {
	keys := c.cache.Keys()
	devices := make([]Device, 0)

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
			if item.Device.DeviceLevel == level {
				devices = append(devices, item.Device)
			}
		}
	}

	return devices
}

// GetDevicesByToken 根据Token查找设备（用于Token验证）
func (c *DeviceCache) GetDevicesByToken(token string) []Device {
	keys := c.cache.Keys()
	devices := make([]Device, 0)

	for _, key := range keys {
		if item, ok := c.cache.Get(key); ok && !item.IsExpired() {
			if item.Device.Token == token {
				devices = append(devices, item.Device)
			}
		}
	}

	return devices
}
