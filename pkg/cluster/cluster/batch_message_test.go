package cluster

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchMessageEncoding 测试批量消息编码解码
func TestBatchMessageEncoding(t *testing.T) {
	// 创建测试消息
	messages := []*proto.Message{
		{
			Id:        1,
			MsgType:   uint32(proto.MsgTypeRequest),
			Content:   []byte("test message 1"),
			Timestamp: uint64(time.Now().UnixNano()),
		},
		{
			Id:        2,
			MsgType:   uint32(proto.MsgTypeResp),
			Content:   []byte("test message 2"),
			Timestamp: uint64(time.Now().UnixNano()),
		},
		{
			Id:        3,
			MsgType:   uint32(proto.MsgTypeHeartbeat),
			Content:   []byte("heartbeat"),
			Timestamp: uint64(time.Now().UnixNano()),
		},
	}

	// 创建批量消息
	batchMsg := &proto.BatchMessage{
		Messages: messages,
		Count:    uint32(len(messages)),
	}

	// 测试编码
	data, err := batchMsg.Encode()
	require.NoError(t, err)
	assert.Greater(t, len(data), 0)

	t.Logf("Batch message encoded size: %d bytes", len(data))

	// 测试解码
	decodedBatch := &proto.BatchMessage{}
	err = decodedBatch.Decode(data)
	require.NoError(t, err)

	// 验证解码结果
	assert.Equal(t, batchMsg.Count, decodedBatch.Count)
	assert.Len(t, decodedBatch.Messages, len(messages))

	for i, originalMsg := range messages {
		decodedMsg := decodedBatch.Messages[i]
		assert.Equal(t, originalMsg.Id, decodedMsg.Id)
		assert.Equal(t, originalMsg.MsgType, decodedMsg.MsgType)
		assert.Equal(t, originalMsg.Content, decodedMsg.Content)
		assert.Equal(t, originalMsg.Timestamp, decodedMsg.Timestamp)
	}
}

// TestSendBatchOptimization 测试批量发送优化
func TestSendBatchOptimization(t *testing.T) {
	opts := NewOptions()
	opts.SendQueueLength = 100
	opts.MaxSendQueueSize = 1024 * 1024

	node := NewImprovedNode(1, "batch-test", "127.0.0.1:8080", opts)
	defer node.Stop()
	node.SetTestMode(true) // 启用测试模式

	t.Run("SingleMessage", func(t *testing.T) {
		// 测试单个消息发送
		msg := &proto.Message{
			Id:        1,
			MsgType:   uint32(proto.MsgTypeRequest),
			Content:   []byte("single message"),
			Timestamp: uint64(time.Now().UnixNano()),
		}

		err := node.sendBatch([]*proto.Message{msg})
		assert.NoError(t, err)
	})

	t.Run("BatchMessages", func(t *testing.T) {
		// 测试批量消息发送
		messages := make([]*proto.Message, 5)
		for i := 0; i < 5; i++ {
			messages[i] = &proto.Message{
				Id:        uint64(i + 10),
				MsgType:   uint32(proto.MsgTypeRequest),
				Content:   []byte("batch message " + string(rune(i+'0'))),
				Timestamp: uint64(time.Now().UnixNano()),
			}
		}

		err := node.sendBatch(messages)
		assert.NoError(t, err)
	})

	t.Run("EmptyBatch", func(t *testing.T) {
		// 测试空批量
		err := node.sendBatch([]*proto.Message{})
		assert.NoError(t, err)
	})
}

// TestBatchMessageSize 测试批量消息大小计算
func TestBatchMessageSize(t *testing.T) {
	messages := []*proto.Message{
		{
			Id:        1,
			MsgType:   1,
			Content:   make([]byte, 100),
			Timestamp: uint64(time.Now().UnixNano()),
		},
		{
			Id:        2,
			MsgType:   2,
			Content:   make([]byte, 200),
			Timestamp: uint64(time.Now().UnixNano()),
		},
	}

	batchMsg := &proto.BatchMessage{
		Messages: messages,
		Count:    uint32(len(messages)),
	}

	// 计算大小
	size := batchMsg.Size()
	assert.Greater(t, size, 300) // 应该大于消息内容总和

	// 验证编码后的大小
	data, err := batchMsg.Encode()
	require.NoError(t, err)
	assert.Equal(t, size, len(data))

	t.Logf("Batch message size: %d bytes", size)
}

// TestBatchMessagePerformance 测试批量消息性能
func TestBatchMessagePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// 创建大量消息
	messageCount := 1000
	messages := make([]*proto.Message, messageCount)
	for i := 0; i < messageCount; i++ {
		messages[i] = &proto.Message{
			Id:        uint64(i),
			MsgType:   uint32(proto.MsgTypeRequest),
			Content:   []byte("performance test message"),
			Timestamp: uint64(time.Now().UnixNano()),
		}
	}

	batchMsg := &proto.BatchMessage{
		Messages: messages,
		Count:    uint32(len(messages)),
	}

	// 测试编码性能
	start := time.Now()
	data, err := batchMsg.Encode()
	encodeTime := time.Since(start)
	require.NoError(t, err)

	t.Logf("Encoded %d messages in %v (%.2f msg/ms)", 
		messageCount, encodeTime, float64(messageCount)/float64(encodeTime.Milliseconds()))

	// 测试解码性能
	start = time.Now()
	decodedBatch := &proto.BatchMessage{}
	err = decodedBatch.Decode(data)
	decodeTime := time.Since(start)
	require.NoError(t, err)

	t.Logf("Decoded %d messages in %v (%.2f msg/ms)", 
		messageCount, decodeTime, float64(messageCount)/float64(decodeTime.Milliseconds()))

	// 验证解码正确性
	assert.Equal(t, uint32(messageCount), decodedBatch.Count)
	assert.Len(t, decodedBatch.Messages, messageCount)
}

// TestBatchMessageErrorHandling 测试批量消息错误处理
func TestBatchMessageErrorHandling(t *testing.T) {
	t.Run("InvalidData", func(t *testing.T) {
		batchMsg := &proto.BatchMessage{}
		
		// 测试空数据
		err := batchMsg.Decode([]byte{})
		assert.Error(t, err)
		
		// 测试不完整数据
		err = batchMsg.Decode([]byte{1, 2})
		assert.Error(t, err)
	})

	t.Run("CorruptedMessage", func(t *testing.T) {
		// 创建一个有效的批量消息
		messages := []*proto.Message{
			{
				Id:        1,
				MsgType:   1,
				Content:   []byte("test"),
				Timestamp: uint64(time.Now().UnixNano()),
			},
		}

		batchMsg := &proto.BatchMessage{
			Messages: messages,
			Count:    1,
		}

		data, err := batchMsg.Encode()
		require.NoError(t, err)

		// 损坏数据
		if len(data) > 10 {
			data[10] = 0xFF // 损坏某个字节
		}

		// 尝试解码损坏的数据
		corruptedBatch := &proto.BatchMessage{}
		err = corruptedBatch.Decode(data)
		// 可能成功也可能失败，取决于损坏的位置
		t.Logf("Decode corrupted data result: %v", err)
	})
}

// BenchmarkBatchMessageEncoding 基准测试批量消息编码
func BenchmarkBatchMessageEncoding(b *testing.B) {
	messages := make([]*proto.Message, 10)
	for i := 0; i < 10; i++ {
		messages[i] = &proto.Message{
			Id:        uint64(i),
			MsgType:   1,
			Content:   make([]byte, 100),
			Timestamp: uint64(time.Now().UnixNano()),
		}
	}

	batchMsg := &proto.BatchMessage{
		Messages: messages,
		Count:    10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = batchMsg.Encode()
	}
}

// BenchmarkBatchMessageDecoding 基准测试批量消息解码
func BenchmarkBatchMessageDecoding(b *testing.B) {
	messages := make([]*proto.Message, 10)
	for i := 0; i < 10; i++ {
		messages[i] = &proto.Message{
			Id:        uint64(i),
			MsgType:   1,
			Content:   make([]byte, 100),
			Timestamp: uint64(time.Now().UnixNano()),
		}
	}

	batchMsg := &proto.BatchMessage{
		Messages: messages,
		Count:    10,
	}

	data, _ := batchMsg.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodedBatch := &proto.BatchMessage{}
		_ = decodedBatch.Decode(data)
	}
}

// BenchmarkSendBatch 基准测试批量发送
func BenchmarkSendBatch(b *testing.B) {
	opts := NewOptions()
	opts.SendQueueLength = 1000
	opts.MaxSendQueueSize = 10 * 1024 * 1024

	node := NewImprovedNode(1, "benchmark", "127.0.0.1:8080", opts)
	defer node.Stop()
	node.SetTestMode(true)

	messages := make([]*proto.Message, 5)
	for i := 0; i < 5; i++ {
		messages[i] = &proto.Message{
			Id:        uint64(i),
			MsgType:   1,
			Content:   make([]byte, 100),
			Timestamp: uint64(time.Now().UnixNano()),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = node.sendBatch(messages)
	}
}
