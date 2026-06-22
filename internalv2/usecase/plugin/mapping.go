package plugin

import (
	"math"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
)

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
