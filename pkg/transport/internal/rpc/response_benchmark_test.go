package rpc

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// BenchmarkServiceSynchronousResponse isolates the response delivery boundary;
// TCP benchmarks separately include admission, budgets and wire encoding.
func BenchmarkServiceSynchronousResponse(b *testing.B) {
	for _, size := range []int{64, 65537} {
		b.Run(fmt.Sprintf("Bytes%d", size), func(b *testing.B) {
			payload := bytes.Repeat([]byte{0x5a}, size)
			svc := &Service{ctx: context.Background(), handler: func(_ context.Context, p []byte) ([]byte, error) { return p, nil }}
			req := Request{Context: context.Background(), Payload: core.NewOwnedBuffer(payload, nil), RespondBorrowed: func(resp Response) {
				if resp.Err != nil || !bytes.Equal(resp.Payload, payload) {
					b.Fatal("invalid response")
				}
			}}
			defer req.Payload.Release()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := svc.handle(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
