package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (p *User) processConnack(uid string, msg *reactor.UserMessage) {
	conn := msg.Conn
	if conn.FromNode == 0 {
		p.Error("processConnack: from node is 0", zap.String("uid", uid))
		return
	}
	if msg.Frame == nil {
		p.Error("processConnack: frame is nil", zap.String("uid", uid))
		return
	}
	if options.G.IsLocalNode(conn.FromNode) {
		connack := msg.Frame.(*wkproto.ConnackPacket)
		if connack.ReasonCode == wkproto.ReasonSuccess {
			realConn := service.ConnManager.GetConn(conn.ConnId)
			if realConn != nil {
				realConn.SetMaxIdle(options.G.ConnIdleTime)
				realConn.SetContext(conn)
			}
			connack.NodeId = options.G.Cluster.NodeId
			// 更新连接
			reactor.User.UpdateConn(conn)
		}
		reactor.User.ConnWrite(conn, connack)
	} else {
		reactor.User.AddMessageToOutbound(uid, msg)
	}
}
