package message

import (
	"context"
	"strconv"
	"testing"
)

var benchmarkSyncResult SyncChannelMessagesResult

func BenchmarkSyncChannelMessagesEventEnrichment(b *testing.B) {
	for _, tc := range []struct {
		name          string
		totalMessages int
		streamEvery   int
	}{
		{name: "all_stream_100", totalMessages: 100, streamEvery: 1},
		{name: "mixed_1000_stream_10pct", totalMessages: 1000, streamEvery: 10},
		{name: "ordinary_1000", totalMessages: 1000, streamEvery: 0},
	} {
		b.Run(tc.name, func(b *testing.B) {
			reader, store := benchmarkSyncFixtures(tc.totalMessages, tc.streamEvery)
			app := New(Options{Reader: reader, EventStore: store})
			query := SyncChannelMessagesQuery{
				LoginUID:         "u1",
				ChannelID:        "g1",
				ChannelType:      2,
				Limit:            tc.totalMessages,
				IncludeEventMeta: true,
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := app.SyncChannelMessages(context.Background(), query)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkSyncResult = result
			}
		})
	}
}

func benchmarkSyncFixtures(totalMessages int, streamEvery int) (*recordingChannelMessageReader, *recordingMessageEventStore) {
	messages := make([]SyncedMessage, 0, totalMessages)
	rows := make(map[MessageEventMessageKey][]MessageEventState, totalMessages)
	for i := 0; i < totalMessages; i++ {
		clientMsgNo := "cmn-" + strconv.Itoa(i)
		msg := SyncedMessage{
			ChannelID:   "g1",
			ChannelType: 2,
			ClientMsgNo: clientMsgNo,
			MessageID:   uint64(i + 1),
			MessageSeq:  uint64(i + 1),
			Payload:     []byte("base"),
		}
		if streamEvery > 0 && i%streamEvery == 0 {
			msg.Setting = legacySettingStream
			key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: clientMsgNo}
			rows[key] = []MessageEventState{{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     clientMsgNo,
				EventKey:        EventKeyDefault,
				Status:          EventStatusOpen,
				LastMsgEventSeq: 1,
				SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`),
				LastOccurredAt:  int64(i + 1),
			}}
		}
		messages = append(messages, msg)
	}
	return &recordingChannelMessageReader{page: ChannelMessagePage{Messages: messages}}, &recordingMessageEventStore{stateRows: rows}
}
