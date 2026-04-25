package handler

import (
	"context"
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
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
