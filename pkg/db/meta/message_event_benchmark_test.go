package meta

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

var benchmarkMessageEventResult MessageEventAppendResult

func BenchmarkMessageEventAppend(b *testing.B) {
	store := openTestMetaStore(b)
	defer store.close(b)
	ctx := context.Background()
	shard := store.db.HashSlot(4)
	payload := []byte(`{"kind":"text","delta":"x"}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := shard.AppendMessageEvent(ctx, MessageEventAppend{
			ChannelID:   "bench-channel",
			ChannelType: 2,
			ClientMsgNo: "cmn-" + strconv.Itoa(i),
			EventID:     "evt-" + strconv.Itoa(i),
			EventType:   EventTypeStreamDelta,
			Payload:     payload,
			UpdatedAt:   int64(i + 1),
		})
		if err != nil {
			b.Fatal(err)
		}
		benchmarkMessageEventResult = result
	}
}

func BenchmarkMessageEventAppendIdempotent(b *testing.B) {
	store := openTestMetaStore(b)
	defer store.close(b)
	ctx := context.Background()
	shard := store.db.HashSlot(4)
	event := MessageEventAppend{
		ChannelID:   "bench-channel",
		ChannelType: 2,
		ClientMsgNo: "cmn-idempotent",
		EventID:     "evt-idempotent",
		EventType:   EventTypeStreamDelta,
		Payload:     []byte(`{"kind":"text","delta":"x"}`),
		UpdatedAt:   1,
	}
	if _, err := shard.AppendMessageEvent(ctx, event); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := shard.AppendMessageEvent(ctx, event)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkMessageEventResult = result
	}
}

func BenchmarkMessageEventWriteBatchAppend(b *testing.B) {
	for _, batchSize := range []int{1, 16, 64} {
		b.Run("batch_"+strconv.Itoa(batchSize), func(b *testing.B) {
			store := openTestMetaStore(b)
			defer store.close(b)
			payload := []byte(`{"kind":"text","delta":"x"}`)
			hashSlot := HashSlot(4)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wb := store.db.NewBatch()
				base := i * batchSize
				for j := 0; j < batchSize; j++ {
					n := base + j
					result, err := wb.AppendMessageEvent(hashSlot, MessageEventAppend{
						ChannelID:   "bench-channel",
						ChannelType: 2,
						ClientMsgNo: fmt.Sprintf("cmn-%d-%d", i, j),
						EventID:     "evt-" + strconv.Itoa(n),
						EventType:   EventTypeStreamDelta,
						Payload:     payload,
						UpdatedAt:   int64(n + 1),
					})
					if err != nil {
						b.Fatal(err)
					}
					benchmarkMessageEventResult = result
				}
				if err := wb.Commit(context.Background()); err != nil {
					b.Fatal(err)
				}
				if err := wb.Close(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(batchSize), "events/op")
		})
	}
}
