package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestApplyFetchAppendsAtExplicitBaseSeq(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), []Record{{ID: 1, ClientMsgNo: "c-1", FromUID: "u1", Payload: []byte("one")}}, AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}

	checkpoint := Checkpoint{Epoch: 2, LogStartOffset: 0, HW: 3}
	epochPoint := EpochPoint{Epoch: 2, StartOffset: 2}
	result, err := log.ApplyFetch(context.Background(), ApplyFetchRequest{
		BaseSeq:    2,
		Records:    []Record{{ID: 2, ClientMsgNo: "c-2", FromUID: "u2", Payload: []byte("two")}, {ID: 3, ClientMsgNo: "c-3", FromUID: "u3", Payload: []byte("three")}},
		Checkpoint: &checkpoint,
		EpochPoint: &epochPoint,
	})
	if err != nil {
		t.Fatalf("ApplyFetch(): %v", err)
	}
	if result.BaseSeq != 2 || result.LastSeq != 3 || result.Count != 2 {
		t.Fatalf("result = %#v, want base=2 last=3 count=2", result)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 3 {
		t.Fatalf("LEO = %d, want 3", leo)
	}
	gotCheckpoint, ok, err := log.LoadCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if !ok || gotCheckpoint != checkpoint {
		t.Fatalf("checkpoint = (%+v, %v), want (%+v, true)", gotCheckpoint, ok, checkpoint)
	}
	history, ok, err := log.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory(): %v", err)
	}
	if !ok || len(history) != 1 || history[0] != epochPoint {
		t.Fatalf("history = (%+v, %v), want [%+v]", history, ok, epochPoint)
	}
}

func TestApplyFetchRejectsBaseSeqMismatch(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if _, err := log.Append(context.Background(), testRecords(10, "one"), AppendOptions{}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	_, err := log.ApplyFetch(context.Background(), ApplyFetchRequest{
		BaseSeq: 3,
		Records: []Record{
			{ID: 11, Payload: []byte("two")},
		},
	})
	if !errors.Is(err, dberrors.ErrConflict) {
		t.Fatalf("ApplyFetch() err = %v, want conflict", err)
	}
	leo, err := log.LEO(context.Background())
	if err != nil {
		t.Fatalf("LEO(): %v", err)
	}
	if leo != 1 {
		t.Fatalf("LEO = %d, want 1", leo)
	}
}
