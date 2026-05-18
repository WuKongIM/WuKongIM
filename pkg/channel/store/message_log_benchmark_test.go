package store

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func BenchmarkChannelStoreAppend(b *testing.B) {
	for _, recordsPerAppend := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("records=%d", recordsPerAppend), func(b *testing.B) {
			st := newTestChannelStore(b)
			records, mutable := benchmarkAppendRecords(b, st, recordsPerAppend)

			b.ReportAllocs()
			b.SetBytes(int64(recordByteCount(records)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkStampAppendRecords(records, mutable, i*recordsPerAppend)
				if _, err := st.Append(records); err != nil {
					b.Fatalf("Append() error = %v", err)
				}
			}
			elapsed := b.Elapsed().Seconds()
			b.ReportMetric(float64(b.N)/elapsed, "appends/s")
			b.ReportMetric(float64(b.N*recordsPerAppend)/elapsed, "records/s")
		})
	}
}

func BenchmarkChannelStoreAppendParallel(b *testing.B) {
	st := newTestChannelStore(b)
	records, mutable := benchmarkAppendRecords(b, st, 1)
	var sequence atomic.Uint64

	b.ReportAllocs()
	b.SetBytes(int64(recordByteCount(records)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		localRecords := benchmarkCloneRecords(records)
		for pb.Next() {
			messageID := sequence.Add(1)
			benchmarkStampAppendRecords(localRecords, mutable, int(messageID-1))
			if _, err := st.Append(localRecords); err != nil {
				b.Fatalf("Append() error = %v", err)
			}
		}
	})
	elapsed := b.Elapsed().Seconds()
	b.ReportMetric(float64(b.N)/elapsed, "appends/s")
	b.ReportMetric(float64(b.N)/elapsed, "records/s")
}

func BenchmarkChannelStoreAppendParallelChannels(b *testing.B) {
	for _, channelCount := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("channels=%d", channelCount), func(b *testing.B) {
			engine := openTestEngine(b)
			stores := make([]*ChannelStore, 0, channelCount)
			for i := 0; i < channelCount; i++ {
				key, id := testChannelStoreIdentity(fmt.Sprintf("bench-channel-%03d", i))
				stores = append(stores, engine.ForChannel(key, id))
			}

			records, mutable := benchmarkAppendRecords(b, stores[0], 1)
			var sequence atomic.Uint64
			var channelCursor atomic.Uint64

			b.ReportAllocs()
			b.SetBytes(int64(recordByteCount(records)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				localRecords := benchmarkCloneRecords(records)
				for pb.Next() {
					messageID := sequence.Add(1)
					st := stores[int(channelCursor.Add(1)-1)%len(stores)]
					benchmarkStampAppendRecordsForStore(localRecords, mutable, st, messageID)
					if _, err := st.Append(localRecords); err != nil {
						b.Fatalf("Append() error = %v", err)
					}
				}
			})
			elapsed := b.Elapsed().Seconds()
			b.ReportMetric(float64(b.N)/elapsed, "appends/s")
			b.ReportMetric(float64(b.N)/elapsed, "records/s")
		})
	}
}

func benchmarkCloneRecords(records []channel.Record) []channel.Record {
	cloned := make([]channel.Record, len(records))
	for i := range records {
		cloned[i] = records[i]
		cloned[i].Payload = append([]byte(nil), records[i].Payload...)
	}
	return cloned
}

type benchmarkAppendMutableFields struct {
	clientMsgNoOffset int
	channelIDOffset   int
	channelIDLen      int
}

func benchmarkAppendRecords(tb testing.TB, st *ChannelStore, recordsPerAppend int) ([]channel.Record, []benchmarkAppendMutableFields) {
	tb.Helper()

	records := make([]channel.Record, 0, recordsPerAppend)
	mutable := make([]benchmarkAppendMutableFields, 0, recordsPerAppend)
	for i := 0; i < recordsPerAppend; i++ {
		messageID := uint64(i + 1)
		payload := mustEncodeStoreMessage(tb, channel.Message{
			MessageID:   messageID,
			ClientMsgNo: fmt.Sprintf("%020d", messageID),
			FromUID:     "bench-u1",
			ChannelID:   st.id.ID,
			ChannelType: st.id.Type,
			Payload:     []byte("bench-payload"),
		})
		clientMsgNoOffset, channelIDOffset, channelIDLen := benchmarkCompatibilityMutableOffsets(tb, payload)
		records = append(records, channel.Record{
			ID:        messageID,
			Payload:   payload,
			SizeBytes: len(payload),
		})
		mutable = append(mutable, benchmarkAppendMutableFields{
			clientMsgNoOffset: clientMsgNoOffset,
			channelIDOffset:   channelIDOffset,
			channelIDLen:      channelIDLen,
		})
	}
	return records, mutable
}

func benchmarkStampAppendRecords(records []channel.Record, mutable []benchmarkAppendMutableFields, base int) {
	for i := range records {
		messageID := uint64(base + i + 1)
		records[i].ID = messageID
		binary.BigEndian.PutUint64(records[i].Payload[1:9], messageID)
		benchmarkWriteUint20(records[i].Payload[mutable[i].clientMsgNoOffset:], messageID)
	}
}

func benchmarkStampAppendRecordsForStore(records []channel.Record, mutable []benchmarkAppendMutableFields, st *ChannelStore, messageID uint64) {
	benchmarkStampAppendRecords(records, mutable, int(messageID-1))
	for i := range records {
		copy(records[i].Payload[mutable[i].channelIDOffset:mutable[i].channelIDOffset+mutable[i].channelIDLen], st.id.ID)
		records[i].Payload[12] = st.id.Type
	}
}

func benchmarkWriteUint20(dst []byte, value uint64) {
	if len(dst) < 20 {
		return
	}
	for i := 19; i >= 0; i-- {
		dst[i] = byte('0' + value%10)
		value /= 10
	}
}

func benchmarkCompatibilityMutableOffsets(tb testing.TB, payload []byte) (clientMsgNoOffset int, channelIDOffset int, channelIDLen int) {
	tb.Helper()

	pos := channel.DurableMessageHeaderSize
	msgKeyOffset, msgKeyLen := benchmarkCompatibilityStringOffset(tb, payload, pos)
	clientMsgNoLenOffset := msgKeyOffset + msgKeyLen
	clientMsgNoOffset, clientMsgNoLen := benchmarkCompatibilityStringOffset(tb, payload, clientMsgNoLenOffset)
	if clientMsgNoLen != 20 {
		tb.Fatalf("clientMsgNo length = %d, want 20", clientMsgNoLen)
	}
	streamNoLenOffset := clientMsgNoOffset + clientMsgNoLen
	streamNoOffset, streamNoLen := benchmarkCompatibilityStringOffset(tb, payload, streamNoLenOffset)
	channelIDLenOffset := streamNoOffset + streamNoLen
	channelIDOffset, channelIDLen = benchmarkCompatibilityStringOffset(tb, payload, channelIDLenOffset)
	return clientMsgNoOffset, channelIDOffset, channelIDLen
}

func benchmarkCompatibilityStringOffset(tb testing.TB, payload []byte, lenOffset int) (int, int) {
	tb.Helper()
	if len(payload)-lenOffset < 4 {
		tb.Fatalf("compatibility payload too short at offset %d", lenOffset)
	}
	fieldLen := int(binary.BigEndian.Uint32(payload[lenOffset : lenOffset+4]))
	fieldOffset := lenOffset + 4
	if len(payload)-fieldOffset < fieldLen {
		tb.Fatalf("compatibility field at offset %d length %d exceeds payload size %d", fieldOffset, fieldLen, len(payload))
	}
	return fieldOffset, fieldLen
}
