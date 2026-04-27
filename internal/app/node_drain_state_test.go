package app

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestNodeDrainStateMarksLocalNodeDraining(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	state := newNodeDrainState(3, func() time.Time { return now })

	state.Observe(2, controllermeta.NodeStatusAlive)
	ready, reason := state.Ready()
	require.False(t, ready)
	require.Equal(t, "unknown", reason)

	state.Observe(3, controllermeta.NodeStatusDraining)

	ready, reason = state.Ready()
	require.False(t, ready)
	require.Equal(t, "draining", reason)
	require.True(t, state.Draining())
	require.False(t, state.KnownNotDraining())
}

func TestNodeDrainStateTreatsStaleUnknownAsNotReady(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	state := newNodeDrainState(3, func() time.Time { return now })

	ready, reason := state.Ready()
	require.False(t, ready)
	require.Equal(t, "unknown", reason)

	state.Observe(3, controllermeta.NodeStatusAlive)
	ready, reason = state.Ready()
	require.True(t, ready)
	require.Equal(t, "alive", reason)

	now = now.Add(state.staleAfter + time.Millisecond)
	ready, reason = state.Ready()
	require.False(t, ready)
	require.Equal(t, "stale", reason)
	require.False(t, state.KnownNotDraining())
}

func TestReadyzReportIncludesNodeNotDrainingCheck(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	state := newNodeDrainState(3, func() time.Time { return now })
	state.Observe(3, controllermeta.NodeStatusAlive)
	app := &App{nodeDrainState: state}

	_, payload := app.readyzReport(context.Background())

	body, ok := payload.(map[string]any)
	require.True(t, ok)
	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, checks["node_not_draining"])
	require.Equal(t, "alive", checks["node_drain_reason"])
}
