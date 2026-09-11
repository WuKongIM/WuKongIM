package store

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIndexedCommittedReadBoundsAndNoRangeFallback(t *testing.T) {
	ctx := context.Background()
	f := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = f.Close() })
	id := ch.ChannelID{ID: "lookup", Type: 2}
	s, e := f.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, e)
	defer s.Close()
	_, e = s.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 10, ClientMsgNo: "same", FromUID: "a", Payload: []byte("deleted")}, {ID: 11, ClientMsgNo: "same", FromUID: "b", Payload: []byte("visible")}, {ID: 12, ClientMsgNo: "same", FromUID: "c", Payload: []byte("uncommitted")}}})
	require.NoError(t, e)
	for _, q := range []ReadCommittedRequest{{MessageID: 11}, {ClientMsgNo: "same"}} {
		q.MinSeq = 2
		q.MaxSeq = 2
		q.Limit = 10
		q.MaxBytes = 1024
		r, e := s.ReadCommitted(ctx, q)
		require.NoError(t, e)
		require.Len(t, r.Messages, 1)
		require.Equal(t, uint64(11), r.Messages[0].MessageID)
	}
	for _, q := range []ReadCommittedRequest{{MessageID: 10}, {MessageID: 12}, {MessageID: 999}, {ClientMsgNo: "absent"}} {
		q.MinSeq = 2
		q.MaxSeq = 2
		q.Limit = 10
		q.MaxBytes = 1024
		r, e := s.ReadCommitted(ctx, q)
		require.NoError(t, e)
		require.Empty(t, r.Messages)
	}
	_, e = s.ReadCommitted(ctx, ReadCommittedRequest{ClientMsgNo: "same", MinSeq: 1, MaxSeq: 3, Limit: 1, MaxBytes: 1024})
	require.Error(t, e)
	_, e = s.ReadCommitted(ctx, ReadCommittedRequest{MessageID: 11, MinSeq: 1, MaxSeq: 3, Limit: 1, MaxBytes: 1})
	require.Error(t, e)
	_, e = s.ReadCommitted(ctx, ReadCommittedRequest{ClientMsgNo: "same", MessageID: 11, MinSeq: 1, MaxSeq: 3, Limit: 1, MaxBytes: 1024})
	require.Error(t, e)
}
