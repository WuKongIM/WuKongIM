package cluster

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

func TestAdaptiveSendQueue_BasicOperations(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	defer queue.Close()

	// 测试基本发送
	msg := &proto.Message{MsgType: 1}
	err := queue.Send(msg, false)
	assert.NoError(t, err)

	// 测试接收
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	receivedMsg, ok := queue.Receive(ctx)
	assert.True(t, ok)
	assert.Equal(t, uint32(1), receivedMsg.MsgType)
}

func TestAdaptiveSendQueue_AutoExpansion(t *testing.T) {
	baseCapacity := 5
	maxCapacity := 20
	queue := NewAdaptiveSendQueue(baseCapacity, maxCapacity, 1024*1024)
	defer queue.Close()

	// 填满基础容量
	for i := 0; i < baseCapacity; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false)
		assert.NoError(t, err)
	}

	// 验证队列已满
	stats := queue.GetStats()
	assert.Equal(t, baseCapacity, stats["queue_length"])
	assert.Equal(t, baseCapacity, stats["current_capacity"])

	// 发送更多消息触发扩容
	msg := &proto.Message{MsgType: 100}
	err := queue.Send(msg, false)
	assert.NoError(t, err)

	// 验证扩容
	stats = queue.GetStats()
	expandedCapacity := stats["current_capacity"].(int)
	assert.Greater(t, expandedCapacity, baseCapacity)
	assert.Equal(t, uint64(1), stats["total_expanded"])

	t.Logf("Queue expanded from %d to %d", baseCapacity, expandedCapacity)
}

func TestAdaptiveSendQueue_PriorityQueue(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	queue.EnablePriority(5)
	defer queue.Close()

	// 发送普通消息
	normalMsg := &proto.Message{MsgType: 1}
	err := queue.Send(normalMsg, false)
	assert.NoError(t, err)

	// 发送高优先级消息
	priorityMsg := &proto.Message{MsgType: 2}
	err = queue.Send(priorityMsg, true)
	assert.NoError(t, err)

	// 验证高优先级消息先被接收
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	receivedMsg, ok := queue.Receive(ctx)
	assert.True(t, ok)
	assert.Equal(t, uint32(2), receivedMsg.MsgType) // 高优先级消息

	receivedMsg, ok = queue.Receive(ctx)
	assert.True(t, ok)
	assert.Equal(t, uint32(1), receivedMsg.MsgType) // 普通消息
}

func TestAdaptiveSendQueue_BatchReceive(t *testing.T) {
	queue := NewAdaptiveSendQueue(20, 80, 1024*1024)
	defer queue.Close()

	// 发送多个消息
	messageCount := 10
	for i := 0; i < messageCount; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false)
		assert.NoError(t, err)
	}

	// 批量接收
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msgs, ok := queue.BatchReceive(ctx, 5, 1024)
	assert.True(t, ok)
	assert.Len(t, msgs, 5)

	// 验证消息顺序
	for i, msg := range msgs {
		assert.Equal(t, uint32(i), msg.MsgType)
	}
}

func TestAdaptiveSendQueue_RateLimiting(t *testing.T) {
	maxSize := uint64(50) // 很小的限制
	queue := NewAdaptiveSendQueue(10, 40, maxSize)
	defer queue.Close()

	// 先发送一些小消息来接近限制
	var successCount int
	for i := 0; i < 5; i++ {
		smallMsg := &proto.Message{
			MsgType: uint32(i),
			Content: make([]byte, 10), // 10字节 + 头部 ≈ 22字节
		}
		err := queue.Send(smallMsg, false)
		if err == nil {
			successCount++
		} else if err == ErrRateLimited {
			// 限流被触发，这是预期的
			t.Logf("Rate limiting triggered after %d successful sends", successCount)
			assert.Equal(t, ErrRateLimited, err)
			return
		} else {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// 如果到这里还没有触发限流，发送一个大消息
	largeMsg := &proto.Message{
		MsgType: 999,
		Content: make([]byte, 50), // 这个肯定会超过限制
	}

	err := queue.Send(largeMsg, false)
	assert.Error(t, err)
	assert.Equal(t, ErrRateLimited, err)
}

func TestAdaptiveSendQueue_QueueFull(t *testing.T) {
	queue := NewAdaptiveSendQueue(2, 2, 1024*1024) // 最大容量也是2
	defer queue.Close()

	// 填满队列
	for i := 0; i < 2; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false)
		assert.NoError(t, err)
	}

	// 再发送应该失败
	msg := &proto.Message{MsgType: 100}
	err := queue.Send(msg, false)
	assert.Error(t, err)
	assert.Equal(t, ErrChanIsFull, err)

	// 验证统计
	stats := queue.GetStats()
	assert.Equal(t, uint64(1), stats["total_dropped"])
}

func TestAdaptiveSendQueue_Shrinking(t *testing.T) {
	queue := NewAdaptiveSendQueue(5, 20, 1024*1024)
	defer queue.Close()

	// 先扩容
	for i := 0; i < 10; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false)
		assert.NoError(t, err)
	}

	// 验证扩容
	stats := queue.GetStats()
	expandedCapacity := stats["current_capacity"].(int)
	assert.Greater(t, expandedCapacity, 5)

	// 消费大部分消息
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for i := 0; i < 9; i++ {
		_, ok := queue.Receive(ctx)
		assert.True(t, ok)
	}

	// 检查是否应该缩容
	shouldShrink := queue.ShouldShrink()
	assert.True(t, shouldShrink)

	// 执行缩容
	queue.Shrink()

	// 验证缩容
	stats = queue.GetStats()
	newCapacity := stats["current_capacity"].(int)
	assert.Less(t, newCapacity, expandedCapacity)
}

func TestAdaptiveSendQueue_ConcurrentAccess(t *testing.T) {
	// 使用大容量避免扩容导致的并发问题
	queue := NewAdaptiveSendQueue(1000, 1000, 10*1024*1024)

	const numProducers = 5
	const numConsumers = 3
	const messagesPerProducer = 50

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	// 启动生产者
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			for j := 0; j < messagesPerProducer; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					msg := &proto.Message{
						MsgType: uint32(producerID*1000 + j),
					}
					err := queue.Send(msg, j%10 == 0) // 10%高优先级
					if err != nil && err != ErrChanIsFull && err != ErrRateLimited {
						t.Errorf("Producer %d failed to send message %d: %v", producerID, j, err)
					}
				}
			}
		}(i)
	}

	// 启动消费者
	receivedCount := make([]int, numConsumers)
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func(consumerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					msg, ok := queue.Receive(ctx)
					if ok && msg != nil {
						receivedCount[consumerID]++
					}
				}
			}
		}(i)
	}

	// 等待一段时间让生产者和消费者工作
	time.Sleep(1 * time.Second)
	cancel() // 停止所有goroutine

	wg.Wait()
	queue.Close() // 现在安全关闭队列

	// 验证统计
	stats := queue.GetStats()
	totalSent := stats["total_sent"].(uint64)
	totalDropped := stats["total_dropped"].(uint64)

	t.Logf("Total sent: %d, Total dropped: %d", totalSent, totalDropped)

	// 验证至少发送了一些消息
	assert.Greater(t, totalSent, uint64(0))

	// 验证消费者接收了消息
	totalReceived := 0
	for i, count := range receivedCount {
		t.Logf("Consumer %d received %d messages", i, count)
		totalReceived += count
	}
	assert.Greater(t, totalReceived, 0)
}

func TestAdaptiveSendQueue_Stats(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	queue.EnablePriority(5)
	defer queue.Close()

	// 发送一些普通消息（不使用优先级队列）
	for i := 0; i < 2; i++ {
		msg := &proto.Message{MsgType: uint32(i)}
		err := queue.Send(msg, false) // 全部发送到普通队列
		assert.NoError(t, err)
	}

	stats := queue.GetStats()

	// 验证基本统计
	assert.Equal(t, 10, stats["base_capacity"])
	assert.Equal(t, 40, stats["max_capacity"])
	assert.Equal(t, 10, stats["current_capacity"])
	assert.Equal(t, uint64(2), stats["total_sent"])
	assert.Equal(t, uint64(0), stats["total_dropped"])
	assert.Equal(t, true, stats["priority_enabled"])

	// 验证利用率
	utilization := queue.GetUtilization()
	expectedUtilization := float64(2) / float64(10) * 100 // 2个普通消息
	assert.InDelta(t, expectedUtilization, utilization, 1.0)
}

// 基准测试
func BenchmarkAdaptiveSendQueue_Send(b *testing.B) {
	queue := NewAdaptiveSendQueue(1000, 4000, 100*1024*1024)
	defer queue.Close()

	msg := &proto.Message{MsgType: 1}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = queue.Send(msg, false)
		}
	})
}

func BenchmarkAdaptiveSendQueue_Receive(b *testing.B) {
	queue := NewAdaptiveSendQueue(1000, 4000, 100*1024*1024)
	defer queue.Close()

	// 预填充队列
	msg := &proto.Message{MsgType: 1}
	for i := 0; i < 500; i++ {
		_ = queue.Send(msg, false)
	}

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = queue.Receive(ctx)
		}
	})
}

func BenchmarkAdaptiveSendQueue_BatchReceive(b *testing.B) {
	queue := NewAdaptiveSendQueue(1000, 4000, 100*1024*1024)
	defer queue.Close()

	// 预填充队列
	msg := &proto.Message{MsgType: 1}
	for i := 0; i < 500; i++ {
		_ = queue.Send(msg, false)
	}

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = queue.BatchReceive(ctx, 10, 1024)
	}
}
