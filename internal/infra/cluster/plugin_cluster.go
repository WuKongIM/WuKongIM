package cluster

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
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

// PluginChannelOwnerBatchNode exposes Slot-authoritative bulk ownership reads.
type PluginChannelOwnerBatchNode interface {
	BatchGetChannelRuntimeMetas(context.Context, []metadb.ChannelKey) (map[metadb.ChannelKey]metadb.ChannelRuntimeMeta, error)
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

// ChannelOwnerNodes avoids a separate Slot authority read for every conversation.
// Each bounded batch uses current Slot authority; missing metadata alone may
// initialize append authority through the same path as a single-channel lookup.
func (r *PluginChannelOwnerReader) ChannelOwnerNodes(ctx context.Context, ids []message.ChannelID) ([]uint64, error) {
	if r == nil || r.node == nil {
		return nil, pluginusecase.ErrChannelOwnerReaderRequired
	}
	owners := make([]uint64, len(ids))
	reader, batchOK := r.node.(PluginChannelOwnerBatchNode)
	if !batchOK {
		for i, id := range ids {
			owner, err := r.ChannelOwnerNode(ctx, id)
			if err != nil {
				return nil, err
			}
			owners[i] = owner
		}
		return owners, nil
	}
	const batchSize = 512
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		keys := make([]metadb.ChannelKey, end-start)
		for i, id := range ids[start:end] {
			keys[i] = metadb.ChannelKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
		}
		metas, err := reader.BatchGetChannelRuntimeMetas(ctx, keys)
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithCancel(ctx)
		var workers sync.WaitGroup
		var firstErr error
		var errOnce sync.Once
		setError := func(err error) {
			errOnce.Do(func() {
				firstErr = err
				cancel()
			})
		}
		// Cold channels enter the existing coalesced metadata initializer in
		// parallel, bounded independently of the size of the membership list.
		limit := make(chan struct{}, 16)
	launch:
		for i, key := range keys {
			meta, ok := metas[key]
			if ok {
				owners[start+i] = meta.Leader
				continue
			}
			select {
			case limit <- struct{}{}:
			case <-callCtx.Done():
				setError(callCtx.Err())
				break launch
			}
			workers.Add(1)
			goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskPluginChannelOwnerInit, func() {
				defer workers.Done()
				defer func() { <-limit }()
				if err := callCtx.Err(); err != nil {
					setError(err)
					return
				}
				created, err := r.node.ResolveChannelAppendAuthority(callCtx, channelruntime.ChannelID{ID: key.ChannelID, Type: uint8(key.ChannelType)})
				if err != nil {
					setError(err)
					return
				}
				owners[start+i] = uint64(created.Leader)
			})
		}
		workers.Wait()
		cancel()
		if firstErr != nil {
			return nil, firstErr
		}
	}
	return owners, nil
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
