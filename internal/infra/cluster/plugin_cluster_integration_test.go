//go:build integration

package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A large membership directory can contain channels without runtime metadata.
// Their ordinary authority initialization must share the callback deadline.
func TestPluginOwnerBatchColdChannelsShareInitializationBudget(t *testing.T) {
	ids := make([]message.ChannelID, 128)
	for i := range ids {
		ids[i] = message.ChannelID{ID: fmt.Sprintf("cold-%d", i), Type: 2}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	node := &coldPluginOwnerNode{}
	got, err := NewPluginChannelOwnerReader(node).ChannelOwnerNodes(ctx, ids)
	require.NoError(t, err)
	require.Len(t, got, len(ids))
	require.Greater(t, node.peak.Load(), int64(1))
	require.LessOrEqual(t, node.peak.Load(), int64(16))
	require.Zero(t, node.active.Load())
	for _, owner := range got {
		require.Equal(t, uint64(2), owner)
	}
}

type coldPluginOwnerNode struct {
	active atomic.Int64
	peak   atomic.Int64
}

func (*coldPluginOwnerNode) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}
func (*coldPluginOwnerNode) BatchGetChannelRuntimeMetas(context.Context, []metadb.ChannelKey) (map[metadb.ChannelKey]metadb.ChannelRuntimeMeta, error) {
	return map[metadb.ChannelKey]metadb.ChannelRuntimeMeta{}, nil
}
func (n *coldPluginOwnerNode) ResolveChannelAppendAuthority(ctx context.Context, _ channelruntime.ChannelID) (channelruntime.Meta, error) {
	active := n.active.Add(1)
	defer n.active.Add(-1)
	for old := n.peak.Load(); active > old; old = n.peak.Load() {
		if n.peak.CompareAndSwap(old, active) {
			break
		}
	}
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return channelruntime.Meta{}, ctx.Err()
	case <-timer.C:
		return channelruntime.Meta{Leader: 2}, nil
	}
}
