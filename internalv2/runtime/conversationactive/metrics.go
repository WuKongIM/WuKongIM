package conversationactive

import (
	"sync/atomic"
	"time"
)

// Metrics 收集运行时指标
type Metrics struct {
	// 缓存指标
	HotCacheHits     atomic.Uint64
	HotCacheMisses   atomic.Uint64
	ColdCacheHits    atomic.Uint64
	ColdCacheMisses  atomic.Uint64

	// 写入指标
	MarkActiveOps    atomic.Uint64
	MarkActiveErrors atomic.Uint64

	// 刷盘指标
	FlushOps         atomic.Uint64
	FlushErrors      atomic.Uint64
	FlushRowsTotal   atomic.Uint64

	// 迁移指标
	HotToColdMoves   atomic.Uint64
	ColdToHotMoves   atomic.Uint64

	// 自适应刷盘指标
	IntervalAdjustments atomic.Uint64
	CurrentInterval     atomic.Int64 // 纳秒
}

// NewMetrics 创建指标收集器
func NewMetrics() *Metrics {
	return &Metrics{}
}

// GetCacheHitRate 获取缓存命中率（热缓存）
func (m *Metrics) GetCacheHitRate() float64 {
	hits := m.HotCacheHits.Load()
	misses := m.HotCacheMisses.Load()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// GetTotalCacheHitRate 获取总缓存命中率（热+冷）
func (m *Metrics) GetTotalCacheHitRate() float64 {
	hotHits := m.HotCacheHits.Load()
	coldHits := m.ColdCacheHits.Load()
	hotMisses := m.HotCacheMisses.Load()
	coldMisses := m.ColdCacheMisses.Load()

	total := hotHits + coldHits + hotMisses + coldMisses
	if total == 0 {
		return 0
	}
	return float64(hotHits+coldHits) / float64(total)
}

// GetFlushSuccessRate 获取刷盘成功率
func (m *Metrics) GetFlushSuccessRate() float64 {
	ops := m.FlushOps.Load()
	errors := m.FlushErrors.Load()
	if ops == 0 {
		return 0
	}
	success := ops - errors
	return float64(success) / float64(ops)
}

// GetAverageFlushRows 获取平均每次刷盘条目数
func (m *Metrics) GetAverageFlushRows() float64 {
	ops := m.FlushOps.Load()
	if ops == 0 {
		return 0
	}
	total := m.FlushRowsTotal.Load()
	return float64(total) / float64(ops)
}

// GetCurrentFlushInterval 获取当前刷盘间隔
func (m *Metrics) GetCurrentFlushInterval() time.Duration {
	ns := m.CurrentInterval.Load()
	return time.Duration(ns)
}

// Snapshot 获取当前指标快照
type MetricsSnapshot struct {
	HotCacheHits        uint64
	HotCacheMisses      uint64
	ColdCacheHits       uint64
	ColdCacheMisses     uint64
	CacheHitRate        float64
	TotalCacheHitRate   float64

	MarkActiveOps       uint64
	MarkActiveErrors    uint64

	FlushOps            uint64
	FlushErrors         uint64
	FlushRowsTotal      uint64
	FlushSuccessRate    float64
	AverageFlushRows    float64

	HotToColdMoves      uint64
	ColdToHotMoves      uint64

	IntervalAdjustments uint64
	CurrentInterval     time.Duration
}

// GetSnapshot 获取当前指标快照
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	return MetricsSnapshot{
		HotCacheHits:        m.HotCacheHits.Load(),
		HotCacheMisses:      m.HotCacheMisses.Load(),
		ColdCacheHits:       m.ColdCacheHits.Load(),
		ColdCacheMisses:     m.ColdCacheMisses.Load(),
		CacheHitRate:        m.GetCacheHitRate(),
		TotalCacheHitRate:   m.GetTotalCacheHitRate(),

		MarkActiveOps:       m.MarkActiveOps.Load(),
		MarkActiveErrors:    m.MarkActiveErrors.Load(),

		FlushOps:            m.FlushOps.Load(),
		FlushErrors:         m.FlushErrors.Load(),
		FlushRowsTotal:      m.FlushRowsTotal.Load(),
		FlushSuccessRate:    m.GetFlushSuccessRate(),
		AverageFlushRows:    m.GetAverageFlushRows(),

		HotToColdMoves:      m.HotToColdMoves.Load(),
		ColdToHotMoves:      m.ColdToHotMoves.Load(),

		IntervalAdjustments: m.IntervalAdjustments.Load(),
		CurrentInterval:     m.GetCurrentFlushInterval(),
	}
}
