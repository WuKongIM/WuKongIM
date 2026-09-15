//go:build integration

package meta

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkRuntimeMetadataPointRead(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	keys := make([]string, 600)
	batch := db.NewWriteBatch()
	defer batch.Close()
	for i := range keys {
		keys[i] = fmt.Sprintf("runtime-read-%04d", i)
		if err := batch.UpsertChannelRuntimeMeta(uint16(i%256), testRuntimeMeta(keys[i], 2)); err != nil {
			b.Fatal(err)
		}
	}
	if err := batch.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := (i * 137) % len(keys)
		m, err := db.ForHashSlot(uint16(n%256)).GetChannelRuntimeMeta(ctx, keys[n], 2)
		if err != nil || m.ChannelID != keys[n] {
			b.Fatalf("read: %+v %v", m, err)
		}
	}
}
