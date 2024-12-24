package track

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMessageString(t *testing.T) {
	// 创建一个新的消息实例
	msg := Message{
		PreStart: time.Now().Add(-5 * time.Millisecond),
	}

	// 模拟消息经过多个位置
	msg.Record(PositionStart)
	time.Sleep(10 * time.Millisecond) // 模拟耗时
	msg.Record(PositionChannelOnSend)
	time.Sleep(20 * time.Millisecond) // 模拟耗时
	msg.Record(PositionPushOnline)

	// 获取消息的字符串表示
	output := msg.String()
	fmt.Println(output)
	assert.Contains(t, output, "1001000010000000")
}

func TestMessageEncodeDecode(t *testing.T) {
	// 测试用例：创建多个不同的 Message 对象
	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "Basic test",
			message: Message{
				Path:     3, // Path: 0000000000000011
				Cost:     [16]uint16{0, 10, 20, 30},
				PreStart: time.Now().Add(-time.Minute),
			},
		},
		{
			name: "Edge case with no cost",
			message: Message{
				Path:     1, // Path: 0000000000000001
				Cost:     [16]uint16{0, 0, 0, 0},
				PreStart: time.Now(),
			},
		},
		{
			name: "Full cost scenario",
			message: Message{
				Path:     32767, // Path: 0111111111111111
				Cost:     [16]uint16{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000, 1100, 1200, 1300, 1400, 1500, 1600},
				PreStart: time.Now().Add(-time.Hour),
			},
		},
	}

	// 遍历所有测试用例
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 编码
			encoded := tt.message.Encode()
			// 解码
			var decoded Message
			err := decoded.Decode(encoded)
			if err != nil {
				t.Fatalf("Failed to decode message: %v", err)
			}

			// 检查编码后的值是否与解码后的值一致
			if tt.message.Path != decoded.Path {
				t.Errorf("Expected path %d, got %d", tt.message.Path, decoded.Path)
			}

			for i := 0; i < len(tt.message.Cost); i++ {
				if tt.message.Cost[i] != decoded.Cost[i] {
					t.Errorf("Expected cost at position %d to be %d, got %d", i, tt.message.Cost[i], decoded.Cost[i])
				}
			}

			// 对比时间戳（精确到纳秒）
			if !tt.message.PreStart.Equal(decoded.PreStart) {
				t.Errorf("Expected preStart time %v, got %v", tt.message.PreStart, decoded.PreStart)
			}
		})
	}
}
