package cmdsync

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncSkipsTerminalSourcesInBothReadPaths(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			store := newCmdSyncStore()
			closed := CommandChannelKey{ChannelID: "closed____cmd", ChannelType: 2}
			live := CommandChannelKey{ChannelID: "live____cmd", ChannelType: 2}
			for _, key := range []CommandChannelKey{closed, live} {
				store.memberships = append(store.memberships, metadb.UserCMDChannelMembership{UID: "u", CommandChannelID: key.ChannelID, ChannelType: 2, StartSeq: 1})
			}
			store.messages[live] = []SyncedMessage{{MessageID: 7, MessageSeq: 3, ChannelID: live.ChannelID, ChannelType: 2}}
			reader := &terminalSourceReader{cmdSyncStore: store, closed: closed, err: ErrChannelDisbanded}
			var messages MessageStore = reader
			if batch {
				messages = &terminalSourceBatchReader{reader}
			}
			app := New(Options{States: store, Messages: messages})
			got, err := app.Sync(context.Background(), SyncQuery{UID: "u", Limit: 10})
			if err != nil || len(got.Messages) != 1 || got.Messages[0].MessageID != 7 {
				t.Fatalf("closed source blocked live command: %+v %v", got, err)
			}
			if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u", LastMessageSeq: 3}); err != nil {
				t.Fatal(err)
			}
			if len(store.acks) != 1 || store.acks[0].CommandChannelID != live.ChannelID || store.acks[0].AckSeq != 3 {
				t.Fatalf("ack crossed closed source: %+v", store.acks)
			}
			prior := []SyncRecord{{CommandChannelID: live.ChannelID, ChannelType: 2, LastReturnedMsgSeq: 3}}
			app.records.Replace("u", prior)
			reader.err = errors.New("source owner unavailable")
			if _, err := app.Sync(context.Background(), SyncQuery{UID: "u"}); err == nil {
				t.Fatal("unavailable source treated as closed")
			}
			if records := app.records.Peek("u"); len(records) != 1 || records[0] != prior[0] {
				t.Fatalf("failed read replaced ack generation: %+v", records)
			}
		})
	}
}

type terminalSourceReader struct {
	*cmdSyncStore
	closed CommandChannelKey
	err    error
}

func (s *terminalSourceReader) LoadCommandMessages(ctx context.Context, key CommandChannelKey, from uint64, limit int) ([]SyncedMessage, error) {
	if key == s.closed {
		return nil, s.err
	}
	return s.cmdSyncStore.LoadCommandMessages(ctx, key, from, limit)
}

type terminalSourceBatchReader struct{ *terminalSourceReader }

func (s *terminalSourceBatchReader) LoadCommandMessagesBatch(ctx context.Context, queries []CommandMessageRead) ([]CommandMessageReadResult, error) {
	out := make([]CommandMessageReadResult, len(queries))
	for i, q := range queries {
		out[i].Messages, out[i].Err = s.LoadCommandMessages(ctx, q.Key, q.FromSeq, q.Limit)
	}
	return out, nil
}
