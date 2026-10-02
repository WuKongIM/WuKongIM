package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTReplayNodeForegroundGates(t *testing.T) {
	var absent *Node
	maintenance := &Node{}
	maintenance.started.Store(true)
	maintenance.maintenance.Store(true)
	for _, tc := range []struct {
		node *Node
		err  error
	}{{absent, ErrNotStarted}, {&Node{}, ErrNotStarted}, {maintenance, ErrMaintenance}} {
		_, err := tc.node.PrepareChannelMQTTReplay(context.Background(), ch.MQTTReplayRequest{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ReadChannelMQTTReplay(context.Background(), ch.MQTTReplayConsumerRequest{})
		require.ErrorIs(t, err, tc.err)
	}
}
