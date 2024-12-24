package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// 收到消息
func (p *Channel) OnMessage(m *proto.Message) {
	err := p.processPool.Submit(func() {
		p.handleMessage(m)
	})
	if err != nil {
		p.Error("onMessage: submit error", zap.Error(err))
	}
}

func (p *Channel) handleMessage(m *proto.Message) {
	// fmt.Println("recv------>", msgType(m.MsgType).String())
	switch msgType(m.MsgType) {
	case msgSendack:
		p.handleSendack(m)
	case msgOutboundReq:
		p.handleOutboundReq(m)
	case msgChannelJoinReq:
		p.handleJoin(m)
	case msgChannelJoinResp:
		p.handleJoinResp(m)
	case msgNodeHeartbeatReq:
		p.handleHeartbeatReq(m)
	case msgNodeHeartbeatResp:
		p.handleHeartbeatResp(m)

	}
}

func (p *Channel) handleSendack(m *proto.Message) {
	batchReq := sendackBatchReq{}
	err := batchReq.decode(m.Content)
	if err != nil {
		p.Error("decode sendackReq failed", zap.Error(err))
		return
	}
	p.sendSendack(batchReq)
}

func (p *Channel) handleOutboundReq(m *proto.Message) {
	req := &outboundReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode outboundReq failed", zap.Error(err))
		return
	}
	if options.G.IsLocalNode(req.fromNode) {
		p.Warn("channel: outbound request from self", zap.Uint64("fromNode", req.fromNode))
		return
	}

	reactor.Channel.WakeIfNeed(req.channelId, req.channelType)
	reactor.Channel.AddMessages(req.channelId, req.channelType, req.messages)
}

func (p *Channel) handleJoin(m *proto.Message) {
	req := &channelJoinReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode joinReq failed", zap.Error(err))
		return
	}
	reactor.Channel.Join(req.channelId, req.channelType, req.from)
}

func (p *Channel) handleJoinResp(m *proto.Message) {
	resp := &channelJoinResp{}
	err := resp.decode(m.Content)
	if err != nil {
		p.Error("decode joinResp failed", zap.Error(err))
		return
	}
	reactor.Channel.JoinResp(resp.channelId, resp.channelType, resp.from)
}

func (p *Channel) handleHeartbeatReq(m *proto.Message) {
	req := &nodeHeartbeatReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode heartbeatReq failed", zap.Error(err))
		return
	}
	reactor.Channel.HeartbeatReq(req.channelId, req.channelType, req.fromNode)
}

func (p *Channel) handleHeartbeatResp(m *proto.Message) {
	resp := &nodeHeartbeatResp{}
	err := resp.decode(m.Content)
	if err != nil {
		p.Error("decode heartbeatResp failed", zap.Error(err))
		return
	}
	reactor.Channel.HeartbeatResp(resp.channelId, resp.channelType, resp.fromNode)
}
