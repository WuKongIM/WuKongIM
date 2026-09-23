package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTReplayPreparationRetriesShortPageBeforeExtending(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	// Target has the exact committed original log, but no shared replay yet.
	first, err := f.target.PrepareMQTTReplay(ctx, f.generation, 1, 4, ReadOptions{Limit: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.After.Through)
	retry, err := f.target.PrepareMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, first, retry, "a lost short reply must not create an overlapping extension")
	second, err := f.target.PrepareMQTTReplay(ctx, f.generation, 2, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, first.After, second.Before)
	require.Equal(t, f.all.After, second.After)
	all, err := f.target.PrepareMQTTReplay(ctx, f.generation, 1, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.all, all)
	source, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Zero(t, source.CopiedThrough)
}
