package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMQTTCopyNodeForegroundGates(t *testing.T) {
	var absent *Node
	maintenance := &Node{}
	maintenance.started.Store(true)
	maintenance.maintenance.Store(true)
	for _, tc := range []struct {
		n   *Node
		err error
	}{{absent, ErrNotStarted}, {&Node{}, ErrNotStarted}, {maintenance, ErrMaintenance}} {
		_, err := tc.n.CopyChannelMQTTReplay(context.Background(), ch.MQTTReplayRequest{})
		require.ErrorIs(t, err, tc.err)
	}
}
