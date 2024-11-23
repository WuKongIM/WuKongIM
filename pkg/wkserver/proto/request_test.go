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
