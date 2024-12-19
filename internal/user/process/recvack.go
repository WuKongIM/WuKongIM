package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (u *User) processRecvack(msg *reactor.UserMessage) {
	recvackPacket := msg.Frame.(*wkproto.RecvackPacket)
	persist := !recvackPacket.NoPersist // 是否需要持久化

	trace.GlobalTrace.Metrics.App().RecvackPacketCountAdd(1)
	trace.GlobalTrace.Metrics.App().RecvackPacketBytesAdd(recvackPacket.GetFrameSize())

	conn := msg.Conn
	isCmd := recvackPacket.SyncOnce                           // 是命令消息
	isMaster := conn.DeviceLevel == wkproto.DeviceLevelMaster // 是master设备，只有master设备才能擦除指令消息

	if isCmd && persist && isMaster {
		currMsg := service.RetryManager.RetryMessage(conn.FromNode, conn.ConnId, recvackPacket.MessageID)
		if currMsg != nil {
			// 删除最近会话的缓存
			service.ConversationManager.DeleteFromCache(conn.Uid, currMsg.ChannelId, currMsg.ChannelType)
			// 更新最近会话的已读位置
			err := service.Store.DB().UpdateConversationIfSeqGreaterAsync(conn.Uid, currMsg.ChannelId, currMsg.ChannelType, uint64(recvackPacket.MessageSeq))
			if err != nil {
				u.Error("UpdateConversationIfSeqGreaterAsync failed", zap.Error(err), zap.String("channelId", currMsg.ChannelId), zap.Uint8("channelType", currMsg.ChannelType), zap.Uint64("messageSeq", uint64(recvackPacket.MessageSeq)))
			}
		}
	}
	if persist { // 只有需要持久化的消息才会重试
		// r.Debug("remove retry", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId), zap.Int64("messageID", recvackPacket.MessageID))
		err := service.RetryManager.RemoveRetry(conn.FromNode, conn.ConnId, recvackPacket.MessageID)
		if err != nil {
			u.Warn("removeRetry error", zap.Error(err), zap.String("uid", conn.Uid), zap.String("deviceId", conn.DeviceId), zap.Int64("connId", conn.ConnId), zap.Uint64("fromNode", conn.FromNode), zap.Int64("messageID", recvackPacket.MessageID))
		}
	}

	// service.RetryManager.Remove(msg.Uid, msg.MsgId)
}
