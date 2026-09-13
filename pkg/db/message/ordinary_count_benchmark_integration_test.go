//go:build integration

package message

import (
	"context"
	"testing"
)

// BenchmarkOrdinaryCount measures warm durable-index reads without payload scans.
func BenchmarkOrdinaryCount(b *testing.B) {
	for _, mixed := range []bool{false, true} {
		name := "ordinary"
		if mixed {
			name = "mixed"
		}
		b.Run(name, func(b *testing.B) {
			s := openTestMessageStore(b)
			defer s.close(b)
			log := testChannelLog(s)
			flags := make([]bool, 10000)
			for i := range flags {
				flags[i] = mixed && i%3 == 0
			}
			appendBadgeRows(b, log, 1, false, flags...)
			ctx := context.Background()
			want, err := log.CountOrdinaryMessages(ctx, 100, 10000)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := log.CountOrdinaryMessages(ctx, 100, 10000)
				if err != nil || got != want {
					b.Fatalf("count = %d, %v; want %d", got, err, want)
				}
			}
		})
	}
}
