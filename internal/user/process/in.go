package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// 收到消息
func (p *User) OnMessage(m *proto.Message) {
	err := p.processPool.Submit(func() {
		p.handleMessage(m)
	})
	if err != nil {
		p.Error("onMessage: submit error", zap.Error(err))
	}
}

func (p *User) handleMessage(m *proto.Message) {
	// fmt.Println("recv------>", msgType(m.MsgType).String())
	switch msgType(m.MsgType) {
	// 节点加入
	case msgUserJoinReq:
		p.handleJoin(m)
		// 加入返回
	case msgUserJoinResp:
		p.handleJoinResp(m)
		// 收到发件箱
	case msgOutboundReq:
		p.handleOutboundReq(m)
		// 心跳
	case msgNodeHeartbeatReq:
		p.handleNodeHeartbeatReq(m)
		// 心跳回执
	case msgNodeHeartbeatResp:
		p.handleNodeHeartbeatResp(m)
	}
}

// 收到加入请求
func (p *User) handleJoin(m *proto.Message) {
	req := &userJoinReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode joinReq failed", zap.Error(err))
		return
	}
	reactor.User.Join(req.uid, req.from)
}

func (p *User) handleJoinResp(m *proto.Message) {
	resp := &userJoinResp{}
	err := resp.decode(m.Content)
	if err != nil {
		p.Error("decode joinResp failed", zap.Error(err))
		return
	}
	reactor.User.JoinResp(resp.uid)
}

func (p *User) handleOutboundReq(m *proto.Message) {
	req := &outboundReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode outbound failed", zap.Error(err), zap.Int("data", len(m.Content)))
		return
	}
	if req.fromNode == options.G.Cluster.NodeId {
		p.Warn("outbound request from self", zap.Uint64("fromNode", req.fromNode))
		return
	}

	// var authMsg *reactor.UserMessage // 认证消息
	// for _, msg := range req.messages {
	// 	if msg.Frame != nil && msg.Frame.GetFrameType() == wkproto.CONNECT {
	// 		authMsg = msg
	// 		break
	// 	}
	// }
	// 唤醒用户
	reactor.User.WakeIfNeed(req.uid)

	for _, msg := range req.messages {
		isWrite := msg.Conn != nil && msg.Frame == nil && len(msg.WriteData) > 0 // 是否是写消息
		if isWrite && msg.Conn.FromNode == options.G.Cluster.NodeId {
			reactor.User.ConnWriteBytesNoAdvance(msg.Conn, msg.WriteData)
			continue
		}
		reactor.User.AddMessageNoAdvance(req.uid, msg)
	}
	reactor.User.Advance(req.uid)

}

func (p *User) handleNodeHeartbeatReq(m *proto.Message) {
	req := &nodeHeartbeatReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode nodeHeartbeatReq failed", zap.Error(err))
		return
	}
	reactor.User.HeartbeatReq(req.uid, req.fromNode, req.connIds)
}

func (p *User) handleNodeHeartbeatResp(m *proto.Message) {
	resp := &nodeHeartbeatResp{}
	err := resp.decode(m.Content)
	if err != nil {
		p.Error("decode nodeHeartbeatResp failed", zap.Error(err))
		return
	}
	reactor.User.HeartbeatResp(resp.uid, resp.fromNode, resp.connIds)
}
