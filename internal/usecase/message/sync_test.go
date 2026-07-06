package message

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSyncChannelMessagesNormalizesPersonChannelAndCapsLimit(t *testing.T) {
	reader := &recordingChannelMessageReader{
		page: ChannelMessagePage{
			Messages: []SyncedMessage{{MessageID: 88, MessageSeq: 9}},
			HasMore:  true,
		},
	}
	app := New(Options{Reader: reader})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:        "u1",
		ChannelID:       "u2",
		ChannelType:     channelTypePerson,
		StartMessageSeq: 1,
		EndMessageSeq:   10,
		Limit:           20000,
		PullMode:        PullModeUp,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if !result.More {
		t.Fatalf("More = false, want true")
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != 88 {
		t.Fatalf("messages = %#v, want message 88", result.Messages)
	}
	if len(reader.queries) != 1 {
		t.Fatalf("queries = %#v, want one query", reader.queries)
	}
	wantChannel := ChannelID{ID: runtimechannelid.EncodePersonChannel("u1", "u2"), Type: channelTypePerson}
	if got := reader.queries[0]; got.ChannelID != wantChannel || got.StartSeq != 1 || got.EndSeq != 10 || got.Limit != 10000 || got.PullMode != PullModeUp {
		t.Fatalf("query = %#v, want normalized person capped-limit query", got)
	}
}

func TestSyncChannelMessagesReturnsEmptyForMissingChannelRuntime(t *testing.T) {
	app := New(Options{Reader: &recordingChannelMessageReader{err: metadb.ErrNotFound}})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if result.More || len(result.Messages) != 0 {
		t.Fatalf("result = %#v, want empty page", result)
	}
}

func TestSyncChannelMessagesRejectsMissingRequiredFields(t *testing.T) {
	app := New(Options{Reader: &recordingChannelMessageReader{}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		ChannelID:   "g1",
		ChannelType: 2,
	})

	if !errors.Is(err, ErrSyncLoginUIDRequired) {
		t.Fatalf("error = %v, want login uid required", err)
	}
}

func TestSyncChannelMessagesPropagatesReaderError(t *testing.T) {
	readerErr := errors.New("reader failed")
	app := New(Options{Reader: &recordingChannelMessageReader{err: readerErr}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		Limit:       30,
	})

	if !errors.Is(err, readerErr) {
		t.Fatalf("error = %v, want reader error", err)
	}
}

func TestSyncChannelMessagesEnrichesFullEventMeta(t *testing.T) {
	key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	store := &recordingMessageEventStore{stateRows: map[MessageEventMessageKey][]MessageEventState{
		key: {
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        EventKeyDefault,
				Status:          EventStatusClosed,
				LastMsgEventSeq: 2,
				SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`),
				EndReason:       3,
			},
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        "tool",
				Status:          EventStatusOpen,
				LastMsgEventSeq: 3,
				SnapshotPayload: []byte(`{"kind":"text","text":"tool"}`),
			},
			{
				ChannelID:       "g1",
				ChannelType:     2,
				ClientMsgNo:     "cmn-1",
				EventKey:        EventKeyFinish,
				Status:          EventStatusClosed,
				LastMsgEventSeq: 4,
			},
		},
	}}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1", MessageID: 9, Payload: []byte("base")},
	}}}
	app := New(Options{Reader: reader, EventStore: store})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "full",
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 1 {
		t.Fatalf("state calls = %#v, want one call", store.stateCalls)
	}
	if store.stateCalls[0].limit != maxMessageEventSummaryLanes || len(store.stateCalls[0].keys) != 1 || store.stateCalls[0].keys[0] != key {
		t.Fatalf("state call = %#v, want message event key with lane cap", store.stateCalls[0])
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages = %#v, want one message", result.Messages)
	}
	msg := result.Messages[0]
	if string(msg.Payload) != "base" || string(msg.StreamData) != "hello" || msg.End != 1 || msg.EndReason != 3 || msg.Error != "" {
		t.Fatalf("stream fields = payload %q stream %q end %d reason %d error %q", msg.Payload, msg.StreamData, msg.End, msg.EndReason, msg.Error)
	}
	if msg.EventHint == nil || msg.EventHint.ClientMsgNo != "cmn-1" || msg.EventHint.FromMsgEventSeq != 0 {
		t.Fatalf("event hint = %#v, want cmn-1/0", msg.EventHint)
	}
	if msg.EventMeta == nil {
		t.Fatal("EventMeta = nil, want event summary")
	}
	if !msg.EventMeta.HasEvents || !msg.EventMeta.Completed || msg.EventMeta.EventVersion != 3 || msg.EventMeta.LastMsgEventSeq != 3 || msg.EventMeta.EventCount != 2 || msg.EventMeta.OpenEventCount != 1 {
		t.Fatalf("event meta = %#v, want completed two-lane summary", msg.EventMeta)
	}
	if len(msg.EventMeta.Events) != 2 || msg.EventMeta.Events[0].EventKey != EventKeyDefault || msg.EventMeta.Events[1].EventKey != "tool" {
		t.Fatalf("event lanes = %#v, want main/tool order", msg.EventMeta.Events)
	}
	if msg.EventMeta.Events[0].Snapshot == nil || msg.EventMeta.Events[1].Snapshot == nil {
		t.Fatalf("snapshots = %#v, want decoded snapshots in full mode", msg.EventMeta.Events)
	}
}

func TestSyncChannelMessagesBasicEventMetaOmitsSnapshots(t *testing.T) {
	key := MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"}
	store := &recordingMessageEventStore{stateRows: map[MessageEventMessageKey][]MessageEventState{
		key: {{EventKey: EventKeyDefault, Status: EventStatusOpen, LastMsgEventSeq: 1, SnapshotPayload: []byte(`{"kind":"text","text":"hello"}`)}},
	}}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, EventStore: store})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "basic",
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if got := result.Messages[0].EventMeta.Events[0].Snapshot; got != nil {
		t.Fatalf("snapshot = %#v, want nil in basic mode", got)
	}
}

func TestSyncChannelMessagesSkipsEventStoreWhenSummaryModeEmpty(t *testing.T) {
	store := &recordingMessageEventStore{}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, EventStore: store})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: 2,
	})

	if err != nil {
		t.Fatalf("SyncChannelMessages() error = %v", err)
	}
	if len(store.stateCalls) != 0 {
		t.Fatalf("state calls = %#v, want none", store.stateCalls)
	}
}

func TestSyncChannelMessagesPropagatesEventStoreError(t *testing.T) {
	storeErr := errors.New("event store failed")
	store := &recordingMessageEventStore{stateErr: storeErr}
	reader := &recordingChannelMessageReader{page: ChannelMessagePage{Messages: []SyncedMessage{
		{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn-1"},
	}}}
	app := New(Options{Reader: reader, EventStore: store})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "g1",
		ChannelType:      2,
		EventSummaryMode: "basic",
	})

	if !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want event store error", err)
	}
}

type recordingChannelMessageReader struct {
	queries []ChannelMessageQuery
	page    ChannelMessagePage
	err     error
}

func (r *recordingChannelMessageReader) SyncMessages(_ context.Context, query ChannelMessageQuery) (ChannelMessagePage, error) {
	r.queries = append(r.queries, query)
	return r.page, r.err
}
