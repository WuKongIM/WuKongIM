package handler

import (
	"errors"
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestLoadMsgUsesExplicitCommittedHW(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	engine := openTestEngine(t)
	st := engine.ForChannel(KeyFromChannelID(id), id)
	mustAppendEncodedMessages(t, st,
		core.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m1", Payload: []byte("one")},
		core.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m2", Payload: []byte("two")},
		core.Message{MessageID: 13, ChannelID: id.ID, ChannelType: id.Type, FromUID: "u1", ClientMsgNo: "m3", Payload: []byte("three")},
	)
	msg, err := LoadMsg(st, 2, 2)
	if err != nil {
		t.Fatalf("LoadMsg(2, 2) error = %v", err)
	}
	if string(msg.Payload) != "two" {
		t.Fatalf("Payload = %q, want %q", msg.Payload, "two")
	}
	if msg.MessageSeq != 2 {
		t.Fatalf("MessageSeq = %d, want 2", msg.MessageSeq)
	}

	_, err = LoadMsg(st, 2, 3)
	if !errors.Is(err, core.ErrMessageNotFound) {
		t.Fatalf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestLoadNextRangeMsgsCapsByExplicitCommittedHW(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	engine := openTestEngine(t)
	st := engine.ForChannel(KeyFromChannelID(id), id)
	mustAppendEncodedMessages(t, st,
		core.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("one")},
		core.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("two")},
		core.Message{MessageID: 13, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("three")},
	)
	msgs, err := LoadNextRangeMsgs(st, 2, 1, 0, 10)
	if err != nil {
		t.Fatalf("LoadNextRangeMsgs() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].MessageSeq != 1 || msgs[1].MessageSeq != 2 {
		t.Fatalf("seqs = %+v", msgs)
	}
}

func TestLoadNextRangeMsgsUnlimitedReadsAcrossBatches(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	engine := openTestEngine(t)
	st := engine.ForChannel(KeyFromChannelID(id), id)

	messages := make([]core.Message, 0, seqReadChunkLimit+3)
	for i := 0; i < seqReadChunkLimit+3; i++ {
		messages = append(messages, core.Message{
			MessageID:   uint64(i + 1),
			ChannelID:   id.ID,
			ChannelType: id.Type,
			Payload:     []byte{byte(i % 251)},
		})
	}
	mustAppendEncodedMessages(t, st, messages...)
	msgs, err := LoadNextRangeMsgs(st, uint64(len(messages)), 1, 0, 0)
	if err != nil {
		t.Fatalf("LoadNextRangeMsgs() error = %v", err)
	}
	if len(msgs) != len(messages) {
		t.Fatalf("len(msgs) = %d, want %d", len(msgs), len(messages))
	}
	if msgs[0].MessageSeq != 1 || msgs[len(msgs)-1].MessageSeq != uint64(len(messages)) {
		t.Fatalf("seq bounds = first:%d last:%d", msgs[0].MessageSeq, msgs[len(msgs)-1].MessageSeq)
	}
}

func TestMessagesFromLogRecordsPreserveActualOffsets(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	first := mustEncodeMessagePayload(t, core.Message{
		MessageID:   11,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Payload:     []byte("one"),
	})
	second := mustEncodeMessagePayload(t, core.Message{
		MessageID:   12,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		Payload:     []byte("two"),
	})

	msgs, err := messagesFromLogRecords([]store.LogRecord{
		{Offset: 8, Payload: first},
		{Offset: 10, Payload: second},
	}, 11, 0)
	if err != nil {
		t.Fatalf("messagesFromLogRecords() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].MessageSeq != 9 || msgs[1].MessageSeq != 11 {
		t.Fatalf("seqs = [%d %d], want [9 11]", msgs[0].MessageSeq, msgs[1].MessageSeq)
	}
}

func TestLoadMsgUsesStructuredSequenceLookup(t *testing.T) {
	st := &fakeSequenceReadStore{
		bySeq: map[uint64]core.Message{
			2: {MessageID: 12, MessageSeq: 2, Payload: []byte("two")},
		},
	}

	msg, err := loadMsgFromStore(st, 2, 2)
	if err != nil {
		t.Fatalf("loadMsgFromStore() error = %v", err)
	}
	if msg.MessageID != 12 || msg.MessageSeq != 2 {
		t.Fatalf("message = %+v", msg)
	}
	if st.getCalls != 1 {
		t.Fatalf("GetMessageBySeq() calls = %d, want 1", st.getCalls)
	}
	if st.listCalls != 0 {
		t.Fatalf("ListMessagesBySeq() calls = %d, want 0", st.listCalls)
	}
}

func mustEncodeMessagePayload(t *testing.T, msg core.Message) []byte {
	t.Helper()
	payload, err := encodeMessage(msg)
	if err != nil {
		t.Fatalf("encodeMessage() error = %v", err)
	}
	return payload
}

type fakeSequenceReadStore struct {
	bySeq     map[uint64]core.Message
	getCalls  int
	listCalls int
}

func (f *fakeSequenceReadStore) GetMessageBySeq(seq uint64) (core.Message, bool, error) {
	f.getCalls++
	msg, ok := f.bySeq[seq]
	return msg, ok, nil
}

func (f *fakeSequenceReadStore) ListMessagesBySeq(fromSeq uint64, limit int, maxBytes int, reverse bool) ([]core.Message, error) {
	f.listCalls++
	return nil, nil
}
