package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
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
	switch options.ReactorMsgType(m.MsgType) {
	case options.ReactorChannelMsgTypeSendack:
		p.handleSendack(m)
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
