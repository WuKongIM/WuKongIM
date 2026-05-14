package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type cmdConversationShardStore interface {
	GetCMDConversationState(ctx context.Context, uid, channelID string, channelType int64) (CMDConversationState, error)
	UpsertCMDConversationState(ctx context.Context, state CMDConversationState) error
	AdvanceCMDConversationReadSeq(ctx context.Context, patch CMDConversationReadPatch) error
	ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]CMDConversationState, error)
}

func openCMDConversationTestShard(t *testing.T) cmdConversationShardStore {
	t.Helper()

	db := openTestDB(t)
	shard, ok := any(db.ForSlot(1)).(cmdConversationShardStore)
	require.True(t, ok, "cmd conversation shard store methods missing")
	return shard
}

func TestShardListCMDConversationActiveReturnsOnlyCMDRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(7)

	require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100}))
	require.NoError(t, shard.UpsertUserConversationState(ctx, UserConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 200}))

	cmdRows, err := shard.ListCMDConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 100}}, cmdRows)

	chatRows, err := shard.ListUserConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 200}}, chatRows)
}

func TestShardListCMDConversationActiveReturnsActiveAtDesc(t *testing.T) {
	ctx := context.Background()
	shard := openCMDConversationTestShard(t)

	states := []CMDConversationState{
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
		{UID: "u1", ChannelID: "g2____cmd", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g3____cmd", ChannelType: 2, ActiveAt: 0, UpdatedAt: 30},
		{UID: "u1", ChannelID: "g4____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u2", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 500, UpdatedAt: 50},
	}
	for _, state := range states {
		require.NoError(t, shard.UpsertCMDConversationState(ctx, state))
	}

	got, err := shard.ListCMDConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []CMDConversationState{
		{UID: "u1", ChannelID: "g2____cmd", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g4____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}, got)
}

func TestShardUpsertCMDConversationStatePreservesMaxFields(t *testing.T) {
	ctx := context.Background()
	shard := openCMDConversationTestShard(t)

	require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 4,
		ActiveAt:     300,
		UpdatedAt:    20,
	}))
	require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      8,
		DeletedToSeq: 7,
		ActiveAt:     100,
		UpdatedAt:    30,
	}))

	got, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 7,
		ActiveAt:     300,
		UpdatedAt:    30,
	}, got)
}

func TestShardAdvanceCMDConversationReadSeqIsMonotonic(t *testing.T) {
	ctx := context.Background()
	shard := openCMDConversationTestShard(t)

	require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 5, ActiveAt: 100}))
	require.NoError(t, shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 4, UpdatedAt: 200}))

	got, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(5), got.ReadSeq)

	require.NoError(t, shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 300}))
	got, err = shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(9), got.ReadSeq)
	require.Equal(t, int64(300), got.UpdatedAt)
}

func TestShardAdvanceCMDConversationReadSeqMissingStateNoops(t *testing.T) {
	ctx := context.Background()
	shard := openCMDConversationTestShard(t)

	require.NoError(t, shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 300}))
	_, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWriteBatchUpsertCMDConversationStateReplacesActiveIndexWithinBatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.UpsertCMDConversationState(7, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10}))
	require.NoError(t, wb.UpsertCMDConversationState(7, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 20}))
	require.NoError(t, wb.Commit())

	got, err := shard.ListCMDConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 20}}, got)
}

func TestWriteBatchAdvanceCMDConversationReadSeqSeesEarlierBatchWrite(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	wb := db.NewWriteBatch()
	defer wb.Close()
	require.NoError(t, wb.UpsertCMDConversationState(7, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 5, UpdatedAt: 10}))
	require.NoError(t, wb.AdvanceCMDConversationReadSeq(7, []CMDConversationReadPatch{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 20}}))
	require.NoError(t, wb.Commit())

	got, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(9), got.ReadSeq)
	require.Equal(t, int64(20), got.UpdatedAt)
}

func TestShardListCMDConversationActiveSkipsStaleIndexEntries(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	require.NoError(t, shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10}))

	db.mu.Lock()
	batch := db.db.NewBatch()
	indexKey := encodeCMDConversationActiveIndexKey(7, "u1", 100, 2, "g1____cmd")
	require.NoError(t, batch.Set(indexKey, nil, nil))
	require.NoError(t, batch.Commit(nil))
	require.NoError(t, batch.Close())
	db.mu.Unlock()

	got, err := shard.ListCMDConversationActive(ctx, "u1", 10)
	require.NoError(t, err)
	require.Equal(t, []CMDConversationState{{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10}}, got)
}
