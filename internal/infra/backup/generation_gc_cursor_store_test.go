package backup_test

import (
	"context"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

func TestControllerGenerationGCCursorStorePersistsIndependentBoundedCursors(t *testing.T) {
	ctx := context.Background()
	state := &erasureLedgerStateStore{}
	store, err := backupinfra.NewControllerGenerationGCCursorStore(state)
	require.NoError(t, err)

	_, found, err := store.LoadGenerationGCCursor(ctx, "primary")
	require.NoError(t, err)
	require.False(t, found)

	primary := generationGCTestCursor("primary", 1, "objects/a")
	secondary := generationGCTestCursor("secondary", 1, "objects/b")
	require.NoError(t, store.CompareAndSwapGenerationGCCursor(ctx, "secondary", 0, secondary))
	require.NoError(t, store.CompareAndSwapGenerationGCCursor(ctx, "primary", 0, primary))

	require.Len(t, state.state.GenerationGCCursors, 2)
	require.Equal(t, "primary", state.state.GenerationGCCursors[0].Repository)
	require.Equal(t, "secondary", state.state.GenerationGCCursors[1].Repository)
	require.Equal(t, "objects/a", state.state.GenerationGCCursors[0].AfterKey)

	primary.AfterKey = "objects/c"
	primary.Revision = 2
	require.NoError(t, store.CompareAndSwapGenerationGCCursor(ctx, "primary", 1, primary))
	loaded, found, err := store.LoadGenerationGCCursor(ctx, "primary")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), loaded.Revision)
	require.Equal(t, "objects/c", loaded.AfterKey)
	require.Equal(t, "objects/b", state.state.GenerationGCCursors[1].AfterKey)
}

func TestControllerGenerationGCCursorStoreRejectsStaleRepositoryRevision(t *testing.T) {
	ctx := context.Background()
	state := &erasureLedgerStateStore{}
	store, err := backupinfra.NewControllerGenerationGCCursorStore(state)
	require.NoError(t, err)

	require.NoError(t, store.CompareAndSwapGenerationGCCursor(
		ctx, "primary", 0, generationGCTestCursor("primary", 1, ""),
	))
	err = store.CompareAndSwapGenerationGCCursor(
		ctx, "primary", 0, generationGCTestCursor("primary", 1, "objects/stale"),
	)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

func generationGCTestCursor(
	repository string,
	revision uint64,
	afterKey string,
) backupcontract.GenerationGCCursor {
	return backupcontract.GenerationGCCursor{
		Repository: repository, Revision: revision, CycleID: "cycle-1",
		AfterKey: afterKey, CutoffUnixMillis: 1_753_400_100_000,
		UpdatedAtUnixMillis: 1_753_400_110_000,
	}
}
