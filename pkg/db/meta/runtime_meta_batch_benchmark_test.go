package meta

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkRuntimeMetadataBatch includes key preparation and owned result assembly
// for both paths, across the 256 default hash slots used by conversation reads.
func BenchmarkRuntimeMetadataBatch(b *testing.B) {
	db, keys := runtimeMetadataBatchFixture(b)
	ctx := context.Background()
	for _, mode := range []string{"point", "batch"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				var rows []ChannelRuntimeMeta
				if mode == "batch" {
					var err error
					rows, err = db.GetChannelRuntimeMetaBatch(ctx, keys)
					if err != nil {
						b.Fatal(err)
					}
				} else {
					rows = make([]ChannelRuntimeMeta, 0, len(keys))
					for _, key := range keys {
						row, ok, err := db.HashSlot(key.HashSlot).GetChannelRuntimeMeta(ctx, key.ChannelID, key.ChannelType)
						if err != nil || !ok {
							b.Fatalf("%v %v", ok, err)
						}
						rows = append(rows, row)
					}
				}
				if len(rows) != len(keys) {
					b.Fatal("missing rows")
				}
			}
		})
	}
}

func runtimeMetadataBatchFixture(tb testing.TB) (*MetaDB, []ChannelRuntimeMetaReadKey) {
	store := openTestMetaStore(tb)
	tb.Cleanup(func() { store.close(tb) })
	ctx := context.Background()
	keys := make([]ChannelRuntimeMetaReadKey, 100)
	for i := range keys {
		key := ChannelRuntimeMetaReadKey{HashSlot: HashSlot((i * 73) % 256), ChannelID: fmt.Sprintf("preview-%03d", i), ChannelType: 1}
		keys[i] = key
		row := ChannelRuntimeMeta{ChannelID: key.ChannelID, ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, MinISR: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}}
		if _, err := store.db.HashSlot(key.HashSlot).UpsertChannelRuntimeMeta(ctx, row); err != nil {
			tb.Fatal(err)
		}
	}
	return store.db, keys
}
