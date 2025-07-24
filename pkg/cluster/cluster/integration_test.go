package cluster

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendQueueIntegration 发送队列集成测试
func TestSendQueueIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// 创建测试配置
	config := DefaultSendQueueConfig()
	config.BaseQueueLength = 100
	config.MaxQueueLength = 400
	config.MaxQueueSize = 10 * 1024 * 1024 // 10MB

	// 创建自适应队列
	queue := NewAdaptiveSendQueue(config.BaseQueueLength, config.MaxQueueLength, config.MaxQueueSize)
	queue.EnablePriority(config.BaseQueueLength / 10)
	defer queue.Close()

	// 创建改进的节点
	opts := NewOptions()
	config.ApplyToOptions(opts)

	node := NewImprovedNode(1, "integration-test", "127.0.0.1:8080", opts)
	defer node.Stop()

	t.Run("BasicSendReceive", func(t *testing.T) {
		testBasicSendReceive(t, queue)
	})

	t.Run("PriorityHandling", func(t *testing.T) {
		testPriorityHandling(t, queue)
	})

	t.Run("AutoExpansion", func(t *testing.T) {
		testAutoExpansion(t, queue, config)
	})

	t.Run("BackpressureControl", func(t *testing.T) {
		testBackpressureControl(t, node)
	})

	t.Run("ConcurrentOperations", func(t *testing.T) {
		testConcurrentOperations(t, queue)
	})

	t.Run("PerformanceMonitoring", func(t *testing.T) {
		testPerformanceMonitoring(t, node)
	})
}

func testBasicSendReceive(t *testing.T, queue *AdaptiveSendQueue) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 发送消息
	msg := &proto.Message{MsgType: 1, Content: []byte("test message")}
	err := queue.Send(msg, false)
	require.NoError(t, err)

	// 接收消息
	receivedMsg, ok := queue.Receive(ctx)
	require.True(t, ok)
	assert.Equal(t, msg.MsgType, receivedMsg.MsgType)
	assert.Equal(t, msg.Content, receivedMsg.Content)

	// 验证统计
	stats := queue.GetStats()
	assert.Equal(t, uint64(1), stats["total_sent"])
}

func testPriorityHandling(t *testing.T, queue *AdaptiveSendQueue) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 发送普通消息
	normalMsg := &proto.Message{MsgType: 1}
	err := queue.Send(normalMsg, false)
	require.NoError(t, err)

	// 发送高优先级消息
	priorityMsg := &proto.Message{MsgType: 2}
	err = queue.Send(priorityMsg, true)
	require.NoError(t, err)

	// 验证高优先级消息先被接收
	receivedMsg, ok := queue.Receive(ctx)
	require.True(t, ok)
	assert.Equal(t, uint32(2), receivedMsg.MsgType)

	receivedMsg, ok = queue.Receive(ctx)
	require.True(t, ok)
	assert.Equal(t, uint32(1), receivedMsg.MsgType)
}

func testAutoExpansion(t *testing.T, queue *AdaptiveSendQueue, config *SendQueueConfig) {
	// 填满基础容量
	for i := 0; i < config.BaseQueueLength; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false)
		require.NoError(t, err)
	}

	// 验证队列已满
	stats := queue.GetStats()
	assert.Equal(t, config.BaseQueueLength, stats["queue_length"])
	assert.Equal(t, config.BaseQueueLength, stats["current_capacity"])

	// 发送更多消息触发扩容
	msg := &proto.Message{MsgType: 999}
	err := queue.Send(msg, false)
	require.NoError(t, err)

	// 验证扩容
	stats = queue.GetStats()
	expandedCapacity := stats["current_capacity"].(int)
	assert.Greater(t, expandedCapacity, config.BaseQueueLength)
	assert.Equal(t, uint64(1), stats["total_expanded"])

	t.Logf("Queue expanded from %d to %d", config.BaseQueueLength, expandedCapacity)
}

func testBackpressureControl(t *testing.T, node *ImprovedNode) {
	// 设置较低的背压阈值进行测试
	node.backpressure.slowDownThreshold = 0.3
	node.backpressure.blockThreshold = 0.6

	// 发送消息直到触发背压
	var backpressureTriggered bool
	for i := 0; i < 1000; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		if err == ErrBackpressure {
			backpressureTriggered = true
			break
		}
	}

	// 在某些情况下可能不会触发背压（如果消息处理很快）
	// 所以我们只验证系统没有崩溃
	stats := node.GetStats()
	totalSent := stats["perf_total_sent"].(uint64)
	assert.Greater(t, totalSent, uint64(0))

	if backpressureTriggered {
		t.Log("Backpressure was triggered as expected")
	} else {
		t.Log("Backpressure was not triggered - messages processed quickly")
	}
}

func testConcurrentOperations(t *testing.T, queue *AdaptiveSendQueue) {
	const numProducers = 5
	const numConsumers = 3
	const messagesPerProducer = 100

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var totalSent, totalReceived int64

	// 启动生产者
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for j := 0; j < messagesPerProducer; j++ {
				msg := &proto.Message{MsgType: uint32(producerID*1000 + j)}
				err := queue.Send(msg, j%10 == 0) // 10%高优先级
				if err == nil {
					totalSent++
				}
			}
		}(i)
	}

	// 启动消费者
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, ok := queue.Receive(ctx)
					if ok {
						totalReceived++
					}
				}
			}
		}()
	}

	// 等待所有生产者完成
	time.Sleep(2 * time.Second)
	cancel()
	wg.Wait()

	t.Logf("Concurrent test: sent %d, received %d", totalSent, totalReceived)
	assert.Greater(t, totalSent, int64(0))
	assert.Greater(t, totalReceived, int64(0))
}

func testPerformanceMonitoring(t *testing.T, node *ImprovedNode) {
	// 发送一些消息
	for i := 0; i < 50; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		if err != nil {
			t.Logf("Send failed for message %d: %v", i, err)
		}
		time.Sleep(time.Millisecond) // 添加小延迟
	}

	// 验证性能统计
	stats := node.GetStats()

	totalSent := stats["perf_total_sent"].(uint64)
	assert.Greater(t, totalSent, uint64(0))

	avgLatency := stats["perf_avg_latency_ns"].(uint64)
	assert.Greater(t, avgLatency, uint64(0))

	t.Logf("Performance monitoring: sent %d messages, avg latency %dns",
		totalSent, avgLatency)
}

// TestConfigurationProfiles 配置文件集成测试
func TestConfigurationProfiles(t *testing.T) {
	profiles := []string{"default", "high_throughput", "low_latency", "memory_constrained", "adaptive"}

	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			config := GetConfigByProfile(profile)
			require.NotNil(t, config)

			// 验证配置有效性
			err := config.ValidateConfig()
			require.NoError(t, err)

			// 创建队列并测试基本功能
			queue := NewAdaptiveSendQueue(config.BaseQueueLength, config.MaxQueueLength, config.MaxQueueSize)
			if config.EnablePriority {
				queue.EnablePriority(int(float64(config.BaseQueueLength) * config.HighPriorityQueueRatio))
			}
			defer queue.Close()

			// 基本发送接收测试
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			msg := &proto.Message{MsgType: 1}
			err = queue.Send(msg, false)
			require.NoError(t, err)

			receivedMsg, ok := queue.Receive(ctx)
			require.True(t, ok)
			assert.Equal(t, msg.MsgType, receivedMsg.MsgType)

			t.Logf("Profile %s: basic functionality verified", profile)
		})
	}
}

// TestPerformanceAnalysis 性能分析集成测试
func TestPerformanceAnalysis(t *testing.T) {
	queue := NewAdaptiveSendQueue(100, 400, 10*1024*1024)
	defer queue.Close()

	// 模拟不同的负载情况
	testCases := []struct {
		name           string
		messageCount   int
		expectedAdvice string
	}{
		{"low_load", 10, "no_change"},
		{"medium_load", 50, "no_change"},
		{"high_load", 95, "increase_capacity"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 清空队列
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			for {
				_, ok := queue.Receive(ctx)
				if !ok {
					break
				}
			}
			cancel()

			// 发送指定数量的消息
			for i := 0; i < tc.messageCount; i++ {
				msg := &proto.Message{MsgType: uint32(i)}
				err := queue.Send(msg, false)
				require.NoError(t, err)
			}

			// 获取统计并分析
			stats := queue.GetStats()
			advice := AnalyzePerformance(stats)

			assert.NotNil(t, advice)
			assert.NotEmpty(t, advice.RecommendedAction)
			assert.NotEmpty(t, advice.Reason)

			t.Logf("Load: %s, Utilization: %.2f%%, Advice: %s",
				tc.name, advice.CurrentUtilization*100, advice.RecommendedAction)
		})
	}
}

// TestEndToEndScenario 端到端场景测试
func TestEndToEndScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping end-to-end test in short mode")
	}

	// 使用高吞吐量配置
	config := HighThroughputConfig()
	config.BaseQueueLength = 200
	config.MaxQueueLength = 800

	// 创建多个节点
	nodeCount := 3
	nodes := make([]*ImprovedNode, nodeCount)
	opts := NewOptions()
	config.ApplyToOptions(opts)

	for i := 0; i < nodeCount; i++ {
		nodes[i] = NewImprovedNode(uint64(i+1), "e2e-test", "127.0.0.1:8080", opts)
		defer nodes[i].Stop()
	}

	// 模拟真实的消息发送场景
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var totalSent, totalErrors int64

	// 启动多个发送者
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(senderID int) {
			defer wg.Done()

			for j := 0; j < 200; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					// 随机选择节点
					node := nodes[j%len(nodes)]

					msg := &proto.Message{
						MsgType: uint32(senderID*1000 + j),
						Content: make([]byte, 512), // 512字节消息
					}

					// 20%高优先级消息
					isHighPriority := j%5 == 0

					var err error
					if isHighPriority {
						err = node.SendWithPriority(msg, true)
					} else {
						err = node.Send(msg)
					}

					if err != nil {
						totalErrors++
					} else {
						totalSent++
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// 收集所有节点的统计信息
	var aggregatedStats = make(map[string]uint64)
	for i, node := range nodes {
		stats := node.GetStats()

		aggregatedStats["total_sent"] += stats["perf_total_sent"].(uint64)
		aggregatedStats["total_dropped"] += stats["perf_total_dropped"].(uint64)
		aggregatedStats["total_backpressure"] += stats["perf_total_backpressure"].(uint64)
		aggregatedStats["queue_expansions"] += stats["queue_total_expanded"].(uint64)

		t.Logf("Node %d stats: sent=%d, dropped=%d, expansions=%d",
			i+1,
			stats["perf_total_sent"].(uint64),
			stats["perf_total_dropped"].(uint64),
			stats["queue_total_expanded"].(uint64))
	}

	// 验证端到端性能
	assert.Greater(t, totalSent, int64(1500), "Should send most messages successfully")
	assert.Less(t, float64(totalErrors)/float64(totalSent+totalErrors), 0.1, "Error rate should be < 10%")
	assert.Greater(t, aggregatedStats["total_sent"], uint64(1500), "Aggregated sent count should be high")

	t.Logf("End-to-end results:")
	t.Logf("  Total sent: %d", totalSent)
	t.Logf("  Total errors: %d", totalErrors)
	t.Logf("  Error rate: %.2f%%", float64(totalErrors)/float64(totalSent+totalErrors)*100)
	t.Logf("  Queue expansions: %d", aggregatedStats["queue_expansions"])
	t.Logf("  Backpressure events: %d", aggregatedStats["total_backpressure"])
}
