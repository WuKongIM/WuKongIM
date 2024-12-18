package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (p *User) Send(actions []reactor.UserAction) {

	var err error
	for _, a := range actions {
		err = p.processPool.Submit(func() {
			p.processAction(a)
		})
		if err != nil {
			p.Error("submit err", zap.Error(err), zap.String("uid", a.Uid), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

// 用户行为逻辑处理
func (p *User) processAction(a reactor.UserAction) {
	switch a.Type {
	case reactor.UserActionElection: // 选举
		p.processElection(a)
	case reactor.UserActionJoin: // 处理加入
		p.processJoin(a)
	case reactor.UserActionJoinResp: // 加入返回
		p.processJoinResp(a)
	case reactor.UserActionOutboundForward: // 处理发件箱
		p.processOutbound(a)
	case reactor.UserActionInbound: // 处理收件箱
		p.processInbound(a)
	case reactor.UserActionWrite: // 连接写
		p.processWrite(a)
	case reactor.UserActionNodeHeartbeatReq: // 领导发起心跳
		p.processNodeHeartbeatReq(a)
	case reactor.UserActionNodeHeartbeatResp: // 节点心跳回执
		p.processNodeHeartbeatResp(a)
	case reactor.UserActionConnClose: // 连接关闭
		p.processConnClose(a)
	case reactor.UserActionUserClose:
		fmt.Println("UserActionUserClose....", a.Uid)
	default:
	}
}

func (p *User) processInbound(a reactor.UserAction) {
	if len(a.Messages) == 0 {
		return
	}
	// 从收件箱中取出消息
	for _, m := range a.Messages {
		if m.Frame == nil {
			continue
		}
		switch m.Frame.GetFrameType() {
		// 客户端请求连接
		case wkproto.CONNECT:
			if a.Role == reactor.RoleLeader {
				p.processConnect(a.Uid, m)
			} else {
				// 如果不是领导节点，则专投递给发件箱这样就会被领导节点处理
				reactor.User.AddMessageToOutbound(a.Uid, m)
			}
			// 服务器回应连接(其他节点发来的，并不是客户端发来的)
		case wkproto.CONNACK:
			p.processConnack(a.Uid, m)
			// 客户端请求心跳
		case wkproto.PING:
			p.processPing(m)
			// 客户端发送消息
		case wkproto.SEND:
			p.processSend(m)
			// 客户端收到消息回执
		case wkproto.RECVACK:
			if a.Role == reactor.RoleLeader {
				p.processRecvack(m)
			} else {
				reactor.User.AddMessageToOutbound(a.Uid, m)
			}

		}
	}
}

// 处理选举
func (p *User) processElection(a reactor.UserAction) {
	slotId := service.Cluster.GetSlotId(a.Uid)
	leaderInfo, err := service.Cluster.SlotLeaderNodeInfo(slotId)
	if err != nil {
		p.Error("get slot leader info failed", zap.Error(err), zap.Uint32("slotId", slotId))
		return
	}
	if leaderInfo == nil {
		p.Error("slot not exist", zap.Uint32("slotId", slotId))
		return
	}
	if leaderInfo.Id == 0 {
		p.Error("slot leader id is 0", zap.Uint32("slotId", slotId))
		return
	}

	reactor.User.UpdateConfig(a.Uid, reactor.UserConfig{
		LeaderId: leaderInfo.Id,
	})
}

func (p *User) processJoin(a reactor.UserAction) {
	req := &userJoinReq{
		from: options.G.Cluster.NodeId,
		uid:  a.Uid,
	}

	err := p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgUserJoinReq),
		Content: req.encode(),
	})
	if err != nil {
		p.Error("send join req failed", zap.Error(err))
		return
	}
}

func (p *User) processJoinResp(a reactor.UserAction) {

	resp := &userJoinResp{
		uid:  a.Uid,
		from: options.G.Cluster.NodeId,
	}

	err := p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgUserJoinResp),
		Content: resp.encode(),
	})
	if err != nil {
		p.Error("send join resp failed", zap.Error(err))
		return
	}
}

func (p *User) processConnClose(a reactor.UserAction) {
	if len(a.Conns) == 0 {
		p.Warn("processConnClose: conns is empty", zap.String("uid", a.Uid))
		return
	}
	for _, c := range a.Conns {
		if !options.G.IsLocalNode(c.FromNode) {
			p.Info("processConnClose: conn not local node", zap.String("uid", a.Uid), zap.Uint64("fromNode", c.FromNode))
			continue
		}
		conn := service.ConnManager.GetConn(c.ConnId)
		if conn == nil {
			p.Warn("processConnClose: conn not exist", zap.String("uid", a.Uid), zap.Int64("connId", c.ConnId))
			continue
		}
		err := conn.Close()
		if err != nil {
			p.Debug("Failed to close the conn", zap.Error(err))
		}
	}
}

func (p *User) processOutbound(a reactor.UserAction) {
	if len(a.Messages) == 0 {
		p.Warn("processOutbound: messages is empty", zap.String("actionType", a.Type.String()))
		return
	}
	req := &outboundReq{
		fromNode: options.G.Cluster.NodeId,
		uid:      a.Uid,
		messages: a.Messages,
	}
	data, err := req.encode()
	if err != nil {
		p.Error("encode failed", zap.Error(err))
		return
	}

	err = p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgOutboundReq),
		Content: data,
	})
	if err != nil {
		p.Error("processOutbound: send failed", zap.Error(err))
	}

}

func (p *User) processWrite(a reactor.UserAction) {

	if len(a.Messages) == 0 {
		return
	}
	for _, m := range a.Messages {
		if m.Conn == nil {
			continue
		}
		if m.Conn.FromNode == 0 {
			fmt.Println("processWrite: from node is 0", a.Uid)
			continue
		}
		if !options.G.IsLocalNode(m.Conn.FromNode) {
			reactor.User.AddMessageToOutbound(a.Uid, m)
			continue
		}
		conn := service.ConnManager.GetConn(m.Conn.ConnId)
		if conn == nil {
			p.Warn("processWrite: conn not exist", zap.String("uid", a.Uid), zap.Uint64("fromNode", m.Conn.FromNode), zap.Int64("connId", m.Conn.ConnId))
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

func (p *User) processNodeHeartbeatReq(a reactor.UserAction) {

	connIds := make([]int64, 0)
	for _, c := range a.Conns {
		connIds = append(connIds, c.ConnId)
	}
	req := &nodeHeartbeatReq{
		uid:      a.Uid,
		fromNode: options.G.Cluster.NodeId,
		connIds:  connIds,
	}
	err := p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgNodeHeartbeatReq),
		Content: req.encode(),
	})
	if err != nil {
		p.Error("send node heartbeat req failed", zap.Error(err))
	}
}

func (p *User) processNodeHeartbeatResp(a reactor.UserAction) {
	conns := reactor.User.ConnsByUid(a.Uid)
	connIds := make([]int64, 0, len(conns))
	for _, conn := range conns {
		connIds = append(connIds, conn.ConnId)
	}
	resp := &nodeHeartbeatResp{
		uid:      a.Uid,
		fromNode: options.G.Cluster.NodeId,
		connIds:  connIds,
	}
	err := p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgNodeHeartbeatResp),
		Content: resp.encode(),
	})
	if err != nil {
		p.Error("send node heartbeat resp failed", zap.Error(err))
	}
}

func (p *User) sendToNode(toNodeId uint64, msg *proto.Message) error {

	err := service.Cluster.Send(toNodeId, msg)
	return err
}

func (p *User) processConnect(uid string, msg *reactor.UserMessage) {
	reasonCode, packet, err := p.handleConnect(msg)
	if err != nil {
		p.Error("handle connect failed", zap.Error(err), zap.String("uid", uid))
		return
	}
	if reasonCode != wkproto.ReasonSuccess && packet == nil {
		packet = &wkproto.ConnackPacket{
			ReasonCode: reasonCode,
		}
	}

	reactor.User.AddMessage(uid, &reactor.UserMessage{
		Conn:   msg.Conn,
		Frame:  packet,
		ToNode: msg.Conn.FromNode,
	})
}

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

func (p *User) processPing(msg *reactor.UserMessage) {
	p.handlePing(msg)
}

func (p *User) processSend(msg *reactor.UserMessage) {
	p.handleSend(msg)
}
