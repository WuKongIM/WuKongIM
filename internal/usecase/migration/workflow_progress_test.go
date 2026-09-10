package migration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type failingProgressSource struct{ err error }

func (s failingProgressSource) ReadStoppedNode(context.Context, NodeOptions, func(Row) error, func(SourceFile) error) (NodeSnapshot, error) {
	return NodeSnapshot{}, s.err
}

func TestPrepareProgressReportsFailedStageAndPreservesError(t *testing.T) {
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "progress-test", 4096)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	boom := errors.New("source fixture failure")
	plan := Plan{Sources: []NodeOptions{{NodeID: 1, Options: Options{DataDir: t.TempDir(), ShardCount: 1}}}}
	var stages []string
	_, err = Prepare(context.Background(), plan, w, failingProgressSource{err: boom}, nil, func(_ uint64, stage string) { stages = append(stages, stage) })
	require.ErrorIs(t, err, boom)
	require.GreaterOrEqual(t, len(stages), 2)
	require.Equal(t, "source capture started", stages[0])
	require.True(t, strings.HasPrefix(stages[len(stages)-1], "source capture failed after "), stages)
	for _, stage := range stages {
		require.NotContains(t, stage, "source capture completed")
		require.NotContains(t, stage, boom.Error(), "stage timing logs should not copy source error details")
	}
}
