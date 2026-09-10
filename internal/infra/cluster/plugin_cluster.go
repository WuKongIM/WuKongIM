package cluster

import (
	"context"
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// PluginClusterNode exposes cluster control state for plugin host RPCs.
type PluginClusterNode interface {
	// LocalControlSnapshot returns the latest locally visible control snapshot.
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// PluginChannelOwnerNode exposes channel authority resolution for plugin host RPCs.
type PluginChannelOwnerNode interface {
	// GetChannelRuntimeMeta reads current ownership through the Slot authority.
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
	// ResolveChannelAppendAuthority initializes authority for a channel without runtime metadata.
	ResolveChannelAppendAuthority(context.Context, channelruntime.ChannelID) (channelruntime.Meta, error)
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

// PluginChannelOwnerReader adapts channel authority metadata to plugin owner lookups.
type PluginChannelOwnerReader struct {
	node PluginChannelOwnerNode
}

// NewPluginChannelOwnerReader creates a PluginChannelOwnerReader.
func NewPluginChannelOwnerReader(node PluginChannelOwnerNode) *PluginChannelOwnerReader {
	return &PluginChannelOwnerReader{node: node}
}

// ChannelOwnerNode returns the channel append authority leader.
func (r *PluginChannelOwnerReader) ChannelOwnerNode(ctx context.Context, id message.ChannelID) (uint64, error) {
	if r == nil || r.node == nil {
		return 0, pluginusecase.ErrChannelOwnerReaderRequired
	}
	// Plugin forwards address a node directly and cannot invalidate the append
	// router on failure. Read current ownership so a cached pre-failover leader
	// cannot keep receiving otherwise independent plugin requests indefinitely.
	meta, err := r.node.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if errors.Is(err, metadb.ErrNotFound) {
		created, createErr := r.node.ResolveChannelAppendAuthority(ctx, channelruntime.ChannelID{ID: id.ID, Type: id.Type})
		return uint64(created.Leader), createErr
	}
	if err != nil {
		return 0, err
	}
	return meta.Leader, nil
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
