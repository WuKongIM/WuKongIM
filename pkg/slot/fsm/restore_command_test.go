package fsm

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestApplyRestorePortableCommandFiltersPhysicalSlotBatch(t *testing.T) {
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	uid2 := uidForHashSlot(t, 16, 2)
	uid7 := uidForHashSlot(t, 16, 7)

	command, err := EncodeTouchConversationActiveAtBatchCommandChecked(
		16,
		[]ConversationActivePatchBatchItem{
			{
				HashSlot: 2,
				Patch: metadb.ConversationActivePatch{
					UID: uid2, Kind: metadb.ConversationKindNormal,
					ChannelID:   "restore-channel",
					ChannelType: 2, ActiveAt: 10,
				},
			},
			{
				HashSlot: 7,
				Patch: metadb.ConversationActivePatch{
					UID: uid7, Kind: metadb.ConversationKindNormal,
					ChannelID:   "restore-channel",
					ChannelType: 2, ActiveAt: 20,
				},
			},
		},
	)
	require.NoError(t, err)

	applied, err := ApplyRestorePortableCommand(
		context.Background(), db, 2, command,
	)
	require.NoError(t, err)
	require.True(t, applied)

	state, err := db.ForHashSlot(2).GetConversationState(
		context.Background(), metadb.ConversationKindNormal,
		uid2, "restore-channel", 2,
	)
	require.NoError(t, err)
	require.Equal(t, int64(10), state.ActiveAt)
	_, err = db.ForHashSlot(7).GetConversationState(
		context.Background(), metadb.ConversationKindNormal,
		uid7, "restore-channel", 2,
	)
	require.True(t, errors.Is(err, metadb.ErrNotFound))
}
