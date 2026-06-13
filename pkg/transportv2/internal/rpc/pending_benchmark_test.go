package rpc

import (
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
)

func BenchmarkPendingTableStoreComplete(b *testing.B) {
	for _, shards := range []int{1, 16, 64} {
		shards := shards
		b.Run("Shards"+strconv.Itoa(shards), func(b *testing.B) {
			table := NewPendingTable(shards)
			resp := Response{Payload: []byte("ok")}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ch := make(chan Response, 1)
				id := uint64(i + 1)
				table.Store(id, ch)
				if !table.Complete(id, resp) {
					b.Fatalf("Complete(%d) = false, want true", id)
				}
				<-ch
			}
		})
	}
}

func BenchmarkPendingTableStoreCompleteParallel(b *testing.B) {
	for _, shards := range []int{1, 16, 64} {
		shards := shards
		b.Run("Shards"+strconv.Itoa(shards), func(b *testing.B) {
			table := NewPendingTable(shards)
			resp := Response{Payload: []byte("ok")}
			var ids atomic.Uint64

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					ch := make(chan Response, 1)
					id := ids.Add(1)
					table.Store(id, ch)
					if !table.Complete(id, resp) {
						b.Fatalf("Complete(%d) = false, want true", id)
					}
					<-ch
				}
			})
		})
	}
}

func BenchmarkPendingTableFailAll(b *testing.B) {
	errBoom := errors.New("boom")
	for _, pending := range []int{128, 4096} {
		pending := pending
		b.Run("Pending"+strconv.Itoa(pending), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				table := NewPendingTable(16)
				for id := 0; id < pending; id++ {
					table.Store(uint64(id+1), make(chan Response, 1))
				}
				table.FailAll(errBoom)
			}
			b.ReportMetric(float64(pending), "pending/op")
		})
	}
}
