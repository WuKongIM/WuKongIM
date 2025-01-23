package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (h *Handler) closeConn(ctx *eventbus.UserContext) {
	events := ctx.Events
	for _, e := range events {
		conn := e.Conn
		if conn == nil {
			h.Error("closeConn: conn is nil")
			return
		}
		if conn.NodeId == 0 {
			h.Error("closeConn: conn node id is 0")
			return
		}
		// 移除逻辑连接
		eventbus.User.RemoveConn(conn)

		// 如果不是本地节点，则转发关闭请求
		if !options.G.IsLocalNode(conn.NodeId) {
			h.forwardToNode(conn.NodeId, conn.Uid, e)
			return
		}

		// 关闭真实连接
		realConn := service.ConnManager.GetConn(conn.ConnId)
		if realConn == nil {
			h.Info("closeConn: conn not exist", zap.String("uid", conn.Uid), zap.Int64("connId", conn.ConnId))
			continue
		}
		err := realConn.Close()
		if err != nil {
			h.Info("closeConn: Failed to close the conn", zap.Error(err))
		}
	}
}

func (h *Handler) removeConn(ctx *eventbus.UserContext) {

	for _, event := range ctx.Events {
		eventbus.User.RemoveConn(event.Conn)
		if event.Conn.Auth {
			h.notifyUserOfflineIfNeed(event.Conn)
		}
	}

}

func (h *Handler) connLeaderRemove(ctx *eventbus.UserContext) {
	h.removeConn(ctx)
}

func (h *Handler) notifyUserOfflineIfNeed(conn *eventbus.Conn) {
	slotId := service.Cluster.GetSlotId(conn.Uid)
	if slotId == 0 {
		return
	}
	// 通知用户下线
	leaderId := service.Cluster.SlotLeaderId(slotId)
	if options.G.IsLocalNode(leaderId) {
		deviceOnlineCount := eventbus.User.ConnCountByDeviceFlag(conn.Uid, conn.DeviceFlag)
		totalOnlineCount := eventbus.User.ConnCountByUid(conn.Uid)
		service.Webhook.Offline(conn.Uid, wkproto.DeviceFlag(conn.DeviceFlag), conn.ConnId, deviceOnlineCount, totalOnlineCount) // 触发离线webhook
	}

}
