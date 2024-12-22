package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"go.uber.org/zap"
)

// 向连接写数据
func (p *User) processWrite(a reactor.UserAction) {

	if len(a.Messages) == 0 {
		return
	}
	for _, m := range a.Messages {
		if m.Conn == nil {
			continue
		}
		if m.Conn.FromNode == 0 {
			continue
		}
		if !options.G.IsLocalNode(m.Conn.FromNode) {
			m.ToNode = m.Conn.FromNode // 指定发送节点
			reactor.User.AddMessageToOutbound(a.Uid, m)
			continue
		}
		// 记录消息路径
		m.Track.Record(track.PositionConnWrite)

		// 统计发送消息数（TODO: 这里不准，暂时视包数为消息数）
		m.Conn.OutMsgCount.Add(1)
		m.Conn.OutMsgByteCount.Add(int64(len(m.WriteData)))
		// 统计发送包数
		m.Conn.OutPacketCount.Add(1)
		m.Conn.OutPacketByteCount.Add(int64(len(m.WriteData)))
		// 获取真实连接
		conn := service.ConnManager.GetConn(m.Conn.ConnId)
		if conn == nil {
			// p.Warn("processWrite: conn not exist", zap.String("uid", a.Uid), zap.Uint64("fromNode", m.Conn.FromNode), zap.Int64("connId", m.Conn.ConnId))
			continue
		}
		wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
		if wsok {
			err := wsConn.WriteServerBinary(m.WriteData)
			if err != nil {
				p.Warn("Failed to ws write the message", zap.Error(err))
			}
		} else {
			_, err := conn.WriteToOutboundBuffer(m.WriteData)
			if err != nil {
				p.Warn("Failed to write the message", zap.Error(err))
			}
		}
		_ = conn.WakeWrite()
	}

}
