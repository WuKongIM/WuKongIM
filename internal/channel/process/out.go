package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (c *Channel) Send(actions []reactor.ChannelAction) {

	var err error
	for _, a := range actions {
		err = c.processPool.Submit(func() {
			c.processAction(a)
		})
		if err != nil {
			c.Error("submit err", zap.Error(err), zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

// 频道行为逻辑处理
func (c *Channel) processAction(a reactor.ChannelAction) {
	switch a.Type {
	case reactor.ChannelActionElection: // 选举
		c.processElection(a)
	case reactor.ChannelActionJoin: // 处理加入
		c.processJoin(a)
	case reactor.ChannelActionJoinResp: // 加入返回
		c.processJoinResp(a)
	case reactor.ChannelActionHeartbeatReq: // 心跳
		c.processHeartbeatReq(a)
	case reactor.ChannelActionHeartbeatResp: // 心跳回执
		c.processHeartbeatResp(a)
	case reactor.ChannelActionInbound: // 处理收件箱
		c.processInbound(a)
	case reactor.ChannelActionOutboundForward: // 处理发件箱
		c.processOutbound(a)
	case reactor.ChannelActionClose: // 存储消息
		c.Info("channel close", zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType))
	}
}

func (c *Channel) processElection(a reactor.ChannelAction) {
	channelKey := wkutil.ChannelToKey(a.FakeChannelId, a.ChannelType)
	if c.isElectioning(channelKey) {
		return
	}
	defer c.unsetElectioning(channelKey)

	c.setElectioning(channelKey)

	timeoutCtx, cancel := c.WithTimeout()
	defer cancel()
	fmt.Println("LoadOrCreateChannel====>", a.FakeChannelId, a.ChannelType)
	cfg, err := service.Cluster.LoadOrCreateChannel(timeoutCtx, a.FakeChannelId, a.ChannelType)
	if err != nil {
		c.Error("load or create channel failed", zap.Error(err), zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType))
		return
	}
	if cfg.LeaderId == 0 {
		c.Error("election leader failed", zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType))
		return
	}
	reactor.Channel.UpdateConfig(a.FakeChannelId, a.ChannelType, reactor.ChannelConfig{
		LeaderId: cfg.LeaderId,
	})
}

func (c *Channel) processJoin(a reactor.ChannelAction) {
	req := &channelJoinReq{
		channelId:   a.FakeChannelId,
		channelType: a.ChannelType,
		from:        options.G.Cluster.NodeId,
	}
	data, err := req.encode()
	if err != nil {
		c.Error("channel: processJoin: encode failed", zap.Error(err))
		return
	}
	// fmt.Println("processJoin--->", a.To, req)
	err = c.sendToNode(a.To, &proto.Message{
		MsgType: msgChannelJoinReq.uint32(),
		Content: data,
	})
	if err != nil {
		c.Error("channel: processJoin: send failed", zap.Error(err))
	}
}

func (c *Channel) processJoinResp(a reactor.ChannelAction) {
	resp := &channelJoinResp{
		channelId:   a.FakeChannelId,
		channelType: a.ChannelType,
		from:        options.G.Cluster.NodeId,
	}
	data, err := resp.encode()
	if err != nil {
		c.Error("channel: processJoinResp: encode failed", zap.Error(err))
		return
	}
	err = c.sendToNode(a.To, &proto.Message{
		MsgType: msgChannelJoinResp.uint32(),
		Content: data,
	})
	if err != nil {
		c.Error("channel: processJoinResp: send failed", zap.Error(err))
	}
}

func (c *Channel) processHeartbeatReq(a reactor.ChannelAction) {
	req := &nodeHeartbeatReq{
		channelId:   a.FakeChannelId,
		channelType: a.ChannelType,
		fromNode:    options.G.Cluster.NodeId,
	}
	data, err := req.encode()
	if err != nil {
		c.Error("channel: processHeartbeatReq: encode failed", zap.Error(err))
		return
	}
	err = c.sendToNode(a.To, &proto.Message{
		MsgType: msgNodeHeartbeatReq.uint32(),
		Content: data,
	})
	if err != nil {
		c.Error("channel: processHeartbeatReq: send failed", zap.Error(err))
	}
}

func (c *Channel) processHeartbeatResp(a reactor.ChannelAction) {
	resp := &nodeHeartbeatResp{
		channelId:   a.FakeChannelId,
		channelType: a.ChannelType,
		fromNode:    options.G.Cluster.NodeId,
	}
	data, err := resp.encode()
	if err != nil {
		c.Error("channel: processHeartbeatResp: encode failed", zap.Error(err))
		return
	}
	err = c.sendToNode(a.To, &proto.Message{
		MsgType: msgNodeHeartbeatResp.uint32(),
		Content: data,
	})
	if err != nil {
		c.Error("channel: processHeartbeatResp: send failed", zap.Error(err))
	}
}

func (c *Channel) processInbound(a reactor.ChannelAction) {
	if len(a.Messages) == 0 {
		return
	}
	var (
		storageMessages            []*reactor.ChannelMessage // 存储的消息
		storageNotifyQueueMessages []*reactor.ChannelMessage // 存储通知队列的消息
		sendackMessages            []*reactor.ChannelMessage // 发送回执的消息
	)
	for _, m := range a.Messages {
		switch m.MsgType {
		// 消息发送
		case reactor.ChannelMsgSend:
			c.processSend(a.Role, m)
			// 权限验证
		case reactor.ChannelMsgPermission:
			c.processPermission(a.ChannelInfo, m)
			// 消息存储
		case reactor.ChannelMsgStorage:
			if storageMessages == nil {
				storageMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			storageMessages = append(storageMessages, m)
		case reactor.ChannelMsgStorageNotifyQueue:
			if storageNotifyQueueMessages == nil {
				storageNotifyQueueMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			storageNotifyQueueMessages = append(storageNotifyQueueMessages, m)
			// 消息发送回执
		case reactor.ChannelMsgSendack:
			if sendackMessages == nil {
				sendackMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			sendackMessages = append(sendackMessages, m)
		}
	}
	if len(storageMessages) > 0 {
		c.processStorage(a.FakeChannelId, a.ChannelType, storageMessages)
	}
	if len(storageNotifyQueueMessages) > 0 {
		c.processStorageNotifyQueue(a.FakeChannelId, a.ChannelType, storageNotifyQueueMessages)
	}
	if len(sendackMessages) > 0 {
		c.processSendack(a.FakeChannelId, a.ChannelType, sendackMessages)
	}
}

func (c *Channel) processOutbound(a reactor.ChannelAction) {
	if len(a.Messages) == 0 {
		c.Warn("channel: processOutbound: messages is empty", zap.String("actionType", a.Type.String()))
		return
	}
	req := &outboundReq{
		fromNode:    options.G.Cluster.NodeId,
		channelId:   a.FakeChannelId,
		channelType: a.ChannelType,
		messages:    a.Messages,
	}
	data, err := req.encode()
	if err != nil {
		c.Error("channel: processOutbound: encode failed", zap.Error(err))
		return
	}
	err = c.sendToNode(a.To, &proto.Message{
		MsgType: uint32(msgOutboundReq),
		Content: data,
	})
	if err != nil {
		c.Error("channel: processOutbound: send failed", zap.Error(err))
	}

}

func (c *Channel) sendToNode(toNodeId uint64, msg *proto.Message) error {

	err := service.Cluster.Send(toNodeId, msg)
	return err
}
