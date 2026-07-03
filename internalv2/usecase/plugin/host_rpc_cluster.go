package plugin

import (
	"context"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

// ClusterConfig handles PDK-compatible /cluster/config host RPCs through the v2 cluster reader.
func (a *App) ClusterConfig(ctx context.Context, _ string) (*pluginproto.ClusterConfig, error) {
	if a == nil || a.clusterReader == nil {
		return nil, ErrClusterReaderRequired
	}
	snapshot, err := a.clusterReader.ClusterSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return clusterConfigFromSnapshot(snapshot), nil
}

// ClusterChannelsBelongNode handles PDK-compatible /cluster/channels/belongNode host RPCs.
func (a *App) ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, _ string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error) {
	if a == nil || a.channelOwners == nil {
		return nil, ErrChannelOwnerReaderRequired
	}
	if req == nil || len(req.GetChannels()) == 0 {
		return nil, ErrChannelRequired
	}
	groups := make(map[uint64][]*pluginproto.Channel)
	for _, item := range req.GetChannels() {
		id, err := channelIDFromPluginChannel(item)
		if err != nil {
			return nil, err
		}
		owner, err := a.channelOwners.ChannelOwnerNode(ctx, id)
		if err != nil {
			return nil, err
		}
		if owner == 0 {
			return nil, fmt.Errorf("%w: %s/%d", ErrChannelOwnerUnknown, id.ID, id.Type)
		}
		groups[owner] = append(groups[owner], clonePluginChannel(item))
	}
	nodeIDs := make([]uint64, 0, len(groups))
	for nodeID := range groups {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	resp := &pluginproto.ClusterChannelBelongNodeBatchResp{
		ClusterChannelBelongNodeResps: make([]*pluginproto.ClusterChannelBelongNodeResp, 0, len(nodeIDs)),
	}
	for _, nodeID := range nodeIDs {
		resp.ClusterChannelBelongNodeResps = append(resp.ClusterChannelBelongNodeResps, &pluginproto.ClusterChannelBelongNodeResp{
			NodeId:   nodeID,
			Channels: groups[nodeID],
		})
	}
	return resp, nil
}
