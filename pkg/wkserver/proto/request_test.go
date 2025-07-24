package proto

import (
	"bytes"
	"fmt"
	"testing"
)

func TestRequest(t *testing.T) {

	var data []byte
	var err error
	t.Run("Marshal", func(t *testing.T) {
		// 测试代码
		req := &Request{
			Id:   12345,
			Path: "/test/path",
			Body: []byte("hello, world"),
		}

		// Marshal
		data, err = req.Marshal()
		if err != nil {
			fmt.Println("Marshal error:", err)
			return
		}

	})

	t.Run("Unmarshal", func(t *testing.T) {
		// Unmarshal
		var newReq Request
		if err := newReq.Unmarshal(data); err != nil {
			fmt.Println("Unmarshal error:", err)
			return
		}
	})
}

func TestConnect_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    Connect
		wantErr  bool
		expected Connect
	}{
		{
			name: "Basic test",
			input: Connect{
				Id:    1,
				Uid:   "user123",
				Token: "token123",
				Body:  []byte("hello world"),
			},
			wantErr: false,
			expected: Connect{
				Id:    1,
				Uid:   "user123",
				Token: "token123",
				Body:  []byte("hello world"),
			},
		},
		{
			name: "Empty Uid and Token",
			input: Connect{
				Id:    42,
				Uid:   "",
				Token: "",
				Body:  []byte("just body"),
			},
			wantErr: false,
			expected: Connect{
				Id:    42,
				Uid:   "",
				Token: "",
				Body:  []byte("just body"),
			},
		},
		{
			name: "Empty Body",
			input: Connect{
				Id:    1001,
				Uid:   "testuser",
				Token: "testtoken",
				Body:  []byte{},
			},
			wantErr: false,
			expected: Connect{
				Id:    1001,
				Uid:   "testuser",
				Token: "testtoken",
				Body:  []byte{},
			},
		},
		{
			name: "Large Body",
			input: Connect{
				Id:    9001,
				Uid:   "largeuser",
				Token: "largetoken",
				Body:  bytes.Repeat([]byte("a"), 1<<16), // 64KB Body
			},
			wantErr: false,
			expected: Connect{
				Id:    9001,
				Uid:   "largeuser",
				Token: "largetoken",
				Body:  bytes.Repeat([]byte("a"), 1<<16),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Marshal
			data, err := tt.input.Marshal()
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 测试 Unmarshal
			var result Connect
			err = result.Unmarshal(data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 如果没有错误，验证结果是否与预期一致
			if !tt.wantErr && !compareConnect(result, tt.expected) {
				t.Errorf("Unmarshal() got = %+v, want = %+v", result, tt.expected)
			}
		})
	}
}

// 比较两个 Connect 对象是否相等
func compareConnect(a, b Connect) bool {
	return a.Id == b.Id && a.Uid == b.Uid && a.Token == b.Token && bytes.Equal(a.Body, b.Body)
}

func TestConnack_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    Connack
		wantErr  bool
		expected Connack
	}{
		{
			name: "Basic test",
			input: Connack{
				Id:     1,
				Status: 0,
				Body:   []byte("hello world"),
			},
			wantErr: false,
			expected: Connack{
				Id:     1,
				Status: 0,
				Body:   []byte("hello world"),
			},
		},
		{
			name: "Empty Body",
			input: Connack{
				Id:     42,
				Status: 1,
				Body:   []byte{},
			},
			wantErr: false,
			expected: Connack{
				Id:     42,
				Status: 1,
				Body:   []byte{},
			},
		},
		{
			name: "Large Body",
			input: Connack{
				Id:     9001,
				Status: 2,
				Body:   bytes.Repeat([]byte("a"), 1<<16), // 64KB Body
			},
			wantErr: false,
			expected: Connack{
				Id:     9001,
				Status: 2,
				Body:   bytes.Repeat([]byte("a"), 1<<16),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Marshal
			data, err := tt.input.Marshal()
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 测试 Unmarshal
			var result Connack
			err = result.Unmarshal(data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 如果没有错误，验证结果是否与预期一致
			if !tt.wantErr && !compareConnack(result, tt.expected) {
				t.Errorf("Unmarshal() got = %+v, want = %+v", result, tt.expected)
			}
		})
	}
}

// 比较两个 Connack 对象是否相等
func compareConnack(a, b Connack) bool {
	return a.Id == b.Id && a.Status == b.Status && bytes.Equal(a.Body, b.Body)
}

func TestResponse_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    Response
		wantErr  bool
		expected Response
	}{
		{
			name: "Basic test",
			input: Response{
				Id:        1,
				Status:    StatusOK,
				Timestamp: 1637849938,
				Body:      []byte("hello world"),
			},
			wantErr: false,
			expected: Response{
				Id:        1,
				Status:    StatusOK,
				Timestamp: 1637849938,
				Body:      []byte("hello world"),
			},
		},
		{
			name: "Empty Body",
			input: Response{
				Id:        42,
				Status:    StatusNotFound,
				Timestamp: 1637849938,
				Body:      []byte{},
			},
			wantErr: false,
			expected: Response{
				Id:        42,
				Status:    StatusNotFound,
				Timestamp: 1637849938,
				Body:      []byte{},
			},
		},
		{
			name: "Large Body",
			input: Response{
				Id:        9001,
				Status:    StatusError,
				Timestamp: 1637849938,
				Body:      bytes.Repeat([]byte("a"), 1<<16), // 64KB Body
			},
			wantErr: false,
			expected: Response{
				Id:        9001,
				Status:    StatusError,
				Timestamp: 1637849938,
				Body:      bytes.Repeat([]byte("a"), 1<<16),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Marshal
			data, err := tt.input.Marshal()
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 测试 Unmarshal
			var result Response
			err = result.Unmarshal(data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 如果没有错误，验证结果是否与预期一致
			if !tt.wantErr && !compareResponse(result, tt.expected) {
				t.Errorf("Unmarshal() got = %+v, want = %+v", result, tt.expected)
			}
		})
	}
}

// 比较两个 Response 对象是否相等
func compareResponse(a, b Response) bool {
	return a.Id == b.Id && a.Status == b.Status && a.Timestamp == b.Timestamp && bytes.Equal(a.Body, b.Body)
}

func TestMessage_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    Message
		wantErr  bool
		expected Message
	}{
		{
			name: "Basic test",
			input: Message{
				Id:        1,
				MsgType:   1001,
				Content:   []byte("hello world"),
				Timestamp: 1637849938,
			},
			wantErr: false,
			expected: Message{
				Id:        1,
				MsgType:   1001,
				Content:   []byte("hello world"),
				Timestamp: 1637849938,
			},
		},
		{
			name: "Empty Content",
			input: Message{
				Id:        42,
				MsgType:   2001,
				Content:   []byte{},
				Timestamp: 1637849938,
			},
			wantErr: false,
			expected: Message{
				Id:        42,
				MsgType:   2001,
				Content:   []byte{},
				Timestamp: 1637849938,
			},
		},
		{
			name: "Large Content",
			input: Message{
				Id:        9001,
				MsgType:   3001,
				Content:   bytes.Repeat([]byte("a"), 1<<16), // 64KB Content
				Timestamp: 1637849938,
			},
			wantErr: false,
			expected: Message{
				Id:        9001,
				MsgType:   3001,
				Content:   bytes.Repeat([]byte("a"), 1<<16),
				Timestamp: 1637849938,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Marshal
			data, err := tt.input.Marshal()
			if (err != nil) != tt.wantErr {
				t.Errorf("Marshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 测试 Unmarshal
			var result Message
			err = result.Unmarshal(data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			// 如果没有错误，验证结果是否与预期一致
			if !tt.wantErr && !compareMessage(result, tt.expected) {
				t.Errorf("Unmarshal() got = %+v, want = %+v", result, tt.expected)
			}
		})
	}
}

// 比较两个 Message 对象是否相等
func compareMessage(a, b Message) bool {
	return a.Id == b.Id && a.MsgType == b.MsgType && a.Timestamp == b.Timestamp && bytes.Equal(a.Content, b.Content)
}

// TestBatchMessage_EncodeDecodeBasic 测试 BatchMessage 基本编码解码功能
func TestBatchMessage_EncodeDecodeBasic(t *testing.T) {
	tests := []struct {
		name     string
		input    BatchMessage
		wantErr  bool
		expected BatchMessage
	}{
		{
			name: "Empty batch",
			input: BatchMessage{
				Messages: []*Message{},
				Count:    0,
			},
			wantErr: false,
			expected: BatchMessage{
				Messages: []*Message{},
				Count:    0,
			},
		},
		{
			name: "Single message",
			input: BatchMessage{
				Messages: []*Message{
					{
						Id:        1,
						MsgType:   uint32(MsgTypeRequest),
						Content:   []byte("test message"),
						Timestamp: 1637849938,
					},
				},
				Count: 1,
			},
			wantErr: false,
			expected: BatchMessage{
				Messages: []*Message{
					{
						Id:        1,
						MsgType:   uint32(MsgTypeRequest),
						Content:   []byte("test message"),
						Timestamp: 1637849938,
					},
				},
				Count: 1,
			},
		},
		{
			name: "Multiple messages",
			input: BatchMessage{
				Messages: []*Message{
					{
						Id:        1,
						MsgType:   uint32(MsgTypeRequest),
						Content:   []byte("message 1"),
						Timestamp: 1637849938,
					},
					{
						Id:        2,
						MsgType:   uint32(MsgTypeResp),
						Content:   []byte("message 2"),
						Timestamp: 1637849939,
					},
					{
						Id:        3,
						MsgType:   uint32(MsgTypeHeartbeat),
						Content:   []byte{},
						Timestamp: 1637849940,
					},
				},
				Count: 3,
			},
			wantErr: false,
			expected: BatchMessage{
				Messages: []*Message{
					{
						Id:        1,
						MsgType:   uint32(MsgTypeRequest),
						Content:   []byte("message 1"),
						Timestamp: 1637849938,
					},
					{
						Id:        2,
						MsgType:   uint32(MsgTypeResp),
						Content:   []byte("message 2"),
						Timestamp: 1637849939,
					},
					{
						Id:        3,
						MsgType:   uint32(MsgTypeHeartbeat),
						Content:   []byte{},
						Timestamp: 1637849940,
					},
				},
				Count: 3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Encode
			data, err := tt.input.Encode()
			if (err != nil) != tt.wantErr {
				t.Errorf("Encode() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return // 如果期望错误，不继续测试解码
			}

			// 测试 Decode
			var result BatchMessage
			err = result.Decode(data)
			if err != nil {
				t.Errorf("Decode() error = %v", err)
				return
			}

			// 验证结果
			if !compareBatchMessage(result, tt.expected) {
				t.Errorf("Decode() got = %+v, want = %+v", result, tt.expected)
			}
		})
	}
}

// 比较两个 BatchMessage 对象是否相等
func compareBatchMessage(a, b BatchMessage) bool {
	if a.Count != b.Count {
		return false
	}

	if len(a.Messages) != len(b.Messages) {
		return false
	}

	for i, msgA := range a.Messages {
		msgB := b.Messages[i]
		if !compareMessage(*msgA, *msgB) {
			return false
		}
	}

	return true
}

// TestBatchMessage_EdgeCases 测试 BatchMessage 边界情况
func TestBatchMessage_EdgeCases(t *testing.T) {
	t.Run("Large batch", func(t *testing.T) {
		// 创建大量消息的批次
		messageCount := 1000
		messages := make([]*Message, messageCount)
		for i := 0; i < messageCount; i++ {
			messages[i] = &Message{
				Id:        uint64(i),
				MsgType:   uint32(MsgTypeRequest),
				Content:   []byte(fmt.Sprintf("message %d", i)),
				Timestamp: uint64(1637849938 + i),
			}
		}

		batchMsg := BatchMessage{
			Messages: messages,
			Count:    uint32(messageCount),
		}

		// 编码
		data, err := batchMsg.Encode()
		if err != nil {
			t.Errorf("Encode() error = %v", err)
			return
		}

		// 解码
		var result BatchMessage
		err = result.Decode(data)
		if err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}

		// 验证
		if result.Count != uint32(messageCount) {
			t.Errorf("Count mismatch: got %d, want %d", result.Count, messageCount)
		}

		if len(result.Messages) != messageCount {
			t.Errorf("Messages length mismatch: got %d, want %d", len(result.Messages), messageCount)
		}

		// 验证前几个和后几个消息
		for i := 0; i < 5; i++ {
			if !compareMessage(*result.Messages[i], *messages[i]) {
				t.Errorf("Message %d mismatch", i)
			}
		}

		for i := messageCount - 5; i < messageCount; i++ {
			if !compareMessage(*result.Messages[i], *messages[i]) {
				t.Errorf("Message %d mismatch", i)
			}
		}
	})

	t.Run("Messages with large content", func(t *testing.T) {
		// 创建包含大内容的消息
		largeContent := bytes.Repeat([]byte("A"), 64*1024) // 64KB
		messages := []*Message{
			{
				Id:        1,
				MsgType:   uint32(MsgTypeRequest),
				Content:   largeContent,
				Timestamp: 1637849938,
			},
			{
				Id:        2,
				MsgType:   uint32(MsgTypeResp),
				Content:   bytes.Repeat([]byte("B"), 32*1024), // 32KB
				Timestamp: 1637849939,
			},
		}

		batchMsg := BatchMessage{
			Messages: messages,
			Count:    2,
		}

		// 编码
		data, err := batchMsg.Encode()
		if err != nil {
			t.Errorf("Encode() error = %v", err)
			return
		}

		// 解码
		var result BatchMessage
		err = result.Decode(data)
		if err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}

		// 验证
		if !compareBatchMessage(result, batchMsg) {
			t.Errorf("Large content batch message mismatch")
		}
	})

	t.Run("Mixed message types", func(t *testing.T) {
		// 创建不同类型的消息
		messages := []*Message{
			{
				Id:        1,
				MsgType:   uint32(MsgTypeConnect),
				Content:   []byte("connect data"),
				Timestamp: 1637849938,
			},
			{
				Id:        2,
				MsgType:   uint32(MsgTypeConnack),
				Content:   []byte("connack data"),
				Timestamp: 1637849939,
			},
			{
				Id:        3,
				MsgType:   uint32(MsgTypeRequest),
				Content:   []byte("request data"),
				Timestamp: 1637849940,
			},
			{
				Id:        4,
				MsgType:   uint32(MsgTypeResp),
				Content:   []byte("response data"),
				Timestamp: 1637849941,
			},
			{
				Id:        5,
				MsgType:   uint32(MsgTypeHeartbeat),
				Content:   []byte{},
				Timestamp: 1637849942,
			},
			{
				Id:        6,
				MsgType:   uint32(MsgTypeMessage),
				Content:   []byte("message data"),
				Timestamp: 1637849943,
			},
		}

		batchMsg := BatchMessage{
			Messages: messages,
			Count:    uint32(len(messages)),
		}

		// 编码
		data, err := batchMsg.Encode()
		if err != nil {
			t.Errorf("Encode() error = %v", err)
			return
		}

		// 解码
		var result BatchMessage
		err = result.Decode(data)
		if err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}

		// 验证
		if !compareBatchMessage(result, batchMsg) {
			t.Errorf("Mixed message types batch mismatch")
		}
	})
}

// TestBatchMessage_ErrorHandling 测试 BatchMessage 错误处理
func TestBatchMessage_ErrorHandling(t *testing.T) {
	t.Run("Decode empty data", func(t *testing.T) {
		var batchMsg BatchMessage
		err := batchMsg.Decode([]byte{})
		if err == nil {
			t.Error("Expected error for empty data, got nil")
		}
		if err.Error() != "batch message data too short" {
			t.Errorf("Expected 'batch message data too short', got '%s'", err.Error())
		}
	})

	t.Run("Decode insufficient data", func(t *testing.T) {
		var batchMsg BatchMessage
		// 只有2个字节，不足以读取Count字段（需要4个字节）
		err := batchMsg.Decode([]byte{0x01, 0x02})
		if err == nil {
			t.Error("Expected error for insufficient data, got nil")
		}
		if err.Error() != "batch message data too short" {
			t.Errorf("Expected 'batch message data too short', got '%s'", err.Error())
		}
	})

	t.Run("Decode truncated message data", func(t *testing.T) {
		// 创建一个有效的批量消息
		originalMsg := &Message{
			Id:        1,
			MsgType:   uint32(MsgTypeRequest),
			Content:   []byte("test message"),
			Timestamp: 1637849938,
		}

		batchMsg := BatchMessage{
			Messages: []*Message{originalMsg},
			Count:    1,
		}

		// 编码
		data, err := batchMsg.Encode()
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}

		// 截断数据（移除最后10个字节）
		if len(data) > 10 {
			truncatedData := data[:len(data)-10]

			var result BatchMessage
			err = result.Decode(truncatedData)
			if err == nil {
				t.Error("Expected error for truncated data, got nil")
			}
		}
	})

	t.Run("Count mismatch", func(t *testing.T) {
		// 手动构造错误的数据：Count说有2个消息，但实际只有1个
		originalMsg := &Message{
			Id:        1,
			MsgType:   uint32(MsgTypeRequest),
			Content:   []byte("test"),
			Timestamp: 1637849938,
		}

		// 先编码一个正常的消息
		msgData, err := originalMsg.Encode()
		if err != nil {
			t.Fatalf("Message encode error = %v", err)
		}

		// 手动构造批量消息数据：Count=2，但只有1个消息
		data := make([]byte, 4+len(msgData))
		// 写入Count=2
		data[0] = 2
		data[1] = 0
		data[2] = 0
		data[3] = 0
		// 写入1个消息数据
		copy(data[4:], msgData)

		var result BatchMessage
		err = result.Decode(data)
		if err == nil {
			t.Error("Expected error for count mismatch, got nil")
		}
	})

	t.Run("Invalid message in batch", func(t *testing.T) {
		// 手动构造包含无效消息的批量数据
		data := []byte{
			// Count = 1
			1, 0, 0, 0,
			// 无效的消息数据（太短）
			1, 2, 3,
		}

		var result BatchMessage
		err := result.Decode(data)
		if err == nil {
			t.Error("Expected error for invalid message, got nil")
		}
	})
}

// TestBatchMessage_Size 测试 BatchMessage Size 方法
func TestBatchMessage_Size(t *testing.T) {
	tests := []struct {
		name     string
		input    BatchMessage
		expected int
	}{
		{
			name: "Empty batch",
			input: BatchMessage{
				Messages: []*Message{},
				Count:    0,
			},
			expected: 4, // 只有Count字段
		},
		{
			name: "Single message",
			input: BatchMessage{
				Messages: []*Message{
					{
						Id:        1,
						MsgType:   1,
						Content:   []byte("test"),
						Timestamp: 1637849938,
					},
				},
				Count: 1,
			},
			expected: 4 + (8 + 4 + 8 + 4 + 4), // Count + Message fields
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size := tt.input.Size()
			if size != tt.expected {
				t.Errorf("Size() = %d, want %d", size, tt.expected)
			}

			// 验证Size()与实际编码后的长度一致
			data, err := tt.input.Encode()
			if err != nil {
				t.Errorf("Encode() error = %v", err)
				return
			}

			if len(data) != size {
				t.Errorf("Encoded data length %d != Size() %d", len(data), size)
			}
		})
	}
}

// TestBatchMessage_Performance 性能测试
func TestBatchMessage_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// 创建大批量消息
	messageCount := 10000
	messages := make([]*Message, messageCount)
	for i := 0; i < messageCount; i++ {
		messages[i] = &Message{
			Id:        uint64(i),
			MsgType:   uint32(MsgTypeRequest),
			Content:   []byte("performance test message"),
			Timestamp: uint64(1637849938 + i),
		}
	}

	batchMsg := BatchMessage{
		Messages: messages,
		Count:    uint32(messageCount),
	}

	// 测试编码性能
	data, err := batchMsg.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	t.Logf("Encoded %d messages, total size: %d bytes", messageCount, len(data))

	// 测试解码性能
	var result BatchMessage
	err = result.Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	// 验证解码结果
	if result.Count != uint32(messageCount) {
		t.Errorf("Count mismatch: got %d, want %d", result.Count, messageCount)
	}

	if len(result.Messages) != messageCount {
		t.Errorf("Messages length mismatch: got %d, want %d", len(result.Messages), messageCount)
	}
}

// BenchmarkBatchMessage_Encode 基准测试编码性能
func BenchmarkBatchMessage_Encode(b *testing.B) {
	// 创建不同大小的批量消息进行基准测试
	sizes := []int{1, 10, 100, 1000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			// 准备测试数据
			messages := make([]*Message, size)
			for i := 0; i < size; i++ {
				messages[i] = &Message{
					Id:        uint64(i),
					MsgType:   uint32(MsgTypeRequest),
					Content:   []byte("benchmark test message"),
					Timestamp: uint64(1637849938 + i),
				}
			}

			batchMsg := BatchMessage{
				Messages: messages,
				Count:    uint32(size),
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := batchMsg.Encode()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBatchMessage_Decode 基准测试解码性能
func BenchmarkBatchMessage_Decode(b *testing.B) {
	// 创建不同大小的批量消息进行基准测试
	sizes := []int{1, 10, 100, 1000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			// 准备测试数据
			messages := make([]*Message, size)
			for i := 0; i < size; i++ {
				messages[i] = &Message{
					Id:        uint64(i),
					MsgType:   uint32(MsgTypeRequest),
					Content:   []byte("benchmark test message"),
					Timestamp: uint64(1637849938 + i),
				}
			}

			batchMsg := BatchMessage{
				Messages: messages,
				Count:    uint32(size),
			}

			// 预先编码
			data, err := batchMsg.Encode()
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var result BatchMessage
				err := result.Decode(data)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkBatchMessage_EncodeDecodeRoundtrip 基准测试完整的编码解码往返
func BenchmarkBatchMessage_EncodeDecodeRoundtrip(b *testing.B) {
	// 创建测试数据
	messages := make([]*Message, 50)
	for i := 0; i < 50; i++ {
		messages[i] = &Message{
			Id:        uint64(i),
			MsgType:   uint32(MsgTypeRequest),
			Content:   []byte("roundtrip test message"),
			Timestamp: uint64(1637849938 + i),
		}
	}

	batchMsg := BatchMessage{
		Messages: messages,
		Count:    50,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 编码
		data, err := batchMsg.Encode()
		if err != nil {
			b.Fatal(err)
		}

		// 解码
		var result BatchMessage
		err = result.Decode(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
