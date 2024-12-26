package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"go.uber.org/zap"
)

func (h *Handler) writeFrame(ctx *eventbus.UserContext) {
	for _, event := range ctx.Events {
		conn := event.Conn
		frame := event.Frame
		if conn == nil || frame == nil {
			h.Error("write frame conn or frame is nil")
			continue
		}
		if conn.NodeId == 0 {
			h.Error("writeFrame: conn node id is 0")
			continue
		}
		// 如果不是本地节点，则转发写请求
		if !options.G.IsLocalNode(conn.NodeId) {
			h.forwardsToNode(conn.NodeId, conn.Uid, []*eventbus.Event{event})
			continue
		}

		// 本地节点写请求
		h.writeLocalFrame(event)

	}
}
func (h *Handler) writeLocalFrame(event *eventbus.Event) {
	conn := event.Conn
	frame := event.Frame
	data, err := eventbus.Proto.EncodeFrame(frame, conn.ProtoVersion)
	if err != nil {
		h.Error("writeFrame: encode frame err", zap.Error(err))
	}

	// 记录消息路径
	event.Track.Record(track.PositionConnWrite)

	// 统计发送消息数（TODO: 这里不准，暂时视包数为消息数）
	conn.OutMsgCount.Add(1)
	conn.OutMsgByteCount.Add(int64(len(data)))
	// 统计发送包数
	conn.OutPacketCount.Add(1)
	conn.OutPacketByteCount.Add(int64(len(data)))

	// 获取到真实连接
	realConn := service.ConnManager.GetConn(conn.ConnId)
	if realConn == nil {
		h.Error("writeFrame: conn not exist", zap.String("uid", conn.Uid), zap.Int64("connId", conn.ConnId))
		return
	}
	wsConn, wsok := realConn.(wknet.IWSConn) // websocket连接
	if wsok {
		err := wsConn.WriteServerBinary(data)
		if err != nil {
			h.Warn("writeFrame: Failed to ws write the message", zap.Error(err))
		}
	} else {
		_, err := realConn.WriteToOutboundBuffer(data)
		if err != nil {
			h.Warn("writeFrame: Failed to write the message", zap.Error(err))
		}
	}
	_ = realConn.WakeWrite()
}
