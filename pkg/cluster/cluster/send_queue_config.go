package cluster

import (
	"fmt"
	"runtime"
	"time"
)

// SendQueueConfig 发送队列配置
type SendQueueConfig struct {
	// 基础配置
	BaseQueueLength int    `json:"base_queue_length"` // 基础队列长度
	MaxQueueLength  int    `json:"max_queue_length"`  // 最大队列长度
	MaxQueueSize    uint64 `json:"max_queue_size"`    // 最大队列大小（字节）

	// 自适应配置
	EnableAdaptive  bool          `json:"enable_adaptive"`  // 启用自适应扩容
	ExpandThreshold float64       `json:"expand_threshold"` // 扩容阈值（利用率）
	ShrinkThreshold float64       `json:"shrink_threshold"` // 缩容阈值（利用率）
	ExpandCooldown  time.Duration `json:"expand_cooldown"`  // 扩容冷却时间

	// 优先级配置
	EnablePriority         bool    `json:"enable_priority"`           // 启用优先级队列
	HighPriorityQueueRatio float64 `json:"high_priority_queue_ratio"` // 高优先级队列比例

	// 背压配置
	EnableBackpressure    bool    `json:"enable_backpressure"`    // 启用背压控制
	BackpressureThreshold float64 `json:"backpressure_threshold"` // 背压阈值
	BackpressureStrategy  string  `json:"backpressure_strategy"`  // 背压策略

	// 批量处理配置
	MaxBatchSize  int           `json:"max_batch_size"`  // 最大批量大小
	MaxBatchBytes uint64        `json:"max_batch_bytes"` // 最大批量字节数
	BatchTimeout  time.Duration `json:"batch_timeout"`   // 批量超时时间
	SendStrategy  string        `json:"send_strategy"`   // 发送策略

	// 监控配置
	EnableMonitoring   bool          `json:"enable_monitoring"`   // 启用监控
	MonitoringInterval time.Duration `json:"monitoring_interval"` // 监控间隔
	LogPerformance     bool          `json:"log_performance"`     // 记录性能日志
}

// DefaultSendQueueConfig 默认发送队列配置
func DefaultSendQueueConfig() *SendQueueConfig {
	return &SendQueueConfig{
		// 基础配置
		BaseQueueLength: 4056 * 10,         // 40,560
		MaxQueueLength:  4056 * 40,         // 162,240 (4倍扩容)
		MaxQueueSize:    256 * 1024 * 1024, // 256MB

		// 自适应配置
		EnableAdaptive:  true,
		ExpandThreshold: 0.8,  // 80%时扩容
		ShrinkThreshold: 0.25, // 25%时缩容
		ExpandCooldown:  time.Second,

		// 优先级配置
		EnablePriority:         true,
		HighPriorityQueueRatio: 0.1, // 10%

		// 背压配置
		EnableBackpressure:    true,
		BackpressureThreshold: 0.9,         // 90%时启用背压
		BackpressureStrategy:  "slow_down", // slow_down, drop_oldest, block

		// 批量处理配置
		MaxBatchSize:  64,
		MaxBatchBytes: 64 * 1024 * 1024, // 64MB
		BatchTimeout:  time.Millisecond * 100,
		SendStrategy:  "adaptive", // default, batch, latency, throughput, adaptive

		// 监控配置
		EnableMonitoring:   true,
		MonitoringInterval: time.Minute,
		LogPerformance:     true,
	}
}

// HighThroughputConfig 高吞吐量配置
func HighThroughputConfig() *SendQueueConfig {
	config := DefaultSendQueueConfig()

	// 增大队列容量
	config.BaseQueueLength = 4056 * 20      // 81,120
	config.MaxQueueLength = 4056 * 80       // 324,480
	config.MaxQueueSize = 512 * 1024 * 1024 // 512MB

	// 更激进的扩容策略
	config.ExpandThreshold = 0.7 // 70%时扩容
	config.ShrinkThreshold = 0.2 // 20%时缩容

	// 大批量处理
	config.MaxBatchSize = 128
	config.MaxBatchBytes = 128 * 1024 * 1024 // 128MB
	config.SendStrategy = "throughput"

	return config
}

// LowLatencyConfig 低延迟配置
func LowLatencyConfig() *SendQueueConfig {
	config := DefaultSendQueueConfig()

	// 较小的队列，快速处理
	config.BaseQueueLength = 4056 * 5 // 20,280
	config.MaxQueueLength = 4056 * 20 // 81,120

	// 快速扩容
	config.ExpandThreshold = 0.6 // 60%时扩容
	config.ExpandCooldown = time.Millisecond * 500

	// 小批量，快速发送
	config.MaxBatchSize = 16
	config.MaxBatchBytes = 16 * 1024 * 1024 // 16MB
	config.BatchTimeout = time.Millisecond * 50
	config.SendStrategy = "latency"

	return config
}

// MemoryConstrainedConfig 内存受限配置
func MemoryConstrainedConfig() *SendQueueConfig {
	config := DefaultSendQueueConfig()

	// 较小的队列容量
	config.BaseQueueLength = 4056 * 2      // 8,112
	config.MaxQueueLength = 4056 * 8       // 32,448
	config.MaxQueueSize = 64 * 1024 * 1024 // 64MB

	// 保守的扩容策略
	config.ExpandThreshold = 0.9 // 90%时扩容
	config.ShrinkThreshold = 0.3 // 30%时缩容

	// 启用背压控制
	config.BackpressureThreshold = 0.8
	config.BackpressureStrategy = "drop_oldest"

	return config
}

// AdaptiveConfig 自适应配置（根据系统资源动态调整）
func AdaptiveConfig() *SendQueueConfig {
	config := DefaultSendQueueConfig()

	// 根据CPU核心数调整队列大小
	cpuCount := runtime.NumCPU()
	baseMultiplier := cpuCount * 2

	config.BaseQueueLength = 4056 * baseMultiplier
	config.MaxQueueLength = 4056 * baseMultiplier * 4

	// 根据可用内存调整队列大小
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	availableMemMB := memStats.Sys / 1024 / 1024

	if availableMemMB > 1024 { // > 1GB
		config.MaxQueueSize = 512 * 1024 * 1024 // 512MB
	} else if availableMemMB > 512 { // > 512MB
		config.MaxQueueSize = 256 * 1024 * 1024 // 256MB
	} else {
		config.MaxQueueSize = 128 * 1024 * 1024 // 128MB
	}

	config.SendStrategy = "adaptive"

	return config
}

// GetConfigByProfile 根据配置文件获取配置
func GetConfigByProfile(profile string) *SendQueueConfig {
	switch profile {
	case "high_throughput":
		return HighThroughputConfig()
	case "low_latency":
		return LowLatencyConfig()
	case "memory_constrained":
		return MemoryConstrainedConfig()
	case "adaptive":
		return AdaptiveConfig()
	default:
		return DefaultSendQueueConfig()
	}
}

// ValidateConfig 验证配置
func (c *SendQueueConfig) ValidateConfig() error {
	if c.BaseQueueLength <= 0 {
		return fmt.Errorf("base queue length must be positive")
	}

	if c.MaxQueueLength < c.BaseQueueLength {
		return fmt.Errorf("max queue length must be >= base queue length")
	}

	if c.ExpandThreshold <= 0 || c.ExpandThreshold >= 1 {
		return fmt.Errorf("expand threshold must be between 0 and 1")
	}

	if c.ShrinkThreshold <= 0 || c.ShrinkThreshold >= c.ExpandThreshold {
		return fmt.Errorf("shrink threshold must be between 0 and expand threshold")
	}

	if c.BackpressureThreshold <= 0 || c.BackpressureThreshold >= 1 {
		return fmt.Errorf("backpressure threshold must be between 0 and 1")
	}

	return nil
}

// ApplyToOptions 将配置应用到选项
func (c *SendQueueConfig) ApplyToOptions(opts *Options) {
	opts.SendQueueLength = c.BaseQueueLength
	opts.MaxSendQueueSize = c.MaxQueueSize
	opts.MaxMessageBatchSize = c.MaxBatchBytes
}

// 性能调优建议
type PerformanceTuningAdvice struct {
	CurrentUtilization float64 `json:"current_utilization"`
	RecommendedAction  string  `json:"recommended_action"`
	RecommendedConfig  string  `json:"recommended_config"`
	Reason             string  `json:"reason"`
}

// AnalyzePerformance 分析性能并给出调优建议
func AnalyzePerformance(stats map[string]interface{}) *PerformanceTuningAdvice {
	advice := &PerformanceTuningAdvice{}

	// 获取队列利用率
	queueLen, ok1 := stats["queue_length"].(int)
	queueCap, ok2 := stats["current_capacity"].(int)

	if ok1 && ok2 && queueCap > 0 {
		advice.CurrentUtilization = float64(queueLen) / float64(queueCap)

		if advice.CurrentUtilization > 0.9 {
			advice.RecommendedAction = "increase_capacity"
			advice.RecommendedConfig = "high_throughput"
			advice.Reason = "Queue utilization is very high, consider increasing capacity"
		} else if advice.CurrentUtilization < 0.2 {
			advice.RecommendedAction = "decrease_capacity"
			advice.RecommendedConfig = "memory_constrained"
			advice.Reason = "Queue utilization is low, consider decreasing capacity to save memory"
		} else {
			advice.RecommendedAction = "no_change"
			advice.RecommendedConfig = "current"
			advice.Reason = "Queue utilization is within optimal range"
		}
	}

	// 检查丢弃率
	totalSent, ok3 := stats["perf_total_sent"].(uint64)
	totalDropped, ok4 := stats["perf_total_dropped"].(uint64)

	if ok3 && ok4 && totalSent > 0 {
		dropRate := float64(totalDropped) / float64(totalSent+totalDropped)

		if dropRate > 0.05 { // 5%丢弃率
			advice.RecommendedAction = "enable_backpressure"
			advice.RecommendedConfig = "adaptive"
			advice.Reason = fmt.Sprintf("High drop rate (%.2f%%), enable backpressure control", dropRate*100)
		}
	}

	return advice
}

// 配置模板
var ConfigTemplates = map[string]*SendQueueConfig{
	"default":            DefaultSendQueueConfig(),
	"high_throughput":    HighThroughputConfig(),
	"low_latency":        LowLatencyConfig(),
	"memory_constrained": MemoryConstrainedConfig(),
	"adaptive":           AdaptiveConfig(),
}
