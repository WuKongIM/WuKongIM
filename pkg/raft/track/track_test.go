package track

import (
	"testing"
	"time"
)

func TestRecordEncodeDecode(t *testing.T) {
	// 测试用例：创建多个不同的 Message 对象
	tests := []struct {
		name    string
		message Record
	}{
		{
			name: "Basic test",
			message: Record{
				Path:     3, // Path: 0000000000000011
				Cost:     [16]uint16{0, 10, 20, 30},
				PreStart: time.Now().Add(-time.Minute),
			},
		},
		{
			name: "Edge case with no cost",
			message: Record{
				Path:     1, // Path: 0000000000000001
				Cost:     [16]uint16{0, 0, 0, 0},
				PreStart: time.Now(),
			},
		},
		{
			name: "Full cost scenario",
			message: Record{
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
			var decoded Record
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
