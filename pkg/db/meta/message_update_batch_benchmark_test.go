package meta

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkMessageUpdateBatchSnapshots measures the per-Slot preview read shape
// across distinct logical hash slots, with a populated metadata keyspace.
func BenchmarkMessageUpdateBatchSnapshots(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	batch := db.NewWriteBatch()
	for i := 0; i < 600; i++ {
		if err := batch.UpsertChannel(uint16(i%256), Channel{ChannelID: fmt.Sprintf("preview-%04d", i), ChannelType: 2}); err != nil {
			b.Fatal(err)
		}
	}
	if err := batch.Commit(); err != nil {
		b.Fatal(err)
	}
	batch.Close()
	for _, count := range []int{8, 16, 200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			slots := make([]uint16, count)
			reads := make([]MessageUpdateRead, count)
			for i := range reads {
				slots[i] = uint16(i % 256)
				reads[i] = MessageUpdateRead{ChannelID: fmt.Sprintf("preview-%04d", i), ChannelType: 2, IDs: []uint64{uint64(i + 1)}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pages, err := db.ReadMessageUpdatesBatch(context.Background(), slots, reads)
				if err != nil || len(pages) != count {
					b.Fatalf("pages=%d error=%v", len(pages), err)
				}
			}
		})
	}
}
