package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
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
		conn := service.ConnManager.GetConn(c.ConnId)
		if conn == nil {
			u.Warn("processConnClose: conn not exist", zap.String("uid", a.Uid), zap.Int64("connId", c.ConnId))
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
	fmt.Println("user close--->", a.Uid)
	for _, c := range a.Conns {
		fmt.Println("close conn--->", c.ConnId, c.FromNode, c.Uid)
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
