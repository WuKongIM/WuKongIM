package cmdsync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncBatchesDirectoryReadsAndKeepsGlobalOrderAndAcks(t *testing.T) {
	store := newCmdSyncStore()
	for i := 0; i < 70; i++ {
		key := CommandChannelKey{ChannelID: fmt.Sprintf("c%03d____cmd", i), ChannelType: 2}
		store.memberships = append(store.memberships, metadb.UserCMDChannelMembership{UID: "u", CommandChannelID: key.ChannelID, ChannelType: 2, StartSeq: 1})
		store.messages[key] = []SyncedMessage{{MessageID: uint64(i + 1), MessageSeq: 9, ServerTimestampMS: int64(70 - i)}}
	}
	batch := &cmdBatchStore{cmdSyncStore: store}
	app := New(Options{States: store, Messages: batch})
	got, err := app.Sync(context.Background(), SyncQuery{UID: "u", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch.sizes, []int{32, 32, 6}) {
		t.Fatalf("unbounded or serial reads: %v", batch.sizes)
	}
	if len(got.Messages) != 2 || got.Messages[0].MessageID != 70 || got.Messages[1].MessageID != 69 {
		t.Fatalf("lost global order: %+v", got.Messages)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u", LastMessageSeq: 9}); err != nil {
		t.Fatal(err)
	}
	if len(store.acks) != 2 {
		t.Fatalf("acked unreturned channels: %+v", store.acks)
	}
	for _, ack := range store.acks {
		if ack.AckSeq != 9 || (ack.CommandChannelID != "c068____cmd" && ack.CommandChannelID != "c069____cmd") {
			t.Fatalf("wrong ack: %+v", ack)
		}
	}
}

func TestSyncBatchFailureKeepsPreviousAcknowledgementGeneration(t *testing.T) {
	for _, badShape := range []bool{false, true} {
		store := newCmdSyncStore()
		for i := 0; i < 33; i++ {
			store.memberships = append(store.memberships, metadb.UserCMDChannelMembership{UID: "u", CommandChannelID: fmt.Sprintf("g%d____cmd", i), ChannelType: 2, StartSeq: 1})
		}
		records := NewSyncRecordCache(SyncRecordCacheOptions{})
		prior := []SyncRecord{{CommandChannelID: "previous____cmd", ChannelType: 2, LastReturnedMsgSeq: 8}}
		records.Replace("u", prior)
		batch := &cmdBatchStore{cmdSyncStore: store, failCall: 2, badShape: badShape}
		app := New(Options{States: store, Messages: batch, Records: records})
		if _, err := app.Sync(context.Background(), SyncQuery{UID: "u", Limit: 2}); err == nil {
			t.Fatal("accepted incomplete batch")
		}
		if !reflect.DeepEqual(records.Peek("u"), prior) {
			t.Fatal("failed sync replaced acknowledgement generation")
		}
	}
}

type cmdBatchStore struct {
	*cmdSyncStore
	sizes    []int
	failCall int
	badShape bool
}

func (s *cmdBatchStore) LoadCommandMessagesBatch(ctx context.Context, queries []CommandMessageRead) ([][]SyncedMessage, error) {
	s.sizes = append(s.sizes, len(queries))
	if len(s.sizes) == s.failCall {
		if s.badShape {
			return nil, nil
		}
		return nil, errors.New("unavailable batch")
	}
	out := make([][]SyncedMessage, len(queries))
	for i, q := range queries {
		msgs, err := s.cmdSyncStore.LoadCommandMessages(ctx, q.Key, q.FromSeq, q.Limit)
		if err != nil {
			return nil, err
		}
		out[i] = msgs
	}
	return out, nil
}
