package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 权限判断
func (h *Handler) permission(ctx *eventbus.ChannelContext) {

	events := ctx.Events
	channelId := ctx.ChannelId
	channelType := ctx.ChannelType
	// 记录消息轨迹
	for _, event := range events {
		event.Track.Record(track.PositionChannelPermission)
	}

	// --------------- 判断频道权限 ----------------
	reasonCode, err := service.Permission.HasPermissionForChannel(channelId, channelType)
	if err != nil {
		h.Error("hasPermissionForChannel error", zap.Error(err))
		reasonCode = wkproto.ReasonSystemError
	}
	if reasonCode != wkproto.ReasonSuccess {
		h.Info("hasPermissionForChannel failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("reasonCode", reasonCode.String()))
		for _, event := range events {
			event.ReasonCode = reasonCode
		}
		return
	}

	// --------------- 判断发送者权限 ----------------
	for _, event := range events {
		senderInfo := service.SenderInfo{
			UID:      event.Conn.Uid,
			DeviceID: event.Conn.DeviceId,
		}
		reasonCode, err = service.Permission.HasPermissionForSender(channelId, channelType, senderInfo)
		if err != nil {
			h.Error("hasPermissionForSender error", zap.Error(err), zap.String("fromUid", event.Conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Int("events", len(events)))
			reasonCode = wkproto.ReasonSystemError
		} else if reasonCode != wkproto.ReasonSuccess {
			h.Info("hasPermissionForSender failed", zap.String("fromUid", event.Conn.Uid), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("reasonCode", reasonCode.String()))
		}
		event.ReasonCode = reasonCode
	}

}
