package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const defaultHostConversationChannelsLimit = 1000

// SendMessage handles the legacy /message/send host RPC through the message usecase.
func (a *App) SendMessage(ctx context.Context, req *pluginproto.SendReq, _ string) (*pluginproto.SendResp, error) {
	if a == nil || a.messages == nil {
		return nil, ErrMessageSenderRequired
	}
	cmd, err := sendCommandFromPluginReq(req, a.defaultSenderUID)
	if err != nil {
		return nil, err
	}
	result, err := a.messages.Send(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return sendRespFromResult(result), nil
}

// ChannelMessages handles the legacy /channel/messages host RPC through the authoritative reader.
func (a *App) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, _ string) (*pluginproto.ChannelMessageBatchResp, error) {
	if a == nil || a.messageReader == nil {
		return nil, ErrMessageReaderRequired
	}
	if req == nil {
		req = &pluginproto.ChannelMessageBatchReq{}
	}
	resp := &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: make([]*pluginproto.ChannelMessageResp, 0, len(req.GetChannelMessageReqs()))}
	for _, item := range req.GetChannelMessageReqs() {
		page, err := a.messageReader.SyncMessages(ctx, channelMessageQueryFromPluginReq(item))
		if errors.Is(err, metadb.ErrNotFound) {
			resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
			continue
		}
		if err != nil {
			return nil, err
		}
		resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
	}
	return resp, nil
}

// ClusterConfig handles the legacy /cluster/config host RPC through an authoritative cluster reader.
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

// ClusterChannelsBelongNode handles legacy channel owner grouping without local fallback guesses.
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

// ConversationChannels handles the legacy /conversation/channels host RPC through an authoritative reader.
func (a *App) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, _ string) (*pluginproto.ConversationChannelResp, error) {
	if a == nil || a.conversations == nil {
		return nil, ErrConversationReaderRequired
	}
	uid := ""
	if req != nil {
		uid = strings.TrimSpace(req.GetUid())
	}
	if uid == "" {
		return nil, ErrConversationUIDRequired
	}
	channels, err := a.conversations.ConversationChannels(ctx, uid, defaultHostConversationChannelsLimit)
	if err != nil {
		return nil, err
	}
	return conversationChannelsRespFromChannelIDs(channels), nil
}

func channelIDFromPluginChannel(item *pluginproto.Channel) (channel.ChannelID, error) {
	if item == nil || strings.TrimSpace(item.GetChannelId()) == "" {
		return channel.ChannelID{}, ErrChannelRequired
	}
	return channel.ChannelID{ID: item.GetChannelId(), Type: uint8(item.GetChannelType())}, nil
}
