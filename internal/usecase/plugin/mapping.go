package plugin

import (
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultHostChannelMessagesLimit = 100
const maxHostChannelMessagesLimit = 10000

func sendCommandFromPluginReq(req *pluginproto.SendReq, defaultSenderUID string) (message.SendCommand, error) {
	if req == nil {
		req = &pluginproto.SendReq{}
	}
	fromUID := strings.TrimSpace(req.GetFromUid())
	if fromUID == "" {
		fromUID = strings.TrimSpace(defaultSenderUID)
		if fromUID == "" {
			return message.SendCommand{}, ErrDefaultSenderUIDRequired
		}
	}
	header := req.GetHeader()
	return message.SendCommand{
		Framer: frame.Framer{
			NoPersist: header.GetNoPersist(),
			RedDot:    header.GetRedDot(),
			SyncOnce:  header.GetSyncOnce(),
		},
		ClientMsgNo: req.GetClientMsgNo(),
		FromUID:     fromUID,
		ChannelID:   req.GetChannelId(),
		ChannelType: uint8(req.GetChannelType()),
		Payload:     append([]byte(nil), req.GetPayload()...),
		Origin:      message.SendOriginPlugin,
	}, nil
}

func sendRespFromResult(result message.SendResult) *pluginproto.SendResp {
	return &pluginproto.SendResp{MessageId: result.MessageID}
}

func channelMessageQueryFromPluginReq(req *pluginproto.ChannelMessageReq) message.ChannelMessageQuery {
	if req == nil {
		req = &pluginproto.ChannelMessageReq{}
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultHostChannelMessagesLimit
	} else if limit > maxHostChannelMessagesLimit {
		limit = maxHostChannelMessagesLimit
	}
	return message.ChannelMessageQuery{
		ChannelID: channel.ChannelID{
			ID:   req.GetChannelId(),
			Type: uint8(req.GetChannelType()),
		},
		StartSeq: req.GetStartMessageSeq(),
		Limit:    limit,
		PullMode: message.PullModeUp,
	}
}

func channelMessageRespFromPage(req *pluginproto.ChannelMessageReq, page message.ChannelMessagePage) *pluginproto.ChannelMessageResp {
	if req == nil {
		req = &pluginproto.ChannelMessageReq{}
	}
	resp := &pluginproto.ChannelMessageResp{
		ChannelId:       req.GetChannelId(),
		ChannelType:     req.GetChannelType(),
		StartMessageSeq: req.GetStartMessageSeq(),
		Limit:           uint32(effectiveChannelMessageLimit(req)),
		Messages:        make([]*pluginproto.Message, 0, len(page.Messages)),
	}
	for _, msg := range page.Messages {
		resp.Messages = append(resp.Messages, pluginMessageFromChannelMessage(msg))
	}
	return resp
}

func effectiveChannelMessageLimit(req *pluginproto.ChannelMessageReq) int {
	return channelMessageQueryFromPluginReq(req).Limit
}

func pluginMessageFromChannelMessage(msg channel.Message) *pluginproto.Message {
	return &pluginproto.Message{
		MessageId:   messageIDToInt64(msg.MessageID),
		MessageSeq:  msg.MessageSeq,
		ClientMsgNo: msg.ClientMsgNo,
		StreamNo:    msg.StreamNo,
		StreamId:    msg.StreamID,
		Timestamp:   timestampToUint32(msg.Timestamp),
		From:        msg.FromUID,
		ChannelId:   msg.ChannelID,
		ChannelType: uint32(msg.ChannelType),
		Topic:       msg.Topic,
		Payload:     append([]byte(nil), msg.Payload...),
	}
}

func messageIDToInt64(id uint64) int64 {
	if id > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(id)
}

func timestampToUint32(ts int32) uint32 {
	if ts < 0 {
		return 0
	}
	return uint32(ts)
}

func clusterConfigFromSnapshot(snapshot ClusterSnapshot) *pluginproto.ClusterConfig {
	nodes := append([]ClusterNode(nil), snapshot.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	slots := append([]ClusterSlot(nil), snapshot.Slots...)
	sort.Slice(slots, func(i, j int) bool { return slots[i].ID < slots[j].ID })

	resp := &pluginproto.ClusterConfig{
		Nodes: make([]*pluginproto.Node, 0, len(nodes)),
		Slots: make([]*pluginproto.Slot, 0, len(slots)),
	}
	for _, node := range nodes {
		resp.Nodes = append(resp.Nodes, &pluginproto.Node{
			Id:            node.ID,
			ClusterAddr:   node.ClusterAddr,
			ApiServerAddr: node.APIServerAddr,
			Online:        node.Online,
		})
	}
	for _, slot := range slots {
		resp.Slots = append(resp.Slots, &pluginproto.Slot{
			Id:       slot.ID,
			Leader:   slot.Leader,
			Term:     slot.Term,
			Replicas: append([]uint64(nil), slot.Replicas...),
		})
	}
	return resp
}

func clonePluginChannel(item *pluginproto.Channel) *pluginproto.Channel {
	if item == nil {
		return &pluginproto.Channel{}
	}
	return &pluginproto.Channel{
		ChannelId:   item.GetChannelId(),
		ChannelType: item.GetChannelType(),
	}
}

func conversationChannelsRespFromChannelIDs(channels []channel.ChannelID) *pluginproto.ConversationChannelResp {
	resp := &pluginproto.ConversationChannelResp{Channels: make([]*pluginproto.Channel, 0, len(channels))}
	for _, id := range channels {
		resp.Channels = append(resp.Channels, &pluginproto.Channel{
			ChannelId:   id.ID,
			ChannelType: uint32(id.Type),
		})
	}
	return resp
}
