package server

import (
	"fmt"

	reactor "github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// 收到消息
func (p *processUser) onMessage(m *proto.Message) {
	err := p.s.userProcessPool.Submit(func() {
		p.handleMessage(m)
	})
	if err != nil {
		p.Error("onMessage: submit error", zap.Error(err))
	}
}

func (p *processUser) handleMessage(m *proto.Message) {
	fmt.Println("onMessage------>", m.MsgType)
	switch msgType(m.MsgType) {
	// 节点加入
	case msgUserJoinReq:
		p.handleJoin(m)
	// 收到发件箱
	case msgOutboundReq:
		p.handleOutboundReq(m)
	}
}

// 收到加入请求
func (p *processUser) handleJoin(m *proto.Message) {
	req := &userJoinReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode joinReq failed", zap.Error(err))
		return
	}
	reactor.User.Join(req.uid, req.from)
}

func (p *processUser) handleOutboundReq(m *proto.Message) {
	req := &outboundReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("decode outbound failed", zap.Error(err))
		return
	}
	if req.fromNode == p.s.opts.Cluster.NodeId {
		p.Warn("outbound request from self", zap.Uint64("fromNode", req.fromNode))
		return
	}
	reactor.User.AddMessages(req.uid, req.messages)
}
