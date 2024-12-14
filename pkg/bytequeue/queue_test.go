package bytequeue

import (
	"bytes"
	"testing"
)

func TestByteQueue(t *testing.T) {
	// 创建一个新的 ByteQueue
	bq := New()
	defer bq.Reset() // 确保测试结束后重置

	// 测试 Write 方法
	t.Run("Write", func(t *testing.T) {
		data := []byte("hello")
		n, err := bq.Write(data)
		if err != nil {
			t.Fatalf("Write() failed: %v", err)
		}
		if n != len(data) {
			t.Errorf("Write() = %d, want %d", n, len(data))
		}
		if !bytes.Equal(bq.buffer.B, data) {
			t.Errorf("Write() buffer = %v, want %v", bq.buffer.B, data)
		}
	})

	// 测试 Peek 方法
	t.Run("Peek", func(t *testing.T) {
		data := []byte("hello")
		// 在偏移量 0 处读取 5 个字节
		result := bq.Peek(0, 5)
		if !bytes.Equal(result, data) {
			t.Errorf("Peek() = %v, want %v", result, data)
		}

		// 读取部分数据，从位置 2 开始读取 3 个字节
		result = bq.Peek(2, 3)
		expected := []byte("llo")
		if !bytes.Equal(result, expected) {
			t.Errorf("Peek() = %v, want %v", result, expected)
		}

		// 测试越界，startPosition 大于数据总大小
		result = bq.Peek(100, 3)
		if result != nil {
			t.Errorf("Peek() = %v, want nil", result)
		}

		// 测试读取超过缓冲区剩余数据
		result = bq.Peek(0, 10) // 请求更多字节
		if !bytes.Equal(result, data) {
			t.Errorf("Peek() = %v, want %v", result, data)
		}
	})

	// 测试 Discard 方法
	t.Run("Discard", func(t *testing.T) {
		// 写入一些数据
		_, _ = bq.Write([]byte("world"))

		// 丢弃前 5 个字节
		bq.Discard(5)
		if len(bq.buffer.B) != 5 {
			t.Errorf("Discard() buffer size = %d, want 5", len(bq.buffer.B))
		}

		// 丢弃所有数据
		bq.Discard(10)
		if len(bq.buffer.B) != 0 {
			t.Errorf("Discard() buffer size = %d, want 0", len(bq.buffer.B))
		}
	})

	// 测试 Reset 方法
	t.Run("Reset", func(t *testing.T) {
		// 写入数据
		_, _ = bq.Write([]byte("reset"))

		// 重置
		bq.Reset()

		// 确保缓冲区已清空
		if len(bq.buffer.B) != 0 {
			t.Errorf("Reset() buffer size = %d, want 0", len(bq.buffer.B))
		}
		if bq.offsetSize != 0 {
			t.Errorf("Reset() offsetSize = %d, want 0", bq.offsetSize)
		}
		if bq.totalSize != 0 {
			t.Errorf("Reset() totalSize = %d, want 0", bq.totalSize)
		}
	})
}
