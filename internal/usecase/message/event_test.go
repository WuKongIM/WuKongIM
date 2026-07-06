package message

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestAppendMessageEventNormalizesPersonChannelAndClonesPayload(t *testing.T) {
	store := &recordingMessageEventStore{
		appendResult: MessageEventAppendResult{
			ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
			ChannelType: int64(channelTypePerson),
			ClientMsgNo: "cmn-1",
			EventID:     "evt-1",
			EventKey:    EventKeyDefault,
			MsgEventSeq: 7,
			Status:      EventStatusOpen,
			State: MessageEventState{
				SnapshotPayload: []byte(`{"kind":"text","text":"hi"}`),
			},
		},
	}
	app := New(Options{
		EventStore: store,
		Now:        func() time.Time { return time.UnixMilli(1700000000123) },
	})
	payload := []byte(`{"kind":"text","delta":"hi"}`)

	result, err := app.AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID:   " u2 ",
		ChannelType: int64(channelTypePerson),
		FromUID:     " u1 ",
		MessageID:   99,
		ClientMsgNo: " cmn-1 ",
		EventID:     " evt-1 ",
		EventType:   " Stream.Delta ",
		Payload:     payload,
	})

	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if len(store.appendCalls) != 1 {
		t.Fatalf("append calls = %#v, want one call", store.appendCalls)
	}
	call := store.appendCalls[0]
	if call.ChannelID != runtimechannelid.EncodePersonChannel("u1", "u2") || call.ChannelType != int64(channelTypePerson) || call.FromUID != "u1" {
		t.Fatalf("append call channel = %#v, want normalized person channel", call)
	}
	if call.ClientMsgNo != "cmn-1" || call.EventID != "evt-1" || call.EventKey != EventKeyDefault || call.EventType != EventTypeStreamDelta {
		t.Fatalf("append call event fields = %#v, want normalized event fields", call)
	}
	if call.UpdatedAt != 1700000000123 {
		t.Fatalf("UpdatedAt = %d, want fixed now", call.UpdatedAt)
	}
	payload[0] = '['
	if string(call.Payload) != `{"kind":"text","delta":"hi"}` {
		t.Fatalf("stored payload mutated to %q", call.Payload)
	}
	result.State.SnapshotPayload[0] = '['
	if string(store.appendResult.State.SnapshotPayload) != `{"kind":"text","text":"hi"}` {
		t.Fatalf("result state was not cloned")
	}
	if result.FromUID != "u1" || result.MessageID != 99 {
		t.Fatalf("result sender/message = %q/%d, want u1/99", result.FromUID, result.MessageID)
	}
}

func TestAppendMessageEventNormalizesAgentChannel(t *testing.T) {
	store := &recordingMessageEventStore{appendResult: MessageEventAppendResult{Status: EventStatusOpen}}
	app := New(Options{EventStore: store})

	_, err := app.AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID:   "agent-a",
		ChannelType: int64(channelTypeAgent),
		FromUID:     "u1",
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventType:   EventTypeStreamOpen,
	})

	if err != nil {
		t.Fatalf("AppendMessageEvent() error = %v", err)
	}
	if got, want := store.appendCalls[0].ChannelID, runtimechannelid.EncodeAgentChannel("u1", "agent-a"); got != want {
		t.Fatalf("agent channel = %q, want %q", got, want)
	}
}

func TestAppendMessageEventRejectsMismatchedEncodedAgentChannel(t *testing.T) {
	app := New(Options{EventStore: &recordingMessageEventStore{}})

	_, err := app.AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID:   runtimechannelid.EncodeAgentChannel("u2", "agent-a"),
		ChannelType: int64(channelTypeAgent),
		FromUID:     "u1",
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventType:   EventTypeStreamOpen,
	})

	if !errors.Is(err, runtimechannelid.ErrInvalidAgentChannel) {
		t.Fatalf("error = %v, want invalid agent channel", err)
	}
}

func TestAppendMessageEventRejectsMissingFields(t *testing.T) {
	app := New(Options{EventStore: &recordingMessageEventStore{}})
	cases := []struct {
		name  string
		event MessageEventAppend
		err   error
	}{
		{name: "channel id", event: MessageEventAppend{ChannelType: 2, ClientMsgNo: "cmn", EventID: "evt", EventType: EventTypeStreamOpen}, err: ErrMessageEventChannelIDRequired},
		{name: "channel type", event: MessageEventAppend{ChannelID: "g1", ClientMsgNo: "cmn", EventID: "evt", EventType: EventTypeStreamOpen}, err: ErrMessageEventChannelTypeRequired},
		{name: "client msg no", event: MessageEventAppend{ChannelID: "g1", ChannelType: 2, EventID: "evt", EventType: EventTypeStreamOpen}, err: ErrMessageEventClientMsgNoRequired},
		{name: "event id", event: MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn", EventType: EventTypeStreamOpen}, err: ErrMessageEventIDRequired},
		{name: "event type", event: MessageEventAppend{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "cmn", EventID: "evt"}, err: ErrMessageEventTypeRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.AppendMessageEvent(context.Background(), tc.event)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestAppendMessageEventRequiresStore(t *testing.T) {
	app := New(Options{})

	_, err := app.AppendMessageEvent(context.Background(), MessageEventAppend{
		ChannelID:   "g1",
		ChannelType: 2,
		ClientMsgNo: "cmn-1",
		EventID:     "evt-1",
		EventType:   EventTypeStreamOpen,
	})

	if !errors.Is(err, ErrMessageEventStoreRequired) {
		t.Fatalf("error = %v, want store required", err)
	}
}

type recordingMessageEventStore struct {
	appendCalls  []MessageEventAppend
	appendResult MessageEventAppendResult
	appendErr    error

	stateCalls []messageEventStateCall
	stateRows  map[MessageEventMessageKey][]MessageEventState
	stateErr   error
}

type messageEventStateCall struct {
	keys  []MessageEventMessageKey
	limit int
}

func (r *recordingMessageEventStore) AppendMessageEvent(_ context.Context, event MessageEventAppend) (MessageEventAppendResult, error) {
	r.appendCalls = append(r.appendCalls, event)
	return r.appendResult, r.appendErr
}

func (r *recordingMessageEventStore) GetMessageEventStatesBatch(_ context.Context, keys []MessageEventMessageKey, limit int) (map[MessageEventMessageKey][]MessageEventState, error) {
	cp := append([]MessageEventMessageKey(nil), keys...)
	r.stateCalls = append(r.stateCalls, messageEventStateCall{keys: cp, limit: limit})
	if r.stateErr != nil {
		return nil, r.stateErr
	}
	out := make(map[MessageEventMessageKey][]MessageEventState, len(r.stateRows))
	for key, rows := range r.stateRows {
		out[key] = cloneMessageEventStates(rows)
	}
	return out, nil
}
