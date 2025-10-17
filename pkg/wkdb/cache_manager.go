package wkdb

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// CacheManager 缓存管理器，负责统一管理所有缓存的清理和维护
type CacheManager struct {
	// 缓存引用
	permissionCache    *PermissionCache
	conversationCache  *ConversationCache
	channelInfoCache   *ChannelInfoCache
	clusterConfigCache *ChannelClusterConfigCache
	deviceCache        *DeviceCache

	// 清理配置
	cleanupInterval time.Duration // 清理间隔
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup

	wklog.Log
}

// NewCacheManager 创建缓存管理器
func NewCacheManager(
	permissionCache *PermissionCache,
	conversationCache *ConversationCache,
	channelInfoCache *ChannelInfoCache,
	clusterConfigCache *ChannelClusterConfigCache,
	deviceCache *DeviceCache,
) *CacheManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &CacheManager{
		permissionCache:    permissionCache,
		conversationCache:  conversationCache,
		channelInfoCache:   channelInfoCache,
		clusterConfigCache: clusterConfigCache,
		deviceCache:        deviceCache,
		cleanupInterval:    10 * time.Minute, // 默认每10分钟清理一次
		ctx:                ctx,
		cancel:             cancel,
		Log:                wklog.NewWKLog("CacheManager"),
	}
}

// Start 启动缓存管理器
func (cm *CacheManager) Start() {
	cm.Info("Starting cache manager", zap.Duration("cleanup_interval", cm.cleanupInterval))

	cm.wg.Add(1)
	go cm.cleanupLoop()
}

// Stop 停止缓存管理器
func (cm *CacheManager) Stop() {
	cm.Info("Stopping cache manager")
	cm.cancel()
	cm.wg.Wait()
	cm.Info("Cache manager stopped")
}

// SetCleanupInterval 设置清理间隔
func (cm *CacheManager) SetCleanupInterval(interval time.Duration) {
	cm.cleanupInterval = interval
	cm.Info("Cache cleanup interval updated", zap.Duration("new_interval", interval))
}

// cleanupLoop 清理循环
func (cm *CacheManager) cleanupLoop() {
	defer cm.wg.Done()

	ticker := time.NewTicker(cm.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			cm.Info("Cache cleanup loop stopped")
			return
		case <-ticker.C:
			cm.performCleanup()
		}
	}
}

// performCleanup 执行清理操作
func (cm *CacheManager) performCleanup() {
	start := time.Now()
	totalRemoved := 0

	cm.Debug("Starting cache cleanup")

	// 清理权限缓存
	if cm.permissionCache != nil {
		removed := cm.permissionCache.RemoveExpiredItems()
		totalRemoved += removed
		if removed > 0 {
			cm.Debug("Cleaned permission cache", zap.Int("removed", removed))
		}
	}

	// 清理会话缓存
	if cm.conversationCache != nil {
		removed := cm.conversationCache.RemoveExpiredItems()
		totalRemoved += removed
		if removed > 0 {
			cm.Debug("Cleaned conversation cache", zap.Int("removed", removed))
		}
	}

	// 清理频道信息缓存
	if cm.channelInfoCache != nil {
		removed := cm.channelInfoCache.RemoveExpiredItems()
		totalRemoved += removed
		if removed > 0 {
			cm.Debug("Cleaned channel info cache", zap.Int("removed", removed))
		}
	}

	// 清理集群配置缓存
	if cm.clusterConfigCache != nil {
		removed := cm.clusterConfigCache.RemoveExpiredItems()
		totalRemoved += removed
		if removed > 0 {
			cm.Debug("Cleaned cluster config cache", zap.Int("removed", removed))
		}
	}

	// 清理设备缓存
	if cm.deviceCache != nil {
		removed := cm.deviceCache.RemoveExpiredItems()
		totalRemoved += removed
		if removed > 0 {
			cm.Debug("Cleaned device cache", zap.Int("removed", removed))
		}
	}

	duration := time.Since(start)
	if totalRemoved > 0 {
		cm.Info("Cache cleanup completed",
			zap.Int("total_removed", totalRemoved),
			zap.Duration("duration", duration),
		)
	} else {
		cm.Debug("Cache cleanup completed - no expired items found",
			zap.Duration("duration", duration),
		)
	}
}

// ForceCleanup 强制执行一次清理
func (cm *CacheManager) ForceCleanup() {
	cm.Info("Force cleanup requested")
	cm.performCleanup()
}

// GetCacheStats 获取所有缓存的统计信息
func (cm *CacheManager) GetCacheStats() map[string]interface{} {
	stats := make(map[string]interface{})

	if cm.permissionCache != nil {
		permissionStats := cm.permissionCache.GetCacheStats()
		for k, v := range permissionStats {
			stats["permission_"+k] = v
		}
	}

	if cm.conversationCache != nil {
		conversationStats := cm.conversationCache.GetCacheStats()
		for k, v := range conversationStats {
			stats["conversation_"+k] = v
		}
	}

	if cm.channelInfoCache != nil {
		channelInfoStats := cm.channelInfoCache.GetCacheStats()
		for k, v := range channelInfoStats {
			stats["channel_info_"+k] = v
		}
	}

	if cm.clusterConfigCache != nil {
		clusterConfigStats := cm.clusterConfigCache.GetCacheStats()
		for k, v := range clusterConfigStats {
			stats["cluster_config_"+k] = v
		}
	}

	if cm.deviceCache != nil {
		deviceStats := cm.deviceCache.GetCacheStats()
		for k, v := range deviceStats {
			stats["device_"+k] = v
		}
	}

	// 添加管理器自身的统计
	stats["cleanup_interval_seconds"] = cm.cleanupInterval.Seconds()

	return stats
}

// GetTotalCacheSize 获取所有缓存的总大小
func (cm *CacheManager) GetTotalCacheSize() int {
	total := 0

	if cm.permissionCache != nil {
		total += cm.permissionCache.GetCacheSize()
	}

	if cm.conversationCache != nil {
		total += cm.conversationCache.GetCacheSize()
	}

	if cm.channelInfoCache != nil {
		total += cm.channelInfoCache.GetCacheSize()
	}

	if cm.clusterConfigCache != nil {
		total += cm.clusterConfigCache.GetCacheSize()
	}

	if cm.deviceCache != nil {
		total += cm.deviceCache.GetCacheSize()
	}

	return total
}

// GetTotalMaxCacheSize 获取所有缓存的最大容量
func (cm *CacheManager) GetTotalMaxCacheSize() int {
	total := 0

	if cm.permissionCache != nil {
		total += cm.permissionCache.GetMaxCacheSize()
	}

	if cm.conversationCache != nil {
		total += cm.conversationCache.GetMaxCacheSize()
	}

	if cm.channelInfoCache != nil {
		total += cm.channelInfoCache.GetMaxCacheSize()
	}

	if cm.clusterConfigCache != nil {
		total += cm.clusterConfigCache.GetMaxCacheSize()
	}

	if cm.deviceCache != nil {
		total += cm.deviceCache.GetMaxCacheSize()
	}

	return total
}

// GetCacheUtilization 获取缓存利用率
func (cm *CacheManager) GetCacheUtilization() float64 {
	currentSize := cm.GetTotalCacheSize()
	maxSize := cm.GetTotalMaxCacheSize()

	if maxSize == 0 {
		return 0.0
	}

	return float64(currentSize) / float64(maxSize) * 100.0
}

// ClearAllCaches 清空所有缓存
func (cm *CacheManager) ClearAllCaches() {
	cm.Info("Clearing all caches")

	if cm.permissionCache != nil {
		cm.permissionCache.ClearCache()
	}

	if cm.conversationCache != nil {
		cm.conversationCache.ClearCache()
	}

	if cm.channelInfoCache != nil {
		cm.channelInfoCache.ClearCache()
	}

	if cm.clusterConfigCache != nil {
		cm.clusterConfigCache.ClearCache()
	}

	if cm.deviceCache != nil {
		cm.deviceCache.ClearCache()
	}

	cm.Info("All caches cleared")
}

// WarmUpAllCaches 预热所有缓存
func (cm *CacheManager) WarmUpAllCaches(warmupData *CacheWarmupData) {
	cm.Info("Starting cache warmup")

	if warmupData.PermissionData != nil && cm.permissionCache != nil {
		// 权限缓存预热需要根据具体的数据结构来实现
		cm.Info("Permission cache warmup skipped - requires specific implementation")
	}

	if warmupData.ConversationData != nil && cm.conversationCache != nil {
		// 会话缓存预热
		cm.Info("Conversation cache warmup skipped - requires specific implementation")
	}

	if warmupData.ChannelInfoData != nil && cm.channelInfoCache != nil {
		cm.channelInfoCache.WarmUpCache(warmupData.ChannelInfoData)
	}

	if warmupData.ClusterConfigData != nil && cm.clusterConfigCache != nil {
		cm.clusterConfigCache.WarmUpCache(warmupData.ClusterConfigData)
	}

	if warmupData.DeviceData != nil && cm.deviceCache != nil {
		cm.deviceCache.WarmUpCache(warmupData.DeviceData)
	}

	cm.Info("Cache warmup completed")
}

// CacheWarmupData 缓存预热数据
type CacheWarmupData struct {
	PermissionData    interface{}                 `json:"permission_data"`
	ConversationData  interface{}                 `json:"conversation_data"`
	ChannelInfoData   []ChannelInfo               `json:"channel_info_data"`
	ClusterConfigData []ChannelClusterConfig      `json:"cluster_config_data"`
	DeviceData        []Device                    `json:"device_data"`
}

// LogCacheReport 输出缓存报告
func (cm *CacheManager) LogCacheReport() {
	stats := cm.GetCacheStats()
	utilization := cm.GetCacheUtilization()

	cm.Info("=== Cache Report ===")
	cm.Info("Cache Utilization", zap.Float64("utilization_percent", utilization))
	cm.Info("Total Cache Size", zap.Int("current", cm.GetTotalCacheSize()), zap.Int("max", cm.GetTotalMaxCacheSize()))

	// 输出各个缓存的详细统计
	for key, value := range stats {
		if key != "cleanup_interval_seconds" {
			cm.Info("Cache Stat", zap.String("key", key), zap.Any("value", value))
		}
	}
}

// StartPeriodicReport 启动定期报告
func (cm *CacheManager) StartPeriodicReport(interval time.Duration) {
	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-cm.ctx.Done():
				return
			case <-ticker.C:
				cm.LogCacheReport()
			}
		}
	}()
}
