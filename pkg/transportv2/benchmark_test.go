package transportv2_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/testkit"
)

func BenchmarkTransportV2RPC(b *testing.B) {
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})
	defer h.Close()

	payload := []byte("benchmark-rpc")
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Client.Call(ctx, testkit.ServerNodeID, uint64(i), transportv2.PriorityRPC, testkit.ServiceID, payload); err != nil {
			b.Fatalf("Call() error = %v", err)
		}
	}
}
