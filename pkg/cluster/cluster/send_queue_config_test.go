package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultSendQueueConfig(t *testing.T) {
	config := DefaultSendQueueConfig()

	assert.Equal(t, 4056*10, config.BaseQueueLength)
	assert.Equal(t, 4056*40, config.MaxQueueLength)
	assert.Equal(t, uint64(256*1024*1024), config.MaxQueueSize)
	assert.True(t, config.EnableAdaptive)
	assert.True(t, config.EnablePriority)
	assert.True(t, config.EnableBackpressure)
	assert.True(t, config.EnableMonitoring)

	// 验证配置有效性
	err := config.ValidateConfig()
	assert.NoError(t, err)
}

func TestHighThroughputConfig(t *testing.T) {
	config := HighThroughputConfig()

	// 高吞吐量配置应该有更大的队列
	assert.Equal(t, 4056*20, config.BaseQueueLength)
	assert.Equal(t, 4056*80, config.MaxQueueLength)
	assert.Equal(t, uint64(512*1024*1024), config.MaxQueueSize)

	// 更激进的扩容策略
	assert.Equal(t, 0.7, config.ExpandThreshold)
	assert.Equal(t, 0.2, config.ShrinkThreshold)

	// 大批量处理
	assert.Equal(t, 128, config.MaxBatchSize)
	assert.Equal(t, uint64(128*1024*1024), config.MaxBatchBytes)
	assert.Equal(t, "throughput", config.SendStrategy)

	err := config.ValidateConfig()
	assert.NoError(t, err)
}

func TestLowLatencyConfig(t *testing.T) {
	config := LowLatencyConfig()

	// 低延迟配置应该有较小的队列和快速处理
	assert.Equal(t, 4056*5, config.BaseQueueLength)
	assert.Equal(t, 4056*20, config.MaxQueueLength)

	// 快速扩容
	assert.Equal(t, 0.6, config.ExpandThreshold)

	// 小批量，快速发送
	assert.Equal(t, 16, config.MaxBatchSize)
	assert.Equal(t, uint64(16*1024*1024), config.MaxBatchBytes)
	assert.Equal(t, "latency", config.SendStrategy)

	err := config.ValidateConfig()
	assert.NoError(t, err)
}

func TestMemoryConstrainedConfig(t *testing.T) {
	config := MemoryConstrainedConfig()

	// 内存受限配置应该有较小的队列容量
	assert.Equal(t, 4056*2, config.BaseQueueLength)
	assert.Equal(t, 4056*8, config.MaxQueueLength)
	assert.Equal(t, uint64(64*1024*1024), config.MaxQueueSize)

	// 保守的扩容策略
	assert.Equal(t, 0.9, config.ExpandThreshold)
	assert.Equal(t, 0.3, config.ShrinkThreshold)

	// 启用背压控制
	assert.Equal(t, 0.8, config.BackpressureThreshold)
	assert.Equal(t, "drop_oldest", config.BackpressureStrategy)

	err := config.ValidateConfig()
	assert.NoError(t, err)
}

func TestAdaptiveConfig(t *testing.T) {
	config := AdaptiveConfig()

	// 自适应配置应该根据系统资源调整
	assert.Greater(t, config.BaseQueueLength, 0)
	assert.Greater(t, config.MaxQueueLength, config.BaseQueueLength)
	assert.Greater(t, config.MaxQueueSize, uint64(0))
	assert.Equal(t, "adaptive", config.SendStrategy)

	err := config.ValidateConfig()
	assert.NoError(t, err)
}

func TestGetConfigByProfile(t *testing.T) {
	testCases := []struct {
		profile  string
		expected string
	}{
		{"high_throughput", "throughput"},
		{"low_latency", "latency"},
		{"memory_constrained", "drop_oldest"},
		{"adaptive", "adaptive"},
		{"default", "adaptive"},
		{"unknown", "adaptive"},
	}

	for _, tc := range testCases {
		t.Run(tc.profile, func(t *testing.T) {
			config := GetConfigByProfile(tc.profile)
			assert.NotNil(t, config)

			if tc.profile == "memory_constrained" {
				assert.Equal(t, tc.expected, config.BackpressureStrategy)
			} else {
				assert.Equal(t, tc.expected, config.SendStrategy)
			}

			err := config.ValidateConfig()
			assert.NoError(t, err)
		})
	}
}

func TestConfigValidation(t *testing.T) {
	testCases := []struct {
		name        string
		modifyFunc  func(*SendQueueConfig)
		expectError bool
		errorMsg    string
	}{
		{
			name: "negative base queue length",
			modifyFunc: func(c *SendQueueConfig) {
				c.BaseQueueLength = -1
			},
			expectError: true,
			errorMsg:    "base queue length must be positive",
		},
		{
			name: "max queue length less than base",
			modifyFunc: func(c *SendQueueConfig) {
				c.MaxQueueLength = c.BaseQueueLength - 1
			},
			expectError: true,
			errorMsg:    "max queue length must be >= base queue length",
		},
		{
			name: "invalid expand threshold",
			modifyFunc: func(c *SendQueueConfig) {
				c.ExpandThreshold = 1.5
			},
			expectError: true,
			errorMsg:    "expand threshold must be between 0 and 1",
		},
		{
			name: "invalid shrink threshold",
			modifyFunc: func(c *SendQueueConfig) {
				c.ShrinkThreshold = c.ExpandThreshold + 0.1
			},
			expectError: true,
			errorMsg:    "shrink threshold must be between 0 and expand threshold",
		},
		{
			name: "invalid backpressure threshold",
			modifyFunc: func(c *SendQueueConfig) {
				c.BackpressureThreshold = 1.5
			},
			expectError: true,
			errorMsg:    "backpressure threshold must be between 0 and 1",
		},
		{
			name: "valid config",
			modifyFunc: func(c *SendQueueConfig) {
				// 不修改，保持有效配置
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultSendQueueConfig()
			tc.modifyFunc(config)

			err := config.ValidateConfig()
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestApplyToOptions(t *testing.T) {
	config := HighThroughputConfig()
	opts := NewOptions()

	// 记录原始值
	originalSendQueueLength := opts.SendQueueLength
	originalMaxSendQueueSize := opts.MaxSendQueueSize
	originalMaxMessageBatchSize := opts.MaxMessageBatchSize

	// 应用配置
	config.ApplyToOptions(opts)

	// 验证配置已应用
	assert.NotEqual(t, originalSendQueueLength, opts.SendQueueLength)
	assert.NotEqual(t, originalMaxSendQueueSize, opts.MaxSendQueueSize)
	assert.NotEqual(t, originalMaxMessageBatchSize, opts.MaxMessageBatchSize)

	assert.Equal(t, config.BaseQueueLength, opts.SendQueueLength)
	assert.Equal(t, config.MaxQueueSize, opts.MaxSendQueueSize)
	assert.Equal(t, config.MaxBatchBytes, opts.MaxMessageBatchSize)
}

func TestAnalyzePerformance(t *testing.T) {
	testCases := []struct {
		name           string
		stats          map[string]interface{}
		expectedAction string
		expectedConfig string
	}{
		{
			name: "high utilization",
			stats: map[string]interface{}{
				"queue_length":       950, // 95% 利用率，超过 90% 阈值
				"current_capacity":   1000,
				"perf_total_sent":    uint64(1000),
				"perf_total_dropped": uint64(10),
			},
			expectedAction: "increase_capacity",
			expectedConfig: "high_throughput",
		},
		{
			name: "low utilization",
			stats: map[string]interface{}{
				"queue_length":       100,
				"current_capacity":   1000,
				"perf_total_sent":    uint64(1000),
				"perf_total_dropped": uint64(0),
			},
			expectedAction: "decrease_capacity",
			expectedConfig: "memory_constrained",
		},
		{
			name: "high drop rate",
			stats: map[string]interface{}{
				"queue_length":       500,
				"current_capacity":   1000,
				"perf_total_sent":    uint64(900),
				"perf_total_dropped": uint64(100), // 10% drop rate
			},
			expectedAction: "enable_backpressure",
			expectedConfig: "adaptive",
		},
		{
			name: "optimal utilization",
			stats: map[string]interface{}{
				"queue_length":       500,
				"current_capacity":   1000,
				"perf_total_sent":    uint64(1000),
				"perf_total_dropped": uint64(10), // 1% drop rate
			},
			expectedAction: "no_change",
			expectedConfig: "current",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			advice := AnalyzePerformance(tc.stats)

			assert.NotNil(t, advice)
			assert.Equal(t, tc.expectedAction, advice.RecommendedAction)
			assert.Equal(t, tc.expectedConfig, advice.RecommendedConfig)
			assert.NotEmpty(t, advice.Reason)

			if queueLen, ok := tc.stats["queue_length"].(int); ok {
				if queueCap, ok := tc.stats["current_capacity"].(int); ok && queueCap > 0 {
					expectedUtilization := float64(queueLen) / float64(queueCap)
					assert.InDelta(t, expectedUtilization, advice.CurrentUtilization, 0.01)
				}
			}
		})
	}
}

func TestConfigTemplates(t *testing.T) {
	// 验证所有配置模板都是有效的
	for name, config := range ConfigTemplates {
		t.Run(name, func(t *testing.T) {
			assert.NotNil(t, config, "Config template %s should not be nil", name)

			err := config.ValidateConfig()
			assert.NoError(t, err, "Config template %s should be valid", name)

			// 验证基本属性
			assert.Greater(t, config.BaseQueueLength, 0)
			assert.GreaterOrEqual(t, config.MaxQueueLength, config.BaseQueueLength)
			assert.Greater(t, config.MaxQueueSize, uint64(0))
			assert.Greater(t, config.ExpandThreshold, 0.0)
			assert.Less(t, config.ExpandThreshold, 1.0)
			assert.Greater(t, config.ShrinkThreshold, 0.0)
			assert.Less(t, config.ShrinkThreshold, config.ExpandThreshold)
		})
	}
}

// 基准测试
func BenchmarkConfigValidation(b *testing.B) {
	config := DefaultSendQueueConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.ValidateConfig()
	}
}

func BenchmarkGetConfigByProfile(b *testing.B) {
	profiles := []string{"default", "high_throughput", "low_latency", "memory_constrained", "adaptive"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		profile := profiles[i%len(profiles)]
		_ = GetConfigByProfile(profile)
	}
}

func BenchmarkAnalyzePerformance(b *testing.B) {
	stats := map[string]interface{}{
		"queue_length":       500,
		"current_capacity":   1000,
		"perf_total_sent":    uint64(1000),
		"perf_total_dropped": uint64(50),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AnalyzePerformance(stats)
	}
}
