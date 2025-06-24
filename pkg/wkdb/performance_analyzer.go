package wkdb

import (
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// PerformanceAnalyzer 性能分析器
type PerformanceAnalyzer struct {
	wklog.Log
	db DB
}

// NewPerformanceAnalyzer 创建性能分析器
func NewPerformanceAnalyzer(db DB) *PerformanceAnalyzer {
	return &PerformanceAnalyzer{
		Log: wklog.NewWKLog("PerformanceAnalyzer"),
		db:  db,
	}
}

// AnalyzeCommonBottlenecks 分析常见性能瓶颈
func (pa *PerformanceAnalyzer) AnalyzeCommonBottlenecks() {
	pa.Info("=== Performance Bottleneck Analysis ===")

	wk := pa.db.(*wukongDB)
	// 1. 分析缓存命中率
	pa.analyzeCachePerformance()

	// 2. 分析内存使用
	pa.analyzeMemoryUsage()

	// 3. 分析 Goroutine 数量
	pa.analyzeGoroutines()

	// 4. 分析方法调用性能
	pa.analyzeMethodPerformance(wk)

	// 5. 提供优化建议
	pa.provideOptimizationSuggestions(wk)
}

// analyzeCachePerformance 分析缓存性能
func (pa *PerformanceAnalyzer) analyzeCachePerformance() {
	pa.Info("--- Cache Performance Analysis ---")
	wk := pa.db.(*wukongDB)
	// 权限缓存统计
	permissionStats := wk.permissionCache.GetCacheStats()
	pa.Info("Permission Cache Stats", zap.Any("stats", permissionStats))

	// 会话缓存统计
	conversationStats := wk.conversationCache.GetCacheStats()
	pa.Info("Conversation Cache Stats", zap.Any("stats", conversationStats))

	// 集群配置缓存统计
	clusterConfigStats := wk.clusterConfigCache.GetCacheStats()
	pa.Info("Cluster Config Cache Stats", zap.Any("stats", clusterConfigStats))

	// 频道信息缓存统计
	channelInfoStats := wk.channelInfoCache.GetCacheStats()
	pa.Info("Channel Info Cache Stats", zap.Any("stats", channelInfoStats))

	// 分析缓存利用率
	pa.analyzeCacheUtilization(permissionStats, "Permission")
	pa.analyzeCacheUtilization(conversationStats, "Conversation")
	pa.analyzeCacheUtilization(clusterConfigStats, "ClusterConfig")
	pa.analyzeCacheUtilization(channelInfoStats, "ChannelInfo")
}

// analyzeCacheUtilization 分析缓存利用率
func (pa *PerformanceAnalyzer) analyzeCacheUtilization(stats map[string]interface{}, cacheType string) {
	if cacheLen, ok := stats["total_cache_len"].(int); ok {
		if cacheMax, ok := stats["cache_max"].(int); ok {
			utilization := float64(cacheLen) / float64(cacheMax) * 100

			pa.Info("Cache Utilization",
				zap.String("cache_type", cacheType),
				zap.Int("current_size", cacheLen),
				zap.Int("max_size", cacheMax),
				zap.Float64("utilization_percent", utilization),
			)

			// 提供建议
			if utilization > 90 {
				pa.Warn("Cache utilization is high, consider increasing cache size",
					zap.String("cache_type", cacheType),
					zap.Float64("utilization", utilization),
				)
			} else if utilization < 30 {
				pa.Info("Cache utilization is low, consider reducing cache size",
					zap.String("cache_type", cacheType),
					zap.Float64("utilization", utilization),
				)
			}
		}
	}
}

// analyzeMemoryUsage 分析内存使用
func (pa *PerformanceAnalyzer) analyzeMemoryUsage() {
	pa.Info("--- Memory Usage Analysis ---")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	pa.Info("Memory Statistics",
		zap.Uint64("alloc_bytes", m.Alloc),
		zap.Uint64("total_alloc_bytes", m.TotalAlloc),
		zap.Uint64("sys_bytes", m.Sys),
		zap.Uint64("heap_alloc_bytes", m.HeapAlloc),
		zap.Uint64("heap_sys_bytes", m.HeapSys),
		zap.Uint64("heap_objects", m.HeapObjects),
		zap.Uint32("num_gc", m.NumGC),
		zap.Float64("gc_cpu_fraction", m.GCCPUFraction),
	)

	// 内存使用建议
	allocMB := float64(m.Alloc) / 1024 / 1024
	sysMB := float64(m.Sys) / 1024 / 1024

	pa.Info("Memory Usage Summary",
		zap.Float64("allocated_mb", allocMB),
		zap.Float64("system_mb", sysMB),
	)

	if allocMB > 1000 { // 超过1GB
		pa.Warn("High memory usage detected", zap.Float64("allocated_mb", allocMB))
	}

	if m.GCCPUFraction > 0.1 { // GC占用超过10%CPU
		pa.Warn("High GC CPU usage", zap.Float64("gc_cpu_fraction", m.GCCPUFraction))
	}
}

// analyzeGoroutines 分析 Goroutine 数量
func (pa *PerformanceAnalyzer) analyzeGoroutines() {
	pa.Info("--- Goroutine Analysis ---")

	numGoroutines := runtime.NumGoroutine()
	pa.Info("Goroutine Count", zap.Int("count", numGoroutines))

	if numGoroutines > 10000 {
		pa.Warn("High number of goroutines detected", zap.Int("count", numGoroutines))
	} else if numGoroutines > 1000 {
		pa.Info("Moderate number of goroutines", zap.Int("count", numGoroutines))
	}
}

// analyzeMethodPerformance 分析方法性能
func (pa *PerformanceAnalyzer) analyzeMethodPerformance(wk *wukongDB) {
	pa.Info("--- Method Performance Analysis ---")

	// 获取最慢的方法
	slowMethods := wk.performanceMonitor.GetTopSlowMethods(10)

	pa.Info("Top 10 Slowest Methods:")
	for i, method := range slowMethods {
		pa.Info("Slow method",
			zap.Int("rank", i+1),
			zap.String("method", method.Method),
			zap.Duration("avg_time", method.AverageTime),
		)
	}

	// 分析方法调用频率
	methodStats := wk.performanceMonitor.GetMethodStats()

	type methodCall struct {
		Method    string
		CallCount int64
	}

	var frequentMethods []methodCall
	for method, stats := range methodStats {
		frequentMethods = append(frequentMethods, methodCall{
			Method:    method,
			CallCount: stats.CallCount,
		})
	}

	// 按调用次数排序
	sort.Slice(frequentMethods, func(i, j int) bool {
		return frequentMethods[i].CallCount > frequentMethods[j].CallCount
	})

	pa.Info("Top 10 Most Called Methods:")
	limit := 10
	if len(frequentMethods) < limit {
		limit = len(frequentMethods)
	}

	for i := 0; i < limit; i++ {
		method := frequentMethods[i]
		pa.Info("Frequent method",
			zap.Int("rank", i+1),
			zap.String("method", method.Method),
			zap.Int64("call_count", method.CallCount),
		)
	}
}

// provideOptimizationSuggestions 提供优化建议
func (pa *PerformanceAnalyzer) provideOptimizationSuggestions(wk *wukongDB) {
	pa.Info("--- Optimization Suggestions ---")

	var suggestions []string

	// 基于内存使用的建议
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	if m.GCCPUFraction > 0.05 {
		suggestions = append(suggestions, "Consider optimizing memory allocations to reduce GC pressure")
	}

	if m.HeapObjects > 10000000 { // 超过1000万对象
		suggestions = append(suggestions, "High number of heap objects, consider object pooling")
	}

	// 基于 Goroutine 数量的建议
	if runtime.NumGoroutine() > 1000 {
		suggestions = append(suggestions, "High goroutine count, check for goroutine leaks")
	}

	// 基于缓存的建议
	permissionStats := wk.permissionCache.GetCacheStats()
	if totalLen, ok := permissionStats["total_cache_len"].(int); ok {
		if maxSize, ok := permissionStats["cache_max"].(int); ok {
			if float64(totalLen)/float64(maxSize) > 0.9 {
				suggestions = append(suggestions, "Permission cache is near capacity, consider increasing size")
			}
		}
	}

	// 基于方法性能的建议
	slowMethods := wk.performanceMonitor.GetTopSlowMethods(3)
	for _, method := range slowMethods {
		if method.AverageTime > time.Millisecond*100 {
			suggestions = append(suggestions, fmt.Sprintf("Method '%s' is slow (avg: %v), consider optimization", method.Method, method.AverageTime))
		}
	}

	// 输出建议
	if len(suggestions) == 0 {
		pa.Info("No specific optimization suggestions at this time")
	} else {
		pa.Info("Optimization Suggestions:")
		for i, suggestion := range suggestions {
			pa.Info("Suggestion", zap.Int("index", i+1), zap.String("suggestion", suggestion))
		}
	}
}

// GeneratePerformanceReport 生成性能报告
func (pa *PerformanceAnalyzer) GeneratePerformanceReport() string {
	report := "=== WuKongIM Performance Report ===\n\n"
	wk := pa.db.(*wukongDB)
	// 系统信息
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	report += fmt.Sprintf("System Information:\n")
	report += fmt.Sprintf("- Memory Allocated: %.2f MB\n", float64(m.Alloc)/1024/1024)
	report += fmt.Sprintf("- System Memory: %.2f MB\n", float64(m.Sys)/1024/1024)
	report += fmt.Sprintf("- Goroutines: %d\n", runtime.NumGoroutine())
	report += fmt.Sprintf("- GC Count: %d\n", m.NumGC)
	report += fmt.Sprintf("- GC CPU Fraction: %.4f\n\n", m.GCCPUFraction)

	// 缓存信息
	report += "Cache Information:\n"

	permissionStats := wk.permissionCache.GetCacheStats()
	if totalLen, ok := permissionStats["total_cache_len"].(int); ok {
		if maxSize, ok := permissionStats["cache_max"].(int); ok {
			utilization := float64(totalLen) / float64(maxSize) * 100
			report += fmt.Sprintf("- Permission Cache: %d/%d (%.1f%%)\n", totalLen, maxSize, utilization)
		}
	}

	conversationStats := wk.conversationCache.GetCacheStats()
	if cacheLen, ok := conversationStats["conversation_cache_len"].(int); ok {
		if maxSize, ok := conversationStats["conversation_cache_max"].(int); ok {
			utilization := float64(cacheLen) / float64(maxSize) * 100
			report += fmt.Sprintf("- Conversation Cache: %d/%d (%.1f%%)\n", cacheLen, maxSize, utilization)
		}
	}

	clusterConfigStats := wk.clusterConfigCache.GetCacheStats()
	if cacheLen, ok := clusterConfigStats["cluster_config_cache_len"].(int); ok {
		if maxSize, ok := clusterConfigStats["cluster_config_cache_max"].(int); ok {
			utilization := float64(cacheLen) / float64(maxSize) * 100
			report += fmt.Sprintf("- Cluster Config Cache: %d/%d (%.1f%%)\n", cacheLen, maxSize, utilization)
		}
	}

	report += "\n"

	// 性能热点
	slowMethods := wk.performanceMonitor.GetTopSlowMethods(5)
	if len(slowMethods) > 0 {
		report += "Top 5 Slowest Methods:\n"
		for i, method := range slowMethods {
			report += fmt.Sprintf("%d. %s: %v\n", i+1, method.Method, method.AverageTime)
		}
		report += "\n"
	}

	return report
}

// StartPerformanceMonitoring 启动性能监控
func (pa *PerformanceAnalyzer) StartPerformanceMonitoring(interval time.Duration) {
	wk := pa.db.(*wukongDB)
	// 启动定期性能分析
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			pa.AnalyzeCommonBottlenecks()
		}
	}()

	// 启动性能监控器的定期报告
	wk.performanceMonitor.StartPeriodicReport(interval * 2) // 每2个分析周期输出一次详细报告
}
