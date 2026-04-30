package handler

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestSyncMessagesReturnsLatestPageAscending(t *testing.T) {
	store := openMessageQueryStore(t)
	id := channel.ChannelID{ID: "sync-room-1", Type: 2}
	appendQueryMessages(t, store, id,
		channel.Message{MessageID: 11, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("one")},
		channel.Message{MessageID: 12, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("two")},
		channel.Message{MessageID: 13, ChannelID: id.ID, ChannelType: id.Type, Payload: []byte("three")},
	)

	result, err := SyncMessages(store, 3, SyncMessagesRequest{
		ChannelID: id,
		Limit:     2,
	})

	require.NoError(t, err)
	require.True(t, result.HasMore)
	require.Len(t, result.Messages, 2)
	require.Equal(t, []uint64{2, 3}, []uint64{result.Messages[0].MessageSeq, result.Messages[1].MessageSeq})
	require.Equal(t, []uint64{12, 13}, []uint64{result.Messages[0].MessageID, result.Messages[1].MessageID})
}

func TestSyncMessagesPullsNextRangeAscending(t *testing.T) {
	store := openMessageQueryStore(t)
	id := channel.ChannelID{ID: "sync-room-2", Type: 2}
	appendQueryMessages(t, store, id,
		channel.Message{MessageID: 21, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 22, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 23, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 24, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 25, ChannelID: id.ID, ChannelType: id.Type},
	)

	result, err := SyncMessages(store, 5, SyncMessagesRequest{
		ChannelID: id,
		StartSeq:  2,
		EndSeq:    5,
		Limit:     10,
		PullMode:  SyncPullModeUp,
	})

	require.NoError(t, err)
	require.False(t, result.HasMore)
	require.Len(t, result.Messages, 3)
	require.Equal(t, []uint64{2, 3, 4}, []uint64{result.Messages[0].MessageSeq, result.Messages[1].MessageSeq, result.Messages[2].MessageSeq})
}

func TestSyncMessagesPullsPreviousRangeAscendingWithMore(t *testing.T) {
	store := openMessageQueryStore(t)
	id := channel.ChannelID{ID: "sync-room-3", Type: 2}
	appendQueryMessages(t, store, id,
		channel.Message{MessageID: 31, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 32, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 33, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 34, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 35, ChannelID: id.ID, ChannelType: id.Type},
	)

	result, err := SyncMessages(store, 5, SyncMessagesRequest{
		ChannelID: id,
		StartSeq:  5,
		EndSeq:    1,
		Limit:     2,
		PullMode:  SyncPullModeDown,
	})

	require.NoError(t, err)
	require.True(t, result.HasMore)
	require.Len(t, result.Messages, 2)
	require.Equal(t, []uint64{4, 5}, []uint64{result.Messages[0].MessageSeq, result.Messages[1].MessageSeq})
}

func TestSyncMessagesClampsAllDirectionsToMinAvailableSeq(t *testing.T) {
	store := openMessageQueryStore(t)
	id := channel.ChannelID{ID: "sync-room-retention", Type: 2}
	appendQueryMessages(t, store, id,
		channel.Message{MessageID: 41, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 42, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 43, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 44, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 45, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 46, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 47, ChannelID: id.ID, ChannelType: id.Type},
		channel.Message{MessageID: 48, ChannelID: id.ID, ChannelType: id.Type},
	)

	latest, err := SyncMessages(store, 8, SyncMessagesRequest{
		ChannelID:       id,
		Limit:           10,
		MinAvailableSeq: 6,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{6, 7, 8}, syncMessageSeqs(latest.Messages))

	up, err := SyncMessages(store, 8, SyncMessagesRequest{
		ChannelID:       id,
		StartSeq:        1,
		EndSeq:          9,
		Limit:           10,
		PullMode:        SyncPullModeUp,
		MinAvailableSeq: 6,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{6, 7, 8}, syncMessageSeqs(up.Messages))

	down, err := SyncMessages(store, 8, SyncMessagesRequest{
		ChannelID:       id,
		StartSeq:        8,
		EndSeq:          1,
		Limit:           10,
		PullMode:        SyncPullModeDown,
		MinAvailableSeq: 6,
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{6, 7, 8}, syncMessageSeqs(down.Messages))
}

func syncMessageSeqs(messages []channel.Message) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.MessageSeq)
	}
	return seqs
}
