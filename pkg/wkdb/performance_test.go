package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestPerformanceMonitor(t *testing.T) {
	monitor := wkdb.NewPerformanceMonitor()

	// 测试方法调用记录
	monitor.RecordMethodCall("TestMethod", 100*time.Millisecond)
	monitor.RecordMethodCall("TestMethod", 200*time.Millisecond)
	monitor.RecordMethodCall("TestMethod", 150*time.Millisecond)

	stats := monitor.GetMethodStats()
	assert.Contains(t, stats, "TestMethod")

	methodStats := stats["TestMethod"]
	assert.Equal(t, int64(3), methodStats.CallCount)
	assert.Equal(t, 450*time.Millisecond, methodStats.TotalTime)
	assert.Equal(t, 150*time.Millisecond, methodStats.AverageTime)
	assert.Equal(t, 200*time.Millisecond, methodStats.MaxTime)
	assert.Equal(t, 100*time.Millisecond, methodStats.MinTime)
}

func TestPerformanceMonitorCacheStats(t *testing.T) {
	monitor := wkdb.NewPerformanceMonitor()

	// 记录缓存命中和未命中
	monitor.RecordCacheHit("permission")
	monitor.RecordCacheHit("permission")
	monitor.RecordCacheMiss("permission")

	monitor.RecordCacheHit("conversation")
	monitor.RecordCacheMiss("conversation")
	monitor.RecordCacheMiss("conversation")

	// 测试缓存命中率
	permissionHitRate := monitor.GetCacheHitRate("permission")
	assert.InDelta(t, 66.67, permissionHitRate, 0.01) // 2/3 = 66.67%

	conversationHitRate := monitor.GetCacheHitRate("conversation")
	assert.InDelta(t, 33.33, conversationHitRate, 0.01) // 1/3 = 33.33%

	// 测试缓存统计
	cacheStats := monitor.GetCacheStats()
	assert.Equal(t, int64(2), cacheStats.PermissionCacheHits)
	assert.Equal(t, int64(1), cacheStats.PermissionCacheMisses)
	assert.Equal(t, int64(1), cacheStats.ConversationCacheHits)
	assert.Equal(t, int64(2), cacheStats.ConversationCacheMisses)
}

func TestPerformanceMonitorMethodTimer(t *testing.T) {
	monitor := wkdb.NewPerformanceMonitor()

	// 测试方法计时器
	timer := monitor.NewMethodTimer("TimedMethod")
	time.Sleep(50 * time.Millisecond)
	timer.Stop()

	stats := monitor.GetMethodStats()
	assert.Contains(t, stats, "TimedMethod")

	methodStats := stats["TimedMethod"]
	assert.Equal(t, int64(1), methodStats.CallCount)
	assert.True(t, methodStats.AverageTime >= 50*time.Millisecond)
}

func TestPerformanceMonitorTopSlowMethods(t *testing.T) {
	monitor := wkdb.NewPerformanceMonitor()

	// 记录不同速度的方法
	monitor.RecordMethodCall("FastMethod", 10*time.Millisecond)
	monitor.RecordMethodCall("MediumMethod", 50*time.Millisecond)
	monitor.RecordMethodCall("SlowMethod", 100*time.Millisecond)
	monitor.RecordMethodCall("VerySlowMethod", 200*time.Millisecond)

	// 获取最慢的方法
	slowMethods := monitor.GetTopSlowMethods(3)
	assert.Len(t, slowMethods, 3)

	// 验证排序（最慢的在前面）
	assert.Equal(t, "VerySlowMethod", slowMethods[0].Method)
	assert.Equal(t, 200*time.Millisecond, slowMethods[0].AverageTime)

	assert.Equal(t, "SlowMethod", slowMethods[1].Method)
	assert.Equal(t, 100*time.Millisecond, slowMethods[1].AverageTime)

	assert.Equal(t, "MediumMethod", slowMethods[2].Method)
	assert.Equal(t, 50*time.Millisecond, slowMethods[2].AverageTime)
}

func TestPerformanceAnalyzer(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	analyzer := wkdb.NewPerformanceAnalyzer(d)

	// 模拟一些操作来生成性能数据
	channelId := "test_channel"
	channelType := uint8(1)
	uid := "test_user"

	// 添加一些数据
	members := []wkdb.Member{{Uid: uid}}
	err = d.AddDenylist(channelId, channelType, members)
	assert.NoError(t, err)

	err = d.AddSubscribers(channelId, channelType, members)
	assert.NoError(t, err)

	err = d.AddAllowlist(channelId, channelType, members)
	assert.NoError(t, err)

	// 执行一些查询操作
	_, err = d.ExistDenylist(channelId, channelType, uid)
	assert.NoError(t, err)

	_, err = d.ExistSubscriber(channelId, channelType, uid)
	assert.NoError(t, err)

	_, err = d.ExistAllowlist(channelId, channelType, uid)
	assert.NoError(t, err)

	// 分析性能
	analyzer.AnalyzeCommonBottlenecks()

	// 生成性能报告
	report := analyzer.GeneratePerformanceReport()
	assert.NotEmpty(t, report)
	assert.Contains(t, report, "Performance Report")
	assert.Contains(t, report, "System Information")
	assert.Contains(t, report, "Cache Information")

	t.Logf("Performance Report:\n%s", report)
}

// BenchmarkPerformanceMonitorOverhead 测试性能监控的开销
func BenchmarkPerformanceMonitorOverhead(b *testing.B) {
	monitor := wkdb.NewPerformanceMonitor()

	b.Run("RecordMethodCall", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			monitor.RecordMethodCall("BenchmarkMethod", time.Microsecond)
		}
	})

	b.Run("RecordCacheHit", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			monitor.RecordCacheHit("permission")
		}
	})

	b.Run("MethodTimer", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			timer := monitor.NewMethodTimer("BenchmarkTimerMethod")
			timer.Stop()
		}
	})
}

// TestPerformanceIntegration 集成测试：验证性能监控与数据库操作的集成
func TestPerformanceIntegration(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	// 执行一系列数据库操作
	channelId := "perf_test_channel"
	channelType := uint8(1)
	uid := "perf_test_user"

	// 测试权限操作
	members := []wkdb.Member{{Uid: uid}}

	start := time.Now()
	err = d.AddDenylist(channelId, channelType, members)
	duration := time.Since(start)
	d.GetPerformanceMonitor().RecordMethodCall("AddDenylist", duration)
	assert.NoError(t, err)

	start = time.Now()
	exists, err := d.ExistDenylist(channelId, channelType, uid)
	duration = time.Since(start)
	d.GetPerformanceMonitor().RecordMethodCall("ExistDenylist", duration)
	assert.NoError(t, err)
	assert.True(t, exists)

	// 测试集群配置操作
	config := wkdb.ChannelClusterConfig{
		ChannelId:   channelId,
		ChannelType: channelType,
		LeaderId:    1,
		ConfVersion: 100,
	}

	start = time.Now()
	err = d.SaveChannelClusterConfig(config)
	duration = time.Since(start)
	d.GetPerformanceMonitor().RecordMethodCall("SaveChannelClusterConfig", duration)
	assert.NoError(t, err)

	start = time.Now()
	_, err = d.GetChannelClusterConfig(channelId, channelType)
	duration = time.Since(start)
	d.GetPerformanceMonitor().RecordMethodCall("GetChannelClusterConfig", duration)
	assert.NoError(t, err)

	// 获取性能统计
	methodStats := d.GetPerformanceMonitor().GetMethodStats()
	assert.Contains(t, methodStats, "AddDenylist")
	assert.Contains(t, methodStats, "ExistDenylist")
	assert.Contains(t, methodStats, "SaveChannelClusterConfig")
	assert.Contains(t, methodStats, "GetChannelClusterConfig")

	// 验证方法调用次数
	assert.Equal(t, int64(1), methodStats["AddDenylist"].CallCount)
	assert.Equal(t, int64(1), methodStats["ExistDenylist"].CallCount)
	assert.Equal(t, int64(1), methodStats["SaveChannelClusterConfig"].CallCount)
	assert.Equal(t, int64(1), methodStats["GetChannelClusterConfig"].CallCount)

	// 输出性能报告
	d.GetPerformanceMonitor().LogPerformanceReport()
}
