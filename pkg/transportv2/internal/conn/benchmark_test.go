package conn

import (
	"bytes"
	"net"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func BenchmarkConnWriteOutboundBatch(b *testing.B) {
	cases := []struct {
		name           string
		frameCount     int
		payloadSize    int
		maxBatchFrames int
		maxBatchBytes  int
	}{
		{
			name:           "SingleFullBatch",
			frameCount:     64,
			payloadSize:    256,
			maxBatchFrames: 64,
			maxBatchBytes:  64 * 256,
		},
		{
			name:           "SplitByFrameLimit",
			frameCount:     128,
			payloadSize:    256,
			maxBatchFrames: 32,
			maxBatchBytes:  128 * 256,
		},
		{
			name:           "SplitByByteLimit",
			frameCount:     128,
			payloadSize:    1024,
			maxBatchFrames: 128,
			maxBatchBytes:  16 * 1024,
		},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			limits := testLimits()
			limits.MaxFrameBodyBytes = max(limits.MaxFrameBodyBytes, tc.payloadSize)
			limits.MaxQueuedBytesPerConn = int64(tc.frameCount * tc.payloadSize * 2)
			limits.MaxQueuedItemsPerConn = tc.frameCount * 2
			limits.MaxBatchFrames = tc.maxBatchFrames
			limits.MaxBatchBytes = tc.maxBatchBytes

			c := New(newDeadlineConn(), Config{Limits: limits}, nil)
			payload := bytes.Repeat([]byte("b"), tc.payloadSize)
			items := make([]sched.Item, tc.frameCount)
			var outbounds []Outbound
			var frames []wire.Frame
			var buffers net.Buffers

			b.SetBytes(int64(tc.frameCount * tc.payloadSize))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fillBenchmarkOutboundItems(items, payload)
				var err error
				outbounds, frames, err = c.writeOutboundBatch(items, outbounds, frames, &buffers)
				if err != nil {
					b.Fatalf("writeOutboundBatch() error = %v", err)
				}
			}
		})
	}
}

func fillBenchmarkOutboundItems(items []sched.Item, payload []byte) {
	priorities := [...]core.Priority{
		core.PriorityRaft,
		core.PriorityControl,
		core.PriorityRPC,
		core.PriorityBulk,
	}
	for i := range items {
		items[i] = sched.Item{
			Priority: priorities[i%len(priorities)],
			Bytes:    len(payload),
			Value: Outbound{
				Kind:      core.FrameKindData,
				Priority:  priorities[i%len(priorities)],
				ServiceID: uint16(1 + i%8),
				Payload:   core.NewOwnedBuffer(payload, nil),
			},
		}
	}
}
