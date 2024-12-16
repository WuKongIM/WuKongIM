package process

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *Channel) processStorage(fakeChannelId string, channelType uint8, messages []*reactor.ChannelMessage) {

	// 存储消息
	storages := c.toStorageMessages(messages)

	reasonCode := wkproto.ReasonSuccess

	timeoutCtx, cancel := c.WithTimeout()
	defer cancel()
	results, err := service.Store.AppendMessages(timeoutCtx, fakeChannelId, channelType, storages)
	if err != nil {
		c.Error("store message failed", zap.Error(err), zap.Int("msgs", len(storages)), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		reasonCode = wkproto.ReasonSystemError
	}

	for _, message := range messages {
		message.ReasonCode = reasonCode
		if reasonCode == wkproto.ReasonSuccess {
			message.MsgType = reactor.ChannelMsgStorageNotifyQueue
		} else {
			message.MsgType = reactor.ChannelMsgSendack
		}
		for _, result := range results {
			if result.LogId() == uint64(message.MessageId) {
				message.MessageSeq = result.LogIndex()
				break
			}
		}
	}
	reactor.Channel.AddMessages(fakeChannelId, channelType, messages)
}

// 转换成存储消息
func (c *Channel) toStorageMessages(messages []*reactor.ChannelMessage) []wkdb.Message {
	storages := make([]wkdb.Message, 0, len(messages))
	for _, m := range messages {
		msg := wkdb.Message{
			RecvPacket: wkproto.RecvPacket{
				Framer: wkproto.Framer{
					RedDot:    m.SendPacket.Framer.RedDot,
					SyncOnce:  m.SendPacket.Framer.SyncOnce,
					NoPersist: m.SendPacket.Framer.NoPersist,
				},
				Setting:     m.SendPacket.Setting,
				MessageID:   m.MessageId,
				MessageSeq:  uint32(m.MessageSeq),
				ClientMsgNo: m.SendPacket.ClientMsgNo,
				ClientSeq:   m.SendPacket.ClientSeq,
				FromUID:     m.Conn.Uid,
				ChannelID:   m.FakeChannelId,
				ChannelType: m.ChannelType,
				Expire:      m.SendPacket.Expire,
				Timestamp:   int32(time.Now().Unix()),
				Topic:       m.SendPacket.Topic,
				StreamNo:    m.SendPacket.StreamNo,
				Payload:     m.SendPacket.Payload,
			},
		}
		storages = append(storages, msg)
	}
	return storages
}
