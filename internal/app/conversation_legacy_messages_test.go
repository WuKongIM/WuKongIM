package app

import (
	"bytes"
	"context"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationLegacyMessageReaderOwnsPayloadsAcrossReads(t *testing.T) {
	source := legacyConversationMessageBatchReader{messages: []messageusecase.SyncedMessage{{
		MessageID: 1, MessageSeq: 1, ChannelID: "g1", ChannelType: 2,
		Payload: []byte("base"), StreamData: []byte("stream"),
	}}}
	reader := conversationLegacyMessageReader{messages: messageusecase.New(messageusecase.Options{
		Reader: source, PersistedReader: source, Memberships: legacyConversationMembership{},
	})}
	query := []conversationusecase.LegacyMessageQuery{{ChannelID: "g1", ChannelType: 2, Limit: 1}}
	first, err := reader.ReadLegacyMessagesBatch(context.Background(), "u1", query)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadLegacyMessagesBatch(context.Background(), "u1", query)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Messages[0].Payload[0] = 'X'
	first[0].Messages[0].StreamData[0] = 'Y'
	for _, msg := range []messageusecase.SyncedMessage{source.messages[0], {
		Payload: second[0].Messages[0].Payload, StreamData: second[0].Messages[0].StreamData,
	}} {
		if string(msg.Payload) != "base" || string(msg.StreamData) != "stream" {
			t.Fatal("one response aliased the source or a different response")
		}
	}
}

func TestConversationLegacyMessageReaderAllocationBudget(t *testing.T) {
	source := legacyConversationMessageBatchReader{messages: make([]messageusecase.SyncedMessage, 10)}
	for i := range source.messages {
		source.messages[i] = messageusecase.SyncedMessage{MessageSeq: uint64(i + 1), ChannelID: "g1", ChannelType: 2, Payload: []byte("base"), StreamData: []byte("stream")}
	}
	reader := conversationLegacyMessageReader{messages: messageusecase.New(messageusecase.Options{Reader: source, PersistedReader: source, Memberships: legacyConversationMembership{}})}
	query := []conversationusecase.LegacyMessageQuery{{ChannelID: "g1", ChannelType: 2, Limit: 10}}
	allocs := testing.AllocsPerRun(100, func() {
		result, err := reader.ReadLegacyMessagesBatch(context.Background(), "u1", query)
		if err != nil || len(result) != 1 || len(result[0].Messages) != 10 {
			t.Fatalf("read = %v, %v", result, err)
		}
	})
	// Allow response bookkeeping plus one ownership copy of each base/stream
	// payload. A second per-message copy in the adapter exceeds this budget.
	if allocs > 35 {
		t.Fatalf("ten-message read allocated %.0f objects, budget 35", allocs)
	}
}

// BenchmarkConversationLegacyMessageReader includes the real message usecase
// ownership boundary and the sibling adapter, with deterministic in-memory rows.
func BenchmarkConversationLegacyMessageReader(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{{"256B", 256}, {"4KiB", 4096}} {
		b.Run(size.name, func(b *testing.B) {
			source := legacyConversationMessageBatchReader{messages: make([]messageusecase.SyncedMessage, 10)}
			for i := range source.messages {
				source.messages[i] = messageusecase.SyncedMessage{MessageID: uint64(i + 1), MessageSeq: uint64(i + 1), ChannelID: "g1", ChannelType: 2,
					Payload: bytes.Repeat([]byte{'p'}, size.bytes), StreamData: bytes.Repeat([]byte{'s'}, size.bytes)}
			}
			reader := conversationLegacyMessageReader{messages: messageusecase.New(messageusecase.Options{Reader: source, PersistedReader: source, Memberships: legacyConversationMembership{}})}
			query := []conversationusecase.LegacyMessageQuery{{ChannelID: "g1", ChannelType: 2, Limit: 10}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := reader.ReadLegacyMessagesBatch(context.Background(), "u1", query)
				if err != nil || len(result) != 1 || len(result[0].Messages) != 10 {
					b.Fatalf("read = %v, %v", result, err)
				}
			}
		})
	}
}

func TestConversationLegacyMessageReaderIncludesOldStreamEventFields(t *testing.T) {
	messageReader := legacyConversationMessageBatchReader{messages: []messageusecase.SyncedMessage{{
		Setting: 2, MessageID: 9, MessageSeq: 3, ClientMsgNo: "c1",
		ChannelID: "g1", ChannelType: 2, Payload: []byte("base"),
	}}}
	eventKey := messageusecase.MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "c1"}
	messages := messageusecase.New(messageusecase.Options{
		Reader:          messageReader,
		PersistedReader: messageReader,
		Memberships:     legacyConversationMembership{},
		EventStore: legacyConversationEventStore{states: map[messageusecase.MessageEventMessageKey][]messageusecase.MessageEventState{
			eventKey: {
				{EventKey: messageusecase.EventKeyDefault, Status: messageusecase.EventStatusClosed, LastMsgEventSeq: 2, SnapshotPayload: []byte(`{"kind":"text","text":"done"}`), EndReason: 3},
				{EventKey: messageusecase.EventKeyFinish, Status: messageusecase.EventStatusClosed, LastMsgEventSeq: 3},
			},
		}},
	})
	reader := conversationLegacyMessageReader{messages: messages}

	result, err := reader.ReadLegacyMessagesBatch(context.Background(), "u1", []conversationusecase.LegacyMessageQuery{{
		ChannelID: "g1", ChannelType: 2, Limit: 1,
	}})
	if err != nil {
		t.Fatalf("ReadLegacyMessagesBatch(): %v", err)
	}
	if len(result) != 1 || len(result[0].Messages) != 1 {
		t.Fatalf("result = %#v, want one message", result)
	}
	msg := result[0].Messages[0]
	if msg.End != 1 || msg.EndReason != 3 || string(msg.StreamData) != "done" {
		t.Fatalf("legacy stream fields = %#v", msg)
	}
	if msg.EventMeta == nil || !msg.EventMeta.Completed || len(msg.EventMeta.Events) != 1 || msg.EventMeta.Events[0].Snapshot == nil {
		t.Fatalf("legacy event meta = %#v", msg.EventMeta)
	}
	if msg.EventHint == nil || msg.EventHint.ClientMsgNo != "c1" {
		t.Fatalf("legacy event hint = %#v", msg.EventHint)
	}
}

type legacyConversationMessageBatchReader struct {
	messages []messageusecase.SyncedMessage
}

func (r legacyConversationMessageBatchReader) SyncMessages(context.Context, messageusecase.ChannelMessageQuery) (messageusecase.ChannelMessagePage, error) {
	return messageusecase.ChannelMessagePage{Messages: r.messages}, nil
}

func (r legacyConversationMessageBatchReader) SyncMessagesBatch(_ context.Context, queries []messageusecase.ChannelMessageQuery) ([]messageusecase.ChannelMessageReadResult, error) {
	result := make([]messageusecase.ChannelMessageReadResult, len(queries))
	for index := range result {
		result[index].Page.Messages = append([]messageusecase.SyncedMessage(nil), r.messages...)
	}
	return result, nil
}

type legacyConversationMembership struct{}

func (legacyConversationMembership) GetUserChannelMembership(_ context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	return metadb.UserChannelMembership{UID: uid, ChannelID: channelID, ChannelType: channelType, JoinSeq: 1}, true, nil
}

type legacyConversationEventStore struct {
	states map[messageusecase.MessageEventMessageKey][]messageusecase.MessageEventState
}

func (legacyConversationEventStore) AppendMessageEvent(context.Context, messageusecase.MessageEventAppend) (messageusecase.MessageEventAppendResult, error) {
	return messageusecase.MessageEventAppendResult{}, nil
}

func (s legacyConversationEventStore) GetMessageEventStatesBatch(_ context.Context, _ []messageusecase.MessageEventMessageKey, _ int) (map[messageusecase.MessageEventMessageKey][]messageusecase.MessageEventState, error) {
	return s.states, nil
}
