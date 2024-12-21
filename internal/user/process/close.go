package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 连接关闭了
func (u *User) processConnClose(a reactor.UserAction) {
	if len(a.Conns) == 0 {
		u.Warn("processConnClose: conns is empty", zap.String("uid", a.Uid))
		return
	}
	for _, c := range a.Conns {
		if !options.G.IsLocalNode(c.FromNode) {
			u.Info("processConnClose: conn not local node", zap.String("uid", a.Uid), zap.Uint64("fromNode", c.FromNode))
			continue
		}

		if c.Auth {
			deviceOnlineCount := reactor.User.ConnCountByDeviceFlag(c.Uid, c.DeviceFlag)
			totalOnlineCount := reactor.User.ConnCountByUid(c.Uid)
			service.Webhook.Offline(c.Uid, wkproto.DeviceFlag(c.DeviceFlag), c.ConnId, deviceOnlineCount, totalOnlineCount) // 触发离线webhook
		}

		conn := service.ConnManager.GetConn(c.ConnId)
		if conn == nil {
			u.Debug("processConnClose: conn not exist", zap.String("uid", a.Uid), zap.Int64("connId", c.ConnId))
			continue
		}
		err := conn.Close()
		if err != nil {
			u.Debug("Failed to close the conn", zap.Error(err))
		}
	}
}

// 用户关闭了
func (u *User) processUserClose(a reactor.UserAction) {
	for _, c := range a.Conns {
		conn := service.ConnManager.GetConn(c.ConnId)
		if conn == nil {
			continue
		}
		err := conn.Close()
		if err != nil {
			u.Debug("Failed to close the conn", zap.Error(err))
		}
	}
}
