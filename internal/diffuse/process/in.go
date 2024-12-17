package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

// 收到消息
func (d *Diffuse) OnMessage(m *proto.Message) {
	err := d.processPool.Submit(func() {
		d.handleMessage(m)
	})
	if err != nil {
		d.Error("Diffuse onMessage: submit error", zap.Error(err))
	}
}

func (d *Diffuse) handleMessage(m *proto.Message) {
	switch msgType(m.MsgType) {
	case msgOutboundReq:
		d.handleOutboundReq(m)
	}
}

func (d *Diffuse) handleOutboundReq(m *proto.Message) {
	req := &outboundReq{}
	err := req.decode(m.Content)
	if err != nil {
		d.Error("diffuse: decode outboundReq failed", zap.Error(err))
		return
	}
	if options.G.IsLocalNode(req.fromNode) {
		d.Warn("diffuse: outbound request from self", zap.Uint64("fromNode", req.fromNode))
		return
	}
	reactor.Diffuse.AddMessages(req.messages)
}
