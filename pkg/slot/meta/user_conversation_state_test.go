package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type userConversationShardStore interface {
	GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (UserConversationState, error)
	UpsertUserConversationState(ctx context.Context, state UserConversationState) error
	TouchUserConversationActiveAt(ctx context.Context, uid, channelID string, channelType int64, activeAt int64) error
	ClearUserConversationActiveAt(ctx context.Context, uid string, keys []ConversationKey) error
	ListUserConversationActive(ctx context.Context, uid string, limit int) ([]UserConversationState, error)
	ListUserConversationStatePage(ctx context.Context, uid string, after ConversationCursor, limit int) ([]UserConversationState, ConversationCursor, bool, error)
	UpsertChannelUpdateLog(ctx context.Context, entry ChannelUpdateLog) error
	BatchGetChannelUpdateLogs(ctx context.Context, keys []ConversationKey) (map[ConversationKey]ChannelUpdateLog, error)
	DeleteChannelUpdateLogs(ctx context.Context, keys []ConversationKey) error
}

func openConversationTestShard(t *testing.T) userConversationShardStore {
	t.Helper()

	db := openTestDB(t)
	shard, ok := any(db.ForSlot(1)).(userConversationShardStore)
	require.True(t, ok, "user conversation shard store methods missing")
	return shard
}

func TestShardListUserConversationActiveReturnsActiveAtDesc(t *testing.T) {
	ctx := context.Background()
	shard := openConversationTestShard(t)

	states := []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g3", ChannelType: 2, ActiveAt: 0, UpdatedAt: 30},
		{UID: "u1", ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u2", ChannelID: "g1", ChannelType: 2, ActiveAt: 500, UpdatedAt: 50},
	}
	for _, state := range states {
		require.NoError(t, shard.UpsertUserConversationState(ctx, state))
	}

	got, err := shard.ListUserConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}, got)
}

func TestShardTouchUserConversationActiveAtPreservesUpdatedAt(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	state := UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 3,
		ActiveAt:     100,
		UpdatedAt:    200,
	}
	require.NoError(t, shard.UpsertUserConversationState(ctx, state))
	require.NoError(t, shard.TouchUserConversationActiveAt(ctx, "u1", "g1", 2, 300))
	require.NoError(t, shard.TouchUserConversationActiveAt(ctx, "u1", "g1", 2, 250))

	got, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(200), got.UpdatedAt)
	require.Equal(t, uint64(10), got.ReadSeq)
	require.Equal(t, uint64(3), got.DeletedToSeq)
}

func TestShardTouchUserConversationActiveAtAllowsHashSlotZero(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard, ok := any(db.ForHashSlot(0)).(userConversationShardStore)
	require.True(t, ok, "user conversation shard store methods missing")

	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
		UID:         "u0",
		ChannelID:   "g0",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   10,
	}))
	require.NoError(t, shard.TouchUserConversationActiveAt(ctx, "u0", "g0", 2, 300))

	got, err := shard.GetUserConversationState(ctx, "u0", "g0", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(10), got.UpdatedAt)
}

func TestShardListUserConversationStatePageReturnsStableCursor(t *testing.T) {
	ctx := context.Background()
	shard := openConversationTestShard(t)

	states := []UserConversationState{
		{UID: "u1", ChannelID: "g3", ChannelType: 2, UpdatedAt: 30},
		{UID: "u1", ChannelID: "g1", ChannelType: 2, UpdatedAt: 10},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, UpdatedAt: 20},
		{UID: "u2", ChannelID: "g4", ChannelType: 2, UpdatedAt: 40},
	}
	for _, state := range states {
		require.NoError(t, shard.UpsertUserConversationState(ctx, state))
	}

	page1, cursor, done, err := shard.ListUserConversationStatePage(ctx, "u1", ConversationCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, UpdatedAt: 10},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, UpdatedAt: 20},
	}, page1)
	require.Equal(t, ConversationCursor{ChannelType: 2, ChannelID: "g2"}, cursor)

	page2, cursor, done, err := shard.ListUserConversationStatePage(ctx, "u1", cursor, 2)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g3", ChannelType: 2, UpdatedAt: 30},
	}, page2)
	require.Equal(t, ConversationCursor{ChannelType: 2, ChannelID: "g3"}, cursor)
}

func TestShardClearUserConversationActiveAtZeroesOnlyActiveField(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	first := UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      11,
		DeletedToSeq: 3,
		ActiveAt:     100,
		UpdatedAt:    200,
	}
	second := UserConversationState{
		UID:          "u1",
		ChannelID:    "g2",
		ChannelType:  2,
		ReadSeq:      22,
		DeletedToSeq: 4,
		ActiveAt:     150,
		UpdatedAt:    300,
	}
	require.NoError(t, shard.UpsertUserConversationState(ctx, first))
	require.NoError(t, shard.UpsertUserConversationState(ctx, second))

	require.NoError(t, shard.ClearUserConversationActiveAt(ctx, "u1", []ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
	}))

	gotFirst, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      11,
		DeletedToSeq: 3,
		ActiveAt:     0,
		UpdatedAt:    200,
	}, gotFirst)

	gotSecond, err := shard.GetUserConversationState(ctx, "u1", "g2", 2)
	require.NoError(t, err)
	require.Equal(t, second, gotSecond)
}

func TestWriteBatchUpsertUserConversationStateReplacesActiveIndex(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(7)
	ctx := context.Background()

	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   10,
	}))

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.UpsertUserConversationState(7, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    200,
		UpdatedAt:   10,
	}))
	require.NoError(t, wb.Commit())

	got, err := shard.ListUserConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10},
	}, got)
}

func TestShardUpsertUserConversationStatePreservesMaxActiveAt(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		UpdatedAt:   10,
	}))
	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   20,
	}))

	got, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(20), got.UpdatedAt)
}

func TestWriteBatchUpsertUserConversationStateReplacesActiveIndexWithinBatch(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(7)
	ctx := context.Background()

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.UpsertUserConversationState(7, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   10,
	}))
	require.NoError(t, wb.UpsertUserConversationState(7, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    200,
		UpdatedAt:   10,
	}))
	require.NoError(t, wb.Commit())

	got, err := shard.ListUserConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10},
	}, got)
}

func TestWriteBatchUpsertUserConversationStatePreservesMaxActiveAt(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(7)
	ctx := context.Background()

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.UpsertUserConversationState(7, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		UpdatedAt:   10,
	}))
	require.NoError(t, wb.UpsertUserConversationState(7, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    100,
		UpdatedAt:   20,
	}))
	require.NoError(t, wb.Commit())

	got, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	require.NoError(t, err)
	require.Equal(t, int64(300), got.ActiveAt)
	require.Equal(t, int64(20), got.UpdatedAt)
}

func TestShardListUserConversationActiveSkipsStaleIndexEntries(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(7)
	ctx := context.Background()

	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    200,
		UpdatedAt:   10,
	}))

	db.mu.Lock()
	batch := db.db.NewBatch()
	indexKey := encodeUserConversationActiveIndexKey(7, "u1", 100, 2, "g1")
	require.NoError(t, batch.Set(indexKey, nil, nil))
	require.NoError(t, batch.Commit(nil))
	require.NoError(t, batch.Close())
	db.mu.Unlock()

	got, err := shard.ListUserConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10},
	}, got)
}
