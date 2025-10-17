package cluster

import (
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

func TestImprovedNode_BasicSend(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 10
	opts.MaxSendQueueSize = 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	msg := &proto.Message{MsgType: 1}
	err := node.Send(msg)
	assert.NoError(t, err)

	// 验证统计
	stats := node.GetStats()
	assert.Equal(t, uint64(1), stats["perf_total_sent"])
}

func TestImprovedNode_PrioritySend(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 10
	opts.MaxSendQueueSize = 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 发送普通消息
	normalMsg := &proto.Message{MsgType: 1}
	err := node.Send(normalMsg)
	assert.NoError(t, err)

	// 发送高优先级消息
	priorityMsg := &proto.Message{MsgType: 2}
	err = node.SendWithPriority(priorityMsg, true)
	assert.NoError(t, err)

	stats := node.GetStats()
	assert.Equal(t, uint64(2), stats["perf_total_sent"])
}

func TestImprovedNode_SendStrategy(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 100
	opts.MaxSendQueueSize = 10 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 测试不同发送策略
	strategies := []SendStrategy{
		SendStrategyDefault,
		SendStrategyBatch,
		SendStrategyLatency,
		SendStrategyThroughput,
	}

	for _, strategy := range strategies {
		node.SetSendStrategy(strategy)

		// 发送一些消息
		for i := 0; i < 10; i++ {
			msg := &proto.Message{MsgType: uint32(i)}
			err := node.Send(msg)
			assert.NoError(t, err)
		}
	}

	stats := node.GetStats()
	assert.Equal(t, uint64(40), stats["perf_total_sent"]) // 4 strategies * 10 messages
}

func TestImprovedNode_Backpressure(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 5 // 很小的队列
	opts.MaxSendQueueSize = 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 不启动消息处理，让队列积压
	// node.Start() // 不调用Start，消息不会被处理

	// 发送消息直到触发背压
	var backpressureTriggered bool
	for i := 0; i < 50; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		if err == ErrBackpressure || err == ErrChanIsFull {
			backpressureTriggered = true
			break
		}
	}

	assert.True(t, backpressureTriggered, "Backpressure should be triggered")

	stats := node.GetStats()
	backpressureEvents := stats["perf_total_backpressure"].(uint64)
	assert.Greater(t, backpressureEvents, uint64(0))
}

func TestImprovedNode_QueueExpansion(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 5 // 小基础容量
	opts.MaxSendQueueSize = 10 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 发送足够多的消息触发扩容
	for i := 0; i < 20; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		if err != nil && err != ErrChanIsFull {
			t.Logf("Send failed at message %d: %v", i, err)
		}
	}

	stats := node.GetStats()

	// 验证队列扩容
	currentCapacity := stats["queue_current_capacity"].(int)
	baseCapacity := stats["queue_base_capacity"].(int)
	assert.Greater(t, currentCapacity, baseCapacity, "Queue should have expanded")

	totalExpanded := stats["queue_total_expanded"].(uint64)
	assert.Greater(t, totalExpanded, uint64(0), "Should have expansion events")

	t.Logf("Queue expanded from %d to %d (%d expansions)",
		baseCapacity, currentCapacity, totalExpanded)
}

func TestImprovedNode_PerformanceMonitoring(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 100
	opts.MaxSendQueueSize = 10 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 发送一些消息
	messageCount := 50
	for i := 0; i < messageCount; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		assert.NoError(t, err)

		// 添加一些延迟来测试延迟统计
		time.Sleep(time.Microsecond * 100)
	}

	stats := node.GetStats()

	// 验证性能统计
	totalSent := stats["perf_total_sent"].(uint64)
	assert.Equal(t, uint64(messageCount), totalSent)

	avgLatency := stats["perf_avg_latency_ns"].(uint64)
	assert.Greater(t, avgLatency, uint64(0))

	maxLatency := stats["perf_max_latency_ns"].(uint64)
	assert.GreaterOrEqual(t, maxLatency, avgLatency)

	t.Logf("Performance stats - Sent: %d, Avg Latency: %dns, Max Latency: %dns",
		totalSent, avgLatency, maxLatency)
}

func TestImprovedNode_ConcurrentSend(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 1000
	opts.MaxSendQueueSize = 100 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	const numGoroutines = 10
	const messagesPerGoroutine = 100

	var wg sync.WaitGroup
	var successCount, errorCount int64

	// 启动多个goroutine并发发送
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for j := 0; j < messagesPerGoroutine; j++ {
				msg := &proto.Message{
					MsgType: uint32(goroutineID*1000 + j),
				}

				err := node.SendWithPriority(msg, j%10 == 0) // 10%高优先级
				if err != nil {
					errorCount++
				} else {
					successCount++
				}
			}
		}(i)
	}

	wg.Wait()

	stats := node.GetStats()
	totalSent := stats["perf_total_sent"].(uint64)
	totalDropped := stats["perf_total_dropped"].(uint64)

	t.Logf("Concurrent send results:")
	t.Logf("  Success: %d, Errors: %d", successCount, errorCount)
	t.Logf("  Total sent: %d, Total dropped: %d", totalSent, totalDropped)
	t.Logf("  Queue capacity: %d",
		stats["queue_current_capacity"].(int))

	// 验证大部分消息成功发送
	assert.Greater(t, totalSent, uint64(numGoroutines*messagesPerGoroutine/2))
}

func TestImprovedNode_BackpressureStrategies(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 10
	opts.MaxSendQueueSize = 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 测试背压阈值
	node.backpressure.slowDownThreshold = 0.5 // 50%时减速
	node.backpressure.blockThreshold = 0.8    // 80%时阻塞

	// 填充队列到50%
	for i := 0; i < 5; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := node.Send(msg)
		assert.NoError(t, err)
	}

	// 此时应该开始减速但不阻塞
	start := time.Now()
	msg := &proto.Message{MsgType: 100}
	err := node.Send(msg)
	duration := time.Since(start)

	// 应该有轻微延迟（减速策略）
	assert.NoError(t, err)
	assert.Greater(t, duration, time.Millisecond*5) // 至少有一些延迟

	stats := node.GetStats()
	backpressureEvents := stats["perf_total_backpressure"].(uint64)
	assert.Greater(t, backpressureEvents, uint64(0))
}

// 基准测试
func BenchmarkImprovedNode_Send(b *testing.B) {
	opts := NewOptions()
	opts.SendQueueLength = 10000
	opts.MaxSendQueueSize = 100 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	msg := &proto.Message{MsgType: 1}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = node.Send(msg)
		}
	})
}

func BenchmarkImprovedNode_SendWithPriority(b *testing.B) {
	opts := NewOptions()
	opts.SendQueueLength = 10000
	opts.MaxSendQueueSize = 100 * 1024 * 1024

	node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
	defer node.Stop()

	msg := &proto.Message{MsgType: 1}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = node.SendWithPriority(msg, true)
		}
	})
}

func BenchmarkImprovedNode_SendStrategies(b *testing.B) {
	opts := NewOptions()
	opts.SendQueueLength = 10000
	opts.MaxSendQueueSize = 100 * 1024 * 1024

	strategies := []struct {
		name     string
		strategy SendStrategy
	}{
		{"Default", SendStrategyDefault},
		{"Batch", SendStrategyBatch},
		{"Latency", SendStrategyLatency},
		{"Throughput", SendStrategyThroughput},
	}

	for _, s := range strategies {
		b.Run(s.name, func(b *testing.B) {
			node := NewImprovedNode(1, "test-uid", "127.0.0.1:8080", opts)
			node.SetSendStrategy(s.strategy)
			defer node.Stop()

			msg := &proto.Message{MsgType: 1}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = node.Send(msg)
			}
		})
	}
}
