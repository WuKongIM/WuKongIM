package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (d *Diffuse) Send(actions []reactor.DiffuseAction) {
	var err error
	for _, a := range actions {
		err = d.processPool.Submit(func() {
			d.processAction(a)
		})
		if err != nil {
			d.Error("diffuse submit err", zap.Error(err), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

func (d *Diffuse) processAction(a reactor.DiffuseAction) {
	switch a.Type {
	case reactor.DiffuseActionInbound: // 处理收件箱
		d.processInbound(a)
	case reactor.DiffuseActionOutboundForward: // 处理发件箱
		d.processOutbound(a)
	}
}

func (d *Diffuse) processInbound(a reactor.DiffuseAction) {
	if len(a.Messages) == 0 {
		return
	}
	// 从收件箱中取出消息
	for _, msg := range a.Messages {
		switch msg.MsgType {
		case reactor.ChannelMsgDiffuse:
			d.processDiffuse(msg)
		}
	}

}

func (d *Diffuse) processOutbound(a reactor.DiffuseAction) {
	if len(a.Messages) == 0 {
		d.Warn("diffuse: processOutbound: messages is empty", zap.String("actionType", a.Type.String()))
		return
	}
	req := &outboundReq{
		fromNode: options.G.Cluster.NodeId,
		messages: a.Messages,
	}
	data, err := req.encode()
	if err != nil {
		d.Error("diffuse: processOutbound: encode failed", zap.Error(err))
		return
	}
	err = d.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgOutboundReq),
		Content: data,
	})
	if err != nil {
		d.Error("diffuse: processOutbound: send failed", zap.Error(err))
	}

}

func (d *Diffuse) sendToNode(toNodeId uint64, msg *proto.Message) error {

	err := service.Cluster.Send(toNodeId, msg)
	return err
}
