package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/track"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (h *Handler) sendack(ctx *eventbus.ChannelContext) {
	events := ctx.Events
	for _, e := range events {
		sendPacket := e.Frame.(*wkproto.SendPacket)
		e.Track.Record(track.PositionChannelSendack)
		if options.G.Logger.TraceOn {
			h.Trace(e.Track.String(),
				"sendack",
				zap.Int64("messageId", e.MessageId),
				zap.Uint64("messageSeq", e.MessageSeq),
				zap.Uint64("clientSeq", sendPacket.ClientSeq),
				zap.String("clientMsgNo", sendPacket.ClientMsgNo),
				zap.String("channelId", ctx.ChannelId),
				zap.Uint8("channelType", ctx.ChannelType),
				zap.String("reasonCode", e.ReasonCode.String()),
				zap.String("conn.uid", e.Conn.Uid),
				zap.String("conn.deviceId", e.Conn.DeviceId),
				zap.Uint64("conn.fromNode", e.Conn.NodeId),
				zap.Int64("conn.connId", e.Conn.ConnId),
			)
		}
	}

	var uidMap = make(map[string]struct{}, len(events))
	for _, e := range events {
		// 系统发的不需要回执
		if options.G.IsSystemDevice(e.Conn.DeviceId) {
			continue
		}

		sendPacket := e.Frame.(*wkproto.SendPacket)
		eventbus.User.ConnWrite(e.Conn, &wkproto.SendackPacket{
			Framer:      sendPacket.Framer,
			MessageID:   e.MessageId,
			MessageSeq:  uint32(e.MessageSeq),
			ClientMsgNo: sendPacket.ClientMsgNo,
			ClientSeq:   sendPacket.ClientSeq,
			ReasonCode:  wkproto.ReasonCode(e.ReasonCode),
		})
		uidMap[e.Conn.Uid] = struct{}{}
	}

	// 推进
	for uid := range uidMap {
		eventbus.User.Advance(uid)
	}

}
