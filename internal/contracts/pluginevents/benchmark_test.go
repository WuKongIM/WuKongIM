package pluginevents

import (
	"fmt"
	"testing"
	"unsafe"
)

var benchmarkPersistAfterCommittedSink PersistAfterCommitted
var benchmarkPersistAfterCommittedScalarSink uint64
var benchmarkReceiveOfflineSink ReceiveOffline
var benchmarkReceiveOfflineScalarSink uint64

func BenchmarkPersistAfterCommittedClone(b *testing.B) {
	payloadSizes := []int{128, 1024, 16 * 1024}
	scopedCounts := []int{0, 10, 1000}
	for _, payloadSize := range payloadSizes {
		for _, scopedCount := range scopedCounts {
			name := fmt.Sprintf("payload_%d/scoped_%d", payloadSize, scopedCount)
			b.Run(name, func(b *testing.B) {
				event := benchmarkPersistAfterCommitted(payloadSize, scopedCount)
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize + scopedCount*int(unsafe.Sizeof(""))))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					clone := event.Clone()
					if len(clone.Payload) != payloadSize || len(clone.MessageScopedUIDs) != scopedCount {
						b.Fatal("invalid clone")
					}
					benchmarkPersistAfterCommittedScalarSink += uint64(clone.Payload[0])
					if scopedCount > 0 {
						benchmarkPersistAfterCommittedScalarSink += uint64(len(clone.MessageScopedUIDs[scopedCount-1]))
					}
					benchmarkPersistAfterCommittedSink = clone
				}
			})
		}
	}
}

func BenchmarkReceiveOfflineClone(b *testing.B) {
	payloadSizes := []int{128, 1024, 16 * 1024}
	scopedCounts := []int{0, 10, 1000}
	for _, payloadSize := range payloadSizes {
		for _, scopedCount := range scopedCounts {
			name := fmt.Sprintf("payload_%d/scoped_%d", payloadSize, scopedCount)
			b.Run(name, func(b *testing.B) {
				event := benchmarkReceiveOffline(payloadSize, scopedCount)
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize + scopedCount*int(unsafe.Sizeof(""))))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					clone := event.Clone()
					if len(clone.Payload) != payloadSize || len(clone.MessageScopedUIDs) != scopedCount {
						b.Fatal("invalid clone")
					}
					benchmarkReceiveOfflineScalarSink += uint64(clone.Payload[0])
					if scopedCount > 0 {
						benchmarkReceiveOfflineScalarSink += uint64(len(clone.MessageScopedUIDs[scopedCount-1]))
					}
					benchmarkReceiveOfflineSink = clone
				}
			})
		}
	}
}

func benchmarkPersistAfterCommitted(payloadSize, scopedCount int) PersistAfterCommitted {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	scopedUIDs := make([]string, scopedCount)
	for i := range scopedUIDs {
		scopedUIDs[i] = fmt.Sprintf("u%d", i)
	}
	return PersistAfterCommitted{
		MessageID:         11,
		MessageSeq:        5,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "sender",
		SenderNodeID:      1,
		SenderSessionID:   2,
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:           payload,
		RedDot:            true,
		MessageScopedUIDs: scopedUIDs,
	}
}

func benchmarkReceiveOffline(payloadSize, scopedCount int) ReceiveOffline {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	scopedUIDs := make([]string, scopedCount)
	for i := range scopedUIDs {
		scopedUIDs[i] = fmt.Sprintf("u%d", i)
	}
	return ReceiveOffline{
		MessageID:         11,
		MessageSeq:        5,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "sender",
		UID:               "bot",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:           payload,
		MessageScopedUIDs: scopedUIDs,
	}
}
