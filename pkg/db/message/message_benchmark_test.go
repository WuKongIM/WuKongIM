package message

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func BenchmarkChannelLogAppend(b *testing.B) {
	for _, recordsPerAppend := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("records=%d", recordsPerAppend), func(b *testing.B) {
			log, closeFn := openBenchmarkLog(b, "bench-append")
			defer closeFn()
			payload := []byte("benchmark-payload")
			nextID := uint64(1)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				records := make([]Record, recordsPerAppend)
				for j := range records {
					id := nextID
					nextID++
					records[j] = Record{ID: id, ClientMsgNo: fmt.Sprintf("c-%020d", id), FromUID: "bench-u1", Payload: payload}
				}
				if _, err := log.Append(context.Background(), records, AppendOptions{}); err != nil {
					b.Fatalf("Append(): %v", err)
				}
			}
			b.ReportMetric(float64(b.N*recordsPerAppend), "records")
		})
	}
}

func BenchmarkChannelLogAppendParallel(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-parallel")
	defer closeFn()
	var nextID atomic.Uint64
	payload := []byte("benchmark-payload")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := nextID.Add(1)
			_, err := log.Append(context.Background(), []Record{{
				ID:          id,
				ClientMsgNo: fmt.Sprintf("c-%020d", id),
				FromUID:     "bench-u1",
				Payload:     payload,
			}}, AppendOptions{})
			if err != nil {
				b.Fatalf("Append(): %v", err)
			}
		}
	})
}

func BenchmarkChannelLogRead(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-read")
	defer closeFn()
	seedBenchmarkMessages(b, log, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := log.Read(context.Background(), 1, ReadOptions{Limit: 100, MaxBytes: 1 << 20}); err != nil {
			b.Fatalf("Read(): %v", err)
		}
	}
}

func BenchmarkChannelLogGetByMessageID(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-id")
	defer closeFn()
	seedBenchmarkMessages(b, log, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint64(i%1000 + 1)
		if _, ok, err := log.GetByMessageID(context.Background(), id); err != nil || !ok {
			b.Fatalf("GetByMessageID(%d) = ok %v err %v", id, ok, err)
		}
	}
}

func BenchmarkChannelLogRetentionTrim(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-retention")
	defer closeFn()
	payload := []byte("benchmark-payload")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint64(i + 1)
		if _, err := log.Append(context.Background(), []Record{{ID: seq, ClientMsgNo: fmt.Sprintf("c-%020d", seq), FromUID: "bench-u1", Payload: payload}}, AppendOptions{}); err != nil {
			b.Fatalf("Append(): %v", err)
		}
		if _, err := log.TrimPrefixThrough(context.Background(), seq); err != nil {
			b.Fatalf("TrimPrefixThrough(): %v", err)
		}
	}
}

func openBenchmarkLog(b *testing.B, key ChannelKey) (*ChannelLog, func()) {
	b.Helper()
	eng, err := engine.Open(b.TempDir(), engine.Options{})
	if err != nil {
		b.Fatalf("engine.Open(): %v", err)
	}
	db := NewDB(eng)
	return db.Channel(key, ChannelID{ID: string(key), Type: 1}), func() {
		if err := eng.Close(); err != nil {
			b.Fatalf("engine.Close(): %v", err)
		}
	}
}

func seedBenchmarkMessages(b *testing.B, log *ChannelLog, count int) {
	b.Helper()
	records := make([]Record, count)
	payload := []byte("benchmark-payload")
	for i := range records {
		id := uint64(i + 1)
		records[i] = Record{ID: id, ClientMsgNo: fmt.Sprintf("c-%020d", id), FromUID: "bench-u1", Payload: payload}
	}
	if _, err := log.Append(context.Background(), records, AppendOptions{}); err != nil {
		b.Fatalf("Append(): %v", err)
	}
}
