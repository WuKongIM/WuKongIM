package cluster

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTMetadataNodeForegroundGates(t *testing.T) {
	ctx := context.Background()
	var absent *Node
	maintenance := &Node{}
	maintenance.started.Store(true)
	maintenance.maintenance.Store(true)
	for _, tc := range []struct {
		node *Node
		err  error
	}{{absent, ErrNotStarted}, {&Node{}, ErrNotStarted}, {maintenance, ErrMaintenance}} {
		_, err := tc.node.ReadMQTT(ctx, metadb.MQTTRead{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ReadMQTTRecovery(ctx, 0, metadb.MQTTRead{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTSession(ctx, 0, metadb.MQTTSession{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.ReclaimMQTTSession(ctx, metadb.MQTTSessionReclamation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTSubscription(ctx, metadb.MQTTSubscriptionMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTDeliveryCursor(ctx, metadb.MQTTDeliveryCursorMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.MutateMQTTWindow(ctx, metadb.MQTTWindowMutation{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTSourceBinding(ctx, 0, metadb.MQTTSourceBinding{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTInboxAdmission(ctx, 0, metadb.MQTTInboxAdmission{})
		require.ErrorIs(t, err, tc.err)
		_, err = tc.node.CompareAndSwapMQTTWill(ctx, 0, metadb.MQTTWill{})
		require.ErrorIs(t, err, tc.err)
	}
}
