package cluster

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// PluginClusterNode exposes cluster control state for plugin host RPCs.
type PluginClusterNode interface {
	// LocalControlSnapshot returns the latest locally visible control snapshot.
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// PluginChannelOwnerNode exposes ChannelV2 authority resolution for plugin host RPCs.
type PluginChannelOwnerNode interface {
	// ResolveChannelAppendAuthority resolves the ChannelV2 append authority.
	ResolveChannelAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error)
}

// PluginClusterReader adapts cluster control snapshots to plugin cluster snapshots.
type PluginClusterReader struct {
	node PluginClusterNode
}

// NewPluginClusterReader creates a PluginClusterReader.
func NewPluginClusterReader(node PluginClusterNode) *PluginClusterReader {
	return &PluginClusterReader{node: node}
}

// ClusterSnapshot returns one plugin-compatible cluster snapshot.
func (r *PluginClusterReader) ClusterSnapshot(ctx context.Context) (pluginusecase.ClusterSnapshot, error) {
	if r == nil || r.node == nil {
		return pluginusecase.ClusterSnapshot{}, pluginusecase.ErrClusterReaderRequired
	}
	snapshot, err := r.node.LocalControlSnapshot(ctx)
	if err != nil {
		return pluginusecase.ClusterSnapshot{}, err
	}
	return pluginClusterSnapshotFromControl(snapshot), nil
}

// PluginChannelOwnerReader adapts ChannelV2 authority metadata to plugin owner lookups.
type PluginChannelOwnerReader struct {
	node PluginChannelOwnerNode
}

// NewPluginChannelOwnerReader creates a PluginChannelOwnerReader.
func NewPluginChannelOwnerReader(node PluginChannelOwnerNode) *PluginChannelOwnerReader {
	return &PluginChannelOwnerReader{node: node}
}

// ChannelOwnerNode returns the ChannelV2 append authority leader.
func (r *PluginChannelOwnerReader) ChannelOwnerNode(ctx context.Context, id message.ChannelID) (uint64, error) {
	if r == nil || r.node == nil {
		return 0, pluginusecase.ErrChannelOwnerReaderRequired
	}
	meta, err := r.node.ResolveChannelAppendAuthority(ctx, channelv2.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return 0, err
	}
	return uint64(meta.Leader), nil
}

func pluginClusterSnapshotFromControl(snapshot control.Snapshot) pluginusecase.ClusterSnapshot {
	out := pluginusecase.ClusterSnapshot{
		Nodes: make([]pluginusecase.ClusterNode, 0, len(snapshot.Nodes)),
		Slots: make([]pluginusecase.ClusterSlot, 0, len(snapshot.Slots)),
	}
	for _, node := range snapshot.Nodes {
		out.Nodes = append(out.Nodes, pluginusecase.ClusterNode{
			ID:          node.NodeID,
			ClusterAddr: node.Addr,
			Online:      node.Status == control.NodeAlive,
		})
	}
	for _, slot := range snapshot.Slots {
		out.Slots = append(out.Slots, pluginusecase.ClusterSlot{
			ID:       slot.SlotID,
			Leader:   slot.PreferredLeader,
			Term:     saturatingUint32(slot.ConfigEpoch),
			Replicas: append([]uint64(nil), slot.DesiredPeers...),
		})
	}
	return out
}

func saturatingUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}
