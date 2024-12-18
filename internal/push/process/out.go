package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (p *Push) Send(actions []reactor.PushAction) {
	var err error
	for _, a := range actions {
		err = p.processPool.Submit(func() {
			p.processAction(a)
		})
		if err != nil {
			p.Error("push submit err", zap.Error(err), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

func (p *Push) processAction(a reactor.PushAction) {
	switch a.Type {
	case reactor.PushActionInbound: // 处理收件箱
		p.processInbound(a)
	case reactor.PushActionOutboundForward: // 处理发件箱
		p.processOutbound(a)
	}
}

func (p *Push) processInbound(a reactor.PushAction) {
	if len(a.Messages) == 0 {
		return
	}
	// 从收件箱中取出消息
	var pushMessages []*reactor.ChannelMessage
	var offlineMessages []*reactor.ChannelMessage
	for _, msg := range a.Messages {
		switch msg.MsgType {
		case reactor.ChannelMsgPush: // 在线消息
			if pushMessages == nil {
				pushMessages = make([]*reactor.ChannelMessage, 0, 10)
			}
			pushMessages = append(pushMessages, msg)
		case reactor.ChannelMsgOffline: // 离线消息
			if offlineMessages == nil {
				offlineMessages = make([]*reactor.ChannelMessage, 0, 50)
			}
			offlineMessages = append(offlineMessages, msg)
		}
	}
	if len(pushMessages) > 0 {
		p.processPush(pushMessages)
	}
	if len(offlineMessages) > 0 {
		p.processOffline(offlineMessages)
	}

}

func (p *Push) processOutbound(a reactor.PushAction) {
	if len(a.Messages) == 0 {
		p.Warn("push: processOutbound: messages is empty", zap.String("actionType", a.Type.String()))
		return
	}
	req := &outboundReq{
		fromNode: options.G.Cluster.NodeId,
		messages: a.Messages,
	}
	data, err := req.encode()
	if err != nil {
		p.Error("push: processOutbound: encode failed", zap.Error(err))
		return
	}
	err = p.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgOutboundReq),
		Content: data,
	})
	if err != nil {
		p.Error("push: processOutbound: send failed", zap.Error(err))
	}

}

func (d *Push) sendToNode(toNodeId uint64, msg *proto.Message) error {

	err := service.Cluster.Send(toNodeId, msg)
	return err
}
