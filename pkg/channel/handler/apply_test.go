package handler

import (
	"testing"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestApplyFetchPersistsCommittedIdempotency(t *testing.T) {
	id := core.ChannelID{ID: "c1", Type: 1}
	engine := openTestEngine(t)
	st := engine.ForChannel(KeyFromChannelID(id), id)

	payload1, err := encodeMessage(core.Message{
		MessageID:   11,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		FromUID:     "u1",
		ClientMsgNo: "m1",
		Payload:     []byte("one"),
	})
	if err != nil {
		t.Fatalf("encodeMessage() error = %v", err)
	}
	payload2, err := encodeMessage(core.Message{
		MessageID:   12,
		ChannelID:   id.ID,
		ChannelType: id.Type,
		FromUID:     "u1",
		ClientMsgNo: "m2",
		Payload:     []byte("two"),
	})
	if err != nil {
		t.Fatalf("encodeMessage() error = %v", err)
	}

	if _, err := st.Append([]core.Record{{Payload: payload1, SizeBytes: len(payload1)}}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	_, err = ApplyFetch(st, core.ApplyFetchStoreRequest{
		PreviousCommittedHW: 0,
		Records:             []core.Record{{Payload: payload2, SizeBytes: len(payload2)}},
		Checkpoint: &core.Checkpoint{
			Epoch: 1,
			HW:    2,
		},
	})
	if err != nil {
		t.Fatalf("ApplyFetch() error = %v", err)
	}

	first, ok, err := st.GetIdempotency(core.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m1",
	})
	if err != nil {
		t.Fatalf("GetIdempotency(first) error = %v", err)
	}
	if !ok || first.MessageSeq != 1 {
		t.Fatalf("first entry = %+v ok=%v, want seq=1", first, ok)
	}

	second, ok, err := st.GetIdempotency(core.IdempotencyKey{
		ChannelID:   id,
		FromUID:     "u1",
		ClientMsgNo: "m2",
	})
	if err != nil {
		t.Fatalf("GetIdempotency(second) error = %v", err)
	}
	if !ok || second.MessageSeq != 2 {
		t.Fatalf("second entry = %+v ok=%v, want seq=2", second, ok)
	}
}
