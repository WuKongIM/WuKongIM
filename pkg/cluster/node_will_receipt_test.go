package cluster

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestWillReceiptNodeForegroundGates(t *testing.T) {
	var absent *Node
	maintenance := &Node{}
	maintenance.started.Store(true)
	maintenance.maintenance.Store(true)
	for _, tc := range []struct {
		node *Node
		err  error
	}{{absent, ErrNotStarted}, {&Node{}, ErrNotStarted}, {maintenance, ErrMaintenance}} {
		got, err := tc.node.ReadChannelWillReceipt(context.Background(), ch.WillReceiptRequest{})
		require.ErrorIs(t, err, tc.err)
		require.Zero(t, got)
	}
}
