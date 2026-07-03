package handler

import (
	"context"
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func TestFetchFromSeqOneReturnsCommittedMessagesOnly(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, engine := newAppendService(t, id)
	st := engine.ForChannel(KeyFromChannelID(id), id)

	mustAppendEncodedMessages(t, st,
		core.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("one")},
		core.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m2", Payload: []byte("two")},
		core.Message{MessageID: 13, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m3", Payload: []byte("three")},
	)
	rt.channels[KeyFromChannelID(id)].status.LogStartOffset = 0
	rt.channels[KeyFromChannelID(id)].status.HW = 2

	result, err := svc.Fetch(context.Background(), core.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(result.Messages))
	}
	if result.NextSeq != 3 {
		t.Fatalf("NextSeq = %d, want 3", result.NextSeq)
	}
	if result.CommittedSeq != 2 {
		t.Fatalf("CommittedSeq = %d, want 2", result.CommittedSeq)
	}
	if result.Messages[0].FromUID != "u1" || result.Messages[0].ClientMsgNo != "m1" {
		t.Fatalf("first message = %+v", result.Messages[0])
	}
}

func TestFetchClampsStartBelowRetentionFloor(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, engine := newAppendService(t, id)
	st := engine.ForChannel(KeyFromChannelID(id), id)

	mustAppendEncodedMessages(t, st,
		core.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("one")},
		core.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("two")},
		core.Message{MessageID: 13, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("three")},
		core.Message{MessageID: 14, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("four")},
		core.Message{MessageID: 15, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("five")},
		core.Message{MessageID: 16, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("six")},
		core.Message{MessageID: 17, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("seven")},
		core.Message{MessageID: 18, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("eight")},
		core.Message{MessageID: 19, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("nine")},
		core.Message{MessageID: 20, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("ten")},
	)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.HW = 10
	handle.status.RetentionThroughSeq = 5
	handle.status.MinAvailableSeq = 6

	result, err := svc.Fetch(context.Background(), core.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if result.RetentionThroughSeq != 5 || result.MinAvailableSeq != 6 {
		t.Fatalf("retention fields = (%d, %d), want (5, 6)", result.RetentionThroughSeq, result.MinAvailableSeq)
	}
	if len(result.Messages) != 5 || result.Messages[0].MessageSeq != 6 {
		t.Fatalf("Messages = %+v, want clamped fetch from seq 6", result.Messages)
	}
	for _, msg := range result.Messages {
		if msg.MessageSeq < 6 {
			t.Fatalf("Messages include seq below floor: %+v", result.Messages)
		}
	}
}

func TestFetchEmptyResultIncludesRetentionState(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, _ := newAppendService(t, id)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.HW = 5
	handle.status.RetentionThroughSeq = 4
	handle.status.MinAvailableSeq = 5

	result, err := svc.Fetch(context.Background(), core.FetchRequest{
		ChannelID: id,
		FromSeq:   6,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if result.NextSeq != 6 || result.CommittedSeq != 5 {
		t.Fatalf("empty result offsets = %+v, want NextSeq 6 and CommittedSeq 5", result)
	}
	if result.RetentionThroughSeq != 4 || result.MinAvailableSeq != 5 {
		t.Fatalf("retention fields = (%d, %d), want (4, 5)", result.RetentionThroughSeq, result.MinAvailableSeq)
	}
}

func TestFetchRejectsInvalidBudget(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, _, _ := newAppendService(t, id)

	_, err := svc.Fetch(context.Background(), core.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     1,
		MaxBytes:  0,
	})
	if err != core.ErrInvalidFetchBudget {
		t.Fatalf("expected ErrInvalidFetchBudget, got %v", err)
	}
}

func TestFetchReturnsNotReadyWhenCommitHWIsProvisional(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	svc, rt, engine := newAppendService(t, id)
	st := engine.ForChannel(KeyFromChannelID(id), id)

	mustAppendEncodedMessages(t, st,
		core.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("one")},
		core.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("two")},
	)
	handle := rt.channels[KeyFromChannelID(id)]
	handle.status.HW = 2
	handle.status.CheckpointHW = 1
	handle.status.CommitReady = false

	_, err := svc.Fetch(context.Background(), core.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	if err != core.ErrNotReady {
		t.Fatalf("expected ErrNotReady, got %v", err)
	}
}

func TestFetchMessagesFromStoreUsesStructuredScan(t *testing.T) {
	st := &fakeFetchStore{
		messages: []core.Message{
			{MessageID: 11, MessageSeq: 1, Payload: []byte("one")},
			{MessageID: 12, MessageSeq: 2, Payload: []byte("two")},
			{MessageID: 13, MessageSeq: 3, Payload: []byte("three")},
		},
	}

	result, err := fetchMessagesFromStore(st, 2, 1, 10, 1024)
	if err != nil {
		t.Fatalf("fetchMessagesFromStore() error = %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(result.Messages))
	}
	if result.NextSeq != 3 {
		t.Fatalf("NextSeq = %d, want 3", result.NextSeq)
	}
	if st.listCalls != 1 {
		t.Fatalf("ListMessagesBySeq() calls = %d, want 1", st.listCalls)
	}
}

func mustAppendEncodedMessages(t *testing.T, st interface {
	Append([]core.Record) (uint64, error)
}, messages ...core.Message) {
	t.Helper()

	records := make([]core.Record, 0, len(messages))
	for _, msg := range messages {
		payload, err := encodeMessage(msg)
		if err != nil {
			t.Fatalf("encodeMessage() error = %v", err)
		}
		records = append(records, core.Record{Payload: payload, SizeBytes: len(payload)})
	}
	if _, err := st.Append(records); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

type fakeFetchStore struct {
	messages  []core.Message
	listCalls int
}

func (f *fakeFetchStore) ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]core.Message, error) {
	f.listCalls++
	return f.messages, nil
}
