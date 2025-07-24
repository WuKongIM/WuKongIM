package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 客户端收到消息后的回执
func (h *Handler) recvack(event *eventbus.Event) {
	recvackPacket := event.Frame.(*wkproto.RecvackPacket)
	persist := !recvackPacket.NoPersist // 是否需要持久化
	// 记录消息路径
	event.Track.Record(track.PositionUserRecvack)

	trace.GlobalTrace.Metrics.App().RecvackPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().RecvackPacketBytesAdd(recvackPacket.GetFrameSize())

	conn := event.Conn
	isCmd := recvackPacket.SyncOnce                           // 是命令消息
	isMaster := conn.DeviceLevel == wkproto.DeviceLevelMaster // 是master设备，只有master设备才能擦除指令消息

	var currMsg *types.RetryMessage
	if persist {
		currMsg = service.RetryManager.RetryMessage(conn.NodeId, conn.ConnId, recvackPacket.MessageID)
	}
	if isCmd && persist && isMaster {
		if currMsg != nil {
			// 更新最近会话的已读位置
			err := service.Store.UpdateConversationIfSeqGreaterAsync(conn.Uid, currMsg.ChannelId, currMsg.ChannelType, uint64(recvackPacket.MessageSeq))
			if err != nil && err != wkdb.ErrNotFound {
				h.Error("UpdateConversationIfSeqGreaterAsync failed", zap.Error(err), zap.String("channelId", currMsg.ChannelId), zap.Uint8("channelType", currMsg.ChannelType), zap.Uint64("messageSeq", uint64(recvackPacket.MessageSeq)))
			}
		}
	}
	if persist { // 只有需要持久化的消息才会重试
		// r.Debug("remove retry", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId), zap.Int64("messageID", recvackPacket.MessageID))
		err := service.RetryManager.RemoveRetry(conn.NodeId, conn.ConnId, recvackPacket.MessageID)
		if err != nil {
			h.Warn("removeRetry error", zap.Error(err), zap.String("uid", conn.Uid), zap.String("deviceId", conn.DeviceId), zap.Int64("connId", conn.ConnId), zap.Uint64("nodeId", conn.NodeId), zap.Int64("messageID", recvackPacket.MessageID))
		}

		if options.G.Logger.TraceOn {
			if currMsg != nil {
				if currMsg.ChannelType == wkproto.ChannelTypePerson { // 只打印个人的 ，理论上currMsg应该一直有值。
					h.Trace("收到消息回执...",
						"recvack",
						zap.Int64("messageId", recvackPacket.MessageID),
						zap.Uint64("messageSeq", uint64(recvackPacket.MessageSeq)),
						zap.String("uid", conn.Uid),
						zap.String("deviceId", conn.DeviceId),
						zap.String("deviceFlag", conn.DeviceFlag.String()),
						zap.Int64("connId", conn.ConnId),
						zap.String("channelId", currMsg.ChannelId),
						zap.Uint8("channelType", currMsg.ChannelType),
					)
				}

			} else {
				h.Trace("收到消息回执",
					"recvack",
					zap.Int64("messageId", recvackPacket.MessageID),
					zap.Uint64("messageSeq", uint64(recvackPacket.MessageSeq)),
					zap.String("uid", conn.Uid),
					zap.String("deviceId", conn.DeviceId),
					zap.String("deviceFlag", conn.DeviceFlag.String()),
					zap.Int64("connId", conn.ConnId),
				)
			}

		}
	}

}
