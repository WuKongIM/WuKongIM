package wkdb

import (
	"runtime"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// PerformanceMonitor 性能监控器
type PerformanceMonitor struct {
	// 方法调用统计
	methodStats map[string]*MethodStats
	mu          sync.RWMutex
	
	// 缓存统计
	cacheStats *CacheStats
	
	// 系统资源统计
	systemStats *SystemStats
	
	wklog.Log
}

// MethodStats 方法调用统计
type MethodStats struct {
	CallCount    int64         `json:"call_count"`
	TotalTime    time.Duration `json:"total_time"`
	AverageTime  time.Duration `json:"average_time"`
	MaxTime      time.Duration `json:"max_time"`
	MinTime      time.Duration `json:"min_time"`
	LastCallTime time.Time     `json:"last_call_time"`
}

// CacheStats 缓存统计
type CacheStats struct {
	PermissionCacheHits   int64 `json:"permission_cache_hits"`
	PermissionCacheMisses int64 `json:"permission_cache_misses"`
	ConversationCacheHits int64 `json:"conversation_cache_hits"`
	ConversationCacheMisses int64 `json:"conversation_cache_misses"`
	ClusterConfigCacheHits int64 `json:"cluster_config_cache_hits"`
	ClusterConfigCacheMisses int64 `json:"cluster_config_cache_misses"`
	ChannelInfoCacheHits  int64 `json:"channel_info_cache_hits"`
	ChannelInfoCacheMisses int64 `json:"channel_info_cache_misses"`
}

// SystemStats 系统资源统计
type SystemStats struct {
	MemoryUsage    uint64    `json:"memory_usage"`
	GoroutineCount int       `json:"goroutine_count"`
	GCCount        uint32    `json:"gc_count"`
	LastGCTime     time.Time `json:"last_gc_time"`
	HeapObjects    uint64    `json:"heap_objects"`
}

// NewPerformanceMonitor 创建性能监控器
func NewPerformanceMonitor() *PerformanceMonitor {
	return &PerformanceMonitor{
		methodStats: make(map[string]*MethodStats),
		cacheStats:  &CacheStats{},
		systemStats: &SystemStats{},
		Log:         wklog.NewWKLog("PerformanceMonitor"),
	}
}

// RecordMethodCall 记录方法调用
func (pm *PerformanceMonitor) RecordMethodCall(methodName string, duration time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	stats, exists := pm.methodStats[methodName]
	if !exists {
		stats = &MethodStats{
			MinTime: duration,
			MaxTime: duration,
		}
		pm.methodStats[methodName] = stats
	}
	
	stats.CallCount++
	stats.TotalTime += duration
	stats.AverageTime = stats.TotalTime / time.Duration(stats.CallCount)
	stats.LastCallTime = time.Now()
	
	if duration > stats.MaxTime {
		stats.MaxTime = duration
	}
	if duration < stats.MinTime {
		stats.MinTime = duration
	}
}

// RecordCacheHit 记录缓存命中
func (pm *PerformanceMonitor) RecordCacheHit(cacheType string) {
	switch cacheType {
	case "permission":
		pm.cacheStats.PermissionCacheHits++
	case "conversation":
		pm.cacheStats.ConversationCacheHits++
	case "cluster_config":
		pm.cacheStats.ClusterConfigCacheHits++
	case "channel_info":
		pm.cacheStats.ChannelInfoCacheHits++
	}
}

// RecordCacheMiss 记录缓存未命中
func (pm *PerformanceMonitor) RecordCacheMiss(cacheType string) {
	switch cacheType {
	case "permission":
		pm.cacheStats.PermissionCacheMisses++
	case "conversation":
		pm.cacheStats.ConversationCacheMisses++
	case "cluster_config":
		pm.cacheStats.ClusterConfigCacheMisses++
	case "channel_info":
		pm.cacheStats.ChannelInfoCacheMisses++
	}
}

// UpdateSystemStats 更新系统资源统计
func (pm *PerformanceMonitor) UpdateSystemStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	pm.systemStats.MemoryUsage = m.Alloc
	pm.systemStats.GoroutineCount = runtime.NumGoroutine()
	pm.systemStats.GCCount = m.NumGC
	pm.systemStats.HeapObjects = m.HeapObjects
	
	if m.LastGC > 0 {
		pm.systemStats.LastGCTime = time.Unix(0, int64(m.LastGC))
	}
}

// GetMethodStats 获取方法统计信息
func (pm *PerformanceMonitor) GetMethodStats() map[string]*MethodStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	result := make(map[string]*MethodStats)
	for k, v := range pm.methodStats {
		result[k] = &MethodStats{
			CallCount:    v.CallCount,
			TotalTime:    v.TotalTime,
			AverageTime:  v.AverageTime,
			MaxTime:      v.MaxTime,
			MinTime:      v.MinTime,
			LastCallTime: v.LastCallTime,
		}
	}
	return result
}

// GetCacheStats 获取缓存统计信息
func (pm *PerformanceMonitor) GetCacheStats() *CacheStats {
	return &CacheStats{
		PermissionCacheHits:     pm.cacheStats.PermissionCacheHits,
		PermissionCacheMisses:   pm.cacheStats.PermissionCacheMisses,
		ConversationCacheHits:   pm.cacheStats.ConversationCacheHits,
		ConversationCacheMisses: pm.cacheStats.ConversationCacheMisses,
		ClusterConfigCacheHits:  pm.cacheStats.ClusterConfigCacheHits,
		ClusterConfigCacheMisses: pm.cacheStats.ClusterConfigCacheMisses,
		ChannelInfoCacheHits:    pm.cacheStats.ChannelInfoCacheHits,
		ChannelInfoCacheMisses:  pm.cacheStats.ChannelInfoCacheMisses,
	}
}

// GetSystemStats 获取系统统计信息
func (pm *PerformanceMonitor) GetSystemStats() *SystemStats {
	pm.UpdateSystemStats()
	return &SystemStats{
		MemoryUsage:    pm.systemStats.MemoryUsage,
		GoroutineCount: pm.systemStats.GoroutineCount,
		GCCount:        pm.systemStats.GCCount,
		LastGCTime:     pm.systemStats.LastGCTime,
		HeapObjects:    pm.systemStats.HeapObjects,
	}
}

// GetCacheHitRate 获取缓存命中率
func (pm *PerformanceMonitor) GetCacheHitRate(cacheType string) float64 {
	var hits, misses int64
	
	switch cacheType {
	case "permission":
		hits = pm.cacheStats.PermissionCacheHits
		misses = pm.cacheStats.PermissionCacheMisses
	case "conversation":
		hits = pm.cacheStats.ConversationCacheHits
		misses = pm.cacheStats.ConversationCacheMisses
	case "cluster_config":
		hits = pm.cacheStats.ClusterConfigCacheHits
		misses = pm.cacheStats.ClusterConfigCacheMisses
	case "channel_info":
		hits = pm.cacheStats.ChannelInfoCacheHits
		misses = pm.cacheStats.ChannelInfoCacheMisses
	default:
		return 0.0
	}
	
	total := hits + misses
	if total == 0 {
		return 0.0
	}
	
	return float64(hits) / float64(total) * 100.0
}

// LogPerformanceReport 输出性能报告
func (pm *PerformanceMonitor) LogPerformanceReport() {
	pm.Info("=== Performance Report ===")
	
	// 方法调用统计
	methodStats := pm.GetMethodStats()
	pm.Info("Method Call Statistics:")
	for method, stats := range methodStats {
		pm.Info("Method stats",
			zap.String("method", method),
			zap.Int64("call_count", stats.CallCount),
			zap.Duration("avg_time", stats.AverageTime),
			zap.Duration("max_time", stats.MaxTime),
			zap.Duration("min_time", stats.MinTime),
		)
	}
	
	// 缓存统计
	pm.Info("Cache Hit Rates:")
	cacheTypes := []string{"permission", "conversation", "cluster_config", "channel_info"}
	for _, cacheType := range cacheTypes {
		hitRate := pm.GetCacheHitRate(cacheType)
		pm.Info("Cache hit rate",
			zap.String("cache_type", cacheType),
			zap.Float64("hit_rate_percent", hitRate),
		)
	}
	
	// 系统资源统计
	systemStats := pm.GetSystemStats()
	pm.Info("System Resource Usage:",
		zap.Uint64("memory_usage_bytes", systemStats.MemoryUsage),
		zap.Int("goroutine_count", systemStats.GoroutineCount),
		zap.Uint32("gc_count", systemStats.GCCount),
		zap.Uint64("heap_objects", systemStats.HeapObjects),
	)
}

// StartPeriodicReport 启动定期性能报告
func (pm *PerformanceMonitor) StartPeriodicReport(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			pm.LogPerformanceReport()
		}
	}()
}

// MethodTimer 方法计时器
type MethodTimer struct {
	monitor    *PerformanceMonitor
	methodName string
	startTime  time.Time
}

// NewMethodTimer 创建方法计时器
func (pm *PerformanceMonitor) NewMethodTimer(methodName string) *MethodTimer {
	return &MethodTimer{
		monitor:    pm,
		methodName: methodName,
		startTime:  time.Now(),
	}
}

// Stop 停止计时并记录
func (mt *MethodTimer) Stop() {
	duration := time.Since(mt.startTime)
	mt.monitor.RecordMethodCall(mt.methodName, duration)
}

// GetTopSlowMethods 获取最慢的方法
func (pm *PerformanceMonitor) GetTopSlowMethods(limit int) []struct {
	Method      string
	AverageTime time.Duration
} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	type methodTime struct {
		Method      string
		AverageTime time.Duration
	}
	
	var methods []methodTime
	for method, stats := range pm.methodStats {
		methods = append(methods, methodTime{
			Method:      method,
			AverageTime: stats.AverageTime,
		})
	}
	
	// 简单排序（冒泡排序，适用于小数据集）
	for i := 0; i < len(methods)-1; i++ {
		for j := 0; j < len(methods)-i-1; j++ {
			if methods[j].AverageTime < methods[j+1].AverageTime {
				methods[j], methods[j+1] = methods[j+1], methods[j]
			}
		}
	}
	
	if limit > 0 && limit < len(methods) {
		methods = methods[:limit]
	}
	
	result := make([]struct {
		Method      string
		AverageTime time.Duration
	}, len(methods))
	
	for i, m := range methods {
		result[i] = struct {
			Method      string
			AverageTime time.Duration
		}{
			Method:      m.Method,
			AverageTime: m.AverageTime,
		}
	}
	
	return result
}
