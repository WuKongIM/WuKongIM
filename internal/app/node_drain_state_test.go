package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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

func TestLocalNodeDrainingDisablesGatewayAdmission(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	gw, err := gateway.New(gateway.Options{
		Handler: nopGatewayHandler{},
		Listeners: []gateway.ListenerOptions{{
			Name:      "listener-a",
			Network:   "tcp",
			Address:   "127.0.0.1:0",
			Transport: "stdnet",
			Protocol:  "jsonrpc",
		}},
	})
	require.NoError(t, err)
	app := &App{
		cfg:            Config{Node: NodeConfig{ID: 3}},
		gateway:        gw,
		nodeDrainState: newNodeDrainState(3, func() time.Time { return now }),
	}

	app.updateGatewayAdmissionFromDrainState()
	require.False(t, gw.AcceptingNewSessions())

	app.observeNodeStatusChange(3, controllermeta.NodeStatusAlive)
	require.True(t, gw.AcceptingNewSessions())

	app.observeNodeStatusChange(3, controllermeta.NodeStatusDraining)
	require.False(t, gw.AcceptingNewSessions())

	app.observeNodeStatusChange(2, controllermeta.NodeStatusAlive)
	require.False(t, gw.AcceptingNewSessions())
}

type nopGatewayHandler struct{}

func (nopGatewayHandler) OnListenerError(string, error) {}
func (nopGatewayHandler) OnSessionOpen(*gatewaytypes.Context) error {
	return nil
}
func (nopGatewayHandler) OnFrame(*gatewaytypes.Context, frame.Frame) error {
	return nil
}
func (nopGatewayHandler) OnSessionClose(*gatewaytypes.Context) error {
	return nil
}
func (nopGatewayHandler) OnSessionError(*gatewaytypes.Context, error) {}
