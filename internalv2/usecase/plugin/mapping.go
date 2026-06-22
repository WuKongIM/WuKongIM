package plugin

import (
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultHostChannelMessagesLimit = 100
const maxHostChannelMessagesLimit = 10000
const defaultHostConversationChannelsLimit = 1000

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
	channelType := uint8(req.GetChannelType())
	return message.SendCommand{
		ClientMsgNo:            req.GetClientMsgNo(),
		FromUID:                fromUID,
		ChannelID:              req.GetChannelId(),
		ChannelType:            channelType,
		Payload:                append([]byte(nil), req.GetPayload()...),
		NoPersist:              header.GetNoPersist(),
		SyncOnce:               header.GetSyncOnce(),
		RedDot:                 header.GetRedDot(),
		NormalizePersonChannel: channelType == frame.ChannelTypePerson,
		Origin:                 message.SendOriginPlugin,
	}, nil
}

func sendRespFromResult(result message.SendResult) *pluginproto.SendResp {
	return &pluginproto.SendResp{MessageId: messageIDToInt64(result.MessageID)}
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
		ChannelID: message.ChannelID{
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
		resp.Messages = append(resp.Messages, pluginMessageFromSyncedMessage(msg))
	}
	return resp
}

func effectiveChannelMessageLimit(req *pluginproto.ChannelMessageReq) int {
	return channelMessageQueryFromPluginReq(req).Limit
}

func pluginMessageFromSyncedMessage(msg message.SyncedMessage) *pluginproto.Message {
	return &pluginproto.Message{
		MessageId:   messageIDToInt64(msg.MessageID),
		MessageSeq:  msg.MessageSeq,
		ClientMsgNo: msg.ClientMsgNo,
		Timestamp:   timestampToUint32(msg.Timestamp),
		From:        msg.FromUID,
		ChannelId:   msg.ChannelID,
		ChannelType: uint32(msg.ChannelType),
		Topic:       msg.Topic,
		Payload:     append([]byte(nil), msg.Payload...),
	}
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

func channelIDFromPluginChannel(item *pluginproto.Channel) (message.ChannelID, error) {
	if item == nil || strings.TrimSpace(item.GetChannelId()) == "" {
		return message.ChannelID{}, ErrChannelRequired
	}
	return message.ChannelID{ID: item.GetChannelId(), Type: uint8(item.GetChannelType())}, nil
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

func conversationChannelsRespFromChannelIDs(channels []message.ChannelID) *pluginproto.ConversationChannelResp {
	resp := &pluginproto.ConversationChannelResp{
		Channels: make([]*pluginproto.Channel, 0, len(channels)),
	}
	for _, channel := range channels {
		resp.Channels = append(resp.Channels, &pluginproto.Channel{
			ChannelId:   channel.ID,
			ChannelType: uint32(channel.Type),
		})
	}
	return resp
}

func messageBatchFromPersistAfter(event pluginevents.PersistAfterCommitted) *pluginproto.MessageBatch {
	return &pluginproto.MessageBatch{Messages: []*pluginproto.Message{{
		MessageId:   messageIDToInt64(event.MessageID),
		MessageSeq:  event.MessageSeq,
		ClientMsgNo: event.ClientMsgNo,
		Timestamp:   timestampSeconds(event.ServerTimestampMS),
		From:        event.FromUID,
		ChannelId:   event.ChannelID,
		ChannelType: uint32(event.ChannelType),
		Payload:     append([]byte(nil), event.Payload...),
	}}}
}

func messageIDToInt64(id uint64) int64 {
	if id > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(id)
}

func timestampSeconds(ms int64) uint32 {
	if ms <= 0 {
		return 0
	}
	sec := ms / 1000
	if sec > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(sec)
}

func timestampToUint32(ts int32) uint32 {
	if ts < 0 {
		return 0
	}
	return uint32(ts)
}
