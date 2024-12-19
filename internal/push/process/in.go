package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// 收到消息
func (p *Push) OnMessage(m *proto.Message) {
	err := p.processPool.Submit(func() {
		p.handleMessage(m)
	})
	if err != nil {
		p.Error("Diffuse onMessage: submit error", zap.Error(err))
	}
}

func (p *Push) handleMessage(m *proto.Message) {
	switch msgType(m.MsgType) {
	case msgOutboundReq:
		p.handleOutboundReq(m)
	}
}

func (p *Push) handleOutboundReq(m *proto.Message) {
	req := &outboundReq{}
	err := req.decode(m.Content)
	if err != nil {
		p.Error("push: decode outboundReq failed", zap.Error(err))
		return
	}
	if options.G.IsLocalNode(req.fromNode) {
		p.Warn("push: outbound request from self", zap.Uint64("fromNode", req.fromNode))
		return
	}
	reactor.Push.PushMessages(req.messages)
}
