package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

// TestProcessBatchLatency 测试 processBatch 的延迟问题
func TestProcessBatchLatency(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 100
	opts.MaxSendQueueSize = 1024 * 1024

	node := NewImprovedNode(1, "latency-test", "127.0.0.1:8080", opts)
	defer node.Stop()

	// 启用测试模式，模拟消息发送成功
	node.SetTestMode(true)

	// 启动消息处理
	node.Start()

	// 测试单个消息的处理延迟
	t.Run("SingleMessageLatency", func(t *testing.T) {
		start := time.Now()

		msg := &proto.Message{MsgType: 1}
		err := node.Send(msg)
		assert.NoError(t, err)

		// 等待消息被处理（通过检查统计信息）
		timeout := time.After(time.Second * 2)
		ticker := time.NewTicker(time.Millisecond * 10)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				t.Fatal("Message processing timeout")
			case <-ticker.C:
				stats := node.GetStats()
				if stats["perf_total_sent"].(uint64) > 0 {
					latency := time.Since(start)
					t.Logf("Single message processing latency: %v", latency)

					// 验证延迟应该在合理范围内（< 200ms）
					assert.Less(t, latency, time.Millisecond*200,
						"Message processing should be fast")
					return
				}
			}
		}
	})

	// 测试批量消息的处理延迟
	t.Run("BatchMessageLatency", func(t *testing.T) {
		messageCount := 10
		start := time.Now()

		// 发送多个消息
		for i := 0; i < messageCount; i++ {
			msg := &proto.Message{MsgType: uint32(i + 100)}
			err := node.Send(msg)
			assert.NoError(t, err)
		}

		// 等待所有消息被处理
		timeout := time.After(time.Second * 3)
		ticker := time.NewTicker(time.Millisecond * 10)
		defer ticker.Stop()

		initialSent := node.GetStats()["perf_total_sent"].(uint64)
		targetSent := initialSent + uint64(messageCount)

		for {
			select {
			case <-timeout:
				t.Fatal("Batch message processing timeout")
			case <-ticker.C:
				stats := node.GetStats()
				currentSent := stats["perf_total_sent"].(uint64)
				if currentSent >= targetSent {
					latency := time.Since(start)
					t.Logf("Batch message processing latency: %v for %d messages",
						latency, messageCount)

					// 验证批量处理延迟应该在合理范围内（< 500ms）
					assert.Less(t, latency, time.Millisecond*500,
						"Batch message processing should be fast")
					return
				}
			}
		}
	})

	// 测试连续消息的处理延迟
	t.Run("ContinuousMessageLatency", func(t *testing.T) {
		messageCount := 50
		latencies := make([]time.Duration, messageCount)

		for i := 0; i < messageCount; i++ {
			start := time.Now()

			msg := &proto.Message{MsgType: uint32(i + 200)}
			err := node.Send(msg)
			assert.NoError(t, err)

			// 等待这个消息被处理
			timeout := time.After(time.Second)
			ticker := time.NewTicker(time.Millisecond * 5)

			initialSent := node.GetStats()["perf_total_sent"].(uint64)

		waitLoop:
			for {
				select {
				case <-timeout:
					t.Fatalf("Message %d processing timeout", i)
				case <-ticker.C:
					stats := node.GetStats()
					currentSent := stats["perf_total_sent"].(uint64)
					if currentSent > initialSent {
						latencies[i] = time.Since(start)
						break waitLoop
					}
				}
			}
			ticker.Stop()
		}

		// 分析延迟分布
		var totalLatency time.Duration
		var maxLatency time.Duration
		var minLatency time.Duration = time.Hour // 初始化为一个大值

		for i, latency := range latencies {
			totalLatency += latency
			if latency > maxLatency {
				maxLatency = latency
			}
			if latency < minLatency {
				minLatency = latency
			}

			t.Logf("Message %d latency: %v", i, latency)
		}

		avgLatency := totalLatency / time.Duration(messageCount)

		t.Logf("Latency statistics:")
		t.Logf("  Average: %v", avgLatency)
		t.Logf("  Min: %v", minLatency)
		t.Logf("  Max: %v", maxLatency)

		// 验证平均延迟应该很低
		assert.Less(t, avgLatency, time.Millisecond*100,
			"Average latency should be low")

		// 验证最大延迟不应该太高
		assert.Less(t, maxLatency, time.Millisecond*500,
			"Max latency should be reasonable")
	})
}

// TestAdaptiveQueueNotification 测试自适应队列的通知机制
func TestAdaptiveQueueNotification(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	defer queue.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	// 测试消息通知
	t.Run("MessageNotification", func(t *testing.T) {
		// 启动一个goroutine等待通知
		notified := make(chan bool, 1)
		go func() {
			if queue.WaitForMessage(ctx, time.Millisecond*500) {
				notified <- true
			} else {
				notified <- false
			}
		}()

		// 短暂延迟后发送消息
		time.Sleep(time.Millisecond * 50)
		msg := &proto.Message{MsgType: 1}
		err := queue.Send(msg, false)
		assert.NoError(t, err)

		// 验证通知被接收
		select {
		case result := <-notified:
			assert.True(t, result, "Should receive message notification")
		case <-time.After(time.Second):
			t.Fatal("Notification timeout")
		}
	})

	// 测试优先级消息通知
	t.Run("PriorityMessageNotification", func(t *testing.T) {
		queue.EnablePriority(5)

		// 启动一个goroutine等待通知
		notified := make(chan bool, 1)
		go func() {
			if queue.WaitForMessage(ctx, time.Millisecond*500) {
				notified <- true
			} else {
				notified <- false
			}
		}()

		// 短暂延迟后发送优先级消息
		time.Sleep(time.Millisecond * 50)
		msg := &proto.Message{MsgType: 2}
		err := queue.Send(msg, true) // 高优先级
		assert.NoError(t, err)

		// 验证通知被接收
		select {
		case result := <-notified:
			assert.True(t, result, "Should receive priority message notification")
		case <-time.After(time.Second):
			t.Fatal("Priority notification timeout")
		}
	})
}

// TestBatchReceiveTimeout 测试批量接收的超时机制
func TestBatchReceiveTimeout(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	defer queue.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	// 测试空队列的超时
	t.Run("EmptyQueueTimeout", func(t *testing.T) {
		start := time.Now()
		msgs, ok := queue.BatchReceive(ctx, 10, 1024)
		duration := time.Since(start)

		assert.False(t, ok, "Should timeout on empty queue")
		assert.Nil(t, msgs, "Should return nil messages")
		assert.Greater(t, duration, time.Millisecond*90, "Should wait for timeout")
		assert.Less(t, duration, time.Millisecond*150, "Should not wait too long")
	})

	// 测试部分消息的超时
	t.Run("PartialBatchTimeout", func(t *testing.T) {
		// 先发送一个消息
		msg := &proto.Message{MsgType: 1}
		err := queue.Send(msg, false)
		assert.NoError(t, err)

		start := time.Now()
		msgs, ok := queue.BatchReceive(ctx, 10, 1024) // 期望10个消息
		duration := time.Since(start)

		assert.True(t, ok, "Should return partial batch")
		assert.Len(t, msgs, 1, "Should return 1 message")

		// 修正期望：当队列中有消息时，BatchReceive 会立即返回，
		// 只有在等待第一个消息时才会有超时延迟
		t.Logf("Partial batch duration: %v", duration)
		assert.Less(t, duration, time.Millisecond*50, "Should return quickly when messages are available")
	})
}

// BenchmarkMessageProcessingLatency 基准测试消息处理延迟
func BenchmarkMessageProcessingLatency(b *testing.B) {
	opts := NewOptions()
	opts.SendQueueLength = 1000
	opts.MaxSendQueueSize = 10 * 1024 * 1024

	node := NewImprovedNode(1, "benchmark", "127.0.0.1:8080", opts)
	defer node.Stop()
	node.SetTestMode(true) // 启用测试模式
	node.Start()

	// 预热
	for i := 0; i < 100; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		_ = node.Send(msg)
	}
	time.Sleep(time.Millisecond * 100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			msg := &proto.Message{MsgType: 1}
			_ = node.Send(msg)
		}
	})
}
