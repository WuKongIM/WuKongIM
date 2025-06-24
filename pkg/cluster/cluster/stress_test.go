package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

// StressTestConfig 压力测试配置
type StressTestConfig struct {
	Duration        time.Duration
	ProducerCount   int
	ConsumerCount   int
	MessageRate     int     // 每秒消息数
	MessageSize     int     // 消息大小（字节）
	PriorityRatio   float64 // 高优先级消息比例
	BurstEnabled    bool
	BurstInterval   time.Duration
	BurstMultiplier int
}

// StressTestResult 压力测试结果
type StressTestResult struct {
	Duration              time.Duration
	TotalMessagesSent     uint64
	TotalMessagesReceived uint64
	TotalMessagesDropped  uint64
	MessageRate           float64 // 消息/秒
	ThroughputMBps        float64 // MB/秒
	AvgLatencyMs          float64
	MaxLatencyMs          float64
	P95LatencyMs          float64
	QueueExpansions       uint64
	BackpressureEvents    uint64
	ErrorRate             float64
	MemoryUsageMB         float64
}

// TestAdaptiveQueue_HighThroughputStress 高吞吐量压力测试
func TestAdaptiveQueue_HighThroughputStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	config := StressTestConfig{
		Duration:        30 * time.Second,
		ProducerCount:   20,
		ConsumerCount:   10,
		MessageRate:     10000, // 10K msg/s
		MessageSize:     1024,  // 1KB
		PriorityRatio:   0.1,   // 10% 高优先级
		BurstEnabled:    true,
		BurstInterval:   5 * time.Second,
		BurstMultiplier: 3,
	}

	result := runAdaptiveQueueStressTest(t, config)

	// 验证性能指标
	assert.Greater(t, result.MessageRate, float64(8000), "Message rate should be > 8K/s")
	assert.Less(t, result.ErrorRate, 0.05, "Error rate should be < 5%")
	assert.Greater(t, result.ThroughputMBps, float64(8), "Throughput should be > 8 MB/s")

	t.Logf("High Throughput Stress Test Results:")
	logStressTestResult(t, result)
}

// TestAdaptiveQueue_LowLatencyStress 低延迟压力测试
func TestAdaptiveQueue_LowLatencyStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	config := StressTestConfig{
		Duration:      20 * time.Second,
		ProducerCount: 5,
		ConsumerCount: 5,
		MessageRate:   5000, // 5K msg/s
		MessageSize:   256,  // 256B
		PriorityRatio: 0.2,  // 20% 高优先级
		BurstEnabled:  false,
	}

	result := runAdaptiveQueueStressTest(t, config)

	// 验证延迟指标
	assert.Less(t, result.AvgLatencyMs, float64(10), "Average latency should be < 10ms")
	assert.Less(t, result.P95LatencyMs, float64(50), "P95 latency should be < 50ms")

	t.Logf("Low Latency Stress Test Results:")
	logStressTestResult(t, result)
}

// TestAdaptiveQueue_MemoryConstrainedStress 内存受限压力测试
func TestAdaptiveQueue_MemoryConstrainedStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	config := StressTestConfig{
		Duration:        15 * time.Second,
		ProducerCount:   10,
		ConsumerCount:   3, // 故意设置较少消费者
		MessageRate:     8000,
		MessageSize:     2048, // 2KB
		PriorityRatio:   0.05,
		BurstEnabled:    true,
		BurstInterval:   3 * time.Second,
		BurstMultiplier: 5,
	}

	// 使用内存受限配置
	queue := NewAdaptiveSendQueue(1000, 4000, 50*1024*1024) // 50MB限制
	queue.EnablePriority(100)
	defer queue.Close()

	result := runQueueStressTest(t, queue, config)

	// 验证内存使用
	assert.Less(t, result.MemoryUsageMB, float64(60), "Memory usage should be < 60MB")
	assert.Greater(t, result.QueueExpansions, uint64(0), "Should have queue expansions")

	t.Logf("Memory Constrained Stress Test Results:")
	logStressTestResult(t, result)
}

// TestImprovedNode_ConcurrentStress 并发节点压力测试
func TestImprovedNode_ConcurrentStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	opts := NewOptions()
	opts.SendQueueLength = 5000
	opts.MaxSendQueueSize = 100 * 1024 * 1024 // 100MB

	nodeCount := 5
	nodes := make([]*ImprovedNode, nodeCount)

	// 创建多个节点
	for i := 0; i < nodeCount; i++ {
		nodes[i] = NewImprovedNode(uint64(i+1), fmt.Sprintf("node-%d", i),
			fmt.Sprintf("127.0.0.1:%d", 8080+i), opts)
		defer nodes[i].Stop()
	}

	config := StressTestConfig{
		Duration:        20 * time.Second,
		ProducerCount:   15,
		ConsumerCount:   0, // 节点内部处理
		MessageRate:     12000,
		MessageSize:     512,
		PriorityRatio:   0.15,
		BurstEnabled:    true,
		BurstInterval:   4 * time.Second,
		BurstMultiplier: 2,
	}

	result := runNodeStressTest(t, nodes, config)

	// 验证多节点性能
	assert.Greater(t, result.MessageRate, float64(10000), "Multi-node rate should be > 10K/s")
	assert.Less(t, result.ErrorRate, 0.1, "Multi-node error rate should be < 10%")

	t.Logf("Concurrent Node Stress Test Results:")
	logStressTestResult(t, result)
}

// TestAdaptiveQueue_BackpressureStress 背压机制压力测试
func TestAdaptiveQueue_BackpressureStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// 创建容易触发背压的配置
	queue := NewAdaptiveSendQueue(100, 400, 10*1024*1024)
	queue.EnablePriority(20)
	defer queue.Close()

	config := StressTestConfig{
		Duration:        10 * time.Second,
		ProducerCount:   20, // 大量生产者
		ConsumerCount:   2,  // 少量消费者
		MessageRate:     15000,
		MessageSize:     1024,
		PriorityRatio:   0.3,
		BurstEnabled:    true,
		BurstInterval:   2 * time.Second,
		BurstMultiplier: 10, // 大突发
	}

	result := runQueueStressTest(t, queue, config)

	// 验证背压机制工作
	assert.Greater(t, result.BackpressureEvents, uint64(0), "Should have backpressure events")
	assert.Greater(t, result.QueueExpansions, uint64(0), "Should have queue expansions")

	// 即使有背压，也应该处理大部分消息
	successRate := float64(result.TotalMessagesReceived) / float64(result.TotalMessagesSent)
	assert.Greater(t, successRate, 0.7, "Success rate should be > 70% even with backpressure")

	t.Logf("Backpressure Stress Test Results:")
	logStressTestResult(t, result)
}

// runAdaptiveQueueStressTest 运行自适应队列压力测试
func runAdaptiveQueueStressTest(t *testing.T, config StressTestConfig) *StressTestResult {
	// 根据配置创建队列
	baseCapacity := config.ProducerCount * 100
	maxCapacity := baseCapacity * 4
	maxSize := uint64(config.MessageSize * config.MessageRate * 10) // 10秒缓冲

	queue := NewAdaptiveSendQueue(baseCapacity, maxCapacity, maxSize)
	queue.EnablePriority(baseCapacity / 10)
	defer queue.Close()

	return runQueueStressTest(t, queue, config)
}

// runQueueStressTest 运行队列压力测试
func runQueueStressTest(t *testing.T, queue *AdaptiveSendQueue, config StressTestConfig) *StressTestResult {
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalSent, totalReceived, totalDropped uint64
	var totalLatency, maxLatency uint64
	latencies := make([]time.Duration, 0, 10000)
	var latencyMutex sync.Mutex

	startTime := time.Now()

	// 启动生产者
	for i := 0; i < config.ProducerCount; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()

			messageInterval := time.Second / time.Duration(config.MessageRate/config.ProducerCount)
			ticker := time.NewTicker(messageInterval)
			defer ticker.Stop()

			burstTicker := time.NewTicker(config.BurstInterval)
			defer burstTicker.Stop()

			messageData := make([]byte, config.MessageSize)
			rand.Read(messageData)

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					sendMessage(queue, producerID, messageData, config.PriorityRatio,
						&totalSent, &totalDropped, &totalLatency, &maxLatency, &latencies, &latencyMutex)
				case <-burstTicker.C:
					if config.BurstEnabled {
						// 突发发送
						for j := 0; j < config.BurstMultiplier; j++ {
							sendMessage(queue, producerID, messageData, config.PriorityRatio,
								&totalSent, &totalDropped, &totalLatency, &maxLatency, &latencies, &latencyMutex)
						}
					}
				}
			}
		}(i)
	}

	// 启动消费者
	for i := 0; i < config.ConsumerCount; i++ {
		wg.Add(1)
		go func(consumerID int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, ok := queue.Receive(ctx)
					if ok {
						atomic.AddUint64(&totalReceived, 1)
					}
				}
			}
		}(i)
	}

	// 等待测试完成
	<-ctx.Done()
	wg.Wait()

	duration := time.Since(startTime)
	stats := queue.GetStats()

	// 计算结果
	result := &StressTestResult{
		Duration:              duration,
		TotalMessagesSent:     atomic.LoadUint64(&totalSent),
		TotalMessagesReceived: atomic.LoadUint64(&totalReceived),
		TotalMessagesDropped:  atomic.LoadUint64(&totalDropped),
		QueueExpansions:       stats["total_expanded"].(uint64),
	}

	if duration.Seconds() > 0 {
		result.MessageRate = float64(result.TotalMessagesSent) / duration.Seconds()
		result.ThroughputMBps = float64(result.TotalMessagesSent*uint64(config.MessageSize)) /
			(1024 * 1024 * duration.Seconds())
	}

	if result.TotalMessagesSent > 0 {
		result.ErrorRate = float64(result.TotalMessagesDropped) / float64(result.TotalMessagesSent)
	}

	// 计算延迟统计
	latencyMutex.Lock()
	if len(latencies) > 0 {
		var totalLatencyNs int64
		var maxLatencyNs int64

		for _, lat := range latencies {
			ns := lat.Nanoseconds()
			totalLatencyNs += ns
			if ns > maxLatencyNs {
				maxLatencyNs = ns
			}
		}

		result.AvgLatencyMs = float64(totalLatencyNs) / float64(len(latencies)) / 1e6
		result.MaxLatencyMs = float64(maxLatencyNs) / 1e6

		// 计算P95延迟
		if len(latencies) >= 20 {
			// 简单的P95计算
			p95Index := int(float64(len(latencies)) * 0.95)
			result.P95LatencyMs = float64(latencies[p95Index].Nanoseconds()) / 1e6
		}
	}
	latencyMutex.Unlock()

	// 获取内存使用
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	result.MemoryUsageMB = float64(memStats.Alloc) / 1024 / 1024

	return result
}

// sendMessage 发送单个消息
func sendMessage(queue *AdaptiveSendQueue, producerID int, messageData []byte, priorityRatio float64,
	totalSent, totalDropped, totalLatency, maxLatency *uint64,
	latencies *[]time.Duration, latencyMutex *sync.Mutex) {

	msg := &proto.Message{
		MsgType: uint32(producerID),
		Content: messageData,
	}

	isHighPriority := rand.Float64() < priorityRatio

	start := time.Now()
	err := queue.Send(msg, isHighPriority)
	latency := time.Since(start)

	if err != nil {
		atomic.AddUint64(totalDropped, 1)
	} else {
		atomic.AddUint64(totalSent, 1)

		// 记录延迟
		latencyNs := uint64(latency.Nanoseconds())
		atomic.AddUint64(totalLatency, latencyNs)

		currentMax := atomic.LoadUint64(maxLatency)
		for latencyNs > currentMax {
			if atomic.CompareAndSwapUint64(maxLatency, currentMax, latencyNs) {
				break
			}
			currentMax = atomic.LoadUint64(maxLatency)
		}

		// 采样延迟数据
		if rand.Intn(100) < 10 { // 10%采样率
			latencyMutex.Lock()
			*latencies = append(*latencies, latency)
			latencyMutex.Unlock()
		}
	}
}

// runNodeStressTest 运行节点压力测试
func runNodeStressTest(t *testing.T, nodes []*ImprovedNode, config StressTestConfig) *StressTestResult {
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	var wg sync.WaitGroup
	var totalSent, totalDropped uint64

	startTime := time.Now()

	// 启动生产者
	for i := 0; i < config.ProducerCount; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()

			messageInterval := time.Second / time.Duration(config.MessageRate/config.ProducerCount)
			ticker := time.NewTicker(messageInterval)
			defer ticker.Stop()

			messageData := make([]byte, config.MessageSize)
			rand.Read(messageData)

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// 随机选择节点
					node := nodes[rand.Intn(len(nodes))]

					msg := &proto.Message{
						MsgType: uint32(producerID),
						Content: messageData,
					}

					isHighPriority := rand.Float64() < config.PriorityRatio

					var err error
					if isHighPriority {
						err = node.SendWithPriority(msg, true)
					} else {
						err = node.Send(msg)
					}

					if err != nil {
						atomic.AddUint64(&totalDropped, 1)
					} else {
						atomic.AddUint64(&totalSent, 1)
					}
				}
			}
		}(i)
	}

	// 等待测试完成
	<-ctx.Done()
	wg.Wait()

	duration := time.Since(startTime)

	// 聚合所有节点的统计
	var totalNodeSent, totalNodeDropped, totalExpansions, totalBackpressure uint64
	for _, node := range nodes {
		stats := node.GetStats()
		totalNodeSent += stats["perf_total_sent"].(uint64)
		totalNodeDropped += stats["perf_total_dropped"].(uint64)
		totalExpansions += stats["queue_total_expanded"].(uint64)
		totalBackpressure += stats["perf_total_backpressure"].(uint64)
	}

	result := &StressTestResult{
		Duration:             duration,
		TotalMessagesSent:    atomic.LoadUint64(&totalSent),
		TotalMessagesDropped: atomic.LoadUint64(&totalDropped),
		QueueExpansions:      totalExpansions,
		BackpressureEvents:   totalBackpressure,
	}

	if duration.Seconds() > 0 {
		result.MessageRate = float64(result.TotalMessagesSent) / duration.Seconds()
		result.ThroughputMBps = float64(result.TotalMessagesSent*uint64(config.MessageSize)) /
			(1024 * 1024 * duration.Seconds())
	}

	if result.TotalMessagesSent > 0 {
		result.ErrorRate = float64(result.TotalMessagesDropped) / float64(result.TotalMessagesSent)
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	result.MemoryUsageMB = float64(memStats.Alloc) / 1024 / 1024

	return result
}

// logStressTestResult 输出压力测试结果
func logStressTestResult(t *testing.T, result *StressTestResult) {
	t.Logf("  Duration: %v", result.Duration)
	t.Logf("  Messages Sent: %d", result.TotalMessagesSent)
	t.Logf("  Messages Received: %d", result.TotalMessagesReceived)
	t.Logf("  Messages Dropped: %d", result.TotalMessagesDropped)
	t.Logf("  Message Rate: %.2f msg/s", result.MessageRate)
	t.Logf("  Throughput: %.2f MB/s", result.ThroughputMBps)
	t.Logf("  Error Rate: %.2f%%", result.ErrorRate*100)
	t.Logf("  Avg Latency: %.2f ms", result.AvgLatencyMs)
	t.Logf("  Max Latency: %.2f ms", result.MaxLatencyMs)
	t.Logf("  P95 Latency: %.2f ms", result.P95LatencyMs)
	t.Logf("  Queue Expansions: %d", result.QueueExpansions)
	t.Logf("  Backpressure Events: %d", result.BackpressureEvents)
	t.Logf("  Memory Usage: %.2f MB", result.MemoryUsageMB)
}
