package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type processUser struct {
	s *Server
	wklog.Log
}

func newProcessUser(s *Server) *processUser {

	return &processUser{
		s:   s,
		Log: wklog.NewWKLog("processUser"),
	}
}

func (p *processUser) send(actions []reactor.UserAction) {

	var err error
	for _, a := range actions {
		err = p.s.userProcessPool.Submit(func() {
			p.processAction(a)
		})
		if err != nil {
			p.Error("submit err", zap.Error(err), zap.String("uid", a.Uid), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

func (p *processUser) processAction(a reactor.UserAction) {
	switch a.Type {
	case reactor.UserActionElection: // 选举
		p.processElection(a)
	case reactor.UserActionJoin: // 加入
		p.processJoin(a)
	case reactor.UserActionJoinResp: // 加入返回
		p.processJoinResp(a)
	case reactor.UserActionOutboundForward: // 发件
		p.processOutbound(a)
	case reactor.UserActionInbound: // 收件
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

// 处理选举
func (p *processUser) processElection(a reactor.UserAction) {
	slotId := p.s.cluster.GetSlotId(a.Uid)
	leaderInfo, err := p.s.cluster.SlotLeaderNodeInfo(slotId)
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

func (p *processUser) processJoin(a reactor.UserAction) {
	req := &userJoinReq{
		from: p.s.opts.Cluster.NodeId,
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

func (p *processUser) processJoinResp(a reactor.UserAction) {

	resp := &userJoinResp{
		uid:  a.Uid,
		from: p.s.opts.Cluster.NodeId,
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

func (p *processUser) processConnClose(a reactor.UserAction) {
	if len(a.Conns) == 0 {
		p.Warn("processConnClose: conns is empty", zap.String("uid", a.Uid))
		return
	}
	for _, c := range a.Conns {
		if !p.s.opts.IsLocalNode(c.FromNode) {
			p.Info("processConnClose: conn not local node", zap.String("uid", a.Uid), zap.Uint64("fromNode", c.FromNode))
			continue
		}
		conn := p.s.connManager.getConn(c.ConnId)
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

func (p *processUser) processOutbound(a reactor.UserAction) {
	if len(a.Messages) == 0 {
		p.Warn("processOutbound: messages is empty", zap.String("actionType", a.Type.String()))
		return
	}
	req := &outboundReq{
		fromNode: p.s.opts.Cluster.NodeId,
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

func (p *processUser) processInbound(a reactor.UserAction) {
	if len(a.Messages) == 0 {
		return
	}
	// 从收件箱中取出消息
	for _, m := range a.Messages {
		if m.Frame == nil {
			continue
		}
		switch m.Frame.GetFrameType() {
		case wkproto.CONNECT: // 连接包
			if a.Role == reactor.RoleLeader {
				p.processConnect(a.Uid, m)
			} else {
				// 如果不是领导节点，则专投递给发件箱这样就会被领导节点处理
				reactor.User.AddMessageToOutbound(a.Uid, m)
			}
		case wkproto.CONNACK: // 连接回执包
			p.processConnack(a.Uid, m)
		case wkproto.PING: // 心跳包
			p.processPing(m)
		case wkproto.SEND: // 发送消息
			p.processSend(m)
		}
	}
}

func (p *processUser) processWrite(a reactor.UserAction) {

	if len(a.Messages) == 0 {
		return
	}
	for _, m := range a.Messages {
		if m.Conn == nil {
			continue
		}
		if !p.s.opts.IsLocalNode(m.Conn.FromNode) {
			reactor.User.AddMessageToOutbound(a.Uid, m)
			continue
		}
		conn := p.s.connManager.getConn(m.Conn.ConnId)
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

func (p *processUser) processNodeHeartbeatReq(a reactor.UserAction) {

	connIds := make([]int64, 0)
	for _, c := range a.Conns {
		connIds = append(connIds, c.ConnId)
	}
	req := &nodeHeartbeatReq{
		uid:      a.Uid,
		fromNode: p.s.opts.Cluster.NodeId,
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

func (p *processUser) processNodeHeartbeatResp(a reactor.UserAction) {
	conns := reactor.User.ConnsByUid(a.Uid)
	connIds := make([]int64, 0, len(conns))
	for _, conn := range conns {
		connIds = append(connIds, conn.ConnId)
	}
	resp := &nodeHeartbeatResp{
		uid:      a.Uid,
		fromNode: p.s.opts.Cluster.NodeId,
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

func (p *processUser) sendToNode(toNodeId uint64, msg *proto.Message) error {

	err := p.s.cluster.Send(toNodeId, msg)
	return err
}

func (p *processUser) processConnect(uid string, msg *reactor.UserMessage) {
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

func (p *processUser) processConnack(uid string, msg *reactor.UserMessage) {
	conn := msg.Conn
	if conn.FromNode == 0 {
		p.Error("processConnack: from node is 0", zap.String("uid", uid))
		return
	}
	if msg.Frame == nil {
		p.Error("processConnack: frame is nil", zap.String("uid", uid))
		return
	}
	if p.s.opts.IsLocalNode(conn.FromNode) {
		connack := msg.Frame.(*wkproto.ConnackPacket)
		if connack.ReasonCode == wkproto.ReasonSuccess {
			realConn := p.s.connManager.getConn(conn.ConnId)
			if realConn != nil {
				realConn.SetMaxIdle(p.s.opts.ConnIdleTime)
				realConn.SetContext(conn)
			}
			connack.NodeId = p.s.opts.Cluster.NodeId
			// 更新连接
			reactor.User.UpdateConn(conn)
		}
		reactor.User.ConnWrite(conn, connack)
	} else {
		reactor.User.AddMessageToOutbound(uid, msg)
	}
}

func (p *processUser) processPing(msg *reactor.UserMessage) {
	p.handlePing(msg)
}

func (p *processUser) processSend(msg *reactor.UserMessage) {
	p.handleSend(msg)
}
