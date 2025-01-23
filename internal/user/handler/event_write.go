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
			// 统计
			h.totalOut(conn, frame)
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
	// 统计
	h.totalOut(conn, frame)
	// 记录消息路径
	event.Track.Record(track.PositionConnWrite)

	// 获取到真实连接
	realConn := service.ConnManager.GetConn(conn.ConnId)
	if realConn == nil {
		h.Info("writeFrame: conn not exist", zap.String("uid", conn.Uid), zap.Uint64("nodeId", conn.NodeId), zap.Int64("connId", conn.ConnId), zap.Uint64("sourceNodeId", event.SourceNodeId))
		// 如果连接不存在了，并且写入事件是其他节点发起的，说明其他节点还不知道连接已经关闭，需要通知其他节点关闭连接
		if event.SourceNodeId != 0 && !options.G.IsLocalNode(event.SourceNodeId) {
			h.forwardToNode(event.SourceNodeId, conn.Uid, &eventbus.Event{
				Type:         eventbus.EventConnRemove,
				Conn:         conn,
				SourceNodeId: options.G.Cluster.NodeId,
			})
		} else if event.SourceNodeId == 0 || options.G.IsLocalNode(event.SourceNodeId) { // 如果是本节点事件，直接删除连接
			eventbus.User.RemoveConn(conn)
		}
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
