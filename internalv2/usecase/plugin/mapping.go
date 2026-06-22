package plugin

import (
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

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
