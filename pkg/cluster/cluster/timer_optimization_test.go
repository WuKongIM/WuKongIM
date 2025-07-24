package cluster

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
)

// TestTimerPoolPerformance 测试 Timer 池的性能
func TestTimerPoolPerformance(t *testing.T) {
	pool := NewTimerPool()
	
	// 测试 Timer 池的基本功能
	t.Run("BasicFunctionality", func(t *testing.T) {
		timeout := time.Millisecond * 100
		timer := pool.Get(timeout)
		
		start := time.Now()
		select {
		case <-timer.C:
			duration := time.Since(start)
			assert.InDelta(t, timeout.Nanoseconds(), duration.Nanoseconds(), 
				float64(time.Millisecond*10), "Timer should fire approximately on time")
		case <-time.After(time.Millisecond * 200):
			t.Fatal("Timer did not fire within expected time")
		}
		
		pool.Put(timer)
	})
	
	// 测试 Timer 复用
	t.Run("TimerReuse", func(t *testing.T) {
		timer1 := pool.Get(time.Millisecond * 50)
		pool.Put(timer1)
		
		timer2 := pool.Get(time.Millisecond * 100)
		// 在理想情况下，timer2 应该是复用的 timer1
		// 但我们主要测试功能正确性
		
		start := time.Now()
		<-timer2.C
		duration := time.Since(start)
		
		assert.InDelta(t, time.Millisecond*100, duration, 
			float64(time.Millisecond*10), "Reused timer should work correctly")
		
		pool.Put(timer2)
	})
}

// TestWaitForMessageOptimization 测试消息等待优化
func TestWaitForMessageOptimization(t *testing.T) {
	queue := NewAdaptiveSendQueue(10, 40, 1024*1024)
	defer queue.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 测试优化后的等待机制
	t.Run("OptimizedWaiting", func(t *testing.T) {
		// 启动一个 goroutine 在延迟后发送消息
		go func() {
			time.Sleep(time.Millisecond * 50)
			msg := &proto.Message{MsgType: 1}
			_ = queue.Send(msg, false)
		}()

		start := time.Now()
		result := queue.WaitForMessage(ctx, time.Millisecond*200)
		duration := time.Since(start)

		assert.True(t, result, "Should receive message notification")
		assert.Less(t, duration, time.Millisecond*100, 
			"Should receive notification quickly")
		assert.Greater(t, duration, time.Millisecond*40, 
			"Should wait for the message to be sent")
	})

	// 测试超时情况
	t.Run("TimeoutHandling", func(t *testing.T) {
		start := time.Now()
		result := queue.WaitForMessage(ctx, time.Millisecond*100)
		duration := time.Since(start)

		assert.False(t, result, "Should timeout when no message")
		assert.InDelta(t, time.Millisecond*100, duration, 
			float64(time.Millisecond*10), "Should timeout at expected time")
	})
}

// BenchmarkTimerCreation 基准测试：Timer 创建性能对比
func BenchmarkTimerCreation(b *testing.B) {
	timeout := time.Millisecond * 100

	b.Run("time.NewTimer", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			timer := time.NewTimer(timeout)
			timer.Stop()
		}
	})

	b.Run("time.After", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			select {
			case <-time.After(timeout):
			default:
			}
		}
	})

	b.Run("TimerPool", func(b *testing.B) {
		pool := NewTimerPool()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			timer := pool.Get(timeout)
			pool.Put(timer)
		}
	})
}

// BenchmarkWaitForMessage 基准测试：消息等待性能对比
func BenchmarkWaitForMessage(b *testing.B) {
	queue := NewAdaptiveSendQueue(1000, 4000, 10*1024*1024)
	defer queue.Close()

	ctx := context.Background()
	timeout := time.Microsecond * 100 // 很短的超时，主要测试创建开销

	b.Run("OptimizedWaitForMessage", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = queue.WaitForMessage(ctx, timeout)
		}
	})
}

// TestMemoryUsage 测试内存使用情况
func TestMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	// 强制 GC
	runtime.GC()
	runtime.GC()
	
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// 测试大量 Timer 操作的内存影响
	queue := NewAdaptiveSendQueue(100, 400, 10*1024*1024)
	defer queue.Close()

	ctx := context.Background()
	timeout := time.Microsecond * 10

	// 执行大量操作
	for i := 0; i < 10000; i++ {
		_ = queue.WaitForMessage(ctx, timeout)
		
		if i%1000 == 0 {
			runtime.GC() // 定期触发 GC
		}
	}

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m2)

	memIncrease := m2.Alloc - m1.Alloc
	t.Logf("Memory increase after 10000 operations: %d bytes", memIncrease)
	
	// 验证内存增长在合理范围内（< 1MB）
	assert.Less(t, memIncrease, uint64(1024*1024), 
		"Memory increase should be reasonable")
}

// TestConcurrentTimerUsage 测试并发 Timer 使用
func TestConcurrentTimerUsage(t *testing.T) {
	queue := NewAdaptiveSendQueue(100, 400, 10*1024*1024)
	defer queue.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	const numGoroutines = 50
	const operationsPerGoroutine = 100

	// 启动多个 goroutine 并发使用 Timer
	done := make(chan bool, numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			for j := 0; j < operationsPerGoroutine; j++ {
				timeout := time.Microsecond * time.Duration(10+j%90)
				_ = queue.WaitForMessage(ctx, timeout)
				
				// 偶尔发送消息
				if j%10 == 0 {
					msg := &proto.Message{MsgType: uint32(id*1000 + j)}
					_ = queue.Send(msg, false)
				}
			}
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// goroutine 完成
		case <-time.After(time.Second * 10):
			t.Fatal("Concurrent test timeout")
		}
	}

	t.Logf("Concurrent test completed successfully with %d goroutines", numGoroutines)
}

// TestTimerPoolStress 压力测试 Timer 池
func TestTimerPoolStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	pool := NewTimerPool()
	
	const numOperations = 100000
	const numGoroutines = 10
	
	done := make(chan bool, numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- true }()
			
			for j := 0; j < numOperations/numGoroutines; j++ {
				timeout := time.Microsecond * time.Duration(1+j%100)
				timer := pool.Get(timeout)
				
				// 模拟使用 timer
				select {
				case <-timer.C:
					// timer 触发
				default:
					// timer 未触发
				}
				
				pool.Put(timer)
			}
		}()
	}
	
	// 等待所有 goroutine 完成
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
			// goroutine 完成
		case <-time.After(time.Second * 30):
			t.Fatal("Stress test timeout")
		}
	}
	
	t.Logf("Timer pool stress test completed: %d operations", numOperations)
}
