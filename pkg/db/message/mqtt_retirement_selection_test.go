package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTRetirementSelectionRoundsDownWithBoundedContinuation(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	before, err := f.target.LoadCheckpoint()
	require.NoError(t, err)
	_, present, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, present, "selection uses accepted journals, not replica-local bodies")
	first, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, 0, 1)
	require.NoError(t, err)
	require.False(t, first.Done)
	require.False(t, first.HasCandidate)
	require.Zero(t, first.Candidate)
	require.Equal(t, uint64(7), first.BeforeAnchor)
	require.Equal(t, uint64(6), first.Captured.Anchor.Through)
	next, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 5, first.BeforeAnchor, 1)
	require.NoError(t, err)
	require.True(t, next.Done)
	require.True(t, next.HasCandidate)
	require.Zero(t, next.BeforeAnchor)
	require.Equal(t, first.Captured, next.Captured)
	require.Equal(t, uint64(5), next.Candidate.Manifest.LastOffset)
	require.Equal(t, uint64(4), next.Candidate.Anchor.Through)
	whole, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 6, 0, 1)
	require.NoError(t, err)
	require.True(t, whole.Done)
	require.True(t, whole.HasCandidate)
	require.Equal(t, first.Captured, whole.Candidate)
	none, err := f.target.SelectMQTTReplayRetirementAnchor(ctx, f.generation, 7, 3, 0, 64)
	require.NoError(t, err)
	require.True(t, none.Done)
	require.False(t, none.HasCandidate)
	require.Zero(t, none.BeforeAnchor)
	after, err := f.target.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, present, err = f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, present)
}
